package execution

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Wave-parallel scheduling: the execution half of the workflow engine.
//
// The scheduler takes a task graph and runs it with the wave semantics of
// Schedule: wave 0 holds every task whose dependencies are already satisfied,
// wave 1 everything unblocked once wave 0 lands, and so on. Tasks inside one
// wave have no dependency edges between them, so they run concurrently,
// bounded by Config.Workers; waves themselves run in strict sequence.
//
// The scheduler is storage-agnostic: it never touches Store or Journal. The
// caller supplies the work function that performs one task and journals task
// transitions from inside it, so a crashed process can still recover through
// the normal replay path.

// schedDefaultWorkers is the within-wave concurrency used when Config.Workers
// is zero or negative.
const schedDefaultWorkers = 4

// SchedRetryFunc decides what happens after a task attempt failed. It is
// called with the 1-based attempt number that just failed (attempt 1 is the
// first run of the task). Returning false gives up: the task is recorded as
// failed with the last error. Returning true schedules one more attempt after
// the returned delay; a zero delay retries immediately. The hook runs on the
// task's worker goroutine, so it must not block for long and must not call
// Scheduler.Run re-entrantly.
type SchedRetryFunc func(taskID string, attempt int, err error) (time.Duration, bool)

// Config controls one scheduler.
type Config struct {
	// Workers caps how many tasks of the same wave run at once. Zero or
	// negative means schedDefaultWorkers.
	Workers int
	// Retry, when set, is consulted after every failed attempt.
	Retry SchedRetryFunc
	// OnTaskDone fires when a task (or its retry chain) succeeded. It runs on
	// the task's worker goroutine.
	OnTaskDone func(taskID string)
	// OnTaskError fires when a task is recorded as failed for good: retries
	// exhausted, retry declined, or a retry abandoned because the context
	// ended. It runs on the task's worker goroutine.
	OnTaskError func(taskID string, err error)
	// OnWaveStart fires on the Run goroutine before a wave's tasks are
	// launched, with the 0-based wave index and its task ids in schedule
	// order. A wave skipped because the context is already cancelled does not
	// fire it.
	OnWaveStart func(wave int, taskIDs []string)
}

// Scheduler executes a task DAG wave by wave. Create one with NewScheduler
// and run a graph with Run. A Scheduler is stateless between runs and safe
// to reuse, but each Run should receive its own snapshot of the graph.
type Scheduler struct {
	cfg  Config
	work func(ctx context.Context, task Task) error
}

// NewScheduler builds a scheduler around the function that actually performs
// one task. The work function receives the task snapshot and the scheduler's
// context; returning nil completes the task, anything else is a failure that
// goes through Config.Retry.
func NewScheduler(cfg Config, work func(ctx context.Context, task Task) error) (*Scheduler, error) {
	if work == nil {
		return nil, errors.New("scheduler needs a non-nil work function")
	}
	return &Scheduler{cfg: cfg, work: work}, nil
}

// SchedulerTaskError is one task's terminal failure.
type SchedulerTaskError struct {
	ID  string
	Err error
	// Attempts counts the attempts the scheduler made, 1-based. It is 0 for
	// tasks that arrived already failed or cancelled.
	Attempts int
}

// SchedulerResult accounts for every task handed to a Run: each input task
// appears in exactly one of Completed, Failed, Blocked or Remaining.
type SchedulerResult struct {
	// Completed lists ids that finished successfully, sorted. It includes
	// tasks that arrived as StatusCompleted and were not run again.
	Completed []string
	// Failed lists tasks that ended in failure, sorted by id.
	Failed []SchedulerTaskError
	// Blocked lists ids that never ran because a dependency failed, sorted.
	Blocked []string
	// Remaining lists runnable ids the scheduler never started because the
	// context ended first, sorted.
	Remaining []string
	// Waves is the number of waves that were started.
	Waves int
}

// Run executes the tasks wave by wave until every task reached an outcome or
// the context ends.
//
// Structural problems (empty or duplicate ids, unknown dependencies,
// dependency cycles) are reported as a single error before anything runs.
// When the context is cancelled, the wave in flight drains to completion and
// no further wave starts; the returned error then names every task that never
// started. A failing task never stops its wave-mates — it only blocks its
// dependents, which are reported in SchedulerResult.Blocked.
func (s *Scheduler) Run(ctx context.Context, tasks []Task) (SchedulerResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	st, err := schedNewRun(s, tasks)
	if err != nil {
		return SchedulerResult{}, err
	}
	return st.execute(ctx)
}

