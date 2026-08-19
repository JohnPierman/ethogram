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

| | 10/day | 100/day |
|---|---|---|
| **`novelty`** (per-entity) | **11** | **60** |
| composite (Fisher) | 0 | 0 |

549 labelled events, `lanl-r11-d7-14-001`. Replicated on a second subset at 21 of 262.

At 10 alerts/day that is 11 hits in 70 alerts — **16% precision against a 0.013% base rate,
about 1,200× better than chance**. Precision improves as the budget tightens.

**The composite catches nothing while its own best component catches 60.** Combining
currently destroys the signal, so use the detectors directly. See the paper.

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
| `noveltyrate` | is it producing *new* values faster than it usually does? | per-entity | opt-in, **not yet measured** |

`noveltyrate` is implemented and tested but **no recorded run includes it**, so nothing here
claims it works. Do not read it as a capability.

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
- **The combination layer does not work.** Diagnosed, not mysterious — see the paper.
- **Only one of six synthetic attack types is detected** by any current arm.

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
