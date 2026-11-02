package execution

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Graph is a dependency-aware set of tasks. Everything is computed from a
// snapshot slice so the store can call it without holding locks.
type Graph struct{ Tasks []Task }

// Index maps task ids to positions.
func (g Graph) Index() map[string]int {
	m := make(map[string]int, len(g.Tasks))
	for i, t := range g.Tasks {
		m[t.ID] = i
	}
	return m
}

// Get returns the task with id.
func (g Graph) Get(id string) (Task, bool) {
	for _, t := range g.Tasks {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

// Dependents returns ids that directly depend on id.
func (g Graph) Dependents(id string) []string {
	out := []string{}
	for _, t := range g.Tasks {
		for _, dep := range t.DependsOn {
			if dep == id {
				out = append(out, t.ID)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// Ready lists tasks whose dependencies are all completed and which are queued
// or paused, ordered by priority (higher first) then creation order.
func (g Graph) Ready() []Task {
	idx := g.Index()
	ready := make([]Task, 0, len(g.Tasks))
	for _, t := range g.Tasks {
		if t.Status != StatusQueued && t.Status != StatusPaused {
			continue
		}
		ok := true
		for _, dep := range t.DependsOn {
			pos, exists := idx[dep]
			if !exists {
				continue // dangling dependency: treated as satisfied, reported elsewhere
			}
			if g.Tasks[pos].Status != StatusCompleted {
				ok = false
				break
			}
		}
		if ok {
			ready = append(ready, t)
		}
	}
	sort.SliceStable(ready, func(i, j int) bool {
		if ready[i].Priority != ready[j].Priority {
			return ready[i].Priority > ready[j].Priority
		}
		return ready[i].CreatedAt < ready[j].CreatedAt
	})
	return ready
}

// Validate checks the whole graph: unknown dependencies, duplicate ids,
// self-dependencies and cycles. It returns every problem found, not just the
// first, so the model can repair its plan in one attempt.
func (g Graph) Validate() []string {
	var problems []string
	ids := map[string]int{}
	for _, t := range g.Tasks {
		if strings.TrimSpace(t.ID) == "" {
			problems = append(problems, "task with empty id")
			continue
		}
		ids[t.ID]++
		self := map[string]bool{}
		for _, dep := range t.DependsOn {
			if dep == t.ID && !self[dep] {
				self[dep] = true
				problems = append(problems, "task "+strconv.Quote(t.ID)+" depends on itself")
			}
		}
	}
	for id, count := range ids {
		if count > 1 {
			problems = append(problems, fmt.Sprintf("duplicate task id %q", id))
		}
	}
	for _, t := range g.Tasks {
		for _, dep := range t.DependsOn {
			if dep == t.ID {
				continue // already reported above
			}
			if _, ok := ids[dep]; !ok {
				problems = append(problems, fmt.Sprintf("task %q depends on unknown task %q", t.ID, dep))
			}
		}
	}
	if cycle := g.Cycle(); len(cycle) > 0 {
		problems = append(problems, "dependency cycle: "+strings.Join(cycle, " → "))
	}
	return problems
}

// Cycle returns one dependency cycle, or nil when the graph is acyclic.
func (g Graph) Cycle() []string {
	const (
		white = 0 // unvisited
		gray  = 1 // on the current path
		black = 2 // finished
	)
	color := make(map[string]int, len(g.Tasks))
	byID := make(map[string]Task, len(g.Tasks))
	for _, t := range g.Tasks {
		byID[t.ID] = t
	}
	var path []string
	var visit func(id string) []string
	visit = func(id string) []string {
		switch color[id] {
		case gray:
			// found a back edge: cut the cycle out of the current path
			for i, p := range path {
				if p == id {
					out := append([]string{}, path[i:]...)
					return append(out, id)
				}
			}
			return []string{id, id}
		case black:
			return nil
		}
		color[id] = gray
		path = append(path, id)
		t, ok := byID[id]
		if ok {
			deps := append([]string{}, t.DependsOn...)
			sort.Strings(deps)
			for _, dep := range deps {
				if _, exists := byID[dep]; !exists {
					continue
				}
				if cycle := visit(dep); cycle != nil {
					return cycle
				}
			}
		}
		path = path[:len(path)-1]
		color[id] = black
		return nil
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if cycle := visit(id); cycle != nil {
			return cycle
		}
	}
	return nil
}

// Blocked lists tasks that can never run because a dependency failed or was
// cancelled. They are reported as blocked rather than silently stuck.
func (g Graph) Blocked() []string {
	idx := g.Index()
	var out []string
	for _, t := range g.Tasks {
		if t.Status.Terminal() {
			continue
		}
		for _, dep := range t.DependsOn {
			pos, ok := idx[dep]
			if !ok {
				continue
			}
			switch g.Tasks[pos].Status {
			case StatusFailed, StatusCancelled:
				out = append(out, t.ID)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Progress summarises completion.
type Progress struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Running   int `json:"running"`
	Queued    int `json:"queued"`
}

// Progress counts task states.
func (g Graph) Progress() Progress {
	var p Progress
	for _, t := range g.Tasks {
		p.Total++
		switch t.Status {
		case StatusCompleted:
			p.Completed++
		case StatusFailed:
			p.Failed++
		case StatusRunning, StatusVerifying, StatusPlanning:
			p.Running++
		case StatusQueued, StatusPaused, StatusWaiting:
			p.Queued++
		}
	}
	return p
}

// Conflicts returns pairs of ready tasks that plan to touch the same file.
// Callers use this to avoid running two writers against one file at once.
func (g Graph) Conflicts(ready []Task) [][2]string {
	var pairs [][2]string
	for i := 0; i < len(ready); i++ {
		for j := i + 1; j < len(ready); j++ {
			if sharesFile(ready[i], ready[j]) {
				pairs = append(pairs, [2]string{ready[i].ID, ready[j].ID})
			}
		}
	}
	return pairs
}

func sharesFile(a, b Task) bool {
	if len(a.FilesPlan) == 0 || len(b.FilesPlan) == 0 {
		return false
	}
	seen := make(map[string]bool, len(a.FilesPlan))
	for _, f := range a.FilesPlan {
		seen[strings.TrimSpace(f)] = true
	}
	for _, f := range b.FilesPlan {
		if seen[strings.TrimSpace(f)] {
			return true
		}
	}
	return false
}
