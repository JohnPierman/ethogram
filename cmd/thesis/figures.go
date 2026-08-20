package main

// Diagrams for the thesis, hand-authored as inline SVG.
//
// # The rule each of these is held to
//
// A diagram earns its place when it lets a cold reader see a mechanism they would otherwise
// have to assemble from prose. Where two options are compared, the figure draws the
// DIFFERENCE — the band that moves, the bin that splits, the arrow that reverses — because
// two labelled boxes side by side is a restated option list, not a comparison.
//
// # Theme and colour
//
// Structure, text and arrowheads use `currentColor`, so they follow the page's foreground
// in light and dark alike. Exactly two literal hues carry meaning, via CSS custom
// properties the page defines per mode:
//
//	--fig-a   the per-entity question, the corrected form, the "after"
//	--fig-b   the population question, the defective form, the "before"
//
// The pair is slot 1 (blue) and slot 2 (orange) of a validated categorical order, and it
// passes every hard gate in both modes: worst adjacent CVD ΔE 24.7 light / 26.8 dark
// against an ≥8 target, normal-vision ΔE 33.6 / 31.8 against a ≥15 floor, contrast ≥3:1 on
// both surfaces. A hue means the same thing in every figure, so it is never reassigned.
//
// Every figure also carries a direct label, so identity is never colour alone.
//
// Arrowheads reference `#fig-arrow`, defined once by the page template rather than per
// figure: repeating the marker in each fragment would duplicate an id across the document.

// figures returns every diagram, keyed by the id a `<!-- figure: id -->` anchor uses.
func figures() map[string]string {
	return map[string]string{
		"population-vs-entity":   figPopulationVsEntity,
		"score-before-observe":   figScoreBeforeObserve,
		"novelty-tail":           figNoveltyTail,
		"open-closed-vocabulary": figOpenClosedVocabulary,
		"circular-vs-grid":       figCircularVsGrid,
		"volume-null":            figVolumeNull,
		"pairing-scope":          figPairingScope,
		"combination-destroys":   figCombinationDestroys,
		"category-lift":          figCategoryLift,
		"base-rate":              figBaseRate,
		"abstention":             figAbstention,
	}
}

// sharedDefs is emitted once by the page template. Arrow markers inherit currentColor via
// `context-stroke` where supported, with a plain fill fallback.
const sharedDefs = `<svg width="0" height="0" aria-hidden="true" focusable="false" style="position:absolute">
<defs>
<marker id="fig-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
<path d="M0,1 L9,5 L0,9 z" fill="currentColor"/>
</marker>
<marker id="fig-arrow-a" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
<path d="M0,1 L9,5 L0,9 z" fill="var(--fig-a)"/>
</marker>
<marker id="fig-arrow-b" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="6" markerHeight="6" orient="auto-start-reverse">
<path d="M0,1 L9,5 L0,9 z" fill="var(--fig-b)"/>
</marker>
</defs>
</svg>`

// ---------------------------------------------------------------------------
// The governing idea: which reference the question is asked against
// ---------------------------------------------------------------------------