// RunGraph is Run for a Graph built elsewhere.
func (s *Scheduler) RunGraph(ctx context.Context, g Graph) (SchedulerResult, error) {
	return s.Run(ctx, g.Tasks)
}

// schedRunState is the per-run bookkeeping for one Scheduler.Run call.
type schedRunState struct {
	cfg     Config
	work    func(ctx context.Context, task Task) error
	workers int

	// pending holds non-terminal tasks as they arrived; runnable is the
	// subset handed to Schedule with dependencies stripped to tasks that
	// will actually run. deps maps a runnable task id to its runnable
	// dependencies only (dependencies already completed at input are gone).
	pending  []Task
	runnable []Task
	deps     map[string][]string

	mu       sync.Mutex
	done     map[string]bool
	failed   map[string]error
	blocked  map[string]bool
	attempts map[string]int
}

// schedNewRun validates the input, classifies every task and computes the
// blocked closure before any work starts.
func schedNewRun(s *Scheduler, tasks []Task) (*schedRunState, error) {
	if problems := schedValidateTasks(tasks); len(problems) > 0 {
		return nil, errors.New("scheduler input invalid: " + strings.Join(problems, "; "))
	}
	st := &schedRunState{
		cfg:      s.cfg,
		work:     s.work,
		workers:  s.cfg.Workers,
		done:     map[string]bool{},
		failed:   map[string]error{},
		blocked:  map[string]bool{},
		attempts: map[string]int{},
		deps:     map[string][]string{},
	}
	if st.workers <= 0 {
		st.workers = schedDefaultWorkers
	}
	for _, t := range tasks {
		switch t.Status {
		case StatusCompleted:
			st.done[t.ID] = true
		case StatusFailed, StatusCancelled:
			st.failed[t.ID] = fmt.Errorf("task was already %s before the scheduler started", t.Status)
		default:
			st.pending = append(st.pending, t)
		}
	}
	// Fixpoint: a task whose dependency failed or was blocked can never run,
	// and that blocking propagates down the whole dependent chain.
	for {
		changed := false
		for _, t := range st.pending {
			if st.blocked[t.ID] {
				continue
			}
			for _, dep := range t.DependsOn {
				_, depFailed := st.failed[dep]
				if depFailed || st.blocked[dep] {
					st.blocked[t.ID] = true
					changed = true
					break
				}
			}
		}
		if !changed {
			break
		}
	}
	inRun := map[string]bool{}
	for _, t := range st.pending {
		if !st.blocked[t.ID] {
			inRun[t.ID] = true
		}
	}
	for _, t := range st.pending {
		if !inRun[t.ID] {
			continue
		}
		rt := t
		rt.DependsOn = nil
		for _, dep := range t.DependsOn {
			if inRun[dep] {
				rt.DependsOn = append(rt.DependsOn, dep)
			}
		}
		st.runnable = append(st.runnable, rt)
		st.deps[t.ID] = rt.DependsOn
	}
	return st, nil
}

// schedValidateTasks reports every structural problem at once, in a
// deterministic order: empty or duplicate ids, unknown dependencies and
// dependency cycles.
func schedValidateTasks(tasks []Task) []string {
	var problems []string
	ids := make(map[string]int, len(tasks))
	for _, t := range tasks {
		if strings.TrimSpace(t.ID) == "" {
			problems = append(problems, "task with empty id")
			continue
		}
		ids[t.ID]++
	}
	var dups []string
	for id, n := range ids {
		if n > 1 {
			dups = append(dups, id)
		}
	}
	sort.Strings(dups)
	for _, id := range dups {
		problems = append(problems, fmt.Sprintf("duplicate task id %q", id))
	}
	for _, t := range tasks {
		if strings.TrimSpace(t.ID) == "" {
			continue
		}
		for _, dep := range t.DependsOn {
			if _, ok := ids[dep]; !ok {
				problems = append(problems, fmt.Sprintf("task %q depends on unknown task %q", t.ID, dep))
			}
		}
	}
	if cycle := (Graph{Tasks: tasks}).Cycle(); len(cycle) > 0 {
		problems = append(problems, "dependency cycle: "+strings.Join(cycle, " → "))
	}
	sort.Strings(problems)
	return problems
}

