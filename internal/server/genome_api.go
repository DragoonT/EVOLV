package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dxn111/EVOLV/internal/genome"
)

// genomeIndex opens the organ registry for the running workspace.
func (s *Server) genomeIndex() (*genome.Index, error) {
	return genome.OpenIndex(s.ws.Root)
}

// handleOrgans lists every grown organ with health, worst first. The existing
// /api/genome route stays the plain genome file listing; this is the organ view.
func (s *Server) handleOrgans(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	idx, err := s.genomeIndex()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows := idx.HealthReport(time.Now().UTC())
	active, problem := 0, 0
	for _, row := range rows {
		if row.Status == "active" {
			active++
		}
		if row.Verdict == "broken" || row.Verdict == "flaky" {
			problem++
		}
	}
	writeJSONOK(w, map[string]any{
		"organs": rows,
		"summary": map[string]any{
			"total": len(rows), "active": active, "needing_attention": problem,
		},
	})
}

// handleGenomeVersions lists archived versions for one organ.
func (s *Server) handleGenomeVersions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	idx, err := s.genomeIndex()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	kind := genome.Kind(strings.TrimSpace(r.URL.Query().Get("kind")))
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if !kind.Valid() || name == "" {
		writeJSONError(w, http.StatusBadRequest, "kind and name are required")
		return
	}
	asset, ok := idx.Get(kind, name)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "no such organ")
		return
	}
	writeJSONOK(w, map[string]any{"asset": asset, "versions": idx.Versions(kind, name)})
}

// handleGenomeRollback restores a previous version. It requires confirm=true:
// replacing live code is exactly the kind of action that needs a deliberate
// second signal.
func (s *Server) handleGenomeRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Kind    string `json:"kind"`
		Name    string `json:"name"`
		Version int    `json:"version"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if !in.Confirm {
		writeJSONError(w, http.StatusPreconditionRequired, "rollback requires confirm:true")
		return
	}
	kind := genome.Kind(strings.TrimSpace(in.Kind))
	if !kind.Valid() || strings.TrimSpace(in.Name) == "" || in.Version < 1 {
		writeJSONError(w, http.StatusBadRequest, "kind, name and version are required")
		return
	}
	idx, err := s.genomeIndex()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	content, err := idx.ReadVersion(kind, in.Name, in.Version)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("version %d of that organ is not archived", in.Version))
		return
	}
	plan, err := idx.PlanRollback(kind, in.Name, in.Version)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := writeWorkspaceFile(s, plan.Path, content); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	asset, err := idx.PlanRestore(plan, content)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	_ = idx.ArchiveVersion(kind, in.Name, asset.Version, content)
	if s.ag != nil {
		s.ag.ReloadGrownCode()
	}
	s.bus.Publish("genome:rollback", map[string]any{
		"kind": string(kind), "name": in.Name, "to": in.Version, "now": asset.Version,
	})
	writeJSONOK(w, map[string]any{"ok": true, "asset": asset, "restored": in.Version})
}

// handleGenomeStatus quarantines or re-activates an organ.
func (s *Server) handleGenomeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	idx, err := s.genomeIndex()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	asset, err := idx.SetStatus(genome.Kind(in.Kind), in.Name, in.Status)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSONOK(w, map[string]any{"ok": true, "asset": asset})
}

// handleLessons returns what the organism has learned for a described task.
func (s *Server) handleLessons(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	book, err := genome.OpenBook(s.ws.Root)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	text := strings.TrimSpace(r.URL.Query().Get("q"))
	traits := genome.Traits(text)
	insights := book.InsightsFor(traits, 8)
	writeJSONOK(w, map[string]any{
		"query": text, "traits": traits, "insights": insights,
		"advice": genome.RenderAdvice(insights), "samples": len(book.Playbooks),
	})
}

// handleGenomeBackfill registers organs that predate the index.
func (s *Server) handleGenomeBackfill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if s.ag == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "agent runtime unavailable")
		return
	}
	added := s.ag.BackfillOrgans()
	writeJSONOK(w, map[string]any{"ok": true, "registered": added})
}

// writeWorkspaceFile writes a workspace-relative file atomically.
func writeWorkspaceFile(s *Server, rel string, content []byte) error {
	return atomicWrite(s.ws.Dir(rel), content)
}
