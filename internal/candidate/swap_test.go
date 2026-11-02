package candidate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPromoteBinaryInstallsIntoEmptyTarget(t *testing.T) {
	dir := t.TempDir()
	cand := filepath.Join(dir, "cand")
	target := filepath.Join(dir, "sub", "evolv")
	if err := os.WriteFile(cand, []byte("new-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := PromoteBinary(target, cand)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Replaced || res.Hash == "" {
		t.Fatalf("result = %+v", res)
	}
	raw, _ := os.ReadFile(target)
	if string(raw) != "new-binary" {
		t.Fatalf("target content = %q", raw)
	}
}

func TestPromoteBinaryBacksUpAndReplaces(t *testing.T) {
	dir := t.TempDir()
	cand := filepath.Join(dir, "cand")
	target := filepath.Join(dir, "evolv")
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cand, []byte("new-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := PromoteBinary(target, cand)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Replaced || res.Backup == "" {
		t.Fatalf("result = %+v", res)
	}
	raw, _ := os.ReadFile(target)
	if string(raw) != "new-binary" {
		t.Fatalf("target = %q", raw)
	}
	backup, err := os.ReadFile(res.Backup)
	if err != nil || string(backup) != "old-binary" {
		t.Fatalf("backup = %q err=%v", backup, err)
	}
	// restore rolls back
	if err := RestoreBinary(target, res.Backup); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(target)
	if string(raw) != "old-binary" {
		t.Fatalf("restore = %q", raw)
	}
}

func TestPromoteBinaryRejectsMissingAndEmpty(t *testing.T) {
	dir := t.TempDir()
	if _, err := PromoteBinary(filepath.Join(dir, "t"), filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing candidate accepted")
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := PromoteBinary(filepath.Join(dir, "t"), empty); err == nil {
		t.Fatal("empty candidate accepted")
	}
}

func TestCopyFilePreservesMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix modes")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst")
	if err := copyFile(src, dst, 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("mode = %v, want executable", info.Mode())
	}
}

func TestHashFileStable(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := hashFile(p)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := hashFile(p)
	if a != b || a == "" {
		t.Fatalf("hash = %q / %q", a, b)
	}
}
