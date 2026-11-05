package execution

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusTransitions(t *testing.T) {
	cases := []struct {
		from, to Status
		ok       bool
	}{
		{StatusQueued, StatusRunning, true},
		{StatusQueued, StatusCompleted, false},
		{StatusRunning, StatusVerifying, true},
		{StatusVerifying, StatusCompleted, true},
		{StatusCompleted, StatusRunning, false},
		{StatusCompleted, StatusQueued, true},
		{StatusFailed, StatusQueued, true},
		{StatusCancelled, StatusRunning, false},
		{StatusPaused, StatusRunning, true},
		{StatusRunning, StatusRunning, true},
	}
	for _, c := range cases {
		if got := CanTransition(c.from, c.to); got != c.ok {
			t.Errorf("CanTransition(%s,%s) = %v, want %v", c.from, c.to, got, c.ok)
		}
	}
}

func TestGraphValidateFindsCycles(t *testing.T) {
	g := Graph{Tasks: []Task{
		{ID: "a", DependsOn: []string{"c"}},
		{ID: "b", DependsOn: []string{"a"}},
		{ID: "c", DependsOn: []string{"b"}},
	}}
	problems := g.Validate()
	if len(problems) == 0 || !strings.Contains(strings.Join(problems, ";"), "cycle") {
		t.Fatalf("problems = %v", problems)
	}
	if cycle := g.Cycle(); len(cycle) == 0 {
		t.Fatal("expected a cycle")
	}
}

func TestGraphValidateDanglingAndDuplicate(t *testing.T) {
	g := Graph{Tasks: []Task{
		{ID: "a", DependsOn: []string{"ghost"}},
		{ID: "a"},
	}}
	joined := strings.Join(g.Validate(), ";")
	if !strings.Contains(joined, "duplicate") || !strings.Contains(joined, "unknown task") {
		t.Fatalf("problems = %s", joined)
	}
}

func TestGraphReadyRespectsDependencies(t *testing.T) {
	g := Graph{Tasks: []Task{
		{ID: "a", Status: StatusCompleted},
		{ID: "b", Status: StatusQueued, DependsOn: []string{"a"}, Priority: 1},
		{ID: "c", Status: StatusQueued, DependsOn: []string{"b"}},
		{ID: "d", Status: StatusQueued, Priority: 5},
	}}
	ready := g.Ready()
	if len(ready) != 2 || ready[0].ID != "d" || ready[1].ID != "b" {
		t.Fatalf("ready = %v", ready)
	}
}

func TestGraphBlocked(t *testing.T) {
	g := Graph{Tasks: []Task{
		{ID: "a", Status: StatusFailed},
		{ID: "b", Status: StatusQueued, DependsOn: []string{"a"}},
		{ID: "c", Status: StatusCompleted},
	}}
	blocked := g.Blocked()
	if len(blocked) != 1 || blocked[0] != "b" {
		t.Fatalf("blocked = %v", blocked)
	}
}

func TestGraphConflictsByFile(t *testing.T) {
	ready := []Task{
		{ID: "a", FilesPlan: []string{"x.go"}},
		{ID: "b", FilesPlan: []string{"x.go"}},
		{ID: "c", FilesPlan: []string{"y.go"}},
	}
	pairs := (Graph{}).Conflicts(ready)
	if len(pairs) != 1 || pairs[0][0] != "a" || pairs[0][1] != "b" {
		t.Fatalf("conflicts = %v", pairs)
	}
}

