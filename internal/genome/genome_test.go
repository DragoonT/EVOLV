package genome

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordVersionsAndIdempotency(t *testing.T) {
	root := t.TempDir()
	idx, err := OpenIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	a1, changed, err := idx.Record(KindTool, "word_count", "workspace/tools/word_count.js", []byte("function word_count(){return 1}"), Provenance{Trigger: "user"})
	if err != nil || !changed {
		t.Fatalf("first record: changed=%v err=%v", changed, err)
	}
	if a1.Version != 1 || a1.Status != "active" {
		t.Fatalf("asset = %+v", a1)
	}
	// identical content must not bump the version
	a2, changed, err := idx.Record(KindTool, "word_count", "workspace/tools/word_count.js", []byte("function word_count(){return 1}"), Provenance{})
	if err != nil || changed || a2.Version != 1 {
		t.Fatalf("idempotent record bumped: changed=%v version=%d", changed, a2.Version)
	}
	// new content bumps
	a3, changed, _ := idx.Record(KindTool, "word_count", "workspace/tools/word_count.js", []byte("function word_count(){return 2}"), Provenance{Trigger: "run"})
	if !changed || a3.Version != 2 || a3.Created != a1.Created {
		t.Fatalf("second version = %+v", a3)
	}
	// survives reload
	reopened, err := OpenIndex(root)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reopened.Get(KindTool, "word_count")
	if !ok || got.Version != 2 || got.Hash != a3.Hash {
		t.Fatalf("reloaded = %+v ok=%v", got, ok)
	}
}

func TestRecordRejectsBadKind(t *testing.T) {
	idx, _ := OpenIndex(t.TempDir())
	if _, _, err := idx.Record(Kind("banana"), "x", "p", []byte("y"), Provenance{}); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

func TestVersionHistoryAndRollbackPlan(t *testing.T) {
	root := t.TempDir()
	idx, _ := OpenIndex(root)
	v1 := []byte("v1 body")
	_, _, _ = idx.Record(KindSkill, "debug", "workspace/skills/debug.md", v1, Provenance{})
	if err := idx.ArchiveVersion(KindSkill, "debug", 1, v1); err != nil {
		t.Fatal(err)
	}
	_, _, _ = idx.Record(KindSkill, "debug", "workspace/skills/debug.md", []byte("v2 body"), Provenance{})
	_ = idx.ArchiveVersion(KindSkill, "debug", 2, []byte("v2 body"))
	versions := idx.Versions(KindSkill, "debug")
	if len(versions) != 2 {
		t.Fatalf("versions = %+v", versions)
	}
	if !versions[0].Current || versions[0].Version != 2 || versions[1].Current {
		t.Fatalf("current flag wrong: %+v", versions)
	}
	plan, err := idx.PlanRollback(KindSkill, "debug", 1)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if plan.From != 2 || plan.To != 1 || plan.TargetHash != HashBytes(v1) {
		t.Fatalf("plan = %+v", plan)
	}
	if _, err := idx.PlanRollback(KindSkill, "debug", 2); err == nil {
		t.Fatal("rollback to current version accepted")
	}
	if _, err := idx.PlanRollback(KindSkill, "debug", 9); err == nil {
		t.Fatal("rollback to unarchived version accepted")
	}
}

func TestArchiveVersionIsImmutable(t *testing.T) {
	idx, _ := OpenIndex(t.TempDir())
	_ = idx.ArchiveVersion(KindPrompt, "p", 1, []byte("original"))
	_ = idx.ArchiveVersion(KindPrompt, "p", 1, []byte("tampered"))
	raw, err := idx.ReadVersion(KindPrompt, "p", 1)
	if err != nil || string(raw) != "original" {
		t.Fatalf("archived version changed: %q", raw)
	}
}

func TestHealthReportClassifies(t *testing.T) {
	idx, _ := OpenIndex(t.TempDir())
	_, _, _ = idx.Record(KindTool, "good", "p", []byte("a"), Provenance{})
	_, _, _ = idx.Record(KindTool, "broken", "p", []byte("b"), Provenance{})
	_, _, _ = idx.Record(KindTool, "fresh", "p", []byte("c"), Provenance{})
	idx.NoteUse(KindTool, "good", true, "")
	idx.NoteUse(KindTool, "good", true, "")
	idx.NoteUse(KindTool, "broken", false, "boom")
	idx.NoteUse(KindTool, "broken", false, "boom")
	rows := idx.HealthReport(time.Now())
	if len(rows) != 3 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].Name != "broken" || rows[0].Verdict != "broken" {
		t.Fatalf("worst first = %+v", rows[0])
	}
	if rows[0].SuccessRate != 0 {
		t.Fatalf("broken success rate = %d", rows[0].SuccessRate)
	}
	byName := map[string]HealthRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	if byName["good"].Verdict != "healthy" || byName["good"].SuccessRate != 100 {
		t.Fatalf("good = %+v", byName["good"])
	}
	if byName["fresh"].Verdict != "unproven" || byName["fresh"].SuccessRate != -1 {
		t.Fatalf("fresh = %+v", byName["fresh"])
	}
}

