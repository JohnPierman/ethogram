# ethogram

Per-entity anomaly detection for security telemetry, as a Go package.

An **ethogram** is ethology's catalogue of the behaviours of one individual. That is what
this builds: a profile per account, and a verdict on whether the account has departed from
*its own* behaviour — not from the organisation's average. An account that is permanently
unusual is not thereby suspicious.

```go
import "github.com/JohnPierman/ethogram/domain/novelty"
```

The paper is [`docs/PAPER.md`](docs/PAPER.md). It is short. Everything below is a summary.

## What it detects

On LANL days 7–14, labelled red-team events caught per alert budget:

| | 10/day | 100/day | 1000/day |
|---|---|---|---|
| **`novelty`** (per-entity) | **11** | **60** | **201** |
| `noveltyrate` (per-entity) | 0 | 22 | **185** |
| `pairing` (per-entity) | 4 | 59 | 127 |
| composite (Fisher) | 0 | 6 | 113 |
| union of all arms, same budget | 2 | 21 | 96 |

549 labelled events, `lanl-r11-b1000-weighted-d7-14-005`. `novelty` replicated on a second
subset at 21 of 262.

At 10 alerts/day that is 11 hits in 70 alerts — **16% precision against a 0.013% base rate,
about 1,200× better than chance**. Precision improves as the budget tightens.

### Against published baselines

Eight reference implementations, run on exactly the same 4,190,603 events with the same
labels and the same per-analyst-day budgets:

| | 100/day found | recall | 1000/day found | recall |
|---|---|---|---|---|
| **`novelty`** (per-entity) | **60** | **11%** | **201** | **37%** |
| composite (Fisher) | 6 | 1.1% | 113 | 21% |
| `entity_ewma` — per-entity EWMA | 2 | 0.4% | 11 | 2.0% |
| `lof` | 0 | 0 | 10 | 1.8% |
| `hst`, `ocsvm`, `eif`, `iforest`, `pca`, `rrcf` | 0 | 0 | 0 | 0 |

**Seven of the eight comparators reach nothing at 100 alerts/day, and six of the eight still
reach nothing at 1,000.** A zero there is measured, not missing. The one comparator that does
better than the rest, `entity_ewma`, is the one that also conditions on the entity — which is
the paper's point rather than a coincidence, and why §1.1 groups its lines by the scope of the
null rather than by who wrote the method.

Precision at those budgets: 8.6% and 2.9% for `novelty`, against a 0.013% base rate — a lift
of 650× and 220×. Both columns are lower bounds: the corpus has no true negative class, so an
unlabelled alert on genuine but unrecorded activity counts as a false alarm. Derived from
`results/budget-curve-r11-d7-14.json`; the figure and table in §1.1 are generated from the
same file.

**No combination beats its own best component at the same budget, and the optimum is not a
combination at all.** Four rules are implemented -- Fisher, the corrected minimum, a union
that alerts when any arm ranks an event highly, and a weighted arm that scores each alert by
a likelihood ratio fitted per detector on burn-in -- and all four lose to simply using the
best arm. The last one settles why. Refit its weights on the evaluation labels, which is an
oracle and separates the arms properly, and it still loses; and an exhaustive search over
budget splits, choosing the split with the labels in hand, finds the optimum at the corner --
the whole budget to the best arm. Diverting 5% of it costs 13 detections at 1000/day.

The reason is that the arms are **substitutes, not complements**: 74.6% of `noveltyrate`'s
detections are also found by `novelty`. Splitting halves each arm's depth and the survivors
coincide. Use the detectors directly; the paper works through it.

**Read as a game, the corner result is a theorem and the conclusion usually drawn from it is
wrong.** Rows are detectors, columns are the mechanism an adversary picks, entries are
`P(detected | mechanism)`. Under any *known* attack mix the expected detection of a weighting
is linear in that weighting, and a linear form on a simplex is maximised at a vertex -- so no
mixture fitted to published industry base rates can beat the best single arm. That is not a
property of this corpus. But the best single arm **guarantees nothing**: it is blind to some
mechanism, and a rational adversary picks the blind spot.

At 1000 alerts/day on the planted corpus. "Retained" is the share of what the *best* arm
reaches on a mechanism that this rule reaches, on the mechanism where it does worst;
`low_and_slow` is excluded throughout because no arm of this framework reaches it at all.

| rule | found | alerts | worst mechanism's retained share |
|---|---|---|---|
| best single arm | **384** | 7,000 | **0** |
| randomised over the two best arms | 344 | 7,000 | 0 |
| corrected minimum | 234 | 7,000 | 0 |
| composite (Fisher) | 227 | 7,000 | 0 |
| union, all arms, equal cost | 221 | 7,000 | 0 |
| randomised, competitive-ratio mixture | 189 | 7,000 | **0.421** |
| union, all arms, equal depth | 535 | 31,505 (×4.5) | **1.000** |
| *per-entity routing, oracle floor–ceiling* | *448–535* | *7,000* | *—* |

Every rule this repository had tested guarantees **zero** against its worst mechanism. Two
allocations do better, and the table prices both: the competitive-ratio mixture retains 42.1%
of the achievable on *every* reachable mechanism at the same budget, for 42% of the expected
detection; or buy the guarantee outright at 4.5× the alerts, where the equal-depth union
reaches every arm's best on every mechanism. Coverage is available. It is not free, and it is
not obtained by reweighting.