const figPopulationVsEntity = `<figure class="fig">
<svg viewBox="0 0 760 330" role="img" class="figsvg"
 aria-label="Two accounts judged against the population's working hours, then against each account's own hours. The population reference flags the night-shift engineer on every event and misses the accountant's 3am event. Each account's own reference does the opposite.">
<g font-size="12" fill="currentColor">

<text x="0" y="14" font-size="13" font-weight="600" fill="var(--fig-b)">Compared to everyone</text>
<text x="400" y="14" font-size="13" font-weight="600" fill="var(--fig-a)">Compared to itself</text>

<g stroke="currentColor" stroke-width="1" opacity="0.45">
<line x1="0" y1="300" x2="340" y2="300"/><line x1="400" y1="300" x2="740" y2="300"/>
</g>
<g font-size="10" opacity="0.7">
<text x="0" y="316" text-anchor="start">00:00</text><text x="113" y="316" text-anchor="middle">08:00</text><text x="227" y="316" text-anchor="middle">16:00</text><text x="340" y="316" text-anchor="end">24:00</text>
<text x="400" y="316" text-anchor="start">00:00</text><text x="513" y="316" text-anchor="middle">08:00</text><text x="627" y="316" text-anchor="middle">16:00</text><text x="740" y="316" text-anchor="end">24:00</text>
</g>

<rect x="128" y="88" width="114" height="180" fill="var(--fig-b)" opacity="0.13"/>
<text x="185" y="34" font-size="10" text-anchor="middle" fill="var(--fig-b)">one band for everyone: 09:00–17:00</text>

<rect x="400" y="88" width="42" height="60" fill="var(--fig-a)" opacity="0.16"/>
<rect x="726" y="88" width="14" height="60" fill="var(--fig-a)" opacity="0.16"/>
<rect x="583" y="208" width="71" height="60" fill="var(--fig-a)" opacity="0.16"/>
<text x="570" y="34" font-size="10" text-anchor="middle" fill="var(--fig-a)">a band per account, from its own past</text>

<text x="0" y="76" font-size="11">night-shift engineer</text>
<text x="400" y="76" font-size="11">night-shift engineer</text>
<text x="0" y="196" font-size="11">day-shift accountant</text>
<text x="400" y="196" font-size="11">day-shift accountant</text>

<g>
<circle cx="7" cy="110" r="5" fill="var(--fig-b)"/><circle cx="21" cy="110" r="5" fill="var(--fig-b)"/><circle cx="35" cy="110" r="5" fill="var(--fig-b)"/>
<circle cx="49" cy="110" r="5" fill="var(--fig-b)"/><circle cx="63" cy="110" r="5" fill="var(--fig-b)"/><circle cx="77" cy="110" r="5" fill="var(--fig-b)"/>
<circle cx="91" cy="110" r="5" fill="var(--fig-b)"/><circle cx="105" cy="110" r="5" fill="var(--fig-b)"/><circle cx="326" cy="110" r="5" fill="var(--fig-b)"/>
<text x="0" y="136" font-size="10" fill="var(--fig-b)">hundreds of events, every one of them outside the band, every one flagged</text>
</g>

<g>
<circle cx="185" cy="230" r="5" fill="currentColor" opacity="0.55"/><circle cx="199" cy="230" r="5" fill="currentColor" opacity="0.55"/>
<circle cx="21" cy="230" r="5" fill="var(--fig-b)"/>
<circle cx="21" cy="230" r="9" fill="none" stroke="var(--fig-b)" stroke-width="1.5"/>
<text x="0" y="256" font-size="10" fill="var(--fig-b)">flagged too — and buried among the engineer's hundreds when the budget is spent</text>
</g>

<g>
<circle cx="414" cy="110" r="5" fill="currentColor" opacity="0.55"/><circle cx="428" cy="110" r="5" fill="currentColor" opacity="0.55"/><circle cx="733" cy="110" r="5" fill="currentColor" opacity="0.55"/>
<text x="446" y="136" font-size="10" opacity="0.75">nothing flagged — this is what it always does</text>
</g>

<g>
<circle cx="585" cy="230" r="5" fill="currentColor" opacity="0.55"/><circle cx="599" cy="230" r="5" fill="currentColor" opacity="0.55"/>
<circle cx="421" cy="230" r="5" fill="var(--fig-a)"/>
<circle cx="421" cy="230" r="9" fill="none" stroke="var(--fig-a)" stroke-width="1.5"/>
<text x="446" y="256" font-size="10" fill="var(--fig-a)">the 03:00 event flagged — it never did this before</text>
</g>

</g>
</svg>
<figcaption><strong>The reference moves with the account.</strong> Both panels hold the same
two accounts and the same events. On the left one band serves everyone, and the failure is
not that the accountant's 03:00 event goes unflagged — it is flagged. It is that the
night-shift engineer is flagged on <em>every event it ever produces</em>, so the day's alert
budget is spent on accounts that are behaving exactly as they always have, and the one event
that matters is buried among them. On the right each account is judged against its own past:
the engineer's night work is unremarkable, and the accountant's 03:00 event is the only thing
left to look at. Nothing about the events changed, only what they were compared
against.</figcaption>
</figure>`

// ---------------------------------------------------------------------------
// The ordering that makes novelty detection possible at all
// ---------------------------------------------------------------------------

const figScoreBeforeObserve = `<figure class="fig">
<svg viewBox="0 0 760 250" role="img" class="figsvg"
 aria-label="Observing before scoring makes a first-ever value already known by the time it is scored, so it is reported as ordinary. Scoring before observing reports it as novel. The only difference is the order of two steps.">
<g font-size="12" fill="currentColor">

<text x="0" y="14" font-size="13" font-weight="600" fill="var(--fig-b)">Observe, then score</text>
<text x="400" y="14" font-size="13" font-weight="600" fill="var(--fig-a)">Score, then observe</text>

<g fill="none" stroke="currentColor" stroke-width="1.5">
<rect x="0" y="34" width="120" height="40" rx="4"/>
<rect x="0" y="106" width="120" height="40" rx="4"/>
<rect x="200" y="70" width="130" height="40" rx="4"/>
<rect x="400" y="34" width="120" height="40" rx="4"/>
<rect x="400" y="106" width="120" height="40" rx="4"/>
<rect x="600" y="70" width="130" height="40" rx="4"/>
</g>
<g text-anchor="middle" font-size="11">
<text x="60" y="58">event: value X</text>
<text x="60" y="130">history of X</text>
<text x="265" y="88">score X</text>
<text x="460" y="58">event: value X</text>
<text x="460" y="130">history of X</text>
<text x="665" y="88">score X</text>
</g>

<g stroke="var(--fig-b)" stroke-width="1.5" fill="none" marker-end="url(#fig-arrow-b)">
<path d="M120,54 C165,54 165,120 196,124"/>
<path d="M120,126 L196,94"/>
</g>
<text x="128" y="76" font-size="10" fill="var(--fig-b)">1. record X first</text>
<text x="128" y="146" font-size="10" fill="var(--fig-b)">2. read: X is known</text>

<g stroke="var(--fig-a)" stroke-width="1.5" fill="none" marker-end="url(#fig-arrow-a)">
<path d="M520,54 L596,84"/>
<path d="M520,126 L596,100"/>
</g>
<text x="528" y="46" font-size="10" fill="var(--fig-a)">1. score against history</text>
<text x="528" y="146" font-size="10" fill="var(--fig-a)">2. record X after</text>

<text x="265" y="140" font-size="11" text-anchor="middle" fill="var(--fig-b)">p is high</text>
<text x="265" y="158" font-size="11" text-anchor="middle" fill="var(--fig-b)">&#8220;seen before&#8221;</text>
<text x="265" y="182" font-size="12" text-anchor="middle" font-weight="600" fill="var(--fig-b)">MISSED</text>
<text x="665" y="140" font-size="11" text-anchor="middle" fill="var(--fig-a)">p is small</text>
<text x="665" y="158" font-size="11" text-anchor="middle" fill="var(--fig-a)">&#8220;first ever&#8221;</text>
<text x="665" y="182" font-size="12" text-anchor="middle" font-weight="600" fill="var(--fig-a)">ALERT</text>

<text x="0" y="228" font-size="11" opacity="0.8">Both arrangements return a number. Only one of them is measuring anything.</text>
</g>
</svg>
<figcaption><strong>The failure is silent, which is what makes it dangerous.</strong> Update
state before scoring and a first-ever value is already a known value by the time it is
judged, so novelty detection dies while still emitting plausible p-values. Nothing in the
output announces it. The framework therefore makes the ordering unexpressible rather than
conventional: <code>Score</code> holds no means of writing, and the update it computes does
not exist until it has produced it.</figcaption>
</figure>`

