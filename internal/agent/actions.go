package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"github.com/dxn111/EVOLV/internal/workspace"
)

// JSRuntime executes tools and plugins the agent grows into itself.
// goja is a pure-Go ES5.1+ interpreter: no cgo, cross-compiles everywhere.
type JSRuntime struct {
	a       *Agent
	vm      *goja.Runtime
	mu      sync.Mutex
	tools   map[string]toolDef
	plugins []string
}

type toolDef struct {
	Name string `json:"name"`
	Desc string `json:"description"`
	fn   goja.Callable
}

// NewJSRuntime creates the sandbox.
func NewJSRuntime(a *Agent) *JSRuntime {
	vm := goja.New()
	r := &JSRuntime{a: a, vm: vm, tools: map[string]toolDef{}}
	_ = vm.Set("evolv", map[string]any{
		"log": func(args ...goja.Value) {
			parts := make([]string, len(args))
			for i, v := range args {
				parts[i] = v.String()
			}
			r.a.bus.Publish("plugin:log", map[string]string{"text": strings.Join(parts, " ")})
		},
		"toast": func(msg string) {
			r.a.bus.Publish("plugin:toast", map[string]string{"text": msg})
		},
		"workspace_dir": func() string { return r.a.ws.Root },
	})
	return r
}

// LoadAll loads every tool and plugin currently in the genome.
func (r *JSRuntime) LoadAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.loadToolsLocked()
	r.loadPluginsLocked()
}

func (r *JSRuntime) loadToolsLocked() {
	ents, err := os.ReadDir(r.a.ws.Dir(workspace.ToolsDir))
	if err != nil {
		return
	}
	names := []string{}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		raw, err := os.ReadFile(filepath.Join(r.a.ws.Dir(workspace.ToolsDir), n))
		if err != nil {
			continue
		}
		r.evalToolLocked(strings.TrimSuffix(n, ".js"), string(raw))
	}
}

// evalToolLocked compiles one tool file: `function name(args){...}` exported
// either as a bare function declaration or via `export default`.
func (r *JSRuntime) evalToolLocked(name, code string) {
	src := "(function(){" + code + "; return typeof " + sanitizeIdent(name) + "==='function'? " + sanitizeIdent(name) + " : null})()"
	val, err := r.vm.RunString(src)
	if err != nil {
		r.a.bus.Publish("plugin:log", map[string]string{"text": "tool " + name + " failed: " + err.Error()})
		return
	}
	if goja.IsUndefined(val) || goja.IsNull(val) {
		return
	}
	if fn, ok := goja.AssertFunction(val); ok {
		r.tools[name] = toolDef{Name: name, fn: fn}
	}
}

var identRe = regexp.MustCompile(`[^a-zA-Z0-9_]`)

func sanitizeIdent(s string) string { return identRe.ReplaceAllString(s, "_") }

func (r *JSRuntime) loadPluginsLocked() {
	ents, err := os.ReadDir(r.a.ws.Dir(workspace.PluginsDir))
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		codePath := filepath.Join(r.a.ws.Dir(workspace.PluginsDir), e.Name(), "plugin.js")
		if raw, err := os.ReadFile(codePath); err == nil {
			if _, err := r.vm.RunString(string(raw)); err == nil {
				r.plugins = append(r.plugins, e.Name())
			}
		}
	}
}

// ListTools returns grown tools.
func (r *JSRuntime) ListTools() []toolDef {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]toolDef, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, toolDef{Name: t.Name, Desc: t.Desc})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RunTool executes a grown tool with JSON args.
func (r *JSRuntime) RunTool(name string, args map[string]any) (any, error) {
	r.mu.Lock()
	t, ok := r.tools[name]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no such tool: %s", name)
	}
	argVal := r.vm.ToValue(args)
	res, err := t.fn(goja.Undefined(), argVal)
	if err != nil {
		return nil, err
	}
	return res.Export(), nil
}