`make robust` reproduces all of this from committed results, with no corpus. Two smaller
findings fall out. **Randomising is not the same as dividing**, and only dividing had been
tested -- one arm at full depth chosen by lottery finds 344 where the best combination rule
finds 234 at the same alert spend. And every cell whose improvement raises the guarantee lies
in a mechanism the headline table above does not compare arms on, so **improving `novelty`
against `noveltyrate` has a shadow price of exactly zero**.

The union does earn its keep in one place. Let every arm keep its own top *B* and emit the
deduplicated union, and it matches the best arm on **every attack type at once** -- 533
labelled events against the best single arm's 384 -- for 4.5x the alerts. Worth buying only
if a caught incident outweighs ~164 wasted investigations at 1000/day, or ~35 at 100/day.

On a corpus with 856 planted attacks, the population `marginal` arm detects **120 of 120
account takeovers** at 1000 alerts/day, and it is the only method that does.

## How it works

Each detector asks one question and returns a p-value under a stated null, or **abstains** —
"I had no inputs" is a real answer here, not a neutral score.

| Detector | Asks | Scope | State |
|---|---|---|---|
| `novelty` | has this account used this value before? | per-entity | on, **measured** |
| `timing` | is this hour unusual for this account? | per-entity | on, measured |
| `volume` | is this much activity unusual for this account? | per-entity | on, measured |
| `cooccurrence` | do these two values co-occur as the population predicts? | population | **off**, superseded |
| `marginal` | is this value rare across the whole population? | population | on, measured |
| `pairing` | has *this account* combined these two values before? | per-entity | **on**, measured |
| `noveltyrate` | is it producing *new* values faster than it usually does? | per-entity | **on, measured** |
| `drift` | has this account's *rate* shifted upward and stayed there? | per-entity | opt-in, **measured: does not work here** |

`noveltyrate` exists because `novelty`'s p-value for a first-ever value is roughly one over
the size of the account's history, which makes it structurally unable to alert on a small
account however anomalous it is. The rate question is scale-free instead. It is the broadest
arm on planted attacks -- the only one reaching four of six types.

**Both per-entity arms are now on by default, and the population co-occurrence arm is off.**
Two recorded corpora were the stated bar and both clear it: `pairing` reaches 4/59/127
detections at 10/100/1000 alerts a day on `r11` and 4/59/142 on the injected corpus, and
`noveltyrate` 0/22/185 and 0/21/384. The form `pairing` replaces is measured *miscalibrated* --
18.4% of scored events below 1e-12, with no detections at any budget -- and it asks whether a
pairing is unusual for the *population*, which is the question section 1.2 disavows. An account
that always uses NTLM and its own workstation has a near-zero observed co-occurrence weight
against an expectation in the hundreds, so the null collapses on every one of its events for
being consistently itself. `-pairing=false` still selects it, for the ablation, and it is not a
configuration to deploy.

Nothing names a field. The registry infers each field's kind — categorical, boolean,
discrete, continuous, identifier — from the values it sees, so a new log source is
configuration, not code. Which field identifies the account is the one thing you must say.

Alert volume is not capped. The objective is `U = v·TP − c·FP`: you supply what a caught
incident is worth relative to a wasted investigation, and the cut follows. Measured on a
real run, a 100/day budget can drop 68% of its queue and lose no detections.

### Reweighting the selection by history length (`-weighting history`)

`novelty`'s p-value for a first-ever value is about one over the account's history length, so
among novel events the ranking nearly sorts accounts by size. `-weighting history` learns a
weight per history-length stratum on the burn-in window and freezes it at the boundary, which is
what keeps the weights independent of every p-value they rank and keeps the transform deployable
-- every quantity it reads is a property of the single event or of frozen state.

Measured on the injected corpus, detections at 10/100/1000 alerts a day:

| arm | unweighted | weighted | p × n, median | strata floored |
|---|---|---|---|---|
| `novelty` | 11 / 60 / 303 | 11 / **63** / **410** | 3.3 → 16.2 | 0 |
| `timing` | 2 / 9 / 21 | 2 / **15** / **34** | 2,840 → 1.6e8 | 4 |
| `volume` | 0 / 1 / 5 | 0 / 1 / **7** | 1,790 → 8.1e7 | 4 |
| `marginal` | 0 / 76 / 120 | **7** / **30** / **60** | 1,440 → 6.1e7 | 2 |
| `pairing` | 4 / 59 / 142 | unchanged | 45 → 45 | -- |
| `noveltyrate` | 0 / 21 / 384 | unchanged | 319 → 319 | -- |

**It is off by default and it is not a uniform win.** It works on the arm it was aimed at --
`novelty` gains 35% at the deepest budget, and p × n moving 3.3 → 16.2 says the 1/n dependence
actually moved rather than the detections improving by luck. Elsewhere the last column is the
thing to read: a stratum whose estimated null proportion clamps to 1 earns weight zero, and since
a top-B selection cannot drop those events without leaving budget unspent they are floored and
rank last. With four of five strata floored, `timing` and `volume` have effectively stopped
alerting on all but their shortest-history accounts. `pairing` and `noveltyrate` clamped in every
stratum, so nothing was learned and their weights fell back to uniform -- the right answer to no
evidence, and reported as `degenerate` rather than hidden.

