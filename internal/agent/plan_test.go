package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dxn111/EVOLV/internal/workspace"
)

// TestSetPlanPersists: set_plan validates, writes plan.json atomically,
// normalizes statuses and reports done/total.
func TestSetPlanPersists(t *testing.T) {
	a := testAgent(t)

	act := action{Action: "set_plan", Title: "rebuild the importer",
		Steps: []planStepArg{
			{Title: "map the repo", Status: "done"},
			{Title: "write the parser", Status: "doing"},
			{Title: "add tests", Status: "todo"},
			{Title: "", Status: "todo"},         // dropped: no title
			{Title: "ship it", Status: "weird"}, // normalized to todo
		}}
	res := execOK(t, a, act)
	if res["ok"] != true {
		t.Fatalf("set_plan failed: %v", res)
	}
	if res["note"] != "plan updated: 1/4 steps done" {
		t.Fatalf("bad note: %v", res["note"])
	}

	p := a.ws.ReadPlan()
	if p == nil {
		t.Fatal("plan.json not persisted")
	}
	if p.Title != "rebuild the importer" || len(p.Steps) != 4 {
		t.Fatalf("bad plan persisted: %+v", p)
	}
	if p.Steps[3].Status != "todo" {
		t.Fatalf("bad status normalization: %+v", p.Steps[3])
	}
	// no .tmp litter
	if _, err := os.Stat(filepath.Join(a.ws.Dir(""), workspace.PlanFile+".tmp")); !os.IsNotExist(err) {
		t.Error("atomic rename left a .tmp file behind")
	}
}

// TestSetPlanRejectsEmpty: no steps / no titles must fail cleanly, nothing persisted.
func TestSetPlanRejectsEmpty(t *testing.T) {
	a := testAgent(t)
	res := execOK(t, a, action{Action: "set_plan"})
	if res["ok"] != false || !strings.Contains(res["error"].(string), "needs steps") {
		t.Fatalf("empty plan not rejected: %v", res)
	}
	res = execOK(t, a, action{Action: "set_plan", Steps: []planStepArg{{Title: "  ", Status: "todo"}}})
	if res["ok"] != false || !strings.Contains(res["error"].(string), "titles") {
		t.Fatalf("blank titles not rejected: %v", res)
	}
	if a.ws.ReadPlan() != nil {
		t.Error("rejected plans must not be persisted")
	}
}

// TestPlanSurvivesRestart: a fresh workspace handle reads the same plan.
func TestPlanSurvivesRestart(t *testing.T) {
	a := testAgent(t)
	a.Execute(action{Action: "set_plan", Title: "T", Steps: []planStepArg{{Title: "step", Status: "doing"}}})
	p := a.ws.ReadPlan() // same file a restarted kernel would load
	if p == nil || len(p.Steps) != 1 || p.Steps[0].Status != "doing" {
		t.Fatalf("plan did not persist correctly: %+v", p)
	}
	if p.Updated == "" {
		t.Error("plan.Updated must be stamped")
	}
}

// TestPlanAdvance: one call marks the doing step done and pulls the next
// todo into doing; plan.json and the bus event both move forward.
func TestPlanAdvance(t *testing.T) {
	a := testAgent(t)
	a.Execute(action{Action: "set_plan", Title: "pipeline",
		Steps: []planStepArg{
			{Title: "map", Status: "done"},
			{Title: "build", Status: "doing"},
			{Title: "test", Status: "todo"},
			{Title: "ship", Status: "todo"},
		}})
	res := execOK(t, a, action{Action: "plan_advance"})
	if res["ok"] != true || res["step"] != "build" {
		t.Fatalf("plan_advance failed: %v", res)
	}
	p := a.ws.ReadPlan()
	want := []string{"done", "done", "doing", "todo"}
	for i, s := range p.Steps {
		if s.Status != want[i] {
			t.Fatalf("step %d status = %s, want %s (%+v)", i, s.Status, want[i], p.Steps)
		}
	}
	// advance again → "test" done, "ship" starts
	execOK(t, a, action{Action: "plan_advance"})
	p = a.ws.ReadPlan()
	if p.Steps[2].Status != "done" || p.Steps[3].Status != "doing" {
		t.Fatalf("second advance wrong: %+v", p.Steps)
	}
	// third advance completes the last doing step ("ship") → all done
	res = execOK(t, a, action{Action: "plan_advance"})
	if res["ok"] != true || res["step"] != "ship" {
		t.Fatalf("third advance failed: %v", res)
	}
	for _, s := range a.ws.ReadPlan().Steps {
		if s.Status != "done" {
			t.Fatalf("plan not fully done: %+v", a.ws.ReadPlan().Steps)
		}
	}
	// fourth advance: nothing doing, no index → clean rejection
	res = execOK(t, a, action{Action: "plan_advance"})
	if res["ok"] != false || !strings.Contains(res["error"].(string), "no index") {
		t.Fatalf("exhausted plan not handled cleanly: %v", res)
	}
}

