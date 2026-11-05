package genome

import (
	"fmt"
	"sort"
	"strings"
)

// Playbook suggestion ranking: given what the organism just did, surface
// which recorded playbooks are most relevant. This is the compounding half
// of the playbook story — Book.Search ranks by free-text lexical relevance,
// Suggest ranks by structured overlap with the just-finished work.

// Suggestion is one playbook ranked by relevance to what just happened.
// Score is normalised to [0,1]; Reasons are short human-readable
// justifications, always emitted in a fixed order so the same input yields
// the same text.
type Suggestion struct {
	Playbook Playbook `json:"playbook"`
	Score    float64  `json:"score"`
	Reasons  []string `json:"reasons,omitempty"`
}

// SuggestContext describes what just happened, using only vocabulary the
// genome already records:
//
//   - Traits is the normalised task-trait vocabulary produced by Traits()
//     (the genome's tag/operation kind: bugfix, feature, refactor, ui, ...).
//     Playbooks record no separate operation-kind field; this vocabulary is
//     what they do record, so it is what Suggest scores against.
//   - Task is raw task text; when Traits is empty it is distilled with
//     Traits(), so a caller may pass either.
//   - AssetKind/AssetName identify the organ just produced or touched, when
//     any. Playbooks record no asset kind of their own: only tool and skill
//     names have a counterpart (Playbook.Tools / Playbook.Skills), so the
//     same-organ match is kind-gated to those, and prompt, plugin and
//     component kinds fall back to a name probe of both lists.
//   - Skills/Tools are what was actually consulted, when known.
//
// Every field is optional; an all-empty context matches nothing.
type SuggestContext struct {
	Task      string   `json:"task,omitempty"`
	Traits    []string `json:"traits,omitempty"`
	AssetKind Kind     `json:"asset_kind,omitempty"`
	AssetName string   `json:"asset_name,omitempty"`
	Skills    []string `json:"skills,omitempty"`
	Tools     []string `json:"tools,omitempty"`
}

// Weights are integer basis points (1000 == 1.0) so every score is an exact
// sum of fixed fractions and identical totals compare equal as floats. The
// weight table:
//
//	dimension                    weight           cap
//	shared task trait            +100 per trait   400
//	  (the trait vocabulary is the genome's tag/operation kind)
//	same organ used by name      +200 flat        200
//	  (the context's asset name found in the playbook's
//	   Tools or Skills, gated by the context's asset kind)
//	shared skill                 +50 per skill    100
//	shared tool                  +50 per tool     100
//	previously verified outcome  +200 flat        200
//	  (earned only by playbooks that already overlap on
//	   at least one dimension above)
//
// A failed outcome earns nothing and is flagged with a caution reason. The
// raw total can therefore never exceed 1000; it is clamped defensively and
// reported as Score = points/1000, i.e. normalised to [0,1].
const (
	suggWeightPerTrait  = 100
	suggTraitCap        = 400
	suggWeightSameOrgan = 200
	suggWeightVerified  = 200
	suggWeightPerSkill  = 50
	suggSkillCap        = 100
	suggWeightPerTool   = 50
	suggToolCap         = 100
	suggTotal           = 1000
)

