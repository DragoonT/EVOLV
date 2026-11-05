package swarm

import "fmt"

// Unit is one piece of work the colony distributes. Tags describe the kind of
// work so strategies can steer it to matching workers; Weight is the unit's
// relative cost, where 0 counts as 1. The strategies in this file count
// units, not weight; Weight stays available to callers and future strategies.
type Unit struct {
	ID     string
	Tags   []string
	Weight float64
}

// EffectiveWeight returns the unit's weight with the 0 => 1 default applied.
func (u Unit) EffectiveWeight() float64 {
	if u.Weight <= 0 {
		return 1
	}
	return u.Weight
}

// Worker is one agent slot able to take work. Capacity counts units in flight
// (0 counts as 1); Load is work already committed before this pass.
type Worker struct {
	ID       string
	Capacity int
	Tags     []string
	Load     int
}

// EffectiveCapacity returns the worker's capacity with the 0 => 1 default
// applied, so a zero-value worker can still take one unit.
func (w Worker) EffectiveCapacity() int {
	if w.Capacity <= 0 {
		return 1
	}
	return w.Capacity
}

// Assignment pairs one unit with the worker that takes it.
type Assignment struct {
	UnitID   string
	WorkerID string
}

// Strategy plans work distribution for one batch. Implementations must be
// pure: never mutate the inputs and always return the same plan for the same
// slices. A unit that cannot be placed is left out of the plan, never failed;
// callers who need the guarantee run ValidateAssignments afterwards.
type Strategy interface {
	Name() string
	Assign(units []Unit, workers []Worker) []Assignment
}

// Assign runs one strategy over the whole batch. A nil strategy assigns
// nothing, so callers can wire one in without nil checks.
func Assign(units []Unit, workers []Worker, s Strategy) []Assignment {
	if s == nil {
		return []Assignment{}
	}
	return s.Assign(units, workers)
}

// ValidateAssignments checks that a plan is internally consistent: every
// assignment names a known unit and worker, no unit is assigned twice, and
// no worker ends above capacity (its pre-existing Load plus assigned units).
func ValidateAssignments(units []Unit, workers []Worker, assignments []Assignment) error {
	for _, a := range assignments {
		if stratIndexOfUnit(units, a.UnitID) < 0 {
			return fmt.Errorf("assignment references unknown unit %q", a.UnitID)
		}
		if stratIndexOfWorker(workers, a.WorkerID) < 0 {
			return fmt.Errorf("assignment references unknown worker %q", a.WorkerID)
		}
	}
	for i, a := range assignments {
		for _, prev := range assignments[:i] {
			if prev.UnitID == a.UnitID {
				return fmt.Errorf("unit %q is assigned more than once", a.UnitID)
			}
		}
	}
	for _, w := range workers {
		assigned := 0
		for _, a := range assignments {
			if a.WorkerID == w.ID {
				assigned++
			}
		}
		if w.Load+assigned > w.EffectiveCapacity() {
			return fmt.Errorf("worker %q would run %d units over capacity %d", w.ID, w.Load+assigned, w.EffectiveCapacity())
		}
	}
	return nil
}

// RoundRobinStrategy deals units to workers left to right, cycling back to
// the first worker whenever the cursor passes the end. Workers without
// remaining capacity are skipped mid-cycle; each call starts the cursor at
// the first worker, so identical slices always produce identical plans.
type RoundRobinStrategy struct{}

func (RoundRobinStrategy) Name() string { return "round-robin" }

func (RoundRobinStrategy) Assign(units []Unit, workers []Worker) []Assignment {
	slots := stratSlots(workers)
	out := make([]Assignment, 0, len(units))
	cursor := 0
	for _, u := range units {
		if len(slots) == 0 {
			break
		}
		for scanned := 0; scanned < len(slots); scanned++ {
			i := (cursor + scanned) % len(slots)
			if stratFree(slots[i]) <= 0 {
				continue
			}
			slots[i].taken++
			out = append(out, Assignment{UnitID: u.ID, WorkerID: slots[i].id})
			cursor = (i + 1) % len(slots)
			break
		}
	}
	return out
}

// LeastLoadedStrategy gives each unit to the worker with the lowest current
// load — its pre-existing Load plus units taken in this pass — breaking ties
// by worker slice order. Units beyond total capacity stay unassigned.
type LeastLoadedStrategy struct{}

func (LeastLoadedStrategy) Name() string { return "least-loaded" }

func (LeastLoadedStrategy) Assign(units []Unit, workers []Worker) []Assignment {
	slots := stratSlots(workers)
	out := make([]Assignment, 0, len(units))
	for _, u := range units {
		best := -1
		for i := range slots {
			if stratFree(slots[i]) <= 0 {
				continue
			}
			if best < 0 || stratTotal(slots[i]) < stratTotal(slots[best]) {
				best = i
			}
		}
		if best < 0 {
			continue
		}
		slots[best].taken++
		out = append(out, Assignment{UnitID: u.ID, WorkerID: slots[best].id})
	}
	return out
}

// TagMatchStrategy only sends a unit to workers sharing at least one tag
// with it; a unit with no non-empty tags may go anywhere. Among eligible
// workers with remaining capacity it picks the least loaded one, tie-break by
// worker slice order as LeastLoadedStrategy does. A unit whose tag matches no
// worker — or only full ones — gets no assignment.
type TagMatchStrategy struct{}

func (TagMatchStrategy) Name() string { return "tag-match" }

func (TagMatchStrategy) Assign(units []Unit, workers []Worker) []Assignment {
	slots := stratSlots(workers)
	out := make([]Assignment, 0, len(units))
	for _, u := range units {
		best := -1
		for i := range slots {
			if !stratTagMatch(u.Tags, slots[i].tags) {
				continue
			}
			if stratFree(slots[i]) <= 0 {
				continue
			}
			if best < 0 || stratTotal(slots[i]) < stratTotal(slots[best]) {
				best = i
			}
		}
		if best < 0 {
			continue
		}
		slots[best].taken++
		out = append(out, Assignment{UnitID: u.ID, WorkerID: slots[best].id})
	}
	return out
}

// stratSlot is a private, mutable copy of one worker used while planning, so
// the caller's slices are never written to.
type stratSlot struct {
	id       string
	tags     []string
	capacity int
	load     int
	taken    int
}

func stratSlots(workers []Worker) []stratSlot {
	slots := make([]stratSlot, len(workers))
	for i, w := range workers {
		slots[i] = stratSlot{
			id:       w.ID,
			tags:     append([]string(nil), w.Tags...),
			capacity: w.EffectiveCapacity(),
			load:     w.Load,
		}
	}
	return slots
}

func stratFree(s stratSlot) int  { return s.capacity - s.load - s.taken }
func stratTotal(s stratSlot) int { return s.load + s.taken }

// stratTagMatch reports whether unit and worker share at least one non-empty
// tag. Empty tags are ignored; a unit whose tags are all empty is untagged
// and matches every worker.
func stratTagMatch(unitTags, workerTags []string) bool {
	untagged := true
	for _, ut := range unitTags {
		if ut == "" {
			continue
		}
		untagged = false
		for _, wt := range workerTags {
			if ut == wt {
				return true
			}
		}
	}
	return untagged
}

func stratIndexOfUnit(units []Unit, id string) int {
	for i, u := range units {
		if u.ID == id {
			return i
		}
	}
	return -1
}

func stratIndexOfWorker(workers []Worker, id string) int {
	for i, w := range workers {
		if w.ID == id {
			return i
		}
	}
	return -1
}
