package main

// dashboardTemplate is the whole page. The index is substituted for the /*DATA*/ marker
// at render time, so the file loads no external resource and can be opened from disk.
//
// The design tokens match cmd/report and the whitepaper, so a screenshot from either
// sits beside the paper without looking foreign. Light and dark are both defined
// explicitly: the viewer's theme is not known here, and a page that paints only one of
// them borrows the host's background for the other.
const dashboardTemplate = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Idiolect — Results Dashboard</title>
<style>
:root{
  --ink:#17191B; --ink-2:#454B50; --ink-3:#6B7379;
  --accent:#1C4657; --accent-2:#7E5209;
  --rule:#D9DEE0; --rule-2:#B3BBBF;
  --panel:#F1F4F5; --panel-2:#FBF7EF;
  --crit:#862B36; --good:#1F5537; --warn:#8A5A00; --page:#FFFFFF;
  --entity:#1C4657; --population:#7E5209;
}
@media (prefers-color-scheme: dark){
  :root:not([data-theme="light"]){
    --ink:#E8EAEB; --ink-2:#B8BEC2; --ink-3:#8C9499;
    --accent:#7FB3C8; --accent-2:#D9A94E;
    --rule:#3A4147; --rule-2:#525A61;
    --panel:#22272B; --panel-2:#2A2622;
    --crit:#D98996; --good:#7FBF98; --warn:#D9A94E; --page:#17191B;
    --entity:#7FB3C8; --population:#D9A94E;
  }
}
:root[data-theme="dark"]{
  --ink:#E8EAEB; --ink-2:#B8BEC2; --ink-3:#8C9499;
  --accent:#7FB3C8; --accent-2:#D9A94E;
  --rule:#3A4147; --rule-2:#525A61;
  --panel:#22272B; --panel-2:#2A2622;
  --crit:#D98996; --good:#7FBF98; --warn:#D9A94E; --page:#17191B;
  --entity:#7FB3C8; --population:#D9A94E;
}
*{box-sizing:border-box;margin:0;padding:0}
body{background:var(--page);color:var(--ink);
  font-family:Georgia,"Sitka Text",Cambria,serif;line-height:1.55;
  max-width:1200px;margin:0 auto;padding:2rem 1.25rem;font-variant-numeric:tabular-nums}
h1{font-size:1.6rem;color:var(--accent);margin-bottom:.2rem}
h2{font-size:1.1rem;color:var(--accent);margin:1.8rem 0 .5rem;
  border-bottom:1px solid var(--rule);padding-bottom:.25rem}
.sub{color:var(--ink-3);font-family:"Segoe UI",Calibri,sans-serif;font-size:.88rem}
.sans{font-family:"Segoe UI",Calibri,sans-serif}
.controls{display:flex;flex-wrap:wrap;gap:1rem;align-items:flex-end;
  background:var(--panel);border:1px solid var(--rule);border-radius:6px;
  padding:.9rem 1.1rem;margin:1.1rem 0}
.controls label{display:block;font-family:"Segoe UI",Calibri,sans-serif;
  font-size:.7rem;text-transform:uppercase;letter-spacing:.05em;color:var(--ink-2);
  margin-bottom:.25rem}
select{font:inherit;font-size:.9rem;padding:.3rem .4rem;background:var(--page);
  color:var(--ink);border:1px solid var(--rule-2);border-radius:4px;max-width:100%}
.card{background:var(--panel);border:1px solid var(--rule);border-radius:6px;
  padding:.9rem 1.1rem;margin:.8rem 0}
.card.notrun{background:var(--panel-2);border-style:dashed}
.prov{color:var(--ink-3);font-size:.75rem;
  font-family:"Cascadia Mono",Consolas,monospace;
  border-top:1px solid var(--rule);margin-top:.6rem;padding-top:.4rem;
  overflow-wrap:anywhere}
.caption{color:var(--ink-3);font-size:.8rem;margin:.4rem 0 .1rem;
  font-family:"Segoe UI",Calibri,sans-serif;max-width:82ch}
.tblwrap{overflow-x:auto}
table{border-collapse:collapse;width:100%;font-size:.88rem;margin:.4rem 0}
th{font-family:"Segoe UI",Calibri,sans-serif;font-size:.68rem;text-transform:uppercase;
  letter-spacing:.05em;color:var(--ink-2);text-align:left;
  border-bottom:1px solid var(--rule-2);padding:.35rem .55rem;white-space:nowrap}
td{border-bottom:1px solid var(--rule);padding:.3rem .55rem}
th:not(:first-child),td:not(:first-child){text-align:right}
tbody tr:hover{background:var(--page)}
.scope{display:inline-block;font-family:"Segoe UI",Calibri,sans-serif;font-size:.62rem;
  text-transform:uppercase;letter-spacing:.05em;padding:.05rem .35rem;border-radius:3px;
  border:1px solid currentColor;margin-left:.4rem;vertical-align:.08em}
