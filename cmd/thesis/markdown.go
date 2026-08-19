package main

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// A Markdown renderer for exactly the subset docs/THESIS.md uses, and nothing more.
//
// # Why not a library
//
// The module's only dependency is pgx, deliberately, and a document renderer is not worth
// changing that for. The subset in use is small and closed — headings, paragraphs, tables,
// flat lists, blockquotes, fenced code, rules, and four inline forms — so it is cheaper to
// render exactly that and fail loudly on anything else than to take a general parser.
//
// # Why generate rather than hand-write the HTML
//
// The alternative was to write docs/thesis.html by hand beside docs/THESIS.md. That
// duplicates two thousand lines of prose across two files that must then be kept in
// agreement by memory, which is the defect this repository has now fixed twice: a
// hand-maintained coverage table that drifted from `go test -cover`, and a hand-written
// detector list that claimed a composition the code had stopped using. Generating removes
// the possibility rather than the temptation.
//
// The Markdown stays canonical for prose. Figures live in Go and are injected at anchors,
// because an inline SVG in a Markdown source would be unreadable in the source and
// invisible on GitHub.

// figureAnchor matches the marker that pulls a diagram into the page:
//
//	<!-- figure: per-entity-norm -->
//
// An anchor naming a figure that does not exist is an error rather than a silent omission.
// A document that quietly drops a diagram reads as complete, which is how a missing figure
// survives review.
var figureAnchor = regexp.MustCompile(`^<!--\s*figure:\s*([a-z0-9-]+)\s*-->$`)

var (
	inlineCode   = regexp.MustCompile("`([^`]+)`")
	inlineBold   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	inlineItalic = regexp.MustCompile(`\*([^*\n]+)\*`)
	inlineLink   = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	headingLine  = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	orderedItem  = regexp.MustCompile(`^(\d+)\.\s+(.*)$`)
	slugStrip    = regexp.MustCompile(`[^a-z0-9\s-]`)
	slugSpace    = regexp.MustCompile(`\s`)
)

// Heading is one entry in the rendered document's table of contents.
type Heading struct {
	Level int
	Text  string
	Slug  string
	// Plain is the heading with inline markup removed, for the contents list and for the
	// document outline where markup would be noise.
	Plain string
}

// document is the result of rendering.
type document struct {
	Body     string
	Headings []Heading
}

// slug builds the anchor id for a heading, matching the scheme the Markdown source's own
// cross-references already assume: lowercase, punctuation dropped, then each remaining
// whitespace character replaced by a hyphen.
//
// Runs are deliberately NOT collapsed, which is the part that is easy to get wrong.
// Dropping the em-dash from "Detector I — categorical novelty" leaves two adjacent spaces,
// and those become two hyphens: the source's own link is
// "#91-detector-i--categorical-novelty". Collapsing would produce one hyphen and break
// every cross-reference through an em-dashed heading, of which this document has many.
//
// The algorithm has to agree with what a reader wrote by hand, so it is pinned by test
// rather than adjusted until the links happen to work.
func slug(text string) string {
	s := strings.ToLower(stripInline(text))
	s = slugStrip.ReplaceAllString(s, "")
	return slugSpace.ReplaceAllString(strings.TrimSpace(s), "-")
}

// stripInline removes Markdown emphasis so a heading's text can be used as a label.
func stripInline(text string) string {
	text = inlineLink.ReplaceAllString(text, "$1")
	text = inlineCode.ReplaceAllString(text, "$1")
	text = strings.ReplaceAll(text, "**", "")
	text = strings.ReplaceAll(text, "*", "")
	return text
}

