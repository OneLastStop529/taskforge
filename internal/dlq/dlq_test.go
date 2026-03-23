package dlq_test

import (
	"context"
	"testing"
	"time"

	"github.com/OneLastStop529/taskforge/internal/dlq"
	"github.com/OneLastStop529/taskforge/internal/task"
)

func TestMemoryBackend_PutGetAndList(t *testing.T) {
	be := dlq.NewMemoryBackend()
	defer be.Close() //nolint:errcheck

	ctx := context.Background()
	first := &task.DLQEntry{ID: "1", FailedAt: time.Unix(10, 0)}
	second := &task.DLQEntry{ID: "2", FailedAt: time.Unix(20, 0)}

	if err := be.PutEntry(ctx, first); err != nil {
		t.Fatalf("PutEntry first: %v", err)
	}
	if err := be.PutEntry(ctx, second); err != nil {
		t.Fatalf("PutEntry second: %v", err)
	}

	got, err := be.GetEntry(ctx, "2")
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if got.ID != "2" {
		t.Fatalf("got id %q, want 2", got.ID)
	}

	ids, err := be.ListEntries(ctx, 0, 10)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(ids) != 2 || ids[0] != "2" || ids[1] != "1" {
		t.Fatalf("got ids %v, want [2 1]", ids)
	}
}
