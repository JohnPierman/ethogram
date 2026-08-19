# Changelog

All notable changes to this project are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Equation numbers `(1)`–`(20)`, requirements `R1`–`R6`, and evaluation hypotheses
`E1`–`E9` refer to the whitepaper that specifies this implementation.

## [Unreleased]

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
