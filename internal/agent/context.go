package agent

import (
	"strings"

	"github.com/dxn111/EVOLV/internal/providers"
)

// Context discipline for long assignments.
//
// Trimming history by count alone is dangerous: the message that gets dropped
// is usually the original request, and with it the constraints the user cares
// about. Instead the compaction keeps anchors and summarises the middle.

const (
	// historySoftLimit is when compaction kicks in.
	historySoftLimit = 80
	// historyKeepRecent is how many trailing messages are always preserved.
	historyKeepRecent = 48
	// anchorMaxChars bounds each pinned message inside the summary.
	anchorMaxChars = 600
	// compactSummaryChars bounds the whole summarised block.
	compactSummaryChars = 4000
)

// anchorKinds classify a message worth pinning across compaction.
type anchorKind int

const (
	anchorNone anchorKind = iota
	anchorUserRequest
	anchorConstraint
	anchorDecision
)

// isAnchor decides whether a queued message must survive compaction.
func isAnchor(msg providers.Message) anchorKind {
	if msg.Role == "user" {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			return anchorNone
		}
		// kernel result batches are history, not instructions
		if strings.HasPrefix(content, "EVOLV KERNEL") {
			return anchorNone
		}
		for _, marker := range []string{"must", "do not", "don't", "never", "always", "requirement", "constraint", "important:"} {
			if strings.Contains(strings.ToLower(content), marker) {
				return anchorConstraint
			}
		}
		return anchorUserRequest
	}
	if msg.Role == "assistant" {
		lower := strings.ToLower(msg.Content)
		for _, marker := range []string{"decided", "decision:", "plan:", "## plan", "approach:"} {
			if strings.Contains(lower, marker) {
				return anchorDecision
			}
		}
	}
	return anchorNone
}

// compactHistory trims a conversation while preserving its anchors.
//
// Layout of the compacted history:
//  1. a synthetic system note listing the pinned anchors
//  2. the most recent messages, verbatim
//
// The note is regenerated on every compaction, so anchors are never lost even
// across many trims. Recent turns keep full fidelity for the model to reason
// over tool results.
func compactHistory(history []providers.Message) ([]providers.Message, bool) {
	if len(history) <= historySoftLimit {
		return history, false
	}
	recentStart := len(history) - historyKeepRecent
	if recentStart < 0 {
		recentStart = 0
	}
	// Never split a tool-result batch from the assistant turn that produced it.
	for recentStart > 0 && recentStart < len(history) &&
		history[recentStart].Role == "user" && strings.HasPrefix(history[recentStart].Content, "EVOLV KERNEL") {
		recentStart--
	}
	dropped := history[:recentStart]
	recent := history[recentStart:]

	var anchors []string
	seen := map[string]bool{}
	for _, msg := range dropped {
		kind := isAnchor(msg)
		if kind == anchorNone {
			continue
		}
		label := "earlier request"
		switch kind {
		case anchorConstraint:
			label = "constraint"
		case anchorDecision:
			label = "decision"
		}
		text := clip(msg.Content, anchorMaxChars)
		key := label + "\x00" + text
		if seen[key] {
			continue
		}
		seen[key] = true
		anchors = append(anchors, "- ("+label+") "+oneLine(text))
	}
	// Also carry forward anchors already present in a previous summary so they
	// survive repeated compactions.
	for _, msg := range dropped {
		if msg.Role == "system" && strings.HasPrefix(msg.Content, compactHeader) {
			anchors = append(anchors, extractSummaryAnchors(msg.Content)...)
		}
	}

	var out []providers.Message
	if len(anchors) > 0 {
		summary := compactHeader + strings.Join(dedupeLines(anchors), "\n")
		if len(summary) > compactSummaryChars {
			summary = summary[:compactSummaryChars] + "\n…(older anchors trimmed)"
		}
		out = append(out, providers.Message{Role: "system", Content: summary})
	}
	out = append(out, recent...)
	return out, true
}

const compactHeader = "## EARLIER CONTEXT (compacted to save space; original wording preserved)\n"

// oneLine collapses whitespace so an anchor stays readable on one bullet.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// extractSummaryAnchors pulls the bullet lines out of an existing summary.
func extractSummaryAnchors(summary string) []string {
	body := strings.TrimPrefix(summary, compactHeader)
	var out []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			out = append(out, line)
		}
	}
	return out
}

// dedupeLines keeps the first occurrence of each line, preserving order.
func dedupeLines(lines []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	return out
}

// contextStats reports the prompt pressure for observability.
type contextStats struct {
	Messages    int `json:"messages"`
	Bytes       int `json:"bytes"`
	Compactions int `json:"compactions"`
}

// stats summarises the current history size.
func (a *Agent) historyBytes() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	total := 0
	for _, m := range a.history {
		total += len(m.Content)
	}
	return total
}
