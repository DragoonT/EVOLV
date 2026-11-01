package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ---------- the workbench: a real dev environment for the user's project ----------
//
// The genome inspector edits the organism's own workspace; the workbench is
// the counterpart for the user's bound project (config.project_dir, or the
// organism home when nothing is bound). One fs API behind it: tree / read /
// write / mkdir / delete / rename — every path resolved and locked inside
// the root, every write capped, binary files refused politely.

const (
	wbMaxRead  = 512 << 10 // 512 KB per file read
	wbMaxWrite = 1 << 20   // 1 MB per file write
	wbMaxDepth = 6         // tree depth cap
	wbSkipDir  = ".git,node_modules,vendor,dist,build,.cache,__pycache__,.venv"
)

// wbRoot returns the directory the workbench operates on and a display name.
func (s *Server) wbRoot() (string, string) {
	if p := s.ws.Config.ProjectDir; p != "" {
		if abs, err := filepath.Abs(p); err == nil {
			return abs, filepath.Base(abs)
		}
	}
	return s.ws.Root, "organism home"
}

// wbResolve cleans rel and anchors it inside root, refusing escapes and
// symlink tricks. Returns the absolute path or an error.
func wbResolve(root, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" || rel == "." || rel == "/" {
		return root, nil
	}
	clean := filepath.Clean("/" + rel) // forces absolute shape, kills ..
	abs := filepath.Join(root, clean)
	// symlink hardening: what we touch must stay under the (real) root
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		if real != realRoot && !strings.HasPrefix(real, realRoot+string(filepath.Separator)) {
			return "", fmt.Errorf("path escapes the project root")
		}
	} else {
		// target doesn't exist yet — check the deepest existing ancestor
		dir := filepath.Dir(abs)
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			if real != realRoot && !strings.HasPrefix(real, realRoot+string(filepath.Separator)) {
				return "", fmt.Errorf("path escapes the project root")
			}
		}
	}
	return abs, nil
}

// wbWriteJSON writes a JSON response.
func wbWriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// wbErr writes a JSON error with status.
func wbErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": msg})
}

// wbSkip is the set of directories the tree never descends into.
var wbSkip = func() map[string]bool {
	m := map[string]bool{}
	for _, d := range strings.Split(wbSkipDir, ",") {
		m[d] = true
	}
	return m
}()

// wbEntry is one row of the tree.
type wbEntry struct {
	Name string    `json:"name"`
	Path string    `json:"path"` // slash-separated, relative to root
	Dir  bool      `json:"dir"`
	Size int64     `json:"size,omitempty"`
	Open bool      `json:"open,omitempty"` // expanded subtree included
	Kids []wbEntry `json:"kids,omitempty"`
}

// handleWbTree serves a (lazy) recursive listing: each dir row carries kids
// only when open=true, so the UI can expand level by level.
func (s *Server) handleWbTree(w http.ResponseWriter, r *http.Request) {
	root, name := s.wbRoot()
	rel := r.URL.Query().Get("path")
	depth := 0
	fmt.Sscanf(r.URL.Query().Get("depth"), "%d", &depth)
	if depth < 1 {
		depth = 2
	}
	if depth > wbMaxDepth {
		depth = wbMaxDepth
	}
	abs, err := wbResolve(root, rel)
	if err != nil {
		wbErr(w, 400, err.Error())
		return
	}
	ents, err := os.ReadDir(abs)
	if err != nil {
		wbErr(w, 400, err.Error())
		return
	}
	open := r.URL.Query().Get("open") == "1"
	out := []wbEntry{}
	for _, e := range ents {
		if e.IsDir() && wbSkip[e.Name()] {
			continue
		}
		info, err := e.Info()
		var size int64
		if err == nil {
			size = info.Size()
		}
		row := wbEntry{Name: e.Name(), Path: joinRel(rel, e.Name()), Dir: e.IsDir(), Size: size}
		if e.IsDir() && open && depth > 1 {
			row.Open = true
			row.Kids = s.wbKids(root, row.Path, depth-1)
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return out[i].Name < out[j].Name
	})
	wbWriteJSON(w, map[string]any{"ok": true, "root": name, "path": rel, "entries": out})
}

