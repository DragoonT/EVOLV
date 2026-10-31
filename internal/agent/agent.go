// Package agent is EVOLV's mind. It runs the chat loop, teaches the model the
// evolv-block protocol (self-modification works on ANY model, even tiny free
// ones), parses those blocks out of the stream, executes them against the
// living filesystem, and pushes every mutation to the bus.
package agent

import (
        "context"
        "encoding/json"
        "fmt"
        "os"
        "path/filepath"
        "regexp"
        "sort"
        "strings"
        "sync"

        "github.com/dxn111/EVOLV/internal/bus"
        "github.com/dxn111/EVOLV/internal/providers"
        "github.com/dxn111/EVOLV/internal/workspace"
)

// Agent is the living mind.
type Agent struct {
        ws        *workspace.W
        bus       *bus.Bus
        prov      providers.Provider
        Model     string
        mu        sync.Mutex
        history   []providers.Message
        js        *JSRuntime
        evolution int // mutation counter
        maxLoops  int
}

// New builds an Agent.
func New(ws *workspace.W, b *bus.Bus, prov providers.Provider, model string) *Agent {
        a := &Agent{ws: ws, bus: b, prov: prov, Model: model, maxLoops: 6}
        a.js = NewJSRuntime(a)
        a.js.LoadAll()
        return a
}

// ProviderName returns the active backend name.
func (a *Agent) ProviderName() string {
        a.mu.Lock()
        defer a.mu.Unlock()
        if a.prov == nil {
                return "unknown"
        }
        return a.prov.Name()
}

// SetProvider hot-swaps the model backend mid-life.
func (a *Agent) SetProvider(p providers.Provider, model string) {
        a.mu.Lock()
        a.prov, a.Model = p, model
        a.mu.Unlock()
        a.bus.Publish("agent:provider", map[string]string{"provider": p.Name(), "model": model})
}

// Reset wipes conversation memory (the genome stays).
func (a *Agent) Reset() {
        a.mu.Lock()
        a.history = nil
        a.mu.Unlock()
        a.bus.Publish("agent:reset", map[string]bool{"ok": true})
}

// EvolutionCount returns how many mutations have happened.
func (a *Agent) EvolutionCount() int { return a.evolution }

// HistoryLen returns conversation length.
func (a *Agent) HistoryLen() int {
        a.mu.Lock()
        defer a.mu.Unlock()
        return len(a.history)
}

// ---------- the evolv-block protocol ----------

var evolvBlockRe = regexp.MustCompile("(?s)```evolv\\s*\n(.*?)```")

type action struct {
        Action string `json:"action"`
        Name   string `json:"name"`
        Title  string `json:"title"`
        Icon   string `json:"icon"`
        Desc   string `json:"description"`
        HTML   string `json:"html"`
        Code   string `json:"code"`
        Content string `json:"content"`
        Path   string `json:"path"`
        Command string `json:"command"`
        Args   map[string]any `json:"args"`
}

// SystemPrompt assembles the mind: genome constants + evolvable fragments.
func (a *Agent) SystemPrompt() string {
        var b strings.Builder
        b.WriteString(basePrompt)
        b.WriteString("\n\n## ORGANISM STATE\n")
        b.WriteString("- " + a.ws.SystemInfo() + "\n")
        b.WriteString(fmt.Sprintf("- mutations committed: %d\n", a.evolution))

        // evolvable system prompt fragments
        if frags, err := a.listDir(a.ws.Dir(workspace.PromptsDir), ".md"); err == nil {
                for _, f := range frags {
                        if raw, err := os.ReadFile(f); err == nil && len(strings.TrimSpace(string(raw))) > 0 {
                                b.WriteString("\n### prompt fragment: " + filepath.Base(f) + "\n" + string(raw) + "\n")
                        }
                }
        }

        // skills (titles + first lines; full file is readable via read_file)
        if skills, err := a.listDir(a.ws.Dir(workspace.SkillsDir), ".md"); err == nil && len(skills) > 0 {
                b.WriteString("\n## SKILLS (grown into yourself)\n")
                for _, s := range skills {
                        raw, _ := os.ReadFile(s)
                        first := strings.SplitN(strings.TrimSpace(string(raw)), "\n", 2)
                        title := strings.TrimPrefix(first[0], "# ")
                        b.WriteString("- " + title + " (skills/" + filepath.Base(s) + ")\n")
                }
        }

        // grown tools
        if tools := a.js.ListTools(); len(tools) > 0 {
                b.WriteString("\n## GROWN TOOLS (callable via run_tool)\n")
                for _, t := range tools {
                        b.WriteString("- " + t.Name + ": " + t.Desc + "\n")
                }
        }

        // mounted components
        if comps := a.ListComponents(); len(comps) > 0 {
                b.WriteString("\n## MOUNTED UI COMPONENTS\n")
                for _, c := range comps {
                        b.WriteString("- " + c + "\n")
                }
        }
        return b.String()
}