The general lesson is worth more than the flag: **Storey's null-proportion estimator measures
departure from uniformity, so on an arm whose null is wrong it reweights the miscalibration.**
Calibrate first, weight second.

### Aggregating to the entity-day, four ways

The framework's premise is that the unit of analysis is the individual, while the budget is spent
on events. `entity_days` in every result aggregates the same evidence to the unit an analyst
actually triages, four ways, on `lanl-inj-b1000-online-d7-14-010`:

| rule | 10/day | 100/day | 1000/day |
|---|---|---|---|
| Fisher's sum over the day | **10** | **74** | 172 |
| count-normalised (`standardised_x2`) | **10** | 43 | 172 |
| Higher Criticism | 9 | 69 | 172 |
| Bonferroni-corrected minimum | 8 | 67 | 172 |
| *the event ranking, for comparison* | *0 of 25 entity-days* | *3 of 138* | *28 of 585* |

Of 172 labelled entity-days out of 4,944. The 1000/day column is saturated -- 4,944 entity-days
over seven days is 706 a day, below the budget -- so every rule alerts on everything and the
column measures nothing.

**The unit change is the result, and it is large.** At 70 alerts a day the entity-day ranking finds
10 labelled accounts where the event ranking at 700 alerts finds 3 across 138 accounts. Changing
the denominator from millions of events to thousands of entity-days is what pays.

**The choice of aggregate is not.** Fisher's sum leads at both informative budgets, and Higher
Criticism -- which is normalised so its null barely moves with the event count, and which beats
Fisher 200-0 on a synthetic sparse alternative -- comes second. This is the same shape as the
Good-Turing result: the principled statistic loses because the unprincipled one's bias happens to
point at the right accounts on this corpus, where the busiest entity-days are also the labelled
ones. Reported rather than buried, because "we replaced it with the normalised form" would read as
an improvement and it is not one here.

Higher Criticism needs order statistics rather than a fixed-size summary, so each entity-day keeps
its 32 smallest log p-values. That bound reorders the tail of the ranking and not its head:
recomputing at shallower depths from the recorded tails, the full order matches on 936 of 4,944
entity-days at k=8, while **the top 10 per day is identical at every depth** and the top 100
differs by 5 of 700 at k=8 and by none from k=24. What a budget surfaces does not depend on the
bound.

### Open vocabularies (`-open-vocabulary`)

Equation (4) reserves `alpha / (n + alpha(K+1))` for values never seen, which depends on the total
count and the number of distinct values and **never on how the mass is spread across them**:

| an account's history | true P(new) | equation (4) |
|---|---|---|
| 3 addresses, 1,000 visits each | ~ 0 | 0.00033 |
| 500 addresses, seen once each | ~ 1 | 0.001 |

Two histories differing by everything get nearly the same answer, and the second is what a
compromised account looks like. `-open-vocabulary` reserves the mass by Good-Turing instead --
`P(new) = N1 / n`, the singleton rate -- which reads the shape directly and needs only the
count-of-counts.

**On LANL it costs discrimination**, novelty's median labelled percentile moving 0.07% to 0.18%.
That is the estimator being honest: LANL's per-entity vocabularies are fairly closed, the attack
signal *is* "used a value never used before", and plenty of ordinary accounts carry singletons, so
Good-Turing correctly raises `P(new)` for them and dampens the very signal the attack produces.

**On a corpus with open vocabularies it is the difference between nothing and everything.**
`make openvocab` generates one -- 150 accounts whose destination is a brand-new host 80% of the
time, 150 that revisit a fixed set of five, and the attacks planted on the *closed* ones -- and
replays it twice:

| arm | 10/day | 100/day | 1000/day |
|---|---|---|---|
| `novelty`, equation (4) | 0 | 0 | 0 |
| `novelty`, Good-Turing | 0 | **144** | **864 of 864** |
| `pairing`, equation (4) | 0 | 0 | 0 |
| `pairing`, Good-Turing | 0 | **144** | **864 of 864** |
| `noveltyrate`, either | 30 | 72 | 72 |
| composite, equation (4) | 0 | 1 | 1 |
| composite, Good-Turing | **6** | **72** | **75** |

Under equation (4) the 150 open accounts produce about **16,800 innocent first-ever values a day**,
each scoring the same `1/n` as a planted one, so they fill the budget and `novelty` detects nothing
at all. Good-Turing gives those accounts `P(new) ~ 0.8` and the closed accounts a tiny reserve, and
the plants become the extreme events they are.

`noveltyrate` is the control and it does not move: it compares an account's novelty rate against
its *own* history, so it was never confounded and the estimator change cannot help it.

**The corpus is synthetic and built to have exactly this property**, so it demonstrates a mechanism
and cannot establish a rate. What it settles is where the LANL result comes from: the correction is
not weaker than equation (4), it is answering a question LANL does not ask.

It also measures the cost #3 is about. The open-vocabulary corpus replays at **525 events a second
against LANL's ~1,900**, because equation (5)'s tail is a sum over the whole value set and that set
never stops growing.