// wbKids lists one directory level for nested tree rows.
func (s *Server) wbKids(root, rel string, depth int) []wbEntry {
	abs, err := wbResolve(root, rel)
	if err != nil {
		return nil
	}
	ents, err := os.ReadDir(abs)
	if err != nil {
		return nil
	}
	out := []wbEntry{}
	for _, e := range ents {
		if e.IsDir() && wbSkip[e.Name()] {
			continue
		}
		info, err := e.Info()
		var size int64
		if err == nil {
			size = info.Size()
		}
		row := wbEntry{Name: e.Name(), Path: joinRel(rel, e.Name()), Dir: e.IsDir(), Size: size}
		if e.IsDir() && depth > 1 {
			row.Open = true
			row.Kids = s.wbKids(root, row.Path, depth-1)
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// joinRel appends one name to a relative path, slash-separated.
func joinRel(rel, name string) string {
	if rel == "" || rel == "." {
		return name
	}
	return strings.TrimPrefix(rel+"/"+name, "./")
}

// handleWbRead returns a file's text (binary files are refused, not garbled).
func (s *Server) handleWbRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		wbErr(w, 405, "GET only")
		return
	}
	root, _ := s.wbRoot()
	abs, err := wbResolve(root, r.URL.Query().Get("path"))
	if err != nil {
		wbErr(w, 400, err.Error())
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		wbErr(w, 404, "no such file")
		return
	}
	if info.IsDir() {
		wbErr(w, 400, "that's a directory")
		return
	}
	if info.Size() > wbMaxRead {
		wbErr(w, 400, fmt.Sprintf("file too big to open (%d KB > %d KB)", info.Size()>>10, wbMaxRead>>10))
		return
	}
	raw, err := os.ReadFile(abs)
	if err != nil {
		wbErr(w, 400, err.Error())
		return
	}
	if !isMostlyText(raw) {
		wbErr(w, 415, "binary file — the workbench edits text")
		return
	}
	wbWriteJSON(w, map[string]any{"ok": true, "path": r.URL.Query().Get("path"), "content": string(raw), "size": info.Size()})
}

// isMostlyText refuses NUL-bearing payloads (binaries) up to 8 KB probe.
func isMostlyText(raw []byte) bool {
	probe := raw
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	for _, b := range probe {
		if b == 0 {
			return false
		}
	}
	return true
}

// handleWbWrite writes a file (creating parents), capped at 1 MB.
func (s *Server) handleWbWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		wbErr(w, 405, "POST only")
		return
	}
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		wbErr(w, 400, "bad json")
		return
	}
	if len(req.Content) > wbMaxWrite {
		wbErr(w, 400, fmt.Sprintf("write too big (%d KB > %d KB)", len(req.Content)>>10, wbMaxWrite>>10))
		return
	}
	root, _ := s.wbRoot()
	abs, err := wbResolve(root, req.Path)
	if err != nil {
		wbErr(w, 400, err.Error())
		return
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		wbErr(w, 400, err.Error())
		return
	}
	if err := os.WriteFile(abs, []byte(req.Content), 0o644); err != nil {
		wbErr(w, 400, err.Error())
		return
	}
	s.bus.Publish("fs:change", map[string]string{"path": req.Path, "op": "write", "source": "workbench"})
	wbWriteJSON(w, map[string]any{"ok": true, "path": req.Path, "bytes": len(req.Content)})
}

// handleWbMkdir creates a directory (with parents).
func (s *Server) handleWbMkdir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		wbErr(w, 405, "POST only")
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		wbErr(w, 400, "need path")
		return
	}
	root, _ := s.wbRoot()
	abs, err := wbResolve(root, req.Path)
	if err != nil {
		wbErr(w, 400, err.Error())
		return
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		wbErr(w, 400, err.Error())
		return
	}
	s.bus.Publish("fs:change", map[string]string{"path": req.Path, "op": "mkdir", "source": "workbench"})
	wbWriteJSON(w, map[string]any{"ok": true, "path": req.Path})
}

// handleWbDelete removes a file or an empty directory — never the root itself.
func (s *Server) handleWbDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		wbErr(w, 405, "POST only")
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Path) == "" {
		wbErr(w, 400, "need path")
		return
	}
	root, _ := s.wbRoot()
	abs, err := wbResolve(root, req.Path)
	if err != nil || abs == root {
		wbErr(w, 400, "refusing to delete the root")
		return
	}
	if err := os.Remove(abs); err != nil {
		wbErr(w, 400, err.Error())
		return
	}
	s.bus.Publish("fs:change", map[string]string{"path": req.Path, "op": "delete", "source": "workbench"})
	wbWriteJSON(w, map[string]any{"ok": true, "path": req.Path})
}

// handleWbRename moves/renames a file or directory within the root.
func (s *Server) handleWbRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		wbErr(w, 405, "POST only")
		return
	}
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.From == "" || req.To == "" {
		wbErr(w, 400, "need from + to")
		return
	}
	root, _ := s.wbRoot()
	src, err := wbResolve(root, req.From)
	if err != nil {
		wbErr(w, 400, err.Error())
		return
	}
	dst, err := wbResolve(root, req.To)
	if err != nil {
		wbErr(w, 400, err.Error())
		return
	}
	if src == root || dst == root {
		wbErr(w, 400, "refusing to move the root")
		return
	}
	if _, err := os.Stat(src); err != nil {
		wbErr(w, 404, "no such file")
		return
	}
	if _, err := os.Lstat(dst); err == nil {
		wbErr(w, 409, "target already exists")
		return
	}
	if err := os.Rename(src, dst); err != nil {
		wbErr(w, 400, err.Error())
		return
	}
	s.bus.Publish("fs:change", map[string]string{"path": req.To, "op": "rename", "source": "workbench"})
	wbWriteJSON(w, map[string]any{"ok": true, "from": req.From, "to": req.To})
}

// handleWbRoot tells the UI where the workbench is pointed and how big it is.
func (s *Server) handleWbRoot(w http.ResponseWriter, r *http.Request) {
	root, name := s.wbRoot()
	files := 0
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if wbSkip[d.Name()] && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		files++
		return nil
	})
	wbWriteJSON(w, map[string]any{"ok": true, "root": root, "name": name, "files": files})
}
