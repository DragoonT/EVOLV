package server

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Search is the Workbench's ripgrep-lite: it scans the bound project once,
// honours the skip list, filters by glob/extension, ranks results so the most
// useful hits come first, and returns context lines. No index and no external
// binary, so it works identically on every platform.
//
// Caps are deliberate: a search is an interactive operation, not a report.

const (
	searchMaxFiles     = 20000 // files visited
	searchMaxFileBytes = 1 << 20
	searchDefaultLimit = 100
	searchMaxLimit     = 500
	searchContextLines = 1
)

// searchOptions is the parsed query.
type searchOptions struct {
	Query      string
	Regex      bool
	CaseSens   bool
	Exts       []string // e.g. ["go", "ts"]
	PathGlob   string   // e.g. "internal/*"
	Limit      int
	MaxPerFile int
}

// SearchHit is one matching line.
type SearchHit struct {
	Path    string   `json:"path"`
	Line    int      `json:"line"`
	Col     int      `json:"col"`
	Text    string   `json:"text"`
	Before  []string `json:"before,omitempty"`
	After   []string `json:"after,omitempty"`
	Score   int      `json:"score"`
	Matches int      `json:"matches"`
}

// SearchResult is the full response.
type SearchResult struct {
	Query     string      `json:"query"`
	Hits      []SearchHit `json:"hits"`
	Files     int         `json:"files"`
	Scanned   int         `json:"scanned"`
	Truncated bool        `json:"truncated"`
	Regex     bool        `json:"regex"`
	Error     string      `json:"error,omitempty"`
}

// parseSearchOptions reads query parameters into options, returning a
// user-facing error for a bad regex or an out-of-range limit.
func parseSearchOptions(values map[string]string) (searchOptions, string) {
	opts := searchOptions{
		Query:      strings.TrimSpace(values["q"]),
		Regex:      isTruthy(values["regex"]),
		CaseSens:   isTruthy(values["case"]),
		PathGlob:   strings.TrimSpace(values["path"]),
		Limit:      searchDefaultLimit,
		MaxPerFile: 20,
	}
	if raw := strings.TrimSpace(values["ext"]); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			part = strings.Trim(strings.TrimSpace(part), ".")
			if part != "" {
				opts.Exts = append(opts.Exts, strings.ToLower(part))
			}
		}
	}
	if raw := strings.TrimSpace(values["limit"]); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return opts, "limit must be a positive integer"
		}
		if n > searchMaxLimit {
			n = searchMaxLimit
		}
		opts.Limit = n
	}
	return opts, ""
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// compileMatcher turns the query into a matcher function.
func compileMatcher(opts searchOptions) (func(string) (int, int), bool, string) {
	if opts.Query == "" {
		return nil, false, "need q"
	}
	if opts.Regex {
		expr := opts.Query
		if !opts.CaseSens {
			expr = "(?i)" + expr
		}
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, true, "invalid regular expression: " + err.Error()
		}
		return func(line string) (int, int) {
			locs := re.FindAllStringIndex(line, -1)
			if len(locs) == 0 {
				return -1, 0
			}
			return locs[0][0], len(locs)
		}, true, ""
	}
	needle := opts.Query
	if !opts.CaseSens {
		needle = strings.ToLower(needle)
	}
	return func(line string) (int, int) {
		hay := line
		if !opts.CaseSens {
			hay = strings.ToLower(line)
		}
		idx := strings.Index(hay, needle)
		if idx < 0 {
			return -1, 0
		}
		return idx, strings.Count(hay, needle)
	}, false, ""
}

