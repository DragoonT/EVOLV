package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedTestRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"internal/server/handler.go":      "package server\n\n// HandleToken validates a token.\nfunc HandleToken(tok string) error {\n\treturn nil\n}\n",
		"internal/server/handler_test.go": "package server\n\nfunc TestToken(t *testing.T) { /* token */ }\n",
		"web/app.ts":                      "export function tokenize(s: string) {\n  const token = s;\n  return token;\n}\n",
		"README.md":                       "# EVOLV\nTokens are validated by HandleToken.\nSee documentation.\n",
		"node_modules/pkg/index.js":       "token token token\n",
		"binary.dat":                      "\x00token\x00",
	}
	for name, content := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestSearchFindsAndRanks(t *testing.T) {
	root := seedTestRepo(t)

	res := Scan(root, Options{Query: "HandleToken"})
	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if len(res.Hits) < 2 {
		t.Fatalf("expected at least 2 hits, got %d", len(res.Hits))
	}
	// Declaration in handler.go should rank higher than README
	if res.Hits[0].Path != "internal/server/handler.go" {
		t.Fatalf("expected declaration to rank first, got %s", res.Hits[0].Path)
	}
	if res.Hits[0].Line != 4 {
		t.Fatalf("expected line 4, got line %d", res.Hits[0].Line)
	}
}

func TestSearchCaseSensitive(t *testing.T) {
	root := seedTestRepo(t)

	resInsensitive := Scan(root, Options{Query: "token", Exts: []string{"ts"}})
	if len(resInsensitive.Hits) == 0 {
		t.Fatal("expected hits for case-insensitive token in web/app.ts")
	}

	resSensitive := Scan(root, Options{Query: "TOKEN", CaseSens: true, Exts: []string{"ts"}})
	if len(resSensitive.Hits) != 0 {
		t.Fatalf("expected 0 hits for TOKEN with case-sensitivity, got %d", len(resSensitive.Hits))
	}
}

func TestSearchRegex(t *testing.T) {
	root := seedTestRepo(t)

	res := Scan(root, Options{Query: `func\s+Handle\w+`, Regex: true})
	if res.Error != "" {
		t.Fatalf("regex error: %s", res.Error)
	}
	if len(res.Hits) == 0 {
		t.Fatal("expected regex match")
	}
	if res.Hits[0].Path != "internal/server/handler.go" {
		t.Fatalf("expected match in handler.go, got: %s", res.Hits[0].Path)
	}
}

func TestSearchExtAndGlobFilter(t *testing.T) {
	root := seedTestRepo(t)

	// Ext filter
	resGo := Scan(root, Options{Query: "token", Exts: []string{"go"}})
	for _, h := range resGo.Hits {
		if !strings.HasSuffix(h.Path, ".go") {
			t.Fatalf("expected only .go files, got: %s", h.Path)
		}
	}

	// Glob filter
	resWeb := Scan(root, Options{Query: "token", PathGlob: "web/*"})
	for _, h := range resWeb.Hits {
		if !strings.HasPrefix(h.Path, "web/") {
			t.Fatalf("expected only web/ path, got: %s", h.Path)
		}
	}
}

func TestSearchContextLines(t *testing.T) {
	root := seedTestRepo(t)

	res := Scan(root, Options{Query: "HandleToken", Context: 1})
	foundLine4 := false
	for _, h := range res.Hits {
		if h.Path == "internal/server/handler.go" && h.Line == 4 {
			foundLine4 = true
			if len(h.Before) == 0 || !strings.Contains(h.Before[0], "// HandleToken") {
				t.Fatalf("expected Before context line for line 4, got: %v", h.Before)
			}
			if len(h.After) == 0 || !strings.Contains(h.After[0], "return nil") {
				t.Fatalf("expected After context line for line 4, got: %v", h.After)
			}
		}
	}
	if !foundLine4 {
		t.Fatal("expected hit on line 4 of handler.go")
	}
}

func TestSearchSkipsNodeModulesAndBinary(t *testing.T) {
	root := seedTestRepo(t)

	res := Scan(root, Options{Query: "token"})
	for _, h := range res.Hits {
		if strings.Contains(h.Path, "node_modules") {
			t.Fatalf("node_modules was not skipped: %s", h.Path)
		}
		if strings.Contains(h.Path, "binary.dat") {
			t.Fatalf("binary file was not skipped: %s", h.Path)
		}
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	root := seedTestRepo(t)
	res := Scan(root, Options{Query: "  "})
	if res.Error == "" {
		t.Fatal("expected error on empty query")
	}
}
