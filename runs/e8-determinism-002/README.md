# Run e8-determinism-002

A result of kind `e8`; it claims E8.

## Provenance

| Field | Value |
| --- | --- |
| Run id | e8-determinism-002 |
| Started | 2026-08-14T23:30:20Z |
| Finished | 2026-08-14T23:30:20Z |
| Go version | go1.26.3 |

Not recorded: `run.git_sha`, `run.git_dirty`, `runtime.wall_seconds`, `runtime.events_per_sec`.

## Corpus

Synthetic input, as recorded: E8 requires no corpus (§12.3); the probe and its history are constructed so the batch compositions differ by two orders of magnitude

| Figure | Value |
| --- | --- |
| Rows read | not recorded (`corpus.rows_read` is absent) |
| Events warmed (burn-in) | not recorded (`corpus.events_warmed` is absent) |
| Events scored | not recorded (`corpus.events_scored` is absent) |
| Events skipped | not recorded (`corpus.events_skipped` is absent) |
| Row errors | not recorded (`corpus.row_errors` is absent) |

The burn-in boundary is not recorded (`corpus.burn_in.end_seconds` is absent).
Coverage kind, as recorded: control (`corpus.coverage.statement` is absent).

## Sampling

No entity sampling block is recorded (`parameters.entity_sample` is absent); the full population was scored.

## Parameters

| Parameter | Value |
| --- | --- |
| alpha | 1 |
| half_life_days | 7 |
| bandwidth_hours | 1.5 |
| grid | not recorded (`parameters.grid` is absent) |
| top_k_per_day | not recorded (`parameters.top_k_per_day` is absent) |
| compositions | 5 |
| repeats | 64 |

## Runtime

The runtime block is not recorded (`runtime` is absent).

---

The command line was not recorded (`run.command` is absent).

Rendered by cmd/runreport from e8-determinism.json only. A number that does not appear in the result file does not appear here.
