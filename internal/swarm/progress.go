package swarm

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Tracker is a live progress rollup for swarm runs: it keeps a small
// mutex-guarded table of per-agent states and hands the UI cheap counts
// instead of forcing it to scan raw engine state.
//
// The status vocabulary is adapted from Engine's own words. Engine run states
// are idle | running | done | stopped | error (State.Status) and agent states
// are ready | thinking | voted | done (AgentState.Status); Mark accepts all of
// those plus the generic words pending, succeeded, failed and
// cancelled/canceled, and normalizes every input onto these five canonical
// values so Snapshot buckets always sum to Total:
//
//	idle      ← idle, pending, ready, "" (registered, not started)
//	running   ← running, thinking
//	done      ← done, succeeded, voted
//	error     ← error, failed (also records into LastErrors)
//	stopped   ← stopped, cancelled, canceled
//
// Any unrecognized word is treated as non-terminal (idle) rather than
// guessing, so a typo can never inflate a completion count.

// Canonical tracker statuses, mirroring Engine's run state strings.
const (
	ProgStatusIdle    = "idle"
	ProgStatusRunning = "running"
	ProgStatusDone    = "done"
	ProgStatusError   = "error"
	ProgStatusStopped = "stopped"
)

const (
	// progErrorCap bounds the LastErrors ring.
	progErrorCap = 5
	// progThroughputWindow is the trailing window for ThroughputPerMin.
	progThroughputWindow = 5 * time.Minute
	// progSuccessCap bounds the throughput event history; it only exists to
	// cap memory on very long runs.
	progSuccessCap = 4096
)

// progNow is the clock used for every timestamp the Tracker records. It is a
// var so tests can inject a deterministic clock; production code never
// reassigns it.
var progNow = func() time.Time { return time.Now() }

// progAgent is one tracked unit of work.
type progAgent struct {
	id     string
	label  string
	status string // canonical ProgStatus* value
	note   string // optional detail, e.g. the failure message
}