### Bounded state for open vocabularies (`-max-values k`)

Equation (4) sums over every value an entity has been seen with, so the state grows with the
vocabulary for as long as the vocabulary keeps opening. `-max-values k` holds only the heaviest k
per (entity, field) in a space-saving sketch, and the reason to prefer space-saving over simply
dropping values is that **the error becomes a number the run reports**:

- a held count never under-states the truth, and over-states it by at most the weight it
  inherited;
- an evicted value's true weight is at most the least weight held when it went;
- the total is exact, because eviction moves weight rather than discarding it.

Below the ceiling the sketch is exact and says so, so a field that never saturates carries no
caveat it has not earned.

**Bounding it naively destroys the thing it was supposed to make affordable.** On the
open-vocabulary corpus above, at both a 64-value and a 256-value ceiling, `novelty` went from 864
of 864 planted detections to **0** -- while the sketch did exactly what it claimed. Good-Turing
reads the singleton rate, the singleton rate lives in the tail, and a heavy-hitters sketch evicts
the tail one value at a time: the estimator was handed a distribution with its tail cut off and
read a *closed* vocabulary where the truth is open, which is the exact confound the Good-Turing
reserve exists to remove.

That is also the half of the design that was missing -- "a heavy-hitters sketch for the head,
**plus Good-Turing's count-of-counts for the tail**". With the evicted count-of-counts reported
through `novelty.TailReporter`:

| | unbounded | `-max-values 64` |
|---|---|---|
| novelty value rows held | 238,621 | **12,522** |
| events per second | 534 | **780** |
| `novelty` at 10/day | 0 | **30** |
| `novelty` at 100/day | 144 | **300** |
| `novelty` at 1000/day | 864 | 864 |
| worst overstatement | -- | 16.6 on a per-entity total of 1,960 |

**19x less state, 46% faster, and more detections at the tight budgets** -- 150 of 2,400 sets
saturated, 274,361 evictions, and the 864 at the deepest budget unchanged. The gain at 10 and 100
a day is a mechanism worth naming rather than a bonus: the evicted singleton count is *additional*
evidence that a vocabulary is still opening, so the open accounts are suppressed harder than the
held rows alone would suppress them, and the closed accounts' plants surface earlier.

On a synthetic corpus, so it demonstrates a mechanism rather than establishing a rate. The
unbounded store remains the default: it answers equation (4) exactly, and which of the two a run
used is recorded.

### Per-entity routing (`-route policy`)

Routing is not a global allocation: it chooses per entity, so in principle it can send an
established account to a per-entity null and a cold one to the population null and collect both.
That is why it was the one construction with headroom left -- the population `marginal`'s 76
detections at 100 alerts a day overlap the per-entity arms' by **zero**, and an oracle bracket put
routing at 448-536 against the best single arm's 384.

Two decisions were fixed before the run, because choosing either against the evaluation labels
would make the number an oracle wearing a policy's clothes:

- **one queue, ranked by the routed arm's conformal quantile** -- a rank in that arm's own burn-in
  distribution, which is the one scale every arm shares. Putting model p-values from different
  nulls in one queue is the cross-scale comparison the corrected minimum was already measured
  failing on; giving each arm a budget share makes the shares a free parameter.
- **a preference order taken from the arms' own declared abstention thresholds** --
  `noveltyrate`'s MinHistory = 50, `drift`'s eight closed periods, `timing`'s need for spread --
  with the per-entity arms before the population `marginal`. A test asserts that correspondence so
  a threshold cannot drift into being a fitted parameter.

**It loses at every budget, and the reason generalises.** On
`lanl-inj-b1000-route-d7-14-011`:

| | 10/day | 100/day | 1000/day |
|---|---|---|---|
| per-entity routing | 0 | 21 | 381 |
| `noveltyrate` alone | 0 | 21 | 384 |
| the best arm at that budget | **11** | **76** | **384** |
| oracle bracket | -- | -- | *448-536* |

Of 1,286 profiled entities the policy sent 684 to `noveltyrate`, 482 to `marginal`, 119 to
`novelty` and 1 to `timing`, declining none. But **117 of the 129 labelled entities went to a single
arm** -- every attacked account has history, median 3,392 burn-in events -- so routing reproduces
that arm almost exactly and inherits its weakness at the tight budgets.

The disjointness that motivated routing is disjointness in *which events* each arm detects, not in
*which entities* each arm suits. A policy conditioned on history cannot exploit it, because
**history does not predict mechanism**. Reaching the oracle bracket would need a router
conditioned on something correlated with the mechanism an account is under attack by, which is not
knowable at routing time -- so this is a bound on the construction rather than a tuning problem.

Every entity's decision and every also-admissible arm is recorded in the result, so a different
preference order can be evaluated without a second replay.

### Online error control (`-online lord`)

Benjamini-Hochberg needs the whole batch: an operator at 14:00 cannot use a threshold that depends
on how many events arrive by 23:59. LORD++ needs only the prefix, and alpha-investing gives it a
property a fixed-q batch rule lacks -- it spends error budget on each test and earns it back on
each rejection, so a barren stretch decays the alerting rate without ending it. Measured in
simulation: after one rejection and 500,000 barren tests the level is 2.8e-10, six orders down and
still strictly positive, and one rejection lifts it again. Realised FDR at q = 0.1 over 1,000 streams
of 10,000 hypotheses is 0.006, 0.009, 0.043 and 0.087 at non-null fractions of 0, 0.001, 0.01
and 0.1.

