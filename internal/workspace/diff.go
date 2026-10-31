package workspace

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Snapshot diffing: what exactly did mutation batch N change? Walk the union
// of files in two snapshots and produce a line-level diff per file, so the
// genome inspector can show the organism's anatomy before and after.

// DiffLine is one line of a file diff. Tag is " ", "-" or "+".
type DiffLine struct {
	Tag  string `json:"tag"`
	Text string `json:"text"`
}

// FileDiff is the diff of a single path between two snapshots.
type FileDiff struct {
	Path   string     `json:"path"`
	Status string     `json:"status"` // added | removed | modified
	Lines  []DiffLine `json:"lines,omitempty"`
}

const maxDiffLines = 2000 // per file; genomes are small but stay safe

// DiffSnapshots compares two snapshots (ids, "from" older) file by file.
func (w *W) DiffSnapshots(fromID, toID string) ([]FileDiff, error) {
	fromID, toID = strings.TrimSpace(fromID), strings.TrimSpace(toID)
	if err := validateSnapshotID(fromID); err != nil {
		return nil, err
	}
	if err := validateSnapshotID(toID); err != nil {
		return nil, err
	}
	fromDir := filepath.Join(w.Dir(SnapshotsDir), fromID)
	toDir := filepath.Join(w.Dir(SnapshotsDir), toID)
	for _, d := range [2]string{fromDir, toDir} {
		if info, err := os.Stat(d); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("no such snapshot: %s", filepath.Base(d))
		}
	}

	fromFiles, err := collectFiles(fromDir)
	if err != nil {
		return nil, err
	}
	toFiles, err := collectFiles(toDir)
	if err != nil {
		return nil, err
	}

	paths := unionKeys(fromFiles, toFiles)
	sort.Strings(paths)

	var out []FileDiff
	for _, p := range paths {
		fTxt, hasF := fromFiles[p]
		tTxt, hasT := toFiles[p]
		switch {
		case hasF && !hasT:
			out = append(out, FileDiff{Path: p, Status: "removed", Lines: tagLines(fTxt, "-")})
		case !hasF && hasT:
			out = append(out, FileDiff{Path: p, Status: "added", Lines: tagLines(tTxt, "+")})
		default:
			if fTxt == tTxt {
				continue // identical
			}
			out = append(out, FileDiff{Path: p, Status: "modified", Lines: lineDiff(fTxt, tTxt)})
		}
	}
	return out, nil
}

// collectFiles maps workspace-relative paths to their text content.
func collectFiles(root string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		if strings.Contains(rel, ".git") {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil // file vanished mid-walk
		}
		out[rel] = string(raw)
		return nil
	})
	return out, err
}

func unionKeys(a, b map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for k := range a {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for k := range b {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

func tagLines(txt, tag string) []DiffLine {
	lines := strings.Split(strings.TrimRight(txt, "\n"), "\n")
	if len(lines) > maxDiffLines {
		lines = lines[:maxDiffLines]
	}
	out := make([]DiffLine, 0, len(lines))
	for _, l := range lines {
		out = append(out, DiffLine{Tag: tag, Text: l})
	}
	return out
}

// lineDiff is a classic LCS line diff, trimmed to a unified-style window:
// up to 3 context lines around each change block.
func lineDiff(a, b string) []DiffLine {
	as := strings.Split(strings.TrimRight(a, "\n"), "\n")
	bs := strings.Split(strings.TrimRight(b, "\n"), "\n")
	n, m := len(as), len(bs)
	if n > maxDiffLines || m > maxDiffLines {
		// too big for LCS — degrade to whole-file replace
		var out []DiffLine
		for _, l := range tagLines(a, "-") {
			out = append(out, l)
		}
		for _, l := range tagLines(b, "+") {
			out = append(out, l)
		}
		return out
	}

	// LCS table
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if as[i] == bs[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	// walk to full tagged sequence
	var full []DiffLine
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case as[i] == bs[j]:
			full = append(full, DiffLine{Tag: " ", Text: as[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			full = append(full, DiffLine{Tag: "-", Text: as[i]})
			i++
		default:
			full = append(full, DiffLine{Tag: "+", Text: bs[j]})
			j++
		}
	}
	for ; i < n; i++ {
		full = append(full, DiffLine{Tag: "-", Text: as[i]})
	}
	for ; j < m; j++ {
		full = append(full, DiffLine{Tag: "+", Text: bs[j]})
	}

	return withContext(full, 3)
}

// withContext collapses long unchanged runs, keeping `ctx` lines around
// changes and a "…" marker where text was skipped.
func withContext(lines []DiffLine, ctx int) []DiffLine {
	keep := make([]bool, len(lines))
	for i, l := range lines {
		if l.Tag != " " {
			for k := i - ctx; k <= i+ctx; k++ {
				if k >= 0 && k < len(lines) {
					keep[k] = true
				}
			}
		}
	}
	var out []DiffLine
	gap := false
	for i, l := range lines {
		if keep[i] {
			out = append(out, l)
			gap = false
		} else if !gap {
			out = append(out, DiffLine{Tag: " ", Text: "⋯"})
			gap = true
		}
	}
	return out
}
