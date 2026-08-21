package main

// The page shell. Self-contained: no external stylesheet, font, script or image, so the
// document opens from disk and survives being copied anywhere.
//
// Light and dark are both defined explicitly. A viewer's theme has three states — an
// explicit choice, or the system default with nothing stamped — so every colour has a value
// on bare :root, the dark values are repeated under both `prefers-color-scheme` and
// `[data-theme="dark"]`, and the body paints its own background rather than borrowing one.
//
// --fig-a and --fig-b are the two figure hues, stepped per mode from a validated
// categorical order. They are declared here rather than inside the SVG because an SVG
// fragment cannot carry a media query, and hard-coding one mode's hue would leave every
// diagram wrong on the other ground.
const pageTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>__TITLE__</title>
<style>
:root{
  --ink:#17191B; --ink-2:#454B50; --ink-3:#6B7379;
  --accent:#1C4657; --accent-2:#7E5209;
  --rule:#D9DEE0; --rule-2:#B3BBBF;
  --panel:#F4F6F7; --panel-2:#FBF7EF;
  --crit:#862B36; --good:#1F5537; --page:#FFFFFF;
  --fig-a:#2a78d6; --fig-b:#eb6834;
}
@media (prefers-color-scheme:dark){
  :root:not([data-theme="light"]){
    --ink:#E8EAEB; --ink-2:#B8BEC2; --ink-3:#8C9499;
    --accent:#7FB3C8; --accent-2:#D9A94E;
    --rule:#343A40; --rule-2:#4A5257;
    --panel:#20252A; --panel-2:#2A2622;
    --crit:#D98996; --good:#7FBF98; --page:#15171A;
    --fig-a:#3987e5; --fig-b:#d95926;
  }
}
:root[data-theme="dark"]{
  --ink:#E8EAEB; --ink-2:#B8BEC2; --ink-3:#8C9499;
  --accent:#7FB3C8; --accent-2:#D9A94E;
  --rule:#343A40; --rule-2:#4A5257;
  --panel:#20252A; --panel-2:#2A2622;
  --crit:#D98996; --good:#7FBF98; --page:#15171A;
  --fig-a:#3987e5; --fig-b:#d95926;
}
*{box-sizing:border-box;margin:0;padding:0}
html{scroll-behavior:smooth}
body{background:var(--page);color:var(--ink);
  font-family:Georgia,"Sitka Text",Cambria,serif;line-height:1.62;
  font-variant-numeric:tabular-nums;
  display:grid;grid-template-columns:minmax(0,1fr);
  max-width:1180px;margin:0 auto;padding:2rem 1.25rem 5rem}
@media (min-width:1000px){
  body{grid-template-columns:15rem minmax(0,1fr);gap:3rem;padding-top:3rem}
  nav.toc{position:sticky;top:2rem;align-self:start;max-height:calc(100vh - 4rem);
    overflow-y:auto;font-size:.8rem;border-right:1px solid var(--rule);padding-right:1rem}
}
nav.toc{font-family:"Segoe UI",Calibri,sans-serif;margin-bottom:2rem}
nav.toc b{display:block;font-size:.68rem;text-transform:uppercase;letter-spacing:.07em;
  color:var(--ink-3);margin:0 0 .6rem}
nav.toc a{display:block;color:var(--ink-2);text-decoration:none;padding:.16rem 0;
  line-height:1.35}
nav.toc a:hover{color:var(--accent);text-decoration:underline}
nav.toc a.h1{font-weight:700;color:var(--ink);margin-top:.7rem}
nav.toc a.h3{padding-left:.8rem;font-size:.94em;color:var(--ink-3)}
main{min-width:0}
h1,h2,h3,h4{color:var(--accent);line-height:1.25;scroll-margin-top:1.5rem}
h1{font-size:1.95rem;margin:2.2rem 0 .8rem}
h1:first-child{margin-top:0}
h2{font-size:1.35rem;margin:2.4rem 0 .7rem;border-bottom:1px solid var(--rule);
  padding-bottom:.3rem}
h3{font-size:1.1rem;margin:1.9rem 0 .5rem}
h4{font-size:.98rem;margin:1.5rem 0 .4rem;color:var(--ink-2)}
p{margin:.85rem 0;max-width:74ch}
ul,ol{margin:.85rem 0 .85rem 1.5rem;max-width:74ch}
li{margin:.35rem 0}
a{color:var(--accent)}
strong{color:var(--ink);font-weight:700}
code{font-family:"Cascadia Mono",Consolas,ui-monospace,monospace;font-size:.87em;
  background:var(--panel);padding:.1em .32em;border-radius:3px;
  overflow-wrap:break-word}
