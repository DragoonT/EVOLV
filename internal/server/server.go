// Package server wires the organism to the browser: static serving of the
// living UI, a WebSocket hub mirroring the bus, and a small JSON API
// (chat, settings, models, files).
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dxn111/EVOLV/internal/agent"
	"github.com/dxn111/EVOLV/internal/bus"
	"github.com/dxn111/EVOLV/internal/providers"
	"github.com/dxn111/EVOLV/internal/update"
	"github.com/dxn111/EVOLV/internal/workspace"
)

// Server is the organism's interface to the world.
type Server struct {
	ws        *workspace.W
	bus       *bus.Bus
	ag        *agent.Agent
	reg       *providers.Registry
	mux       *http.ServeMux
	upgr      websocket.Upgrader
	mu        sync.Mutex
	live      int
	update    *update.Info // newer release, if any
	genMu     sync.Mutex
	genCancel context.CancelFunc // cancels the in-flight generation (stop button)
}

// New builds the server.
func New(ws *workspace.W, b *bus.Bus, ag *agent.Agent, reg *providers.Registry) *Server {
	s := &Server{
		ws: ws, bus: b, ag: ag, reg: reg, mux: http.NewServeMux(),
		upgr: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true }, // localhost organism
		},
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleUI)
	s.mux.HandleFunc("/ws", s.handleWS)
	s.mux.HandleFunc("/api/chat", s.handleChat)
	s.mux.HandleFunc("/api/settings", s.handleSettings)
	s.mux.HandleFunc("/api/models", s.handleModels)
	s.mux.HandleFunc("/api/evolution", s.handleEvolution)
	s.mux.HandleFunc("/api/plan", s.handlePlan)
	s.mux.HandleFunc("/api/replay", s.handleReplay)
	s.mux.HandleFunc("/api/mind", s.handleMind)
	s.mux.HandleFunc("/api/genome", s.handleGenome)
	s.mux.HandleFunc("/api/files", s.handleFiles)
	s.mux.HandleFunc("/api/reset", s.handleReset)
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/update", s.handleUpdate)
	s.mux.HandleFunc("/api/update/run", s.handleUpdateRun)
	s.mux.HandleFunc("/api/snapshots", s.handleSnapshots)
	s.mux.HandleFunc("/api/snapshots/restore", s.handleSnapshotRestore)
	s.mux.HandleFunc("/api/snapshots/diff", s.handleSnapshotDiff)
	s.mux.HandleFunc("/api/dream", s.handleDream)
	s.mux.HandleFunc("/api/stop", s.handleStop)
	s.mux.HandleFunc("/api/export", s.handleExport)
	s.mux.HandleFunc("/api/wb/root", s.handleWbRoot)
	s.mux.HandleFunc("/api/wb/tree", s.handleWbTree)
	s.mux.HandleFunc("/api/wb/read", s.handleWbRead)
	s.mux.HandleFunc("/api/wb/write", s.handleWbWrite)
	s.mux.HandleFunc("/api/wb/mkdir", s.handleWbMkdir)
	s.mux.HandleFunc("/api/wb/delete", s.handleWbDelete)
	s.mux.HandleFunc("/api/wb/rename", s.handleWbRename)
	s.WatchReleases()
	s.ag.StartDreaming() // the organism wakes itself when idle
}

// Handler returns the root handler.
func (s *Server) Handler() http.Handler { return s.mux }

// liveCount tracks WS clients (delta +1/-1, returns new total).
func (s *Server) liveCount(delta int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.live += delta
	return s.live
}

// Listen binds and returns the chosen port.
func (s *Server) Listen(prefer int) (int, net.Listener, error) {
	port := workspace.PickPort(prefer)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return 0, nil, err
	}
	return port, ln, nil
}

