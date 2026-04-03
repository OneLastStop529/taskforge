package idempotency

import (
	"context"
	"sync"
)

// MemoryBackend stores idempotency claims in-process for tests and local use.
type MemoryBackend struct {
	mu      sync.RWMutex
	records map[string]Record
}

// NewMemoryBackend constructs an in-memory idempotency backend.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{
		records: make(map[string]Record),
	}
}

// ClaimOrGet atomically stores a new claim or returns the canonical record.
func (b *MemoryBackend) ClaimOrGet(_ context.Context, key, taskID string) (Record, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if record, ok := b.records[key]; ok {
		return record, false, nil
	}
	record := Record{Key: key, TaskID: taskID}
	b.records[key] = record
	return record, true, nil
}

// Get returns the canonical record for a key.
func (b *MemoryBackend) Get(_ context.Context, key string) (Record, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	record, ok := b.records[key]
	if !ok {
		return Record{}, ErrNotFound
	}
	return record, nil
}

// ReleaseIfOwner removes a claim only when the caller owns it.
func (b *MemoryBackend) ReleaseIfOwner(_ context.Context, key, taskID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	record, ok := b.records[key]
	if !ok || record.TaskID != taskID {
		return nil
	}
	delete(b.records, key)
	return nil
}

// Close releases resources held by the backend.
func (b *MemoryBackend) Close() error {
	return nil
}
