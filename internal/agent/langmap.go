package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Lightweight, dependency-free symbol extractors for languages EVOLV meets in
// real user projects. These are deliberately shallow: enough to tell a human
// (or the model) where the interesting declarations live without parsing a
// full grammar or shelling out to a language server.
//
// Every extractor must be:
//   - bounded (skip huge files),
//   - safe on garbage input (regexes, not a hand-rolled tokenizer),
//   - honest (only report what the pattern actually proves).

const maxAnalyzedFileBytes = 1 << 20

// langAnalyzer turns source text into symbols.
type langAnalyzer struct {
	name     string
	patterns []langPattern
}

type langPattern struct {
	kind string
	re   *regexp.Regexp
}

var (
	pyAnalyzer = langAnalyzer{name: "python", patterns: []langPattern{
		{"class", regexp.MustCompile(`(?m)^\s*class\s+([A-Za-z_]\w*)\s*[(:]`)},
		{"function", regexp.MustCompile(`(?m)^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)\s*\(`)},
		{"route", regexp.MustCompile(`(?m)^\s*@\w+\.(?:route|get|post|put|delete|patch)\(\s*["']([^"']+)["']`)},
	}}
	rustAnalyzer = langAnalyzer{name: "rust", patterns: []langPattern{
		{"struct", regexp.MustCompile(`(?m)^\s*(?:pub(?:\([^)]*\))?\s+)?struct\s+([A-Za-z_]\w*)`)},
		{"enum", regexp.MustCompile(`(?m)^\s*(?:pub(?:\([^)]*\))?\s+)?enum\s+([A-Za-z_]\w*)`)},
		{"trait", regexp.MustCompile(`(?m)^\s*(?:pub(?:\([^)]*\))?\s+)?trait\s+([A-Za-z_]\w*)`)},
		{"function", regexp.MustCompile(`(?m)^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)`)},
	}}
	javaAnalyzer = langAnalyzer{name: "java", patterns: []langPattern{
		{"class", regexp.MustCompile(`(?m)^\s*(?:public\s+|private\s+|protected\s+)?(?:final\s+|abstract\s+)?class\s+([A-Za-z_]\w*)`)},
		{"interface", regexp.MustCompile(`(?m)^\s*(?:public\s+)?interface\s+([A-Za-z_]\w*)`)},
		{"method", regexp.MustCompile(`(?m)^\s*(?:public|private|protected)\s+(?:static\s+)?(?:final\s+)?[\w<>\[\], ?]+\s+([a-zA-Z_]\w*)\s*\([^;{]*\)\s*\{`)},
	}}
	csAnalyzer = langAnalyzer{name: "csharp", patterns: []langPattern{
		{"class", regexp.MustCompile(`(?m)^\s*(?:public\s+|internal\s+)?(?:sealed\s+|abstract\s+)?class\s+([A-Za-z_]\w*)`)},
		{"interface", regexp.MustCompile(`(?m)^\s*(?:public\s+)?interface\s+([A-Za-z_]\w*)`)},
		{"method", regexp.MustCompile(`(?m)^\s*(?:public|private|protected|internal)\s+(?:static\s+|async\s+)*(?:[\w<>\[\], ?]+)\s+([A-Za-z_]\w*)\s*\(`)},
	}}
	rubyAnalyzer = langAnalyzer{name: "ruby", patterns: []langPattern{
		{"class", regexp.MustCompile(`(?m)^\s*class\s+([A-Z]\w*(?:::[A-Z]\w*)*)`)},
		{"module", regexp.MustCompile(`(?m)^\s*module\s+([A-Z]\w*(?:::[A-Z]\w*)*)`)},
		{"function", regexp.MustCompile(`(?m)^\s*def\s+(?:self\.)?([A-Za-z_]\w*[?!=]?)`)},
	}}
	phpAnalyzer = langAnalyzer{name: "php", patterns: []langPattern{
		{"class", regexp.MustCompile(`(?m)^\s*(?:abstract\s+|final\s+)?class\s+([A-Za-z_]\w*)`)},
		{"interface", regexp.MustCompile(`(?m)^\s*interface\s+([A-Za-z_]\w*)`)},
		{"function", regexp.MustCompile(`(?m)^\s*(?:public\s+|private\s+|protected\s+)?function\s+([A-Za-z_]\w*)`)},
	}}
	elixirAnalyzer = langAnalyzer{name: "elixir", patterns: []langPattern{
		{"module", regexp.MustCompile(`(?m)^\s*defmodule\s+([\w.]+)`)},
		{"function", regexp.MustCompile(`(?m)^\s*defp?\s+([a-z_]\w*[?!]?)`)},
	}}
	shellAnalyzer = langAnalyzer{name: "shell", patterns: []langPattern{
		{"function", regexp.MustCompile(`(?m)^\s*function\s+([A-Za-z_]\w*)`)},
		{"function", regexp.MustCompile(`(?m)^([A-Za-z_]\w*)\s*\(\)\s*\{`)},
	}}
	sqlAnalyzer = langAnalyzer{name: "sql", patterns: []langPattern{
		{"table", regexp.MustCompile(`(?mi)^\s*create\s+table\s+(?:if\s+not\s+exists\s+)?["` + "`" + `\[]?([A-Za-z_][\w.]*)`)},
		{"view", regexp.MustCompile(`(?mi)^\s*create\s+(?:or\s+replace\s+)?view\s+["` + "`" + `\[]?([A-Za-z_][\w.]*)`)},
		{"function", regexp.MustCompile(`(?mi)^\s*create\s+(?:or\s+replace\s+)?function\s+["` + "`" + `\[]?([A-Za-z_][\w.]*)`)},
	}}
	swiftAnalyzer = langAnalyzer{name: "swift", patterns: []langPattern{
		{"struct", regexp.MustCompile(`(?m)^\s*(?:public\s+|internal\s+)?struct\s+([A-Za-z_]\w*)`)},
		{"class", regexp.MustCompile(`(?m)^\s*(?:public\s+|internal\s+)?class\s+([A-Za-z_]\w*)`)},
		{"function", regexp.MustCompile(`(?m)^\s*(?:public\s+|private\s+)?func\s+([A-Za-z_]\w*)`)},
	}}
	kotlinAnalyzer = langAnalyzer{name: "kotlin", patterns: []langPattern{
		{"class", regexp.MustCompile(`(?m)^\s*(?:data\s+|sealed\s+|abstract\s+)?class\s+([A-Za-z_]\w*)`)},
		{"function", regexp.MustCompile(`(?m)^\s*(?:suspend\s+)?fun\s+([A-Za-z_]\w*)`)},
	}}
)