// TestPlanAdvanceByIndex: explicit index marks any step done without a doing step.
func TestPlanAdvanceByIndex(t *testing.T) {
	a := testAgent(t)
	a.Execute(action{Action: "set_plan", Title: "T",
		Steps: []planStepArg{{Title: "a", Status: "todo"}, {Title: "b", Status: "todo"}}})
	res := execOK(t, a, action{Action: "plan_advance", Args: map[string]any{"index": float64(1)}})
	if res["ok"] != true {
		t.Fatalf("index advance failed: %v", res)
	}
	p := a.ws.ReadPlan()
	if p.Steps[1].Status != "done" || p.Steps[0].Status != "doing" {
		t.Fatalf("index advance wrong: %+v", p.Steps)
	}
}

// TestPlanAdvanceRejectsBogus: no plan, or no doing+no index, must fail cleanly.
func TestPlanAdvanceRejectsBogus(t *testing.T) {
	a := testAgent(t)
	res := execOK(t, a, action{Action: "plan_advance"})
	if res["ok"] != false || !strings.Contains(res["error"].(string), "no plan") {
		t.Fatalf("no-plan not rejected: %v", res)
	}
	a.Execute(action{Action: "set_plan", Title: "T",
		Steps: []planStepArg{{Title: "a", Status: "todo"}, {Title: "b", Status: "done"}}})
	res = execOK(t, a, action{Action: "plan_advance"})
	if res["ok"] != false || !strings.Contains(res["error"].(string), "no index") {
		t.Fatalf("no-doing not rejected: %v", res)
	}
	// out-of-range index rejected too
	res = execOK(t, a, action{Action: "plan_advance", Args: map[string]any{"index": float64(9)}})
	if res["ok"] != false {
		t.Fatalf("out-of-range index accepted: %v", res)
	}
}

// TestPlanInPrompt: the declared plan is injected into the system prompt so
// the brain always knows where it is in its own work.
func TestPlanInPrompt(t *testing.T) {
	a := testAgent(t)
	if strings.Contains(a.SystemPrompt(), "CURRENT PLAN") {
		t.Fatal("plan block present before any plan was declared")
	}
	a.Execute(action{Action: "set_plan", Title: "rebuild the core",
		Steps: []planStepArg{
			{Title: "audit", Status: "done"},
			{Title: "rewrite engine", Status: "doing"},
			{Title: "ship release", Status: "todo"},
		}})
	sp := a.SystemPrompt()
	if !strings.Contains(sp, "## CURRENT PLAN — rebuild the core") {
		t.Fatal("plan title not in system prompt")
	}
	if !strings.Contains(sp, "1. [x] audit") || !strings.Contains(sp, "2. [~] rewrite engine") ||
		!strings.Contains(sp, "3. [ ] ship release") {
		t.Fatalf("plan steps not rendered in prompt:\n%s", sp)
	}
	// advance → prompt reflects the new position
	a.Execute(action{Action: "plan_advance"})
	sp = a.SystemPrompt()
	if !strings.Contains(sp, "2. [x] rewrite engine") || !strings.Contains(sp, "3. [~] ship release") {
		t.Fatal("prompt did not follow plan_advance")
	}
}
