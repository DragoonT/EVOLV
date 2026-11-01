package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// shellCommand returns the platform shell binary and args needed to run a
// user-supplied command string.
//
// On Unix the invocation is the classic:
//
//	sh -c "<command>"
//
// On Windows, "sh" is rarely present on clean machines and MSYS/Git-Bash
// mangles drive paths (C:\... -> /c/...). We use the standard Windows shell
// (COMSPEC / cmd.exe /C) which is guaranteed to exist on every Windows install
// and executes CLI tools (go, npm, git, python) and shell builtins natively.
//
// Users can override the shell on any platform by setting EVOLV_SHELL.
func shellCommand(command string) (bin string, args []string) {
	if custom := os.Getenv("EVOLV_SHELL"); custom != "" {
		flag := "-c"
		base := strings.ToLower(filepath.Base(custom))
		if base == "cmd.exe" || base == "cmd" {
			flag = "/C"
		}
		return custom, []string{flag, command}
	}
	if runtime.GOOS == "windows" {
		comspec := os.Getenv("COMSPEC")
		if comspec == "" {
			comspec = "cmd.exe"
		}
		return comspec, []string{"/C", command}
	}
	return "sh", []string{"-c", command}
}
