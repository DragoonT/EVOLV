// Package genome gives EVOLV's grown organs a real lifecycle: stable identity,
// versions, provenance and health. Without it, tools, skills, plugins and
// prompts accumulate as anonymous files and every improvement is a gamble.
package genome

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Kind is the type of a grown organ.
type Kind string

const (
	KindTool      Kind = "tool"
	KindSkill     Kind = "skill"
	KindPrompt    Kind = "prompt"
	KindPlugin    Kind = "plugin"
	KindComponent Kind = "component"
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	switch k {
	case KindTool, KindSkill, KindPrompt, KindPlugin, KindComponent:
		return true
	}
	return false
}

// Provenance records where an asset came from.
type Provenance struct {
	// Trigger is why it was created: "user", "dream", a run id, an import.
	Trigger string `json:"trigger,omitempty"`
	// Run is the durable run that produced it, when any.
	Run string `json:"run,omitempty"`
	// Model is the model that wrote it, when known.
	Model string `json:"model,omitempty"`
	// Notes is a short human explanation.
	Notes string `json:"notes,omitempty"`
}

// Health counters power the "is this getting better?" view.
type Health struct {
	Uses     int    `json:"uses"`
	Failures int    `json:"failures"`
	LastUsed string `json:"last_used,omitempty"`
	// LastError is the most recent failure, trimmed.
	LastError string `json:"last_error,omitempty"`
	// Reverts counts how many times it was rolled back.
	Reverts int `json:"reverts,omitempty"`
}

// Asset is the metadata record for one grown organ. The implementation lives
// in its usual file; this record is what makes it accountable.
type Asset struct {
	ID      string     `json:"id"`
	Kind    Kind       `json:"kind"`
	Name    string     `json:"name"`
	Version int        `json:"version"`
	Hash    string     `json:"hash"`
	Path    string     `json:"path"` // workspace-relative
	Created string     `json:"created"`
	Updated string     `json:"updated"`
	Parent  string     `json:"parent,omitempty"` // previous version of this asset
	Origin  Provenance `json:"origin,omitempty"`
	Health  Health     `json:"health"`
	Status  string     `json:"status"` // active | quarantined | archived
	Title   string     `json:"title,omitempty"`
	Summary string     `json:"summary,omitempty"`
}

// activeStatus is the only status that is callable/loaded.
const activeStatus = "active"

// HashBytes returns the content hash used everywhere in the genome.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// AssetID is the stable identity of an organ: kind + name.
func AssetID(kind Kind, name string) string { return string(kind) + ":" + name }

// Index is the on-disk registry of every asset.
type Index struct {
	Root   string            `json:"-"`
	Assets map[string]*Asset `json:"assets"`
	Path   string            `json:"-"`
}

// IndexRel is where the index lives inside the workspace.
const IndexRel = "workspace/genome/index.json"

