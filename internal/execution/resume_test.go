package execution

import (
	"fmt"
	"strings"
	"testing"
)

func resumTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.CloseAll() })
	return s
}

func resumSeedRun(t *testing.T, s *Store, id string, tasks []Task) {
	t.Helper()
	if _, err := s.CreateRun(id, "resume test run", "pick up where it died", tasks); err != nil {
		t.Fatalf("create run %s: %v", id, err)
	}
}

func resumMove(t *testing.T, s *Store, runID, taskID string, to Status) {
	t.Helper()
	if _, err := s.Transition(runID, taskID, to); err != nil {
		t.Fatalf("transition %s → %s: %v", taskID, to, err)
	}
}

// resumFinish drives queued → running → verifying → completed.
func resumFinish(t *testing.T, s *Store, runID, taskID string) {
	t.Helper()
	resumMove(t, s, runID, taskID, StatusRunning)
	resumMove(t, s, runID, taskID, StatusVerifying)
	resumMove(t, s, runID, taskID, StatusCompleted)
}

// resumStamp pins a fixed-width timestamp so run.Tasks keeps creation order:
// the store sorts tasks by the CreatedAt string, and RFC3339Nano's trimmed
// fractions make fast wall-clock stamps compare non-chronologically.
func resumStamp(n int) string {
	return fmt.Sprintf("2036-01-01T00:00:00.%09dZ", n)
}

// resumTask builds a queued task with an explicit, order-stable timestamp.
func resumTask(id, title string, deps []string, n int) Task {
	return Task{
		ID:        id,
		Title:     title,
		Status:    StatusQueued,
		DependsOn: deps,
		CreatedAt: resumStamp(n),
		UpdatedAt: resumStamp(n),
	}
}

func resumIndexOf(ids []string, want string) int {
	for i, id := range ids {
		if id == want {
			return i
		}
	}
	return -1
}

func TestResumePlanClassifiesMixedStatus(t *testing.T) {
	s := resumTestStore(t)
	resumSeedRun(t, s, "run-mixed", []Task{
		resumTask("done1", "already shipped", nil, 1),
		resumTask("done2", "also shipped", nil, 2),
		resumTask("mid", "died mid-flight", []string{"done1"}, 3),
		resumTask("fail1", "exploded", nil, 4),
		resumTask("after-fail", "waits on fail1", []string{"fail1"}, 5),
		resumTask("wait1", "needs an external event", nil, 6),
		resumTask("after-wait", "waits on wait1", []string{"wait1"}, 7),
		resumTask("gone1", "cancelled earlier", nil, 8),
	})
	resumFinish(t, s, "run-mixed", "done1")
	resumFinish(t, s, "run-mixed", "done2")
	resumMove(t, s, "run-mixed", "mid", StatusRunning)
	resumMove(t, s, "run-mixed", "fail1", StatusFailed)
	// waiting is only reachable from planning or running, not from queued.
	resumMove(t, s, "run-mixed", "wait1", StatusRunning)
	resumMove(t, s, "run-mixed", "wait1", StatusWaiting)
	resumMove(t, s, "run-mixed", "gone1", StatusCancelled)

	plan, err := PlanResume(s, "run-mixed")
	if err != nil {
		t.Fatalf("PlanResume: %v", err)
	}
	wantRequeue := []string{"mid", "fail1", "after-fail", "gone1"}
	if got := strings.Join(plan.Requeue, ","); got != strings.Join(wantRequeue, ",") {
		t.Errorf("requeue = %v, want %v", plan.Requeue, wantRequeue)
	}
	wantSkip := []string{"done1", "done2"}
	if got := strings.Join(plan.Skip, ","); got != strings.Join(wantSkip, ",") {
		t.Errorf("skip = %v, want %v", plan.Skip, wantSkip)
	}
	if got := strings.Join(plan.Inspect, ","); got != "wait1" {
		t.Errorf("inspect = %v, want [wait1]", plan.Inspect)
	}
	if got := strings.Join(plan.Blocked, ","); got != "after-wait" {
		t.Errorf("blocked = %v, want [after-wait] (dependency is parked in Inspect)", plan.Blocked)
	}
	if plan.NRequeue != 4 || plan.NSkip != 2 || plan.NInspect != 1 || plan.NBlocked != 1 {
		t.Errorf("counts = %d/%d/%d/%d, want 4/2/1/1",
			plan.NRequeue, plan.NSkip, plan.NInspect, plan.NBlocked)
	}
	if i, j := resumIndexOf(plan.Requeue, "fail1"), resumIndexOf(plan.Requeue, "after-fail"); i >= j {
		t.Errorf("dependency order violated: fail1 at %d, after-fail at %d", i, j)
	}
	if plan.LastSeq == 0 {
		t.Error("journal tail not read: LastSeq = 0")
	}
	if len(plan.Notes) == 0 {
		t.Error("expected at least one journal note")
	}
}