// renderInline escapes the text and then applies the four inline forms.
//
// Code spans are handled first and their contents are excluded from every later
// substitution, so `**` inside a code span stays literal — which matters here, because the
// prose quotes Markdown and Go alike.
func renderInline(text string) string {
	const sentinel = "\x00CODE%d\x00"
	var spans []string
	text = inlineCode.ReplaceAllStringFunc(text, func(m string) string {
		spans = append(spans, inlineCode.FindStringSubmatch(m)[1])
		return fmt.Sprintf(sentinel, len(spans)-1)
	})

	text = html.EscapeString(text)
	text = inlineLink.ReplaceAllStringFunc(text, func(m string) string {
		parts := inlineLink.FindStringSubmatch(m)
		label, href := parts[1], parts[2]
		external := strings.HasPrefix(href, "http")
		attrs := ""
		if external {
			// Opened in a new tab with noopener: the thesis links out to papers and to
			// GitHub, and a reader following a citation should not lose their place in a
			// long document.
			attrs = ` target="_blank" rel="noopener"`
		}
		return fmt.Sprintf(`<a href="%s"%s>%s</a>`, href, attrs, label)
	})
	text = inlineBold.ReplaceAllString(text, "<strong>$1</strong>")
	text = inlineItalic.ReplaceAllString(text, "<em>$1</em>")

	for i, code := range spans {
		text = strings.Replace(text, fmt.Sprintf(sentinel, i), "<code>"+html.EscapeString(code)+"</code>", 1)
	}
	return text
}

// renderMarkdown converts the supported subset to HTML and collects the headings.
func renderMarkdown(source string, figures map[string]string) (document, error) {
	var (
		out      strings.Builder
		doc      document
		lines    = strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")
		para     []string
		quote    []string
		list     []string
		ordered  []string
		inFence  bool
		fence    []string
		fenceTag string
	)

	// flushParagraph and its siblings each close exactly one block. Every dispatch below
	// closes the others first, so a block boundary never depends on a blank line being
	// present — the source is not uniformly spaced and relying on that produced merged
	// paragraphs.
	flushParagraph := func() {
		if len(para) > 0 {
			out.WriteString("<p>" + renderInline(strings.Join(para, "\n")) + "</p>\n")
			para = nil
		}
	}
	flushQuote := func() {
		if len(quote) > 0 {
			inner, err := renderMarkdown(strings.Join(quote, "\n"), figures)
			if err == nil {
				out.WriteString("<blockquote>\n" + inner.Body + "</blockquote>\n")
			}
			quote = nil
		}
	}
	flushList := func() {
		if len(list) > 0 {
			out.WriteString("<ul>\n")
			for _, item := range list {
				out.WriteString("<li>" + renderInline(item) + "</li>\n")
			}
			out.WriteString("</ul>\n")
			list = nil
		}
	}
	flushOrdered := func() {
		if len(ordered) > 0 {
			out.WriteString("<ol>\n")
			for _, item := range ordered {
				out.WriteString("<li>" + renderInline(item) + "</li>\n")
			}
			out.WriteString("</ol>\n")
			ordered = nil
		}
	}
	flushAll := func() {
		flushParagraph()
		flushQuote()
		flushList()
		flushOrdered()
	}

	for index := 0; index < len(lines); index++ {
		line := lines[index]
		trimmed := strings.TrimSpace(line)

		// Fenced code is verbatim: no inline substitution, no block dispatch inside it.
		if strings.HasPrefix(trimmed, "```") {
			if inFence {
				out.WriteString(`<pre><code`)
				if fenceTag != "" {
					out.WriteString(` class="lang-` + html.EscapeString(fenceTag) + `"`)
				}
				out.WriteString(">" + html.EscapeString(strings.Join(fence, "\n")) + "</code></pre>\n")
				fence, fenceTag, inFence = nil, "", false
			} else {
				flushAll()
				fenceTag = strings.TrimPrefix(trimmed, "```")
				inFence = true
			}
			continue
		}
		if inFence {
			fence = append(fence, line)
			continue
		}

		if match := figureAnchor.FindStringSubmatch(trimmed); match != nil {
			flushAll()
			svg, ok := figures[match[1]]
			if !ok {
				return document{}, fmt.Errorf("thesis: figure %q is referenced at line %d "+
					"but no such figure is defined; a document that silently drops a "+
					"diagram reads as complete", match[1], index+1)
			}
			out.WriteString(svg + "\n")
			continue
		}

		if trimmed == "" {
			flushAll()
			continue
		}

		if trimmed == "---" {
			flushAll()
			out.WriteString("<hr>\n")
			continue
		}

		if match := headingLine.FindStringSubmatch(line); match != nil {
			flushAll()
			level := len(match[1])
			text := match[2]
			id := slug(text)
			doc.Headings = append(doc.Headings, Heading{
				Level: level, Text: text, Slug: id, Plain: stripInline(text),
			})
			// The anchor link is a permalink affordance: a long document is navigated by
			// section, and a reader who wants to cite one should not have to hunt for its id.
			out.WriteString(fmt.Sprintf(
				`<h%d id="%s">%s<a class="anchor" href="#%s" aria-label="link to this section">#</a></h%d>`+"\n",
				level, id, renderInline(text), id, level))
			continue
		}

		if strings.HasPrefix(trimmed, "|") {
			flushAll()
			var rows []string
			for index < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[index]), "|") {
				rows = append(rows, strings.TrimSpace(lines[index]))
				index++
			}
			index-- // the outer loop advances
			out.WriteString(renderTable(rows))
			continue
		}

		if strings.HasPrefix(trimmed, "> ") || trimmed == ">" {
			flushParagraph()
			flushList()
			flushOrdered()
			quote = append(quote, strings.TrimPrefix(strings.TrimPrefix(trimmed, ">"), " "))
			continue
		}

		if strings.HasPrefix(trimmed, "- ") {
			flushParagraph()
			flushQuote()
			flushOrdered()
			list = append(list, strings.TrimPrefix(trimmed, "- "))
			continue
		}

		if match := orderedItem.FindStringSubmatch(trimmed); match != nil {
			flushParagraph()
			flushQuote()
			flushList()
			ordered = append(ordered, match[2])
			continue
		}

		// A continuation line inside a list item belongs to that item, because the source
		// wraps long items across lines.
		if len(list) > 0 && strings.HasPrefix(line, "  ") {
			list[len(list)-1] += " " + trimmed
			continue
		}
		if len(ordered) > 0 && strings.HasPrefix(line, "  ") {
			ordered[len(ordered)-1] += " " + trimmed
			continue
		}

		flushQuote()
		flushList()
		flushOrdered()
		para = append(para, trimmed)
	}

	if inFence {
		return document{}, fmt.Errorf("thesis: unterminated code fence")
	}
	flushAll()
	doc.Body = out.String()
	return doc, nil
}

