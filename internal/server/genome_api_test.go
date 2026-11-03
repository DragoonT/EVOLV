package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dxn111/EVOLV/internal/genome"
)

// seedOrgan writes an organ file and registers it, as the agent would.
func seedOrgan(t *testing.T, s *Server, kind genome.Kind, name string, versions ...string) {
	t.Helper()
	rel := map[genome.Kind]string{
		genome.KindTool:   filepath.ToSlash(filepath.Join("workspace", "tools", name+".js")),
		genome.KindSkill:  filepath.ToSlash(filepath.Join("workspace", "skills", name+".md")),
		genome.KindPrompt: filepath.ToSlash(filepath.Join("workspace", "prompts", name+".md")),
	}[kind]
	path := s.ws.Dir(rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	idx, err := genome.OpenIndex(s.ws.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range versions {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		asset, _, err := idx.Record(kind, name, rel, []byte(content), genome.Provenance{Trigger: "test"})
		if err != nil {
			t.Fatal(err)
		}
		if err := idx.ArchiveVersion(kind, name, asset.Version, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOrgansListShowsHealth(t *testing.T) {
	s, _ := newWbServer(t)
	seedOrgan(t, s, genome.KindTool, "good", "function good(){return 1}")
	seedOrgan(t, s, genome.KindTool, "bad", "function bad(){throw 1}")
	idx, _ := genome.OpenIndex(s.ws.Root)
	idx.NoteUse(genome.KindTool, "good", true, "")
	idx.NoteUse(genome.KindTool, "bad", false, "boom")
	idx.NoteUse(genome.KindTool, "bad", false, "boom")

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/organs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("organs -> %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Organs []struct {
			Name    string `json:"name"`
			Verdict string `json:"verdict"`
		} `json:"organs"`
		Summary struct {
			Total            int `json:"total"`
			NeedingAttention int `json:"needing_attention"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Summary.Total != 2 || out.Summary.NeedingAttention != 1 {
		t.Fatalf("summary = %+v", out.Summary)
	}
	if out.Organs[0].Name != "bad" || out.Organs[0].Verdict != "broken" {
		t.Fatalf("worst first = %+v", out.Organs[0])
	}
}

func TestOrganRollbackRequiresConfirmAndRestores(t *testing.T) {
	s, _ := newWbServer(t)
	seedOrgan(t, s, genome.KindSkill, "debug", "# Debug\n\nv1 body", "# Debug\n\nv2 body")

	rec := httptest.NewRecorder()
	body := `{"kind":"skill","name":"debug","version":1}`
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/organs/rollback", strings.NewReader(body)))
	if rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("no confirm -> %d, want 428", rec.Code)
	}

	rec = httptest.NewRecorder()
	body = `{"kind":"skill","name":"debug","version":1,"confirm":true}`
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/organs/rollback", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback -> %d: %s", rec.Code, rec.Body.String())
	}
	raw, err := os.ReadFile(filepath.Join(s.ws.Root, "workspace", "skills", "debug.md"))
	if err != nil || !strings.Contains(string(raw), "v1 body") {
		t.Fatalf("file not restored: %q", raw)
	}
	var out struct {
		Asset struct {
			Version int `json:"version"`
		} `json:"asset"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Asset.Version != 3 {
		t.Fatalf("rollback version = %d, want a new version 3", out.Asset.Version)
	}
}

func TestOrganVersionsEndpoint(t *testing.T) {
	s, _ := newWbServer(t)
	seedOrgan(t, s, genome.KindTool, "calc", "v1", "v2")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/organs/versions?kind=tool&name=calc", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("versions -> %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Versions []struct {
			Version int  `json:"version"`
			Current bool `json:"current"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Versions) != 2 || !out.Versions[0].Current || out.Versions[0].Version != 2 {
		t.Fatalf("versions = %+v", out.Versions)
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/organs/versions?kind=tool", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing name -> %d", rec.Code)
	}
}

func TestGenomeFileListingUnchanged(t *testing.T) {
	s, _ := newWbServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/genome", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("genome listing -> %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"files"`) {
		t.Fatalf("genome contract broken: %s", rec.Body.String())
	}
}

func TestLessonsEndpoint(t *testing.T) {
	s, _ := newWbServer(t)
	book, err := genome.OpenBook(s.ws.Root)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_ = book.Record(genome.Playbook{
			Traits: []string{"bugfix", "tests"}, Outcome: "verified",
			Verify: "go test ./...", Skills: []string{"systematic-debugging"},
			At: "2026-11-0" + string(rune('1'+i)),
		})
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/lessons?q=fix%20the%20broken%20login%20test", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("lessons -> %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Traits   []string `json:"traits"`
		Insights []struct {
			Success int    `json:"success_rate"`
			Advice  string `json:"advice"`
		} `json:"insights"`
		Advice string `json:"advice"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Traits) == 0 {
		t.Fatalf("no traits derived: %s", rec.Body.String())
	}
	if len(out.Insights) == 0 || out.Insights[0].Success != 100 {
		t.Fatalf("insights = %+v", out.Insights)
	}
	if !strings.Contains(out.Advice, "recorded outcomes") {
		t.Fatalf("advice = %q", out.Advice)
	}
}

func TestOrganStatusQuarantine(t *testing.T) {
	s, _ := newWbServer(t)
	seedOrgan(t, s, genome.KindTool, "risky", "function risky(){}")
	rec := httptest.NewRecorder()
	body := `{"kind":"tool","name":"risky","status":"quarantined"}`
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/organs/status", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status -> %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/organs/status", strings.NewReader(`{"kind":"tool","name":"risky","status":"bogus"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bogus status -> %d", rec.Code)
	}
}
