package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dxn111/EVOLV/internal/bus"
	"github.com/dxn111/EVOLV/internal/workspace"
)

func repoMapTestAgent(t *testing.T) *Agent {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, workspace.WorkspaceDir), 0o755); err != nil {
		t.Fatal(err)
	}
	ws := &workspace.W{
		Root: root,
		Config: &workspace.Config{
			APIKeys:  map[string]string{},
			BaseURLs: map[string]string{},
		},
	}
	return New(ws, bus.New(), nil, "test")
}

func TestMapRepoBuildsTreeSymbolsAndCache(t *testing.T) {
	a := repoMapTestAgent(t)
	project := t.TempDir()
	mustWrite := func(rel, content string) {
		t.Helper()
		path := filepath.Join(project, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("main.go", "package main\ntype Server struct{}\nfunc Start() {}\nfunc (s *Server) Stop() {}\n")
	mustWrite("web/app.ts", "export class App {}\nexport const boot = async () => true\n")
	mustWrite("node_modules/noise/index.js", "function hidden() {}")

	a.ws.Config.ProjectDir = project
	res := execOK(t, a, action{Action: "map_repo", Depth: 4})
	if res["ok"] != true || res["root"] != project {
		t.Fatalf("map_repo: %v", res)
	}
	files := res["files"].([]any)
	for _, f := range files {
		if f == "node_modules/" || f == "node_modules/noise/index.js" {
			t.Fatalf("ignored dependency appeared in map: %v", files)
		}
	}
	if !hasMappedSymbol(res, "main.go", "type", "Server") ||
		!hasMappedSymbol(res, "main.go", "function", "Start") ||
		!hasMappedSymbol(res, "main.go", "method", "Server.Stop") ||
		!hasMappedSymbol(res, "web/app.ts", "class", "App") ||
		!hasMappedSymbol(res, "web/app.ts", "function", "boot") {
		t.Fatalf("missing symbols: %v", res["symbols"])
	}

	raw, err := os.ReadFile(a.ws.Dir(workspace.RepoMapFile))
	if err != nil {
		t.Fatalf("cache missing: %v", err)
	}
	var cached repoMap
	if err := json.Unmarshal(raw, &cached); err != nil || cached.Root != project {
		t.Fatalf("bad cache: root=%q err=%v", cached.Root, err)
	}

	// Mapping the same project again must replace the existing cache cleanly,
	// including on Windows where file replacement semantics differ.
	mustWrite("second.go", "package main\nfunc AddedLater() {}\n")
	res = execOK(t, a, action{Action: "map_repo", Depth: 4})
	if res["ok"] != true || !hasMappedSymbol(res, "second.go", "function", "AddedLater") {
		t.Fatalf("map_repo refresh: %v", res)
	}
	raw, err = os.ReadFile(a.ws.Dir(workspace.RepoMapFile))
	if err != nil {
		t.Fatalf("refreshed cache missing: %v", err)
	}
	if err := json.Unmarshal(raw, &cached); err != nil {
		t.Fatalf("bad refreshed cache: %v", err)
	}
}

func TestMapRepoExplicitPathAndDepth(t *testing.T) {
	a := repoMapTestAgent(t)
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "a", "b", "deep.go"), []byte("package b\nfunc Deep() {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := execOK(t, a, action{Action: "map_repo", Path: project, Depth: 1})
	if res["ok"] != true {
		t.Fatalf("map_repo: %v", res)
	}
	if hasMappedSymbol(res, "a/b/deep.go", "function", "Deep") {
		t.Fatalf("depth limit ignored: %v", res)
	}
}

func TestMapRepoRejectsMissingRoot(t *testing.T) {
	a := repoMapTestAgent(t)
	res := execOK(t, a, action{Action: "map_repo", Path: filepath.Join(t.TempDir(), "missing")})
	if res["ok"] != false {
		t.Fatalf("missing root should fail: %v", res)
	}
}

func hasMappedSymbol(res map[string]any, path, kind, name string) bool {
	for _, file := range res["symbols"].([]any) {
		fm := file.(map[string]any)
		if fm["path"] != path {
			continue
		}
		for _, symbol := range fm["symbols"].([]any) {
			sm := symbol.(map[string]any)
			if sm["kind"] == kind && sm["name"] == name {
				return true
			}
		}
	}
	return false
}
