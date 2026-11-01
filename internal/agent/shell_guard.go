package agent

import (
	"fmt"
	"regexp"
	"strings"
)

// GuardDecision holds the security outcome of inspecting a shell command.
type GuardDecision struct {
	Blocked         bool   `json:"blocked"`
	Reason          string `json:"reason,omitempty"`
	RequiresConfirm bool   `json:"requires_confirm,omitempty"`
}

type dangerousPattern struct {
	re     *regexp.Regexp
	reason string
}

var dangerousPatterns = []dangerousPattern{
	// Recursive root deletion on Unix
	{
		re:     regexp.MustCompile(`(?i)\brm\s+-[a-z]*r[a-z]*f[a-z]*\s+(?:/|/\*|~|~\*|\$HOME)(?:\s|;|\||&|$)`),
		reason: "recursive root or home directory deletion",
	},
	{
		re:     regexp.MustCompile(`(?i)\brm\s+-[a-z]*f[a-z]*r[a-z]*\s+(?:/|/\*|~|~\*|\$HOME)(?:\s|;|\||&|$)`),
		reason: "recursive root or home directory deletion",
	},
	{
		re:     regexp.MustCompile(`(?i)\brm\s+-[a-z]*r[a-z]*f[a-z]*\s+/(?:bin|boot|dev|etc|lib|proc|root|sys|usr|var)(?:/|\s|;|\||&|$)`),
		reason: "deletion of system root directory",
	},
	// Windows recursive drive/system deletion
	{
		re:     regexp.MustCompile(`(?i)\b(?:rd|rmdir)(?:\s+/[a-z]+)*\s+/s(?:\s+/[a-z]+)*\s+(?:[a-zA-Z]:[\\/]?|[\\/])(?:\s|;|\||&|$)`),
		reason: "recursive drive root deletion",
	},
	{
		re:     regexp.MustCompile(`(?i)\bdel(?:\s+/[a-z]+)*\s+/s(?:\s+/[a-z]+)*\s+(?:[a-zA-Z]:[\\/]?|[a-zA-Z]:[\\/]windows|[\\/]windows)(?:[\\/].*)?(?:\s|;|\||&|$)`),
		reason: "recursive drive root or windows system directory deletion",
	},
	// Disk formatting and raw block device overwriting
	{
		re:     regexp.MustCompile(`(?i)\bformat\s+[a-zA-Z]:`),
		reason: "drive volume formatting",
	},
	{
		re:     regexp.MustCompile(`(?i)\bmkfs(?:\.[a-z0-9]+)?\s+/dev/`),
		reason: "filesystem creation on raw disk device",
	},
	{
		re:     regexp.MustCompile(`(?i)\bdd\s+.*of=/dev/(?:sd[a-z]|nvme[0-9]|hd[a-z]|vd[a-z])`),
		reason: "raw block device overwrite via dd",
	},
	// Fork bombs
	{
		re:     regexp.MustCompile(`:\(\)\s*\{\s*:\|:&\s*\};:`),
		reason: "fork bomb denial of service",
	},
	// System power / reboot
	{
		re:     regexp.MustCompile(`(?i)\b(?:shutdown|reboot|poweroff|halt)\b(?:\s+-[a-z0-9]+|\s+(?:now|0)|\s*$)`),
		reason: "system shutdown or reboot command",
	},
	{
		re:     regexp.MustCompile(`(?i)\binit\s+0\b`),
		reason: "system runlevel shutdown",
	},
}

// CheckShellCommand inspects cmd and determines whether it represents a destructive operation.
// If a dangerous verb is detected and confirmed is false, execution is blocked and requires confirmation.
func CheckShellCommand(cmd string, cwd string, confirmed bool) GuardDecision {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return GuardDecision{Blocked: false}
	}

	for _, p := range dangerousPatterns {
		if p.re.MatchString(trimmed) {
			if confirmed {
				return GuardDecision{Blocked: false}
			}
			return GuardDecision{
				Blocked:         true,
				Reason:          fmt.Sprintf("blocked destructive command (%s): pass confirm:true to execute if intended", p.reason),
				RequiresConfirm: true,
			}
		}
	}

	return GuardDecision{Blocked: false}
}
