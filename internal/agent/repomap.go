package agent

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dxn111/EVOLV/internal/workspace"
)

const (
	maxRepoMapEntries = 2000
	maxRepoMapBytes   = 1 << 20
)

type repoMap struct {
	Root      string            `json:"root"`
	Generated string            `json:"generated"`
	Files     []string          `json:"files"`
	Symbols   []repoFileSymbols `json:"symbols"`
	Profile   *projectProfile   `json:"profile,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
}

type repoFileSymbols struct {
	Path    string       `json:"path"`
	Symbols []repoSymbol `json:"symbols"`
}

type repoSymbol struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Line int    `json:"line"`
}

var jsSymbolPatterns = []struct {
	kind string
	re   *regexp.Regexp
}{
	{"function", regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)\s*\(`)},
	{"class", regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:default\s+)?class\s+([A-Za-z_$][\w$]*)\b`)},
	{"function", regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\([^\n]*\)|[A-Za-z_$][\w$]*)\s*=>`)},
}

func buildRepoMap(root string, depth int) (*repoMap, error) {
	if depth <= 0 || depth > 8 {
		depth = 4
	}
	m := &repoMap{Root: root, Generated: time.Now().UTC().Format(time.RFC3339), Files: []string{}, Symbols: []repoFileSymbols{}}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return nil
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if d.IsDir() && ignoredRepoDir(d.Name()) {
			return filepath.SkipDir
		}
		if len(parts) > depth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if len(m.Files) >= maxRepoMapEntries {
			m.Truncated = true
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			m.Files = append(m.Files, rel+"/")
			return nil
		}
		m.Files = append(m.Files, rel)
		info, err := d.Info()
		if err != nil || info.Size() > maxRepoMapBytes {
			return nil
		}
		syms := symbolsForFile(path)
		if len(syms) > 0 {
			m.Symbols = append(m.Symbols, repoFileSymbols{Path: rel, Symbols: syms})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(m.Files)
	sort.Slice(m.Symbols, func(i, j int) bool { return m.Symbols[i].Path < m.Symbols[j].Path })
	prof := profileProject(root, m.Files)
	m.Profile = &prof
	return m, nil
}

func ignoredRepoDir(name string) bool {
	switch name {
	case ".git", ".evolv", "node_modules", "vendor", "dist", "build", "coverage", "__pycache__":
		return true
	}
	return false
}

func symbolsForFile(path string) []repoSymbol {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return goSymbols(path)
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs":
		return jsSymbols(path)
	}
	return languageSymbols(path)
}

func goSymbols(path string) []repoSymbol {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil
	}
	var out []repoSymbol
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			name := d.Name.Name
			kind := "function"
			if d.Recv != nil {
				kind = "method"
				name = receiverName(d.Recv.List[0].Type) + "." + name
			}
			out = append(out, repoSymbol{Kind: kind, Name: name, Line: fset.Position(d.Pos()).Line})
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				if t, ok := spec.(*ast.TypeSpec); ok {
					kind := "type"
					if _, ok := t.Type.(*ast.InterfaceType); ok {
						kind = "interface"
					}
					out = append(out, repoSymbol{Kind: kind, Name: t.Name.Name, Line: fset.Position(t.Pos()).Line})
				}
			}
		}
	}
	return out
}

func receiverName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.StarExpr:
		return receiverName(x.X)
	case *ast.IndexExpr:
		return receiverName(x.X)
	case *ast.IndexListExpr:
		return receiverName(x.X)
	}
	return "receiver"
}

func jsSymbols(path string) []repoSymbol {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := string(raw)
	var out []repoSymbol
	seen := map[string]bool{}
	for _, p := range jsSymbolPatterns {
		for _, match := range p.re.FindAllStringSubmatchIndex(text, -1) {
			name := text[match[2]:match[3]]
			line := 1 + strings.Count(text[:match[0]], "\n")
			key := p.kind + "\x00" + name + "\x00" + strconv.Itoa(line)
			if !seen[key] {
				seen[key] = true
				out = append(out, repoSymbol{Kind: p.kind, Name: name, Line: line})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

func writeRepoMapCache(a *Agent, m *repoMap) (string, error) {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	path := a.ws.Dir(workspace.RepoMapFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}
