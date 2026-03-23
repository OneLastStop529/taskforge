package taskforge_test

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"

	"github.com/OneLastStop529/taskforge/pkg/taskforge"
)

func redisTestConfig(t *testing.T) (taskforge.Config, func()) {
	t.Helper()

	addr := os.Getenv("TASKFORGE_REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}

	db := 15
	if raw := os.Getenv("TASKFORGE_REDIS_DB"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("invalid TASKFORGE_REDIS_DB: %v", err)
		}
		db = parsed
	}

	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Skipf("redis unavailable at %s db=%d: %v", addr, db, err)
	}
	if err := client.FlushDB(ctx).Err(); err != nil {
		_ = client.Close()
		t.Fatalf("FlushDB before test: %v", err)
	}

	cfg := taskforge.DefaultConfig()
	cfg.BrokerBackend = taskforge.BackendRedis
	cfg.ResultBackend = taskforge.BackendRedis
	cfg.DLQBackend = taskforge.BackendRedis
	cfg.Redis.Addr = addr
	cfg.Redis.DB = db
	cfg.Concurrency = 1
	cfg.DefaultRetryPolicy.InitialDelay = 20 * time.Millisecond
	cfg.DefaultRetryPolicy.Multiplier = 1.0

	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = client.FlushDB(ctx).Err()
		_ = client.Close()
	}
	return cfg, cleanup
}

func openRedisApp(t *testing.T, cfg taskforge.Config, label string) *taskforge.App {
	t.Helper()

	app, err := taskforge.Open(cfg)
	if err != nil {
		t.Fatalf("Open %s app: %v", label, err)
	}
	return app
}

func waitForResult(t *testing.T, app *taskforge.App, id string, done func(*taskforge.Result) bool) *taskforge.Result {
	t.Helper()

	deadline := time.Now().Add(4 * time.Second)
	var last *taskforge.Result
	for time.Now().Before(deadline) {
		r, err := app.GetResult(context.Background(), id)
		if err == nil {
			last = r
			if done(r) {
				return r
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for result %s, last=%v", id, last)
	return nil
}

func waitForDLQEntry(t *testing.T, app *taskforge.App, id string) *taskforge.DLQEntry {
	t.Helper()

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		entry, err := app.GetDLQEntry(context.Background(), id)
		if err == nil {
			return entry
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for dlq entry %s", id)
	return nil
}
