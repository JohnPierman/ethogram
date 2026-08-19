"""Baseline anomaly detectors compared at matched alert budgets (whitepaper §12.4).

Eight baselines, in two groups that answer two different questions.

**Population-scope models** embodying the "standard formulation" of §3 — one fixed
numeric feature vector per event, no per-entity state. These test *breadth of
technique*: if the framework's advantage is real it should survive against several
families, and if they all score zero the "attacks are per-entity anomalies" explanation
is strengthened rather than assumed.

    isolation family      Isolation Forest [1], Extended Isolation Forest [2],
                          streaming Half-Space Trees [3], Robust Random Cut Forest [4]
    density               Local Outlier Factor [5]
    boundary              One-Class SVM [6], via the Nystroem + SGD approximation that
                          makes it tractable at this row count
    linear subspace       PCA reconstruction error [7]

**A per-entity model**, `entity_ewma`, which is the decisive comparison and the one the
other seven cannot make. Every model above compares an event to the population; the
framework compares an entity to itself. The measured gap between them therefore
confounds two claims — that the per-entity *framing* wins, and that this framework's
calibration, abstention and combination add value on top of that framing. A naive
per-entity EWMA z-score shares the framing and has none of the machinery, so it
separates the two. It may well not flatter the framework; a baseline that makes the
question testable is worth more than one that cannot.

The baselines are not part of the framework: they are free to use RNGs (R2/R4 do not
bind them, §12.4), but every seed and library version is recorded in the output so a run
can be reproduced exactly.

Input:  the gzipped feature CSV produced by the Go exporter — an entity column, ten
        numeric feature columns (hour_frac..outcome, with -1 encoding a missing value),
        is_redteam and in_sample flags, rows sorted by t ascending. in_sample marks the
        exporter's deterministic uniform sample; a row may be both sampled and red-team.
Output: a baselines JSON with per-model detections at each alert budget, **the identity
        of every red-team event detected**, score percentile summaries, wall times and
        full provenance (seeds, versions, input checksum), so the §12.4 comparison table
        and its per-category breakdown can be regenerated from it alone.

Naming the detected events matters and was absent before: without it a baseline's
detections are a count that cannot be attributed to a category, and the per-category
comparison — the table that makes an advantage attributable to a *kind* of anomaly
rather than asserted in aggregate — is unmeasurable by construction.

Matched-budget detection: for each corpus day d the alert threshold for a budget of b
alerts/day is the score quantile of that day's uniform-sample rows at position
q_d = 1 - b / N_d, where N_d = (day's sample count) x multiplier reconstructs the day's
true population from the sample. If b >= N_d the threshold is -inf (everything alerts).
A red-team row counts as detected at budget b when its score reaches its own day's
threshold. Red-team rows outside the sample never influence a threshold.

**The per-entity baseline needs whole entities, not a uniform event sample.** A 1-in-100
uniform sample over events decimates every entity's history, so `entity_ewma` measured
on such an export is handicapped in a way the population models are not. Run it against
an export whose sampling is over *entities* (`cmd/subset -entity-sample`, then
`cmd/features -sample-mod 1`), where per-entity histories are left whole. The output
records which regime the input was in; `entity_history_intact` is False when it was not.

Every model is normalised to "higher score = more anomalous"; where a library or
formulation scores the other way the sign is flipped internally and documented at the
point of the flip.

References:
[1] F. T. Liu, K. M. Ting and Z.-H. Zhou, "Isolation Forest", ICDM 2008.
[2] S. Hariri, M. Carrasco Kind and R. J. Brunner, "Extended Isolation Forest",
    IEEE Transactions on Knowledge and Data Engineering 33(4), 2021.
[3] S. C. Tan, K. M. Ting and T. F. Liu, "Fast Anomaly Detection for Streaming
    Data", IJCAI 2011.
[4] S. Guha, N. Mishra, G. Roy and O. Schrijvers, "Robust Random Cut Forest Based
    Anomaly Detection on Streams", ICML 2016.
[5] M. M. Breunig, H.-P. Kriegel, R. T. Ng and J. Sander, "LOF: Identifying
    Density-Based Local Outliers", SIGMOD 2000.
[6] B. Scholkopf, J. C. Platt, J. Shawe-Taylor, A. J. Smola and R. C. Williamson,
    "Estimating the Support of a High-Dimensional Distribution", Neural Computation
    13(7), 2001.
[7] M.-L. Shyu, S.-C. Chen, K. Sarinnapakorn and L. Chang, "A Novel Anomaly Detection
    Scheme Based on Principal Component Classifier", ICDM Foundations Workshop, 2003.
"""

from __future__ import annotations

import argparse
import hashlib
import importlib.metadata
import io
import json
import math
import os
import sys
import time
import warnings
from dataclasses import dataclass
from datetime import datetime, timezone

import numpy as np
import pandas as pd
from sklearn.decomposition import PCA
from sklearn.ensemble import IsolationForest
from sklearn.kernel_approximation import Nystroem
from sklearn.linear_model import SGDOneClassSVM
from sklearn.neighbors import LocalOutlierFactor

with warnings.catch_warnings():
    # rrcf 0.4.4 still imports pkg_resources; silence that deprecation noise on
    # import without masking warnings raised by our own code afterwards.
    warnings.filterwarnings("ignore", category=DeprecationWarning)
    warnings.filterwarnings("ignore", category=UserWarning)
    import rrcf

