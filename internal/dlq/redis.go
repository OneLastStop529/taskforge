package dlq

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	taskredis "github.com/OneLastStop529/taskforge/internal/redis"
	"github.com/OneLastStop529/taskforge/internal/task"
	redis "github.com/redis/go-redis/v9"
)

// RedisConfig holds the connection settings for a Redis-backed DLQ backend.
type RedisConfig struct {
	Connection     taskredis.Config
	EntryKeyPrefix string
	IndexSortedSet string
}

// RedisClient is the narrow client surface RedisBackend needs.
type RedisClient interface {
	Set(ctx context.Context, key string, value []byte) error
	Get(ctx context.Context, key string) ([]byte, error)
	Del(ctx context.Context, keys ...string) error
	ZAdd(ctx context.Context, key string, score float64, member string) error
	ZRevRange(ctx context.Context, key string, start, stop int64) ([]string, error)
	ZRem(ctx context.Context, key string, members ...string) error
	Close() error
}

// RedisBackend stores DLQ entries in Redis-compatible key/value storage.
type RedisBackend struct {
	redisClient    RedisClient
	entryKeyPrefix string
	indexSortedSet string
}

type goRedisClient struct {
	client *redis.Client
}

func (c *goRedisClient) Set(ctx context.Context, key string, value []byte) error {
	return c.client.Set(ctx, key, value, 0).Err()
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

func (c *goRedisClient) Del(ctx context.Context, keys ...string) error {
	return c.client.Del(ctx, keys...).Err()
}

func (c *goRedisClient) ZAdd(ctx context.Context, key string, score float64, member string) error {
	return c.client.ZAdd(ctx, key, redis.Z{Score: score, Member: member}).Err()
}

func (c *goRedisClient) ZRevRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return c.client.ZRevRange(ctx, key, start, stop).Result()
}

func (c *goRedisClient) ZRem(ctx context.Context, key string, members ...string) error {
	values := make([]any, 0, len(members))
	for _, member := range members {
		values = append(values, member)
	}
	return c.client.ZRem(ctx, key, values...).Err()
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
	entryKeyPrefix := cfg.EntryKeyPrefix
	if entryKeyPrefix == "" {
		entryKeyPrefix = "taskforge:dlq:entry:"
	}
	indexSortedSet := cfg.IndexSortedSet
	if indexSortedSet == "" {
		indexSortedSet = "taskforge:dlq:index"
	}
	return &RedisBackend{
		redisClient:    client,
		entryKeyPrefix: entryKeyPrefix,
		indexSortedSet: indexSortedSet,
	}
}

// PutEntry stores or overwrites a DLQ entry.
func (b *RedisBackend) PutEntry(ctx context.Context, entry *task.DLQEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("taskforge: marshal redis dlq entry %q: %w", entry.ID, err)
	}
	if err := b.redisClient.Set(ctx, b.entryKey(entry.ID), data); err != nil {
		return fmt.Errorf("taskforge: set redis dlq entry %q: %w", entry.ID, err)
	}
	failedAt := entry.FailedAt
	if failedAt.IsZero() {
		failedAt = time.Now()
	}
	if err := b.redisClient.ZAdd(ctx, b.indexSortedSet, float64(failedAt.UnixMilli()), entry.ID); err != nil {
		return fmt.Errorf("taskforge: index redis dlq entry %q: %w", entry.ID, err)
	}
	return nil
}

// GetEntry retrieves a DLQ entry by task ID.
func (b *RedisBackend) GetEntry(ctx context.Context, id string) (*task.DLQEntry, error) {
	data, err := b.redisClient.Get(ctx, b.entryKey(id))
	if err != nil {
		return nil, fmt.Errorf("taskforge: get redis dlq entry %q: %w", id, err)
	}
	if data == nil {
		return nil, fmt.Errorf("taskforge: dlq entry not found for task %q", id)
	}

	var entry task.DLQEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("taskforge: unmarshal redis dlq entry %q: %w", id, err)
	}
	return &entry, nil
}

// ListEntries returns DLQ task IDs ordered from newest to oldest.
func (b *RedisBackend) ListEntries(ctx context.Context, offset, limit int) ([]string, error) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 100
	}
	ids, err := b.redisClient.ZRevRange(ctx, b.indexSortedSet, int64(offset), int64(offset+limit-1))
	if err != nil {
		return nil, fmt.Errorf("taskforge: list redis dlq entries: %w", err)
	}
	return ids, nil
}

// DeleteEntry removes a DLQ entry by task ID.
func (b *RedisBackend) DeleteEntry(ctx context.Context, id string) error {
	if err := b.redisClient.Del(ctx, b.entryKey(id)); err != nil {
		return fmt.Errorf("taskforge: delete redis dlq entry %q: %w", id, err)
	}
	if err := b.redisClient.ZRem(ctx, b.indexSortedSet, id); err != nil {
		return fmt.Errorf("taskforge: deindex redis dlq entry %q: %w", id, err)
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

func (b *RedisBackend) entryKey(id string) string {
	return b.entryKeyPrefix + id
}
