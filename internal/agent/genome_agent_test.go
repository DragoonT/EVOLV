package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dxn111/EVOLV/internal/genome"
)

func TestEvolveRegistersVersionedOrgans(t *testing.T) {
	a, _ := mockAgent(t)
	res := execRun(t, a, action{Action: "evolve_tool", Name: "word_count", Desc: "count words", Code: "function word_count(a){return String(a).split(' ').length}"})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("evolve_tool failed: %v", res)
	}
	if res["organ"] != "tool:word_count" || res["version"].(float64) != 1 {
		t.Fatalf("organ = %v v%v", res["organ"], res["version"])
	}
	// re-evolving with different code bumps the version — but only through the
	// stale-write contract, exactly like the agent must (hash from the first write).
	res2 := execRun(t, a, action{Action: "evolve_tool", Name: "word_count", Code: "function word_count(a){return 5}", ExpectedHash: res["hash"].(string)})
	if ok, _ := res2["ok"].(bool); !ok {
		t.Fatalf("re-evolve rejected: %v", res2)
	}
	if res2["version"].(float64) != 2 {
		t.Fatalf("version = %v", res2["version"])
	}
	idx, err := genome.OpenIndex(a.ws.Root)
	if err != nil {
		t.Fatal(err)
	}
	asset, ok := idx.Get(genome.KindTool, "word_count")
	if !ok || asset.Version != 2 {
		t.Fatalf("indexed asset = %+v", asset)
	}
	if versions := idx.Versions(genome.KindTool, "word_count"); len(versions) != 2 {
		t.Fatalf("archived versions = %d", len(versions))
	}
}

func TestOrganRollbackRestoresWorkingVersion(t *testing.T) {
	a, _ := mockAgent(t)
	v1 := execRun(t, a, action{Action: "evolve_tool", Name: "calc", Code: "function calc(a){return a+1}"})
	v2 := execRun(t, a, action{Action: "evolve_tool", Name: "calc", Code: "function calc(a){throw new Error('broken')}", ExpectedHash: v1["hash"].(string)})
	if ok, _ := v2["ok"].(bool); !ok {
		t.Fatalf("second evolve failed: %v", v2)
	}

	files := filepath.Join(a.ws.Root, "workspace", "tools")
	raw, _ := os.ReadFile(filepath.Join(files, "calc.js"))
	if !strings.Contains(string(raw), "broken") {
		t.Fatalf("expected broken v2 on disk: %s", raw)
	}

	res := execRun(t, a, action{Action: "organ_rollback", Kind: "tool", Name: "calc", Version: 1})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("rollback failed: %v", res)
	}
	if res["restored_version"].(float64) != 1 {
		t.Fatalf("restored = %v", res["restored_version"])
	}
	raw, _ = os.ReadFile(filepath.Join(files, "calc.js"))
	if strings.Contains(string(raw), "broken") || !strings.Contains(string(raw), "a+1") {
		t.Fatalf("rollback did not restore v1: %s", raw)
	}
	// rollback is itself recorded as a new version, so it can be undone
	idx, _ := genome.OpenIndex(a.ws.Root)
	asset, _ := idx.Get(genome.KindTool, "calc")
	if asset.Version != 3 {
		t.Fatalf("post-rollback version = %d, want 3", asset.Version)
	}
	if asset.Health.Reverts != 1 {
		t.Fatalf("reverts = %d", asset.Health.Reverts)
	}
}

func TestOrganHistoryAndList(t *testing.T) {
	a, _ := mockAgent(t)
	execRun(t, a, action{Action: "evolve_skill", Name: "debugging", Content: "# Debug\n\nUse when tests fail."})
	list := execRun(t, a, action{Action: "organ_list", Kind: "skill"})
	if list["count"].(float64) < 1 {
		t.Fatalf("list = %v", list["count"])
	}
	hist := execRun(t, a, action{Action: "organ_history", Kind: "skill", Name: "debugging"})
	if ok, _ := hist["ok"].(bool); !ok {
		t.Fatalf("history failed: %v", hist)
	}
	if len(hist["versions"].([]any)) != 1 {
		t.Fatalf("versions = %v", hist["versions"])
	}
	// unknown organ is an error, not an empty success
	bad := execRun(t, a, action{Action: "organ_history", Kind: "skill", Name: "nope"})
	if ok, _ := bad["ok"].(bool); ok {
		t.Fatal("unknown organ reported ok")
	}
}

