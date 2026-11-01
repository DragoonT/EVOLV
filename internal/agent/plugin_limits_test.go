package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dxn111/EVOLV/internal/bus"
)

// TestPlugin_InfiniteLoopLoadTimeout verifies that a plugin containing a top-level
// infinite loop does not freeze the agent process and times out cleanly.
func TestPlugin_InfiniteLoopLoadTimeout(t *testing.T) {
	a := testAgent(t)
	a.js.SetPluginLoadTimeout(100 * time.Millisecond)

	a.js.mu.Lock()
	_, err := a.js.buildPluginVM("infinite_loader", "while(true) {}", nil)
	a.js.mu.Unlock()

	if err == nil {
		t.Fatal("expected error on top-level infinite loop, got nil")
	}
	if !errors.Is(err, ErrExecutionTimeout) && !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
}

// TestPlugin_CallStackCeiling verifies that deep recursion in a plugin triggers
// the stack overflow guard rather than crashing the Go runtime.
func TestPlugin_CallStackCeiling(t *testing.T) {
	a := testAgent(t)
	a.js.SetPluginLoadTimeout(2 * time.Second)

	a.js.mu.Lock()
	_, err := a.js.buildPluginVM("deep_recursion", `
		function recurse() {
			return recurse();
		}
		recurse();
	`, nil)
	a.js.mu.Unlock()

	if err == nil {
		t.Fatal("expected stack overflow error, got nil")
	}
	if !errors.Is(err, ErrCallStackExceeded) && !strings.Contains(strings.ToLower(err.Error()), "stack") {
		t.Fatalf("expected call stack error, got: %v", err)
	}
}

// TestPlugin_TimerTimeoutDoesNotHangLoop verifies that an infinite loop in a timer
// callback is interrupted after the event timeout and publishes plugin:error without stalling the loop.
func TestPlugin_TimerTimeoutDoesNotHangLoop(t *testing.T) {
	a := testAgent(t)
	a.js.SetPluginLoadTimeout(1 * time.Second)

	sub, _ := a.bus.Subscribe()
	defer a.bus.Unsubscribe(sub)

	a.js.mu.Lock()
	p, err := a.js.buildPluginVM("hanging_timer", `
		evolv.every(50, function() {
			while(true) {}
		});
	`, nil)
	a.js.mu.Unlock()
	if err != nil {
		t.Fatalf("failed to build plugin: %v", err)
	}

	p.SetEventTimeout(80 * time.Millisecond)
	go p.loop()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-sub.Events():
			if ev.Type == "plugin:error" {
				var payload map[string]string
				if err := json.Unmarshal(ev.Payload, &payload); err == nil {
					if payload["plugin"] == "hanging_timer" && payload["type"] == "timer" {
						if strings.Contains(payload["error"], "timeout") {
							return // Success: received plugin:error with timeout
						}
					}
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for plugin:error event from hanging timer")
		}
	}
}