// renderTable renders a GitHub-style pipe table. The second row is the alignment rule and
// is consumed rather than rendered.
func renderTable(rows []string) string {
	if len(rows) < 2 {
		return ""
	}
	cells := func(row string) []string {
		row = strings.TrimSuffix(strings.TrimPrefix(row, "|"), "|")
		parts := strings.Split(row, "|")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return parts
	}

	header := cells(rows[0])
	aligns := make([]string, len(header))
	for i, spec := range cells(rows[1]) {
		if i >= len(aligns) {
			break
		}
		switch {
		case strings.HasPrefix(spec, ":") && strings.HasSuffix(spec, ":"):
			// A CSS keyword, not prose: it cannot be respelled without breaking the
			// alignment. Scoped to this line so the rest of the file stays spell-checked.
			aligns[i] = "center" //nolint:misspell // CSS keyword
		case strings.HasSuffix(spec, ":"):
			aligns[i] = "right"
		}
	}

	var out strings.Builder
	// Wrapped so a wide table scrolls inside its own box rather than making the page
	// scroll horizontally.
	out.WriteString(`<div class="tablewrap"><table>` + "\n<thead><tr>")
	for i, cell := range header {
		out.WriteString(tag("th", cell, align(aligns, i)))
	}
	out.WriteString("</tr></thead>\n<tbody>\n")
	for _, row := range rows[2:] {
		out.WriteString("<tr>")
		for i, cell := range cells(row) {
			out.WriteString(tag("td", cell, align(aligns, i)))
		}
		out.WriteString("</tr>\n")
	}
	out.WriteString("</tbody></table></div>\n")
	return out.String()
}

func align(aligns []string, i int) string {
	if i < len(aligns) && aligns[i] != "" {
		return ` style="text-align:` + aligns[i] + `"`
	}
	return ""
}

func tag(name, content, attrs string) string {
	return "<" + name + attrs + ">" + renderInline(content) + "</" + name + ">"
}
