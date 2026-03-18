package result_test

import (
	"context"
	"testing"
	"time"

	"github.com/OneLastStop529/taskforge/internal/result"
	"github.com/OneLastStop529/taskforge/internal/task"
)

func TestMemoryBackend_SetAndGet(t *testing.T) {
	be := result.NewMemoryBackend(0)
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
}

func TestMemoryBackend_NotFound(t *testing.T) {
	be := result.NewMemoryBackend(0)
	defer be.Close() //nolint:errcheck

	_, err := be.GetResult(context.Background(), "missing")
	if err == nil {
		t.Error("expected error for missing result")
	}
}

func TestMemoryBackend_TTLExpiry(t *testing.T) {
	ttl := 50 * time.Millisecond
	be := result.NewMemoryBackend(ttl)
	defer be.Close() //nolint:errcheck

	ctx := context.Background()
	r := &task.Result{ID: "ttl1", Name: "t", State: task.StateSuccess}
	_ = be.SetResult(ctx, r)

	// Should be available immediately.
	if _, err := be.GetResult(ctx, "ttl1"); err != nil {
		t.Fatalf("expected result before TTL: %v", err)
	}

	// After TTL expires, it should be gone.
	time.Sleep(ttl + 10*time.Millisecond)
	if _, err := be.GetResult(ctx, "ttl1"); err == nil {
		t.Error("expected result to be expired")
	}
}

func TestMemoryBackend_Overwrite(t *testing.T) {
	be := result.NewMemoryBackend(0)
	defer be.Close() //nolint:errcheck

	ctx := context.Background()
	r1 := &task.Result{ID: "ow", Name: "t", State: task.StatePending}
	_ = be.SetResult(ctx, r1)
	r2 := &task.Result{ID: "ow", Name: "t", State: task.StateSuccess}
	_ = be.SetResult(ctx, r2)

	got, _ := be.GetResult(ctx, "ow")
	if got.State != task.StateSuccess {
		t.Errorf("expected SUCCESS after overwrite, got %q", got.State)
	}
}