FEATURE_COLUMNS = [
    "hour_frac", "dow_frac", "src_user_h", "dst_user_h", "src_comp_h",
    "dst_comp_h", "auth_type_h", "logon_type_h", "orientation_h", "outcome",
]
SECONDS_PER_DAY = 86400
DEFAULT_SAMPLE_RATE = 100  # the exporter's default 1-in-100 uniform sample
EULER_MASCHERONI = 0.5772156649
MISSING = -1.0  # the exporter's encoding for "this field was absent from the event"

# Rows per block where a model lifts its input into a higher-dimensional space before
# scoring. Bounds peak memory without touching any arithmetic: the maps involved are
# row-wise, so a chunked pass and a whole-corpus pass agree exactly.
SCORING_CHUNK = 100_000

# Per-model seeds are derived from the global seed by fixed offsets so the recorded
# seeds block is self-explanatory and each model's RNG stream is independent.
MODEL_SEED_OFFSETS = {
    "iforest": 1, "eif": 2, "hst": 3, "rrcf": 4,
    "lof": 5, "ocsvm": 6, "pca": 7, "entity_ewma": 8,
}

# Which question each model answers. The dashboard groups on this, because a per-entity
# baseline scoring well says something entirely different from a population one doing so.
MODEL_SCOPE = {
    "iforest": "population", "eif": "population", "hst": "population",
    "rrcf": "population", "lof": "population", "ocsvm": "population",
    "pca": "population", "entity_ewma": "per-entity",
}

MODEL_FAMILY = {
    "iforest": "isolation", "eif": "isolation", "hst": "isolation",
    "rrcf": "isolation", "lof": "density", "ocsvm": "boundary",
    "pca": "linear subspace", "entity_ewma": "per-entity moment",
}

ALL_MODELS = tuple(MODEL_SEED_OFFSETS)

# rrcf costs about two hours at this row count where every other model costs minutes, so
# it is excluded unless asked for by name. Recorded in the output either way.
DEFAULT_MODELS = tuple(m for m in ALL_MODELS if m != "rrcf")

NOTE = ("baselines are not part of the framework and are not held to R2/R4 (§12.4). "
        "Detector IV is a population-marginal estimator, NOT an isolation forest: the "
        "§12.4 comparison is between independent models, not framework-against-its-own-"
        "component. The category census is the sharper statement of why they differ — "
        "no labelled event is a population-marginal outlier, which is a property of the "
        "attacks rather than of any model. Seven of the eight models here are "
        "population-scope and share that blind spot by construction; entity_ewma is the "
        "one that does not, and it is the baseline the framework has to beat to show "
        "that its calibration machinery earns its place above the per-entity framing "
        "alone")


@dataclass(frozen=True)
class ModelParameters:
    """Hyperparameters for the eight baselines (§12.4 defaults; tests shrink them)."""

    iforest_trees: int = 100
    iforest_max_samples: int = 256
    eif_trees: int = 100
    eif_subsample: int = 256
    hst_trees: int = 25
    hst_depth: int = 8
    hst_window: int = 250
    rrcf_trees: int = 40
    rrcf_tree_size: int = 256
    rrcf_thinning: int = 5
    # LOF and One-Class SVM are superlinear in the fit-set size, so both fit on a
    # deterministic random subsample of the sample rows and score every row. The cap is
    # recorded in the output; it is a tractability bound, not a tuning choice.
    lof_neighbours: int = 20
    lof_fit_cap: int = 50_000
    ocsvm_fit_cap: int = 50_000
    ocsvm_components: int = 200
    ocsvm_nu: float = 0.05
    pca_components: int = 5
    # Half-life in events, not seconds: a naive baseline has no notion of corpus time,
    # which is part of what distinguishes it from §6.2's lazy decay on event timestamps.
    ewma_half_life_events: float = 32.0
    ewma_min_observations: int = 8


@dataclass(frozen=True)
class FeatureSet:
    """The exporter's feature CSV, loaded into arrays plus its provenance checksum."""

    matrix: np.ndarray      # (rows, 10) float64, -1 encodes missing
    t: np.ndarray           # (rows,) int64 corpus seconds; half of an event key
    entity: np.ndarray      # (rows,) object; the other half of an event key
    days: np.ndarray        # (rows,) int64 corpus day, floor(t / 86400)
    is_redteam: np.ndarray  # (rows,) bool
    in_sample: np.ndarray   # (rows,) bool
    sha256: str             # of the gzipped bytes as stored on disk


@dataclass(frozen=True)
class ModelEvaluation:
    """One model's scores over the rows it scored, ready for budget evaluation."""

    days: np.ndarray
    t: np.ndarray
    entity: np.ndarray
    scores: np.ndarray           # higher = more anomalous
    sample_mask: np.ndarray      # rows eligible for threshold estimation
    red_mask: np.ndarray
    population_multiplier: int   # sample count x this estimates the true population
    wall_seconds: float


def load_features(path: str) -> FeatureSet:
    """Read the gzipped feature CSV; hash the gzipped bytes for provenance.

    The entity column is loaded because a detection has to be *named* to be attributed
    to an anomaly category. A baseline that records only how many red-team events it
    caught cannot be compared per category, which is the comparison §12.4 exists to make.
    """
    with open(path, "rb") as fh:
        raw = fh.read()
    frame = pd.read_csv(
        io.BytesIO(raw), compression="gzip",
        usecols=["t", "entity"] + FEATURE_COLUMNS + ["is_redteam", "in_sample"],
    )
    seconds = frame["t"].to_numpy(dtype=np.float64)
    return FeatureSet(
        matrix=frame[FEATURE_COLUMNS].to_numpy(dtype=np.float64),
        t=np.floor(seconds).astype(np.int64),
        entity=frame["entity"].to_numpy(dtype=object),
        days=np.floor(seconds / SECONDS_PER_DAY).astype(np.int64),
        is_redteam=frame["is_redteam"].to_numpy() != 0,
        in_sample=frame["in_sample"].to_numpy() != 0,
        sha256=hashlib.sha256(raw).hexdigest(),
    )