// analyzersByExt maps a lowercase extension to its analyzer.
var analyzersByExt = map[string]langAnalyzer{
	".py":    pyAnalyzer,
	".pyi":   pyAnalyzer,
	".rs":    rustAnalyzer,
	".java":  javaAnalyzer,
	".cs":    csAnalyzer,
	".rb":    rubyAnalyzer,
	".php":   phpAnalyzer,
	".ex":    elixirAnalyzer,
	".exs":   elixirAnalyzer,
	".sh":    shellAnalyzer,
	".bash":  shellAnalyzer,
	".zsh":   shellAnalyzer,
	".sql":   sqlAnalyzer,
	".swift": swiftAnalyzer,
	".kt":    kotlinAnalyzer,
	".kts":   kotlinAnalyzer,
	".c":     javaAnalyzer,
	".h":     javaAnalyzer,
	".cc":    javaAnalyzer,
	".cpp":   javaAnalyzer,
	".hpp":   javaAnalyzer,
}

// languageOf reports the display language for a path, or "" when unknown.
func languageOf(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".jsx":
		return "jsx"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "tsx"
	}
	if a, ok := analyzersByExt[ext]; ok {
		return a.name
	}
	return ""
}

// languageSymbols extracts symbols for any supported language. Unknown or
// oversized files return nil rather than guessing.
func languageSymbols(path string) []repoSymbol {
	ext := strings.ToLower(filepath.Ext(path))
	if a, ok := analyzersByExt[ext]; ok {
		raw, err := readBounded(path)
		if err != nil {
			return nil
		}
		return analyzeText(string(raw), a)
	}
	return nil
}

