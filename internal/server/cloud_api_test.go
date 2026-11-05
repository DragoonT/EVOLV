package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dxn111/EVOLV/internal/bus"
	"github.com/dxn111/EVOLV/internal/workspace"
)

func TestServerCloudRoutesIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	ws := &workspace.W{Root: tmpDir, Config: &workspace.Config{}}
	b := bus.New()
	s := New(ws, b, nil, nil)

	// 1. Provision a tenant via /api/cloud/tenants
	tenantJSON := `{"id":"team-1","name":"Engineering Team","tier":"pro"}`
	tenantReq := httptest.NewRequest(http.MethodPost, "/api/cloud/tenants", strings.NewReader(tenantJSON))
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, tenantReq)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create tenant failed: got %d, want %d: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// 2. Query usage via /api/cloud/usage?tenant_id=team-1
	req := httptest.NewRequest(http.MethodGet, "/api/cloud/usage?tenant_id=team-1", nil)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /api/cloud/usage?tenant_id=team-1, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Tenant struct {
			ID   string `json:"id"`
			Tier string `json:"tier"`
		} `json:"tenant"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Tenant.ID != "team-1" || payload.Tenant.Tier != "pro" {
		t.Errorf("unexpected tenant response: %+v", payload)
	}
}
