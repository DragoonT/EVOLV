package execution

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ResumePlan is the result of analysing a dead or interrupted run so the
// engine can pick up where it died. Every task in the run lands in exactly
// one bucket:
//
//	Skip     completed tasks whose results are kept as-is
//	Requeue  tasks to hand back to the scheduler, in dependency order:
//	         interrupted mid-flight work (queued/planning/running/verifying)
//	         and failed or cancelled work worth another attempt. A task only
//	         appears after its dependencies.
//	Inspect  waiting/paused tasks: a human or an external event must decide
//	         what happens next before they can move
//	Blocked  tasks that cannot be requeued automatically because a
//	         dependency is parked in Inspect (or transitively Blocked);
//	         resolving the dependency unblocks them
type ResumePlan struct {
	RunID    string
	Requeue  []string
	Skip     []string
	Inspect  []string
	Blocked  []string
	NRequeue int
	NSkip    int
	NInspect int
	NBlocked int
	LastSeq  int64    // last journal sequence observed for the run
	Notes    []string // journal-derived observations for the operator
}

// String renders a one-line summary: bucket counts, member ids for non-empty
// buckets, and the journal sequence the plan was built from.
func (p *ResumePlan) String() string {
	if p == nil {
		return "resume plan <nil>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "resume plan %s: journal-seq=%d", p.RunID, p.LastSeq)
	fmt.Fprintf(&b, " requeue=%d", p.NRequeue)
	if p.NRequeue > 0 {
		fmt.Fprintf(&b, " [%s]", strings.Join(p.Requeue, " "))
	}
	fmt.Fprintf(&b, " skip=%d", p.NSkip)
	if p.NSkip > 0 {
		fmt.Fprintf(&b, " [%s]", strings.Join(p.Skip, " "))
	}
	fmt.Fprintf(&b, " inspect=%d", p.NInspect)
	if p.NInspect > 0 {
		fmt.Fprintf(&b, " [%s]", strings.Join(p.Inspect, " "))
	}
	fmt.Fprintf(&b, " blocked=%d", p.NBlocked)
	if p.NBlocked > 0 {
		fmt.Fprintf(&b, " [%s]", strings.Join(p.Blocked, " "))
	}
	return b.String()
}

// PlanResume classifies every task of runID so a crashed run can be resumed.
// It reads the live task states and the run's journal tail; the journal only
// informs the plan's notes, classification always comes from task state.
func PlanResume(s *Store, runID string) (*ResumePlan, error) {
	if s == nil {
		return nil, fmt.Errorf("resume: nil store")
	}
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("resume: run id is required")
	}
	run, ok := s.Get(runID)
	if !ok {
		return nil, fmt.Errorf("run %q not found", runID)
	}
	plan := &ResumePlan{RunID: runID}
	if events, err := s.Events(runID); err == nil {
		resumJournalNotes(events, plan)
	} else {
		// The journal is evidence, not a requirement: a run whose journal
		// cannot be re-read still gets a usable plan from task states.
		plan.Notes = append(plan.Notes, fmt.Sprintf("journal unreadable: %v", err))
	}
	plan.Requeue, plan.Blocked, plan.Skip, plan.Inspect = resumClassify(run.Tasks)
	plan.NRequeue, plan.NSkip, plan.NInspect, plan.NBlocked =
		len(plan.Requeue), len(plan.Skip), len(plan.Inspect), len(plan.Blocked)
	for _, id := range plan.Requeue {
		if t, ok := run.Graph().Get(id); ok && t.LastError != "" {
			plan.Notes = append(plan.Notes, fmt.Sprintf("requeue %s: last error: %s", id, resumTrimError(t.LastError)))
		}
	}
	if run.Status.Active() {
		plan.Notes = append(plan.Notes, "run is still active; mid-flight tasks are planned as interrupted")
	}
	return plan, nil
}