**On the corpus it detects nothing, and that is the finding.** Over 4,494,396 tests at q = 0.1:

| stream | rejections | true positives | realised FDR | final level |
|---|---|---|---|---|
| `novelty` | **0** | 0 | -- | 1.1e-12 |
| composite (negative control) | 79,286 | 366 | **0.995** | 8.9e-04 |

Valid error control at this scale demands a level near 1e-12, and `novelty`'s labelled events do
not reach it -- where the fixed budget's realised cut is 8.5e-06, six orders looser, and detects 60
of them. The two are not competing rules; they are a control and a capacity, and the gap between
them is what a calibrated per-alert probability would close.

The composite is the negative control #16 asked for, and it behaves exactly as predicted: its
p-values are anti-conservative enough that the rule spends its wealth on false rejections, earns it
back, and spends again. **No spending rule recovers a level from p-values that are not
calibrated**, which is tracked as #55.

### The sub-hourly inter-arrival arm (`-burst`)

Every arm reaches 0 of 288 planted low-and-slow events at every budget, while an unsupervised
local-outlier-factor baseline reaches 12 -- so the signal is there and the framework's instruments
cannot see it. The plant is twelve events at ninety-second intervals, repeated on three days.
`volume` tests an **hourly** window and averages a seventeen-minute burst against fifty-three
minutes of nothing; `timing` tests time of day and the plant sits in the victim's usual hour; and
nothing asks whether the events came too close together.

The statistic is exact rather than asymptotic. Under a Gamma(k, θ) renewal null the span of *k*
consecutive arrivals is Gamma((k−1)κ, θ), so a short span is a lower tail of the regularised
incomplete gamma. The minimum over window sizes is a scan statistic, and treating it as one test is
the mistake that disqualified two other arms, so it is corrected by Šidák over the windows examined
-- which for nested, positively dependent windows over-states the multiplicity and is therefore
conservative.

**The calibration check came first, and the first null failed it.** That check is not a formality:
it is what disqualified `volume` (24.7% of scored events below 1e−12) and the partitioned
`cooccurrence` arm (99.0%). The obvious homogeneous-Poisson null is calibrated on its own process --
on synthetic streams at one event a minute, ten minutes and an hour, 59,950 scans each, the fraction
below p = 0.1 is 0.025 to 0.032 and nothing at all falls below 1e−12 -- and on real traffic it was
worse than either arm it was meant to improve on. Two independent corrections were needed, both
measured on the same 500,000-row prefix of `auth-r11`:

| null | share of scored events below 1e−12 |
|---|---:|
| exponential gaps, ties floored at one second | **36.66%** |
| Gamma(κ, θ) gaps fitted per entity, ties floored | 5.77% |
| Gamma(κ, θ) gaps, tied timestamps collapsed | **0.021%** |

against `timing`'s 0.056% on the same events.

**Real authentication traffic is not a Poisson process.** Machine accounts authenticate in tight
clusters, so short spans are ordinary and an exponential null finds the ordinary astronomically
improbable. Fitting κ from the entity's own first and second gap moments widens the lower tail
exactly where clustered traffic lives. κ is **capped at one**: a measured κ above one is
under-dispersion, and honouring it would make the null *tighter* than Poisson, so a machine
authenticating every sixty seconds would find a fifty-second gap wildly surprising. An entity whose
dispersion cannot be measured abstains rather than falling back to the narrowest null available,
which is the lesson `volume` learned the hard way.

**The log's resolution is not evidence, and that was the larger correction.** LANL records to the
whole second and one logical action emits several authentication rows, so entities routinely have
many arrivals at one instant. Flooring such a window at one second was called conservative and is
not -- thirty-two arrivals inside one second is astronomically improbable under *any* renewal null,
however wide, so the log's own granularity became the most extreme evidence in the corpus. The scan
buffer holds distinct instants and cannot testify about structure the log did not record.

**What the widening costs is not uniform, and that is the per-entity null working as designed.** The
same plant, twelve arrivals ninety seconds apart:

| victim | fitted κ | plant |
|---|---:|---:|
| quiet, gaps averaging twenty minutes | 0.958 | **p = 2.5e−08** |
| already bursty, pairs a minute apart | 0.338 | p = 6.3e−04 |

For an account that routinely produces sixty-second gaps, a run of ninety-second gaps is close to
what it does anyway. The reference set for an event is the account that produced it, so the plant
genuinely is unremarkable there -- which bounds what this arm can deliver and is stated rather than
averaged away.

**On the corpus it does not fill the hole, and the measurement says why.** Over 4,494,396 scored
events it reaches **0 of 288** low-and-slow plants at every budget, and 23 events at 1000 alerts a
day — 18 credential-spray and 5 account-takeover, a mechanism it was not built for.

| | low-and-slow plants | credential spray |
|---|---:|---:|
| abstentions | **0 of 288** | 0 of 320 |
| most extreme p | **0.31** | **1.1e−08** |
| 10th percentile | 0.978 | 5.0e−06 |
| median | 1.000 | 0.994 |

