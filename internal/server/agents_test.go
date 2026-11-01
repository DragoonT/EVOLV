package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dxn111/EVOLV/internal/agent"
	"github.com/dxn111/EVOLV/internal/bus"
	"github.com/dxn111/EVOLV/internal/workspace"
)

func newAgentServer(t *testing.T) (*Server, *workspace.W) {
	t.Helper()
	root := t.TempDir()
	ws := &workspace.W{Root: root, Config: &workspace.Config{
		APIKeys: map[string]string{}, BaseURLs: map[string]string{},
		DisabledWidgets: map[string]bool{}, PinnedCreations: map[string]bool{}, ArchivedCreations: map[string]bool{},
	}}
	for _, rel := range []string{workspace.WorkspaceDir, workspace.ComponentsDir, workspace.ToolsDir, workspace.SkillsDir, workspace.PromptsDir, workspace.PluginsDir} {
		if err := os.MkdirAll(ws.Dir(rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(ws.Dir(filepath.Join(workspace.ToolsDir, "reader.js")), []byte("function reader(){}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ws.Dir(filepath.Join(workspace.SkillsDir, "research.md")), []byte("# Research"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := bus.New()
	a := agent.New(ws, b, nil, "test")
	return New(ws, b, a, nil), ws
}

func agentRequest(t *testing.T, s *Server, method, body string, want int) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/api/agents", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s /api/agents -> %d (want %d): %s", method, rec.Code, want, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func TestAgentsCloneSwitchAndReload(t *testing.T) {
	s, ws := newAgentServer(t)
	initial := agentRequest(t, s, http.MethodGet, "", http.StatusOK)
	if agents := initial["agents"].([]any); len(agents) != 1 || agents[0].(map[string]any)["id"] != "main" {
		t.Fatalf("initial roster should contain only main: %v", initial)
	}

	created := agentRequest(t, s, http.MethodPost, `{"action":"clone","name":"Research One","identity":"careful","tools":["reader","reader"],"skills":["research"]}`, http.StatusOK)
	profile := created["agent"].(map[string]any)
	if profile["id"] != "research-one" || profile["number"] != float64(2) {
		t.Fatalf("unexpected clone: %v", created)
	}
	if tools := profile["tools"].([]any); len(tools) != 1 || tools[0] != "reader" {
		t.Fatalf("capabilities were not normalized: %v", tools)
	}
	agentRequest(t, s, http.MethodPost, `{"action":"switch","id":"main"}`, http.StatusOK)

	b := bus.New()
	reloaded := New(ws, b, agent.New(ws, b, nil, "test"), nil)
	roster := agentRequest(t, reloaded, http.MethodGet, "", http.StatusOK)
	if agents := roster["agents"].([]any); len(agents) != 2 || agents[1].(map[string]any)["number"] != float64(2) {
		t.Fatalf("clone did not survive reload: %v", roster)
	}
}

func TestAgentsRejectDuplicateAndUnknownCapabilities(t *testing.T) {
	s, _ := newAgentServer(t)
	body := `{"action":"clone","name":"Builder","tools":["reader"],"skills":["research"]}`
	agentRequest(t, s, http.MethodPost, body, http.StatusOK)
	agentRequest(t, s, http.MethodPost, body, http.StatusConflict)
	agentRequest(t, s, http.MethodPost, `{"action":"clone","name":"Unsafe","tools":["does-not-exist"]}`, http.StatusBadRequest)
}

func TestAgentsUnavailableWithoutRuntime(t *testing.T) {
	s, _ := newWbServer(t)
	agentRequest(t, s, http.MethodGet, "", http.StatusServiceUnavailable)
}
