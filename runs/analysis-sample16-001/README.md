# Run analysis-sample16-001

A result of kind `analysis`; it claims E1, E2, E3, E4, E9.

## Provenance

| Field | Value |
| --- | --- |
| Run id | analysis-sample16-001 |
| Started | 2026-08-15T20:33:36Z |
| Finished | 2026-08-15T20:33:36Z |
| Go version | go1.26.3 |

Not recorded: `run.git_sha`, `run.git_dirty`, `runtime.wall_seconds`, `runtime.events_per_sec`.

## Corpus

| File | sha256 (truncated to twelve characters) |
| --- | --- |
| auth.txt.gz | 9c6b0cc261b0… |
| redteam.txt.gz | 606635837c68… |

Digests are truncated for the page; result.json beside this report carries them in full.

| Figure | Value |
| --- | --- |
| Rows read | 239,471,570 |
| Events warmed (burn-in) | 4,181,736 |
| Events scored | 4,542,974 |
| Events skipped | 70,712,458 |
| Row errors | 0 |

Burn-in ends at 604,800 seconds of corpus time, fixed at commit `24c5a53`.
Coverage, as recorded: corpus days 7.00 to 14.00 scored (burn-in days 0 to 7.00).

## Sampling

No entity sampling block is recorded (`parameters.entity_sample` is absent); the full population was scored.

## Parameters

| Parameter | Value |
| --- | --- |
| alpha | not recorded (`parameters.alpha` is absent) |
| half_life_days | not recorded (`parameters.half_life_days` is absent) |
| bandwidth_hours | not recorded (`parameters.bandwidth_hours` is absent) |
| grid | not recorded (`parameters.grid` is absent) |
| top_k_per_day | 200 |
| bh_denominator | exact per-day scored-event counts recorded by the run |
| bootstrap_resamples | 2,000 |
| bootstrap_seed | 20,260,814 |
| ground_truth_caveat | unlabelled events count as negatives; the corpus has no true negative class (§12.5), so realised FDR is an upper bound |

## Detection at matched budget

| Budget/day | Alerts | True positives | False negatives | Red-team scored | Recall | Precision |
| --- | --- | --- | --- | --- | --- | --- |
| 10 | 70 | 0 | 549 | 549 | 0.0% [0.0, 0.7] | 0.0% [0.0, 5.2] |
| 25 | 175 | 0 | 549 | 549 | 0.0% [0.0, 0.7] | 0.0% [0.0, 2.1] |
| 50 | 350 | 0 | 549 | 549 | 0.0% [0.0, 0.7] | 0.0% [0.0, 1.1] |
| 100 | 700 | 0 | 549 | 549 | 0.0% [0.0, 0.7] | 0.0% [0.0, 0.5] |

## Head-to-head

| Budget/day | Baseline | Framework detected | Baseline detected | Δ (pp) | Ratio |
| --- | --- | --- | --- | --- | --- |
| 10 | eif | 0 | 0 | +0.0 | — |
| 10 | hst | 0 | 0 | +0.0 | — |
| 10 | iforest | 0 | 0 | +0.0 | — |
| 10 | rrcf | 0 | 0 | +0.0 | — |
| 25 | eif | 0 | 0 | +0.0 | — |
| 25 | hst | 0 | 0 | +0.0 | — |
| 25 | iforest | 0 | 0 | +0.0 | — |
| 25 | rrcf | 0 | 0 | +0.0 | — |
| 50 | eif | 0 | 0 | +0.0 | — |
| 50 | hst | 0 | 0 | +0.0 | — |
| 50 | iforest | 0 | 0 | +0.0 | — |
| 50 | rrcf | 0 | 0 | +0.0 | — |
| 100 | eif | 0 | 0 | +0.0 | — |
| 100 | hst | 0 | 0 | +0.0 | — |
| 100 | iforest | 0 | 0 | +0.0 | — |
| 100 | rrcf | 0 | 0 | +0.0 | — |

Where the ratio column shows —, the run records: the baseline detected nothing at this budget, so a relative improvement is a division by zero; the difference is reported in percentage points of recall.

## Calibration

| Procedure | Nominal q | Discoveries | True positives | Realised FDR | Conservatism ratio | Saturated days |
| --- | --- | --- | --- | --- | --- | --- |
| benjamini-hochberg | 0.001 | 1,400 | 0 | 1 [0.997, 1] | 1,000 | 7 |
| benjamini-hochberg | 0.005 | 1,400 | 0 | 1 [0.997, 1] | 200 | 7 |
| benjamini-hochberg | 0.01 | 1,400 | 0 | 1 [0.997, 1] | 100 | 7 |
| benjamini-hochberg | 0.05 | 1,400 | 0 | 1 [0.997, 1] | 20 | 7 |
| benjamini-hochberg | 0.1 | 1,400 | 0 | 1 [0.997, 1] | 10 | 7 |
| benjamini-hochberg | 0.25 | 1,400 | 0 | 1 [0.997, 1] | 4 | 7 |
| benjamini-yekutieli | 0.001 | 1,400 | 0 | 1 [0.997, 1] | 1,000 | 7 |
| benjamini-yekutieli | 0.005 | 1,400 | 0 | 1 [0.997, 1] | 200 | 7 |
| benjamini-yekutieli | 0.01 | 1,400 | 0 | 1 [0.997, 1] | 100 | 7 |
| benjamini-yekutieli | 0.05 | 1,400 | 0 | 1 [0.997, 1] | 20 | 7 |
| benjamini-yekutieli | 0.1 | 1,400 | 0 | 1 [0.997, 1] | 10 | 7 |
| benjamini-yekutieli | 0.25 | 1,400 | 0 | 1 [0.997, 1] | 4 | 7 |

**Warning: rows above record `saturated_days` greater than zero. The discovery count is right-censored by the retention limit, and the realised FDR derived from it is a lower bound on the count, not a measurement.**

## Runtime

The runtime block is not recorded (`runtime` is absent).

---

The command line was not recorded (`run.command` is absent).

Rendered by cmd/runreport from analysis-sample16.json only. A number that does not appear in the result file does not appear here.
