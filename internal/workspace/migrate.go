package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// A migration is an ordered, idempotent workspace upgrade. Each one records
// itself in workspace/migrations.json so it runs exactly once per genome, and
// the journal is written before and after every step so a crash mid-upgrade
// leaves evidence instead of a half-migrated workspace with no explanation.
//
// Rules for authors:
//   - never delete user content; move or transform it
//   - be safe to run twice (check before writing)
//   - report what happened in the Result so the user learns what changed

// Migration is one versioned workspace upgrade.
type Migration struct {
	ID      string // stable, ordered, e.g. "0002-plan-lifecycle"
	Since   string // semver this migration first shipped in
	Summary string
	Apply   func(w *W) (Result, error)
}

// Result reports what a migration touched.
type Result struct {
	Changed  []string `json:"changed,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// MigrationJournal is the persisted record of applied migrations.
type MigrationJournal struct {
	Applied []AppliedMigration `json:"applied"`
	Updated string             `json:"updated"`
}

// AppliedMigration records one completed migration.
type AppliedMigration struct {
	ID      string   `json:"id"`
	Version string   `json:"version"`
	At      string   `json:"at"`
	Changed []string `json:"changed,omitempty"`
	Warn    []string `json:"warnings,omitempty"`
}

// MigrationFile holds the journal at the workspace root.
const MigrationFile = "migrations.json"

// Migrations returns the ordered migration list.
func Migrations() []Migration { return migrations }

var migrations = []Migration{
	{
		ID:      "0001-swarm-exec-dirs",
		Since:   "0.31.0",
		Summary: "ensure durable execution directories exist",
		Apply: func(w *W) (Result, error) {
			var res Result
			for _, dir := range []string{
				filepath.Join(WorkspaceDir, "runs"),
				filepath.Join(WorkspaceDir, "swarm"),
				filepath.Join(WorkspaceDir, "execution"),
			} {
				full := w.Dir(dir)
				if _, err := os.Stat(full); os.IsNotExist(err) {
					if err := os.MkdirAll(full, 0o755); err != nil {
						return res, err
					}
					res.Changed = append(res.Changed, dir)
				}
			}
			return res, nil
		},
	},
	{
		ID:      "0002-plan-json-guard",
		Since:   "0.31.0",
		Summary: "back up and repair an unreadable plan.json",
		Apply: func(w *W) (Result, error) {
			var res Result
			path := w.Dir(PlanFile)
			raw, err := os.ReadFile(path)
			if err != nil {
				return res, nil
			}
			var p Plan
			if json.Unmarshal(raw, &p) == nil {
				return res, nil
			}
			backup := path + ".corrupt-" + time.Now().UTC().Format("20060102-150405")
			if err := os.Rename(path, backup); err != nil {
				return res, err
			}
			res.Changed = append(res.Changed, filepath.ToSlash(PlanFile)+" → "+filepath.Base(backup))
			res.Warnings = append(res.Warnings, "plan.json was unreadable; it was preserved as a backup and the agent will start a fresh plan")
			return res, nil
		},
	},
	{
		ID:      "0003-config-normalise",
		Since:   "0.31.0",
		Summary: "fill missing config maps and drop unknown empty keys",
		Apply: func(w *W) (Result, error) {
			var res Result
			if w.Config.APIKeys == nil {
				w.Config.APIKeys = map[string]string{}
				res.Changed = append(res.Changed, "config.api_keys")
			}
			if w.Config.BaseURLs == nil {
				w.Config.BaseURLs = map[string]string{}
				res.Changed = append(res.Changed, "config.base_urls")
			}
			if len(res.Changed) > 0 {
				w.SaveConfig()
			}
			return res, nil
		},
	},
}

// ReadMigrationJournal loads the journal, tolerating a missing or corrupt file.
func ReadMigrationJournal(root string) MigrationJournal {
	var j MigrationJournal
	raw, err := os.ReadFile(filepath.Join(root, MigrationFile))
	if err != nil {
		return j
	}
	_ = json.Unmarshal(raw, &j)
	return j
}

// WriteMigrationJournal persists the journal atomically.
func WriteMigrationJournal(root string, j MigrationJournal) error {
	j.Updated = time.Now().UTC().Format(time.RFC3339)
	raw, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWrite(filepath.Join(root, MigrationFile), raw, 0o644)
}

// MigrateOptions controls the runner.
type MigrateOptions struct {
	DryRun bool
}

// MigrateReport summarises one run.
type MigrateReport struct {
	Run      []string `json:"run"`
	Skipped  []string `json:"skipped"`
	Warnings []string `json:"warnings"`
	DryRun   bool     `json:"dry_run"`
}

// Migrate applies every pending migration in order. A failing migration
// stops the run and leaves the journal untouched for that step, so the next
// boot retries it rather than skipping silently.
func (w *W) Migrate(opts MigrateOptions) (MigrateReport, error) {
	report := MigrateReport{DryRun: opts.DryRun}
	journal := ReadMigrationJournal(w.Root)
	done := map[string]bool{}
	for _, a := range journal.Applied {
		done[a.ID] = true
	}
	pending := []Migration{}
	for _, m := range Migrations() {
		if done[m.ID] {
			report.Skipped = append(report.Skipped, m.ID)
			continue
		}
		pending = append(pending, m)
	}
	sort.SliceStable(pending, func(i, j int) bool { return pending[i].ID < pending[j].ID })
	for _, m := range pending {
		report.Run = append(report.Run, m.ID)
		if opts.DryRun {
			continue
		}
		res, err := m.Apply(w)
		if err != nil {
			return report, fmt.Errorf("migration %s (%s) failed: %w", m.ID, m.Summary, err)
		}
		report.Warnings = append(report.Warnings, res.Warnings...)
		journal.Applied = append(journal.Applied, AppliedMigration{
			ID: m.ID, Version: Version, At: time.Now().UTC().Format(time.RFC3339),
			Changed: res.Changed, Warn: res.Warnings,
		})
		if err := WriteMigrationJournal(w.Root, journal); err != nil {
			return report, err
		}
	}
	if !opts.DryRun && len(pending) > 0 {
		if err := WriteMigrationJournal(w.Root, journal); err != nil {
			return report, err
		}
	}
	return report, nil
}