.scope.per-entity{color:var(--entity)}
.scope.population{color:var(--population)}
.scope.mixed{color:var(--ink-3)}
.hit{color:var(--good);font-weight:700}
.zero{color:var(--ink-3)}
.cell{font-variant-numeric:tabular-nums;font-weight:600}
.cell .sub{display:block;font-weight:400;font-size:.7rem;opacity:.8}
.denom td,.denom th{color:var(--ink-2);font-size:.8rem;border-bottom:2px solid var(--rule-2)}
.control-row td{background:var(--panel-2)}
.warn{border-left:5px solid var(--warn);background:var(--panel-2);
  padding:.6rem .9rem;margin:.5rem 0;border-radius:4px;font-size:.88rem}
.warn.critical{border-left-color:var(--crit)}
.warn.note{border-left-color:var(--rule-2)}
.warn b{font-family:"Segoe UI",Calibri,sans-serif;font-size:.68rem;
  text-transform:uppercase;letter-spacing:.06em;display:block;margin-bottom:.15rem}
.warn.critical b{color:var(--crit)}
.warn.warning b{color:var(--warn)}
.warn.note b{color:var(--ink-3)}
.na{color:var(--ink-3);font-style:italic;font-size:.82rem;text-align:left}
.eid{font-family:"Cascadia Mono",Consolas,monospace;color:var(--accent-2);
  font-weight:700;margin-right:.5rem}
.notrun-mark{color:var(--crit);font-family:"Segoe UI",Calibri,sans-serif;
  font-weight:600;letter-spacing:.06em}
.headline{background:var(--panel);border:1px solid var(--accent);
  border-left:6px solid var(--accent);border-radius:6px;padding:1rem 1.2rem;margin:1rem 0}
.headline p{font-size:1.05rem;max-width:86ch}
.pill{display:inline-block;background:var(--panel-2);border:1px solid var(--rule-2);
  border-radius:10px;padding:.05rem .5rem;font-size:.72rem;
  font-family:"Segoe UI",Calibri,sans-serif;color:var(--ink-2);margin-right:.35rem}
footer{margin-top:2.5rem;color:var(--ink-3);font-size:.78rem;
  border-top:1px solid var(--rule-2);padding-top:.8rem;max-width:86ch}
</style>
</head>
<body>
<h1>Idiolect — Results</h1>
<p class="sub">Calibrated behavioural anomaly detection: an account has an idiolect, and
this reports when it stops speaking in its own. Every number on this page came out of a
recorded run in <code>results/</code>. A measurement that does not exist renders as NOT
RUN, never as zero.</p>

<div class="headline" id="headline"></div>

<div class="controls">
  <div><label for="run">Run</label><select id="run"></select></div>
  <div><label for="budget">Alert budget</label><select id="budget"></select></div>
  <div><label for="baseline">Compare against</label><select id="baseline"></select></div>
</div>

<div id="body"></div>

<footer>
Rendered by <code>cmd/dashboard</code> from <code>results/*.json</code> only. The page
carries no build timestamp on purpose: regenerating it from unchanged results produces an
unchanged file, so a diff means a measurement changed. Scope badges are the reading that
matters most here — <span class="scope per-entity">per-entity</span> asks whether an
entity acted unlike itself, <span class="scope population">population</span> asks whether
it acted unlike everyone else, and the project's central finding is that only the first
discriminates.
</footer>

<script id="index" type="application/json">/*DATA*/</script>
<script>
const DATA = JSON.parse(document.getElementById("index").textContent);
const $ = (id) => document.getElementById(id);

const state = { run: 0, budget: null, baseline: 0 };

// REAL_CAMPAIGN must match realCampaign in index.go: it is the bucket the Go side puts a
// labelled event in when no planted attack accounts for it, and the page looks it up by name.
const REAL_CAMPAIGN = "real campaign";

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, c =>
    ({ "&":"&amp;", "<":"&lt;", ">":"&gt;", '"':"&quot;", "'":"&#39;" }[c]));
}
function scopeBadge(scope) {
  if (!scope) return "";
  return ' <span class="scope ' + esc(scope) + '">' + esc(scope) + "</span>";
}
function hits(n) {
  return n > 0 ? '<span class="hit">' + n + "</span>" : '<span class="zero">0</span>';
}
function pct(x) { return (100 * x).toFixed(2) + "%"; }
function num(n) { return (n ?? 0).toLocaleString("en-GB"); }

