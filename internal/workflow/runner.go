// Package workflow runner coordinates built-in and dynamic DAG pipelines,
// manages pipeline execution history, and publishes progress events.
package workflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// PipelineInfo describes a registered workflow pipeline.
type PipelineInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Steps       []string `json:"steps"`
}

// CustomStep defines an ad-hoc step in a dynamic workflow spec.
type CustomStep struct {
	ID        string   `json:"id"`
	DependsOn []string `json:"depends_on,omitempty"`
	Command   string   `json:"command,omitempty"`
	TimeoutMs int      `json:"timeout_ms,omitempty"`
	Attempts  int      `json:"attempts,omitempty"`
}

// CustomSpec defines an ad-hoc DAG submitted for execution.
type CustomSpec struct {
	Name  string       `json:"name"`
	Steps []CustomStep `json:"steps"`
}

// RunnerConfig provides execution settings and callbacks.
type RunnerConfig struct {
	ProjectRoot func() string
	BusPub      func(event string, payload any)
	Parallelism int
}

// Runner executes pipelines and tracks execution history.
type Runner struct {
	mu          sync.RWMutex
	exec        *Executor
	history     []Result
	maxHist     int
	projectRoot func() string
	busPub      func(event string, payload any)
}

// NewRunner creates a new workflow runner.
func NewRunner(cfg RunnerConfig) *Runner {
	p := cfg.Parallelism
	if p <= 0 {
		p = 4
	}
	r := &Runner{
		exec:        &Executor{Parallelism: p},
		history:     make([]Result, 0, 30),
		maxHist:     30,
		projectRoot: cfg.ProjectRoot,
		busPub:      cfg.BusPub,
	}
	return r
}

// ListPipelines returns the catalog of built-in pipelines.
func (r *Runner) ListPipelines() []PipelineInfo {
	return []PipelineInfo{
		{
			ID:          "quality_gate",
			Name:        "Quality Gate",
			Description: "Concurrent code formatting check, compiler vetting, and short unit test verification.",
			Steps:       []string{"fmt", "vet", "test", "verdict"},
		},
		{
			ID:          "security_audit",
			Name:        "Security Audit",
			Description: "Scans repository for exposed API credentials, private keys, and sensitive file exposures.",
			Steps:       []string{"secret_scan", "sensitive_files", "verdict"},
		},
		{
			ID:          "workspace_health",
			Name:        "Workspace Health",
			Description: "Verifies workspace directory integrity and git repository status.",
			Steps:       []string{"layout", "git_status", "verdict"},
		},
	}
}

// GetPipeline returns information about a specific built-in pipeline.
func (r *Runner) GetPipeline(id string) (PipelineInfo, bool) {
	for _, p := range r.ListPipelines() {
		if p.ID == id {
			return p, true
		}
	}
	return PipelineInfo{}, false
}

// History returns a copy of past workflow execution results.
func (r *Runner) History() []Result {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Result, len(r.history))
	copy(out, r.history)
	return out
}

func (r *Runner) recordHistory(res Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.history) >= r.maxHist {
		r.history = r.history[1:]
	}
	r.history = append(r.history, res)
}

func (r *Runner) publish(event string, payload any) {
	if r.busPub != nil {
		r.busPub(event, payload)
	}
}

func (r *Runner) root() string {
	if r.projectRoot != nil {
		if d := r.projectRoot(); d != "" {
			if fi, err := os.Stat(d); err == nil && fi.IsDir() {
				return d
			}
		}
	}
	cwd, err := os.Getwd()
	if err == nil && cwd != "" {
		return cwd
	}
	return "."
}

// Run executes a named built-in pipeline or custom spec.
func (r *Runner) Run(ctx context.Context, name string, custom *CustomSpec) (*Result, error) {
	var spec Spec
	var err error

	if custom != nil && len(custom.Steps) > 0 {
		spec, err = r.buildCustomSpec(*custom)
		if err != nil {
			return nil, err
		}
	} else {
		spec, err = r.buildBuiltinSpec(name)
		if err != nil {
			return nil, err
		}
	}

	runID := fmt.Sprintf("wf-%d", time.Now().UnixNano())
	r.publish("workflow:start", map[string]any{
		"id":        runID,
		"name":      spec.Name,
		"timestamp": time.Now().UnixMilli(),
		"steps":     len(spec.Steps),
	})

	res, err := r.exec.Run(ctx, spec)
	if err != nil {
		r.publish("workflow:error", map[string]any{
			"id":    runID,
			"name":  spec.Name,
			"error": err.Error(),
		})
		return nil, err
	}

	r.recordHistory(*res)

	r.publish("workflow:done", map[string]any{
		"id":          runID,
		"name":        res.Name,
		"succeeded":   res.Succeeded(),
		"duration_ms": float64(res.Duration.Microseconds()) / 1000.0,
		"steps_count": len(res.Steps),
	})

	return res, nil
}

