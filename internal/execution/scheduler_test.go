package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// schedEventLog records ordered scheduler events so tests can assert on the
// exact interleaving of task starts and finishes.
type schedEventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *schedEventLog) add(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, fmt.Sprintf(format, args...))
}

func (l *schedEventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

// indexOf returns the position of an exact event, or -1.
func (l *schedEventLog) indexOf(event string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, e := range l.events {
		if e == event {
			return i
		}
	}
	return -1
}

// schedFlight tracks how many task bodies run at once and the peak reached.
type schedFlight struct {
	inFlight atomic.Int64
	mu       sync.Mutex
	peak     int64
}

func (f *schedFlight) enter() {
	n := f.inFlight.Add(1)
	f.mu.Lock()
	if n > f.peak {
		f.peak = n
	}
	f.mu.Unlock()
}

func (f *schedFlight) exit() { f.inFlight.Add(-1) }

func (f *schedFlight) maxInFlight() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.peak
}

// schedTaskSet builds queued tasks from specs like "a", "b:a" or "d:b+c",
// where the part after the colon lists dependencies separated by "+".
func schedTaskSet(specs []string) []Task {
	out := make([]Task, 0, len(specs))
	for _, spec := range specs {
		parts := strings.SplitN(spec, ":", 2)
		t := NewTask(parts[0], "task "+parts[0])
		if len(parts) == 2 && parts[1] != "" {
			t.DependsOn = strings.Split(parts[1], "+")
		}
		out = append(out, t)
	}
	return out
}

// schedIDs maps a task list to a set of ids for membership assertions.
func schedIDs(tasks []Task) map[string]bool {
	m := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		m[t.ID] = true
	}
	return m
}