func TestSetStatus(t *testing.T) {
	idx, _ := OpenIndex(t.TempDir())
	_, _, _ = idx.Record(KindPlugin, "pulse", "p", []byte("x"), Provenance{})
	if _, err := idx.SetStatus(KindPlugin, "pulse", "quarantined"); err != nil {
		t.Fatal(err)
	}
	a, _ := idx.Get(KindPlugin, "pulse")
	if a.Status != "quarantined" {
		t.Fatalf("status = %s", a.Status)
	}
	if _, err := idx.SetStatus(KindPlugin, "pulse", "nonsense"); err == nil {
		t.Fatal("bogus status accepted")
	}
}

func TestTraits(t *testing.T) {
	got := Traits("Fix the broken login handler and add a regression test")
	want := map[string]bool{"bugfix": true, "tests": true, "api": true}
	if len(got) == 0 {
		t.Fatal("no traits")
	}
	for _, trait := range got {
		if !want[trait] {
			// feature may appear via "add"; accept it
			if trait != "feature" {
				t.Fatalf("unexpected trait %q in %v", trait, got)
			}
		}
	}
}

func TestInsightsFromPlaybooks(t *testing.T) {
	book := &Book{}
	for i := 0; i < 4; i++ {
		_ = book.Record(Playbook{
			Traits: []string{"bugfix", "tests"}, Skills: []string{"systematic-debugging"},
			Verify: "go test ./...", Outcome: "verified", Attempts: 0, At: "2026-11-0" + string(rune('1'+i)),
		})
	}
	_ = book.Record(Playbook{Traits: []string{"bugfix"}, Outcome: "failed", Attempts: 3, At: "2026-11-09"})
	ins := book.InsightsFor([]string{"bugfix", "tests"}, 5)
	if len(ins) == 0 {
		t.Fatal("no insights")
	}
	top := ins[0]
	if top.Success < 75 {
		t.Fatalf("top insight = %+v", top)
	}
	if top.Verify != "go test ./..." {
		t.Fatalf("verify not recovered: %+v", top)
	}
	if len(top.Skills) == 0 || top.Skills[0] != "systematic-debugging" {
		t.Fatalf("skills = %v", top.Skills)
	}
	text := RenderAdvice(ins)
	if text == "" || !contains(text, "recorded outcomes") {
		t.Fatalf("advice = %q", text)
	}
}

func TestInsightsSingleSampleIsHumble(t *testing.T) {
	book := &Book{}
	_ = book.Record(Playbook{Traits: []string{"refactor"}, Outcome: "verified", At: "2026-11-01"})
	ins := book.InsightsFor([]string{"refactor"}, 3)
	if len(ins) != 1 || !contains(ins[0].Advice, "hint, not proof") {
		t.Fatalf("insight = %+v", ins)
	}
}

func TestPlaybookBookPersistsAndCaps(t *testing.T) {
	root := t.TempDir()
	book, err := OpenBook(root)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxPlaybooks+20; i++ {
		_ = book.Record(Playbook{Traits: []string{"feature"}, Outcome: "verified", At: time.Now().UTC().Format("20060102T150405.000000000")})
	}
	reopened, err := OpenBook(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Playbooks) != maxPlaybooks {
		t.Fatalf("playbooks = %d, want cap %d", len(reopened.Playbooks), maxPlaybooks)
	}
}

func TestOpenIndexToleratesCorruptIndex(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, IndexRel)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte("{not json"), 0o644)
	idx, err := OpenIndex(root)
	if err != nil {
		t.Fatalf("corrupt index blocked open: %v", err)
	}
	if len(idx.Assets) != 0 {
		t.Fatalf("assets = %d", len(idx.Assets))
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && stringsContains(s, sub) }
