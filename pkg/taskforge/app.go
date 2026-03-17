// Package taskforge provides the public API for interacting with Taskforge,
// a cloud-native distributed task execution platform inspired by Celery,
// Temporal, and distributed job processing systems.
//
// # Quick Start
//
//	app := taskforge.New(taskforge.DefaultConfig())
//	app.Register("send_email", func(ctx context.Context, payload []byte) ([]byte, error) {
//	    // handle task
//	    return nil, nil
//	})
//	// In one goroutine / process:
//	app.StartWorker(ctx)
//	// In another:
//	id, _ := app.Enqueue(ctx, "send_email", map[string]string{"to": "user@example.com"})
//	result, _ := app.GetResult(ctx, id)
package taskforge

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"github.com/OneLastStop529/taskforge/internal/broker"
	"github.com/OneLastStop529/taskforge/internal/result"
	"github.com/OneLastStop529/taskforge/internal/scheduler"
	"github.com/OneLastStop529/taskforge/internal/task"
	"github.com/OneLastStop529/taskforge/internal/worker"
)

// HandlerFunc is the user-facing task handler signature.
type HandlerFunc = task.HandlerFunc

// Result is the outcome of a task execution exposed to callers.
type Result = task.Result

// State represents a task lifecycle state.
type State = task.State

const (
	StatePending  = task.StatePending
	StateRunning  = task.StateRunning
	StateSuccess  = task.StateSuccess
	StateFailed   = task.StateFailed
	StateRetrying = task.StateRetrying
	StateRevoked  = task.StateRevoked
)

// Config holds top-level Taskforge configuration.
type Config struct {
	// DefaultQueue is the queue name used when none is specified.
	DefaultQueue string
	// Concurrency is the number of concurrent task workers.
	Concurrency int
	// ResultTTL controls how long results are retained. Zero means forever.
	ResultTTL time.Duration
	// DefaultRetryPolicy is applied to all tasks unless overridden per-enqueue.
	DefaultRetryPolicy task.RetryPolicy
}

// DefaultConfig returns a sensible out-of-the-box Config.
func DefaultConfig() Config {
	return Config{
		DefaultQueue:       "default",
		Concurrency:        10,
		ResultTTL:          24 * time.Hour,
		DefaultRetryPolicy: task.DefaultRetryPolicy(),
	}
}

// EnqueueOption configures a single Enqueue call.
type EnqueueOption func(*task.Message)

// WithQueue sets the destination queue.
func WithQueue(q string) EnqueueOption {
	return func(m *task.Message) { m.Queue = q }
}

// WithDelay schedules the task to run after a delay.
func WithDelay(d time.Duration) EnqueueOption {
	return func(m *task.Message) { m.ScheduledAt = time.Now().Add(d) }
}

// WithScheduledAt sets an absolute scheduled time.
func WithScheduledAt(t time.Time) EnqueueOption {
	return func(m *task.Message) { m.ScheduledAt = t }
}

// WithRetryPolicy overrides the default retry policy for this task.
func WithRetryPolicy(rp task.RetryPolicy) EnqueueOption {
	return func(m *task.Message) { m.RetryPolicy = rp }
}

// WithTimeout sets a per-task execution timeout.
func WithTimeout(d time.Duration) EnqueueOption {
	return func(m *task.Message) { m.Timeout = d }
}

// WithPriority sets the task priority (lower number = higher priority when dequeued first).
func WithPriority(p int) EnqueueOption {
	return func(m *task.Message) { m.Priority = p }
}

// App is the central Taskforge application object.
type App struct {
	cfg       Config
	broker    broker.Broker
	registry  *task.Registry
	backend   result.Backend
	scheduler *scheduler.Scheduler
}

// New creates a new Taskforge App backed by an in-memory broker and result backend.
func New(cfg Config) *App {
	b := broker.NewMemoryBroker()
	be := result.NewMemoryBackend(cfg.ResultTTL)
	reg := task.NewRegistry()
	a := &App{
		cfg:      cfg,
		broker:   b,
		registry: reg,
		backend:  be,
	}
	a.scheduler = scheduler.New(a.dispatchMsg, newID, nil)
	return a
}

// Register binds a task name to a handler function.
// Call this before starting the worker.
func (a *App) Register(name string, fn HandlerFunc) {
	a.registry.Register(name, fn)
}

// Enqueue serializes payload as JSON, creates a task message, and places it
// on the broker. Returns the new task ID.
func (a *App) Enqueue(ctx context.Context, name string, payload interface{}, opts ...EnqueueOption) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("taskforge: marshal payload: %w", err)
	}
	msg := &task.Message{
		ID:          newID(),
		Name:        name,
		Payload:     json.RawMessage(raw),
		Queue:       a.cfg.DefaultQueue,
		RetryPolicy: a.cfg.DefaultRetryPolicy,
		EnqueuedAt:  time.Now(),
	}
	for _, o := range opts {
		o(msg)
	}
	if err := a.broker.Enqueue(ctx, msg); err != nil {
		return "", fmt.Errorf("taskforge: enqueue: %w", err)
	}
	// Persist PENDING state immediately so callers can poll before the worker picks it up.
	_ = a.backend.SetResult(ctx, &task.Result{
		ID:    msg.ID,
		Name:  name,
		State: task.StatePending,
	})
	return msg.ID, nil
}

// GetResult retrieves the result for the given task ID.
func (a *App) GetResult(ctx context.Context, id string) (*Result, error) {
	return a.backend.GetResult(ctx, id)
}

// StartWorker starts the worker pool and blocks until ctx is cancelled.
func (a *App) StartWorker(ctx context.Context) error {
	w := worker.New(a.broker, a.registry, a.backend, worker.Options{
		Queues:      []string{a.cfg.DefaultQueue},
		Concurrency: a.cfg.Concurrency,
	})
	return w.Start(ctx)
}

// AddSchedule registers a periodic task that fires on the given interval.
func (a *App) AddSchedule(name, taskName, queue string, interval time.Duration, payload interface{}) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("taskforge: marshal scheduled payload: %w", err)
	}
	a.scheduler.Add(scheduler.Entry{
		Name:        name,
		TaskName:    taskName,
		Queue:       queue,
		Schedule:    scheduler.EverySchedule{Interval: interval},
		Payload:     raw,
		RetryPolicy: a.cfg.DefaultRetryPolicy,
	})
	return nil
}

// StartScheduler runs the periodic scheduler until ctx is cancelled.
func (a *App) StartScheduler(ctx context.Context) {
	a.scheduler.Start(ctx)
}

// Close releases all resources held by the App.
func (a *App) Close() error {
	if err := a.broker.Close(); err != nil {
		return err
	}
	return a.backend.Close()
}

// dispatchMsg is the scheduler callback that enqueues a pre-built message.
func (a *App) dispatchMsg(ctx context.Context, msg *task.Message) error {
	return a.broker.Enqueue(ctx, msg)
}

// newID generates a random 16-char hex task ID.
func newID() string {
	b := make([]byte, 8)
	rand.Read(b) //nolint:gosec // Not cryptographic – just a unique task ID.
	return fmt.Sprintf("%x", b)
}
