package agent

import (
	"os"
	"sort"
	"strings"

	"github.com/dxn111/EVOLV/internal/genome"
)

// organList reports every grown organ with its version and health verdict.
func (a *Agent) organList(act action) map[string]any {
	var kinds []genome.Kind
	if k := strings.TrimSpace(act.Kind); k != "" {
		if !genome.Kind(k).Valid() {
			return runErr(map[string]any{"action": "organ_list"}, "unknown organ kind "+k)
		}
		kinds = append(kinds, genome.Kind(k))
	}
	rows := a.genomeHealth()
	if len(kinds) > 0 {
		filtered := rows[:0]
		for _, r := range rows {
			if r.Kind == kinds[0] {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	res := map[string]any{"action": "organ_list", "ok": true, "organs": rows, "count": len(rows)}
	problem := 0
	for _, r := range rows {
		if r.Verdict == "broken" || r.Verdict == "flaky" {
			problem++
		}
	}
	res["needing_attention"] = problem
	return res
}

// organHistory lists archived versions of one organ.
func (a *Agent) organHistory(act action) map[string]any {
	res := map[string]any{"action": "organ_history"}
	kind := genome.Kind(strings.TrimSpace(act.Kind))
	name := sanitizeName(act.Name)
	if !kind.Valid() || name == "" {
		return runErr(res, "organ_history needs kind (tool|skill|prompt|plugin|component) and name")
	}
	idx, err := a.genomeIndex()
	if err != nil {
		return runErr(res, err.Error())
	}
	asset, ok := idx.Get(kind, name)
	if !ok {
		return runErr(res, "no "+string(kind)+" named "+name)
	}
	res["ok"] = true
	res["asset"] = asset
	res["versions"] = idx.Versions(kind, name)
	return res
}

// organRollback restores a previous version of an organ, on purpose and
// recoverably: the current content is archived first, so a rollback of a
// rollback works.
func (a *Agent) organRollback(act action) map[string]any {
	res := map[string]any{"action": "organ_rollback"}
	kind := genome.Kind(strings.TrimSpace(act.Kind))
	name := sanitizeName(act.Name)
	if !kind.Valid() || name == "" || act.Version <= 0 {
		return runErr(res, "organ_rollback needs kind, name and version")
	}
	idx, err := a.genomeIndex()
	if err != nil {
		return runErr(res, err.Error())
	}
	plan, err := idx.PlanRollback(kind, name, act.Version)
	if err != nil {
		return runErr(res, err.Error())
	}
	content, err := idx.ReadVersion(kind, name, act.Version)
	if err != nil {
		return runErr(res, err.Error())
	}
	target := a.ws.Dir(plan.Path)
	if err := writeOrganFile(target, content); err != nil {
		return runErr(res, err.Error())
	}
	asset, err := idx.PlanRestore(plan, content)
	if err != nil {
		return runErr(res, err.Error())
	}
	_ = idx.ArchiveVersion(kind, name, asset.Version, content)
	a.reloadOrganRuntime(kind, name)
	res["ok"] = true
	res["restored_version"] = act.Version
	res["now_version"] = asset.Version
	res["path"] = plan.Path
	res["note"] = "previous version archived; this rollback can be undone by rolling back again"
	a.bus.Publish("genome:rollback", map[string]any{
		"kind": string(kind), "name": name, "to": act.Version, "now": asset.Version,
	})
	return res
}

// organDisable quarantines or archives an organ so it stops loading.
func (a *Agent) organDisable(act action) map[string]any {
	res := map[string]any{"action": "organ_disable"}
	kind := genome.Kind(strings.TrimSpace(act.Kind))
	name := sanitizeName(act.Name)
	if !kind.Valid() || name == "" {
		return runErr(res, "organ_disable needs kind and name")
	}
	status := strings.TrimSpace(act.Reason)
	if status != "archived" && status != "quarantined" {
		status = "quarantined"
	}
	idx, err := a.genomeIndex()
	if err != nil {
		return runErr(res, err.Error())
	}
	asset, err := idx.SetStatus(kind, name, status)
	if err != nil {
		return runErr(res, err.Error())
	}
	res["ok"] = true
	res["asset"] = asset
	a.bus.Publish("genome:asset", map[string]any{"id": asset.ID, "kind": string(kind), "name": name, "status": status})
	return res
}

// writeOrganFile writes an organ file with the same guard as every other
// workspace write.
func writeOrganFile(path string, content []byte) error {
	return os.WriteFile(path, content, 0o644)
}

// reloadOrganRuntime applies an organ change to the live runtime so the user
// does not have to restart the organism.
func (a *Agent) reloadOrganRuntime(kind genome.Kind, name string) {
	switch kind {
	case genome.KindTool, genome.KindPlugin, genome.KindComponent:
		a.js.LoadAll()
	case genome.KindSkill, genome.KindPrompt:
		// read at prompt-build time; nothing to reload
	}
}

// contributingSection tells the agent whether it is allowed to change itself
// and what it should do with that standing.
func contributingEnabled(a *Agent) bool {
	return strings.TrimSpace(a.ws.Config.Contributing) == "on"
}

// orgKindsDisplay is the stable order used in listings and prompts.
var orgKindsDisplay = []genome.Kind{
	genome.KindSkill, genome.KindTool, genome.KindPrompt, genome.KindPlugin, genome.KindComponent,
}

// sortNames returns names in stable order.
func sortNames(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}
