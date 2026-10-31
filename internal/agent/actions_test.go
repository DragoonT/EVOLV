package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dxn111/EVOLV/internal/bus"
	"github.com/dxn111/EVOLV/internal/workspace"
)

func testAgent(t *testing.T) *Agent {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws, err := workspace.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	return New(ws, bus.New(), nil, "test")
}

func execOK(t *testing.T, a *Agent, act action) map[string]any {
	t.Helper()
	raw := a.Execute(act)
	raw = strings.TrimPrefix(raw, "kernel:")
	// parse last {...}
	i := strings.Index(raw, "{")
	if i < 0 {
		t.Fatalf("no json in %q", raw)
	}
	return nilOrPanic(raw[i:])
}

func nilOrPanic(s string) map[string]any {
	var m map[string]any
	if err := json.NewDecoder(strings.NewReader(s)).Decode(&m); err != nil {
		panic(err)
	}
	return m
}

func TestAbsolutePathsAnywhere(t *testing.T) {
	a := testAgent(t)
	outside := filepath.Join(os.TempDir(), "evolv-outside-test")
	os.MkdirAll(outside, 0o755)
	defer os.RemoveAll(outside)

	res := execOK(t, a, action{Action: "write_file", Path: filepath.Join(outside, "hello.txt"), Content: "hi"})
	if res["ok"] != true {
		t.Fatalf("write outside workspace failed: %v", res)
	}
	res = execOK(t, a, action{Action: "read_file", Path: filepath.Join(outside, "hello.txt")})
	if res["content"] != "hi" {
		t.Fatalf("read outside failed: %v", res)
	}
}

func TestEditFile(t *testing.T) {
	a := testAgent(t)
	p := filepath.Join(a.ws.Root, "t.txt")
	os.WriteFile(p, []byte("alpha beta"), 0o644)
	res := execOK(t, a, action{Action: "edit_file", Path: p, Find: "beta", Replace: "gamma"})
	if res["ok"] != true || res["replacements"].(float64) != 1 {
		t.Fatalf("edit_file: %v", res)
	}
	raw, _ := os.ReadFile(p)
	if string(raw) != "alpha gamma" {
		t.Fatalf("content=%q", raw)
	}
}

func TestSetProjectAndShellCwd(t *testing.T) {
	a := testAgent(t)
	proj := t.TempDir()
	res := execOK(t, a, action{Action: "set_project", Path: proj})
	if res["ok"] != true || a.ws.Config.ProjectDir != proj {
		t.Fatalf("set_project: %v", res)
	}
	res = execOK(t, a, action{Action: "shell", Command: "pwd"})
	if res["output"] != proj+"\n" {
		t.Fatalf("shell cwd: %v", res)
	}
}

func TestTreeAndListDir(t *testing.T) {
	a := testAgent(t)
	os.MkdirAll(filepath.Join(a.ws.Root, "workspace/ui/components"), 0o755)
	os.WriteFile(filepath.Join(a.ws.Root, "workspace/ui/components/x.html"), []byte("x"), 0o644)
	res := execOK(t, a, action{Action: "tree", Path: "", Depth: 5})
	if res["ok"] != true {
		t.Fatalf("tree: %v", res)
	}
	files := res["files"].([]any)
	if len(files) == 0 {
		t.Fatal("tree empty")
	}
}

func TestSnapshotAndRollback(t *testing.T) {
	a := testAgent(t)
	skill := filepath.Join(a.ws.Root, "workspace/skills/dream.md")
	os.WriteFile(skill, []byte("v1"), 0o644)

	res := execOK(t, a, action{Action: "snapshot"})
	if res["ok"] != true || res["snapshot"] == "" {
		t.Fatalf("snapshot: %v", res)
	}
	id := res["snapshot"].(string)

	// mutate the genome after the snapshot
	os.WriteFile(skill, []byte("v2-broken"), 0o644)
	os.WriteFile(filepath.Join(a.ws.Root, "workspace/ui/index.html"), []byte("<h1>broken</h1>"), 0o644)

	res = execOK(t, a, action{Action: "rollback", Path: id})
	if res["ok"] != true {
		t.Fatalf("rollback: %v", res)
	}
	raw, _ := os.ReadFile(skill)
	if string(raw) != "v1" {
		t.Fatalf("skill not restored: %q", raw)
	}
	raw, _ = os.ReadFile(filepath.Join(a.ws.Root, "workspace/ui/index.html"))
	if string(raw) == "<h1>broken</h1>" {
		t.Fatal("ui not restored")
	}
	// restore with an unknown id must fail cleanly
	res = execOK(t, a, action{Action: "rollback", Path: "19990101-000000"})
	if res["ok"] != false {
		t.Fatalf("bogus rollback should fail: %v", res)
	}
}

func TestSnapshotRestoreWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ws, err := workspace.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if ids := ws.ListSnapshots(); len(ids) != 0 {
		t.Fatalf("fresh workspace has snapshots: %v", ids)
	}
	id, err := ws.Snapshot()
	if err != nil || id == "" {
		t.Fatalf("snapshot: %v %v", id, err)
	}
	if ids := ws.ListSnapshots(); len(ids) != 1 || ids[0] != id {
		t.Fatalf("list: %v", ids)
	}
	if err := ws.Restore(id); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if err := ws.Restore("nope"); err == nil {
		t.Fatal("bogus restore should error")
	}
}
