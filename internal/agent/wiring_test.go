package agent

import (
	"testing"
	"time"
)

// The agent's bus must feed plugin sandboxes: publish on the bus, the
// plugin's evolv.on fires. This is the nervous-system wiring end-to-end.
func TestAgentFeedsBusToPlugins(t *testing.T) {
	a := testAgent(t)
	done := make(chan string, 1)
	a.js.mu.Lock()
	p, err := a.js.buildPluginVM("wired", "evolv.on('evolution', function(e){ __b(e.kind); });")
	if err == nil {
		a.js.vms = append(a.js.vms, p)
	}
	a.js.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.vm.Set("__b", func(s string) { done <- s }); err != nil {
		t.Fatal(err)
	}
	go p.loop()
	a.commit("skill", "wired-test", "/tmp/x", "nervous system check") // publishes on the bus
	select {
	case k := <-done:
		if k != "skill" {
			t.Fatalf("got %q", k)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bus → plugin wiring broken")
	}
}
