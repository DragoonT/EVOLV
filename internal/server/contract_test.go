package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Contract tests pin the shape of the critical endpoints so the frontend and
// external callers can rely on them.

func contractGET(t *testing.T, s *Server, path string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestContractHealth(t *testing.T) {
	s, _ := newWbServer(t)
	code, body := contractGET(t, s, "/api/health")
	if code != http.StatusOK {
		t.Fatalf("health -> %d", code)
	}
	for _, key := range []string{"status", "version", "provider", "model", "uptime_seconds", "requests", "errors", "evolutions", "runs"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("health missing %q: %v", key, body)
		}
	}
	if body["version"] != "0.31.1" {
		t.Fatalf("version = %v", body["version"])
	}
}

func TestContractSettingsNeverLeaksKeys(t *testing.T) {
	s, _ := newWbServer(t)
	s.ws.Config.APIKeys["openai"] = "sk-live-secret-value"
	code, body := contractGET(t, s, "/api/settings")
	if code != http.StatusOK {
		t.Fatalf("settings -> %d", code)
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "sk-live-secret-value") {
		t.Fatalf("settings leaked an API key: %s", raw)
	}
	keys, ok := body["keys_present"].(map[string]any)
	if !ok || keys["openai"] != true {
		t.Fatalf("keys_present = %v", body["keys_present"])
	}
}

func TestContractHistoryPagination(t *testing.T) {
	s, _ := newWbServer(t)
	code, body := contractGET(t, s, "/api/history?limit=10&offset=0")
	if code == http.StatusServiceUnavailable {
		t.Skip("no agent runtime in this fixture")
	}
	if code != http.StatusOK {
		t.Fatalf("history -> %d", code)
	}
	if _, ok := body["messages"]; !ok {
		if _, ok := body["history"]; !ok {
			t.Fatalf("history has no message list: %v", body)
		}
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/history?limit=999", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit -> %d", rec.Code)
	}
}

func TestContractErrorsAreJSON(t *testing.T) {
	s, _ := newWbServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/runs/create", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad create -> %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("error content type = %q", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == nil {
		t.Fatalf("error body = %s", rec.Body.String())
	}
}

func TestContractRunsShape(t *testing.T) {
	s, _ := newWbServer(t)
	code, body := contractGET(t, s, "/api/runs")
	if code != http.StatusOK {
		t.Fatalf("runs -> %d", code)
	}
	if _, ok := body["runs"].([]any); !ok {
		t.Fatalf("runs must be a list: %v", body["runs"])
	}
}