It abstained on none of them, so this is not a coverage gap: the arm looked at all 288 and found the
plant unremarkable, while reaching 1.1e−08 on spray, so it is capable on this corpus.

**The plant is slower than its victims' own bursts.** All eight low-and-slow victims are heavily
clustered — fitted κ from **0.004 to 0.107**, median 0.017, against a mean gap of 425 s — so twelve
arrivals ninety seconds apart sit inside their ordinary behaviour. An inter-arrival test can only
find events arriving closer together than the account's own habit, and this plant is not that. The
un-widened exponential null *would* have called them extreme, and would have called 36.7% of all
background extreme too.

The allocation equilibrium is unmoved: maximin value still exactly 0, competitive ratio still 0.421,
the mixture unchanged, and `burst` at weight zero — the same outcome the sequential-change arm got,
for the same reason. An arm that reaches nothing no other arm reaches cannot move an equilibrium
(`robust-inj-burst-d7-14-001`).

Per-entity state is 32 arrival instants plus four scalars, fixed size however long the entity is
watched (§13.3). The abstention gate counts **undiscounted** gaps: a discounted count saturates at
1/(1−δ), so a minimum above that ceiling is unsatisfiable forever rather than merely slow to reach.
The arm reports how many entities cleared its gate, because "found nothing" and "was never able to
speak" are different claims and only the second is a gap in coverage — here it is neither, and the
report says so: 741 of 1,507 entities eligible, and none of the plants abstained on.

### Calibrating the combination, not only its inputs (`-conformal-composite`)

The composite's realised false discovery rate was **0.978 at every nominal q from 0.001 to 0.25**.
A level that changes nothing is not a control, and §10.1's conformal calibration does not supply
one: it makes each detector's p-value super-uniform *by ranking alone*, whatever that detector's
null does, and that is a guarantee about each **marginal**. A step-up over composites reads a
function of several of them, and nothing had been said about that function.

The combination is deterministic in its inputs, so its burn-in distribution is available on exactly
the same terms. `-conformal-composite` ranks the composite against that distribution.

**It needs a split burn-in, and the split is not a detail.** The statistic whose distribution is
ranked must be the statistic the scoring path computes, and that statistic consumes conformally
calibrated inputs — which do not exist until the input conformal is frozen. Fitting both on one
window is circular: the composite's distribution would be built from uncalibrated inputs and applied
to calibrated ones, and −2 ln P runs to thousands on a model tail and to tens on a rank. So the
window is split at its midpoint; the input conformal and the covariance freeze there, the
composite's distribution accumulates from there to the boundary, and freezes too. The cost is
stated: both input estimates are fitted on half the window they used to have.

**The measurement is a within-run control, which is what makes it readable.** The run records the
combination's own log tail alongside the conformal value, so the same events, the same calibrated
inputs, the same frozen covariance and the same split can be stepped up both ways. Comparing across
runs would confound the calibration with the split, and the split moves every cross-arm quantity —
`min-p`'s realised rate at its tightest rejecting level went from 0.672 to 0.965 under it.

| nominal q | Fisher + Brown | | conformalised composite | |
|---:|---:|---:|---:|---:|
| | discoveries | realised | discoveries | realised |
| 0.001 | 732 | 1.000 | **0** | — |
| 0.005 | 979 | 1.000 | **0** | — |
| 0.01 | 1,273 | 1.000 | **0** | — |
| 0.05 | 2,283 | 0.979 | **0** | — |
| 0.1 | 2,593 | 0.977 | 1,010 | 1.000 |
| 0.25 | 2,950 | 0.978 | 2,011 | 0.978 |
| **spread** | | **0.023** | | **1.000** |

**The calibration is what the combination needed.** On identical inputs the uncalibrated composite
rejects 732 events at q = 0.001; the conformalised one rejects nothing until q reaches 0.1. A level
that refuses when it is tight is what a level is for. Detection is essentially unchanged —
0 / 5 / **111** true positives at 10 / 100 / 1000 alerts a day against 0 / 6 / **113** for the
baseline — so the fix costs no recall.

It does not make the level *accurate*: where it does reject, the realised rate is still 0.978.
**Calibrating the combination fixes the knob, not the evidence.**

### What Brown's correction actually gets wrong (`-brown-diagnostic`)

"Brown's correction is an approximation of the dependence" is the kind of statement that survives
indefinitely because nothing contradicts it. It has an exact measurable content, and the reason is
that **Brown does not touch the mean**: with c = Var/2E and f = 2E²/Var and E = 2J, c·f = E
identically, so the assumed distribution has Fisher's mean and a different variance. Brown is a
variance correction and nothing else. That splits the diagnosis in two, with different causes and
different repairs:

- **the mean.** A departure from 2J means the marginals are not uniform. No covariance estimate
  reaches it.
- **the variance.** A departure from 4J + 2ΣCov means the covariance is wrong — mis-measured, or
  measured on burn-in and since drifted. Observed *above* the prediction means Brown's tail is too
  light, which is the anti-conservative direction.

Measured per corpus day and per J, over 4.19 million scored events and 76 cells:

