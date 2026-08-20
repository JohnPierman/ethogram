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

549 labelled events, `lanl-r11-b1000-weighted-d7-14-003`. `novelty` replicated on a second
subset at 21 of 262.

At 10 alerts/day that is 11 hits in 70 alerts — **16% precision against a 0.013% base rate,
about 1,200× better than chance**. Precision improves as the budget tightens.

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
| `cooccurrence` | do these two values co-occur as the population predicts? | population | on, measured |
| `marginal` | is this value rare across the whole population? | population | on, measured |
| `pairing` | has *this account* combined these two values before? | per-entity | opt-in, measured |
| `noveltyrate` | is it producing *new* values faster than it usually does? | per-entity | opt-in, **measured** |

`noveltyrate` exists because `novelty`'s p-value for a first-ever value is roughly one over
the size of the account's history, which makes it structurally unable to alert on a small
account however anomalous it is. The rate question is scale-free instead. It is the broadest
arm on planted attacks -- the only one reaching four of six types. Two corpora now agree that
it works, which was the bar set for turning it on by default, so that is a decision waiting
to be taken rather than evidence waiting to arrive.

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
  split does win, by 11 detections of 384.
- **Detection by attack type depends on the budget.** At 100 alerts/day one of six planted
  types is reached; at 1000/day five of six are. `low_and_slow` (288 planted events) is
  reached by no arm of this framework at any budget -- though a local-outlier-factor baseline
  reaches 12 of them, so it is a gap in this approach rather than in the corpus.
- **Population scope is not useless, which an earlier version of this README implied.** The
  population `marginal` owns account takeover outright and a population baseline reaches the
  one type no arm here does. The scopes are complementary.

## Getting started

```sh
make test      # full suite, race detector on
make e8        # determinism gate; needs no corpus and no database
make cover     # 80% floor on domain and application
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

Every figure in the paper is a diagram drawn in code, not a plot of data, so no figure can
disagree with a result. Numbers in the paper cite the result file they came from.

**Never edit a result file by hand. Re-run instead.**

## Contributing

Commits are `type(scope): imperative`, ≤72 characters, no trailers. Every PR carries a
`CHANGELOG.md` entry. Never force-push; never squash without asking.

## Licence

Apache-2.0 — see [`LICENSE`](LICENSE). Corpus licences are separate, are not granted by it,
and are recorded in [`DATA.md`](DATA.md).
