// Package providers abstracts model backends behind one streaming chat call.
// Default: Kilo Gateway anonymous (free, IP-based, zero signup) — with BYOK
// for OpenAI / Anthropic / OpenRouter / Groq / Ollama / any OpenAI-compatible
// endpoint.
package providers

import (
        "bufio"
        "bytes"
        "context"
        "encoding/json"
        "fmt"
        "io"
        "net/http"
        "os"
        "strings"
        "time"
)

// Message is one conversation turn.
type Message struct {
        Role    string `json:"role"` // system | user | assistant
        Content string `json:"content"`
}

// Delta is a streamed token (or reasoning) chunk.
type Delta struct {
        Content   string // visible text
        Reasoning string // thinking tokens, if any
        Err       error
        Done      bool
}

// Provider is a chat backend.
type Provider interface {
        Name() string
        Models(ctx context.Context) ([]string, error)
        Stream(ctx context.Context, model string, msgs []Message, onDelta func(Delta)) (string, error)
}

// Registry resolves provider names.
type Registry struct{ m map[string]Provider }

// NewRegistry builds a registry.
func NewRegistry() *Registry { return &Registry{m: map[string]Provider{}} }

// Add registers a provider.
func (r *Registry) Add(p Provider) { r.m[p.Name()] = p }

// Get resolves a provider by name.
func (r *Registry) Get(name string) Provider { return r.m[name] }

// Names lists registered providers.
func (r *Registry) Names() []string {
        out := []string{}
        for k := range r.m {
                out = append(out, k)
        }
        return out
}

// ---------- shared OpenAI-compatible streaming client ----------

type openaiCompat struct {
        name    string
        baseURL string
        apiKey  string
        headers map[string]string
        client  *http.Client
}

func (o *openaiCompat) Name() string { return o.name }

type oaiModelsResp struct {
        Data []struct {
                ID string `json:"id"`
        } `json:"data"`
}

func (o *openaiCompat) Models(ctx context.Context) ([]string, error) {
        req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimSuffix(o.baseURL, "/")+"/models", nil)
        if err != nil {
                return nil, err
        }
        o.decorate(req)
        resp, err := o.client.Do(req)
        if err != nil {
                return nil, err
        }
        defer resp.Body.Close()
        var mr oaiModelsResp
        if err := json.NewDecoder(resp.Body).Decode(&mr); err != nil {
                return nil, err
        }
        out := make([]string, 0, len(mr.Data))
        for _, m := range mr.Data {
                out = append(out, m.ID)
        }
        return out, nil
}

func (o *openaiCompat) decorate(req *http.Request) {
        if o.apiKey != "" {
                req.Header.Set("Authorization", "Bearer "+o.apiKey)
        }
        for k, v := range o.headers {
                req.Header.Set(k, v)
        }
}

type oaiChunk struct {
        Choices []struct {
                Delta struct {
                        Content   string `json:"content"`
                        Reasoning string `json:"reasoning"`
                } `json:"delta"`
                FinishReason *string `json:"finish_reason"`
        } `json:"choices"`
        Error *struct {
                Message string `json:"message"`
        } `json:"error"`
}

func (o *openaiCompat) Stream(ctx context.Context, model string, msgs []Message, onDelta func(Delta)) (string, error) {
        body := map[string]any{
                "model":       model,
                "messages":    msgs,
                "max_tokens":  8192,
                "temperature": 0.8,
                "stream":      true,
        }
        raw, _ := json.Marshal(body)
        req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(o.baseURL, "/")+"/chat/completions", bytes.NewReader(raw))
        if err != nil {
                return "", err
        }
        req.Header.Set("Content-Type", "application/json")
        o.decorate(req)

        resp, err := o.client.Do(req)
        if err != nil {
                return "", err
        }
        defer resp.Body.Close()

        debug := os.Getenv("EVOLV_DEBUG") != ""
        if debug {
                fmt.Fprintf(os.Stderr, "[evolv-debug] %s status=%d content-type=%s\n", o.baseURL, resp.StatusCode, resp.Header.Get("Content-Type"))
        }

        if resp.StatusCode != http.StatusOK {
                b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
                return "", fmt.Errorf("%s http %d: %s", o.name, resp.StatusCode, strings.TrimSpace(string(b)))
        }

        var full strings.Builder
        sc := bufio.NewScanner(resp.Body)
        sc.Buffer(make([]byte, 1<<20), 1<<20)
        for sc.Scan() {
                line := sc.Text()
                if !strings.HasPrefix(line, "data:") {
                        continue
                }
                data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
                if data == "" || data == "[DONE]" {
                        continue
                }
                var ch oaiChunk
                if err := json.Unmarshal([]byte(data), &ch); err != nil {
                        continue
                }
                if ch.Error != nil {
                        return full.String(), fmt.Errorf("%s: %s", o.name, ch.Error.Message)
                }
                if len(ch.Choices) > 0 {
                        d := ch.Choices[0].Delta
                        dv := Delta{}
                        if d.Content != "" {
                                full.WriteString(d.Content)
                                dv.Content = d.Content
                        }
                        if d.Reasoning != "" {
                                dv.Reasoning = d.Reasoning
                        }
                        if dv.Content != "" || dv.Reasoning != "" && onDelta != nil {
                                onDelta(dv)
                        }
                }
        }
        if onDelta != nil {
                onDelta(Delta{Done: true})
        }
        return full.String(), sc.Err()
}

