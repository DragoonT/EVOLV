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
