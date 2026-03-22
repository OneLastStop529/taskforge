package taskforge_test

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	redis "github.com/redis/go-redis/v9"

	"github.com/OneLastStop529/taskforge/pkg/taskforge"
)

func TestRedisIntegration_EnqueueWorkerResultAcrossApps(t *testing.T) {
	cfg, cleanup := redisTestConfig(t)
	defer cleanup()

	workerApp, err := taskforge.Open(cfg)
	if err != nil {
		t.Fatalf("Open worker app: %v", err)
	}
	defer workerApp.Close() //nolint:errcheck

	producerApp, err := taskforge.Open(cfg)
	if err != nil {
		t.Fatalf("Open producer app: %v", err)
	}
	defer producerApp.Close() //nolint:errcheck

	resultApp, err := taskforge.Open(cfg)
	if err != nil {
		t.Fatalf("Open result app: %v", err)
	}
	defer resultApp.Close() //nolint:errcheck

	workerApp.Register("double", func(_ context.Context, payload []byte) ([]byte, error) {
		var n int
		if err := json.Unmarshal(payload, &n); err != nil {
			return nil, err
		}
		return json.Marshal(n * 2)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = workerApp.StartWorker(ctx) }()

	id, err := producerApp.Enqueue(ctx, "double", 21)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	r := waitForResult(t, resultApp, id, func(r *taskforge.Result) bool {
		return r.State == taskforge.StateSuccess
	})

	var got int
	if err := json.Unmarshal(r.Output, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
}

func TestRedisIntegration_DelayedTaskSurvivesWorkerRestart(t *testing.T) {
	cfg, cleanup := redisTestConfig(t)
	defer cleanup()

	worker1, err := taskforge.Open(cfg)
	if err != nil {
		t.Fatalf("Open worker1: %v", err)
	}
	defer worker1.Close() //nolint:errcheck

	worker2, err := taskforge.Open(cfg)
	if err != nil {
		t.Fatalf("Open worker2: %v", err)
	}
	defer worker2.Close() //nolint:errcheck

	producerApp, err := taskforge.Open(cfg)
	if err != nil {
		t.Fatalf("Open producer: %v", err)
	}
	defer producerApp.Close() //nolint:errcheck

	resultApp, err := taskforge.Open(cfg)
	if err != nil {
		t.Fatalf("Open result app: %v", err)
	}
	defer resultApp.Close() //nolint:errcheck

	handler := func(_ context.Context, payload []byte) ([]byte, error) {
		return payload, nil
	}
	worker1.Register("echo", handler)
	worker2.Register("echo", handler)

	ctx1, cancel1 := context.WithCancel(context.Background())
	go func() { _ = worker1.StartWorker(ctx1) }()

	id, err := producerApp.Enqueue(context.Background(), "echo", map[string]string{"msg": "hello"}, taskforge.WithDelay(200*time.Millisecond))
	if err != nil {
		t.Fatalf("Enqueue delayed task: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	cancel1()
	time.Sleep(50 * time.Millisecond)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	go func() { _ = worker2.StartWorker(ctx2) }()

	r := waitForResult(t, resultApp, id, func(r *taskforge.Result) bool {
		return r.State == taskforge.StateSuccess
	})

	var payload map[string]string
	if err := json.Unmarshal(r.Output, &payload); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if payload["msg"] != "hello" {
		t.Fatalf("got payload %v, want hello", payload)
	}
}

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
