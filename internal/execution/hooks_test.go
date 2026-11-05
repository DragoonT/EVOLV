package execution

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// hookRecorder returns a HookFunc that appends name to ran when it fires.
func hookRecorder(ran *[]string, name string) HookFunc {
	return func(_ context.Context, _ TaskInfo) error {
		*ran = append(*ran, name)
		return nil
	}
}

// hookCounter returns a HookFunc that bumps a shared counter when it fires.
func hookCounter(n *atomic.Int64) HookFunc {
	return func(_ context.Context, _ TaskInfo) error {
		n.Add(1)
		return nil
	}
}

func TestHookRegisterOrderDeterministic(t *testing.T) {
	var h Hooks
	var ran []string
	h.Register(HookBeforeTask, "alpha", hookRecorder(&ran, "alpha"))
	h.Register(HookBeforeTask, "beta", hookRecorder(&ran, "beta"))
	h.Register(HookBeforeTask, "gamma", hookRecorder(&ran, "gamma"))
	want := []string{"alpha", "beta", "gamma"}
	for i := 0; i < 3; i++ {
		ran = nil
		if err := h.Fire(context.Background(), HookBeforeTask, TaskInfo{TaskID: "t1"}); err != nil {
			t.Fatalf("fire %d: unexpected error: %v", i, err)
		}
		if !slices.Equal(ran, want) {
			t.Fatalf("fire %d: order = %v, want %v", i, ran, want)
		}
	}
}

func TestHookReplaceByName(t *testing.T) {
	var h Hooks
	var ran []string
	h.Register(HookAfterTask, "x", hookRecorder(&ran, "x-old"))
	h.Register(HookAfterTask, "y", hookRecorder(&ran, "y"))
	h.Register(HookAfterTask, "x", hookRecorder(&ran, "x-new"))
	if err := h.Fire(context.Background(), HookAfterTask, TaskInfo{TaskID: "t2"}); err != nil {
		t.Fatalf("fire: unexpected error: %v", err)
	}
	// "x" is replaced in place, so the first-registration order is kept.
	want := []string{"x-new", "y"}
	if !slices.Equal(ran, want) {
		t.Fatalf("ran = %v, want %v", ran, want)
	}
}

func TestHookPanicIsolation(t *testing.T) {
	var h Hooks
	healthyRan := false
	h.Register(HookOnFailure, "boom", func(context.Context, TaskInfo) error {
		panic("exploded")
	})
	h.Register(HookOnFailure, "healthy", func(context.Context, TaskInfo) error {
		healthyRan = true
		return nil
	})
	err := h.Fire(context.Background(), HookOnFailure, TaskInfo{TaskID: "t3"})
	if !healthyRan {
		t.Error("healthy hook after panicking hook did not run")
	}
	if err == nil {
		t.Fatal("fire: expected error from panicking hook, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"boom", "panicked", "exploded"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q does not mention %q", msg, want)
		}
	}
}

func TestHookJoinedErrors(t *testing.T) {
	errFirst := errors.New("first failed")
	errSecond := errors.New("second failed")
	var h Hooks
	healthyRan := false
	h.Register(HookOnFailure, "first", func(context.Context, TaskInfo) error { return errFirst })
	h.Register(HookOnFailure, "second", func(context.Context, TaskInfo) error { return errSecond })
	h.Register(HookOnFailure, "healthy", func(context.Context, TaskInfo) error {
		healthyRan = true
		return nil
	})
	err := h.Fire(context.Background(), HookOnFailure, TaskInfo{TaskID: "t4"})
	if !healthyRan {
		t.Error("healthy hook after failing hooks did not run")
	}
	if err == nil {
		t.Fatal("fire: expected joined error, got nil")
	}
	if !errors.Is(err, errFirst) || !errors.Is(err, errSecond) {
		t.Errorf("joined error %v does not wrap both sentinel errors", err)
	}
	msg := err.Error()
	for _, want := range []string{"first", "second"} {
		if !strings.Contains(msg, want) {
			t.Errorf("joined error %q does not name hook %q", msg, want)
		}
	}
}

func TestHookUnregister(t *testing.T) {
	var h Hooks
	var ran []string
	h.Register(HookOnCancel, "a", hookRecorder(&ran, "a"))
	h.Register(HookOnCancel, "b", hookRecorder(&ran, "b"))
	h.Unregister(HookOnCancel, "a")
	if err := h.Fire(context.Background(), HookOnCancel, TaskInfo{TaskID: "t5"}); err != nil {
		t.Fatalf("fire: unexpected error: %v", err)
	}
	if !slices.Equal(ran, []string{"b"}) {
		t.Fatalf("ran = %v, want [b]", ran)
	}
	// Unregistering an unknown name is a harmless no-op.
	h.Unregister(HookOnCancel, "missing")
	h.Unregister(HookBeforeTask, "b") // wrong point, also a no-op
	ran = nil
	if err := h.Fire(context.Background(), HookOnCancel, TaskInfo{TaskID: "t5"}); err != nil {
		t.Fatalf("fire after no-op unregister: unexpected error: %v", err)
	}
	if !slices.Equal(ran, []string{"b"}) {
		t.Fatalf("ran after no-op unregisters = %v, want [b]", ran)
	}
}

