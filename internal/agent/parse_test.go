package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustActions(t *testing.T, text string) []action {
	t.Helper()
	var acts []action
	for _, line := range extractMutations(text) {
		var a action
		if err := json.Unmarshal([]byte(line), &a); err != nil {
			t.Fatalf("extractMutations produced unparseable object %q: %v", line, err)
		}
		acts = append(acts, a)
	}
	return acts
}

func TestExtractFencedBlocks(t *testing.T) {
	acts := mustActions(t, "Sure.\n```evolv\n{\"action\":\"evolve_skill\",\"name\":\"a\",\"content\":\"x\"}\n{\"action\":\"read_file\",\"path\":\"/tmp/f\"}\n```\nDone!")
	if len(acts) != 2 || acts[0].Name != "a" || acts[1].Path != "/tmp/f" {
		t.Fatalf("fenced block parse wrong: %+v", acts)
	}
}

func TestExtractUnterminatedFence(t *testing.T) {
	// model streams past the closing fence — mutations must survive
	acts := mustActions(t, "reading now\n```evolv\n{\"action\":\"read_file\",\"path\":\"workspace/ui/index.html\"}\n{\"action\":\"read_file\",\"path\":\"workspace/ui/style.css\"}\n")
	if len(acts) != 2 {
		t.Fatalf("unterminated fence dropped mutations: %+v", acts)
	}
}

func TestExtractSalvagesStrayActionJSON(t *testing.T) {
	// tiny model forgot the fence entirely and printed raw JSON as chat text
	text := "I'll read my current body first.\n" +
		"{\"action\": \"read_file\",\"path\": \"workspace/ui/index.html\"} \n" +
		"{\"action\":\"read_file\",\"path\":\"workspace/ui/style.css\"}{"
	acts := mustActions(t, text)
	if len(acts) != 2 {
		t.Fatalf("stray action JSON not salvaged: %+v (%v)", acts, extractMutations(text))
	}
	if acts[0].Path != "workspace/ui/index.html" || acts[1].Path != "workspace/ui/style.css" {
		t.Fatalf("salvaged actions wrong: %+v", acts)
	}
}

func TestExtractIgnoresNonActionJSON(t *testing.T) {
	acts := mustActions(t, `Here is some data {"foo": 1, "bar": [2,3]} in prose.`)
	if len(acts) != 0 {
		t.Fatalf("non-action JSON must not execute: %+v", acts)
	}
}

func TestScanJSONObjectsGarbage(t *testing.T) {
	// `}{`, trailing junk, braces inside strings, escaped quotes
	s := "noise {\"a\":\"x{y\\\"z\"}{\"b\":2} tail { junk"
	got := scanJSONObjects(s)
	if len(got) != 2 || !strings.Contains(got[0], `"a"`) || !strings.Contains(got[1], `"b":2`) {
		t.Fatalf("scanner not tolerant: %#v", got)
	}
}

func TestExtractFenceWordDoesNotMatchEvolvX(t *testing.T) {
	// "```evolving" must not open a block
	acts := mustActions(t, "```evolving\nnothing here\n```")
	if len(acts) != 0 {
		t.Fatalf("wrong fence matched: %+v", acts)
	}
}

func TestExtractMutationsDedupesRepeats(t *testing.T) {
	// tiny models repeat the same call three times in one breath
	text := "```evolv\n" +
		"{\"action\":\"read_file\",\"path\":\"workspace/ui/index.html\"}\n" +
		"{\"action\":\"read_file\",\"path\":\"workspace/ui/index.html\"}\n" +
		"{\"action\":\"read_file\",\"path\":\"workspace/ui/style.css\"}\n" +
		"```\n" +
		`and leaked: {"action":"read_file","path":"workspace/ui/index.html"}` + "\n"
	acts := extractMutations(text)
	if len(acts) != 2 {
		t.Fatalf("duplicates not collapsed: %d acts", len(acts))
	}
}

func TestResStatus(t *testing.T) {
	if resStatus(`kernel:{"ok":true,"path":"/x"}`) != "ok" {
		t.Fatal("ok result misread")
	}
	if resStatus(`kernel:{"ok":false,"error":"nope"}`) != "error" {
		t.Fatal("error result misread")
	}
}