// commonDays is the window both arms scored. Comparing over anything wider credits or
// faults an arm for days it never saw, which is the defect restrictToCommonDays exists
// to prevent in the offline analysis.
function commonDays(a, b) {
  const set = new Set(b || []);
  return (a || []).filter(d => set.has(d));
}

function selectedRun()      { return DATA.runs[state.run]; }
function selectedBaseline() { return DATA.baselines[state.baseline]; }

// ---------------------------------------------------------------- controls
function fillControls() {
  $("run").innerHTML = DATA.runs.map((r, i) =>
    '<option value="' + i + '">' + esc(r.run_id) + " — " + num(r.events_scored) +
    " events, " + r.labelled_scored + " labelled</option>").join("");
  $("budget").innerHTML = DATA.budgets.map(b =>
    '<option value="' + b + '">' + b + " alerts / analyst-day</option>").join("");
  $("baseline").innerHTML = DATA.baselines.length
    ? DATA.baselines.map((b, i) =>
        '<option value="' + i + '">' + esc(b.run_id) + "</option>").join("")
    : '<option value="-1">no baselines result present</option>';

  state.budget = DATA.budgets.includes(100) ? 100 : DATA.budgets[DATA.budgets.length - 1];
  $("budget").value = state.budget;

  $("run").onchange = e => { state.run = +e.target.value; render(); };
  $("budget").onchange = e => { state.budget = +e.target.value; render(); };
  $("baseline").onchange = e => { state.baseline = +e.target.value; render(); };
}

// ---------------------------------------------------------------- panels
function provenance(run) {
  return '<div class="prov">run ' + esc(run.run_id) + " · " + esc(run.file) +
    " · git " + esc(run.git_sha) + (run.dirty ? " (DIRTY TREE)" : "") +
    " · " + esc(run.started) + " → " + esc(run.finished) +
    (run.coverage ? " · " + esc(run.coverage) : "") + "</div>";
}

function warningsPanel(run) {
  if (!run.warnings || !run.warnings.length) return "";
  return run.warnings.map(w =>
    '<div class="warn ' + esc(w.severity) + '"><b>' + esc(w.severity) + "</b>" +
    esc(w.text) + "</div>").join("");
}

function summaryPanel(run) {
  const pills = [
    num(run.events_scored) + " events scored",
    run.labelled_scored + " labelled events",
    run.entities_scored >= 0 ? num(run.entities_scored) + " entities" : null,
    run.events_skipped ? num(run.events_skipped) + " skipped" : null,
    run.conformal ? "conformal calibration on" : "model p-values",
    run.open_vocabulary ? "Good–Turing on" : null,
    // Deliberately not "full population": the replay applying no entity sample says
    // nothing about whether the corpus it read was already a subset, and the result
    // records only the former. Claiming the latter is how a sampled measurement gets
    // quoted as a population one.
    run.sampled ? "replay sampled: " + esc(run.sample_note)
                : "no entity sampling in the replay",
  ].filter(Boolean);
  return '<div class="card">' +
    pills.map(p => '<span class="pill">' + esc(p) + "</span>").join("") +
    (run.corpus_files && run.corpus_files.length
      ? '<div class="caption">corpus: ' + run.corpus_files.map(esc).join(", ") +
        " — check the file's manifest for whether it is itself a subset; a replay that " +
        "applied no sample can still have read one</div>"
      : "") +
    provenance(run) + "</div>";
}

// Event-scoped arms. Entity-day arms are deliberately in their own table: their budget
// buys entity-days rather than events, and one table would imply the two are comparable.
function armsPanel(run) {
  const budget = String(state.budget);
  const rows = run.arms.filter(a => a.unit === "event");
  if (!rows.length) return "";
  const order = { composite: 0, combination: 1, detector: 2 };
  rows.sort((a, b) => (order[a.group] - order[b.group]) || a.name.localeCompare(b.name));

  const body = rows.map(a => {
    const d = a.detections[budget];
    if (!d) {
      return "<tr><td>" + esc(a.name) + scopeBadge(a.scope) +
        '</td><td colspan="3" class="na">not measured at this budget</td></tr>';
    }
    return "<tr><td>" + esc(a.name) + scopeBadge(a.scope) + "</td><td>" +
      hits(d.tp) + " of " + d.total + "</td><td>" +
      (d.total ? pct(d.tp / d.total) : "—") + "</td><td>" + num(d.alerts) + "</td></tr>";
  }).join("");

  return '<div class="card"><div class="tblwrap"><table>' +
    "<thead><tr><th>arm</th><th>labelled events found</th><th>recall</th>" +
    "<th>alerts issued</th></tr></thead><tbody>" + body + "</tbody></table></div>" +
    '<div class="caption">Each detector arm ranks on its own p-value alone, beside the ' +
    "composite rather than instead of it. A combination pays a multiplicity cost of " +
    "order J for carrying detectors that do not discriminate; these arms measure what " +
    "each detector would have found on its own.</div>" + "</div>";
}

