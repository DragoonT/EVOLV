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
	byTitle := map[string]string{}
	byID := map[string]bool{}
	ids := make([]string, len(spec.Steps))
	for i, step := range spec.Steps {
		id := strings.TrimSpace(step.ID)
		if id == "" {
			id = fmt.Sprintf("t%d", i+1)
		}
		if byID[id] {
			res.Problems = append(res.Problems, "duplicate step id "+id)
			id = fmt.Sprintf("%s-%d", id, i+1)
		}
		byID[id] = true
		ids[i] = id
		title := strings.ToLower(strings.TrimSpace(step.Title))
		if title != "" {
			if prev, exists := byTitle[title]; exists && prev != id {
				res.Problems = append(res.Problems, "duplicate step title: "+step.Title)
			}
			byTitle[title] = id
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for i, step := range spec.Steps {
		title := strings.TrimSpace(step.Title)
		if title == "" {
			res.Problems = append(res.Problems, ids[i]+" has no title")
			title = ids[i]
		}
		var deps []string
		for _, ref := range step.After {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			target := ref
			if !byID[target] {
				if id, ok := byTitle[strings.ToLower(ref)]; ok {
					target = id
				} else {
					res.Problems = append(res.Problems, fmt.Sprintf("%s depends on unknown step %q", ids[i], ref))
					continue
				}
			}
			if target == ids[i] {
				res.Problems = append(res.Problems, ids[i]+" depends on itself")
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
				problems = append(problems, fmt.Sprintf("%s waits on missing task %q", t.ID, dep))
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
