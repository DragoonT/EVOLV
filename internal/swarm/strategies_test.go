package swarm

import (
	"reflect"
	"strings"
	"testing"
)

func TestStrategyNames(t *testing.T) {
	if name := (RoundRobinStrategy{}).Name(); name != "round-robin" {
		t.Fatalf("round robin name %q", name)
	}
	if name := (LeastLoadedStrategy{}).Name(); name != "least-loaded" {
		t.Fatalf("least loaded name %q", name)
	}
	if name := (TagMatchStrategy{}).Name(); name != "tag-match" {
		t.Fatalf("tag match name %q", name)
	}
}

func TestStrategyDefaultsApply(t *testing.T) {
	if got := (Unit{}).EffectiveWeight(); got != 1 {
		t.Fatalf("zero weight => %v, want 1", got)
	}
	if got := (Unit{Weight: -3}).EffectiveWeight(); got != 1 {
		t.Fatalf("negative weight => %v, want 1", got)
	}
	if got := (Unit{Weight: 2.5}).EffectiveWeight(); got != 2.5 {
		t.Fatalf("weight 2.5 => %v, want 2.5", got)
	}
	if got := (Worker{}).EffectiveCapacity(); got != 1 {
		t.Fatalf("zero capacity => %d, want 1", got)
	}
	if got := (Worker{Capacity: -2}).EffectiveCapacity(); got != 1 {
		t.Fatalf("negative capacity => %d, want 1", got)
	}
	if got := (Worker{Capacity: 3}).EffectiveCapacity(); got != 3 {
		t.Fatalf("capacity 3 => %d, want 3", got)
	}
}

func TestRoundRobinStrategyCyclesDeterministically(t *testing.T) {
	// Three single-slot workers: the first three units fill them in slice
	// order and the rest find nothing free.
	got := RoundRobinStrategy{}.Assign(
		stratUnits("u1", "u2", "u3", "u4", "u5"),
		[]Worker{{ID: "w1"}, {ID: "w2"}, {ID: "w3"}},
	)
	if want := "u1->w1,u2->w2,u3->w3"; stratPairs(got) != want {
		t.Fatalf("round robin fixture: got %s, want %s", stratPairs(got), want)
	}

	// Two workers with room for two: the cursor cycles back after the end.
	got = RoundRobinStrategy{}.Assign(
		stratUnits("u1", "u2", "u3", "u4"),
		[]Worker{{ID: "w1", Capacity: 2}, {ID: "w2", Capacity: 2}},
	)
	if want := "u1->w1,u2->w2,u3->w1,u4->w2"; stratPairs(got) != want {
		t.Fatalf("round robin wrap: got %s, want %s", stratPairs(got), want)
	}

	// Pre-existing load consumes capacity before the cursor ever reaches it.
	got = RoundRobinStrategy{}.Assign(
		stratUnits("u1", "u2", "u3", "u4"),
		[]Worker{{ID: "w1", Capacity: 2, Load: 1}, {ID: "w2", Capacity: 2}},
	)
	if want := "u1->w1,u2->w2,u3->w2"; stratPairs(got) != want {
		t.Fatalf("round robin with load: got %s, want %s", stratPairs(got), want)
	}

	// Same slices in, same plan out — every time.
	units := stratUnits("u1", "u2", "u3")
	workers := []Worker{{ID: "w1", Capacity: 1}, {ID: "w2", Capacity: 1}}
	first := RoundRobinStrategy{}.Assign(units, workers)
	for i := 0; i < 5; i++ {
		again := RoundRobinStrategy{}.Assign(units, workers)
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("round robin not deterministic: %s vs %s", stratPairs(first), stratPairs(again))
		}
	}
}

func TestRoundRobinStrategyRespectsCapacity(t *testing.T) {
	units := stratUnits("u1", "u2", "u3", "u4")
	workers := []Worker{{ID: "w1", Capacity: 1}, {ID: "w2", Capacity: 2, Load: 1}}
	got := RoundRobinStrategy{}.Assign(units, workers)
	stratAssertWithinCapacity(t, units, workers, got)
	if want := "u1->w1,u2->w2"; stratPairs(got) != want {
		t.Fatalf("got %s, want %s", stratPairs(got), want)
	}
}

