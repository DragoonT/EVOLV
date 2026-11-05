package genome

import (
	"reflect"
	"testing"
)

// suggFixture is the hand-computed fixture used across the tests. Weights
// (see playbook_suggest.go): shared trait +100 capped at 400, same organ by
// name +200, shared skill +50 capped at 100, shared tool +50 capped at 100,
// previously verified outcome +200; score = points/1000.
//
// Context: traits [bugfix ui tests], skills [go-test gofmt],
// tools [build-runner], asset kind tool named "build-runner".
//
//	pb-a: bugfix+ui (200) + same organ (200) + go-test (50)
//	      + build-runner (50) + verified (200)           = 700 → 0.7
//	pb-b: bugfix (100) + same organ (200) + gofmt (50)
//	      + build-runner (50) + failed (+0)              = 400 → 0.4
//	pb-c: no overlap                                     → omitted
//	pb-d: gofmt (50)                                     =  50 → 0.05
func suggFixture() []Playbook {
	return []Playbook{
		{ID: "pb-a", Title: "Fix panel styling with tests", Traits: []string{"bugfix", "ui"},
			Skills: []string{"go-test"}, Tools: []string{"build-runner"},
			Outcome: "verified", Attempts: 1},
		{ID: "pb-b", Title: "Retry the broken build", Traits: []string{"bugfix"},
			Skills: []string{"gofmt"}, Tools: []string{"build-runner"},
			Outcome: "failed", Attempts: 2},
		{ID: "pb-c", Title: "Write changelog", Traits: []string{"docs"}, Outcome: "verified"},
		{ID: "pb-d", Title: "Speed up queries", Traits: []string{"perf"},
			Skills: []string{"gofmt"}, Outcome: "unverified"},
	}
}

func suggContext() SuggestContext {
	return SuggestContext{
		Traits:    []string{"bugfix", "ui", "tests"},
		AssetKind: KindTool,
		AssetName: "build-runner",
		Skills:    []string{"go-test", "gofmt"},
		Tools:     []string{"build-runner"},
	}
}

func suggFindByID(t *testing.T, out []Suggestion, id string) *Suggestion {
	t.Helper()
	for i := range out {
		if out[i].Playbook.ID == id {
			return &out[i]
		}
	}
	t.Fatalf("no suggestion with id %q in %v", id, suggIDs(out))
	return nil
}

func suggIDs(out []Suggestion) []string {
	ids := make([]string, 0, len(out))
	for _, s := range out {
		ids = append(ids, s.Playbook.ID)
	}
	return ids
}

// TestSuggestExactScores pins the hand-computed fixture: exact scores,
// exact order, and zero-overlap omission.
func TestSuggestExactScores(t *testing.T) {
	out := Suggest(suggFixture(), suggContext())

	wantOrder := []string{"pb-a", "pb-b", "pb-d"}
	if got := suggIDs(out); !reflect.DeepEqual(got, wantOrder) {
		t.Fatalf("order = %v, want %v", got, wantOrder)
	}
	wantScores := map[string]float64{"pb-a": 0.7, "pb-b": 0.4, "pb-d": 0.05}
	for id, want := range wantScores {
		if got := suggFindByID(t, out, id).Score; got != want {
			t.Errorf("%s score = %v, want exactly %v", id, got, want)
		}
	}
}

// TestSuggestReasons pins the human-readable why strings and their fixed
// order for the two most instructive fixtures.
func TestSuggestReasons(t *testing.T) {
	out := Suggest(suggFixture(), suggContext())

	want := map[string][]string{
		"pb-a": {
			`matches trait "bugfix"`,
			`matches trait "ui"`,
			`used the same tool "build-runner"`,
			"shares skills: go-test",
			"shares tools: build-runner",
			"this route verified before",
		},
		"pb-b": {
			`matches trait "bugfix"`,
			`used the same tool "build-runner"`,
			"shares skills: gofmt",
			"shares tools: build-runner",
			"caution: this route failed before",
		},
		"pb-d": {
			"shares skills: gofmt",
		},
	}
	for id, reasons := range want {
		if got := suggFindByID(t, out, id).Reasons; !reflect.DeepEqual(got, reasons) {
			t.Errorf("%s reasons = %#v, want %#v", id, got, reasons)
		}
	}
}

