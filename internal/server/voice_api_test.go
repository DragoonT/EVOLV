package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVoiceEndpoints(t *testing.T) {
	s, _ := newWbServer(t)

	// 1. POST /api/voice/speak with valid text
	postBody, _ := json.Marshal(map[string]any{
		"text":  "Voice system test utterance.",
		"voice": "natural",
		"rate":  1.0,
		"pitch": 1.0,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/voice/speak", bytes.NewReader(postBody))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/voice/speak expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var res map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if res["ok"] != true {
		t.Fatalf("expected ok=true, got %+v", res)
	}
	ut, ok := res["utterance"].(map[string]any)
	if !ok || ut["text"] != "Voice system test utterance." {
		t.Fatalf("expected utterance text 'Voice system test utterance.', got %+v", ut)
	}

	// 2. POST /api/voice/speak with empty text -> 400
	badBody, _ := json.Marshal(map[string]any{
		"text": "   ",
	})
	badReq := httptest.NewRequest(http.MethodPost, "/api/voice/speak", bytes.NewReader(badBody))
	badRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty text, got %d", badRec.Code)
	}

	// 3. GET /api/voice/history
	histReq := httptest.NewRequest(http.MethodGet, "/api/voice/history", nil)
	histRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(histRec, histReq)
	if histRec.Code != http.StatusOK {
		t.Fatalf("GET /api/voice/history expected 200, got %d: %s", histRec.Code, histRec.Body.String())
	}
	var histRes map[string]any
	if err := json.Unmarshal(histRec.Body.Bytes(), &histRes); err != nil {
		t.Fatalf("invalid history json: %v", err)
	}
	if histRes["ok"] != true {
		t.Fatalf("expected ok=true in history, got %+v", histRes)
	}
	items, ok := histRes["utterances"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("expected at least 1 utterance in history, got %+v", histRes)
	}

	// 4. Method not allowed tests
	getSpeakReq := httptest.NewRequest(http.MethodGet, "/api/voice/speak", nil)
	getSpeakRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(getSpeakRec, getSpeakReq)
	if getSpeakRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET /api/voice/speak, got %d", getSpeakRec.Code)
	}

	postHistReq := httptest.NewRequest(http.MethodPost, "/api/voice/history", nil)
	postHistRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(postHistRec, postHistReq)
	if postHistRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST /api/voice/history, got %d", postHistRec.Code)
	}
}
