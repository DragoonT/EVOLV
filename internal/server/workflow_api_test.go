package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWorkflowEndpoints(t *testing.T) {
	s, _ := newWbServer(t)

	// 1. GET /api/workflows
	getReq := httptest.NewRequest(http.MethodGet, "/api/workflows", nil)
	getRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /api/workflows expected 200, got %d: %s", getRec.Code, getRec.Body.String())
	}
	var getRes map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &getRes); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if getRes["ok"] != true {
		t.Fatalf("expected ok=true, got: %+v", getRes)
	}
	pipelines, ok := getRes["pipelines"].([]any)
	if !ok || len(pipelines) == 0 {
		t.Fatalf("expected at least 1 pipeline, got: %+v", getRes)
	}

	// 2. POST /api/workflows/run with custom fast DAG
	postBody, _ := json.Marshal(map[string]any{
		"workflow": "custom",
		"custom": map[string]any{
			"name": "custom_api_test",
			"steps": []map[string]any{
				{
					"id":      "step_a",
					"command": "go env GOOS",
				},
				{
					"id":         "step_b",
					"depends_on": []string{"step_a"},
					"command":    "go env GOARCH",
				},
			},
		},
	})
	postReq := httptest.NewRequest(http.MethodPost, "/api/workflows/run", bytes.NewReader(postBody))
	postRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(postRec, postReq)

	if postRec.Code != http.StatusOK {
		t.Fatalf("POST /api/workflows/run expected 200, got %d: %s", postRec.Code, postRec.Body.String())
	}
	var postRes map[string]any
	if err := json.Unmarshal(postRec.Body.Bytes(), &postRes); err != nil {
		t.Fatalf("invalid json in run response: %v", err)
	}
	if postRes["ok"] != true || postRes["succeeded"] != true {
		t.Fatalf("expected workflow success, got: %+v", postRes)
	}

	// 3. GET /api/workflows/history
	histReq := httptest.NewRequest(http.MethodGet, "/api/workflows/history", nil)
	histRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(histRec, histReq)

	if histRec.Code != http.StatusOK {
		t.Fatalf("GET /api/workflows/history expected 200, got %d: %s", histRec.Code, histRec.Body.String())
	}
	var histRes map[string]any
	if err := json.Unmarshal(histRec.Body.Bytes(), &histRes); err != nil {
		t.Fatalf("invalid json in history response: %v", err)
	}
	items, ok := histRes["history"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("expected at least 1 history item, got: %+v", histRes)
	}

	// 4. Bad request checks
	badBody, _ := json.Marshal(map[string]any{})
	badReq := httptest.NewRequest(http.MethodPost, "/api/workflows/run", bytes.NewReader(badBody))
	badRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty run request, got %d", badRec.Code)
	}

	// 5. Method not allowed checks
	notAllowedReq := httptest.NewRequest(http.MethodPost, "/api/workflows", nil)
	notAllowedRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(notAllowedRec, notAllowedReq)
	if notAllowedRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST /api/workflows, got %d", notAllowedRec.Code)
	}
}
