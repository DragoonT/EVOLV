package agent

import (
	"strings"

	"github.com/dxn111/EVOLV/internal/genome"
)

// The improvement loop.
//
// Recording outcomes is bookkeeping. The loop that actually makes EVOLV better
// over time is:
//
//	1. when a run_finish settles a task, capture HOW it was done
//	   (skills consulted, tools used, verify command, attempts, outcome),
//	2. derive traits from the task text,
//	3. next time a similar task appears, inject what previously worked.
//
// The result is an organism whose second attempt at a class of problem starts
// from evidence instead of from zero.

// sessionSkills / sessionTools track what the current turn consulted, so the
// playbook reflects the work rather than the model's memory of it.
func (a *Agent) noteConsultedSkill(name string) {
	a.mu.Lock()
	if a.consulted == nil {
		a.consulted = map[string]bool{}
	}
	a.consulted["skill:"+name] = true
	a.mu.Unlock()
}

func (a *Agent) noteUsedTool(name string) {
	a.mu.Lock()
	if a.consulted == nil {
		a.consulted = map[string]bool{}
	}
	a.consulted["tool:"+name] = true
	a.mu.Unlock()
}

// takeConsulted returns and clears the current consultation set.
func (a *Agent) takeConsulted() (skills, tools []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for key := range a.consulted {
		switch {
		case strings.HasPrefix(key, "skill:"):
			skills = append(skills, strings.TrimPrefix(key, "skill:"))
		case strings.HasPrefix(key, "tool:"):
			tools = append(tools, strings.TrimPrefix(key, "tool:"))
		}
	}
	a.consulted = nil
	return sortNames(skills), sortNames(tools)
}

// openBook loads the playbook collection.
func (a *Agent) openBook() (*genome.Book, error) {
	return genome.OpenBook(a.ws.Root)
}

// recordPlaybook captures one completed (or failed) task for later retrieval.
func (a *Agent) recordPlaybook(runID, taskID, title, verify string, attempts int, outcome string, durationMS int64) {
	book, err := a.openBook()
	if err != nil {
		return
	}
	skills, tools := a.takeConsulted()
	p := genome.Playbook{
		Title: title, Traits: genome.Traits(title + " " + verify),
		Skills: skills, Tools: tools, Verify: verify, Attempts: attempts,
		Outcome: outcome, DurationMS: durationMS, Run: runID, Task: taskID,
		At: nowISO(),
	}
	_ = book.Record(p)
}

// lessonsFor returns prompt-ready advice for a task's traits.
func (a *Agent) lessonsFor(text string) string {
	book, err := a.openBook()
	if err != nil {
		return ""
	}
	traits := genome.Traits(text)
	if len(traits) == 0 {
		return ""
	}
	return genome.RenderAdvice(book.InsightsFor(traits, 3))
}