// TestSuggestEachDimensionContributes checks that trait, same-organ,
// skill and tool matching each add their documented weight.
func TestSuggestEachDimensionContributes(t *testing.T) {
	// Outcome is deliberately unverified so no case inherits the flat
	// +200 verified bonus; each case isolates one dimension.
	p := Playbook{ID: "solo", Outcome: "unverified",
		Traits: []string{"bugfix", "ui", "tests", "api", "perf"},
		Skills: []string{"go-test", "gofmt", "review"},
		Tools:  []string{"build-runner", "pack"}}
	cases := []struct {
		name string
		ctx  SuggestContext
		want float64
	}{
		{"trait only", SuggestContext{Traits: []string{"bugfix"}}, 0.1},
		{"same organ only", SuggestContext{AssetKind: KindTool, AssetName: "build-runner"}, 0.2},
		{"skill only", SuggestContext{Skills: []string{"go-test"}}, 0.05},
		{"tool only", SuggestContext{Tools: []string{"build-runner"}}, 0.05},
		{"skill by asset name (kind skill)", SuggestContext{AssetKind: KindSkill, AssetName: "go-test"}, 0.2},
		{"asset name with unknown kind probes both lists", SuggestContext{AssetName: "go-test"}, 0.2},
		{"two traits", SuggestContext{Traits: []string{"bugfix", "ui"}}, 0.2},
		{"five traits capped", SuggestContext{Traits: []string{"bugfix", "ui", "tests", "api", "perf"}}, 0.4},
		{"six traits still capped", SuggestContext{Traits: []string{"bugfix", "ui", "tests", "api", "perf", "docs"}}, 0.4},
		{"three skills capped", SuggestContext{Skills: []string{"go-test", "gofmt", "review"}}, 0.1},
		{"three tools capped", SuggestContext{Tools: []string{"build-runner", "pack", "deploy"}}, 0.1},
	}
	for _, tc := range cases {
		out := Suggest([]Playbook{p}, tc.ctx)
		if len(out) != 1 {
			t.Errorf("%s: got %d suggestions, want 1", tc.name, len(out))
			continue
		}
		if out[0].Score != tc.want {
			t.Errorf("%s: score = %v, want exactly %v", tc.name, out[0].Score, tc.want)
		}
	}
}

// TestSuggestVerifiedOnlyAppliesWithOverlap: a verified playbook that shares
// nothing is still omitted — the outcome weight is earned only on top of an
// overlap dimension.
func TestSuggestVerifiedOnlyAppliesWithOverlap(t *testing.T) {
	p := Playbook{ID: "lucky", Traits: []string{"docs"}, Outcome: "verified"}
	if out := Suggest([]Playbook{p}, suggContext()); len(out) != 0 {
		t.Fatalf("zero-overlap verified playbook included: %v", out)
	}
	// With overlap it earns the flat +200: trait (100) + verified (200) = 0.3.
	p.Traits = []string{"bugfix"}
	out := Suggest([]Playbook{p}, suggContext())
	if len(out) != 1 || out[0].Score != 0.3 {
		t.Fatalf("score = %v (%v), want exactly 0.3", out, suggIDs(out))
	}
}

// TestSuggestMaxScoreIsOne builds a playbook that hits every dimension at
// its cap and asserts the normalised maximum.
func TestSuggestMaxScoreIsOne(t *testing.T) {
	p := Playbook{
		ID: "max", Title: "Everything", Outcome: "verified",
		Traits: []string{"bugfix", "ui", "tests", "api", "perf"}, // 400 (capped)
		Skills: []string{"go-test", "gofmt", "review"},           // 100 (capped)
		Tools:  []string{"build-runner", "pack"},                 // 100 (capped)
	}
	// A context that overlaps every trait (5, capped), both tools (capped)
	// and enough skills (2, capped), plus the same organ.
	ctx := SuggestContext{
		Traits:    []string{"bugfix", "ui", "tests", "api", "perf"},
		Skills:    []string{"go-test", "gofmt"},
		Tools:     []string{"build-runner", "pack"},
		AssetKind: KindTool,
		AssetName: "build-runner",
	}
	out := Suggest([]Playbook{p}, ctx)
	if len(out) != 1 || out[0].Score != 1.0 {
		t.Fatalf("score = %v, want exactly 1.0 (400+200+100+100+200)", out)
	}
	// 5 trait reasons + same organ + skills + tools + outcome.
	if len(out[0].Reasons) != 9 {
		t.Errorf("reasons = %#v, want 9 entries", out[0].Reasons)
	}
}

