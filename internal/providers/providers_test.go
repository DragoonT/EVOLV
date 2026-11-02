package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// collect runs a provider stream and returns the concatenated visible text
// plus the reasoning it emitted.
func collect(t *testing.T, p Provider, model string, msgs []Message) (text, reasoning string, deltas int) {
	t.Helper()
	var out, think strings.Builder
	text, err := p.Stream(context.Background(), model, msgs, func(d Delta) {
		deltas++
		out.WriteString(d.Content)
		think.WriteString(d.Reasoning)
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if text != out.String() {
		t.Fatalf("returned text %q != streamed text %q", text, out.String())
	}
	return out.String(), think.String(), deltas
}

func TestOpenAICompatStreamsContentAndReasoning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"hmm\"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	p := &openaiCompat{name: "test", baseURL: srv.URL, client: srv.Client()}
	text, reasoning, _ := collect(t, p, "m", []Message{{Role: "user", Content: "hi"}})

	if text != "Hello world" {
		t.Fatalf("text = %q, want %q", text, "Hello world")
	}
	if reasoning != "hmm" {
		t.Fatalf("reasoning = %q, want %q", reasoning, "hmm")
	}
}

// Some gateways accept stream:true but answer with one JSON body.
func TestOpenAICompatNonStreamingFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{
					"content":           "full reply",
					"reasoning_content": "why",
				},
			}},
		})
	}))
	defer srv.Close()

	p := &openaiCompat{name: "test", baseURL: srv.URL, client: srv.Client()}
	text, reasoning, _ := collect(t, p, "m", []Message{{Role: "user", Content: "hi"}})

	if text != "full reply" {
		t.Fatalf("text = %q, want %q", text, "full reply")
	}
	if reasoning != "why" {
		t.Fatalf("reasoning = %q, want %q", reasoning, "why")
	}
}

// A nil callback must not panic even when content is present (the old
// `content != "" || reasoning != "" && onDelta != nil` precedence bug).
func TestOpenAICompatNilCallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
	}))
	defer srv.Close()

	p := &openaiCompat{name: "test", baseURL: srv.URL, client: srv.Client()}
	text, err := p.Stream(context.Background(), "m", []Message{{Role: "user", Content: "hi"}}, nil)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if text != "ok" {
		t.Fatalf("text = %q, want %q", text, "ok")
	}
}

func TestAnthropicStreamsThinking(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"pondering\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Answer\"}}\n\n")
		io.WriteString(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	p := &anthropic{apiKey: "k", client: srv.Client(), baseURL: srv.URL}
	text, reasoning, _ := collect(t, p, "claude-sonnet-4-6", []Message{{Role: "user", Content: "hi"}})

	if text != "Answer" {
		t.Fatalf("text = %q, want %q", text, "Answer")
	}
	if reasoning != "pondering" {
		t.Fatalf("reasoning = %q, want %q", reasoning, "pondering")
	}
	var req map[string]any
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if _, ok := req["thinking"]; ok {
		t.Fatalf("thinking should be off by default, body = %s", gotBody)
	}
}

func TestAnthropicThinkingOptIn(t *testing.T) {
	t.Setenv("EVOLV_THINKING", "1")
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n")
	}))
	defer srv.Close()

	p := &anthropic{apiKey: "k", client: srv.Client(), baseURL: srv.URL}
	if _, _, _ = collect(t, p, "claude-sonnet-4-6", []Message{{Role: "user", Content: "hi"}}); len(gotBody) == 0 {
		t.Fatal("no request body captured")
	}
	var req map[string]any
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("request body: %v", err)
	}
	think, ok := req["thinking"].(map[string]any)
	if !ok || think["type"] != "enabled" {
		t.Fatalf("expected thinking enabled, body = %s", gotBody)
	}
}

func TestRegistryNilSafe(t *testing.T) {
	var r *Registry
	if r.Get("x") != nil {
		t.Fatal("nil registry Get must return nil")
	}
	if names := r.Names(); len(names) != 0 {
		t.Fatalf("nil registry names = %v", names)
	}
	r.Add(nil) // must not panic
}

func TestRegistryNamesStable(t *testing.T) {
	r := NewRegistry()
	r.Add(&openaiCompat{name: "zeta"})
	r.Add(&openaiCompat{name: "alpha"})
	got := r.Names()
	if len(got) != 2 || got[0] != "alpha" || got[1] != "zeta" {
		t.Fatalf("names = %v (must be sorted for stable clients)", got)
	}
}
