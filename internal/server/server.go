// Package server wires the organism to the browser: static serving of the
// living UI, a WebSocket hub mirroring the bus, and a small JSON API
// (chat, settings, models, files).
package server

import (
        "context"
        "encoding/json"
        "fmt"
        "log"
        "net"
        "net/http"
        "os"
        "path/filepath"
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
        ws      *workspace.W
        bus     *bus.Bus
        ag      *agent.Agent
        reg     *providers.Registry
        mux     *http.ServeMux
        upgr    websocket.Upgrader
        mu      sync.Mutex
        live    int
        update  *update.Info // newer release, if any
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
        s.mux.HandleFunc("/api/files", s.handleFiles)
        s.mux.HandleFunc("/api/reset", s.handleReset)
        s.mux.HandleFunc("/api/health", s.handleHealth)
        s.mux.HandleFunc("/api/update", s.handleUpdate)
        s.mux.HandleFunc("/api/update/run", s.handleUpdateRun)
        s.WatchReleases()
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
                defer cancel()
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

// applyProvider rebuilds the active provider from config.
func (s *Server) applyProvider() {
        c := s.ws.Config
        p, model := providers.Build(c.Provider, c.Model, c.APIKeys[c.Provider], c.BaseURLs)
        s.ag.SetProvider(p, model)
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

func (s *Server) handleEvolution(w http.ResponseWriter, r *http.Request) {
        json.NewEncoder(w).Encode(map[string]any{
                "history": s.ws.ReadEvolution(200),
                "count":   s.ag.EvolutionCount(),
                "components": s.ag.ListComponents(),
        })
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
        q := r.URL.Query().Get("path")
        root := s.ws.Dir(workspace.WorkspaceDir)
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
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
        s.ag.Reset()
        w.Write([]byte(`{"ok":true}`))
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
                "current": workspace.Version,
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