func TestOrganDisableStopsLoading(t *testing.T) {
	a, _ := mockAgent(t)
	execRun(t, a, action{Action: "evolve_tool", Name: "temp", Code: "function temp(){return 1}"})
	res := execRun(t, a, action{Action: "organ_disable", Kind: "tool", Name: "temp", Reason: "quarantined"})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("disable failed: %v", res)
	}
	idx, _ := genome.OpenIndex(a.ws.Root)
	asset, _ := idx.Get(genome.KindTool, "temp")
	if asset.Status != "quarantined" {
		t.Fatalf("status = %s", asset.Status)
	}
}

func TestFailedToolUseDegradesOrganHealth(t *testing.T) {
	a, _ := mockAgent(t)
	execRun(t, a, action{Action: "evolve_tool", Name: "explodes", Code: "function explodes(){throw new Error('boom')}"})
	for i := 0; i < 2; i++ {
		execRun(t, a, action{Action: "run_tool", Name: "explodes", Args: map[string]any{}})
	}
	rows := a.genomeHealth()
	found := false
	for _, r := range rows {
		if r.Kind == genome.KindTool && r.Name == "explodes" {
			found = true
			if r.Uses == 0 {
				t.Fatalf("uses not recorded: %+v", r)
			}
			if r.Verdict != "broken" && r.Verdict != "flaky" {
				t.Fatalf("verdict = %s after failures (%+v)", r.Verdict, r)
			}
		}
	}
	if !found {
		t.Fatalf("organ missing from health report: %+v", rows)
	}
	sp := a.SystemPrompt()
	if !strings.Contains(sp, "ORGANS THAT ARE FAILING") {
		t.Fatalf("failing organ not surfaced in prompt")
	}
}

func TestPlaybookRecordedAndUsedAsLesson(t *testing.T) {
	a, ws := mockAgent(t)
	proj := t.TempDir()
	ws.Config.ProjectDir = proj
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	created := execRun(t, a, action{Action: "run_work", Title: "fix the broken login handler",
		Steps: []planStepArg{{Title: "fix the broken login handler and add a regression test"}}})
	run := created["run"].(string)
	fin := execRun(t, a, action{Action: "run_finish", Run: run, Task: "t1", VerifyCommand: "go build ./..."})
	if ok, _ := fin["ok"].(bool); !ok {
		t.Fatalf("finish failed: %v", fin)
	}

	book, err := a.openBook()
	if err != nil {
		t.Fatal(err)
	}
	if len(book.Playbooks) == 0 {
		t.Fatal("no playbook recorded after a verified task")
	}
	last := book.Playbooks[0]
	if last.Outcome != "verified" || last.Verify != "go build ./..." {
		t.Fatalf("playbook = %+v", last)
	}
	if len(last.Traits) == 0 {
		t.Fatalf("traits not derived: %+v", last)
	}

	// a similar task now starts with the lesson attached
	a.lessons = a.lessonsFor("fix the broken login handler again")
	if !strings.Contains(a.lessons, "WHAT WORKED BEFORE") {
		t.Fatalf("lessons missing: %q", a.lessons)
	}
	if !strings.Contains(a.SystemPrompt(), "WHAT WORKED BEFORE") {
		t.Fatal("lessons not injected into the system prompt")
	}
}

func TestBackfillRegistersPreexistingOrgans(t *testing.T) {
	a, _ := mockAgent(t)
	// simulate a workspace upgraded from a version without an index
	toolsDir := filepath.Join(a.ws.Root, "workspace", "tools")
	if err := os.MkdirAll(toolsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolsDir, "legacy.js"), []byte("function legacy(){return 1}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := a.backfillGenome(); n < 1 {
		t.Fatalf("backfill registered %d organs", n)
	}
	idx, _ := genome.OpenIndex(a.ws.Root)
	if _, ok := idx.Get(genome.KindTool, "legacy"); !ok {
		t.Fatal("legacy organ not registered")
	}
	// idempotent
	if n := a.backfillGenome(); n != 0 {
		t.Fatalf("second backfill registered %d", n)
	}
}
