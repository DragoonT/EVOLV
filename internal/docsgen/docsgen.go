// Package docsgen turns README.md into a standalone single-page docs site.
// It extracts `##` sections, renders a pragmatic markdown subset (headings,
// paragraphs, lists, tables, fenced code, bold/italic/inline code, links)
// and wraps everything in an inline-styled dark page with a sticky TOC.
package docsgen

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// Section is one `## Title` chunk of the README.
type Section struct {
	Title string
	Slug  string
	HTML  string
}

var (
	reH2     = regexp.MustCompile(`(?m)^## (.+)$`)
	reH3     = regexp.MustCompile(`(?m)^### (.+)$`)
	reLi     = regexp.MustCompile(`(?m)^[-*] (.+)$`)
	reBold   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reItalic = regexp.MustCompile(`(^|[\s(])\*([^*\n]+)\*`)
	reCode   = regexp.MustCompile("`([^`]+)`")
	reLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	reTable  = regexp.MustCompile(`^\|.*\|$`)
	reTSeper = regexp.MustCompile(`^\|[\s:|-]+\|$`)
)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// inline renders bold / italic / code / links inside one line.
func inline(s string) string {
	s = html.EscapeString(s)
	s = reCode.ReplaceAllStringFunc(s, func(m string) string {
		return "<code>" + strings.Trim(m, "`") + "</code>"
	})
	s = reBold.ReplaceAllString(s, "<b>$1</b>")
	s = reItalic.ReplaceAllString(s, "$1<i>$2</i>")
	s = reLink.ReplaceAllString(s, `<a href="$2">$1</a>`)
	return s
}

// Render converts README markdown into sections of HTML.
func Render(readme string) []Section {
	// Repositories checked out with core.autocrlf use CRLF. Normalize before
	// parsing so generated headings and code blocks never retain stray '\r'
	// bytes or fail git diff --check.
	readme = strings.ReplaceAll(readme, "\r\n", "\n")
	readme = strings.ReplaceAll(readme, "\r", "\n")
	// drop the leading `# ` title (the page header shows it) and HTML comments
	readme = regexp.MustCompile(`(?m)^# .*$`).ReplaceAllString(readme, "")
	readme = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(readme, "")

	parts := reH2.Split(readme, -1)
	titles := reH2.FindAllStringSubmatch(readme, -1)
	var out []Section
	for i, t := range titles {
		title := strings.TrimSpace(t[1])
		body := parts[i+1]
		out = append(out, Section{
			Title: title,
			Slug:  slugify(title),
			HTML:  renderBody(body),
		})
	}
	return out
}

func renderBody(body string) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	var b strings.Builder
	var table []string
	flushTable := func() {
		if len(table) == 0 {
			return
		}
		b.WriteString("<table>")
		for i, row := range table {
			if reTSeper.MatchString(row) {
				continue
			}
			cells := strings.Split(strings.Trim(row, "|"), "|")
			tag := "td"
			if i == 0 {
				tag = "th"
			}
			b.WriteString("<tr>")
			for _, c := range cells {
				fmt.Fprintf(&b, "<%s>%s</%s>", tag, inline(strings.TrimSpace(c)), tag)
			}
			b.WriteString("</tr>")
		}
		b.WriteString("</table>")
		table = nil
	}

	inCode := false
	var code strings.Builder
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			if inCode {
				b.WriteString("<pre><code>" + html.EscapeString(strings.Trim(code.String(), "\n")) + "</code></pre>")
				code.Reset()
			}
			inCode = !inCode
			continue
		case inCode:
			code.WriteString(ln + "\n")
			continue
		}
		if reTable.MatchString(trimmed) {
			table = append(table, trimmed)
			continue
		}
		flushTable()
		switch {
		case trimmed == "":
			// paragraph break — nothing to emit
		case reH3.MatchString(ln):
			t := reH3.FindStringSubmatch(ln)[1]
			fmt.Fprintf(&b, "<h3 id=%q>%s</h3>", slugify(t), inline(t))
		case reLi.MatchString(ln):
			t := reLi.FindStringSubmatch(ln)[1]
			fmt.Fprintf(&b, "<li>%s</li>", inline(t))
		default:
			fmt.Fprintf(&b, "<p>%s</p>", inline(trimmed))
		}
	}
	if inCode {
		b.WriteString("<pre><code>" + html.EscapeString(strings.Trim(code.String(), "\n")) + "</code></pre>")
	}
	flushTable()
	return b.String()
}

// Page wraps sections into the full standalone HTML document.
func Page(title, tagline string, sections []Section) string {
	var toc, body strings.Builder
	for _, s := range sections {
		fmt.Fprintf(&toc, `<a href="#%s">%s</a>`, s.Slug, html.EscapeString(s.Title))
		fmt.Fprintf(&body, `<section id=%q><h2>%s</h2>%s</section>`, s.Slug, html.EscapeString(s.Title), s.HTML)
	}
	return fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s — docs</title>
<style>
:root{--bg:#0b0e17;--bg2:#111625;--line:#232a3d;--text:#e8ebf2;--dim:#8b93a8;--accent:#d97757}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);
font:15px/1.65 -apple-system,'Segoe UI',Roboto,Helvetica,Arial,sans-serif}
header{padding:64px 24px 40px;text-align:center;border-bottom:1px solid var(--line)}
header h1{font-size:44px;margin:0;letter-spacing:-1px}
header p{color:var(--accent);margin:10px 0 0}
.wrap{display:flex;max-width:1060px;margin:0 auto;gap:36px;padding:32px 24px 80px}
nav{width:230px;flex-shrink:0;position:sticky;top:24px;align-self:flex-start;
display:flex;flex-direction:column;gap:2px}
nav a{color:var(--dim);text-decoration:none;font-size:13px;padding:5px 10px;border-radius:7px}
nav a:hover{background:var(--bg2);color:var(--text)}
main{flex:1;min-width:0}
section{margin-bottom:44px}
h2{font-size:26px;border-bottom:1px solid var(--line);padding-bottom:10px}
h3{font-size:17px;margin-top:28px}
p,li{color:#c6ccd9}
code{background:var(--bg2);border:1px solid var(--line);padding:1px 6px;border-radius:5px;
font:13px ui-monospace,'SF Mono',Menlo,monospace;color:#9fd3b8}
pre{background:var(--bg2);border:1px solid var(--line);border-radius:10px;padding:14px 16px;overflow-x:auto}
pre code{border:none;background:none;padding:0;color:#c6ccd9}
table{border-collapse:collapse;width:100%%;font-size:13.5px;margin:14px 0}
th,td{border:1px solid var(--line);padding:7px 11px;text-align:left;vertical-align:top}
th{background:var(--bg2);color:var(--text)}
a{color:var(--accent)}
@media(max-width:800px){nav{display:none}.wrap{padding:20px 16px 60px}header h1{font-size:32px}}
</style></head><body>
<header><h1>%s</h1><p>%s</p></header>
<div class="wrap"><nav aria-label="contents">%s</nav><main>%s</main></div>
</body></html>`,
		html.EscapeString(title), html.EscapeString(title), html.EscapeString(tagline),
		toc.String(), body.String())
}
