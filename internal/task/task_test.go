package task_test

import (
	"context"
	"testing"
	"time"

	"github.com/OneLastStop529/taskforge/internal/task"
)

func TestRetryPolicyNextDelay(t *testing.T) {
	p := task.RetryPolicy{
		MaxAttempts:  5,
		InitialDelay: time.Second,
		MaxDelay:     30 * time.Second,
		Multiplier:   2.0,
	}
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{5, 30 * time.Second}, // capped
		{9, 30 * time.Second}, // still capped
	}
	for _, tc := range tests {
		got := p.NextDelay(tc.attempt)
		if got != tc.want {
			t.Errorf("NextDelay(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestRegistry_RegisterAndLookup(t *testing.T) {
	reg := task.NewRegistry()
	called := false
	reg.Register("ping", func(_ context.Context, _ []byte) ([]byte, error) {
		called = true
		return nil, nil
	})
	fn, err := reg.Lookup("ping")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fn == nil {
		t.Fatal("expected non-nil handler")
	}
	_, _ = fn(context.Background(), nil)
	if !called {
		t.Error("handler was not called")
	}
}

func TestRegistry_LookupUnknown(t *testing.T) {
	reg := task.NewRegistry()
	_, err := reg.Lookup("nope")
	if err == nil {
		t.Fatal("expected error for unknown task")
	}
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	reg := task.NewRegistry()
	reg.Register("dup", func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil })
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	reg.Register("dup", func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil })
}

func TestRegistry_Names(t *testing.T) {
	reg := task.NewRegistry()
	reg.Register("a", func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil })
	reg.Register("b", func(_ context.Context, _ []byte) ([]byte, error) { return nil, nil })
	names := reg.Names()
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}
}