def entity_history_intact(features: FeatureSet) -> bool:
    """True when the export kept whole entities rather than a uniform event sample.

    A uniform 1-in-N sample over *events* decimates every entity's history, which
    handicaps the per-entity baseline in a way the population models do not feel. The
    exporter marks such rows with in_sample; when every row is sampled the file was
    written with -sample-mod 1 and per-entity histories are whole.
    """
    return bool(features.in_sample.all())


# --------------------------------------------------------------------------- iforest

def score_iforest(matrix: np.ndarray, fit_mask: np.ndarray, seed: int,
                  trees: int, max_samples: int) -> np.ndarray:
    """Isolation Forest [1]: fit on the uniform-sample rows, score every row.

    sklearn's score_samples is "lower = more abnormal"; the sign is flipped here so
    higher = more anomalous, matching every other model in this module.
    """
    fit_points = matrix[fit_mask]
    model = IsolationForest(
        n_estimators=trees,
        max_samples=min(max_samples, len(fit_points)),
        random_state=seed,
    )
    model.fit(fit_points)
    return -model.score_samples(matrix)


# ------------------------------------------------------------------------------- eif

@dataclass(frozen=True)
class EifTree:
    """One extended isolation tree flattened into arrays for vectorised routing.

    Leaves are self-loops (left == right == own index) with a zero normal, so routing
    all points a fixed height_limit number of steps parks every point on its leaf.
    """

    normals: np.ndarray     # (nodes, d) random hyperplane normals; zero at leaves
    intercepts: np.ndarray  # (nodes,)
    left: np.ndarray        # (nodes,) child index for x.n < p
    right: np.ndarray       # (nodes,)
    depth: np.ndarray       # (nodes,)
    leaf_size: np.ndarray   # (nodes,) points that settled at a leaf; 0 for internal


def expected_path_length(sizes: np.ndarray) -> np.ndarray:
    """c(n) = 2H(n-1) - 2(n-1)/n, H(i) = ln(i) + Euler-Mascheroni; c(2)=1, c(<=1)=0."""
    sizes = np.asarray(sizes, dtype=np.float64)
    out = np.zeros_like(sizes)
    out[sizes == 2.0] = 1.0
    big = sizes > 2.0
    n = sizes[big]
    out[big] = 2.0 * (np.log(n - 1.0) + EULER_MASCHERONI) - 2.0 * (n - 1.0) / n
    return out


def _build_eif_tree(points: np.ndarray, rng: np.random.Generator,
                    height_limit: int) -> EifTree:
    """Grow one tree per Hariri et al. [2] at full extension level (d-1).

    Each internal node draws a normal n ~ N(0,1)^d and an intercept p uniform on the
    node's data projected onto n; the split is x.n < p. Degenerate nodes (all points
    project identically, or one side would be empty) become leaves early.
    """
    dimensions = points.shape[1]
    normals: list[np.ndarray] = []
    intercepts: list[float] = []
    left: list[int] = []
    right: list[int] = []
    depth: list[int] = []
    leaf_size: list[int] = []

    def make_leaf(count: int, level: int) -> int:
        index = len(depth)
        normals.append(np.zeros(dimensions))
        intercepts.append(0.0)
        left.append(index)
        right.append(index)
        depth.append(level)
        leaf_size.append(count)
        return index

    def grow(subset: np.ndarray, level: int) -> int:
        if len(subset) <= 1 or level >= height_limit:
            return make_leaf(len(subset), level)
        normal = rng.standard_normal(dimensions)
        projection = subset @ normal
        low, high = float(projection.min()), float(projection.max())
        if low == high:
            return make_leaf(len(subset), level)
        intercept = float(rng.uniform(low, high))
        below = projection < intercept
        if below.all() or not below.any():
            return make_leaf(len(subset), level)
        index = len(depth)
        normals.append(normal)
        intercepts.append(intercept)
        left.append(-1)
        right.append(-1)
        depth.append(level)
        leaf_size.append(0)
        left[index] = grow(subset[below], level + 1)
        right[index] = grow(subset[~below], level + 1)
        return index

    grow(points, 0)
    return EifTree(
        normals=np.asarray(normals), intercepts=np.asarray(intercepts),
        left=np.asarray(left), right=np.asarray(right),
        depth=np.asarray(depth), leaf_size=np.asarray(leaf_size),
    )


def _eif_route(matrix: np.ndarray, tree: EifTree, height_limit: int) -> np.ndarray:
    """Vectorised routing of all points through one tree, level by level.

    Returns per-point path lengths: depth of the leaf reached plus the standard
    c(leaf_size) extension for the unbuilt subtree below it.
    """
    current = np.zeros(len(matrix), dtype=np.int64)
    for _ in range(height_limit):
        below = (np.einsum("ij,ij->i", matrix, tree.normals[current])
                 < tree.intercepts[current])
        current = np.where(below, tree.left[current], tree.right[current])
    return tree.depth[current] + expected_path_length(tree.leaf_size[current])