func TestStoreLifecycle(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRun("run-1", "test run", "objective", []Task{
		{ID: "a", Title: "first"},
		{ID: "b", Title: "second", DependsOn: []string{"a"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != StatusRunning || len(run.Tasks) != 2 {
		t.Fatalf("run = %+v", run)
	}
	if _, err := s.Transition("run-1", "a", StatusRunning); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition("run-1", "a", StatusCompleted); err == nil {
		t.Fatal("running → completed should be rejected")
	}
	if _, err := s.Transition("run-1", "a", StatusVerifying); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordVerification("run-1", "a", Verification{Kind: "test", Command: "go test", Passed: true, ExitCode: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition("run-1", "a", StatusCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transition("run-1", "b", StatusRunning); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAttempt("run-1", "b", "boom"); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseAll(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.CloseAll()
	got, ok := reopened.Get("run-1")
	if !ok {
		t.Fatal("run lost after restart")
	}
	if got.Tasks[0].Status != StatusCompleted || !got.Tasks[0].Verified() {
		t.Fatalf("task a = %+v", got.Tasks[0])
	}
	if got.Tasks[1].Status != StatusQueued || got.Tasks[1].Attempts != 2 || !strings.Contains(got.Tasks[1].LastError, "interrupted") {
		t.Fatalf("recovered task b = %+v", got.Tasks[1])
	}
	if got.Tasks[1].Status.Active() {
		t.Fatal("recovered task must not look active")
	}
}

func TestStoreRejectsInvalidPlan(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.CloseAll()
	if _, err := s.CreateRun("run-bad", "bad", "", []Task{{ID: "a", DependsOn: []string{"ghost"}}, {ID: "a"}}); err == nil {
		t.Fatal("invalid plan accepted")
	}
}

func TestStoreRejectsBadRunID(t *testing.T) {
	s, _ := Open(t.TempDir())
	defer s.CloseAll()
	if _, err := s.CreateRun("../escape", "x", "", []Task{{ID: "a"}}); err == nil {
		t.Fatal("path traversal run id accepted")
	}
}

func TestJournalRecoversFromTornLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "j.ndjson")
	j, _, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := j.Append("test", "t", map[string]int{"n": i}); err != nil {
			t.Fatal(err)
		}
	}
	_ = j.Close()
	// Simulate a crash mid-write: append a truncated JSON object.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"seq":4,"kind":"test","data":{"n":"tru`)
	_ = f.Close()

	j2, last, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if last != 3 {
		t.Fatalf("last = %d, want 3", last)
	}
	ev, err := j2.Append("test", "t", map[string]int{"n": 9})
	if err != nil {
		t.Fatal(err)
	}
	if ev.Seq != 4 {
		t.Fatalf("seq = %d, want 4", ev.Seq)
	}
	_ = j2.Close()
	events, err := Replay(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4", len(events))
	}
	var payload map[string]int
	if err := json.Unmarshal(events[3].Data, &payload); err != nil || payload["n"] != 9 {
		t.Fatalf("last event data = %s", events[3].Data)
	}
}

func TestRunAutoClosesWhenAllTasksDone(t *testing.T) {
	s, _ := Open(t.TempDir())
	defer s.CloseAll()
	if _, err := s.CreateRun("run-auto", "auto", "", []Task{{ID: "only"}}); err != nil {
		t.Fatal(err)
	}
	for _, step := range []Status{StatusRunning, StatusVerifying, StatusCompleted} {
		if _, err := s.Transition("run-auto", "only", step); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
	run, _ := s.Get("run-auto")
	if run.Status != StatusCompleted || run.ClosedAt == "" {
		t.Fatalf("run status = %s closed at %q", run.Status, run.ClosedAt)
	}
}

func TestVerificationLevels(t *testing.T) {
	if (Task{Status: StatusCompleted}).Level() != LevelUntested {
		t.Fatal("completed without evidence must be untested")
	}
	if (Task{Verify: []Verification{{Passed: true}}}).Level() != LevelVerified {
		t.Fatal("passing check must be verified")
	}
	if (Task{Verify: []Verification{{Passed: false}}}).Level() != LevelFailed {
		t.Fatal("failing check must be failed")
	}
}

func TestStoreCancelAndRetry(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.CloseAll()

	tasks := []Task{
		{ID: "t1", Title: "first"},
		{ID: "t2", Title: "second", DependsOn: []string{"t1"}},
	}
	if _, err := s.CreateRun("run-cr", "cancel retry", "", tasks); err != nil {
		t.Fatal(err)
	}

	// Cancel the run
	cancelled, err := s.Cancel("run-cr", "user requested cancel")
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != StatusCancelled || cancelled.ClosedAt == "" {
		t.Fatalf("expected run to be cancelled, got %+v", cancelled)
	}
	if cancelled.Tasks[0].Status != StatusCancelled || cancelled.Tasks[1].Status != StatusCancelled {
		t.Fatalf("expected tasks to be cancelled, got %+v", cancelled.Tasks)
	}

	// Cancelling an already terminal run fails
	if _, err := s.Cancel("run-cr", "again"); err == nil {
		t.Fatal("expected cancel on already cancelled run to fail")
	}

	// Retry specific task t1
	reopened, err := s.Retry("run-cr", "t1")
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Status != StatusQueued || reopened.ClosedAt != "" {
		t.Fatalf("expected run to reopen as queued, got %+v", reopened)
	}
	if reopened.Tasks[0].Status != StatusQueued {
		t.Fatalf("expected t1 to be queued, got %s", reopened.Tasks[0].Status)
	}
	if reopened.Tasks[1].Status != StatusCancelled {
		t.Fatalf("expected t2 to remain cancelled, got %s", reopened.Tasks[1].Status)
	}

	// Retry all remaining cancelled/failed tasks
	allRetried, err := s.Retry("run-cr", "")
	if err != nil {
		t.Fatal(err)
	}
	if allRetried.Tasks[1].Status != StatusQueued {
		t.Fatalf("expected t2 to be queued now, got %s", allRetried.Tasks[1].Status)
	}

	// Persists across restart
	if err := s.CloseAll(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.CloseAll()
	persisted, ok := s2.Get("run-cr")
	if !ok || persisted.Status != StatusQueued || len(persisted.Tasks) != 2 {
		t.Fatalf("run not restored correctly: %+v", persisted)
	}
}

func TestCreatedAtLessChronological(t *testing.T) {
	// RFC3339Nano trims trailing fraction zeros, so naive string order puts
	// the fractionless older stamp after the fractional newer one.
	older := "2026-01-02T10:00:00Z"
	newer := "2026-01-02T10:00:00.5Z"
	if !createdAtLess(older, newer) {
		t.Fatalf("older fractionless stamp must sort before newer fractional stamp")
	}
	if createdAtLess(newer, older) {
		t.Fatalf("newer stamp must not sort before older")
	}
	// Mixed widths within the same second stay chronological.
	a := "2026-01-02T10:00:00.25Z"
	b := "2026-01-02T10:00:00.5Z"
	if !createdAtLess(a, b) || createdAtLess(b, a) {
		t.Fatalf("fraction widths must compare numerically: %s vs %s", a, b)
	}
	// Unparsable stamps fall back to deterministic string order.
	if !createdAtLess("aaa", "bbb") || createdAtLess("bbb", "aaa") {
		t.Fatalf("unparsable stamps must fall back to string order")
	}
}

func TestNormalizeRunTasksMixedFractionOrder(t *testing.T) {
	tasks := []Task{
		{ID: "late", Title: "created with fraction", CreatedAt: "2026-01-02T10:00:00.5Z", DependsOn: []string{"early"}},
		{ID: "early", Title: "created without fraction", CreatedAt: "2026-01-02T10:00:00Z"},
	}
	out := normalizeRunTasks(tasks)
	if out[0].ID != "early" || out[1].ID != "late" {
		t.Fatalf("chronological order violated: got %s then %s", out[0].ID, out[1].ID)
	}
}