func TestHookZeroValueNoop(t *testing.T) {
	var h Hooks
	if err := h.Fire(context.Background(), HookBeforeTask, TaskInfo{TaskID: "t6"}); err != nil {
		t.Fatalf("zero-value fire: unexpected error: %v", err)
	}
	var hp *Hooks // nil receiver must be safe everywhere
	hp.Register(HookBeforeTask, "x", func(context.Context, TaskInfo) error { return nil })
	hp.Unregister(HookBeforeTask, "x")
	if err := hp.Fire(context.Background(), HookBeforeTask, TaskInfo{TaskID: "t6"}); err != nil {
		t.Fatalf("nil-receiver fire: unexpected error: %v", err)
	}
}

func TestHookContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	errAttempt := errors.New("attempt blew up")
	var h Hooks
	var got TaskInfo
	ctxErrAtHook := error(nil)
	h.Register(HookBeforeTask, "inspect", func(ctx context.Context, info TaskInfo) error {
		ctxErrAtHook = ctx.Err()
		got = info
		return nil
	})
	err := h.Fire(ctx, HookBeforeTask, TaskInfo{
		TaskID: "t7", RunID: "r7",
		From: StatusRunning, To: StatusFailed,
		Err: errAttempt, Attempt: 2,
	})
	if err != nil {
		t.Fatalf("fire: unexpected error: %v", err)
	}
	if !errors.Is(ctxErrAtHook, context.Canceled) {
		t.Errorf("hook saw ctx.Err() = %v, want context.Canceled", ctxErrAtHook)
	}
	if got.TaskID != "t7" || got.RunID != "r7" {
		t.Errorf("info IDs = %q/%q, want t7/r7", got.TaskID, got.RunID)
	}
	if got.From != StatusRunning || got.To != StatusFailed {
		t.Errorf("info statuses = %s→%s, want running→failed", got.From, got.To)
	}
	if !errors.Is(got.Err, errAttempt) {
		t.Errorf("info.Err = %v, want %v", got.Err, errAttempt)
	}
	if got.Attempt != 2 {
		t.Errorf("info.Attempt = %d, want 2", got.Attempt)
	}
	if got.At.IsZero() {
		t.Error("info.At left zero; Fire should stamp it")
	}
}

func TestHookEmptyPoint(t *testing.T) {
	var h Hooks
	ran := false
	h.Register(HookAfterTask, "a", func(context.Context, TaskInfo) error {
		ran = true
		return nil
	})
	if err := h.Fire(context.Background(), HookOnCancel, TaskInfo{TaskID: "t8"}); err != nil {
		t.Fatalf("fire on empty point: unexpected error: %v", err)
	}
	if ran {
		t.Error("hook from another point fired on empty point")
	}
}

func TestHookConcurrentRegisterFire(t *testing.T) {
	var h Hooks
	var fires atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			name := string(rune('a' + w))
			for i := 0; i < 50; i++ {
				h.Register(HookAfterTask, name, hookCounter(&fires))
				if i%10 == 0 {
					_ = h.Fire(context.Background(), HookAfterTask, TaskInfo{TaskID: "t9"})
				}
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			h.Unregister(HookAfterTask, "a")
		}
	}()
	wg.Wait()
	if err := h.Fire(context.Background(), HookAfterTask, TaskInfo{TaskID: "t9"}); err != nil {
		t.Fatalf("final fire: unexpected error: %v", err)
	}
}

func TestHookAtStamp(t *testing.T) {
	var h Hooks
	before := time.Now().UTC()
	var got time.Time
	h.Register(HookBeforeTask, "stamp", func(_ context.Context, info TaskInfo) error {
		got = info.At
		return nil
	})
	if err := h.Fire(context.Background(), HookBeforeTask, TaskInfo{TaskID: "t10"}); err != nil {
		t.Fatalf("fire: unexpected error: %v", err)
	}
	if got.Before(before) {
		t.Errorf("stamped At %v precedes fire start %v", got, before)
	}
	// A caller-supplied At is preserved, not overwritten.
	custom := before.Add(-time.Hour)
	h.Register(HookBeforeTask, "custom", func(_ context.Context, info TaskInfo) error {
		if !info.At.Equal(custom) {
			t.Errorf("info.At = %v, want caller-supplied %v", info.At, custom)
		}
		return nil
	})
	if err := h.Fire(context.Background(), HookBeforeTask, TaskInfo{TaskID: "t10", At: custom}); err != nil {
		t.Fatalf("fire with custom At: unexpected error: %v", err)
	}
}
