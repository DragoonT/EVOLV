package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dxn111/EVOLV/internal/bus"
	"github.com/dxn111/EVOLV/internal/providers"
	"github.com/dxn111/EVOLV/internal/workspace"
)

func TestActionRunWorkflow(t *testing.T) {
	td := t.TempDir()
	t.Setenv("HOME", td)
	t.Setenv("USERPROFILE", td)

	ws, err := workspace.Load(nil)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}

	b := bus.New()
	prov, model := providers.Build("mock", "", "", nil)
	a := New(ws, b, prov, model)

	outJSON := a.Execute(action{
		Action:   "run_workflow",
		Workflow: "workspace_health",
	})
	raw := strings.TrimPrefix(outJSON, "kernel:")

	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("failed to parse execute output: %v (%s)", err, outJSON)
	}

	if out["workflow"] != "workspace_health" {
		t.Fatalf("expected workflow 'workspace_health', got: %v", out["workflow"])
	}
	steps, ok := out["steps"].(map[string]any)
	if !ok || len(steps) == 0 {
		t.Fatalf("expected non-empty steps map, got: %+v", out["steps"])
	}
	if out["summary"] == "" {
		t.Fatalf("expected non-empty summary, got: %+v", out)
	}
}