func TestSchedulerRunsWavesInDependencyOrder(t *testing.T) {
	log := &schedEventLog{}
	var doneOrder []string
	var waveMu, doneMu sync.Mutex
	var waves [][]string
	s, err := NewScheduler(Config{
		Workers: 4,
		OnWaveStart: func(wave int, ids []string) {
			waveMu.Lock()
			waves = append(waves, append([]string(nil), ids...))
			waveMu.Unlock()
		},
		OnTaskDone: func(taskID string) {
			doneMu.Lock()
			doneOrder = append(doneOrder, taskID)
			doneMu.Unlock()
		},
	}, func(ctx context.Context, task Task) error {
		log.add("start:%s", task.ID)
		log.add("end:%s", task.ID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Run(context.Background(), schedTaskSet([]string{"a", "b:a", "c:a", "d:b+c"}))
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if res.Waves != 3 {
		t.Fatalf("started %d waves, want 3", res.Waves)
	}
	wantWaves := [][]string{{"a"}, {"b", "c"}, {"d"}}
	if len(waves) != len(wantWaves) {
		t.Fatalf("OnWaveStart fired %d times, want %d: %v", len(waves), len(wantWaves), waves)
	}
	for i, want := range wantWaves {
		if strings.Join(waves[i], ",") != strings.Join(want, ",") {
			t.Errorf("wave %d = %v, want %v", i, waves[i], want)
		}
	}
	// Every child must start only after its parent finished: compare exact
	// positions in the recorded event sequence.
	for _, edge := range [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}} {
		start := log.indexOf("start:" + edge[1])
		end := log.indexOf("end:" + edge[0])
		if start < 0 || end < 0 {
			t.Fatalf("missing events for edge %v: %v", edge, log.snapshot())
		}
		if start < end {
			t.Errorf("task %s started at %d before parent %s finished at %d", edge[1], start, edge[0], end)
		}
	}
	if len(doneOrder) != 4 {
		t.Errorf("OnTaskDone fired %d times, want 4", len(doneOrder))
	}
	got := strings.Join(res.Completed, ",")
	if got != "a,b,c,d" {
		t.Errorf("Completed = %q, want a,b,c,d", got)
	}
}

func TestSchedulerConcurrencyCap(t *testing.T) {
	tests := []struct {
		name    string
		workers int
		cap     int
		peakMin int64
	}{
		{"one worker runs serially", 1, 1, 1},
		{"three workers never exceed three", 3, 3, 3},
		{"negative workers fall back to default", -2, schedDefaultWorkers, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flight := &schedFlight{}
			s, err := NewScheduler(Config{Workers: tc.workers}, func(ctx context.Context, task Task) error {
				flight.enter()
				defer flight.exit()
				time.Sleep(20 * time.Millisecond)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			specs := []string{"j1", "j2", "j3", "j4", "j5", "j6"}
			res, err := s.Run(context.Background(), schedTaskSet(specs))
			if err != nil {
				t.Fatalf("run failed: %v", err)
			}
			if len(res.Completed) != len(specs) {
				t.Fatalf("completed %d tasks, want %d", len(res.Completed), len(specs))
			}
			if peak := flight.maxInFlight(); peak > int64(tc.cap) {
				t.Errorf("peak in-flight %d exceeds Workers=%d", peak, tc.cap)
			} else if peak < tc.peakMin {
				t.Errorf("peak in-flight %d below expected minimum %d (no parallelism?)", peak, tc.peakMin)
			}
		})
	}
}

func TestSchedulerZeroConfigAppliesDefaults(t *testing.T) {
	flight := &schedFlight{}
	s, err := NewScheduler(Config{}, func(ctx context.Context, task Task) error {
		flight.enter()
		defer flight.exit()
		time.Sleep(20 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	specs := []string{"z1", "z2", "z3", "z4", "z5", "z6", "z7", "z8"}
	res, err := s.Run(context.Background(), schedTaskSet(specs))
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(res.Completed) != len(specs) {
		t.Fatalf("completed %d tasks, want %d", len(res.Completed), len(specs))
	}
	peak := flight.maxInFlight()
	if peak > int64(schedDefaultWorkers) {
		t.Errorf("zero config peaked at %d in-flight, want <= %d", peak, schedDefaultWorkers)
	}
	if peak < 2 {
		t.Errorf("zero config peaked at %d in-flight; default of %d workers did not parallelise", peak, schedDefaultWorkers)
	}
}

func TestSchedulerFailingTaskDoesNotStopWaveMates(t *testing.T) {
	log := &schedEventLog{}
	boom := errors.New("boom")
	s, err := NewScheduler(Config{}, func(ctx context.Context, task Task) error {
		log.add("start:%s", task.ID)
		if task.ID == "a" {
			return boom
		}
		log.add("end:%s", task.ID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Wave 0: a, b (independent). Wave 1: c depends on a, d depends on b.
	res, err := s.Run(context.Background(), schedTaskSet([]string{"a", "b", "c:a", "d:b"}))
	if err == nil {
		t.Fatal("expected an error naming the failed task")
	}
	if !strings.Contains(err.Error(), "a: boom") {
		t.Errorf("error %q does not name task a's failure", err)
	}
	if !strings.Contains(err.Error(), "c") {
		t.Errorf("error %q does not mention blocked task c", err)
	}
	completed := map[string]bool{}
	for _, id := range res.Completed {
		completed[id] = true
	}
	// Wave-mate b and downstream d still ran to completion.
	if !completed["b"] || !completed["d"] {
		t.Errorf("wave-mates b/d did not complete: %v", res.Completed)
	}
	if log.indexOf("start:a") < 0 || log.indexOf("start:b") < 0 {
		t.Errorf("wave-0 tasks did not both start: %v", log.snapshot())
	}
	if log.indexOf("start:c") >= 0 {
		t.Errorf("blocked task c must never start: %v", log.snapshot())
	}
	if len(res.Failed) != 1 || res.Failed[0].ID != "a" || !errors.Is(res.Failed[0].Err, boom) {
		t.Errorf("Failed = %+v, want [a: boom]", res.Failed)
	}
	if strings.Join(res.Blocked, ",") != "c" {
		t.Errorf("Blocked = %v, want [c]", res.Blocked)
	}
	if res.Failed[0].Attempts != 1 {
		t.Errorf("attempts = %d, want 1 (no retry hook)", res.Failed[0].Attempts)
	}
}

func TestSchedulerCancelDrainsWaveAndNamesRemaining(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := &schedEventLog{}
	var startedWaves int
	s, err := NewScheduler(Config{
		Workers: 2,
		OnWaveStart: func(wave int, ids []string) {
			startedWaves++
		},
	}, func(taskCtx context.Context, task Task) error {
		log.add("start:%s", task.ID)
		if task.ID == "b" {
			cancel() // cancel mid-wave
		}
		time.Sleep(5 * time.Millisecond)
		log.add("end:%s", task.ID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Wave 0: a, b, c, d. Wave 1: e (after a), f (after b).
	res, err := s.Run(ctx, schedTaskSet([]string{"a", "b", "c", "d", "e:a", "f:b"}))
	if err == nil {
		t.Fatal("expected a cancellation error")
	}
	if !strings.Contains(err.Error(), "context cancelled") || !strings.Contains(err.Error(), "never started: e, f") {
		t.Errorf("error %q does not name remaining tasks e and f", err)
	}
	// The in-flight wave drained: all four wave-0 tasks completed.
	completed := map[string]bool{}
	for _, id := range res.Completed {
		completed[id] = true
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if !completed[id] {
			t.Errorf("wave drained incompletely: %q missing from Completed %v", id, res.Completed)
		}
		if log.indexOf("end:"+id) < 0 {
			t.Errorf("task %q never finished (wave did not drain): %v", id, log.snapshot())
		}
	}
	// Later waves never started.
	if strings.Join(res.Remaining, ",") != "e,f" {
		t.Errorf("Remaining = %v, want [e f]", res.Remaining)
	}
	if log.indexOf("start:e") >= 0 || log.indexOf("start:f") >= 0 {
		t.Errorf("tasks after the cancelled wave still started: %v", log.snapshot())
	}
	if startedWaves != 1 || res.Waves != 1 {
		t.Errorf("waves started = callback %d / result %d, want 1", startedWaves, res.Waves)
	}
}

func TestSchedulerRetryHookCountsAttempts(t *testing.T) {
	var mu sync.Mutex
	runs := map[string]int{}
	var hookLog []string
	failures := map[string]int{"flaky": 1, "hopeless": 99}
	s, err := NewScheduler(Config{
		Workers: 2,
		Retry: func(taskID string, attempt int, err error) (time.Duration, bool) {
			mu.Lock()
			hookLog = append(hookLog, fmt.Sprintf("%s:%d", taskID, attempt))
			mu.Unlock()
			if attempt < 3 {
				return time.Millisecond, true
			}
			return 0, false
		},
	}, func(ctx context.Context, task Task) error {
		mu.Lock()
		runs[task.ID]++
		n := runs[task.ID]
		mu.Unlock()
		if n <= failures[task.ID] {
			return fmt.Errorf("attempt %d of %s failed", n, task.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Run(context.Background(), schedTaskSet([]string{"flaky", "hopeless"}))
	if err == nil {
		t.Fatal("expected an error for the hopeless task")
	}
	if !strings.Contains(err.Error(), "hopeless: attempt 3 of hopeless failed") {
		t.Errorf("error %q does not carry the last failure", err)
	}
	mu.Lock()
	hookCount := len(hookLog)
	mu.Unlock()
	if hookCount != 4 {
		t.Errorf("retry hook consulted %d times, want 4: %v", hookCount, hookLog)
	}
	var flakyHooks, hopelessHooks []string
	for _, h := range hookLog {
		switch {
		case strings.HasPrefix(h, "flaky:"):
			flakyHooks = append(flakyHooks, h)
		case strings.HasPrefix(h, "hopeless:"):
			hopelessHooks = append(hopelessHooks, h)
		}
	}
	if strings.Join(flakyHooks, ",") != "flaky:1" {
		t.Errorf("flaky hook calls = %v, want [flaky:1]", flakyHooks)
	}
	if strings.Join(hopelessHooks, ",") != "hopeless:1,hopeless:2,hopeless:3" {
		t.Errorf("hopeless hook calls = %v, want attempts 1..3", hopelessHooks)
	}
	if runs["flaky"] != 2 {
		t.Errorf("flaky ran %d times, want 2 (fail once, then succeed)", runs["flaky"])
	}
	if runs["hopeless"] != 3 {
		t.Errorf("hopeless ran %d times, want 3 (three allowed attempts)", runs["hopeless"])
	}
	completed := map[string]bool{}
	for _, id := range res.Completed {
		completed[id] = true
	}
	if !completed["flaky"] {
		t.Errorf("flaky should have completed after one retry: %v", res.Completed)
	}
	if len(res.Failed) != 1 || res.Failed[0].ID != "hopeless" || res.Failed[0].Attempts != 3 {
		t.Errorf("Failed = %+v, want [hopeless with 3 attempts]", res.Failed)
	}
}

func TestSchedulerRejectsInvalidGraphs(t *testing.T) {
	tests := []struct {
		name  string
		tasks []Task
		want  string
	}{
		{"dependency cycle", schedTaskSet([]string{"a:b", "b:a"}), "dependency cycle"},
		{"duplicate id", schedTaskSet([]string{"a", "a"}), `duplicate task id "a"`},
		{"unknown dependency", schedTaskSet([]string{"a:x"}), `unknown task "x"`},
		{"empty id", []Task{NewTask(" ", "blank")}, "task with empty id"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ran := false
			s, err := NewScheduler(Config{}, func(ctx context.Context, task Task) error {
				ran = true
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			res, err := s.Run(context.Background(), tc.tasks)
			if err == nil {
				t.Fatalf("expected an invalid-input error, got result %+v", res)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
			if ran {
				t.Error("work ran despite invalid input")
			}
		})
	}
}

func TestSchedulerHonoursArrivingStatuses(t *testing.T) {
	log := &schedEventLog{}
	done := NewTask("done", "already done")
	done.Status = StatusCompleted
	preFailed := NewTask("prefail", "failed earlier")
	preFailed.Status = StatusCancelled
	free := NewTask("free", "independent")
	next := NewTask("next", "after done")
	next.DependsOn = []string{"done"}
	after := NewTask("after", "after prefail")
	after.DependsOn = []string{"prefail"}
	s, err := NewScheduler(Config{Workers: 2}, func(ctx context.Context, task Task) error {
		log.add("start:%s", task.ID)
		log.add("end:%s", task.ID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.RunGraph(context.Background(), Graph{Tasks: []Task{done, preFailed, free, next, after}})
	if err == nil {
		t.Fatal("expected an error covering prefail and after")
	}
	if !strings.Contains(err.Error(), "prefail") || !strings.Contains(err.Error(), "after") {
		t.Errorf("error %q does not account for prefail/after", err)
	}
	completed := schedIDsByName(res.Completed)
	if !completed["done"] {
		t.Error("pre-completed task must be reported in Completed")
	}
	if !completed["next"] || !completed["free"] {
		t.Errorf("runnable tasks did not complete: %v", res.Completed)
	}
	if log.indexOf("start:done") >= 0 {
		t.Error("pre-completed task must not run again")
	}
	if log.indexOf("start:prefail") >= 0 {
		t.Error("pre-cancelled task must not run")
	}
	if log.indexOf("start:after") >= 0 {
		t.Error("task blocked by a pre-failed dependency must not run")
	}
	if len(res.Failed) != 1 || res.Failed[0].ID != "prefail" || res.Failed[0].Attempts != 0 {
		t.Errorf("Failed = %+v, want [prefail with 0 attempts]", res.Failed)
	}
	if strings.Join(res.Blocked, ",") != "after" {
		t.Errorf("Blocked = %v, want [after]", res.Blocked)
	}
	if len(res.Remaining) != 0 {
		t.Errorf("Remaining = %v, want none", res.Remaining)
	}
}

func TestSchedulerEmptyGraphRunsNothing(t *testing.T) {
	s, err := NewScheduler(Config{}, func(ctx context.Context, task Task) error {
		t.Error("work must not run for an empty graph")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Run(context.Background(), nil)
	if err != nil {
		t.Fatalf("empty graph failed: %v", err)
	}
	if res.Waves != 0 || len(res.Completed) != 0 {
		t.Errorf("result = %+v, want an empty result", res)
	}
}

func schedIDsByName(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}
