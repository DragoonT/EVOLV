package genome

import (
	"fmt"
	"sort"
	"strings"
)

// Playbooks are what make EVOLV improve rather than merely accumulate.
//
// An outcome log records that tasks pass or fail. A playbook records *how the
// work was done* — which skills and tools it actually used, and what happened —
// and then derives concrete advice the agent sees next time it meets a similar
// task. Every number in an insight comes from recorded outcomes; nothing here
// is a guess.

// Playbook is one recorded way of doing a kind of work.
type Playbook struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Traits     []string `json:"traits"` // normalised task traits (see Traits)
	Skills     []string `json:"skills"` // skills consulted
	Tools      []string `json:"tools"`  // grown tools used
	Verify     string   `json:"verify,omitempty"`
	Attempts   int      `json:"attempts"`
	Outcome    string   `json:"outcome"` // verified | unverified | failed
	DurationMS int64    `json:"duration_ms,omitempty"`
	At         string   `json:"at"`
	Run        string   `json:"run,omitempty"`
	Task       string   `json:"task,omitempty"`
	Notes      string   `json:"notes,omitempty"`
}

// Book is the playbook collection.
type Book struct {
	Root      string     `json:"-"`
	Path      string     `json:"-"`
	Playbooks []Playbook `json:"playbooks"`
}

// PlaybooksRel is where the playbook file lives.
const PlaybooksRel = "workspace/genome/playbooks.json"

// maxPlaybooks bounds the file so it never becomes a liability.
const maxPlaybooks = 500

// OpenBook loads the playbook collection.
func OpenBook(workspaceRoot string) (*Book, error) {
	book := &Book{Root: workspaceRoot, Path: workspacePath(workspaceRoot, PlaybooksRel)}
	if raw, err := readFile(book.Path); err == nil {
		_ = jsonUnmarshal(raw, book)
	}
	book.Path = workspacePath(workspaceRoot, PlaybooksRel)
	return book, nil
}

// Record appends a playbook, keeping the newest maxPlaybooks entries.
func (b *Book) Record(p Playbook) error {
	if p.ID == "" {
		p.ID = strings.Join(p.Traits, "+") + "@" + p.At
	}
	b.Playbooks = append(b.Playbooks, p)
	sort.SliceStable(b.Playbooks, func(i, j int) bool { return b.Playbooks[i].At > b.Playbooks[j].At })
	if len(b.Playbooks) > maxPlaybooks {
		b.Playbooks = b.Playbooks[:maxPlaybooks]
	}
	return b.Save()
}

// Save persists the book atomically.
func (b *Book) Save() error {
	if b.Path == "" {
		return nil
	}
	return writeFileAtomic(b.Path, mustJSON(map[string]any{"playbooks": b.Playbooks}))
}