def score_eif(matrix: np.ndarray, fit_mask: np.ndarray, seed: int,
              trees: int, subsample: int) -> np.ndarray:
    """Extended Isolation Forest [2], pure numpy, full extension level.

    Fit on the uniform-sample rows, score every row. The anomaly score is the
    standard s(x) = 2^(-E[path length] / c(subsample)); higher = more anomalous.
    """
    rng = np.random.default_rng(seed)
    fit_points = matrix[fit_mask]
    actual = min(subsample, len(fit_points))
    height_limit = int(math.ceil(math.log2(max(actual, 2))))
    path_sum = np.zeros(len(matrix))
    for _ in range(trees):
        chosen = rng.choice(len(fit_points), size=actual, replace=False)
        tree = _build_eif_tree(fit_points[chosen], rng, height_limit)
        path_sum += _eif_route(matrix, tree, height_limit)
    normaliser = float(expected_path_length(np.array([actual]))[0])
    if normaliser <= 0.0:
        normaliser = 1.0  # degenerate fit set; keep the score finite
    return np.power(2.0, -(path_sum / trees) / normaliser)


# ------------------------------------------------------------------------------- hst

def _build_hst_tree(rng: np.random.Generator, dimensions: int,
                    depth: int) -> tuple[np.ndarray, np.ndarray]:
    """Build one half-space tree per Tan, Ting and Liu [3].

    The work range per dimension comes from a random perturbation s_q ~ U(0,1):
    [s_q - 2 max(s_q, 1-s_q), s_q + 2 max(s_q, 1-s_q)]. Each node splits a random
    dimension at the midpoint of its work range; child ranges are the two halves.
    The structure is a complete binary tree in heap layout (children of i at 2i+1,
    2i+2) so leaf routing can be pure index arithmetic.
    """
    perturbation = rng.uniform(size=dimensions)
    span = 2.0 * np.maximum(perturbation, 1.0 - perturbation)
    internal = 2 ** depth - 1
    total = 2 ** (depth + 1) - 1
    node_min = np.empty((total, dimensions))
    node_max = np.empty((total, dimensions))
    node_min[0] = perturbation - span
    node_max[0] = perturbation + span
    split_dim = np.zeros(internal, dtype=np.int64)
    split_val = np.zeros(internal)
    for node in range(internal):
        q = int(rng.integers(dimensions))
        mid = 0.5 * (node_min[node, q] + node_max[node, q])
        split_dim[node] = q
        split_val[node] = mid
        for child in (2 * node + 1, 2 * node + 2):
            node_min[child] = node_min[node]
            node_max[child] = node_max[node]
        node_max[2 * node + 1, q] = mid
        node_min[2 * node + 2, q] = mid
    return split_dim, split_val


def _hst_leaf_indices(points: np.ndarray, split_dim: np.ndarray,
                      split_val: np.ndarray, depth: int) -> np.ndarray:
    """Vectorised leaf index of every point in one half-space tree."""
    rows = np.arange(len(points))
    node = np.zeros(len(points), dtype=np.int64)
    for _ in range(depth):
        below = points[rows, split_dim[node]] < split_val[node]
        node = 2 * node + np.where(below, 1, 2)
    return (node - (2 ** depth - 1)).astype(np.int32)


def score_hst(matrix: np.ndarray, seed: int, trees: int, depth: int,
              window: int) -> np.ndarray:
    """Streaming Half-Space Trees [3] over the full row stream in time order.

    Features are clipped to the unit cube (missing values, encoded -1, clip to 0).
    Each arriving point is scored from the reference-window mass BEFORE it is
    inserted into the latest window; windows swap every `window` arrivals. Because
    tree structure is fixed and the reference mass is frozen within a window, whole
    windows are scored and inserted as numpy batches — exactly equivalent to the
    point-at-a-time loop.

    Sign convention: [3] treats low mass as anomalous, so the internal score
    sum_trees(mass(leaf) x 2^depth) is negated here to give higher = more anomalous.
    Scoring reads the leaf at maximum depth; the paper's early size-limit
    termination is an efficiency device that does not change the ranking at these
    window sizes. Points in the first window are scored against an empty reference
    (mass 0, the most anomalous score) — the warm-up transient of the streaming
    formulation.
    """
    rng = np.random.default_rng(seed)
    points = np.clip(matrix, 0.0, 1.0)
    count = len(points)
    leaf = np.empty((trees, count), dtype=np.int32)
    for index in range(trees):
        split_dim, split_val = _build_hst_tree(rng, points.shape[1], depth)
        leaf[index] = _hst_leaf_indices(points, split_dim, split_val, depth)
    reference = np.zeros((trees, 2 ** depth))
    weight = float(2 ** depth)
    scores = np.empty(count)
    for start in range(0, count, window):
        stop = min(start + window, count)
        latest = np.zeros_like(reference)
        mass = np.zeros(stop - start)
        for index in range(trees):
            block = leaf[index, start:stop]
            mass += reference[index, block]
            np.add.at(latest[index], block, 1.0)
        scores[start:stop] = -(mass * weight)
        if stop - start == window:
            reference = latest
    return scores


# ------------------------------------------------------------------------------ rrcf

def rrcf_thinned_masks(in_sample: np.ndarray, is_redteam: np.ndarray,
                       thinning: int) -> tuple[np.ndarray, np.ndarray]:
    """Deterministic 1-in-`thinning` thinning of the sample rows for rrcf.

    Keeps sample rows whose ordinal within the sample subset satisfies
    ordinal % thinning == 0, plus ALL red-team rows. Returns (kept rows, rows that
    count as sample rows for rrcf threshold estimation) — the latter is the thinned
    sample only, so rows kept purely for being red-team never shape a threshold.
    """
    ordinal = np.cumsum(in_sample) - 1
    thinned_sample = in_sample & ((ordinal % thinning) == 0)
    return thinned_sample | is_redteam, thinned_sample


