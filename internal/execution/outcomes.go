package execution

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Outcome learning: every finished task leaves a small, structured record of
// what was attempted and how it went. Over time that tells EVOLV which verify
// commands actually catch problems, which kinds of tasks fail on the first
// attempt, and how long work really takes — the evidence an orchestration
// policy needs to improve instead of guessing.
//
// Records are append-only JSONL and never contain file contents or prompts.

// Outcome is one task's ending.
type Outcome struct {
	At         string   `json:"at"`
	Run        string   `json:"run"`
	Task       string   `json:"task"`
	Title      string   `json:"title,omitempty"`
	Files      []string `json:"files,omitempty"`
	Status     Status   `json:"status"`
	Attempts   int      `json:"attempts"`
	DurationMS int64    `json:"duration_ms,omitempty"`
	Verify     string   `json:"verify,omitempty"`
	VerifyOK   bool     `json:"verify_ok"`
	Verified   bool     `json:"verified"`
	HadDeps    int      `json:"had_deps,omitempty"`
	BlockedBy  string   `json:"blocked_by,omitempty"`
	Error      string   `json:"error,omitempty"`
}

// OutcomeLog appends and reads outcomes.
type OutcomeLog struct{ path string }

// OpenOutcomes opens (creating) the outcome log under root.
func OpenOutcomes(root string) (*OutcomeLog, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &OutcomeLog{path: filepath.Join(root, "outcomes.jsonl")}, nil
}

// Append writes one outcome. It is safe to call for every terminal task.
func (l *OutcomeLog) Append(o Outcome) error {
	if o.At == "" {
		o.At = time.Now().UTC().Format(time.RFC3339Nano)
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

// Read parses the log, skipping torn or corrupt trailing lines.
func (l *OutcomeLog) Read() []Outcome {
	raw, err := os.ReadFile(l.path)
	if err != nil {
		return nil
	}
	var out []Outcome
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var o Outcome
		if json.Unmarshal([]byte(line), &o) == nil && o.Task != "" {
			out = append(out, o)
		}
	}
	return out
}

// Insights summarises what the log has learned.
type Insights struct {
	Tasks          int            `json:"tasks"`
	VerifiedRate   int            `json:"verified_rate"` // percent
	FirstTryRate   int            `json:"first_try_rate"`
	AverageMS      int64          `json:"average_ms"`
	TopVerifiers   []VerifierStat `json:"top_verifiers,omitempty"`
	FailureReasons []string       `json:"failure_reasons,omitempty"`
	ByStatus       map[string]int `json:"by_status"`
}

// VerifierStat is how one verification command performed.
type VerifierStat struct {
	Command string `json:"command"`
	Runs    int    `json:"runs"`
	Passed  int    `json:"passed"`
}

// Summarise computes insights from outcomes.
func Summarise(outcomes []Outcome) Insights {
	in := Insights{ByStatus: map[string]int{}}
	if len(outcomes) == 0 {
		return in
	}
	in.Tasks = len(outcomes)
	verified, firstTry, duration := 0, 0, int64(0)
	verifiers := map[string]*VerifierStat{}
	reasons := map[string]int{}
	for _, o := range outcomes {
		in.ByStatus[string(o.Status)]++
		if o.Verified {
			verified++
		}
		if o.Attempts == 0 && o.Status == StatusCompleted {
			firstTry++
		}
		duration += o.DurationMS
		if o.Verify != "" {
			v, ok := verifiers[o.Verify]
			if !ok {
				v = &VerifierStat{Command: o.Verify}
				verifiers[o.Verify] = v
			}
			v.Runs++
			if o.VerifyOK {
				v.Passed++
			}
		}
		if o.Error != "" && o.Status == StatusFailed {
			key := clip(o.Error, 120)
			reasons[key]++
		}
	}
	in.VerifiedRate = verified * 100 / in.Tasks
	in.FirstTryRate = firstTry * 100 / in.Tasks
	in.AverageMS = duration / int64(in.Tasks)
	for _, v := range verifiers {
		in.TopVerifiers = append(in.TopVerifiers, *v)
	}
	sort.Slice(in.TopVerifiers, func(i, j int) bool {
		if in.TopVerifiers[i].Runs != in.TopVerifiers[j].Runs {
			return in.TopVerifiers[i].Runs > in.TopVerifiers[j].Runs
		}
		return in.TopVerifiers[i].Command < in.TopVerifiers[j].Command
	})
	if len(in.TopVerifiers) > 8 {
		in.TopVerifiers = in.TopVerifiers[:8]
	}
	for reason := range reasons {
		in.FailureReasons = append(in.FailureReasons, reason)
		if len(in.FailureReasons) >= 8 {
			break
		}
	}
	sort.Strings(in.FailureReasons)
	return in
}

// RecordRunOutcomes appends one outcome per task that reached a terminal state
// and has not been recorded before. It returns how many were written.
func (l *OutcomeLog) RecordRunOutcomes(run *Run) int {
	if run == nil {
		return 0
	}
	existing := map[string]bool{}
	for _, o := range l.Read() {
		existing[o.Run+"\x00"+o.Task] = true
	}
	written := 0
	for _, t := range run.Tasks {
		if !t.Status.Terminal() {
			continue
		}
		key := run.ID + "\x00" + t.ID
		if existing[key] {
			continue
		}
		o := Outcome{
			Run: run.ID, Task: t.ID, Title: t.Title, Files: t.FilesTouch,
			Status: t.Status, Attempts: t.Attempts, HadDeps: len(t.DependsOn),
			Verified: t.Verified(), Error: t.LastError,
		}
		if len(t.Verify) > 0 {
			last := t.Verify[len(t.Verify)-1]
			o.Verify = last.Command
			o.VerifyOK = last.Passed
			o.DurationMS = last.DurationMS
		}
		if t.Status == StatusFailed {
			o.BlockedBy = firstFailedDep(run, t)
		}
		if err := l.Append(o); err == nil {
			written++
		}
	}
	return written
}

// firstFailedDep returns the first unfinished dependency of a failed task.
func firstFailedDep(run *Run, task Task) string {
	for _, dep := range task.DependsOn {
		for _, t := range run.Tasks {
			if t.ID == dep && t.Status == StatusFailed {
				return dep
			}
		}
	}
	return ""
}

// clip shortens s to n runes.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
