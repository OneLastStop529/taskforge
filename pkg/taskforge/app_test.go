package taskforge_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/OneLastStop529/taskforge/pkg/taskforge"
)

func TestApp_EnqueueAndProcess(t *testing.T) {
	cfg := taskforge.DefaultConfig()
	cfg.Concurrency = 2
	cfg.ResultTTL = time.Minute
	app := taskforge.New(cfg)
	defer app.Close() //nolint:errcheck

	app.Register("double", func(_ context.Context, payload []byte) ([]byte, error) {
		var n int
		_ = json.Unmarshal(payload, &n)
		return json.Marshal(n * 2)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_ = app.StartWorker(ctx)
	}()

	id, err := app.Enqueue(ctx, "double", 21)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Poll for result.
	var r *taskforge.Result
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r, err = app.GetResult(ctx, id)
		if err == nil && r.State == taskforge.StateSuccess {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if r == nil || r.State != taskforge.StateSuccess {
		t.Fatalf("task did not succeed; state=%v err=%v", r, err)
	}
	var got int
	_ = json.Unmarshal(r.Output, &got)
	if got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
}

func TestApp_GetResultPending(t *testing.T) {
	app := taskforge.New(taskforge.DefaultConfig())
	defer app.Close() //nolint:errcheck

	// Register a handler but don't start a worker, so the task stays PENDING.
	app.Register("noop", func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil })

	ctx := context.Background()
	id, err := app.Enqueue(ctx, "noop", nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	r, err := app.GetResult(ctx, id)
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if r.State != taskforge.StatePending {
		t.Errorf("expected PENDING, got %s", r.State)
	}
}

func TestApp_TaskRetry(t *testing.T) {
	cfg := taskforge.DefaultConfig()
	cfg.Concurrency = 1
	cfg.DefaultRetryPolicy.MaxAttempts = 3
	cfg.DefaultRetryPolicy.InitialDelay = 20 * time.Millisecond
	cfg.DefaultRetryPolicy.Multiplier = 1.0
	app := taskforge.New(cfg)
	defer app.Close() //nolint:errcheck

	attempts := 0
	app.Register("flaky", func(_ context.Context, _ []byte) ([]byte, error) {
		attempts++
		if attempts < 3 {
			return nil, context.DeadlineExceeded
		}
		return nil, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = app.StartWorker(ctx) }()

	id, _ := app.Enqueue(ctx, "flaky", nil)
	deadline := time.Now().Add(4 * time.Second)
	var r *taskforge.Result
	for time.Now().Before(deadline) {
		r, _ = app.GetResult(ctx, id)
		if r != nil && (r.State == taskforge.StateSuccess || r.State == taskforge.StateFailed) {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if r == nil {
		t.Fatal("no result returned")
	}
	if r.State != taskforge.StateSuccess {
		t.Errorf("expected SUCCESS after retries, got %s", r.State)
	}
	if attempts < 3 {
		t.Errorf("expected at least 3 attempts, got %d", attempts)
	}
}

func TestApp_WithDelay(t *testing.T) {
	app := taskforge.New(taskforge.DefaultConfig())
	defer app.Close() //nolint:errcheck

	app.Register("noop2", func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = app.StartWorker(ctx) }()

	// Schedule task 200ms in the future; it should not succeed immediately.
	id, err := app.Enqueue(ctx, "noop2", nil, taskforge.WithDelay(200*time.Millisecond))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Check immediately – should still be PENDING.
	r, _ := app.GetResult(ctx, id)
	if r != nil && r.State == taskforge.StateSuccess {
		t.Error("task should not have succeeded yet")
	}

	// After the delay it should succeed.
	time.Sleep(500 * time.Millisecond)
	r, err = app.GetResult(ctx, id)
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if r.State != taskforge.StateSuccess {
		t.Errorf("expected SUCCESS after delay, got %s", r.State)
	}
}

func TestApp_UnknownTaskFails(t *testing.T) {
	cfg := taskforge.DefaultConfig()
	cfg.DefaultRetryPolicy.MaxAttempts = 1
	app := taskforge.New(cfg)
	defer app.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() { _ = app.StartWorker(ctx) }()

	id, _ := app.Enqueue(ctx, "ghost", nil)
	deadline := time.Now().Add(2 * time.Second)
	var r *taskforge.Result
	for time.Now().Before(deadline) {
		r, _ = app.GetResult(ctx, id)
		if r != nil && r.State == taskforge.StateFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if r == nil || r.State != taskforge.StateFailed {
		t.Errorf("expected FAILED for unknown task, got %v", r)
	}
}

func TestApp_ReplayDLQEntry(t *testing.T) {
	cfg := taskforge.DefaultConfig()
	cfg.Concurrency = 1
	cfg.DefaultRetryPolicy.MaxAttempts = 1
	app := taskforge.New(cfg)
	defer app.Close() //nolint:errcheck

	shouldFail := true
	app.Register("flaky_once", func(_ context.Context, payload []byte) ([]byte, error) {
		if shouldFail {
			return nil, context.DeadlineExceeded
		}
		return payload, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = app.StartWorker(ctx) }()

	originalID, err := app.Enqueue(ctx, "flaky_once", map[string]string{"msg": "retry me"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var failed *taskforge.Result
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		failed, _ = app.GetResult(ctx, originalID)
		if failed != nil && failed.State == taskforge.StateFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if failed == nil || failed.State != taskforge.StateFailed {
		t.Fatalf("expected original task to fail, got %v", failed)
	}

	shouldFail = false
	replayID, err := app.ReplayDLQEntry(ctx, originalID)
	if err != nil {
		t.Fatalf("ReplayDLQEntry: %v", err)
	}
	if replayID == originalID {
		t.Fatal("expected replay to allocate a new task ID")
	}

	var replayed *taskforge.Result
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		replayed, _ = app.GetResult(ctx, replayID)
		if replayed != nil && replayed.State == taskforge.StateSuccess {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if replayed == nil || replayed.State != taskforge.StateSuccess {
		t.Fatalf("expected replayed task to succeed, got %v", replayed)
	}

	entry, err := app.GetDLQEntry(ctx, originalID)
	if err != nil {
		t.Fatalf("GetDLQEntry: %v", err)
	}
	if entry.ReplayCount != 1 {
		t.Fatalf("got replay count %d, want 1", entry.ReplayCount)
	}
	if entry.LastReplayedTaskID != replayID {
		t.Fatalf("got last replayed task id %q, want %q", entry.LastReplayedTaskID, replayID)
	}
	if entry.LastReplayedAt.IsZero() {
		t.Fatal("expected last replayed at to be set")
	}
}

func TestApp_PurgeDLQEntry(t *testing.T) {
	cfg := taskforge.DefaultConfig()
	cfg.Concurrency = 1
	cfg.DefaultRetryPolicy.MaxAttempts = 1
	app := taskforge.New(cfg)
	defer app.Close() //nolint:errcheck

	app.Register("always_fail", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, context.DeadlineExceeded
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = app.StartWorker(ctx) }()

	id, err := app.Enqueue(ctx, "always_fail", nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		entry, getErr := app.GetDLQEntry(ctx, id)
		if getErr == nil && entry != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := app.PurgeDLQEntry(ctx, id); err != nil {
		t.Fatalf("PurgeDLQEntry: %v", err)
	}
	if _, err := app.GetDLQEntry(ctx, id); err == nil {
		t.Fatal("expected purged dlq entry lookup to fail")
	}

	ids, err := app.ListDLQEntries(ctx, 0, 10)
	if err != nil {
		t.Fatalf("ListDLQEntries: %v", err)
	}
	for _, existing := range ids {
		if existing == id {
			t.Fatalf("expected purged id %q to be absent from list %v", id, ids)
		}
	}
}

func TestDefaultConfig_UsesMemoryBackends(t *testing.T) {
	cfg := taskforge.DefaultConfig()

	if cfg.BrokerBackend != taskforge.BackendMemory {
		t.Fatalf("expected memory broker backend, got %q", cfg.BrokerBackend)
	}
	if cfg.ResultBackend != taskforge.BackendMemory {
		t.Fatalf("expected memory result backend, got %q", cfg.ResultBackend)
	}
	if cfg.DLQBackend != taskforge.BackendMemory {
		t.Fatalf("expected memory dlq backend, got %q", cfg.DLQBackend)
	}
	if cfg.IdempotencyBackend != taskforge.BackendMemory {
		t.Fatalf("expected memory idempotency backend, got %q", cfg.IdempotencyBackend)
	}
	if cfg.Redis.Addr == "" {
		t.Fatal("expected default redis address to be set")
	}
}

func TestOpen_InvalidConfig(t *testing.T) {
	cfg := taskforge.DefaultConfig()
	cfg.DefaultQueue = ""

	_, err := taskforge.Open(cfg)
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestOpen_RedisBackendsConstruct(t *testing.T) {
	cfg := taskforge.DefaultConfig()
	cfg.BrokerBackend = taskforge.BackendRedis
	cfg.ResultBackend = taskforge.BackendRedis
	cfg.DLQBackend = taskforge.BackendRedis
	cfg.IdempotencyBackend = taskforge.BackendRedis

	app, err := taskforge.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer app.Close() //nolint:errcheck
}