func (a *Agent) listDir(dir, ext string) ([]string, error) {
        ents, err := os.ReadDir(dir)
        if err != nil {
                return nil, err
        }
        out := []string{}
        for _, e := range ents {
                if !e.IsDir() && strings.HasSuffix(e.Name(), ext) {
                        out = append(out, filepath.Join(dir, e.Name()))
                }
        }
        sort.Strings(out)
        return out, nil
}

// Chat processes one user turn: stream, parse evolv blocks, execute, feed
// results back, repeat. Returns the final visible text.
func (a *Agent) Chat(ctx context.Context, userMsg string) string {
        a.mu.Lock()
        a.history = append(a.history, providers.Message{Role: "user", Content: userMsg})
        hist := append([]providers.Message{}, a.history...)
        a.mu.Unlock()

        final := ""
        for loop := 0; loop < a.maxLoops; loop++ {
                msgs := append([]providers.Message{{Role: "system", Content: a.SystemPrompt()}}, hist...)

                a.bus.Publish("agent:phase", map[string]string{"phase": "thinking"})
                text, err := a.prov.Stream(ctx, a.Model, msgs, func(d providers.Delta) {
                        if d.Err != nil {
                                return
                        }
                        if d.Content != "" {
                                a.bus.Publish("agent:token", map[string]string{"text": d.Content})
                        }
                        if d.Reasoning != "" {
                                a.bus.Publish("agent:reasoning", map[string]string{"text": d.Reasoning})
                        }
                })
                if err != nil {
                        errMsg := fmt.Sprintf("\n\n⚠️ **model error**: `%s`\n\nTry another model in Settings — free anonymous models rotate often.", err.Error())
                        a.bus.Publish("agent:token", map[string]string{"text": errMsg})
                        a.mu.Lock()
                        a.history = append(a.history, providers.Message{Role: "assistant", Content: errMsg})
                        a.mu.Unlock()
                        return errMsg
                }

                hist = append(hist, providers.Message{Role: "assistant", Content: text})
                final = text

                blocks := evolvBlockRe.FindAllStringSubmatch(text, -1)
                if len(blocks) == 0 {
                        break // no mutations requested; conversation turn complete
                }

                // execute all requested mutations, then feed results back
                var results []string
                for _, blk := range blocks {
                        for _, line := range splitJSONObjects(blk[1]) {
                                var act action
                                if err := json.Unmarshal([]byte(line), &act); err != nil {
                                        results = append(results, "parse_error: "+err.Error())
                                        continue
                                }
                                results = append(results, a.Execute(act))
                        }
                }
                hist = append(hist, providers.Message{
                        Role:    "user",
                        Content: "EVOLV KERNEL — tool results:\n" + strings.Join(results, "\n") + "\nContinue. Summarize what changed for the user. Only emit more evolv blocks if genuinely needed.",
                })
                a.bus.Publish("agent:phase", map[string]string{"phase": "evolving"})
        }

        a.mu.Lock()
        a.history = hist
        // keep memory bounded
        if len(a.history) > 80 {
                a.history = a.history[len(a.history)-60:]
        }
        a.mu.Unlock()
        return final
}

// splitJSONObjects extracts whitespace-separated JSON objects from a block
// (tolerant of multiple actions per block and of trailing commas/notes).
func splitJSONObjects(s string) []string {
        dec := json.NewDecoder(strings.NewReader(s))
        var out []string
        for {
                var raw json.RawMessage
                if err := dec.Decode(&raw); err != nil {
                        return out
                }
                out = append(out, string(raw))
        }
}

// ListComponents returns mounted component names.
func (a *Agent) ListComponents() []string {
        ents, err := os.ReadDir(a.ws.Dir(workspace.ComponentsDir))
        if err != nil {
                return nil
        }
        var out []string
        for _, e := range ents {
                if !e.IsDir() && strings.HasSuffix(e.Name(), ".html") {
                        out = append(out, strings.TrimSuffix(e.Name(), ".html"))
                }
        }
        sort.Strings(out)
        return out
}