def score_rrcf(points: np.ndarray, seed: int, trees: int, tree_size: int) -> np.ndarray:
    """Robust Random Cut Forest [4] via the rrcf package, streaming CoDisp.

    Points are inserted in time order with index eviction (the oldest point is
    forgotten once the tree holds tree_size points); the score is the average CoDisp
    across trees at insertion time. CoDisp is already higher = more anomalous.
    """
    forest = [rrcf.RCTree(random_state=seed + offset) for offset in range(trees)]
    scores = np.empty(len(points))
    for index, point in enumerate(points):
        total = 0.0
        for tree in forest:
            if index >= tree_size:
                tree.forget_point(index - tree_size)
            tree.insert_point(point, index=index)
            total += tree.codisp(index)
        scores[index] = total / len(forest)
    return scores


# ------------------------------------------------------------------------------- lof

def fit_subsample(fit_mask: np.ndarray, cap: int, seed: int) -> np.ndarray:
    """Row indices of a deterministic subsample of the fit set, capped at `cap`.

    LOF and One-Class SVM are superlinear in the fit-set size and intractable at the
    full row count. Capping is a tractability bound rather than a tuning choice, so it
    is drawn once, recorded, and shared by both models' documentation.
    """
    eligible = np.flatnonzero(fit_mask)
    if len(eligible) <= cap:
        return eligible
    rng = np.random.default_rng(seed)
    return np.sort(rng.choice(eligible, size=cap, replace=False))


def score_lof(matrix: np.ndarray, fit_mask: np.ndarray, seed: int,
              neighbours: int, fit_cap: int) -> np.ndarray:
    """Local Outlier Factor [5] in novelty mode: fit on the sample, score every row.

    LOF is the density family's standard representative and answers a different question
    from the isolation ensembles: not "how few cuts isolate this point" but "is this
    point in a sparser neighbourhood than its own neighbours are". A point can sit well
    inside the global hull and still be locally isolated, which is the case isolation
    forests are weakest on.

    sklearn's score_samples is the negative LOF, so "lower = more abnormal"; the sign is
    flipped here to match every other model in this module.
    """
    chosen = fit_subsample(fit_mask, fit_cap, seed)
    model = LocalOutlierFactor(
        n_neighbors=min(neighbours, max(len(chosen) - 1, 1)), novelty=True)
    model.fit(matrix[chosen])
    return -model.score_samples(matrix)


# ----------------------------------------------------------------------------- ocsvm

def score_ocsvm(matrix: np.ndarray, fit_mask: np.ndarray, seed: int, fit_cap: int,
                components: int, nu: float) -> np.ndarray:
    """One-Class SVM [6] via the Nystroem + SGD approximation, scoring every row.

    The exact kernel SVM is O(n^2) in the fit set and cannot be run at this row count.
    sklearn's documented scalable substitute is a Nystroem kernel map followed by
    SGDOneClassSVM, which approximates the same decision function in linear time. That
    is a deviation from the textbook formulation of [6] and is recorded as one: the
    output's parameters block names the approximation rather than claiming an exact SVM.

    decision_function is "higher = more normal"; the sign is flipped.

    Scoring is chunked: the Nystroem map lifts each row to `components` dimensions, so
    transforming the whole corpus at once would allocate rows x components floats — some
    gigabytes at corpus row counts, for an intermediate that is consumed immediately.
    Chunking changes no arithmetic, since the map and the decision function are both
    row-wise.
    """
    chosen = fit_subsample(fit_mask, fit_cap, seed)
    fit_points = matrix[chosen]
    mapping = Nystroem(gamma=1.0 / matrix.shape[1],
                       n_components=min(components, len(fit_points)),
                       random_state=seed)
    mapped = mapping.fit_transform(fit_points)
    model = SGDOneClassSVM(nu=nu, random_state=seed)
    model.fit(mapped)

    scores = np.empty(len(matrix))
    for start in range(0, len(matrix), SCORING_CHUNK):
        stop = min(start + SCORING_CHUNK, len(matrix))
        block = mapping.transform(matrix[start:stop])
        scores[start:stop] = -model.decision_function(block)
    return scores


# ------------------------------------------------------------------------------- pca

def score_pca(matrix: np.ndarray, fit_mask: np.ndarray, seed: int,
              components: int) -> np.ndarray:
    """PCA reconstruction error [7]: fit the subspace on the sample, score every row.

    The linear-subspace family, and the closest of these baselines to what most security
    tooling actually ships. An event is anomalous when it lies far from the principal
    subspace the bulk of traffic occupies; the score is the squared reconstruction
    residual, which is already higher = more anomalous.
    """
    fit_points = matrix[fit_mask]
    model = PCA(n_components=min(components, matrix.shape[1], len(fit_points)),
                svd_solver="full", random_state=seed)
    model.fit(fit_points)
    residual = matrix - model.inverse_transform(model.transform(matrix))
    return np.einsum("ij,ij->i", residual, residual)


# ----------------------------------------------------------------------- entity_ewma

