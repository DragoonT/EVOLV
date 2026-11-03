package agent

import (
	"strings"
	"testing"

	"github.com/dxn111/EVOLV/internal/providers"
)

func filler(n int) []providers.Message {
	out := make([]providers.Message, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, providers.Message{Role: "assistant", Content: strings.Repeat("ok ", 40)})
	}
	return out
}

func TestCompactKeepsShortHistory(t *testing.T) {
	hist := []providers.Message{{Role: "user", Content: "hi"}}
	out, changed := compactHistory(hist)
	if changed || len(out) != 1 {
		t.Fatalf("short history changed: %v", changed)
	}
}

func TestCompactPreservesOriginalRequestAndConstraints(t *testing.T) {
	hist := []providers.Message{
		{Role: "user", Content: "Refactor the parser into its own package."},
		{Role: "assistant", Content: "understood"},
		{Role: "user", Content: "Do not change the public API — that is a hard constraint."},
		{Role: "assistant", Content: "Plan: split parse.go and keep exports."},
	}
	hist = append(hist, filler(120)...)
	out, changed := compactHistory(hist)
	if !changed {
		t.Fatal("long history not compacted")
	}
	joined := ""
	for _, m := range out {
		joined += m.Content + "\n"
	}
	for _, want := range []string{"Refactor the parser", "Do not change the public API", "Plan: split parse.go"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("anchor lost: %q\n%s", want, joined)
		}
	}
	if out[0].Role != "system" || !strings.HasPrefix(out[0].Content, compactHeader) {
		t.Fatalf("summary not first: %+v", out[0])
	}
	if len(out) > historyKeepRecent+4 {
		t.Fatalf("too much retained: %d", len(out))
	}
}

func TestCompactSurvivesRepeatedRounds(t *testing.T) {
	hist := []providers.Message{
		{Role: "user", Content: "Build the export pipeline. Never drop checksums."},
	}
	hist = append(hist, filler(100)...)
	for round := 0; round < 5; round++ {
		var changed bool
		hist, changed = compactHistory(hist)
		if !changed && round > 0 {
			t.Fatalf("round %d did not compact", round)
		}
		hist = append(hist, filler(60)...)
	}
	out, _ := compactHistory(hist)
	joined := ""
	for _, m := range out {
		joined += m.Content + "\n"
	}
	if !strings.Contains(joined, "Never drop checksums") {
		t.Fatalf("anchor lost after repeated compaction:\n%s", joined)
	}
	// anchors must not be duplicated
	if strings.Count(joined, "Never drop checksums") > 1 {
		t.Fatalf("anchor duplicated:\n%s", joined)
	}
}

func TestCompactDoesNotSplitToolBatches(t *testing.T) {
	hist := []providers.Message{{Role: "user", Content: "start"}}
	hist = append(hist, filler(historySoftLimit-2)...)
	hist = append(hist,
		providers.Message{Role: "assistant", Content: "```evolv\n{\"action\":\"list_dir\"}\n```"},
		providers.Message{Role: "user", Content: "EVOLV KERNEL — tool results:\nkernel:{\"ok\":true}"},
	)
	out, changed := compactHistory(hist)
	if !changed {
		t.Fatal("expected compaction")
	}
	if out[len(out)-1].Role != "user" || !strings.HasPrefix(out[len(out)-1].Content, "EVOLV KERNEL") {
		t.Fatalf("tool batch split: %+v", out[len(out)-1])
	}
}

func TestCompactIgnoresKernelResultsAsAnchors(t *testing.T) {
	hist := []providers.Message{
		{Role: "user", Content: "EVOLV KERNEL — tool results:\n" + strings.Repeat("x", 100)},
	}
	hist = append(hist, filler(120)...)
	out, _ := compactHistory(hist)
	if out[0].Role == "system" && strings.Contains(out[0].Content, "EVOLV KERNEL") {
		t.Fatalf("kernel results were pinned as instructions:\n%s", out[0].Content)
	}
}

func TestChatCountsCompactions(t *testing.T) {
	p := &scriptedProvider{replies: []string{"done"}}
	a := newLoopTestAgent(t, p)
	a.mu.Lock()
	a.history = append(filler(historySoftLimit+10), providers.Message{Role: "user", Content: "the task: keep it small"})
	a.mu.Unlock()
	a.Chat(t.Context(), "continue")
	if a.Stats().Compactions == 0 {
		t.Fatal("compaction not counted")
	}
	joined := ""
	a.mu.Lock()
	for _, m := range a.history {
		joined += m.Content + "\n"
	}
	a.mu.Unlock()
	if !strings.Contains(joined, "keep it small") {
		t.Fatalf("anchor lost from live history")
	}
}
