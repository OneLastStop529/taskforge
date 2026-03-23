package dlq

import (
	"context"
	"fmt"
	"sync"

	"github.com/OneLastStop529/taskforge/internal/task"
)

// MemoryBackend is an in-process DLQ backend for single-process execution and tests.
type MemoryBackend struct {
	mu      sync.RWMutex
	entries map[string]*task.DLQEntry
	order   []string
}

// NewMemoryBackend creates a MemoryBackend.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{
		entries: make(map[string]*task.DLQEntry),
	}
}

// PutEntry stores or overwrites a DLQ entry.
func (b *MemoryBackend) PutEntry(_ context.Context, entry *task.DLQEntry) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.entries[entry.ID]; !exists {
		b.order = append(b.order, entry.ID)
	}
	copied := *entry
	b.entries[entry.ID] = &copied
	return nil
}

// GetEntry retrieves a DLQ entry by task ID.
func (b *MemoryBackend) GetEntry(_ context.Context, id string) (*task.DLQEntry, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	entry, ok := b.entries[id]
	if !ok {
		return nil, fmt.Errorf("taskforge: dlq entry not found for task %q", id)
	}
	copied := *entry
	return &copied, nil
}

// ListEntries returns DLQ task IDs ordered from newest to oldest.
func (b *MemoryBackend) ListEntries(_ context.Context, offset, limit int) ([]string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = len(b.order)
	}
	ids := make([]string, 0, limit)
	for i := len(b.order) - 1 - offset; i >= 0 && len(ids) < limit; i-- {
		ids = append(ids, b.order[i])
	}
	return ids, nil
}

// Close releases resources held by the backend.
func (b *MemoryBackend) Close() error {
	return nil
}