// resumClassify assigns every task to one of the four resume buckets. Skip
// and Inspect are decided by status alone; the rest are requeued when all
// their dependencies are completed or already requeued, and blocked when a
// dependency is parked elsewhere (Inspect, or itself Blocked). The requeue
// list is dependency-ordered with a stable Kahn pass, so equal-priority work
// keeps its original (creation) order.
func resumClassify(tasks []Task) (requeue, blocked, skip, inspect []string) {
	byID := make(map[string]Task, len(tasks))
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
		ids = append(ids, t.ID)
	}
	completed := map[string]bool{}
	decided := map[string]bool{} // placed in Skip or Inspect
	for _, id := range ids {
		switch byID[id].Status {
		case StatusCompleted:
			skip = append(skip, id)
			completed[id] = true
			decided[id] = true
		case StatusWaiting, StatusPaused:
			inspect = append(inspect, id)
			decided[id] = true
		}
	}
	requeued := map[string]bool{}
	for {
		progress := false
		for _, id := range ids {
			if decided[id] || requeued[id] {
				continue
			}
			if resumDepsSatisfied(byID[id].DependsOn, byID, completed, requeued) {
				requeue = append(requeue, id)
				requeued[id] = true
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	for _, id := range ids {
		if !decided[id] && !requeued[id] {
			// Nothing left to satisfy its dependencies: either a dependency
			// is parked in Inspect, or the remaining graph is cyclic (cycles
			// are rejected at CreateRun, so this is defensive).
			blocked = append(blocked, id)
		}
	}
	return requeue, blocked, skip, inspect
}

// resumDepsSatisfied reports whether every dependency of t is completed or
// already queued for re-run. Unknown (dangling) dependencies are treated as
// satisfied, matching Graph.Ready's policy.
func resumDepsSatisfied(deps []string, byID map[string]Task, completed, requeued map[string]bool) bool {
	for _, dep := range deps {
		if _, ok := byID[dep]; !ok {
			continue
		}
		if !completed[dep] && !requeued[dep] {
			return false
		}
	}
	return true
}

// resumJournalNotes folds the journal tail into the plan: the last sequence
// number and, when present, how the run was last closed.
func resumJournalNotes(events []Event, plan *ResumePlan) {
	if len(events) == 0 {
		return
	}
	last := events[len(events)-1]
	plan.LastSeq = last.Seq
	plan.Notes = append(plan.Notes, fmt.Sprintf("journal: %d events through seq %d (last %s)",
		len(events), last.Seq, last.Kind))
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind != KindRunClosed {
			continue
		}
		var data struct {
			Status Status `json:"status"`
		}
		_ = json.Unmarshal(events[i].Data, &data)
		plan.Notes = append(plan.Notes, fmt.Sprintf("run closed as %s at %s", data.Status, events[i].At))
		return
	}
}

// resumTrimError keeps an error note readable.
func resumTrimError(msg string) string {
	msg = strings.TrimSpace(msg)
	if len(msg) <= 200 {
		return msg
	}
	return msg[:197] + "..."
}

// ApplyResume performs the requeues a plan asks for, using only the store's
// public mutation API. Failed and cancelled tasks go through Store.Retry
// (which also reopens a closed run) and are applied first, so later
// transitions never land inside a closed run; every other requeued task goes
// through Store.Transition to queued. Each applied requeue bumps the task's
// attempt counter via Store.RecordAttempt with an empty reason, so the task's
// real LastError is preserved. Already-queued tasks are left untouched.
//
// ApplyResume stops at the first store error and returns, in plan order, the
// plan ids that are queued when it returns — applied or already queued. On
// error the slice is best effort: it reflects the state actually reached.
func ApplyResume(s *Store, plan *ResumePlan) ([]string, error) {
	if s == nil {
		return nil, fmt.Errorf("resume: nil store")
	}
	if plan == nil {
		return nil, fmt.Errorf("resume: nil plan")
	}
	var firstErr error
	// Pass one: failed and cancelled tasks, because Store.Retry reopens a
	// terminal run before anything else is requeued.
	for _, id := range plan.Requeue {
		if firstErr != nil {
			break
		}
		run, ok := s.Get(plan.RunID)
		if !ok {
			firstErr = fmt.Errorf("run %q not found", plan.RunID)
			break
		}
		t, ok := run.Graph().Get(id)
		if !ok {
			firstErr = fmt.Errorf("task %q not found", id)
			break
		}
		if t.Status != StatusFailed && t.Status != StatusCancelled {
			continue
		}
		if _, err := s.Retry(plan.RunID, id); err != nil {
			firstErr = err
			break
		}
		if err := s.RecordAttempt(plan.RunID, id, ""); err != nil {
			firstErr = err
			break
		}
	}
	// Pass two: interrupted mid-flight tasks.
	for _, id := range plan.Requeue {
		if firstErr != nil {
			break
		}
		run, ok := s.Get(plan.RunID)
		if !ok {
			firstErr = fmt.Errorf("run %q not found", plan.RunID)
			break
		}
		t, ok := run.Graph().Get(id)
		if !ok {
			firstErr = fmt.Errorf("task %q not found", id)
			break
		}
		if t.Status == StatusQueued {
			continue // already queued, nothing to do
		}
		if _, err := s.Transition(plan.RunID, id, StatusQueued); err != nil {
			firstErr = err
			break
		}
		if err := s.RecordAttempt(plan.RunID, id, ""); err != nil {
			firstErr = err
			break
		}
	}
	// Report which plan ids are queued now, in plan order.
	run, ok := s.Get(plan.RunID)
	if !ok {
		if firstErr == nil {
			firstErr = fmt.Errorf("run %q not found", plan.RunID)
		}
		return nil, firstErr
	}
	var out []string
	for _, id := range plan.Requeue {
		if t, ok := run.Graph().Get(id); ok && t.Status == StatusQueued {
			out = append(out, id)
		}
	}
	return out, firstErr
}
