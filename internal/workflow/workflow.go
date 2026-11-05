// Package workflow is a small declarative DAG workflow engine. A Spec lists
// Steps with dependency edges; an Executor runs ready steps concurrently,
// passes completed dependency outputs forward as a deps map, retries failed
// attempts with a small backoff, and applies a per-step timeout. A failed or
// timed-out step causes all transitive dependents to be skipped, and
// cancelling the parent context stops scheduling with clear per-step
// statuses.
//
// The package depends on the standard library only and is intentionally
// decoupled from internal/execution (which inspired the cycle detection).
package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Func is the unit of work executed by a Step. deps maps the ID of each
// direct dependency to its output; every dependency in the map completed
// successfully, because dependents of unsuccessful steps are skipped.
type Func func(ctx context.Context, deps map[string]any) (any, error)

// Step is a single node of the workflow DAG.
type Step struct {
	ID        string
	DependsOn []string
	// Timeout bounds each attempt of the step; zero means no deadline.
	// An attempt that hits its deadline ends the step with StatusTimeout;
	// timeouts are not retried.
	Timeout time.Duration
	// Attempts is the number of tries for the whole step; 0 means 1. Failed
	// attempts are retried after a small backoff (wfBackoffDelay).
	Attempts int
	// Fn is the step body; a nil Fn is rejected by Validate.
	Fn Func
}

// Spec is a named workflow description.
type Spec struct {
	Name  string
	Steps []Step
}

// Status is the terminal outcome of one step.
type Status string

// The possible step outcomes. Skipped steps never ran; timeout steps hit
// their per-step deadline.
const (
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusSkipped   Status = "skipped"
	StatusTimeout   Status = "timeout"
)

// StepResult records the outcome of one step.
type StepResult struct {
	ID       string        `json:"id"`
	Status   Status        `json:"status"`
	Output   any           `json:"output,omitempty"`
	Err      error         `json:"-"`
	Duration time.Duration `json:"duration_ns"`
	Attempts int           `json:"attempts"`
}

// MarshalJSON serializes StepResult with human-readable error and duration in milliseconds.
func (sr StepResult) MarshalJSON() ([]byte, error) {
	type Alias StepResult
	errMsg := ""
	if sr.Err != nil {
		errMsg = sr.Err.Error()
	}
	return json.Marshal(&struct {
		Alias
		Error      string  `json:"error,omitempty"`
		DurationMs float64 `json:"duration_ms"`
	}{
		Alias:      Alias(sr),
		Error:      errMsg,
		DurationMs: float64(sr.Duration.Microseconds()) / 1000.0,
	})
}

// Result summarises a whole run. Run returns a Result for every valid spec,
// even when individual steps failed or were skipped; inspect the per-step
// statuses. The error return of Run is reserved for invalid specs.
type Result struct {
	Name     string                `json:"name"`
	Steps    map[string]StepResult `json:"steps"`
	Order    []string              `json:"order"`
	Outputs  map[string]any        `json:"outputs,omitempty"`
	Duration time.Duration         `json:"duration_ns"`
}

// Succeeded returns true when all steps in the result succeeded.
func (r Result) Succeeded() bool {
	if len(r.Steps) == 0 {
		return false
	}
	for _, sr := range r.Steps {
		if sr.Status != StatusSucceeded {
			return false
		}
	}
	return true
}

// MarshalJSON serializes Result with total duration in milliseconds and overall success.
func (r Result) MarshalJSON() ([]byte, error) {
	type Alias Result
	return json.Marshal(&struct {
		Alias
		DurationMs float64 `json:"duration_ms"`
		Succeeded  bool    `json:"succeeded"`
	}{
		Alias:      Alias(r),
		DurationMs: float64(r.Duration.Microseconds()) / 1000.0,
		Succeeded:  r.Succeeded(),
	})
}

