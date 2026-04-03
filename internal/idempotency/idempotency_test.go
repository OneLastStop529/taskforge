package idempotency_test

import (
	"context"
	"errors"
	"testing"

	"github.com/OneLastStop529/taskforge/internal/idempotency"
)

func TestMemoryBackend_ClaimOrGet(t *testing.T) {
	be := idempotency.NewMemoryBackend()
	defer be.Close() //nolint:errcheck

	ctx := context.Background()
	record, claimed, err := be.ClaimOrGet(ctx, "invoice:123", "task-1")
	if err != nil {
		t.Fatalf("ClaimOrGet first: %v", err)
	}
	if !claimed {
		t.Fatal("expected first claim to succeed")
	}
	if record.TaskID != "task-1" {
		t.Fatalf("got task id %q, want task-1", record.TaskID)
	}

	record, claimed, err = be.ClaimOrGet(ctx, "invoice:123", "task-2")
	if err != nil {
		t.Fatalf("ClaimOrGet second: %v", err)
	}
	if claimed {
		t.Fatal("expected duplicate claim to reuse existing record")
	}
	if record.TaskID != "task-1" {
		t.Fatalf("got canonical task id %q, want task-1", record.TaskID)
	}
}

func TestMemoryBackend_GetNotFound(t *testing.T) {
	be := idempotency.NewMemoryBackend()
	defer be.Close() //nolint:errcheck

	_, err := be.Get(context.Background(), "missing")
	if !errors.Is(err, idempotency.ErrNotFound) {
		t.Fatalf("got err %v, want ErrNotFound", err)
	}
}

func TestMemoryBackend_ReleaseIfOwner(t *testing.T) {
	be := idempotency.NewMemoryBackend()
	defer be.Close() //nolint:errcheck

	ctx := context.Background()
	if _, _, err := be.ClaimOrGet(ctx, "invoice:123", "task-1"); err != nil {
		t.Fatalf("ClaimOrGet: %v", err)
	}

	if err := be.ReleaseIfOwner(ctx, "invoice:123", "task-2"); err != nil {
		t.Fatalf("ReleaseIfOwner non-owner: %v", err)
	}
	record, err := be.Get(ctx, "invoice:123")
	if err != nil {
		t.Fatalf("Get after non-owner release: %v", err)
	}
	if record.TaskID != "task-1" {
		t.Fatalf("got task id %q, want task-1", record.TaskID)
	}

	if err := be.ReleaseIfOwner(ctx, "invoice:123", "task-1"); err != nil {
		t.Fatalf("ReleaseIfOwner owner: %v", err)
	}
	_, err = be.Get(ctx, "invoice:123")
	if !errors.Is(err, idempotency.ErrNotFound) {
		t.Fatalf("got err %v, want ErrNotFound", err)
	}
}
