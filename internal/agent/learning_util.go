package agent

import "time"

func nowISO() string { return time.Now().UTC().Format(time.RFC3339) }

// errText renders an error for health records without leaking nil strings.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
