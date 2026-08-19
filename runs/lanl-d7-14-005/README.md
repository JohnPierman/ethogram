# Run lanl-d7-14-005

A result of kind `replay`; it claims T5, E1, E2, E3, E9, E4.

## Provenance

| Field | Value |
| --- | --- |
| Run id | lanl-d7-14-005 |
| Git sha | f938969f64e1331c545ce692699c0fbfdd82fa03 |
| Git dirty | **DIRTY — the working tree had uncommitted changes when this run was recorded** |
| Started | 2026-08-15T14:15:24Z |
| Finished | 2026-08-15T18:24:05Z |
| Go version | go1.26.3 |
| Wall seconds | 14920.4575582 |
| Events/sec | 5324.0436957205 |

## Corpus

| File | sha256 (truncated to twelve characters) |
| --- | --- |
| auth.txt.gz | 9c6b0cc261b0… |
| redteam.txt.gz | 606635837c68… |

Digests are truncated for the page; result.json beside this report carries them in full.

| Figure | Value |
| --- | --- |
| Rows read | 239,471,570 |
| Events warmed (burn-in) | 37,218,638 |
| Events scored | 42,218,530 |
| Events skipped | 0 |
| Row errors | 0 |

Burn-in ends at 604,800 seconds of corpus time, fixed at commit `24c5a53`.
Coverage, as recorded: corpus days 7.00 to 14.00 scored (burn-in days 0 to 7.00).

## Sampling

No entity sampling block is recorded (`parameters.entity_sample` is absent); the full population was scored.

## Parameters

| Parameter | Value |
| --- | --- |
| alpha | 1 |
| half_life_days | 7 |
| bandwidth_hours | 1.5 |
| grid | 512 |
| top_k_per_day | 200 |

## Anomaly categories

| Category | Scored events | Red-team events |
| --- | --- | --- |
| novel_pair | 25,409 | 29 |
| novel_value | 75,077 | 215 |
| off_hours | 16,210,028 | 181 |
| population_rare | 63,830 | 0 |
| volume_burst | 14,955,042 | 199 |

Definition, as recorded: structural properties of an event relative to the history it was scored against, derived from verdict evidence and NOT from which detector produced the smallest p-value; a partition defined by our own detectors would flatter the framework by construction

## Runtime

| Figure | Value |
| --- | --- |
| events_per_sec | 5324.0436957205 |
| gc_percent | 400 |
| graph_edges | 1,726,049 |
| graph_nodes | 78,999 |
| heap_alloc_mb | 1420.2282028198242 |
| heap_sys_mb | 3275.625 |
| novelty_rows | 672,902 |
| timing_entities | 22,588 |
| volume_entities | 22,588 |
| wall_seconds | 14920.4575582 |

---

The command line was not recorded (`run.command` is absent).

Rendered by cmd/runreport from lanl-d7-14.json only. A number that does not appear in the result file does not appear here.
