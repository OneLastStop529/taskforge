package task

import (
	"context"
	"fmt"
	"sync"
)

// HandlerFunc is the function signature for task handlers.
// It receives a context and raw JSON payload, and returns raw JSON output or an error.
type HandlerFunc func(ctx context.Context, payload []byte) ([]byte, error)

// Registry maps task names to their handler functions.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]HandlerFunc
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]HandlerFunc)}
}

// Register associates a task name with a handler. Panics if the name is empty or already registered.
func (r *Registry) Register(name string, fn HandlerFunc) {
	if name == "" {
		panic("taskforge: task name must not be empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[name]; exists {
		panic(fmt.Sprintf("taskforge: task %q is already registered", name))
	}
	r.handlers[name] = fn
}

// Lookup returns the handler for a task name, or an error if not found.
func (r *Registry) Lookup(name string) (HandlerFunc, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fn, ok := r.handlers[name]
	if !ok {
		return nil, fmt.Errorf("taskforge: no handler registered for task %q", name)
	}
	return fn, nil
}

// Names returns all registered task names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.handlers))
	for n := range r.handlers {
		names = append(names, n)
	}
	return names
}
