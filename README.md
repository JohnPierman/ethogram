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
