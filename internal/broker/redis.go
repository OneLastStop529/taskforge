package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	taskredis "github.com/OneLastStop529/taskforge/internal/redis"
	"github.com/OneLastStop529/taskforge/internal/task"
	redis "github.com/redis/go-redis/v9"
)

// RedisConfig holds the connection settings for a Redis-backed broker.
type RedisConfig struct {
	Connection            taskredis.Config
	ReadyQueuePrefix      string
	DelayedQueuePrefix    string
	ProcessingQueuePrefix string
	LeasePrefix           string
	LeaseTimeout          time.Duration
}

// RedisClient is the narrow client surface RedisBroker needs.
type RedisClient interface {
	EnqueueReady(ctx context.Context, key string, value []byte) error
	ReserveReady(ctx context.Context, readyKey, processingKey, leaseKey string, leaseUntil time.Time) ([]byte, error)
	AckReservation(ctx context.Context, processingKey, leaseKey string, value []byte) error
	RecoverExpired(ctx context.Context, readyKey, processingKey, leaseKey string, now time.Time, count int64) error
	EnqueueScheduled(ctx context.Context, key string, value []byte, runAt time.Time) error
	PromoteScheduledReady(ctx context.Context, delayedKey, readyKey string, now time.Time, count int64) error
	Close() error
}

// RedisBroker stores ready tasks in Redis lists and delayed tasks in sorted sets.
type RedisBroker struct {
	redisClient        RedisClient
	readyQueuePrefix   string
	delayedQueuePrefix string
	processingPrefix   string
	leasePrefix        string
	leaseTimeout       time.Duration
	pollInterval       time.Duration
}

type goRedisClient struct {
	client *redis.Client
}

func (c *goRedisClient) EnqueueReady(ctx context.Context, key string, value []byte) error {
	return c.client.RPush(ctx, key, string(value)).Err()
}

var reserveReadyScript = redis.NewScript(`
local value = redis.call("LPOP", KEYS[1])
if not value then
  return false
end
redis.call("RPUSH", KEYS[2], value)
redis.call("ZADD", KEYS[3], ARGV[1], value)
return value
`)

func (c *goRedisClient) ReserveReady(ctx context.Context, readyKey, processingKey, leaseKey string, leaseUntil time.Time) ([]byte, error) {
	value, err := reserveReadyScript.Run(ctx, c.client, []string{readyKey, processingKey, leaseKey}, leaseUntil.UnixMilli()).Result()
	if err == redis.Nil || value == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	switch v := value.(type) {
	case string:
		return []byte(v), nil
	case []byte:
		return v, nil
	default:
		return nil, fmt.Errorf("taskforge: unexpected redis reserved value type %T", value)
	}
}

var ackReservationScript = redis.NewScript(`
redis.call("LREM", KEYS[1], 1, ARGV[1])
redis.call("ZREM", KEYS[2], ARGV[1])
return 1
`)

func (c *goRedisClient) AckReservation(ctx context.Context, processingKey, leaseKey string, value []byte) error {
	return ackReservationScript.Run(ctx, c.client, []string{processingKey, leaseKey}, string(value)).Err()
}

func (c *goRedisClient) EnqueueScheduled(ctx context.Context, key string, value []byte, runAt time.Time) error {
	return c.client.ZAdd(ctx, key, redis.Z{
		Score:  float64(runAt.UnixMilli()),
		Member: string(value),
	}).Err()
}

var recoverExpiredScript = redis.NewScript(`
local expired = redis.call("ZRANGEBYSCORE", KEYS[3], "-inf", ARGV[1], "LIMIT", 0, ARGV[2])
for _, value in ipairs(expired) do
  redis.call("LREM", KEYS[2], 1, value)
  redis.call("RPUSH", KEYS[1], value)
  redis.call("ZREM", KEYS[3], value)
end
return #expired
`)

func (c *goRedisClient) RecoverExpired(ctx context.Context, readyKey, processingKey, leaseKey string, now time.Time, count int64) error {
	return recoverExpiredScript.Run(ctx, c.client, []string{readyKey, processingKey, leaseKey}, now.UnixMilli(), count).Err()
}

var promoteScheduledScript = redis.NewScript(`
local values = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", ARGV[1], "LIMIT", 0, ARGV[2])
for _, value in ipairs(values) do
  redis.call("ZREM", KEYS[1], value)
  redis.call("RPUSH", KEYS[2], value)
end
return #values
`)

func (c *goRedisClient) PromoteScheduledReady(ctx context.Context, delayedKey, readyKey string, now time.Time, count int64) error {
	return promoteScheduledScript.Run(ctx, c.client, []string{delayedKey, readyKey}, now.UnixMilli(), count).Err()
}

func (c *goRedisClient) Close() error {
	return c.client.Close()
}

// NewRedisBroker creates a RedisBroker from connection config.
func NewRedisBroker(cfg RedisConfig) (*RedisBroker, error) {
	client, err := taskredis.NewClient(cfg.Connection)
	if err != nil {
		return nil, err
	}
	return NewRedisBrokerWithClient(&goRedisClient{client: client}, cfg), nil
}

