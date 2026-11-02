package agent

import (
	"strings"
	"testing"
	"time"
)

func loadPluginForTest(t *testing.T, a *Agent, name, code string, perms []string) *pluginVM {
	t.Helper()
	a.js.mu.Lock()
	vm, err := a.js.buildPluginVM(name, code, perms)
	a.js.mu.Unlock()
	if err != nil {
		t.Fatalf("build plugin: %v", err)
	}
	return vm
}

func TestPluginTimerLimitEnforced(t *testing.T) {
	a, _ := mockAgent(t)
	sub, _ := a.bus.Subscribe()
	defer a.bus.Unsubscribe(sub)
	p := loadPluginForTest(t, a, "timers", `
		for (let i = 0; i < 100; i++) evolv.every(1000, () => {});
	`, nil)
	defer p.Stop()
	p.mu.Lock()
	n := len(p.timers)
	p.mu.Unlock()
	if n > maxPluginTimers {
		t.Fatalf("timers = %d, limit %d", n, maxPluginTimers)
	}
	found := false
	deadline := time.After(500 * time.Millisecond)
	for !found {
		select {
		case ev := <-sub.Events():
			if ev.Type == "plugin:warning" && strings.Contains(string(ev.Payload), "every()") {
				found = true
			}
		case <-deadline:
			t.Fatal("limit warning never published")
		}
	}
}

func TestPluginHandlerLimitAndOff(t *testing.T) {
	a, _ := mockAgent(t)
	p := loadPluginForTest(t, a, "handlers", `
		for (let i = 0; i < 200; i++) evolv.on("evolution", () => {});
	`, nil)
	defer p.Stop()
	p.mu.Lock()
	n := len(p.handlers["evolution"])
	p.mu.Unlock()
	if n > maxPluginHandlers {
		t.Fatalf("handlers = %d, limit %d", n, maxPluginHandlers)
	}
	if _, err := p.vm.RunString(`evolv.off("evolution")`); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	after := len(p.handlers["evolution"])
	p.mu.Unlock()
	if after != 0 {
		t.Fatalf("off() left %d handlers", after)
	}
}

func TestPluginBridgeBudget(t *testing.T) {
	a, _ := mockAgent(t)
	p := loadPluginForTest(t, a, "bridge", `evolv.log("hi")`, nil)
	defer p.Stop()
	ok := 0
	for i := 0; i < maxPluginBridgeCalls+10; i++ {
		if p.bridgeAllowed() {
			ok++
		}
	}
	if ok != maxPluginBridgeCalls {
		t.Fatalf("bridge allowed %d calls, want %d", ok, maxPluginBridgeCalls)
	}
}

func TestPluginStopRetiresLoop(t *testing.T) {
	a, _ := mockAgent(t)
	p := loadPluginForTest(t, a, "stopme", `evolv.every(50, () => {})`, nil)
	done := make(chan struct{})
	go func() { p.loop(); close(done) }()
	p.Stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("plugin loop did not stop")
	}
	p.Stop() // idempotent
}

func TestPluginReloadStopsPreviousRuntime(t *testing.T) {
	a, _ := mockAgent(t)
	first := loadPluginForTest(t, a, "reload", `evolv.every(50, () => {})`, nil)
	go first.loop()
	a.js.mu.Lock()
	a.js.vms = append(a.js.vms, first)
	a.js.mu.Unlock()
	a.js.LoadAll()
	select {
	case <-first.stop:
	case <-time.After(2 * time.Second):
		t.Fatal("reload did not stop the previous runtime")
	}
	a.js.mu.Lock()
	remaining := len(a.js.vms)
	a.js.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("vms after LoadAll with no plugins on disk = %d", remaining)
	}
}
