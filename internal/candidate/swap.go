package candidate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// SwapResult records a binary replacement.
type SwapResult struct {
	Replaced bool   `json:"replaced"`
	Target   string `json:"target"`
	Backup   string `json:"backup,omitempty"`
	Hash     string `json:"hash,omitempty"`
	Note     string `json:"note,omitempty"`
}

// PromoteBinary atomically replaces target with candidate.
//
// Safety contract:
//   - the candidate must exist and be non-empty
//   - the current binary is copied to a timestamped backup first
//   - the swap is a same-directory rename where the platform allows it; on
//     Windows, where a running executable is locked, the backup+move dance is
//     attempted and the failure is reported honestly instead of pretending
//   - if anything fails, the original is restored from the backup
func PromoteBinary(target, candidate string) (SwapResult, error) {
	res := SwapResult{Target: target}
	info, err := os.Stat(candidate)
	if err != nil {
		return res, fmt.Errorf("candidate binary missing: %w", err)
	}
	if info.Size() == 0 {
		return res, fmt.Errorf("candidate binary is empty")
	}
	sum, err := hashFile(candidate)
	if err != nil {
		return res, err
	}
	res.Hash = sum

	if _, err := os.Stat(target); os.IsNotExist(err) {
		// nothing to back up: install directly
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return res, err
		}
		if err := copyFile(candidate, target, 0o755); err != nil {
			return res, err
		}
		res.Replaced = true
		res.Note = "installed into an empty target"
		return res, nil
	}

	backup := fmt.Sprintf("%s.bak-%s", target, time.Now().UTC().Format("20060102-150405"))
	if err := copyFile(target, backup, 0o755); err != nil {
		return res, fmt.Errorf("could not back up the running binary: %w", err)
	}
	res.Backup = backup

	restore := func(cause error) (SwapResult, error) {
		if rerr := copyFile(backup, target, 0o755); rerr != nil {
			return res, fmt.Errorf("swap failed (%v) and the restore also failed (%v) — backup kept at %s", cause, rerr, backup)
		}
		return res, fmt.Errorf("swap rolled back: %w", cause)
	}

	staged := target + ".new-" + time.Now().UTC().Format("150405.000000000")
	if err := copyFile(candidate, staged, 0o755); err != nil {
		return restore(err)
	}
	defer os.Remove(staged)
	if err := os.Rename(staged, target); err != nil {
		if runtime.GOOS == "windows" {
			// The running executable is locked. Fall back to a best-effort
			// move of the old binary out of the way, then a copy.
			old := backup + ".old"
			if merr := os.Rename(target, old); merr != nil {
				return res, fmt.Errorf("cannot replace a running binary on windows (%v); run the new binary from %s", merr, candidate)
			}
			if cerr := copyFile(candidate, target, 0o755); cerr != nil {
				_ = os.Rename(old, target)
				return res, cerr
			}
			res.Note = "windows swap: the previous binary is parked until restart"
			res.Replaced = true
			return res, nil
		}
		return restore(err)
	}
	res.Replaced = true
	return res, nil
}

// RestoreBinary rolls a swap back from its backup.
func RestoreBinary(target, backup string) error {
	if backup == "" {
		return fmt.Errorf("no backup to restore")
	}
	if _, err := os.Stat(backup); err != nil {
		return fmt.Errorf("backup missing: %w", err)
	}
	return copyFile(backup, target, 0o755)
}

// VerifyCandidateBinary boots a candidate and waits for /api/health.
func VerifyCandidateBinary(ctx context.Context, binary, isolatedHome string) (err error) {
	check := bootHealthCheck(ctx, binary, isolatedHome)
	if check.OK {
		return nil
	}
	return fmt.Errorf("candidate failed to boot: %s", check.Output)
}

// hashFile returns sha256:<hex> for a path.
func hashFile(path string) (string, error) {
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

// copyFile copies src to dst atomically through a temp file in dst's dir.
func copyFile(src, dst string, perm os.FileMode) error {
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := io.Copy(tmp, in); err != nil {
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
	return os.Rename(name, dst)
}
