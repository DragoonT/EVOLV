package genome

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Small filesystem helpers kept here so the package has one implementation of
// atomic writes and JSON encoding.

func workspacePath(root, rel string) string { return filepath.Join(root, rel) }

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }

func jsonUnmarshal(raw []byte, v any) error { return json.Unmarshal(raw, v) }

func mustJSON(v any) []byte {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return []byte("{}")
	}
	return raw
}

func writeFileAtomic(path string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
