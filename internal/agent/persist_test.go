package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/dxn111/EVOLV/internal/providers"
)

// memory round-trip: save → mutate in RAM → load restores what was saved
func TestMemoryRoundTrip(t *testing.T) {
	a, _ := mockAgent(t)
	a.history = []providers.Message{
		{Role: "user", Content: "remember the number 41"},
		{Role: "assistant", Content: "41 is remembered"},
	}
	a.saveMemory()
	if _, err := os.Stat(a.memoryFile()); err != nil {
		t.Fatalf("memory file not written: %v", err)
	}
	a.history = nil
	a.loadMemory()
	if len(a.history) != 2 || !strings.Contains(a.history[1].Content, "41") {
		t.Fatalf("memory not restored: %+v", a.history)
	}
}

// reset wipes the notebook from disk
func TestMemoryResetRemovesFile(t *testing.T) {
	a, _ := mockAgent(t)
	a.history = []providers.Message{{Role: "user", Content: "hi"}}
	a.saveMemory()
	a.Reset()
	if _, err := os.Stat(a.memoryFile()); !os.IsNotExist(err) {
		t.Fatal("memory file survived reset")
	}
}

// corrupt memory file must not crash boot
func TestMemoryCorruptTolerated(t *testing.T) {
	a, _ := mockAgent(t)
	_ = os.WriteFile(a.memoryFile(), []byte("{not json"), 0o644)
	a.loadMemory() // must not panic
	if len(a.history) != 0 {
		t.Fatalf("corrupt memory loaded: %+v", a.history)
	}
}

// a full chat turn lands in the notebook automatically
func TestChatPersistsMemory(t *testing.T) {
	a, _ := mockAgent(t)
	a.Chat(t.Context(), "hello, introduce yourself")
	raw, err := os.ReadFile(a.memoryFile())
	if err != nil {
		t.Fatalf("chat did not persist memory: %v", err)
	}
	if !strings.Contains(string(raw), "introduce yourself") {
		t.Fatal("user message missing from persisted memory")
	}
}
