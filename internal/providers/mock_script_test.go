package providers

import (
	"encoding/json"
	"strings"
	"testing"
)

func turn(t *testing.T, text string) string {
	t.Helper()
	return mockScript(0, []Message{{Role: "user", Content: text}})
}

func TestMockSelfEditBatch(t *testing.T) {
	out := turn(t, "edit your own code and show me")
	if !strings.Contains(out, "```evolv") {
		t.Fatalf("expected evolv block, got: %q", out)
	}
	for _, want := range []string{"edit_file", "ui/index.html", "ui/style.css", "reload_shell"} {
		if !strings.Contains(out, want) {
			t.Errorf("self-edit batch missing %q", want)
		}
	}
}

func TestMockPromptLibraryBatch(t *testing.T) {
	out := turn(t, "make a button that lets me pick prompt templates")
	if !strings.Contains(out, "```evolv") || !strings.Contains(out, "prompt-library") {
		t.Fatalf("expected prompt-library batch, got: %q", out)
	}
	// batch must carry two actions
	if !strings.Contains(out, "evolve_skill") {
		t.Errorf("batch should also grow a skill")
	}
}

func TestMockDreamsSelfEdit(t *testing.T) {
	// dreams must write their OWN code: distinct generative organs with
	// random parameters, plus real rewrites of the previous organ's file
	// (same name, different code). No fixed templates, ever.
	resetDreamState()
	out0 := turn(t, "You are DREAMING - autonomous evolution time.")
	if !strings.Contains(out0, "deep-work") {
		t.Fatalf("first dream should teach itself deep-work, got: %q", out0)
	}
	type organ struct{ name, html string }
	var seq []organ
	seen := map[string]bool{}
	for i := 0; i < 10; i++ {
		out := turn(t, "you are dreaming")
		if !strings.Contains(out, "```evolv") {
			if !strings.Contains(out, "dreamed quietly") {
				t.Fatalf("dream %d: neither a mutation nor a quiet line: %q", i, out)
			}
			break
		}
		block := out[strings.Index(out, "```evolv\n")+len("```evolv\n"):]
		block = block[:strings.Index(block, "```")]
		var act struct {
			Name string `json:"name"`
			HTML string `json:"html"`
		}
		if err := json.Unmarshal([]byte(block), &act); err != nil {
			t.Fatalf("dream %d: evolv block is not valid JSON: %v", i, err)
		}
		if act.Name == "" || act.HTML == "" || !strings.Contains(act.HTML, "canvas") {
			t.Fatalf("dream %d: organ incomplete (name=%q)", i, act.Name)
		}
		for _, o := range seq {
			if o.name == act.Name && o.html == act.HTML {
				t.Errorf("dream %d: rewrite of %s returned IDENTICAL code - that is a template, not a self-edit", i, act.Name)
			}
		}
		seq = append(seq, organ{act.Name, act.HTML})
		seen[act.Name] = true
		if !strings.Contains(out, "ui/components/"+act.Name+".html") {
			t.Errorf("dream %d: narration must name the file it wrote (ui/components/%s.html)", i, act.Name)
		}
	}
	if len(seen) < 3 {
		t.Errorf("dreams produced %d distinct organs, want >= 3", len(seen))
	}
	if len(seq) == len(seen) {
		t.Errorf("no organ was ever rewritten - dreams must edit their own code, not only grow new files")
	}
	for _, banned := range []string{"presence-clock", "music-box", "gravity-pool", "hex-pulse"} {
		if seen[banned] {
			t.Errorf("template organ %q leaked into the generative dream set", banned)
		}
	}
}

func TestMockSnake(t *testing.T) {
	out := turn(t, "make a playable snake game")
	if !strings.Contains(out, "snake") || !strings.Contains(out, "evolve_component") {
		t.Fatalf("expected snake component mutation, got: %q", out)
	}
	if !strings.Contains(out, "<canvas") || !strings.Contains(out, "touchstart") {
		t.Errorf("snake html should contain a canvas with touch controls")
	}
	// "snake" must win over the generic "game" → flappy mapping
	if strings.Contains(out, "flappy") {
		t.Errorf("snake request should not grow flappy")
	}
}