function entityDayPanel(run) {
  const budget = String(state.budget);
  const rows = run.arms.filter(a => a.unit === "entity-day");
  if (!rows.length) return "";
  const body = rows.map(a => {
    const d = a.detections[budget];
    if (!d) return "<tr><td>" + esc(a.name) +
      '</td><td colspan="3" class="na">not measured at this budget</td></tr>';
    return "<tr><td>" + esc(a.name) + "</td><td>" + hits(d.tp) + " of " + d.total +
      "</td><td>" + (d.total ? pct(d.tp / d.total) : "—") + "</td><td>" +
      num(d.alerts) + "</td></tr>";
  }).join("");
  const degenerate = rows.some(a => {
    const d = a.detections[budget];
    return d && d.alerts >= (run.entity_days_total || Infinity);
  });
  return "<h2>Ranking accounts rather than events</h2>" +
    '<div class="card"><div class="tblwrap"><table>' +
    "<thead><tr><th>ranking</th><th>labelled entity-days found</th><th>recall</th>" +
    "<th>entity-days alerted</th></tr></thead><tbody>" + body +
    "</tbody></table></div>" +
    '<div class="caption"><b>Read with the confound.</b> Fisher over an entity-day sums ' +
    "−2 ln P across that entity's events, so it grows linearly in the event count and " +
    "sorts by activity at least as much as by anomaly. Attackers use busy service and " +
    "admin accounts, so recall follows activity — exactly the shape of result that " +
    "flatters a framework falsely. The corrected minimum, which penalises the event " +
    "count, detects nothing at all. These are rankings, not calibrated p-values at " +
    "entity scope: an entity's own events are not independent." +
    (degenerate ? " <b>At this budget the alert list covers the whole population, so " +
      "recall is trivially complete and carries no information.</b>" : "") +
    "</div></div>";
}

// The head-to-head. Grouped by scope rather than sorted by score, because a population
// baseline beating another population baseline is a different fact from a per-entity
// baseline doing so.
function baselinePanel(run) {
  if (!DATA.baselines.length) {
    return "<h2>Against industry baselines</h2>" +
      '<div class="card notrun">No baselines result is present in <code>results/</code>. ' +
      '<span class="notrun-mark">NOT RUN</span></div>';
  }
  const base = selectedBaseline();
  const budget = String(state.budget);
  const shared = commonDays(run.days, base.days);
  if (!shared.length) {
    return "<h2>Against industry baselines</h2>" +
      '<div class="card"><div class="warn critical"><b>not comparable</b>' +
      "This run covers corpus days " + (run.days.join(", ") || "unknown") +
      " and this baselines result covers days " + base.days.join(", ") +
      ". They share no day, so no head-to-head is possible: on no event did both arms " +
      "have the opportunity to alert.</div></div>";
  }

  const frameworkRows = run.arms.filter(a => a.unit === "event").map(a => {
    const d = a.detections[budget];
    return { name: a.name + " (framework)", scope: a.scope, family: "calibrated per-entity",
             tp: d ? d.tp : null, total: d ? d.total : null };
  });
  const baselineRows = base.models.map(m => {
    const d = m.detections[budget];
    return { name: m.name, scope: m.scope, family: m.family,
             tp: d ? d.tp : null, total: d ? d.total : null };
  });

  const all = frameworkRows.concat(baselineRows);
  const perEntity = all.filter(r => r.scope === "per-entity");
  const mixed     = all.filter(r => r.scope === "mixed");
  const population= all.filter(r => r.scope === "population");

  const row = r => "<tr><td>" + esc(r.name) + scopeBadge(r.scope) + "</td><td>" +
    esc(r.family) + "</td><td>" +
    (r.tp === null ? '<span class="na">not measured</span>'
                   : hits(r.tp) + " of " + r.total) + "</td></tr>";
  const group = (title, rows) => rows.length
    ? '<tr><th colspan="3" style="text-align:left">' + title + "</th></tr>" +
      rows.map(row).join("")
    : "";

  const mismatch = run.labelled_scored !== base.redteam;
  return "<h2>Against industry baselines</h2><div class=\"card\">" +
    '<div class="tblwrap"><table><thead><tr><th>model</th><th>family</th>' +
    "<th>labelled events found</th></tr></thead><tbody>" +
    group("Asks whether an entity acted unlike itself", perEntity) +
    group("Combines both kinds of question", mixed) +
    group("Asks whether an entity acted unlike the population", population) +
    "</tbody></table></div>" +
    '<div class="caption">Restricted to the ' + shared.length + " corpus day" +
    (shared.length === 1 ? "" : "s") + " both arms scored (" + shared.join(", ") +
    "), so on every event counted here both had the opportunity to alert." +
    (mismatch ? " <b>The two arms scored different labelled populations (" +
      run.labelled_scored + " against " + base.redteam + "), so the denominators " +
      "differ and the counts are not a like-for-like ratio.</b>" : "") +
    " The per-entity baseline is the one that matters: it shares the framework's " +
    "framing and has none of its calibration machinery, so the gap between it and the " +
    "framework's own arms is what the calibration is worth." +
    "</div>" +
    (base.warnings || []).map(w => '<div class="warn ' + esc(w.severity) + '"><b>' +
      esc(w.severity) + "</b>" + esc(w.text) + "</div>").join("") +
    '<div class="prov">baselines ' + esc(base.run_id) + " · " + esc(base.file) +
    " · " + num(base.rows) + " rows, " + num(base.entities) + " entities · per-entity " +
    "history " + (base.history_intact ? "intact" : "DECIMATED") + "</div>" +
    "</div>";
}

