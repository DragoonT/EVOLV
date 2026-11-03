package candidate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// PromoteOptions controls a promotion that also installs the new binary.
type PromoteOptions struct {
	// BinaryDir is where the running evolv binary lives. When empty, the
	// source-only promotion is performed (current behaviour).
	BinaryDir string
	// HealthGate boots the candidate on an isolated home and requires
	// /api/health before swapping the binary in.
	HealthGate bool
	// AllowUnverified permits promoting a candidate whose checks did not all
	// pass. It is refused by default: an unverified kernel is not shippable.
	AllowUnverified bool
}

// PromotionResult describes everything a promotion did, so the ledger can
// record exactly what changed and how to undo it.
type PromotionResult struct {
	Manifest   *Manifest  `json:"manifest"`
	Swapped    SwapResult `json:"binary,omitempty"`
	Rollback   string     `json:"rollback,omitempty"`
	Unverified []string   `json:"unverified,omitempty"`
}

// FailedChecks lists check commands that did not pass.
func (m *Manifest) FailedChecks() []string {
	var out []string
	for _, c := range m.Checks {
		if !c.OK {
			out = append(out, c.Command)
		}
	}
	return out
}

// PromoteWithOptions promotes source, and optionally swaps in the candidate's
// binary after a real boot health check.
//
// Order matters. The binary swap happens *last*: if the source promotion fails
// and rolls back, the running binary was never touched. If the swap itself
// fails, the source promotion is rolled back through the manifest's backup and
// the previous binary is restored.
func (m *Manager) PromoteWithOptions(ctx context.Context, id string, opts PromoteOptions) (*PromotionResult, error) {
	man, root, err := m.load(id)
	if err != nil {
		return nil, err
	}
	if failed := man.FailedChecks(); len(failed) > 0 {
		if !opts.AllowUnverified {
			return nil, fmt.Errorf("candidate %s did not pass %s — fix it and re-stage instead of promoting", id, strings.Join(failed, ", "))
		}
		if man.Status == "failed" {
			// A candidate whose staging checks failed has never been verified in
			// an installable state, so the normal promotion path (which requires
			// "ready" and re-tests afterwards) is the wrong door.
			return nil, fmt.Errorf("candidate %s is failed, not ready; allow_unverified covers partial verification of a ready candidate, not a broken one", id)
		}
	}
	res := &PromotionResult{}
	if opts.AllowUnverified {
		for _, c := range man.Checks {
			if !c.OK {
				res.Unverified = append(res.Unverified, c.Command)
			}
		}
	}
	promoted, err := m.Promote(id)
	if err != nil {
		return res, err
	}
	res.Manifest = promoted
	res.Rollback = promoted.Rollback
	if promoted.Status != "promoted" {
		return res, fmt.Errorf("candidate %s ended in state %s", id, promoted.Status)
	}

	if opts.BinaryDir == "" || promoted.Artifact == "" {
		return res, nil
	}
	binary := filepath.Join(root, filepath.FromSlash(promoted.Artifact))
	if _, statErr := os.Stat(binary); statErr != nil {
		return res, fmt.Errorf("candidate binary missing at %s", binary)
	}

	target := filepath.Join(opts.BinaryDir, "evolv")
	if runtime.GOOS == "windows" {
		target += ".exe"
	}

	if opts.HealthGate {
		home := filepath.Join(root, "swap-health")
		_ = os.RemoveAll(home)
		if gateErr := VerifyCandidateBinary(ctx, binary, home); gateErr != nil {
			return res, gateErr
		}
	}

	swap, err := PromoteBinary(target, binary)
	res.Swapped = swap
	if err != nil {
		// Undo the source promotion too: never leave a half-applied upgrade.
		if rbErr := m.RollbackSource(promoted, root); rbErr != nil {
			return res, fmt.Errorf("%v (source rollback also failed: %v)", err, rbErr)
		}
		return res, err
	}
	return res, nil
}

// RollbackSource restores the live source tree from a promotion backup.
func (m *Manager) RollbackSource(man *Manifest, candidateRoot string) error {
	if man.Rollback == "" {
		return errors.New("no promotion backup recorded")
	}
	backupRoot := filepath.Join(candidateRoot, filepath.FromSlash(man.Rollback))
	if _, err := os.Stat(backupRoot); err != nil {
		return fmt.Errorf("promotion backup missing: %w", err)
	}
	for _, change := range man.Changes {
		backup := filepath.Join(backupRoot, filepath.FromSlash(change.Path))
		live := filepath.Join(m.SourceRoot, filepath.FromSlash(change.Path))
		raw, err := os.ReadFile(backup)
		if os.IsNotExist(err) {
			// the file did not exist before promotion
			_ = os.Remove(live)
			continue
		}
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(live, raw, 0o644); err != nil {
			return err
		}
	}
	return nil
}