// OpenIndex loads (or creates) the asset index rooted at workspace root.
func OpenIndex(workspaceRoot string) (*Index, error) {
	idx := &Index{Root: workspaceRoot, Assets: map[string]*Asset{}, Path: filepath.Join(workspaceRoot, IndexRel)}
	raw, err := os.ReadFile(idx.Path)
	if err == nil {
		var onDisk struct {
			Assets map[string]*Asset `json:"assets"`
		}
		if json.Unmarshal(raw, &onDisk) == nil && onDisk.Assets != nil {
			// Drop records that no longer point at a file: the filesystem is
			// the source of truth, the index is just bookkeeping.
			for id, a := range onDisk.Assets {
				if a == nil || !a.Kind.Valid() {
					continue
				}
				idx.Assets[id] = a
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return idx, nil
}

// Save writes the index atomically.
func (i *Index) Save() error {
	if err := os.MkdirAll(filepath.Dir(i.Path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(map[string]any{"assets": i.Assets, "updated": time.Now().UTC().Format(time.RFC3339)}, "", "  ")
	if err != nil {
		return err
	}
	tmp := i.Path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, i.Path)
}

// Record registers a new version of an organ after its file was written.
//
// If the content is identical to the recorded hash, nothing changes — an
// idempotent re-evolve must not inflate version numbers.
func (i *Index) Record(kind Kind, name, relPath string, content []byte, origin Provenance) (Asset, bool, error) {
	if !kind.Valid() {
		return Asset{}, false, fmt.Errorf("unknown asset kind %q", kind)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Asset{}, false, fmt.Errorf("asset name is required")
	}
	id := AssetID(kind, name)
	hash := HashBytes(content)
	now := time.Now().UTC().Format(time.RFC3339)
	if prev, ok := i.Assets[id]; ok {
		if prev.Hash == hash {
			prev.Updated = now
			if prev.Origin.Trigger == "" {
				prev.Origin = origin
			}
			return *prev, false, i.Save()
		}
		next := &Asset{
			ID: id, Kind: kind, Name: name, Version: prev.Version + 1, Hash: hash,
			Path: relPath, Created: prev.Created, Updated: now, Parent: prev.ID,
			Origin: origin, Health: prev.Health, Status: activeStatus,
			Title: prev.Title, Summary: prev.Summary,
		}
		i.Assets[id] = next
		return *next, true, i.Save()
	}
	a := &Asset{
		ID: id, Kind: kind, Name: name, Version: 1, Hash: hash, Path: relPath,
		Created: now, Updated: now, Origin: origin, Status: activeStatus,
	}
	i.Assets[id] = a
	return *a, true, i.Save()
}

// Get returns an asset by kind and name.
func (i *Index) Get(kind Kind, name string) (Asset, bool) {
	a, ok := i.Assets[AssetID(kind, name)]
	if !ok {
		return Asset{}, false
	}
	return *a, true
}

// SetStatus changes an asset's status (active / quarantined / archived).
func (i *Index) SetStatus(kind Kind, name, status string) (Asset, error) {
	id := AssetID(kind, name)
	a, ok := i.Assets[id]
	if !ok {
		return Asset{}, fmt.Errorf("no %s named %q", kind, name)
	}
	switch status {
	case "active", "quarantined", "archived":
	default:
		return Asset{}, fmt.Errorf("unknown status %q", status)
	}
	a.Status = status
	a.Updated = time.Now().UTC().Format(time.RFC3339)
	if err := i.Save(); err != nil {
		return Asset{}, err
	}
	return *a, nil
}

// NoteUse records that an organ did something, and whether it worked. This is
// the raw signal the health report is built from.
func (i *Index) NoteUse(kind Kind, name string, ok bool, errText string) {
	id := AssetID(kind, name)
	a, exists := i.Assets[id]
	if !exists {
		return
	}
	a.Health.Uses++
	a.Health.LastUsed = time.Now().UTC().Format(time.RFC3339)
	if !ok {
		a.Health.Failures++
		a.Health.LastError = trim(errText, 240)
	}
	_ = i.Save()
}

// NoteRevert records a rollback so chronic instability is visible.
func (i *Index) NoteRevert(kind Kind, name string) {
	id := AssetID(kind, name)
	a, exists := i.Assets[id]
	if !exists {
		return
	}
	a.Health.Reverts++
	_ = i.Save()
}

// List returns assets sorted by kind then name, optionally filtered.
func (i *Index) List(kinds ...Kind) []Asset {
	filter := map[Kind]bool{}
	for _, k := range kinds {
		filter[k] = true
	}
	out := make([]Asset, 0, len(i.Assets))
	for _, a := range i.Assets {
		if len(filter) > 0 && !filter[a.Kind] {
			continue
		}
		out = append(out, *a)
	}
	sort.Slice(out, func(x, y int) bool {
		if out[x].Kind != out[y].Kind {
			return out[x].Kind < out[y].Kind
		}
		return out[x].Name < out[y].Name
	})
	return out
}

// HealthRow is one line of the health report.
type HealthRow struct {
	ID       string `json:"id"`
	Kind     Kind   `json:"kind"`
	Name     string `json:"name"`
	Version  int    `json:"version"`
	Uses     int    `json:"uses"`
	Failures int    `json:"failures"`
	Reverts  int    `json:"reverts"`
	// SuccessRate is percent; -1 when never used.
	SuccessRate int    `json:"success_rate"`
	Verdict     string `json:"verdict"` // healthy | unproven | flaky | broken | stale
	Status      string `json:"status"`
	Updated     string `json:"updated"`
}

// HealthReport classifies every organ by observed behaviour.
func (i *Index) HealthReport(now time.Time) []HealthRow {
	rows := make([]HealthRow, 0, len(i.Assets))
	for _, a := range i.Assets {
		row := HealthRow{
			ID: a.ID, Kind: a.Kind, Name: a.Name, Version: a.Version,
			Uses: a.Health.Uses, Failures: a.Health.Failures, Reverts: a.Health.Reverts,
			Status: a.Status, Updated: a.Updated, SuccessRate: -1,
		}
		if a.Health.Uses > 0 {
			row.SuccessRate = (a.Health.Uses - a.Health.Failures) * 100 / a.Health.Uses
		}
		switch {
		case a.Status != activeStatus:
			row.Verdict = "stale"
		case a.Health.Uses == 0:
			row.Verdict = "unproven"
		case a.Health.Failures*2 > a.Health.Uses:
			row.Verdict = "broken"
		case a.Health.Failures > 0 || a.Health.Reverts > 0:
			row.Verdict = "flaky"
		default:
			row.Verdict = "healthy"
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(x, y int) bool {
		rank := func(v string) int {
			switch v {
			case "broken":
				return 0
			case "flaky":
				return 1
			case "unproven":
				return 2
			case "healthy":
				return 3
			default:
				return 4
			}
		}
		if rank(rows[x].Verdict) != rank(rows[y].Verdict) {
			return rank(rows[x].Verdict) < rank(rows[y].Verdict)
		}
		return rows[x].ID < rows[y].ID
	})
	return rows
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---------- version history and rollback ----------

// historyDir holds immutable snapshots of every version of every asset.
func (i *Index) historyDir(kind Kind, name string) string {
	return filepath.Join(i.Root, "workspace", "genome", "history", string(kind), name)
}

// versionFile is the path of one stored version.
func (i *Index) versionFile(kind Kind, name string, version int) string {
	return filepath.Join(i.historyDir(kind, name), fmt.Sprintf("v%d%s", version, recordExt(kind)))
}

// recordExt is the file extension an asset's implementation uses.
func recordExt(kind Kind) string {
	switch kind {
	case KindTool, KindPlugin:
		return ".js"
	case KindComponent:
		return ".html"
	default:
		return ".md"
	}
}

// Restore writes a previous version back as a NEW current version. The old
// bytes stay archived, so a rollback of a rollback still works.
//
// It is the only supported way to move an asset backwards: Record() alone
// cannot do it, because archiving happens outside the index.
func (i *Index) Restore(kind Kind, name string, version int) (Asset, []byte, error) {
	plan, err := i.PlanRollback(kind, name, version)
	if err != nil {
		return Asset{}, nil, err
	}
	content, err := i.ReadVersion(kind, name, version)
	if err != nil {
		return Asset{}, nil, err
	}
	asset, err := i.PlanRestore(plan, content)
	return asset, content, err
}

// PlanRestore is the index half of Restore, split out so the caller can write
// the file first and only then commit the record.
func (i *Index) PlanRestore(plan RollbackPlan, content []byte) (Asset, error) {
	if HashBytes(content) != plan.TargetHash {
		return Asset{}, fmt.Errorf("archived bytes for v%d do not match the recorded hash", plan.To)
	}
	a, ok := i.Assets[AssetID(plan.Kind, plan.Name)]
	if !ok {
		return Asset{}, fmt.Errorf("no %s named %q", plan.Kind, plan.Name)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	// If the current content already equals the target, do not mint a version.
	if a.Hash == plan.TargetHash {
		return *a, nil
	}
	next := *a
	next.Version = a.Version + 1
	next.Hash = plan.TargetHash
	next.Updated = now
	next.Parent = a.ID
	next.Origin = Provenance{Trigger: "rollback", Notes: fmt.Sprintf("restored v%d", plan.To)}
	next.Health = a.Health
	next.Health.Reverts = a.Health.Reverts + 1
	i.Assets[next.ID] = &next
	return next, i.Save()
}

// ArchiveVersion stores one immutable copy of an asset's content. Rolling back
// needs the exact bytes, not a hash.
func (i *Index) ArchiveVersion(kind Kind, name string, version int, content []byte) error {
	dir := i.historyDir(kind, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := i.versionFile(kind, name, version)
	if _, err := os.Stat(path); err == nil {
		return nil // immutable: the first write wins
	}
	return os.WriteFile(path, content, 0o644)
}

// VersionInfo is one archived version.
type VersionInfo struct {
	Version int    `json:"version"`
	Hash    string `json:"hash"`
	Bytes   int    `json:"bytes"`
	Current bool   `json:"current"`
}

// Versions lists archived versions, newest first.
func (i *Index) Versions(kind Kind, name string) []VersionInfo {
	dir := i.historyDir(kind, name)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	current := 0
	if a, ok := i.Get(kind, name); ok {
		current = a.Version
	}
	var out []VersionInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		var v int
		if _, err := fmt.Sscanf(e.Name(), "v%d", &v); err != nil {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, VersionInfo{Version: v, Hash: HashBytes(raw), Bytes: len(raw), Current: v == current})
	}
	sort.Slice(out, func(x, y int) bool { return out[x].Version > out[y].Version })
	return out
}

// ReadVersion returns the archived bytes of one version.
func (i *Index) ReadVersion(kind Kind, name string, version int) ([]byte, error) {
	if version < 1 {
		return nil, fmt.Errorf("version must be >= 1")
	}
	return os.ReadFile(i.versionFile(kind, name, version))
}

// PlanRollback describes what a rollback would do, without doing it.
type RollbackPlan struct {
	Kind       Kind   `json:"kind"`
	Name       string `json:"name"`
	From       int    `json:"from"`
	To         int    `json:"to"`
	Path       string `json:"path"`
	TargetHash string `json:"target_hash"`
}

// PlanRollback validates a rollback request.
func (i *Index) PlanRollback(kind Kind, name string, version int) (RollbackPlan, error) {
	a, ok := i.Get(kind, name)
	if !ok {
		return RollbackPlan{}, fmt.Errorf("no %s named %q", kind, name)
	}
	if version == a.Version {
		return RollbackPlan{}, fmt.Errorf("%s %q is already at version %d", kind, name, version)
	}
	raw, err := i.ReadVersion(kind, name, version)
	if err != nil {
		return RollbackPlan{}, fmt.Errorf("version %d of %s %q is not archived", version, kind, name)
	}
	return RollbackPlan{
		Kind: kind, Name: name, From: a.Version, To: version,
		Path: a.Path, TargetHash: HashBytes(raw),
	}, nil
}
