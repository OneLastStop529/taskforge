package result

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/OneLastStop529/taskforge/internal/task"
)

// entry wraps a result with an expiry time.
type entry struct {
	result    *task.Result
	expiresAt time.Time
}

// MemoryBackend is an in-process, TTL-based result backend.
type MemoryBackend struct {
	mu      sync.RWMutex
	entries map[string]*entry
	ttl     time.Duration
	stopGC  chan struct{}
}

// NewMemoryBackend creates a MemoryBackend that expires results after ttl.
// If ttl is zero, results are kept forever.
func NewMemoryBackend(ttl time.Duration) *MemoryBackend {
	b := &MemoryBackend{
		entries: make(map[string]*entry),
		ttl:     ttl,
		stopGC:  make(chan struct{}),
	}
	if ttl > 0 {
		go b.gc()
	}
	return b
}

// gc periodically removes expired entries.
func (b *MemoryBackend) gc() {
	ticker := time.NewTicker(b.ttl / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			b.mu.Lock()
			now := time.Now()
			for id, e := range b.entries {
				if now.After(e.expiresAt) {
					delete(b.entries, id)
				}
			}
			b.mu.Unlock()
		case <-b.stopGC:
			return
		}
	}
}

// SetResult stores or overwrites the result for a task.
func (b *MemoryBackend) SetResult(_ context.Context, r *task.Result) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := &entry{result: r}
	if b.ttl > 0 {
		e.expiresAt = time.Now().Add(b.ttl)
	}
	b.entries[r.ID] = e
	return nil
}

// GetResult retrieves a stored result by task ID.
func (b *MemoryBackend) GetResult(_ context.Context, id string) (*task.Result, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	e, ok := b.entries[id]
	if !ok {
		return nil, fmt.Errorf("taskforge: result not found for task %q", id)
	}
	if b.ttl > 0 && time.Now().After(e.expiresAt) {
		return nil, fmt.Errorf("taskforge: result expired for task %q", id)
	}
	return e.result, nil
}

// ResolveResultID resolves a full task ID from an exact or unique prefix.
func (b *MemoryBackend) ResolveResultID(_ context.Context, idOrPrefix string) (string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if e, ok := b.entries[idOrPrefix]; ok {
		if b.ttl == 0 || time.Now().Before(e.expiresAt) {
			return idOrPrefix, nil
		}
	}

	var match string
	now := time.Now()
	for id, e := range b.entries {
		if b.ttl > 0 && now.After(e.expiresAt) {
			continue
		}
		if !strings.HasPrefix(id, idOrPrefix) {
			continue
		}
		if match != "" {
			return "", fmt.Errorf("taskforge: result id prefix %q is ambiguous", idOrPrefix)
		}
		match = id
	}
	if match == "" {
		return "", fmt.Errorf("taskforge: result not found for task %q", idOrPrefix)
	}
	return match, nil
}

// Close stops the GC goroutine.
func (b *MemoryBackend) Close() error {
	if b.ttl > 0 {
		close(b.stopGC)
	}
	return nil
}
