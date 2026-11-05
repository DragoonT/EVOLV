package execution

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// HookPoint names a lifecycle moment of the execution engine at which
// synchronous hooks may fire.
type HookPoint string

const (
	// HookBeforeTask fires just before a task attempt starts.
	HookBeforeTask HookPoint = "before-task"
	// HookAfterTask fires after a task attempt ends, success or failure.
	HookAfterTask HookPoint = "after-task"
	// HookOnFailure fires when a task attempt fails.
	HookOnFailure HookPoint = "on-failure"
	// HookOnCancel fires when a task is cancelled.
	HookOnCancel HookPoint = "on-cancel"
)

// HookFunc is one synchronous lifecycle callback. It runs inline at the hook
// point: keep it fast and non-blocking. A returned error is collected by
// Hooks.Fire into a joined error; a panic is recovered and becomes that
// hook's error without stopping the other hooks.
type HookFunc func(ctx context.Context, info TaskInfo) error

// TaskInfo describes the lifecycle event that triggered a hook.
type TaskInfo struct {
	TaskID  string    // task the event is about
	RunID   string    // engine run the task belongs to, if known
	From    Status    // status before the transition (zero on start)
	To      Status    // status after the transition (zero on start)
	Err     error     // attempt error, set for on-failure
	Attempt int       // 1-based attempt number, when known
	At      time.Time // when the event happened; Fire stamps it if left zero
}

// hookEntry is a single registered hook at one point.
type hookEntry struct {
	name string
	fn   HookFunc
}

// Hooks is an ordered registry of synchronous lifecycle callbacks grouped by
// HookPoint. Hooks at a point run sequentially in registration order; every
// hook is isolated from panics and every error is joined. The zero value is
// ready to use, and a nil *Hooks is a safe no-op everywhere.
type Hooks struct {
	hookMu     sync.Mutex
	hookPoints map[HookPoint][]hookEntry
}

// Register adds fn under name at point. Registering a name that already
// exists at that point replaces the previous function and keeps its original
// position, so firing order stays the first-registration order. Registering
// an empty name or a nil fn is a no-op.
func (h *Hooks) Register(point HookPoint, name string, fn HookFunc) {
	if h == nil || name == "" || fn == nil {
		return
	}
	h.hookMu.Lock()
	defer h.hookMu.Unlock()
	if h.hookPoints == nil {
		h.hookPoints = make(map[HookPoint][]hookEntry)
	}
	for i, e := range h.hookPoints[point] {
		if e.name == name {
			h.hookPoints[point][i].fn = fn
			return
		}
	}
	h.hookPoints[point] = append(h.hookPoints[point], hookEntry{name: name, fn: fn})
}

// Unregister removes the hook named name from point. Removing an unknown
// name is a no-op.
func (h *Hooks) Unregister(point HookPoint, name string) {
	if h == nil {
		return
	}
	h.hookMu.Lock()
	defer h.hookMu.Unlock()
	entries := h.hookPoints[point]
	for i, e := range entries {
		if e.name == name {
			h.hookPoints[point] = append(entries[:i], entries[i+1:]...)
			return
		}
	}
}

// Fire runs every hook registered at point sequentially in registration
// order. Each hook runs with panic recovery: a panicking hook becomes that
// hook's error and the remaining hooks still run. All errors are returned
// joined (errors.Join semantics), each naming its hook. Fire on a nil
// receiver, a zero Hooks, or a point with no hooks returns nil without work.
func (h *Hooks) Fire(ctx context.Context, point HookPoint, info TaskInfo) error {
	if h == nil {
		return nil
	}
	h.hookMu.Lock()
	entries := make([]hookEntry, len(h.hookPoints[point]))
	copy(entries, h.hookPoints[point])
	h.hookMu.Unlock()
	if len(entries) == 0 {
		return nil
	}
	if info.At.IsZero() {
		info.At = time.Now().UTC()
	}
	var errs []error
	for _, e := range entries {
		if err := hookInvoke(ctx, e.fn, point, e.name, info); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// hookInvoke runs one hook with panic isolation and names it in any error.
func hookInvoke(ctx context.Context, fn HookFunc, point HookPoint, name string, info TaskInfo) (err error) {
	if fn == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("hook %q at %s panicked: %v", name, point, r)
		}
	}()
	if err := fn(ctx, info); err != nil {
		return fmt.Errorf("hook %q at %s: %w", name, point, err)
	}
	return nil
}