// Traits derives stable, low-cardinality traits from task text. The trait
// vocabulary is deliberately small: it is a key for retrieval, not a taxonomy.
func Traits(text string) []string {
	lower := strings.ToLower(text)
	set := map[string]bool{}
	add := func(t string) { set[t] = true }
	for _, rule := range traitRules {
		for _, kw := range rule.keywords {
			if strings.Contains(lower, kw) {
				add(rule.trait)
				break
			}
		}
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

type traitRule struct {
	trait    string
	keywords []string
}

var traitRules = []traitRule{
	{"bugfix", []string{"fix", "bug", "broken", "fails", "error", "crash", "regression", "debug"}},
	{"feature", []string{"add ", "implement", "feature", "support", "build", "create", "introduce"}},
	{"refactor", []string{"refactor", "restructure", "clean up", "simplify", "extract", "rename"}},
	{"tests", []string{"test", "spec", "coverage", "assert"}},
	{"docs", []string{"doc", "readme", "comment", "guide", "changelog"}},
	{"ui", []string{"ui", "css", "component", "panel", "button", "style", "frontend", "html"}},
	{"api", []string{"api", "endpoint", "route", "handler", "request", "response"}},
	{"data", []string{"database", "migration", "schema", "sql", "model", "query"}},
	{"perf", []string{"slow", "performance", "optimize", "latency", "cache", "speed"}},
	{"security", []string{"security", "auth", "permission", "secret", "token", "sanitize", "escape"}},
	{"tests-passing", []string{"go test", "pytest", "npm test", "cargo test"}},
}

// Insight is advice derived from recorded playbooks.
type Insight struct {
	Trait    string   `json:"trait"`
	Samples  int      `json:"samples"`
	Verified int      `json:"verified"`
	Success  int      `json:"success_rate"`
	Skills   []string `json:"skills,omitempty"`
	Tools    []string `json:"tools,omitempty"`
	Verify   string   `json:"verify,omitempty"`
	Attempts int      `json:"avg_attempts"`
	Advice   string   `json:"advice"`
}

// InsightsFor derives advice for a task's traits from its playbooks.
//
// The rule is simple and honest: an approach that has produced verified work
// before is recommended; one that has not is called out. With fewer than two
// samples the advice says so rather than overfitting a single run.
func (b *Book) InsightsFor(traits []string, limit int) []Insight {
	if len(traits) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, t := range traits {
		want[t] = true
	}
	type acc struct {
		n, verified, attempts int
		skills, tools         map[string]int
		verify                map[string]int
	}
	byTrait := map[string]*acc{}
	// Also weight exact trait-set matches higher than single-trait ones.
	for _, p := range b.Playbooks {
		overlap := 0
		for _, t := range p.Traits {
			if want[t] {
				overlap++
			}
		}
		if overlap == 0 {
			continue
		}
		key := strings.Join(intersectSorted(p.Traits, traits), "+")
		a, ok := byTrait[key]
		if !ok {
			a = &acc{skills: map[string]int{}, tools: map[string]int{}, verify: map[string]int{}}
			byTrait[key] = a
		}
		a.n++
		a.attempts += p.Attempts
		if p.Outcome == "verified" {
			a.verified++
		}
		for _, s := range p.Skills {
			a.skills[s]++
		}
		for _, t := range p.Tools {
			a.tools[t]++
		}
		if p.Verify != "" {
			a.verify[p.Verify]++
		}
	}
	out := make([]Insight, 0, len(byTrait))
	for key, a := range byTrait {
		in := Insight{Trait: key, Samples: a.n, Verified: a.verified}
		if a.n > 0 {
			in.Success = a.verified * 100 / a.n
			in.Attempts = a.attempts / a.n
		}
		in.Skills = topN(a.skills, 3)
		in.Tools = topN(a.tools, 3)
		if cmds := topN(a.verify, 1); len(cmds) == 1 {
			in.Verify = cmds[0]
		}
		switch {
		case a.n == 1:
			in.Advice = fmt.Sprintf("one previous %s task went %s; treat that as a hint, not proof", key, verifiedWord(a.verified))
		case in.Success >= 75:
			in.Advice = fmt.Sprintf("%d of %d similar tasks verified — this approach works here", a.verified, a.n)
		case in.Success >= 40:
			in.Advice = fmt.Sprintf("only %d of %d similar tasks verified — expect a repair cycle", a.verified, a.n)
		default:
			in.Advice = fmt.Sprintf("%d of %d similar tasks verified — the previous approach mostly failed; try a different route", a.verified, a.n)
		}
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Samples != out[j].Samples {
			return out[i].Samples > out[j].Samples
		}
		return out[i].Trait < out[j].Trait
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func verifiedWord(n int) string {
	if n > 0 {
		return "verified"
	}
	return "unverified"
}

func intersects(a []string, want map[string]bool) bool {
	for _, v := range a {
		if want[v] {
			return true
		}
	}
	return false
}

func intersectSorted(a, b []string) []string {
	set := map[string]bool{}
	for _, v := range b {
		set[v] = true
	}
	var out []string
	for _, v := range a {
		if set[v] {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func topN(counts map[string]int, n int) []string {
	type kv struct {
		k string
		v int
	}
	list := make([]kv, 0, len(counts))
	for k, v := range counts {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].v != list[j].v {
			return list[i].v > list[j].v
		}
		return list[i].k < list[j].k
	})
	if len(list) > n {
		list = list[:n]
	}
	out := make([]string, 0, len(list))
	for _, e := range list {
		out = append(out, e.k)
	}
	return out
}

// RenderAdvice formats insights and matched playbooks as prompt-ready text.
func RenderAdvice(insights []Insight, matches ...SearchMatch) string {
	if len(insights) == 0 && len(matches) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## WHAT WORKED BEFORE (from recorded outcomes)\n")
	for _, in := range insights {
		b.WriteString("- " + in.Trait + ": " + in.Advice)
		if len(in.Skills) > 0 {
			b.WriteString(" · skills: " + strings.Join(in.Skills, ", "))
		}
		if len(in.Tools) > 0 {
			b.WriteString(" · tools: " + strings.Join(in.Tools, ", "))
		}
		if in.Verify != "" {
			b.WriteString(" · verification that passed: " + in.Verify)
		}
		b.WriteString("\n")
	}
	if len(matches) > 0 {
		b.WriteString("\n### Relevant past tasks:\n")
		for _, m := range matches {
			p := m.Playbook
			if p.Title == "" {
				continue
			}
			b.WriteString("- \"" + p.Title + "\" [" + p.Outcome + "]")
			if p.Verify != "" {
				b.WriteString(" · verify: " + p.Verify)
			}
			if len(p.Skills) > 0 {
				b.WriteString(" · skills: " + strings.Join(p.Skills, ", "))
			}
			if len(p.Tools) > 0 {
				b.WriteString(" · tools: " + strings.Join(p.Tools, ", "))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("Use this as a hint. Re-derive what the task needs; do not copy blindly.\n")
	return b.String()
}
