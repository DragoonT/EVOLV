package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunMetricsAppearInPrometheus(t *testing.T) {
	s, _ := newWbServer(t)
	s.runStats.turnsStarted.Add(3)
	s.runStats.observeTurn("failed", 1200)
	s.runStats.observeTurn("done", 400)
	s.runStats.observeTool("write_file", true)
	s.runStats.observeTool("run_tests", false)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics -> %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"evolv_agent_turns_started_total 3",
		"evolv_agent_turns_failed_total 1",
		"evolv_tool_calls_total 2",
		"evolv_tool_failures_total 1",
		`evolv_tool_calls_by_action{action="write_file"} 1`,
		"evolv_agent_turn_duration_seconds 0.4",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q\n%s", want, body)
		}
	}
}

func TestRunMetricsJSONSnapshot(t *testing.T) {
	s, _ := newWbServer(t)
	s.runStats.observeTool("list_dir", true)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health -> %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"runs"`) {
		t.Fatalf("health lacks runs: %s", rec.Body.String())
	}
}

func TestRunMetricsBoundedLabels(t *testing.T) {
	m := newRunMetrics()
	m.observeTool(strings.Repeat("x", 200), true)
	m.mu.Lock()
	defer m.mu.Unlock()
	for kind := range m.toolByKind {
		if len(kind) > 32 {
			t.Fatalf("unbounded label %q", kind)
		}
	}
}
