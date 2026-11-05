package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunnerListAndGetPipelines(t *testing.T) {
	r := NewRunner(RunnerConfig{})
	list := r.ListPipelines()
	if len(list) < 3 {
		t.Fatalf("expected at least 3 built-in pipelines, got %d", len(list))
	}
	p, ok := r.GetPipeline("quality_gate")
	if !ok || p.Name != "Quality Gate" {
		t.Fatalf("failed to find quality_gate pipeline: %+v", p)
	}
	_, notFound := r.GetPipeline("non_existent")
	if notFound {
		t.Fatal("expected false for unknown pipeline")
	}
}

func TestRunnerCustomWorkflowExecution(t *testing.T) {
	events := make([]string, 0)
	r := NewRunner(RunnerConfig{
		BusPub: func(event string, payload any) {
			events = append(events, event)
		},
	})

	custom := CustomSpec{
		Name: "test_pipeline",
		Steps: []CustomStep{
			{
				ID:      "step1",
				Command: "go version",
			},
			{
				ID:        "step2",
				DependsOn: []string{"step1"},
				Command:   "go env GOVERSION",
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := r.Run(ctx, "", &custom)
	if err != nil {
		t.Fatalf("custom workflow run failed: %v", err)
	}

	if !res.Succeeded() {
		t.Fatalf("expected all steps to succeed, got %+v", res)
	}
	if len(res.Steps) != 2 {
		t.Fatalf("expected 2 step results, got %d", len(res.Steps))
	}
	if res.Steps["step1"].Status != StatusSucceeded {
		t.Fatalf("step1 status = %v", res.Steps["step1"].Status)
	}
	if res.Steps["step2"].Status != StatusSucceeded {
		t.Fatalf("step2 status = %v", res.Steps["step2"].Status)
	}

	// Verify events
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %v", events)
	}
	if events[0] != "workflow:start" || events[len(events)-1] != "workflow:done" {
		t.Fatalf("unexpected events sequence: %v", events)
	}

	// Verify history
	hist := r.History()
	if len(hist) != 1 {
		t.Fatalf("expected 1 item in history, got %d", len(hist))
	}

	// Test JSON serialization of Result
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("failed to marshal Result to JSON: %v", err)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}
	if data["succeeded"] != true {
		t.Fatalf("expected succeeded=true in json, got %v", data["succeeded"])
	}
}

func TestRunnerWorkspaceHealth(t *testing.T) {
	td := t.TempDir()
	_ = os.MkdirAll(filepath.Join(td, "cmd"), 0o755)
	_ = os.MkdirAll(filepath.Join(td, "internal"), 0o755)
	_ = os.MkdirAll(filepath.Join(td, "assets"), 0o755)

	r := NewRunner(RunnerConfig{
		ProjectRoot: func() string { return td },
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := r.Run(ctx, "workspace_health", nil)
	if err != nil {
		t.Fatalf("workspace_health failed: %v", err)
	}
	if res.Steps["layout"].Status != StatusSucceeded {
		t.Fatalf("layout check failed: %+v", res.Steps["layout"])
	}
}