// ---------------------------------------------------------------------------
// Detector I
// ---------------------------------------------------------------------------

const figNoveltyTail = `<figure class="fig">
<svg viewBox="0 0 800 300" role="img" class="figsvg"
 aria-label="An account's decayed counts over five known values plus reserved mass for anything unseen. The p-value is the total mass of every outcome no more likely than the one observed, which is the shaded region.">
<g font-size="12" fill="currentColor">

<g stroke="currentColor" stroke-width="1" opacity="0.45">
<line x1="60" y1="220" x2="640" y2="220"/>
</g>

<rect x="80"  y="70"  width="70" height="150" fill="currentColor" opacity="0.18"/>
<rect x="170" y="110" width="70" height="110" fill="currentColor" opacity="0.18"/>
<rect x="260" y="150" width="70" height="70"  fill="currentColor" opacity="0.18"/>
<rect x="350" y="186" width="70" height="34"  fill="var(--fig-a)" opacity="0.30"/>
<rect x="440" y="196" width="70" height="24"  fill="var(--fig-a)" opacity="0.30"/>
<rect x="530" y="202" width="70" height="18"  fill="var(--fig-a)" opacity="0.30"/>

<g text-anchor="middle" font-size="11">
<text x="115" y="238">C625</text><text x="205" y="238">C529</text><text x="295" y="238">C467</text>
<text x="385" y="238">C793</text><text x="475" y="238">C112</text>
<text x="565" y="238" fill="var(--fig-a)">never seen</text>
</g>
<g text-anchor="middle" font-size="10" opacity="0.7">
<text x="115" y="62">412</text><text x="205" y="102">298</text><text x="295" y="142">184</text>
<text x="385" y="178">31</text><text x="475" y="188">18</text>
</g>

<line x1="350" y1="186" x2="648" y2="186" stroke="var(--fig-a)" stroke-width="1.5" stroke-dasharray="4 3"/>
<text x="654" y="182" font-size="10" fill="var(--fig-a)">P&#770;(observed) &#8212; the line</text>
<text x="654" y="196" font-size="10" fill="var(--fig-a)">everything below is at or under</text>

<polygon points="385,262 379,274 391,274" fill="var(--fig-a)"/>
<text x="385" y="290" font-size="11" text-anchor="middle" fill="var(--fig-a)">observed here</text>

<text x="60" y="24" font-size="11">decayed count of each value this account has used</text>
<text x="60" y="44" font-size="11" fill="var(--fig-a)">shaded: every outcome no more likely than the one observed — their total mass is p</text>
</g>
</svg>
<figcaption><strong>Detector I asks how much probability sits at or below what happened.</strong>
The account's counts decay with age, and a slice of mass is held back for values it has
never used — so a first-ever value is improbable rather than impossible, and a first-ever
value on an account with no history at all is not an alert. The p-value is not the height of
one bar but the total shaded mass, which is what makes it comparable across fields with
wildly different vocabularies.</figcaption>
</figure>`

