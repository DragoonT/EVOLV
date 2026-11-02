package agent

import (
	"encoding/json"
	"sort"
	"strings"
)

// packageJSON is the slice of a package.json that matters for understanding a
// project: its scripts and its declared dependencies (used to infer the
// framework families actually in play).
type packageJSON struct {
	Name            string            `json:"name"`
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	Workspaces      []string          `json:"workspaces"`
}

// readPackageScripts returns a package.json's scripts, or false when the file
// is missing or unreadable. It never returns partial JSON.
func readPackageScripts(path string) (map[string]string, bool) {
	pkg, err := loadPackageJSON(path)
	if err != nil || len(pkg.Scripts) == 0 {
		return nil, false
	}
	return pkg.Scripts, true
}

// frameworksFromPackage infers framework families from declared deps.
func frameworksFromPackage(path string) []string {
	pkg, err := loadPackageJSON(path)
	if err != nil {
		return nil
	}
	return frameworksOf(pkg)
}

func loadPackageJSON(path string) (*packageJSON, error) {
	raw, err := readBounded(path)
	if err != nil {
		return nil, err
	}
	var pkg packageJSON
	if err := json.Unmarshal(raw, &pkg); err != nil {
		return nil, err
	}
	return &pkg, nil
}

// knownFrameworks maps a dependency name to the family it implies.
var knownFrameworks = []struct {
	dep    string
	family string
}{
	{"next", "next"}, {"react", "react"}, {"vue", "vue"}, {"svelte", "svelte"},
	{"@angular/core", "angular"}, {"express", "express"}, {"fastify", "fastify"},
	{"koa", "koa"}, {"nestjs", "nestjs"}, {"@nestjs/core", "nestjs"},
	{"vite", "vite"}, {"webpack", "webpack"}, {"jest", "jest"}, {"vitest", "vitest"},
	{"playwright", "playwright"}, {"@playwright/test", "playwright"},
	{"cypress", "cypress"}, {"typescript", "typescript"}, {"tailwindcss", "tailwind"},
}

func frameworksOf(pkg *packageJSON) []string {
	if pkg == nil {
		return nil
	}
	have := map[string]bool{}
	for name := range pkg.Dependencies {
		have[name] = true
	}
	for name := range pkg.DevDependencies {
		have[name] = true
	}
	var out []string
	seen := map[string]bool{}
	for _, fw := range knownFrameworks {
		if have[fw.dep] && !seen[fw.family] {
			seen[fw.family] = true
			out = append(out, fw.family)
		}
	}
	return out
}

// describeCommands renders the command map as a stable, readable block.
func describeCommands(cmds map[string]string) string {
	if len(cmds) == 0 {
		return "unknown — inspect the project for its own build/test entry points"
	}
	names := make([]string, 0, len(cmds))
	for name := range cmds {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	for i, name := range names {
		if i > 0 {
			b.WriteString(" · ")
		}
		b.WriteString(name + "=" + cmds[name])
	}
	return b.String()
}
