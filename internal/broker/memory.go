package broker

import (
	"context"
	"sync"
	"time"

	"github.com/OneLastStop529/taskforge/internal/task"
)

// queue is a simple in-memory FIFO channel-backed queue.
type queue struct {
	ch chan *task.Message
}

// MemoryBroker is an in-process broker backed by Go channels.
// It is suitable for testing and single-process deployments.
type MemoryBroker struct {
	mu     sync.RWMutex
	queues map[string]*queue
}

// NewMemoryBroker creates a new MemoryBroker.
func NewMemoryBroker() *MemoryBroker {
	return &MemoryBroker{queues: make(map[string]*queue)}
}

func (b *MemoryBroker) getOrCreateQueue(name string) *queue {
	b.mu.Lock()
	defer b.mu.Unlock()
	q, ok := b.queues[name]
	if !ok {
		q = &queue{ch: make(chan *task.Message, 1024)}
		b.queues[name] = q
	}
	return q
}

// Enqueue places the message onto the appropriate queue, respecting ScheduledAt.
func (b *MemoryBroker) Enqueue(ctx context.Context, msg *task.Message) error {
	q := b.getOrCreateQueue(msg.Queue)
	delay := time.Until(msg.ScheduledAt)
	if delay > 0 {
		go func() {
			select {
			case <-time.After(delay):
				q.ch <- msg
			case <-ctx.Done():
			}
		}()
		return nil
	}
	select {
	case q.ch <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Dequeue blocks until a message is available on one of the queues.
func (b *MemoryBroker) Dequeue(ctx context.Context, queues []string) (*task.Message, error) {
	// Build a reflect-free poll loop: build slice of cases so we can select over N channels.
	// We use a simple busy-select with a short sleep to avoid reflect overhead.
	for {
		for _, name := range queues {
			q := b.getOrCreateQueue(name)
			select {
			case msg := <-q.ch:
				return msg, nil
			default:
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// Ack is a no-op for the in-memory broker (messages are removed on dequeue).
func (b *MemoryBroker) Ack(_ context.Context, _ *task.Message) error { return nil }

// Nack re-enqueues the message so it can be retried.
func (b *MemoryBroker) Nack(ctx context.Context, msg *task.Message) error {
	return b.Enqueue(ctx, msg)
}

// Close is a no-op for the in-memory broker.
func (b *MemoryBroker) Close() error { return nil }