| | Brown | observed |
|---|---:|---:|
| mean of X² | 24 – 34 (= 2J) | **11 – 25** |
| effective degrees of freedom | 24 – 34 | **4.4 – 11.3** |
| variance of X² | 48 – 68 | 40 – 220 |

**Twelve to seventeen detectors carry about six degrees of freedom of evidence, and Brown credits
them with twenty-four to thirty-four.** That is why Fisher's sum was inert: the reference
distribution is wrong in both moments and in opposite directions — the mean too large (making the
tail too heavy) and the variance too small (making it too light).

The mean sitting *below* 2J is conformal calibration working: super-uniform inputs give
E[−2 ln p] under 2 per arm. So the marginals are conservative and the dependence is over-credited,
which is the opposite of the diagnosis one would guess from the realised rate alone.

Burn-in days are reported beside scored days wherever a split is in use, because the covariance is
*fitted* on burn-in: in-sample failure means the correction's form is wrong, out-of-sample failure
alone would mean it had drifted. Here it fails in-sample, so the form is what is wrong.

## The paper's length budget

`docs/PAPER.md` is held to 20 pages, 15 of body plus 5 for figures and citations. What `make
paper` **enforces** is a word budget rather than a page count, and the distinction is not
pedantry: a page count is a property of the renderer as well as of the source. Byte-identical
source measured 20 pages under Windows Chrome and 21 under macOS Chrome 141, because font
embedding differs and moves the line breaks that move the pages.

So the enforced gate is 9,600 words — 20 pages at the 480 words a page this stylesheet was
measured to run — which holds on any machine and runs in CI without a browser. The page count is
reported everywhere and enforced only on `PAPER_PLATFORM`, the platform that renders the
committed PDF. A gate that fails on a machine that did not build the artefact stops carrying
information and starts being routed around.

Only commit `docs/paper.pdf` from `PAPER_PLATFORM`; `make paper` says so when you are elsewhere.

## Honest limits

- **Every figure above is from an entity subset**, where a fixed budget is easier to reach
  than on the full 42M-event corpus. The detectors have never been measured at full scale.
- **Precision is 16% at best.** Good against a 0.013% base rate; not good enough to alert on
  unattended.
- **No combination layer works at a fixed budget, and this is now a result rather than a
  gap.** Four rules tried, each beaten by its own best component. A calibrated per-alert
  probability was the proposed fix; it is implemented (`domain/allocation`) and it does not
  help, and neither does an oracle that reads the evaluation labels. On the real campaign the
  optimal split of a fixed budget *is* the best single arm. Where the arms genuinely do not
  overlap -- the population `marginal` against the per-entity arms on planted takeovers -- a
  split does win, by 11 detections of 384. Under a *known* attack mix that is forced rather
  than measured: the objective is linear in the allocation, so its optimum is a vertex.
- **The best single arm nonetheless guarantees nothing against a chosen mechanism**, and
  neither does any rule previously here. `domain/robust` prices the alternatives: 42.1% of the
  achievable on every reachable mechanism for 42% of the expected detection at the same
  budget, or the full guarantee at 4.5x the alerts. Which of those an operator wants is a
  decision this repository states rather than takes.
- **Where the next unit of work is worth spending is not where the headline compares arms.**
  Every cell whose improvement raises the worst-case guarantee is in `low_and_slow`,
  `off_hours` or `privilege_escalation`.
- **Detection by attack type depends on the budget.** At 100 alerts/day one of six planted
  types is reached; at 1000/day five of six are. `low_and_slow` (288 planted events) is
  reached by no arm of this framework at any budget -- though a local-outlier-factor baseline
  reaches 12 of them, so it is a gap in this approach rather than in the corpus.
- **A sequential-change arm was built for that gap and does not close it.** `domain/drift` is
  Page's cumulative sum on the per-entity rate; it separates a sustained +30% shift from matched
  stationary variation by 237x on synthetic streams and reaches **0 of 288** on the corpus, with
  an inverted response there (median p 0.77 against 0.62 on the real campaign). Three reasons,
  and only the first is the statistic's fault: the planted mechanism is three seventeen-minute
  bursts rather than a sustained elevation, so a daily-period statistic is the wrong instrument;
  the plant is shorter than the arm's eight-period warm-up; and the planted events raise the
  entity's own baseline rate, which raises the reference value and floors the sum. **The fix for
  that column is repairing `volume`'s tail, not adding a sequential statistic.**
- **Adding an uninformative arm makes the combinations measurably worse.** That seventh arm
  reaches one labelled event in 4.49 million, and it takes the composite from 227 detections to
  171 and the corrected minimum from 234 to 134 while leaving every single arm untouched. The
  dilution the combination rules suffer from is now tested rather than inferred.
- **Population scope is not useless, which an earlier version of this README implied.** The
  population `marginal` owns account takeover outright and a population baseline reaches the
  one type no arm here does. The scopes are complementary.

## Usage

The whole API is: build a detector over some state, then for each event **score it, then
observe it**. That order is the design — a value must not be able to explain away its own
first appearance — and it is enforced by the types: `Score` hands back the state update, and
committing it is the only way state advances.

