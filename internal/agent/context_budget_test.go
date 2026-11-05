package agent

import (
	"strings"
	"testing"

	"github.com/dxn111/EVOLV/internal/providers"
)

func TestEstimateTokensASCII(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},                        // empty string is exactly zero
		{"a", 1},                       // 25 → round half up gives 0, clamped to 1
		{"ab", 1},                      // 2*25=50 → (50+50)/100 = 1
		{"abcdefgh", 2},                // 8*25=200 → 250/100 = 2
		{"hello world", 3},             // 11*25=275 → 325/100 = 3
		{"line1\nline2", 3},            // newline is an ASCII rune like any other
		{strings.Repeat("ab", 50), 25}, // 100*25=2500 → 2550/100 = 25
	}
	for _, c := range cases {
		if got := EstimateTokens(c.in); got != c.want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestEstimateTokensCJK(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"你好世界你好", 4},                 // 6 CJK runes: 6*67=402 → 452/100 = 4
		{"世界", 1},                     // 2*67=134 → 184/100 = 1
		{strings.Repeat("你", 15), 10}, // 15*67=1005 → 1055/100 = 10
		{"안녕하세요", 3},                  // Hangul: 5*67=335 → 385/100 = 3
		{"废物", 1},                     // fullwidth-adjacent CJK punctuation range starts at U+3000; these are Han: 2*67=134 → 1
	}
	for _, c := range cases {
		if got := EstimateTokens(c.in); got != c.want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestEstimateTokensMixed(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		// "hello " = 6 ASCII (150) + "世界" = 2 CJK (134) → 284 → 334/100 = 3
		{"hello 世界", 3},
		// "oko " = 4 default-script runes (100) + "世界" (134) → 234 → 284/100 = 2
		{"око 世界", 2},
		// 60 ASCII + 60 CJK: 60*25 + 60*67 = 1500+4020 = 5520 → 5570/100 = 55
		{strings.Repeat("w", 60) + strings.Repeat("字", 60), 55},
	}
	for _, c := range cases {
		if got := EstimateTokens(c.in); got != c.want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestEstimateMessages(t *testing.T) {
	if got := EstimateMessages(nil); got != 0 {
		t.Errorf("EstimateMessages(nil) = %d, want 0", got)
	}
	msgs := []providers.Message{
		// "be terse" = 8 ASCII → 2 tokens + budgetRoleOverhead(4) = 6
		{Role: "system", Content: "be terse"},
		// "你好" = 2 CJK → 1 token + 4 = 5
		{Role: "user", Content: "你好"},
		// empty content → 0 + 4 + 2 images * budgetImageTokens(1000) = 2004
		{Role: "assistant", Content: "", Images: []string{"a.png", "b.png"}},
	}
	// Hand-computed total: 6 + 5 + 2004 = 2015.
	if got := EstimateMessages(msgs); got != 2015 {
		t.Errorf("EstimateMessages(fixture) = %d, want 2015", got)
	}
}

func TestAllocateConservation(t *testing.T) {
	cases := []struct {
		needs      map[string]int
		priorities []string
		total      int
	}{
		{map[string]int{"a": 1000, "b": 500, "c": 500}, []string{"a", "b", "c"}, 2000},
		{map[string]int{"a": 1000, "b": 500, "c": 500}, []string{"a", "b", "c"}, 700},
		{map[string]int{"a": 1000, "b": 500, "c": 500}, []string{"a", "b", "c"}, 13},
		{map[string]int{"docs": 3000, "code": 9000}, nil, 5000},
		{map[string]int{"solo": 250}, []string{"solo"}, 10000},
	}
	for i, c := range cases {
		b := Budget{Total: c.total}
		got := b.Allocate(c.needs, c.priorities)
		sum := 0
		for name, n := range got {
			if need, ok := c.needs[name]; ok && n > need {
				t.Errorf("case %d: %s got %d, above its need %d", i, name, n, need)
			}
			sum += n
		}
		if sum > c.total {
			t.Errorf("case %d: allocation sums to %d, above Total %d", i, sum, c.total)
		}
	}
}

func TestAllocatePriority(t *testing.T) {
	needs := map[string]int{"a": 1000, "b": 500, "c": 500}
	fwd := Budget{Total: 300}.Allocate(needs, []string{"a", "b", "c"})
	// Floors (128 each) cannot all fit in 300, so they are granted in
	// priority order and the lowest priority is cut first: 128+128+44 = 300.
	if fwd["a"] != 128 || fwd["b"] != 128 || fwd["c"] != 44 {
		t.Errorf("forward priorities under 300: got %v, want a=128 b=128 c=44", fwd)
	}
	rev := Budget{Total: 300}.Allocate(needs, []string{"c", "b", "a"})
	if rev["c"] != 128 || rev["b"] != 128 || rev["a"] != 44 {
		t.Errorf("reversed priorities under 300: got %v, want c=128 b=128 a=44", rev)
	}
	tight := Budget{Total: 100}.Allocate(needs, []string{"a", "b", "c"})
	// Extreme pressure: only the first priority gets anything.
	if tight["a"] != 100 || tight["b"] != 0 || tight["c"] != 0 {
		t.Errorf("extreme pressure: got %v, want a=100 b=0 c=0", tight)
	}
	// Roomier budget: the first priority still reaches full need before the
	// others grow past their floors.
	roomy := Budget{Total: 2000}.Allocate(needs, []string{"a", "b", "c"})
	if roomy["a"] != 1000 || roomy["b"] != 500 || roomy["c"] != 500 {
		t.Errorf("roomy budget: got %v, want everyone fully funded", roomy)
	}
}

func TestAllocateFloors(t *testing.T) {
	// Total covers every floor; with nobody reaching full need the spare 16
	// spread proportionally (5/5/5) and the 1-token rounding remainder tops
	// up the first priority: 128+5+1, 128+5, 128+5.
	got := Budget{Total: 400}.Allocate(
		map[string]int{"x": 1000, "y": 1000, "z": 1000}, []string{"x", "y", "z"})
	if got["x"] != 134 || got["y"] != 133 || got["z"] != 133 {
		t.Errorf("floors with spare 16: got %v, want x=134 y=133 z=133", got)
	}
	// Small needs are honoured exactly; surplus budget is never granted
	// above a need.
	got = Budget{Total: 10000}.Allocate(map[string]int{"a": 50, "b": 200}, []string{"a", "b"})
	if got["a"] != 50 || got["b"] != 200 {
		t.Errorf("small needs: got %v, want a=50 b=200", got)
	}
	// A zero-need section stays at zero while its neighbour is served.
	got = Budget{Total: 128}.Allocate(map[string]int{"a": 0, "b": 500}, []string{"a", "b"})
	if got["a"] != 0 || got["b"] != 128 {
		t.Errorf("zero need: got %v, want a=0 b=128", got)
	}
}

func TestAllocateDegenerate(t *testing.T) {
	// Total <= 0: every present section reports zero.
	for _, total := range []int{0, -5} {
		got := Budget{Total: total}.Allocate(map[string]int{"a": 100}, []string{"a"})
		if len(got) != 1 || got["a"] != 0 {
			t.Errorf("Total %d: got %v, want map[a:0]", total, got)
		}
	}
	// Nil/empty needs: empty result, no panic.
	if got := (Budget{Total: 100}).Allocate(nil, []string{"a"}); len(got) != 0 {
		t.Errorf("nil needs: got %v, want empty", got)
	}
	if got := (Budget{Total: 100}).Allocate(map[string]int{}, nil); len(got) != 0 {
		t.Errorf("empty needs: got %v, want empty", got)
	}
	// Negative needs clamp to zero instead of inflating the allocation.
	got := Budget{Total: 200}.Allocate(map[string]int{"a": -50, "b": 100}, []string{"a", "b"})
	if got["a"] != 0 || got["b"] != 100 {
		t.Errorf("negative need: got %v, want a=0 b=100", got)
	}
	// Unknown priority names are ignored; unlisted needs still served.
	got = Budget{Total: 1000}.Allocate(map[string]int{"a": 100}, []string{"zzz"})
	if got["a"] != 100 {
		t.Errorf("unknown priority: got %v, want a=100", got)
	}
	// No priorities at all: sections served in sorted-name order, so the
	// result is deterministic.
	got = Budget{Total: 1000}.Allocate(map[string]int{"b": 300, "a": 200}, nil)
	if got["a"] != 200 || got["b"] != 300 {
		t.Errorf("no priorities: got %v, want a=200 b=300", got)
	}
}

func TestAllocateDeterministic(t *testing.T) {
	needs := map[string]int{"zeta": 900, "alpha": 700, "mid": 250, "top": 4000}
	first := (Budget{Total: 4400}).Allocate(needs, []string{"top"})
	for i := 0; i < 200; i++ {
		next := (Budget{Total: 4400}).Allocate(needs, []string{"top"})
		for name, want := range first {
			if next[name] != want {
				t.Fatalf("run %d: %s = %d, want stable %d", i, name, next[name], want)
			}
		}
	}
	// "top" is fully funded first (its 128 floor plus its whole 3872
	// deficit); the unprioritized rest share the 16 leftover tokens and sit
	// at their 128 floors, served in sorted order alpha, mid, zeta.
	if first["top"] != 4000 {
		t.Fatalf("top priority not fully funded: %v", first)
	}
	for _, name := range []string{"alpha", "mid", "zeta"} {
		if first[name] < budgetFloorTokens {
			t.Fatalf("%s fell below its floor: %v", name, first)
		}
	}
	sum := 0
	for _, n := range first {
		sum += n
	}
	if sum > 4400 {
		t.Fatalf("allocation exceeded Total: %v (sum %d)", first, sum)
	}
}
