// Package task defines the core task types used throughout Taskforge.
package task

import (
	"encoding/json"
	"time"
)

// State represents the lifecycle state of a task.
type State string

const (
	StatePending  State = "PENDING"
	StateRunning  State = "RUNNING"
	StateSuccess  State = "SUCCESS"
	StateFailed   State = "FAILED"
	StateRetrying State = "RETRYING"
	StateRevoked  State = "REVOKED"
)

// RetryPolicy controls how failed tasks are retried.
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts (including the first one).
	MaxAttempts int
	// InitialDelay is the delay before the first retry.
	InitialDelay time.Duration
	// MaxDelay caps the backoff delay.
	MaxDelay time.Duration
	// Multiplier is applied to the delay on each subsequent retry (exponential backoff).
	Multiplier float64
}

// DefaultRetryPolicy returns a sensible default retry policy.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:  3,
		InitialDelay: time.Second,
		MaxDelay:     60 * time.Second,
		Multiplier:   2.0,
	}
}

// NextDelay calculates the backoff delay for a given attempt number (0-indexed).
func (r RetryPolicy) NextDelay(attempt int) time.Duration {
	if attempt <= 0 {
		return r.InitialDelay
	}
	d := float64(r.InitialDelay)
	for i := 0; i < attempt; i++ {
		d *= r.Multiplier
		if time.Duration(d) >= r.MaxDelay {
			return r.MaxDelay
		}
	}
	return time.Duration(d)
}

// Message is the envelope sent through the broker.
type Message struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Payload     json.RawMessage `json:"payload"`
	Queue       string          `json:"queue"`
	Priority    int             `json:"priority"`
	Attempt     int             `json:"attempt"`
	RetryPolicy RetryPolicy     `json:"retry_policy"`
	ScheduledAt time.Time       `json:"scheduled_at"`
	EnqueuedAt  time.Time       `json:"enqueued_at"`
	Timeout     time.Duration   `json:"timeout"`
}

// Result holds the outcome of a task execution.
type Result struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	State      State           `json:"state"`
	Output     json.RawMessage `json:"output,omitempty"`
	Error      string          `json:"error,omitempty"`
	Attempt    int             `json:"attempt"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
}

// DLQEntry captures the terminal failure context for a dead-lettered task.
type DLQEntry struct {
	ID       string    `json:"id"`
	Message  Message   `json:"message"`
	Result   Result    `json:"result"`
	FailedAt time.Time `json:"failed_at"`
}