// ---------- Anthropic native ----------

type anthropic struct {
        apiKey string
        client *http.Client
}

func (a *anthropic) Name() string { return "anthropic" }

func (a *anthropic) Models(ctx context.Context) ([]string, error) {
        return []string{
                "claude-opus-4-7", "claude-sonnet-4-7", "claude-sonnet-4-6",
                "claude-haiku-4-7", "claude-haiku-4-5",
        }, nil
}

type antStreamEvent struct {
        Type  string `json:"type"`
        Delta struct {
                Type string `json:"type"`
                Text string `json:"text"`
        } `json:"delta"`
        Error *struct {
                Message string `json:"message"`
        } `json:"error"`
}

func (a *anthropic) Stream(ctx context.Context, model string, msgs []Message, onDelta func(Delta)) (string, error) {
        // split system messages
        var system strings.Builder
        rest := make([]Message, 0, len(msgs))
        for _, m := range msgs {
                if m.Role == "system" {
                        system.WriteString(m.Content + "\n\n")
                } else {
                        rest = append(rest, m)
                }
        }
        body := map[string]any{
                "model":     model,
                "max_tokens": 8192,
                "stream":    true,
                "messages":  rest,
        }
        if s := strings.TrimSpace(system.String()); s != "" {
                body["system"] = s
        }
        raw, _ := json.Marshal(body)
        req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(raw))
        if err != nil {
                return "", err
        }
        req.Header.Set("Content-Type", "application/json")
        req.Header.Set("x-api-key", a.apiKey)
        req.Header.Set("anthropic-version", "2023-06-01")

        resp, err := a.client.Do(req)
        if err != nil {
                return "", err
        }
        defer resp.Body.Close()
        if resp.StatusCode != http.StatusOK {
                b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
                return "", fmt.Errorf("anthropic http %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
        }

        var full strings.Builder
        sc := bufio.NewScanner(resp.Body)
        sc.Buffer(make([]byte, 1<<20), 1<<20)
        for sc.Scan() {
                line := sc.Text()
                if !strings.HasPrefix(line, "data:") {
                        continue
                }
                var ev antStreamEvent
                if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &ev); err != nil {
                        continue
                }
                switch ev.Type {
                case "content_block_delta":
                        if ev.Delta.Text != "" {
                                full.WriteString(ev.Delta.Text)
                                if onDelta != nil {
                                        onDelta(Delta{Content: ev.Delta.Text})
                                }
                        }
                case "error":
                        if ev.Error != nil {
                                return full.String(), fmt.Errorf("anthropic: %s", ev.Error.Message)
                        }
                }
        }
        if onDelta != nil {
                onDelta(Delta{Done: true})
        }
        return full.String(), sc.Err()
}

// ---------- Mock (offline demo / self-evolution showcase) ----------

type mock struct{ turn int }

func (m *mock) Name() string { return "mock" }
func (m *mock) Models(ctx context.Context) ([]string, error) {
        return []string{"evolv-sim-1"}, nil
}

func (m *mock) Stream(ctx context.Context, model string, msgs []Message, onDelta func(Delta)) (string, error) {
        m.turn++
        script := mockScript(m.turn, msgs)
        // stream it word by word for the full live effect
        words := strings.SplitAfter(script, " ")
        for _, w := range words {
                select {
                case <-ctx.Done():
                        return "", ctx.Err()
                case <-time.After(18 * time.Millisecond):
                }
                if onDelta != nil {
                        onDelta(Delta{Content: w})
                }
        }
        if onDelta != nil {
                onDelta(Delta{Done: true})
        }
        return script, nil
}

