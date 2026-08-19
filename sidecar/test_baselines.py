"""End-to-end tests for the baseline sidecar (whitepaper §12.4).

Runnable directly with plain asserts, no pytest:

    python sidecar/test_baselines.py

Builds a synthetic feature CSV (five corpus days of uniform-sample rows plus planted
extreme red-team rows), runs the full pipeline twice with shrunken model sizes, and
checks output shape, detection floor at matched budgets, determinism under a fixed
seed, the EIF axis-parallel-bias case, and the threshold arithmetic.

Two properties beyond that are load-bearing for the comparison rather than for any one
model, and are tested separately:

  * **detections are named, not just counted** — a count cannot be attributed to an
    anomaly category, and the per-category table is what §12.4 exists to produce;
  * **the per-entity baseline is genuinely per-entity, and scores before it observes** —
    an event ordinary for the population but extreme for its own entity must score high,
    and an event must never be part of the history it is judged against.
"""

from __future__ import annotations

import gzip
import json
import os
import shutil
import sys
import tempfile

import numpy as np

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import baselines

HEADER = ("t,entity,hour_frac,dow_frac,src_user_h,dst_user_h,src_comp_h,dst_comp_h,"
          "auth_type_h,logon_type_h,orientation_h,outcome,is_redteam,in_sample")
SAMPLE_ROWS = 20000
RED_ROWS = 50
DAYS = 5

# A pool rather than a distinct entity per row: the per-entity baseline needs a history
# to judge an event against, and a corpus where every entity appears once cannot test it.
# 200 entities over 20,000 sample rows gives each about 100 events of history.
ENTITY_POOL = 200

REQUIRED_TOP_LEVEL_KEYS = {
    "schema_version", "kind", "hypothesis", "run", "started", "finished", "seeds",
    "versions", "input", "parameters", "results", "caveats", "provenance_complete",
    "note",
}
MODELS = ("iforest", "eif", "hst", "rrcf", "lof", "ocsvm", "pca", "entity_ewma")

TEST_PARAMETERS = baselines.ModelParameters(
    iforest_trees=50, iforest_max_samples=256,
    eif_trees=15, eif_subsample=128,
    hst_trees=15, hst_depth=6, hst_window=250,
    rrcf_trees=5, rrcf_tree_size=128, rrcf_thinning=5,
    lof_neighbours=20, lof_fit_cap=4000,
    ocsvm_fit_cap=4000, ocsvm_components=64, ocsvm_nu=0.05,
    pca_components=5,
    ewma_half_life_events=32.0, ewma_min_observations=8,
)


def write_synthetic_features(path: str, rng: np.random.Generator) -> None:
    """Write a synthetic exporter CSV: a tight benign cluster plus extreme red rows.

    Sample rows draw every feature from U(0.45, 0.55); red rows draw every feature
    from U(0.85, 1.0) — beyond the cluster hull in all ten dimensions, which is what
    matched-budget detection demands: at 100 alerts/day against a 1-in-100 sample
    the day threshold sits at roughly the day's top sample score, so a red row must
    out-score essentially every benign row of its day. All-dimensions-extreme also
    avoids isolation-forest hull saturation (splits on a non-extreme dimension would
    otherwise treat a red like a cluster point), and staying below 1.0 keeps the
    reds distinct inside the half-space trees' unit cube. Red rows are planted in
    days 1..4 so none land in the streaming warm-up window.

    Entities are drawn from a fixed pool in time order rather than being unique per
    row, so every entity accumulates roughly SAMPLE_ROWS / ENTITY_POOL events of
    history. A red row is then extreme against both the population and its own entity's
    past, which is what lets one fixture exercise the population models and the
    per-entity one together.
    """
    t_sample = rng.uniform(0, DAYS * baselines.SECONDS_PER_DAY, SAMPLE_ROWS)
    features_sample = rng.uniform(0.45, 0.55, (SAMPLE_ROWS, 10))
    t_red = rng.uniform(1 * baselines.SECONDS_PER_DAY,
                        DAYS * baselines.SECONDS_PER_DAY, RED_ROWS)
    features_red = rng.uniform(0.85, 1.0, (RED_ROWS, 10))

    t_all = np.concatenate([t_sample, t_red])
    features_all = np.vstack([features_sample, features_red])
    is_red = np.concatenate([np.zeros(SAMPLE_ROWS, int), np.ones(RED_ROWS, int)])
    in_sample = np.concatenate([np.ones(SAMPLE_ROWS, int), np.zeros(RED_ROWS, int)])
    order = np.argsort(t_all, kind="stable")

    with gzip.open(path, "wt", encoding="utf-8", newline="\n") as fh:
        fh.write(HEADER + "\n")
        for rank, row in enumerate(order):
            values = ",".join(f"{value:.6f}" for value in features_all[row])
            fh.write(f"{t_all[row]:.3f},e{rank % ENTITY_POOL},{values},"
                     f"{is_red[row]},{in_sample[row]}\n")