func (r *Runner) buildBuiltinSpec(name string) (Spec, error) {
	target := strings.ToLower(strings.TrimSpace(name))
	switch target {
	case "quality_gate", "quality", "test_and_lint":
		return r.buildSpecQualityGate(), nil
	case "security_audit", "security", "audit":
		return r.buildSpecSecurityAudit(), nil
	case "workspace_health", "health", "sanity":
		return r.buildSpecWorkspaceHealth(), nil
	default:
		return Spec{}, fmt.Errorf("unknown pipeline: %q", name)
	}
}

func (r *Runner) buildSpecQualityGate() Spec {
	dir := r.root()
	return Spec{
		Name: "quality_gate",
		Steps: []Step{
			{
				ID: "fmt",
				Fn: func(ctx context.Context, _ map[string]any) (any, error) {
					cmd := exec.CommandContext(ctx, "gofmt", "-l", ".")
					cmd.Dir = dir
					out, err := cmd.CombinedOutput()
					if err != nil {
						return nil, fmt.Errorf("gofmt failed: %w", err)
					}
					needsFmt := strings.TrimSpace(string(out))
					if needsFmt != "" {
						return map[string]any{
							"clean": false,
							"files": strings.Split(needsFmt, "\n"),
						}, errors.New("formatting needed: " + needsFmt)
					}
					return map[string]any{"clean": true, "message": "all files formatted cleanly"}, nil
				},
			},
			{
				ID: "vet",
				Fn: func(ctx context.Context, _ map[string]any) (any, error) {
					cmd := exec.CommandContext(ctx, "go", "vet", "./...")
					cmd.Dir = dir
					out, err := cmd.CombinedOutput()
					if err != nil {
						return nil, fmt.Errorf("go vet error: %s", strings.TrimSpace(string(out)))
					}
					return map[string]any{"clean": true, "message": "vet check passed"}, nil
				},
			},
			{
				ID: "test",
				Fn: func(ctx context.Context, _ map[string]any) (any, error) {
					cmd := exec.CommandContext(ctx, "go", "test", "-short", "./...")
					cmd.Dir = dir
					out, err := cmd.CombinedOutput()
					if err != nil {
						return nil, fmt.Errorf("test failure: %s", strings.TrimSpace(string(out)))
					}
					return map[string]any{"clean": true, "output": strings.TrimSpace(string(out))}, nil
				},
			},
			{
				ID:        "verdict",
				DependsOn: []string{"fmt", "vet", "test"},
				Fn: func(ctx context.Context, deps map[string]any) (any, error) {
					return map[string]any{
						"verdict": "passed",
						"summary": "Quality gate passed: formatting, vetting, and unit tests all clean.",
						"deps":    deps,
					}, nil
				},
			},
		},
	}
}

func (r *Runner) buildSpecSecurityAudit() Spec {
	dir := r.root()
	return Spec{
		Name: "security_audit",
		Steps: []Step{
			{
				ID: "secret_scan",
				Fn: func(ctx context.Context, _ map[string]any) (any, error) {
					// Lightweight credential scan
					patterns := []string{"AKIA", "ghp_", "sk-proj-", "xoxb-", "-----BEGIN OPENSSH PRIVATE KEY-----"}
					var found []string
					_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
						if err != nil || info.IsDir() {
							if info != nil && info.IsDir() {
								name := info.Name()
								if name == ".git" || name == "node_modules" || name == "dist" {
									return filepath.SkipDir
								}
							}
							return nil
						}
						// Check text files up to 512KB
						if info.Size() > 512*1024 {
							return nil
						}
						ext := strings.ToLower(filepath.Ext(p))
						if ext != ".go" && ext != ".json" && ext != ".yaml" && ext != ".yml" && ext != ".env" && ext != ".md" {
							return nil
						}
						content, readErr := os.ReadFile(p)
						if readErr != nil {
							return nil
						}
						for _, pat := range patterns {
							if bytes.Contains(content, []byte(pat)) {
								rel, _ := filepath.Rel(dir, p)
								found = append(found, fmt.Sprintf("%s (%s)", rel, pat))
								break
							}
						}
						return nil
					})
					if len(found) > 0 {
						return map[string]any{"clean": false, "findings": found}, fmt.Errorf("potential secrets found in: %s", strings.Join(found, ", "))
					}
					return map[string]any{"clean": true, "findings_count": 0}, nil
				},
			},
			{
				ID: "sensitive_files",
				Fn: func(ctx context.Context, _ map[string]any) (any, error) {
					var sensitive []string
					badFiles := []string{".env", ".env.local", "id_rsa", "id_ed25519"}
					for _, bf := range badFiles {
						target := filepath.Join(dir, bf)
						if fi, err := os.Stat(target); err == nil && !fi.IsDir() {
							sensitive = append(sensitive, bf)
						}
					}
					return map[string]any{
						"sensitive_files": sensitive,
						"clean":           len(sensitive) == 0,
					}, nil
				},
			},
			{
				ID:        "verdict",
				DependsOn: []string{"secret_scan", "sensitive_files"},
				Fn: func(ctx context.Context, deps map[string]any) (any, error) {
					return map[string]any{
						"verdict": "audited",
						"summary": "Security audit complete.",
						"details": deps,
					}, nil
				},
			},
		},
	}
}