const figOpenClosedVocabulary = `<figure class="fig">
<svg viewBox="0 0 760 300" role="img" class="figsvg"
 aria-label="Two account histories. One uses three values thousands of times, the other five hundred values once each. A fixed-alpha estimator gives both nearly the same probability of a new value; Good-Turing gives near zero and near one respectively.">
<g font-size="12" fill="currentColor">

<text x="0" y="14" font-size="13" font-weight="600">A closed vocabulary</text>
<text x="400" y="14" font-size="13" font-weight="600">An open vocabulary</text>
<text x="0" y="32" font-size="11" opacity="0.8">3 hosts, ~1000 visits each</text>
<text x="400" y="32" font-size="11" opacity="0.8">500 addresses, seen once each</text>

<g>
<rect x="0"  y="48" width="60" height="90" fill="currentColor" opacity="0.18"/>
<rect x="70" y="52" width="60" height="86" fill="currentColor" opacity="0.18"/>
<rect x="140" y="56" width="60" height="82" fill="currentColor" opacity="0.18"/>
</g>
<g>
<rect x="400" y="132" width="7" height="6" fill="currentColor" opacity="0.18"/>
<rect x="411" y="132" width="7" height="6" fill="currentColor" opacity="0.18"/>
<rect x="422" y="132" width="7" height="6" fill="currentColor" opacity="0.18"/>
<rect x="433" y="132" width="7" height="6" fill="currentColor" opacity="0.18"/>
<rect x="444" y="132" width="7" height="6" fill="currentColor" opacity="0.18"/>
<rect x="455" y="132" width="7" height="6" fill="currentColor" opacity="0.18"/>
<rect x="466" y="132" width="7" height="6" fill="currentColor" opacity="0.18"/>
<rect x="477" y="132" width="7" height="6" fill="currentColor" opacity="0.18"/>
<rect x="488" y="132" width="7" height="6" fill="currentColor" opacity="0.18"/>
<rect x="499" y="132" width="7" height="6" fill="currentColor" opacity="0.18"/>
<rect x="510" y="132" width="7" height="6" fill="currentColor" opacity="0.18"/>
<rect x="521" y="132" width="7" height="6" fill="currentColor" opacity="0.18"/>
<text x="536" y="138" font-size="10" opacity="0.7">… 500 of these</text>
</g>
<line x1="0" y1="138" x2="200" y2="138" stroke="currentColor" opacity="0.45"/>
<line x1="400" y1="138" x2="600" y2="138" stroke="currentColor" opacity="0.45"/>

<text x="0" y="176" font-size="11" font-weight="600">Would a new value be surprising?</text>
<text x="400" y="176" font-size="11" font-weight="600">Would a new value be surprising?</text>
<text x="0" y="196" font-size="11">Yes, very — it has only ever used three.</text>
<text x="400" y="196" font-size="11">No — a new one appears constantly.</text>

<g font-size="11">
<text x="0" y="228" fill="var(--fig-b)">fixed &#945;:</text>
<text x="110" y="228" fill="var(--fig-b)">P(new) = 0.00033</text>
<text x="400" y="228" fill="var(--fig-b)">fixed &#945;:</text>
<text x="510" y="228" fill="var(--fig-b)">P(new) = 0.001</text>
<text x="0" y="252" fill="var(--fig-a)">Good&#8211;Turing:</text>
<text x="110" y="252" fill="var(--fig-a)">P(new) &#8776; 0</text>
<text x="400" y="252" fill="var(--fig-a)">Good&#8211;Turing:</text>
<text x="510" y="252" fill="var(--fig-a)">P(new) &#8776; 1</text>
</g>

<line x1="0" y1="266" x2="740" y2="266" stroke="currentColor" opacity="0.25"/>
<text x="0" y="286" font-size="11" fill="var(--fig-b)">The fixed-&#945; answers differ by a factor of 3, though the truth differs by everything: it counts observations, and never their shape.</text>
</g>
</svg>
<figcaption><strong>Counting observations is not the same as reading the shape of a
distribution.</strong> A fixed-concentration estimator derives reserved mass from the totals
alone, so two histories whose true answers are near zero and near one receive answers
within a factor of three of each other — and the second is what a compromised account looks
like. Good–Turing reads the singleton rate instead, so it adapts without being told which
regime it faces. Measured on this corpus it <em>costs</em> discrimination, for a reason
§9.1 sets out: these vocabularies are closed, and the honesty is expensive when the attack
signal is itself a first-ever value.</figcaption>
</figure>`

// ---------------------------------------------------------------------------
// Detector II
// ---------------------------------------------------------------------------

const figCircularVsGrid = `<figure class="fig">
<svg viewBox="0 0 760 320" role="img" class="figsvg"
 aria-label="An account active between 11pm and 1am. On a circular representation its activity is one contiguous mode across midnight. On a linear 168-cell grid the same activity is split into two bins at opposite ends of the week.">
<g font-size="12" fill="currentColor">

<text x="0" y="14" font-size="13" font-weight="600" fill="var(--fig-a)">Time of day as a circle</text>
<text x="420" y="14" font-size="13" font-weight="600" fill="var(--fig-b)">Time of day as 168 cells</text>

<circle cx="170" cy="170" r="105" fill="none" stroke="currentColor" stroke-width="1" opacity="0.45"/>
<path d="M170,170 L170,65 A105,105 0 0,1 197,68 Z" fill="var(--fig-a)" opacity="0.28"/>
<path d="M170,170 L143,68 A105,105 0 0,1 170,65 Z" fill="var(--fig-a)" opacity="0.28"/>
<g font-size="10" text-anchor="middle" opacity="0.75">
<text x="170" y="52">00:00</text><text x="292" y="174">06:00</text><text x="170" y="292">12:00</text><text x="48" y="174">18:00</text>
</g>
<line x1="170" y1="170" x2="170" y2="60" stroke="currentColor" stroke-width="1" opacity="0.35" stroke-dasharray="3 3"/>
<text x="170" y="170" font-size="11" text-anchor="middle" dy="4" fill="var(--fig-a)">one mode</text>
<text x="170" y="310" font-size="11" text-anchor="middle" fill="var(--fig-a)">23:00 and 01:00 are neighbours</text>

<g>
<rect x="420" y="150" width="300" height="40" fill="none" stroke="currentColor" opacity="0.45"/>
<g stroke="currentColor" opacity="0.2">
<line x1="450" y1="150" x2="450" y2="190"/><line x1="480" y1="150" x2="480" y2="190"/>
<line x1="510" y1="150" x2="510" y2="190"/><line x1="540" y1="150" x2="540" y2="190"/>
<line x1="570" y1="150" x2="570" y2="190"/><line x1="600" y1="150" x2="600" y2="190"/>
<line x1="630" y1="150" x2="630" y2="190"/><line x1="660" y1="150" x2="660" y2="190"/>
<line x1="690" y1="150" x2="690" y2="190"/>
</g>
<rect x="420" y="150" width="30" height="40" fill="var(--fig-b)" opacity="0.35"/>
<rect x="690" y="150" width="30" height="40" fill="var(--fig-b)" opacity="0.35"/>
</g>
<g font-size="10" text-anchor="middle" opacity="0.75">
<text x="435" y="206">00–01</text><text x="570" y="206">…</text><text x="705" y="206">23–24</text>
</g>
<path d="M446,142 C470,110 655,110 694,142" fill="none" stroke="var(--fig-b)" stroke-width="1.5" stroke-dasharray="4 3"/>
<text x="570" y="106" font-size="11" text-anchor="middle" fill="var(--fig-b)">the same two hours of activity</text>
<text x="570" y="240" font-size="11" text-anchor="middle" fill="var(--fig-b)">split across bins that never touch</text>
<text x="570" y="262" font-size="11" text-anchor="middle" fill="var(--fig-b)">each looks like half the evidence</text>
</g>
</svg>
<figcaption><strong>Midnight is not a boundary, and a rectangular encoding invents
one.</strong> An account working either side of midnight has one habit. Cut the day into
cells and that habit lands in the first and last bins, which are adjacent in time and
maximally distant in the representation, so each half is judged on half the evidence. The
circular form has no seam to fall down: measured as a recorded control, an account active
only between 23:00 and 01:00 scores 0.77 at both 23:30 and 00:30 against 0.00098 at midday,
with exactly one fitted mode inside the window.</figcaption>
</figure>`

