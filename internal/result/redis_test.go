package result_test

import (
	"context"
	"strings"
	"testing"
	"time"

	taskredis "github.com/OneLastStop529/taskforge/internal/redis"
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

func (c *fakeRedisClient) ScanKeys(_ context.Context, pattern string, _ int64) ([]string, error) {
	prefix := strings.TrimSuffix(pattern, "*")
	keys := make([]string, 0)
	for key := range c.values {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys, nil
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

func TestRedisBackend_ResolveResultID(t *testing.T) {
	client := newFakeRedisClient()
	be := result.NewRedisBackendWithClient(client, 0, "taskforge:test:")
	defer be.Close() //nolint:errcheck

	ctx := context.Background()
	_ = be.SetResult(ctx, &task.Result{ID: "abc12345", Name: "t", State: task.StateSuccess})
	_ = be.SetResult(ctx, &task.Result{ID: "def67890", Name: "t", State: task.StateSuccess})

	id, err := be.ResolveResultID(ctx, "abc1")
	if err != nil {
		t.Fatalf("ResolveResultID unique prefix: %v", err)
	}
	if id != "abc12345" {
		t.Fatalf("got %q, want %q", id, "abc12345")
	}

	id, err = be.ResolveResultID(ctx, "abc12345")
	if err != nil {
		t.Fatalf("ResolveResultID exact: %v", err)
	}
	if id != "abc12345" {
		t.Fatalf("got %q, want %q", id, "abc12345")
	}
}

func TestRedisBackend_ResolveResultIDAmbiguous(t *testing.T) {
	client := newFakeRedisClient()
	be := result.NewRedisBackendWithClient(client, 0, "taskforge:test:")
	defer be.Close() //nolint:errcheck

	ctx := context.Background()
	_ = be.SetResult(ctx, &task.Result{ID: "abc12345", Name: "t", State: task.StateSuccess})
	_ = be.SetResult(ctx, &task.Result{ID: "abc67890", Name: "t", State: task.StateSuccess})

	if _, err := be.ResolveResultID(ctx, "abc"); err == nil {
		t.Fatal("expected ambiguity error")
	}
}

func TestNewRedisBackend_RequiresAddr(t *testing.T) {
	_, err := result.NewRedisBackend(result.RedisConfig{}, time.Minute)
	if err == nil {
		t.Fatal("expected constructor error")
	}
}

func TestNewRedisBackend_ConstructsClient(t *testing.T) {
	be, err := result.NewRedisBackend(result.RedisConfig{
		Connection: taskredis.Config{Addr: "127.0.0.1:6379"},
	}, time.Minute)
	if err != nil {
		t.Fatalf("NewRedisBackend: %v", err)
	}
	defer be.Close() //nolint:errcheck
}
