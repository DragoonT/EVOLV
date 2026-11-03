package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSearchCodeAction(t *testing.T) {
	a := testAgent(t)
	proj := t.TempDir()
	a.ws.Config.ProjectDir = proj

	files := map[string]string{
		"server.go":      "package main\n\nfunc StartServer() {\n\t// database_url is loaded here\n\tconn := \"postgres://localhost/db\"\n}\n",
		"config.yaml":    "database_url: postgres://localhost/db\nport: 8080\n",
		"README.md":      "# Project\nSet database_url in config.yaml before starting.\n",
		"server_test.go": "package main\n\nfunc TestStartServer(t *testing.T) {\n\t// test connection\n}\n",
	}
	for name, content := range files {
		full := filepath.Join(proj, name)
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Basic search_code across project
	res := execOK(t, a, action{
		Action: "search_code",
		Query:  "database_url",
	})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("search_code failed: %v", res)
	}
	hits, _ := res["hits"].([]any)
	if len(hits) < 3 {
		t.Fatalf("expected at least 3 hits for database_url, got %d", len(hits))
	}
	count, _ := res["count"].(float64)
	if int(count) != len(hits) {
		t.Fatalf("count %v != len(hits) %d", count, len(hits))
	}

	// 2. Filter by extension
	resExt := execOK(t, a, action{
		Action: "search_code",
		Query:  "database_url",
		Exts:   []string{"yaml"},
	})
	if ok, _ := resExt["ok"].(bool); !ok {
		t.Fatalf("search_code ext filter failed: %v", resExt)
	}
	hitsExt, _ := resExt["hits"].([]any)
	if len(hitsExt) != 1 {
		t.Fatalf("expected 1 hit in config.yaml, got %d", len(hitsExt))
	}
	hit0 := hitsExt[0].(map[string]any)
	if hit0["path"] != "config.yaml" {
		t.Fatalf("expected path config.yaml, got %s", hit0["path"])
	}

	// 3. Regex search
	resRegex := execOK(t, a, action{
		Action: "search_code",
		Query:  `func\s+Start\w+`,
		Regex:  true,
	})
	if ok, _ := resRegex["ok"].(bool); !ok {
		t.Fatalf("search_code regex failed: %v", resRegex)
	}
	hitsRegex, _ := resRegex["hits"].([]any)
	if len(hitsRegex) == 0 {
		t.Fatal("expected regex hit for StartServer")
	}

	// 4. grep alias works identically
	resGrep := execOK(t, a, action{
		Action: "grep",
		Query:  "port: 8080",
	})
	if ok, _ := resGrep["ok"].(bool); !ok {
		t.Fatalf("grep alias failed: %v", resGrep)
	}
	hitsGrep, _ := resGrep["hits"].([]any)
	if len(hitsGrep) != 1 {
		t.Fatalf("expected 1 hit for grep alias, got %d", len(hitsGrep))
	}

	// 5. Empty query rejected cleanly
	resEmpty := execOK(t, a, action{
		Action: "search_code",
		Query:  "",
	})
	if ok, _ := resEmpty["ok"].(bool); ok {
		t.Fatal("expected error on empty query, got ok: true")
	}
}
