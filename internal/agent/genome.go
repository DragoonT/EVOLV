package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dxn111/EVOLV/internal/genome"
	"github.com/dxn111/EVOLV/internal/workspace"
)

// genomeIndex opens the organ registry for this workspace.
func (a *Agent) genomeIndex() (*genome.Index, error) {
	return genome.OpenIndex(a.ws.Root)
}

// relPath converts an absolute workspace path into a workspace-relative one.
func (a *Agent) relPath(abs string) string {
	rel, err := filepath.Rel(a.ws.Root, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

// recordOrgan registers a new version of a grown organ and archives its bytes
// so a rollback is always possible.
//
// This is the difference between "the agent wrote a file" and "the workspace
// improved and can prove it": every organ now has an identity, a version, a
// hash and a recoverable history.
func (a *Agent) recordOrgan(kind genome.Kind, name string, absPath string, content []byte, origin genome.Provenance) (genome.Asset, bool) {
	idx, err := a.genomeIndex()
	if err != nil {
		return genome.Asset{}, false
	}
	if origin.Model == "" {
		if _, model := a.ProviderModel(); model != "" {
			origin.Model = model
		}
	}
	asset, changed, err := idx.Record(kind, name, a.relPath(absPath), content, origin)
	if err != nil {
		return genome.Asset{}, false
	}
	_ = idx.ArchiveVersion(kind, name, asset.Version, content)
	if changed {
		a.bus.Publish("genome:asset", map[string]any{
			"id": asset.ID, "kind": string(asset.Kind), "name": asset.Name,
			"version": asset.Version, "status": asset.Status,
		})
	}
	return asset, changed
}

// noteOrganUse feeds the health signal.
func (a *Agent) noteOrganUse(kind genome.Kind, name string, ok bool, errText string) {
	if idx, err := a.genomeIndex(); err == nil {
		idx.NoteUse(kind, name, ok, errText)
	}
}

// organForAction maps an evolve_* action to its asset kind and file location.
func organForAction(action, name string) (genome.Kind, string) {
	switch action {
	case "evolve_tool":
		return genome.KindTool, filepath.Join(workspace.ToolsDir, name+".js")
	case "evolve_skill":
		return genome.KindSkill, filepath.Join(workspace.SkillsDir, name+".md")
	case "evolve_prompt":
		return genome.KindPrompt, filepath.Join(workspace.PromptsDir, name+".md")
	case "evolve_component":
		return genome.KindComponent, filepath.Join(workspace.ComponentsDir, name+".html")
	case "evolve_plugin":
		return genome.KindPlugin, filepath.Join(workspace.PluginsDir, name, "plugin.js")
	}
	return "", ""
}

// BackfillOrgans is the exported entry point used by the server.
func (a *Agent) BackfillOrgans() int { return a.backfillGenome() }

// backfillGenome registers organs that exist on disk but predate the index
// (a workspace upgraded from an older version). It never overwrites a record.
func (a *Agent) backfillGenome() int {
	idx, err := a.genomeIndex()
	if err != nil {
		return 0
	}
	added := 0
	scan := func(kind genome.Kind, dir, ext string, nameOf func(string) string) {
		entries, err := os.ReadDir(a.ws.Dir(dir))
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ext) {
				continue
			}
			name := nameOf(e.Name())
			if name == "" {
				continue
			}
			if _, exists := idx.Get(kind, name); exists {
				continue
			}
			abs := filepath.Join(a.ws.Dir(dir), e.Name())
			raw, err := os.ReadFile(abs)
			if err != nil {
				continue
			}
			asset, changed, err := idx.Record(kind, name, a.relPath(abs), raw, genome.Provenance{Trigger: "backfill"})
			if err == nil && changed {
				_ = idx.ArchiveVersion(kind, name, asset.Version, raw)
				added++
			}
		}
	}
	base := func(suffix string) func(string) string {
		return func(n string) string { return strings.TrimSuffix(n, suffix) }
	}
	scan(genome.KindTool, workspace.ToolsDir, ".js", base(".js"))
	scan(genome.KindSkill, workspace.SkillsDir, ".md", base(".md"))
	scan(genome.KindPrompt, workspace.PromptsDir, ".md", base(".md"))
	scan(genome.KindComponent, workspace.ComponentsDir, ".html", base(".html"))
	return added
}

// genomeHealth returns the organ health report for prompts and APIs.
func (a *Agent) genomeHealth() []genome.HealthRow {
	idx, err := a.genomeIndex()
	if err != nil {
		return nil
	}
	return idx.HealthReport(time.Now().UTC())
}

// organHealthSection summarises organs that are failing, so the prompt does
// not have to inline the whole registry.
func (a *Agent) organHealthSection() string {
	rows := a.genomeHealth()
	var problem, unproven []string
	for _, r := range rows {
		switch r.Verdict {
		case "broken", "flaky":
			problem = append(problem, "  - "+string(r.Kind)+" "+r.Name+" v"+strItoa(r.Version)+": "+r.Verdict+
				" ("+strItoa(r.Uses)+" uses, "+strItoa(r.Failures)+" failures)")
		case "unproven":
			unproven = append(unproven, string(r.Kind)+" "+r.Name)
		}
	}
	if len(problem) == 0 && len(unproven) == 0 {
		return ""
	}
	var b strings.Builder
	if len(problem) > 0 {
		b.WriteString("\n## ORGANS THAT ARE FAILING — repair or retire them\n")
		if len(problem) > 6 {
			problem = problem[:6]
		}
		for _, line := range problem {
			b.WriteString(line + "\n")
		}
		b.WriteString("Use organ_rollback to restore a working version, or evolve_* to replace it.\n")
	}
	if len(unproven) > 0 {
		if len(unproven) > 8 {
			unproven = unproven[:8]
		}
		b.WriteString("Unproven organs (never exercised): " + strings.Join(unproven, ", ") + "\n")
	}
	return b.String()
}

// strItoa is a tiny local helper so prompt strings stay terse.
func strItoa(n int) string { return strconv.Itoa(n) }
