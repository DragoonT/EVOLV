package agent

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// Resource bounds for JS runtimes, plugins, and tools.
const (
	// DefaultMaxCallStackSize limits recursion depth to prevent stack overflow crashes.
	DefaultMaxCallStackSize = 1024

	// DefaultPluginLoadTimeout limits execution time during initial plugin compilation.
	DefaultPluginLoadTimeout = 5 * time.Second

	// DefaultPluginEventTimeout limits execution time for event handlers and timer callbacks.
	DefaultPluginEventTimeout = 5 * time.Second

	// DefaultToolTimeout limits execution time for tool execution.
	DefaultToolTimeout = 10 * time.Second
)

var (
	// ErrExecutionTimeout is returned when JS execution exceeds its allocated time limit.
	ErrExecutionTimeout = errors.New("execution timeout exceeded")

	// ErrCallStackExceeded is returned when JS recursion exceeds maximum call stack size.
	ErrCallStackExceeded = errors.New("maximum call stack size exceeded")
)

// isTimeout reports whether err was caused by an execution timeout interrupt.
func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrExecutionTimeout) {
		return true
	}
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		if errors.Is(interrupted.Unwrap(), ErrExecutionTimeout) {
			return true
		}
		if v, ok := interrupted.Value().(error); ok && errors.Is(v, ErrExecutionTimeout) {
			return true
		}
		if s, ok := interrupted.Value().(string); ok && strings.Contains(s, "timeout") {
			return true
		}
		return true
	}
	return strings.Contains(err.Error(), "timeout")
}

// isStackOverflow reports whether err was caused by exceeding the max call stack size.
func isStackOverflow(err error) bool {
	if err == nil {
		return false
	}
	var stackErr *goja.StackOverflowError
	if errors.As(err, &stackErr) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "stack")
}

// runWithTimeout executes fn within the specified timeout duration.
// If fn exceeds the deadline, vm.Interrupt is invoked with ErrExecutionTimeout.
// Once fn returns, the VM's interrupt flag is safely cleared.
func runWithTimeout(vm *goja.Runtime, timeout time.Duration, fn func() error) error {
	if timeout <= 0 {
		return fn()
	}

	var mu sync.Mutex
	interrupted := false

	timer := time.AfterFunc(timeout, func() {
		mu.Lock()
		interrupted = true
		mu.Unlock()
		vm.Interrupt(ErrExecutionTimeout)
	})

	err := fn()

	timer.Stop()
	mu.Lock()
	vm.ClearInterrupt()
	wasInterrupted := interrupted
	mu.Unlock()

	if wasInterrupted || isTimeout(err) {
		return fmt.Errorf("%w (%v limit)", ErrExecutionTimeout, timeout)
	}
	if isStackOverflow(err) {
		return fmt.Errorf("%w: %v", ErrCallStackExceeded, err)
	}
	return err
}
