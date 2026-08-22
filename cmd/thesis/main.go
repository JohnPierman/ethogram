// Command thesis renders docs/PAPER.md as a self-contained HTML document with diagrams.
//
// # Why this is generated and not hand-written
//
// A richer HTML thesis could have been written beside the Markdown one. That would duplicate
// two thousand lines of prose across two files kept in agreement by memory, which is exactly
// the defect this repository has now fixed twice: a coverage table maintained by hand that
// drifted from `go test -cover`, and a detector list that claimed a composition the code had
// stopped using. Generating makes the drift impossible rather than merely discouraged.
//
// So the Markdown stays canonical for prose and this command owns the presentation: the
// page shell, the contents list, and the diagrams. Diagrams live in Go because an inline SVG
// in the Markdown source would be unreadable in the source and invisible on GitHub; the
// source carries an anchor naming the figure, and this command injects it.
//
// A `-check` mode verifies the committed HTML is current, so CI fails when the Markdown moves
// and the rendered document does not.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"html"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func main() {
	var (
		inPath  = flag.String("in", "docs/PAPER.md", "the canonical Markdown source")
		outPath = flag.String("out", "docs/paper.html", "rendered HTML output")
		check   = flag.Bool("check", false,
			"verify the committed HTML matches the Markdown instead of writing it")
	)
	flag.Parse()

	source, err := os.ReadFile(*inPath)
	if err != nil {
		log.Fatalf("thesis: read %s: %v", *inPath, err)
	}

	page, unused, err := render(string(source))
	if err != nil {
		log.Fatal(err)
	}
	// A figure defined but never referenced is reported rather than shipped: it is either a
	// drawing someone forgot to place, or a leftover, and both are worth knowing about.
	if len(unused) > 0 {
		log.Printf("thesis: NOTE %d figure(s) defined but not referenced: %s",
			len(unused), strings.Join(unused, ", "))
	}

	if *check {
		committed, err := os.ReadFile(*outPath)
		if err != nil {
			log.Fatalf("thesis: %s does not exist; run `make paper` and commit it: %v",
				*outPath, err)
		}
		if !bytes.Equal(committed, page) {
			log.Fatalf("thesis: %s is stale — it does not match %s. "+
				"Run `make paper` and commit the result", *outPath, *inPath)
		}
		fmt.Printf("thesis: %s is current with %s\n", *outPath, *inPath)
		return
	}

	if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*outPath, page, 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("thesis: wrote %s from %s", *outPath, *inPath)
}

// canonicalSource is what the footer names as the document's source.
//
// It is a constant rather than the -in flag's value. The flag carries whatever path the
// caller used — the Makefile passes an absolute one — and embedding that would make the
// rendered page depend on WHERE it was generated, so the same source would produce
// different bytes on a developer's machine and on a CI runner. `make paper-check` caught
// exactly that. The footer is describing a fact about the repository, not about the
// invocation, so it states the repository-relative path.
const canonicalSource = "docs/PAPER.md"

// render produces the page and the list of figures that were defined but never referenced.
func render(source string) (page []byte, unusedFigures []string, err error) {
	all := figures()
	referenced := map[string]bool{}
	for _, line := range strings.Split(source, "\n") {
		if m := figureAnchor.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			referenced[m[1]] = true
		}
	}
	for id := range all {
		if !referenced[id] {
			unusedFigures = append(unusedFigures, id)
		}
	}
	sortStrings(unusedFigures)

	doc, err := renderMarkdown(source, all)
	if err != nil {
		return nil, nil, err
	}

	title := "Idiolect"
	for _, h := range doc.Headings {
		if h.Level == 1 {
			title = h.Plain
			break
		}
	}

	out := pageTemplate
	out = strings.Replace(out, "__TITLE__", html.EscapeString(title), 1)
	out = strings.Replace(out, "__DEFS__", sharedDefs, 1)
	out = strings.Replace(out, "__TOC__", contents(doc.Headings), 1)
	out = strings.Replace(out, "__BODY__", doc.Body, 1)
	out = strings.Replace(out, "__FOOTER__", footer(), 1)

	if broken := brokenAnchors(out); len(broken) > 0 {
		return nil, nil, fmt.Errorf("thesis: %d cross-reference(s) point at a section that "+
			"does not exist: %s.\n\nA link written for an earlier numbering renders as "+
			"ordinary text and silently goes nowhere, so it cannot be caught by reading the "+
			"page. Two had already shipped this way (§9 Results and §9.1, from before the "+
			"results moved to §15). Fix the target or the link",
			len(broken), strings.Join(broken, ", "))
	}
	return []byte(out), unusedFigures, nil
}

// anchorLink and anchorTarget read the rendered page rather than the Markdown, because what
// matters is whether the emitted href resolves against an emitted id — the slug algorithm
// sits between the two and is exactly where a mismatch appears.
var (
	anchorLink   = regexp.MustCompile(`href="#([^"]+)"`)
	anchorTarget = regexp.MustCompile(`id="([^"]+)"`)
)

// brokenAnchors returns every internal link with no matching target, in order and deduplicated.
func brokenAnchors(page string) []string {
	targets := map[string]bool{}
	for _, m := range anchorTarget.FindAllStringSubmatch(page, -1) {
		targets[m[1]] = true
	}
	var broken []string
	seen := map[string]bool{}
	for _, m := range anchorLink.FindAllStringSubmatch(page, -1) {
		if !targets[m[1]] && !seen[m[1]] {
			seen[m[1]] = true
			broken = append(broken, "#"+m[1])
		}
	}
	return broken
}

// contents builds the sidebar.
//
// Two omissions. h4 is dropped: the document has seven and they are sub-points of an
// argument rather than places a reader navigates to. And anything below h1 that appears
// before the first h2 is dropped too — that is the title block's subtitle, which is a
// restatement of the title rather than a destination, and listing it puts a line of noise
// at the top of the contents.
func contents(headings []Heading) string {
	var out strings.Builder
	seenSection := false
	for _, h := range headings {
		if h.Level == 2 {
			seenSection = true
		}
		if h.Level > 3 {
			continue
		}
		if h.Level == 3 && !seenSection {
			continue
		}
		fmt.Fprintf(&out, `<a class="h%d" href="#%s">%s</a>`+"\n",
			h.Level, h.Slug, html.EscapeString(h.Plain))
	}
	return out.String()
}

func footer() string {
	return "Generated by <code>cmd/thesis</code> from <code>" + canonicalSource +
		"</code>, which stays canonical for the prose: this page is never edited directly, " +
		"so the two cannot disagree. Every quantitative claim traces to a recorded run in " +
		"<code>results/</code>. Diagrams are hand-authored inline SVG with no external " +
		"resources, and their two carrying colours are validated for contrast and " +
		"colour-vision separation against both the light and dark surfaces."
}

// sortStrings avoids importing sort for one call on a list that is almost always empty.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
