package server

import (
	"encoding/json"
	"net/http"

	"github.com/dxn111/EVOLV/internal/execution"
)

// handlePlanCompile validates a plan and returns its task graph, execution
// waves and any problems — without creating a run. It is the "will this work?"
// check that a client (or the agent) should call before committing.
func (s *Server) handlePlanCompile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var spec execution.PlanSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid plan JSON: "+err.Error())
		return
	}
	res := execution.CompilePlan(spec)
	out := map[string]any{
		"ok":       len(res.Problems) == 0,
		"tasks":    res.Tasks,
		"waves":    res.Waves,
		"order":    res.Order,
		"problems": res.Problems,
	}
	if len(res.Problems) > 0 {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	writeJSONOK(w, out)
}

// handlePlanStart compiles and stores a plan as a durable run in one call.
func (s *Server) handlePlanStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var spec execution.PlanSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid plan JSON: "+err.Error())
		return
	}
	res := execution.CompilePlan(spec)
	if len(res.Problems) > 0 {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": false, "problems": res.Problems, "wave_count": 0,
		})
		return
	}
	store, err := s.runsStore()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	run, err := store.CreateRun(execution.NewRunID(), spec.Title, spec.Objective, res.Tasks)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONOK(w, map[string]any{
		"ok":         true,
		"run":        run,
		"waves":      res.Waves,
		"order":      res.Order,
		"task_total": len(run.Tasks),
	})
}
