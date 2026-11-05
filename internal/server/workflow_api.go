package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dxn111/EVOLV/internal/workflow"
)

func (s *Server) workflowRunner() *workflow.Runner {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ag != nil && s.ag.WorkflowRunner() != nil {
		return s.ag.WorkflowRunner()
	}
	if s.wfRunner == nil {
		s.wfRunner = workflow.NewRunner(workflow.RunnerConfig{
			ProjectRoot: func() string {
				if s.ws != nil && s.ws.Config != nil && s.ws.Config.ProjectDir != "" {
					return s.ws.Config.ProjectDir
				}
				if s.ws != nil {
					return s.ws.Root
				}
				return ""
			},
			BusPub: func(event string, payload any) {
				if s.bus != nil {
					s.bus.Publish(event, payload)
				}
			},
		})
	}
	return s.wfRunner
}

func (s *Server) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	runner := s.workflowRunner()
	if runner == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "workflow runner unavailable")
		return
	}
	pipelines := runner.ListPipelines()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":        true,
		"pipelines": pipelines,
		"count":     len(pipelines),
	})
}

func (s *Server) handleWorkflowRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Workflow string               `json:"workflow"`
		Name     string               `json:"name"`
		Custom   *workflow.CustomSpec `json:"custom"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}
	wfName := strings.TrimSpace(in.Workflow)
	if wfName == "" {
		wfName = strings.TrimSpace(in.Name)
	}
	if wfName == "" && in.Custom == nil {
		writeJSONError(w, http.StatusBadRequest, "workflow name or custom spec required")
		return
	}

	runner := s.workflowRunner()
	if runner == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "workflow runner unavailable")
		return
	}

	ctx, cancel := r.Context(), func() {}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		ctx, cancel = r.Context(), func() {}
		_ = cancel
	}

	res, err := runner.Run(ctx, wfName, in.Custom)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "workflow execution failed: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":          res.Succeeded(),
		"workflow":    res.Name,
		"succeeded":   res.Succeeded(),
		"duration_ms": float64(res.Duration.Microseconds()) / 1000.0,
		"steps":       res.Steps,
		"outputs":     res.Outputs,
		"order":       res.Order,
		"timestamp":   time.Now().UnixMilli(),
	})
}

func (s *Server) handleWorkflowHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	var hist []workflow.Result
	if runner := s.workflowRunner(); runner != nil {
		hist = runner.History()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"history": hist,
		"count":   len(hist),
	})
}