// Execute performs one action and returns a kernel result line.
func (a *Agent) Execute(act action) string {
	res := map[string]any{"action": act.Action}
	switch act.Action {
	case "evolve_component":
		name := sanitizeName(act.Name)
		if name == "" || act.HTML == "" {
			res["ok"] = false
			res["error"] = "evolve_component needs name + html"
			break
		}
		path := filepath.Join(a.ws.Dir(workspace.ComponentsDir), name+".html")
		if err := os.WriteFile(path, []byte(act.HTML), 0o644); err != nil {
			res["ok"], res["error"] = false, err.Error()
			break
		}
		a.commit("component", name, path, act.Desc)
		res["ok"] = true
		res["path"] = "workspace/ui/components/" + name + ".html"
		res["note"] = "component hot-mounted live"

	case "remove_component":
		name := sanitizeName(act.Name)
		path := filepath.Join(a.ws.Dir(workspace.ComponentsDir), name+".html")
		if err := os.Remove(path); err != nil {
			res["ok"], res["error"] = false, err.Error()
		} else {
			a.commit("remove_component", name, path, "removed")
			res["ok"] = true
		}

	case "evolve_tool":
		name := sanitizeName(act.Name)
		if name == "" || act.Code == "" {
			res["ok"] = false
			res["error"] = "evolve_tool needs name + code"
			break
		}
		path := filepath.Join(a.ws.Dir(workspace.ToolsDir), name+".js")
		if err := os.WriteFile(path, []byte(act.Code), 0o644); err != nil {
			res["ok"], res["error"] = false, err.Error()
			break
		}
		a.js.mu.Lock()
		a.js.evalToolLocked(name, act.Code)
		a.js.mu.Unlock()
		a.commit("tool", name, path, act.Desc)
		res["ok"] = true
		res["note"] = "tool compiled into the sandbox and callable via run_tool"

	case "run_tool":
		out, err := a.js.RunTool(act.Name, act.Args)
		if err != nil {
			res["ok"], res["error"] = false, err.Error()
		} else {
			raw, _ := json.Marshal(out)
			res["ok"] = true
			res["result"] = json.RawMessage(raw)
		}

	case "evolve_skill":
		name := sanitizeName(act.Name)
		if name == "" || act.Content == "" {
			res["ok"] = false
			res["error"] = "evolve_skill needs name + content"
			break
		}
		path := filepath.Join(a.ws.Dir(workspace.SkillsDir), name+".md")
		if err := os.WriteFile(path, []byte(act.Content), 0o644); err != nil {
			res["ok"], res["error"] = false, err.Error()
			break
		}
		a.commit("skill", name, path, act.Desc)
		res["ok"] = true

	case "evolve_prompt":
		name := sanitizeName(act.Name)
		if name == "" || act.Content == "" {
			res["ok"] = false
			res["error"] = "evolve_prompt needs name + content"
			break
		}
		path := filepath.Join(a.ws.Dir(workspace.PromptsDir), name+".md")
		if err := os.WriteFile(path, []byte(act.Content), 0o644); err != nil {
			res["ok"], res["error"] = false, err.Error()
			break
		}
		a.commit("prompt", name, path, act.Desc)
		res["ok"] = true
		res["note"] = "system prompt rewritten; takes effect next turn"

	case "evolve_plugin":
		name := sanitizeName(act.Name)
		if name == "" || act.Code == "" {
			res["ok"] = false
			res["error"] = "evolve_plugin needs name + code"
			break
		}
		dir := filepath.Join(a.ws.Dir(workspace.PluginsDir), name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			res["ok"], res["error"] = false, err.Error()
			break
		}
		manifest, _ := json.MarshalIndent(map[string]any{
			"name": name, "description": act.Desc, "version": "0.1.0", "author": "evolv-agent",
		}, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, "plugin.json"), manifest, 0o644); err != nil {
			res["ok"], res["error"] = false, err.Error()
			break
		}
		if err := os.WriteFile(filepath.Join(dir, "plugin.js"), []byte(act.Code), 0o644); err != nil {
			res["ok"], res["error"] = false, err.Error()
			break
		}
		a.js.mu.Lock()
		if _, err := a.js.vm.RunString(act.Code); err == nil {
			a.js.plugins = append(a.js.plugins, name)
		} else {
			res["ok"], res["error"] = false, err.Error()
			a.js.mu.Unlock()
			a.commit("plugin", name, filepath.Join(dir, "plugin.js"), act.Desc+" (loaded with error)")
			break
		}
		a.js.mu.Unlock()
		a.commit("plugin", name, filepath.Join(dir, "plugin.js"), act.Desc)
		res["ok"] = true

	case "write_file":
		p := safeJoin(a.ws.Root, act.Path)
		if p == "" {
			res["ok"] = false
			res["error"] = "path escapes workspace"
			break
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			res["ok"], res["error"] = false, err.Error()
			break
		}
		if err := os.WriteFile(p, []byte(act.Content), 0o644); err != nil {
			res["ok"], res["error"] = false, err.Error()
		} else {
			a.commit("file", act.Path, p, act.Desc)
			res["ok"] = true
			res["path"] = act.Path
		}

	case "read_file":
		p := safeJoin(a.ws.Root, act.Path)
		if p == "" {
			res["ok"] = false
			res["error"] = "path escapes workspace"
			break
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			res["ok"], res["error"] = false, err.Error()
		} else {
			res["ok"] = true
			res["content"] = string(raw)
		}

	case "list_files":
		var files []string
		root := a.ws.Dir(workspace.WorkspaceDir)
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			files = append(files, rel)
			return nil
		})
		sort.Strings(files)
		res["ok"] = true
		res["files"] = files

	case "shell":
		if strings.TrimSpace(act.Command) == "" {
			res["ok"] = false
			res["error"] = "shell needs command"
			break
		}
		ctx, cancel := contextWithTimeout(30 * time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", "-c", act.Command)
		cmd.Dir = a.ws.Root
		out, err := cmd.CombinedOutput()
		outStr := string(out)
		if len(outStr) > 8000 {
			outStr = outStr[:8000] + "\n…(truncated)"
		}
		res["ok"] = err == nil
		res["output"] = outStr
		if err != nil {
			res["error"] = err.Error()
		}

	default:
		res["ok"] = false
		res["error"] = "unknown action: " + act.Action
	}

	raw, _ := json.Marshal(res)
	return "kernel:" + string(raw)
}

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// commit records a mutation: bus event + evolution ledger.
func (a *Agent) commit(kind, name, path, desc string) {
	a.evolution++
	entry := map[string]any{
		"n": a.evolution, "kind": kind, "name": name, "path": path, "description": desc,
	}
	a.ws.AppendEvolution(entry)
	a.bus.Publish("evolution", entry)
}

func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = regexp.MustCompile(`[^a-z0-9_-]+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// safeJoin guards against path escape.
func safeJoin(root, rel string) string {
	if rel == "" {
		return ""
	}
	p := filepath.Clean(filepath.Join(root, rel))
	if !strings.HasPrefix(p, filepath.Clean(root)+string(filepath.Separator)) {
		return ""
	}
	return p
}