// execute runs wave after wave and assembles the outcome.
func (st *schedRunState) execute(ctx context.Context) (SchedulerResult, error) {
	waves := Schedule(Graph{Tasks: st.runnable})
	byID := make(map[string]Task, len(st.runnable))
	for _, t := range st.runnable {
		byID[t.ID] = t
	}
	wavesStarted := 0
	for i, wave := range waves {
		if ctx.Err() != nil {
			break // the wave in flight was drained by runWave; stop here
		}
		if st.cfg.OnWaveStart != nil {
			st.cfg.OnWaveStart(i, append([]string(nil), wave...))
		}
		wavesStarted++
		st.runWave(ctx, wave, byID)
	}

	st.mu.Lock()
	res := SchedulerResult{Waves: wavesStarted}
	for id := range st.done {
		res.Completed = append(res.Completed, id)
	}
	for id, err := range st.failed {
		res.Failed = append(res.Failed, SchedulerTaskError{ID: id, Err: err, Attempts: st.attempts[id]})
	}
	for id := range st.blocked {
		res.Blocked = append(res.Blocked, id)
	}
	for _, t := range st.runnable {
		if st.done[t.ID] {
			continue
		}
		if _, isFailed := st.failed[t.ID]; isFailed {
			continue
		}
		res.Remaining = append(res.Remaining, t.ID)
	}
	st.mu.Unlock()
	sort.Strings(res.Completed)
	sort.Slice(res.Failed, func(i, j int) bool { return res.Failed[i].ID < res.Failed[j].ID })
	sort.Strings(res.Blocked)
	sort.Strings(res.Remaining)
	return res, schedOutcomeError(res)
}

// schedOutcomeError turns an incomplete outcome into one readable error:
// failures first, then cancellation with the never-started tasks, then
// blocked tasks. It returns nil for a fully successful run.
func schedOutcomeError(res SchedulerResult) error {
	var parts []string
	if len(res.Failed) > 0 {
		msgs := make([]string, 0, len(res.Failed))
		for _, fe := range res.Failed {
			msgs = append(msgs, fmt.Sprintf("%s: %v", fe.ID, fe.Err))
		}
		parts = append(parts, fmt.Sprintf("%d task(s) failed: %s", len(res.Failed), strings.Join(msgs, "; ")))
	}
	if len(res.Remaining) > 0 {
		parts = append(parts, fmt.Sprintf("context cancelled; %d task(s) never started: %s",
			len(res.Remaining), strings.Join(res.Remaining, ", ")))
	}
	if len(res.Blocked) > 0 {
		parts = append(parts, fmt.Sprintf("%d task(s) blocked by failed dependencies: %s",
			len(res.Blocked), strings.Join(res.Blocked, ", ")))
	}
	if len(parts) == 0 {
		return nil
	}
	return errors.New(strings.Join(parts, "; "))
}

// runWave executes one wave with at most st.workers tasks in flight. Every
// task of the wave runs even when the context was cancelled while the wave
// was launching: the wave drains, later waves do not start.
func (st *schedRunState) runWave(ctx context.Context, wave []string, byID map[string]Task) {
	var wg sync.WaitGroup
	queue := make(chan int)
	for i := 0; i < st.workers && i < len(wave); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pos := range queue {
				st.execTask(ctx, byID[wave[pos]])
			}
		}()
	}
	for pos := range wave {
		queue <- pos
	}
	close(queue)
	wg.Wait()
}

// execTask runs one task, honouring Config.Retry. A task whose dependencies
// did not all complete is recorded as blocked and never reaches the work
// function.
func (st *schedRunState) execTask(ctx context.Context, t Task) {
	for _, dep := range st.deps[t.ID] {
		st.mu.Lock()
		depDone := st.done[dep]
		st.mu.Unlock()
		if !depDone {
			st.mu.Lock()
			st.blocked[t.ID] = true
			st.mu.Unlock()
			return
		}
	}
	attempt := 1
	for {
		st.mu.Lock()
		st.attempts[t.ID] = attempt
		st.mu.Unlock()
		err := st.work(ctx, t)
		if err == nil {
			st.mu.Lock()
			st.done[t.ID] = true
			onDone := st.cfg.OnTaskDone
			st.mu.Unlock()
			if onDone != nil {
				onDone(t.ID)
			}
			return
		}
		if st.cfg.Retry != nil {
			if delay, again := st.cfg.Retry(t.ID, attempt, err); again {
				if !schedWait(ctx, delay) {
					st.markFailed(t.ID, fmt.Errorf("%v (attempt %d retry abandoned: %v)", err, attempt, ctx.Err()))
					return
				}
				attempt++
				continue
			}
		}
		st.markFailed(t.ID, err)
		return
	}
}

// schedWait blocks for delay, returning false when the context ends first.
func schedWait(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// markFailed records a terminal failure and fires OnTaskError.
func (st *schedRunState) markFailed(id string, err error) {
	st.mu.Lock()
	st.failed[id] = err
	onErr := st.cfg.OnTaskError
	st.mu.Unlock()
	if onErr != nil {
		onErr(id, err)
	}
}