// ---------- UI (the living frontend, served straight from disk) ----------

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	rel := ""
	switch {
	case r.URL.Path == "/":
		rel = workspace.UIRel() + "/index.html"
	case strings.HasPrefix(r.URL.Path, "/ui/"):
		rel = workspace.UIRel() + "/" + strings.TrimPrefix(r.URL.Path, "/ui/")
	case r.URL.Path == "/style.css" || r.URL.Path == "/app.js":
		rel = workspace.UIRel() + r.URL.Path // shell assets live beside the genome
	default:
		http.NotFound(w, r)
		return
	}
	p := filepath.Join(s.ws.Root, filepath.Clean(rel))
	if !strings.HasPrefix(p, s.ws.Root) {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		http.Error(w, "genome file missing: "+rel, 404)
		return
	}
	switch filepath.Ext(p) {
	case ".html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case ".js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".svg":
		w.Header().Set("Content-Type", "image/svg+xml")
	case ".json":
		w.Header().Set("Content-Type", "application/json")
	}
	w.Header().Set("Cache-Control", "no-store") // the body is always changing
	w.Write(data)
}

// ---------- WebSocket hub ----------

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgr.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] upgrade failed: %v", err)
		return
	}
	log.Printf("[ws] client connected (%d live)", s.liveCount(1))
	defer func() { log.Printf("[ws] client left (%d live)", s.liveCount(-1)); conn.Close() }()

	sub, replay := s.bus.Subscribe()
	defer s.bus.Unsubscribe(sub)

	for _, ev := range replay {
		if err := conn.WriteJSON(ev); err != nil {
			return
		}
	}
	if raw, err := json.Marshal(map[string]any{"agent": map[string]any{"model": s.ag.Model, "provider": s.currentProviderName()}, "evolutions": s.ag.EvolutionCount()}); err == nil {
		conn.WriteJSON(bus.Event{Type: "hello", Payload: raw, Time: time.Now().UTC().Format(time.RFC3339Nano)})
	}

	// reader: pings keepalive; writer: events
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case ev := <-sub.Events():
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
		case <-ticker.C:
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-sub.Closed():
			return
		}
	}
}

func (s *Server) currentProviderName() string {
	if s.reg != nil {
		return s.provName()
	}
	return "unknown"
}

// ---------- API ----------

type chatReq struct {
	Message string `json:"message"`
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var req chatReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Message) == "" {
		http.Error(w, `{"error":"message required"}`, 400)
		return
	}
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("chat panic: %v", rec)
				s.bus.Publish("agent:token", map[string]string{"text": "\n\n⚠️ kernel hiccup — evolution continues."})
			}
		}()
		// detached context: the organism keeps working after the HTTP reply
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		s.genMu.Lock()
		s.genCancel = cancel
		s.genMu.Unlock()
		defer func() {
			s.genMu.Lock()
			s.genCancel = nil
			s.genMu.Unlock()
			cancel()
		}()
		s.ag.Chat(ctx, req.Message)
		s.bus.Publish("agent:phase", map[string]string{"phase": "idle"})
	}()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := *s.ws.Config
		// never leak keys to the client, only whether they exist
		masked := map[string]bool{}
		for k, v := range cfg.APIKeys {
			masked[k] = v != ""
		}
		cfg.APIKeys = nil
		json.NewEncoder(w).Encode(map[string]any{"config": cfg, "keys_present": masked, "providers": s.reg.Names()})
	case http.MethodPost:
		var in workspace.Config
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "bad json", 400)
			return
		}
		c := s.ws.Config
		if in.Provider != "" {
			c.Provider = in.Provider
		}
		if in.Model != "" {
			c.Model = in.Model
		}
		if in.UserName != "" {
			c.UserName = in.UserName
		}
		if in.ProjectDir != "" || in.Theme != "" {
			// empty project_dir is only sent by the agent's set_project; the
			// settings drawer always resends the current value
			if in.ProjectDir != "" {
				c.ProjectDir = in.ProjectDir
			}
			if in.Theme != "" {
				c.Theme = in.Theme
			}
		}
		if in.Port > 0 {
			c.Port = in.Port
		}
		if in.HeartbeatMins > 0 {
			c.HeartbeatMins = in.HeartbeatMins
		}
		c.OpenBrowser = in.OpenBrowser
		c.AutoEvolve = in.AutoEvolve
		// keys: replace map wholesale if provided (masked GET means client
		// resends only changed keys prefixed with "set:")
		if in.APIKeys != nil {
			for k, v := range in.APIKeys {
				if v == "" {
					continue
				}
				c.APIKeys[k] = v
			}
		}
		if in.BaseURLs != nil {
			for k, v := range in.BaseURLs {
				if v != "" {
					c.BaseURLs[k] = v
				}
			}
		}
		s.ws.SaveConfig()
		s.applyProvider()
		s.bus.Publish("config:updated", map[string]string{"provider": c.Provider, "model": c.Model})
		w.Write([]byte(`{"ok":true}`))
	default:
		http.Error(w, "method", 405)
	}
}

