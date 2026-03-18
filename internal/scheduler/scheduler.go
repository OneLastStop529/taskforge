// Package scheduler implements periodic task scheduling (analogous to Celery Beat).
package scheduler

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/OneLastStop529/taskforge/internal/task"
)

// Dispatcher is the function called by the scheduler to enqueue a task message.
type Dispatcher func(ctx context.Context, msg *task.Message) error

// Entry describes a periodic task.
type Entry struct {
	// Name is the unique schedule entry name (can differ from task name).
	Name string
	// TaskName is the registered task name.
	TaskName string
	// Queue is the destination queue.
	Queue string
	// Schedule determines when the task runs.
	Schedule Schedule
	// Payload is the default payload for each invocation.
	Payload []byte
	// RetryPolicy overrides the default retry policy.
	RetryPolicy task.RetryPolicy
}

// Schedule represents a recurring schedule.
type Schedule interface {
	// Next returns the next run time after t.
	Next(t time.Time) time.Time
}

// EverySchedule fires at a fixed interval.
type EverySchedule struct {
	Interval time.Duration
}

// Next returns t + Interval.
func (e EverySchedule) Next(t time.Time) time.Time {
	return t.Add(e.Interval)
}

// Scheduler runs periodic entries and dispatches them via a Dispatcher.
type Scheduler struct {
	mu         sync.Mutex
	entries    []*schedulerEntry
	dispatcher Dispatcher
	logger     *log.Logger
	idGen      func() string
}

type schedulerEntry struct {
	Entry
	nextRun time.Time
}

// New creates a Scheduler.
func New(dispatcher Dispatcher, idGen func() string, logger *log.Logger) *Scheduler {
	if logger == nil {
		logger = log.Default()
	}
	return &Scheduler{
		dispatcher: dispatcher,
		idGen:      idGen,
		logger:     logger,
	}
}

// Add registers a periodic entry. Safe to call before or after Start.
func (s *Scheduler) Add(e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, &schedulerEntry{
		Entry:   e,
		nextRun: e.Schedule.Next(time.Now()),
	})
}

// Start runs the scheduling loop until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.tick(ctx, now)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if now.Before(e.nextRun) {
			continue
		}
		msg := &task.Message{
			ID:          s.idGen(),
			Name:        e.TaskName,
			Queue:       e.Queue,
			Payload:     e.Payload,
			RetryPolicy: e.RetryPolicy,
			EnqueuedAt:  now,
		}
		if err := s.dispatcher(ctx, msg); err != nil {
			s.logger.Printf("taskforge scheduler: failed to dispatch %s: %v", e.Name, err)
		} else {
			s.logger.Printf("taskforge scheduler: dispatched %s", e.Name)
		}
		e.nextRun = e.Schedule.Next(now)
	}
}