def score_entity_ewma(matrix: np.ndarray, entity: np.ndarray, half_life_events: float,
                      min_observations: int) -> np.ndarray:
    """A naive per-entity baseline: EWMA z-score against the entity's own history.

    This is the comparison that separates the framework's *framing* from its machinery.
    It holds per-entity state, as the framework does, and nothing else the framework has:
    no calibrated p-value, no abstention, no combination, no dependence correction, no
    conformal step. If it recovers most of the framework's detections, the value
    demonstrated is in comparing an account to itself rather than in this implementation
    of that idea.

    Per entity it keeps an exponentially weighted mean and variance of each feature, and
    scores an event by its largest standardised deviation across the features present:

        score = max_f |x_f - mu_f| / sqrt(var_f)

    **Scoring precedes observation**, as §5.2 requires of the framework: state is updated
    only after the score is computed, or an event's own value would be part of the
    history it is judged against. Getting this wrong is silent — the numbers stay
    plausible — so the baseline is held to the same discipline even though nothing
    obliges it to be.

    Cold start is deliberately naive: below `min_observations` the entity scores 0, the
    least anomalous value, so it never alerts. A real detector abstains here and says so
    (R3); this one asserts normality on no evidence, which is exactly the behaviour R3
    exists to forbid and is retained so the cost of forbidding it is visible.

    Missing features (encoded -1) neither contribute to the score nor update state.
    Deterministic: no RNG, and the single pass is in the file's own row order.
    """
    decay = 1.0 - 0.5 ** (1.0 / max(half_life_events, 1e-9))
    codes, _ = pd.factorize(entity)  # order of appearance, so deterministic
    width = matrix.shape[1]
    mean = np.zeros((len(_), width))
    variance = np.zeros((len(_), width))
    seen = np.zeros(len(_), dtype=np.int64)
    scores = np.zeros(len(matrix))

    for row in range(len(matrix)):
        code = codes[row]
        values = matrix[row]
        present = values != MISSING
        if seen[code] >= min_observations and present.any():
            deviation = np.abs(values - mean[code])
            spread = np.sqrt(variance[code]) + 1e-12
            scores[row] = float((deviation / spread)[present].max())
        # Observe strictly after scoring. Absent features leave their moments alone.
        delta = np.where(present, values - mean[code], 0.0)
        mean[code] += decay * delta
        variance[code] = np.where(
            present, variance[code] + decay * (delta * delta - variance[code]),
            variance[code])
        seen[code] += 1
    return scores


# ----------------------------------------------------------- matched-budget detection

def alert_threshold(sample_scores: np.ndarray, budget: int,
                    population_multiplier: int) -> float:
    """Alert threshold for `budget` alerts/day from one day's sample scores.

    The day's true population is estimated as len(sample_scores) x multiplier; the
    threshold is the score quantile at q = 1 - budget / N_est (linear interpolation
    between order statistics). If budget >= N_est everything alerts (-inf).
    """
    estimated_population = len(sample_scores) * population_multiplier
    if budget >= estimated_population:
        return float("-inf")
    quantile_position = 1.0 - budget / estimated_population
    return float(np.quantile(sample_scores, quantile_position))


def matched_budget_detections(evaluation: ModelEvaluation, budgets: list[int],
                              day_from: int, day_to: int) -> dict:
    """Red-team detections per budget: score >= the row's own day's threshold.

    Each budget block names the events detected, not merely how many. Attribution to an
    anomaly category is impossible from a count, and the per-category comparison is the
    table §12.4 exists to produce, so the identities travel with the counts.
    """
    days, scores = evaluation.days, evaluation.scores
    window = (days >= day_from) & (days < day_to)
    red_in_window = evaluation.red_mask & window
    red_total = int(red_in_window.sum())

    thresholds: dict[tuple[int, int], float] = {}
    for day in range(day_from, day_to):
        day_sample_scores = scores[(days == day) & evaluation.sample_mask]
        for budget in budgets:
            thresholds[(day, budget)] = alert_threshold(
                day_sample_scores, budget, evaluation.population_multiplier)

    detections_at_budget: dict[str, dict] = {}
    for budget in budgets:
        per_day: dict[str, int] = {}
        detected: list[dict] = []
        detections = 0
        for day in range(day_from, day_to):
            day_red = red_in_window & (days == day)
            if not day_red.any():
                continue
            hit_mask = day_red & (scores >= thresholds[(day, budget)])
            hits = int(hit_mask.sum())
            detections += hits
            per_day[str(day)] = hits
            for row in np.flatnonzero(hit_mask):
                detected.append({"t": int(evaluation.t[row]),
                                 "entity": str(evaluation.entity[row])})
        detected.sort(key=lambda event: (event["t"], event["entity"]))
        detections_at_budget[f"budget_{budget}_per_day"] = {
            "detections": detections,
            "red_team_total": red_total,
            "per_day_detections": per_day,
            "detected_events": detected,
            "events_named": True,
        }
    return detections_at_budget


def score_percentile_summary(scores: np.ndarray) -> dict:
    """Median/p90/p99 sanity summary of a score population (nulls when empty)."""
    if scores.size == 0:
        return {"median": None, "p90": None, "p99": None}
    return {
        "median": float(np.quantile(scores, 0.50)),
        "p90": float(np.quantile(scores, 0.90)),
        "p99": float(np.quantile(scores, 0.99)),
    }


# ---------------------------------------------------------------------------- driver

