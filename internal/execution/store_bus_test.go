package execution

import (
	"testing"
	"time"
)

// SetBus + appendEv must republish journal events to subscribers with run
// attribution, and SetBus(nil) must silence the store again.
func TestStorePublishesToBus(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.CloseAll()
	bus := NewBus()
	s.SetBus(bus)
	sub, err := bus.Subscribe(16)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if _, err := s.CreateRun("bus-run", "bus test", "verify republish", []Task{{ID: "a", Title: "A"}}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := s.Transition("bus-run", "a", StatusRunning); err != nil {
		t.Fatalf("transition: %v", err)
	}

	select {
	case ev := <-sub.C:
		if ev.Kind != KindTaskMoved {
			t.Fatalf("kind = %q, want %q", ev.Kind, KindTaskMoved)
		}
		if ev.RunID != "bus-run" || ev.TaskID() != "a" {
			t.Fatalf("attribution wrong: run=%q task=%q", ev.RunID, ev.TaskID())
		}
		if ev.From != StatusQueued || ev.To != StatusRunning {
			t.Fatalf("transition payload wrong: from=%s to=%s", ev.From, ev.To)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no bus event within 2s")
	}

	s.SetBus(nil)
	if _, err := s.Transition("bus-run", "a", StatusPaused); err != nil {
		t.Fatalf("transition 2: %v", err)
	}
	select {
	case ev := <-sub.C:
		t.Fatalf("bus still delivering after SetBus(nil): got %s", ev.Kind)
	case <-time.After(150 * time.Millisecond):
	}
}
