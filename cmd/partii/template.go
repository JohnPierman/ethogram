//nolint:misspell // CSS property names are American English by specification; the document's prose remains British.
package main

// paperHTML is the Part II document: a fragment that begins with its stylesheet and is
// executed against findings. It carries Part I's visual identity — Letter page at
// 22/21 mm, 10.2 pt Georgia justified, the same heading scale and design tokens — so
// the two documents sit beside each other without looking foreign. Every value the
// template prints arrives already formatted from a result file; the only numbers that
// live here are style measurements.
// paperHTML is a complete standalone document, not a fragment.
//
// The charset declaration is load-bearing rather than boilerplate. The prose is full of
// section signs, en dashes and Greek letters, and the file is written as UTF-8; a
// browser opening a local file with no declared encoding falls back to a locale default,
// which on a Windows host renders every "§" as "Â§". The document is meant to be opened
// from disk and printed, so it has to declare its own encoding.
const paperHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Calibrated Behavioural Anomaly Detection — Part II: Measured Performance</title>
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
.scoreboard{font-family:"Segoe UI",Calibri,sans-serif;font-size:9pt;
  color:var(--ink-2);text-align:left;margin:0 0 4mm}
.pending{background:var(--panel-2);border-left:3px solid var(--accent-2);
  padding:2.5mm 3.5mm;margin:2mm 0 4mm;font-size:9.6pt;text-align:left;
  color:var(--ink-2)}
.tablewrap{overflow-x:auto;margin:2mm 0 3mm}
table{border-collapse:collapse;width:100%;font-size:8.7pt;text-align:left}
caption{caption-side:top;text-align:left;color:var(--ink-3);font-size:8.5pt;
  font-family:"Segoe UI",Calibri,sans-serif;padding-bottom:1mm}
th{font-family:"Segoe UI",Calibri,sans-serif;font-size:7.6pt;text-transform:uppercase;
  letter-spacing:.05em;color:var(--ink-2);border-bottom:1px solid var(--rule-2);
  padding:1.2mm 1.8mm;vertical-align:bottom;text-align:left}
td{border-bottom:1px solid var(--rule);padding:1.1mm 1.8mm;vertical-align:top}
th:not(:first-child),td:not(:first-child){text-align:right}
tr{break-inside:avoid}
.caveats{margin:1mm 0 3mm 6mm;font-size:9.2pt;color:var(--ink-2);text-align:left}
.caveats li{margin:0 0 1.2mm}
.prov{color:var(--ink-3);font-size:8pt;
  font-family:"Cascadia Mono",Consolas,monospace;text-align:left;
  border-top:1px solid var(--rule);margin-top:2mm;padding-top:1.2mm}
.dirty{color:var(--crit);font-weight:bold;
  font-family:"Segoe UI",Calibri,sans-serif}
.sid{font-family:"Cascadia Mono",Consolas,monospace;color:var(--accent-2);
  margin-right:2mm}
@media print{ body{max-width:none;padding:0} }
</style>
</head>
<body>
{{define "tbl"}}<div class="tablewrap"><table>{{if .Caption}}<caption>{{.Caption}}</caption>{{end}}<thead><tr>{{range .Head}}<th>{{.}}</th>{{end}}</tr></thead><tbody>{{range .Rows}}<tr>{{range .}}<td>{{.}}</td>{{end}}</tr>{{end}}</tbody></table></div>{{end}}
<h1>Calibrated Behavioural Anomaly Detection over Schema-Evolving Security Telemetry</h1>
<p class="subtitle">Part II: Measured Performance</p>
<p class="date">Generated <span class="gen">{{.Generated}}</span></p>
<p class="abstract">Every figure in this document was read from a recorded run
committed under <span class="mono">results/</span>: the provenance table below names
the run, the file and the commit behind each section, and no number appears that did
not come out of one of those files. Hypotheses whose runs have not yet been recorded
are shown as explicit not-yet-measured panels rather than estimated, and a section
whose headline figure is missing is withheld entirely rather than half-filled.</p>

<h2>Provenance of every figure</h2>
{{if .Sources}}<div class="tablewrap"><table>
<thead><tr><th>Run</th><th>File</th><th>Commit</th><th>Coverage</th><th>Corpus</th></tr></thead>
<tbody>{{range .Sources}}<tr><td class="mono">{{.RunID}}</td><td class="mono">{{.File}}</td><td class="mono">{{.GitSHA}}{{if .Dirty}} <span class="dirty">working tree dirty</span>{{end}}</td><td>{{.Coverage}}</td><td>{{.Corpus}}</td></tr>{{end}}</tbody>
</table></div>
{{else}}<div class="pending">No result files were found in the results directory; every section below is rendered as not yet measured.</div>{{end}}
<p class="scoreboard">{{.Measured}} measured, {{.Pending}} not yet measured.</p>

{{range .Sections}}<section>
<h2><span class="sid">{{.ID}}</span>{{.Title}}</h2>
{{if .Pending}}<div class="pending">{{.Pending}}</div>{{else}}{{range .Paras}}<p>{{.}}</p>
{{end}}{{if .Table}}{{template "tbl" .Table}}{{end}}{{if .Table2}}{{template "tbl" .Table2}}{{end}}{{if .Table3}}{{template "tbl" .Table3}}{{end}}{{if .Caveats}}<ul class="caveats">{{range .Caveats}}<li>{{.}}</li>{{end}}</ul>{{end}}<p class="prov">{{.Prov}}</p>{{end}}
</section>
{{end}}
</body>
</html>
`
