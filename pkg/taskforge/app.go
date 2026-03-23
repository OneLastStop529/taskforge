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
	"github.com/OneLastStop529/taskforge/internal/dlq"
	taskredis "github.com/OneLastStop529/taskforge/internal/redis"
	"github.com/OneLastStop529/taskforge/internal/result"
	"github.com/OneLastStop529/taskforge/internal/scheduler"
	"github.com/OneLastStop529/taskforge/internal/task"
	"github.com/OneLastStop529/taskforge/internal/worker"
)

// BackendKind identifies the broker/result backend implementation to use.
type BackendKind string

const (
	BackendMemory BackendKind = "memory"
	BackendRedis  BackendKind = "redis"
)

// HandlerFunc is the user-facing task handler signature.
type HandlerFunc = task.HandlerFunc

// Result is the outcome of a task execution exposed to callers.
type Result = task.Result

// DLQEntry is a dead-letter queue inspection record exposed to callers.
type DLQEntry = task.DLQEntry

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
	// BrokerBackend selects the task transport implementation.
	BrokerBackend BackendKind
	// ResultBackend selects the task result storage implementation.
	ResultBackend BackendKind
	// DLQBackend selects the dead-letter queue storage implementation.
	DLQBackend BackendKind
	// Redis contains connection settings for Redis-backed components.
	Redis RedisConfig
}

// RedisConfig holds Redis connection settings for future persistent backends.
type RedisConfig struct {
	Addr     string
	Username string
	Password string
	DB       int
}

// DefaultConfig returns a sensible out-of-the-box Config.
func DefaultConfig() Config {
	return Config{
		DefaultQueue:       "default",
		Concurrency:        10,
		ResultTTL:          24 * time.Hour,
		DefaultRetryPolicy: task.DefaultRetryPolicy(),
		BrokerBackend:      BackendMemory,
		ResultBackend:      BackendMemory,
		DLQBackend:         BackendMemory,
		Redis: RedisConfig{
			Addr: "127.0.0.1:6379",
		},
	}
}

// Validate checks whether the config is internally consistent.
func (c Config) Validate() error {
	c = c.withDefaults()
	if c.DefaultQueue == "" {
		return fmt.Errorf("taskforge: default queue must not be empty")
	}
	if err := validateBackendKind("broker backend", c.BrokerBackend); err != nil {
		return err
	}
	if err := validateBackendKind("result backend", c.ResultBackend); err != nil {
		return err
	}
	if err := validateBackendKind("dlq backend", c.DLQBackend); err != nil {
		return err
	}
	return nil
}

func (c Config) withDefaults() Config {
	if c.DLQBackend == "" {
		c.DLQBackend = c.ResultBackend
	}
	return c
}

