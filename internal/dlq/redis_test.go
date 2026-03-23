package dlq_test

import (
	"context"
	"testing"

	"github.com/OneLastStop529/taskforge/internal/dlq"
	taskredis "github.com/OneLastStop529/taskforge/internal/redis"
	"github.com/OneLastStop529/taskforge/internal/task"
)

type fakeRedisClient struct {
	values map[string][]byte
	zsets  map[string][]string
	closed bool
}

func newFakeRedisClient() *fakeRedisClient {
	return &fakeRedisClient{
		values: make(map[string][]byte),
		zsets:  make(map[string][]string),
	}
}

func (c *fakeRedisClient) Set(_ context.Context, key string, value []byte) error {
	c.values[key] = append([]byte(nil), value...)
	return nil
}

func (c *fakeRedisClient) Get(_ context.Context, key string) ([]byte, error) {
	value, ok := c.values[key]
	if !ok {
		return nil, nil
	}
	return append([]byte(nil), value...), nil
}

func (c *fakeRedisClient) ZAdd(_ context.Context, key string, _ float64, member string) error {
	entries := c.zsets[key]
	filtered := entries[:0]
	for _, existing := range entries {
		if existing != member {
			filtered = append(filtered, existing)
		}
	}
	filtered = append(filtered, member)
	c.zsets[key] = filtered
	return nil
}

func (c *fakeRedisClient) ZRevRange(_ context.Context, key string, start, stop int64) ([]string, error) {
	entries := c.zsets[key]
	reversed := make([]string, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		reversed = append(reversed, entries[i])
	}
	if start >= int64(len(reversed)) {
		return []string{}, nil
	}
	if stop >= int64(len(reversed)) {
		stop = int64(len(reversed) - 1)
	}
	return append([]string(nil), reversed[start:stop+1]...), nil
}

func (c *fakeRedisClient) Close() error {
	c.closed = true
	return nil
}

func TestRedisBackend_PutGetAndList(t *testing.T) {
	client := newFakeRedisClient()
	be := dlq.NewRedisBackendWithClient(client, dlq.RedisConfig{
		EntryKeyPrefix: "taskforge:test:dlq:",
		IndexSortedSet: "taskforge:test:dlq:index",
	})
	defer be.Close() //nolint:errcheck

	ctx := context.Background()
	if err := be.PutEntry(ctx, &task.DLQEntry{ID: "one"}); err != nil {
		t.Fatalf("PutEntry one: %v", err)
	}
	if err := be.PutEntry(ctx, &task.DLQEntry{ID: "two"}); err != nil {
		t.Fatalf("PutEntry two: %v", err)
	}

	entry, err := be.GetEntry(ctx, "two")
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if entry.ID != "two" {
		t.Fatalf("got id %q, want two", entry.ID)
	}

	ids, err := be.ListEntries(ctx, 0, 10)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(ids) != 2 || ids[0] != "two" || ids[1] != "one" {
		t.Fatalf("got ids %v, want [two one]", ids)
	}
}

func TestRedisBackend_Close(t *testing.T) {
	client := newFakeRedisClient()
	be := dlq.NewRedisBackendWithClient(client, dlq.RedisConfig{})

	if err := be.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !client.closed {
		t.Fatal("expected redis client to be closed")
	}
}

func TestNewRedisBackend_RequiresAddr(t *testing.T) {
	_, err := dlq.NewRedisBackend(dlq.RedisConfig{})
	if err == nil {
		t.Fatal("expected constructor error")
	}
}

func TestNewRedisBackend_ConstructsClient(t *testing.T) {
	be, err := dlq.NewRedisBackend(dlq.RedisConfig{
		Connection: taskredis.Config{Addr: "127.0.0.1:6379"},
	})
	if err != nil {
		t.Fatalf("NewRedisBackend: %v", err)
	}
	defer be.Close() //nolint:errcheck
}
