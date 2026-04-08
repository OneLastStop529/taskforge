// Package result defines the interface for storing task results.
package result

import (
	"context"

	"github.com/OneLastStop529/taskforge/internal/task"
)

// Backend is the interface for reading and writing task results.
type Backend interface {
	// SetResult stores a task result.
	SetResult(ctx context.Context, r *task.Result) error
	// GetResult retrieves the result for a task ID.
	GetResult(ctx context.Context, id string) (*task.Result, error)
	// ResolveResultID resolves a full task ID from an exact or unique prefix.
	ResolveResultID(ctx context.Context, idOrPrefix string) (string, error)
	// Close releases any resources held by the backend.
	Close() error
}
