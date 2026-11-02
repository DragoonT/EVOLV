package candidate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dxn111/EVOLV/internal/workspace"
)

// newPromotableCandidate builds a fake candidate directory with a manifest, a
// source tree and an artifact so promotion can be exercised without Go builds.
func newPromotableCandidate(t *testing.T, status string, checks []Check) (*Manager, string, string) {
	t.Helper()
	base := t.TempDir()
	sourceRoot := filepath.Join(base, "source-root")
	if err := os.MkdirAll(sourceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	// A real Go module so post-promotion checks can actually pass.
	if err := os.WriteFile(filepath.Join(sourceRoot, "go.mod"), []byte("module promote.test\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(sourceRoot, "main.go")
	if err := os.WriteFile(live, []byte("package main\n\nfunc main() { println(\"v1\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := &workspace.W{Root: filepath.Join(base, "ws"), Config: &workspace.Config{}}
	m := &Manager{WS: ws, SourceRoot: sourceRoot}

	id := "cand-1"
	candRoot := filepath.Join(ws.Dir("workspace/candidates"), id)
	if err := os.MkdirAll(filepath.Join(candRoot, "source"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(candRoot, "artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	beforeHash := hash([]byte("package main\n\nfunc main() { println(\"v1\") }\n"))
	after := []byte("package main\n\nfunc main() { println(\"v2\") }\n")
	if err := os.WriteFile(filepath.Join(candRoot, "source", "main.go"), after, 0o644); err != nil {
		t.Fatal(err)
	}
	artifact := "artifacts/evolv-candidate"
	if runtime.GOOS == "windows" {
		artifact += ".exe"
	}
	if err := os.WriteFile(filepath.Join(candRoot, filepath.FromSlash(artifact)), []byte("fake-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	man := &Manifest{
		ID: id, Name: "test", SourceRoot: sourceRoot, Status: status, Created: "2026-01-01T00:00:00Z",
		Artifact: artifact,
		Changes:  []Change{{Path: "main.go", BeforeHash: beforeHash, AfterHash: hash(after), Bytes: int64(len(after))}},
		Checks:   checks,
	}
	if err := writeManifest(candRoot, man); err != nil {
		t.Fatal(err)
	}
	return m, id, live
}

func TestPromoteRefusesFailedChecks(t *testing.T) {
	m, id, live := newPromotableCandidate(t, "failed", []Check{{Command: "go test ./...", OK: false}})
	if _, err := m.PromoteWithOptions(context.Background(), id, PromoteOptions{}); err == nil {
		t.Fatal("promoted a candidate with failing checks")
	} else if !strings.Contains(err.Error(), "did not pass") {
		t.Fatalf("err = %v", err)
	}
	raw, _ := os.ReadFile(live)
	if string(raw) != "package main\n\nfunc main() { println(\"v1\") }\n" {
		t.Fatalf("live source changed despite refusal: %q", raw)
	}
}

func TestPromoteAllowUnverifiedStillRefusesBrokenCandidate(t *testing.T) {
	m, id, live := newPromotableCandidate(t, "failed", []Check{{Command: "go test ./...", OK: false}})
	if _, err := m.PromoteWithOptions(context.Background(), id, PromoteOptions{AllowUnverified: true}); err == nil {
		t.Fatal("allow_unverified promoted a broken candidate")
	} else if !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("err = %v", err)
	}
	raw, _ := os.ReadFile(live)
	if string(raw) != "package main\n\nfunc main() { println(\"v1\") }\n" {
		t.Fatalf("live source changed: %q", raw)
	}
}

func TestFailedChecksListsEachFailure(t *testing.T) {
	m := &Manifest{Checks: []Check{{Command: "go test ./...", OK: true}, {Command: "go vet ./...", OK: false}}}
	failed := m.FailedChecks()
	if len(failed) != 1 || failed[0] != "go vet ./..." {
		t.Fatalf("failed = %v", failed)
	}
}

func TestPromoteSwapsBinaryWhenConfigured(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered by the copy path; POSIX rename is what we verify here")
	}
	m, id, _ := newPromotableCandidate(t, "ready", []Check{{Command: "go test ./...", OK: true}})
	binDir := t.TempDir()
	target := filepath.Join(binDir, "evolv")
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := m.PromoteWithOptions(context.Background(), id, PromoteOptions{BinaryDir: binDir})
	if err != nil {
		t.Fatalf("promotion failed: %v", err)
	}
	if !res.Swapped.Replaced {
		t.Fatalf("binary not swapped: %+v", res.Swapped)
	}
	raw, _ := os.ReadFile(target)
	if string(raw) != "fake-binary" {
		t.Fatalf("binary content = %q", raw)
	}
	if res.Swapped.Backup == "" {
		t.Fatal("no backup recorded for rollback")
	}
	if err := RestoreBinary(target, res.Swapped.Backup); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(target)
	if string(raw) != "old-binary" {
		t.Fatalf("rollback failed: %q", raw)
	}
}

func TestRollbackSourceRestoresFiles(t *testing.T) {
	m, id, live := newPromotableCandidate(t, "ready", []Check{{Command: "go test ./...", OK: true}})
	candRoot := filepath.Join(m.WS.Dir("workspace/candidates"), id)
	backupRoot := filepath.Join(candRoot, "promotion-backup", "b1")
	if err := os.MkdirAll(backupRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupRoot, "main.go"), []byte("package main // v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	man := &Manifest{ID: id, Rollback: "promotion-backup/b1", Changes: []Change{{Path: "main.go"}}}
	if err := os.WriteFile(live, []byte("package main // v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.RollbackSource(man, candRoot); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(live)
	if string(raw) != "package main // v1\n" {
		t.Fatalf("rollback content = %q", raw)
	}
}