// Detection by anomaly category: the table that makes an advantage attributable to a
// KIND of anomaly rather than asserted in aggregate.
function categoryPanel(run) {
  const budget = String(state.budget);
  const base = DATA.baselines.length ? selectedBaseline() : null;

  const arms = run.arms.filter(a => a.unit === "event" && a.per_category);
  const models = base ? base.models : [];

  const header = "<tr><th>category</th><th>labelled events</th>" +
    arms.map(a => "<th>" + esc(a.name) + "</th>").join("") +
    models.map(m => "<th>" + esc(m.name) + "</th>").join("") + "</tr>";

  const body = DATA.categories.map(cat => {
    const total = run.categories[cat.id] ?? 0;
    const cells = [];
    for (const a of arms) {
      const counts = a.per_category[budget];
      cells.push("<td>" + (counts ? hits(counts[cat.id] ?? 0) : '<span class="na">—</span>') + "</td>");
    }
    for (const m of models) {
      const detected = (m.detected || {})[budget];
      if (!detected) { cells.push('<td><span class="zero">0</span></td>'); continue; }
      let n = 0;
      for (const key of detected) {
        const cats = run.labelled[key];
        if (cats && cats.includes(cat.id)) n++;
      }
      cells.push("<td>" + hits(n) + "</td>");
    }
    // A category whose structural test depended on this run's composition is marked, so
    // one column is never read as though two runs asked it the same question.
    const scope = (run.category_scopes || {})[cat.id];
    return '<tr class="' + (cat.is_control ? "control-row" : "") + '"><td>' +
      esc(cat.id) + (cat.is_control ? " <b>(control)</b>" : "") +
      (scope ? '<br><span class="na">scope in this run: ' + esc(scope) + "</span>" : "") +
      "</td><td>" + total + "</td>" + cells.join("") + "</tr>";
  }).join("");

  const notAttributable = run.arms
    .filter(a => a.unit === "event" && !a.per_category && a.not_attributable)
    .map(a => a.name);

  return "<h2>Detection by anomaly category</h2><div class=\"card\">" +
    '<div class="tblwrap"><table><thead>' + header + "</thead><tbody>" + body +
    "</tbody></table></div>" +
    '<div class="caption">Categories are structural properties of an event relative to ' +
    "the history it was scored against, and are <b>deliberately not</b> assigned by " +
    "which detector produced the smallest p-value — a partition drawn along our own " +
    "detectors' firing would be chosen in our favour, and every margin computed on it " +
    "would be circular. <b>" + esc(controlName()) + " is the control:</b> it is what " +
    "isolation-based detectors answer well, so it is the row where the framework should " +
    "show no advantage, and the row that tells you whether the others mean anything." +
    (notAttributable.length
      ? " <br><br><b>Absent from this table:</b> " + esc(notAttributable.join(", ")) +
        " — these arms record detection counts without naming the events behind them, " +
        "so their detections cannot be attributed to a category. That is a gap in what " +
        "the run records, not a zero."
      : "") +
    "</div>" +
    taxonomyTable() + "</div>";
}

