package server

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// runMetrics tracks agent-turn and tool-call outcomes with a small fixed set
// of labels. Cardinality is bounded by construction: no model names, paths or
// action arguments ever become a metric label.
type runMetrics struct {
	turnsStarted  atomic.Int64
	turnsFinished atomic.Int64
	turnsFailed   atomic.Int64
	turnsCanceled atomic.Int64
	turnsLimited  atomic.Int64

	toolCalls    atomic.Int64
	toolFailures atomic.Int64

	mu          sync.Mutex
	toolByKind  map[string]int64
	lastTurnMS  atomic.Int64
	totalTurnMS atomic.Int64
}

func newRunMetrics() *runMetrics {
	return &runMetrics{toolByKind: map[string]int64{}}
}

// observeTool records one tool execution outcome.
func (m *runMetrics) observeTool(action string, ok bool) {
	m.toolCalls.Add(1)
	if !ok {
		m.toolFailures.Add(1)
	}
	if action == "" {
		return
	}
	kind := action
	if len(kind) > 32 {
		kind = kind[:32]
	}
	m.mu.Lock()
	m.toolByKind[kind]++
	m.mu.Unlock()
}

// observeTurn records one completed agent turn.
func (m *runMetrics) observeTurn(status string, elapsedMS int64) {
	m.turnsFinished.Add(1)
	switch status {
	case "failed":
		m.turnsFailed.Add(1)
	case "cancelled":
		m.turnsCanceled.Add(1)
	case "limited":
		m.turnsLimited.Add(1)
	}
	m.lastTurnMS.Store(elapsedMS)
	m.totalTurnMS.Add(elapsedMS)
}

// writePrometheus emits the metrics in text exposition format.
func (m *runMetrics) writePrometheus(w io.Writer) {
	fmt.Fprintf(w, "# HELP evolv_agent_turns_total Agent turns started.\n# TYPE evolv_agent_turns_total counter\n")
	fmt.Fprintf(w, "evolv_agent_turns_started_total %d\n", m.turnsStarted.Load())
	fmt.Fprintf(w, "evolv_agent_turns_finished_total %d\n", m.turnsFinished.Load())
	fmt.Fprintf(w, "evolv_agent_turns_failed_total %d\n", m.turnsFailed.Load())
	fmt.Fprintf(w, "evolv_agent_turns_cancelled_total %d\n", m.turnsCanceled.Load())
	fmt.Fprintf(w, "evolv_agent_turns_limited_total %d\n", m.turnsLimited.Load())
	fmt.Fprintf(w, "# HELP evolv_tool_calls_total Tool actions executed.\n# TYPE evolv_tool_calls_total counter\n")
	fmt.Fprintf(w, "evolv_tool_calls_total %d\n", m.toolCalls.Load())
	fmt.Fprintf(w, "evolv_tool_failures_total %d\n", m.toolFailures.Load())
	fmt.Fprintf(w, "# HELP evolv_agent_turn_duration_seconds Last agent turn duration.\n# TYPE evolv_agent_turn_duration_seconds gauge\n")
	fmt.Fprintf(w, "evolv_agent_turn_duration_seconds %g\n", float64(m.lastTurnMS.Load())/1000)
	fmt.Fprintf(w, "# HELP evolv_tool_calls_by_action Tool calls per action.\n# TYPE evolv_tool_calls_by_action counter\n")
	m.mu.Lock()
	for kind, count := range m.toolByKind {
		fmt.Fprintf(w, "evolv_tool_calls_by_action{action=%q} %d\n", kind, count)
	}
	m.mu.Unlock()
}

// snapshot returns a JSON-friendly view for /api/metrics consumers.
func (m *runMetrics) snapshot() map[string]any {
	m.mu.Lock()
	byKind := make(map[string]int64, len(m.toolByKind))
	for k, v := range m.toolByKind {
		byKind[k] = v
	}
	m.mu.Unlock()
	return map[string]any{
		"agent_turns": map[string]int64{
			"started":   m.turnsStarted.Load(),
			"finished":  m.turnsFinished.Load(),
			"failed":    m.turnsFailed.Load(),
			"cancelled": m.turnsCanceled.Load(),
			"limited":   m.turnsLimited.Load(),
		},
		"tool_calls":     m.toolCalls.Load(),
		"tool_failures":  m.toolFailures.Load(),
		"tool_by_action": byKind,
		"last_turn_ms":   m.lastTurnMS.Load(),
	}
}
