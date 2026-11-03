package execution

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOutcomeLogRecordsTerminalTasksOnce(t *testing.T) {
	dir := t.TempDir()
	log, err := OpenOutcomes(dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(filepath.Join(dir, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.CloseAll()
	if _, err := store.CreateRun("run-o", "outcomes", "", []Task{{ID: "a"}, {ID: "b", DependsOn: []string{"a"}}}); err != nil {
		t.Fatal(err)
	}
	for _, step := range []Status{StatusRunning, StatusVerifying} {
		if _, err := store.Transition("run-o", "a", step); err != nil {
			t.Fatal(err)
		}
	}
	_ = store.RecordVerification("run-o", "a", Verification{Kind: "test", Command: "go test ./...", Passed: true, ExitCode: 0, DurationMS: 1200})
	if _, err := store.Transition("run-o", "a", StatusCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition("run-o", "b", StatusFailed); err != nil {
		t.Fatal(err)
	}
	_ = store.RecordAttempt("run-o", "b", "boom")

	run, _ := store.Get("run-o")
	if n := log.RecordRunOutcomes(run); n != 2 {
		t.Fatalf("recorded %d, want 2", n)
	}
	// recording again must not duplicate
	if n := log.RecordRunOutcomes(run); n != 0 {
		t.Fatalf("duplicated %d outcomes", n)
	}
	got := log.Read()
	if len(got) != 2 {
		t.Fatalf("outcomes = %d", len(got))
	}
	var completed, failed *Outcome
	for i := range got {
		switch got[i].Task {
		case "a":
			completed = &got[i]
		case "b":
			failed = &got[i]
		}
	}
	if completed == nil || !completed.Verified || completed.Verify != "go test ./..." || completed.DurationMS != 1200 {
		t.Fatalf("completed outcome = %+v", completed)
	}
	if failed == nil || failed.Error != "boom" || failed.Verified {
		t.Fatalf("failed outcome = %+v", failed)
	}
}

func TestSummariseInsights(t *testing.T) {
	outcomes := []Outcome{
		{Task: "a", Status: StatusCompleted, Verified: true, Attempts: 0, DurationMS: 1000, Verify: "go test ./...", VerifyOK: true},
		{Task: "b", Status: StatusCompleted, Verified: true, Attempts: 1, DurationMS: 3000, Verify: "go test ./...", VerifyOK: true},
		{Task: "c", Status: StatusFailed, Attempts: 2, DurationMS: 2000, Verify: "npm test", VerifyOK: false, Error: "verification failed"},
		{Task: "d", Status: StatusCancelled},
	}
	in := Summarise(outcomes)
	if in.Tasks != 4 {
		t.Fatalf("tasks = %d", in.Tasks)
	}
	if in.VerifiedRate != 50 {
		t.Fatalf("verified rate = %d", in.VerifiedRate)
	}
	if in.FirstTryRate != 25 {
		t.Fatalf("first try rate = %d (only one completed with zero attempts)", in.FirstTryRate)
	}
	if len(in.TopVerifiers) != 2 || in.TopVerifiers[0].Command != "go test ./..." {
		t.Fatalf("verifiers = %+v", in.TopVerifiers)
	}
	if in.TopVerifiers[0].Runs != 2 || in.TopVerifiers[0].Passed != 2 {
		t.Fatalf("verifier stats = %+v", in.TopVerifiers[0])
	}
	if len(in.FailureReasons) == 0 {
		t.Fatal("failure reasons missing")
	}
	if in.ByStatus["completed"] != 2 || in.ByStatus["failed"] != 1 || in.ByStatus["cancelled"] != 1 {
		t.Fatalf("by status = %v", in.ByStatus)
	}
}

func TestOutcomeLogSkipsCorruptLines(t *testing.T) {
	dir := t.TempDir()
	log, _ := OpenOutcomes(dir)
	_ = log.Append(Outcome{Task: "a", Status: StatusCompleted})
	f, err := os.OpenFile(filepath.Join(dir, "outcomes.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{corrupt\n")
	_ = f.Close()
	_ = log.Append(Outcome{Task: "b", Status: StatusCompleted})
	got := log.Read()
	if len(got) != 2 {
		t.Fatalf("outcomes = %d, want 2 (corrupt line skipped)", len(got))
	}
}

func TestSummariseEmpty(t *testing.T) {
	in := Summarise(nil)
	if in.Tasks != 0 || in.VerifiedRate != 0 {
		t.Fatalf("in = %+v", in)
	}
}