// ---------- Factory ----------

// KiloBaseURL is Kilo Gateway's OpenAI-compatible endpoint.
const KiloBaseURL = "https://ai-gateway.kilo.ai/api/v1"

// KiloFreeModels are the curated anonymous-tier defaults (IP rate-limited,
// no key). kilo-auto/free rotates the live free pool server-side.
var KiloFreeModels = []string{
        "kilo-auto/free",
        "openrouter/free",
        "nvidia/nemotron-3.5-super-vl:free",
        "nvidia/nemotron-3.5-lightning:free",
        "nvidia/nemotron-3-ultra-550b-a55b:free",
        "nvidia/nemotron-3-nano-omni-30b-a3b-reasoning:free",
        "cohere/north-mini-code:free",
        "poolside/laguna-s-2.1:free",
        "poolside/laguna-xs-2.1:free",
        "reflection/beam-free",
        "recursive/flint-v1.0:free",
        "liquid/lfm-2.5-2.6b:free",
        "dots-studio/dots-3-note-preview:free",
        "xingchen-agi/xing4.0-29b-a4b:free",
        "inclusionai/ling-3.1-flash-1019-free",
}

// KiloPaidModels are suggestions unlocked when the user adds a Kilo key (BYOK).
var KiloPaidModels = []string{
        "kilo-auto/frontier", "kilo-auto/efficient", "kilo-auto/small",
        "anthropic/claude-opus-4.7", "anthropic/claude-sonnet-4.6",
        "openai/gpt-6-luna", "z-ai/glm-5.3", "moonshotai/kimi-k3",
        "qwen/qwen3.8-max-0902", "deepseek/deepseek-v4.1-flash-1017",
        "x-ai/grok-4.7", "mistralai/mistral-large-4-0",
}

// Build wires the provider registry from config values.
func Build(provider, model, apiKey string, base_urls map[string]string) (Provider, string) {
        client := &http.Client{Timeout: 5 * time.Minute}
        key := func(p string) string {
                if apiKey != "" {
                        return apiKey
                }
                return ""
        }
        _ = key
        switch provider {
        case "kilo", "":
                return &openaiCompat{name: "kilo", baseURL: KiloBaseURL, apiKey: apiKey, client: client}, defaultOr(model, "kilo-auto/free")
        case "openai":
                base := base_urls["openai"]
                if base == "" {
                        base = "https://api.openai.com/v1"
                }
                return &openaiCompat{name: "openai", baseURL: base, apiKey: apiKey, client: client}, defaultOr(model, "gpt-5.2")
        case "openrouter":
                return &openaiCompat{name: "openrouter", baseURL: "https://openrouter.ai/api/v1", apiKey: apiKey, client: client,
                        headers: map[string]string{"HTTP-Referer": "https://github.com/dxn111/EVOLV", "X-Title": "EVOLV"}}, defaultOr(model, "openrouter/free")
        case "groq":
                return &openaiCompat{name: "groq", baseURL: "https://api.groq.com/openai/v1", apiKey: apiKey, client: client}, defaultOr(model, "llama-3.3-70b-versatile")
        case "ollama":
                base := base_urls["ollama"]
                if base == "" {
                        base = "http://127.0.0.1:11434/v1"
                }
                return &openaiCompat{name: "ollama", baseURL: base, client: client}, defaultOr(model, "llama3.2")
        case "custom":
                base := base_urls["custom"]
                if base == "" {
                        base = "http://localhost:8000/v1"
                }
                return &openaiCompat{name: "custom", baseURL: base, apiKey: apiKey, client: client}, defaultOr(model, "default")
        case "anthropic":
                return &anthropic{apiKey: apiKey, client: client}, defaultOr(model, "claude-sonnet-4-6")
        case "mock":
                return &mock{}, "evolv-sim-1"
        default:
                return &openaiCompat{name: "kilo", baseURL: KiloBaseURL, apiKey: apiKey, client: client}, defaultOr(model, "kilo-auto/free")
        }
}

func defaultOr(v, d string) string {
        if strings.TrimSpace(v) == "" {
                return d
        }
        return v
}
