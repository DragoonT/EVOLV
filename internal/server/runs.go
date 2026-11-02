package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dxn111/EVOLV/internal/execution"
)

// runsDir is where durable execution runs live inside the genome.
const runsDir = "workspace/execution/runs"

// runsStore lazily opens the execution store. It is opened on first use so a
// broken run directory never blocks boot.
func (s *Server) runsStore() (*execution.Store, error) {
	s.execMu.Lock()
	defer s.execMu.Unlock()
	if s.execStore != nil {
		return s.execStore, nil
	}
	store, err := execution.Open(s.ws.Dir(runsDir))
	if err != nil {
		return nil, err
	}
	s.execStore = store
	return store, nil
}

// handleRuns lists durable execution runs.
func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	store, err := s.runsStore()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	runs := store.List()
	out := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		progress := run.Graph().Progress()
		out = append(out, map[string]any{
			"id": run.ID, "title": run.Title, "objective": run.Objective,
			"status": run.Status, "created_at": run.CreatedAt, "updated_at": run.UpdatedAt,
			"progress": progress,
			"ready":    len(run.Graph().Ready()),
		})
	}
	writeJSONOK(w, map[string]any{"runs": out})
}

// handleRunDetail returns one run with its graph, blocked tasks and evidence.
func (s *Server) handleRunDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "id is required")
		return
	}
	store, err := s.runsStore()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	run, ok := store.Get(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "run not found")
		return
	}
	graph := run.Graph()
	writeJSONOK(w, map[string]any{
		"run":      run,
		"progress": graph.Progress(),
		"blocked":  graph.Blocked(),
		"problems": graph.Validate(),
		"ready":    graph.Ready(),
	})
}

// handleRunEvents returns the journal history for one run.
func (s *Server) handleRunEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "id is required")
		return
	}
	store, err := s.runsStore()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	events, err := store.Events(id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSONOK(w, map[string]any{"id": id, "events": events})
}

// handleRunCreate registers a run from a plan (used by the agent's plan tool
// and by clients that want durable tracking for a multi-step assignment).
func (s *Server) handleRunCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Title     string           `json:"title"`
		Objective string           `json:"objective"`
		Tasks     []execution.Task `json:"tasks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Title) == "" || len(in.Tasks) == 0 {
		writeJSONError(w, http.StatusBadRequest, "title and tasks are required")
		return
	}
	store, err := s.runsStore()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	run, err := store.CreateRun(execution.NewRunID(), in.Title, in.Objective, in.Tasks)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSONOK(w, run)
}

// handleRunTransition moves a task between lifecycle states.
func (s *Server) handleRunTransition(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Run        string                  `json:"run"`
		Task       string                  `json:"task"`
		Status     execution.Status        `json:"status"`
		Reason     string                  `json:"reason"`
		Verify     *execution.Verification `json:"verification"`
		Checkpoint string                  `json:"checkpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Run == "" || in.Task == "" || in.Status == "" {
		writeJSONError(w, http.StatusBadRequest, "run, task and status are required")
		return
	}
	store, err := s.runsStore()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if in.Verify != nil {
		if err := store.RecordVerification(in.Run, in.Task, *in.Verify); err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
	}
	if in.Checkpoint != "" {
		_ = store.Checkpoint(in.Run, in.Task, in.Checkpoint)
	}
	task, err := store.Transition(in.Run, in.Task, in.Status)
	if err != nil {
		code := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			code = http.StatusNotFound
		}
		if strings.Contains(err.Error(), "cannot move") {
			code = http.StatusConflict
		}
		writeJSONError(w, code, err.Error())
		return
	}
	if in.Status == execution.StatusFailed && in.Reason != "" {
		_ = store.RecordAttempt(in.Run, in.Task, in.Reason)
	}
	type runState struct {
		Run *execution.Run `json:"run"`
	}
	updated, _ := store.Get(in.Run)
	writeJSONOK(w, map[string]any{"task": task, "run": updated})
}
