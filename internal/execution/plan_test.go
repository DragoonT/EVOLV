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

// --- validation error sharpening (wave hardening) ---

func TestCompilePlanUnknownDependencyNamesStepAndReason(t *testing.T) {
	res := CompilePlan(PlanSpec{Title: "x", Steps: []PlanStepSpec{
		{ID: "build", Title: "Build", After: []string{"ghost"}},
	}})
	if len(res.Problems) != 1 {
		t.Fatalf("problems = %v, want exactly one", res.Problems)
	}
	want := `step "build" depends on unknown step "ghost"`
	if res.Problems[0] != want {
		t.Fatalf("problem = %q, want %q", res.Problems[0], want)
	}
	if len(res.Waves) != 0 {
		t.Fatalf("produced a schedule for a broken plan: %v", res.Waves)
	}
}

func TestCompilePlanSelfDependencyNamesStep(t *testing.T) {
	res := CompilePlan(PlanSpec{Title: "x", Steps: []PlanStepSpec{
		{ID: "a", Title: "a", After: []string{"a"}},
	}})
	joined := strings.Join(res.Problems, "; ")
	if !strings.Contains(joined, `step "a" depends on itself`) {
		t.Fatalf("problems %v do not name the self-dependent step", res.Problems)
	}
}

func TestCompilePlanDuplicateIDReportsBothOccurrences(t *testing.T) {
	res := CompilePlan(PlanSpec{Title: "x", Steps: []PlanStepSpec{
		{ID: "a", Title: "one"},
		{ID: "a", Title: "two"},
	}})
	joined := strings.Join(res.Problems, "; ")
	if !strings.Contains(joined, `duplicate step id "a" (steps 1 and 2)`) {
		t.Fatalf("problems %v do not report both duplicate occurrences", res.Problems)
	}
}

func TestCompilePlanDuplicateTitleNamesBothSteps(t *testing.T) {
	res := CompilePlan(PlanSpec{Title: "x", Steps: []PlanStepSpec{
		{ID: "a", Title: "Same"},
		{ID: "b", Title: "Same"},
	}})
	joined := strings.Join(res.Problems, "; ")
	if !strings.Contains(joined, `duplicate step title "Same" (steps "a" and "b")`) {
		t.Fatalf("problems %v do not name both steps sharing the title", res.Problems)
	}
}

func TestCompilePlanRejectsWhitespaceOnlyStepIDs(t *testing.T) {
	res := CompilePlan(PlanSpec{Title: "x", Steps: []PlanStepSpec{
		{ID: "   ", Title: "blank id"},
		{ID: "\t\n ", Title: "tabs and newlines"},
	}})
	if len(res.Problems) != 2 {
		t.Fatalf("problems = %v, want two whitespace-id problems", res.Problems)
	}
	for i, want := range []string{`step 1 has a whitespace-only id "   "`, `step 2 has a whitespace-only id "\t\n "`} {
		if res.Problems[i] != want {
			t.Errorf("problem %d = %q, want %q", i, res.Problems[i], want)
		}
	}
	if len(res.Waves) != 0 {
		t.Fatalf("produced a schedule despite whitespace-only ids: %v", res.Waves)
	}
	// An absent id (empty string) keeps the auto-assign contract.
	ok := CompilePlan(PlanSpec{Title: "y", Steps: []PlanStepSpec{{Title: "solo"}}})
	if len(ok.Problems) != 0 || len(ok.Tasks) != 1 || ok.Tasks[0].ID != "t1" {
		t.Fatalf("absent id should auto-assign: problems %v, tasks %v", ok.Problems, ok.Tasks)
	}
}

func TestCompilePlanSurfacesCyclePath(t *testing.T) {
	res := CompilePlan(PlanSpec{Title: "x", Steps: []PlanStepSpec{
		{ID: "a", Title: "a", After: []string{"b"}},
		{ID: "b", Title: "b", After: []string{"c"}},
		{ID: "c", Title: "c", After: []string{"a"}},
	}})
	joined := strings.Join(res.Problems, "; ")
	if !strings.Contains(joined, "dependency cycle: a → b → c → a") {
		t.Fatalf("problems %v do not surface the concrete cycle path", res.Problems)
	}
}

// --- Summary renderer ---

func TestCompileResultSummaryListsWavesAndDeps(t *testing.T) {
	res := CompilePlan(PlanSpec{Title: "s", Steps: []PlanStepSpec{
		{ID: "one", Title: "First"},
		{ID: "two", Title: "Second", After: []string{"one"}},
		{ID: "three", Title: "Third", After: []string{"one"}},
		{ID: "four", Title: "Fourth", After: []string{"two", "three"}},
	}})
	if len(res.Problems) != 0 {
		t.Fatalf("problems: %v", res.Problems)
	}
	s := res.Summary()
	for _, want := range []string{
		"plan: 4 step(s), 3 wave(s)",
		"wave 1: one (First)",
		"wave 2: two (Second) after: one",
		"wave 2: three (Third) after: one",
		"wave 3: four (Fourth) after: two, three",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("summary %q missing %q", s, want)
		}
	}
	// Deterministic: identical plans render identically.
	if again := CompilePlan(resToSpec(res)).Summary(); again != s {
		t.Fatalf("summary not deterministic:\n%q\n%q", s, again)
	}
}

// resToSpec rebuilds a spec from a compiled result so the determinism check
// exercises the same input shape twice.
func resToSpec(res CompileResult) PlanSpec {
	byID := map[string]Task{}
	for _, tk := range res.Tasks {
		byID[tk.ID] = tk
	}
	spec := PlanSpec{Title: "s"}
	for _, id := range res.Order {
		tk := byID[id]
		spec.Steps = append(spec.Steps, PlanStepSpec{ID: id, Title: tk.Title, After: tk.DependsOn})
	}
	return spec
}

func TestCompileResultSummaryListsProblemsWhenInvalid(t *testing.T) {
	bad := CompilePlan(PlanSpec{Steps: []PlanStepSpec{{Title: "a"}}})
	s := bad.Summary()
	if !strings.Contains(s, "plan invalid: 1 problem(s)") {
		t.Fatalf("summary %q does not report the problem count", s)
	}
	if !strings.Contains(s, "plan needs a title") {
		t.Fatalf("summary %q does not list the problem", s)
	}
	if strings.Contains(s, "wave") {
		t.Fatalf("summary %q must not show waves for an invalid plan", s)
	}
}
