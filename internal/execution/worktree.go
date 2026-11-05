package execution

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Worktree records an isolated git worktree attached to an engineering run.
type Worktree struct {
	Path      string `json:"path"`              // absolute path to the worktree directory
	Branch    string `json:"branch"`            // git branch name created for this worktree
	BaseRef   string `json:"base_ref"`          // base commit or branch when created
	Commit    string `json:"commit,omitempty"`  // latest commit hash in the worktree
	Active    bool   `json:"active"`            // true while the worktree is actively attached
	Merged    bool   `json:"merged,omitempty"`  // true if merged into the target branch
	Cleaned   bool   `json:"cleaned,omitempty"` // true if removed from disk
	CreatedAt string `json:"created_at,omitempty"`
}

// WorktreeOptions configures worktree creation.
type WorktreeOptions struct {
	BranchPrefix string // default "evolv/run-"
	BaseRef      string // default "HEAD"
	WorktreeDir  string // optional custom directory; defaults to <repoRoot>/.evolv/worktrees/run-<runID>
}

// gitTimeout is the maximum time allowed for any single git command.
const gitTimeout = 30 * time.Second

// gitExec runs a git command in the specified directory.
func gitExec(ctx context.Context, dir string, args ...string) (string, error) {
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), gitTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"NO_COLOR=1",
		"GIT_CONFIG_NOSYSTEM=1",
	)
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	if err != nil {
		if trimmed != "" {
			return "", fmt.Errorf("git %s: %s (%w)", strings.Join(args, " "), trimmed, err)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return trimmed, nil
}

// IsGitRepo reports whether dir is inside a git working tree.
func IsGitRepo(dir string) bool {
	if dir == "" {
		return false
	}
	out, err := gitExec(nil, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// RepoRoot returns the top-level directory of the git repository enclosing dir.
func RepoRoot(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("directory is empty")
	}
	out, err := gitExec(nil, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("finding git repo root for %q: not a git repository: %w", dir, err)
	}
	return filepath.Clean(out), nil
}

// CurrentRef returns the current branch name, or the commit SHA if in detached HEAD.
func CurrentRef(dir string) (string, error) {
	branch, err := gitExec(nil, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err == nil && branch != "" && branch != "HEAD" {
		return branch, nil
	}
	return gitExec(nil, dir, "rev-parse", "HEAD")
}

// CreateWorktree allocates a new isolated git worktree for the given runID.
func CreateWorktree(repoDir, runID string, opts WorktreeOptions) (*Worktree, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, errors.New("runID is required")
	}
	root, err := RepoRoot(repoDir)
	if err != nil {
		return nil, err
	}

	branchPrefix := opts.BranchPrefix
	if branchPrefix == "" {
		branchPrefix = "evolv/run-"
	}
	branch := branchPrefix + runID

	baseRef := opts.BaseRef
	if baseRef == "" {
		baseRef = "HEAD"
	}
	baseCurrent, _ := CurrentRef(root)
	if baseCurrent != "" {
		baseRef = baseCurrent
	}

	worktreePath := opts.WorktreeDir
	if worktreePath == "" {
		worktreePath = filepath.Join(root, ".evolv", "worktrees", "run-"+runID)
	}
	worktreePath = filepath.Clean(worktreePath)

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return nil, fmt.Errorf("creating worktree parent dir %s: %w", filepath.Dir(worktreePath), err)
	}

	// If the worktree path already exists on disk, prune/remove it cleanly first.
	if _, err := os.Stat(worktreePath); err == nil {
		_, _ = gitExec(nil, root, "worktree", "remove", "--force", worktreePath)
		_ = os.RemoveAll(worktreePath)
		_, _ = gitExec(nil, root, "worktree", "prune")
	}

	// Add worktree on a dedicated branch (-B creates or resets the branch at baseRef).
	_, err = gitExec(nil, root, "worktree", "add", "-B", branch, worktreePath, baseRef)
	if err != nil {
		return nil, fmt.Errorf("creating git worktree at %s on branch %s: %w", worktreePath, branch, err)
	}

	headCommit, _ := gitExec(nil, worktreePath, "rev-parse", "HEAD")

	return &Worktree{
		Path:      worktreePath,
		Branch:    branch,
		BaseRef:   baseRef,
		Commit:    headCommit,
		Active:    true,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// CommitWorktree stages and commits all changes in the worktree.
// If there are no changes, it returns the current commit hash without creating an empty commit.
func CommitWorktree(wt *Worktree, message string) (string, error) {
	if wt == nil || wt.Path == "" {
		return "", errors.New("worktree is nil or has empty path")
	}
	if !wt.Active {
		return "", errors.New("worktree is not active")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = "chore(evolv): update worktree for run"
	}

	// Stage all changes (new, modified, deleted)
	if _, err := gitExec(nil, wt.Path, "add", "-A"); err != nil {
		return "", fmt.Errorf("staging changes in worktree %s: %w", wt.Path, err)
	}

	// Check if working tree is clean
	status, err := gitExec(nil, wt.Path, "status", "--porcelain")
	if err != nil {
		return "", fmt.Errorf("checking status of worktree %s: %w", wt.Path, err)
	}
	if status == "" {
		// Clean, return current HEAD commit
		return gitExec(nil, wt.Path, "rev-parse", "HEAD")
	}

	// Commit
	if _, err := gitExec(nil, wt.Path, "commit", "-m", message); err != nil {
		return "", fmt.Errorf("committing worktree %s: %w", wt.Path, err)
	}

	newCommit, err := gitExec(nil, wt.Path, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	wt.Commit = newCommit
	return newCommit, nil
}

// DiffWorktree reports unstaged and staged changes in the worktree, or diff against base.
func DiffWorktree(wt *Worktree) (string, error) {
	if wt == nil || wt.Path == "" {
		return "", errors.New("worktree is nil or has empty path")
	}
	// Mark untracked files as intent-to-add so newly created files appear as diff additions
	_, _ = gitExec(nil, wt.Path, "add", "-N", ".")
	// Diff against HEAD in the worktree
	diff, err := gitExec(nil, wt.Path, "diff", "HEAD")
	if err == nil && diff != "" {
		return diff, nil
	}
	// If clean against HEAD, check if any untracked or staged changes exist
	status, _ := gitExec(nil, wt.Path, "status", "--short")
	if status != "" {
		return status, nil
	}
	// Check diff against BaseRef if available
	if wt.BaseRef != "" && wt.BaseRef != "HEAD" {
		baseDiff, bErr := gitExec(nil, wt.Path, "diff", wt.BaseRef+"...HEAD")
		if bErr == nil && baseDiff != "" {
			return baseDiff, nil
		}
	}
	return "", nil
}

// MergeWorktree merges the worktree branch into the base repository.
func MergeWorktree(repoDir string, wt *Worktree) error {
	if wt == nil || wt.Branch == "" {
		return errors.New("worktree is nil or missing branch")
	}
	root, err := RepoRoot(repoDir)
	if err != nil {
		return err
	}
	// Commit any uncommitted changes in the worktree first
	_, _ = CommitWorktree(wt, "chore(evolv): final worktree snapshot before merge")

	// Merge the worktree branch into the current branch of the main repo
	_, err = gitExec(nil, root, "merge", "--no-edit", wt.Branch)
	if err != nil {
		return fmt.Errorf("merging worktree branch %s into %s: %w", wt.Branch, root, err)
	}
	wt.Merged = true
	return nil
}

// RemoveWorktree deletes the worktree from disk and prunes git tracking.
func RemoveWorktree(repoDir string, wt *Worktree, deleteBranch bool) error {
	if wt == nil || wt.Path == "" {
		return nil
	}
	root, err := RepoRoot(repoDir)
	if err != nil {
		root = repoDir
	}

	_, _ = gitExec(nil, root, "worktree", "remove", "--force", wt.Path)
	_, _ = gitExec(nil, root, "worktree", "prune")

	// Windows or locked files safety: ensure directory is cleaned
	_ = os.RemoveAll(wt.Path)

	if deleteBranch && wt.Branch != "" {
		_, _ = gitExec(nil, root, "branch", "-D", wt.Branch)
	}

	wt.Active = false
	wt.Cleaned = true
	return nil
}

// syncDir fsyncs a directory entry so that creations and removals beneath it
// survive a crash or power loss. Some platforms and filesystems refuse to
// sync directories; callers doing best-effort cleanup should tolerate the
// error, callers needing durability should propagate it.
func syncDir(dir string) error {
	// Windows NTFS/FAT32 does not support directory fsync (FlushFileBuffers on directory handles returns Access Denied).
	if runtime.GOOS == "windows" {
		return nil
	}
	f, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("opening directory %s for fsync: %w", dir, err)
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("fsyncing directory %s: %w", dir, err)
	}
	return nil
}

// WorktreesBaseDir returns the base directory under which this package
// organizes worktrees for the repository enclosing dir:
// <repoRoot>/.evolv/worktrees. It is the directory to pass to GCWorktrees.
func WorktreesBaseDir(dir string) (string, error) {
	root, err := RepoRoot(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".evolv", "worktrees"), nil
}

// GCWorktrees scans baseDir (see WorktreesBaseDir) for worktree directories
// whose mtime is older than olderThan, removes them from disk, and returns
// the paths it removed (sorted). A missing baseDir is not an error: there is
// simply nothing to collect. After any removal it fsyncs baseDir so the
// deletions are durable, then runs a best-effort `git worktree prune` against
// the enclosing repository so git's worktree metadata stays consistent.
//
// Active-run safety: worktree state is tracked only in the execution Store
// (Run.Worktree.Active); there is no on-disk active marker inside the
// worktree directories themselves, and a directory's mtime does not
// necessarily change while a run is writing to nested paths. Callers must
// therefore run GCWorktrees only when no run holds a worktree — typically at
// startup, before scheduling runs — or filter baseDir entries against the
// live runs in the Store themselves.
func GCWorktrees(baseDir string, olderThan time.Duration) (removed []string, err error) {
	if baseDir == "" {
		return nil, errors.New("gc worktrees: base directory is empty")
	}
	if olderThan < 0 {
		return nil, fmt.Errorf("gc worktrees in %s: negative olderThan (%s)", baseDir, olderThan)
	}
	entries, readErr := os.ReadDir(baseDir)
	if readErr != nil {
		if errors.Is(readErr, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("gc worktrees: scanning %s: %w", baseDir, readErr)
	}

	cutoff := time.Now().Add(-olderThan)
	var errs []error
	anyRemoved := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(baseDir, entry.Name())
		info, statErr := entry.Info()
		if statErr != nil {
			errs = append(errs, fmt.Errorf("gc worktrees: statting %s: %w", path, statErr))
			continue
		}
		if !info.ModTime().Before(cutoff) {
			continue
		}
		if rmErr := os.RemoveAll(path); rmErr != nil {
			errs = append(errs, fmt.Errorf("gc worktrees: removing stale worktree %s: %w", path, rmErr))
			continue
		}
		removed = append(removed, path)
		anyRemoved = true
	}

	if anyRemoved {
		if syncErr := syncDir(baseDir); syncErr != nil {
			errs = append(errs, fmt.Errorf("gc worktrees: %w", syncErr))
		}
		// Best-effort metadata prune in the enclosing repository.
		if root := filepath.Dir(filepath.Dir(baseDir)); IsGitRepo(root) {
			_, _ = gitExec(nil, root, "worktree", "prune")
		}
	}

	sort.Strings(removed)
	return removed, errors.Join(errs...)
}

// WorktreePoolConfig configures a WorktreePool.
type WorktreePoolConfig struct {
	// RepoDir is a path inside the repository whose worktrees are pooled. Required.
	RepoDir string
	// BranchPrefix is used when (re)targeting pooled worktrees; default "evolv/run-".
	BranchPrefix string
	// BaseRef overrides the reuse base for acquired worktrees; when empty the
	// current branch/HEAD of the repository is used at each acquire, mirroring
	// CreateWorktree.
	BaseRef string
	// MaxIdle bounds how many worktrees are kept on disk for reuse between
	// runs; values <= 0 mean the default of 2. Releases beyond the bound are
	// discarded immediately.
	MaxIdle int
}

// WorktreePool is an optional reuse layer over per-run worktrees. Instead of
// creating a fresh worktree and deleting it after every run, a pool keeps a
// bounded set of worktree directories on disk and re-targets them (branch
// reset to the current base plus a full clean) at each Acquire, so the
// per-run checkout cost shrinks to a local reset.
//
// A pooled worktree is only safe to reuse because Acquire resets it to the
// base ref with `reset --hard` + `clean -fdx`: uncommitted or untracked work
// left in a released worktree is destroyed by the next Acquire. Callers must
// CommitWorktree (and MergeWorktree if desired) before releasing when the
// changes matter.
//
// A WorktreePool is safe for concurrent use.
type WorktreePool struct {
	cfg    WorktreePoolConfig
	mu     sync.Mutex
	idle   []*Worktree       // stack of reusable worktrees
	live   map[string]string // worktree path -> runID currently holding it
	closed bool
	next   int // next pool slot index
}

// NewWorktreePool creates a pool for cfg.RepoDir. It does not touch the disk;
// worktrees are allocated lazily by Acquire.
func NewWorktreePool(cfg WorktreePoolConfig) *WorktreePool {
	if cfg.BranchPrefix == "" {
		cfg.BranchPrefix = "evolv/run-"
	}
	if cfg.MaxIdle <= 0 {
		cfg.MaxIdle = 2
	}
	return &WorktreePool{cfg: cfg, live: make(map[string]string)}
}

// poolBaseDir resolves the worktrees base directory for the pool's repository.
func (p *WorktreePool) poolBaseDir() (string, error) {
	base, err := WorktreesBaseDir(p.cfg.RepoDir)
	if err != nil {
		return "", fmt.Errorf("worktree pool: %w", err)
	}
	return base, nil
}

// slotPath returns a unique, not-yet-existing path for a pooled worktree.
func (p *WorktreePool) slotPath(base string) string {
	for {
		p.next++
		path := filepath.Join(base, fmt.Sprintf("pool-%d", p.next))
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return path
		}
	}
}

// Acquire returns a worktree for runID, reusing an idle pooled worktree when
// one is available (reset to the current base with a clean tree) and creating
// a fresh worktree otherwise. The returned Worktree is active; it must be
// returned to the pool with Release.
func (p *WorktreePool) Acquire(runID string) (*Worktree, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, errors.New("worktree pool acquire: runID is required")
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, fmt.Errorf("worktree pool acquire for run %q: pool is closed", runID)
	}
	if p.holdsRunLocked(runID) {
		p.mu.Unlock()
		return nil, fmt.Errorf("worktree pool acquire for run %q: this run already holds a pooled worktree", runID)
	}
	var cand *Worktree
	if n := len(p.idle); n > 0 {
		cand = p.idle[n-1]
		p.idle = p.idle[:n-1]
	}
	p.mu.Unlock()

	if cand != nil {
		wt, err := p.retarget(cand, runID)
		if err == nil {
			p.mu.Lock()
			p.live[wt.Path] = runID
			p.mu.Unlock()
			return wt, nil
		}
		// The stale entry is unusable (deleted underneath, corrupted, ...);
		// discard it and fall back to a fresh worktree.
		_ = p.discard(cand)
	}

	base, err := p.poolBaseDir()
	if err != nil {
		return nil, err
	}
	wt, err := CreateWorktree(p.cfg.RepoDir, runID, WorktreeOptions{
		BranchPrefix: p.cfg.BranchPrefix,
		BaseRef:      p.cfg.BaseRef,
		WorktreeDir:  p.slotPath(base),
	})
	if err != nil {
		return nil, fmt.Errorf("worktree pool acquire for run %q: %w", runID, err)
	}
	p.mu.Lock()
	p.live[wt.Path] = runID
	p.mu.Unlock()
	return wt, nil
}

// holdsRunLocked reports whether a live pooled worktree already belongs to
// runID, guarding against double acquisition by the same run.
func (p *WorktreePool) holdsRunLocked(runID string) bool {
	for _, held := range p.live {
		if held == runID {
			return true
		}
	}
	return false
}

// Release returns a worktree to the pool. With discard=false the directory is
// kept on disk for reuse by a later Acquire (bounded by MaxIdle; releases
// beyond the bound are discarded instead); with discard=true it is removed
// immediately with an fsync-safe cleanup. After Close, every release discards.
// Any uncommitted work in wt is destroyed either way (see WorktreePool).
func (p *WorktreePool) Release(wt *Worktree, discard bool) error {
	if wt == nil || wt.Path == "" {
		return errors.New("worktree pool release: worktree is nil or has empty path")
	}
	p.mu.Lock()
	if _, ok := p.live[wt.Path]; ok {
		delete(p.live, wt.Path)
	} else if p.idleContainsLocked(wt.Path) {
		p.mu.Unlock()
		return fmt.Errorf("worktree pool release: worktree %s was already released", wt.Path)
	}
	if p.closed {
		discard = true
	}
	if !discard && len(p.idle) < p.cfg.MaxIdle {
		wt.Active = false
		p.idle = append(p.idle, wt)
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()
	return p.discard(wt)
}

// Close discards all idle pooled worktrees and closes the pool to further
// acquires. Worktrees still checked out by runs are untouched; releasing them
// after Close discards them. Close is idempotent.
func (p *WorktreePool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	idle := p.idle
	p.idle = nil
	p.mu.Unlock()

	var errs []error
	for _, wt := range idle {
		if err := p.discard(wt); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// idleContainsLocked reports whether an idle worktree with the given path exists.
func (p *WorktreePool) idleContainsLocked(path string) bool {
	for _, wt := range p.idle {
		if wt.Path == path {
			return true
		}
	}
	return false
}

// discard removes a pooled worktree from disk with an fsync-safe cleanup.
func (p *WorktreePool) discard(wt *Worktree) error {
	root, err := RepoRoot(p.cfg.RepoDir)
	if err != nil {
		root = p.cfg.RepoDir
	}
	_, _ = gitExec(nil, root, "worktree", "remove", "--force", wt.Path)
	_, _ = gitExec(nil, root, "worktree", "prune")
	if rmErr := os.RemoveAll(wt.Path); rmErr != nil {
		return fmt.Errorf("worktree pool discard: removing worktree %s: %w", wt.Path, rmErr)
	}
	if syncErr := syncDir(filepath.Dir(wt.Path)); syncErr != nil {
		return fmt.Errorf("worktree pool discard: worktree %s is removed but %w", wt.Path, syncErr)
	}
	wt.Active = false
	wt.Cleaned = true
	return nil
}

// retarget resets an idle pooled worktree for a new run: hard-reset tracked
// state to the current base ref, purge untracked and ignored files, and move
// the branch prefix+runID onto that base. The ref is resolved to a commit
// hash in the main repository first, because refs like HEAD inside the
// worktree would resolve against the worktree's own HEAD.
func (p *WorktreePool) retarget(wt *Worktree, runID string) (*Worktree, error) {
	if _, err := os.Stat(wt.Path); err != nil {
		return nil, fmt.Errorf("worktree pool reuse: checking %s: %w", wt.Path, err)
	}
	root, err := RepoRoot(p.cfg.RepoDir)
	if err != nil {
		return nil, fmt.Errorf("worktree pool reuse: %w", err)
	}
	baseRef := p.cfg.BaseRef
	if baseRef == "" {
		baseRef, err = CurrentRef(root)
		if err != nil {
			return nil, fmt.Errorf("worktree pool reuse: resolving base ref in %s: %w", root, err)
		}
	}
	baseCommit, err := gitExec(nil, root, "rev-parse", "--verify", baseRef+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("worktree pool reuse: resolving base ref %q in %s: %w", baseRef, root, err)
	}

	branch := p.cfg.BranchPrefix + runID
	if _, err := gitExec(nil, wt.Path, "reset", "--hard", baseCommit); err != nil {
		return nil, fmt.Errorf("worktree pool reuse: resetting worktree %s to %s: %w", wt.Path, baseCommit, err)
	}
	if _, err := gitExec(nil, wt.Path, "clean", "-fdx"); err != nil {
		return nil, fmt.Errorf("worktree pool reuse: cleaning worktree %s: %w", wt.Path, err)
	}
	if _, err := gitExec(nil, wt.Path, "checkout", "-B", branch, baseCommit); err != nil {
		return nil, fmt.Errorf("worktree pool reuse: checking out branch %s in worktree %s: %w", branch, wt.Path, err)
	}
	commit, _ := gitExec(nil, wt.Path, "rev-parse", "HEAD")

	retargeted := *wt
	retargeted.Branch = branch
	retargeted.BaseRef = baseCommit
	retargeted.Commit = commit
	retargeted.Active = true
	retargeted.Merged = false
	retargeted.Cleaned = false
	retargeted.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	return &retargeted, nil
}