func (r *Runner) buildSpecWorkspaceHealth() Spec {
	dir := r.root()
	return Spec{
		Name: "workspace_health",
		Steps: []Step{
			{
				ID: "layout",
				Fn: func(ctx context.Context, _ map[string]any) (any, error) {
					required := []string{"cmd", "internal", "assets"}
					missing := []string{}
					for _, req := range required {
						if _, err := os.Stat(filepath.Join(dir, req)); err != nil {
							missing = append(missing, req)
						}
					}
					if len(missing) > 0 {
						return nil, fmt.Errorf("missing core directories: %s", strings.Join(missing, ", "))
					}
					return map[string]any{"intact": true}, nil
				},
			},
			{
				ID: "git_status",
				Fn: func(ctx context.Context, _ map[string]any) (any, error) {
					cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
					cmd.Dir = dir
					out, err := cmd.CombinedOutput()
					if err != nil {
						return map[string]any{"git": false, "error": err.Error()}, nil
					}
					lines := strings.TrimSpace(string(out))
					count := 0
					if lines != "" {
						count = len(strings.Split(lines, "\n"))
					}
					return map[string]any{"dirty_entries": count, "clean": count == 0}, nil
				},
			},
			{
				ID:        "verdict",
				DependsOn: []string{"layout", "git_status"},
				Fn: func(ctx context.Context, deps map[string]any) (any, error) {
					return map[string]any{
						"verdict": "healthy",
						"summary": "Workspace health verified.",
						"results": deps,
					}, nil
				},
			},
		},
	}
}

func (r *Runner) buildCustomSpec(custom CustomSpec) (Spec, error) {
	if strings.TrimSpace(custom.Name) == "" {
		custom.Name = "custom_workflow"
	}
	dir := r.root()
	steps := make([]Step, len(custom.Steps))
	for i, cs := range custom.Steps {
		if strings.TrimSpace(cs.ID) == "" {
			return Spec{}, fmt.Errorf("step %d has empty ID", i)
		}
		cmdStr := strings.TrimSpace(cs.Command)
		if cmdStr == "" {
			return Spec{}, fmt.Errorf("step %q requires a command", cs.ID)
		}
		var timeout time.Duration
		if cs.TimeoutMs > 0 {
			timeout = time.Duration(cs.TimeoutMs) * time.Millisecond
		}
		attempts := cs.Attempts
		if attempts <= 0 {
			attempts = 1
		}

		capturedCmd := cmdStr
		steps[i] = Step{
			ID:        cs.ID,
			DependsOn: cs.DependsOn,
			Timeout:   timeout,
			Attempts:  attempts,
			Fn: func(ctx context.Context, deps map[string]any) (any, error) {
				parts := strings.Fields(capturedCmd)
				cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
				cmd.Dir = dir
				out, err := cmd.CombinedOutput()
				if err != nil {
					return nil, fmt.Errorf("command %q failed: %w (output: %s)", capturedCmd, err, strings.TrimSpace(string(out)))
				}
				return map[string]any{
					"output": strings.TrimSpace(string(out)),
					"deps":   deps,
				}, nil
			},
		}
	}
	spec := Spec{Name: custom.Name, Steps: steps}
	if err := Validate(spec); err != nil {
		return Spec{}, err
	}
	return spec, nil
}
