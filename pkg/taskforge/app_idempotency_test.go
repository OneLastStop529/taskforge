package taskforge

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OneLastStop529/taskforge/internal/broker"
	"github.com/OneLastStop529/taskforge/internal/dlq"
	"github.com/OneLastStop529/taskforge/internal/idempotency"
	"github.com/OneLastStop529/taskforge/internal/result"
	"github.com/OneLastStop529/taskforge/internal/task"
)

type countingBroker struct {
	mu       sync.Mutex
	messages []*task.Message
	fail     error
}

func (b *countingBroker) Enqueue(_ context.Context, msg *task.Message) error {
	if b.fail != nil {
		return b.fail
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	copied := *msg
	b.messages = append(b.messages, &copied)
	return nil
}

func (b *countingBroker) Dequeue(_ context.Context, _ []string) (*task.Message, error) {
	return nil, context.Canceled
}
func (b *countingBroker) Ack(_ context.Context, _ *task.Message) error  { return nil }
func (b *countingBroker) Nack(_ context.Context, _ *task.Message) error { return nil }
func (b *countingBroker) Close() error                                  { return nil }

func (b *countingBroker) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.messages)
}

func newTestAppWithBroker(t *testing.T, b broker.Broker) *App {
	t.Helper()

	cfg := DefaultConfig()
	cfg.ResultTTL = time.Minute
	return newApp(
		cfg,
		b,
		result.NewMemoryBackend(cfg.ResultTTL),
		dlq.NewMemoryBackend(),
		idempotency.NewMemoryBackend(),
	)
}

func TestAppEnqueue_WithIdempotencyKeyReusesCanonicalTaskID(t *testing.T) {
	b := &countingBroker{}
	app := newTestAppWithBroker(t, b)
	defer app.Close() //nolint:errcheck

	firstID, err := app.Enqueue(context.Background(), "send_email", map[string]string{"to": "one@example.com"}, WithIdempotencyKey("email:123"))
	if err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	secondID, err := app.Enqueue(context.Background(), "send_email", map[string]string{"to": "two@example.com"}, WithIdempotencyKey("email:123"))
	if err != nil {
		t.Fatalf("second Enqueue: %v", err)
	}

	if firstID != secondID {
		t.Fatalf("got ids %q and %q, want canonical reuse", firstID, secondID)
	}
	if b.count() != 1 {
		t.Fatalf("got %d broker enqueues, want 1", b.count())
	}

	r, err := app.GetResult(context.Background(), firstID)
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if r.State != StatePending {
		t.Fatalf("got state %s, want PENDING", r.State)
	}
}

func TestAppEnqueue_WithIdempotencyKeyRollbackOnBrokerFailure(t *testing.T) {
	b := &countingBroker{fail: errors.New("boom")}
	app := newTestAppWithBroker(t, b)
	defer app.Close() //nolint:errcheck

	_, err := app.Enqueue(context.Background(), "send_email", nil, WithIdempotencyKey("email:123"))
	if err == nil {
		t.Fatal("expected enqueue error")
	}

	record, getErr := app.idempotency.Get(context.Background(), "email:123")
	if !errors.Is(getErr, idempotency.ErrNotFound) {
		t.Fatalf("got idempotency record %v err=%v, want record released", record, getErr)
	}
}

func TestAppEnqueue_WithIdempotencyKeyConcurrentRace(t *testing.T) {
	b := &countingBroker{}
	app := newTestAppWithBroker(t, b)
	defer app.Close() //nolint:errcheck

	const callers = 16
	ids := make(chan string, callers)
	errs := make(chan error, callers)

	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := app.Enqueue(context.Background(), "send_email", nil, WithIdempotencyKey("email:123"))
			if err != nil {
				errs <- err
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)

	for err := range errs {
		t.Fatalf("Enqueue: %v", err)
	}

	var canonical string
	for id := range ids {
		if canonical == "" {
			canonical = id
			continue
		}
		if id != canonical {
			t.Fatalf("got duplicate ids %q and %q, want canonical reuse", canonical, id)
		}
	}
	if b.count() != 1 {
		t.Fatalf("got %d broker enqueues, want 1", b.count())
	}
}

func TestAppEnqueue_WithoutIdempotencyKeyPreservesBehavior(t *testing.T) {
	b := &countingBroker{}
	app := newTestAppWithBroker(t, b)
	defer app.Close() //nolint:errcheck

	firstID, err := app.Enqueue(context.Background(), "send_email", nil)
	if err != nil {
		t.Fatalf("first Enqueue: %v", err)
	}
	secondID, err := app.Enqueue(context.Background(), "send_email", nil)
	if err != nil {
		t.Fatalf("second Enqueue: %v", err)
	}

	if firstID == secondID {
		t.Fatal("expected distinct ids without idempotency key")
	}
	if b.count() != 2 {
		t.Fatalf("got %d broker enqueues, want 2", b.count())
	}
}

func TestReplayDLQEntry_ClearsOriginalIdempotencyKey(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Concurrency = 1
	cfg.DefaultRetryPolicy.MaxAttempts = 1
	app := New(cfg)
	defer app.Close() //nolint:errcheck

	var attempts atomic.Int32
	app.Register("toggle_fail", func(_ context.Context, payload []byte) ([]byte, error) {
		if attempts.Add(1) == 1 {
			return nil, context.DeadlineExceeded
		}
		return payload, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = app.StartWorker(ctx) }()

	originalID, err := app.Enqueue(ctx, "toggle_fail", map[string]string{"msg": "hello"}, WithIdempotencyKey("email:123"))
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	failed := false
	for time.Now().Before(deadline) {
		r, getErr := app.GetResult(ctx, originalID)
		if getErr == nil && r.State == StateFailed {
			failed = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !failed {
		t.Fatalf("timed out waiting for original task %q to fail", originalID)
	}

	replayID, err := app.ReplayDLQEntry(ctx, originalID)
	if err != nil {
		t.Fatalf("ReplayDLQEntry: %v", err)
	}
	if replayID == originalID {
		t.Fatal("expected replay to allocate a new task ID")
	}

	replayedID, err := app.Enqueue(ctx, "toggle_fail", map[string]string{"msg": "ignored"}, WithIdempotencyKey("email:123"))
	if err != nil {
		t.Fatalf("duplicate Enqueue after failure: %v", err)
	}
	if replayedID != originalID {
		t.Fatalf("got duplicate enqueue id %q, want original failed id %q", replayedID, originalID)
	}

	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r, getErr := app.GetResult(ctx, replayID)
		if getErr == nil && r.State == StateSuccess {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for replayed task %q to succeed", replayID)
}
