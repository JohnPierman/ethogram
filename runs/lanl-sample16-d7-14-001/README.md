# Run lanl-sample16-d7-14-001

A result of kind `replay`; it claims T5, E1, E2, E3, E9, E4.

## Provenance

| Field | Value |
| --- | --- |
| Run id | lanl-sample16-d7-14-001 |
| Git sha | 8c302f68d4e71d8e179680acad38dfe4ff27f5f7 |
| Git dirty | **DIRTY — the working tree had uncommitted changes when this run was recorded** |
| Started | 2026-08-15T19:38:05Z |
| Finished | 2026-08-15T20:32:58Z |
| Go version | go1.26.3 |
| Wall seconds | 3292.9442453 |
| Events/sec | 2649.5164661389963 |

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

> **WARNING — entity sampling was applied.** One entity in 16 was kept; selector: FNV-1a 64 of the entity identifier, modulo N, equals zero.
>
> a deterministic sample of ENTITIES, not of events: per-entity histories are left whole, and only whole entities are dropped. Every labelled entity is kept regardless of the sample, so the labelled share of this corpus is inflated relative to the full population and a detection rate measured here is NOT comparable to one measured on the full population. The co-occurrence graph and the population marginals are built from the retained entities only
>
> A sampled run's detection rate is not comparable to a full-population one.

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
| novel_pair | 4,866 | 31 |
| novel_value | 8,740 | 215 |
| off_hours | 1,722,918 | 181 |
| population_rare | 7,255 | 0 |
| volume_burst | 1,153,204 | 199 |

Definition, as recorded: structural properties of an event relative to the history it was scored against, derived from verdict evidence and NOT from which detector produced the smallest p-value; a partition defined by our own detectors would flatter the framework by construction

## Runtime

| Figure | Value |
| --- | --- |
| events_per_sec | 2649.5164661389963 |
| gc_percent | 400 |
| graph_edges | 194,886 |
| graph_nodes | 15,391 |
| heap_alloc_mb | 77.07582092285156 |
| heap_sys_mb | 379.6875 |
| novelty_rows | 55,817 |
| timing_entities | 1,488 |
| volume_entities | 1,488 |
| wall_seconds | 3292.9442453 |

---

The command line was not recorded (`run.command` is absent).

Rendered by cmd/runreport from lanl-sample16-d7-14.json only. A number that does not appear in the result file does not appear here.
