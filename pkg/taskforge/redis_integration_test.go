package taskforge_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/OneLastStop529/taskforge/pkg/taskforge"
)

func TestRedisIntegration_EnqueueWorkerResultAcrossApps(t *testing.T) {
	cfg, cleanup := redisTestConfig(t)
	defer cleanup()

	workerApp := openRedisApp(t, cfg, "worker")
	defer workerApp.Close() //nolint:errcheck

	producerApp := openRedisApp(t, cfg, "producer")
	defer producerApp.Close() //nolint:errcheck

	resultApp := openRedisApp(t, cfg, "result")
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

	worker1 := openRedisApp(t, cfg, "worker1")
	defer worker1.Close() //nolint:errcheck

	worker2 := openRedisApp(t, cfg, "worker2")
	defer worker2.Close() //nolint:errcheck

	producerApp := openRedisApp(t, cfg, "producer")
	defer producerApp.Close() //nolint:errcheck

	resultApp := openRedisApp(t, cfg, "result")
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

func TestRedisIntegration_DLQVisibleAcrossApps(t *testing.T) {
	cfg, cleanup := redisTestConfig(t)
	defer cleanup()

	workerApp := openRedisApp(t, cfg, "worker")
	defer workerApp.Close() //nolint:errcheck

	producerApp := openRedisApp(t, cfg, "producer")
	defer producerApp.Close() //nolint:errcheck

	inspectorApp := openRedisApp(t, cfg, "inspector")
	defer inspectorApp.Close() //nolint:errcheck

	workerApp.Register("always_fail", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, context.DeadlineExceeded
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = workerApp.StartWorker(ctx) }()

	id, err := producerApp.Enqueue(ctx, "always_fail", nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	r := waitForResult(t, inspectorApp, id, func(r *taskforge.Result) bool {
		return r.State == taskforge.StateFailed
	})
	if r.State != taskforge.StateFailed {
		t.Fatalf("got result state %s, want FAILED", r.State)
	}

	entry := waitForDLQEntry(t, inspectorApp, id)
	if entry.ID != id {
		t.Fatalf("got dlq id %q, want %q", entry.ID, id)
	}
	if entry.Result.Error == "" {
		t.Fatal("expected dlq result error to be populated")
	}

	ids, err := inspectorApp.ListDLQEntries(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ListDLQEntries: %v", err)
	}
	if len(ids) == 0 || ids[0] != id {
		t.Fatalf("got dlq ids %v, want first id %q", ids, id)
	}
}

func TestRedisIntegration_ReplayDLQEntryAcrossApps(t *testing.T) {
	cfg, cleanup := redisTestConfig(t)
	defer cleanup()

	workerApp := openRedisApp(t, cfg, "worker")
	defer workerApp.Close() //nolint:errcheck

	producerApp := openRedisApp(t, cfg, "producer")
	defer producerApp.Close() //nolint:errcheck

	inspectorApp := openRedisApp(t, cfg, "inspector")
	defer inspectorApp.Close() //nolint:errcheck

	var mu sync.Mutex
	shouldFail := true
	workerApp.Register("toggle_fail", func(_ context.Context, payload []byte) ([]byte, error) {
		mu.Lock()
		fail := shouldFail
		shouldFail = false
		mu.Unlock()
		if fail {
			return nil, context.DeadlineExceeded
		}
		return payload, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = workerApp.StartWorker(ctx) }()

	originalID, err := producerApp.Enqueue(ctx, "toggle_fail", map[string]string{"msg": "hello"})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	failed := waitForResult(t, inspectorApp, originalID, func(r *taskforge.Result) bool {
		return r.State == taskforge.StateFailed
	})
	if failed.State != taskforge.StateFailed {
		t.Fatalf("got result state %s, want FAILED", failed.State)
	}

	replayID, err := inspectorApp.ReplayDLQEntry(context.Background(), originalID)
	if err != nil {
		t.Fatalf("ReplayDLQEntry: %v", err)
	}
	if replayID == originalID {
		t.Fatal("expected replay to allocate a new task ID")
	}

	replayed := waitForResult(t, inspectorApp, replayID, func(r *taskforge.Result) bool {
		return r.State == taskforge.StateSuccess
	})

	var payload map[string]string
	if err := json.Unmarshal(replayed.Output, &payload); err != nil {
		t.Fatalf("unmarshal replay output: %v", err)
	}
	if payload["msg"] != "hello" {
		t.Fatalf("got payload %v, want hello", payload)
	}

	entry := waitForDLQEntry(t, inspectorApp, originalID)
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

func TestRedisIntegration_PurgeDLQEntryAcrossApps(t *testing.T) {
	cfg, cleanup := redisTestConfig(t)
	defer cleanup()

	workerApp := openRedisApp(t, cfg, "worker")
	defer workerApp.Close() //nolint:errcheck

	producerApp := openRedisApp(t, cfg, "producer")
	defer producerApp.Close() //nolint:errcheck

	inspectorApp := openRedisApp(t, cfg, "inspector")
	defer inspectorApp.Close() //nolint:errcheck

	workerApp.Register("always_fail", func(_ context.Context, _ []byte) ([]byte, error) {
		return nil, context.DeadlineExceeded
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = workerApp.StartWorker(ctx) }()

	id, err := producerApp.Enqueue(ctx, "always_fail", nil)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	_ = waitForDLQEntry(t, inspectorApp, id)

	if err := inspectorApp.PurgeDLQEntry(context.Background(), id); err != nil {
		t.Fatalf("PurgeDLQEntry: %v", err)
	}
	if _, err := inspectorApp.GetDLQEntry(context.Background(), id); err == nil {
		t.Fatal("expected purged dlq entry lookup to fail")
	}

	ids, err := inspectorApp.ListDLQEntries(context.Background(), 0, 10)
	if err != nil {
		t.Fatalf("ListDLQEntries: %v", err)
	}
	for _, existing := range ids {
		if existing == id {
			t.Fatalf("expected purged id %q to be absent from list %v", id, ids)
		}
	}
}
