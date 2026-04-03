package idempotency_test

import (
	"context"
	"errors"
	"testing"

	"github.com/OneLastStop529/taskforge/internal/idempotency"
	taskredis "github.com/OneLastStop529/taskforge/internal/redis"
)

type fakeRedisClient struct {
	values map[string][]byte
	closed bool
}

func newFakeRedisClient() *fakeRedisClient {
	return &fakeRedisClient{
		values: make(map[string][]byte),
	}
}

func (c *fakeRedisClient) SetNX(_ context.Context, key string, value []byte) (bool, error) {
	if _, ok := c.values[key]; ok {
		return false, nil
	}
	c.values[key] = append([]byte(nil), value...)
	return true, nil
}

func (c *fakeRedisClient) Get(_ context.Context, key string) ([]byte, error) {
	value, ok := c.values[key]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), value...), nil
}

func (c *fakeRedisClient) DeleteIfValueMatches(_ context.Context, key string, value []byte) error {
	current, ok := c.values[key]
	if !ok {
		return nil
	}
	if string(current) == string(value) {
		delete(c.values, key)
	}
	return nil
}

func (c *fakeRedisClient) Close() error {
	c.closed = true
	return nil
}

func TestRedisBackend_ClaimOrGet(t *testing.T) {
	client := newFakeRedisClient()
	be := idempotency.NewRedisBackendWithClient(client, idempotency.RedisConfig{
		KeyPrefix: "taskforge:test:idempotency:",
	})
	defer be.Close() //nolint:errcheck

	ctx := context.Background()
	record, claimed, err := be.ClaimOrGet(ctx, "invoice:123", "task-1")
	if err != nil {
		t.Fatalf("ClaimOrGet first: %v", err)
	}
	if !claimed {
		t.Fatal("expected first claim to succeed")
	}
	if record.TaskID != "task-1" {
		t.Fatalf("got task id %q, want task-1", record.TaskID)
	}

	record, claimed, err = be.ClaimOrGet(ctx, "invoice:123", "task-2")
	if err != nil {
		t.Fatalf("ClaimOrGet second: %v", err)
	}
	if claimed {
		t.Fatal("expected duplicate claim to reuse existing record")
	}
	if record.TaskID != "task-1" {
		t.Fatalf("got canonical task id %q, want task-1", record.TaskID)
	}
}

func TestRedisBackend_GetNotFound(t *testing.T) {
	be := idempotency.NewRedisBackendWithClient(newFakeRedisClient(), idempotency.RedisConfig{})
	defer be.Close() //nolint:errcheck

	_, err := be.Get(context.Background(), "missing")
	if !errors.Is(err, idempotency.ErrNotFound) {
		t.Fatalf("got err %v, want ErrNotFound", err)
	}
}

func TestRedisBackend_ReleaseIfOwner(t *testing.T) {
	client := newFakeRedisClient()
	be := idempotency.NewRedisBackendWithClient(client, idempotency.RedisConfig{})
	defer be.Close() //nolint:errcheck

	ctx := context.Background()
	if _, _, err := be.ClaimOrGet(ctx, "invoice:123", "task-1"); err != nil {
		t.Fatalf("ClaimOrGet: %v", err)
	}

	if err := be.ReleaseIfOwner(ctx, "invoice:123", "task-2"); err != nil {
		t.Fatalf("ReleaseIfOwner non-owner: %v", err)
	}
	record, err := be.Get(ctx, "invoice:123")
	if err != nil {
		t.Fatalf("Get after non-owner release: %v", err)
	}
	if record.TaskID != "task-1" {
		t.Fatalf("got task id %q, want task-1", record.TaskID)
	}

	if err := be.ReleaseIfOwner(ctx, "invoice:123", "task-1"); err != nil {
		t.Fatalf("ReleaseIfOwner owner: %v", err)
	}
	_, err = be.Get(ctx, "invoice:123")
	if !errors.Is(err, idempotency.ErrNotFound) {
		t.Fatalf("got err %v, want ErrNotFound", err)
	}
}

func TestRedisBackend_Close(t *testing.T) {
	client := newFakeRedisClient()
	be := idempotency.NewRedisBackendWithClient(client, idempotency.RedisConfig{})

	if err := be.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !client.closed {
		t.Fatal("expected redis client to be closed")
	}
}

func TestNewRedisBackend_RequiresAddr(t *testing.T) {
	_, err := idempotency.NewRedisBackend(idempotency.RedisConfig{})
	if err == nil {
		t.Fatal("expected constructor error")
	}
}

func TestNewRedisBackend_ConstructsClient(t *testing.T) {
	be, err := idempotency.NewRedisBackend(idempotency.RedisConfig{
		Connection: taskredis.Config{Addr: "127.0.0.1:6379"},
	})
	if err != nil {
		t.Fatalf("NewRedisBackend: %v", err)
	}
	defer be.Close() //nolint:errcheck
}
