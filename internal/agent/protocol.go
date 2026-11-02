package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Protocol limits for a single assistant turn. They exist so one runaway
// response cannot stall the loop or smuggle thousands of calls into the UI.
const (
	// protocolMaxActionBytes rejects a single decoded action larger than this.
	protocolMaxActionBytes = 256 << 10
	// protocolMaxActions caps how many actions one turn may execute.
	protocolMaxActions = 64
	// protocolMaxDepth caps brace nesting inside one action object.
	protocolMaxDepth = 64
)

// actionSpec is one protocol action extracted from an assistant turn.
type actionSpec struct {
	Raw  string // the JSON object, trimmed
	line int    // 1-based line offset inside the turn, for diagnostics
}

// protocolProblem is a non-fatal issue found while extracting actions. It is
// surfaced to the model so it can repair its own next attempt.
type protocolProblem struct {
	Reason string
	Raw    string
}

// extractProtocol returns ordered, de-duplicated action JSON objects plus the
// problems found on the way. It is deliberately forgiving with fences (small
// models forget them constantly) but strict about what it hands to the
// executor: only balanced, plausibly-action objects are returned.
//
// Order of precedence:
//  1. fenced ```evolv blocks (the protocol)
//  2. the last fence left unterminated (model streamed past the closer)
//  3. stray {"action":...} objects leaked into prose, but only when the turn
//     produced no fenced block at all — a model quoting an example inside a
//     fenced block is never executed twice.
func extractProtocol(text string) ([]actionSpec, []protocolProblem) {
	var actions []actionSpec
	var problems []protocolProblem
	fencedAny := false
	rest := text
	const fence = "```evolv"

	for {
		i := strings.Index(rest, fence)
		if i < 0 {
			break
		}
		line := 1 + strings.Count(text[:len(text)-len(rest)+i], "\n")
		after := rest[i+len(fence):]
		line++
		after = strings.TrimPrefix(strings.TrimPrefix(after, "\r\n"), "\n")
		j := closingFence(after)
		body := after
		if j >= 0 {
			body = after[:j]
			rest = after[j+3:]
		} else {
			// Unterminated fence: the model streamed past the closer. Treat the
			// remainder of the turn as the block body and stop scanning.
			rest = ""
		}
		fencedAny = true
		found, probs := scanActions(body, line)
		actions = append(actions, found...)
		problems = append(problems, probs...)
	}

	if !fencedAny {
		found, probs := scanActions(rest, 1)
		// salvage only action-shaped objects from prose
		for _, spec := range found {
			if strings.Contains(spec.Raw, `"action"`) {
				actions = append(actions, spec)
			}
		}
		problems = append(problems, probs...)
	}

	actions = dedupeSpecs(actions)
	if len(actions) > protocolMaxActions {
		dropped := len(actions) - protocolMaxActions
		problems = append(problems, protocolProblem{Reason: fmt.Sprintf("%d actions dropped: a single turn may run at most %d", dropped, protocolMaxActions)})
		actions = actions[:protocolMaxActions]
	}
	return actions, problems
}

// closingFence finds the fence that closes a block, ignoring fence marks that
// appear inside a JSON string (generated code often contains them).
func closingFence(s string) int {
	inStr, esc := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if esc {
			esc = false
			continue
		}
		switch {
		case c == '\\' && inStr:
			esc = true
		case c == '"':
			inStr = !inStr
		case !inStr && strings.HasPrefix(s[i:], "```"):
			return i
		}
	}
	return -1
}

// scanActions extracts balanced JSON objects at any nesting depth, skipping
// braces inside strings and escaped quotes.
func scanActions(s string, startLine int) ([]actionSpec, []protocolProblem) {
	var out []actionSpec
	var problems []protocolProblem
	depth, start, inStr, esc := 0, 0, false, false
	line := startLine
	objLine := startLine

	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' {
			line++
		}
		if esc {
			esc = false
			continue
		}
		switch {
		case c == '\\' && inStr:
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '{':
			if depth == 0 {
				start = i
				objLine = line
			}
			depth++
			if depth > protocolMaxDepth {
				problems = append(problems, protocolProblem{Reason: fmt.Sprintf("action nesting exceeded %d levels", protocolMaxDepth), Raw: snippet(s[start : i+1])})
				depth, inStr = 0, false
			}
		case c == '}':
			if depth == 0 {
				continue // stray closer outside any object
			}
			depth--
			if depth == 0 {
				raw := strings.TrimSpace(s[start : i+1])
				if !json.Valid([]byte(raw)) {
					problems = append(problems, protocolProblem{Reason: "malformed action JSON: fix the syntax and retry", Raw: snippet(raw)})
					continue
				}
				if len(raw) > protocolMaxActionBytes {
					problems = append(problems, protocolProblem{Reason: fmt.Sprintf("action rejected: %d bytes exceeds the %d byte limit", len(raw), protocolMaxActionBytes), Raw: snippet(raw)})
					continue
				}
				out = append(out, actionSpec{Raw: raw, line: objLine})
			}
		}
	}
	if depth > 0 || inStr {
		problems = append(problems, protocolProblem{Reason: "truncated action: the JSON object never closed", Raw: snippet(s[start:])})
	}
	return out, problems
}

// dedupeSpecs removes byte-identical actions while preserving order. A model
// repeating the same read three times gets one execution.
func dedupeSpecs(in []actionSpec) []actionSpec {
	seen := map[string]bool{}
	out := make([]actionSpec, 0, len(in))
	for _, spec := range in {
		key := strings.Join(strings.Fields(spec.Raw), " ")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, spec)
	}
	return out
}

// parseAction decodes one action object into the executor's action struct and
// checks that the shape is plausible. Returning a typed error keeps the loop
// honest: a malformed action is reported, never silently executed.
func parseAction(raw string) (action, error) {
	var act action
	if err := json.Unmarshal([]byte(raw), &act); err != nil {
		return act, fmt.Errorf("invalid action JSON: %w", err)
	}
	act.Action = strings.TrimSpace(act.Action)
	if act.Action == "" {
		return act, fmt.Errorf("action has no \"action\" name")
	}
	return act, nil
}

func snippet(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= 160 {
		return s
	}
	return string(r[:160]) + "…"
}

// clipProtocol shortens a raw action for inclusion in feedback.
func clipProtocol(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