// TestSuggestTieBreakDeterministic: equal scores order lexicographically by
// playbook name; a higher score always wins regardless of name or position.
func TestSuggestTieBreakDeterministic(t *testing.T) {
	zeta := Playbook{ID: "zeta", Traits: []string{"bugfix"}}
	alpha := Playbook{ID: "alpha", Traits: []string{"bugfix"}}
	mid := Playbook{ID: "mid", Traits: []string{"bugfix", "ui"}}
	// Deliberately shuffled input order.
	out := Suggest([]Playbook{zeta, mid, alpha}, SuggestContext{Traits: []string{"bugfix", "ui"}})
	want := []string{"mid", "alpha", "zeta"}
	if got := suggIDs(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	// A playbook with no ID falls back to its title for the tie-break.
	untitled := Playbook{ID: "", Title: "aaa", Traits: []string{"bugfix"}}
	out = Suggest([]Playbook{zeta, untitled}, SuggestContext{Traits: []string{"bugfix"}})
	if len(out) != 2 || out[0].Playbook.Title != "aaa" || out[1].Playbook.ID != "zeta" {
		t.Fatalf("title tie-break order = %v, want title \"aaa\" before zeta", suggIDs(out))
	}
}

// TestSuggestEmptyInputsSafe: nil/empty playbooks and an all-empty context
// all yield an empty slice, never a panic.
func TestSuggestEmptyInputsSafe(t *testing.T) {
	ctx := suggContext()
	for name, out := range map[string][]Suggestion{
		"nil playbooks":                Suggest(nil, ctx),
		"empty playbooks":              Suggest([]Playbook{}, ctx),
		"empty context":                Suggest(suggFixture(), SuggestContext{}),
		"both empty":                   Suggest(nil, SuggestContext{}),
		"context only task whitespace": Suggest(suggFixture(), SuggestContext{Task: "   "}),
	} {
		if len(out) != 0 {
			t.Errorf("%s: got %d suggestions, want 0", name, len(out))
		}
	}
}

// TestSuggestDerivesTraitsFromTaskText: a caller may pass raw task text;
// traits come from the same Traits() vocabulary the playbooks record.
func TestSuggestDerivesTraitsFromTaskText(t *testing.T) {
	out := Suggest(suggFixture(), SuggestContext{Task: "fix broken button css"})
	if got := suggIDs(out); !reflect.DeepEqual(got, []string{"pb-a", "pb-b"}) {
		t.Fatalf("order = %v, want [pb-a pb-b]", got)
	}
	if got := suggFindByID(t, out, "pb-a").Score; got != 0.4 {
		t.Errorf("pb-a score = %v, want exactly 0.4 (bugfix+ui 200 + verified 200)", got)
	}
}

// TestSuggestOrderStableAcrossCalls: same inputs, same output order —
// every time.
func TestSuggestOrderStableAcrossCalls(t *testing.T) {
	first := Suggest(suggFixture(), suggContext())
	for i := range 10 {
		again := Suggest(suggFixture(), suggContext())
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("call %d diverged:\nfirst: %v\nagain: %v", i, first, again)
		}
	}
}

// TestSuggestScoresWithinUnitRange: score never escapes [0,1] even when the
// context overlaps everything many times over.
func TestSuggestScoresWithinUnitRange(t *testing.T) {
	many := SuggestContext{
		Traits:    []string{"bugfix", "ui", "tests", "api", "perf", "docs", "data"},
		Skills:    []string{"go-test", "gofmt", "review", "lint", "fmt"},
		Tools:     []string{"build-runner", "pack", "deploy"},
		AssetKind: KindTool,
		AssetName: "build-runner",
	}
	for _, p := range suggFixture() {
		for _, s := range Suggest([]Playbook{p}, many) {
			if s.Score < 0 || s.Score > 1 {
				t.Errorf("%s score %v outside [0,1]", s.Playbook.ID, s.Score)
			}
		}
	}
}