// Detection by attack type: the coverage map. Each synthetic attack type was built to ask
// ONE question, so a row of zeroes against a column says which mechanism a model cannot
// express — which is the thing the real campaign's uneven type mix cannot tell you.
//
// A matrix rather than a bar chart: fifteen models against seven types is far past the point
// where grouped bars are readable, and "which cell is dark" is exactly the question.
function attackTypePanel(run) {
  const types = DATA.attack_types;
  if (!types) return "";
  const budget = String(state.budget);
  const base = DATA.baselines.length ? selectedBaseline() : null;

  // Columns: every planted type in the order it was designed in, then the real campaign,
  // which is never summed with them.
  const planted = types.order && types.order.length
    ? types.order
    : Object.keys(types.planted || {}).sort();
  const columns = planted.concat([REAL_CAMPAIGN]);

  // Denominator per column, counted from the labelled events THIS run actually scored, not
  // from what was planted: an event outside the scored window is not a miss anyone could
  // have avoided, and dividing by it would understate every model equally but wrongly.
  const scored = {};
  for (const key of Object.keys(run.labelled || {})) {
    const entity = key.slice(key.indexOf("|") + 1);
    const kind = types.victim_type[entity] || REAL_CAMPAIGN;
    scored[kind] = (scored[kind] || 0) + 1;
  }

  const rows = [];
  for (const a of run.arms) {
    if (a.unit !== "event" || !a.per_type) continue;
    rows.push({ name: "ours: " + a.name, scope: a.scope, counts: a.per_type[budget] || {} });
  }

  // The baselines come from their own run, and a baseline computed over a different labelled
  // population cannot share this table: its counts would be divided by THIS run's
  // denominators. Rendering showed entity_ewma at "1 (0.18%)" — one detection out of 262
  // labelled events, presented as a rate over 549. So the baseline rows are admitted only on
  // a matched population, and refused with the numbers named when they are not.
  const matched = base && run.labelled_scored === base.redteam;
  if (matched) {
    for (const m of base.models) {
      if (!m.per_type) continue;
      rows.push({ name: "base: " + m.name, scope: m.scope, counts: m.per_type[budget] || {} });
    }
  }
  const unmatched = base && !matched
    ? '<div class="warn"><b>baselines withheld from this table</b>' +
      "The selected baselines run <code>" + esc(base.run_id) + "</code> scored " +
      num(base.redteam) + " labelled events and this run scored " + num(run.labelled_scored) +
      ". Its detections would be counted against this run's denominators, which is not a " +
      "measurement of anything. Select a baselines run over the same events, or read its " +
      "own numbers in the panel above. This is a missing comparison, not a zero.</div>"
    : "";

  if (!rows.length) {
    return "<h2>Detection by attack type</h2><div class=\"card\">" +
      '<div class="warn notrun"><b>NOT RUN</b>No run in this directory was scored against ' +
      "the planted ground truth, so there is nothing to break down by attack type. The " +
      "taxonomy is present (" + esc(types.file) + ") but no result references it.</div></div>";
  }

  const header = '<tr><th style="text-align:left">model</th>' +
    columns.map(c => "<th>" + esc(c.replace(/_/g, " ")) + "</th>").join("") + "</tr>";
  const denom = '<tr class="denom"><td style="text-align:left">labelled events scored</td>' +
    columns.map(c => "<td>" + num(scored[c] || 0) + "</td>").join("") + "</tr>";

  const body = rows.map(r => {
    const cells = columns.map(c => {
      const n = r.counts[c] || 0;
      const total = scored[c] || 0;
      const recall = total ? n / total : 0;
      return '<td class="cell" style="' + heatStyle(recall) + '" title="' +
        esc(r.name + " caught " + n + " of " + total + " " + c) + '">' +
        (total === 0 ? '<span class="na">—</span>'
                     : n + '<span class="sub">' + pct(recall) + "</span>") + "</td>";
    });
    return '<tr><td style="text-align:left">' + esc(r.name) + scopeBadge(r.scope) +
      "</td>" + cells.join("") + "</tr>";
  }).join("");

  return "<h2>Detection by attack type</h2><div class=\"card\">" +
    unmatched +
    '<div class="tblwrap"><table><thead>' + header + "</thead><tbody>" +
    denom + body + "</tbody></table></div>" +
    '<div class="caption"><b>The synthetic columns and the real column answer different ' +
    "questions and must not be added together.</b> A planted attack measures whether a " +
    "detector responds to a mechanism <i>by construction</i>; the real campaign measures " +
    "whether it found an actual intrusion. Every planted event is individually plausible — " +
    "each destination, authentication type and logon type is a value that occurs in the " +
    "corpus — so only the combination, the timing or the volume is unprecedented for the " +
    "victim. Victims are disjoint from every account the real labels name, which is what " +
    "makes the two columns separable at all. Shading is recall within the column; the " +
    "number is the count." +
    "</div>" + premiseTable(types) +
    '<div class="prov">taxonomy ' + esc(types.run_id) + " · " + esc(types.file) + " · " +
    num(Object.keys(types.victim_type).length) + " victim accounts</div>" +
    "</div>";
}

