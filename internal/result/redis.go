package result

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	taskredis "github.com/OneLastStop529/taskforge/internal/redis"
	"github.com/OneLastStop529/taskforge/internal/task"
	redis "github.com/redis/go-redis/v9"
)

// RedisConfig holds the connection settings for a Redis-backed result store.
type RedisConfig struct {
	Connection taskredis.Config
	KeyPrefix  string
}

// RedisClient is the narrow client surface RedisBackend needs.
type RedisClient interface {
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Get(ctx context.Context, key string) ([]byte, error)
	ScanKeys(ctx context.Context, pattern string, count int64) ([]string, error)
	Close() error
}

// RedisBackend stores task results in Redis-compatible key/value storage.
type RedisBackend struct {
	redisClient RedisClient
	ttl         time.Duration
	keyPrefix   string
}

type goRedisClient struct {
	client *redis.Client
}

func (c *goRedisClient) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

func (c *goRedisClient) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := c.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return value, nil
}

func (c *goRedisClient) ScanKeys(ctx context.Context, pattern string, count int64) ([]string, error) {
	var (
		cursor uint64
		keys   []string
	)
	for {
		batch, next, err := c.client.Scan(ctx, cursor, pattern, count).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			return keys, nil
		}
	}
}

func (c *goRedisClient) Close() error {
	return c.client.Close()
}

// NewRedisBackend creates a RedisBackend from connection config.
func NewRedisBackend(cfg RedisConfig, ttl time.Duration) (*RedisBackend, error) {
	client, err := taskredis.NewClient(cfg.Connection)
	if err != nil {
		return nil, err
	}
	return NewRedisBackendWithClient(&goRedisClient{client: client}, ttl, cfg.KeyPrefix), nil
}

// NewRedisBackendWithClient creates a RedisBackend from an injected client.
// This keeps the backend testable before the concrete Redis client lands.
func NewRedisBackendWithClient(client RedisClient, ttl time.Duration, keyPrefix string) *RedisBackend {
	if keyPrefix == "" {
		keyPrefix = "taskforge:result:"
	}
	return &RedisBackend{
		redisClient: client,
		ttl:         ttl,
		keyPrefix:   keyPrefix,
	}
}

// SetResult stores or overwrites the result for a task.
func (b *RedisBackend) SetResult(ctx context.Context, r *task.Result) error {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("taskforge: marshal redis result: %w", err)
	}
	if err := b.redisClient.Set(ctx, b.key(r.ID), data, b.ttl); err != nil {
		return fmt.Errorf("taskforge: set redis result %q: %w", r.ID, err)
	}
	return nil
}

// GetResult retrieves a stored result by task ID.
func (b *RedisBackend) GetResult(ctx context.Context, id string) (*task.Result, error) {
	data, err := b.redisClient.Get(ctx, b.key(id))
	if err != nil {
		return nil, fmt.Errorf("taskforge: get redis result %q: %w", id, err)
	}
	if data == nil {
		return nil, fmt.Errorf("taskforge: result not found for task %q", id)
	}

	var r task.Result
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("taskforge: unmarshal redis result %q: %w", id, err)
	}
	return &r, nil
}

// ResolveResultID resolves a full task ID from an exact or unique prefix.
func (b *RedisBackend) ResolveResultID(ctx context.Context, idOrPrefix string) (string, error) {
	data, err := b.redisClient.Get(ctx, b.key(idOrPrefix))
	if err != nil {
		return "", fmt.Errorf("taskforge: get redis result %q: %w", idOrPrefix, err)
	}
	if data != nil {
		return idOrPrefix, nil
	}

	keys, err := b.redisClient.ScanKeys(ctx, b.key(idOrPrefix)+"*", 100)
	if err != nil {
		return "", fmt.Errorf("taskforge: scan redis result ids for prefix %q: %w", idOrPrefix, err)
	}

	var match string
	for _, key := range keys {
		id := strings.TrimPrefix(key, b.keyPrefix)
		if !strings.HasPrefix(id, idOrPrefix) {
			continue
		}
		if match != "" {
			return "", fmt.Errorf("taskforge: result id prefix %q is ambiguous", idOrPrefix)
		}
		match = id
	}
	if match == "" {
		return "", fmt.Errorf("taskforge: result not found for task %q", idOrPrefix)
	}
	return match, nil
}

// Close releases any resources held by the backend.
func (b *RedisBackend) Close() error {
	if b.redisClient == nil {
		return nil
	}
	return b.redisClient.Close()
}

func (b *RedisBackend) key(id string) string {
	return b.keyPrefix + id
}
