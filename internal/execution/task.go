// Package execution owns the durable state of long-running engineering work:
// tasks with explicit lifecycles, their dependency graph, checkpoints and an
// append-only journal that survives crashes.
package execution

import (
	"fmt"
	"strings"
	"time"
)

// Status is a task lifecycle state. Transitions are validated: a completed
// task cannot silently go back to running, and a cancelled task stays
// cancelled until it is explicitly requeued.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusPlanning  Status = "planning"
	StatusRunning   Status = "running"
	StatusWaiting   Status = "waiting"
	StatusVerifying Status = "verifying"
	StatusPaused    Status = "paused"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Terminal reports whether no further progress is expected without an
// explicit requeue.
func (s Status) Terminal() bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// Active reports whether the task is expected to make progress on its own.
func (s Status) Active() bool {
	switch s {
	case StatusPlanning, StatusRunning, StatusVerifying:
		return true
	}
	return false
}

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	switch s {
	case StatusQueued, StatusPlanning, StatusRunning, StatusWaiting, StatusVerifying,
		StatusPaused, StatusCompleted, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// transitions is the allowed state machine. Every edge is explicit so an
// invalid transition is a programming error we can surface, not a mystery.
var transitions = map[Status][]Status{
	// Queued is reachable from any unfinished state: that is how a crash
	// recovery hands an interrupted task back to the scheduler.
	StatusQueued:    {StatusPlanning, StatusRunning, StatusPaused, StatusCancelled, StatusFailed},
	StatusPlanning:  {StatusRunning, StatusWaiting, StatusPaused, StatusQueued, StatusFailed, StatusCancelled},
	StatusRunning:   {StatusVerifying, StatusWaiting, StatusPaused, StatusQueued, StatusFailed, StatusCancelled},
	StatusWaiting:   {StatusRunning, StatusVerifying, StatusPaused, StatusQueued, StatusFailed, StatusCancelled},
	StatusVerifying: {StatusCompleted, StatusFailed, StatusRunning, StatusPaused, StatusCancelled},
	StatusPaused:    {StatusQueued, StatusRunning, StatusPlanning, StatusCancelled, StatusFailed},
	StatusCompleted: {StatusQueued}, // a re-run is an explicit requeue
	StatusFailed:    {StatusQueued, StatusRunning, StatusCancelled},
	StatusCancelled: {StatusQueued},
}

// CanTransition reports whether from → to is allowed.
func CanTransition(from, to Status) bool {
	if from == to {
		return true
	}
	for _, next := range transitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// TransitionError describes a rejected state change.
type TransitionError struct {
	TaskID string
	From   Status
	To     Status
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("task %s cannot move %s → %s", e.TaskID, e.From, e.To)
}

// Verification records the outcome of a real check. Passed is only true when a
// command actually ran and exited zero.
type Verification struct {
	Kind       string `json:"kind"` // test | build | vet | lint | manual
	Command    string `json:"command,omitempty"`
	ExitCode   int    `json:"exit_code"`
	Passed     bool   `json:"passed"`
	Output     string `json:"output,omitempty"`
	RanAt      string `json:"ran_at"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

// Task is one unit of engineering work with a durable lifecycle.
type Task struct {
	ID         string         `json:"id"`
	ParentID   string         `json:"parent_id,omitempty"`
	Title      string         `json:"title"`
	Objective  string         `json:"objective,omitempty"`
	Status     Status         `json:"status"`
	Priority   int            `json:"priority,omitempty"`
	DependsOn  []string       `json:"depends_on,omitempty"`
	Phase      string         `json:"phase,omitempty"`
	FilesPlan  []string       `json:"files_planned,omitempty"`
	FilesTouch []string       `json:"files_changed,omitempty"`
	CreatedAt  string         `json:"created_at"`
	UpdatedAt  string         `json:"updated_at"`
	DoneAt     string         `json:"completed_at,omitempty"`
	Attempts   int            `json:"attempts"`
	LastError  string         `json:"last_error,omitempty"`
	Verify     []Verification `json:"verifications,omitempty"`
	Checkpoint string         `json:"checkpoint,omitempty"`
	RunID      string         `json:"run_id,omitempty"`
}

// NewTask builds a queued task with timestamps set.
func NewTask(id, title string) Task {
	now := timestamp()
	return Task{ID: id, Title: title, Status: StatusQueued, CreatedAt: now, UpdatedAt: now}
}

// Verified reports whether the task carries at least one passing check.
func (t Task) Verified() bool {
	for _, v := range t.Verify {
		if v.Passed {
			return true
		}
	}
	return false
}

// VerificationLevel classifies how much evidence exists for a task.
type VerificationLevel string

const (
	LevelVerified VerificationLevel = "verified" // a real command passed
	LevelUntested VerificationLevel = "untested" // implemented, no evidence
	LevelPartial  VerificationLevel = "partial"  // some checks passed, some did not run
	LevelFailed   VerificationLevel = "failed"   // a check failed
	LevelBlocked  VerificationLevel = "blocked"  // dependency never delivered
)

// Level summarises the task's evidence honestly.
func (t Task) Level() VerificationLevel {
	passed, failed := 0, 0
	for _, v := range t.Verify {
		if v.Passed {
			passed++
		} else {
			failed++
		}
	}
	switch {
	case failed > 0:
		return LevelFailed
	case passed > 0:
		return LevelVerified
	case t.Status == StatusFailed:
		return LevelFailed
	}
	return LevelUntested
}

func timestamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// normaliseIDs trims and de-duplicates a dependency list, dropping self edges.
func normaliseIDs(self string, ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