def _evaluate_models(features: FeatureSet, seed: int, sample_rate: int,
                     models: tuple[str, ...],
                     parameters: ModelParameters) -> dict[str, ModelEvaluation]:
    """Run the selected baselines, timing each; returns evaluations keyed by model."""
    sample, red = features.in_sample, features.is_redteam
    evaluations: dict[str, ModelEvaluation] = {}

    def offset(name: str) -> int:
        return seed + MODEL_SEED_OFFSETS[name]

    def record(name: str, score: object) -> None:
        """Time a whole-row model and store its evaluation, if it was selected."""
        if name not in models:
            return
        started = time.perf_counter()
        scores = score()  # type: ignore[operator]
        evaluations[name] = ModelEvaluation(
            days=features.days, t=features.t, entity=features.entity, scores=scores,
            sample_mask=sample, red_mask=red, population_multiplier=sample_rate,
            wall_seconds=time.perf_counter() - started)

    record("iforest", lambda: score_iforest(
        features.matrix, sample, offset("iforest"),
        parameters.iforest_trees, parameters.iforest_max_samples))
    record("eif", lambda: score_eif(
        features.matrix, sample, offset("eif"),
        parameters.eif_trees, parameters.eif_subsample))
    record("hst", lambda: score_hst(
        features.matrix, offset("hst"),
        parameters.hst_trees, parameters.hst_depth, parameters.hst_window))
    record("lof", lambda: score_lof(
        features.matrix, sample, offset("lof"),
        parameters.lof_neighbours, parameters.lof_fit_cap))
    record("ocsvm", lambda: score_ocsvm(
        features.matrix, sample, offset("ocsvm"), parameters.ocsvm_fit_cap,
        parameters.ocsvm_components, parameters.ocsvm_nu))
    record("pca", lambda: score_pca(
        features.matrix, sample, offset("pca"), parameters.pca_components))
    record("entity_ewma", lambda: score_entity_ewma(
        features.matrix, features.entity,
        parameters.ewma_half_life_events, parameters.ewma_min_observations))

    # rrcf scores a thinned subset, so it carries its own row arrays and its own
    # population multiplier rather than the shared ones above.
    if "rrcf" in models:
        kept, thinned_sample = rrcf_thinned_masks(sample, red, parameters.rrcf_thinning)
        started = time.perf_counter()
        kept_scores = score_rrcf(features.matrix[kept], offset("rrcf"),
                                 parameters.rrcf_trees, parameters.rrcf_tree_size)
        evaluations["rrcf"] = ModelEvaluation(
            days=features.days[kept], t=features.t[kept], entity=features.entity[kept],
            scores=kept_scores, sample_mask=thinned_sample[kept], red_mask=red[kept],
            population_multiplier=sample_rate * parameters.rrcf_thinning,
            wall_seconds=time.perf_counter() - started)
    return evaluations


def _model_results(name: str, evaluation: ModelEvaluation, budgets: list[int],
                   day_from: int, day_to: int) -> dict:
    """The per-model results block: detections, percentile summaries, wall time.

    `scope` and `family` travel with each model because the reader has to be able to
    tell a population baseline from a per-entity one without a second document: the two
    answer different questions, and reading them off one leaderboard is the mistake the
    whole comparison exists to avoid.
    """
    window = (evaluation.days >= day_from) & (evaluation.days < day_to)
    return {
        "scope": MODEL_SCOPE[name],
        "family": MODEL_FAMILY[name],
        "detections_at_budget": matched_budget_detections(evaluation, budgets,
                                                          day_from, day_to),
        "red_score_percentiles": score_percentile_summary(
            evaluation.scores[evaluation.red_mask & window]),
        "sample_score_percentiles": score_percentile_summary(
            evaluation.scores[evaluation.sample_mask & window]),
        "wall_seconds": float(evaluation.wall_seconds),
    }


def _utc_now() -> str:
    return datetime.now(timezone.utc).isoformat()


def _model_hyperparameters(parameters: ModelParameters) -> dict:
    """Every model's hyperparameters, recorded so a run can be reproduced exactly."""
    return {
        "iforest": {"n_estimators": parameters.iforest_trees,
                    "max_samples": parameters.iforest_max_samples},
        "eif": {"trees": parameters.eif_trees,
                "subsample": parameters.eif_subsample,
                "extension_level": "full (d-1)"},
        "hst": {"trees": parameters.hst_trees, "depth": parameters.hst_depth,
                "window": parameters.hst_window},
        "rrcf": {"num_trees": parameters.rrcf_trees,
                 "tree_size": parameters.rrcf_tree_size, "shingle": 1,
                 "thinning": parameters.rrcf_thinning},
        "lof": {"n_neighbors": parameters.lof_neighbours,
                "fit_cap": parameters.lof_fit_cap, "novelty": True},
        "ocsvm": {"fit_cap": parameters.ocsvm_fit_cap,
                  "nystroem_components": parameters.ocsvm_components,
                  "nu": parameters.ocsvm_nu,
                  "approximation": "Nystroem kernel map + SGDOneClassSVM; the exact "
                                   "kernel SVM is O(n^2) in the fit set and cannot be "
                                   "run at this row count. Recorded as a deviation "
                                   "from the textbook formulation rather than "
                                   "presented as an exact one-class SVM"},
        "pca": {"n_components": parameters.pca_components,
                "score": "squared reconstruction residual"},
        "entity_ewma": {"half_life_events": parameters.ewma_half_life_events,
                        "min_observations": parameters.ewma_min_observations,
                        "score": "max standardised deviation over present features",
                        "cold_start": "scores 0 (never alerts) below min_observations; "
                                      "a naive baseline asserts normality where R3 "
                                      "requires a detector to abstain, and that is "
                                      "retained so the cost of the rule is visible"},
    }