// handleStop aborts the in-flight generation — the organism mid-thought
// simply stops. It does not roll back mutations it already committed.
func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	s.genMu.Lock()
	cancel := s.genCancel
	s.genMu.Unlock()
	if cancel != nil {
		cancel()
		s.bus.Publish("agent:token", map[string]string{"text": "\n\n*(stopped — the organism paused mid-thought)*\n"})
		w.Write([]byte(`{"ok":true}`))
		return
	}
	w.Write([]byte(`{"ok":false,"error":"nothing in flight"}`))
}

// applyProvider rebuilds the active provider from config.
func (s *Server) applyProvider() {
	c := s.ws.Config
	p, model := providers.Build(c.Provider, c.Model, c.APIKeys[c.Provider], c.BaseURLs)
	s.ag.SetProvider(p, model)
}

// handleExport turns a conversation into a standalone HTML transcript the
// user can share. The browser owns the conversation, so it POSTs the messages
// here and gets back one self-contained file (no JS, no external assets).
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var in struct {
		Title    string `json:"title"`
		Messages []struct {
			Role string `json:"role"`
			Text string `json:"text"`
		} `json:"messages"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "bad body", 400)
		return
	}
	if err := json.Unmarshal(body, &in); err != nil {
		http.Error(w, "bad json", 400)
		return
	}
	if in.Title == "" {
		in.Title = "EVOLV conversation"
	}
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>`)
	b.WriteString(html.EscapeString(in.Title))
	b.WriteString(`</title><style>
body{margin:0;background:#141311;color:#e8e3d8;font:15px/1.65 -apple-system,'Segoe UI',Roboto,sans-serif}
main{max-width:820px;margin:0 auto;padding:48px 20px 80px}
header{border-bottom:1px solid #2e2b27;padding-bottom:24px;margin-bottom:8px}
.logo{display:flex;align-items:center;gap:14px}
.mark{width:44px;height:44px;border-radius:12px;background:#d97757;color:#1f1e1d;font-weight:800;font-size:26px;display:flex;align-items:center;justify-content:center}
h1{font-size:20px;margin:0;color:#f4f1ea}
.sub{color:#8f887c;font-size:13px;margin-top:4px}
.msg{margin:28px 0}
.who{font-size:11px;letter-spacing:.12em;text-transform:uppercase;color:#8f887c;margin-bottom:6px}
.msg.user .who{color:#d97757}
.bubble{white-space:pre-wrap;word-wrap:break-word}
.msg.user .bubble{background:#211f1c;border:1px solid #2e2b27;border-radius:12px;padding:12px 16px}
footer{margin-top:56px;border-top:1px solid #2e2b27;padding-top:18px;color:#6b665c;font-size:12px}
a{color:#d97757}
</style></head><body><main>
<header><div class="logo"><div class="mark">E</div><div>
<h1>`)
	b.WriteString(html.EscapeString(in.Title))
	b.WriteString(`</h1><div class="sub">transcript exported from EVOLV — the self-evolving AI workspace</div>
</div></div></header>
`)
	for _, m := range in.Messages {
		role := "EVOLV"
		if m.Role == "user" {
			role = "YOU"
		}
		b.WriteString(`<div class="msg `)
		if m.Role == "user" {
			b.WriteString("user")
		} else {
			b.WriteString("evolv")
		}
		b.WriteString(`"><div class="who">`)
		b.WriteString(role)
		b.WriteString("</div><div class=\"bubble\">")
		b.WriteString(html.EscapeString(m.Text))
		b.WriteString("</div></div>\n")
	}
	b.WriteString(`<footer>` + fmt.Sprintf("%d messages", len(in.Messages)) + ` · generated by <a href="https://github.com/dxn111/EVOLV">EVOLV</a> · <span id="ts"></span>
<script>document.getElementById('ts').textContent = new Date().toLocaleString();</script>
</footer></main></body></html>`)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="evolv-transcript.html"`)
	w.Write([]byte(b.String()))
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	c := s.ws.Config
	p, model := providers.Build(c.Provider, c.Model, c.APIKeys[c.Provider], c.BaseURLs)
	models := []string{}
	if p != nil {
		ctx, cancel := contextWithTimeout(r.Context())
		defer cancel()
		if list, err := p.Models(ctx); err == nil {
			models = list
		}
	}
	if c.Provider == "kilo" || c.Provider == "" {
		models = append(models, providers.KiloFreeModels...)
		models = append(models, providers.KiloPaidModels...)
		models = dedupe(models)
	}
	json.NewEncoder(w).Encode(map[string]any{"provider": p.Name(), "model": model, "models": models})
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// handlePlan serves the agent's current task plan (or null when none).
func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(s.ws.ReadPlan())
}

func (s *Server) handleEvolution(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]any{
		"history":    s.ws.ReadEvolution(500), // client virtualizes: renders 200, pages the rest
		"count":      s.ag.EvolutionCount(),
		"components": s.ag.ListComponents(),
	})
}

// handleReplay serves the ENTIRE mutation ledger — every edit the organism
// ever committed, from birth to now. The replay timeline renders it.
func (s *Server) handleReplay(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]any{
		"count":   s.ag.EvolutionCount(),
		"history": s.ws.ReadEvolution(0), // 0 = everything
	})
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	root := s.ws.Dir(workspace.WorkspaceDir)
	switch r.Method {
	case http.MethodPost:
		// the human edits the genome too — writes land live like the agent's
		var req struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Path) == "" {
			http.Error(w, `{"error":"path required"}`, 400)
			return
		}
		p := filepath.Join(root, filepath.Clean(req.Path))
		if !strings.HasPrefix(p, root) {
			http.Error(w, `{"error":"escape"}`, 400)
			return
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			http.Error(w, `{"error":"mkdir"}`, 500)
			return
		}
		if err := os.WriteFile(p, []byte(req.Content), 0o644); err != nil {
			http.Error(w, `{"error":"write"}`, 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		return
	case http.MethodGet:
		q := r.URL.Query().Get("path")
		p := root
		if q != "" {
			clean := filepath.Clean(filepath.Join(root, q))
			if !strings.HasPrefix(clean, root) {
				http.Error(w, "escape", 400)
				return
			}
			p = clean
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(raw)
	case http.MethodDelete:
		// the human prunes the genome — same rules as write, lands live
		q := r.URL.Query().Get("path")
		if strings.TrimSpace(q) == "" {
			http.Error(w, `{"error":"path required"}`, 400)
			return
		}
		p := filepath.Join(root, filepath.Clean(q))
		if !strings.HasPrefix(p, root) || p == root {
			http.Error(w, `{"error":"escape"}`, 400)
			return
		}
		if err := os.RemoveAll(p); err != nil {
			http.Error(w, `{"error":"delete"}`, 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	default:
		http.Error(w, "method", 405)
	}
}

// handleGenome lists every file in the living genome (workspace tree).
func (s *Server) handleGenome(w http.ResponseWriter, r *http.Request) {
	root := s.ws.Dir(workspace.WorkspaceDir)
	var files []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err == nil {
			files = append(files, rel)
		}
		return nil
	})
	sort.Strings(files)
	json.NewEncoder(w).Encode(map[string]any{"files": files})
}

// handleMind is a snapshot of everything the organism currently knows and
// carries: skills, prompts, grown tools, live plugins, mounted components.
func (s *Server) handleMind(w http.ResponseWriter, r *http.Request) {
	dirNames := func(rel string) []string {
		ents, err := os.ReadDir(s.ws.Dir(rel))
		if err != nil {
			return nil
		}
		var out []string
		for _, e := range ents {
			if !e.IsDir() {
				out = append(out, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
			}
		}
		sort.Strings(out)
		return out
	}
	json.NewEncoder(w).Encode(map[string]any{
		"skills":     dirNames(workspace.SkillsDir),
		"prompts":    dirNames(workspace.PromptsDir),
		"tools":      dirNames(workspace.ToolsDir),
		"components": s.ag.ListComponents(),
		"plugins":    s.ag.PluginNames(),
		"evolutions": s.ag.EvolutionCount(),
		"snapshots":  len(s.ws.ListSnapshots()),
	})
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	s.ag.Reset()
	w.Write([]byte(`{"ok":true}`))
}

// handleSnapshots lists the genome's frozen states (time travel).
func (s *Server) handleSnapshots(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]any{
		"snapshots": s.ws.ListSnapshots(),
		"current":   s.ag.LastSnapshot(),
	})
}

// handleSnapshotRestore rewinds the organism to a snapshot id.
func (s *Server) handleSnapshotRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, `{"error":"id required"}`, 400)
		return
	}
	if err := s.ws.Restore(req.ID); err != nil {
		http.Error(w, `{"error":"`+strings.ReplaceAll(err.Error(), `"`, "")+`"}`, 400)
		return
	}
	s.ag.ReloadGrownCode()
	s.bus.Publish("shell:reload", map[string]string{"reason": "genome rolled back to " + req.ID})
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// handleSnapshotDiff shows what a mutation actually changed: line-level diff
// between two genome snapshots. ?from=<id>&to=<id> — omit `to` for current.
func (s *Server) handleSnapshotDiff(w http.ResponseWriter, r *http.Request) {
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	if from == "" || to == "" {
		http.Error(w, `{"error":"from and to required"}`, 400)
		return
	}
	diffs, err := s.ws.DiffSnapshots(from, to)
	if err != nil {
		http.Error(w, `{"error":"`+strings.ReplaceAll(err.Error(), `"`, "")+`"}`, 400)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"diffs": diffs})
}