// matchPath reports whether a relative path passes the ext and glob filters.
func matchPath(rel string, opts searchOptions) bool {
	if len(opts.Exts) > 0 {
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(rel), "."))
		ok := false
		for _, want := range opts.Exts {
			if ext == want {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if opts.PathGlob != "" {
		glob := filepath.ToSlash(opts.PathGlob)
		trimmed := strings.TrimSuffix(glob, "/")
		if !strings.HasPrefix(filepath.ToSlash(rel), trimmed) && !strings.Contains(filepath.ToSlash(rel), trimmed) {
			if ok, err := filepath.Match(glob, filepath.ToSlash(rel)); err != nil || !ok {
				return false
			}
		}
	}
	return true
}

// scoreHit ranks a hit. Declaration-like lines and matches near the start of a
// path (shallower files) score higher; matches in test files score lower so
// implementation shows up first.
func scoreHit(rel, line string, matches, col int) int {
	score := matches * 2
	trimmed := strings.TrimSpace(line)
	for _, prefix := range []string{"func ", "type ", "class ", "def ", "fn ", "struct ", "impl ", "interface ", "export ", "const ", "var "} {
		if strings.HasPrefix(trimmed, prefix) {
			score += 8
			break
		}
	}
	if strings.Contains(rel, "_test.") || strings.Contains(rel, ".test.") || strings.Contains(rel, "_spec.") || strings.Contains(rel, "/tests/") {
		score -= 4
	}
	// Declaration lines rank above prose and tests; file depth is only a weak
	// tiebreak, never enough to beat a declaration.
	depth := strings.Count(rel, "/")
	switch {
	case depth <= 1:
		score++
	case depth >= 4:
		score--
	}
	if col >= 0 && col < 8 {
		score++
	}
	return score
}

// searchProject walks root and returns ranked hits.
func searchProject(root string, opts searchOptions) SearchResult {
	matcher, isRegex, qerr := compileMatcher(opts)
	result := SearchResult{Query: opts.Query, Hits: []SearchHit{}, Regex: isRegex}
	if qerr != "" {
		result.Error = qerr
		return result
	}

	type candidate struct {
		hit SearchHit
		seq int
	}
	var found []candidate
	seq := 0
	visited := 0
	scanned := 0

	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if wbSkip[d.Name()] && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if visited >= searchMaxFiles {
			result.Truncated = true
			return filepath.SkipAll
		}
		visited++
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !matchPath(rel, opts) {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil || info.Size() > searchMaxFileBytes {
			return nil
		}
		raw, readErr := os.ReadFile(p)
		if readErr != nil || !isMostlyText(raw) {
			return nil
		}
		scanned++
		if strings.IndexByte(string(raw), 0) >= 0 {
			return nil
		}
		lines := strings.Split(string(raw), "\n")
		perFile := 0
		for i, line := range lines {
			if perFile >= opts.MaxPerFile {
				break
			}
			col, matches := matcher(line)
			if col < 0 {
				continue
			}
			hit := SearchHit{
				Path: rel, Line: i + 1, Col: col + 1,
				Text: strings.TrimRight(line, "\r"), Matches: matches,
				Score: scoreHit(rel, line, matches, col),
			}
			if searchContextLines > 0 {
				from := i - searchContextLines
				if from < 0 {
					from = 0
				}
				to := i + searchContextLines + 1
				if to > len(lines) {
					to = len(lines)
				}
				if i > from {
					hit.Before = append([]string{}, lines[from:i]...)
				}
				if to > i+1 {
					hit.After = append([]string{}, lines[i+1:to]...)
				}
			}
			found = append(found, candidate{hit: hit, seq: seq})
			seq++
			perFile++
			if len(found) >= opts.Limit*8 {
				result.Truncated = true
				return filepath.SkipAll
			}
		}
		return nil
	})

	sort.SliceStable(found, func(i, j int) bool {
		if found[i].hit.Score != found[j].hit.Score {
			return found[i].hit.Score > found[j].hit.Score
		}
		return found[i].seq < found[j].seq
	})
	limit := opts.Limit
	if len(found) < limit {
		limit = len(found)
	} else {
		result.Truncated = true
	}
	for i := 0; i < limit; i++ {
		result.Hits = append(result.Hits, found[i].hit)
	}
	result.Files = scanned
	result.Scanned = visited
	return result
}