def run_pipeline(features_path: str, out_path: str, run_id: str, seed: int,
                 budgets: list[int], day_from: int, day_to: int,
                 scored_events: int | None, sample_rate: int = DEFAULT_SAMPLE_RATE,
                 models: tuple[str, ...] = DEFAULT_MODELS,
                 parameters: ModelParameters = ModelParameters()) -> dict:
    """Load features, run the selected baselines, write the JSON, return it."""
    started = _utc_now()
    features = load_features(features_path)
    intact = entity_history_intact(features)
    evaluations = _evaluate_models(features, seed, sample_rate, models, parameters)

    caveats = []
    if not intact:
        caveats.append(
            "the export is a uniform sample over EVENTS, so every entity's history is "
            "decimated at the same rate. The population models are unaffected — they "
            "hold no per-entity state — but entity_ewma is handicapped by it, and a "
            "poor showing here is not evidence against the per-entity framing. Re-run "
            "against an entity-sampled export (cmd/subset -entity-sample, then "
            "cmd/features -sample-mod 1) before reading its number")
    skipped = [m for m in ALL_MODELS if m not in models]
    if skipped:
        caveats.append(
            f"models not run in this pass: {', '.join(skipped)}. They are absent from "
            "results rather than recorded as zero, so a reader cannot mistake a model "
            "that did not run for one that detected nothing")

    document = {
        "schema_version": "1",
        "kind": "baselines",
        "hypothesis": ["E1", "E2"],
        "run": {"run_id": run_id},
        "started": started,
        "finished": _utc_now(),
        "seeds": {"global": seed, **{name: seed + offset
                                     for name, offset in MODEL_SEED_OFFSETS.items()
                                     if name in models}},
        "versions": {
            "python": sys.version.split()[0],
            "numpy": np.__version__,
            "pandas": pd.__version__,
            "scikit-learn": importlib.metadata.version("scikit-learn"),
            "rrcf": importlib.metadata.version("rrcf"),
        },
        "input": {
            "features_path": features_path,
            "features_sha256": features.sha256,
            "rows_total": int(len(features.matrix)),
            "rows_sample": int(features.in_sample.sum()),
            "rows_redteam": int(features.is_redteam.sum()),
            "entities": int(len(pd.unique(features.entity))),
            "entity_history_intact": intact,
            "sampling": (
                f"uniform 1-in-{sample_rate} by event-digest hash (Go exporter)"
                + (f"; rrcf additionally thinned 1-in-{parameters.rrcf_thinning}"
                   if "rrcf" in models else "")),
        },
        "parameters": {
            "budgets": [int(budget) for budget in budgets],
            "days_from": int(day_from),
            "days_to": int(day_to),
            "scored_events": scored_events,
            "sample_rate": int(sample_rate),
            "models_run": list(models),
            "models_available": list(ALL_MODELS),
            "models": {name: block
                       for name, block in _model_hyperparameters(parameters).items()
                       if name in models},
        },
        "results": {name: _model_results(name, evaluation, budgets, day_from, day_to)
                    for name, evaluation in evaluations.items()},
        "caveats": caveats,
        "provenance_complete": True,
        "note": NOTE,
    }
    out_dir = os.path.dirname(out_path)
    if out_dir:
        os.makedirs(out_dir, exist_ok=True)
    with open(out_path, "w", encoding="utf-8") as fh:
        json.dump(document, fh, indent=1)
    return document


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--features", required=True, help="gzipped feature CSV (Go exporter)")
    ap.add_argument("--out", required=True, help="baselines JSON path")
    ap.add_argument("--run-id", required=True, help="run identifier (recorded)")
    ap.add_argument("--seed", type=int, default=42, help="global RNG seed (recorded)")
    ap.add_argument("--budgets", default="10,25,50,100",
                    help="comma-separated alert budgets per day")
    ap.add_argument("--days-from", type=int, default=7,
                    help="first corpus day of the detection window (inclusive)")
    ap.add_argument("--days-to", type=int, default=30,
                    help="last corpus day of the detection window (exclusive)")
    ap.add_argument("--scored-events", type=int, default=None,
                    help="the framework run's events_scored, for context (recorded)")
    ap.add_argument("--sample-rate", type=int, default=DEFAULT_SAMPLE_RATE,
                    help="the exporter's -sample-mod; 1 when whole entities were kept. "
                         "Sets the multiplier that reconstructs a day's true population "
                         "from its sampled rows, so a wrong value moves every threshold")
    ap.add_argument("--models", default=",".join(DEFAULT_MODELS),
                    help=f"comma-separated models to run, from {','.join(ALL_MODELS)}. "
                         "rrcf is excluded by default: it costs about two hours at "
                         "corpus row counts where every other model costs minutes")
    args = ap.parse_args()

    budgets = [int(part) for part in args.budgets.split(",") if part.strip()]
    models = tuple(part.strip() for part in args.models.split(",") if part.strip())
    unknown = [name for name in models if name not in ALL_MODELS]
    if unknown:
        ap.error(f"unknown model(s) {', '.join(unknown)}; "
                 f"choose from {', '.join(ALL_MODELS)}")

    document = run_pipeline(args.features, args.out, args.run_id, args.seed, budgets,
                            args.days_from, args.days_to, args.scored_events,
                            sample_rate=args.sample_rate, models=models)
    top_budget = f"budget_{max(budgets)}_per_day"
    summary = ", ".join(
        f"{name} {block['detections_at_budget'][top_budget]['detections']}"
        f"/{block['detections_at_budget'][top_budget]['red_team_total']}"
        for name, block in document["results"].items())
    print(f"baselines: {document['input']['rows_total']} rows "
          f"({document['input']['rows_sample']} sample, "
          f"{document['input']['rows_redteam']} red-team, "
          f"{document['input']['entities']} entities, "
          f"entity history {'intact' if document['input']['entity_history_intact'] else 'DECIMATED'}"
          f"), seed {args.seed}; "
          f"detections at {max(budgets)}/day: {summary}", file=sys.stderr)
    for caveat in document["caveats"]:
        print(f"  caveat: {caveat}", file=sys.stderr)


if __name__ == "__main__":
    main()