// handleDream puts the organism to sleep on demand (one autonomous evolution turn).
func (s *Server) handleDream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	if s.ag.Dreaming() {
		json.NewEncoder(w).Encode(map[string]bool{"ok": false, "dreaming": true})
		return
	}
	go func() {
		s.ag.DreamNow()
	}()
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(map[string]any{
		"status": "alive", "version": workspace.Version,
		"provider": s.provName(), "model": s.ag.Model,
		"evolutions": s.ag.EvolutionCount(),
	})
}

// WatchReleases polls GitHub for newer releases and pushes a live banner to
// every connected browser when one appears. Checks at boot and every 6 hours.
func (s *Server) WatchReleases() {
	go func() {
		check := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			info, err := update.Latest(ctx, workspace.Version)
			if err != nil || info == nil {
				return
			}
			s.mu.Lock()
			first := s.update == nil
			s.update = info
			s.mu.Unlock()
			if first {
				raw, _ := json.Marshal(info)
				s.bus.Publish("update:available", raw)
			}
		}
		check()
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		for range t.C {
			check()
		}
	}()
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	u := s.update
	s.mu.Unlock()
	json.NewEncoder(w).Encode(map[string]any{
		"current":   workspace.Version,
		"available": u,
	})
}

// handleUpdateRun performs an in-place self-update and answers with JSON.
func (s *Server) handleUpdateRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	ver, err := update.SelfUpdate(ctx)
	if err != nil {
		http.Error(w, `{"error":"`+strings.ReplaceAll(err.Error(), `"`, "")+`"}`, 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": ver, "note": "restart evolv to run the new genome"})
}