const figVolumeNull = `<figure class="fig">
<svg viewBox="0 0 720 260" role="img" class="figsvg"
 aria-label="An account whose daily volume has always ranged from 60 to 480 events. The literal Gamma-Poisson predictive is far too narrow and scores a typical day at 1.4e-79. Widening the null by the account's own measured dispersion makes the same day unremarkable.">
<g font-size="12" fill="currentColor">

<text x="0" y="14" font-size="11">this account's daily volume has always ranged 60 – 480</text>
<rect x="90" y="26" width="330" height="14" fill="currentColor" opacity="0.14"/>
<g font-size="10" opacity="0.7">
<text x="86" y="52">60</text><text x="410" y="52">480</text>
</g>

<line x1="40" y1="180" x2="700" y2="180" stroke="currentColor" opacity="0.45"/>
<g font-size="10" opacity="0.7" text-anchor="middle">
<text x="40" y="198">0</text><text x="255" y="198">240</text><text x="470" y="198">480</text><text x="690" y="198">720</text>
</g>
<text x="370" y="222" font-size="11" text-anchor="middle" opacity="0.8">events in the window</text>

<path d="M235,180 C245,180 248,74 255,74 C262,74 265,180 275,180 Z" fill="var(--fig-b)" opacity="0.30" stroke="var(--fig-b)" stroke-width="1.5"/>
<path d="M60,180 C150,178 170,104 255,104 C340,104 380,178 460,180 C520,180 560,180 640,180" fill="none" stroke="var(--fig-a)" stroke-width="2"/>

<line x1="255" y1="60" x2="255" y2="180" stroke="currentColor" stroke-width="1" stroke-dasharray="3 3" opacity="0.6"/>
<text x="255" y="52" font-size="11" text-anchor="middle">today: 240</text>

<g font-size="11">
<text x="470" y="86" fill="var(--fig-b)">the literal predictive</text>
<text x="470" y="102" fill="var(--fig-b)">240 scores P = 1.4&#215;10&#8315;&#8311;&#8313;</text>
<text x="470" y="140" fill="var(--fig-a)">widened by the account's</text>
<text x="470" y="156" fill="var(--fig-a)">own measured spread: ordinary</text>
</g>
</svg>
<figcaption><strong>A null that narrows as history accumulates will reject an account for its
own habits.</strong> The specified predictive expresses uncertainty about the rate, and that
uncertainty shrinks with evidence — so at a seven-day half-life it becomes Poisson in all
but name, while real telemetry arrives in sessions. An account whose daily volume had always
swung between 60 and 480 scored 1.4×10⁻⁷⁹ for doing 240 again. The repair measures the
dispersion instead of assuming it away, floored so it can only ever widen a null: events
below p = 10⁻¹² fell from 22.1% to 0.2%.</figcaption>
</figure>`

// ---------------------------------------------------------------------------
// Detector III
// ---------------------------------------------------------------------------

