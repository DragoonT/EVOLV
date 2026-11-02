package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunsAPIEndToEnd(t *testing.T) {
	s, _ := newWbServer(t)

	create := `{"title":"ship a feature","objective":"make it real","tasks":[
		{"id":"model","title":"data model","status":"queued"},
		{"id":"api","title":"endpoint","depends_on":["model"]},
		{"id":"tests","title":"regression tests","depends_on":["api"]}
	]}`
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/runs/create", strings.NewReader(create)))
	if rec.Code != http.StatusOK {
		t.Fatalf("create -> %d: %s", rec.Code, rec.Body.String())
	}
	var run struct {
		ID    string `json:"id"`
		Tasks []struct {
			ID string `json:"id"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if run.ID == "" || len(run.Tasks) != 3 {
		t.Fatalf("run = %+v", run)
	}

	// advance model through a legal path, with evidence
	for _, step := range []string{"running", "verifying"} {
		body, _ := json.Marshal(map[string]any{"run": run.ID, "task": "model", "status": step})
		rec = httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/runs/transition", strings.NewReader(string(body))))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s -> %d: %s", step, rec.Code, rec.Body.String())
		}
	}
	body, _ := json.Marshal(map[string]any{
		"run": run.ID, "task": "model", "status": "completed",
		"verification": map[string]any{"kind": "test", "command": "go test ./...", "passed": true, "exit_code": 0},
	})
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/runs/transition", strings.NewReader(string(body))))
	if rec.Code != http.StatusOK {
		t.Fatalf("complete -> %d: %s", rec.Code, rec.Body.String())
	}

	// an illegal transition is a conflict, not a silent success
	body, _ = json.Marshal(map[string]any{"run": run.ID, "task": "model", "status": "running"})
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/runs/transition", strings.NewReader(string(body))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("illegal transition -> %d", rec.Code)
	}

	// list + detail expose progress and ready tasks
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), run.ID) {
		t.Fatalf("list -> %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs/detail?id="+run.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("detail -> %d: %s", rec.Code, rec.Body.String())
	}
	var detail struct {
		Progress struct {
			Completed int `json:"completed"`
			Total     int `json:"total"`
		} `json:"progress"`
		Ready []struct {
			ID string `json:"id"`
		} `json:"ready"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Progress.Completed != 1 || detail.Progress.Total != 3 {
		t.Fatalf("progress = %+v", detail.Progress)
	}
	if len(detail.Ready) != 1 || detail.Ready[0].ID != "api" {
		t.Fatalf("ready = %+v", detail.Ready)
	}

	// journal events are readable
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs/events?id="+run.ID, nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "task.moved") {
		t.Fatalf("events -> %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRunsAPIValidation(t *testing.T) {
	s, _ := newWbServer(t)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/runs/create", strings.NewReader(`{"title":""}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty create -> %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs/detail", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing id -> %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs/detail?id=nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id -> %d", rec.Code)
	}
}
