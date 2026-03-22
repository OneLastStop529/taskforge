package result_test

import (
	"context"
	"testing"
	"time"

	"github.com/OneLastStop529/taskforge/internal/result"
	"github.com/OneLastStop529/taskforge/internal/task"
)

type fakeRedisClient struct {
	values map[string][]byte
	ttls   map[string]time.Duration
	closed bool
}

func newFakeRedisClient() *fakeRedisClient {
	return &fakeRedisClient{
		values: make(map[string][]byte),
		ttls:   make(map[string]time.Duration),
	}
}

func (c *fakeRedisClient) Set(_ context.Context, key string, value []byte, ttl time.Duration) error {
	c.values[key] = append([]byte(nil), value...)
	c.ttls[key] = ttl
	return nil
}

func (c *fakeRedisClient) Get(_ context.Context, key string) ([]byte, error) {
	value, ok := c.values[key]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), value...), nil
}

func (c *fakeRedisClient) Close() error {
	c.closed = true
	return nil
}

func TestRedisBackend_SetAndGet(t *testing.T) {
	client := newFakeRedisClient()
	be := result.NewRedisBackendWithClient(client, time.Minute, "taskforge:test:")
	defer be.Close() //nolint:errcheck

	ctx := context.Background()
	r := &task.Result{ID: "1", Name: "test", State: task.StatePending}
	if err := be.SetResult(ctx, r); err != nil {
		t.Fatalf("SetResult: %v", err)
	}

	got, err := be.GetResult(ctx, "1")
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if got.State != task.StatePending {
		t.Errorf("got state %q, want PENDING", got.State)
	}
	if client.ttls["taskforge:test:1"] != time.Minute {
		t.Errorf("got ttl %s, want %s", client.ttls["taskforge:test:1"], time.Minute)
	}
}

func TestRedisBackend_NotFound(t *testing.T) {
	be := result.NewRedisBackendWithClient(newFakeRedisClient(), 0, "")
	defer be.Close() //nolint:errcheck

	_, err := be.GetResult(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error for missing result")
	}
}

func TestRedisBackend_Overwrite(t *testing.T) {
	be := result.NewRedisBackendWithClient(newFakeRedisClient(), 0, "")
	defer be.Close() //nolint:errcheck

	ctx := context.Background()
	_ = be.SetResult(ctx, &task.Result{ID: "ow", Name: "t", State: task.StatePending})
	_ = be.SetResult(ctx, &task.Result{ID: "ow", Name: "t", State: task.StateSuccess})

	got, err := be.GetResult(ctx, "ow")
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if got.State != task.StateSuccess {
		t.Errorf("expected SUCCESS after overwrite, got %q", got.State)
	}
}

func TestRedisBackend_Close(t *testing.T) {
	client := newFakeRedisClient()
	be := result.NewRedisBackendWithClient(client, 0, "")

	if err := be.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !client.closed {
		t.Fatal("expected redis client to be closed")
	}
}

func TestNewRedisBackend_RequiresAddr(t *testing.T) {
	_, err := result.NewRedisBackend(result.RedisConfig{}, time.Minute)
	if err == nil {
		t.Fatal("expected constructor error")
	}
}

func TestNewRedisBackend_ConstructsClient(t *testing.T) {
	be, err := result.NewRedisBackend(result.RedisConfig{Addr: "127.0.0.1:6379"}, time.Minute)
	if err != nil {
		t.Fatalf("NewRedisBackend: %v", err)
	}
	defer be.Close() //nolint:errcheck
}
