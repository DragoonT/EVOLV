package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dxn111/EVOLV/internal/bus"
	"github.com/dxn111/EVOLV/internal/providers"
	"github.com/dxn111/EVOLV/internal/workspace"
)

func TestVoiceManagerSpeak(t *testing.T) {
	b := bus.New()
	vm := NewVoiceManager(b)

	sub, _ := b.Subscribe()
	defer b.Unsubscribe(sub)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ut, err := vm.Speak(ctx, "Refactored the canvas layout successfully.", "natural", 1.0, 1.0, "agent")
	if err != nil {
		t.Fatalf("speak failed: %v", err)
	}

	if ut.ID == "" {
		t.Fatal("expected non-empty utterance ID")
	}
	if ut.Text != "Refactored the canvas layout successfully." {
		t.Fatalf("unexpected text: %s", ut.Text)
	}
	if ut.Voice != "natural" {
		t.Fatalf("unexpected voice: %s", ut.Voice)
	}

	// Verify bus event
	select {
	case ev := <-sub.Events():
		if ev.Type != "voice:speak" {
			t.Fatalf("expected type voice:speak, got %s", ev.Type)
		}
		var p map[string]any
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if p["text"] != "Refactored the canvas layout successfully." {
			t.Fatalf("unexpected event text: %v", p["text"])
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for voice:speak event")
	}

	// Check history
	hist := vm.History()
	if len(hist) != 1 {
		t.Fatalf("expected 1 utterance in history, got %d", len(hist))
	}
	if hist[0].ID != ut.ID {
		t.Fatalf("expected history utterance ID %s, got %s", ut.ID, hist[0].ID)
	}

	// Test empty text error
	if _, err := vm.Speak(ctx, "   ", "", 1.0, 1.0, "agent"); err == nil {
		t.Fatal("expected error on empty speech text")
	}

	// Test long text truncation
	longText := strings.Repeat("A", 1500)
	utLong, err := vm.Speak(ctx, longText, "", 1.0, 1.0, "agent")
	if err != nil {
		t.Fatalf("unexpected error on long text: %v", err)
	}
	if len(utLong.Text) > 1003 { // 1000 + "..."
		t.Fatalf("expected truncated text length <= 1003, got %d", len(utLong.Text))
	}
}

func TestActionSpeak(t *testing.T) {
	td := t.TempDir()
	t.Setenv("HOME", td)

	ws, err := workspace.Load(nil)
	if err != nil {
		t.Fatalf("workspace.Load: %v", err)
	}

	b := bus.New()
	prov, model := providers.Build("mock", "", "", nil)
	a := New(ws, b, prov, model)

	outJSON := a.Execute(action{
		Action: "speak",
		Text:   "Organism is fully operational.",
	})
	raw := strings.TrimPrefix(outJSON, "kernel:")

	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("failed to parse execute output: %v (%s)", err, outJSON)
	}

	if out["ok"] != true {
		t.Fatalf("expected ok=true, got: %s", outJSON)
	}
	if out["text"] != "Organism is fully operational." {
		t.Fatalf("expected text 'Organism is fully operational.', got %v", out["text"])
	}

	// Empty text test
	outEmptyJSON := a.Execute(action{
		Action: "speak",
	})
	rawEmpty := strings.TrimPrefix(outEmptyJSON, "kernel:")
	var outEmpty map[string]any
	if err := json.Unmarshal([]byte(rawEmpty), &outEmpty); err != nil {
		t.Fatalf("failed to parse execute output: %v (%s)", err, outEmptyJSON)
	}
	if outEmpty["ok"] != false {
		t.Fatalf("expected ok=false on empty text, got: %s", outEmptyJSON)
	}
}
