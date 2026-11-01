package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ---------- shellCommand unit tests ----------

// TestShellCommandPlatform verifies that shellCommand selects a real binary
// and the right flag for the current OS.
func TestShellCommandPlatform(t *testing.T) {
	bin, args := shellCommand("echo hi")
	if bin == "" {
		t.Fatal("shellCommand returned an empty binary")
	}
	if len(args) < 2 {
		t.Fatalf("shellCommand args too short: %v", args)
	}
	// The command string must always appear as the last arg.
	if args[len(args)-1] != "echo hi" {
		t.Fatalf("command not passed through: %v", args)
	}
	switch runtime.GOOS {
	case "windows":
		// Must be cmd.exe with /C flag
		flag := args[len(args)-2]
		if flag != "/C" {
			t.Fatalf("unexpected flag on windows: %q (bin=%s)", flag, bin)
		}
		if !strings.Contains(strings.ToLower(bin), "cmd.exe") {
			t.Fatalf("expected cmd.exe on windows, got %q", bin)
		}
	default:
		if !strings.HasSuffix(bin, "sh") {
			t.Fatalf("expected sh on %s, got %q", runtime.GOOS, bin)
		}
		if args[0] != "-c" {
			t.Fatalf("expected -c flag, got %v", args)
		}
	}
}

// TestShellCustomOverride: EVOLV_SHELL overrides default shell.
func TestShellCustomOverride(t *testing.T) {
	t.Setenv("EVOLV_SHELL", "/bin/custom_sh")
	bin, args := shellCommand("echo hi")
	if bin != "/bin/custom_sh" || len(args) != 2 || args[0] != "-c" || args[1] != "echo hi" {
		t.Fatalf("custom shell not honored: bin=%s args=%v", bin, args)
	}
}

// ---------- shell action integration tests ----------

// TestShellEmptyCommand: blank commands are rejected before any exec.
func TestShellEmptyCommand(t *testing.T) {
	a := testAgent(t)
	res := execOK(t, a, action{Action: "shell", Command: "   "})
	if res["ok"] != false {
		t.Fatalf("empty command should be rejected: %v", res)
	}
	if !strings.Contains(res["error"].(string), "needs command") {
		t.Fatalf("wrong error: %v", res)
	}
}

// TestShellCwdExplicit: an explicit cwd is honoured.
func TestShellCwdExplicit(t *testing.T) {
	a := testAgent(t)
	dir := t.TempDir()

	// Create a small marker file so we can verify the cwd.
	marker := filepath.Join(dir, "marker.txt")
	os.WriteFile(marker, []byte("found"), 0o644)

	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "type marker.txt"
	} else {
		cmd = "cat marker.txt"
	}
	res := execOK(t, a, action{Action: "shell", Command: cmd, Cwd: dir})
	if res["ok"] != true {
		t.Fatalf("shell with explicit cwd failed: %v", res)
	}
	if !strings.Contains(res["output"].(string), "found") {
		t.Fatalf("unexpected output: %v", res["output"])
	}
}

// TestShellOutputTruncation: output beyond 8000 bytes is truncated.
func TestShellOutputTruncation(t *testing.T) {
	a := testAgent(t)

	// Create a file with >8000 bytes to output
	f := filepath.Join(t.TempDir(), "large.txt")
	if err := os.WriteFile(f, []byte(strings.Repeat("A", 9000)), 0o644); err != nil {
		t.Fatal(err)
	}

	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "type " + f
	} else {
		cmd = "cat " + f
	}
	res := execOK(t, a, action{Action: "shell", Command: cmd})
	if res["ok"] != true {
		t.Fatalf("shell failed: %v", res)
	}
	out, ok := res["output"].(string)
	if !ok || len(out) <= 8000 {
		t.Fatalf("output too short to test truncation: %q", out)
	}
	if !strings.Contains(out, "truncated") {
		t.Fatalf("truncation marker missing: %s", out)
	}
}