func validateBackendKind(name string, kind BackendKind) error {
	switch kind {
	case BackendMemory, BackendRedis:
		return nil
	default:
		return fmt.Errorf("taskforge: unsupported %s %q", name, kind)
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
	dlq       dlq.Backend
	scheduler *scheduler.Scheduler
}

// New creates a new Taskforge App using the configured broker and result backends.
// It panics if the config is invalid or selects an unavailable backend.
func New(cfg Config) *App {
	app, err := Open(cfg)
	if err != nil {
		panic(err)
	}
	return app
}

// Open creates a new Taskforge App using the configured broker and result backends.
func Open(cfg Config) (*App, error) {
	cfg = cfg.withDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	b, err := buildBroker(cfg)
	if err != nil {
		return nil, err
	}
	be, err := buildResultBackend(cfg)
	if err != nil {
		_ = b.Close()
		return nil, err
	}
	deadletters, err := buildDLQBackend(cfg)
	if err != nil {
		_ = be.Close()
		_ = b.Close()
		return nil, err
	}
	return newApp(cfg, b, be, deadletters), nil
}

// NewMemory creates a new Taskforge App backed by the in-memory implementations.
func NewMemory(cfg Config) *App {
	cfg.BrokerBackend = BackendMemory
	cfg.ResultBackend = BackendMemory
	return New(cfg)
}

func newApp(cfg Config, b broker.Broker, be result.Backend, deadletters dlq.Backend) *App {
	reg := task.NewRegistry()
	a := &App{
		cfg:      cfg,
		broker:   b,
		registry: reg,
		backend:  be,
		dlq:      deadletters,
	}
	a.scheduler = scheduler.New(a.dispatchMsg, newID, nil)
	return a
}

func redisConnection(cfg Config) taskredis.Config {
	return taskredis.Config{
		Addr:     cfg.Redis.Addr,
		Username: cfg.Redis.Username,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	}
}

func buildBroker(cfg Config) (broker.Broker, error) {
	switch cfg.BrokerBackend {
	case BackendMemory:
		return broker.NewMemoryBroker(), nil
	case BackendRedis:
		return broker.NewRedisBroker(broker.RedisConfig{
			Connection: redisConnection(cfg),
		})
	default:
		return nil, fmt.Errorf("taskforge: unsupported broker backend %q", cfg.BrokerBackend)
	}
}

func buildResultBackend(cfg Config) (result.Backend, error) {
	switch cfg.ResultBackend {
	case BackendMemory:
		return result.NewMemoryBackend(cfg.ResultTTL), nil
	case BackendRedis:
		return result.NewRedisBackend(result.RedisConfig{
			Connection: redisConnection(cfg),
		}, cfg.ResultTTL)
	default:
		return nil, fmt.Errorf("taskforge: unsupported result backend %q", cfg.ResultBackend)
	}
}

func buildDLQBackend(cfg Config) (dlq.Backend, error) {
	switch cfg.DLQBackend {
	case BackendMemory:
		return dlq.NewMemoryBackend(), nil
	case BackendRedis:
		return dlq.NewRedisBackend(dlq.RedisConfig{
			Connection: redisConnection(cfg),
		})
	default:
		return nil, fmt.Errorf("taskforge: unsupported dlq backend %q", cfg.DLQBackend)
	}
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
	return a.enqueueMessage(ctx, msg)
}

// GetResult retrieves the result for the given task ID.
func (a *App) GetResult(ctx context.Context, id string) (*Result, error) {
	return a.backend.GetResult(ctx, id)
}

// ReplayDLQEntry re-enqueues a dead-lettered task using a new task ID.
func (a *App) ReplayDLQEntry(ctx context.Context, id string, opts ...EnqueueOption) (string, error) {
	entry, err := a.dlq.GetEntry(ctx, id)
	if err != nil {
		return "", fmt.Errorf("taskforge: replay dlq entry: %w", err)
	}

	msg := entry.Message
	msg.ID = newID()
	msg.Attempt = 0
	msg.EnqueuedAt = time.Now()
	msg.ScheduledAt = time.Time{}
	for _, o := range opts {
		o(&msg)
	}
	if msg.Queue == "" {
		msg.Queue = a.cfg.DefaultQueue
	}
	return a.enqueueMessage(ctx, &msg)
}

// StartWorker starts the worker pool and blocks until ctx is cancelled.
func (a *App) StartWorker(ctx context.Context) error {
	w := worker.New(a.broker, a.registry, a.backend, a.dlq, worker.Options{
		Queues:      []string{a.cfg.DefaultQueue},
		Concurrency: a.cfg.Concurrency,
	})
	return w.Start(ctx)
}

// GetDLQEntry retrieves the dead-lettered entry for the given task ID.
func (a *App) GetDLQEntry(ctx context.Context, id string) (*DLQEntry, error) {
	return a.dlq.GetEntry(ctx, id)
}

// ListDLQEntries returns dead-lettered task IDs ordered from newest to oldest.
func (a *App) ListDLQEntries(ctx context.Context, offset, limit int) ([]string, error) {
	return a.dlq.ListEntries(ctx, offset, limit)
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
	if err := a.backend.Close(); err != nil {
		return err
	}
	return a.dlq.Close()
}

// dispatchMsg is the scheduler callback that enqueues a pre-built message.
func (a *App) dispatchMsg(ctx context.Context, msg *task.Message) error {
	return a.broker.Enqueue(ctx, msg)
}

func (a *App) enqueueMessage(ctx context.Context, msg *task.Message) (string, error) {
	if err := a.broker.Enqueue(ctx, msg); err != nil {
		return "", fmt.Errorf("taskforge: enqueue: %w", err)
	}
	// Persist PENDING state immediately so callers can poll before the worker picks it up.
	_ = a.backend.SetResult(ctx, &task.Result{
		ID:    msg.ID,
		Name:  msg.Name,
		State: task.StatePending,
	})
	return msg.ID, nil
}

// newID generates a random 16-char hex task ID.
func newID() string {
	b := make([]byte, 8)
	rand.Read(b) //nolint:gosec // Not cryptographic – just a unique task ID.
	return fmt.Sprintf("%x", b)
}
