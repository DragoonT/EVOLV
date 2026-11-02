package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrStaleWrite is returned when a write would clobber content that changed
// since it was read.
var ErrStaleWrite = errors.New("stale write")

// FileError describes a rejected or failed filesystem operation in a way the
// HTTP layer can map to a status code without string matching.
type FileError struct {
	Op     string
	Path   string
	Status int // HTTP status suggestion
	Detail string
	Err    error
}

func (e *FileError) Error() string {
	msg := e.Op + " " + e.Path
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *FileError) Unwrap() error { return e.Err }

// HashFile returns sha256:<hex> of a file's bytes.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// HashBytes hashes in-memory content with the same scheme as HashFile.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// AtomicWrite writes data to path via a temp file in the same directory and a
// rename, so a crash never leaves a half-written file behind.
func AtomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// CheckWithin reports whether target stays inside root after resolving
// symlinks in the existing part of the path. A symlink pointing outside the
// root is an escape even when the textual path looks fine.
func CheckWithin(root, target string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return &FileError{Op: "resolve", Path: target, Status: 400, Detail: "path escapes the workspace root"}
	}
	// Walk the existing prefix and resolve it; a symlinked directory inside
	// the root that points outside would otherwise slip through.
	current := absRoot
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		resolvedRoot = absRoot
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			break // the rest does not exist yet: nothing more to resolve
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		resolved, err := filepath.EvalSymlinks(current)
		if err != nil {
			return &FileError{Op: "resolve", Path: current, Status: 400, Detail: "broken symlink", Err: err}
		}
		if rel, err := filepath.Rel(resolvedRoot, resolved); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return &FileError{Op: "resolve", Path: current, Status: 400, Detail: "symlink escapes the workspace root"}
		}
	}
	return nil
}

// GuardedWrite writes data only when the on-disk hash matches expected, and
// always atomically. An empty expected hash on an existing file is a stale
// write: the caller must read first.
func GuardedWrite(path string, data []byte, expected string) (string, error) {
	current, err := os.ReadFile(path)
	switch {
	case err == nil:
		actual := HashBytes(current)
		if string(current) == string(data) {
			return actual, nil // identical content: nothing to guard
		}
		if expected == "" {
			return actual, &FileError{Op: "write", Path: path, Status: 409, Detail: "stale write: read the file first and pass its hash " + actual, Err: ErrStaleWrite}
		}
		if expected != actual {
			return actual, &FileError{Op: "write", Path: path, Status: 409, Detail: fmt.Sprintf("stale write: expected %s, current is %s", expected, actual), Err: ErrStaleWrite}
		}
	case !os.IsNotExist(err):
		return "", &FileError{Op: "write", Path: path, Status: 500, Err: err}
	}
	if err := AtomicWrite(path, data, 0o644); err != nil {
		return "", &FileError{Op: "write", Path: path, Status: 500, Err: err}
	}
	return HashBytes(data), nil
}
