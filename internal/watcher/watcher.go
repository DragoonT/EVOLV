// Package watcher watches the living filesystem and emits bus events when
// anything mutates. This is what makes "the app rewrites itself while you
// watch" possible: agent writes file -> watcher fires -> WS push -> browser
// hot-swaps, no refresh.
package watcher

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dxn111/EVOLV/internal/bus"
)

// Change describes one filesystem mutation.
type Change struct {
	Path string `json:"path"` // workspace-relative
	Kind string `json:"kind"` // create | write | remove | rename
	Zone string `json:"zone"` // ui | components | tools | skills | prompts | plugins | other
}

// Watcher polls the workspace tree (cheap, dependency-free, portable) and
// publishes fs:change events on the bus. Poll interval is fast enough to
// feel instant (150ms) while costing almost nothing.
type Watcher struct {
	root     string
	interval time.Duration
	bus      *bus.Bus
	seen     map[string]string // path -> mtime+size signature
	stop     chan struct{}
}

// New builds a Watcher rooted at root.
func New(root string, b *bus.Bus) *Watcher {
	return &Watcher{
		root:     root,
		interval: 150 * time.Millisecond,
		bus:      b,
		seen:     map[string]string{},
		stop:     make(chan struct{}),
	}
}

// Start begins watching in the background.
func (w *Watcher) Start() {
	w.snapshot() // baseline so boot doesn't spam events
	go w.loop()
}

// Stop halts the watcher.
func (w *Watcher) Stop() { close(w.stop) }

func (w *Watcher) loop() {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
			w.scan()
		}
	}
}

func sig(info os.FileInfo) string {
	return info.ModTime().Format(time.RFC3339Nano) + ":" + itoa(info.Size())
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func (w *Watcher) snapshot() {
	_ = filepath.WalkDir(w.root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			w.seen[p] = sig(info)
		}
		return nil
	})
}

func (w *Watcher) scan() {
	current := map[string]string{}
	var events []Change
	_ = filepath.WalkDir(w.root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".tmp") || strings.Contains(p, "/logs/") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		s := sig(info)
		current[p] = s
		old, existed := w.seen[p]
		rel, _ := filepath.Rel(w.root, p)
		switch {
		case !existed:
			events = append(events, Change{Path: rel, Kind: "create", Zone: zone(rel)})
		case old != s:
			events = append(events, Change{Path: rel, Kind: "write", Zone: zone(rel)})
		}
		return nil
	})
	for p := range w.seen {
		if _, ok := current[p]; !ok {
			rel, _ := filepath.Rel(w.root, p)
			events = append(events, Change{Path: rel, Kind: "remove", Zone: zone(rel)})
		}
	}
	w.seen = current
	for _, ev := range events {
		w.bus.Publish("fs:change", ev)
	}
}

// zone classifies a workspace-relative path into an organism region.
func zone(rel string) string {
	rel = filepath.ToSlash(rel)
	switch {
	case strings.HasPrefix(rel, "workspace/ui/components/"):
		return "components"
	case strings.HasPrefix(rel, "workspace/ui/"):
		return "ui"
	case strings.HasPrefix(rel, "workspace/tools/"):
		return "tools"
	case strings.HasPrefix(rel, "workspace/skills/"):
		return "skills"
	case strings.HasPrefix(rel, "workspace/prompts/"):
		return "prompts"
	case strings.HasPrefix(rel, "workspace/plugins/"):
		return "plugins"
	default:
		return "other"
	}
}