def strip_volatile(document: dict) -> str:
    """Canonical JSON of a pipeline document minus timestamps and wall times."""
    clone = json.loads(json.dumps(document))
    clone.pop("started")
    clone.pop("finished")

    def drop_wall_seconds(node: object) -> None:
        if isinstance(node, dict):
            node.pop("wall_seconds", None)
            for value in node.values():
                drop_wall_seconds(value)
        elif isinstance(node, list):
            for value in node:
                drop_wall_seconds(value)

    drop_wall_seconds(clone)
    return json.dumps(clone, sort_keys=True)


def test_output_shape(out_path: str) -> None:
    """Test 1: the output JSON is written, parses, and carries every required key."""
    with open(out_path, encoding="utf-8") as fh:
        document = json.load(fh)
    missing = REQUIRED_TOP_LEVEL_KEYS - set(document)
    assert not missing, f"output JSON missing top-level keys: {sorted(missing)}"
    assert document["schema_version"] == "1"
    assert document["kind"] == "baselines"
    assert document["run"]["run_id"], "run_id must be recorded"
    assert document["provenance_complete"] is True
    for model in MODELS:
        assert model in document["results"], f"results missing model {model}"
        assert "wall_seconds" in document["results"][model]
    print("test_output_shape: ok")


def test_detection_floor(document: dict) -> None:
    """Test 2: every model detects >= 60% of the planted reds at budget 100/day."""
    for model in MODELS:
        block = document["results"][model]["detections_at_budget"]["budget_100_per_day"]
        assert block["red_team_total"] == RED_ROWS, (
            f"{model}: expected {RED_ROWS} red rows in window, "
            f"got {block['red_team_total']}")
        floor = 0.6 * block["red_team_total"]
        assert block["detections"] >= floor, (
            f"{model}: {block['detections']}/{block['red_team_total']} detected at "
            f"budget 100/day, below the 60% floor")
    print("test_detection_floor: ok "
          + ", ".join(f"{m}={document['results'][m]['detections_at_budget']['budget_100_per_day']['detections']}"
                      for m in MODELS))


def test_determinism(first: dict, second: dict) -> None:
    """Test 3: same seed twice gives byte-identical results (volatile keys removed)."""
    assert strip_volatile(first) == strip_volatile(second), (
        "pipeline output differs between two runs with the same seed")
    print("test_determinism: ok")


def test_eif_axis_bias() -> None:
    """Test 4: EIF scores an off-diagonal outlier above the diagonal cluster median.

    A diagonal cluster with an off-diagonal outlier is the axis-parallel-bias case
    Extended Isolation Forest exists to fix (Hariri et al. [2]): axis-parallel cuts
    leave ghost regions along both marginals, while extended (oblique) cuts isolate
    the off-diagonal point quickly.
    """
    rng = np.random.default_rng(3)
    diagonal = rng.uniform(0.0, 1.0, 800)
    cluster = np.stack([diagonal, diagonal], axis=1) + rng.normal(0.0, 0.01, (800, 2))
    outlier = np.array([[0.9, 0.1]])
    matrix = np.vstack([cluster, outlier])
    fit_mask = np.ones(len(matrix), dtype=bool)
    scores = baselines.score_eif(matrix, fit_mask, seed=5, trees=60, subsample=128)
    cluster_median = float(np.median(scores[:-1]))
    assert scores[-1] > cluster_median, (
        f"EIF outlier score {scores[-1]:.4f} not above cluster median "
        f"{cluster_median:.4f}")
    print("test_eif_axis_bias: ok")


