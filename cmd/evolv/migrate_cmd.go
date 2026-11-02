package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/dxn111/EVOLV/internal/workspace"
)

// runMigrate applies (or previews) workspace migrations without booting the
// server. It is the safe way to inspect an upgrade before committing to it.
func runMigrate(args []string) {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "show pending migrations without applying them")
	jsonOut := fs.Bool("json", false, "print a machine-readable report")
	fs.SetOutput(os.Stdout)
	fs.Usage = func() {
		fmt.Print("\n  evolv migrate [--dry-run] [--json]\n\n")
		fmt.Println("  upgrades the workspace at ~/.evolv to this binary's schema")
		fmt.Println("  without starting the server. Safe to run twice.")
		fmt.Println()
	}
	if err := fs.Parse(args); err != nil {
		return
	}
	w, err := workspace.Load(nil)
	if err != nil {
		failWith(*jsonOut, "could not open workspace: %v", err)
	}
	report, mErr := w.Migrate(workspace.MigrateOptions{DryRun: *dry})
	if mErr != nil {
		failWith(*jsonOut, "migration failed: %v", mErr)
	}
	if *jsonOut {
		emitJSON(map[string]any{
			"ok":      true,
			"version": workspace.Version,
			"dry_run": report.DryRun,
			"run":     report.Run,
			"skipped": report.Skipped,
			"warning": report.Warnings,
			"root":    w.Root,
		})
		return
	}
	cardTop("workspace migrations")
	if report.DryRun {
		cardKV("mode", "dry run", yellow)
	} else {
		cardKV("mode", "applied", green)
	}
	cardKV("version", "v"+workspace.Version, dim)
	cardKV("root", w.Root, dim)
	if len(report.Run) == 0 {
		cardKV("pending", "none — workspace already current", green)
	}
	for _, id := range report.Run {
		cardKV("apply", id, violet)
	}
	for _, warn := range report.Warnings {
		cardKV("warning", warn, yellow)
	}
	cardBottom()
	fmt.Println()
}
