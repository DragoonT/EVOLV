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
// The id is time-ordered so lexical sort == chronological order. Two batches
// in the same second get -01, -02 suffixes so they never collide.
func (w *W) Snapshot() (string, error) {
	base := time.Now().UTC().Format("20060102-150405")
	root := filepath.Join(w.Dir(SnapshotsDir), base)
	id := base
	for i := 1; ; i++ {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			break
		}
		id = fmt.Sprintf("%s-%02d", base, i)
		root = filepath.Join(w.Dir(SnapshotsDir), id)
	}
	dest := root
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

// validateSnapshotID refuses anything that isn't a plain snapshot id
// (no separators, no dots, no path games).
func validateSnapshotID(id string) error {
	ok := len(id) == 15 || (len(id) == 18 && id[15] == '-')
	if !ok || strings.ContainsAny(id, "./\\") {
		return fmt.Errorf("no such snapshot: %s", id)
	}
	return nil
}

// Restore rewinds the genome to a snapshot. Returns the snapshot id restored.
func (w *W) Restore(id string) error {
	id = strings.TrimSpace(id)
	if err := validateSnapshotID(id); err != nil {
		return err
	}
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
		// 20060102-150405 or 20060102-150405-NN (same-second batches)
		if e.IsDir() && (len(e.Name()) == 15 || (len(e.Name()) == 18 && e.Name()[15:16] == "-")) {
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