// heatStyle shades a cell by recall on one hue, light to dark, so the eye reads magnitude
// and not category. Text flips to the page colour once the ground is dark enough to need it.
function heatStyle(recall) {
  if (recall <= 0) return "";
  const t = Math.min(1, Math.sqrt(recall)); // sqrt: the interesting range here is the low end
  return "background:color-mix(in oklab, var(--accent) " + (12 + 76 * t).toFixed(0) +
    "%, var(--page));" + (t > 0.55 ? "color:var(--page);" : "");
}

// premiseTable states what each type was built to ask. Without it a reader concludes a
// detector is bad at an attack that was constructed to carry a signal it cannot express —
// low_and_slow is deliberately the case the dispersion widening tolerates.
function premiseTable(types) {
  const kinds = (types.order && types.order.length
    ? types.order
    : Object.keys(types.premise || {}).sort()).filter(k => (types.premise || {})[k]);
  if (!kinds.length) return "";
  return '<div class="tblwrap"><table><thead><tr>' +
    '<th style="text-align:left">attack type</th><th>planted</th>' +
    '<th style="text-align:left">the one question it asks</th></tr></thead><tbody>' +
    kinds.map(k => '<tr><td style="text-align:left">' + esc(k.replace(/_/g, " ")) +
      "</td><td>" + num((types.planted || {})[k] || 0) + '</td><td style="text-align:left">' +
      esc(types.premise[k]) + "</td></tr>").join("") +
    "</tbody></table></div>";
}

function controlName() {
  const c = DATA.categories.find(c => c.is_control);
  return c ? c.id : "population_rare";
}

function taxonomyTable() {
  if (!DATA.categories.some(c => c.contrast)) return "";
  return '<div class="tblwrap"><table><thead><tr><th style="text-align:left">category</th>' +
    '<th style="text-align:left">structural test</th>' +
    '<th style="text-align:left">why a population model cannot express it</th></tr></thead><tbody>' +
    DATA.categories.map(c =>
      '<tr><td style="text-align:left">' + esc(c.id) + '</td><td style="text-align:left">' +
      esc(c.test) + '</td><td style="text-align:left">' + esc(c.contrast) + "</td></tr>"
    ).join("") + "</tbody></table></div>";
}

// Calibration: the share of scored events each detector calls astronomically significant.
// A well-specified null puts almost nothing below 1e-12; a large share is a null that is
// wrong rather than merely loose.
function calibrationPanel(run) {
  if (!run.detectors || !run.detectors.length) return "";
  const body = run.detectors.map(d => {
    const bad = d.share > 0.01;
    return "<tr><td>" + esc(d.name) + scopeBadge(d.scope) + "</td><td>" +
      num(d.evaluated) + "</td><td>" + num(d.abstained) + "</td><td" +
      (bad ? ' style="color:var(--crit);font-weight:700"' : "") + ">" +
      pct(d.share) + "</td></tr>";
  }).join("");
  return "<h2>Detector calibration</h2><div class=\"card\">" +
    '<div class="tblwrap"><table><thead><tr><th>detector</th><th>evaluated</th>' +
    "<th>abstained</th><th>share below p = 1e−12</th></tr></thead><tbody>" + body +
    "</tbody></table></div>" +
    '<div class="caption">A well-specified null puts almost nothing below 1e−12. A large ' +
    "share is a null that is <b>wrong rather than loose</b>, and such a detector dominates " +
    "any combination it enters — which is how an informative detector's signal gets " +
    "averaged away. Abstentions are not scores: a detector with no basis for an opinion " +
    "declines, and the combination drops it from the degrees of freedom rather than " +
    "receiving a confident middle value from it.</div></div>";
}

function scoreboardPanel() {
  return "<h2>Hypothesis scoreboard</h2>" + DATA.hypotheses.map(h =>
    '<div class="card' + (h.runs.length ? "" : " notrun") + '">' +
    '<span class="eid">' + esc(h.id) + "</span>" + esc(h.title) +
    (h.runs.length
      ? '<div class="prov">' + h.runs.map(esc).join(" · ") + "</div>"
      : ' — <span class="notrun-mark">NOT RUN</span>')
    + "</div>").join("");
}

