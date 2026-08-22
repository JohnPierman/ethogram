# Changelog

All notable changes to this project are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Equation numbers `(1)`–`(20)`, requirements `R1`–`R6`, and evaluation hypotheses
`E1`–`E9` refer to the whitepaper that specifies this implementation.

## [Unreleased]

### Added

- **`statistics.SaturatingWeight` names the trap that produced three separate defects (#37).**
  Every per-entity accumulator here is discounted by 2^(-elapsed/halfLife), so a discounted count
  does not grow without bound -- it saturates at 1/(1-delta). An arm requiring a minimum
  discounted weight is therefore imposing a condition that, past a certain sparsity, is
  **unsatisfiable however long the entity is observed**: the arm does not warm up slowly, it never
  warms up. `MinimumWeightReachable` and `MaximumGapForWeight` make the claim checkable, and the
  three arms now state their own coverage through them
- **The timing arm's real coverage is stated: about 12.4 hours between events (#37).** Its weight
  is discounted per event and saturates at 10.6 for a once-daily account against a minimum of 20,
  so the standardised statistic is unavailable to any account sparser than that **permanently**.
  That reframes #37: the issue attributes the arm's silence on regular accounts to a zero spread,
  and the weight gate reaches a great deal further. Measured: a once-daily account abstains for
  want of history after sixty days, and after six hundred
- **The two timing abstention causes are now distinguishable**, which was #37's first
  requirement. They point opposite ways -- too little history is a warm-up that passes, no spread
  is a property of the account that never will -- and a single `abstained_unusable` total could
  not tell them apart. `timing.AbstentionCause` carries which, the verdict's reason names it, and
  a run records `abstain_causes` beside `status_counts` so the split is a number rather than a
  guess

### Fixed

- **The Postgres volume store no longer drops the dispersion state (#33).** `WindowExpected`,
  `DispersionWindows` and `DispersionSum` had no columns, so `SaveState` never wrote them and
  `FindByEntity` never read them: a Postgres-backed run lost the measured width of every entity's
  null across a restart and silently resumed against equation (11) un-widened. Since the
  dispersion gate now decides whether the arm abstains, that loss changed a verdict's *kind* and
  not only its number. Three columns, three `ADD COLUMN IF NOT EXISTS` migrations defaulting to
  zero -- which is what a fresh state carries, so an existing row migrates to "not yet measured"
  rather than to a fabricated value

### Added

- **A schema-drift guard that needs no database.** The equivalence test that would have caught
  #33 sits behind the `integration` tag and so never runs in CI, and it could not have caught it
  anyway: it compared a hand-written list of fields naming only the ones already persisted. The
  new guard reflects over each persisted state struct and requires every field's column to appear
  in the DDL, the SELECT **and** the INSERT separately
- **The equivalence test now compares whole structs and exercises the dispersion fold.** Field
  lists are how #33 survived; `*mVol != *pVol` covers any field added later, and the fold sets a
  non-zero window expectation so the accumulators are not two zeros being compared
- **`TestPersistedArmsAreTheRecordedSet` records that four of the seven arms have no Postgres
  store at all** -- `noveltyrate`, `drift` and `marginal` have no table, no accessor, nothing.
  That costs no recorded measurement, since every run in `results/` uses the memory stores, but
  it was previously discoverable only by grepping for a table that is not there

### Added

- **`objective.CostRatio`, `Threshold` and `CostCurve` close the operating-point question
  (#14).** The operating point is now derived from a stated cost ratio and recorded, rather than
  reproducible only by whoever repeats the derivation. `CostRatio` refuses a non-positive or
  non-finite cost naming which side was wrong; `PosteriorThreshold` is the decision-theoretic
  content, `c_review/(c_review + c_miss)`; `Threshold` carries it onto the per-event scale through
  `alpha = p(1-tau)/(tau(1-p))`; and `CostCurve` gives the Drummond-Holte view across the range of
  ratios rather than at one guessed point. `AccuracyEquivalent` names the one-to-one ratio that
  maximising accuracy silently assumes
- **The published base-rate table is reproduced from the cost model, as a test.** All five rows
  -- 90/50/25/10/5% precision, their demanded alpha and their per-day counts -- agree with the
  table computed from the corpus's own base rate, at the two significant figures the table is
  printed to. Two derivations of one table now agree in CI rather than by inspection
- **A clamped alpha is reported rather than applied silently.** At an extreme ratio the arithmetic
  asks for alpha above 1, which means the demanded precision is unreachable at this base rate even
  by alerting on everything; `OperatingPoint.AlphaClamped` says so
- **`cmd/analyse` records the operating point and the cost curve in its parameters block**, with
  the identity beside them so an analyst can redo the arithmetic by hand (R5)
- **The baseline sidecar declares and pins its dependencies (#36).**
  `sidecar/requirements.txt` pins `numpy`, `pandas`, `scikit-learn` and `rrcf` to the versions
  the committed `results/baselines-*.json` files record in their own `versions` block, so a
  re-run reproduces those numbers rather than re-resolving to whatever is current. `setuptools<81`
  is pinned with the reason stated: `rrcf` imports `pkg_resources` at module load, setuptools
  removed it in 81.0, and Python 3.14 venvs no longer seed setuptools at all, so a fresh
  environment cannot import the sidecar at all and `pip install setuptools` resolves 84.x and
  still fails. `rrcf` 0.4.4 is its last release and carries no fix
- **CI runs the sidecar end to end.** `sidecar/test_baselines.py` existed and nothing ran it,
  which is why an environment that could not even import the sidecar went undetected. It needs
  no pytest and no corpus -- it builds its own synthetic feature table -- so the new job is an
  install and one command
- **DATA.md states how to reproduce the baselines**, alongside the corpus derivation it already
  covered, including the supported Python range and why `rrcf` is excluded from a default run

### Changed

- **The oracle-weights row is removed from section 2.4 (#34).** No recorded run produced its four
  numbers and they were computed under the timing statistic section 3.3 replaces, so they were
  stale as well as unrecorded. Deleting rather than re-running is the cheaper repair because the
  row is no longer load-bearing: it existed to bound what a fitted allocation rule could reach,
  and section 2.3 now bounds the same thing with a proof that needs no oracle -- the objective is
  linear in the allocation, so its optimum is a vertex whatever the weights are fitted on

### Changed

- **The dilution result is replicated on the real campaign, where it is much larger (#39).**
  `lanl-r11-b1000-drift-d7-14-006`: adding the uninformative seventh arm takes the corrected
  minimum from 162 detections to 59 and the composite from 113 to 60, against 234 to 134 and 227
  to 171 on the planted corpus. Every single arm's count is unchanged on both. The loss is larger
  where the signal is sparsest, which is the direction the mechanism predicts and the opposite of
  what a reader would guess from the planted corpus alone
- **Section 3.3 no longer carries the `timing` repair at length.** Its one non-obvious fact --
  that raising the grid would not have lifted the floor, because only 6 of 613 labelled events sat
  on it and the rest were held up by the statistic -- is folded into the sentence that names the
  ceiling, and the appendix that restated the rest is gone. The paper is back to 17 pages

- **The sequential-change arm is now scored on the corpus, and it does not work there
  (#39, A1).** `lanl-inj-b1000-drift-d7-14-006`: 0 of 288 planted low-and-slow events, one
  detection in 4.49 million, and an inverted response on the column it was built for -- median
  p 0.77 against 0.62 on the real campaign. The game value of section 2.3 therefore stays at
  zero, the guarantee stays at 0.421 and the arm takes weight zero in the optimum. Three causes,
  and only the first is the statistic's: the planted mechanism is three seventeen-minute bursts
  rather than a sustained elevation, so a daily-period cumulative sum is the wrong instrument and
  an hourly-window test is the right one; the plant is shorter than the arm's eight-period
  warm-up; and the planted events raise the entity's own baseline rate, which raises the reference
  value and floors the sum. Protecting the null over S from the change left the baseline exposed.
  **The fix for that column is repairing `volume`'s tail, not adding a sequential statistic**
- **The dilution the combination rules suffer from is now tested rather than inferred.** That
  seventh arm is close to a controlled injection of noise -- one labelled event in 4.49 million,
  abstaining on 66% of them -- and adding it takes the composite from 227 detections to 171 and
  the corrected minimum from 234 to 134 at 1000 alerts a day, while leaving every single arm's
  row identical. Adding a test that knows nothing costs a combination a fifth to a half of what
  it had

- **The paper is rewritten as a finished paper rather than a working notebook (#39, A7).** It was
  21 pages against a 20-page ceiling and so failing its own gate; it is now 19, at 8,841 words
  against 11,856, while gaining a whole results section. The cuts are verbosity, not evidence:
  every measurement, interval and caveat is retained, and several passages that narrated a table
  in prose became the table. The six planted mechanisms, the six detector nulls, the four
  combination rules and six of the threats to validity are now tables where they were paragraphs;
  section 5.3's three per-budget tables became one at the budget where the methods separate, with
  the budget dependence stated in a sentence, because a reader given three tables of the same
  shape has lost the sentence saying which budget each was
- **Section 5.8 is new: allocation read as a two-person zero-sum game.** It states why section
  5.5's corner result is forced rather than measured, reports that the portfolio's game value is
  zero and why that is a coverage defect, gives the competitive-ratio equilibrium and its price,
  distinguishes randomising from dividing and measures both, and locates every positive shadow
  price in the two mechanisms the paper's headline does not compare arms on
- **Section 6.3 now separates what is repaired from what is missing**, carrying the replaced
  timing statistic and the new drift statistic that the threats section previously had to both
  raise and answer
- **Four references were added and are cited**: Page for the cumulative sum, von Neumann and
  Dantzig for the equilibrium and its reduction to a linear programme, and Auer for the
  adversarial bandit bound section 6.3 uses to close the online-learning direction

### Added

- **`domain/drift`: a sequential change statistic for the mechanism the volume predictive
  cannot reach (#39, A1).** Equation (11)'s null is structurally over-dispersed, which is
  correct for asking whether *this period* is surprising and is exactly why a modest shift
  sustained over many periods sits inside it in every period. Page's one-sided cumulative sum
  accumulates the excess instead, so the evidence grows linearly in the number of periods while
  the spread of its null grows as the square root. The p-value is the upper tail of the sum
  standardised against the entity's own realised sums, which is the construction section 6.2
  adopts for timing and for the same reason: it removes a scale that is not comparable across
  entities and it has no floor
- **Measured on synthetic streams of known construction: the cumulative sum separates a
  sustained +30% shift from matched stationary variation by 57x, and equation (11)'s predictive
  by 1.2x.** Both statistics are fitted on the same forty stationary periods and then score
  forty more, so the comparison isolates what each accumulates rather than how each is fitted.
  This reproduces, on a stream whose construction is known, the inverted response the paper
  records on the planted corpus -- median p 0.72 on low-and-slow against 0.29 elsewhere
- **`domain/routing`: per-entity detector assignment (#39, A5).** Expected detection is linear
  in a global allocation, so the best global allocation is a single detector; routing is not a
  global allocation and is the only construction here able to beat the best single arm at equal
  cost, because it exploits arms reaching different events instead of averaging over them. It
  routes on frozen per-entity state only, its preference order is stated rather than fitted, and
  it abstains under R3 where no arm's null is well specified
- **The headroom for per-entity routing is now bounded from the recorded matrix: 460 to 535
  labelled events against the best single arm's 384.** The floor routes each mechanism to the
  arm that reaches it most, which any per-entity policy matches by construction; the ceiling is
  the union of every arm at full depth, since no routing reaches an event no arm reaches. Both
  are oracles and neither charges the alert cost of attaining it

### Fixed

- **`domain/drift` names the bound its discount must satisfy, rather than abstaining silently
  below it.** Discounted weight saturates at 1/(1-delta) however long an entity is observed, so
  a half-life short enough to put that ceiling below the minimum weight disables the arm for
  every entity for all time. At daily periods the framework's seven-day half-life clears it,
  giving a saturating weight of 10.6 against a minimum of 8 -- but not by much, and a run that
  shortened the half-life below about five days would record only a column of abstentions.
  `ReachesMinWeight` is what a caller consults instead of discovering it in a result file

- **The allocation of a fixed budget across detectors is now solved as a two-person zero-sum
  game (#39), by `domain/robust` and `cmd/robust`.** Section 5.5 established that the optimum
  is the corner; this states why that result is forced rather than measured. Expected detection
  under any known attack mix is linear in the allocation weights, so its maximum over the
  simplex is at a vertex and no prior-weighted mixture -- including one fitted to published
  industry base rates -- can beat the best single arm. A property test checks it directly over
  the vertices and a spread of interior mixtures
- **The portfolio's game value is zero, and the analysis reports that rather than an
  allocation.** Every arm scores 0 on planted low-and-slow, so the saddle point is (any arm,
  low-and-slow) and no reweighting of rows changes a column of zeros. `Matrix.Unreachable`
  names such mechanisms, because a value of zero grades the coverage and not the allocation,
  and reading it as the latter is the error the report is shaped to prevent
- **A well-posed robust objective, since maximin is not one here.** Normalising each mechanism
  by the best rate any single arm reaches against it and maximising the worst-case retained
  fraction gives an interior equalising optimum: 29.6% of the achievable on every mechanism at
  once, against 0% for any single arm and for every combination rule the paper tests. The price
  is reported beside it -- under a stated prior the robust mixture gives up 59% of expected
  detection -- because neither number alone is a decision
- **Randomising over detectors is now distinguished from dividing the budget between them, and
  measured.** Sections 5.4 and 5.5 test unions and splits, which give every arm a fraction of
  its depth; a mixed strategy runs one arm at full depth chosen by lottery, and is a different
  object. It is exactly evaluable from the per-arm detections already recorded, because each arm
  at full budget is the recorded configuration. At 1000 alerts a day an even randomisation over
  the two best arms finds 344 against the best combination rule's 234 at the same alert spend,
  and still loses to the best single arm's 384 -- which is what linearity requires
- **Per-mechanism attacker cost as a stated parameter.** The value-zero equilibrium assumes an
  uncovered mechanism is free to mount, and low-and-slow is slow by construction. Charging it
  twelve times the cost of credential spray removes it from the adversary's reply and raises the
  guarantee from 0.019 to 0.043. The cost and the exchange rate are stated and never fitted: a
  cost fitted to the labels the allocation is scored on would make the equilibrium a restatement
  of the corpus
- **`cmd/methodtable -matrix` emits the per-mechanism table as data.** The robust analysis reads
  exactly the rectangle the Markdown table renders, so the table and the analysis of it cannot
  disagree about a count. `make matrix` and `make robust` reproduce both, and neither needs the
  corpus: allocation is about spending a budget across detectors whose performance is already
  measured

- **Section 1.1's figure is now one line per detector across the whole budget range (#29),
  generated by `cmd/budgetcurve` from recorded runs rather than typed.** It replaces a
  hand-drawn figure that carried the base-rate identity, a band for where published detectors
  operate, and two rules for this framework's own operating points -- from which a reader could
  see the arithmetic but not how the framework compares to a named method at a budget they care
  about
- **The figure plots the share of the queue that is real against the alert budget, and no longer
  against the false-alarm rate.** The first version of it used both quantities as axes, and on
  this corpus they are not independent: every method given a budget of *b* emits the same *b* x
  7 alerts, so alpha is that queue over the background population and was a restatement of the
  budget. Six lines therefore collapsed onto eight horizontal bands, the one quantity that
  varies was squeezed onto a single axis with half its points in a break column at zero, the
  lines doubled back across each other, and the six end-of-line labels -- placed where the lines
  converge, the two arms of this framework ending 0.03 decades apart -- overprinted into one
  illegible smear. The budget takes the horizontal axis, where it is the quantity a SOC actually
  sets and the one every comparison in the paper is matched on; alpha is read off an exact
  second scale above the plot; precision has the vertical axis to itself and separates the drawn
  methods over four decades. Identity moved to a legend, which cannot collide, and which carries
  the reach each method achieved
- **Colour in that figure is the scope of the null, not the authorship of the method.** The blue
  family judges an event against the account that produced it and the orange family against the
  pooled population, so the figure shows section 1.2's distinction before it shows which line is
  ours; `entity_ewma` is a comparator drawn in the blue family because it keeps a per-entity
  mean. The classification is written to `parameters.scope_by_series` in the curve file, so it
  is checkable rather than taken on trust. A method that found nothing at a budget is drawn on a
  banded row below an axis break, since a precision of exactly zero has no place on a log scale
  and an omitted point reads as unmeasured
- **A headline table under that figure, `cmd/thesis/figures/budget-table.html`, generated by the
  same command from the same series.** The figure answers how the comparison moves with the
  budget and is a poor answer to what each method actually did: reading a count off a log axis
  is an estimate, and four of the eight measured comparators are not drawn at all. The table
  reports found, precision and recall for every method that was run, at 100 and at 1,000 alerts
  per analyst-day. Two budgets rather than one because a single operating point makes the
  comparison look like a property of the methods when most of it is a property of what was
  affordable -- seven of the eight comparators reach nothing at 100 a day and six of the eight
  still reach nothing at 1,000, while this framework's novelty arm goes from 11% of the campaign
  to 37%. Both artefacts read the same series, so a cell cannot contradict a point on the curve
- **`cmd/budgetcurve -curve`, and `make figure-budget-curve-redraw`,** redraw the figure and the
  table from the committed curve file without re-reading the replay. The measurement and the
  drawing have very different lifetimes: the replay is 46 MB and is not in the repository, while
  the drawing is revised whenever the figure turns out to be hard to read. Without this the only
  way to move a line is to edit the committed SVG by hand, which is how a figure stops matching
  the numbers it claims to plot. A test asserts the two paths emit byte-identical output from the
  same measurement, so it is not a shortcut with a caveat
- **The README carries the head-to-head against the eight published baselines,** which it
  previously compared only this framework's own arms against, and a usage section: the smallest
  runnable score-then-observe loop with its real output, what must be declared and what is
  inferred, and why an abstention is not a neutral score
- `cmd/budgetcurve` **refuses to draw two detectors that did not read the same events.** Shared
  axes assert comparability, and section 1.1 is the section insisting every comparison be made
  at a matched number of alerts per analyst-day, so a mismatch in population or in ground truth
  is an error rather than a caveat. On the committed inputs the populations match exactly:
  4,190,603 events and 549 labelled events on both sides, a relative difference of 0.0000%
- `results/baselines-r11-d7-14-full.json`, the eight reference implementations re-run at full
  density on precisely the events the framework scored -- `sample_rate 1`,
  `entity_history_intact: true`
- `results/baselines-r11-d7-14.json` is retained even though the run above supersedes it,
  because it is the measurement that showed why the re-run was needed: at a uniform 1-in-4
  EVENT sample, `entity_ewma` -- the only per-entity baseline, and so the closest comparator to
  this framework -- had every entity's history decimated and detected nothing below a budget of
  5,000. At full density it detects at 50 and at 100. Plotting the handicapped number would
  have flattered this framework by choice of comparator
- `results/budget-curve-r11-d7-14.json` carries every plotted point with its provenance, and
  all eight measured comparators rather than only the four drawn, so a different four can be
  redrawn without re-running anything
- `make figure-budget-curve`. The framework run it reads is deliberately not in `results/`: a
  budget of 10,000 needs `-topk 10000`, and such a result file is about 46 MB -- an intermediate
  on the scale of the alert ledger, which this repository already keeps out of `results/` for
  the same reason

- **`timing` scores by a per-entity standardised statistic, and `cmd/replay
  -timing-standardise` selects it (#26).** The level-set mass of equation (9) is read from a
  512-point grid and reports nothing below one half-cell, 9.77e-04, which sat AT the arm's own
  realised alert cut at 10 and 100 alerts a day: the detector could not alert there whatever it
  observed, so its zeros were a property of the statistic rather than a measurement. The
  reported quantity is now the event's ln U standardised by the mean and spread of the ln U
  this entity's own events have received, which has no floor
- `timing.State.LogUSum` and `LogUSumSq`, discounted under the same rule as the moments, carry
  that null in two numbers so the state stays fixed size (§13.3). #26 proposed reusing
  `allocation.Tail`, which is a frozen PER-DETECTOR null fitted from a retained sample; a
  per-entity form of it would have to retain each entity's own density history and the state
  would grow without bound
- `MinStandardiseWeight` gates the statistic on the entity's discounted observation weight, and
  the detector abstains below it rather than falling back to the mass, which would put two
  different nulls in one ranked queue

- **`cmd/replay -volume-min-periods`, and the `volume_gate_probe` diagnostic that chose its
  value.** #25 asks for a threshold picked from a measurement rather than from taste, and the
  four candidates it nominates would have cost a two-hour replay each. The probe answers all
  of them from one ungated pass: it retains the smallest `-topk` p-values per (day, completed
  period) cell, so a candidate's realised cut is exact rather than binned. The budget is per
  DAY, which the first version of this got wrong -- a single cut pooled across the window
  credits a labelled event at 1.96e-07 with clearing a cut the loosest day set, when on its
  own day the cut was 1e-12 and it was nowhere near. Every quantity is therefore accumulated
  per day, and at a threshold of zero the block's `labelled_clearing_cut` must equal the
  volume arm's own `true_positives`: the same selection computed a second way, so a
  disagreement indicts the instrument rather than the arm
- **`cmd/volumegate`** writes the decision as a result file: the threshold adopted, the rule
  that was applied, and every rejected candidate with the measurement that rejected it. It
  refuses a parent run that armed the gate, because a gated event carries no p-value and such
  a run cannot decide a threshold below its own. `-max-abstain-share` makes the "small share"
  half of #25's rule an explicit recorded number rather than a judgement in prose
- `volume.State.CompletedPeriods`, the undiscounted count of periods folded into the
  posterior, with `completed_periods` reported on every volume verdict. `Rate.B` is the same
  count under the per-period discount and settles near 10.6 at T1/2 = 7 days, so it cannot
  express how many periods an established entity has and cannot gate on it

- the **abstract and the contributions carry the allocation result**, which they did not. A
  paper whose sharpest negative finding appears only in section 5.5 is a paper whose abstract
  is out of date with it: the allocation bound is now one of four negative results stated up
  front, and the statistic that decides whether dividing a budget pays -- the overlap between
  two detectors' detections, 74.6% between the two strongest and 0% where a split does win --
  is stated as a contribution rather than left as a remark in the results
- **the allocation result is now a recorded run.** `lanl-r11-b1000-weighted-d7-14-003` and
  `lanl-inj-b1000-weighted-d7-14-003` carry the weighted arm and the oracle split search, so
  paper section 5.5 cites result files rather than a ledger. The Go implementation reproduces
  the offline screen the finding was developed against to four decimal places on every fitted
  weight -- 0.3760, 0.4135, 0.4908 on the real campaign -- and to the detection on every cell
- the per-detector arms are **unchanged** across the two runs that added the arm: `novelty`
  11/60/201 on the real campaign, `noveltyrate` 384 at 1000/day on the planted corpus, every
  other arm identical. `-weighted` mirrors the burn-in ranking and observes the scoring window;
  a moved per-arm count would have meant it was perturbing what it measures

### Fixed

- section 5.5 cited `lanl-inj-b1000-weighted-d7-14-004` where the recorded run is `-005`. The two
  ids shared a line and only the first was bumped when the run ids moved; caught by checking
  every run-id the paper cites against the run ids the result files actually record, which is
  now the check worth keeping
- section 1.1 lost the full corpus's base rate of one in 76,901 when the hand-drawn figure that
  carried it was replaced. The figure's own base rate of one in 7,633 is stated as a multiple of
  it, so the multiple had nothing to refer to

- **`timing` can now express a departure it has detected (#26).** Detections at 10, 100 and
  1000 alerts a day go from 0, 0 and 6 to **1, 2 and 7** on the real campaign and from 0, 1 and
  12 to **2, 9 and 21** on the planted corpus. The response to the mechanism the arm was built
  for is not traded for that: the median p-value on planted off-hours attacks falls from
  3.20e-02 to 8.23e-03, widening the separation from the nearest other planted mechanism from
  5.7x to 18.6x. Every other arm is unchanged on both corpora -- `novelty` 11/60/201,
  `noveltyrate` 384 at 1000/day, `marginal` 0/76/120
- **the in-memory timing store dropped any field added to `timing.State`.** It rebuilt the
  struct field by field, so the two new accumulators never came back out of it, the variance
  read as zero and the detector abstained on ALL 4,190,603 events -- a defect that presented as
  a result. It now copies the struct and deep-copies only the moments, and
  `infrastructure/state/memory` has the round-trip tests it previously had none of, written
  with reflection so they fail when a field is ADDED and not copied
- `timing.State`'s two accumulators are persisted by the postgres store, which would otherwise
  reopen the abstention for every entity on each process restart

- **`volume` abstains where an entity has no completed period (#25, R3).** With none, the
  Gamma posterior of equation (10) is the prior, and the detector reported that absence of any
  basis as P = 1, which is an opinion. It now returns `abstained_unusable` with a reason, on
  2,362 of 4,190,603 events on `r11` and 4,399 of 4,494,396 on the planted corpus, against
  zero before. The observation is returned on the abstaining path as well as the scoring one:
  an entity below the gate must keep accruing state, or the abstention is permanent rather
  than provisional and the arm goes silent instead of becoming correct
- **#25's account of the defect does not survive the measurement, and sections 5.1 and 6.2 now
  state what does.** The issue attributes 13,618 sub-1e-12 events to scoring an entity's first
  period against a posterior fitted on no completed periods. A first period scores P = 1
  exactly, so the abstention removes NONE of that pile; at five completed periods, costing
  4.9% of events abstained and 132 labelled events withheld, only 10.6% of it goes. The pile
  belongs to entities with established history, and the cause is equation (11)'s predictive
  being too narrow to tolerate their habitual day-to-day variation. No candidate threshold
  moves the realised cut off the 1e-12 floor on either corpus, so one was adopted on the R3
  requirement alone and the rule's own recommendation is recorded as none
- Section 5.1 quoted the planted corpus's 13,618 sub-1e-12 events and its 4.5e-06 calibrated
  expectation in a passage that states it is reporting `r11`, where the figures are 27,464 and
  4.2e-06
- `completed_periods` is persisted by the postgres volume store, which would otherwise reopen
  the gate for every entity on each process restart

- **the volume detector never abstains, and the paper now says so.** R3 requires a detector
  with no basis for an opinion to say so; this one scores an entity's first period against a
  rate posterior fitted on no completed periods, because an unseen entity is given a
  zero-valued state and the tail is computed from the prior. It abstained on none of 4,494,396
  scored events, against 8,705 for `noveltyrate` and 3,741,825 for `novelty`
- the consequence is that **its alert queue is entirely background at every budget**: 13,618
  events fall below 1e-12 where a calibrated null predicts 4.5e-06, no labelled event goes
  below 1.96e-07, and the realised cut is 1.12e-12 at 10, 100 and 1000 alerts a day alike. Same
  misspecification already recorded for the population co-occurrence null at 18.4%; at 0.303%
  it was small enough to escape notice and large enough to consume twice a 1000/day budget
- volume's response is **inverted** on the mechanism it was built for -- median p 0.72 on
  planted low-and-slow against 0.29 on the other mechanisms -- a second defect the first masks
- section 5.1's enrichment result no longer carries the whole explanation for two arms' zeros.
  Neither the volume nor the timing detector is evidence about per-entity conditioning in
  either direction, and section 5.3's zeros for them read as unmeasured rather than measured
- **the paper attributed the timing and volume detectors' null results entirely to their
  properties not being enriched, and that is only half of it.** The timing detector's tail mass
  is read from a 512-point grid lookup and floored at one half-cell, 1/(2 x 512) = 9.77e-04,
  while the realised alert cut is 3.98e-03 at 1000 alerts a day and 1.00e-03 at 100 and at 10.
  Its most extreme possible answer is a factor of four inside the widest cut and at or above
  the other two, so at the tighter budgets it cannot alert whatever it observes. Section 6.2
  now reports it
- the detector is meanwhile **responding** to the mechanism it was built for -- median p-value
  3.20e-02 on planted off-hours attacks against 0.59 to 0.83 on every other mechanism, more
  than an order of magnitude of separation from its own baseline -- so this is a dynamic-range
  ceiling and not a failure to discriminate. Raising the grid alone would not lift it: 1 of 64
  planted off-hours events and 5 of 549 real labelled events sit at the floor, and the rest are
  held up by a tail mass over density levels saturating. The statistic is the thing to change
- **the structural census in section 5.1 could be read as a detection table.** Its rows count
  labelled events that exhibit a property -- 181 events outside their entity's hours, 33% of
  the campaign -- and it sits three pages from tables whose columns are detections, where the
  timing detector reaches 2 of 64. The header and a lead sentence now say that no detector is
  involved in it
- a variable in `cmd/inject` shadowed the predeclared identifier `real`, which `golangci-lint`
  refuses and which the local linter would have caught before the push
- `weighted_arm.optimal_split.best_split` named an arm called nothing at depth zero where the
  optimum was the whole budget to one arm. The absence of a second arm is the finding, so it is
  rendered as an absence. The two `-003` runs predate the fix and carry the empty key; no
  published figure reads that field, and re-running two hours to change a map key that no
  number depends on is not a trade worth making
- **`make corpus`, `make corpus-check`, and one target per recorded run.** Every result file
  cites its inputs by SHA-256, which proves which file was read and says nothing about how to
  make it. For one release the derivations lived only in a shell history: two `cmd/subset`
  invocations whose parameters survived solely inside the manifests of files that are not in
  the repository, and a combined label file no target, command or document knew how to build.
  Reproducing a result on a second machine meant reading bytes off the first one. Two files in
  -- `auth.txt.gz` and `redteam.txt.gz` -- and everything else is derived and verified
- **`cmd/inject -combined-labels`** writes the real and planted labels as one file, ordered by
  timestamp, which is what a replay over the injected corpus needs since it takes one
  `-redteam` argument. It was previously produced by hand. A test pins that the tool rebuilds
  the shipped file row for row, and skips where the corpus is absent
- **`cmd/corpuscheck`** verifies each derived input against the digests the recorded runs cite
  and fails closed, with a message naming the fix. The cheapest place to learn that a corpus is
  wrong is before a two-hour replay rather than from its numbers afterwards
- `config/corpus-digests.txt` allows one file two digests, on one line, with the reason on that
  line: the combined labels hold the same 1,605 rows in the same order under two gzip
  encodings, and nothing downstream can observe the difference -- the label loader builds sets
  and a count, so line order and compression level do not reach a score. Where a mismatch
  *would* change a result, the auth corpora whose contents are scored, exactly one digest is
  accepted
- `DATA.md` documents the derivation chain, why the injected corpus must not be regenerated
  during a reproduction, and which fields of the taxonomy to diff if it is
- **the oracle bound that makes the allocation result conclusive.** The weighted arm losing
  does not by itself distinguish a bad estimator from a wrong construction, so the replay also
  searches two-arm budget splits exhaustively and picks the best one WITH the evaluation labels
  in hand. On the real campaign the optimum is the corner -- the whole budget to the best
  single arm, at both budgets -- and diverting 5% of it costs 13 detections at 1000/day, so the
  derivative is negative at the corner and no allocation over any number of arms improves on
  it. Recorded as an oracle and never quotable as achievable
- `cmd/replay -weighted` gates the arm, and either it or `-ledger` turns on the burn-in mirror
  the fit needs. Off by default, on the standing rule that a default changing what every result
  means is not changed on an argument
- `cmd/analyse` records each arm's **false-alarm rate**, false positives over events scored.
  It is the only quantity on which this framework is comparable to a published detector at all:
  precision and recall are properties of a corpus and a budget, and the per-event rate is a
  property of the method. It feeds the base-rate figure's operating-point rules
- **a fourth combination rule: the weighted arm.** Each alert is scored by the
  log-likelihood ratio its own detector's fitted weight implies, over that detector's frozen
  burn-in null, and the day's budget goes to the highest scores. There is no share parameter:
  a common scale plus a fitted weight already implies a share, so the allocation falls out of
  the scoring rather than being chosen. A detector whose labelled burn-in events sat where any
  event sits scores every alert it holds at exactly zero and enters the queue only if the
  informative detectors fail to fill it -- which is what stops `volume`, detecting nothing
  anywhere, from drawing a sixth of the budget as it does under rank fusion
- the weighted arm **reports itself unmeasured when no labelled event falls before the
  boundary**, rather than reporting an arm whose every weight is uninformative. A weight fitted
  on nothing is an equal quota under another name, and the two must not render alike
- the arm's fit **records its own evidence**: per detector, the fitted `a`, how many labelled
  burn-in events it surfaced, how many it evaluated and missed, the deviance against
  uninformative and whether that cleared the threshold. A weight in a result can be read back
  to what earned it
- `cmd/replay -ledger` records each labelled burn-in event's **log** p-value per detector, not
  its p-value. A detector's tail reaches ln P = -4000 on this corpus, which is zero as a
  float64, and a weight fitted from a sample of zeros is fitted from nothing. Burn-in labelled
  events happen to sit well short of that, histories being short early in the corpus, but
  "happens to be representable here" is not a property to rely on
- **`domain/allocation`: how much of a budget each detector has earned.** Two frozen
  quantities per detector and a per-alert score built from them. `Tail` is the detector's own
  null over its log p-value, so alerts from detectors that share no p-value scale become
  comparable; `Weight` is the single parameter of a Beta(a, 1) density over those null
  quantiles, fitted on labelled events from a window disjoint from the one being scored. An
  alert's score is the log-likelihood ratio of its being labelled against its being
  background, so the budget divides itself and there is no share parameter to choose
- the tail is **extended below the empirical floor rather than floored at it**. A rank in a
  sample of n cannot fall below 1/(n+1), and on this corpus the alerts worth having are past
  that floor -- the fitting window is meant to be quiet -- so flooring ties the head of the
  queue and orders it by arrival time. Past a high threshold an exponential fit to the
  excesses takes over, which is strictly decreasing everywhere and linear in log p, so no two
  alerts of different extremity tie and nothing underflows at ln p = -4000
- the weight is **tested for significance before it is used**. Two hundred labelled events
  drawn from exactly the uniform null fit a ~ 1 +/- 0.07, and the likelihood ratio at
  a = 0.93 scores an alert at ln q = -4000 some 248 log units above zero -- so a weight that
  is pure sampling noise would buy a detector that found nothing a large share of the queue.
  A fit is kept only when twice its log-likelihood ratio against a = 1 exceeds 2.706, the 5%
  one-sided point for a parameter tested at the boundary of its range
- the fit **counts the labelled events a detector missed**, as right-censored observations
  rather than as absences. Without that term the likelihood treats a detector that surfaced
  two of forty-nine labelled events, at its two most extreme ranks, as the sharpest detector
  in the set: measured on such a sample, a = 0.38 with censoring against a = 0.07 without. A
  detector that abstained contributes neither an observation nor a censoring point, because
  abstention is the absence of an opinion rather than a weak one (R3)
- **the score is per-alert by construction, not by accident.** Every quantity it reads is
  either a property of the single alert or of state frozen before the scoring window began,
  so the same score that ranks a batch thresholds a stream. A score reading an alert's rank
  among the day's events evaluates just as well and cannot be deployed at all: an operator at
  14:00 does not know what arrives by 23:59
- **`application.ReplayCorpusCommand.BurnInSink`**: the burn-in window's events, with their
  verdicts, offered to a caller instead of discarded. It is what makes a weight fittable on
  data the scoring window has not seen, and a test asserts the two sinks partition the stream
  rather than trusting that they do -- if one event reached both, every weight fitted on the
  first would be an oracle
- **`cmd/replay -ledger`**: every per-detector arm's ranked queue, per day, for both windows,
  written whole. A replay of one corpus is eighty minutes and the allocation question has a
  large candidate space with no theory that picks one, so the candidates are screened against
  the recorded order offline and only the winner is run. The cheap substitute was tried first
  and does not work: reconstructing each arm's queue from the committed p-histograms read
  Detector I at 43 where the run recorded 11, because the histogram is twenty bins to the
  decade and the real arms take their top B per day where a reconstruction pools the run
- `make method-table` emits **one table per budget** rather than one table at the widest
  budget. The comparison moves further with the budget than it does with the method -- at
  1000 alerts a day five of six planted types are reached, at 100 exactly one is, at 10 none
  at all -- and a single table at 1000 credits the framework with a reach that is mostly a
  fact about what was affordable. The set is emitted in one pass so three tables cannot come
  from three different files
- **a third combination rule: the union arm.** Alert on any event that *any* detector ranks
  highly, by rank fusion -- each arm ranks the day on its own p-value, an event scores the
  best rank it reaches in any arm, and only the within-arm order is ever read, so no p-value
  is compared across detectors that share no scale. Alerts are deduplicated on
  `(t, entity, src, dst)`, since the arms' sets are not disjoint and an event three arms rank
  highly is one alert. Ties break on the event identity and never on a p-value: rank
  collisions are structural, every arm has a rank 1, and breaking them on log p would hand
  each collision straight back to the arm whose p-values are numerically smallest -- the
  exact bias the rule exists to remove
- the union is reported under **two accountings, because one would mislead either way**. At
  equal cost it is truncated to the same alerts a day every other arm gets; at equal depth
  every arm keeps its own top B and the deduplicated union is emitted whole, at several times
  the budget. `budget_multiple` states how much more
- the union **names the labelled events it caught** rather than leaving them to be
  reconstructed. Every other arm's catch is recoverable by re-ranking the labelled events on
  that arm's p-value; a fused rank is not a p-value and no labelled event carries one, so
  without the recorded keys every per-attack-type row for this arm would be blank
- **`cmd/methodtable` and `make method-table`**: the paper's headline table, one row per
  method and one column per attack type, derived from the recorded runs rather than typed.
  It crosses fifteen methods with seven ground truths and keeping a hundred cells correct by
  hand across a re-run is not a reasonable expectation
- the table carries an **alerts column**. It is the one number a reader could be actively
  misled without: the union's equal-depth rows find the most and spend 4.5x the budget to do
  it, and a recall figure without its cost invites exactly the wrong conclusion
- the dashboard shows the four union arms, each stating which accounting it was charged under
- **GitHub Pages is enabled and publishing**, at https://johnpierman.github.io/ethogram/

### Changed

- **the figure draws both of this framework's arms, not only the one that wins.** Its per-entity
  novelty arm reaches labelled events at every budget -- 11 at 10 alerts a day, where every one
  of the eight comparators reaches none -- but at 50 alerts a day its combined verdict reaches
  none where `entity_ewma` reaches one. Drawing only the arm that dominates would overstate the
  framework in its own opening figure; the composite's weakness here is section 5.4's finding
  arrived at from the other direction
- the four comparators drawn are the four with the most detections summed over the budget range,
  named in the caption as such. Selecting by strength rather than by fame is deliberate: of the
  four most frequently cited methods, three (`iforest`, `pca` and one-class `ocsvm` below 5,000)
  reach nothing here, so a figure built from the best-known names would have compared this
  framework against its weakest available opposition
- a detector that reached nothing at a budget is drawn in a column marked zero past a break in
  the axis, rather than omitted. Precision of exactly zero has no place on a log axis, and a
  line that vanishes reads as "not measured" where the truth is "measured, found nothing"
- section 1.1 states that the figure is measured on the `r11` subset, whose base rate of one in
  7,633 is ten times the full corpus's one in 76,901, so every precision on it is easier than
  the arithmetic in that section demands

- **the paper is rewritten as a research paper rather than a record of how it was found.** It
  stated a claim and corrected it later in six places -- "this refutes part of §3.3's
  diagnosis", "an earlier version of this paper concluded", a paragraph correcting a source-code
  comment the reader never sees -- and each is now said once, correctly, where it belongs. The
  combination rules and the calibration are defined in the method section instead of being
  introduced inside three separate results subsections, so no subsection has to re-explain the
  machinery of the one before it
- the audience is stated: statisticians rather than engineers. The build-assurance section, the
  requirement table's "Enforced by" column, and the changelog of a withdrawn requirement are
  gone; the developer-diary rhetoric with them. Twenty-six bolded pseudo-headings are plain
  topic sentences, and the four argumentative section titles are descriptive
- **a data and evaluation-design section, which the paper did not have.** What an
  authentication event contains, what a red-team exercise is and why its labels are a partial
  record, what each of the four sampling designs is and what base rate it carries, and what the
  six planted mechanisms isolate. A reader previously could not tell what population any figure
  generalised to
- **every proportion now carries a 95% Wilson interval.** They were already computed for one
  arm and reported for none; `cmd/analyse` now emits them for every arm, combination and union
  accounting, and the paper reads them. Two conclusions change: the novelty and pairing
  detectors at 100 alerts a day are 10.9% (8.6-13.8) and 10.7% (8.4-13.6) recall, which is one
  measurement and not a ranking, and the union's domination at equal cost is confirmed as
  separated rather than asserted from point counts

### Fixed

- **the stated mechanism for the population marginal taking 120 of 120 planted takeovers was
  the one the corpus was built to exclude.** The paper said a takeover "moves an account onto
  population-rare values, which is exactly that detector's null". The injector plants the most
  population-COMMON value the victim has never used, precisely so that population rarity is
  held out, and its own comment records the earlier version that got this wrong. The measured
  explanation is cardinality: the marginal's median p-value is 0.59 on the mechanisms that
  substitute the destination computer (3,535 distinct values), 0.17 on the one that substitutes
  the authentication type (14 values, of which three cover 99.6% of resolved ones), and
  3.8e-05 on takeover, which substitutes two low-cardinality fields at once. Holding population
  rarity out succeeds where the vocabulary is large and cannot where it is small, which is a
  limitation of the planted corpus and is now stated as one
- **the utility-cutoff table had the wrong precision at the tightest budget**: 4 of 70 is 5.7%,
  not 6.7%. 6.7% is the 100/day row's figure, 47 of 700. The same table said two thirds of the
  queue is suppressed for free at 10 and 100 a day; the recorded shares are 57% and 68%
- **"the composite does not detect" was contradicted by two of the paper's own tables**, which
  give it 6 at 100 alerts a day and 113 at 1000. The claim is now scoped to what holds: the
  uncalibrated composite detects nothing at 10 to 100 alerts a day, conformal calibration moves
  it to 6 at 100, and it is an order of magnitude below its best component at every budget
- **the composite was computed under a repair the paper never disclosed.** Brown's correction
  needs a positive variance estimate and the burn-in covariance implies Var[X²] = −27.5 with six
  detectors; the code degrades to plain Fisher and records it. Every composite figure is
  therefore plain Fisher, which the method section now says, and the negative estimate is read
  as evidence about the marginals rather than mentioned in passing
- **"the other five detectors divide the remaining 16%" of the corrected minimum's queue.** Four
  do -- pairing 390, novelty rate 292, timing 225, marginal 214 of 7,000 -- and `volume` and
  `cooccurrence` never supply the minimum at all
- **the per-entity baseline's single detection was read three times against its own result
  file's instruction not to read it.** The export samples 1 in 100 events, so every entity's
  history is decimated and the file records `entity_history_intact: false`; an EWMA over an
  entity's past estimated from a hundredth of it is not a measurement of the per-entity framing.
  The paper now declines the comparison, and the "margin rests on one event" claim is withdrawn
  from all three places it appeared
- **the baselines' implementation provenance was wrong.** Extended isolation forest and
  half-space trees are not scikit-learn, and the one-class SVM is a Nyström map with a
  stochastic-gradient objective rather than the exact kernel machine, which the result file
  records as a deviation. Each is now named for what it is
- **the two baseline advantages were reported as two different factors, 97x and 8.3x.** They are
  one factor: reading a 1-in-100 event sample raises the labelled share and the share of the
  corpus a fixed budget covers by the same hundredfold
- **the dash convention was broken by the table that stated it.** The rule is that a dash means
  unmeasured and never zero; the seven baseline rows then carried zeros in every column with a
  note that they were measured at a different budget. Baselines now have their own table at
  their own budget
- **the per-mechanism table had a `total` column summing planted detections and real ones.** The
  corpus that supplies the planted labels states in its manifest that sensitivity to a mechanism
  and detection of an intrusion must not be combined into one headline. The column is gone
- **the day range was written two ways.** "42,218,530 scored events over days 7 to 13" and
  "days 7-14", which reads as eight days, for the same seven-day window. It is now stated once
  as an interval
- **three different quantities were each called "the base rate"**: 1.30e-5 on the full corpus,
  1.31e-4 on the `r11` subset and 3.1% on the baselines' sampled export. All three were correct
  and none was labelled. A corpus table gives the scored count, labelled count and rate for
  every design, and every rate quoted afterwards names its corpus
- the `Šidák` and `×` characters in the headline table were double-encoded mojibake, from a
  paste rather than from the generator. The tables are regenerated
- effective sample size is now stated rather than implied: 549 labelled events on 104 accounts
  with 93.6% of label rows sharing one source computer, and eight victims per planted mechanism
  with deterministic value choice, so "120 of 120" is eight of eight and every interval in the
  paper is optimistic
- the entity-day aggregation moves out of a threats bullet, where it was also being offered as a
  conclusion, and is reported with its confound priced: 65 of 108 labelled entity-days at 100
  alerts a day, and 14 once the statistic is count-normalised, so most of the apparent gain is
  activity rather than evidence
- **the paper's lead conclusion was contradicted by its own data.** It read "per-entity
  conditioning works and population-scope conditioning does not". Measured against planted
  ground truth, the population marginal detects **every one of 120 account takeovers** and a
  population density baseline reaches the low-and-slow exfiltration no arm here touches. The
  claim is now scoped to the real campaign, where it holds, and the scopes are described as
  complementary because that is what the measurement shows
- **the baselines are not all zero, and one of them finds what this framework cannot.** On
  the injected corpus one-class SVM reaches 33 of 120 account takeovers and local outlier
  factor reaches **12 of 288 low-and-slow events**, the one attack type no arm of this
  framework reaches at any budget. The previous text generalised a zero from the matched
  262-event comparison and asserted that isolation, density, boundary and linear-subspace
  models "fail together", which the injected corpus contradicts. Both baseline figures now
  carry the fairness caveat, and it favours the baselines: they read a 1-in-100 sampled
  feature table whose labelled share is 3.0% against the framework's 0.031%, a base rate 97
  times easier, and their budget permitted 1.3% of their corpus against 0.16%, an 8.3 times
  larger share
- **"low-and-slow is invisible to every arm at every budget"** is now "every arm of this
  framework", with the baseline that reaches it named. The unqualified version reads as a
  property of the corpus when it is a property of this approach
- `noveltyrate` was documented as staying opt-in **"until a second corpus agrees"**. A second
  corpus now agrees, so the condition the paper set has been met; the default is a decision
  waiting to be taken rather than evidence waiting to arrive. Recorded in both the paper and
  the README, and the flag default is deliberately left alone
- **the overclaim in `domain/noveltyrate`'s own package documentation.** It said the detector
  took credential spray, lateral movement and account takeover "from nothing to nearly
  everything". Measured at 1000 alerts a day it takes them to 117 of 320, 26 of 40 and 64 of
  120 -- a real effect on three of six types and a smaller one than claimed. Corrected where
  it was written, not only in the paper
- the crowding-out figures in the combination diagnosis were from the superseded 200-topk run.
  On the current run they are starker: `novelty` carries **5,879 of 7,000 min-p alerts, 84%**,
  while `marginal` -- the only arm that detects every planted takeover -- is left with 214, or
  3%
- a claim this changelog's own author introduced and then checked: that at 100 alerts a day no
  method but the marginal reaches a planted type. The composite reaches 24 account takeovers
- **the published site's primary link served raw Markdown.** The index linked `PAPER.md`,
  which Pages returns as `text/markdown` rather than rendering, so "The paper -- start here"
  handed the reader source text. It now links `paper.html` with the PDF beside it
- the Pages workflow's own explanation of why it was switched off cited a private repository,
  a proprietary licence, a thesis and an evaluation report. The repository is public, the
  licence is Apache 2.0, and the other two were deleted
- **the lint gate broke on an upstream tag it was not asked to use.** `go install` of the
  pinned golangci-lint v2.1.6 also resolves the module's latest version to report
  deprecation; that resolved to a v2.13.0 the checksum database did not have, the lookup
  404'd, and a previously green gate failed with no change to this repository. `GOSUMDB=off`
  on that install step skips the lookup. The tool stays pinned to an exact version, and what
  is given up is checksum verification of a linter that ships nothing

### Changed

- **both 1000-alert runs were re-recorded to add the union arm**, in place and under new run
  ids (`lanl-r11-b1000-union-d7-14-002`, `lanl-inj-b1000-union-d7-14-002`). Every per-arm and
  min-p figure reproduces **exactly**, which is worth stating as evidence rather than as
  reassurance: it is a determinism check across a code change (R4), not a claim that nothing
  moved
- **the results section leads with the method-by-attack-type table.** The two separate
  per-attack-type sections it replaces are gone, so the paper answers "which method should I
  use against which attack" in one place instead of three
- the paper is 15 pages and 6,410 words. The ceiling is 15, exceeded above 10 only for tables
  and citations, and `make paper` still fails the build past it


### Added

- **detection broken out by attack type at 10, 100 and 1000 alerts a day** (paper section
  3.6), from `lanl-inj-b1000-conf-d7-14-001`: 856 planted attacks across six types plus the
  549 real labelled events. Attribution is by victim account, which the taxonomy makes
  unambiguous -- victims are disjoint from every account the real labels name, so the account
  alone says which ground truth an event belongs to. Two results carry the section:
  - the population `marginal` arm detects **120 of 120 planted account takeovers**. A
    synthetic takeover moves an account onto values that are rare population-wide, which is
    precisely that detector's question, so the sweep is the expected result -- and it is the
    one place a population-scope model is the right instrument
  - **Detector V is the broadest arm**, the only one reaching four of the six types, and the
    only one reaching credential spray (117/320), lateral movement (26/40) or privilege
    escalation at all
- **low-and-slow exfiltration is invisible to every arm at every budget: 0 of 288.** Recorded
  as a confirmed prediction rather than an unexplained gap -- it is the type the volume
  detector's dispersion widening was expected to miss. With off-hours (2 of 64, timing only)
  these are 352 of the 856 planted events, and they are where the detection headroom is
- results `lanl-inj-b1000-conf-d7-14.json` and `analysis-r11-b1000-conf.json`

- **the paper now cites its sources.** It carried a reference list that nothing referred to,
  which is a defect in a paper rather than a formatting quibble. There are now 18 references
  and 14 inline citations, and **every reference is cited** -- checked mechanically, not by
  eye. Added along the way: the Leiden partition, higher criticism for the sparse-versus-
  diffuse argument about why Fisher loses to the minimum, conformal prediction, and each of
  the six population baselines by its own paper
- the baselines' provenance is now stated where the comparison is made: all six run from
  their **reference scikit-learn implementations**, not reimplementations, so a difference in
  the comparison is a difference in method rather than in somebody's port of it. That is why
  `sidecar/` stays Python -- the comparison's credibility rests on the models being the
  canonical ones

### Fixed

- **two claims the wider budget disproved.** The paper and the README both said five of six
  planted attack types were invisible to every arm. That was measured at 700 alerts and is
  false at 7,000: five of six are reached. Corrected in both places, and the paper now
  separates the two statements it had conflated -- "the detectors cannot express this attack"
  and "this budget cannot afford this attack" -- because only the second is true of four of
  them
- the README described `noveltyrate` as **"not yet measured"** and told the reader not to read
  it as a capability. A recorded run now includes it (185 of 549 at the widest budget, second
  only to Detector I), so the warning was itself the stale claim. It stays opt-in until a
  second corpus agrees
- the README said the composite **"catches nothing"** while its best component caught 60.
  Under conformal calibration it catches 6 at 100/day and 113 at 1000/day -- still beaten by
  its own best component at every budget, which is the accurate and less absolute version of
  the same point
- `coverage.out` is git-ignored. It is a generated profile that CI uploads as an artefact, and
  it had begun showing up as an untracked file in every status

### Changed

- the paper is 11 pages and 4,633 words, up from 9 and 3,393. The ceiling is 15, exceeded
  only for figures and citations, and `make paper` still fails the build above it

### Removed

- **`docs/` now holds only the two published documents**: the paper (`PAPER.md`, its rendered
  `paper.html`, and `paper.pdf`) and the results dashboard. Gone: the static evaluation
  report, the two Part II/III renderings, the generated specification-versus-code document,
  and the eight committed figure SVGs
- **the generators that produced them, 7,932 lines across `cmd/report`, `cmd/partii` and
  `cmd/partiii`.** With their outputs removed they generated nothing that was kept, and dead
  machinery in a repository meant for reading is worse than none. Implementation detail lives
  in the code, which is commented for it
- the gates that checked those outputs: `verify-provenance`, `figures`, `figures-check`,
  `report`, `implementation` and `screenshots`, plus their CI steps and the orphaned
  renderer-detection step that gated them

### Changed

- **the provenance guarantee is restated to match what now enforces it.** It rested on
  `verify-provenance` failing the build for a figure without a backing run. There are no
  data figures any more: every figure in the paper is a diagram drawn in code, so no figure
  *can* disagree with a result, and numbers in the paper cite the result file they came from.
  What still fails the build is drift -- `dashboard-check` and `paper-check` -- and the
  README and the Pages footer now say that rather than the old claim

### Removed

- **`docs/THESIS.md`, replaced by `docs/PAPER.md`.** The thesis was 1,970 lines rendering to
  38 printed pages. The paper is its trimmed replacement at 385 lines and 9 pages, written
  from a full audit of every quantitative claim against the recorded results. Keeping both
  meant maintaining two documents that disagreed: the thesis still asserted in section 5.3
  that "no method tested -- ours or seven published baselines -- surfaces the labelled
  intrusion at a realistic alert budget", which its own section 15 tables contradict, and it
  reported E6's row count as 6,004,252 where the recorded value is 6,004,253. One document,
  and it is the one whose every figure was checked
- the rendering machinery follows: `make thesis` and `make thesis-check` become
  **`make paper-check`**, `cmd/thesis` defaults to `docs/PAPER.md`, CI gates the paper's
  currency instead of the thesis's, and GitHub Pages links the paper. Stale comments naming
  the deleted file are corrected rather than left to puzzle a reader

### Fixed

- **a replay aborted at the burn-in boundary when Brown's covariance implied an impossible
  variance, throwing away the whole run.** Discovered by adding Detector V: with six detectors
  the burn-in covariance yielded `Var[X2] = -27.5` on one corpus and `-17.6` on another, which
  no joint distribution of the statistics can produce, and the run died 25 minutes in having
  written nothing. The domain function is right to refuse such an estimate; the caller was
  wrong to treat it as fatal. Section 10.2 already prescribes degrading to plain Fisher where
  the covariance is unusable, and a covariance that is present but invalid is the same case,
  so the combination now degrades and **records why** on the score. Absent and invalid stay
  distinguishable: no covariance reports no rejection, an unusable one carries the reason, and
  a reader cannot mistake the second for the first

### Removed

- **`docs/TASKS.md` and `docs/HANDOFF.md`.** Together 13,780 words of internal working notes
  -- a task log duplicating the issue tracker, and a session handoff document. Neither belongs
  in a repository meant for public consumption, and nothing linked them any more

### Changed

- **recorded paths no longer name the machine that ran the experiment.** A result file's job
  is to let a reader confirm which bytes produced a number, and the corpus SHA-256 does that
  completely: it is verifiable by anyone holding the same file, which an absolute path under
  someone's home directory is not. `internal/provenance.RecordedPath` reduces a path to the
  part identifying the file within the project -- `data/lanl/auth.txt.gz` -- and every tool
  that records one now uses it: replay, inject, e5 and e7
- **redacted the machine-local paths already recorded in 33 files** under `results/` and
  `runs/`. A REDACTION and not a correction: applied to the raw text, replacing only quoted
  path strings, with each file verified to parse identically once every string is blanked, so
  no count, no p-value and no structure changed. Corpus SHA-256 values are untouched.
  `verify-provenance`, `dashboard-check` and `figures-check` all pass unchanged afterwards,
  which also confirms no rendered artefact was quoting a local path

### Fixed

- **the README listed `noveltyrate` as a peer detector without saying it has never been
  run.** No file in `results/` or `runs/` mentions it, confirmed by search, so nothing
  measured backs it. The detector table now carries scope and state per arm, marks that one
  **not yet measured**, and tells a reader not to read it as a capability

### Added

- **a paper pipeline with a page ceiling that fails the build.** `make paper` renders
  `docs/PAPER.md` to HTML and prints it to `docs/paper.pdf` through headless Chrome, then
  reports the page count and **exits non-zero above 15 pages**. Prose grows past a budget
  nobody measures, and the target is 10-15 pages, exceeded only for figures and citations
- **a print stylesheet, because the screen one made a short document long.** The rendered
  thesis printed at about 330 words to the page: A4 at 18mm margins was carrying text capped
  at a 74-character measure meant for a wide monitor, with screen leading. Print now sets its
  own page size, margins, type size and leading, and releases the measure cap. Measured on the
  same content: **55 pages to 38, about 480 words per page**. A 4,000-word paper is therefore
  roughly 8 pages of prose, which leaves room for figures inside the ceiling

### Added

- **a budget is now a ceiling rather than a quota.** A budget of 1,000 alerts a day permits
  7,000 over a week against a corpus holding a few hundred labelled events, so most of what it
  permits is cost without return. `cmd/analyse -value-ratio` now reports, per budget, the
  prefix of the queue that maximises `U = v*TP - c*FP`, together with what truncating it saves
  and what it costs. Measured on `lanl-r11-d7-14-001` at 100 alerts a day, the min-p arm's
  queue of 700 can be cut to **221 alerts, suppressing 479 and forgoing zero true positives**,
  with precision rising from 6.7% to 21.3%. At 10 a day, 70 alerts cut to 30 for the same 4
  detections. For the composite, whose queue holds no true positives at all, the optimum is to
  **emit nothing** -- which is the objective correctly declining to deploy it
- the cutoff is reported for every arm that recorded an alert list, which is the composite and
  the Sidak-over-minimum arm. The per-detector arms record detection counts without the alert
  lists behind them and therefore cannot be truncated from a recorded run; the output says so
  rather than leaving it to be discovered

### Changed

- **the README is roughly a third of its former length.** The project's writing had become
  verbose and hard to follow, and the README was the worst of it: a reader had to get through
  requirement tables, a provenance essay and a per-combiner diagnosis before learning what the
  thing detects. It now leads with what it detects, how it works as six one-line questions,
  and four honest limits, and defers the rest to the paper

### Fixed

- **the README's headline claim was false, and it understated the framework's own measured
  results.** It read "no arm detects a labelled red-team event at any alert budget when
  scoring individual events". That is true of the *composite* and of nothing else. On the
  real LANL campaign the per-entity novelty detector catches **11, 26, 38 and 60 of 549**
  labelled events at 10, 25, 50 and 100 alerts a day (`lanl-r11-d7-14-001`), and the result
  replicates on an independent entity subset at **2, 8, 15 and 21 of 262**
  (`lanl-holdout-r7-fixed-d7-9-001`). The composite catches **0 at every budget on both**.
  At the tightest budget that is 11 true positives in 70 alerts -- 16% precision against a
  base rate of 1.3e-4, a lift of about 1,200x, with precision *improving* as the budget
  tightens. The numbers were already in `docs/THESIS.md` in five places; the summary
  generalised the composite's zero over the arms that carry the signal
- **the README asserted the retired R6 mapping and mis-stated the detector inventory.** The
  summary paragraph still claimed an operator's error budget maps to an alert volume, which
  R6's withdrawal retired, and the composition presented as default included two opt-in
  flags while omitting the population co-occurrence arm that is on by default

### Added

- **per-attack-type attribution in the README, and the reading of the combination defect it
  makes possible.** Against 856 planted attacks the population marginal detects **76 of 120
  account-takeover events (63% recall)** and no other arm reaches any planted type. The two
  useful arms are sensitive to disjoint things, and so are the two combiners: Fisher keeps 20
  account-takeovers and none of the real campaign, the corrected minimum keeps 47 of the real
  campaign and no account-takeovers. Both failures are now diagnosed from recorded evidence
  -- Fisher averages one informative detector with four uninformative ones, and the minimum
  compares raw p-values across detectors that share no scale, so the novelty detector carries
  1,267 of 1,400 retained alerts and the marginal's signal is unreachable rather than weak.
  Conformal calibration is the predicted fix for the second, and it is being measured
- **swapping the combination rule is the whole difference between detecting nothing and
  detecting, and the evidence was already in the repository.** At 100 alerts a day on the real
  campaign, the Sidak-over-minimum arm catches **47 of 549** (`lanl-r11-d7-14-001`) and **20
  of 262** (`lanl-holdout-r7-fixed-d7-9-001`) where Fisher + Brown catches **0 on both**. That
  arm is computed and recorded in every run; it is simply not the arm the headline is taken
  from, so the reported composite has been the framework's *worst* configuration. Recorded
  with the ordering that matters: the best single detector (60, 21) beats every combination
  available, so combining currently subtracts value, and a deployment should read Detector I
  directly until a combiner beats it
- a statement of the most important outstanding run: **the per-arm detectors have never been
  measured on the full corpus.** `lanl-d7-14-005` records the composite only, at 0, with no
  per-arm breakdown, so whether the novelty detector's 60 survives at full population scale
  is unmeasured. Every per-arm figure quoted comes from an entity subset, where a fixed alert
  budget is easier to reach, and the README now says so beside the numbers

### Changed

- **BREAKING CHANGE: the module is now `github.com/JohnPierman/ethogram`.** The repository is
  renamed from `calibrated-anomaly-detection`, and every import path with it — 268
  occurrences across 95 files. GitHub redirects the old URLs, so existing clones and links
  keep working, but an importer must update its import paths. Done now because nothing is
  published: the module path is baked into every importer, and changing it after a version is
  fetched through the module proxy is not something a rename can undo. An *ethogram* is
  ethology's catalogue of the behaviours of one individual organism, which is what the
  framework builds and tests against; the linguistic reading the old name carried survives
  where it does real work, in Detector I's open-vocabulary estimator
- **the licence is now Apache-2.0**, replacing "PROPRIETARY AND CONFIDENTIAL / all rights
  reserved". Permissive with an express patent grant. Corpus licences are unaffected, are not
  granted by it, and remain recorded in `DATA.md` — no corpus data is distributed here and
  `data/` stays ignored. The repository remains private and **no version is tagged**: going
  public and publishing `v0.1.0` are separate, deliberate acts, and a tag fetched through
  `proxy.golang.org` is cached permanently
- **the README leads with what the evaluation actually found.** It now answers "does it work"
  in the second section rather than leaving a reader to reach §15: the headline is negative,
  no arm detects a labelled red-team event at any budget when scoring individual events, and
  nor do seven published baselines. Beneath it, the four findings that are load-bearing,
  including that ranking entity-days reaches 25 of 46 at 12.5% precision and that most of
  that is an activity artefact. A package whose pitch is calibration and abstention should
  not require a reader to discover its own negative result

### Fixed

- **the README asserted the error-rate-to-alert-volume mapping that R6's withdrawal
  retired**, and it did so in the summary paragraph, which is the most-read prose in the
  repository. It also mis-stated the detector inventory: what is described as the default
  composition included two arms that are opt-in flags (`-novelty-rate`, `-pairing`) and
  omitted the population co-occurrence detector that is actually on by default. Replaced with
  a table of all seven arms, their scope, their null and whether each is on by default, every
  null checked against the `NullHypothesis()` the code returns

### Added

- **`domain/objective`: the alerting goal as a function that can be maximised.** The obvious
  statement of it does not work, and the reason is worth recording. `TP/FP` is precision in
  disguise -- with `P = TP/(TP+FP)` it is `P/(1-P)`, strictly increasing, so maximising one is
  exactly maximising the other -- and it inherits precision's degeneracy: the maximiser is the
  smallest queue containing a true positive. Forbidding `TP = 0` does not prevent that, it
  moves the corner from "alert on nothing" to "alert on one thing". Measured on
  `lanl-conformal-d7-9-001`, `TP/FP` peaks at **one alert per day** -- a ratio of 1.0, two
  alerts, and 1 of 46 campaign-days found. Any objective that is a function of precision alone
  is scale-free, and a scale-free quantity cannot say how many rows to show, so the objective
  must contain a term that grows with true positives found. It is therefore
  `U = v*TP - c*FP`, scored in units of `c` since only the ratio is identifiable from counts
- **the break-even value ratio, reported unconditionally because it takes no parameter.** It is
  `FP/TP`: the value a true positive must carry, in units of the cost of a false positive, for
  an operating point to start paying. That converts "is this queue worth reading" into a
  question an operator answers from their own operation, and it is what makes the objective
  recordable for every run without anyone having fixed a cost model -- a `v/c` chosen after
  seeing a result is not a threshold. `U > 0` exactly when `TP/FP > c/v`, so an operator's
  target ratio *is* the exchange rate; the objective adds what a ratio cannot supply, which is
  how many rows to take
- **`-budgets` on `cmd/replay` and `cmd/analyse`.** The per-day alert budgets were the literal
  `[]int{10, 25, 50, 100}` in two places. They are now a parameter, parsed into an ascending
  deduplicated `objective.Budgets`, validated at the boundary, and recorded in each run's
  provenance. The default is the previous list, so a recorded run still reproduces from
  defaults. A budget above the run's retained per-day alerts is now refused outright rather
  than silently answered from a short list -- `-topk` bounds what any budget can be, which the
  flag documented and nothing enforced
- **`-value-ratio` on `cmd/analyse`, which grades an already-recorded run.** Every detection row
  gains false positives, `TP/FP` (omitted where unbounded), the break-even ratio, and -- only
  when an exchange rate is supplied -- the objective, whether the point beats alerting on
  nothing, and which budget the objective selects. No re-run is needed to grade existing
  results, and a result that carries no objective records that it was unscored rather than
  leaving a reader unable to tell that from an objective of zero
- **the field registry now separates discrete from continuous numeric fields, and both take
  part in every detector.** There was one `numeric` kind and it was scored by Detector IV
  alone: `KindNumeric.IsEligible()` and `.IsScoreable()` were both false, so no measurement
  ever reached Detector I, the co-occurrence graph, the pairing detector, or the novelty-rate
  null. On a framework whose stated unit of analysis is the individual, that meant the
  per-entity question — the central commitment — was never asked of a byte count, a duration,
  or a score. The split is made on how often a value recurs (`Policy.DiscreteMaxRatio`,
  default 0.1, an average multiplicity of ten) rather than on integrality: a field taking
  0.5, 1.0 and 1.5 is a vocabulary of three values and should be counted, and a field of
  whole-byte counts should not be counted merely because its values are integers. A discrete
  field is counted by equations (4) and (5) exactly as a categorical one is — including at
  population scope, where it previously went to a quantile sketch that discarded its exact
  counts and asked a two-sided question of an unordered code set
- **`FieldKind.Token`, the projection that replaces casting.** A detector that counts a
  vocabulary now scores the token a field's kind returns, not the value's text: the text
  itself for categorical, boolean and discrete fields, and a magnitude band for a continuous
  one. Nothing is converted to a Go `int64`, `bool` or `float64` at any point, and the
  package documentation records why — a per-batch cast makes the reading depend on the batch
  an event arrived in (R1), merges values a source distinguishes (`007` and `7`, `1.0` and
  `1`, `TRUE` and `true`) when text is the identity the framework compares on, and commits
  before the evidence is in, since a column that looks integral for forty-nine events can
  emit a sentinel on the fiftieth. What a detector needs is not a type but a representation
  it can score
- **fixed-boundary magnitude banding (`registry.Band`), standing in for §8.2's quantile
  bins.** Bands follow the 1-2-5 preferred-number series, three per decade, labelled as the
  interval they denote (`[1e3,2e3)`, `(-2e3,-1e3]`, `[0]`) so a reader can check the
  assignment by hand (R5). Measured over eight decades of synthetic measurements: 1,000
  distinct values through Detector I store 11 rows rather than 1,000, and 500 distinct values
  produce 11 graph nodes rather than 501 — the unprojected figure, confirmed by reverting the
  projection. **This is not §8.2's mechanism and is not claimed to be.** §8.2 specifies adaptive
  bins from a streaming digest, and adaptive boundaries cannot be used here: a band is a
  *persisted identity*, since Detector I keeps a decayed count per (entity, field, value)
  over a seven-day half-life and the graph keeps edge weights the same way, so a moving
  boundary changes what a stored label means while the counts filed under it stay put — the
  history becomes a sum over incomparable quantities with no repair short of rewriting every
  stored count. The cost of fixing them is that bands are not equal-occupancy, so a field
  whose values all fall inside one band contributes nothing; resolution on narrowly-spread
  fields is forfeited to keep every other field's counts meaningful. Recorded in
  `docs/IMPLEMENTATION.md` §13 as a substitution rather than a completion

### Changed

- **the identifier guard's numeric exemption now rests on a mechanism that exists.** §5.1
  exempts numeric fields from the guard on the grounds that binning contains them, and the
  code recorded that as "a hole in the guard rather than a considered exception" because no
  binning had been built. It is now built, so a numeric surrogate key classifies continuous
  and is contained by its band — bounded state, bounded graph degree — which is what the
  exemption always promised. An opaque high-cardinality token is still caught by the guard;
  the exemption is for numbers only, and a test pins each case
- **binary fields are recognised by a matched pair of tokens rather than by a set of them.**
  `AllTokensBoolean` asked whether every observed value belonged to a flat set of recognised
  tokens, which cannot distinguish a binary field from a field whose two values happen each
  to be recognised: `Success`/`Logoff` passed it and was labelled boolean, as did
  `Fail`/`Enabled`. `IsBooleanPair` matches the pair, which is the question the label claims
  and is also what makes extending the vocabulary safe — a token added to a flat set widened
  the accidental matches multiplicatively, where a pair matches only itself. The vocabulary
  is extended in the same change to `t`/`f`, `y`/`n`, `on`/`off`, `up`/`down`, `allow`/`deny`,
  `permit`/`deny`, `accept`/`reject`, `granted`/`denied`, `pass`/`fail`, `present`/`absent`
  and `active`/`inactive`. LANL's `Success`/`Fail` and CERT's `Logon`/`Logoff` are unaffected,
  so no recorded result changes
- **BREAKING CHANGE:** `registry.KindNumeric` is removed, replaced by `registry.KindDiscrete`
  and `registry.KindContinuous`; `Kinds()` returns seven kinds rather than six. The kind is
  not persisted in any schema and appears in no `results/*.json`, so no stored state or
  recorded result is invalidated — the break is to the domain package's API only. Callers
  discriminating on the kind should use `IsScoreable`, `IsEligible` or `UsesNumericMarginal`
  rather than an equality test, which is what the detectors now do

### Fixed

- **the inference site and the scoring site disagreed about what a number is.** Numeric
  inference counted a value as a measurement whenever `strconv.ParseFloat` accepted it, and
  ParseFloat is a parser for Go source literals: it accepts `NaN`, `Inf`, `infinity`, the
  hexadecimal form `0x1p-2` and the underscore grouping `1_000`. Detector IV then rejects any
  non-finite value and abstains on it, so a field whose missing marker happened to be spelled
  `NaN` was classified numeric on the strength of values no detector would ever score — a
  permanent abstention wearing a kind, and silent. `registry.ParseNumber` is now the single
  definition both sites use, and a sentinel counts against numeric inference exactly as
  `unknown` or `?` does. It also removes a platform hazard from the band boundaries:
  `math.Log10(1000)` is `2.9999999999999996`, so a logarithm would have put 1000 in the band
  below itself, and the decade is read off the formatted decimal exponent instead, which is
  exact by construction (R4)

### Changed

- **R6 is withdrawn and replaced.** It read *an operator-chosen error rate must map to a
  predictable alert volume*, enforced by conformal calibration and FDR control. It now reads
  *an alerted entity must be more likely than not to be genuinely anomalous; alert volume is
  explicitly not bounded*. Three reasons, in increasing order of weight: it was never met, and
  **E3 reports a realised FDR of 1.0 at every nominal `q`** with every day saturated, so the
  dial had no purchase and the guarantee was vacuous; its enforcement mechanism was measured
  to be the wrong instrument rather than a broken one, since a combination dominated by
  miscalibrated marginals produces a statistic no threshold can be attached to (§10.2); and
  holding it aimed the work at a bounded queue, which is a statement about the analyst's
  workload and says nothing about whether the queue is worth reading. At a base rate of
  1.30 × 10⁻⁵ precision is the binding constraint, and a requirement satisfiable while every
  alert is wrong competes for effort with the one that actually binds
- **R6 is now measured rather than asserted, and it is the only requirement of which that is
  true.** R1–R5 are structural and fail a build; a precision requirement can only be measured
  against labelled data, per corpus. That is a weaker guarantee and is documented as one. It
  is recorded as **currently unmet**, with the figures: read as precision at 100 alerts a day
  over 1,777 entity-days of which 46 are labelled, the best recorded arm — Fisher over the day
  under conformal calibration, `lanl-conformal-d7-9-001` — alerts 200 entity-days for 25 true
  positives, so **12.5% where R6 asks for more than half**. The arms that price in the activity
  confound §15.5 identifies are worse: standardised reaches 3.0% (`lanl-entity-std-d7-9-001`)
  and 1.0% (`lanl-conformal-std-d7-9-001`). No result file is altered and nothing is re-run;
  these are readings of the recorded runs against the new requirement
- **the unit R6 names is the entity, and the pipeline's primary unit is still the event.**
  Recorded as open work rather than smuggled in by a change of wording. §15.5 already ranks
  entity-days, so the unit is not unmeasured; what does not yet exist is a per-entity statistic
  whose ranking is not a proxy for how busy the account is, which is what R6 now demands

### Changed

- **the shape-sensitive reserved mass was tested against recall and loses, so it is not
  adopted.** `lanl-r11-openvocab-d7-14-001` is `lanl-r11-d7-14-001` with `-open-vocabulary` and
  nothing else changed — same corpus digest, same 4,190,603 events, same 549 labelled events.
  Good–Turing's `N₁/n` reads vocabulary *shape* rather than *size*, which is precisely what the
  1/*n* finding above says is needed. Measured: **novelty 60 → 46 and pairing 59 → 36** at 100
  alerts a day, and novelty worse at every budget. In the most extreme decile under equation (4)
  it *raises* 43 of 54 p-values by a median factor of 109 — a large history means many values
  seen exactly once, so the singleton rate is high for exactly the accounts equation (4) ranks
  highest, and Good–Turing judges a first-ever value unremarkable for them. Of the 60 events
  equation (4) alerted on it keeps 24, loses 36 and gains 22. **The two effects are aligned on
  this corpus by accident and the accident is load-bearing**: the size-dependence is the wrong
  key in principle and happens to correlate with the answer here. The flag stays off by default
- **corrected two claims this branch's own measurements disproved.** §15.9.1 had said the
  Good–Turing path was the actionable fix and merely untested; it is now tested and loses, as
  above. And §9.2 quoted the timing detector scoring planted off-hours events at a median *p* of
  0.32; on the corrected run it is **0.032**, the figure having moved tenfold purely because a
  different eight accounts were selected as victims. Both are recorded as corrections rather
  than silently adjusted, and the second now carries the caution that any single number there is
  a property of the sample as much as of the detector. The consequence is unchanged: the
  detector alerted on 0 of 64 planted off-hours events at every budget

### Fixed

- **the guard against sampling an already-sampled corpus was disarmed on a corpus derived
  from one.** It keyed on `kind == "corpus-subset"`, so it saw nothing to guard on a corpus
  built *from* a subset under a different kind — and `cmd/inject` writes an augmented corpus
  over the residue-7 subset as `corpus-injection`. That made the injected corpus, the one
  carrying synthetic ground truth, the single corpus on which the
  double-sampling defect could
  still happen silently. The guard now arms on the presence of an inherited `sampling` block,
  because a corpus derived from a sample is still a sample, and the result records which kind
  of derived corpus it read
- **a synthetic label could be credited to a real background row.** A label is keyed on
  `(time, entity, source, destination)` and not on the authentication type, so an injected
  event landing on a second the victim already occupies produced a key matching two rows.
  `privilege_escalation` and `low_and_slow` are the exposed cases, because both hold the host
  and the hour at the account's usual values — which is exactly where its real traffic is.
  Verification of the first injected corpus found one such collision in 856 events; injected
  events are now shifted to the next unoccupied second
- **the held-out evaluation scored red-team accounts and nothing else.**
  `lanl-holdout-r7-d7-9-001` applied entity sampling twice — once building the corpus
  subset as residue 7 of the entity hash, and again in the replay, whose selector keeps
  residue 0. The two sets are disjoint, so every background entity was skipped
  (`events_skipped` 3,077,318) and only the labelled entities, which the sampler exempts,
  survived: **99 entities scored, all 99 of them red-team accounts, zero background**.
  The run wrote a well-formed result with complete provenance and numbers in a plausible
  range, and was read as a held-out replication. Re-run without the second sampling as
  `lanl-holdout-r7-fixed-d7-9-001`: 1,427,225 events over 1,029 entities of which 984 are
  background. **The core finding survives the correction** — novelty is unchanged at 21 of
  262 at 100 alerts/day while every population-scope detector falls to zero — and the
  anomalous `population_rare` census of 31 falls to **0**, matching every other run and
  restoring the structural finding
- corrected the `population_rare` claim's scope. It is stable across every
  correctly-sampled run and its one apparent exception was the defect above, but
  `novel_pair` genuinely does move with the entity sample (29, 20, 10) because the
  co-occurrence graph is built from the retained entities. Both are population-scope
  categories and neither should be quoted from a sampled run without saying so
- the baselines recorded **how many** labelled events each model caught but never
  **which**, so `cmd/analyse` could not attribute a baseline detection to an anomaly
  category and every per-category row rendered as "recorded its detections as a count
  without naming the events". The §12.4 per-category comparison — the table that makes an
  advantage attributable to a kind of anomaly rather than asserted in aggregate — was
  therefore unmeasurable in every committed result. `detections_at_budget` now carries
  `detected_events`, and the per-detector arms are recorded as having the same gap rather
  than being shown as zero

### Added

- **`cmd/inject`, which plants synthetic attacks of named kinds so detection can be measured
  per attack type.** The real campaign arrives as one uneven mix — 194 of 262 labelled events
  are novel pairings while 100 are volume bursts nothing detects — so a per-type table built
  from it alone cannot separate a detector that cannot express a mechanism from a corpus that
  barely contains one. Six types: credential spray, lateral chain, off-hours, privilege
  escalation, low-and-slow and account takeover. Three are deliberately **single-signal** so a
  detection is attributable to one null, and `low_and_slow` is the case the dispersion
  widening was built to tolerate, making it the type most likely to be missed **by design** —
  which is worth measuring rather than assuming. Every injected event is individually
  plausible: destinations, authentication types and logon types are all drawn from values that
  occur in the corpus, so only the combination, the timing or the volume is unprecedented for
  the victim. Victims are disjoint from every account the real labels name, so the account
  alone says which ground truth a label belongs to
- **detections attributed to an anomaly category and to an attack type on the dashboard.** The
  baselines name the events they caught, but our own arms recorded only *how many*, so every
  column the project cares most about sat empty. An arm ranks on its own p-value and alerts on
  the day's most extreme events, so the labelled events it caught are exactly the N with the
  smallest p for that detector: the count is recorded and the ranking recovers the identities.
  A reconstruction, not an estimate — no threshold is re-derived and no histogram is
  interpolated. On top of that, a coverage matrix over the planted types comparing every arm
  against every published baseline. **Structural categories overlap and must not be summed; an
  attack type is a partition.** Both facts are pinned by tests, and the synthetic and real
  columns are never added together on the page, because one measures whether a detector
  responds to a mechanism by construction and the other whether it found a real intrusion
- **detection measured per attack type, on planted ground truth.** From the matched pair
  `lanl-injected-r7-d7-14-002` and `baselines-injected-r7-d7-14-002` — 4,494,396 events and
  1,405 labelled events, 856 planted across six types and 549 real. **Five of the six planted
  types are detected by nothing at all**: 736 of 856 planted events are invisible to every arm
  and every baseline at every budget from 10 to 100 a day. The only detected type is account
  takeover, led by the **population** `marginal` detector at 76 of 120 with `ocsvm` at 33 — and
  that is not presented as a win, because the type changes four fields at once so one can still
  carry residual population rarity, and it is the only type `marginal` ranks highly, so its
  whole budget lands there
- **what the novelty p-value actually ranks, which is the experiment's real product.** The
  misses are not blindness: a planted credential spray scores a median *p* of 5.5e-04 under
  novelty against the real campaign's 6.6e-04, the same band. The threshold is what excluded
  them — recoverable as *p* ≤ 8.52e-06 — and the most extreme planted event of any type misses
  it by a factor of seven. For an unseen value the estimate reduces to the reserved mass, close
  to 1/*n* with α = 1, and **`p × n` has median 1.15 across 32 victims spanning 263 to 20,666
  events of history and four attack types**. So the p-value for a first-ever value is
  essentially **1/*n* — the size of the account's history, not the surprisingness of the
  value** — and among novel events the ranking is close to sorting accounts by event count.
  Clearing the threshold needs about 117,000 events of history; the busiest planted victim had
  20,666, so no attack on an ordinary account could have been alerted whatever it did
- **a third disjoint replication over three and a half times the window.**
  `lanl-r11-d7-14-001`: residue 11, 4,190,603 events over corpus days 7 to 14, 5,059
  entity-days, 549 labelled events, sharing no unlabelled entity with residue 0 or residue 7.
  Novelty 60 of 549 at 100 alerts/day, pairing 59, min-p 47, **Fisher 0**. The category
  structure replicates almost exactly (`novel_value` 190× lift → 189×, `novel_pair` 154× →
  142×, control 0× on both), so the split is **novelty against everything else** rather than
  per-entity against population, and combining continues to lose on every sample measured.
  **92 of 549 labelled events exhibit no structural category at all** and are invisible to
  every detector here, so the honest ceiling on this run is 457
- **`docs/THESIS.md`, the canonical research document.** The problem space, the motivation,
  every detector's model and null in full, calibration, combination, determinism and
  numerical representation, the evaluation design and the results — split into a part for
  a CTO with no mathematics and a part for anyone who has to verify or referee it. It
  absorbs `docs/METHODOLOGY.md`. `README.md` is reduced to orientation and pointers, and
  `docs/HANDOFF.md` is marked archived, so that exactly one document is authoritative
- **`cmd/dashboard`, an interactive evaluation dashboard.** Walks `results/`, distils a
  compact index and emits one self-contained `docs/dashboard.html` that loads no external
  resource. It puts the framework's arms and the §12.4 baselines in one table **grouped by
  the scope of the question each asks**, because the project's central finding is that
  per-entity detectors carried signal and population-scope ones did not; breaks detection
  down by anomaly category with `population_rare` marked as the control it is; and renders
  a missing measurement as NOT RUN rather than as zero. `make dashboard` regenerates it and
  `make dashboard-check` fails CI when the committed page has drifted from the results —
  the page carries no build timestamp precisely so that a diff means a measurement changed
- **integrity warnings computed from each run's own recorded numbers.** Every check
  corresponds to a defect that reached a committed result while being invisible on any
  page: a run whose scored entities are all labelled accounts is reported as **critical**,
  since it has no background population and no detection figure from it is a valid
  measurement. Provenance records what a run did; it cannot record what the run should
  have done instead, and this is the counterpart to it
- **four baseline models, and the one that matters is per-entity.** Local Outlier Factor
  (density), One-Class SVM via the Nystroem + SGD approximation that makes it tractable
  (boundary) and PCA reconstruction error (linear subspace) broaden the comparison beyond
  the isolation family, which shared one blind spot and made a unanimous zero much weaker
  evidence than it appeared. `entity_ewma` — an EWMA z-score against the entity's own
  history — shares the framework's framing and has none of its machinery, and is held to
  the same score-before-observe discipline, because a baseline that cheats makes the
  comparison meaningless in the framework's favour
- **the first matched head-to-head in the project.** `lanl-holdout-r7-fixed-d7-9-001` and
  `baselines-holdout-r7-d7-9-001` cover the same 1,427,225 events, the same 1,029 entities
  and the same 262 labelled events. At 100 alerts/day: six population baselines across
  four inductive biases detect **0**; the naive per-entity baseline detects **1**; the
  framework's novelty arm detects **21**; and **the framework's own Fisher composite
  detects 0**. The per-entity framing is worth something, this framework's calibration is
  worth considerably more on top of it, and the shipped combined configuration is beaten
  by a twenty-line baseline — which is the sharpest form yet of the finding that combining
  an informative detector with uninformative ones destroys signal
- added §10.1 conformal calibration, behind `-conformal` and off by default. Each
  detector's model tail is replaced by its rank in that detector's own burn-in
  distribution, `p_conf = (1 + #{i : p_i ≤ p}) / (n + 1)`, frozen at the boundary beside
  the dependence estimate and the Leiden partition. It is super-uniform under
  exchangeability whether or not the null holds, which is the point: two of these nulls
  have been measured not to. It is off by default because it changes what a recorded
  p-value means. A conformal p-value cannot fall below `1/(n+1)`, so every event past the
  burn-in tail ties at that floor; `CombinedScore.ModelLogP` carries the same combination
  over the detectors' own p-values and breaks those ties, so the floor cannot hand the
  alert budget back to the timestamp
- added entity-day aggregation to the replay result (`results.entity_days`). The
  framework's premise is that the unit of analysis is the individual, while the alert
  budget is spent on individual events; on LANL day 8, 273 labelled events fall on 45
  entities and one account carries 30 of them, which event-level ranking discards. One
  fixed-size record per (entity, corpus day) carries the event count, the most extreme
  `log_p`, `Σ −2 ln P` and the labelled-event count, ranked two ways — the Bonferroni
  corrected minimum `min_log_p + ln(events)`, and Fisher over the day — because a single
  extreme event and an accumulation of moderate ones are different questions. Neither is
  presented as a calibrated p-value at entity scope: an entity's own events are not
  independent
- added a per-day gap table to `cmd/analyse` (`results.gap`): the alert cut at each
  budget, the most extreme labelled `log_p`, the distance between them, and the pooled
  distribution of labelled events by that distance. Detection alone is a count that reads
  identically whatever happens beneath it while it is zero, so a calibration change that
  moved every labelled event from a thousand log-units outside the cut to five outside it
  would be reported as no change at all

- added Detector III (§8): the decayed k-partite co-occurrence graph of equation (12),
  the DCSBM null of (13) with `λ` per (14), the reported single-block fallback of
  (15) whose collapse identity `λ = k_i k_j / 2m` is guarded by an exact-equality
  regression test against the review's B1 factor-of-two fix, and the Šidák-corrected
  one-verdict-per-event rule of (16)
- added the seeded Leiden partition sidecar (field-local blocks per §8.2, D_r and
  m_rs under the Karrer–Newman convention) with its Go loader; the replay engine
  exports the burn-in graph so the partition never conditions on the window it scores
- added the replay runner with full result-JSON provenance, per-day matched-budget
  alert sets, red-team matching, p-value histograms, status counts and T5 runtime
  measurements; schemas loadable from configuration files with a declarative entity
  admit regex (E6)
- added the report renderer: NOT RUN scoreboard for all nine hypotheses, provenance
  footers, figure-manifest verification wired into CI
- added the 168-cell ablation (§7.1) scored with Detector I's own estimator, its
  executable bin-edge-defect demonstration, and the E9 run arm with the substituted
  combination and midnight-straddler split
- added the §12.4 baseline sidecar (IsolationForest, numpy Extended Isolation Forest,
  numpy Half-Space Trees, rrcf CoDisp), seeded and version-stamped, with per-day
  sample-quantile thresholds at matched budgets
- added `cmd/analyse` (E3: realised against nominal FDR, BH and BY, Wilson intervals,
  conservatism ratios), `cmd/e5` (§11.3 schema growth: three held-out fields,
  treatments A and B in one pass, measured mutual information; treatment C recorded
  as NOT RUN with the §11.2 reason), `cmd/e7` (evidence reconstruction), and
  `cmd/wraparound` (the §12.5 control as a recorded run)
- added measured results: `results/e7-lanl-prefix.json` (79.2% of 49,581 sampled
  verdicts fully hand-reconstructable at 1e-9; the novelty tail case reported as
  partial) and `results/control-wraparound.json` (circular representation passes the
  wraparound control; the 168-cell arm exhibits the defect on both sides)
- added the headline banner and per-category comparison to the dashboard, so the
  claim the evaluation exists to make is the first thing on the page rather than
  something a reader must assemble from tables. The banner renders the recorded
  sentence verbatim beside the baseline run it came from, and is omitted entirely when
  no analysis result carries one; a row whose comparison could not be supported renders
  its recorded reason instead of numbers, and an undefined ratio renders an em-dash with
  its explanation rather than a figure
- added `cmd/partii`, which renders Part II of the whitepaper, "Measured Performance",
  from the committed result files. Part I fixes the framework and the evaluation design
  and states that the measurements appear in Part II, so this is a new document rather
  than an edit to a reviewed manuscript. It carries no hand-typed numbers: every figure
  is read from `results/*.json`, and a hypothesis whose result is absent renders as a
  panel naming the file and the exact JSON key that is missing. A test asserts that a
  section with an empty results block renders no digits at all. Today it reports four
  sections measured and five not yet measured, which is the true state
- added Detector IV (§9): the population-scope marginal outlier component, previously
  the only detector in the framework's own table left unimplemented. It shares Detector
  I's equation (4) and (5) arithmetic bit for bit — a regression test asserts the
  identity — and differs only in scope: Detector I asks whether an entity has taken a
  value, Detector IV whether the population has. Numeric fields are scored against a
  deterministic bounded quantile sketch in the spirit of the t-digest at [44], chosen
  over the reference implementation because the scoring path must be reproducible (R4).
  It abstains below a minimum observation count tied to the reciprocal of the
  `population_rare` threshold, so the abstention states a limit of resolution rather
  than a tuned constant, and above a ceiling of a thousand distinct values, which is
  the same threshold read from the other end: with that many values the average share
  is already below the rarity being tested, so membership of the tail distinguishes
  nothing. The ceiling is also what makes the detector affordable — equation (5)'s tail
  is linear in the distinct value count, and at population scope that count belongs to
  the source rather than to one entity. Measured on LANL auth, admitting the host and
  account fields cost a factor of four in throughput for verdicts that by the argument
  above carried no information; §6 scores those fields per entity, where they do
- added a taxonomy of anomaly categories and per-category reporting. Each scored event
  is classified into zero or more structural categories — `population_rare` (§9),
  `novel_value` (§3.1, §6), `off_hours` (§3.1, §7.1, §7.2), `volume_burst` (§3.1, §7.4)
  and `novel_pair` (§3.3, §8) — from the sufficient statistics its verdicts already
  carry. The categories are properties of the event relative to the history it was
  scored against, not of which detector produced the smallest p-value: a partition
  drawn along our own detectors' firing would be chosen in our favour and every margin
  computed on it would be circular. `population_rare` is retained as the control,
  being the category isolation-based detectors answer well
- added the head-to-head comparison against the §12.4 baselines in aggregate and per
  category, at matched alert budget, with Wilson intervals on both arms, McNemar on the
  discordant pairs and a paired bootstrap on the difference. The margin is reported in
  percentage points of recall; the ratio of detections is reported only where the
  baseline's own count is positive, and where it is zero the result records that a
  relative improvement would be a division by zero rather than printing a figure the
  formula invented
- added profile-driven scoring-path performance work: harmonic-major grid pass with
  hoisted per-harmonic products, pooled scratch, sorted-insert alert sets, ingest-side
  entity prefiltering, and insertion-maintained registry path cache

### Fixed

- fixed the volume detector's null, which rejected entities for their own habitual
  behaviour. Equation (11)'s overdispersion `Var/E = (b+ρ)/b` expresses uncertainty about
  the rate `μ`, and that uncertainty shrinks as history accumulates: at T½ = 7 days the
  discounted period count settles at `b ≈ 10.6`, so `Var/E ≤ 1.09` and the null is
  Poisson in all but name, while real telemetry arrives in sessions. An entity whose
  daily volume had always swung between 60 and 480 events scored `P = 1.4e−79` for doing
  240 again. The null is now widened by `φ̂`, the discounted Pearson dispersion of the
  entity's own completed windows, floored at 1 so the correction can only ever widen and
  never sharpen, and equal to (11) exactly below five windows. Measured on LANL days 7–8:
  events below `p = 1e−12` fell from **22.1% to 0.2%**, and 246 labelled events that had
  sat more than 200 log-units outside the alert cut all moved inside 200. Recorded as a
  deviation from (11): §7.4's stated purpose is to avoid a null that over-rejects because
  counts are overdispersed, and the literal (11) does not achieve it
- fixed equation (14) being evaluated across two different graphs. The partition is
  frozen at the burn-in boundary as §8.2 requires, so `D_r` and `m_rs` describe the
  burn-in snapshot, while `k_i`, `k_j` and `m` are read from the live decayed graph;
  `λ = k_i·k_j·m_rs/(D_r·D_s)` therefore carried the ratio of the two graphs' scales and
  inflated without bound as the live graph outgrew the snapshot, collapsing the lower
  tail for every pair. `λ` is now factored as `(k_i·k_j/2m_live)·ω_rs` with
  `ω_rs = m_rs·2m_snapshot/(D_r·D_s)`, a ratio measured entirely within the snapshot; it
  is an algebraic identity when the two graphs coincide and the single-block collapse
  still gives `k_i·k_j/2m` exactly. The partitioned arm's share below `1e−12` fell from
  **99.0% to 43.2%**
- fixed `cmd/e8` never having gated Detector IV. `wire()` registered four detectors while
  the replay path registered five, so E8 — the gate §12.3 lists first, on which every
  other hypothesis depends — had never tested Detector IV's batch independence or
  determinism, while its provenance block asserted the digests described the system
  producing every other result. The detector list was additionally hard-coded at the call
  site, the same defect already fixed once in the replay path, and is now derived from the
  registry that actually ran. E8 still passes with all five detectors under the gate
- fixed numeric fields entering the co-occurrence graph as raw value text. §8.2 admits
  them through quantile bins, which are not implemented, and unbinned each distinct
  measurement became its own node — identifier-like behaviour reached through the very
  exemption §5.1 grants numeric fields on the grounds that binning would contain them.
  They are withheld until the binning exists, with a test asserting it. No recorded result
  changes: LANL auth carries no numeric field
- fixed the corpus-subset command's documented rationale, which claimed the corpus read
  dominates a run. Measured: a full pass over days 0–13 read 239,471,460 rows in 2 minutes
  12 seconds, while scoring the same window took 55 minutes. Reading was never the
  bottleneck; the scoring window, the shadow arms and the entity sample are the levers
- fixed `sidecar/baselines.py`'s note claiming Detector IV wraps an isolation forest. It
  is a population-marginal estimator, so the §12.4 comparison is between independent
  models rather than framework-against-its-own-component
- fixed the combined p-value's underflow, which had made the alert budget meaningless.
  Past roughly `X² = 1450` at two degrees of freedom the χ² tail falls below the least
  positive float64, so every event from there downwards reported exactly zero and became
  indistinguishable from every other. Measured on LANL days 7–13, all 1,400 retained
  alerts had a combined p of exactly zero while labelled attack events sat at 1e−274 —
  representable, far less extreme, and unable to enter the alert set at all, because the
  ordering among the zeros was settled by the timestamp tie-break rather than by
  evidence. The framework detected 0 of 549 labelled events as a direct consequence, and
  the same collapse saturated every BH threshold, so E3's realised FDR described the
  retention limit rather than the composite. `ChiSquareLogSurvival` now supplies `ln Q`,
  which reaches −745 where the tail reaches the smallest normal float and continues
  smoothly beyond; `CombinedScore` carries `LogP`, and alert ranking and BH thresholding
  both use it
- fixed `cmd/e5`, whose discovery counts were right-censored by the alert retention
  limit and reported as though they were measurements. The BH cut is the largest rank
  whose p-value satisfies the step-up condition, and the command searched for it only
  among the alerts it had kept; when the cut reached the last retained alert the true
  cut lay at or beyond that boundary and its position was unknown. The first run
  retained 200 alerts a day and reported 800 discoveries before the schema grew and 600
  after — identically at every q from 0.001 to 0.1, and identically for both treatments,
  because 800 and 600 are four and three days of the cap rather than counts of anything.
  Every realised FDR derived from them was 1.0, which described the retention limit and
  not the composite. Censoring is now detected and reported per era, the realised FDR is
  omitted entirely rather than rendered as a misleading figure when an era is censored,
  and the retention limit is raised with the sorted-slice insertion replaced by a
  bounded max-heap so the higher limit is affordable. The censored run is superseded
  rather than reported
- fixed the recall denominator in `cmd/analyse`, which was derived from the retained
  per-day alert lists rather than from the complete labelled population. A labelled
  event scored unremarkably never entered a retained list, so it was absent from the
  denominator as well as from the numerator, and the reported recall was an upper
  bound rather than a measurement. The denominator is now the run's full
  `red_team_scored` list, and a run that predates that list is reported with its
  provenance saying the figure is an upper bound rather than silently substituting one
- fixed the comparison against the §12.4 baselines, which ranged over the union of the
  two arms' windows. The baselines were scored over days 7–30 and a replay may cover
  fewer, so each arm was measured on a different denominator while the two numbers were
  presented side by side. Every comparison is now confined to the days both arms
  scored, and the retained window travels with the result
- fixed the coverage gate, which named `./application/...` as a literal pattern; `go
  test` exits non-zero on a pattern matching no packages, so the gate failed for a
  reason unrelated to coverage. Scope is now resolved from the packages that exist, and
  the target fails loudly rather than passing vacuously if that set is empty
- fixed the `golangci-lint` invocation: `.golangci.yml` uses the version 2 schema but
  CI drove it through action v6, which runs v1 and rejects that schema as a
  configuration error rather than reporting findings. Moved to action v8 with a pinned
  `v2.1.6`
- fixed the provenance CI job to gate on `cmd/report` existing, so it activates by
  itself when the renderer lands instead of reporting a false failure. It is
  deliberately not made unconditionally green, since a permanently passing version of
  this check would be worse than none
- fixed unchecked hash writes in the event digest by building the encoding as a single
  buffer and hashing once, which also makes the encoding a pure function testable
  independently of the digest

### Added

- added the §7.4 volume core: the Gamma rate posterior of equation (10) under power
  discounting, and the negative binomial upper tail of equation (11) evaluated through
  `math.Lgamma` with upward summation from `k_obs`, so a deep tail is not lost to
  cancellation against 1. Fixtures are exact rationals for `a = 2` and exact `√2`
  expressions for the non-integral `a = 1/2` the paper names as the reason for
  log-gamma; the structural overdispersion `Var/E = (b+ρ)/b > 1` is asserted from the
  closed form and cross-checked against the pmf
- added the §7.2 circular-timing core: decayed trigonometric moments per equation (6)
  with fixed state of `2H + 1` floats, the truncated Fourier density of equation (7)
  clamped at zero, the interpretable-bandwidth mapping and large-κ asymptote of
  equation (8), and the level-set tail mass of equation (9) as a sorted `G = 512` grid
  lookup with a documented resolution floor of `1/2G`
- added `r_h = I_h(κ)/I_0(κ)` by backward recurrence on the consecutive ratio, worst
  relative error 2.22e-15 against scipy `iv` fixtures across κ ∈ [0.5, 700], finite and
  monotone at κ = 5000 where computing `I_h` and `I_0` separately overflows
- added the **§12.5 wraparound control** as a first-class test: an entity active only
  between 23:00 and 01:00 scores 23:30 and 00:30 as unremarkable (P = 0.82 and 0.72),
  scores 12:00 at the grid floor (P = 0.00098), and fits exactly one mode, inside the
  window — midnight is not a seam
- added the hand-computed three-timestamp moment fixture for equation (6), exact dyadic
  expectations at `δ = 1/2` steps, and structural density tests: unit integral up to
  the zero-clamp, mode at the observation, evenness, monotone decay, and uniform cold
  start returning `P = 1` everywhere
- added `LocalMaxima` for the §7.7 evidence view, reporting only maxima above the
  uniform level so truncation ripple near clamped regions cannot render a phantom mode
- added the §7.5 hierarchical blend as an exact convex combination of moment vectors
  with `w = W/(W+τ)`
- added Detector I, per-entity categorical novelty (§6): the smoothed Dirichlet
  predictive of equation (4) with a reserved category for the unseen, the discrete tail
  mass of equation (5), and lazy per-row decay per §6.2 with `δ = 2^(−Δt/T½)`. Fixtures
  are hand-computed and independently verified in exact rational arithmetic; the
  estimator sorts by value before accumulating so the float sum in (5) is bit-identical
  whatever order history arrives in
- added the §6.2 monotonicity and cold-start properties as tests: a novel value is more
  surprising for an entity with richer history at fixed `K`, and an entity with no
  history returns `P = 1` exactly through the general path rather than a special case
- added a super-uniformity test on a non-degenerate discrete null, asserting the
  direction §10.2 predicts, `Pr(P ≤ t) ≤ t`, with Monte Carlo slack; an
  equal-weight null is rejected as vacuous by the test itself
- added the detector half of the §12.5 identifier control: an identifier field receives
  no verdicts and accrues no state rows, proven against the wired detector and its
  repository rather than against the registry alone
- added E8 and R4 runs against the production Detector I, not only the test fixture,
  and an E7-at-unit-scale test recomputing equation (4) from a verdict's own evidence
  with no query back to the store
- added the event model of §5.1, equation (3): `Event` as a partial function over field
  paths, with `dom(e)` exposed as a sorted mask. The value map is unexported and the
  only enumeration is a sorted iterator, so Go's randomised map iteration cannot reach
  a score. Content-derived event identifiers exclude arrival order and batch position,
  which is what lets E8 compare across batch compositions
- added the detector abstraction of §5.2 with the four-valued status of §5.3. `Score`
  receives no write capability and the state update is carried by an `Observation`
  returned from it, so Score-before-Observe is enforced by the type system rather than
  by convention: a detector cannot destroy the novelty it is measuring because it holds
  no writer
- added a `Verdict` type in which a p-value is reachable only when the status is
  `evaluated`, so an abstained verdict has no representable p-value and the neutral
  "0.5 because we do not know" that R3 forbids cannot be constructed
- added a canonical byte encoding of verdicts using IEEE-754 bit patterns rather than
  formatted decimals, making "byte-identical" mean what it says in E8
- added the **E8** batch-independence check and test, which gates every other
  hypothesis. The check validates its own premises, rejecting setups whose pre-probe
  history differs, and ships with a negative control: a detector standardising against
  the batch per equation (1) is required to fail it, so a pass is evidence rather than
  an artefact of a check that cannot fail
- added R4 determinism tests covering repeated scoring against identical state and
  concurrent scoring re-sorted into canonical order, both required to be
  byte-identical. `SortCanonical` imposes a total order with the verdict's canonical
  bytes as final tiebreak
- added architectural fitness tests rejecting `time.Now`, `time.Since`, `time.Until`
  and `math/rand` from the domain layer, enforcing the inward-only dependency rule, and
  flagging a bare returned `0.5`
- added the §6.2 cold-start assertion: with `N = 0` and `K = 0`, equation (4) places
  unit mass on the reserved category and equation (5) returns `P = 1` exactly
- added the field registry of §5.1, held as data: for each `(source, field path)` a
  kind in {categorical, boolean, numeric, identifier, excluded, unknown} together with
  the sufficient statistics supporting it. Detectors iterate the registry and no
  detector names a field, discharging R2. Inference is deterministic and iteration is
  sorted throughout
- added the **§12.5 identifier control** as a first-class test: a field taking a
  distinct value on every event classifies as an identifier and contributes no state
  and no verdicts. The guard waits for evidence, since early in a field's life every
  value is new, and exempts numeric fields, because §8.2 admits those through quantile
  bins from a streaming digest, which independently prevents both harms the guard
  exists to prevent; applying it there would classify a measurement as an identifier
  and silently discard a real signal
- added boolean inference requiring exactly two distinct values, per §6.1's statement
  that a boolean field is the case `K = 2`; a single-valued field is a constant
  categorical field, not a boolean
- added the §5.3 Beta posterior on field presence per `(source, f)`, so a source
  silently ceasing to emit a field is detected as `abstained_unexpected` rather than
  manifesting as quietly degraded scores
- added value-set truncation reporting per §13.3: beyond the cardinality bound required
  for finite state the reserved novelty mass of equation (4) is no longer exact, and
  the condition is recorded rather than concealed
- added Postgres 16 via docker compose on a non-default port under a distinct project
  name, initialised with `--locale=C` so that row ordering, and therefore float
  accumulation order, does not depend on the host locale
- added CI covering gofmt, `go vet`, build, E8 as a separate gating step, `go test
  -race`, an 80% coverage gate on the domain and application layers, `golangci-lint`,
  and a provenance check. Corpus- and database-dependent tests sit behind the `corpus`
  and `integration` build tags so the suite passes in CI, which has neither
- added `DATA.md` recording corpus licences separately from the code licence, the LANL
  schema as verified against the publisher's documentation, measured `redteam.txt`
  ground-truth counts, and the failed-authentication sampling artefact as a threat to
  validity