// analyzeText applies one analyzer's patterns to source text.
func analyzeText(text string, a langAnalyzer) []repoSymbol {
	var out []repoSymbol
	seen := map[string]bool{}
	for _, p := range a.patterns {
		for _, m := range p.re.FindAllStringSubmatchIndex(text, -1) {
			if len(m) < 4 || m[2] < 0 {
				continue
			}
			name := text[m[2]:m[3]]
			line := 1 + strings.Count(text[:m[0]], "\n")
			key := p.kind + "\x00" + name + "\x00" + strconv.Itoa(line)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, repoSymbol{Kind: p.kind, Name: name, Line: line})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > 400 {
		out = out[:400]
	}
	return out
}

func readBounded(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxAnalyzedFileBytes {
		if err == nil {
			return nil, os.ErrInvalid
		}
		return nil, err
	}
	return os.ReadFile(path)
}

// ---------- project intelligence ----------

// projectKind is the coarse shape of a repository, inferred from markers.
type projectKind string

const (
	kindGo      projectKind = "go"
	kindNode    projectKind = "node"
	kindPython  projectKind = "python"
	kindRust    projectKind = "rust"
	kindJVM     projectKind = "jvm"
	kindDotNet  projectKind = "dotnet"
	kindRuby    projectKind = "ruby"
	kindPHP     projectKind = "php"
	kindElixir  projectKind = "elixir"
	kindUnknown projectKind = "unknown"
)

// projectProfile is what map_repo reports about the shape of a project: the
// languages actually present, the build/test commands the project itself
// provides, and the entry points worth reading first.
type projectProfile struct {
	Kind        projectKind       `json:"kind"`
	Languages   map[string]int    `json:"languages"` // language -> file count
	Frameworks  []string          `json:"frameworks,omitempty"`
	Manifests   []string          `json:"manifests,omitempty"`
	EntryPoints []string          `json:"entry_points,omitempty"`
	Tests       []string          `json:"test_roots,omitempty"`
	Commands    map[string]string `json:"commands,omitempty"` // build/test/lint/format
	Notes       []string          `json:"notes,omitempty"`
}

// markerFiles are the files that identify a project kind.
var markerFiles = map[string]projectKind{
	"go.mod": kindGo, "go.work": kindGo,
	"package.json": kindNode, "pnpm-lock.yaml": kindNode, "yarn.lock": kindNode,
	"pyproject.toml": kindPython, "setup.py": kindPython, "requirements.txt": kindPython,
	"Pipfile":    kindPython,
	"Cargo.toml": kindRust,
	"pom.xml":    kindJVM, "build.gradle": kindJVM, "build.gradle.kts": kindJVM,
	"*.csproj": kindDotNet, "*.sln": kindDotNet,
	"Gemfile":       kindRuby,
	"composer.json": kindPHP,
	"mix.exs":       kindElixir,
}

// entryPointNames are filenames that usually open a project.
var entryPointNames = []string{
	"main.go", "cmd", "app.go", "server.go",
	"index.js", "index.ts", "server.js", "server.ts", "app.js", "app.ts", "main.ts",
	"main.py", "__main__.py", "app.py", "manage.py",
	"main.rs", "lib.rs",
	"Main.java", "Program.cs",
}

// profileProject inspects a repository root (depth-limited) and describes what
// it found. It never reads file bodies except small manifests.
func profileProject(root string, files []string) projectProfile {
	prof := projectProfile{Kind: kindUnknown, Languages: map[string]int{}, Commands: map[string]string{}}
	top := map[string]bool{}
	for _, f := range files {
		trimmed := strings.TrimSuffix(f, "/")
		if !strings.Contains(trimmed, "/") {
			top[filepath.Base(trimmed)] = true
		}
		if strings.HasSuffix(f, "/") {
			continue
		}
		if lang := languageOf(f); lang != "" {
			prof.Languages[lang]++
		}
	}
	// project kind from markers (most specific first)
	for _, order := range []projectKind{kindRust, kindGo, kindDotNet, kindJVM, kindPython, kindElixir, kindRuby, kindPHP, kindNode} {
		for marker, kind := range markerFiles {
			if kind != order {
				continue
			}
			if strings.HasPrefix(marker, "*") {
				suffix := strings.TrimPrefix(marker, "*")
				for name := range top {
					if strings.HasSuffix(name, suffix) {
						prof.Kind = kind
						prof.Manifests = append(prof.Manifests, name)
					}
				}
				continue
			}
			if top[marker] {
				prof.Kind = kind
				prof.Manifests = append(prof.Manifests, marker)
			}
		}
		if prof.Kind != kindUnknown {
			break
		}
	}
	sort.Strings(prof.Manifests)

	// commands the project itself defines
	switch prof.Kind {
	case kindGo:
		prof.Commands["test"] = "go test ./..."
		prof.Commands["build"] = "go build ./..."
		prof.Commands["vet"] = "go vet ./..."
		prof.Commands["format"] = "gofmt -w ."
	case kindNode:
		prof.Commands["install"] = "npm install"
		if scripts, ok := readPackageScripts(filepath.Join(root, "package.json")); ok {
			for _, name := range []string{"dev", "start", "build", "test", "lint", "format"} {
				if cmd, ok := scripts[name]; ok {
					prof.Commands[name] = "npm run " + name + "  # " + cmd
				}
			}
			for _, fw := range frameworksFromPackage(filepath.Join(root, "package.json")) {
				prof.Frameworks = append(prof.Frameworks, fw)
			}
		}
	case kindPython:
		prof.Commands["test"] = "pytest"
		prof.Commands["install"] = "pip install -e ."
	case kindRust:
		prof.Commands["test"] = "cargo test"
		prof.Commands["build"] = "cargo build"
		prof.Commands["format"] = "cargo fmt"
	case kindJVM:
		if top["pom.xml"] {
			prof.Commands["test"] = "mvn test"
			prof.Commands["build"] = "mvn package"
		} else {
			prof.Commands["test"] = "./gradlew test"
			prof.Commands["build"] = "./gradlew build"
		}
	case kindDotNet:
		prof.Commands["test"] = "dotnet test"
		prof.Commands["build"] = "dotnet build"
	case kindRuby:
		prof.Commands["test"] = "bundle exec rspec"
	case kindElixir:
		prof.Commands["test"] = "mix test"
		prof.Commands["build"] = "mix compile"
	case kindPHP:
		prof.Commands["test"] = "vendor/bin/phpunit"
	}

	// test roots
	for _, f := range files {
		base := filepath.Base(strings.TrimSuffix(f, "/"))
		if strings.Contains(f, "_test.go") || strings.HasPrefix(base, "test_") ||
			strings.HasSuffix(base, "_test.py") || base == "tests" || base == "__tests__" ||
			strings.HasSuffix(base, ".test.js") || strings.HasSuffix(base, ".test.ts") ||
			strings.HasSuffix(base, "_spec.rb") {
			root := f
			if !strings.HasSuffix(root, "/") {
				root = filepath.ToSlash(filepath.Dir(root)) + "/"
			}
			if !containsString(prof.Tests, root) && len(prof.Tests) < 12 {
				prof.Tests = append(prof.Tests, root)
			}
		}
	}

	// entry points, shallow first
	for _, f := range files {
		if strings.HasSuffix(f, "/") {
			continue
		}
		base := filepath.Base(f)
		for _, name := range entryPointNames {
			if base == name && len(prof.EntryPoints) < 10 {
				prof.EntryPoints = append(prof.EntryPoints, f)
			}
		}
	}
	if len(prof.Languages) == 0 {
		prof.Notes = append(prof.Notes, "no recognized source files in the mapped depth — increase depth or check the project root")
	}
	return prof
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