// Snapshot is the JSON-shaped rollup the server can expose verbatim.
type Snapshot struct {
	Total            int          `json:"total"`
	Pending          int          `json:"pending"`
	Running          int          `json:"running"`
	Succeeded        int          `json:"succeeded"`
	Failed           int          `json:"failed"`
	Cancelled        int          `json:"cancelled"`
	StartedAt        time.Time    `json:"started_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
	PerAgent         []AgentState `json:"per_agent"`
	ThroughputPerMin float64      `json:"throughput_per_min"`
	LastErrors       []string     `json:"last_errors"`
}

// Tracker follows the agents of a swarm run. The zero value is usable: every
// method lazily initializes its state, though NewTracker is the documented
// constructor path. All methods are safe for concurrent use.
type Tracker struct {
	progMu        sync.Mutex
	progAgents    map[string]*progAgent
	progErrors    []string    // oldest first, capped at progErrorCap
	progSuccesses []time.Time // success events, capped at progSuccessCap
	progStarted   time.Time
	progUpdated   time.Time
}

// NewTracker builds an empty tracker.
func NewTracker() *Tracker {
	return &Tracker{progAgents: map[string]*progAgent{}}
}

// progInitLocked makes the zero-value Tracker usable.
func (t *Tracker) progInitLocked() {
	if t.progAgents == nil {
		t.progAgents = map[string]*progAgent{}
	}
}

// progTouchLocked stamps tracker activity; the first touch starts the clock.
func (t *Tracker) progTouchLocked() {
	now := progNow()
	if t.progStarted.IsZero() {
		t.progStarted = now
	}
	t.progUpdated = now
}

// progNormalize maps any accepted status word onto the canonical vocabulary.
func progNormalize(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "idle", "pending", "ready":
		return ProgStatusIdle
	case "running", "thinking":
		return ProgStatusRunning
	case "done", "succeeded", "voted":
		return ProgStatusDone
	case "error", "failed":
		return ProgStatusError
	case "stopped", "cancelled", "canceled":
		return ProgStatusStopped
	default:
		return ProgStatusIdle
	}
}

// Register adds id to the tracked roster with an initial idle status. A
// register for an id that already exists updates its label and keeps the
// current status, so re-registering never silently resets progress. An empty
// id is ignored.
func (t *Tracker) Register(id string, label string) {
	if id == "" {
		return
	}
	t.progMu.Lock()
	defer t.progMu.Unlock()
	t.progInitLocked()
	if a, ok := t.progAgents[id]; ok {
		a.label = label
		t.progTouchLocked()
		return
	}
	t.progAgents[id] = &progAgent{id: id, label: label, status: ProgStatusIdle}
	t.progTouchLocked()
}

// Deregister removes id from the roster; counts are recomputed on the next
// Snapshot. Historical LastErrors and throughput events are kept. Removing an
// unknown id is a no-op.
func (t *Tracker) Deregister(id string) {
	t.progMu.Lock()
	defer t.progMu.Unlock()
	t.progInitLocked()
	if _, ok := t.progAgents[id]; !ok {
		return
	}
	delete(t.progAgents, id)
	t.progTouchLocked()
}

// Mark records a status transition for id (see the vocabulary above). Marking
// a failed status pushes the agent's note — or a bare fallback — onto the
// LastErrors ring. Marking an unknown id is a no-op; the roster is explicit
// via Register.
func (t *Tracker) Mark(id string, status string) {
	t.progMu.Lock()
	defer t.progMu.Unlock()
	t.progInitLocked()
	a, ok := t.progAgents[id]
	if !ok {
		return
	}
	canonical := progNormalize(status)
	if canonical == ProgStatusError && a.status != ProgStatusError {
		t.progRecordErrorLocked(a)
	}
	if canonical == ProgStatusDone && a.status != ProgStatusDone {
		t.progRecordSuccessLocked()
	}
	a.status = canonical
	t.progTouchLocked()
}

// MarkNote attaches an optional detail line to id, typically the error text
// that a later failed Mark should carry into LastErrors. An empty note
// clears it. Unknown ids are ignored.
func (t *Tracker) MarkNote(id string, note string) {
	t.progMu.Lock()
	defer t.progMu.Unlock()
	t.progInitLocked()
	a, ok := t.progAgents[id]
	if !ok {
		return
	}
	a.note = note
	t.progTouchLocked()
}

// progRecordErrorLocked appends a failure line, oldest first, keeping only
// the progErrorCap most recent.
func (t *Tracker) progRecordErrorLocked(a *progAgent) {
	detail := strings.TrimSpace(a.note)
	if detail == "" {
		detail = ProgStatusError
	}
	t.progErrors = append(t.progErrors, a.id+": "+detail)
	if len(t.progErrors) > progErrorCap {
		t.progErrors = t.progErrors[len(t.progErrors)-progErrorCap:]
	}
}

// progRecordSuccessLocked records a completion event for the throughput
// window. Transitions are counted, not re-marks of an already-done agent.
func (t *Tracker) progRecordSuccessLocked() {
	t.progSuccesses = append(t.progSuccesses, progNow())
	if len(t.progSuccesses) > progSuccessCap {
		t.progSuccesses = t.progSuccesses[len(t.progSuccesses)-progSuccessCap:]
	}
}

// Snapshot returns a copy of the rollup, safe to serialize while the run is
// mutating state. PerAgent is sorted by id. ThroughputPerMin is the number of
// success events in the trailing progThroughputWindow divided by the window
// length in minutes; it is 0 when the tracker has never seen activity. An
// empty tracker yields non-nil PerAgent and LastErrors so JSON shows [] not
// null.
func (t *Tracker) Snapshot() Snapshot {
	t.progMu.Lock()
	defer t.progMu.Unlock()
	snap := Snapshot{
		Total:      len(t.progAgents),
		StartedAt:  t.progStarted,
		UpdatedAt:  t.progUpdated,
		PerAgent:   make([]AgentState, 0, len(t.progAgents)),
		LastErrors: make([]string, len(t.progErrors)),
	}
	for i, entry := range t.progErrors {
		snap.LastErrors[len(t.progErrors)-1-i] = entry // newest first
	}
	for _, a := range t.progAgents {
		switch a.status {
		case ProgStatusRunning:
			snap.Running++
		case ProgStatusDone:
			snap.Succeeded++
		case ProgStatusError:
			snap.Failed++
		case ProgStatusStopped:
			snap.Cancelled++
		default:
			snap.Pending++
		}
		snap.PerAgent = append(snap.PerAgent, progRoster(a))
	}
	sort.Slice(snap.PerAgent, func(i, j int) bool {
		return snap.PerAgent[i].ID < snap.PerAgent[j].ID
	})
	if t.progStarted.IsZero() {
		return snap
	}
	cutoff := progNow().Add(-progThroughputWindow)
	recent := 0
	for _, ts := range t.progSuccesses {
		if ts.After(cutoff) {
			recent++
		}
	}
	snap.ThroughputPerMin = float64(recent) / progThroughputWindow.Minutes()
	return snap
}

// progRoster renders one tracked agent as the roster card type the UI already
// knows. When the id follows Engine's "role-nn" convention (e.g. coder-02)
// the card is enriched with the role metadata from Roles, so a bare tracker
// snapshot still paints the same roster as an engine snapshot.
func progRoster(a *progAgent) AgentState {
	card := AgentState{ID: a.id, Name: progDisplayName(a), Status: a.status, Last: a.note}
	roleID, suffix, _ := strings.Cut(a.id, "-")
	role, ok := roleByID(roleID)
	if !ok {
		return card
	}
	card.Role, card.Icon, card.Color, card.Blurb = role.ID, role.Icon, role.Color, role.Blurb
	if n, err := strconv.Atoi(strings.TrimSpace(suffix)); err == nil && n > 0 {
		card.Index = n
	}
	return card
}

// progDisplayName prefers the registered label; without one it reproduces
// Engine's "Role NN" naming when the id carries a role prefix.
func progDisplayName(a *progAgent) string {
	if label := strings.TrimSpace(a.label); label != "" {
		return label
	}
	roleID, suffix, _ := strings.Cut(a.id, "-")
	role, ok := roleByID(roleID)
	if !ok {
		return ""
	}
	if n, err := strconv.Atoi(strings.TrimSpace(suffix)); err == nil && n > 0 {
		return fmt.Sprintf("%s %02d", role.Name, n)
	}
	return role.Name
}