// TestPlugin_EventHandlerTimeoutDoesNotHangLoop verifies that an infinite loop in an
// event callback is interrupted and does not prevent subsequent event handling.
func TestPlugin_EventHandlerTimeoutDoesNotHangLoop(t *testing.T) {
	a := testAgent(t)
	a.js.SetPluginLoadTimeout(1 * time.Second)

	sub, _ := a.bus.Subscribe()
	defer a.bus.Unsubscribe(sub)

	healthyChan := make(chan bool, 1)

	a.js.mu.Lock()
	p, err := a.js.buildPluginVM("event_handler", `
		evolv.on("hang_event", function() {
			while(true) {}
		});
		evolv.on("healthy_event", function() {
			__healthy();
		});
	`, nil)
	a.js.mu.Unlock()
	if err != nil {
		t.Fatalf("failed to build plugin: %v", err)
	}

	_ = p.vm.Set("__healthy", func() {
		healthyChan <- true
	})

	p.SetEventTimeout(80 * time.Millisecond)
	a.js.mu.Lock()
	a.js.vms = append(a.js.vms, p)
	a.js.mu.Unlock()

	go p.loop()

	// Dispatch the hanging event first
	a.js.Dispatch(bus.Event{Type: "hang_event", Payload: json.RawMessage(`{}`)})

	deadline := time.After(2 * time.Second)
	foundError := false
	for !foundError {
		select {
		case ev := <-sub.Events():
			if ev.Type == "plugin:error" {
				var payload map[string]string
				if err := json.Unmarshal(ev.Payload, &payload); err == nil {
					if payload["plugin"] == "event_handler" && payload["type"] == "event:hang_event" {
						foundError = true
					}
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for plugin:error event")
		}
	}

	// Dispatch healthy event: VM must have cleared the interrupt and process it successfully
	a.js.Dispatch(bus.Event{Type: "healthy_event", Payload: json.RawMessage(`{}`)})

	select {
	case <-healthyChan:
		// Succeeded: VM remained usable after earlier handler was interrupted
	case <-time.After(2 * time.Second):
		t.Fatal("healthy event was never processed after earlier handler timed out")
	}
}

// TestTool_InfiniteLoopTimeout verifies that a tool with an infinite loop
// times out cleanly when executed via RunTool.
func TestTool_InfiniteLoopTimeout(t *testing.T) {
	a := testAgent(t)
	a.js.SetToolTimeout(100 * time.Millisecond)

	a.js.mu.Lock()
	a.js.evalToolLocked("hang_tool", `
		function hang_tool(args) {
			while(true) {}
		}
	`)
	a.js.mu.Unlock()

	start := time.Now()
	_, err := a.js.RunTool("hang_tool", map[string]any{"x": 1})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from hanging tool, got nil")
	}
	if !errors.Is(err, ErrExecutionTimeout) && !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
	if elapsed > 1*time.Second {
		t.Fatalf("execution took too long: %v (expected ~100ms)", elapsed)
	}
}

// TestTool_CallStackCeiling verifies that recursive tools hit the stack ceiling cleanly.
func TestTool_CallStackCeiling(t *testing.T) {
	a := testAgent(t)
	a.js.SetToolTimeout(2 * time.Second)

	a.js.mu.Lock()
	a.js.evalToolLocked("recurse_tool", `
		function recurse_tool(args) {
			return recurse_tool(args);
		}
	`)
	a.js.mu.Unlock()

	_, err := a.js.RunTool("recurse_tool", map[string]any{})
	if err == nil {
		t.Fatal("expected error from recursive tool, got nil")
	}
	if !errors.Is(err, ErrCallStackExceeded) && !strings.Contains(strings.ToLower(err.Error()), "stack") {
		t.Fatalf("expected stack overflow error, got: %v", err)
	}
}

// TestTool_ExecuteActionIntegration verifies that agent.Execute handles tool timeouts gracefully in JSON.
func TestTool_ExecuteActionIntegration(t *testing.T) {
	a := testAgent(t)
	a.js.SetToolTimeout(80 * time.Millisecond)

	a.js.mu.Lock()
	a.js.evalToolLocked("slow_tool", `
		function slow_tool(args) {
			while(true) {}
		}
	`)
	a.js.mu.Unlock()

	raw := a.Execute(action{Action: "run_tool", Name: "slow_tool"})
	raw = strings.TrimPrefix(raw, "kernel:")
	var res map[string]any
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatalf("malformed execute json: %v", err)
	}

	if res["ok"] != false {
		t.Fatalf("expected ok=false, got %v", res["ok"])
	}
	errMsg, _ := res["error"].(string)
	if !strings.Contains(errMsg, "timeout") {
		t.Fatalf("expected error mentioning timeout, got: %q", errMsg)
	}
}

// TestPlugin_NormalExecutionPreserved verifies that normal plugins execute, register hooks,
// and fire without spurious timeouts.
func TestPlugin_NormalExecutionPreserved(t *testing.T) {
	a := testAgent(t)
	a.js.SetPluginLoadTimeout(1 * time.Second)

	done := make(chan string, 1)

	a.js.mu.Lock()
	p, err := a.js.buildPluginVM("healthy_plugin", `
		evolv.on("ping", function(data) {
			__notify(data.msg);
		});
	`, nil)
	a.js.mu.Unlock()
	if err != nil {
		t.Fatalf("failed to build plugin: %v", err)
	}

	_ = p.vm.Set("__notify", func(msg string) {
		done <- msg
	})

	p.SetEventTimeout(500 * time.Millisecond)
	a.js.mu.Lock()
	a.js.vms = append(a.js.vms, p)
	a.js.mu.Unlock()

	go p.loop()

	a.js.Dispatch(bus.Event{Type: "ping", Payload: json.RawMessage(`{"msg":"pong"}`)})

	select {
	case msg := <-done:
		if msg != "pong" {
			t.Fatalf("expected 'pong', got %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("normal plugin event was not processed")
	}
}