const figPairingScope = `<figure class="fig">
<svg viewBox="0 0 760 300" role="img" class="figsvg"
 aria-label="One account has only ever used Kerberos on host C700. It now uses NTLM on C700. The population question finds the pairing common and misses it, while also firing on the account's habitual pairing. The per-entity question finds the pairing unprecedented for this account.">
<g font-size="12" fill="currentColor">

<text x="0" y="14" font-size="13" font-weight="600" fill="var(--fig-b)">Has the population paired these?</text>
<text x="420" y="14" font-size="13" font-weight="600" fill="var(--fig-a)">Has this account paired these?</text>

<g font-size="11" text-anchor="middle">
<text x="55" y="70">Kerberos</text><text x="55" y="150">NTLM</text>
<text x="270" y="70">C700</text><text x="270" y="150">C900</text>
<text x="475" y="70">Kerberos</text><text x="475" y="150">NTLM</text>
<text x="690" y="70">C700</text><text x="690" y="150">C900</text>
</g>

<g stroke="currentColor" stroke-width="1.5" opacity="0.35">
<line x1="105" y1="66" x2="235" y2="66"/>
<line x1="105" y1="70" x2="235" y2="146"/>
<line x1="105" y1="146" x2="235" y2="70"/>
<line x1="105" y1="150" x2="235" y2="150"/>
</g>
<text x="170" y="186" font-size="10" text-anchor="middle" opacity="0.75">all four pairings are common somewhere</text>

<g stroke="var(--fig-a)" stroke-width="2">
<line x1="525" y1="66" x2="655" y2="66"/>
</g>
<text x="590" y="58" font-size="10" text-anchor="middle" fill="var(--fig-a)">the only pairing it has ever made</text>

<line x1="525" y1="146" x2="655" y2="70" stroke="var(--fig-a)" stroke-width="2" stroke-dasharray="5 4"/>
<text x="590" y="186" font-size="10" text-anchor="middle" fill="var(--fig-a)">today: NTLM on C700 — never before</text>

<line x1="0" y1="212" x2="740" y2="212" stroke="currentColor" opacity="0.25"/>
<g font-size="11">
<text x="0" y="234" fill="var(--fig-b)">Verdict: unremarkable. Both values are ordinary and the pairing is common.</text>
<text x="0" y="254" fill="var(--fig-b)">And worse: the account's habitual Kerberos–C700 is rarer than the population predicts,</text>
<text x="0" y="272" fill="var(--fig-b)">so this question fires on every event a consistent account produces.</text>
<text x="0" y="294" fill="var(--fig-a)">Verdict: unprecedented for this account. 74% of labelled events are pairings of this kind.</text>
</g>
</svg>
<figcaption><strong>The signal is real; the scope was wrong.</strong> Two values each
individually ordinary, scarcely ever seen together, is a case no detector scoring fields
independently can express. But asking whether the <em>population</em> has paired them
punishes consistency — an account that has always used one authentication type is, under a
population model, astronomically improbable on every event it produces, which is the thing
this framework elsewhere disavows. Asking the same question of the account's own history
needs no new mathematics: a pairing is a value of a composite field, so Detector I's
estimator scores it unchanged.</figcaption>
</figure>`

// ---------------------------------------------------------------------------
// The combination, and what the measurements established
// ---------------------------------------------------------------------------

const figCombinationDestroys = `<figure class="fig">
<svg viewBox="0 0 740 300" role="img" class="figsvg"
 aria-label="For a labelled event, one detector places it deep in its tail and four place it where any event would sit. Summing all five contributions lets the four uninformative ones supply most of the total, so the combined statistic is unremarkable.">
<g font-size="12" fill="currentColor">

<text x="0" y="14" font-size="11">where each detector puts one labelled event, in its own distribution</text>

<g font-size="11">
<text x="0" y="52">novelty</text><text x="0" y="82">marginal</text><text x="0" y="112">cooccurrence</text>
<text x="0" y="142">timing</text><text x="0" y="172">volume</text>
</g>

<g stroke="currentColor" opacity="0.25">
<line x1="110" y1="34" x2="110" y2="182"/>
<line x1="470" y1="34" x2="470" y2="182"/>
</g>
<g font-size="10" opacity="0.7" text-anchor="middle">
<text x="110" y="196">deep in the tail</text><text x="470" y="196">where anything sits</text>
</g>

<circle cx="112" cy="48" r="6" fill="var(--fig-a)"/>
<text x="126" y="52" font-size="10" fill="var(--fig-a)">0.07th percentile</text>
<circle cx="196" cy="78" r="6" fill="currentColor" opacity="0.5"/>
<text x="210" y="82" font-size="10" opacity="0.75">3.79%</text>
<circle cx="368" cy="108" r="6" fill="currentColor" opacity="0.5"/>
<text x="382" y="112" font-size="10" opacity="0.75">18.4%</text>
<circle cx="448" cy="138" r="6" fill="currentColor" opacity="0.5"/>
<text x="462" y="142" font-size="10" opacity="0.75">27.2%</text>
<circle cx="512" cy="168" r="6" fill="currentColor" opacity="0.5"/>
<text x="526" y="172" font-size="10" opacity="0.75">35.8%</text>

<line x1="0" y1="216" x2="740" y2="216" stroke="currentColor" opacity="0.25"/>

<text x="0" y="238" font-size="11">Fisher adds all five contributions together:</text>
<rect x="300" y="228" width="34"  height="14" fill="var(--fig-a)" opacity="0.85"/>
<rect x="336" y="228" width="96"  height="14" fill="currentColor" opacity="0.3"/>
<rect x="434" y="228" width="88"  height="14" fill="currentColor" opacity="0.3"/>
<rect x="524" y="228" width="82"  height="14" fill="currentColor" opacity="0.3"/>
<rect x="608" y="228" width="76"  height="14" fill="currentColor" opacity="0.3"/>
<text x="300" y="260" font-size="10" fill="var(--fig-a)">the one that knows</text>
<text x="430" y="260" font-size="10" opacity="0.75">the four that do not supply most of the total</text>

<text x="0" y="288" font-size="11"><tspan font-weight="600">At 100 alerts a day, of 262 labelled events:</tspan> novelty alone 21 &#183; most-extreme-of-five 20 &#183; <tspan font-weight="600">the five-detector sum 0</tspan></text>
</g>
</svg>
<figcaption><strong>More evidence is not better when most of it is not evidence.</strong>
Fisher's method asks whether the evidence is jointly unusual, which is the right question
only when every detector is informative. Here one is: it places the median labelled event at
the 0.07th percentile of its own distribution, more extreme than 99.93% of all traffic. The
other four place it where any event would sit. The sum is dominated by the four, so a
composite built from a component that finds 21 labelled events finds none — and no cleverer
combiner recovers it, because the remedy is not a better average but not averaging in
detectors that know nothing.</figcaption>
</figure>`

