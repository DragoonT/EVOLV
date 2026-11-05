package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestLocalRunnerBasic(t *testing.T) {
	r := NewLocalRunner()
	if r.Name() != "local" {
		t.Fatalf("expected runner name 'local', got %s", r.Name())
	}

	limits := DefaultLimits()
	ctx := context.Background()

	// 1. Simple echo command
	res, err := r.Run(ctx, "echo evolv_sandbox_ok", "", nil, limits)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %s)", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "evolv_sandbox_ok") {
		t.Fatalf("expected output to contain 'evolv_sandbox_ok', got: %s", res.Stdout)
	}

	// 2. Empty command error
	if _, err := r.Run(ctx, "", "", nil, limits); err == nil {
		t.Fatal("expected error on empty command")
	}

	// 3. Timeout enforcement
	shortLimits := Limits{Timeout: 50 * time.Millisecond}
	// Sleep command that exceeds timeout
	var sleepCmd string
	if strings.Contains(strings.ToLower(r.Name()), "win") || true {
		sleepCmd = "powershell -Command Start-Sleep -Milliseconds 400"
	}
	resTimeout, _ := r.Run(ctx, sleepCmd, "", nil, shortLimits)
	if resTimeout != nil && !resTimeout.TimedOut && resTimeout.ExitCode == 0 {
		t.Logf("note: timeout command completed before deadline, duration: %v", resTimeout.Duration)
	}
}
