package execution

import (
	"strings"
	"testing"
)

func TestCompilePlanBuildsWaves(t *testing.T) {
	res := CompilePlan(PlanSpec{
		Title: "ship it",
		Steps: []PlanStepSpec{
			{Title: "contract"},
			{Title: "backend", After: []string{"contract"}},
			{Title: "frontend", After: []string{"contract"}},
			{Title: "e2e", After: []string{"backend", "frontend"}},
		},
	})
	if len(res.Problems) != 0 {
		t.Fatalf("problems: %v", res.Problems)
	}
	if len(res.Tasks) != 4 {
		t.Fatalf("tasks = %d", len(res.Tasks))
	}
	want := [][]string{{"t1"}, {"t2", "t3"}, {"t4"}}
	if len(res.Waves) != len(want) {
		t.Fatalf("waves = %v", res.Waves)
	}
	for i, w := range want {
		if strings.Join(res.Waves[i], ",") != strings.Join(w, ",") {
			t.Fatalf("wave %d = %v, want %v", i, res.Waves[i], w)
		}
	}
	if len(res.Order) != 4 || res.Order[0] != "t1" || res.Order[3] != "t4" {
		t.Fatalf("order = %v", res.Order)
	}
}

func TestCompilePlanResolvesTitleReferences(t *testing.T) {
	res := CompilePlan(PlanSpec{
		Title: "titles",
		Steps: []PlanStepSpec{
			{ID: "model", Title: "Data model"},
			{ID: "api", Title: "API layer", After: []string{"Data model"}},
		},
	})
	if len(res.Problems) != 0 {
		t.Fatalf("problems: %v", res.Problems)
	}
	if len(res.Tasks[1].DependsOn) != 1 || res.Tasks[1].DependsOn[0] != "model" {
		t.Fatalf("deps = %v", res.Tasks[1].DependsOn)
	}
}

func TestCompilePlanRejectsBrokenPlans(t *testing.T) {
	cases := map[string]PlanSpec{
		"no title": {Steps: []PlanStepSpec{{Title: "a"}}},
		"no steps": {Title: "x"},
		"unknown dep": {Title: "x", Steps: []PlanStepSpec{
			{Title: "a", After: []string{"ghost"}},
		}},
		"cycle": {Title: "x", Steps: []PlanStepSpec{
			{ID: "a", Title: "a", After: []string{"b"}},
			{ID: "b", Title: "b", After: []string{"a"}},
		}},
		"self dep": {Title: "x", Steps: []PlanStepSpec{
			{ID: "a", Title: "a", After: []string{"a"}},
		}},
		"duplicate id": {Title: "x", Steps: []PlanStepSpec{
			{ID: "a", Title: "one"}, {ID: "a", Title: "two"},
		}},
		"empty title": {Title: "x", Steps: []PlanStepSpec{{Title: "  "}}},
	}
	for name, spec := range cases {
		res := CompilePlan(spec)
		if len(res.Problems) == 0 {
			t.Errorf("%s: accepted a broken plan", name)
		}
		if len(res.Waves) != 0 {
			t.Errorf("%s: produced a schedule for a broken plan", name)
		}
	}
}

func TestCompilePlanAcceptsRealisticProject(t *testing.T) {
	res := CompilePlan(PlanSpec{
		Title:     "Build the billing feature",
		Objective: "charge once, idempotently",
		Steps: []PlanStepSpec{
			{ID: "schema", Title: "Schema + migrations", Files: []string{"db/schema.sql"}, Verify: "go test ./db/...", Phase: "data"},
			{ID: "service", Title: "Service layer", After: []string{"schema"}, Files: []string{"internal/billing/service.go"}, Verify: "go test ./internal/billing/...", Phase: "core"},
			{ID: "api", Title: "HTTP handlers", After: []string{"service"}, Phase: "core"},
			{ID: "docs", Title: "Document the endpoint", After: []string{"api"}, Phase: "docs"},
			{ID: "tests", Title: "End-to-end charge test", After: []string{"api"}, Verify: "go test ./...", Phase: "verify"},
		},
	})
	if len(res.Problems) != 0 {
		t.Fatalf("problems: %v", res.Problems)
	}
	if len(res.Waves) != 4 {
		t.Fatalf("waves = %v", res.Waves)
	}
	// data and core run before verify
	order := strings.Join(res.Order, " ")
	if strings.Index(order, "schema") > strings.Index(order, "service") {
		t.Fatalf("dependency order wrong: %s", order)
	}
}

func TestCompilePlanDeterministic(t *testing.T) {
	spec := PlanSpec{Title: "det", Steps: []PlanStepSpec{
		{Title: "a"}, {Title: "b"}, {Title: "c"}, {Title: "d", After: []string{"a", "b", "c"}},
	}}
	first := strings.Join(CompilePlan(spec).Order, ",")
	for i := 0; i < 20; i++ {
		if got := strings.Join(CompilePlan(spec).Order, ","); got != first {
			t.Fatalf("non-deterministic order: %q vs %q", got, first)
		}
	}
}

func TestPlanCanBeExecutedByStore(t *testing.T) {
	res := CompilePlan(PlanSpec{Title: "store me", Steps: []PlanStepSpec{
		{Title: "one"}, {Title: "two", After: []string{"t1"}},
	}})
	if len(res.Problems) != 0 {
		t.Fatalf("problems: %v", res.Problems)
	}
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.CloseAll()
	run, err := store.CreateRun("run-plan", res.Tasks[0].Title, "", res.Tasks)
	if err != nil {
		t.Fatalf("compiled plan rejected by store: %v", err)
	}
	if len(run.Tasks) != 2 {
		t.Fatalf("tasks = %d", len(run.Tasks))
	}
}