const figCategoryLift = `<figure class="fig">
<svg viewBox="0 0 720 280" role="img" class="figsvg"
 aria-label="Lift by anomaly category. Novel value 184 times, novel pairing 150 times, off hours 1.6 times, volume burst 1.4 times, population rare 0. The two novelty categories carry nearly all the discriminative power.">
<g font-size="12" fill="currentColor">

<text x="0" y="14" font-size="11">how much more often a category fires on a labelled event than on ordinary traffic</text>

<g font-size="11">
<text x="0" y="52">novel value</text>
<text x="0" y="92">novel pairing</text>
<text x="0" y="132">off hours</text>
<text x="0" y="172">volume burst</text>
<text x="0" y="212">population rare</text>
</g>
<g font-size="9" opacity="0.65">
<text x="0" y="66">per-entity novelty</text>
<text x="0" y="106">per-entity novelty</text>
<text x="0" y="146">per-entity distribution</text>
<text x="0" y="186">per-entity distribution</text>
<text x="0" y="226">population — the control</text>
</g>

<line x1="150" y1="240" x2="700" y2="240" stroke="currentColor" opacity="0.45"/>
<g font-size="9" opacity="0.6" text-anchor="middle">
<text x="150" y="256">1&#215;</text><text x="333" y="256">10&#215;</text><text x="516" y="256">100&#215;</text><text x="699" y="256">1000&#215;</text>
</g>

<rect x="150" y="38" width="415" height="20" rx="4" fill="var(--fig-a)" opacity="0.85"/>
<text x="573" y="53" font-size="11" font-weight="600" fill="var(--fig-a)">184&#215;</text>
<rect x="150" y="78" width="398" height="20" rx="4" fill="var(--fig-a)" opacity="0.85"/>
<text x="556" y="93" font-size="11" font-weight="600" fill="var(--fig-a)">150&#215;</text>
<rect x="150" y="118" width="37" height="20" rx="4" fill="currentColor" opacity="0.35"/>
<text x="195" y="133" font-size="11" opacity="0.8">1.6&#215;</text>
<rect x="150" y="158" width="27" height="20" rx="4" fill="currentColor" opacity="0.35"/>
<text x="185" y="173" font-size="11" opacity="0.8">1.4&#215;</text>
<line x1="150" y1="198" x2="150" y2="218" stroke="currentColor" stroke-width="2" opacity="0.5"/>
<text x="158" y="213" font-size="11" opacity="0.8">0&#215; — fires on no labelled event at all</text>

</g>
</svg>
<figcaption><strong>The split is not per-entity against population. It is novelty against
everything else.</strong> Two categories carry nearly all the discriminative power and both
ask the same question — has this account done this before — of a value and of a combination.
Two more are per-entity and distributional, and run at 1.4× to 1.6×, firing on roughly a
third of ordinary traffic. The control fires on nothing, as it must. That ordering predicts
the detector results exactly: the two novelty detectors find 21 and 23 labelled events, and
timing, volume and the population marginal find none.</figcaption>
</figure>`

