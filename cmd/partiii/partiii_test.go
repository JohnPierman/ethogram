package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// renderedHTML and renderedMarkdown execute the real render paths once per call, so
// every test exercises the code a user of -out and -out-md runs.
func renderedHTML(t *testing.T) string {
	t.Helper()
	var buf strings.Builder
	if err := paperTemplate.Execute(&buf, viewOf(document(nil))); err != nil {
		t.Fatalf("executing template: %v", err)
	}
	return buf.String()
}

func renderedMarkdown() string { return markdownDocument(document(nil)) }

// ---------------------------------------------------------------------------
// 1. The HTML is a complete standalone document with its encoding declared.
// ---------------------------------------------------------------------------

func TestRenderedHTMLIsCompleteDocument(t *testing.T) {
	html := renderedHTML(t)

	if !strings.HasPrefix(html, "<!doctype html>") {
		t.Error("document must begin with <!doctype html>")
	}
	if !strings.Contains(html, `<meta charset="utf-8">`) {
		t.Error("document must declare its encoding: the prose is full of section signs " +
			"and Greek letters, and an undeclared local file falls back to a locale default")
	}
	if !strings.Contains(html, "<title>") {
		t.Error("document must carry a <title>")
	}
	if !strings.Contains(html, `<html lang="en">`) {
		t.Error("document must open <html lang=\"en\">")
	}
	if !strings.HasSuffix(strings.TrimSpace(html), "</html>") {
		t.Error("document must close </html>")
	}
}

// ---------------------------------------------------------------------------
// 2. No template.HTML anywhere, and auto-escaping demonstrably applied.
// ---------------------------------------------------------------------------

func TestSourceUsesNoTemplateHTML(t *testing.T) {
	// AST-based rather than textual, so the doc comments may name the forbidden type
	// while the code cannot use it.
	fset := token.NewFileSet()
	for _, name := range []string{"main.go", "template.go"} {
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if pkg.Name == "template" && sel.Sel.Name == "HTML" {
				t.Errorf("%s: template.HTML used at %s; all content must pass through "+
					"auto-escaping", name, fset.Position(sel.Pos()))
			}
			return true
		})
	}
}

func TestHTMLEscapesContent(t *testing.T) {
	html := renderedHTML(t)

	// The content deliberately contains a literal "< 25" (the McNemar exact-test
	// bound) and a literal "<graph>" (the partition path). Both must arrive escaped.
	if !strings.Contains(html, "&lt; 25") {
		t.Error("the literal \"< 25\" in the content must render escaped as \"&lt; 25\"")
	}
	if strings.Contains(html, "< 25") {
		t.Error("a raw \"< 25\" reached the HTML output; escaping is not being applied")
	}
	if !strings.Contains(html, "&lt;graph&gt;") {
		t.Error("the literal \"<graph>\" in the content must render escaped inside its code span")
	}
}

// ---------------------------------------------------------------------------
// 3. Rendering is deterministic: byte-identical output on repeated renders.
// ---------------------------------------------------------------------------

func TestRenderingsAreDeterministic(t *testing.T) {
	if a, b := renderedHTML(t), renderedHTML(t); a != b {
		t.Error("two HTML renders differ; the document must carry no timestamp or " +
			"environment-dependent value")
	}
	if a, b := renderedMarkdown(), renderedMarkdown(); a != b {
		t.Error("two Markdown renders differ; the document must carry no timestamp or " +
			"environment-dependent value")
	}
}

// ---------------------------------------------------------------------------
// 4. Every section has a non-empty heading and at least one paragraph or table,
//    and so does every subsection.
// ---------------------------------------------------------------------------