func TestResumePlanRequeueRespectsDependencyOrder(t *testing.T) {
	s := resumTestStore(t)
	resumSeedRun(t, s, "run-chain", []Task{
		resumTask("c1", "first", nil, 1),
		resumTask("c2", "second", []string{"c1"}, 2),
		resumTask("c3", "third", []string{"c2"}, 3),
		resumTask("c4", "fourth", []string{"c3"}, 4),
		resumTask("s1", "independent, done", nil, 5),
		resumTask("s2", "depends only on s1", []string{"s1"}, 6),
	})
	resumMove(t, s, "run-chain", "c1", StatusFailed)
	resumMove(t, s, "run-chain", "c2", StatusRunning)
	resumMove(t, s, "run-chain", "s1", StatusRunning)
	resumMove(t, s, "run-chain", "s1", StatusVerifying)
	resumMove(t, s, "run-chain", "s1", StatusCompleted)
	resumMove(t, s, "run-chain", "s2", StatusFailed)

	plan, err := PlanResume(s, "run-chain")
	if err != nil {
		t.Fatalf("PlanResume: %v", err)
	}
	wantRequeue := []string{"c1", "c2", "c3", "c4", "s2"}
	if got := strings.Join(plan.Requeue, ","); got != strings.Join(wantRequeue, ",") {
		t.Fatalf("requeue = %v, want %v", plan.Requeue, wantRequeue)
	}
	if plan.NSkip != 1 || plan.Skip[0] != "s1" {
		t.Errorf("skip = %v, want [s1]", plan.Skip)
	}
	// Generic property: every dependency of a requeued task is earlier in the
	// list or in Skip.
	pos := map[string]int{}
	for i, id := range plan.Requeue {
		pos[id] = i
	}
	for _, id := range plan.Requeue {
		task, ok := resumGraphOf(t, s, "run-chain").Get(id)
		if !ok {
			t.Fatalf("task %s vanished", id)
		}
		for _, dep := range task.DependsOn {
			pi, ok := pos[dep]
			if ok && pi >= pos[id] {
				t.Errorf("task %s requeued before its dependency %s", id, dep)
			}
		}
	}
}

// resumGraphOf re-fetches the run so the property check uses real edges.
func resumGraphOf(t *testing.T, s *Store, runID string) Graph {
	t.Helper()
	run, ok := s.Get(runID)
	if !ok {
		t.Fatalf("run %s not found", runID)
	}
	return run.Graph()
}

func TestResumePlanUnknownAndEmptyRunID(t *testing.T) {
	s := resumTestStore(t)
	if _, err := PlanResume(s, "ghost-run"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown run: err = %v, want not-found error", err)
	}
	if _, err := PlanResume(s, ""); err == nil {
		t.Error("empty run id: expected an error")
	}
	if _, err := PlanResume(nil, "run-x"); err == nil || !strings.Contains(err.Error(), "nil store") {
		t.Errorf("nil store: err = %v, want nil-store error", err)
	}
}

func TestResumePlanString(t *testing.T) {
	s := resumTestStore(t)
	resumSeedRun(t, s, "run-str", []Task{
		NewTask("keep", "done"),
		NewTask("again", "failed once"),
	})
	resumFinish(t, s, "run-str", "keep")
	resumMove(t, s, "run-str", "again", StatusFailed)

	plan, err := PlanResume(s, "run-str")
	if err != nil {
		t.Fatalf("PlanResume: %v", err)
	}
	got := plan.String()
	for _, want := range []string{
		"resume plan run-str:",
		"requeue=1 [again]",
		"skip=1 [keep]",
		"journal-seq=",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}
	var nilPlan *ResumePlan
	if got := nilPlan.String(); got != "resume plan <nil>" {
		t.Errorf("nil plan String() = %q", got)
	}
}

func TestResumePlanAfterRestartRequeuesMidFlight(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	resumSeedRun(t, s1, "run-dead", []Task{
		NewTask("r1", "was running when the process died"),
		{ID: "r2", Title: "next", DependsOn: []string{"r1"}},
	})
	resumMove(t, s1, "run-dead", "r1", StatusRunning)
	if err := s1.CloseAll(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// Simulate the restart: Open recovers the run and the store itself
	// requeues the mid-flight task, so the plan must see it as requeueable.
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = s2.CloseAll() })

	plan, err := PlanResume(s2, "run-dead")
	if err != nil {
		t.Fatalf("PlanResume after restart: %v", err)
	}
	if got := strings.Join(plan.Requeue, ","); got != "r1,r2" {
		t.Errorf("requeue = %v, want [r1 r2]", plan.Requeue)
	}
	if plan.NInspect != 0 || plan.NBlocked != 0 {
		t.Errorf("inspect=%d blocked=%d, want 0/0", plan.NInspect, plan.NBlocked)
	}
	joined := strings.Join(plan.Notes, "; ")
	if !strings.Contains(joined, "interrupted") {
		t.Errorf("notes = %q, want the interrupted marker from recovery", joined)
	}
}

