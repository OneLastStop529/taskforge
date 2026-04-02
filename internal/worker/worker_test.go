package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OneLastStop529/taskforge/internal/broker"
	"github.com/OneLastStop529/taskforge/internal/dlq"
	"github.com/OneLastStop529/taskforge/internal/result"
	"github.com/OneLastStop529/taskforge/internal/task"
)

func TestFinalize_RetryExhaustedWritesDLQ(t *testing.T) {
	ctx := context.Background()
	results := result.NewMemoryBackend(0)
	defer results.Close() //nolint:errcheck
	deadletters := dlq.NewMemoryBackend()
	defer deadletters.Close() //nolint:errcheck

	w := New(broker.NewMemoryBroker(), task.NewRegistry(), results, deadletters, Options{})
	msg := &task.Message{
		ID:    "task-1",
		Name:  "failer",
		Queue: "default",
		RetryPolicy: task.RetryPolicy{
			MaxAttempts: 1,
		},
	}
	r := &task.Result{
		ID:        msg.ID,
		Name:      msg.Name,
		Attempt:   0,
		StartedAt: time.Now().Add(-time.Second),
	}

	w.finalize(ctx, msg, r, nil, errors.New("boom"))

	got, err := results.GetResult(ctx, msg.ID)
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if got.State != task.StateFailed {
		t.Fatalf("got state %s, want FAILED", got.State)
	}

	entry, err := deadletters.GetEntry(ctx, msg.ID)
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if entry.Result.Error != "boom" {
		t.Fatalf("got dlq error %q, want boom", entry.Result.Error)
	}
	if entry.Message.ID != msg.ID {
		t.Fatalf("got message id %q, want %q", entry.Message.ID, msg.ID)
	}
}

func TestFinalize_RetryableFailureDoesNotWriteDLQ(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	b := broker.NewMemoryBroker()
	results := result.NewMemoryBackend(0)
	defer results.Close() //nolint:errcheck
	deadletters := dlq.NewMemoryBackend()
	defer deadletters.Close() //nolint:errcheck

	w := New(b, task.NewRegistry(), results, deadletters, Options{})
	msg := &task.Message{
		ID:    "task-2",
		Name:  "retrying",
		Queue: "default",
		RetryPolicy: task.RetryPolicy{
			MaxAttempts:  3,
			InitialDelay: time.Millisecond,
			Multiplier:   1,
			MaxDelay:     time.Millisecond,
		},
	}
	r := &task.Result{
		ID:        msg.ID,
		Name:      msg.Name,
		Attempt:   0,
		StartedAt: time.Now().Add(-time.Second),
	}

	w.finalize(ctx, msg, r, nil, errors.New("temporary"))

	if _, err := deadletters.GetEntry(context.Background(), msg.ID); err == nil {
		t.Fatal("expected no dlq entry for retryable failure")
	}

	retryMsg, err := b.Dequeue(ctx, []string{"default"})
	if err != nil {
		t.Fatalf("Dequeue retry message: %v", err)
	}
	if retryMsg.Attempt != 1 {
		t.Fatalf("got retry attempt %d, want 1", retryMsg.Attempt)
	}
}