// The headline, computed from the selected run rather than written down.
//
// It used to be a hardcoded paragraph asserting that no arm detected a labelled event at any
// budget. That was true when only the composite had been measured, and it stayed on the page
// after the per-detector arms were added — so the page carried a claim its own tables
// contradicted, by 60 detections. A summary that cannot go stale is one that is derived.
function headlinePanel(run) {
  const budget = String(state.budget);
  const at = (a) => (a.detections || {})[budget] || {};
  const eventArms = run.arms.filter(a => a.unit === "event");

  const composite = eventArms.find(a => a.group === "composite");
  const compositeTP = composite ? (at(composite).tp ?? 0) : null;

  // The best single detector, which is the number a combination has to beat to be worth its
  // multiplicity cost.
  let best = null;
  for (const a of eventArms.filter(a => a.group === "detector")) {
    if (best === null || (at(a).tp ?? 0) > (at(best).tp ?? 0)) best = a;
  }
  const bestTP = best ? (at(best).tp ?? 0) : null;
  const total = best ? (at(best).total ?? run.labelled_scored) : run.labelled_scored;

  const base = DATA.baselines.length ? selectedBaseline() : null;
  let bestBase = null;
  for (const m of (base ? base.models : [])) {
    if (bestBase === null || (at(m).tp ?? 0) > (at(bestBase).tp ?? 0)) bestBase = m;
  }

  let verdict;
  if (bestTP === null) {
    verdict = "<b>This run recorded no per-detector arm</b>, so the comparison this page " +
      "exists to make is not available from it.";
  } else if (bestTP === 0) {
    verdict = "<b>The headline is negative.</b> No arm detects a labelled event at " +
      state.budget + " alerts per analyst-day on this run.";
  } else {
    verdict = "<b>The signal is in one place.</b> The best single detector, " +
      esc(best.name) + scopeBadge(best.scope) + ", finds <b>" + bestTP + " of " + total +
      "</b> at " + state.budget + " alerts per analyst-day" +
      (compositeTP === null ? "" :
        ", while the composite — the combined verdict the system ships — finds <b>" +
        compositeTP + "</b>" +
        (compositeTP < bestTP
          ? ". Combining destroys the signal its own best component carries."
          : "."));
  }

  const against = bestBase
    ? " Against the published baselines on the selected comparison, the best is " +
      esc(bestBase.name) + scopeBadge(bestBase.scope) + " at " + (at(bestBase).tp ?? 0) +
      "." + (run.labelled_scored !== base.redteam
        ? " <b>Those two arms scored different labelled populations (" +
          num(run.labelled_scored) + " against " + num(base.redteam) +
          "), so the counts are not a like-for-like ratio.</b>"
        : "")
    : "";

  // The project's standing claim is that per-entity detectors carry the signal. On a run
  // where a population-scope arm wins, asserting it anyway would put the page at odds with
  // its own table — so the claim is stated only when this run supports it, and the exception
  // is named when it does not.
  const perEntityTotal = eventArms
    .filter(a => a.group === "detector" && a.scope === "per-entity")
    .reduce((n, a) => Math.max(n, at(a).tp ?? 0), 0);
  const populationTotal = eventArms
    .filter(a => a.group === "detector" && a.scope === "population")
    .reduce((n, a) => Math.max(n, at(a).tp ?? 0), 0);

  let framing;
  if (perEntityTotal === 0 && populationTotal === 0) {
    framing = "No detector of either scope discriminates on this run, so it says nothing " +
      "about which framing is right.";
  } else if (perEntityTotal > populationTotal) {
    framing = "What the measurements establish is narrower than a general improvement: " +
      "<b>per-entity detectors carry signal on this corpus and population-scope detectors " +
      "do not</b> — best per-entity " + perEntityTotal + " against best population " +
      populationTotal + ".";
  } else {
    framing = "<b>On this run a population-scope detector leads</b>, at " + populationTotal +
      " against " + perEntityTotal + " for the best per-entity arm. That is the opposite of " +
      "this project's standing finding, so read it as a question about this run rather than " +
      "as a result: check whether the labelled events it caught are population-rare by " +
      "construction before drawing anything from it.";
  }

  return "<p>" + verdict + against + "</p><p>" + framing + " Every number here comes from " +
    "the selected run, so this summary cannot disagree with the tables below it.</p>";
}

// ---------------------------------------------------------------- render
function render() {
  const run = selectedRun();
  $("headline").innerHTML = headlinePanel(run);
  $("body").innerHTML =
    warningsPanel(run) +
    summaryPanel(run) +
    "<h2>Detection by arm, at " + state.budget + " alerts per analyst-day</h2>" +
    armsPanel(run) +
    baselinePanel(run) +
    attackTypePanel(run) +
    categoryPanel(run) +
    entityDayPanel(run) +
    calibrationPanel(run) +
    scoreboardPanel();
}

if (!DATA.runs.length) {
  // The headline is derived from a run, so with no run there is nothing to say and the empty
  // box is removed rather than left as an unexplained frame.
  $("headline").remove();
  $("body").innerHTML = '<div class="card notrun">No replay result is present in ' +
    "<code>results/</code>. <span class=\"notrun-mark\">NOT RUN</span></div>";
} else {
  fillControls();
  render();
}
</script>
</body>
</html>
`
