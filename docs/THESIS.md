# Idiolect

### Calibrated Behavioural Anomaly Detection over Schema-Evolving Security Telemetry

**The canonical document for this project.** It states the problem, the argument for
attacking it this way, the method in full, and what the measurements established —
including, at length, the parts that failed.

> **On the name.** An *idiolect* is the speech pattern peculiar to one individual — not a
> dialect shared by a group, but the way one specific person uses language, down to the
> words they favour and the constructions they never reach for. It is the linguistic
> statement of this framework's governing commitment: the unit of analysis is the
> individual, and the question is never "is this unusual for the population" but "is this
> unusual *for this entity*" ([§7](#7-the-unit-of-analysis-and-why-it-is-the-individual)).
>
> The name is not a borrowed metaphor. Detector I models each entity's **vocabulary** and
> the central open question is whether that vocabulary is
> [open or closed](#91-detector-i--categorical-novelty); the Good–Turing estimator built
> for the open case comes from computational linguistics, where it was devised to estimate
> the probability of a word never seen before. **An account has an idiolect, and the
> framework reports when it stops speaking in its own.**

> **There is a rendered version of this document**, [`thesis.html`](thesis.html), generated
> from this file by `make thesis`. It carries the same prose plus eleven diagrams and a
> contents sidebar, and it is easier to read for anyone meeting the ideas for the first time.
> This file stays canonical: the page is never edited directly, so the two cannot disagree.

Other documents in this repository are subordinate to this one and have narrower jobs:
`README.md` orients a newcomer, `docs/IMPLEMENTATION.md` is generated and records where
the code deviates from the specification, `DATA.md` records the corpora and their
licences, and the open work lives in
[GitHub issues](https://github.com/JohnPierman/ethogram/issues).
Where any of them disagrees with this document, this document is wrong or they are stale;
either way it is a defect, not a matter of interpretation.

## How to read this

It is deliberately two documents in one, and they describe the same system.

| | For | Contains |
|---|---|---|
| **[Part I](#part-i--the-problem)** | everyone | the problem space, and why the standard approach fails on it |
| **[Part II](#part-ii--the-framework-without-mathematics)** | a CTO deciding whether to build, buy or continue | the method and the findings, no mathematics |
| **[Part III](#part-iii--the-statistical-treatment)** | anyone who has to verify, extend or referee it | every model stated in full, with its null, its state and its failure modes |
| **[Part IV](#part-iv--evaluation-and-results)** | both | how it was measured, and what came out |

A reader who wants only the answer should read Part II and then
[§15.1](#151-what-the-measurements-establish).

### A note on tone

This is a research programme with a **negative headline result**, and the document is
written to say so plainly rather than to bury it. Two of the five detector nulls were
measured to be wrong rather than merely loose. The combination layer, which was the
design's central bet, was measured to *destroy* the signal its best component carries.
A methodology document that quietly drops its failures is not one.

---

# Part I — the problem

## 1. The space

An organisation of any size emits a continuous stream of security telemetry:
authentication records, DNS queries, process launches, file accesses, network flows,
badge swipes. A large enterprise generates hundreds of millions of such events a day. A
security analyst can meaningfully examine a few hundred.

That ratio — eight orders of magnitude — is the entire problem. Everything else is
detail.

The events themselves are structured but not uniform. A single authentication record
carries a timestamp, some notion of who acted, some notion of what they acted upon, and a
handful of attributes whose meaning depends on the vendor, the product version, and
whatever the logging configuration happened to be that week. Formally, an event is a
timestamp, an entity identifier, and a set of field-value pairs:

```
e = (t, u, {(f, v) : f ∈ dom(e)})
```

`dom(e)` — the set of fields actually present on this event — varies from event to event
and grows over time as sources add fields. This is not a corner case. It is the ordinary
condition of the data.

### 1.1 What "anomaly detection" has to mean here

The naive framing — *find the unusual events* — is not merely hard, it is incoherent.
Almost every event is unusual relative to something. A useful system answers a much
narrower question:

> Of the several hundred million events today, **which two hundred should a human look
> at**, and **why**?

Three properties are implied by that phrasing and are easy to lose:

1. **A budget, not a threshold.** The output is sized to the analyst capacity that
   exists, not to a score cut-off whose alert volume nobody can predict in advance.
2. **A reason, not a number.** An analyst can act on "this account used a program it has
   never used"; nobody can act on `0.87`, and nobody can defend `0.87` to an auditor.
3. **Ranking is the product.** Whether an event is "truly anomalous" is unanswerable and
   uninteresting. Whether it deserves attention *ahead of* the other 199,999,999 is the
   whole question.

The second of those needs a qualification that [§4.3](#43-honest-arithmetic-about-how-much-is-too-much)
makes precise: a budget is a statement about the analyst, not about the alerts, and a
system tuned to a quota emits it on a quiet day too. The quantity to control is the
**error rate** — what share of what you are shown is wrong — and the base rate of
intrusions sets hard limits on how good that can be made.

### 1.2 The adversary that matters

The threat model that makes this hard is not malware and not a port scan. Both are
better addressed by signatures. The hard case is an intruder using **valid stolen
credentials to do ordinary things**: authenticating successfully, reading files they are
permitted to read, moving between machines they are permitted to reach.

Nothing about such an event is malformed. Every field is well-formed and every action is
authorised. If it is distinguishable at all, it is distinguishable only as a *departure
from a pattern* — and the pattern in question is the pattern of the specific account
being impersonated, not of the organisation.

That observation is the seed of everything in Part III.

## 2. Motivation — why the standard formulation fails

Call the **standard formulation** what almost every deployed system and almost every
published baseline does: reduce each event to a fixed-length numeric feature vector, fit
an unsupervised outlier model to a pool of such vectors, and rank by the model's score.
Isolation Forest, Local Outlier Factor, One-Class SVM, PCA reconstruction error and
autoencoders are all instances.

It fails on this problem in five distinct ways, and they compound.

### 2.1 The population norm punishes the atypical-but-consistent

A pooled model asks: *is this event unusual for the organisation?* The predictable result
is that the night-shift engineer, the backup service account, the offshore contractor and
the build server are all **permanently anomalous**. They are unusual and they are
supposed to be.

Two consequences follow, and the second is worse than the first. The alert queue fills
with the same handful of well-understood oddities, and the team learns to ignore the
tool. Meanwhile the actual intruder — using a compromised ordinary account to do ordinary
things — never rises above the noise floor at all, because relative to the *population*
they are unremarkable.

The system is loudest exactly where it is least informative.

### 2.2 Sensitivity is lowest precisely when an attack is broadest

Many deployed systems standardise scores against the batch or window the event arrived
in. This is convenient and it is a serious defect: it makes an event's score depend on
its own company.

If a campaign affects a proportion `p` of the current batch, the z-score a
batch-standardised detector assigns to a member of that campaign is

```
z = √((1 − p) / p)
```

which falls monotonically as `p` grows. A campaign touching 1% of a batch is
conspicuous; the same campaign touching 40% of it is nearly invisible, because it has
redefined the norm it is being measured against. **The broader the compromise, the
quieter the detector.**

This is not a hypothetical. It is measured in this repository as hypothesis **E8**, with
a deliberately-broken negative control reproducing the closed form to within 4e-15 while
falling from 6.000 to 0.134 as the campaign's share of the batch grows
(`results/e8-determinism.json`).

The design consequence is a hard requirement: **no score may depend on the composition of
the batch an event arrived in** (requirement R1).

### 2.3 The schema is not known in advance, and does not hold still

The standard formulation fixes its feature vector at design time. Real deployment does
not permit this. New log sources appear; vendors add columns; a product upgrade renames a
field; an integration begins emitting a value set nobody characterised.

Every fixed-schema system responds to this the same way: someone writes a mapping, and
the mapping rots. Worse, the failure is silent — a field that stops being populated
becomes a constant, and a constant is perfectly unsuspicious.

The requirement this implies is stronger than "handle new fields gracefully": **no
component may require advance knowledge of a field's type, cardinality, or value set**
(requirement R2). A field's kind must be *inferred from observed statistics*, and a
component that has not seen enough to infer anything must say so.

### 2.4 An uncalibrated score cannot be turned into a workload

Most models emit a score on an arbitrary, model-specific scale. Such a score supports a
ranking and nothing else. It does not answer:

- how many alerts will a threshold of 0.8 produce tomorrow?
- what error rate am I accepting?
- are these two detectors' scores comparable, and if I combine them, what does the result
  mean?

Without calibration, the operator's only available control is "move the number until the
volume looks right", which must be redone whenever traffic changes. What is wanted
instead is a **p-value under an explicitly stated null** — a quantity whose meaning does
not depend on the model that produced it, and which can therefore be thresholded and
combined at all.

Note that the goal is comparability, not a predictable queue length. An earlier requirement
asked that an operator-chosen error rate map to a predictable alert volume;
[§13](#13-the-design-requirements-and-where-each-is-enforced) records why it was withdrawn.

### 2.5 An alert without evidence cannot be actioned

A score is not a finding. If an analyst cannot reconstruct *why* an event was surfaced,
the alert generates work rather than reducing it, and the system cannot be audited at
all.

The requirement: **every verdict must carry evidence sufficient to reconstruct it by
hand** (requirement R5). Not a feature-importance gesture — the actual sufficient
statistics, such that the p-value can be recomputed from the alert card alone.

### 2.6 What the failures have in common

Four of the five are the same mistake wearing different clothes: **the model's frame of
reference is the population when it should be the individual.** The fifth — schema
rigidity — is what stops most attempts at fixing the other four from surviving contact
with real data.

That is the thesis this project tests.

## 3. What industry actually deploys, and why it is the right comparison

The evaluation compares against models in routine production use rather than against
straw men. Grouped by inductive bias, because the grouping is what makes a shared failure
informative:

| Family | Representative | Asks |
|---|---|---|
| Isolation | Isolation Forest, Extended IF, Half-Space Trees, Robust Random Cut Forest | how few random cuts isolate this point? |
| Density | Local Outlier Factor | is this point in a sparser neighbourhood than its neighbours are? |
| Boundary | One-Class SVM | is this point outside the region enclosing the bulk of the data? |
| Linear subspace | PCA reconstruction error | how far is this point from the subspace ordinary traffic occupies? |
| Per-entity moment | EWMA z-score against the entity's own history | is this unusual *for this account*? |

The first four families are all **population-scope**: they hold no per-entity state, and
so by construction cannot express the proposition "this entity has not previously done
this". If they all score zero on a corpus, that is weak evidence — four models sharing
one blind spot is one observation, not four.

The last row is therefore the decisive comparison, and it is the one this repository was
missing until recently. A naive per-entity EWMA baseline shares the framework's *framing*
and has none of its machinery: no calibrated p-value, no abstention, no combination, no
conformal step. It separates two very different claims —

1. *comparing an account to its own past beats comparing it to the population* — which is
   cheap, and which any competent team can build in a week;
2. *this framework's calibration, abstention and combination add value on top of that
   framing* — which is what the rest of this document is about.

Without that baseline, the second claim is untested. It is implemented in
`sidecar/baselines.py` as `entity_ewma`, held to the same matched-budget protocol as
everything else, and — because a baseline that cheats makes the comparison meaningless in
the framework's favour — held to the same score-before-observe discipline
([§8.1](#81-scoring-strictly-precedes-observation)) even though nothing obliges it to be.

[§15.7](#157-the-matched-head-to-head) is what it measured: the framing is worth
something, and this framework's machinery is worth considerably more on top of it — while
the framework's *default combined configuration* is worth less than the baseline.

---

# Part II — the framework without mathematics

For a reader deciding whether this is worth building, buying, or continuing. Part III
says the same things with the mathematics attached.

## 4. The one idea

**It compares each account to its own past, not to other accounts.**

The question is never "is this unusual for the company" but "is this unusual *for this
account*". The night-shift engineer working at 3am is not an alert; the day-shift
accountant doing it for the first time is. The backup service touching a thousand
machines is not an alert; the workstation that has always touched three, suddenly
touching thirty, is.

Everything else in the design follows from taking that seriously and refusing to cheat on
it.

<!-- figure: population-vs-entity -->

### 4.1 It works on data it has never seen

The system is never told what a field *is*. It watches for a while and works it out: this
one takes a handful of repeating values; that one looks like a unique identifier and
therefore carries no information; this other one is a number.

Until it has seen enough, it says **"I don't know"** — and a component with nothing useful
to say **declines to answer** rather than returning a confident middle value. That sounds
like a small thing. It is the difference between a system that degrades honestly and one
that quietly invents findings.

Onboarding a new log source is a configuration file, not a code change. This was
demonstrated rather than asserted: a second source with a different schema, different
fields and different meaning was ingested end to end — 6,004,252 rows — with **zero code
changes** (hypothesis E6).

### 4.2 Every alert explains itself

An alert carries the evidence that produced it, not just a score. Not "risk 87" but *"this
account has used four applications in six months and this is the fifth"*, with the counts
that support it.

This was verified rather than promised: **79.2% of a sample of 49,581 verdicts could be
recomputed by hand from the evidence card alone**, without querying the system
(hypothesis E7). The remaining 20.8% are a known and documented limitation — one
detector's card deliberately omits a complete value multiset that would be unbounded in
size.

### 4.3 Honest arithmetic about how much is too much

The goal is not a quota. **If nothing suspicious happened today, the right number of
alerts is zero; if a breach is in progress, the right number might be five thousand.** A
system tuned to emit two hundred a day emits two hundred on a quiet day, all of them
benign, and the analyst learns to discount the whole queue — which is the failure
[§2.1](#21-the-population-norm-punishes-the-atypical-but-consistent) describes arriving by
a different route.

So the control that matters is the **error rate**, and the volume follows from it:

> *"Of the alerts you show me, at most one in five may be wrong."*

That is a statement about the alerts, not about the analyst's diary, and it is what a
calibrated p-value buys. The mechanism is false-discovery-rate control
([§10.3](#103-error-rate-to-alert-volume)).

#### The thing that cannot be had, and the arithmetic that says so

"Everything suspicious and nothing else" is not attainable, and it is worth being precise
about why rather than treating it as an engineering shortfall to be closed later. The
constraint is the **base rate**, and it was stated for this exact problem by
[Axelsson in 1999](https://dl.acm.org/doi/10.1145/319709.319710): the factor limiting an
intrusion detector is not its ability to recognise an intrusion but its ability to
suppress false alarms, because intrusions are vanishingly rare against the volume of
ordinary activity. His canonical scenario — a million audit records a day, two intrusions
— gives a base rate near `2 × 10⁻⁵`, and he derives that a false alarm rate below
**1 in 100,000 per event** is needed before a majority of alerts are real.

**This corpus sits in the same regime**, which makes the arithmetic concrete rather than
illustrative. Over LANL days 7–13 the framework scored 42,218,530 events — about 6.03
million a day — of which 549 were labelled: a base rate of `1.30 × 10⁻⁵`, one in 76,901.

Taking the most generous possible case, a detector that misses nothing:

| If you want this share of alerts to be real | you need a false-alarm rate of | which is this many false alerts a day | total alerts a day |
|---|---|---|---|
| 90% | 1.4 × 10⁻⁶ | 9 | 87 |
| **50%** | **1.3 × 10⁻⁵** | **78** | **157** |
| 25% | 3.9 × 10⁻⁵ | 235 | 314 |
| 10% | 1.2 × 10⁻⁴ | 706 | 784 |
| 5% | 2.5 × 10⁻⁴ | 1,490 | 1,569 |

<!-- figure: base-rate -->

Three things follow, and the third is the useful one.

**A perfect detector still produces a queue.** Even at 90% precision — far beyond anything
published — this corpus yields 87 alerts a day. There is no threshold at which the queue
becomes empty and correct, because 78 genuinely suspicious events happen every day.

**The published state of the art is two to three orders of magnitude short.** Detectors in
the literature achieve false-alarm rates around `10⁻²` to `10⁻³`; the table asks for
`10⁻⁵`. At `10⁻³` this corpus would produce roughly **6,000 false alerts a day** against 78
real ones — one real alert in seventy-seven. That is the actual industry condition, and
the survey data matches it: organisations report on the order of
[hundreds to thousands of alerts a day with a majority false](https://www.stamus-networks.com/blog/what-the-2025-sans-detection-response-survey-reveals-false-positives-alert-fatigue-are-worsening),
and a large share never investigated at all.

**And the number this project has been using is, by accident, about right.** The
evaluation's 100 alerts per analyst-day was chosen as a plausible analyst capacity. The
base-rate arithmetic independently asks for ~157 to make half the queue real. Those agree
to within a factor of two — so the budget was defensible, but **for a reason nobody had
stated, and the stated reason was the wrong one.** A capacity figure is a fact about
staffing; the number that belongs in a specification is the one the error rate implies,
and it should move when the traffic or the base rate moves.

#### What this means for the dial

The budget survives, demoted. It is a **capacity valve**, not the control: a limit that
protects the analyst when the error rate the operator chose would produce more work than
exists to do it. And when the valve binds, that is itself the finding — *"at your chosen
error rate, today generated more than you can look at"* is information, where silently
truncating to two hundred is not.

**This is not yet how the system behaves, and that is a defect rather than a nuance.** The
evaluation in Part IV is reported almost entirely at fixed budgets, because the composite's
FDR control currently saturates — E3 reports a realised false discovery rate of 1.0 at
every nominal `q` ([§15.8](#158-the-supporting-hypotheses)), meaning the procedure rejects
essentially everything and the error-rate dial has no purchase. Budgets were the only
thing left that produced comparable numbers across days.

So the ordering is: **the error rate is the right control, the composite is what stops it
working, and that is one more reason the combination is the thing to fix**
([§17](#17-what-follows)).

### 4.4 Nothing is claimed that was not measured

No number appears in any report, dashboard or table in this project unless it came out of
a recorded run. This is structural rather than a matter of care: the report renderer reads
only result files emitted by actual runs, every result carries its git commit, corpus
checksum, row counts, seeds and timings, and a hypothesis with no result renders as a
literal **NOT RUN** card — never blank, never zero, never quietly omitted. Continuous
integration fails the build if any published figure lacks a backing run.

## 5. What was found, including what did not work

### 5.1 What worked

- **The core premise holds.** The per-account measures found the known intrusion; the
  compare-everyone-to-everyone measures found nothing. Not "found less" — nothing.
- **The finding replicates on accounts never previously scored.** Re-run against 984
  background accounts sharing nothing with the original set, the same ordering appears:
  the per-account novelty measure is the only one that discriminates, and the combined
  system still finds nothing while that one component finds 21.
- **Two components were badly miscalibrated and are now fixed**, one of them by a factor
  of more than a hundred.
- **The system is reproducible to the byte.** Identical inputs produce identical outputs
  across batch sizes and repeats, verified with a deliberately-broken control required to
  fail the same check — because a check that cannot fail is not evidence.

### 5.2 What did not work

**We built five detectors and combined their opinions, on the reasonable assumption that
more evidence is better. It is not.**

One detector carried nearly all the signal, four carried almost none, and averaging them
together destroyed it. On a matched comparison — same events, same accounts, same labelled
intrusion — at a budget of 100 alerts per analyst-day, of 262 intrusion events:

| | found |
|---|---|
| our single best component alone | **21** |
| a twenty-line industry-standard baseline that compares each account to its own past | 1 |
| six standard industry models that compare accounts to the population | **0** |
| **our combined system — what the framework ships by default** | **0** |

Read the last two rows together. **The combined system does not merely fail to improve on
its best component; it is beaten by the crudest possible implementation of its own central
idea.** This is not a tuning failure. It is a structural property of averaging an
informative signal with uninformative ones, and no cleverer combination rule recovers it.

The first two rows are the good news, and they are worth as much. The gap between 21 and 1
is what the calibration machinery is worth on top of the idea of comparing an account to
itself — and the gap between 1 and 0 is what that idea is worth on top of what the
industry ordinarily deploys.

**One detector contradicted the method's own governing principle.** It compares accounts
to the wider population — precisely what the design elsewhere argues against — and
contributes nothing to detection. It is being replaced with a per-account version that
preserves the one signal it uniquely covers.

### 5.3 The honest caveat

On the public dataset used here, **no method tested — ours or seven published baselines —
surfaces the labelled intrusion at a realistic alert budget when scoring individual log
lines.**

Some of that is our combination destroying signal, and that is fixable. Some of it may be
that a single authentication event, made with valid stolen credentials, simply does not
contain enough information to be distinguishable, and no amount of engineering recovers
what is not there.

The promising direction, which the evidence supports and which is in progress, is to
score **accounts over a day** rather than individual events. An account accumulating
thirty unusual actions is a far stronger signal than any one of them, and it matches how
an analyst actually works. It is the only construction here that has produced a
non-trivial detection count.

But when the arithmetic is corrected for the fact that busier accounts accumulate more
evidence simply by being busier, most of that count goes away — from 25 of 46 down to 2.
**The direction survives the correction; the specific numbers do not**, and are not
presented as a result ([§15.5](#155-ranking-accounts-rather-than-events)).

### 5.4 What this means commercially

The value is in the **per-account calibration and the explainability**, not in the
ensemble. The next step is to stop combining and present each signal on its own terms —
which is also what an analyst wants, since "this account used a program it has never used"
is a triage instruction and a blended score is not.

---

# Part III — the statistical treatment

The method as built, and as corrected by evidence. Where the two differ, both are stated
and the correction is marked.

## 6. The object being modelled

Telemetry arrives as a stream of events, each a timestamp, an entity identifier, and a set
of field-value pairs, with `dom(e)` the fields present on this event
([§1](#1-the-space)). Nothing in the method may depend on knowing `dom(e)` in advance, on
a field's type, or on its value set — requirement **R2**, and what makes the method
deployable against a corpus nobody has characterised.

Field kinds are therefore **inferred at runtime** from observed statistics rather than
declared:

| Kind | Inference rule |
|---|---|
| identifier | distinct-value ratio `|values| / |observations| ≥ 0.95` over at least 200 observations |
| boolean | takes exactly two recognised tokens |
| numeric | at least 99% of values parse as numbers |
| categorical | otherwise |
| **unknown** | fewer than 50 observations — every detector abstains rather than guessing |

Identifiers are excluded from scoring: a field whose values are nearly all distinct
carries no information about recurrence, and admitting one would make every event novel.

## 7. The unit of analysis, and why it is the individual

Every null hypothesis is conditioned on the entity's own persisted history `H_u`:

```
H₀ : the observed value is drawn from the entity's own predictive, given H_u
```

This is not a stylistic preference. It is the difference between a detector that fires on
every unusual-but-consistent account and one that fires on *change*. An account that
habitually behaves unlike its peers is not thereby suspicious; the same account behaving
unlike **itself** is.

The measurements in [§15](#15-results) show it is also the only part of the method that
carried signal.

**Which field designates the entity is configuration, not code.** The framework does not
know that `src_user` means a user.

## 8. The method

### 8.1 Scoring strictly precedes observation

State must be updated **after** scoring, or a first-ever value is already a known value by
the time it is scored, and novelty detection dies while still emitting plausible numbers.
The failure is silent, which is what makes it dangerous.

It is enforced by **capability separation rather than convention**: `Score` holds no means
of writing state, and returns the update it computed as an `Observation` whose `Commit`
holds the only write capability. A detector therefore cannot update history while
scoring, and the framework cannot update before scoring, **because the value carrying the
update does not exist until `Score` has produced it.**

<!-- figure: score-before-observe -->

### 8.2 Abstention is not a score

Each detector emits a p-value under an explicitly stated null, **or abstains**. A verdict
has four states — `evaluated`, and three flavours of abstention — and only `evaluated`
carries a p-value. A p-value is unreachable unless the status is `evaluated`, so an
abstained verdict has no representable score.

A "0.5 because we do not know" is forbidden (**R3**): it asserts normality on no evidence.
Abstentions reduce the degrees of freedom of the combination rather than contributing a
value.

## 9. The detectors

Four numbered detectors, five scoring components — Detector II has a timing half and a
volume half which share state, which is why their combination needs a dependence
correction.

### 9.1 Detector I — categorical novelty

**The question.** Has this entity taken this value for this field before, and how
surprising is it if not?

**In plain terms.** An account has used four applications in six months. Today it uses a
fifth. That is the whole idea — but "it's new" is not yet a number, and turning it into one
needs three things the naive version gets wrong.

*How new is new?* An account that has used four applications and now uses a fifth is
surprising. An account that uses a different application every single day and now uses
another is not. Both are "a value never seen before"; only the first is evidence.

*What about the first day?* An account with no history at all has never done anything
before, so everything it does is novel. Treating that as suspicious would flag every new
joiner on their first morning, so a first-ever value on an account with no history is
deliberately not an alert.

*What about six months ago?* An application used heavily last year and not since is not
really part of what this account does now. So counts fade with age rather than
accumulating forever, which also keeps the state per account bounded.

The mechanism below is one estimator that handles all three: it holds decayed counts of
what the account has used, keeps a slice of probability in reserve for everything it has
not, and reports how much of the total probability sits at or below what actually
happened.

<!-- figure: novelty-tail -->

For entity `u` and field `f`, with decayed counts `n_v` over observed values, total
`n = Σ n_v` and `K` distinct values observed, the predictive is a symmetric
Dirichlet–multinomial with concentration `α`:

```
P̂(v | H_u) = (n_v + α) / (n + α(K+1))        v observed
P̂(new)     = α / (n + α(K+1))                v unseen
```

and the p-value is the tail mass at or below the observed probability:

```
p = Σ_{w : P̂(w) ≤ P̂(v)} P̂(w)
```

**State and decay.** Counts decay by `2^(−Δt / T½)`, applied lazily on read from each
row's own last-seen timestamp — so no sweep job exists and **no wall clock enters
scoring**.

**A known limitation, and the correction built for it.** The reserved unseen mass
`α / (n + α(K+1))` depends only on the counts, never on the *shape* of the distribution.
For a closed vocabulary this is harmless. For an open one — addresses, hostnames, user
agents — it is wrong by orders of magnitude:

| History | True P(new) | Dirichlet, α = 1 |
|---|---|---|
| 3 values, 1000 observations each | ≈ 0 | 0.00033 |
| 500 values, one observation each | ≈ 1 | 0.001 |

Two situations differing by orders of magnitude receive nearly the same answer, and the
second is what a compromised account looks like.

<!-- figure: open-closed-vocabulary -->

The **Good–Turing** estimator gives `P̂(new) ≈ N₁ / n`, where `N₁` is the number of values
seen exactly once. It reads the shape directly, adapts to open and closed vocabularies
without being told which it faces, needs only the count-of-counts, and names no field.
The Dirichlet form is retained as the fallback where the count-of-counts is too thin, and
observed values are renormalised to the mass Good–Turing leaves, so the categories still
carry unit mass exactly. On a synthetic contrast it separates an open from a closed
vocabulary by a ratio of **2,970** where equation (4) manages **3**.

**Measured, and on this corpus it does not help.** The median labelled event moves from
novelty's 0.07th percentile to its 0.18th — a small *loss* of discrimination — while
detection stays at zero. The estimator engages as designed (116 of 240 histogram bins
move, pulling false extremes toward the middle), so this is not a wiring failure.

The reason is instructive and cuts the way that costs alerts. LANL authentication has
fairly closed per-entity vocabularies; the attack signal *is* "a value never seen
before"; and many ordinary accounts carry singletons — so Good–Turing correctly raises
`P(new)` for them and dampens exactly the signal the attack produces. **It is being honest,
and on a closed vocabulary the honesty costs discrimination.**

Which estimator is right therefore depends on the corpus, and neither is universally
correct. The Good–Turing path is **off by default**, and **remains untested on a genuinely
open-vocabulary corpus, which is the case it exists for.** That is recorded as a
hypothesis, not a result.

### 9.2 Detector II(a) — circular timing

**The question.** Is this time of day unusual for this entity?

**In plain terms.** A workstation that has only ever been used between nine and five is
used at three in the morning. Nothing about that event is malformed; the only thing wrong
with it is *when* it happened, relative to when this account normally acts.

Two things make this harder than bucketing the day into hours.

*Midnight is not a boundary.* Time of day is a loop, not a line. An account that works
between 23:00 and 01:00 has one habit, but any representation that cuts the day at
midnight files that habit in two places at opposite ends — and then judges each half on
half the evidence. The fix is to model the day as a circle, where 23:00 and 01:00 are
neighbours because they are.

*The shape has to be learned, not assumed.* Some accounts have one working window, some
have two, some run continuously. So rather than fitting a single "usual hour", the detector
estimates the account's whole activity curve around the clock — and it does so in a fixed
amount of memory per account, a couple of dozen numbers, no matter how many events that
account has ever produced. That last property is what makes per-account modelling
affordable at hundreds of millions of events a day.

<!-- figure: circular-vs-grid -->

Time of day is a point on a circle, so **midnight is not a boundary**. The entity's
activity density is estimated by a von Mises kernel of concentration `κ`, held as decayed
Fourier moments `(C_h, S_h)` of order `h ≤ H` and total weight `W`:

```
f̂(φ) = 1/2π · [ 1 + 2 Σ_{h=1..H} r_h (C_h/W · cos hφ + S_h/W · sin hφ) ]
```

with `r_h = I_h(κ)/I₀(κ)` the kernel's Fourier coefficients, `I_h` the modified Bessel
function of the first kind. The p-value is the mass of the density at or below the density
at the observed time.

**The state is fixed size** — `2H+1` numbers per entity — regardless of how many events
that entity has ever produced. This is the property that makes per-entity modelling
affordable at hundreds of millions of events a day.

**The control.** A 168-cell weekly-grid alternative is implemented and exhibits the defect
the circular form avoids: an entity active either side of midnight is split across
non-adjacent bins. Measured as a recorded run: an entity active only between 23:00 and
01:00 scores `P(23:30) = 0.77` and `P(00:30) = 0.77` against `P(12:00) = 0.00098`, with
exactly one fitted mode, at 23.86h, inside the window. The grid arm shows the defect on
both sides.

**And this detector nevertheless finds nothing, for a reason worth stating precisely.** It
scores 0 labelled events on every run in this document. The model is correct and the
implementation is exercised — the control above proves both — so the failure is in the
premise, and it is measurable: **real accounts do not have concentrated working hours.**

Across the eight accounts a planted off-hours attack was run against, the account's *modal*
hour holds between 7% and 26% of its own history, and its busiest six hours between 31% and
99%. A typical account is active across most of the day. Fit a von Mises density to that and
the result is close to uniform, and under a near-uniform circular null **no hour is
surprising** — including one holding none of the account's history at all. Three of the eight
victims had literally zero prior events in the hour attacked, and the detector still scored
those events at a median *p* of **0.032**.

That figure deserves one caution, because it moved by a factor of ten between two injected
runs — 0.32 on the first, 0.032 on the second — purely because a different eight accounts were
selected as victims. The detector's response to an off-hours event is dominated by how
concentrated that particular account's habit is, so any single number here is a property of the
sample as much as of the detector. What does not move is the consequence: **0.032 is nowhere
near alert-worthy.** A budget of 700 alerts against 4.49 million events selects roughly the top
1.6 × 10⁻⁴ of the ranking, and this detector alerted on **0 of 64 planted off-hours events at
every budget from 10 to 100 a day.**

The census agrees from the other direction: `off_hours` fires on **37.6% of all background
traffic** and carries a lift of ~1× ([§15.6](#156-which-kinds-of-anomaly-the-attacks-actually-are)).
A property a third of ordinary traffic has is not evidence.

So the honest verdict on Detector II(a) is not that it is broken but that **"unusual hour" is
not a discriminating question on this corpus**, however well it is asked. The opening example
— the nine-to-five workstation used at three in the morning — is a real pattern, and this
detector would catch it. It is not what the accounts in this corpus look like.

### 9.3 Detector II(b) — volume

**The question.** Did this entity produce more events in this window than its own history
predicts?

**In plain terms.** A workstation that has always touched three machines suddenly touches
thirty. The backup service that touches a thousand every night is not an alert; the
workstation is, and the difference is entirely in whose history you compare against.

The trap here is subtler than in the other detectors, and this project fell into it. It is
not enough to know an account's *average* rate — you also have to know how much that rate
normally swings. Real telemetry arrives in bursts: someone opens a laptop, forty events
follow in a minute, then nothing for three hours. An account whose daily volume has always
ranged between 60 and 480 events is not behaving unusually at 240.

A model that tracks only the average becomes *more* confident as it sees more history, and
so ends up rejecting accounts for their own habitual variation — which is precisely what
happened, at odds of 1 in 10⁷⁹. The repair is to measure how much the account actually
swings and widen the expectation to match, in a direction that can only ever make the
detector more forgiving, never less.

<!-- figure: volume-null -->

The entity's event rate `μ` carries a Gamma posterior updated per period with power
discounting, `a ← δa + k`, `b ← δb + 1`. For a window `Ω` with expected activity fraction
`ρ(Ω) = ∫_Ω f̂` — **integrated from the timing density**, which is why the two halves of
Detector II share state — the count is negative binomial:

```
Pr(K = k) = Γ(k+a)/(k! Γ(a)) · (b/(b+ρ))^a · (ρ/(b+ρ))^k
```

**Corrected by measurement, and this was a real defect.** The specification's
overdispersion `Var/E = (b+ρ)/b` expresses uncertainty about `μ`, and that uncertainty
*shrinks* as history accumulates: at `T½ = 7` days the discounted period count settles at
`b ≈ 10.6`, so `Var/E ≤ 1.09` and **the null is Poisson in all but name**.

Real telemetry is not Poisson — events arrive in sessions. An entity whose daily volume
had always swung between 60 and 480 events scored **P = 1.4e−79** for doing 240 again.
The detector was rejecting entities for their own habitual behaviour.

The null is now widened by the entity's **own measured dispersion** — a discounted Pearson
statistic over its completed windows,

```
φ̂ = (1/n) Σ_w (k_w − m_w)² / m_w
```

floored at 1 so the correction **can only widen a null and never sharpen one** — and the
predictive becomes NB with mean `m` and variance `φ̂m`, i.e. `r = m/(φ̂−1)`.

**Measured effect: events below `p = 1e−12` fell from 22.1% to 0.2%**, a factor of 130.

**An operational trap worth recording.** The first version of this fix was statistically
correct and unusable. Summing the NB tail directly needs of the order of 40,000 iterations
per event once dispersion is large, and replay throughput fell from ~200,000 rows a minute
to ~600. It is now evaluated in closed form as `Pr(K ≥ k) = I_q(k, r)` with
`q = m/(r+m)` — a regularised incomplete beta, bounded at 300 iterations whatever `r` is.
A test pins the closed form against the summation wherever the summation still converges,
so the speed was not bought by changing the answer.

### 9.4 Detector III — population co-occurrence (demoted)

**In plain terms.** Every detector so far looks at one field at a time. This one looks at
*combinations*, and it exists because of a case the others cannot express at all.

An account authenticates with Kerberos, which it does constantly. It authenticates to host
C700, which it does constantly. But it has never used Kerberos *on C700*. Each value is
individually unremarkable, so every single-field detector is satisfied, and the event is
invisible to all of them. Only a detector with combinations in its vocabulary can see it.

On this corpus that case is not marginal: **74% of labelled attack events are pairings the
account had never made before.**

The difficulty is deciding *whose* history the combination is judged against, and the
original design chose wrong. Asking whether the *organisation* has seen this pairing
punishes consistency: an account that has always used one authentication type and never
another is, against a population model, wildly improbable on every event it produces. So the
detector fires constantly on the most predictable accounts — the exact failure §2.1
describes. Asking whether *this account* has made this pairing before needs no new
mathematics, because a pairing is just a value of a composite field, and Detector I already
knows how to score a value.

<!-- figure: pairing-scope -->

**The question as specified.** Do these two field values co-occur as often as the
population's block structure predicts?

Values from distinct fields form a multipartite graph with decayed co-occurrence weights.
Under a degree-corrected block model the expected weight between nodes `i` and `j` is

```
λ_ij = k_i k_j m_rs / (D_r D_s)        r = z(i), s = z(j)
```

degenerating to the configuration model `λ = k_i k_j / 2m` with one block. The p-value is
the **lower** tail `Pr(Poisson(λ) ≤ w)` — small when a pairing that ought to have been
observed was not. The partition `z` is discovered offline by Leiden, frozen at the burn-in
boundary, and persisted with its seed and the checksum of its input graph; the scoring
path never runs it, which is what preserves determinism.

**Two corrections and a demotion.**

*Correction 1 — the expression mixed two graphs.* The partition is frozen at the burn-in
boundary while degrees are read from the live graph, so `λ` carried the ratio of the two
graphs' scales and inflated without bound as the live graph outgrew the snapshot. It is
now factored into a live configuration term times a scale-free block affinity,
`λ = (k_i k_j / 2m_live) · ω_rs` with `ω_rs = m_rs · 2m_snapshot / (D_r D_s)` — an
algebraic identity when the graphs coincide. The partitioned arm's saturation fell from
**99.0% to 43.2%**.

*Correction 2 — the tail underflowed.* `λ` runs to thousands here, and the linear tail
floored at 5e−324, tying every extreme event to every other. Evaluated in log space.

*Demotion.* With the underflow repaired the true magnitude became visible: **ln P reaching
−39,278**. That is not a loose null but a meaningless one. More importantly it
**contradicts [§7](#7-the-unit-of-analysis-and-why-it-is-the-individual)**: it tests
departure from the *population* norm, which the method elsewhere explicitly disavows.
That is an inconsistency in the specification, not only in the code.

**Demoted rather than deleted**, because 29 labelled events are novel pairings and it is
the only detector that looks at relationships *between* fields — a real signal type
nothing else covers. The replacement asks the per-entity question: *is this pairing novel
for this entity?* Since a pairing is addressed as a **value of a synthetic composite
field**, Detector I's estimator scores it unchanged, and nothing downstream learns that
pairs exist. The population form is retained behind a flag so the block model can still
be given a fair test at full population.

### 9.5 Detector IV — population marginal

**The question.** Is this value rare in the population as a whole?

**In plain terms.** This is the only detector that deliberately asks the question the rest
of the framework argues against — *is this unusual for the organisation* — and it is here to
be a **control**, not a contributor.

The reasoning: this is what conventional tooling does well, so if the framework's advantage
is real it should show up everywhere *except* here. A result that beat the baselines on this
detector's own ground would suggest the comparison was measuring something other than what
it claims. Keeping a deliberately conventional detector in the composition makes that
checkable rather than assumed.

It earned its place. Not one labelled attack event in the full-population run is a
population-rare value — which is exactly what the framework's premise predicts, and which
explains why six independent industry baselines detect none of them.

<!-- figure: abstention -->

A non-parametric marginal over the population for each field: a t-digest quantile sketch
for numeric fields, decayed counts for categorical ones, abstaining below a minimum
observation count (1000) and above a cardinality ceiling (1000).

Population-scope by construction. It is retained deliberately — not because it is expected
to find intrusions, but because it is **the control**. It answers the category that
isolation-based detectors answer well, so any advantage the framework shows elsewhere can
be read against a category where it should show none.

**A recorded deviation.** The quantile sketch never decays. This was decided rather than
implemented, because implementing it could not change a single p-value: a centroid carries
no timestamp, so the only decay a t-digest admits without rebuilding is a uniform scaling
of every centroid weight — and a quantile is a *ratio* of weights, so uniform scaling
leaves every quantile exactly where it was. The sole observable effect would be to trip
the abstention gate more often while presenting itself as recency. The consequence is
recorded: on a corpus with numeric fields, Detector IV's two halves answer on different
timescales.

## 10. Calibration

### 10.1 Conformal calibration

A p-value is only calibrated if its null is true, and two of these nulls were measured to
be false. The split-conformal p-value

```
p_conf = (1 + #{i : p_i ≤ p}) / (n + 1)
```

over the `n` scores the same detector produced during burn-in is **super-uniform under
exchangeability whatever the model does** — the guarantee comes from ranking, not from the
null holding. It is accumulated during burn-in only and frozen at the boundary, on the
rule that *a quantity used to score an event must not have been fitted on that event*.

**Its limitation must be designed around rather than discovered.** `p_conf` cannot fall
below `1/(n+1)`, so every event more extreme than the entire burn-in set ties at that
floor. Thresholding is the conformal value's job; ordering within a tie is not.
Calibration and ranking are different jobs and must not be served by one number — so the
threshold is taken on the conformal value and **ties are broken on the underlying model
log p-value**.

The test that matters asserts the property against a *deliberately broken* model — one
reporting p-values to `e^−600` for ordinary data — and requires the calibrated output to
stay super-uniform anyway.

### 10.2 Combination, and the finding that overturned it

Fisher's method combines `J` evaluated verdicts:

```
X² = −2 Σ ln P_i  ~  χ²(2J)
```

with Brown's correction for dependence,

```
E[X²] = 2J,  Var[X²] = 4J + 2 Σ_{i<j} cov(−2 ln P_i, −2 ln P_j)
c = Var/2E,  f = 2E²/Var,  p = Pr(χ²(f) ≥ X²/c)
```

**This is the part the evidence overturned.** Where the labelled events sit in each
detector's own distribution over 1,451,839 scored events:

| Detector | median labelled event | most extreme |
|---|---|---|
| **novelty** | **0.07%** | 0.0003% |
| marginal | 3.79% | 3.60% |
| cooccurrence | 18.39% | 18.39% |
| timing | 27.24% | 0.037% |
| volume | 35.82% | 0.23% |

The median labelled event is more novel than **99.93%** of all scored events, and the most
novel one ranks about **fourth out of 1.45 million**. Equation (4) works.

What each combination surfaces at 100 alerts per analyst-day, of 262 labelled events:

| Ranked by | detections |
|---|---|
| novelty alone | **57** |
| Šidák-corrected minimum over five calibrated detectors | 20 |
| Fisher over five | **0** |

<!-- figure: combination-destroys -->

**The combination is not failing to help. It is converting a detector that finds 57
labelled events into a composite that finds none.**

Fisher is a *sum*: it asks whether the evidence is jointly unusual, which is the right
question only when every detector is informative. With one informative detector and four
near-random ones, the sum averages the information away. The minimum asks whether *any*
detector found the entity out of character — the question the method actually poses — and
recovers much of it, but still pays a multiplicity cost of order `J`.

**No cleverer combiner closes that gap.** With `J = 5` the Šidák minimum is already close
to optimal for sparse signal; order-statistic methods such as higher criticism only repay
their complexity with many tests. The remedy is not a better combination but **not
combining uninformative detectors** — which, because detectors are field-agnostic, is a
choice that names no field and so respects R2.

**A second, subtler finding.** Brown's correction as configured *destroys* the statistic
rather than correcting it: the whole alert list collapsed into a span of 0.18 log-units.
The cause is measured. Brown assumes each `P_i` is uniform under its null, fixing
`Var(−2 ln P_i) = 4`; volume's p-values reached `e^−1000`, so its variance ran to
millions. The direct covariance was therefore **measuring the misspecification of the
marginals, not the dependence between the detectors** — 8044.1 against the 0.139 that
Kost–McDermott implies from the same correlation. The correlations themselves, 0.03 to
0.15, are the honest statement of how dependent these detectors are, and they are modest.

### 10.3 Error rate to alert volume

Two decision mechanisms, deliberately not blended — and they are not peers. One is the
control; the other is a safety limit.

**The control is the false discovery rate.** Benjamini–Hochberg at nominal `q`, with
Benjamini–Yekutieli under arbitrary dependence. It is an *absolute* decision carrying an
error-rate guarantee: at most a proportion `q` of what is emitted is expected to be a
false discovery. This is the only one of the two that says anything about the alerts.

It is no longer what any requirement asks for. R6 formerly demanded that an error rate map
to a predictable volume, and this mechanism was its enforcement;
[§13](#13-the-design-requirements-and-where-each-is-enforced) records the withdrawal.

**The limit is the alert budget.** The day's `K` most extreme events: a *relative*
decision, the threshold floating with the day's traffic. It guarantees a workload and
guarantees nothing about correctness, and taken as the primary control it has a specific
pathology — it emits `K` alerts on a day when nothing happened, all of them benign.

#### What the base rate permits

The error rate cannot be set to taste, because the base rate bounds what any threshold can
achieve. With base rate `p`, a detector missing nothing, and a per-event false-alarm rate
`α`, the share of alerts that are real is

```
P(intrusion | alarm) = p / (p + (1 − p)·α)
```

which inverts to `α = p(1 − π)/(π(1 − p))` for a desired precision `π`. On LANL days 7–13,
`p = 549 / 42,218,530 = 1.30 × 10⁻⁵`, so a half-real queue demands `α ≈ 1.3 × 10⁻⁵` — about
78 false alerts a day against 78 real ones. Axelsson's 1999 analysis derives the same order
of magnitude from a structurally identical scenario and concludes that suppressing false
alarms, not recognising intrusions, is the binding constraint. Published detectors operate
two to three orders of magnitude above what the arithmetic requires.

[§4.3](#43-honest-arithmetic-about-how-much-is-too-much) works the consequences through
without the algebra.

#### Why the evaluation nonetheless reports budgets

Because the FDR path does not currently work. **E3 reports a realised FDR of 1.0 at every
nominal `q`, with every day saturated**: the composite is anti-conservative enough that BH
rejects essentially everything, so `q` has no purchase on the output and the guarantee is
vacuous. Under those conditions a budget is the only construction that yields numbers
comparable across days, which is why Part IV is reported at budgets throughout.

That is a defect in the composite, not a property of the corpus, and it is downstream of
the same finding [§10.2](#102-combination-and-the-finding-that-overturned-it) reaches: a
combination dominated by miscalibrated marginals produces a statistic no threshold can be
attached to.

**The former R6 has been withdrawn rather than repaired**, and this measurement is part of
why. Fixing the combination would restore the error rate as a usable dial, but a usable
dial was never the binding problem: at a base rate of 1.30 × 10⁻⁵ the arithmetic above
already shows that no dial setting yields a queue worth reading unless precision improves
first. A requirement that a dial work is satisfiable while every alert on it is wrong,
which is the wrong thing to hold the design to.

## 11. Determinism, and why it needs structural enforcement

Identical event and state must yield identical output (**R4**). In Go this requires
closing five specific leaks, each of which is closed structurally rather than by
convention:

1. **Map iteration order is randomised.** The event's value map is unexported and never
   returned; the only enumeration is a sorted iterator. Nondeterministic traversal is not
   expressible.
2. **Float accumulation order is part of the answer.** Summation order is fixed by sorted
   field path before every reduction.
3. **Goroutine scheduling.** Concurrency is permitted; nondeterministic *reduction* is
   not. Results are re-sorted into a canonical total order before combination, and a test
   asserts the parallel reduction reproduces the sequential one byte for byte.
4. **The wall clock.** Decay uses the event timestamp and the state row's own
   last-observed timestamp. An AST-level architecture test rejects `time.Now` in the
   domain layer.
5. **Database row order is undefined without `ORDER BY`.** Every query feeding scoring or
   reporting carries an explicit total order, and the database is initialised `--locale=C`
   so ordering does not depend on the host.

## 12. Numerical representation

Three separate defects of one kind were found and fixed, and the pattern is worth stating
as methodology rather than as implementation detail:

> **A p-value that underflows is a tie, and a tie is an unranked alert budget.**

- The combined tail underflows past `X² ≈ 1450`; ranking and thresholding moved to `ln p`.
- The negative binomial tail underflows in the deep tail; evaluated by regularised
  incomplete beta.
- Detector III's Poisson tail reaches `ln P ≈ −4000`; the verdict itself now carries
  `ln p`, with the Šidák correction, Fisher's statistic and Brown's correction all in log
  space.

The third is the instructive one. **This codebase had already fixed the same defect
twice** — the combination got a log form, volume got a closed-form tail — but never at the
*verdict boundary*, so every detector's extreme values were destroyed before reaching the
combination. No later stage could undo it: conformal calibration maps ties to ties, and a
minimum over tied values is a tie. Before the repair, 400 alerts held **one** distinct
score; after it, 400.

Every stage that consumes a p-value must therefore consume its logarithm, or the
information is destroyed before it arrives.

## 13. The design requirements, and where each is enforced

These are testable properties, not aspirations. Each has a test that fails loudly.

| | Requirement | Enforced by |
|---|---|---|
| **R1** | No score may depend on the composition of the batch the event arrived in | `Score` reads only persisted state; **E8** asserts byte-identical scores across batch compositions, with a negative control proving the check can fail |
| **R2** | No component may require advance knowledge of a field's type, cardinality, or value set | Corpus readers emit generic field paths; the registry infers kinds; the novelty estimator reserves mass for the unseen |
| **R3** | A detector without inputs must decline rather than assert normality | A p-value is unreachable unless the status is `evaluated`; an abstained verdict has no representable score |
| **R4** | Identical event and state must yield identical output, reproducibly | No wall clock and no randomness in the scoring path, enforced by an AST-level architecture test; canonical total ordering before every float reduction |
| **R5** | Every verdict must carry evidence sufficient to reconstruct it by hand | Sufficient statistics are the evidence; **E7** renders a verdict card from evidence alone with no query back to the store |
| **R6** | An alerted entity must be more likely than not to be genuinely anomalous. Alert volume is explicitly not bounded | **Nothing yet — measured, not asserted, and currently unmet.** Precision is reported at every budget in Part IV; on LANL days 7–13 it is 0 for every arm, so R6 fails loudly by being reported as failed rather than by a test. See the note below |

#### R6 replaced a withdrawn requirement, and is not of the same kind as R1–R5

The earlier R6 read *an operator-chosen error rate must map to a predictable alert volume*,
enforced by conformal calibration and FDR control. It is withdrawn. Three reasons, in
increasing order of weight:

1. **It was never met.** E3 reports a realised FDR of 1.0 at every nominal `q`, with every
   day saturated, so the dial had no purchase on the output and the guarantee was vacuous
   ([§10.3](#103-error-rate-to-alert-volume)).
2. **Its enforcement mechanism was measured to be the wrong instrument**, not merely a
   broken one: a combination dominated by miscalibrated marginals produces a statistic no
   threshold can be attached to, and that finding is
   [§10.2](#102-combination-and-the-finding-that-overturned-it)'s, not a defect local to
   the FDR layer.
3. **Holding it pointed the work at the wrong target.** A bounded queue is a statement
   about the analyst's workload; it says nothing about whether the queue is worth reading.
   At a base rate of 1.30 × 10⁻⁵ the arithmetic of
   [§10.3](#103-error-rate-to-alert-volume) shows precision is the binding
   constraint, and a requirement satisfiable while every alert is wrong competes for effort
   with the constraint that actually binds.

R6 as restated is deliberately **not** a structural property, and it is the only requirement
here that is not. R1–R5 are enforced by tests that fail on a bad build; R6 can only be
*measured*, against labelled data, per corpus. That is a weaker guarantee and is stated as
one. What R6 does provide is a direction: a change that raises precision at unbounded volume
is an improvement even if it lengthens the queue, and a change that shortens the queue
without raising precision is not an improvement at all.

**The entity, not the event, is R6's unit, and the pipeline's primary unit is still the
event.** The per-hypothesis scoreboard scores individual events, so the precision it reports
is per-event and is the wrong denominator for R6 as written.

The entity unit is not unmeasured, however, and R6 should be read against what
[§15.5](#155-ranking-accounts-rather-than-events) already found rather than as virgin ground.
Ranking entity-days instead of events moves detection off zero. Read as precision — which is
what R6 now asks for — the recorded runs at 100 alerts a day give:

| Entity-day ranking | Recall | Precision | Result file |
|---|---|---|---|
| corrected minimum | 0/46 | 0% | `lanl-entity-d7-9-001` |
| Fisher over the day | 15/46 | 7.5% | `lanl-entity-d7-9-001` |
| Fisher, conformal | **25/46** | **12.5%** | `lanl-conformal-d7-9-001` |
| standardised | 6/46 | 3.0% | `lanl-entity-std-d7-9-001` |
| standardised, conformal | 2/46 | 1.0% | `lanl-conformal-std-d7-9-001` |

Two things follow, and the second matters more. **R6 is missed by roughly a factor of four
at the best arm**: 25 true positives among 200 alerted entity-days is 12.5% where R6 asks for
more than half. And §15.5's own caveat is load-bearing — most of the best figure is an
**activity artefact**, since `Σ −2 ln P` grows linearly in the event count, so the arms that
price the event count in (standardised, at 1.0% and 3.0%) are the honest ones and they are
worse. The entity is therefore the right unit for R6 without being, by itself, a route to
satisfying it. What R6 demands of that work is precisely the part §15.5 does not yet have: a
per-entity statistic whose ranking is not a proxy for how busy the account is.

---

# Part IV — evaluation and results

## 14. Design of the evaluation

### 14.1 Corpus

LANL's public authentication corpus, days 0–13. Burn-in is days 0–6 and is frozen; scoring
runs days 7 onward. Denominators, from recorded runs:

| Quantity | Value |
|---|---|
| Raw rows, days 0–13 | 239,471,570 |
| Rows surviving the ingest prefilter | 79,437,168 |
| Burn-in events (days 0–6) | 37,218,638 |
| Scored events (days 7–13), full population | 42,218,530 |
| Labelled red-team events scored, days 7–13 | 549 |
| Labelled red-team events scored, days 7–8 | 262 |
| Labelled rows in the corpus overall | 749, naming 104 distinct users |

Day 8 carries 273 labelled events and day 12 carries 209; they are the two informative
days, and any short run must include day 8.

### 14.2 The matched-budget protocol

For each corpus day, the alert threshold at a budget of `b` alerts/day is the score
quantile that admits `b` of that day's events. A labelled event counts as detected at
budget `b` when its score reaches its own day's threshold. Both arms are measured on the
same window, and comparisons are restricted to days both arms scored.

### 14.3 Anomaly categories, and why they are not chosen by us

Detection is reported separately for each **structural category** of anomaly, so that an
advantage can be attributed to a *kind* of anomaly rather than asserted in aggregate.

The categories are assigned from each event's own evidence — properties of the event
relative to the history it was scored against — and are **deliberately not** assigned by
which detector produced the smallest p-value. A partition drawn along our own detectors'
firing would be a partition chosen in our favour, and every per-category margin computed
on it would be circular.

| Category | Structural test | Why a population model cannot express it |
|---|---|---|
| `novel_value` | the entity has history for the field but has never taken this value | a pooled model holds no per-entity state, so "this entity has not previously exhibited this value" is not expressible in it |
| `off_hours` | the entity's own fitted circular density at this time is below the uniform level | an hour-of-day feature is scored against the population's working pattern, and a rectangular encoding cuts the circle at midnight |
| `volume_burst` | the entity's observed count exceeds its own predicted rate | a population-fitted model reads a quiet account's tenfold increase as ordinary whenever busier accounts sustain that rate |
| `novel_pair` | two eligible values have never co-occurred | each value is individually frequent, so every marginal is satisfied and the combination is invisible to any detector scoring fields independently |
| **`population_rare`** | the value holds under one part in a thousand of its field's population mass | **the control** — this is what isolation-based detectors answer well, so it is where the framework should show no advantage |

## 15. Results

### 15.1 What the measurements establish

On LANL authentication, days 7–13, against 549 labelled red-team events:

- **The per-entity premise holds.** Novelty places the median labelled event at the 0.07th
  percentile of its own distribution.
- **Population-scope detectors do not carry signal here; per-entity detectors do.** That
  distinction, rather than any individual detector's tuning, is the finding.
- **The combination premise does not hold**, as [§10.2](#102-combination-and-the-finding-that-overturned-it) sets out.
- **No arm detects at event level under the original combination.** The honest headline
  remains negative, and the negative is informative rather than merely disappointing.

### 15.2 The headline, stated plainly

**The framework's composite detects 0 of 549 labelled events at every alert budget from 10
to 100 per analyst-day**, on the full-population run. Its combined verdict — the thing the
system ships — finds nothing.

That was long reported against "the four isolation-family baselines detect 0 of 653", a
pairing which should now be retired: the two arms covered different windows over different
denominators. [§15.7](#157-the-matched-head-to-head) replaces it with a comparison on
identical events, and the honest summary changes shape rather than sign — the composite is
still 0, but it is 0 against six population baselines that are also 0, one per-entity
baseline that is 1, and **its own novelty component, which is 21.**

Two of the five detector nulls were subsequently measured to be wrong rather than merely
loose, one of which has been repaired. A ranking dominated by a misspecified null cannot
test the hypothesis, whichever way the count comes out.

### 15.3 The structural finding, and a caveat that qualifies it

On the full-population run, **not one of the 549 labelled events is a population-marginal
outlier** (`population_rare` = 0). That is precisely what the framework's premise predicts,
and it explains why the isolation-forest family scores 0 of 653: the attacks are
per-entity anomalies, invisible to a pooled-feature-cloud model by construction. The
structure the design targets is genuinely present in the data.

**The claim holds across every correctly-sampled run, and its one apparent exception was
an artefact.** The category is computed against a marginal built from the *retained*
entities, so on an entity-sampled corpus it is an estimate against a sampled marginal:

| Run | entities scored | `population_rare` | `novel_pair` |
|---|---|---|---|
| `lanl-d7-14-005` (full population) | all | **0** of 549 | 29 |
| `lanl-openvocab-d7-9-001` (residue 0) | 1,049 | **0** of 262 | 20 |
| `lanl-holdout-r7-fixed-d7-9-001` (residue 7) | 1,029 | **0** of 262 | 10 |
| ~~`lanl-holdout-r7-d7-9-001`~~ (withdrawn) | 99, **all labelled** | ~~31 of 262~~ | ~~14~~ |

The withdrawn row is explained in [§15.4.2](#1542-the-first-version-of-this-section-was-wrong-and-how-it-was-wrong-matters):
that run scored no background population at all, so its "population" marginal was built
from red-team traffic.

`novel_pair` does still move with the sample — 29, 20, 10 — because the co-occurrence
graph is likewise built from the retained entities, and a thinner graph has fewer
established pairings for a new one to be novel against. Both are population-scope
categories and neither should be quoted from a sampled run without saying so.

### 15.4 The finding replicates on entities never scored before

Held-out evaluation drew a disjoint residue class of the entity hash: a corpus sharing
**no unlabelled entity** with the one every earlier run used, carrying all 104 labelled
users. Measuring twice on the same accounts measures memorisation; this measures
generalisation.

From `lanl-holdout-r7-fixed-d7-9-001` — 1,427,225 events scored over 1,029 entities, of
which **984 are background accounts no previous run had seen**, with 262 labelled events.
Per-detector arms, detections of 262:

| Arm | scope | 10/day | 25/day | 50/day | 100/day |
|---|---|---|---|---|---|
| **novelty** | per-entity | **2** | **8** | **15** | **21** |
| cooccurrence | population | 1 | 2 | 2 | 2 |
| marginal | population | 0 | 0 | 0 | 0 |
| timing | per-entity | 0 | 0 | 0 | 0 |
| volume | per-entity | 0 | 0 | 0 | 0 |
| min-p over all five | mixed | 0 | 5 | 13 | 20 |
| **Fisher over all five** | mixed | **0** | **0** | **0** | **0** |

**The core finding carries over intact.** Novelty is the only detector that meaningfully
discriminates; the composite finds nothing while its best component finds 21; and the
min-p arm, at 20, again lands *below* novelty alone — the multiplicity cost of carrying
four detectors that know almost nothing, reproduced on data that had no part in finding
it.

`population_rare` is **0 of 262** on this run, so the structural finding of
[§15.3](#153-the-structural-finding-and-a-caveat-that-qualifies-it) replicates too.

**One caveat remains.** The rates are still not comparable to a full-population figure:
labelled entities are exempt from every sample, so a 1-in-16 subset carries a sixteenth
of the background against all of the labelled population, and the competition an alert
must beat is correspondingly thinner.

#### 15.4.1 And again, on a third disjoint sample over three and a half times the window

Two disjoint samples is a replication; a third over a wider window tests whether the
result was an artefact of scoring only two days. `lanl-r11-d7-14-001` takes residue 11 —
sharing **no unlabelled entity** with residue 0 or residue 7, verified rather than assumed
— and scores **4,190,603 events over corpus days 7 to 14**, 5,059 entity-days, with 549
labelled events.

| Arm | scope | detections of 549 at 100/day |
|---|---|---|
| **novelty** | per-entity | **60** |
| **pairing** | per-entity | **59** |
| min-p over all five | mixed | 47 |
| marginal | population | 0 |
| timing | per-entity | 0 |
| volume | per-entity | 0 |
| **Fisher over all five** | mixed | **0** |

Detection rises from 8.0% of labelled events to 10.9%. That comparison is confounded —
the two runs differ in *both* the window and the residue class — so it is worth separating,
and [§15.4.1.1](#15411-the-rise-is-accumulated-history-not-the-residue-class) does.

The structure behind the result is what replicates, and it does so almost exactly:

| Category | d7–9, residue 7 | d7–14, residue 11 |
|---|---|---|
| `novel_value` | 190× lift | **189× lift** |
| `novel_pair` | 154× lift | **142× lift** |
| `off_hours` | 2× lift | 1× lift |
| `volume_burst` | 1× lift | 1× lift |
| **`population_rare`** | **0× (control)** | **0× (control)** |

Three things this hardens. The split is **novelty against everything else**, not
per-entity against population. The composite still finds nothing while its own strongest
component finds 60, and min-p at 47 is still *below* novelty alone — so combining loses on
every sample measured, on two windows, over three residue classes. And the control fires
on no labelled event on either run.

One thing it sharpens, unfavourably: **92 of 549 labelled events exhibit no structural
category at all.** They are neither novel, nor off-hours, nor a volume departure, nor rare
in the population. Nothing in this framework can see them, so the honest ceiling on this
run is **457, not 549** — and the gap between 60 and 457 is where the remaining work is,
not in the 92.

##### 15.4.1.1 The rise is accumulated history, not the residue class

Two comparisons separate the window from the sample, and both are available from runs
already recorded.

**The residue class contributes nothing.** Labelled entities are exempt from every sample,
so residue 7 and residue 11 carry the *identical* labelled population. Over the two days
both runs scored, the min-p arm catches **16 of 262 on each — the same sixteen events,
event for event**. Different background, same result. Whatever the wider window bought, it
was not bought by changing which accounts made up the background.

**Within the one run, more history is worth roughly double.** The campaign arrives in two
waves, and `lanl-r11-d7-14-001` scores both against the same corpus, the same detector and
the same budget. The only thing that differs is how much of each account's history had
accumulated by then:

| Wave | history beyond burn-in | min-p detections | recall |
|---|---|---|---|
| days 7–9 | 1 to 3 days | 16 of 267 | 6.0% |
| days 12–13 | 5 to 6 days | **31 of 282** | **11.0%** |

**And the confound runs against the effect, which is what makes this convincing.** If the
later wave were simply an easier attack, the comparison would prove nothing. It is
measurably *harder*:

| Wave | `novel_pair` | `off_hours` | **no category at all** |
|---|---|---|---|
| days 7–9 | 73.0% | 59.9% | 9.7% |
| days 12–13 | 59.2% | 7.4% | **23.4%** |

The later wave carries less of the highest-lift category, almost none of `off_hours`, and
**more than twice the share of events no detector here can see**. Restricted to events that
exhibit any structural category at all, recall goes from 16 of 241 (6.6%) to 31 of 216
(**14.4%**) — a factor of 2.2 against a composition that should have pushed it down.

This is direct evidence for one of the improvement directions in
[#12](https://github.com/JohnPierman/ethogram/issues/12): **the
fortnight of effective history this configuration carries is too short.** A 7-day burn-in
with a 7-day half-life means a monthly task looks novel every month, and industry practice
is 30 to 60 days. It is obtained here without running the burn-in grid, which remains the
proper test.

**Two limits on the claim.** It is measured on the min-p arm, the only one that records its
alerts per day; the per-detector arms record a count per budget over the whole run, so the
same decomposition is not available for them without re-running. And "more accumulated
history" and "later in the campaign" are not fully separable from two waves alone — a third
wave, or the burn-in grid, would settle it.

#### 15.4.2 The first version of this section was wrong, and how it was wrong matters

An earlier held-out run, `lanl-holdout-r7-d7-9-001`, applied entity sampling **twice** —
once when the corpus subset was built as residue 7 of the entity hash, and again in the
replay, whose selector keeps residue 0. The two sets are disjoint, so every background
entity was skipped:

| | broken run | corrected run |
|---|---|---|
| `corpus.events_skipped` | **3,077,318** | 0 |
| distinct entities scored | **99** | 1,029 |
| background entities | **0** | 984 |
| `population_rare` | **31** of 262 | **0** of 262 |

**That run scored red-team accounts and nothing else.** Its alert budget was allocated
among events produced exclusively by attacker accounts, so no figure from it was a valid
measurement — and its `population_rare` census was computed against a "population"
consisting of the attack.

This is worth recording as a methodological point rather than filing as a mishap. The run
completed, wrote a well-formed result with complete provenance — git sha, corpus
checksum, row counts, seeds, timings — and produced numbers in an entirely plausible
range. Two of its arms even agreed with the corrected run to within one detection.
Nothing about the artefact announced that 3,077,318 events had been dropped.

It is the same failure mode [§8.1](#81-scoring-strictly-precedes-observation) warns about
for score-before-observe: **the measurement is gone while the numbers stay believable.**
Provenance discipline records what a run *did*; it cannot record what the run should have
done instead. The defences that would have caught this are of a different kind — checking
that a run scored a background population at all, and refusing to sample a corpus whose
manifest already records sampling. Both are tracked in
the double-sampling defect; the first
is now computed for every run and shown on the dashboard.

### 15.5 Ranking accounts rather than events

The evaluation ranked individual events while the thesis is about entities — the cheapest
available change, and the one with a real chance of moving detection off zero.

The campaign is persistent per entity: on day 8, 273 labelled events across 45 entities,
with `U293` carrying 30 and `U66` carrying 27; `U66` reappears on days 12 and 13. An
account with 30 anomalous events in one day is a far stronger signal than any one of
them, and event-level ranking discards it entirely.

Over 1,777 entity-days of which 46 are labelled:

| Ranking | 10/day | 25/day | 50/day | 100/day |
|---|---|---|---|---|
| event level | 0/262 | 0/262 | 0/262 | 0/262 |
| entity-day, corrected minimum | 0/46 | 0/46 | 0/46 | 0/46 |
| entity-day, Fisher | 1/46 | 1/46 | 7/46 | **15/46** |
| entity-day, Fisher, conformal | 1/46 | 4/46 | 12/46 | **25/46 (54%)** |

**Do not report those numbers without this paragraph.** The top of the Fisher ranking on
day 8 is `U66@DOM1` with **177,748 events**. `Σ −2 ln P` grows linearly in the event count,
so this ranking sorts by **activity** at least as much as by anomaly, and the busiest
accounts reach the top by arithmetic. Attackers use busy service and admin accounts, so
recall follows activity — which is exactly the shape of result that flatters a framework
falsely.

#### The confound was measured, and it accounts for most of the result

`standardised_x2 = (Σ −2 ln P − 2n) / (2√n)` asks whether an entity's accumulated evidence
exceeds what its **event count alone** predicts under `χ²(2n)`. It is a standardised score
and not a p-value — the reference needs the entity's events to be independent and they are
the least independent data in the corpus — but it removes the confound the raw sum carries.

Of 46 labelled entity-days, at 100 alerts a day:

| Ranking | `lanl-entity-std-d7-9-001` | `lanl-conformal-std-d7-9-001` |
|---|---|---|
| corrected minimum | 0 | 0 |
| Fisher over the day | 15 | **25** |
| **standardised** | **6** | **2** |

**Most of the entity-day result was the activity artefact.** Under conformal calibration,
where Fisher's raw sum reported 25 of 46, the statistic that prices in the event count
reports **2**. The honest reading of §15.5 is therefore that ranking accounts rather than
events is the right *direction* — it is the only construction in this project that has
produced a non-trivial detection count at all — but that the specific 25-of-46 figure does
not survive the correction, and should not be quoted.

What remains genuinely open is a discrepancy between samples that this measurement
surfaced and does not explain: on the corrected held-out run
(`lanl-holdout-r7-fixed-d7-9-001`, 1,749 entity-days) the **corrected minimum** detects 24
of 46 at 100/day, where on the training sample of almost identical size it detects **0**.
Two rankings disagreeing by that much across entity samples of the same construction is
not sample noise of a kind that can be waved away, and it is recorded as an open question
rather than as a result.

### 15.6 Which kinds of anomaly the attacks actually are

The anomaly categories were built to attribute an advantage to a *kind* of anomaly rather
than assert it in aggregate ([§14.3](#143-anomaly-categories-and-why-they-are-not-chosen-by-us)).
Read as **lift** — how much more often a category fires on a labelled event than on
background — they explain every detector result in this document at once.

From `lanl-pairing-r7-d7-9-002`, 1,427,225 scored events of which 262 are labelled:

| Category | scope | share of labelled | share of background | **lift** |
|---|---|---|---|---|
| `novel_value` | per-entity novelty | 38.5% | 0.2% | **184×** |
| `novel_pair` | per-entity novelty | **74.0%** | 0.5% | **150×** |
| `off_hours` | per-entity distribution | 59.2% | 36.4% | 1.6× |
| `volume_burst` | per-entity distribution | 38.2% | 26.9% | 1.4× |
| **`population_rare`** | **population (control)** | **0.0%** | 0.1% | **0×** |

<!-- figure: category-lift -->

**The split is not per-entity against population. It is novelty against everything else.**

Two categories carry almost all the discriminative power, and both ask the same kind of
question — *has this entity done this before?* — of a value, and of a combination of
values. Two more are per-entity and distributional — *is this hour, is this volume,
unusual for this entity?* — and they run at lift 1.4× to 1.6×, firing on roughly a third
of all background traffic. The control fires on nothing, as it must.

That table predicts the detector arms, and the arms confirm it:

| Detector | what it asks | detections of 262 at 100/day |
|---|---|---|
| `novelty` | is this **value** new for this entity | **21** |
| `pairing` | is this **combination** new for this entity | **23** |
| `timing` | is this **hour** unusual for this entity | 0 |
| `volume` | is this **rate** unusual for this entity | 0 |
| `marginal` | is this value rare in the **population** | 0 |
| `cooccurrence` | is this pairing rare in the **population** | 2 |

**So the premise is right but under-stated, and the detector mix is wrong.** Two of the
five components ask the question that works and three do not — and the two that work are
the two that were hardest to build, because per-entity novelty over an open vocabulary is
exactly what equation (4), the decay rule, the reserved unseen mass and the Good–Turing
path exist to estimate.

A caveat that keeps this honest: `off_hours` and `volume_burst` firing on a third of
background is partly a property of how those categories are *defined* — "below the uniform
density" and "above the predicted rate" are one-sided tests that roughly a third of
ordinary traffic passes by construction. Their low lift says the *category* does not
select attacks; it does not by itself prove the detectors behind them are worthless. That
those detectors also score 0 of 262 is the independent evidence, and the two agree.

### 15.7 The matched head-to-head

Every earlier comparison in this project paired numbers from different windows over
different denominators. This one does not. The framework run
`lanl-holdout-r7-fixed-d7-9-001` and the baselines run `baselines-holdout-r7-d7-9-001`
were computed over **the same 1,427,225 events, the same 1,029 entities, and the same 262
labelled events**, on the same two corpus days, from the same corpus file.

Detections of 262 labelled events, at each alert budget per analyst-day:

| Model | scope | family | 10 | 25 | 50 | 100 |
|---|---|---|---|---|---|---|
| **framework: novelty** | per-entity | Dirichlet–multinomial, calibrated | **2** | **8** | **15** | **21** |
| framework: min-p over five | mixed | Šidák minimum | 0 | 7 | 12 | 20 |
| framework: cooccurrence | population | degree-corrected block model | 1 | 2 | 2 | 2 |
| **baseline: entity_ewma** | **per-entity** | EWMA z-score, uncalibrated | 0 | 0 | 1 | **1** |
| framework: marginal | population | non-parametric marginal | 0 | 0 | 0 | 0 |
| framework: timing | per-entity | von Mises kernel | 0 | 0 | 0 | 0 |
| framework: volume | per-entity | negative binomial | 0 | 0 | 0 | 0 |
| **framework: composite (Fisher)** | mixed | Fisher + Brown | **0** | **0** | **0** | **0** |
| baseline: iforest | population | isolation | 0 | 0 | 0 | 0 |
| baseline: eif | population | isolation | 0 | 0 | 0 | 0 |
| baseline: hst | population | isolation | 0 | 0 | 0 | 0 |
| baseline: lof | population | density | 0 | 0 | 0 | 0 |
| baseline: ocsvm | population | boundary | 0 | 0 | 0 | 0 |
| baseline: pca | population | linear subspace | 0 | 0 | 0 | 0 |

Four things fall out of it, in ascending order of how uncomfortable they are.

**1. Six population baselines across four inductive biases detect nothing, at any
budget.** This is a materially stronger statement than the earlier "four
isolation-family models score 0 of 653", which was one observation wearing four hats.
Isolation, density, boundary and linear-subspace models fail together, and the category
census says why: not one labelled event is a population-marginal outlier. **The attacks
are per-entity anomalies, and no amount of technique applied at population scope reaches
them.**

**2. The per-entity framing is worth something on its own.** `entity_ewma` — an EWMA
z-score against the entity's own history, with no calibration, no abstention, no
combination and no conformal step — finds one labelled event that every population model
misses. One is not many, but it is the only non-zero result outside the framework, and it
comes from the cheapest possible implementation of the framework's central idea.

**3. The framework's calibration machinery earns its place above that framing.** Novelty
finds **21 where the naive per-entity baseline finds 1**. The gap between those two rows
is the clearest measurement in this project of what the Dirichlet–multinomial predictive,
the lazy decay, the reserved unseen mass and the conformal calibration are actually worth:
they are not decoration on the idea of comparing an account to itself, they are most of
its yield.

**4. The framework's own default configuration is beaten by the naive baseline.** The
Fisher composite — what the system ships as its combined verdict — finds **0**, against
`entity_ewma`'s 1 and its own novelty component's 21. Every argument in
[§10.2](#102-combination-and-the-finding-that-overturned-it) is confirmed here on matched
data and against an external reference: **combining an informative detector with four
uninformative ones does not merely fail to help, it produces a system worse than a
twenty-line baseline.**

That fourth reading is the one to act on, and it is why
[§17](#17-what-follows) leads with "stop combining".

### 15.8 The supporting hypotheses

| | Hypothesis | Status |
|---|---|---|
| **E1** | Detections published baselines miss, at matched budget | **Measured, negative and uninformative**: 0 of 549 against 0 of 653 |
| **E2** | Lower alert volume at matched detection | As E1: undecidable while both arms detect nothing |
| **E3** | Realised FDR tracks nominal `q` | **Measured, uninformative**: realised FDR 1.0 at every `q`, all days saturated |
| **E4** | Local conditioning against single-block degeneration | **Measured, and the ablation goes the wrong way**: the partitioned arm is worse calibrated than the single-block one |
| **E5** | Composite validity under schema growth | **Measured, and uninformative**: fully right-censored — see below. Treatment C not run, with reason |
| **E6** | A source admitted without code change | **Measured**: 6,004,252 rows, 5,599,989 events scored, zero code changes |
| **E7** | Verdicts verifiable from evidence alone | **Measured: 79.2%** of 49,581 sampled verdicts |
| **E8** | Batch independence | **Measured: passes**, with the closed form confirmed to 4e-15 |
| **E9** | Circular against the 168-cell grid | Control passes; corpus arm awaiting the run |

#### E5 in detail: measured, and censored to the point of carrying no information

`e5-schema-growth-001` scored 8,672,445 events over days 7–20 with three fields held out
and introduced at the two-week boundary. **Every era, at every nominal `q` in
{0.001, 0.01, 0.05, 0.1}, reports the same discovery count and is flagged
`censored: true` on all seven days.** The Benjamini–Hochberg cut reached the last of the
20,000 retained alerts every day, so the discovery count is a lower bound and no realised
FDR is reported for any era.

The censoring is *detected and declared* rather than hidden, which is the repair that was
made to the E5 harness — but the underlying condition is unchanged, and it has the same
cause as E3's realised FDR of 1.0 at every `q`: **the composite is anti-conservative
enough that BH rejects essentially everything**, so any retention cap binds. E5 cannot be
answered before the combination is fixed, and the run establishes that rather than
answering the hypothesis.

The one thing it does establish is negative and useful: a schema-growth experiment run on
top of a miscalibrated composite measures the composite, not the schema growth.

### 15.9 How much of the shortfall is ranking, not detection

Detecting 60 of 549 invites the reading that the detectors are weak. That is not what the
numbers say, and the distinction decides what to build next: **selecting the right set of
events and ordering events inside it are different problems with different remedies.**

The framework does the first well. `novel_pair` fires on 19,841 of 4,190,603 events — 0.5%
of traffic — and 362 of the 549 labelled events are in there. Against a base rate of 0.013%,
that slice is enriched roughly **140-fold**. The question is what happens next, and the
answer is priced by three reference points at a fixed budget: what a *signal-free* ranking
inside the set would catch, what the arm actually caught, and what a perfect ordering would.

From `lanl-r11-d7-14-001`, 700 alerts over 7 days:

| Set | events in set | labelled in set | random ranking | **achieved** | perfect ordering |
|---|---|---|---|---|---|
| `novel_value` | 8,913 | 215 | 16.9 | **60** | 215 |
| `novel_pair` | 19,841 | 362 | 12.8 | **59** | 362 |

And the same three points on the narrower, disjoint `lanl-pairing-r7-d7-9-002`, 200 alerts:

| Set | events in set | labelled in set | random ranking | **achieved** | perfect ordering |
|---|---|---|---|---|---|
| `novel_value` | 2,994 | 101 | 6.7 | **21** | 101 |
| `novel_pair` | 7,044 | 194 | 5.5 | **23** | 194 |

Two readings, both replicated across the two runs:

**The score is doing real work.** Ranking inside the enriched set beats a signal-free
ordering of the same set by **3.1× to 4.6×**. The estimator, the decay, the reserved unseen
mass and the Good–Turing path are not decoration; strip them and keep only the category
membership and detection falls by roughly a factor of four.

**And most of the remaining recall is reachable by ranking alone.** Perfect ordering inside
the *same* set, with no new detector and no change to the unit of analysis, is **3.6× to
8.4×** what is achieved. That is where the work is — not in a sixth null.

Two boundaries on the claim. Perfect ordering is an upper bound nobody attains, so the
figure prices the *problem*, not an expected gain. And 92 of the 549 labelled events sit
outside every set above; no re-ranking reaches them, so the reachable population is 457.

#### 15.9.1 The ranking is not degenerate, which changes what to fix

The obvious explanation for that headroom would be ties: if every first-ever value received
the same reserved mass, the ordering among novel events would be decided by the tie-break
rather than by evidence. **It is not that.** Among the 549 labelled events of
`lanl-r11-d7-14-001` there are **544 distinct p-values**, and the only tie group of any size
is 6 events at *p* = 1. The estimator already produces a gradient.

What it does not read is **shape**. Equation (4) reserves α / (n + α(K+1)) for the unseen,
which depends on the total count and the number of distinct values and never on how the mass
is spread across them. The two histories that matter most are the ones it cannot
distinguish:

| Account's history | true P(next value is new) | equation (4) |
|---|---|---|
| 3 values, 1,000 observations each | ≈ 0 | 0.00033 |
| 500 values, one observation each | ≈ 1 | 0.001 |

The second is what a compromised account looks like, and the two answers differ by a factor
of three. The Good–Turing path of [§9.1](#91-detector-i--categorical-novelty) reads the
singleton rate instead and separates them.

**It was the obvious fix, it has now been measured, and it makes things worse.**
[§15.9.2](#1592-the-obvious-fix-was-measured-and-it-loses) reports that run. What replaces the
obvious fix is not obvious, and [§15.9.3](#1593-what-the-p-value-actually-ranks) explains why by
pinning down what equation (4) is really sorting on.

#### 15.9.2 The obvious fix was measured, and it loses

`lanl-r11-openvocab-d7-14-001` is `lanl-r11-d7-14-001` with `-open-vocabulary` and **nothing
else changed**: the same corpus file by digest, the same 4,190,603 events, the same 549 labelled
events, the same window. Detections of 549:

| Arm | 10/day | 25/day | 50/day | 100/day |
|---|---|---|---|---|
| novelty | 11 → **6** | 26 → **17** | 38 → **30** | 60 → **46** |
| pairing | 4 → 1 | 8 → 12 | 8 → **24** | 59 → **36** |
| min-p | 4 → 2 | 12 → 12 | 15 → 23 | 47 → 45 |
| composite | 0 → 0 | 0 → 0 | 0 → 0 | 0 → 1 |

**Novelty loses 14 of 60 and pairing 23 of 59 at the operating budget**, and novelty is worse at
every budget measured. The theoretically better-calibrated null is the empirically worse
detector, which is a result and not a bug.

**Why, measured rather than guessed.** Group the 549 labelled events by how extreme equation (4)
scored them, and ask what the shape-sensitive form did to each group:

| Band under equation (4) | n | median *p*: (4) | median *p*: Good–Turing | rose | fell |
|---|---|---|---|---|---|
| **most extreme decile** | 54 | 8.03e-07 | **8.75e-05** | **43** | 1 |
| 2nd decile | 55 | 2.88e-05 | 2.16e-05 | 11 | 35 |
| middle (20–80%) | 330 | 6.46e-04 | 2.44e-03 | 120 | 148 |
| least extreme fifth | 110 | 3.44e-02 | 3.18e-02 | 0 | 79 |

**Good–Turing systematically de-prioritises exactly the events equation (4) ranked highest.** In
the top decile it raises 43 of 54 p-values, by a median factor of 109. Of the 60 events
equation (4) alerted on, it keeps 24, loses 36 and gains 22 — the two forms disagree about most
of what to alert on — and the 36 it lost became a median **162× less extreme**.

The mechanism follows from [§15.9.3](#1593-what-the-p-value-actually-ranks): the events
equation (4) alerts on are on accounts with very large histories, and a large history means many
values seen exactly once, so the singleton rate `N₁/n` is *high* for precisely those accounts.
Good–Turing therefore judges a first-ever value **unremarkable** for them — which is defensible
as an estimate of *P*(new) and destroys the ranking.

**So the two effects are aligned on this corpus by accident, and the accident is load-bearing.**
Equation (4)'s size-dependence is the wrong thing to sort on in principle and happens to select
the right accounts here. Nothing in this measurement says it will on another corpus, and that is
the uncomfortable part: the working configuration is working for a reason the model does not
state.

#### 15.9.3 What the p-value actually ranks

The synthetic attacks answer this, and it is the most useful thing they produced. Detecting
0 of 856 planted events invited the reading that the detectors did not see them. They did: a
planted credential spray scores a median *p* of 5.5e-04 under novelty against the real
campaign's 6.6e-04, the same band. What kept them out was the threshold, and the threshold is
recoverable — novelty caught 60 labelled events at 100 alerts a day, so the 60th smallest
labelled *p* is the least extreme event that still won a slot: ***p* ≤ 8.52e-06**.

Against that, the *most extreme* planted event of each type:

| Type | best *p* under novelty | multiple of the threshold |
|---|---|---|
| account_takeover | 5.57e-05 | **7×** |
| lateral_chain | 1.64e-04 | 19× |
| credential_spray | 1.97e-04 | 23× |
| privilege_escalation | 8.26e-04 | 97× |
| off_hours | 3.25e-01 | 38,092× |
| low_and_slow | 3.20e-01 | 37,584× |

Not one planted event of any kind reaches the threshold, and the closest misses by a factor of
seven. **So what is the p-value sorting on?** For an unseen value the estimate reduces to the
reserved mass α / (n + α(K+1)), which with α = 1 is close to 1/n whenever *n* dominates *K*. If
that is what dominates, then `p × n` should be about 1 for every victim, whatever was planted:

| Victim | history *n* | best novelty *p* | **p × n** |
|---|---|---|---|
| U5087@DOM9 | 263 | 4.17e-03 | 1.10 |
| U9274@DOM1 | 1,059 | 6.35e-04 | 0.67 |
| U3236@DOM1 | 2,847 | 4.06e-04 | 1.15 |
| U691@DOM1 | 5,741 | 1.97e-04 | 1.13 |
| U202@DOM1 | 20,666 | 5.57e-05 | 1.15 |

Across 32 victims spanning **263 to 20,666 events of history and four different attack types,
`p × n` has median 1.15** and lies between 0.50 and 7.22.

**The p-value for a first-ever value is essentially 1/n — the size of the account's history, not
the surprisingness of the value.** Among novel events the ranking is close to *sort accounts by
event count*. Clearing 8.52e-06 requires about **117,000 events** of accumulated history; the
busiest planted victim had 20,666, so **no attack on an ordinary account could have been alerted
regardless of what it did**.

This is the same finding as the 3.6×–8.4× headroom above, stated mechanically instead of
statistically, and it reframes the problem. The ranking is not noisy; it is sorted by the wrong
key. And [§15.9.2](#1592-the-obvious-fix-was-measured-and-it-loses) shows that substituting the
textbook better key makes recall worse, because on this corpus the wrong key is correlated with
the answer. What is needed is a score that separates *how settled this account's vocabulary is*
from *how much traffic it carries*, and neither form currently does.

### 15.10 Detection by attack type, on planted ground truth

The real campaign is one uneven mix, so it cannot separate *this detector cannot express this
mechanism* from *the corpus barely contains this mechanism*. `cmd/inject` plants six kinds of
attack with controlled ground truth to separate them. Three are deliberately single-signal, so
a detection is attributable to one null.

**The constraint that makes it honest.** Every planted event is individually plausible: each
destination, authentication type and logon type is a value that occurs in the corpus, so only
the combination, the timing or the volume is unprecedented for the victim. And the planted value
is the *most ordinary* one the victim has never used — the busiest host it has never reached,
the commonest authentication type it has never used. Choosing an arbitrary unused value instead
plants a **population-rare** one, because most values in an open vocabulary are rare; the first
attempt did exactly that, and the population `marginal` detector consequently scored
`privilege_escalation` at a median *p* of 2.8e-06 while per-entity novelty sat at 3.4e-03. The
type was measuring population rarity under a per-entity name. After the correction, `marginal`
on that type is 1.7e-01.

From the matched pair `lanl-injected-r7-d7-14-002` and `baselines-injected-r7-d7-14-002` —
4,494,396 events, 1,405 labelled events of which 856 are planted and 549 real — at 100 alerts
per analyst-day. **The synthetic and real columns answer different questions and are never
added:** a planted attack measures whether a detector responds to a mechanism *by construction*,
the real campaign whether it found an intrusion.

| Model | spray (320) | chain (40) | off-hrs (64) | priv-esc (24) | slow (288) | takeover (120) | real (549) |
|---|---|---|---|---|---|---|---|
| ours: novelty | 0 | 0 | 0 | 0 | 0 | 0 | **60** |
| ours: pairing | 0 | 0 | 0 | 0 | 0 | 0 | 59 |
| ours: min-p | 0 | 0 | 0 | 0 | 0 | 0 | 47 |
| ours: marginal | 0 | 0 | 0 | 0 | 0 | **76** | 0 |
| ours: composite | 0 | 0 | 0 | 0 | 0 | 20 | 0 |
| ours: timing | 0 | 0 | 0 | 0 | 0 | 0 | 1 |
| ours: volume | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| base: ocsvm | 0 | 0 | 0 | 0 | 0 | 33 | 0 |
| base: lof | 0 | 0 | 0 | 0 | **12** | 0 | 0 |
| base: entity_ewma | 0 | 0 | 0 | 0 | 0 | 1 | 0 |
| base: iforest, eif, hst, pca | 0 | 0 | 0 | 0 | 0 | 0 | 0 |

**Five of the six planted types are detected by nothing at all** — 736 of 856 planted events
invisible to every arm and every baseline, at every budget from 10 to 100 a day. This is the
uncomfortable result of the experiment and it is the honest one.

Four readings, and the order matters because the first three are about the *harness* and only
the fourth is about the method:

1. **The misses are not blindness.** The detectors score the novelty types as anomalous — a
   planted spray sits in the same band as the real campaign's events — and lose on the
   threshold. [§15.9.3](#1593-what-the-p-value-actually-ranks) shows why: no attack on an
   ordinary account can reach the cut, because the cut is set by account size.
2. **`off_hours` and `low_and_slow` are a different kind of zero.** They score *p* ≈ 0.03 to
   0.7 under the nulls built for them — four to five orders of magnitude from the threshold, not
   one. The timing and volume nulls do not respond meaningfully even to attacks constructed to
   exercise them, for the reason [§9.2](#92-detector-iia--circular-timing) gives: accounts'
   hour distributions are diffuse and their volumes over-dispersed, so neither carries
   information on this corpus.
3. **`marginal`'s 76 of 120 is not a clean win, and should not be quoted as one.** Account
   takeover changes destination, authentication type, logon type and hour at once, so three
   fields can carry residual population rarity even under the corrected generator, and the
   population detector is picking one of them up. It is also the only type `marginal` ranks
   highly at all, so its entire budget lands there. What the row shows is that the type is
   *reachable*, not that the detector is good.
4. **Six published baselines across four inductive biases detect nothing on five of six types**,
   including on attacks built to be detectable. `ocsvm` at 33 and `lof` at 12 are the exceptions
   and both are on the volume-and-burst end, which is what a geometric outlier model should
   see.

**One limit on precision, recorded in the taxonomy document itself.** Value choice is
deterministic given the corpus, so every victim of a type receives the same planted values. The
events of one type are therefore not independent draws, and the effective sample size is closer
to the victim count — eight — than to the event count. Read a per-type rate as coarse.

## 16. Threats to validity

Stated because a result that does not name its own weaknesses is not a result.

1. **One corpus, one attack campaign.** Every quantitative claim rests on LANL
   authentication. The red-team activity is a single campaign by a single team; its
   characteristics are not known to be representative.
2. **Entity sampling inflates measured rates.** Labelled entities are exempt from every
   sample, so a 1-in-16 subset carries a sixteenth of the background against all of the
   labelled population. Sampled rates are **not** comparable to full-population ones, and
   the category census is itself sample-dependent ([§15.3](#153-the-structural-finding-and-a-caveat-that-qualifies-it)).
   Applied twice it removes the background entirely, which is what happened to the first
   held-out run ([§15.4.2](#1542-the-first-version-of-this-section-was-wrong-and-how-it-was-wrong-matters)).
3. **The original baseline window did not match any framework window.** Baselines ran days
   7–30 and framework runs days 7–8 or 7–13, so the widely-quoted "0 of 549 against 0 of
   653" pairs two denominators over two windows. [§15.7](#157-the-matched-head-to-head) is
   the matched comparison and is the one that should be quoted; the older pairing should
   not be.
4. **The per-entity baseline detects one event, so the framing claim rests on a thin
   margin.** `entity_ewma` beating six population models 1–0 is the right *direction* but
   a single event; a second corpus or a second campaign could reverse it. What is not
   thin is the 21-against-1 gap between the framework's novelty arm and that baseline.
5. **Entity-day results are confounded by activity, and the confound accounts for most of
   the effect.** Pricing in the event count takes Fisher's 25 of 46 down to 2
   ([§15.5](#155-ranking-accounts-rather-than-events)). A separate, unexplained
   discrepancy between entity samples is recorded there.
6. **The open-vocabulary estimator is untested on an open vocabulary.**
7. **No committed result was produced through the persistent store.** Every recorded run
   used the in-memory stores; the database path exists and is exercised only by tests.

## 17. What follows

The evidence points in one direction, and it is not the direction the design anticipated.

**Stop combining.** One detector carries the signal; combining it with four uninformative
ones costs accuracy under every rule tried, and the theory says no rule recovers it.
Present each signal on its own terms — which is also more actionable, since "this account
used a program it has never used" is an instruction and a blended score is not.

**Move the unit of analysis to where the thesis already put it.** The framework argues
that the entity is the unit; the evaluation ranked events. Closing that gap produced the
project's only non-zero detection, and the remaining work is to remove the activity
confound rather than to discover the effect.

**Replace the population-scope detector with its per-entity form**, which the design's own
principle requires and which preserves the one signal it uniquely covers.

**Test the open-vocabulary path on an open vocabulary**, which is the case it was built
for and the case in which the default estimator is known to be wrong by orders of
magnitude.

**Make the error rate the control it is supposed to be.** The evaluation reports fixed
alert budgets throughout because E3's false-discovery control saturates, and a budget is a
statement about the analyst rather than about the alerts
([§4.3](#43-honest-arithmetic-about-how-much-is-too-much)). This is not a separate work
item so much as the clearest statement of what fixing the combination is *for*: with a
statistic a threshold can be attached to, the composite becomes something a decision rule
can be built on at all.

What that decision rule should optimise has changed. The volume-predictability requirement
has been withdrawn in favour of R6's precision requirement, so a short queue on a quiet day
is no longer the objective — an unbounded queue of alerts that are mostly right is preferred
to a bounded queue that is mostly wrong.

The open work is tracked as
[GitHub issues](https://github.com/JohnPierman/ethogram/issues),
grouped into milestones for detection, evidence and publication.

---

## Appendix A — frozen parameters

Not to be retuned without re-running everything that depends on them.

| Decision | Value |
|---|---|
| Burn-in | `t < 604800` (days 0–6) |
| Scoring window | days 7–13 (`-maxseconds 1209600`) |
| Entity population | source users matching `^U[0-9]` |
| Concentration `α` | 1 |
| Half-life `T½` | 7 days |
| Timing bandwidth | 1.5 h (`κ ≈ 6.48`, `H = 11`) |
| Grid control | 168 weekly cells |
| Alerts retained | 200/day |
| Leiden seed | 42 |
| Registry thresholds | identifier ratio 0.95 over ≥200 observations; numeric fraction 0.99; unknown below 50 |
| Detector IV bounds | minimum 1000 observations, maximum cardinality 1000 |
| Conformal minimum | 10,000 observations before a detector may be calibrated |

## Appendix B — reproducing the results

Corpora are not committed; see `DATA.md` for sources and licence terms.

```sh
make db-up            # Postgres 16, compose project "cad"
make e8               # the determinism gate; needs neither corpus nor database
make test             # full suite, race detector on
make cover            # 80% gate on domain and application
make dashboard        # regenerate docs/dashboard.html from results/
make verify-provenance
```

Corpus- and database-dependent tests are behind the `corpus` and `integration` build tags,
so the default suite runs anywhere.
