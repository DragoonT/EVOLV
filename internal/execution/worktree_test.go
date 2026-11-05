package execution

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func setupTestGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed, skipping git worktree test")
	}

	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_TERMINAL_PROMPT=0",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=tester",
			"GIT_AUTHOR_EMAIL=tester@example.com",
			"GIT_COMMITTER_NAME=tester",
			"GIT_COMMITTER_EMAIL=tester@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %s (%v)", strings.Join(args, " "), string(out), err)
		}
	}

	runGit("init")
	runGit("config", "user.name", "tester")
	runGit("config", "user.email", "tester@example.com")
	runGit("config", "commit.gpgsign", "false")

	// Create initial file & commit
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# Test Repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "initial commit")

	return dir
}

func TestWorktreeLifecycle(t *testing.T) {
	repoDir := setupTestGitRepo(t)

	if !IsGitRepo(repoDir) {
		t.Fatal("expected IsGitRepo to return true for initialized repo")
	}

	runID := "test-wt-001"
	wt, err := CreateWorktree(repoDir, runID, WorktreeOptions{})
	if err != nil {
		t.Fatalf("CreateWorktree failed: %v", err)
	}

	if !wt.Active {
		t.Fatalf("expected worktree to be active")
	}
	if !strings.Contains(wt.Branch, runID) {
		t.Fatalf("unexpected branch name: %s", wt.Branch)
	}
	if _, err := os.Stat(wt.Path); os.IsNotExist(err) {
		t.Fatalf("worktree path does not exist: %s", wt.Path)
	}

	// Verify the file from the base repo exists in the worktree
	wtReadme := filepath.Join(wt.Path, "README.md")
	if _, err := os.Stat(wtReadme); err != nil {
		t.Fatalf("README.md should exist in worktree: %v", err)
	}

	// Make an isolated change in the worktree
	featureFile := filepath.Join(wt.Path, "feature.txt")
	if err := os.WriteFile(featureFile, []byte("isolated feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Ensure the base repo does NOT see this file (isolation verified!)
	baseFeature := filepath.Join(repoDir, "feature.txt")
	if _, err := os.Stat(baseFeature); !os.IsNotExist(err) {
		t.Fatal("feature.txt leaked into main repository before merge!")
	}

	// Check Diff
	diff, err := DiffWorktree(wt)
	if err != nil {
		t.Fatalf("DiffWorktree error: %v", err)
	}
	if !strings.Contains(diff, "feature.txt") {
		t.Fatalf("diff should mention feature.txt, got: %q", diff)
	}

	// Commit worktree
	commitHash, err := CommitWorktree(wt, "feat: implement isolated feature")
	if err != nil {
		t.Fatalf("CommitWorktree error: %v", err)
	}
	if commitHash == "" || wt.Commit != commitHash {
		t.Fatalf("expected commit hash, got wt.Commit=%q", wt.Commit)
	}

	// Merge into main repo
	if err := MergeWorktree(repoDir, wt); err != nil {
		t.Fatalf("MergeWorktree error: %v", err)
	}
	if !wt.Merged {
		t.Fatal("expected wt.Merged to be true")
	}

	// Verify main repo now has feature.txt
	if _, err := os.Stat(baseFeature); err != nil {
		t.Fatalf("feature.txt should now exist in main repository after merge: %v", err)
	}

	// Remove worktree
	if err := RemoveWorktree(repoDir, wt, true); err != nil {
		t.Fatalf("RemoveWorktree error: %v", err)
	}
	if wt.Active {
		t.Fatal("expected wt.Active to be false after removal")
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree path %s should have been deleted", wt.Path)
	}
}

func TestStoreWorktreeJournalAndSnapshot(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.CloseAll()

	runID := "run-wt-journal"
	run, err := store.CreateRun(runID, "Worktree test", "verify worktree persistence", []Task{
		NewTask("t1", "test step"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Worktree != nil {
		t.Fatal("initial run should not have worktree")
	}

	wt := Worktree{
		Path:    filepath.Join(dir, "worktree"),
		Branch:  "evolv/run-wt-journal",
		BaseRef: "main",
		Commit:  "abcdef123456",
		Active:  true,
	}

	if err := store.AttachWorktree(runID, wt); err != nil {
		t.Fatalf("AttachWorktree error: %v", err)
	}

	// Check store in-memory
	got, ok := store.Get(runID)
	if !ok || got.Worktree == nil || got.Worktree.Branch != wt.Branch {
		t.Fatalf("worktree not reflected in store.Get: %+v", got.Worktree)
	}

	// Update worktree (e.g. after commit)
	wt.Commit = "999888777"
	if err := store.UpdateWorktree(runID, wt); err != nil {
		t.Fatalf("UpdateWorktree error: %v", err)
	}

	got, _ = store.Get(runID)
	if got.Worktree.Commit != "999888777" {
		t.Fatalf("expected updated commit, got %s", got.Worktree.Commit)
	}

	// Reopen store from disk and assert replay
	store.CloseAll()
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.CloseAll()

	reloaded, ok := reopened.Get(runID)
	if !ok || reloaded.Worktree == nil {
		t.Fatalf("worktree lost after reopen: %+v", reloaded)
	}
	if reloaded.Worktree.Commit != "999888777" || !reloaded.Worktree.Active {
		t.Fatalf("reloaded worktree state mismatch: %+v", reloaded.Worktree)
	}

	// Detach worktree
	if err := reopened.DetachWorktree(runID); err != nil {
		t.Fatalf("DetachWorktree error: %v", err)
	}
	detached, _ := reopened.Get(runID)
	if detached.Worktree.Active || !detached.Worktree.Cleaned {
		t.Fatalf("expected detached/cleaned worktree, got: %+v", detached.Worktree)
	}
}

func TestWorktreesBaseDir(t *testing.T) {
	repoDir := setupTestGitRepo(t)

	base, err := WorktreesBaseDir(repoDir)
	if err != nil {
		t.Fatalf("WorktreesBaseDir error: %v", err)
	}
	if base != filepath.Join(repoDir, ".evolv", "worktrees") {
		t.Fatalf("unexpected base dir %q, want %q", base, filepath.Join(repoDir, ".evolv", "worktrees"))
	}

	if _, err := WorktreesBaseDir(filepath.Join(t.TempDir(), "not-a-repo")); err == nil {
		t.Fatal("expected error for directory outside a git repository")
	}
}

func TestGCWorktrees(t *testing.T) {
	// Missing base dir is not an error: nothing to collect.
	missing := filepath.Join(t.TempDir(), "worktrees")
	removed, err := GCWorktrees(missing, time.Hour)
	if err != nil {
		t.Fatalf("GCWorktrees on missing base dir: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("expected no removals, got %v", removed)
	}

	if _, err := GCWorktrees("", time.Hour); err == nil {
		t.Fatal("expected error for empty base dir")
	}

	base := t.TempDir()
	if _, err := GCWorktrees(base, -time.Minute); err == nil {
		t.Fatal("expected error for negative olderThan")
	}

	// Fake stale and fresh worktree dirs via os.Chtimes; a stale plain file
	// must be left untouched.
	now := time.Now()
	oldTime := now.Add(-2 * time.Hour)
	oldDir := filepath.Join(base, "run-old")
	freshDir := filepath.Join(base, "run-fresh")
	strayFile := filepath.Join(base, "notes.txt")
	for _, dir := range []string{oldDir, freshDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(oldDir, "nested.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(strayFile, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(oldDir, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(freshDir, now, now); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(strayFile, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	removed, err = GCWorktrees(base, time.Hour)
	if err != nil {
		t.Fatalf("GCWorktrees error: %v", err)
	}
	if len(removed) != 1 || removed[0] != oldDir {
		t.Fatalf("expected removals [%s], got %v", oldDir, removed)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("stale dir %s should have been removed", oldDir)
	}
	if _, err := os.Stat(freshDir); err != nil {
		t.Fatalf("fresh dir %s must survive GC: %v", freshDir, err)
	}
	if _, err := os.Stat(strayFile); err != nil {
		t.Fatalf("plain file %s must survive GC: %v", strayFile, err)
	}
}

func TestGCWorktreesInRealRepoBase(t *testing.T) {
	repoDir := setupTestGitRepo(t)

	base, err := WorktreesBaseDir(repoDir)
	if err != nil {
		t.Fatalf("WorktreesBaseDir error: %v", err)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}

	stale := filepath.Join(base, "run-stale")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	removed, err := GCWorktrees(base, time.Hour)
	if err != nil {
		t.Fatalf("GCWorktrees error: %v", err)
	}
	if len(removed) != 1 || removed[0] != stale {
		t.Fatalf("expected removals [%s], got %v", stale, removed)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale dir %s should have been removed", stale)
	}
}

func TestCreateWorktreeErrorNamesPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-repo")
	_, err := CreateWorktree(missing, "run-x", WorktreeOptions{})
	if err == nil {
		t.Fatal("expected error creating worktree outside a git repository")
	}
	if !strings.Contains(err.Error(), "no-such-repo") {
		t.Fatalf("error should name the concrete path, got: %v", err)
	}
}

func TestWorktreePoolReuseAcrossRuns(t *testing.T) {
	repoDir := setupTestGitRepo(t)

	pool := NewWorktreePool(WorktreePoolConfig{RepoDir: repoDir, MaxIdle: 2})
	defer pool.Close()

	wt1, err := pool.Acquire("run-a")
	if err != nil {
		t.Fatalf("Acquire run-a: %v", err)
	}
	if !wt1.Active {
		t.Fatal("expected acquired worktree to be active")
	}
	if !strings.Contains(wt1.Branch, "run-a") {
		t.Fatalf("unexpected branch %q", wt1.Branch)
	}
	if !strings.Contains(wt1.Path, filepath.Join(".evolv", "worktrees", "pool-")) {
		t.Fatalf("pooled worktree path %q should live under <repo>/.evolv/worktrees/pool-*", wt1.Path)
	}

	feature := filepath.Join(wt1.Path, "feature.txt")
	if err := os.WriteFile(feature, []byte("run-a feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(wt1.Path, "scratch.txt") // untracked, never committed
	if err := os.WriteFile(scratch, []byte("throwaway\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CommitWorktree(wt1, "feat: run-a feature"); err != nil {
		t.Fatalf("CommitWorktree: %v", err)
	}

	if err := pool.Release(wt1, false); err != nil {
		t.Fatalf("Release keep: %v", err)
	}
	if wt1.Active {
		t.Fatal("expected released worktree to be inactive")
	}
	if _, err := os.Stat(wt1.Path); err != nil {
		t.Fatalf("released worktree dir %s should stay on disk for reuse: %v", wt1.Path, err)
	}

	wt2, err := pool.Acquire("run-b")
	if err != nil {
		t.Fatalf("Acquire run-b: %v", err)
	}
	if wt2.Path != wt1.Path {
		t.Fatalf("expected pooled reuse of %s, got fresh %s", wt1.Path, wt2.Path)
	}
	if !strings.Contains(wt2.Branch, "run-b") || wt2.Branch == wt1.Branch {
		t.Fatalf("unexpected branch %q (previous %q)", wt2.Branch, wt1.Branch)
	}
	if !wt2.Active {
		t.Fatal("expected retargeted worktree to be active")
	}
	// run-a's committed file and the untracked scratch file must not leak
	// into run-b's worktree.
	if _, err := os.Stat(feature); !os.IsNotExist(err) {
		t.Fatalf("committed file from run-a leaked into reused worktree: %v", err)
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatalf("untracked file from run-a leaked into reused worktree: %v", err)
	}
}

func TestWorktreePoolBoundedIdle(t *testing.T) {
	repoDir := setupTestGitRepo(t)

	pool := NewWorktreePool(WorktreePoolConfig{RepoDir: repoDir, MaxIdle: 1})
	defer pool.Close()

	a, err := pool.Acquire("r1")
	if err != nil {
		t.Fatalf("Acquire r1: %v", err)
	}
	b, err := pool.Acquire("r2")
	if err != nil {
		t.Fatalf("Acquire r2: %v", err)
	}
	if a.Path == b.Path {
		t.Fatal("concurrent acquires must get distinct worktrees")
	}

	if err := pool.Release(a, false); err != nil {
		t.Fatalf("Release a keep: %v", err)
	}
	if err := pool.Release(b, false); err != nil {
		t.Fatalf("Release b keep: %v", err)
	}
	// MaxIdle=1: b exceeded the bound and must have been discarded, a kept.
	if _, err := os.Stat(b.Path); !os.IsNotExist(err) {
		t.Fatalf("beyond-bound release of %s should discard the worktree", b.Path)
	}
	if _, err := os.Stat(a.Path); err != nil {
		t.Fatalf("within-bound release of %s should keep the worktree: %v", a.Path, err)
	}

	// Close drains the remaining idle worktree.
	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(a.Path); !os.IsNotExist(err) {
		t.Fatalf("Close should discard idle worktree %s", a.Path)
	}
}

func TestWorktreePoolReleaseDiscard(t *testing.T) {
	repoDir := setupTestGitRepo(t)

	pool := NewWorktreePool(WorktreePoolConfig{RepoDir: repoDir})
	defer pool.Close()

	a, err := pool.Acquire("r1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := os.WriteFile(filepath.Join(a.Path, "temp.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := pool.Release(a, true); err != nil {
		t.Fatalf("Release discard: %v", err)
	}
	if a.Active || !a.Cleaned {
		t.Fatalf("expected discarded worktree to be inactive+cleaned, got %+v", a)
	}
	if _, err := os.Stat(a.Path); !os.IsNotExist(err) {
		t.Fatalf("discarded worktree %s should be gone", a.Path)
	}
}

func TestWorktreePoolClose(t *testing.T) {
	repoDir := setupTestGitRepo(t)

	pool := NewWorktreePool(WorktreePoolConfig{RepoDir: repoDir})
	a, err := pool.Acquire("r1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := pool.Release(a, false); err != nil {
		t.Fatalf("Release keep: %v", err)
	}

	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(a.Path); !os.IsNotExist(err) {
		t.Fatalf("Close should discard idle worktree %s", a.Path)
	}

	// Close is idempotent.
	if err := pool.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	// Acquire after Close must fail.
	if _, err := pool.Acquire("r2"); err == nil {
		t.Fatal("expected error acquiring from a closed pool")
	}

	// Releasing after Close discards.
	if err := pool.Release(a, false); err != nil {
		t.Fatalf("Release after Close: %v", err)
	}
	if _, err := os.Stat(a.Path); !os.IsNotExist(err) {
		t.Fatalf("release after Close must not resurrect %s", a.Path)
	}
}

func TestWorktreePoolRecoversFromMissingSlot(t *testing.T) {
	repoDir := setupTestGitRepo(t)

	pool := NewWorktreePool(WorktreePoolConfig{RepoDir: repoDir})
	defer pool.Close()

	a, err := pool.Acquire("r1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := pool.Release(a, false); err != nil {
		t.Fatalf("Release keep: %v", err)
	}
	// Simulate external GC removing the idle slot on disk.
	if err := os.RemoveAll(a.Path); err != nil {
		t.Fatal(err)
	}

	b, err := pool.Acquire("r2")
	if err != nil {
		t.Fatalf("Acquire after external removal: %v", err)
	}
	if b.Path == a.Path {
		t.Fatalf("stale slot should not be reused, got %s", b.Path)
	}
	if _, err := os.Stat(b.Path); err != nil {
		t.Fatalf("fresh worktree %s should exist: %v", b.Path, err)
	}
	if !IsGitRepo(b.Path) {
		t.Fatalf("fresh worktree %s is not a working tree", b.Path)
	}
}

func TestWorktreePoolDoubleAcquireAndRelease(t *testing.T) {
	repoDir := setupTestGitRepo(t)

	pool := NewWorktreePool(WorktreePoolConfig{RepoDir: repoDir})
	defer pool.Close()

	a, err := pool.Acquire("dup")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, err := pool.Acquire("dup"); err == nil {
		t.Fatal("expected error acquiring twice for the same run")
	}

	if err := pool.Release(a, false); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := pool.Release(a, false); err == nil {
		t.Fatal("expected error releasing the same worktree twice")
	}

	if err := pool.Release(nil, false); err == nil {
		t.Fatal("expected error releasing nil worktree")
	}
}

func TestWorktreePoolRejectsBadRepo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-repo")
	pool := NewWorktreePool(WorktreePoolConfig{RepoDir: missing})
	if _, err := pool.Acquire("r1"); err == nil {
		t.Fatal("expected error acquiring worktree outside a git repository")
	} else if !strings.Contains(err.Error(), "no-such-repo") {
		t.Fatalf("error should name the concrete path, got: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("Close on never-used pool: %v", err)
	}
}