def test_events_named(document: dict) -> None:
    """Test 5: every detection is named, so it can be attributed to a category.

    A baseline that records only how many red-team events it caught cannot appear in the
    per-category comparison at all — the table renders "recorded its detections as a
    count without naming the events". The identities are what make §12.4's central table
    computable, so their presence is asserted rather than assumed.
    """
    for model in MODELS:
        for budget_key, block in document["results"][model][
                "detections_at_budget"].items():
            assert block["events_named"] is True, f"{model} {budget_key}: not named"
            named = block["detected_events"]
            assert len(named) == block["detections"], (
                f"{model} {budget_key}: {len(named)} named against "
                f"{block['detections']} counted")
            for event in named:
                assert isinstance(event["t"], int) and isinstance(event["entity"], str)
            keys = [(event["t"], event["entity"]) for event in named]
            assert keys == sorted(keys), f"{model} {budget_key}: not in a total order"
            assert len(set(keys)) == len(keys), f"{model} {budget_key}: duplicate event"
    print("test_events_named: ok")


def test_entity_ewma_is_per_entity() -> None:
    """Test 6: an event ordinary for the population but extreme for its own entity.

    This is the whole point of the baseline, and the property no other model here has.
    Two entities hold opposite but individually stable habits; the probe takes the value
    that is *normal for the population as a whole* and *unprecedented for the entity that
    produced it*. A population model cannot express the difference; this one must.
    """
    width = 10
    rows, entities = [], []
    for step in range(200):
        rows.append(np.full(width, 0.20))
        entities.append("low")
        rows.append(np.full(width, 0.80))
        entities.append("high")
    # 0.80 is unremarkable in the pooled cloud - "high" has produced it 200 times - but
    # "low" has never been near it.
    rows.append(np.full(width, 0.80))
    entities.append("low")

    scores = baselines.score_entity_ewma(
        np.array(rows), np.array(entities, dtype=object),
        half_life_events=32.0, min_observations=8)

    probe = scores[-1]
    habitual = float(np.max(scores[:-1]))
    assert probe > habitual, (
        f"the entity-relative probe scored {probe:.4f}, not above the most extreme "
        f"habitual event at {habitual:.4f}: the baseline is not per-entity")
    print(f"test_entity_ewma_is_per_entity: ok (probe {probe:.1f} > habitual "
          f"{habitual:.1f})")


def test_entity_ewma_scores_before_observing() -> None:
    """Test 7: an event is never part of the history it is judged against.

    §5.2 warns that violating this is silent — novelty dies while the numbers stay
    plausible. The baseline is held to the rule even though nothing obliges it to be,
    because a baseline that cheats makes the comparison meaningless in the framework's
    favour. Asserted two ways: a cold-start entity scores 0, and a repeated departure
    scores strictly lower the second time, which can only happen if the first was
    absorbed into state after being scored.
    """
    width = 3
    stable = [np.full(width, 0.5) for _ in range(40)]
    matrix = np.array(stable + [np.full(width, 0.9), np.full(width, 0.9)])
    entity = np.array(["u"] * len(matrix), dtype=object)
    scores = baselines.score_entity_ewma(matrix, entity, half_life_events=8.0,
                                         min_observations=8)

    assert scores[0] == 0.0, "an entity with no history must not score as anomalous"
    assert (scores[:8] == 0.0).all(), "cold start must hold until min_observations"
    first, second = scores[-2], scores[-1]
    assert first > 0.0, "the first departure must score above zero"
    assert second < first, (
        f"the repeat scored {second:.4f}, not below the first departure's {first:.4f}: "
        "state was not updated after scoring, or was updated before it")

    # A one-event entity can never be scored against itself.
    lone = baselines.score_entity_ewma(
        np.array([np.full(width, 0.9)]), np.array(["v"], dtype=object),
        half_life_events=8.0, min_observations=8)
    assert lone[0] == 0.0, "a first-ever event must not be scored against itself"
    print("test_entity_ewma_scores_before_observing: ok")


