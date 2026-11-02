package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestMigrationsRunOnLoad builds a realistic pre-upgrade workspace and checks
// the upgrade preserves user data while applying pending migrations.
func TestMigrationsRunOnLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, RootDir)
	if err := os.MkdirAll(filepath.Join(root, WorkspaceDir), 0o755); err != nil {
		t.Fatal(err)
	}
	// user config from an older release
	cfg := `{"provider":"openai","model":"gpt-x","api_keys":{"openai":"sk-test"},"version":"0.30.0","open_browser":false}`
	if err := os.WriteFile(filepath.Join(root, ConfigFile), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	// corrupt plan from a crash
	if err := os.WriteFile(filepath.Join(root, PlanFile), []byte("{broken plan"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.MigrationError(); err != nil {
		t.Fatalf("migration error: %v", err)
	}
	report := w.MigrationReport()
	if len(report.Run) == 0 {
		t.Fatalf("no migrations ran: %+v", report)
	}

	// user config preserved
	if w.Config.Provider != "openai" || w.Config.APIKeys["openai"] != "sk-test" {
		t.Fatalf("config lost: %+v", w.Config)
	}
	// corrupt plan preserved as a backup
	matches, _ := filepath.Glob(filepath.Join(root, PlanFile+".corrupt-*"))
	if len(matches) != 1 {
		t.Fatalf("corrupt plan backup = %v", matches)
	}
	if _, err := os.Stat(filepath.Join(root, PlanFile)); !os.IsNotExist(err) {
		t.Fatal("corrupt plan left in place")
	}

	// journal records what happened
	journal := ReadMigrationJournal(root)
	if len(journal.Applied) != len(Migrations()) {
		t.Fatalf("journal = %+v", journal.Applied)
	}

	// a second load is a no-op
	w2, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(w2.MigrationReport().Run) != 0 {
		t.Fatalf("migrations re-ran: %+v", w2.MigrationReport())
	}
}

func TestMigrationsDryRun(t *testing.T) {
	w := &W{Root: t.TempDir(), Config: defaultConfig()}
	report, err := w.Migrate(MigrateOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Run) == 0 || !report.DryRun {
		t.Fatalf("report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(w.Root, MigrationFile)); !os.IsNotExist(err) {
		t.Fatal("dry run wrote the journal")
	}
}

func TestMigrationJournalSurvivesCorruption(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, MigrationFile), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	j := ReadMigrationJournal(root)
	if len(j.Applied) != 0 {
		t.Fatalf("journal = %+v", j)
	}
	// corrupt journal means migrations re-run rather than being skipped
	w := &W{Root: root, Config: defaultConfig()}
	report, err := w.Migrate(MigrateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Run) == 0 {
		t.Fatal("migrations skipped after a corrupt journal")
	}
	raw, err := os.ReadFile(filepath.Join(root, MigrationFile))
	if err != nil {
		t.Fatal(err)
	}
	var written MigrationJournal
	if json.Unmarshal(raw, &written) != nil || len(written.Applied) == 0 {
		t.Fatalf("journal not rewritten: %s", raw)
	}
}