func TestLeastLoadedStrategyPrefersEmptyWorker(t *testing.T) {
	// The worker with no load wins even though the other comes first.
	got := LeastLoadedStrategy{}.Assign(
		stratUnits("u1"),
		[]Worker{{ID: "w1", Capacity: 5, Load: 2}, {ID: "w2", Capacity: 5}},
	)
	if want := "u1->w2"; stratPairs(got) != want {
		t.Fatalf("empty worker first: got %s, want %s", stratPairs(got), want)
	}

	// Equal loads tie-break by worker slice order.
	got = LeastLoadedStrategy{}.Assign(
		stratUnits("u1"),
		[]Worker{{ID: "w1", Capacity: 5, Load: 3}, {ID: "w2", Capacity: 5, Load: 3}},
	)
	if want := "u1->w1"; stratPairs(got) != want {
		t.Fatalf("tie-break by order: got %s, want %s", stratPairs(got), want)
	}

	// Units accumulate: the plan spreads evenly instead of stacking one worker.
	got = LeastLoadedStrategy{}.Assign(
		stratUnits("u1", "u2", "u3", "u4"),
		[]Worker{{ID: "w1", Capacity: 2}, {ID: "w2", Capacity: 2}},
	)
	if want := "u1->w1,u2->w2,u3->w1,u4->w2"; stratPairs(got) != want {
		t.Fatalf("even spread: got %s, want %s", stratPairs(got), want)
	}
}

func TestLeastLoadedStrategyRespectsCapacity(t *testing.T) {
	units := stratUnits("u1", "u2", "u3", "u4")
	workers := []Worker{{ID: "w1", Capacity: 1}, {ID: "w2", Capacity: 2, Load: 1}}
	got := LeastLoadedStrategy{}.Assign(units, workers)
	stratAssertWithinCapacity(t, units, workers, got)
	if want := "u1->w1,u2->w2"; stratPairs(got) != want {
		t.Fatalf("got %s, want %s", stratPairs(got), want)
	}
}

func TestTagMatchStrategyExcludesNonMatchingWorkers(t *testing.T) {
	units := []Unit{
		{ID: "render", Tags: []string{"gpu"}},
		{ID: "lint", Tags: []string{"cpu", "fast"}},
		{ID: "plain"},
	}
	workers := []Worker{{ID: "gpu1", Tags: []string{"gpu"}, Capacity: 2}, {ID: "cpu1", Tags: []string{"cpu"}, Capacity: 2}}
	got := TagMatchStrategy{}.Assign(units, workers)
	// render only fits gpu1; lint only cpu1; plain is untagged so it lands on
	// the least loaded worker (both carry one unit, slice order picks gpu1).
	if want := "render->gpu1,lint->cpu1,plain->gpu1"; stratPairs(got) != want {
		t.Fatalf("got %s, want %s", stratPairs(got), want)
	}
	stratAssertWithinCapacity(t, units, workers, got)

	// A tag that matches no worker leaves the unit unassigned.
	got = TagMatchStrategy{}.Assign(
		[]Unit{{ID: "mystery", Tags: []string{"npu"}}},
		workers,
	)
	if len(got) != 0 {
		t.Fatalf("unit with unknown tag assigned: %s", stratPairs(got))
	}

	// A full matching worker is not eligible; an untagged unit still fits elsewhere.
	full := []Worker{{ID: "gpu1", Tags: []string{"gpu"}, Capacity: 1, Load: 1}, {ID: "cpu1", Tags: []string{"cpu"}}}
	got = TagMatchStrategy{}.Assign(
		[]Unit{{ID: "render", Tags: []string{"gpu"}}, {ID: "plain"}},
		full,
	)
	if want := "plain->cpu1"; stratPairs(got) != want {
		t.Fatalf("full match skipped: got %s, want %s", stratPairs(got), want)
	}
}

func TestTagMatchStrategyLeavesUnmatchedUnitsUnassigned(t *testing.T) {
	units := stratUnits("a", "b", "c")
	for i := range units {
		units[i].Tags = []string{"accelerator"}
	}
	got := TagMatchStrategy{}.Assign(units, []Worker{{ID: "w1", Tags: []string{"cpu"}}})
	if len(got) != 0 {
		t.Fatalf("no worker shares the tag, got assignments %s", stratPairs(got))
	}
	stratAssertWithinCapacity(t, units, []Worker{{ID: "w1", Tags: []string{"cpu"}}}, got)
}

func TestTagMatchStrategyRespectsCapacity(t *testing.T) {
	units := []Unit{
		{ID: "a", Tags: []string{"cpu"}},
		{ID: "b", Tags: []string{"cpu"}},
		{ID: "c", Tags: []string{"cpu"}},
		{ID: "d", Tags: []string{"cpu"}},
	}
	workers := []Worker{{ID: "w1", Tags: []string{"cpu"}, Capacity: 1}, {ID: "w2", Tags: []string{"cpu"}, Capacity: 2, Load: 1}}
	got := TagMatchStrategy{}.Assign(units, workers)
	stratAssertWithinCapacity(t, units, workers, got)
	if len(got) != 2 {
		t.Fatalf("expected exactly the two free slots used, got %s", stratPairs(got))
	}
}