pre{background:var(--panel);border:1px solid var(--rule);border-radius:6px;
  padding:.85rem 1rem;margin:1.1rem 0;overflow-x:auto}
pre code{background:none;padding:0;font-size:.84rem;line-height:1.5}
blockquote{margin:1.2rem 0;padding:.9rem 1.2rem;background:var(--panel);
  border-left:5px solid var(--accent);border-radius:0 5px 5px 0}
blockquote p{margin:.4rem 0}
blockquote p:first-child{margin-top:0}
blockquote p:last-child{margin-bottom:0}
hr{border:0;border-top:1px solid var(--rule-2);margin:2.6rem 0}
.tablewrap{overflow-x:auto;margin:1.1rem 0}
table{border-collapse:collapse;width:100%;font-size:.9rem}
th{font-family:"Segoe UI",Calibri,sans-serif;font-size:.7rem;text-transform:uppercase;
  letter-spacing:.05em;color:var(--ink-2);text-align:left;white-space:nowrap;
  border-bottom:1px solid var(--rule-2);padding:.4rem .6rem}
td{border-bottom:1px solid var(--rule);padding:.34rem .6rem;vertical-align:top}
/* Measurements read as a column, so they are right-aligned on a fixed digit width. .group
   rules off a band of columns that share a heading -- two budgets side by side in one table
   run together without it. */
td.num,th.num{text-align:right;font-variant-numeric:tabular-nums}
td.group,th.group{border-left:1px solid var(--rule-2)}
th[colspan]{text-align:center}
tbody tr:hover{background:var(--panel)}
a.anchor{color:var(--rule-2);text-decoration:none;font-weight:400;margin-left:.4rem;
  opacity:0;font-size:.8em}
h1:hover a.anchor,h2:hover a.anchor,h3:hover a.anchor,h4:hover a.anchor{opacity:1}
figure.fig{margin:1.8rem 0;padding:1.1rem 1.2rem;background:var(--panel);
  border:1px solid var(--rule);border-radius:6px}
svg.figsvg{display:block;width:100%;max-width:100%;height:auto;color:var(--ink)}
figure.fig figcaption{margin-top:.9rem;padding-top:.7rem;
  border-top:1px solid var(--rule);
  font-family:"Segoe UI",Calibri,sans-serif;font-size:.83rem;line-height:1.55;
  color:var(--ink-2);max-width:82ch}
figure.fig figcaption strong{color:var(--ink)}
footer{margin-top:3.5rem;padding-top:1rem;border-top:1px solid var(--rule-2);
  color:var(--ink-3);font-size:.8rem;font-family:"Segoe UI",Calibri,sans-serif;
  max-width:80ch}
/* Print is a different medium and needs a different density. The screen layout runs
   about 330 words to the page, which turns a short paper into a long one; these rules
   roughly double that without making it unreadable. Measured, not guessed: the paper
   target reports its own page count. */
@media print{
  @page{size:A4;margin:18mm 16mm}
  nav.toc{display:none}
  html,body{background:#fff;color:#000}
  body{display:block;max-width:none;margin:0;padding:0;
    font-size:10.2pt;line-height:1.36}
  main{max-width:none;padding:0}
  /* The screen measure caps text at 74-82ch to keep a comfortable line on a wide
     monitor. On a 16mm-margin page that cap wastes a third of every line. */
  p,ul,ol,blockquote,figcaption,.note{max-width:none}
  p{margin:.42rem 0;orphans:3;widows:3}
  ul,ol{margin:.42rem 0 .42rem 1.15rem}
  li{margin:.1rem 0}
  h1{font-size:16pt;margin:0 0 .5rem}
  h2{font-size:13pt;margin:1rem 0 .35rem;break-after:avoid}
  h3{font-size:11pt;margin:.7rem 0 .25rem;break-after:avoid}
  h4{font-size:10.4pt;margin:.6rem 0 .2rem;break-after:avoid}
  table{font-size:8.8pt;break-inside:avoid;width:100%}
  th,td{padding:.18rem .35rem}
  pre{font-size:8.4pt;line-height:1.32;break-inside:avoid}
  pre code{font-size:8.4pt;line-height:1.32}
  figure.fig{break-inside:avoid;margin:.6rem 0}
  figcaption{font-size:8.4pt;line-height:1.35}
  a{color:#000;text-decoration:none}
}
</style>
</head>
<body>
__DEFS__
<nav class="toc"><b>Contents</b>
__TOC__
</nav>
<main>
__BODY__
<footer>__FOOTER__</footer>
</main>
</body>
</html>
`