// Validate reports structural problems with spec: empty or duplicate step
// IDs, nil Fn, dependencies on unknown steps and dependency cycles. All
// problems found are joined into one error; a cycle error names the actual
// path, for example "dependency cycle: a -> b -> a".
func Validate(spec Spec) error {
	var problems []error
	ids := make(map[string]bool, len(spec.Steps))
	for _, st := range spec.Steps {
		if st.ID == "" {
			problems = append(problems, errors.New("workflow: step with empty ID"))
			continue
		}
		if st.Fn == nil {
			problems = append(problems, fmt.Errorf("workflow: step %q has nil Fn", st.ID))
		}
		if ids[st.ID] {
			problems = append(problems, fmt.Errorf("workflow: duplicate step ID %q", st.ID))
		}
		ids[st.ID] = true
	}
	for _, st := range spec.Steps {
		for _, dep := range st.DependsOn {
			if !ids[dep] {
				problems = append(problems, fmt.Errorf("workflow: step %q depends on unknown step %q", st.ID, dep))
			}
		}
	}
	if cycle := wfFindCycle(spec.Steps); len(cycle) > 0 {
		problems = append(problems, fmt.Errorf("workflow: dependency cycle: %s", strings.Join(cycle, " -> ")))
	}
	return errors.Join(problems...)
}

// wfFindCycle returns one dependency cycle as a closed path of step IDs
// ("a", "b", "a"), following DependsOn edges, or nil when the DAG is
// acyclic. Steps with duplicate or empty IDs are ignored; Validate reports
// those separately.
func wfFindCycle(steps []Step) []string {
	byID := make(map[string]Step, len(steps))
	var roots []string
	seen := make(map[string]bool, len(steps))
	for _, st := range steps {
		if st.ID == "" || seen[st.ID] {
			continue
		}
		seen[st.ID] = true
		byID[st.ID] = st
		roots = append(roots, st.ID)
	}
	const (
		white = 0 // unvisited
		gray  = 1 // on the current DFS path
		black = 2 // finished
	)
	color := make(map[string]int, len(roots))
	var path []string
	var visit func(id string) []string
	visit = func(id string) []string {
		switch color[id] {
		case gray:
			// Back edge: cut the cycle out of the current path.
			for i, p := range path {
				if p == id {
					out := append([]string{}, path[i:]...)
					return append(out, id)
				}
			}
			return []string{id, id}
		case black:
			return nil
		}
		color[id] = gray
		path = append(path, id)
		deps := append([]string(nil), byID[id].DependsOn...)
		sort.Strings(deps)
		for _, dep := range deps {
			if _, ok := byID[dep]; !ok {
				continue
			}
			if cycle := visit(dep); cycle != nil {
				return cycle
			}
		}
		path = path[:len(path)-1]
		color[id] = black
		return nil
	}
	for _, id := range roots {
		if cycle := visit(id); cycle != nil {
			return cycle
		}
	}
	return nil
}

// wfDefaultParallelism is used when Executor.Parallelism is not positive.
const wfDefaultParallelism = 4

// Backoff between retry attempts grows from wfBackoffBase, doubling each
// retry, capped at wfBackoffMax.
const (
	wfBackoffBase = 10 * time.Millisecond
	wfBackoffMax  = 100 * time.Millisecond
)

// Executor runs Specs. The zero value is usable.
type Executor struct {
	// Parallelism bounds the number of steps whose Fn runs concurrently.
	// Values <= 0 mean wfDefaultParallelism.
	Parallelism int
}

// Run validates spec and executes its DAG. Steps whose dependencies have all
// succeeded run concurrently, bounded by Parallelism. Each Fn receives the
// outputs of its direct dependencies keyed by their step IDs. Failed steps
// are retried up to Attempts-1 times with a small backoff; a per-step
// timeout (Step.Timeout) ends the step with StatusTimeout. When a step does
// not succeed, all of its transitive dependents are skipped. Cancelling ctx
// stops scheduling: queued steps become skipped and running steps end with
// whatever their Fn returns (typically a context.Canceled failure). Run
// returns a Result for every valid spec; the error return is non-nil only
// when Validate rejects the spec.
func (e *Executor) Run(ctx context.Context, spec Spec) (*Result, error) {
	if err := Validate(spec); err != nil {
		return nil, err
	}
	par := e.Parallelism
	if par <= 0 {
		par = wfDefaultParallelism
	}

	start := time.Now()
	sched := wfNewScheduler(spec.Steps)

	workers := par
	if n := len(spec.Steps); n < workers {
		workers = n
	}
	sem := make(chan struct{}, par) // second cap on concurrent Fn calls
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range sched.ready {
				if !sched.claim(id) {
					continue // already settled (e.g. parent ctx cancelled)
				}
				st := sched.byID[id] // read-only after construction
				deps := sched.depsFor(st)
				sem <- struct{}{}
				res := e.wfRunStep(ctx, st, deps)
				<-sem
				sched.complete(id, res)
			}
		}()
	}

	// Stop scheduling as soon as the parent context is cancelled.
	watchDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			sched.cancelAll(ctx.Err())
		case <-watchDone:
		}
	}()
	wg.Wait()
	close(watchDone)

	return sched.result(spec, start), nil
}

