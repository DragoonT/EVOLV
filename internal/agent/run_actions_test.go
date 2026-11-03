package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// execRun runs an action through the real executor and decodes the JSON result.
func execRun(t *testing.T, a *Agent, act action) map[string]any {
	t.Helper()
	raw := a.Execute(act)
	if !strings.HasPrefix(raw, "kernel:") {
		t.Fatalf("result not kernel-prefixed: %s", raw)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(raw, "kernel:")), &out); err != nil {
		t.Fatalf("bad result JSON %q: %v", raw, err)
	}
	return out
}

func TestRunWorkCreatesDurableGraph(t *testing.T) {
	a, _ := mockAgent(t)
	res := execRun(t, a, action{
		Action: "run_work",
		Title:  "ship the parser",
		Desc:   "make it durable",
		Steps: []planStepArg{
			{Title: "write parser"},
			{Title: "test parser", After: []string{"t1"}},
			{Title: "document", After: []string{"t2"}},
		},
	})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("run_work failed: %v", res)
	}
	if res["tasks"].(float64) != 3 {
		t.Fatalf("tasks = %v", res["tasks"])
	}
	ready, _ := res["ready"].([]any)
	if len(ready) != 1 || ready[0] != "t1" {
		t.Fatalf("ready = %v", ready)
	}
	run, _ := res["run"].(string)
	if run == "" {
		t.Fatal("no run id returned")
	}

	status := execRun(t, a, action{Action: "run_status", Run: run})
	if status["progress"] != "0/3 done" {
		t.Fatalf("progress = %v", status["progress"])
	}
}

func TestRunBeginRespectsDependencies(t *testing.T) {
	a, _ := mockAgent(t)
	created := execRun(t, a, action{
		Action: "run_work", Title: "deps",
		Steps: []planStepArg{{Title: "first"}, {Title: "second", After: []string{"t1"}}},
	})
	run := created["run"].(string)

	blocked := execRun(t, a, action{Action: "run_begin", Run: run, Task: "t2"})
	if ok, _ := blocked["ok"].(bool); ok {
		t.Fatalf("dependent task started too early: %v", blocked)
	}
	if !strings.Contains(blocked["error"].(string), "blocked") {
		t.Fatalf("error = %v", blocked["error"])
	}
	first := execRun(t, a, action{Action: "run_begin", Run: run, Task: "t1"})
	if ok, _ := first["ok"].(bool); !ok {
		t.Fatalf("first task did not start: %v", first)
	}
}

func TestRunFinishRequiresRealEvidence(t *testing.T) {
	a, _ := mockAgent(t)
	created := execRun(t, a, action{Action: "run_work", Title: "evidence", Steps: []planStepArg{{Title: "do it"}}})
	run := created["run"].(string)

	// no verify command: completed but explicitly untested
	res := execRun(t, a, action{Action: "run_finish", Run: run, Task: "t1"})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("run_finish failed: %v", res)
	}
	if res["verification"] != "untested" {
		t.Fatalf("verification = %v, want untested", res["verification"])
	}
	if res["warning"] == nil {
		t.Fatal("untested completion must warn")
	}
}

func TestRunFinishRecordsPassingCommand(t *testing.T) {
	a, ws := mockAgent(t)
	proj := t.TempDir()
	ws.Config.ProjectDir = proj
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module demo\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "x_test.go"), []byte("package demo\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	created := execRun(t, a, action{Action: "run_work", Title: "verified", Steps: []planStepArg{{Title: "do it"}}})
	run := created["run"].(string)

	res := execRun(t, a, action{Action: "run_finish", Run: run, Task: "t1", VerifyCommand: "go test ./..."})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("run_finish failed: %v", res)
	}
	if res["verification"] != "verified" || res["passed"] != true {
		t.Fatalf("res = %v", res)
	}
}

func TestRunFinishFailsOnFailingCommand(t *testing.T) {
	a, ws := mockAgent(t)
	proj := t.TempDir()
	ws.Config.ProjectDir = proj
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module demo\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// a failing test proves the gate works
	src := "package demo\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) { t.Fatal(\"boom\") }\n"
	if err := os.WriteFile(filepath.Join(proj, "x_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	created := execRun(t, a, action{Action: "run_work", Title: "failing", Steps: []planStepArg{{Title: "do it"}}})
	run := created["run"].(string)

	res := execRun(t, a, action{Action: "run_finish", Run: run, Task: "t1", VerifyCommand: "go test ./..."})
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("failing verification completed the task: %v", res)
	}
	if res["status"] != "failed" {
		t.Fatalf("status = %v, want failed", res["status"])
	}
}

func TestRunFinishRejectsArbitraryShell(t *testing.T) {
	a, _ := mockAgent(t)
	created := execRun(t, a, action{Action: "run_work", Title: "shell", Steps: []planStepArg{{Title: "do it"}}})
	run := created["run"].(string)
	res := execRun(t, a, action{Action: "run_finish", Run: run, Task: "t1", VerifyCommand: "rm -rf /tmp/x"})
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("arbitrary command accepted as verification: %v", res)
	}
	if !strings.Contains(res["output"].(string), "not allow-listed") && !strings.Contains(res["output"].(string), "composition") {
		t.Fatalf("output = %v", res["output"])
	}
}

