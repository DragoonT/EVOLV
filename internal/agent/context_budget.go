package agent

import (
	"sort"

	"github.com/dxn111/EVOLV/internal/providers"
)

// Token and size accounting for context management.
//
// Counting characters alone overstates ASCII-heavy prompts and understates
// CJK-heavy ones: tokenizers typically pack ~4 ASCII characters into one
// token but spend close to a token per 1.5 CJK runes. The estimates here are
// deliberately approximate — they exist to decide *when* to compact and how
// to split a prompt budget across sections, not to predict billing.

const (
	// budgetTokenScale is the denominator weights are expressed in, so all
	// arithmetic below stays in integers and stays deterministic.
	budgetTokenScale = 100
	// budgetDefaultWeight is the token weight per non-CJK rune (~4 runes/token).
	budgetDefaultWeight = 25
	// budgetCJKWeight is the token weight per CJK rune (~1.5 runes/token).
	budgetCJKWeight = 67
	// budgetRoleOverhead covers the per-message envelope: the role field and
	// the chat-format framing around each turn.
	budgetRoleOverhead = 4
	// budgetImageTokens is the flat estimate for one attached image, which
	// vision models bill roughly independently of its byte size.
	budgetImageTokens = 1000
	// budgetFloorTokens is the minimum grant any section may receive when the
	// total budget can cover every section's floor.
	budgetFloorTokens = 128
)

// Budget splits a token budget across named prompt sections.
type Budget struct {
	Total int
}

// EstimateTokens estimates the token count of s with a script-aware
// heuristic: default script ~1 token per 4 runes, CJK ~1 token per 1.5
// runes. The string is scanned rune by rune and weights accumulate, so mixed
// ASCII/CJK text is charged proportionally to its actual mix instead of by
// one blanket rule. Empty input estimates to zero.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	weight := 0
	for _, r := range s {
		if budgetIsCJK(r) {
			weight += budgetCJKWeight
		} else {
			weight += budgetDefaultWeight
		}
	}
	// Round half up; never report zero tokens for non-empty text.
	tokens := (weight + budgetTokenScale/2) / budgetTokenScale
	if tokens == 0 {
		tokens = 1
	}
	return tokens
}

// budgetIsCJK reports whether r falls in a CJK block: Han ideographs
// (including extensions), kana, Hangul, fullwidth forms and CJK punctuation.
func budgetIsCJK(r rune) bool {
	switch {
	case r >= 0x3000 && r <= 0x30FF: // CJK punctuation + Hiragana + Katakana
		return true
	case r >= 0x3400 && r <= 0x4DBF: // CJK Extension A
		return true
	case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
		return true
	case r >= 0xAC00 && r <= 0xD7AF: // Hangul syllables
		return true
	case r >= 0xF900 && r <= 0xFAFF: // CJK compatibility ideographs
		return true
	case r >= 0xFF00 && r <= 0xFFEF: // fullwidth and halfwidth forms
		return true
	case r >= 0x20000 && r <= 0x2FA1F: // CJK extensions B..F
		return true
	}
	return false
}

// EstimateMessages estimates the tokens a message slice costs once it
// reaches the model: each message contributes its content text, a small
// per-message envelope overhead for the role/framing, and a flat estimate
// per attached image (vision models bill images roughly independently of
// their byte size).
func EstimateMessages(msgs []providers.Message) int {
	total := 0
	for _, msg := range msgs {
		total += EstimateTokens(msg.Content) + budgetRoleOverhead + len(msg.Images)*budgetImageTokens
	}
	return total
}

