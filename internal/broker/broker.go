// Package broker defines the interface through which tasks are enqueued and consumed.
package broker

import (
	"context"

	"github.com/OneLastStop529/taskforge/internal/task"
)

// Broker is the interface that wraps task message transport.
type Broker interface {
	// Enqueue places a message onto the named queue.
	Enqueue(ctx context.Context, msg *task.Message) error
	// Dequeue blocks until a message is available on one of the given queues,
	// or the context is cancelled.
	Dequeue(ctx context.Context, queues []string) (*task.Message, error)
	// Ack acknowledges successful processing of a message.
	Ack(ctx context.Context, msg *task.Message) error
	// Nack negative-acknowledges a message (signals failure / requeue).
	Nack(ctx context.Context, msg *task.Message) error
	// Close releases any resources held by the broker.
	Close() error
}