```go
package main

import (
	"context"
	"fmt"

	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/registry"
	"github.com/JohnPierman/ethogram/infrastructure/state/memory"
)

func main() {
	ctx := context.Background()
	const halfLife = novelty.HalfLife(7 * event.Day)

	// The field registry infers each field's kind from the values it sees.
	// Nothing here declares a type.
	fields := registry.New(registry.DefaultPolicy())
	nov := novelty.NewDetector(memory.NewNoveltyStore(halfLife), fields, 1.0, halfLife)

	offset := int64(0)
	score := func(at event.Timestamp, dst string, report bool) {
		e := event.New("auth", "U42", at, map[event.FieldPath]event.Value{
			"auth.destination_computer": event.NewValue(dst),
		}, offset)
		offset++

		verdicts, observation, err := nov.Score(ctx, &e) // score against history
		if err != nil {
			panic(err)
		}
		if report {
			for _, v := range verdicts {
				if p, ok := v.PValue(); ok {
					fmt.Printf("%-8s p = %.4g\n", dst, p)
				} else {
					fmt.Printf("%-8s abstained: %s\n", dst, v.Reason())
				}
			}
		}
		if err := observation.Commit(ctx); err != nil { // then advance it
			panic(err)
		}
		fields.ObserveEvent(&e)
	}

	// Burn-in: this account's habit is three machines. DefaultPolicy wants 50
	// observations of a field before it will commit to that field's kind.
	habit := []string{"C625", "C1065", "C529"}
	for i := range 60 {
		score(event.Timestamp(i)*event.Hour, habit[i%len(habit)], false)
	}

	score(61*event.Hour, "C625", true)   // habitual for this account
	score(62*event.Hour, "C17693", true) // never seen for this account
}
```

```
C625     p = 0.3578
C17693   p = 0.06806
```

The account's habitual destination is unremarkable; a machine it has never touched is about
five times more surprising. Note what is *not* in the example: no threshold, no field schema,
and no list of "suspicious" values.

Three things are worth knowing before you wire this to a real log:

- **One field must be named the entity, and one the timestamp.** Everything else is inferred.
  `event.New` takes the entity and the timestamp as arguments for exactly that reason.
- **Abstention is a real answer.** Cut the burn-in above to ten events and every verdict comes
  back `abstained: field kind has not settled; scoring would guess at the type`. A detector
  without its inputs says so; it does not return a neutral score that averages into a
  combination as though it were evidence.
- **Swap `memory` for `postgres`** (`infrastructure/state/postgres`) and nothing else changes.
  State is behind an interface; the in-memory store is for tests and single-process runs.

To score a whole corpus with every detector wired together, use
`application.ReplayCorpusCommand` — `cmd/replay` is a working caller of it, and
`application/replay_test.go` is the smallest one.

## Getting started

```sh
make test      # full suite, race detector on
make e8        # determinism gate; needs no corpus and no database
make cover     # 80% floor on domain and application
make paper     # regenerate docs/paper.html and docs/paper.pdf
```

Corpus- and database-dependent tests sit behind the `corpus` and `integration` build tags,
so the default suite runs anywhere. See [`DATA.md`](DATA.md) for obtaining the LANL and CERT
corpora; no corpus data is distributed here.

## Layout

```
domain/          detectors, registry, calibration, objective — stdlib only
application/     the scoring command
infrastructure/  corpus readers, Postgres and in-memory state
cmd/             replay, analysis, reporting, the dashboard
```

`domain/` and `application/` have no external dependencies. pgx is confined to the Postgres
adapter.

| | |
|---|---|
| Paper | [`docs/PAPER.md`](docs/PAPER.md) · [PDF](docs/paper.pdf) |
| Results dashboard | [`docs/dashboard.html`](docs/dashboard.html) |
| Corpora and licences | [`DATA.md`](DATA.md) |
| Open work | [issues](https://github.com/JohnPierman/ethogram/issues) |

Implementation detail lives in the code, which is commented for it. `docs/` holds only the
two published documents.

## Provenance

No number in any document here was typed by hand. The dashboard reads only
`results/*.json` emitted by a real run, and each result file carries its run id, corpus
SHA-256, row counts, seeds and timestamps. A hypothesis with no result file renders as
**NOT RUN**, never as zero. `make dashboard-check` and `make paper-check` fail the build if
a published document has drifted from its source.

Every figure in the paper is drawn in code, so no figure can disagree with a result. Most are
diagrams of a mechanism and plot nothing. The two that do carry data — §1.1's budget curve and
the table beneath it — are emitted by `cmd/budgetcurve` from a recorded run, never hand-placed,
and both read the same series so a point on the curve cannot contradict a cell in the table.
Numbers in the paper cite the result file they came from.

`make figure-budget-curve-redraw` redraws them from
`results/budget-curve-r11-d7-14.json` without re-measuring, which is what to use when changing
how the figure looks rather than what it shows; a test asserts the two paths produce identical
output. Follow it with `make paper`, and `make paper-check` fails the build if you forget.

**Never edit a result file by hand. Re-run instead.**

## Contributing

Commits are `type(scope): imperative`, ≤72 characters, no trailers. Every PR carries a
`CHANGELOG.md` entry. Never force-push; never squash without asking.

## Licence

Apache-2.0 — see [`LICENSE`](LICENSE). Corpus licences are separate, are not granted by it,
and are recorded in [`DATA.md`](DATA.md).
