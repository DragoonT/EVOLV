package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAtomicWriteReplacesContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "file.txt")
	if err := AtomicWrite(path, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWrite(path, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "two" {
		t.Fatalf("content = %q err=%v", raw, err)
	}
	// no temp files left behind
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("leftover temp files: %v", entries)
	}
}

func TestGuardedWriteStaleDetection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if _, err := GuardedWrite(path, []byte("v1"), ""); err != nil {
		t.Fatal(err)
	}
	hash1 := HashBytes([]byte("v1"))
	if _, err := GuardedWrite(path, []byte("v2"), hash1); err != nil {
		t.Fatalf("fresh write rejected: %v", err)
	}
	// A second writer still holding hash1 must be rejected.
	_, err := GuardedWrite(path, []byte("v3"), hash1)
	if err == nil {
		t.Fatal("stale write accepted")
	}
	if !errors.Is(err, ErrStaleWrite) {
		t.Fatalf("err = %v", err)
	}
	var fe *FileError
	if !errors.As(err, &fe) || fe.Status != 409 {
		t.Fatalf("status = %+v", fe)
	}
	if !strings.Contains(fe.Detail, HashBytes([]byte("v2"))) {
		t.Fatalf("detail should name current hash: %s", fe.Detail)
	}
	// Identical content is a harmless no-op, even without a hash.
	if _, err := GuardedWrite(path, []byte("v2"), ""); err != nil {
		t.Fatalf("identical write rejected: %v", err)
	}
}

func TestGuardedWriteRequiresHashForExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(path, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := GuardedWrite(path, []byte("new"), ""); !errors.Is(err, ErrStaleWrite) {
		t.Fatalf("unhash write accepted: %v", err)
	}
}

func TestCheckWithinRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if err := CheckWithin(root, filepath.Join(root, "ok", "file.txt")); err != nil {
		t.Fatalf("legit path rejected: %v", err)
	}
	if err := CheckWithin(root, filepath.Join(root, "..", "outside.txt")); err == nil {
		t.Fatal("traversal accepted")
	}
}

func TestCheckWithinRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skip("cannot create symlink")
	}
	err := CheckWithin(root, filepath.Join(root, "link", "secret.txt"))
	if err == nil {
		t.Fatal("symlink escape accepted")
	}
	var fe *FileError
	if !errors.As(err, &fe) || !strings.Contains(fe.Detail, "symlink") {
		t.Fatalf("err = %v", err)
	}
}

func TestHashFileMatchesBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "h.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	fromFile, err := HashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if fromFile != HashBytes([]byte("hello")) {
		t.Fatalf("hash mismatch: %s vs %s", fromFile, HashBytes([]byte("hello")))
	}
}