// Suggest ranks recorded playbooks against what just happened, so the agent
// sees the most relevant prior work first. The score is a transparent
// overlap score: every point awarded has a documented weight and a
// human-readable reason — nothing is a guess.
//
// Zero-overlap playbooks are omitted: one that shares no trait, no skill,
// no tool and no organ name with the context says nothing about this task.
// nil or empty playbooks — or a context that matches nothing — yield an
// empty slice. The function is pure: no filesystem, no global state, and
// the same inputs always produce the same order (higher score first, ties
// broken by lexicographic playbook name, fully equal keys keep input order).
func Suggest(playbooks []Playbook, ctx SuggestContext) []Suggestion {
	if len(playbooks) == 0 {
		return []Suggestion{}
	}
	traits := ctx.Traits
	if len(traits) == 0 && ctx.Task != "" {
		traits = Traits(ctx.Task)
	}
	wantTraits := suggSet(traits)
	wantSkills := suggSet(ctx.Skills)
	wantTools := suggSet(ctx.Tools)

	out := make([]Suggestion, 0, len(playbooks))
	for _, p := range playbooks {
		pts := 0
		var reasons []string

		shared := suggIntersect(p.Traits, wantTraits)
		if len(shared) > 0 {
			pts += suggCapped(len(shared)*suggWeightPerTrait, suggTraitCap)
			for _, t := range shared {
				reasons = append(reasons, fmt.Sprintf("matches trait %q", t))
			}
		}
		overlap := len(shared) > 0

		if ctx.AssetName != "" {
			if what, ok := suggSameOrgan(p, ctx); ok {
				pts += suggWeightSameOrgan
				overlap = true
				reasons = append(reasons, fmt.Sprintf("used the same %s %q", what, ctx.AssetName))
			}
		}

		if sharedSkills := suggIntersect(p.Skills, wantSkills); len(sharedSkills) > 0 {
			pts += suggCapped(len(sharedSkills)*suggWeightPerSkill, suggSkillCap)
			overlap = true
			reasons = append(reasons, "shares skills: "+strings.Join(sharedSkills, ", "))
		}

		if sharedTools := suggIntersect(p.Tools, wantTools); len(sharedTools) > 0 {
			pts += suggCapped(len(sharedTools)*suggWeightPerTool, suggToolCap)
			overlap = true
			reasons = append(reasons, "shares tools: "+strings.Join(sharedTools, ", "))
		}

		if !overlap {
			continue // nothing in common: this playbook says nothing about the task
		}

		switch p.Outcome {
		case "verified":
			pts += suggWeightVerified
			reasons = append(reasons, "this route verified before")
		case "failed":
			reasons = append(reasons, "caution: this route failed before")
		}

		if pts > suggTotal {
			pts = suggTotal
		}
		out = append(out, Suggestion{
			Playbook: p,
			Score:    float64(pts) / suggTotal,
			Reasons:  reasons,
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return suggName(out[i].Playbook) < suggName(out[j].Playbook)
	})
	return out
}

// suggSameOrgan reports whether the playbook used the organ named in the
// context, and what to call it in the reason text. Playbooks record no asset
// kind, so the context's kind is used to pick the right list: a tool asset
// is matched against Playbook.Tools, a skill asset against Playbook.Skills.
// For an unknown kind — or prompt/plugin/component, which playbooks do not
// record — both lists are probed and the match is reported under whichever
// list hit. This is the only kind-aware dimension; see SuggestContext.
func suggSameOrgan(p Playbook, ctx SuggestContext) (string, bool) {
	inTools := suggContains(p.Tools, ctx.AssetName)
	inSkills := suggContains(p.Skills, ctx.AssetName)
	switch ctx.AssetKind {
	case KindTool:
		return "tool", inTools
	case KindSkill:
		return "skill", inSkills
	default:
		switch {
		case inTools:
			return "tool", true
		case inSkills:
			return "skill", true
		default:
			return "", false
		}
	}
}

// suggName is a playbook's lexicographic identity for tie-breaking: the
// stable ID when recorded, else the title.
func suggName(p Playbook) string {
	if p.ID != "" {
		return p.ID
	}
	return p.Title
}

func suggSet(vs []string) map[string]bool {
	set := make(map[string]bool, len(vs))
	for _, v := range vs {
		set[v] = true
	}
	return set
}

// suggIntersect returns the playbook-side values present in want, deduped
// and sorted, so reason text and point totals are deterministic regardless
// of how the playbook happened to be recorded.
func suggIntersect(list []string, want map[string]bool) []string {
	if len(list) == 0 || len(want) == 0 {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, v := range list {
		if want[v] && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if len(out) > 1 {
		sort.Strings(out)
	}
	return out
}

func suggContains(list []string, v string) bool {
	for _, e := range list {
		if e == v {
			return true
		}
	}
	return false
}

func suggCapped(n, limit int) int {
	if n > limit {
		return limit
	}
	return n
}