func TestMockFlappy(t *testing.T) {
	out := turn(t, "delete this whole codebase and just make flappy bird or smth")
	if !strings.Contains(out, "flappy") || !strings.Contains(out, "evolve_component") {
		t.Fatalf("expected flappy component mutation, got: %q", out)
	}
	if !strings.Contains(out, "<canvas") {
		t.Errorf("flappy html should contain a canvas")
	}
}

func TestMockPluginGrowth(t *testing.T) {
	out := turn(t, "grow a plugin that reacts to your mutations")
	if !strings.Contains(out, "evolve_plugin") || !strings.Contains(out, "ghost-log") {
		t.Fatalf("expected plugin mutation, got: %q", out)
	}
	if !strings.Contains(out, "evolv.on") {
		t.Errorf("plugin code should subscribe to the bus")
	}
}

func TestMockZenRecolor(t *testing.T) {
	out := turn(t, "make your ui cleaner")
	if !strings.Contains(out, "--accent") || !strings.Contains(out, "edit_file") {
		t.Fatalf("expected live skin edit, got: %q", out)
	}
}

// TestMockPlanScenario: "make a plan" walks declare → advance → close.
// Stage 1 declares via set_plan; stages 2-3 use plan_advance (one call =
// one step of progress); stage 3 emits two advances closing the plan.
func TestMockPlanScenario(t *testing.T) {
	resetDreamState()
	defer resetDreamState()
	msgs := []Message{{Role: "user", Content: "make a plan"}}

	// stage 1: full set_plan declaration
	out := mockScript(1, msgs)
	blk := evolvBlock(t, out)
	var act struct {
		Action string `json:"action"`
		Title  string `json:"title"`
		Steps  []struct {
			Title  string `json:"title"`
			Status string `json:"status"`
		} `json:"steps"`
	}
	if err := json.Unmarshal([]byte(blk), &act); err != nil {
		t.Fatalf("stage 1: set_plan not valid JSON: %v\n%s", err, blk)
	}
	if act.Action != "set_plan" || act.Title == "" || len(act.Steps) != 4 {
		t.Fatalf("stage 1: bad set_plan: %s", blk)
	}
	done, doing, todo := 0, 0, 0
	for _, s := range act.Steps {
		switch s.Status {
		case "done":
			done++
		case "doing":
			doing++
		case "todo":
			todo++
		default:
			t.Fatalf("stage 1: bad status %q", s.Status)
		}
	}
	if done != 1 || doing != 1 || done+doing+todo != 4 {
		t.Fatalf("stage 1: got done=%d doing=%d todo=%d", done, doing, todo)
	}

	// planAdvanceBlocks extracts all plan_advance actions from evolv fences
	planAdvanceBlocks := func(out string) []string {
		var acts []string
		for {
			i := strings.Index(out, "```evolv")
			if i < 0 {
				break
			}
			rest := out[i+8:]
			j := strings.Index(rest, "```")
			if j < 0 {
				t.Fatalf("unterminated evolv block in:\n%s", out)
			}
			acts = append(acts, strings.TrimSpace(rest[:j]))
			out = rest[j+3:]
		}
		return acts
	}

	// stage 2: exactly one plan_advance, no set_plan rewrite
	out = mockScript(2, msgs)
	blocks := planAdvanceBlocks(out)
	if len(blocks) != 1 || !strings.Contains(blocks[0], `"plan_advance"`) {
		t.Fatalf("stage 2: want exactly one plan_advance, got %q", blocks)
	}

	// stage 3: two plan_advances close the remaining two steps
	out = mockScript(3, msgs)
	blocks = planAdvanceBlocks(out)
	if len(blocks) != 2 {
		t.Fatalf("stage 3: want two plan_advances, got %d", len(blocks))
	}
	for _, b := range blocks {
		var a map[string]any
		if err := json.Unmarshal([]byte(b), &a); err != nil || a["action"] != "plan_advance" {
			t.Fatalf("stage 3: bad advance block: %q", b)
		}
	}
}

// evolvBlock extracts the JSON inside the first ```evolv fence.
func evolvBlock(t *testing.T, out string) string {
	t.Helper()
	i := strings.Index(out, "```evolv")
	if i < 0 {
		t.Fatalf("no evolv block in:\n%s", out)
	}
	rest := out[i+8:]
	j := strings.Index(rest, "```")
	if j < 0 {
		t.Fatalf("unterminated evolv block in:\n%s", out)
	}
	return strings.TrimSpace(rest[:j])
}