func TestStrategiesRespectCapacity(t *testing.T) {
	units := stratUnits("u1", "u2", "u3", "u4", "u5")
	workers := []Worker{{ID: "w1", Capacity: 1}, {ID: "w2", Capacity: 2, Load: 1}}
	for _, s := range []Strategy{RoundRobinStrategy{}, LeastLoadedStrategy{}, TagMatchStrategy{}} {
		got := s.Assign(units, workers)
		if len(got) != 2 {
			t.Fatalf("%s: want exactly the 2 free slots used, got %d (%s)", s.Name(), len(got), stratPairs(got))
		}
		stratAssertWithinCapacity(t, units, workers, got)
	}
}

func TestAssignHelperDispatchesToStrategies(t *testing.T) {
	units := stratUnits("u1", "u2")
	workers := []Worker{{ID: "w1"}, {ID: "w2"}}
	for _, s := range []Strategy{RoundRobinStrategy{}, LeastLoadedStrategy{}, TagMatchStrategy{}} {
		viaHelper := Assign(units, workers, s)
		direct := s.Assign(units, workers)
		if !reflect.DeepEqual(viaHelper, direct) {
			t.Fatalf("%s: helper %s != direct %s", s.Name(), stratPairs(viaHelper), stratPairs(direct))
		}
	}
	if got := Assign(units, workers, nil); len(got) != 0 {
		t.Fatalf("nil strategy should assign nothing, got %s", stratPairs(got))
	}
}

func TestValidateAssignmentsCatchesViolations(t *testing.T) {
	units := stratUnits("u1", "u2")
	workers := []Worker{{ID: "w1", Capacity: 1}, {ID: "w2", Capacity: 2, Load: 1}}

	if err := ValidateAssignments(units, workers, []Assignment{{UnitID: "u1", WorkerID: "w1"}, {UnitID: "u2", WorkerID: "w2"}}); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}

	cases := []struct {
		name  string
		plan  []Assignment
		whose string
	}{
		{"unknown unit", []Assignment{{UnitID: "ghost", WorkerID: "w1"}}, "ghost"},
		{"unknown worker", []Assignment{{UnitID: "u1", WorkerID: "nowhere"}}, "nowhere"},
		{"duplicate unit", []Assignment{{UnitID: "u1", WorkerID: "w1"}, {UnitID: "u1", WorkerID: "w2"}}, "u1"},
		{"over capacity", []Assignment{{UnitID: "u1", WorkerID: "w1"}, {UnitID: "u2", WorkerID: "w1"}}, "w1"},
	}
	for _, tc := range cases {
		err := ValidateAssignments(units, workers, tc.plan)
		if err == nil {
			t.Fatalf("%s: accepted", tc.name)
		}
		if !strings.Contains(err.Error(), tc.whose) {
			t.Fatalf("%s: error %q does not name %q", tc.name, err, tc.whose)
		}
	}

	// Pre-existing load counts: w2 sits at Load 1 with capacity 2, so one more
	// unit is fine and a second one overflows.
	if err := ValidateAssignments(units, workers, []Assignment{{UnitID: "u1", WorkerID: "w2"}, {UnitID: "u2", WorkerID: "w2"}}); err == nil {
		t.Fatal("w2 double-assigned past its capacity: accepted")
	}
}

func TestValidateAssignmentsAcceptsPlanAtCapacity(t *testing.T) {
	units := stratUnits("u1", "u2", "u3")
	workers := []Worker{{ID: "w1", Capacity: 2}, {ID: "w2"}}
	plan := []Assignment{{UnitID: "u1", WorkerID: "w1"}, {UnitID: "u2", WorkerID: "w1"}, {UnitID: "u3", WorkerID: "w2"}}
	if err := ValidateAssignments(units, workers, plan); err != nil {
		t.Fatalf("plan exactly at capacity rejected: %v", err)
	}
}

