package worker

import "fmt"

// panicError wraps a recovered panic value into an error.
func panicError(v interface{}) error {
	return fmt.Errorf("panic: %v", v)
}
