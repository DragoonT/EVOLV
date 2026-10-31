package agent

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dxn111/EVOLV/internal/bus"
)

// A plugin must be able to react to bus events live via evolv.on.
func TestPluginOnReactsToEvents(t *testing.T) {
	a := testAgent(t)
	done := make(chan map[string]any, 1)
	a.js.mu.Lock()
	p, err := a.js.buildPluginVM("test", "evolv.on('evolution', function(e){ __bridge({kind: e.kind, name: e.name}); });")
	a.js.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// install a bridge function into the plugin VM
	a.js.vms = append(a.js.vms, p)
	err = p.vm.Set("__bridge", func(m map[string]any) {
		select {
		case done <- m:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	go p.loop()
	// note: evolv.on registered the handler BEFORE __bridge existed — fine.
	a.bus.Publish("evolution", map[string]any{"kind": "skill", "name": "deep-work"})
	select {
	case m := <-done:
		if m["kind"] != "skill" {
			t.Fatalf("wrong payload: %v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("plugin never reacted to the evolution event")
	}
}

// evolv.every must fire on a real timer inside the organism.
func TestPluginEveryFires(t *testing.T) {
	a := testAgent(t)
	ticks := make(chan int, 8)
	a.js.mu.Lock()
	p, err := a.js.buildPluginVM("ticker", "evolv.every(60, function(){ __tick(); });")
	a.js.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	a.js.vms = append(a.js.vms, p)
	_ = p.vm.Set("__tick", func() { ticks <- 1 })
	go p.loop()
	deadline := time.After(2 * time.Second)
	n := 0
	for {
		select {
		case <-ticks:
			n++
			if n >= 3 {
				return // three live ticks — the timer is real
			}
		case <-deadline:
			t.Fatalf("expected >=3 ticks, got %d", n)
		}
	}
}

// Dispatch must never block on a plugin that stopped reading its queue.
func TestDispatchNeverBlocks(t *testing.T) {
	a := testAgent(t)
	a.js.mu.Lock()
	p0, err := a.js.buildPluginVM("silent", "1;")
	a.js.vms = append(a.js.vms, p0)
	a.js.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// no loop started — queue fills up and stays full
	for i := 0; i < pluginEventQueue+50; i++ {
		a.js.Dispatch(bus.Event{Type: "evolution", Payload: json.RawMessage(`{}`)})
	}
}
