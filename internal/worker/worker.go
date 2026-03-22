// Package worker implements the task worker and worker pool.
package worker

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/OneLastStop529/taskforge/internal/broker"
	"github.com/OneLastStop529/taskforge/internal/result"
	"github.com/OneLastStop529/taskforge/internal/task"
)

// Options configure a Worker.
type Options struct {
	// Queues is the list of queue names to consume from, in priority order.
	Queues []string
	// Concurrency is the number of tasks processed simultaneously.
	Concurrency int
	// Logger is used for operational log messages.
	Logger *log.Logger
}

// Worker consumes tasks from a broker and dispatches them to a registry.
type Worker struct {
	opts     Options
	broker   broker.Broker
	registry *task.Registry
	backend  result.Backend
	wg       sync.WaitGroup
	cancel   context.CancelFunc
}

// New creates a Worker with the given components and options.
func New(b broker.Broker, reg *task.Registry, be result.Backend, opts Options) *Worker {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 1
	}
	if len(opts.Queues) == 0 {
		opts.Queues = []string{"default"}
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	return &Worker{
		opts:     opts,
		broker:   b,
		registry: reg,
		backend:  be,
	}
}

// Start launches the worker goroutines and blocks until ctx is cancelled.
func (w *Worker) Start(ctx context.Context) error {
	ctx, w.cancel = context.WithCancel(ctx)
	sem := make(chan struct{}, w.opts.Concurrency)
	for {
		msg, err := w.broker.Dequeue(ctx, w.opts.Queues)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			w.opts.Logger.Printf("taskforge worker: dequeue error: %v", err)
			continue
		}
		sem <- struct{}{}
		w.wg.Add(1)
		go func(m *task.Message) {
			defer func() {
				<-sem
				w.wg.Done()
			}()
			w.process(ctx, m)
		}(msg)
	}
	w.wg.Wait()
	return nil
}

// Stop gracefully stops the worker.
func (w *Worker) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
}

// process executes a single task message.
func (w *Worker) process(ctx context.Context, msg *task.Message) {
	r := &task.Result{
		ID:        msg.ID,
		Name:      msg.Name,
		State:     task.StateRunning,
		Attempt:   msg.Attempt,
		StartedAt: time.Now(),
	}
	_ = w.backend.SetResult(ctx, r)

	handler, err := w.registry.Lookup(msg.Name)
	if err != nil {
		w.finalize(ctx, msg, r, nil, err)
		return
	}

	// Apply per-task timeout if configured.
	execCtx := ctx
	if msg.Timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, msg.Timeout)
		defer cancel()
	}

	output, execErr := w.safeExec(execCtx, handler, msg.Payload)
	w.finalize(ctx, msg, r, output, execErr)
}

// safeExec calls the handler and recovers from panics.
func (w *Worker) safeExec(ctx context.Context, fn task.HandlerFunc, payload []byte) (out []byte, err error) {
	defer func() {
		if p := recover(); p != nil {
			w.opts.Logger.Printf("taskforge worker: panic recovered: %v", p)
			err = panicError(p)
		}
	}()
	return fn(ctx, payload)
}

// finalize decides whether to mark the task as succeeded, failed, or retrying.
func (w *Worker) finalize(ctx context.Context, msg *task.Message, r *task.Result, output []byte, err error) {
	r.FinishedAt = time.Now()
	if err == nil {
		r.State = task.StateSuccess
		if len(output) > 0 {
			r.Output = json.RawMessage(output)
		}
		w.opts.Logger.Printf("taskforge worker: task %s (%s) succeeded", msg.Name, msg.ID)
		_ = w.backend.SetResult(ctx, r)
		_ = w.broker.Ack(ctx, msg)
		return
	}

	r.Error = err.Error()
	nextAttempt := msg.Attempt + 1
	if nextAttempt < msg.RetryPolicy.MaxAttempts {
		r.State = task.StateRetrying
		delay := msg.RetryPolicy.NextDelay(msg.Attempt)
		w.opts.Logger.Printf("taskforge worker: task %s (%s) failed (attempt %d/%d), retrying in %s: %v",
			msg.Name, msg.ID, nextAttempt, msg.RetryPolicy.MaxAttempts, delay, err)
		_ = w.backend.SetResult(ctx, r)
		retry := *msg
		retry.Attempt = nextAttempt
		retry.ScheduledAt = time.Now().Add(delay)
		_ = w.broker.Ack(ctx, msg)
		_ = w.broker.Enqueue(ctx, &retry)
		return
	}

	r.State = task.StateFailed
	w.opts.Logger.Printf("taskforge worker: task %s (%s) failed permanently after %d attempts: %v",
		msg.Name, msg.ID, msg.RetryPolicy.MaxAttempts, err)
	_ = w.backend.SetResult(ctx, r)
	_ = w.broker.Ack(ctx, msg)
}
