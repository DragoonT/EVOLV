package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func seedSearchProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"internal/server/handler.go":      "package server\n\n// HandleToken validates a token.\nfunc HandleToken(tok string) error {\n\treturn nil\n}\n",
		"internal/server/handler_test.go": "package server\n\nfunc TestToken(t *testing.T) { /* token */ }\n",
		"web/app.ts":                      "export function tokenize(s: string) { return s }\n",
		"README.md":                       "Tokens are validated by HandleToken.\n",
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

func searchVia(t *testing.T, s *Server, query string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/wb/search?"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("search %s -> %d: %s", query, rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func hitPaths(out map[string]any) []string {
	hits, _ := out["hits"].([]any)
	paths := make([]string, 0, len(hits))
	for _, h := range hits {
		m, _ := h.(map[string]any)
		paths = append(paths, m["path"].(string))
	}
	return paths
}

func TestSearchFindsAndRanks(t *testing.T) {
	s, _ := newWbServer(t)
	root := seedSearchProject(t)
	s.ws.Config.ProjectDir = root

	out := searchVia(t, s, "q=token")
	paths := hitPaths(out)
	if len(paths) == 0 {
		t.Fatalf("no hits: %v", out)
	}
	// node_modules is skipped
	for _, p := range paths {
		if len(p) > 12 && p[:12] == "node_modules" {
			t.Fatalf("searched node_modules: %v", paths)
		}
	}
	// Ranking intent: declaration lines beat prose; test files sink below
	// implementation files. (A file may legitimately outrank another when it
	// contains more matches — the assertion is about the declaration.)
	rank := map[string]int{}
	for i, p := range paths {
		if _, seen := rank[p]; !seen {
			rank[p] = i
		}
	}
	declIdx, ok := rank["internal/server/handler.go"]
	if !ok {
		t.Fatalf("handler.go missing from hits: %v", paths)
	}
	readmeIdx, ok := rank["README.md"]
	if ok && readmeIdx < declIdx {
		t.Fatalf("prose outranked a declaration: %v", paths)
	}
	testIdx, ok := rank["internal/server/handler_test.go"]
	if ok && testIdx < declIdx {
		t.Fatalf("test file outranked implementation: %v", paths)
	}
	if len(paths) > 0 && paths[len(paths)-1] != "README.md" && rank["README.md"] < len(paths)-1 {
		t.Logf("ranked order: %v", paths)
	}
}

func TestSearchExtensionFilter(t *testing.T) {
	s, _ := newWbServer(t)
	s.ws.Config.ProjectDir = seedSearchProject(t)
	out := searchVia(t, s, "q=token&ext=ts")
	for _, p := range hitPaths(out) {
		if filepath.Ext(p) != ".ts" {
			t.Fatalf("ext filter leaked %s", p)
		}
	}
}

func TestSearchRegexAndCase(t *testing.T) {
	s, _ := newWbServer(t)
	s.ws.Config.ProjectDir = seedSearchProject(t)
	out := searchVia(t, s, "q=Handle%5BA-Z%5D%5Cw%2B&regex=1")
	if len(hitPaths(out)) == 0 {
		t.Fatalf("regex found nothing: %v", out)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/wb/search?q=%5B&regex=1", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid regex -> %d", rec.Code)
	}
	// case-sensitive miss
	out = searchVia(t, s, "q=TOKEN&case=1")
	if len(hitPaths(out)) != 0 {
		t.Fatalf("case-sensitive search matched lowercase: %v", hitPaths(out))
	}
}

func TestSearchPathFilter(t *testing.T) {
	s, _ := newWbServer(t)
	s.ws.Config.ProjectDir = seedSearchProject(t)
	out := searchVia(t, s, "q=token&path=web/")
	paths := hitPaths(out)
	if len(paths) == 0 {
		t.Fatalf("path filter found nothing")
	}
	for _, p := range paths {
		if filepath.ToSlash(p) != "web/app.ts" {
			t.Fatalf("path filter leaked %s", p)
		}
	}
}

func TestSearchSkipsBinary(t *testing.T) {
	s, _ := newWbServer(t)
	s.ws.Config.ProjectDir = seedSearchProject(t)
	out := searchVia(t, s, "q=token&limit=200")
	for _, p := range hitPaths(out) {
		if p == "binary.dat" {
			t.Fatal("searched a binary file")
		}
	}
}

func TestSearchLimitValidation(t *testing.T) {
	s, _ := newWbServer(t)
	s.ws.Config.ProjectDir = seedSearchProject(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/wb/search?q=token&limit=abc", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit -> %d", rec.Code)
	}
	out := searchVia(t, s, "q=token&limit=1")
	if len(hitPaths(out)) != 1 {
		t.Fatalf("limit ignored: %v", hitPaths(out))
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	s, _ := newWbServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/wb/search?q=", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty query -> %d", rec.Code)
	}
}