// wfRunStep executes one step with retries, backoff and per-attempt timeout.
// A step attempt that exhausts its own deadline is classified as
// StatusTimeout and is not retried.
func (e *Executor) wfRunStep(ctx context.Context, st Step, deps map[string]any) StepResult {
	attempts := st.Attempts
	if attempts <= 0 {
		attempts = 1
	}
	start := time.Now()
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return StepResult{ID: st.ID, Status: StatusFailed, Err: err, Duration: time.Since(start), Attempts: attempt}
		}
		var stepCtx context.Context
		var cancel context.CancelFunc
		if st.Timeout > 0 {
			stepCtx, cancel = context.WithTimeout(ctx, st.Timeout)
		} else {
			stepCtx, cancel = context.WithCancel(ctx)
		}
		out, err := wfSafeFn(stepCtx, st, deps)
		cancel()
		if err == nil {
			return StepResult{ID: st.ID, Status: StatusSucceeded, Output: out, Duration: time.Since(start), Attempts: attempt}
		}
		lastErr = err
		parentDead := ctx.Err() != nil
		stepTimedOut := !parentDead && (errors.Is(err, context.DeadlineExceeded) || stepCtx.Err() == context.DeadlineExceeded)
		if stepTimedOut {
			return StepResult{ID: st.ID, Status: StatusTimeout, Err: lastErr, Duration: time.Since(start), Attempts: attempt}
		}
		if parentDead || attempt == attempts {
			return StepResult{ID: st.ID, Status: StatusFailed, Err: lastErr, Duration: time.Since(start), Attempts: attempt}
		}
		// Small backoff before the next attempt; bail out early if the
		// parent context dies while waiting.
		timer := time.NewTimer(wfBackoffDelay(attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return StepResult{ID: st.ID, Status: StatusFailed, Err: ctx.Err(), Duration: time.Since(start), Attempts: attempt}
		case <-timer.C:
		}
	}
	return StepResult{ID: st.ID, Status: StatusFailed, Err: lastErr, Duration: time.Since(start), Attempts: attempts}
}

// wfSafeFn runs st.Fn, converting a panic into an error so one bad step
// cannot take down a worker and wedge the whole run.
func wfSafeFn(ctx context.Context, st Step, deps map[string]any) (out any, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("workflow: step %q panicked: %v", st.ID, r)
		}
	}()
	return st.Fn(ctx, deps)
}

// wfBackoffDelay returns the wait before the retry following attempt
// (1-based): wfBackoffBase, 2x, 4x, ... capped at wfBackoffMax.
func wfBackoffDelay(attempt int) time.Duration {
	d := wfBackoffBase
	for i := 1; i < attempt && d < wfBackoffMax; i++ {
		d *= 2
	}
	if d > wfBackoffMax {
		d = wfBackoffMax
	}
	return d
}

// wfScheduler coordinates the DAG at run time. All mutable state is guarded
// by mu; byID and order are read-only after wfNewScheduler returns. The
// ready channel is buffered with capacity len(steps), so every send is
// non-blocking; it is closed exactly once, when the last step becomes
// terminal.
type wfScheduler struct {
	mu         sync.Mutex
	ready      chan string
	byID       map[string]Step
	dependents map[string][]string // deduplicated reverse edges
	remaining  map[string]int      // unsatisfied direct dependency count
	running    map[string]bool     // claimed, Fn in flight
	terminal   map[string]StepResult
	order      []string
	pending    int // steps not yet terminal
	closed     bool
	stopping   bool // parent context cancelled; no new enqueues
}

