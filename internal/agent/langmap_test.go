package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func namesOf(syms []repoSymbol) []string {
	out := make([]string, 0, len(syms))
	for _, s := range syms {
		out = append(out, s.Kind+":"+s.Name)
	}
	return out
}

func hasSymbol(syms []repoSymbol, want string) bool {
	for _, s := range syms {
		if s.Kind+":"+s.Name == want {
			return true
		}
	}
	return false
}

func TestPythonSymbols(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"app.py": "class Service:\n    pass\n\nasync def handle(req):\n    return req\n\n@app.route('/users')\ndef users():\n    pass\n",
	})
	syms := languageSymbols(filepath.Join(root, "app.py"))
	for _, want := range []string{"class:Service", "function:handle", "function:users", "route:/users"} {
		if !hasSymbol(syms, want) {
			t.Fatalf("missing %s in %v", want, namesOf(syms))
		}
	}
}

func TestRustSymbols(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"lib.rs": "pub struct Engine {}\npub enum Mode { Fast }\npub trait Runner {}\npub async fn run() {}\nfn helper() {}\n",
	})
	syms := languageSymbols(filepath.Join(root, "lib.rs"))
	for _, want := range []string{"struct:Engine", "enum:Mode", "trait:Runner", "function:run", "function:helper"} {
		if !hasSymbol(syms, want) {
			t.Fatalf("missing %s in %v", want, namesOf(syms))
		}
	}
}

func TestRubyAndShellSymbols(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"lib.rb": "module Shop\n  class Cart\n    def total\n    end\n  end\nend\n",
		"run.sh": "#!/bin/sh\nbuild() {\n  echo hi\n}\n",
	})
	rb := languageSymbols(filepath.Join(root, "lib.rb"))
	if !hasSymbol(rb, "module:Shop") || !hasSymbol(rb, "class:Cart") || !hasSymbol(rb, "function:total") {
		t.Fatalf("ruby = %v", namesOf(rb))
	}
	sh := languageSymbols(filepath.Join(root, "run.sh"))
	if !hasSymbol(sh, "function:build") {
		t.Fatalf("shell = %v", namesOf(sh))
	}
}

func TestSQLSymbols(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"schema.sql": "CREATE TABLE users (id int);\ncreate or replace view active_users as select 1;\n",
	})
	syms := languageSymbols(filepath.Join(root, "schema.sql"))
	if !hasSymbol(syms, "table:users") || !hasSymbol(syms, "view:active_users") {
		t.Fatalf("sql = %v", namesOf(syms))
	}
}

func TestLanguageOf(t *testing.T) {
	cases := map[string]string{
		"a.go": "go", "a.ts": "typescript", "a.tsx": "tsx", "a.jsx": "jsx",
		"a.py": "python", "a.rs": "rust", "a.rb": "ruby", "a.ex": "elixir",
		"a.unknown": "",
	}
	for file, want := range cases {
		if got := languageOf(file); got != want {
			t.Errorf("languageOf(%s) = %q, want %q", file, got, want)
		}
	}
}

func TestProfileGoProject(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":               "module demo\n\ngo 1.22\n",
		"main.go":              "package main\n\nfunc main() {}\n",
		"internal/x/x.go":      "package x\n",
		"internal/x/x_test.go": "package x\n",
	})
	m, err := buildRepoMap(root, 4)
	if err != nil {
		t.Fatal(err)
	}
	if m.Profile == nil || m.Profile.Kind != kindGo {
		t.Fatalf("profile = %+v", m.Profile)
	}
	if m.Profile.Languages["go"] != 3 {
		t.Fatalf("languages = %v", m.Profile.Languages)
	}
	if m.Profile.Commands["test"] != "go test ./..." {
		t.Fatalf("commands = %v", m.Profile.Commands)
	}
	foundTestRoot := false
	for _, tr := range m.Profile.Tests {
		if tr == "internal/x/" {
			foundTestRoot = true
		}
	}
	if !foundTestRoot {
		t.Fatalf("test roots = %v", m.Profile.Tests)
	}
	if len(m.Profile.EntryPoints) == 0 || m.Profile.EntryPoints[0] != "main.go" {
		t.Fatalf("entry points = %v", m.Profile.EntryPoints)
	}
}

func TestProfileNodeProjectUsesItsOwnScripts(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"package.json": `{"name":"web","scripts":{"dev":"vite","build":"vite build","test":"vitest run"},"devDependencies":{"vite":"5","typescript":"5","react":"18"}}`,
		"src/main.tsx": "export function App() { return null }\n",
	})
	m, err := buildRepoMap(root, 4)
	if err != nil {
		t.Fatal(err)
	}
	if m.Profile.Kind != kindNode {
		t.Fatalf("kind = %s", m.Profile.Kind)
	}
	if m.Profile.Commands["test"] != "npm run test  # vitest run" {
		t.Fatalf("commands = %v", m.Profile.Commands)
	}
	found := false
	for _, fw := range m.Profile.Frameworks {
		if fw == "vite" || fw == "react" || fw == "typescript" {
			found = true
		}
	}
	if !found {
		t.Fatalf("frameworks = %v", m.Profile.Frameworks)
	}
	if m.Profile.Commands["dev"] == "" {
		t.Fatal("dev script not surfaced")
	}
}

func TestProfileRustAndPython(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		kind  projectKind
		test  string
	}{
		"rust":   {map[string]string{"Cargo.toml": "[package]\nname=\"x\"\n", "src/main.rs": "fn main() {}\n"}, kindRust, "cargo test"},
		"python": {map[string]string{"pyproject.toml": "[project]\n", "app.py": "def x():\n  pass\n"}, kindPython, "pytest"},
	} {
		root := t.TempDir()
		writeTree(t, root, tc.files)
		m, err := buildRepoMap(root, 3)
		if err != nil {
			t.Fatal(err)
		}
		if m.Profile.Kind != tc.kind {
			t.Fatalf("%s kind = %s", name, m.Profile.Kind)
		}
		if m.Profile.Commands["test"] != tc.test {
			t.Fatalf("%s commands = %v", name, m.Profile.Commands)
		}
	}
}

func TestMapRepoActionExposesProfile(t *testing.T) {
	a, ws := mockAgent(t)
	proj := t.TempDir()
	ws.Config.ProjectDir = proj
	writeTree(t, proj, map[string]string{"go.mod": "module demo\n\ngo 1.22\n", "main.go": "package main\nfunc main(){}\n"})
	res := execRun(t, a, action{Action: "map_repo"})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("map_repo failed: %v", res)
	}
	if res["kind"] != "go" {
		t.Fatalf("kind = %v", res["kind"])
	}
	cmds, _ := res["commands"].(map[string]any)
	if cmds["test"] != "go test ./..." {
		t.Fatalf("commands = %v", res["commands"])
	}
}
