// Package idempotency defines enqueue-time deduplication storage.
package idempotency

import (
	"context"
	"errors"
)

var ErrNotFound = errors.New("taskforge: idempotency key not found")

// Record stores the canonical task bound to an idempotency key.
type Record struct {
	Key    string `json:"key"`
	TaskID string `json:"task_id"`
}

// Backend claims and resolves idempotency keys to canonical task IDs.
type Backend interface {
	// ClaimOrGet atomically binds key to taskID or returns the existing record.
	ClaimOrGet(ctx context.Context, key, taskID string) (record Record, claimed bool, err error)
	// Get returns the canonical record for key.
	Get(ctx context.Context, key string) (Record, error)
	// ReleaseIfOwner deletes key only when it still points at taskID.
	ReleaseIfOwner(ctx context.Context, key, taskID string) error
	// Close releases any resources held by the backend.
	Close() error
}
