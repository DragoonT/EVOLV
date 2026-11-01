package agent

import (
	"strings"
	"testing"
)

// TestTypedToolManifest: evolve_tool with a schema persists a versioned
// manifest; re-evolving bumps the version.
func TestTypedToolManifest(t *testing.T) {
	a := testAgent(t)
	code := `function adder(args){ return {sum: args.a + args.b}; }`
	res := execOK(t, a, action{Action: "evolve_tool", Name: "adder", Desc: "adds two numbers",
		Code: code, Schema: map[string]string{"a": "number", "b": "number"}})
	if res["ok"] != true {
		t.Fatalf("evolve_tool failed: %v", res)
	}
	if res["version"] != float64(1) {
		t.Fatalf("first version should be 1, got %v", res["version"])
	}
	m := readToolManifest(a, "adder")
	if m == nil || m.Version != 1 || m.Schema["a"] != "number" || m.Description != "adds two numbers" {
		t.Fatalf("bad manifest: %+v", m)
	}
	if m.Updated == "" {
		t.Error("manifest.Updated must be stamped")
	}
	// re-evolve → version bumps
	res = execOK(t, a, action{Action: "evolve_tool", Name: "adder", Desc: "adder v2", Code: code,
		Schema: map[string]string{"a": "number", "b": "number", "c": "number"}})
	if res["version"] != float64(2) {
		t.Fatalf("second version should be 2, got %v", res["version"])
	}
}

// TestTypedToolValidation: run_tool enforces the schema before code runs.
func TestTypedToolValidation(t *testing.T) {
	a := testAgent(t)
	a.Execute(action{Action: "evolve_tool", Name: "adder", Code: `function adder(args){ return {sum: args.a + args.b}; }`,
		Schema: map[string]string{"a": "number", "b": "number"}})

	res := execOK(t, a, action{Action: "run_tool", Name: "adder", Args: map[string]any{"a": 2.0, "b": 3.0}})
	if res["ok"] != true {
		t.Fatalf("valid call rejected: %v", res)
	}
	// wrong type → precise error, not a JS stack trace
	res = execOK(t, a, action{Action: "run_tool", Name: "adder", Args: map[string]any{"a": "two", "b": 3.0}})
	if res["ok"] != false || !strings.Contains(res["error"].(string), `arg "a" must be number, got string`) {
		t.Fatalf("wrong type not rejected precisely: %v", res)
	}
	// missing arg → precise error
	res = execOK(t, a, action{Action: "run_tool", Name: "adder", Args: map[string]any{"a": 2.0}})
	if res["ok"] != false || !strings.Contains(res["error"].(string), `missing required arg "b"`) {
		t.Fatalf("missing arg not rejected: %v", res)
	}
	// nil args on a typed tool → missing-arg errors, not panic
	res = execOK(t, a, action{Action: "run_tool", Name: "adder"})
	if res["ok"] != false || !strings.Contains(res["error"].(string), `missing required arg "a"`) {
		t.Fatalf("nil args not handled: %v", res)
	}
}

// TestUntypedToolsStillRun: tools without a manifest keep working (back-compat).
func TestUntypedToolsStillRun(t *testing.T) {
	a := testAgent(t)
	a.Execute(action{Action: "evolve_tool", Name: "legacy", Code: `function legacy(args){ return {echo: args.x}; }`})
	if readToolManifest(a, "legacy") != nil {
		t.Fatal("legacy tool must not have a manifest")
	}
	res := execOK(t, a, action{Action: "run_tool", Name: "legacy", Args: map[string]any{"x": "hi"}})
	if res["ok"] != true {
		t.Fatalf("untyped tool broke: %v", res)
	}
}

// TestListToolsManifests: list_tools reports versions and schemas.
func TestListToolsManifests(t *testing.T) {
	a := testAgent(t)
	res := execOK(t, a, action{Action: "list_tools"})
	if res["ok"] != true || res["count"] != float64(0) {
		t.Fatalf("empty list_tools failed: %v", res)
	}
	a.Execute(action{Action: "evolve_tool", Name: "adder", Desc: "adds", Code: `function adder(args){ return {}; }`,
		Schema: map[string]string{"a": "number"}})
	res = execOK(t, a, action{Action: "list_tools"})
	if res["count"] != float64(1) {
		t.Fatalf("count wrong: %v", res)
	}
	tools := res["tools"].([]any)
	ti := tools[0].(map[string]any)
	if ti["name"] != "adder" || ti["version"] != float64(1) {
		t.Fatalf("tool info wrong: %v", ti)
	}
	schema := ti["schema"].(map[string]any)
	if schema["a"] != "number" {
		t.Fatalf("schema not reported: %v", schema)
	}
}

// TestSchemaBadTypeRejected: an unknown type in the schema fails at evolve time.
func TestSchemaBadTypeRejected(t *testing.T) {
	a := testAgent(t)
	a.Execute(action{Action: "evolve_tool", Name: "odd", Code: `function odd(args){ return {}; }`,
		Schema: map[string]string{"x": "float128"}})
	res := execOK(t, a, action{Action: "run_tool", Name: "odd", Args: map[string]any{"x": 1.0}})
	if res["ok"] != false || !strings.Contains(res["error"].(string), "unknown type") {
		t.Fatalf("bad schema type not rejected: %v", res)
	}
}