// NewRedisBrokerWithClient creates a RedisBroker from an injected client.
func NewRedisBrokerWithClient(client RedisClient, cfg RedisConfig) *RedisBroker {
	readyPrefix := cfg.ReadyQueuePrefix
	if readyPrefix == "" {
		readyPrefix = "taskforge:queue:"
	}
	delayedPrefix := cfg.DelayedQueuePrefix
	if delayedPrefix == "" {
		delayedPrefix = "taskforge:queue:delayed:"
	}
	processingPrefix := cfg.ProcessingQueuePrefix
	if processingPrefix == "" {
		processingPrefix = "taskforge:queue:processing:"
	}
	leasePrefix := cfg.LeasePrefix
	if leasePrefix == "" {
		leasePrefix = "taskforge:queue:lease:"
	}
	leaseTimeout := cfg.LeaseTimeout
	if leaseTimeout <= 0 {
		leaseTimeout = 30 * time.Second
	}
	return &RedisBroker{
		redisClient:        client,
		readyQueuePrefix:   readyPrefix,
		delayedQueuePrefix: delayedPrefix,
		processingPrefix:   processingPrefix,
		leasePrefix:        leasePrefix,
		leaseTimeout:       leaseTimeout,
		pollInterval:       10 * time.Millisecond,
	}
}

// Enqueue places the message onto the appropriate Redis-backed queue.
func (b *RedisBroker) Enqueue(ctx context.Context, msg *task.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("taskforge: marshal redis message %q: %w", msg.ID, err)
	}
	if delay := time.Until(msg.ScheduledAt); delay > 0 {
		if err := b.redisClient.EnqueueScheduled(ctx, b.delayedQueueKey(msg.Queue), data, msg.ScheduledAt); err != nil {
			return fmt.Errorf("taskforge: enqueue scheduled redis message %q: %w", msg.ID, err)
		}
		return nil
	}
	if err := b.redisClient.EnqueueReady(ctx, b.readyQueueKey(msg.Queue), data); err != nil {
		return fmt.Errorf("taskforge: enqueue redis message %q: %w", msg.ID, err)
	}
	return nil
}

// Dequeue blocks until a message is available on one of the queues.
func (b *RedisBroker) Dequeue(ctx context.Context, queues []string) (*task.Message, error) {
	for {
		for _, queue := range queues {
			if err := b.promoteScheduled(ctx, queue); err != nil {
				return nil, err
			}
			if err := b.redisClient.RecoverExpired(ctx, b.readyQueueKey(queue), b.processingQueueKey(queue), b.leaseKey(queue), time.Now(), 32); err != nil {
				return nil, fmt.Errorf("taskforge: recover expired redis queue %q: %w", queue, err)
			}
			data, err := b.redisClient.ReserveReady(ctx, b.readyQueueKey(queue), b.processingQueueKey(queue), b.leaseKey(queue), time.Now().Add(b.leaseTimeout))
			if err != nil {
				return nil, fmt.Errorf("taskforge: reserve redis queue %q: %w", queue, err)
			}
			if data == nil {
				continue
			}
			var msg task.Message
			if err := json.Unmarshal(data, &msg); err != nil {
				return nil, fmt.Errorf("taskforge: unmarshal redis message from queue %q: %w", queue, err)
			}
			return &msg, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(b.pollInterval):
		}
	}
}

// Ack acknowledges successful processing of a message.
func (b *RedisBroker) Ack(ctx context.Context, msg *task.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("taskforge: marshal ack redis message %q: %w", msg.ID, err)
	}
	if err := b.redisClient.AckReservation(ctx, b.processingQueueKey(msg.Queue), b.leaseKey(msg.Queue), data); err != nil {
		return fmt.Errorf("taskforge: ack redis message %q: %w", msg.ID, err)
	}
	return nil
}

// Nack re-enqueues the message so it can be retried.
func (b *RedisBroker) Nack(ctx context.Context, msg *task.Message) error {
	return b.Enqueue(ctx, msg)
}

// Close releases any resources held by the broker.
func (b *RedisBroker) Close() error {
	if b.redisClient == nil {
		return nil
	}
	return b.redisClient.Close()
}

func (b *RedisBroker) promoteScheduled(ctx context.Context, queue string) error {
	if err := b.redisClient.PromoteScheduledReady(ctx, b.delayedQueueKey(queue), b.readyQueueKey(queue), time.Now(), 32); err != nil {
		return fmt.Errorf("taskforge: promote scheduled redis queue %q: %w", queue, err)
	}
	return nil
}

func (b *RedisBroker) readyQueueKey(queue string) string {
	return b.readyQueuePrefix + queue
}

func (b *RedisBroker) delayedQueueKey(queue string) string {
	return b.delayedQueuePrefix + queue
}

func (b *RedisBroker) processingQueueKey(queue string) string {
	return b.processingPrefix + queue
}

func (b *RedisBroker) leaseKey(queue string) string {
	return b.leasePrefix + queue
}
