package search

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Search limits and defaults.
const (
	DefaultLimit    = 50
	MaxLimit        = 500
	DefaultContext  = 2
	MaxFileBytes    = 1 << 20 // 1 MB
	MaxFilesToVisit = 25000
)

// Options configures a codebase search.
type Options struct {
	Query      string   `json:"query"`
	Regex      bool     `json:"regex,omitempty"`
	CaseSens   bool     `json:"case_sensitive,omitempty"`
	Exts       []string `json:"exts,omitempty"`      // e.g. ["go", "ts"]
	PathGlob   string   `json:"path_glob,omitempty"` // e.g. "internal/*"
	Limit      int      `json:"limit,omitempty"`
	MaxPerFile int      `json:"max_per_file,omitempty"`
	Context    int      `json:"context,omitempty"` // context lines before and after
}

// Hit represents a matching line within a file.
type Hit struct {
	Path    string   `json:"path"`
	Line    int      `json:"line"`
	Col     int      `json:"col"`
	Text    string   `json:"text"`
	Before  []string `json:"before,omitempty"`
	After   []string `json:"after,omitempty"`
	Score   int      `json:"score"`
	Matches int      `json:"matches"`
}

// Result is the consolidated outcome of a search.
type Result struct {
	Query     string `json:"query"`
	Hits      []Hit  `json:"hits"`
	Files     int    `json:"files"`
	Scanned   int    `json:"scanned"`
	Truncated bool   `json:"truncated"`
	Regex     bool   `json:"regex"`
	Error     string `json:"error,omitempty"`
}

// defaultSkipDirs lists directories that should never be searched.
var defaultSkipDirs = map[string]bool{
	".git":         true,
	".evolv":       true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
	".cache":       true,
	"__pycache__":  true,
	".venv":        true,
	"coverage":     true,
	".idea":        true,
	".vscode":      true,
}

// Scan searches root for occurrences matching opts.
func Scan(root string, opts Options) Result {
	opts.Query = strings.TrimSpace(opts.Query)
	res := Result{Query: opts.Query, Hits: []Hit{}}
	if opts.Query == "" {
		res.Error = "search query is required"
		return res
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultLimit
	} else if limit > MaxLimit {
		limit = MaxLimit
	}
	opts.Limit = limit

	if opts.MaxPerFile <= 0 {
		opts.MaxPerFile = 20
	}
	if opts.Context < 0 {
		opts.Context = 0
	} else if opts.Context > 5 {
		opts.Context = 5
	}

	matcher, isRegex, qerr := compileMatcher(opts)
	res.Regex = isRegex
	if qerr != "" {
		res.Error = qerr
		return res
	}

	type candidate struct {
		hit Hit
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
			if defaultSkipDirs[d.Name()] && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if visited >= MaxFilesToVisit {
			res.Truncated = true
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
		if statErr != nil || info.Size() > MaxFileBytes {
			return nil
		}

		raw, readErr := os.ReadFile(p)
		if readErr != nil || !IsMostlyText(raw) {
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
			hit := Hit{
				Path:    rel,
				Line:    i + 1,
				Col:     col + 1,
				Text:    strings.TrimRight(line, "\r"),
				Matches: matches,
				Score:   scoreHit(rel, line, matches, col),
			}
			if opts.Context > 0 {
				from := i - opts.Context
				if from < 0 {
					from = 0
				}
				to := i + opts.Context + 1
				if to > len(lines) {
					to = len(lines)
				}
				for k := from; k < i; k++ {
					hit.Before = append(hit.Before, strings.TrimRight(lines[k], "\r"))
				}
				for k := i + 1; k < to; k++ {
					hit.After = append(hit.After, strings.TrimRight(lines[k], "\r"))
				}
			}
			found = append(found, candidate{hit: hit, seq: seq})
			seq++
			perFile++

			if len(found) >= opts.Limit*8 {
				res.Truncated = true
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

	hitLimit := opts.Limit
	if len(found) < hitLimit {
		hitLimit = len(found)
	} else {
		res.Truncated = true
	}
	for i := 0; i < hitLimit; i++ {
		res.Hits = append(res.Hits, found[i].hit)
	}
	res.Files = scanned
	res.Scanned = visited
	return res
}

func compileMatcher(opts Options) (func(string) (int, int), bool, string) {
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

func matchPath(rel string, opts Options) bool {
	if len(opts.Exts) > 0 {
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(rel), "."))
		ok := false
		for _, want := range opts.Exts {
			want = strings.ToLower(strings.TrimPrefix(want, "."))
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

// IsMostlyText reports whether raw looks like readable text rather than binary data.
func IsMostlyText(raw []byte) bool {
	if len(raw) == 0 {
		return true
	}
	probe := raw
	if len(probe) > 4096 {
		probe = probe[:4096]
	}
	if strings.IndexByte(string(probe), 0) >= 0 {
		return false
	}
	if !utf8.Valid(probe) {
		return false
	}
	return true
}