// Allocate distributes b.Total across the sections in needs, honouring
// priorities in order:
//
//  1. every present section is first guaranteed min(its need,
//     budgetFloorTokens) while the total can still cover all floors;
//  2. the budget remaining after floors fills needs in priority order —
//     sections earlier in priorities reach their full need first;
//  3. once a section can no longer be fully funded, the leftover budget is
//     spread proportionally across the still-starved sections, then topped
//     up in priority order so no rounding crumbs are stranded.
//
// Sections listed in priorities but absent from needs are ignored; needs
// entries missing from priorities are served last, in sorted-name order;
// missing or negative needs are treated as zero. The result never sums to
// more than b.Total and never exceeds any section's need. Degenerate inputs
// (nil map, Total <= 0) return an empty/zero allocation rather than
// panicking. The returned map is always non-nil when needs is non-nil.
func (b Budget) Allocate(needs map[string]int, priorities []string) map[string]int {
	alloc := map[string]int{}
	if len(needs) == 0 {
		return alloc
	}
	if b.Total <= 0 {
		for name := range needs {
			alloc[name] = 0
		}
		return alloc
	}

	ordered := budgetOrder(needs, priorities)

	// Normalise needs: negatives clamp to zero so a malformed entry can
	// never push the sum over Total.
	need := make(map[string]int, len(needs))
	for name, n := range needs {
		if n < 0 {
			n = 0
		}
		need[name] = n
	}

	// Phase 1: floors. If every floor fits, grant them all; otherwise grant
	// floors in priority order until the budget runs dry, which is exactly
	// when the lowest-priority sections are cut first.
	remaining := b.Total
	floorsFit := 0
	for _, name := range ordered {
		floorsFit += min(need[name], budgetFloorTokens)
	}
	if floorsFit <= remaining {
		for _, name := range ordered {
			grant := min(need[name], budgetFloorTokens)
			alloc[name] += grant
			remaining -= grant
		}
	} else {
		for _, name := range ordered {
			if remaining <= 0 {
				break
			}
			grant := min(min(need[name], budgetFloorTokens), remaining)
			alloc[name] += grant
			remaining -= grant
		}
		return alloc
	}

	// Phase 2: full-need fill in priority order until someone starves.
	for _, name := range ordered {
		deficit := need[name] - alloc[name]
		if deficit <= 0 || remaining <= 0 {
			continue
		}
		if remaining >= deficit {
			alloc[name] += deficit
			remaining -= deficit
			continue
		}
		// Phase 3: this section and everyone after it share the leftover
		// budget proportionally to their deficits, then priority-ordered
		// top-up absorbs rounding leftovers.
		starved := orderedAfter(ordered, name)
		budgetSpread(alloc, need, starved, remaining)
		remaining = 0
	}
	return alloc
}

// budgetOrder lists section names in serve order: those named in priorities
// first (deduplicated, first mention wins, unknown names dropped), then any
// remaining needs entries in sorted-name order so output is deterministic.
func budgetOrder(needs map[string]int, priorities []string) []string {
	ordered := make([]string, 0, len(needs))
	seen := make(map[string]bool, len(needs))
	for _, name := range priorities {
		if _, ok := needs[name]; !ok || seen[name] {
			continue
		}
		seen[name] = true
		ordered = append(ordered, name)
	}
	var rest []string
	for name := range needs {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(ordered, rest...)
}

// orderedAfter returns the tail of ordered starting at name.
func orderedAfter(ordered []string, name string) []string {
	for i, n := range ordered {
		if n == name {
			return ordered[i:]
		}
	}
	return nil
}

// budgetSpread divides amount across sections proportionally to their
// unfunded deficits, then tops up in serve order so the rounding remainder
// lands on the earlier sections. Grants never exceed the deficit of their
// section and never sum to more than amount.
func budgetSpread(alloc, need map[string]int, sections []string, amount int) {
	if amount <= 0 || len(sections) == 0 {
		return
	}
	deficit := make(map[string]int, len(sections))
	sum := 0
	for _, name := range sections {
		d := need[name] - alloc[name]
		if d > 0 {
			deficit[name] = d
			sum += d
		}
	}
	if sum <= 0 {
		return
	}
	// Enough budget covers every deficit outright: assign in serve order
	// without proportional arithmetic.
	if amount >= sum {
		for _, name := range sections {
			d := deficit[name]
			alloc[name] += d
			amount -= d
		}
		return
	}
	granted := make(map[string]int, len(sections))
	for _, name := range sections {
		share := amount * deficit[name] / sum
		granted[name] = share
		alloc[name] += share
	}
	// Priority-ordered top-up of the rounding remainder.
	left := amount
	for _, name := range sections {
		left -= granted[name]
	}
	for _, name := range sections {
		if left <= 0 {
			break
		}
		d := deficit[name] - granted[name]
		if d > left {
			d = left
		}
		if d > 0 {
			alloc[name] += d
			left -= d
		}
	}
}