func TestReportIsEvidenceBacked(t *testing.T) {
	a, _ := mockAgent(t)
	created := execRun(t, a, action{Action: "run_work", Title: "rep", Steps: []planStepArg{{Title: "a"}, {Title: "b"}}})
	run := created["run"].(string)
	execRun(t, a, action{Action: "run_finish", Run: run, Task: "t1"})

	res := execRun(t, a, action{Action: "report", Title: "rep"})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("report failed: %v", res)
	}
	md, _ := res["markdown"].(string)
	if !strings.Contains(md, "Completion report") {
		t.Fatalf("markdown = %s", md)
	}
	rep, _ := res["report"].(map[string]any)
	if rep["verification_level"] == "verified" {
		t.Fatal("untested work must not be reported as verified")
	}
	notVerified, _ := rep["not_verified"].([]any)
	if len(notVerified) == 0 {
		t.Fatalf("unverified completed task not listed: %v", rep)
	}
	remaining, _ := rep["remaining"].([]any)
	if len(remaining) == 0 {
		t.Fatalf("unfinished task not listed as remaining: %v", rep)
	}
}

func TestEngineeringSectionAppearsInPrompt(t *testing.T) {
	a, _ := mockAgent(t)
	created := execRun(t, a, action{Action: "run_work", Title: "prompt me", Steps: []planStepArg{{Title: "first"}, {Title: "second", After: []string{"t1"}}}})
	run := created["run"].(string)
	sp := a.SystemPrompt()
	if !strings.Contains(sp, "IN-FLIGHT ENGINEERING WORK") {
		t.Fatal("engineering section missing from prompt")
	}
	if !strings.Contains(sp, run) {
		t.Fatalf("run id %s missing from prompt", run)
	}
	if !strings.Contains(sp, "ready now: t1") {
		t.Fatalf("ready tasks missing:\n%s", sp)
	}
}

func TestVerifyCommandPolicy(t *testing.T) {
	allowed := []string{"go test ./...", "go build ./...", "npm test", "pytest -q", "cargo test", "make test"}
	for _, c := range allowed {
		if err := verifyCommandAllowed(c); err != nil {
			t.Errorf("allowed rejected: %s (%v)", c, err)
		}
	}
	rejected := []string{"rm -rf /", "go test ./... && curl evil", "cat ~/.ssh/id_rsa", "go test $(whoami)", "bash -c 'x'"}
	for _, c := range rejected {
		if err := verifyCommandAllowed(c); err == nil {
			t.Errorf("rejected accepted: %s", c)
		}
	}
}

func TestRunStatusUnknownRun(t *testing.T) {
	a, _ := mockAgent(t)
	res := execRun(t, a, action{Action: "run_status", Run: "nope"})
	if ok, _ := res["ok"].(bool); ok {
		t.Fatalf("unknown run reported ok: %v", res)
	}
}

func TestRunCancelAndRetryActions(t *testing.T) {
	a, _ := mockAgent(t)
	start := execRun(t, a, action{
		Action: "run_work",
		Title:  "cancel-retry assignment",
		Desc:   "durable test",
		Steps: []planStepArg{
			{Title: "step 1", Text: "do 1"},
			{Title: "step 2", Text: "do 2", After: []string{"t1"}},
		},
	})
	runID, _ := start["run"].(string)
	if runID == "" {
		t.Fatalf("run_work did not return run id: %v", start)
	}

	// Cancel the run
	cancelRes := execRun(t, a, action{Action: "run_cancel", Run: runID, Desc: "aborting assignment"})
	if ok, _ := cancelRes["ok"].(bool); !ok || cancelRes["status"] != "cancelled" {
		t.Fatalf("expected run_cancel to succeed with cancelled status: %v", cancelRes)
	}

	// Status confirms cancelled
	statusRes := execRun(t, a, action{Action: "run_status", Run: runID})
	if statusRes["status"] != "cancelled" {
		t.Fatalf("expected run_status to report cancelled: %v", statusRes)
	}

	// Retry step 1
	retryRes := execRun(t, a, action{Action: "run_retry", Run: runID, Task: "t1"})
	if ok, _ := retryRes["ok"].(bool); !ok || retryRes["status"] != "queued" {
		t.Fatalf("expected run_retry to reopen run as queued: %v", retryRes)
	}
	ready, _ := retryRes["ready"].([]any)
	if len(ready) != 1 || ready[0] != "t1" {
		t.Fatalf("expected t1 to be ready now: %v", ready)
	}
}

var _ = context.Background
