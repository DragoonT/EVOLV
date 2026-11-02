package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkbenchStaleWriteConflict(t *testing.T) {
	s, project := newWbServer(t)
	path := filepath.Join(project, "notes.txt")
	if err := os.WriteFile(path, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	// read returns a hash
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/wb/read?path=notes.txt", nil))
	var readResp struct {
		Hash    string `json:"hash"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &readResp); err != nil || readResp.Hash == "" {
		t.Fatalf("read = %s", rec.Body.String())
	}

	// another writer changes the file
	if err := os.WriteFile(path, []byte("v2-other"), 0o644); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{"path": "notes.txt", "content": "v3-mine", "expected_hash": readResp.Hash})
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/wb/write", strings.NewReader(string(body))))
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale write -> %d: %s", rec.Code, rec.Body.String())
	}
	var conflict struct {
		Conflict    bool   `json:"conflict"`
		CurrentHash string `json:"current_hash"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &conflict); err != nil || !conflict.Conflict || conflict.CurrentHash == "" {
		t.Fatalf("conflict body = %s", rec.Body.String())
	}
	raw, _ := os.ReadFile(path)
	if string(raw) != "v2-other" {
		t.Fatalf("stale write clobbered newer content: %q", raw)
	}

	// writing with the fresh hash succeeds and returns the new hash
	body, _ = json.Marshal(map[string]any{"path": "notes.txt", "content": "v3-mine", "expected_hash": conflict.CurrentHash})
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/wb/write", strings.NewReader(string(body))))
	if rec.Code != http.StatusOK {
		t.Fatalf("fresh write -> %d: %s", rec.Code, rec.Body.String())
	}
	raw, _ = os.ReadFile(path)
	if string(raw) != "v3-mine" {
		t.Fatalf("content = %q", raw)
	}
}
