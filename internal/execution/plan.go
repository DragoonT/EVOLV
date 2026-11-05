package execution

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// PlanCompiler turns a written plan into a validated task graph.
//
// It exists so a mission can be decomposed deterministically — by the model,
// by a swarm, or by a human in markdown — and then checked *before* any work
// starts. A plan that cannot be scheduled is rejected with every problem at
// once, which is far cheaper than discovering the cycle after three tasks.

// PlanStepSpec is one line of an incoming plan.
type PlanStepSpec struct {
	ID        string   `json:"id,omitempty"`
	Title     string   `json:"title"`
	Objective string   `json:"objective,omitempty"`
	After     []string `json:"after,omitempty"`  // dependency ids or titles
	Files     []string `json:"files,omitempty"`  // files this step will touch
	Verify    string   `json:"verify,omitempty"` // command that proves the step
	Phase     string   `json:"phase,omitempty"`
	Priority  int      `json:"priority,omitempty"`
}

// PlanSpec is a whole plan as submitted by a client or the model.
type PlanSpec struct {
	Title     string         `json:"title"`
	Objective string         `json:"objective,omitempty"`
	Steps     []PlanStepSpec `json:"steps"`
}

// CompileResult reports what the compiler produced and refused.
type CompileResult struct {
	Tasks    []Task     `json:"tasks"`
	Problems []string   `json:"problems,omitempty"`
	Order    []string   `json:"execution_order,omitempty"`
	Waves    [][]string `json:"waves,omitempty"`
}

