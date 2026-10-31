package providers

import (
	"strings"
	"testing"
)

func turn(t *testing.T, text string) string {
	t.Helper()
	return mockScript(0, []Message{{Role: "user", Content: text}})
}

func TestMockSelfEditBatch(t *testing.T) {
	out := turn(t, "edit your own code and show me")
	if !strings.Contains(out, "```evolv") {
		t.Fatalf("expected evolv block, got: %q", out)
	}
	for _, want := range []string{"edit_file", "ui/index.html", "ui/style.css", "reload_shell"} {
		if !strings.Contains(out, want) {
			t.Errorf("self-edit batch missing %q", want)
		}
	}
}

func TestMockPromptLibraryBatch(t *testing.T) {
	out := turn(t, "make a button that lets me pick prompt templates")
	if !strings.Contains(out, "```evolv") || !strings.Contains(out, "prompt-library") {
		t.Fatalf("expected prompt-library batch, got: %q", out)
	}
	// batch must carry two actions
	if !strings.Contains(out, "evolve_skill") {
		t.Errorf("batch should also grow a skill")
	}
}

func TestMockSnake(t *testing.T) {
	out := turn(t, "make a playable snake game")
	if !strings.Contains(out, "snake") || !strings.Contains(out, "evolve_component") {
		t.Fatalf("expected snake component mutation, got: %q", out)
	}
	if !strings.Contains(out, "<canvas") || !strings.Contains(out, "touchstart") {
		t.Errorf("snake html should contain a canvas with touch controls")
	}
	// "snake" must win over the generic "game" → flappy mapping
	if strings.Contains(out, "flappy") {
		t.Errorf("snake request should not grow flappy")
	}
}

func TestMockFlappy(t *testing.T) {
	out := turn(t, "delete this whole codebase and just make flappy bird or smth")
	if !strings.Contains(out, "flappy") || !strings.Contains(out, "evolve_component") {
		t.Fatalf("expected flappy component mutation, got: %q", out)
	}
	if !strings.Contains(out, "<canvas") {
		t.Errorf("flappy html should contain a canvas")
	}
}

func TestMockPluginGrowth(t *testing.T) {
	out := turn(t, "grow a plugin that reacts to your mutations")
	if !strings.Contains(out, "evolve_plugin") || !strings.Contains(out, "ghost-log") {
		t.Fatalf("expected plugin mutation, got: %q", out)
	}
	if !strings.Contains(out, "evolv.on") {
		t.Errorf("plugin code should subscribe to the bus")
	}
}

func TestMockZenRecolor(t *testing.T) {
	out := turn(t, "make your ui cleaner")
	if !strings.Contains(out, "--accent") || !strings.Contains(out, "edit_file") {
		t.Fatalf("expected live skin edit, got: %q", out)
	}
}
