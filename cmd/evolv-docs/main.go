// evolv-docs regenerates docs/index.html from README.md.
// Usage: go run ./cmd/evolv-docs   (run from the repo root)
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dxn111/EVOLV/internal/docsgen"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "read README:", err)
		os.Exit(1)
	}
	secs := docsgen.Render(string(readme))
	if len(secs) == 0 {
		fmt.Fprintln(os.Stderr, "no ## sections found in README")
		os.Exit(1)
	}
	page := docsgen.Page("EVOLV", "software that writes itself — one binary · zero dependencies · it edits its own code", secs)
	out := filepath.Join(root, "docs", "index.html")
	if err := os.WriteFile(out, []byte(page), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	fmt.Printf("docs: wrote %s (%d sections, %d bytes)\n", out, len(secs), len(page))
}