// CompilePlan converts a plan into tasks, resolving dependencies declared by
// id or by title, de-duplicating ids, and reporting every problem.
func CompilePlan(spec PlanSpec) CompileResult {
	res := CompileResult{Tasks: []Task{}}
	if strings.TrimSpace(spec.Title) == "" {
		res.Problems = append(res.Problems, "plan needs a title")
	}
	if len(spec.Steps) == 0 {
		res.Problems = append(res.Problems, "plan needs at least one step")
		return res
	}
	if len(spec.Steps) > 64 {
		res.Problems = append(res.Problems, fmt.Sprintf("plan has %d steps; the limit is 64", len(spec.Steps)))
		return res
	}

	// ids: explicit, else t1..tN. Titles are also valid references.
	// An absent id ("") keeps the legacy behaviour of auto-assigning t1..tN;
	// an explicitly whitespace-only id is rejected: the author clearly meant
	// to name the step, so silently renaming it would hide the mistake.
	byTitle := map[string]string{}
	byID := map[string]int{} // id -> index of first occurrence
	ids := make([]string, len(spec.Steps))
	for i, step := range spec.Steps {
		id := strings.TrimSpace(step.ID)
		switch {
		case step.ID == "":
			id = fmt.Sprintf("t%d", i+1)
		case id == "":
			res.Problems = append(res.Problems, fmt.Sprintf("step %d has a whitespace-only id %q", i+1, step.ID))
			id = fmt.Sprintf("t%d", i+1)
		}
		if first, dup := byID[id]; dup {
			res.Problems = append(res.Problems, fmt.Sprintf("duplicate step id %q (steps %d and %d)", id, first+1, i+1))
			id = fmt.Sprintf("%s-%d", id, i+1)
		}
		byID[id] = i
		ids[i] = id
		title := strings.ToLower(strings.TrimSpace(step.Title))
		if title != "" {
			if prev, exists := byTitle[title]; exists && prev != id {
				res.Problems = append(res.Problems, fmt.Sprintf("duplicate step title %q (steps %q and %q)", step.Title, prev, id))
			}
			byTitle[title] = id
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i, step := range spec.Steps {
		title := strings.TrimSpace(step.Title)
		if title == "" {
			res.Problems = append(res.Problems, fmt.Sprintf("step %q has no title", ids[i]))
			title = ids[i]
		}
		var deps []string
		for _, ref := range step.After {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			target := ref
			if _, known := byID[target]; !known {
				if id, ok := byTitle[strings.ToLower(ref)]; ok {
					target = id
				} else {
					res.Problems = append(res.Problems, fmt.Sprintf("step %q depends on unknown step %q", ids[i], ref))
					continue
				}
			}
			if target == ids[i] {
				res.Problems = append(res.Problems, fmt.Sprintf("step %q depends on itself", ids[i]))
				continue
			}
			deps = append(deps, target)
		}
		if len(deps) > 0 {
			deps = normaliseIDs(ids[i], deps)
		}
		res.Tasks = append(res.Tasks, Task{
			ID: ids[i], Title: title, Objective: strings.TrimSpace(step.Objective),
			Status: StatusQueued, DependsOn: deps, Phase: strings.TrimSpace(step.Phase),
			Priority: step.Priority, FilesPlan: normaliseIDs("", step.Files),
			CreatedAt: now, UpdatedAt: now,
		})
	}

	graph := Graph{Tasks: res.Tasks}
	res.Problems = append(res.Problems, graph.Validate()...)
	res.Problems = append(res.Problems, scheduleProblems(graph)...)
	if len(res.Problems) == 0 {
		res.Waves = Schedule(graph)
		res.Order = flattenWaves(res.Waves)
	}
	return res
}

// scheduleProblems catches plans that validate structurally but cannot be
// executed sanely, e.g. a dependency on a step that never runs.
func scheduleProblems(g Graph) []string {
	var problems []string
	done := map[string]bool{}
	for _, t := range g.Tasks {
		for _, dep := range t.DependsOn {
			if done[dep] {
				continue
			}
			if _, ok := g.Get(dep); !ok {
				problems = append(problems, fmt.Sprintf("step %q waits on missing task %q", t.ID, dep))
			}
		}
		done[t.ID] = true
	}
	return problems
}

// Schedule groups tasks into waves: every task in wave N can run concurrently
// because all of its dependencies are in earlier waves.
func Schedule(g Graph) [][]string {
	remaining := map[string]Task{}
	completed := map[string]bool{}
	for _, t := range g.Tasks {
		remaining[t.ID] = t
	}
	var waves [][]string
	for len(remaining) > 0 {
		var ready []Task
		for _, t := range remaining {
			ok := true
			for _, dep := range t.DependsOn {
				if !completed[dep] {
					ok = false
					break
				}
			}
			if ok {
				ready = append(ready, t)
			}
		}
		if len(ready) == 0 {
			// Unschedulable remainder (cycle or dangling dep): report what is
			// left in one final wave so the caller can see it.
			var stuck []string
			for id := range remaining {
				stuck = append(stuck, id)
			}
			sort.Strings(stuck)
			waves = append(waves, stuck)
			break
		}
		sort.Slice(ready, func(i, j int) bool {
			if ready[i].Priority != ready[j].Priority {
				return ready[i].Priority > ready[j].Priority
			}
			return ready[i].ID < ready[j].ID
		})
		wave := make([]string, 0, len(ready))
		for _, t := range ready {
			wave = append(wave, t.ID)
			completed[t.ID] = true
			delete(remaining, t.ID)
		}
		waves = append(waves, wave)
	}
	return waves
}

func flattenWaves(waves [][]string) []string {
	var out []string
	for _, w := range waves {
		out = append(out, w...)
	}
	return out
}

// Summary renders a compiled plan as a compact, deterministic listing, one
// line per step with its 1-based wave number and dependencies — suitable for
// logs and human review. When compilation produced problems, the summary
// lists those instead of a schedule (a plan with problems is never scheduled).
func (r CompileResult) Summary() string {
	var b strings.Builder
	if len(r.Problems) > 0 {
		fmt.Fprintf(&b, "plan invalid: %d problem(s)", len(r.Problems))
		for _, p := range r.Problems {
			fmt.Fprintf(&b, "\n  - %s", p)
		}
		return b.String()
	}
	byID := make(map[string]Task, len(r.Tasks))
	for _, t := range r.Tasks {
		byID[t.ID] = t
	}
	fmt.Fprintf(&b, "plan: %d step(s), %d wave(s)", len(r.Tasks), len(r.Waves))
	if len(r.Waves) == 0 {
		// No schedule was computed; list the steps without wave numbers.
		for _, t := range r.Tasks {
			writeSummaryStep(&b, 0, t)
		}
		return b.String()
	}
	for i, wave := range r.Waves {
		for _, id := range wave {
			t, ok := byID[id]
			if !ok {
				// Wave entry with no matching task (hand-built result):
				// still list it so the summary never loses a step.
				writeSummaryStep(&b, i+1, Task{ID: id, Title: id})
				continue
			}
			writeSummaryStep(&b, i+1, t)
		}
	}
	return b.String()
}

// writeSummaryStep appends one "  wave N: id (title; after: ...)" line.
// wave 0 means "no wave assigned".
func writeSummaryStep(b *strings.Builder, wave int, t Task) {
	if wave > 0 {
		fmt.Fprintf(b, "\n  wave %d: %s (%s)", wave, t.ID, t.Title)
	} else {
		fmt.Fprintf(b, "\n  step %s (%s)", t.ID, t.Title)
	}
	if len(t.DependsOn) > 0 {
		fmt.Fprintf(b, " after: %s", strings.Join(t.DependsOn, ", "))
	}
}
