package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const samplePlan = `{"title":"billing","objective":"charge once","steps":[
 {"id":"schema","title":"schema","verify":"go test ./db/..."},
 {"id":"service","title":"service","after":["schema"]},
 {"id":"e2e","title":"e2e","after":["service"],"verify":"go test ./..."}
]}`

func TestPlanCompileOK(t *testing.T) {
	s, _ := newWbServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/plan/compile", strings.NewReader(samplePlan)))
	if rec.Code != http.StatusOK {
		t.Fatalf("compile -> %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		OK    bool                  `json:"ok"`
		Tasks []struct{ ID string } `json:"tasks"`
		Waves [][]string            `json:"waves"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || len(out.Tasks) != 3 {
		t.Fatalf("out = %+v", out)
	}
	if len(out.Waves) != 3 {
		t.Fatalf("waves = %v", out.Waves)
	}
}

func TestPlanCompileRejectsCycle(t *testing.T) {
	s, _ := newWbServer(t)
	body := `{"title":"bad","steps":[{"id":"a","title":"a","after":["b"]},{"id":"b","title":"b","after":["a"]}]}`
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/plan/compile", strings.NewReader(body)))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cycle -> %d", rec.Code)
	}
	var out struct {
		OK       bool     `json:"ok"`
		Problems []string `json:"problems"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.OK || len(out.Problems) == 0 {
		t.Fatalf("out = %+v", out)
	}
}

func TestPlanStartCreatesRun(t *testing.T) {
	s, _ := newWbServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/plan/start", strings.NewReader(samplePlan)))
	if rec.Code != http.StatusOK {
		t.Fatalf("start -> %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Run struct {
			ID    string                `json:"id"`
			Tasks []struct{ ID string } `json:"tasks"`
		} `json:"run"`
		Waves [][]string `json:"waves"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Run.ID == "" || len(out.Run.Tasks) != 3 {
		t.Fatalf("run = %+v", out.Run)
	}
	// the run is visible through the runs API
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs", nil))
	if !strings.Contains(rec.Body.String(), out.Run.ID) {
		t.Fatalf("run missing from list: %s", rec.Body.String())
	}
}

func TestPlanStartRefusesBrokenPlan(t *testing.T) {
	s, _ := newWbServer(t)
	body := `{"title":"bad","steps":[{"title":"a","after":["ghost"]}]}`
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/plan/start", strings.NewReader(body)))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("broken plan start -> %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs", nil))
	if strings.Contains(rec.Body.String(), `"title":"bad"`) {
		t.Fatal("broken plan created a run")
	}
}

func TestPlanCompileBadJSON(t *testing.T) {
	s, _ := newWbServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/plan/compile", strings.NewReader("{")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad json -> %d", rec.Code)
	}
}
