package workspace

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Time travel: the whole evolvable genome (ui, tools, skills, prompts,
// plugins, the evolution ledger) is copied into snapshots/<id>/ before every
// mutation batch, so any state of the organism can be restored later.

// SnapshotsDir is where genome snapshots live, relative to the organism home.
const SnapshotsDir = "snapshots"

// snapshotParts are the workspace-relative paths that make up the genome.
var snapshotParts = []string{"workspace/ui", "workspace/tools", "workspace/skills", "workspace/prompts", "workspace/plugins", EvolutionFile, ManifestFile}

// MaxSnapshots caps how many genome states are kept.
const MaxSnapshots = 20

// Snapshot copies the current genome into snapshots/<id>/ and returns the id.
// The id is time-ordered so lexical sort == chronological order.
func (w *W) Snapshot() (string, error) {
	id := time.Now().UTC().Format("20060102-150405")
	dest := filepath.Join(w.Dir(SnapshotsDir), id)
	for _, part := range snapshotParts {
		src := w.Dir(part)
		info, err := os.Stat(src)
		if err != nil {
			continue // absent part, nothing to copy
		}
		d := filepath.Join(dest, part)
		if info.IsDir() {
			if err := copyTree(src, d); err != nil {
				return "", err
			}
		} else {
			if err := copyFile(src, d); err != nil {
				return "", err
			}
		}
	}
	w.pruneSnapshots()
	return id, nil
}

// Restore rewinds the genome to a snapshot. Returns the snapshot id restored.
func (w *W) Restore(id string) error {
	id = filepath.Base(strings.TrimSpace(id)) // no path games
	src := filepath.Join(w.Dir(SnapshotsDir), id)
	if info, err := os.Stat(src); err != nil || !info.IsDir() {
		return fmt.Errorf("no such snapshot: %s", id)
	}
	for _, part := range snapshotParts {
		s := filepath.Join(src, part)
		if _, err := os.Stat(s); err != nil {
			continue // snapshot predates this part; leave current state
		}
		if err := os.RemoveAll(w.Dir(part)); err != nil {
			return err
		}
		if err := copyTree(s, w.Dir(part)); err != nil {
			return err
		}
	}
	return nil
}

// ListSnapshots returns snapshot ids, newest first.
func (w *W) ListSnapshots() []string {
	ents, err := os.ReadDir(w.Dir(SnapshotsDir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && len(e.Name()) == 15 { // 20060102-150405
			out = append(out, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

// pruneSnapshots keeps only the MaxSnapshots newest genome states.
func (w *W) pruneSnapshots() {
	ids := w.ListSnapshots()
	for i := MaxSnapshots; i < len(ids); i++ {
		os.RemoveAll(filepath.Join(w.Dir(SnapshotsDir), ids[i]))
	}
}

func copyTree(src, dest string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // tolerate vanishing files mid-copy
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return nil
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dest string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, raw, 0o644)
}
