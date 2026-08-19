//nolint:misspell // CSS property names are American English by specification; the document's prose remains British.
package main

// paperHTML is the Part III document: a complete standalone page that carries Part I
// and Part II's visual identity — Letter page at 22/21 mm, 10.2 pt Georgia justified,
// the same heading scale and design tokens — so the three documents sit beside each
// other without looking foreign. Unlike Part II's template it is executed against
// hand-authored content rather than result files: Part III is prose about code, and
// the only numbers in it are quoted from the repository's own artefacts.
//
// The charset declaration is load-bearing rather than boilerplate. The prose is full
// of section signs, en dashes and Greek letters, and the file is written as UTF-8; a
// browser opening a local file with no declared encoding falls back to a locale
// default, which on a Windows host renders every "§" as "Â§". The document is meant
// to be opened from disk and printed, so it has to declare its own encoding.
//
// Rendering goes through html/template with auto-escaping throughout; no content
// enters as template.HTML. Inline code spans are structured data (an []inline per
// paragraph or table cell), so the template can wrap them in <code> without any
// string of prose ever being trusted as markup. The title, subtitle, byline and
// abstract arrive from the same shared constants the Markdown rendering uses, so
// the two artefacts cannot drift.
const paperHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Calibrated Behavioural Anomaly Detection — Part III: Implementation</title>
<style>
:root{
  --ink:#17191B; --ink-2:#454B50; --ink-3:#6B7379;
  --accent:#1C4657; --accent-2:#7E5209;
  --rule:#D9DEE0; --rule-2:#B3BBBF;
  --panel:#F1F4F5; --panel-2:#FBF7EF;
  --crit:#862B36; --good:#1F5537;
}
@page{ size:Letter; margin:22mm 21mm 20mm 21mm; }
*{box-sizing:border-box;margin:0;padding:0}
body{background:#FFFFFF;color:var(--ink);
  font-family:Georgia,"Sitka Text",Cambria,serif;font-size:10.2pt;line-height:1.52;
  text-align:justify;max-width:176mm;margin:0 auto;padding:12mm 6mm 18mm;
  font-variant-numeric:tabular-nums}
h1{font-size:18.5pt;color:var(--accent);line-height:1.25;text-align:left;
  margin:0 0 2mm}
h2{font-size:12.5pt;color:var(--accent);text-align:left;margin:7mm 0 2.5mm;
  border-bottom:1px solid var(--rule);padding-bottom:1mm}
h3{font-size:10.5pt;color:var(--ink);text-align:left;margin:4mm 0 1.5mm}
p{margin:0 0 2.6mm}
code,.mono{font-family:"Cascadia Mono",Consolas,monospace;font-size:.92em}
.subtitle{color:var(--ink-2);font-size:11pt;text-align:left;margin:0 0 1mm}
.date{color:var(--ink-3);font-size:9pt;text-align:left;margin:0 0 5mm;
  font-family:"Segoe UI",Calibri,sans-serif}
.abstract{background:var(--panel);font-size:9.8pt;border:1px solid var(--rule);
  border-radius:2px;padding:3mm 4mm;margin:0 0 6mm}
.tablewrap{overflow-x:auto;margin:2mm 0 3mm}
table{border-collapse:collapse;width:100%;font-size:8.7pt;text-align:left}
caption{caption-side:top;text-align:left;color:var(--ink-3);font-size:8.5pt;
  font-family:"Segoe UI",Calibri,sans-serif;padding-bottom:1mm}
th{font-family:"Segoe UI",Calibri,sans-serif;font-size:7.6pt;text-transform:uppercase;
  letter-spacing:.05em;color:var(--ink-2);border-bottom:1px solid var(--rule-2);
  padding:1.2mm 1.8mm;vertical-align:bottom;text-align:left}
td{border-bottom:1px solid var(--rule);padding:1.1mm 1.8mm;vertical-align:top}
tr{break-inside:avoid}
.sid{font-family:"Cascadia Mono",Consolas,monospace;color:var(--accent-2);
  margin-right:2mm}
pre.codeblock{background:var(--panel);border:1px solid var(--rule);border-radius:2px;
  padding:2.5mm 3.5mm;margin:0 0 2.6mm;font-family:"Cascadia Mono",Consolas,monospace;
  font-size:8.7pt;line-height:1.45;overflow-x:auto;text-align:left;break-inside:avoid}
@media print{ body{max-width:none;padding:0} }
</style>
</head>
<body>
{{define "rich"}}{{range .}}{{if .Code}}<code>{{.Text}}</code>{{else}}{{.Text}}{{end}}{{end}}{{end}}
{{define "tbl"}}<div class="tablewrap"><table>{{if .Caption}}<caption>{{.Caption}}</caption>{{end}}<thead><tr>{{range .Head}}<th>{{.}}</th>{{end}}</tr></thead><tbody>{{range .Rows}}<tr>{{range .}}<td>{{template "rich" .}}</td>{{end}}</tr>{{end}}</tbody></table></div>{{end}}
<h1>{{.Title}}</h1>
<p class="subtitle">{{.Subtitle}}</p>
<p class="date">{{.Byline}}</p>
<p class="abstract">{{template "rich" .Abstract}}</p>

{{range .Sections}}<section>
<h2><span class="sid">{{.Number}}</span>{{.Title}}</h2>
{{range .Paras}}<p>{{template "rich" .}}</p>
{{end}}{{if .Code}}<pre class="codeblock">{{.Code}}</pre>
{{end}}{{if .Table}}{{template "tbl" .Table}}{{end}}{{range .Subs}}<h3>{{.Title}}</h3>
{{range .Paras}}<p>{{template "rich" .}}</p>
{{end}}{{if .Table}}{{template "tbl" .Table}}{{end}}{{end}}</section>
{{end}}
</body>
</html>
`
