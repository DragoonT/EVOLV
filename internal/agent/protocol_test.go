package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func parseAll(t *testing.T, text string) []action {
	t.Helper()
	specs, _ := extractProtocol(text)
	out := make([]action, 0, len(specs))
	for _, spec := range specs {
		act, err := parseAction(spec.Raw)
		if err != nil {
			t.Fatalf("parse %q: %v", spec.Raw, err)
		}
		out = append(out, act)
	}
	return out
}

func actionsOf(t *testing.T, text string) []string {
	t.Helper()
	var out []string
	for _, act := range parseAll(t, text) {
		out = append(out, act.Action)
	}
	return out
}

func TestProtocolFencedBlock(t *testing.T) {
	text := "Sure, doing it now.\n\n```evolv\n{\"action\":\"read_file\",\"path\":\"a.go\"}\n{\"action\":\"write_file\",\"path\":\"b.go\",\"content\":\"x\"}\n```\n\nDone."
	got := actionsOf(t, text)
	if len(got) != 2 || got[0] != "read_file" || got[1] != "write_file" {
		t.Fatalf("actions = %v", got)
	}
}

func TestProtocolUnterminatedFence(t *testing.T) {
	text := "```evolv\n{\"action\":\"read_file\",\"path\":\"a.go\"}"
	got := actionsOf(t, text)
	if len(got) != 1 || got[0] != "read_file" {
		t.Fatalf("actions = %v", got)
	}
}

func TestProtocolBracesInsideStrings(t *testing.T) {
	// Braces and escaped quotes inside strings must not confuse the scanner.
	text := "```evolv\n" + `{"action":"write_file","path":"a.js","content":"const x = {a:{b:1}}; console.log(\"}{\")"}` + "\n```"
	specs, problems := extractProtocol(text)
	if len(problems) > 0 {
		t.Fatalf("problems = %+v", problems)
	}
	if len(specs) != 1 {
		t.Fatalf("specs = %d", len(specs))
	}
	var act action
	var err error
	if act, err = parseAction(specs[0].Raw); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(act.Content, "const x = {a:{b:1}}") {
		t.Fatalf("content = %q", act.Content)
	}
}

func TestProtocolEscapedQuotes(t *testing.T) {
	text := "```evolv\n{\"action\":\"write_file\",\"path\":\"a.go\",\"content\":\"fmt.Println(\\\"hi {there}\\\")\"}\n```"
	acts := parseAll(t, text)
	if len(acts) != 1 || !strings.Contains(acts[0].Content, `"hi {there}"`) {
		t.Fatalf("acts = %+v", acts)
	}
}

func TestProtocolJsonGluedTogether(t *testing.T) {
	acts := parseAll(t, "```evolv\n{\"action\":\"read_file\",\"path\":\"a\"}{\"action\":\"list_dir\",\"path\":\".\"}\n```")
	if len(acts) != 2 {
		t.Fatalf("acts = %+v", acts)
	}
}

func TestProtocolIgnoresExampleInProse(t *testing.T) {
	text := "Here is what an action looks like: {\"action\":\"write_file\",\"path\":\"x\"} — but I will not run it.\n```evolv\n{\"action\":\"read_file\",\"path\":\"y.go\"}\n```"
	acts := parseAll(t, text)
	if len(acts) != 1 || acts[0].Action != "read_file" {
		t.Fatalf("prose example executed: %+v", acts)
	}
}

func TestProtocolSalvagesUnfencedActions(t *testing.T) {
	acts := parseAll(t, "no fence here {\"action\":\"read_file\",\"path\":\"a\"} and more text")
	if len(acts) != 1 || acts[0].Action != "read_file" {
		t.Fatalf("acts = %+v", acts)
	}
}

func TestProtocolTruncatedObjectIsReported(t *testing.T) {
	specs, problems := extractProtocol("```evolv\n{\"action\":\"write_file\",\"path\":\"a.go\",\"content\":\"unfin")
	if len(specs) != 0 {
		t.Fatalf("specs = %v", specs)
	}
	if len(problems) == 0 || !strings.Contains(problems[0].Reason, "truncated") {
		t.Fatalf("problems = %+v", problems)
	}
}

func TestProtocolDedupe(t *testing.T) {
	text := "```evolv\n{\"action\":\"read_file\",\"path\":\"a\"}\n{\"action\":\"read_file\",\"path\":\"a\"}\n```"
	if acts := parseAll(t, text); len(acts) != 1 {
		t.Fatalf("duplicate executed: %+v", acts)
	}
}

func TestProtocolRejectsEmptyAction(t *testing.T) {
	if _, err := parseAction(`{"path":"x"}`); err == nil {
		t.Fatal("action without name accepted")
	}
	if _, err := parseAction(`not json`); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func TestProtocolCapsActionCount(t *testing.T) {
	var b strings.Builder
	b.WriteString("```evolv\n")
	for i := 0; i < protocolMaxActions+5; i++ {
		b.WriteString(`{"action":"read_file","path":"f`)
		b.WriteString(strings.Repeat("x", i+1))
		b.WriteString("\"}\n")
	}
	b.WriteString("```")
	specs, problems := extractProtocol(b.String())
	if len(specs) != protocolMaxActions {
		t.Fatalf("specs = %d, want %d", len(specs), protocolMaxActions)
	}
	found := false
	for _, p := range problems {
		if strings.Contains(p.Reason, "at most") {
			found = true
		}
	}
	if !found {
		t.Fatalf("cap not reported: %+v", problems)
	}
}

func TestProtocolRejectsOversizedAction(t *testing.T) {
	huge := strings.Repeat("a", protocolMaxActionBytes+10)
	text := "```evolv\n{\"action\":\"write_file\",\"path\":\"a.go\",\"content\":\"" + huge + "\"}\n```"
	specs, problems := extractProtocol(text)
	if len(specs) != 0 {
		t.Fatalf("oversized action accepted: %d", len(specs))
	}
	if len(problems) == 0 {
		t.Fatal("oversize not reported")
	}
}

func TestProtocolRejectsDeepNesting(t *testing.T) {
	deep := strings.Repeat(`{"a":`, protocolMaxDepth+5) + "1" + strings.Repeat("}", protocolMaxDepth+5)
	text := "```evolv\n{\"action\":\"write_file\",\"args\":" + deep + "}\n```"
	_, problems := extractProtocol(text)
	if len(problems) == 0 {
		t.Fatal("deep nesting not reported")
	}
}

func TestExtractMutationsStillCompatible(t *testing.T) {
	text := "```evolv\n{\"action\":\"read_file\",\"path\":\"a\"}\n```"
	if got := extractMutations(text); len(got) != 1 {
		t.Fatalf("legacy extractor = %v", got)
	}
}

// FuzzExtractProtocol asserts the extractor never panics and never emits an
// object that cannot be decoded when it claims one was found cleanly.
func FuzzExtractProtocol(f *testing.F) {
	seeds := []string{
		"```evolv\n{\"action\":\"read_file\"}\n```",
		"{\"action\":\"x\"}{\"action\":\"y\"}",
		"```evolv\n{\"action\":",
		"text {a:{b:\"}\"}} more",
		"```evolv\n\"unterminated string",
		strings.Repeat("{", 200),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		specs, problems := extractProtocol(text)
		_ = problems
		for _, spec := range specs {
			var act action
			if err := jsonUnmarshalStrict(spec.Raw, &act); err != nil {
				t.Fatalf("extractor emitted invalid JSON %q: %v", spec.Raw, err)
			}
		}
	})
}

func jsonUnmarshalStrict(raw string, v *action) error {
	return json.Unmarshal([]byte(raw), v)
}
