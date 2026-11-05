package cloud

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCloudAPIEndpoints(t *testing.T) {
	meter := NewMeter()
	secret := "webhook-secret-key"
	bot := NewBot(secret, meter, nil, nil)
	api := NewAPI(bot, meter)

	mux := http.NewServeMux()
	api.RegisterRoutes(mux)

	// 1. Provision Tenant via API
	tenantPayload := []byte(`{"id":"tenant-alpha","name":"Alpha Inc","tier":"pro"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/cloud/tenants", bytes.NewReader(tenantPayload))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create tenant: got status %d, want %d: %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	// 2. Query Tenant
	req = httptest.NewRequest(http.MethodGet, "/api/cloud/tenants?id=tenant-alpha", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("get tenant: got status %d, want %d", rec.Code, http.StatusOK)
	}

	// 3. Webhook Dispatch with Signature
	commentEv := WebhookCommentEvent{Action: "created"}
	commentEv.Issue.Number = 77
	commentEv.Issue.Title = "Memory leak in parser"
	commentEv.Comment.Body = "@evolv-bot fix memory leak"
	commentEv.Repository.FullName = "alpha/service"
	body, _ := json.Marshal(commentEv)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req = httptest.NewRequest(http.MethodPost, "/api/cloud/webhook/github?tenant_id=tenant-alpha", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issue_comment")
	req.Header.Set("X-Hub-Signature-256", sig)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("webhook dispatch: got status %d, want %d: %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}

	// 4. Query Usage
	req = httptest.NewRequest(http.MethodGet, "/api/cloud/usage?tenant_id=tenant-alpha", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("get usage: got status %d, want %d", rec.Code, http.StatusOK)
	}

	var usageResp struct {
		Tenant Tenant `json:"tenant"`
		Usage  Usage  `json:"usage"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&usageResp); err != nil {
		t.Fatalf("decode usage resp: %v", err)
	}
	if usageResp.Usage.RunsCount != 1 {
		t.Errorf("usage runs count = %d, want 1", usageResp.Usage.RunsCount)
	}
}