func TestStrategiesDoNotMutateInputs(t *testing.T) {
	units := []Unit{
		{ID: "u1", Tags: []string{"gpu", "urgent"}, Weight: 2},
		{ID: "u2", Tags: []string{"cpu"}},
		{ID: "u3", Tags: []string{"gpu"}, Weight: 0},
	}
	workers := []Worker{
		{ID: "w1", Capacity: 1, Tags: []string{"gpu"}, Load: 0},
		{ID: "w2", Capacity: 2, Tags: []string{"cpu"}, Load: 1},
	}
	unitsCopy := stratCloneUnits(units)
	workersCopy := stratCloneWorkers(workers)
	for _, s := range []Strategy{RoundRobinStrategy{}, LeastLoadedStrategy{}, TagMatchStrategy{}} {
		_ = Assign(units, workers, s)
		if !reflect.DeepEqual(units, unitsCopy) {
			t.Fatalf("%s mutated units", s.Name())
		}
		if !reflect.DeepEqual(workers, workersCopy) {
			t.Fatalf("%s mutated workers", s.Name())
		}
	}
}

func TestStrategiesAreDeterministic(t *testing.T) {
	units := stratUnits("u1", "u2", "u3", "u4")
	workers := []Worker{
		{ID: "w1", Capacity: 2, Tags: []string{"gpu"}, Load: 1},
		{ID: "w2", Capacity: 3, Tags: []string{"cpu"}},
	}
	for _, s := range []Strategy{RoundRobinStrategy{}, LeastLoadedStrategy{}, TagMatchStrategy{}} {
		first := s.Assign(units, workers)
		for i := 0; i < 3; i++ {
			again := s.Assign(units, workers)
			if !reflect.DeepEqual(first, again) {
				t.Fatalf("%s not deterministic: %s vs %s", s.Name(), stratPairs(first), stratPairs(again))
			}
		}
	}
}

func TestStrategiesHandleNilAndEmptyInputs(t *testing.T) {
	strategies := []Strategy{RoundRobinStrategy{}, LeastLoadedStrategy{}, TagMatchStrategy{}, nil}
	full := stratUnits("u1", "u2")
	workers := []Worker{{ID: "w1"}, {ID: "w2"}}

	for _, s := range strategies {
		for _, units := range [][]Unit{nil, {}} {
			for _, ws := range [][]Worker{nil, {}} {
				got := Assign(units, ws, s)
				if len(got) != 0 {
					t.Fatalf("Assign(%v, %v, %T) = %s, want empty", units, ws, s, stratPairs(got))
				}
				if s == nil {
					continue
				}
				if direct := s.Assign(units, ws); len(direct) != 0 {
					t.Fatalf("%s.Assign(%v, %v) = %s, want empty", s.Name(), units, ws, stratPairs(direct))
				}
			}
		}
	}

	// More workers than units is fine too: only the units that exist get placed.
	if got := Assign(full[:1], workers, RoundRobinStrategy{}); stratPairs(got) != "u1->w1" {
		t.Fatalf("fewer units than workers: got %s", stratPairs(got))
	}
	if err := ValidateAssignments(nil, nil, nil); err != nil {
		t.Fatalf("validating empty everything: %v", err)
	}
}

// stratUnits builds bare units from ids, keeping the fixtures terse.
func stratUnits(ids ...string) []Unit {
	units := make([]Unit, len(ids))
	for i, id := range ids {
		units[i] = Unit{ID: id}
	}
	return units
}

// stratPairs renders a plan as "u1->w1,u2->w2" so failures read as fixtures.
func stratPairs(assignments []Assignment) string {
	parts := make([]string, len(assignments))
	for i, a := range assignments {
		parts[i] = a.UnitID + "->" + a.WorkerID
	}
	return strings.Join(parts, ",")
}

func stratCloneUnits(units []Unit) []Unit {
	out := make([]Unit, len(units))
	for i, u := range units {
		out[i] = u
		out[i].Tags = append([]string(nil), u.Tags...)
	}
	return out
}

func stratCloneWorkers(workers []Worker) []Worker {
	out := make([]Worker, len(workers))
	for i, w := range workers {
		out[i] = w
		out[i].Tags = append([]string(nil), w.Tags...)
	}
	return out
}

// stratAssertWithinCapacity checks the plan twice: through
// ValidateAssignments and through a plain count per worker.
func stratAssertWithinCapacity(t *testing.T, units []Unit, workers []Worker, plan []Assignment) {
	t.Helper()
	if err := ValidateAssignments(units, workers, plan); err != nil {
		t.Fatalf("plan %s invalid: %v", stratPairs(plan), err)
	}
	counts := map[string]int{}
	for _, a := range plan {
		counts[a.WorkerID]++
	}
	for _, w := range workers {
		if w.Load+counts[w.ID] > w.EffectiveCapacity() {
			t.Fatalf("worker %s at %d/%d after plan %s", w.ID, w.Load+counts[w.ID], w.EffectiveCapacity(), stratPairs(plan))
		}
	}
}
