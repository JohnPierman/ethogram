package main

import (
	"strings"
	"testing"
)

// renderOnly is the common shape: render a fragment with no figures and return the body.
func renderOnly(t *testing.T, source string) string {
	t.Helper()
	doc, err := renderMarkdown(source, map[string]string{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return doc.Body
}

// ---------------------------------------------------------------------------
// Anchors: the source's own cross-references depend on these
// ---------------------------------------------------------------------------

// TestABrokenCrossReferenceFailsTheRender guards a defect two links had already shipped
// with: §9 and §9.1 were written when the results lived at §9 and stayed behind when they
// moved to §15. A link to a section that does not exist renders as ordinary text and goes
// nowhere when clicked, so reading the page does not reveal it — only resolving every href
// against every id does.
func TestABrokenCrossReferenceFailsTheRender(t *testing.T) {
	source := "# Idiolect\n\n## 15. Results\n\nSee [§9](#9-results) for the numbers.\n"
	if _, _, err := render(source); err == nil {
		t.Fatal("a link to a section that does not exist was rendered without complaint")
	} else if !strings.Contains(err.Error(), "#9-results") {
		t.Errorf("the error does not name the broken target: %v", err)
	}

	// The same document with the link pointing at a heading that exists must render.
	fixed := "# Idiolect\n\n## 15. Results\n\nSee [§15](#15-results) for the numbers.\n"
	if _, _, err := render(fixed); err != nil {
		t.Errorf("a resolving cross-reference was rejected: %v", err)
	}
}

func TestBrokenAnchorsReportsEachTargetOnceAndIgnoresExternalLinks(t *testing.T) {
	page := `<h2 id="a">A</h2><a href="#a">ok</a><a href="#b">no</a><a href="#b">no again</a>` +
		`<a href="https://example.com/#c">external</a>`
	got := brokenAnchors(page)
	if len(got) != 1 || got[0] != "#b" {
		t.Errorf("brokenAnchors = %v, want exactly [#b] — a repeated broken link is one "+
			"defect, and an external URL carrying a fragment is not an internal link", got)
	}
}

// TestSlugMatchesTheSourcesOwnCrossReferences pins the anchor scheme.
//
// The Markdown links to its own sections by hand — "§15.3" points at
// "#153-the-structural-finding-and-a-caveat-that-qualifies-it" — so the slug algorithm is
// not free to change. Without this test a refactor could quietly break every internal link
// in a 1,500-line document, and the page would still render.
func TestSlugMatchesTheSourcesOwnCrossReferences(t *testing.T) {
	cases := map[string]string{
		"15.3 The structural finding, and a caveat that qualifies it": "153-the-structural-finding-and-a-caveat-that-qualifies-it",
		"9.1 Detector I — categorical novelty":                        "91-detector-i--categorical-novelty",
		"4.3 Honest arithmetic about how much is too much":            "43-honest-arithmetic-about-how-much-is-too-much",
		"Part I — the problem":                                        "part-i--the-problem",
		"10.2 Combination, and the finding that overturned it":        "102-combination-and-the-finding-that-overturned-it",
	}
	for heading, want := range cases {
		if got := slug(heading); got != want {
			t.Errorf("slug(%q)\n got %q\nwant %q", heading, got, want)
		}
	}
}

func TestHeadingsCarryAnAnchorAndAreCollected(t *testing.T) {
	doc, err := renderMarkdown("## 2. Motivation\n\ntext\n", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Body, `<h2 id="2-motivation">`) {
		t.Errorf("heading lacks its id:\n%s", doc.Body)
	}
	if len(doc.Headings) != 1 || doc.Headings[0].Level != 2 {
		t.Fatalf("headings = %+v, want one h2", doc.Headings)
	}
	if doc.Headings[0].Plain != "2. Motivation" {
		t.Errorf("plain text = %q", doc.Headings[0].Plain)
	}
}

// ---------------------------------------------------------------------------
// Inline forms
// ---------------------------------------------------------------------------

func TestCodeSpansAreNotFurtherInterpreted(t *testing.T) {
	// The prose quotes Markdown and Go, so emphasis markers inside a code span must stay
	// literal. Getting this wrong silently eats source text.
	body := renderOnly(t, "a `**not bold**` and `a*b` here\n")
	if !strings.Contains(body, "<code>**not bold**</code>") {
		t.Errorf("emphasis inside a code span was interpreted:\n%s", body)
	}
	if strings.Contains(body, "<strong>") {
		t.Errorf("code span produced emphasis:\n%s", body)
	}
}

func TestTextIsEscapedButMarkupIsNot(t *testing.T) {
	body := renderOnly(t, "a < b and 5 > 3 with `x < y`\n")
	if strings.Contains(body, "a < b") {
		t.Errorf("raw < reached the output:\n%s", body)
	}
	if !strings.Contains(body, "&lt;") {
		t.Errorf("< was not escaped:\n%s", body)
	}
}

func TestExternalLinksOpenAwayAndInternalOnesDoNot(t *testing.T) {
	body := renderOnly(t, "see [the paper](https://example.org/x) and [§9](#9-the-detectors)\n")
	if !strings.Contains(body, `href="https://example.org/x" target="_blank" rel="noopener"`) {
		t.Errorf("external link is not opened away with noopener:\n%s", body)
	}
	if strings.Contains(body, `href="#9-the-detectors" target`) {
		t.Errorf("an internal cross-reference must not open a new tab:\n%s", body)
	}
}

// ---------------------------------------------------------------------------
// Blocks
// ---------------------------------------------------------------------------

func TestTablesRenderWithAHeaderAndScrollBox(t *testing.T) {
	body := renderOnly(t, "| a | b |\n|---|---:|\n| 1 | 2 |\n")
	for _, want := range []string{`<div class="tablewrap">`, "<thead>", "<th>a</th>",
		`text-align:right`, "<td>1</td>"} {
		if !strings.Contains(body, want) {
			t.Errorf("table output lacks %q:\n%s", want, body)
		}
	}
	// The alignment row is a rule, not data.
	if strings.Contains(body, "---") {
		t.Errorf("the alignment row was rendered as a cell:\n%s", body)
	}
}

func TestFencedCodeIsVerbatim(t *testing.T) {
	body := renderOnly(t, "```go\nif a < b { **x** }\n```\n")
	if !strings.Contains(body, `class="lang-go"`) {
		t.Errorf("fence language lost:\n%s", body)
	}
	if strings.Contains(body, "<strong>") {
		t.Errorf("emphasis was applied inside a code fence:\n%s", body)
	}
	if !strings.Contains(body, "&lt;") {
		t.Errorf("fence content was not escaped:\n%s", body)
	}
}

func TestAnUnterminatedFenceIsAnError(t *testing.T) {
	if _, err := renderMarkdown("```\nno end\n", map[string]string{}); err == nil {
		t.Error("an unterminated fence was accepted; the rest of the document would be " +
			"swallowed into a code block and still render")
	}
}

func TestListsAndBlockquotesRender(t *testing.T) {
	body := renderOnly(t, "- one\n- two\n\n1. first\n2. second\n\n> quoted\n")
	for _, want := range []string{"<ul>", "<li>one</li>", "<ol>", "<li>first</li>",
		"<blockquote>"} {
		if !strings.Contains(body, want) {
			t.Errorf("output lacks %q:\n%s", want, body)
		}
	}
}

func TestAWrappedListItemStaysOneItem(t *testing.T) {
	// The source wraps long items across lines; each must remain a single <li> or a
	// sentence is split mid-clause into two bullets.
	body := renderOnly(t, "- a claim that runs on\n  and continues here\n- second\n")
	if strings.Count(body, "<li>") != 2 {
		t.Errorf("wrapped item was split; want 2 items:\n%s", body)
	}
	if !strings.Contains(body, "runs on and continues here") {
		t.Errorf("continuation was not joined:\n%s", body)
	}
}

// ---------------------------------------------------------------------------
// Figures
// ---------------------------------------------------------------------------

func TestAFigureAnchorIsReplacedByItsDiagram(t *testing.T) {
	body, err := renderMarkdown("before\n\n<!-- figure: demo -->\n\nafter\n",
		map[string]string{"demo": "<figure>DIAGRAM</figure>"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.Body, "<figure>DIAGRAM</figure>") {
		t.Errorf("the anchor was not replaced:\n%s", body.Body)
	}
	if strings.Contains(body.Body, "<!--") {
		t.Errorf("the anchor comment survived into the output:\n%s", body.Body)
	}
}

// TestAnUnknownFigureIsAnErrorNotAnOmission is the point of the anchor mechanism. A document
// that silently drops a diagram reads as complete, which is how a missing figure survives
// review — the same reason a hypothesis with no result renders NOT RUN rather than blank.
func TestAnUnknownFigureIsAnErrorNotAnOmission(t *testing.T) {
	_, err := renderMarkdown("<!-- figure: nonexistent -->\n", map[string]string{})
	if err == nil {
		t.Fatal("an anchor naming an undefined figure was silently ignored")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("the error does not name the missing figure: %v", err)
	}
}

func TestEveryDefinedFigureIsWellFormedAndAccessible(t *testing.T) {
	for id, svg := range figures() {
		if !strings.Contains(svg, "<figcaption>") {
			t.Errorf("figure %q has no caption; the claim it makes must be stated", id)
		}
		if !strings.Contains(svg, `role="img"`) || !strings.Contains(svg, "aria-label=") {
			t.Errorf("figure %q is unreadable to a screen reader: needs role and aria-label", id)
		}
		if !strings.Contains(svg, "viewBox=") {
			t.Errorf("figure %q has no viewBox, so it cannot scale", id)
		}
		// Self-contained: a diagram that reaches outside the page cannot be trusted to
		// render from disk, and a script inside an SVG is not a diagram.
		for _, forbidden := range []string{"<script", "<foreignObject", "<style", "http://", "https://"} {
			if strings.Contains(svg, forbidden) {
				t.Errorf("figure %q contains %q; figures must be self-contained", id, forbidden)
			}
		}
		if strings.Count(svg, "<figure") != 1 || strings.Count(svg, "</figure>") != 1 {
			t.Errorf("figure %q is not wrapped in exactly one <figure>", id)
		}
	}
}

// ---------------------------------------------------------------------------
// The page
// ---------------------------------------------------------------------------

func TestTheContentsSkipsTheSubtitleAndDeepHeadings(t *testing.T) {
	toc := contents([]Heading{
		{Level: 1, Plain: "Idiolect", Slug: "idiolect"},
		{Level: 3, Plain: "A subtitle restating the title", Slug: "sub"},
		{Level: 2, Plain: "1. The space", Slug: "1-the-space"},
		{Level: 3, Plain: "1.1 Something", Slug: "11-something"},
		{Level: 4, Plain: "A sub-point", Slug: "a-sub-point"},
	})
	if strings.Contains(toc, "A subtitle restating") {
		t.Error("the title block's subtitle reached the contents")
	}
	if strings.Contains(toc, "A sub-point") {
		t.Error("an h4 reached the contents")
	}
	for _, want := range []string{"Idiolect", "1. The space", "1.1 Something"} {
		if !strings.Contains(toc, want) {
			t.Errorf("contents lacks %q", want)
		}
	}
}

func TestRenderIsDeterministicAndSelfContained(t *testing.T) {
	source := "# Title\n\n## 1. One\n\ntext with `code`\n\n<!-- figure: novelty-tail -->\n"
	first, _, err := render(source)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := render(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Error("two renders of the same source differ; the page must carry no timestamp")
	}
	page := string(first)
	if strings.Contains(page, "<script") {
		t.Error("the page contains a script; it is a document, not an application")
	}
	// No external resource: it must open from disk and survive being copied anywhere.
	for _, forbidden := range []string{`src="http`, `href="http` + `://cdn`, "@import"} {
		if strings.Contains(page, forbidden) {
			t.Errorf("the page reaches for an external resource: %q", forbidden)
		}
	}
	// Both dark paths must exist: an explicit choice stamps data-theme, while the system
	// default stamps nothing and is reachable only through the media query. A page defining
	// one of them is wrong on the other ground. Matched on fragments that avoid the CSS
	// feature's American spelling, which the repository's spell check would flag as prose.
	if !strings.Contains(page, "@media (prefers-") || !strings.Contains(page, `data-theme="dark"`) {
		t.Error("both dark-mode paths must be defined: the system default stamps nothing, " +
			"so a page defining only one of them is wrong on the other ground")
	}
	if !strings.Contains(page, "--fig-a") || !strings.Contains(page, "--fig-b") {
		t.Error("the figure hues are not defined, so every diagram loses its meaning colour")
	}
}

// TestThePageDoesNotDependOnWhereItWasGenerated is a regression guard for a real CI failure.
//
// The footer used to name the -in flag's value, and the Makefile passes an absolute path.
// The rendered page therefore embedded a machine-specific path, so the same source produced
// different bytes on a developer's machine and on a runner, and `make thesis-check` failed
// on a branch whose prose had not moved. A generated artefact must depend on its input, not
// on the invocation that produced it.
func TestThePageDoesNotDependOnWhereItWasGenerated(t *testing.T) {
	page, _, err := render("# Title\n\ntext\n")
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(page)
	for _, machineSpecific := range []string{
		"C:\\", "/home/runner", "/Users/", ".claude", "worktrees", "AppData",
	} {
		if strings.Contains(rendered, machineSpecific) {
			t.Errorf("the page contains %q, so it is not reproducible across machines and "+
				"a currency check will fail for the wrong reason", machineSpecific)
		}
	}
	if !strings.Contains(rendered, canonicalSource) {
		t.Errorf("the footer no longer names the canonical source %q", canonicalSource)
	}
}

func TestUnreferencedFiguresAreReported(t *testing.T) {
	// A drawing nobody placed is either forgotten or leftover, and both are worth knowing.
	_, unused, err := render("# Title\n\ntext\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(unused) != len(figures()) {
		t.Errorf("got %d unreferenced figures, want all %d", len(unused), len(figures()))
	}
	// Reported in a stable order, so the message does not churn between runs.
	for i := 1; i < len(unused); i++ {
		if unused[i] < unused[i-1] {
			t.Errorf("unreferenced figures are not sorted: %v", unused)
			break
		}
	}
}
