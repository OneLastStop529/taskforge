package idempotency

import (
	"context"
	"encoding/json"
	"fmt"

	taskredis "github.com/OneLastStop529/taskforge/internal/redis"
	redis "github.com/redis/go-redis/v9"
)

// RedisConfig holds the connection settings for a Redis-backed idempotency backend.
type RedisConfig struct {
	Connection taskredis.Config
	KeyPrefix  string
}

// RedisClient is the narrow client surface RedisBackend needs.
type RedisClient interface {
	SetNX(ctx context.Context, key string, value []byte) (bool, error)
	Get(ctx context.Context, key string) ([]byte, error)
	DeleteIfValueMatches(ctx context.Context, key string, value []byte) error
	Close() error
}

// RedisBackend stores idempotency claims in Redis-compatible key/value storage.
type RedisBackend struct {
	redisClient RedisClient
	keyPrefix   string
}

type goRedisClient struct {
	client *redis.Client
}

func (c *goRedisClient) SetNX(ctx context.Context, key string, value []byte) (bool, error) {
	return c.client.SetNX(ctx, key, value, 0).Result()
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

var deleteIfValueMatchesScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

func (c *goRedisClient) DeleteIfValueMatches(ctx context.Context, key string, value []byte) error {
	return deleteIfValueMatchesScript.Run(ctx, c.client, []string{key}, string(value)).Err()
}

func (c *goRedisClient) Close() error {
	return c.client.Close()
}

// NewRedisBackend creates a RedisBackend from connection config.
func NewRedisBackend(cfg RedisConfig) (*RedisBackend, error) {
	client, err := taskredis.NewClient(cfg.Connection)
	if err != nil {
		return nil, err
	}
	return NewRedisBackendWithClient(&goRedisClient{client: client}, cfg), nil
}

// NewRedisBackendWithClient creates a RedisBackend from an injected client.
func NewRedisBackendWithClient(client RedisClient, cfg RedisConfig) *RedisBackend {
	keyPrefix := cfg.KeyPrefix
	if keyPrefix == "" {
		keyPrefix = "taskforge:idempotency:"
	}
	return &RedisBackend{
		redisClient: client,
		keyPrefix:   keyPrefix,
	}
}

// ClaimOrGet atomically stores a new claim or returns the canonical record.
func (b *RedisBackend) ClaimOrGet(ctx context.Context, key, taskID string) (Record, bool, error) {
	record := Record{Key: key, TaskID: taskID}
	data, err := json.Marshal(record)
	if err != nil {
		return Record{}, false, fmt.Errorf("taskforge: marshal redis idempotency record %q: %w", key, err)
	}
	claimed, err := b.redisClient.SetNX(ctx, b.key(key), data)
	if err != nil {
		return Record{}, false, fmt.Errorf("taskforge: claim redis idempotency key %q: %w", key, err)
	}
	if claimed {
		return record, true, nil
	}
	existing, err := b.Get(ctx, key)
	if err != nil {
		return Record{}, false, err
	}
	return existing, false, nil
}

// Get returns the canonical record for a key.
func (b *RedisBackend) Get(ctx context.Context, key string) (Record, error) {
	data, err := b.redisClient.Get(ctx, b.key(key))
	if err != nil {
		return Record{}, fmt.Errorf("taskforge: get redis idempotency key %q: %w", key, err)
	}
	if data == nil {
		return Record{}, ErrNotFound
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("taskforge: unmarshal redis idempotency key %q: %w", key, err)
	}
	return record, nil
}

// ReleaseIfOwner removes a claim only when the caller owns it.
func (b *RedisBackend) ReleaseIfOwner(ctx context.Context, key, taskID string) error {
	record := Record{Key: key, TaskID: taskID}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("taskforge: marshal redis idempotency record %q: %w", key, err)
	}
	if err := b.redisClient.DeleteIfValueMatches(ctx, b.key(key), data); err != nil {
		return fmt.Errorf("taskforge: release redis idempotency key %q: %w", key, err)
	}
	return nil
}

// Close releases any resources held by the backend.
func (b *RedisBackend) Close() error {
	if b.redisClient == nil {
		return nil
	}
	return b.redisClient.Close()
}

func (b *RedisBackend) key(key string) string {
	return b.keyPrefix + key
}