func TestEverySectionHasHeadingAndBody(t *testing.T) {
	sections := document(nil)
	if len(sections) == 0 {
		t.Fatal("document has no sections")
	}
	for i, s := range sections {
		if s.Number == "" || s.Title == "" {
			t.Errorf("section %d: empty number or title", i)
		}
		if want := fmt.Sprintf("%d", i+1); s.Number != want {
			t.Errorf("section %d: number %q, want %q (sections must be numbered in order)",
				i, s.Number, want)
		}
		if len(s.Paras) == 0 && s.Table == nil {
			t.Errorf("section %s %q: no paragraphs and no table", s.Number, s.Title)
		}
		for _, sub := range s.Subs {
			if sub.Title == "" {
				t.Errorf("section %s: subsection with empty title", s.Number)
			}
			if len(sub.Paras) == 0 && sub.Table == nil {
				t.Errorf("section %s, subsection %q: no paragraphs and no table", s.Number, sub.Title)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 5. The coverage table reports the below-target packages and marks them.
// ---------------------------------------------------------------------------

// coverageProfile writes a synthetic `go test -coverprofile` file.
//
// Two packages, with counts chosen so one clears the 80% target and the other does not:
// `domain/kept` covers 9 of 10 statements, `domain/thin` covers 1 of 4.
func coverageProfile(t *testing.T) string {
	t.Helper()
	body := "mode: atomic\n" +
		modulePath + "/domain/kept/a.go:1.1,2.2 9 3\n" +
		modulePath + "/domain/kept/b.go:1.1,2.2 1 0\n" +
		modulePath + "/domain/thin/c.go:1.1,2.2 1 7\n" +
		modulePath + "/domain/thin/c.go:5.1,6.2 3 0\n"
	path := filepath.Join(t.TempDir(), "coverage.out")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCoverageTableIsMeasuredFromAProfileRatherThanWritten(t *testing.T) {
	// The figures here used to be maintained by hand inside a generated document, and
	// they drifted: cooccurrence was written as 75.5% while go test -cover said 72.6%.
	// Reading them out of the profile makes drift unrepresentable.
	measured, err := readCoverage(coverageProfile(t), modulePath)
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]packageCoverage{}
	for _, entry := range measured {
		got[entry.Package] = entry
	}
	if kept := got["domain/kept"]; kept.Covered != 9 || kept.Total != 10 ||
		kept.Percent < 89.9 || kept.Percent > 90.1 {
		t.Errorf("domain/kept = %+v, want 9 of 10 at 90%%", kept)
	}
	if thin := got["domain/thin"]; thin.Covered != 1 || thin.Total != 4 ||
		thin.Percent < 24.9 || thin.Percent > 25.1 {
		t.Errorf("domain/thin = %+v, want 1 of 4 at 25%%", thin)
	}
	// Descending by coverage, so a reader sees the shortfalls at the bottom in a stable
	// order and two renderings of one profile produce identical bytes.
	if measured[0].Package != "domain/kept" {
		t.Errorf("ordering = %v, want the best-covered package first", measured)
	}

	var testing12 *section
	sections := document(measured)
	for i := range sections {
		if sections[i].Number == "12" {
			testing12 = &sections[i]
		}
	}
	if testing12 == nil || testing12.Table == nil {
		t.Fatal("section 12 with its coverage table not found")
	}
	marks := map[string]string{}
	for _, row := range testing12.Table.Rows {
		if len(row) != 3 {
			t.Fatalf("coverage row %v: want 3 cells", row)
		}
		marks[row[0]] = row[2]
	}
	if marks["`domain/kept`"] != "met" {
		t.Errorf("a package above the target is marked %q", marks["`domain/kept`"])
	}
	if marks["`domain/thin`"] != "below target" {
		t.Errorf("a package below the target must be marked as such, got %q â€” "+
			"shortfalls are reported honestly, not hidden", marks["`domain/thin`"])
	}
	// The shortfall must be named in the prose too, not left to the table alone.
	if !strings.Contains(coverageParagraph(measured), "`domain/thin`") {
		t.Error("the below-target package is not named in the section's prose")
	}
}

func TestCoverageRendersNotMeasuredWithoutAProfile(t *testing.T) {
	// The same rule the report renderer applies to a missing result: an absent
	// measurement renders as NOT MEASURED, never as a stale number.
	var testing12 *section
	sections := document(nil)
	for i := range sections {
		if sections[i].Number == "12" {
			testing12 = &sections[i]
		}
	}
	if testing12 == nil {
		t.Fatal("section 12 not found")
	}
	if testing12.Table != nil {
		t.Error("a coverage table was rendered with no profile to measure it from")
	}
	joined := strings.Join(testing12.Paras, "\n")
	if !strings.Contains(joined, "NOT MEASURED") {
		t.Error("the absence of coverage is not declared; a reader cannot tell an " +
			"unmeasured document from a fully covered one")
	}

	html, md := renderedHTML(t), renderedMarkdown()
	for _, fragment := range []string{"NOT MEASURED"} {
		if !strings.Contains(html, fragment) {
			t.Errorf("HTML rendering lost %q", fragment)
		}
		if !strings.Contains(md, fragment) {
			t.Errorf("Markdown rendering lost %q", fragment)
		}
	}
}

func TestAMalformedCoverageProfileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.out")
	if err := os.WriteFile(path, []byte("mode: atomic\nnonsense\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCoverage(path, modulePath); err == nil {
		t.Error("a malformed coverage profile was accepted; the document would then " +
			"carry figures derived from a file nobody could parse")
	}
}

// ---------------------------------------------------------------------------
// 6. Both renderings carry the same section and subsection headings in the same
//    order, so an edit to one cannot silently omit a section from the other.
// ---------------------------------------------------------------------------

func TestHeadingParityBetweenRenderings(t *testing.T) {
	html, md := renderedHTML(t), renderedMarkdown()

	htmlAt, mdAt := 0, 0
	advance := func(doc string, from int, needle, what string) int {
		idx := strings.Index(doc[from:], needle)
		if idx < 0 {
			t.Fatalf("%s: heading %q missing or out of order", what, needle)
		}
		return from + idx + len(needle)
	}

	for _, s := range document(nil) {
		h2 := `<h2><span class="sid">` + s.Number + `</span>` + s.Title + `</h2>`
		htmlAt = advance(html, htmlAt, h2, "HTML")
		mdAt = advance(md, mdAt, "\n## "+s.Number+". "+s.Title+"\n", "Markdown")

		for _, sub := range s.Subs {
			htmlAt = advance(html, htmlAt, "<h3>"+sub.Title+"</h3>", "HTML")
			mdAt = advance(md, mdAt, "\n### "+sub.Title+"\n", "Markdown")
		}
	}
}

// ---------------------------------------------------------------------------
// 7. The Markdown contains no raw HTML tags, and its table of contents resolves.
// ---------------------------------------------------------------------------

// stripCode removes fenced blocks and inline code spans, which may legitimately
// carry angle brackets, leaving only the prose that must be HTML-free.
func stripCode(md string) string {
	var prose strings.Builder
	for i, fenced := range strings.Split(md, "```") {
		if i%2 == 1 {
			continue // inside a fenced block
		}
		for j, span := range strings.Split(fenced, "`") {
			if j%2 == 1 {
				continue // inside an inline code span
			}
			prose.WriteString(span)
		}
	}
	return prose.String()
}

func TestMarkdownHasNoRawHTML(t *testing.T) {
	md := renderedMarkdown()
	tag := regexp.MustCompile(`<[A-Za-z/!]`)
	if loc := tag.FindStringIndex(stripCode(md)); loc != nil {
		start := loc[0]
		end := min(start+60, len(stripCode(md)))
		t.Errorf("Markdown contains what reads as a raw HTML tag near: %q", stripCode(md)[start:end])
	}
}

func TestMarkdownTOCAnchorsMatchHeadings(t *testing.T) {
	md := renderedMarkdown()
	if !strings.Contains(md, "\n## Contents\n") {
		t.Fatal("Markdown must open with a Contents section")
	}
	for _, s := range document(nil) {
		heading := mdSectionHeading(s)
		link := "(#" + slugOf(heading) + ")"
		if !strings.Contains(md, link) {
			t.Errorf("table of contents carries no link %q for section %q", link, heading)
		}
	}
}

// ---------------------------------------------------------------------------
// 8. The inline-code parser, which is the only markup mechanism.
// ---------------------------------------------------------------------------

func TestParseInline(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []inline
	}{
		{"plain", "no code here", []inline{{Text: "no code here"}}},
		{"one span", "the `Score` method", []inline{
			{Text: "the "}, {Text: "Score", Code: true}, {Text: " method"},
		}},
		{"leading span", "`LogP` is ln P", []inline{
			{Text: "LogP", Code: true}, {Text: " is ln P"},
		}},
		{"adjacent spans", "`a``b`", []inline{
			{Text: "a", Code: true}, {Text: "b", Code: true},
		}},
		{"unterminated", "a stray ` backtick", []inline{
			{Text: "a stray "}, {Text: " backtick"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseInline(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("parseInline(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("parseInline(%q)[%d] = %v, want %v", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 9. The render entry points write both artefacts.
// ---------------------------------------------------------------------------

func TestRenderWritesBothArtefacts(t *testing.T) {
	dir := t.TempDir()
	htmlPath := filepath.Join(dir, "part-iii.html")
	mdPath := filepath.Join(dir, "IMPLEMENTATION.md")

	if err := renderHTML(htmlPath, nil); err != nil {
		t.Fatalf("renderHTML: %v", err)
	}
	if err := renderMarkdown(mdPath, nil); err != nil {
		t.Fatalf("renderMarkdown: %v", err)
	}
	for _, p := range []string{htmlPath, mdPath} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if info.Size() == 0 {
			t.Errorf("%s was written empty", p)
		}
	}
}
