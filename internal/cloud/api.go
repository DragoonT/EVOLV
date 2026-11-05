package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// API provides HTTP endpoints for cloud webhook intake, tenant management, and resource metering.
type API struct {
	bot   *Bot
	meter *Meter
}

// NewAPI instantiates cloud HTTP handlers.
func NewAPI(bot *Bot, meter *Meter) *API {
	return &API{
		bot:   bot,
		meter: meter,
	}
}

// RegisterRoutes binds cloud endpoints to the provided HTTP multiplexer.
func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/cloud/webhook/github", a.HandleGitHubWebhook)
	mux.HandleFunc("/api/cloud/usage", a.HandleUsage)
	mux.HandleFunc("/api/cloud/tenants", a.HandleTenants)
	mux.HandleFunc("/api/cloud/dispatch", a.HandleDispatch)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// HandleGitHubWebhook processes inbound GitHub webhook events.
func (a *API) HandleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10*1024*1024))
	if err != nil {
		writeError(w, http.StatusBadRequest, "reading payload: "+err.Error())
		return
	}

	// Verify GitHub HMAC signature
	sigHeader := r.Header.Get("X-Hub-Signature-256")
	if !VerifyWebhookSignature(a.bot.secret, body, sigHeader) {
		writeError(w, http.StatusUnauthorized, "invalid webhook signature")
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	if eventType == "" {
		writeError(w, http.StatusBadRequest, "missing X-GitHub-Event header")
		return
	}

	tenantID := r.URL.Query().Get("tenant_id")
	if tenantID == "" {
		tenantID = r.Header.Get("X-EVOLV-Tenant-ID")
	}
	if tenantID == "" {
		tenantID = "default"
	}

	// Ensure tenant exists in meter
	if a.meter != nil {
		if _, err := a.meter.GetTenant(tenantID); err != nil {
			a.meter.RegisterTenant(tenantID, "Default Tenant", TierFree)
		}
	}

	result, err := a.bot.HandleEvent(r.Context(), tenantID, eventType, body)
	if err != nil {
		writeError(w, http.StatusPaymentRequired, err.Error())
		return
	}

	if result == nil {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "ignored",
			"message": "event does not contain an actionable @evolv-bot command",
		})
		return
	}

	writeJSON(w, http.StatusAccepted, result)
}

// HandleUsage returns resource consumption metrics and tier quotas for a tenant.
func (a *API) HandleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	tenantID := r.URL.Query().Get("tenant_id")
	if tenantID == "" {
		tenantID = "default"
	}

	tenant, err := a.meter.GetTenant(tenantID)
	if err != nil {
		writeError(w, http.StatusNotFound, "tenant not found")
		return
	}

	usage, err := a.meter.GetUsage(tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	limits := DefaultTierLimits(tenant.Tier)

	writeJSON(w, http.StatusOK, map[string]any{
		"tenant": tenant,
		"limits": limits,
		"usage":  usage,
	})
}

// HandleTenants manages multi-tenant provisioning.
func (a *API) HandleTenants(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tenantID := r.URL.Query().Get("id")
		if tenantID == "" {
			writeError(w, http.StatusBadRequest, "id parameter required")
			return
		}
		tenant, err := a.meter.GetTenant(tenantID)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, tenant)

	case http.MethodPost:
		var req struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Tier Tier   `json:"tier"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
			return
		}
		if strings.TrimSpace(req.ID) == "" {
			writeError(w, http.StatusBadRequest, "id is required")
			return
		}
		if req.Tier == "" {
			req.Tier = TierFree
		}
		tenant := a.meter.RegisterTenant(req.ID, req.Name, req.Tier)
		writeJSON(w, http.StatusCreated, tenant)

	default:
		writeError(w, http.StatusMethodNotAllowed, "GET or POST required")
	}
}

// HandleDispatch provides direct API-triggered SWE runs.
func (a *API) HandleDispatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}

	var req struct {
		TenantID    string `json:"tenant_id"`
		Repo        string `json:"repo"`
		IssueNumber int    `json:"issue_number"`
		Title       string `json:"title"`
		Action      string `json:"action"` // "fix", "review", "test", "evolv"
		Prompt      string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}

	if req.TenantID == "" {
		req.TenantID = "default"
	}
	if req.Repo == "" || req.Action == "" {
		writeError(w, http.StatusBadRequest, "repo and action are required")
		return
	}

	// Ensure tenant exists
	if _, err := a.meter.GetTenant(req.TenantID); err != nil {
		a.meter.RegisterTenant(req.TenantID, "Default Tenant", TierFree)
	}

	cmd := &Command{
		Action: req.Action,
		Args:   strings.Fields(req.Prompt),
		Raw:    req.Prompt,
	}

	if req.Title == "" {
		req.Title = fmt.Sprintf("API Run: %s", req.Action)
	}

	ctx, cancel := contextWithTimeout(r.Context(), 15*time.Minute)
	defer cancel()

	result, err := a.bot.ExecuteCommand(ctx, req.TenantID, req.Repo, req.IssueNumber, req.Title, cmd)
	if err != nil {
		writeError(w, http.StatusPaymentRequired, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func contextWithTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}
