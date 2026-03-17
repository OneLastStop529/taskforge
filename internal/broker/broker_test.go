package broker_test

import (
	"context"
	"testing"
	"time"

	"github.com/OneLastStop529/taskforge/internal/broker"
	"github.com/OneLastStop529/taskforge/internal/task"
)

func newMsg(id, name, queue string) *task.Message {
	return &task.Message{
		ID:          id,
		Name:        name,
		Queue:       queue,
		RetryPolicy: task.DefaultRetryPolicy(),
		EnqueuedAt:  time.Now(),
	}
}

func TestMemoryBroker_EnqueueDequeue(t *testing.T) {
	b := broker.NewMemoryBroker()
	defer b.Close() //nolint:errcheck

	ctx := context.Background()
	msg := newMsg("1", "ping", "default")
	if err := b.Enqueue(ctx, msg); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	got, err := b.Dequeue(ctx, []string{"default"})
	if err != nil {
		t.Fatalf("Dequeue: %v", err)
	}
	if got.ID != msg.ID {
		t.Errorf("got ID %q, want %q", got.ID, msg.ID)
	}
}

func TestMemoryBroker_MultipleQueues(t *testing.T) {
	b := broker.NewMemoryBroker()
	defer b.Close() //nolint:errcheck

	ctx := context.Background()
	_ = b.Enqueue(ctx, newMsg("a", "t1", "q1"))
	_ = b.Enqueue(ctx, newMsg("b", "t2", "q2"))

	got, err := b.Dequeue(ctx, []string{"q2", "q1"})
	if err != nil {
		t.Fatal(err)
	}
	// q2 is first in the list, so it should be drained first.
	if got.Queue != "q2" {
		t.Errorf("expected q2, got %q", got.Queue)
	}
}

func TestMemoryBroker_Ack(t *testing.T) {
	b := broker.NewMemoryBroker()
	ctx := context.Background()
	msg := newMsg("x", "t", "default")
	_ = b.Enqueue(ctx, msg)
	got, _ := b.Dequeue(ctx, []string{"default"})
	if err := b.Ack(ctx, got); err != nil {
		t.Fatalf("Ack: %v", err)
	}
}

func TestMemoryBroker_Nack_Requeues(t *testing.T) {
	b := broker.NewMemoryBroker()
	ctx := context.Background()
	msg := newMsg("y", "t", "default")
	_ = b.Enqueue(ctx, msg)
	got, _ := b.Dequeue(ctx, []string{"default"})
	if err := b.Nack(ctx, got); err != nil {
		t.Fatalf("Nack: %v", err)
	}
	// The message should be re-available.
	got2, err := b.Dequeue(ctx, []string{"default"})
	if err != nil {
		t.Fatal(err)
	}
	if got2.ID != msg.ID {
		t.Errorf("expected re-queued message %q, got %q", msg.ID, got2.ID)
	}
}

func TestMemoryBroker_ContextCancelled(t *testing.T) {
	b := broker.NewMemoryBroker()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err := b.Dequeue(ctx, []string{"empty"})
	if err == nil {
		t.Error("expected error when context is already cancelled")
	}
}

func TestMemoryBroker_ScheduledDelay(t *testing.T) {
	b := broker.NewMemoryBroker()
	ctx := context.Background()
	msg := newMsg("z", "delayed", "default")
	msg.ScheduledAt = time.Now().Add(50 * time.Millisecond)
	_ = b.Enqueue(ctx, msg)

	// Should not be available yet.
	dctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	_, err := b.Dequeue(dctx, []string{"default"})
	if err == nil {
		t.Error("expected timeout; message should not be available yet")
	}

	// After the delay it should appear.
	time.Sleep(80 * time.Millisecond)
	dctx2, cancel2 := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel2()
	got, err := b.Dequeue(dctx2, []string{"default"})
	if err != nil {
		t.Fatalf("expected delayed message to be available: %v", err)
	}
	if got.ID != msg.ID {
		t.Errorf("got %q, want %q", got.ID, msg.ID)
	}
}