func TestApplyResumeRequeuesAndBumpsAttempts(t *testing.T) {
	s := resumTestStore(t)
	resumSeedRun(t, s, "run-apply", []Task{
		resumTask("keep", "done", nil, 1),
		resumTask("again", "failed once", nil, 2),
		resumTask("dropped", "cancelled earlier", nil, 3),
	})
	if err := s.RecordAttempt("run-apply", "again", "boom"); err != nil {
		t.Fatalf("record attempt: %v", err)
	}
	resumFinish(t, s, "run-apply", "keep")
	resumMove(t, s, "run-apply", "again", StatusFailed)
	resumMove(t, s, "run-apply", "dropped", StatusCancelled)

	plan, err := PlanResume(s, "run-apply")
	if err != nil {
		t.Fatalf("PlanResume: %v", err)
	}
	if got := strings.Join(plan.Requeue, ","); got != "again,dropped" {
		t.Fatalf("requeue = %v, want [again dropped]", plan.Requeue)
	}

	applied, err := ApplyResume(s, plan)
	if err != nil {
		t.Fatalf("ApplyResume: %v", err)
	}
	if got := strings.Join(applied, ","); got != "again,dropped" {
		t.Errorf("applied = %q, want again,dropped", got)
	}
	run, ok := s.Get("run-apply")
	if !ok {
		t.Fatal("run vanished")
	}
	graph := run.Graph()
	again, _ := graph.Get("again")
	if again.Status != StatusQueued || again.Attempts != 2 {
		t.Errorf("again: status=%s attempts=%d, want queued/2", again.Status, again.Attempts)
	}
	if again.LastError != "boom" {
		t.Errorf("again: LastError = %q, want it preserved", again.LastError)
	}
	dropped, _ := graph.Get("dropped")
	if dropped.Status != StatusQueued || dropped.Attempts != 1 {
		t.Errorf("dropped: status=%s attempts=%d, want queued/1", dropped.Status, dropped.Attempts)
	}
	keep, _ := graph.Get("keep")
	if keep.Status != StatusCompleted {
		t.Errorf("keep: status = %s, want completed (skip is untouched)", keep.Status)
	}
	if run.Status.Terminal() {
		t.Errorf("run status = %s, want an open run after resume", run.Status)
	}
}

func TestApplyResumeReopensClosedRun(t *testing.T) {
	s := resumTestStore(t)
	resumSeedRun(t, s, "run-closed", []Task{
		NewTask("keep", "done"),
		NewTask("again", "failed"),
	})
	resumFinish(t, s, "run-closed", "keep")
	resumMove(t, s, "run-closed", "again", StatusFailed)
	if err := s.Close("run-closed", StatusFailed); err != nil {
		t.Fatalf("close run: %v", err)
	}

	plan, err := PlanResume(s, "run-closed")
	if err != nil {
		t.Fatalf("PlanResume: %v", err)
	}
	notes := strings.Join(plan.Notes, "; ")
	if !strings.Contains(notes, "closed as failed") {
		t.Errorf("notes = %q, want a closed-as-failed note", notes)
	}
	applied, err := ApplyResume(s, plan)
	if err != nil {
		t.Fatalf("ApplyResume: %v", err)
	}
	if len(applied) != 1 || applied[0] != "again" {
		t.Errorf("applied = %v, want [again]", applied)
	}
	run, ok := s.Get("run-closed")
	if !ok {
		t.Fatal("run vanished")
	}
	if run.Status != StatusQueued {
		t.Errorf("run status = %s, want reopened to queued", run.Status)
	}
	if t2, _ := run.Graph().Get("again"); t2.Status != StatusQueued {
		t.Errorf("again status = %s, want queued", t2.Status)
	}
}

func TestApplyResumeNoopWhenNothingToRequeue(t *testing.T) {
	s := resumTestStore(t)
	resumSeedRun(t, s, "run-done", []Task{NewTask("only", "finished")})
	resumFinish(t, s, "run-done", "only")

	plan, err := PlanResume(s, "run-done")
	if err != nil {
		t.Fatalf("PlanResume: %v", err)
	}
	if plan.NRequeue != 0 || plan.NSkip != 1 {
		t.Fatalf("plan = %s, want 1 skip and nothing else", plan)
	}
	applied, err := ApplyResume(s, plan)
	if err != nil {
		t.Fatalf("ApplyResume: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("applied = %v, want empty", applied)
	}
	if _, err := ApplyResume(s, nil); err == nil || !strings.Contains(err.Error(), "nil plan") {
		t.Errorf("nil plan: err = %v, want nil-plan error", err)
	}
}