const figBaseRate = `<figure class="fig">
<svg viewBox="0 0 720 300" role="img" class="figsvg"
 aria-label="To make half of alerts real at this corpus's base rate of 1.3 in 100000, a detector needs a false-alarm rate near 1e-5. Published detectors operate between 1e-2 and 1e-3, two to three orders of magnitude away. This framework operates at 1.4e-5 at ten alerts a day, below the rate the arithmetic requires, and at 1.6e-3 at a thousand alerts a day, inside the published band.">
<g font-size="12" fill="currentColor">

<text x="0" y="14" font-size="11">false-alarm rate needed for a given share of alerts to be real, at this corpus's base rate</text>

<line x1="70" y1="240" x2="690" y2="240" stroke="currentColor" opacity="0.45"/>
<line x1="70" y1="40" x2="70" y2="240" stroke="currentColor" opacity="0.45"/>

<g font-size="9" opacity="0.65" text-anchor="end">
<text x="64" y="64">10&#8315;&#178;</text>
<text x="64" y="112">10&#8315;&#179;</text>
<text x="64" y="160">10&#8315;&#8308;</text>
<text x="64" y="208">10&#8315;&#8309;</text>
</g>
<text x="20" y="150" font-size="10" opacity="0.7" transform="rotate(-90 20 150)" text-anchor="middle">false-alarm rate</text>

<rect x="70" y="56" width="620" height="56" fill="var(--fig-b)" opacity="0.16"/>
<text x="80" y="80" font-size="11" fill="var(--fig-b)">where published detectors actually operate</text>
<text x="80" y="98" font-size="10" fill="var(--fig-b)">at 10&#8315;&#179; this corpus yields ~6,000 false alerts a day against 78 real ones</text>

<polyline points="140,232 260,208 380,190 500,166 620,140" fill="none" stroke="var(--fig-a)" stroke-width="2"/>
<g fill="var(--fig-a)">
<circle cx="140" cy="232" r="4.5"/><circle cx="260" cy="208" r="4.5"/><circle cx="380" cy="190" r="4.5"/>
<circle cx="500" cy="166" r="4.5"/><circle cx="620" cy="140" r="4.5"/>
</g>
<g font-size="9" opacity="0.75" text-anchor="middle">
<text x="140" y="258">90%</text><text x="260" y="258">50%</text><text x="380" y="258">25%</text><text x="500" y="258">10%</text><text x="620" y="258">5%</text>
</g>
<text x="380" y="278" font-size="10" opacity="0.7" text-anchor="middle">share of alerts that are real</text>

<line x1="260" y1="112" x2="260" y2="208" stroke="var(--fig-a)" stroke-width="1.5" stroke-dasharray="4 3"/>
<text x="272" y="130" font-size="11" fill="var(--fig-a)">half the queue real needs 1.3&#215;10&#8315;&#8309;</text>
<text x="272" y="148" font-size="10" fill="var(--fig-a)">— about two orders of magnitude below the shaded band</text>

<rect x="70" y="102" width="620" height="99" fill="currentColor" opacity="0.07"/>
<line x1="70" y1="201" x2="690" y2="201" stroke="currentColor" stroke-width="2"/>
<text x="686" y="196" font-size="10.5" text-anchor="end">this framework at 10 alerts/day: &#945; = 1.4&#215;10&#8315;&#8309;</text>
<line x1="70" y1="102" x2="690" y2="102" stroke="currentColor" stroke-width="1.5" stroke-dasharray="5 4"/>
<text x="686" y="118" font-size="10.5" text-anchor="end">and at 1000 alerts/day: &#945; = 1.6&#215;10&#8315;&#179;</text>
<text x="76" y="216" font-size="9.5" opacity="0.75">the band its budgets span</text>

</g>
</svg>
<figcaption><strong>The base rate, not the model, sets the ceiling.</strong> Over the scored
window this corpus carries 549 labelled events among 42,218,530 — one in 76,901. Inverting
the arithmetic for a desired precision gives the false-alarm rate a detector would have to
achieve, and it lands two to three orders of magnitude below where published detectors
operate. Two consequences follow: "everything suspicious and nothing else" is not
attainable here by any known method, and even a perfect detector still yields 87 alerts a
day, because 78 genuinely suspicious events happen daily. What an operator can choose is the
error rate; the volume follows from it.
<br><br>
The two horizontal rules mark where this framework actually operates, measured on the
<code>r11</code> subset, and the band between them is what its alert budget spans. At 10 alerts a day
its false-alarm rate is &#945;&nbsp;=&nbsp;1.4&#215;10&#8315;&#8309; &#8212; the order the
arithmetic asks for, and two orders of magnitude below where published detectors sit. At
1000 a day it is 1.6&#215;10&#8315;&#179;, inside the shaded band.
<br><br>
<em>The rules are deliberately not points on the curve.</em> The curve inverts the identity
for a detector that misses nothing; this one misses most, with recall of 2.0% at the tighter
operating point, and its queue is 15.7% real rather than the large majority that perfect
recall at that rate would give. The reading is therefore the opposite of the usual one:
suppressing false alarms to the rate the base rate demands is not this framework's binding
constraint, because it already achieves it. Recall is.</figcaption>
</figure>`

const figAbstention = `<figure class="fig">
<svg viewBox="0 0 700 200" role="img" class="figsvg"
 aria-label="A verdict has four states. Only the evaluated state carries a p-value; the three abstentions carry none and reduce the degrees of freedom of the combination instead.">
<g font-size="12" fill="currentColor">
<rect x="0" y="40" width="150" height="56" rx="4" fill="var(--fig-a)" opacity="0.16" stroke="var(--fig-a)" stroke-width="1.5"/>
<text x="75" y="64" font-size="12" text-anchor="middle" font-weight="600" fill="var(--fig-a)">evaluated</text>
<text x="75" y="82" font-size="10" text-anchor="middle" fill="var(--fig-a)">carries a p-value</text>

<g fill="none" stroke="currentColor" stroke-width="1.5" opacity="0.6">
<rect x="220" y="8" width="200" height="40" rx="4"/>
<rect x="220" y="60" width="200" height="40" rx="4"/>
<rect x="220" y="112" width="200" height="40" rx="4"/>
</g>
<g font-size="11" opacity="0.85">
<text x="234" y="26">abstained: structural</text>
<text x="234" y="42" font-size="9" opacity="0.8">the source never sends this field</text>
<text x="234" y="78">abstained: unexpected</text>
<text x="234" y="94" font-size="9" opacity="0.8">usually sent, absent here</text>
<text x="234" y="130">abstained: unusable</text>
<text x="234" y="146" font-size="9" opacity="0.8">present, but nothing can be concluded</text>
</g>

<line x1="420" y1="28" x2="500" y2="70" stroke="currentColor" stroke-width="1.5" opacity="0.6" marker-end="url(#fig-arrow)"/>
<line x1="420" y1="80" x2="500" y2="80" stroke="currentColor" stroke-width="1.5" opacity="0.6" marker-end="url(#fig-arrow)"/>
<line x1="420" y1="132" x2="500" y2="90" stroke="currentColor" stroke-width="1.5" opacity="0.6" marker-end="url(#fig-arrow)"/>

<text x="512" y="76" font-size="11">no p-value exists</text>
<text x="512" y="94" font-size="11">to be combined</text>

<text x="0" y="132" font-size="11" width="150">A &#8220;0.5 because</text>
<text x="0" y="150" font-size="11">we do not know&#8221;</text>
<text x="0" y="168" font-size="11">is unrepresentable:</text>
<text x="0" y="186" font-size="11">it asserts normality.</text>
</g>
</svg>
<figcaption><strong>Declining is not the same as reporting nothing unusual.</strong> A verdict
has four states and only one of them carries a p-value, so a detector with no basis for an
opinion cannot express a confident middle value — the field is unreachable. An abstention
reduces the degrees of freedom of the combination rather than contributing to it. This is
what lets the system degrade honestly instead of inventing findings when a source changes
shape.</figcaption>
</figure>`
