package docsgen

import (
	"strings"
	"testing"
)

const sample = `# EVOLV

<!-- hidden comment must be dropped -->

## First section

Intro line with **bold**, *italic*, ` + "`inline`" + ` and [a link](https://example.com).

Some prose.

- item one
- item two

### Sub heading

` + "```sh\n" + `evolv --port 8080
echo "hello <script>"
` + "```\n" + `

| Version | What |
|---|---|
| v1.0 | one |
| v2.0 | two |

## Second section

Tail prose.
`

func TestRenderSections(t *testing.T) {
	secs := Render(sample)
	if len(secs) != 2 {
		t.Fatalf("want 2 sections, got %d: %+v", len(secs), secs)
	}
	if secs[0].Title != "First section" || secs[0].Slug != "first-section" {
		t.Fatalf("bad first section: %+v", secs[0])
	}
	if !strings.Contains(secs[0].HTML, "<li>item one</li>") {
		t.Error("list items not rendered")
	}
	if !strings.Contains(secs[0].HTML, `<h3 id="sub-heading">`) {
		t.Error("h3 anchor missing")
	}
	if !strings.Contains(secs[0].HTML, "hello &lt;script&gt;") || strings.Contains(secs[0].HTML, "<script>") {
		t.Error("code fence not HTML-escaped")
	}
	if strings.Contains(secs[0].HTML, "&lt;b&gt;") || !strings.Contains(secs[0].HTML, "<b>bold</b>") {
		t.Error("inline bold broken")
	}
	if !strings.Contains(secs[0].HTML, `<a href="https://example.com">a link</a>`) {
		t.Error("link broken")
	}
	// tables
	if !strings.Contains(secs[0].HTML, "<th>Version</th>") || !strings.Contains(secs[0].HTML, "<td>v2.0</td>") {
		t.Error("table not rendered as th/td")
	}
	if strings.Contains(secs[0].HTML, "<tr><td>---") || strings.Contains(secs[0].HTML, "|---|") {
		t.Error("separator row leaked")
	}
	// comment dropped entirely
	full := Page("t", "d", Render(sample))
	if strings.Contains(full, "hidden comment") {
		t.Error("comment leaked")
	}
}

func TestPageWellformed(t *testing.T) {
	p := Page("EVOLV", "software that writes itself", Render(sample))
	for _, want := range []string{
		`<h1>EVOLV</h1>`, `href="#first-section"`, `<section id="second-section">`,
		"<!doctype html>", `</html>`,
	} {
		if !strings.Contains(p, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(p, "%%") {
		t.Error("unexpanded fmt %% leaked into CSS")
	}
	if n := strings.Count(p, "<section"); n != 2 {
		t.Errorf("want 2 <section>, got %d", n)
	}
}

func TestNoRawMarkdownLeaks(t *testing.T) {
	p := Page("t", "d", Render(sample))
	for _, leak := range []string{"**bold**", "|---|", "```"} {
		if strings.Contains(p, leak) {
			t.Errorf("raw markdown leaked: %q", leak)
		}
	}
}

func TestRenderNormalizesCRLF(t *testing.T) {
	crlf := strings.ReplaceAll(sample, "\n", "\r\n")
	p := Page("t", "d", Render(crlf))
	if strings.Contains(p, "\r") {
		t.Fatal("generated documentation retained carriage returns")
	}
}
