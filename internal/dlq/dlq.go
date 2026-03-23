// Package dlq defines storage for terminally failed task inspection data.
package dlq

import (
	"context"

	"github.com/OneLastStop529/taskforge/internal/task"
)

// Backend stores and retrieves dead-letter queue entries.
type Backend interface {
	// PutEntry stores or overwrites a DLQ entry.
	PutEntry(ctx context.Context, entry *task.DLQEntry) error
	// GetEntry retrieves a DLQ entry by task ID.
	GetEntry(ctx context.Context, id string) (*task.DLQEntry, error)
	// ListEntries returns DLQ task IDs ordered from newest to oldest.
	ListEntries(ctx context.Context, offset, limit int) ([]string, error)
	// Close releases any resources held by the backend.
	Close() error
}