def test_unrun_models_are_absent(features_path: str, workdir: str) -> None:
    """Test 8: a model that did not run is absent, never recorded as zero.

    The same rule the report renderer enforces with its NOT RUN card. A model listed
    with zero detections is a measurement; a model that never ran is not, and collapsing
    the two would let an unrun baseline read as a beaten one.
    """
    selected = ("pca", "entity_ewma")
    document = baselines.run_pipeline(
        features_path, os.path.join(workdir, "subset.json"),
        run_id="baselines-test-subset", seed=42, budgets=[100],
        day_from=0, day_to=DAYS, scored_events=None, sample_rate=100,
        models=selected, parameters=TEST_PARAMETERS)

    assert set(document["results"]) == set(selected), (
        f"results carry {sorted(document['results'])}, want {sorted(selected)}")
    assert document["parameters"]["models_run"] == list(selected)
    assert set(document["parameters"]["models"]) == set(selected), (
        "hyperparameters recorded for a model that did not run")
    assert any("models not run" in caveat for caveat in document["caveats"]), (
        "skipped models must be named in the caveats, not left to be inferred")
    for name in selected:
        assert document["results"][name]["scope"] in ("population", "per-entity")
        assert document["results"][name]["family"]
    print("test_unrun_models_are_absent: ok")


def test_threshold_arithmetic() -> None:
    """Test 5: threshold arithmetic on a hand-built day of 1000 known scores.

    1000 sample rows estimate a population of N_est = 1000 x 100 = 100000; budget 10
    puts the threshold at q = 1 - 10/100000 = 0.9999. With scores 0..999 the linear
    interpolation lands at 0.9999 x 999 = 998.9001.
    """
    scores = np.arange(1000, dtype=np.float64)
    threshold = baselines.alert_threshold(scores, budget=10, population_multiplier=100)
    expected_quantile = 1.0 - 10 / 100000
    assert threshold == float(np.quantile(scores, expected_quantile)), (
        "threshold does not equal the quantile at 1 - b/N_est")
    assert abs(threshold - 998.9001) < 1e-6, f"threshold {threshold!r} != 998.9001"
    assert baselines.alert_threshold(np.array([1.0]), budget=100,
                                     population_multiplier=1) == float("-inf"), (
        "budget >= estimated population must alert on everything")
    print("test_threshold_arithmetic: ok")


def main() -> None:
    workdir = tempfile.mkdtemp(prefix="baselines-test-")
    try:
        features_path = os.path.join(workdir, "features.csv.gz")
        write_synthetic_features(features_path, np.random.default_rng(7))

        first = baselines.run_pipeline(
            features_path, os.path.join(workdir, "run1.json"),
            run_id="baselines-test-1", seed=42, budgets=[10, 100],
            day_from=0, day_to=DAYS, scored_events=2_000_000,
            sample_rate=100, models=MODELS, parameters=TEST_PARAMETERS)
        test_output_shape(os.path.join(workdir, "run1.json"))
        test_detection_floor(first)
        test_events_named(first)

        second = baselines.run_pipeline(
            features_path, os.path.join(workdir, "run2.json"),
            run_id="baselines-test-1", seed=42, budgets=[10, 100],
            day_from=0, day_to=DAYS, scored_events=2_000_000,
            sample_rate=100, models=MODELS, parameters=TEST_PARAMETERS)
        test_determinism(first, second)

        test_eif_axis_bias()
        test_entity_ewma_is_per_entity()
        test_entity_ewma_scores_before_observing()
        test_unrun_models_are_absent(features_path, workdir)
        test_threshold_arithmetic()
        print("ALL TESTS PASSED")
    finally:
        shutil.rmtree(workdir, ignore_errors=True)


if __name__ == "__main__":
    main()
