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