func wfNewScheduler(steps []Step) *wfScheduler {
	s := &wfScheduler{
		ready:      make(chan string, len(steps)),
		byID:       make(map[string]Step, len(steps)),
		dependents: make(map[string][]string, len(steps)),
		remaining:  make(map[string]int, len(steps)),
		running:    make(map[string]bool, len(steps)),
		terminal:   make(map[string]StepResult, len(steps)),
		pending:    len(steps),
	}
	for _, st := range steps {
		s.byID[st.ID] = st
		s.order = append(s.order, st.ID)
	}
	for _, st := range steps {
		seen := make(map[string]bool, len(st.DependsOn))
		for _, dep := range st.DependsOn {
			if seen[dep] {
				continue // tolerate duplicate dependency entries
			}
			seen[dep] = true
			s.dependents[dep] = append(s.dependents[dep], st.ID)
			s.remaining[st.ID]++
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.order {
		if s.remaining[id] == 0 {
			s.enqueueLocked(id)
		}
	}
	s.maybeCloseLocked()
	return s
}

// claim marks id as running and reports whether the worker may execute it.
// It returns false when the scheduler already settled the step (parent ctx
// cancelled).
func (s *wfScheduler) claim(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if _, done := s.terminal[id]; done {
		return false
	}
	if s.running[id] {
		return false
	}
	s.running[id] = true
	return true
}

// depsFor builds the deps map for a claimed step: outputs of its direct
// dependencies, keyed by dependency ID. All of them succeeded by
// construction.
func (s *wfScheduler) depsFor(st Step) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	deps := make(map[string]any, len(st.DependsOn))
	for _, dep := range st.DependsOn {
		deps[dep] = s.terminal[dep].Output
	}
	return deps
}

// complete records the outcome of a claimed step and releases its
// dependents: successful steps may enqueue dependents whose remaining count
// reaches zero; unsuccessful steps doom all transitive dependents.
func (s *wfScheduler) complete(id string, res StepResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, id)
	if _, done := s.terminal[id]; done {
		return // keep the first verdict
	}
	s.terminal[id] = res
	s.pending--
	if res.Status == StatusSucceeded {
		for _, d := range s.dependents[id] {
			if _, done := s.terminal[d]; done {
				continue
			}
			s.remaining[d]--
			if s.remaining[d] == 0 {
				s.enqueueLocked(d)
			}
		}
	} else {
		s.doomFromLocked(id)
	}
	s.maybeCloseLocked()
}

// doomFromLocked marks every not-yet-terminal transitive dependent of
// failedID as skipped. Such steps are never running: a step only starts once
// all of its direct dependencies succeeded, so everything downstream of a
// failure is still queued.
func (s *wfScheduler) doomFromLocked(failedID string) {
	queue := append([]string(nil), s.dependents[failedID]...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if _, done := s.terminal[id]; done {
			continue
		}
		s.terminal[id] = StepResult{
			ID:     id,
			Status: StatusSkipped,
			Err:    fmt.Errorf("workflow: skipped because dependency %q finished with status %q", failedID, s.terminal[failedID].Status),
		}
		s.pending--
		queue = append(queue, s.dependents[id]...)
	}
}

// cancelAll settles the run after parent ctx cancellation: every step that
// is not running and not yet terminal becomes skipped. Running steps are
// left to their Fn, which observes the cancelled context.
func (s *wfScheduler) cancelAll(reason error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return // everything is already terminal
	}
	s.stopping = true
	for _, id := range s.order {
		if _, done := s.terminal[id]; done {
			continue
		}
		if s.running[id] {
			continue
		}
		s.terminal[id] = StepResult{ID: id, Status: StatusSkipped, Err: reason}
		s.pending--
	}
	s.maybeCloseLocked()
}

// enqueueLocked queues a ready step. Sends never block: the channel is
// buffered with len(steps) and each step is enqueued at most once.
func (s *wfScheduler) enqueueLocked(id string) {
	if s.closed || s.stopping {
		return
	}
	s.ready <- id
}

// maybeCloseLocked closes the ready channel exactly once, when no step is
// left to settle. Workers then drain and exit, ending the run.
func (s *wfScheduler) maybeCloseLocked() {
	if !s.closed && s.pending == 0 {
		close(s.ready)
		s.closed = true
	}
}

// result assembles the final Result after all workers have exited.
func (s *wfScheduler) result(spec Spec, start time.Time) *Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := time.Since(start)
	if d <= 0 {
		d = time.Nanosecond
	}
	res := &Result{
		Name:     spec.Name,
		Steps:    make(map[string]StepResult, len(s.order)),
		Order:    append([]string(nil), s.order...),
		Outputs:  make(map[string]any, len(s.terminal)),
		Duration: d,
	}
	for _, id := range s.order {
		sr := s.terminal[id]
		res.Steps[id] = sr
		if sr.Status == StatusSucceeded {
			res.Outputs[id] = sr.Output
		}
	}
	return res
}
