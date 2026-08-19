package cooccurrence_test

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/cooccurrence"
)

// ---------------------------------------------------------------------------
// The §8.3 hand fixture: two fields, five nodes, four edges, no decay (all weight
// at one timestamp).
//
//	(u1,l1) w=4   (u2,l1) w=2   (u3,l2) w=3   (u1,l2) w=1
//
// Degrees k(u1)=5, k(u2)=2, k(u3)=3, k(l1)=6, k(l2)=4; total m = 10. Blocks within
// each field: ub1={u1,u2}, ub2={u3}, lb1={l1}, lb2={l2}, giving D(ub1)=7, D(ub2)=3,
// D(lb1)=6, D(lb2)=4, and inter-block weights m(ub1,lb1)=6, m(ub1,lb2)=1,
// m(ub2,lb2)=3, m(ub2,lb1)=0. No within-block edges exist here, so no doubling
// arises in this fixture; the Karrer–Newman doubling is exercised by the collapse
// test below.
// ---------------------------------------------------------------------------

var (
	nodeU1 = cooccurrence.NodeID{Field: "user", Value: "u1"}
	nodeU2 = cooccurrence.NodeID{Field: "user", Value: "u2"}
	nodeU3 = cooccurrence.NodeID{Field: "user", Value: "u3"}
	nodeL1 = cooccurrence.NodeID{Field: "location", Value: "l1"}
	nodeL2 = cooccurrence.NodeID{Field: "location", Value: "l2"}
)

const fixtureTotal = 10.0

var fixtureDegrees = map[cooccurrence.NodeID]float64{
	nodeU1: 5, nodeU2: 2, nodeU3: 3, nodeL1: 6, nodeL2: 4,
}

func fixturePartition() *cooccurrence.Partition {
	return &cooccurrence.Partition{
		Seed:          42,
		GraphChecksum: "fixture-checksum",
		Resolution:    1.0,
		Blocks: map[cooccurrence.NodeID]cooccurrence.BlockID{
			nodeU1: "ub1", nodeU2: "ub1", nodeU3: "ub2",
			nodeL1: "lb1", nodeL2: "lb2",
		},
		DegreeSums: map[cooccurrence.BlockID]float64{
			"ub1": 7, "ub2": 3, "lb1": 6, "lb2": 4,
		},
		// Σ_r D_r = 2m of the snapshot: 20, consistent with fixtureTotal = 10.
		TotalDegree: 2 * fixtureTotal,
		BlockWeights: map[cooccurrence.BlockPair]float64{
			cooccurrence.NewBlockPair("ub1", "lb1"): 6,
			cooccurrence.NewBlockPair("ub1", "lb2"): 1,
			cooccurrence.NewBlockPair("ub2", "lb2"): 3,
			cooccurrence.NewBlockPair("ub2", "lb1"): 0,
		},
	}
}

func within(got, want, tolerance float64) bool {
	d := got - want
	return d < tolerance && d > -tolerance
}

// TestLambdaOnHandFixture checks (14) against every cross-field pair of the
// fixture, worked by hand: λ_ij = k_i·k_j·m_rs/(D_r·D_s).
func TestLambdaOnHandFixture(t *testing.T) {
	p := fixturePartition()
	cases := []struct {
		name string
		a, b cooccurrence.NodeID
		want float64
	}{
		{"u1_l1", nodeU1, nodeL1, 30.0 / 7.0}, // 5·6·6/(7·6)
		{"u2_l1", nodeU2, nodeL1, 12.0 / 7.0}, // 2·6·6/(7·6)
		{"u3_l2", nodeU3, nodeL2, 3.0},        // 3·4·3/(3·4)
		{"u1_l2", nodeU1, nodeL2, 5.0 / 7.0},  // 5·4·1/(7·4)
		{"u2_l2", nodeU2, nodeL2, 2.0 / 7.0},  // 2·4·1/(7·4)
		{"u3_l1", nodeU3, nodeL1, 0.0},        // 3·6·0/(3·6)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lambda, usedFallback := cooccurrence.Lambda(p, tc.a, tc.b,
				fixtureDegrees[tc.a], fixtureDegrees[tc.b], fixtureTotal)
			if usedFallback {
				t.Fatal("partition assigns both nodes; (14) must not fall back")
			}
			if !within(lambda, tc.want, 1e-12) {
				t.Errorf("lambda = %v, want %v", lambda, tc.want)
			}
		})
	}

	// Blocks that scarcely interact yield λ ≈ 0, hence P ≈ 1, correctly declining
	// to flag an uninformative pairing: m(ub2, lb1) = 0 gives λ = 0 and P = 1
	// exactly, whatever the observed weight.
	lambda, _ := cooccurrence.Lambda(p, nodeU3, nodeL1,
		fixtureDegrees[nodeU3], fixtureDegrees[nodeL1], fixtureTotal)
	if lambda != 0 {
		t.Fatalf("λ(u3, l1) = %v, want exactly 0", lambda)
	}
	if got := cooccurrence.PoissonLowerTail(lambda, 0); got != 1 {
		t.Errorf("P at λ = 0 is %v, want exactly 1", got)
	}
}

// TestSingleBlockCollapseIsConfigurationModel is the regression guard for review
// item B1. Equation (15): with one block, D_1 = 2m and m_11 = 2m under the
// Karrer–Newman convention (weight internal to a block is counted from both
// endpoints), so the GENERAL (14) path gives λ = k_i·k_j·2m/(2m·2m) = k_i·k_j/2m
// EXACTLY — the configuration-model expectation. The erroneous draft had m_11 = m,
// giving k_i·k_j/4m: wrong by a factor of two. The assertion is exact equality,
// not tolerance, through the general code path with a one-block partition, not a
// special-cased formula.
func TestSingleBlockCollapseIsConfigurationModel(t *testing.T) {
	const (
		ka = 5.0
		kb = 6.0
		m  = 10.0
	)
	single := &cooccurrence.Partition{
		Blocks: map[cooccurrence.NodeID]cooccurrence.BlockID{
			nodeU1: "b1", nodeU2: "b1", nodeU3: "b1", nodeL1: "b1", nodeL2: "b1",
		},
		DegreeSums: map[cooccurrence.BlockID]float64{"b1": 2 * m},
		BlockWeights: map[cooccurrence.BlockPair]float64{
			cooccurrence.NewBlockPair("b1", "b1"): 2 * m,
		},
		TotalDegree: 2 * m,
	}

	lambda, usedFallback := cooccurrence.Lambda(single, nodeU1, nodeL1, ka, kb, m)
	if usedFallback {
		t.Fatal("one-block partition assigns both nodes; (14) must not fall back")
	}
	if ratio := lambda * 2 * m / (ka * kb); ratio != 1 {
		t.Errorf("collapse identity broken: λ·2m/(k_i·k_j) = %v, want exactly 1 (B1)", ratio)
	}
	if lambda != ka*kb/(2*m) {
		t.Errorf("λ = %v, want exactly %v = k_i·k_j/2m", lambda, ka*kb/(2*m))
	}

	// The nil-partition fallback must agree exactly, and must say it fell back.
	fallback, usedFallback := cooccurrence.Lambda(nil, nodeU1, nodeL1, ka, kb, m)
	if !usedFallback {
		t.Error("nil partition must report usedFallback (§8.4)")
	}
	if fallback != lambda {
		t.Errorf("fallback λ = %v differs from the one-block (14) collapse %v; "+
			"(15) and (14) must agree exactly", fallback, lambda)
	}
}

// TestErroneousHalfWeightConventionIsDetectablyWrong documents why the convention
// matters. The pre-fix draft (review item B1) had m_11 = m rather than 2m, giving
// λ = k_i·k_j·m/(2m·2m) = k_i·k_j/4m — 0.75 here, half the correct 1.5. The
// correct convention must not produce that number.
func TestErroneousHalfWeightConventionIsDetectablyWrong(t *testing.T) {
	const (
		ka = 5.0
		kb = 6.0
		m  = 10.0
	)
	blocks := map[cooccurrence.NodeID]cooccurrence.BlockID{nodeU1: "b1", nodeL1: "b1"}

	erroneous := &cooccurrence.Partition{
		Blocks:     blocks,
		DegreeSums: map[cooccurrence.BlockID]float64{"b1": 2 * m},
		BlockWeights: map[cooccurrence.BlockPair]float64{
			cooccurrence.NewBlockPair("b1", "b1"): m, // the pre-fix draft
		},
		TotalDegree: 2 * m,
	}
	wrong, _ := cooccurrence.Lambda(erroneous, nodeU1, nodeL1, ka, kb, m)
	if wrong != 0.75 {
		t.Fatalf("the erroneous convention should reproduce the draft's 0.75, got %v", wrong)
	}
	if wrong == ka*kb/(2*m) {
		t.Error("the erroneous convention accidentally satisfies the collapse identity")
	}

	correct := &cooccurrence.Partition{
		Blocks:     blocks,
		DegreeSums: map[cooccurrence.BlockID]float64{"b1": 2 * m},
		BlockWeights: map[cooccurrence.BlockPair]float64{
			cooccurrence.NewBlockPair("b1", "b1"): 2 * m,
		},
		TotalDegree: 2 * m,
	}
	lambda, _ := cooccurrence.Lambda(correct, nodeU1, nodeL1, ka, kb, m)
	if lambda == wrong {
		t.Errorf("the correct convention produced the draft's erroneous %v", wrong)
	}
}

// TestLambdaIsInvariantToTheSnapshotScale is the regression guard for the defect that
// put 99.0% of scored events below p = 1e-12 in the partitioned arm of
// results/lanl-sample16-d7-14.json.
//
// The partition is frozen at the burn-in boundary (§8.2 requires it: a quantity used to
// score an event must not have been fitted on it), but the degrees and the total weight
// handed to Lambda are read from the LIVE decayed graph at scoring time. Equation (14)
// is a maximum-likelihood estimate over one graph, and mixing block statistics from the
// snapshot with node degrees from the live graph leaves λ carrying the ratio of their
// scales — so λ drifts upward as the live graph outgrows the snapshot, and the lower
// tail Pr(Poisson(λ) ≤ w) collapses for every pair, guilty or innocent.
//
// The block structure a partition describes is a RATIO — which blocks associate more
// than chance would have it — and a ratio cannot depend on how big the graph was when
// it was measured. Two snapshots of the same structure at different scales must
// therefore price the same live pair identically.
func TestLambdaIsInvariantToTheSnapshotScale(t *testing.T) {
	const (
		ka = 5.0
		kb = 6.0
		m  = 10.0 // the LIVE graph, identical in both calls
	)

	base := fixturePartition()

	// The same structure, measured on a snapshot three times the size: every degree
	// sum and every block weight scales together, which is what a graph growing
	// uniformly does. Nothing about which blocks associate has changed.
	const c = 3.0
	scaled := fixturePartition()
	for id, d := range scaled.DegreeSums {
		scaled.DegreeSums[id] = d * c
	}
	for pair, w := range scaled.BlockWeights {
		scaled.BlockWeights[pair] = w * c
	}
	scaled.TotalDegree *= c

	for _, tc := range []struct {
		name string
		a, b cooccurrence.NodeID
	}{
		{"u1-l1, densely associated blocks", nodeU1, nodeL1},
		{"u1-l2, sparsely associated blocks", nodeU1, nodeL2},
		{"u3-l2", nodeU3, nodeL2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want, fellBack := cooccurrence.Lambda(base, tc.a, tc.b, ka, kb, m)
			if fellBack {
				t.Fatal("both nodes are assigned; (14) must not fall back")
			}
			got, fellBack := cooccurrence.Lambda(scaled, tc.a, tc.b, ka, kb, m)
			if fellBack {
				t.Fatal("the scaled snapshot must not fall back either")
			}
			if !within(got, want, 1e-12) {
				t.Errorf("λ = %v on a snapshot %v× the size, want %v: the block term "+
					"must be a scale-free affinity, or λ inherits the ratio between "+
					"the frozen snapshot and the live graph", got, c, want)
			}
		})
	}
}

// TestPoissonLowerTail pins the tail against hand-computable values and the
// (0, 1] guard.
func TestPoissonLowerTail(t *testing.T) {
	// Pr(Poisson(ln 2) ≤ 0) = e^(−ln 2) = 1/2.
	if got := cooccurrence.PoissonLowerTail(math.Ln2, 0); !within(got, 0.5, 1e-12) {
		t.Errorf("P(λ = ln 2, w = 0) = %v, want 1/2", got)
	}
	// Pr(Poisson(3) ≤ 2) = e^(−3)·(1 + 3 + 9/2); scipy poisson.cdf(2, 3).
	if got := cooccurrence.PoissonLowerTail(3, 2); !within(got, 0.42319008112684364, 1e-10) {
		t.Errorf("P(λ = 3, w = 2) = %v, want 0.42319008112684364", got)
	}
	// n = ⌊w⌋: a fractional decayed weight prices as its integer floor.
	if a, b := cooccurrence.PoissonLowerTail(3, 2.9), cooccurrence.PoissonLowerTail(3, 2); a != b {
		t.Errorf("P(λ = 3, w = 2.9) = %v differs from P(λ = 3, w = 2) = %v; n must be ⌊w⌋", a, b)
	}
	// λ = 0 asserts nothing: P = 1 for any weight.
	for _, w := range []float64{0, 1, 2.5, 1e9} {
		if got := cooccurrence.PoissonLowerTail(0, w); got != 1 {
			t.Errorf("P(λ = 0, w = %v) = %v, want exactly 1", w, got)
		}
	}
	// A huge λ with w = 0 underflows e^(−λ); the guard floors at the smallest
	// positive float64 rather than returning the 0 that would poison (18).
	got := cooccurrence.PoissonLowerTail(1e6, 0)
	if got <= 0 {
		t.Errorf("P(λ = 1e6, w = 0) = %v; must be floored positive, never 0", got)
	}
	if got > 1e-100 {
		t.Errorf("P(λ = 1e6, w = 0) = %v; want a tiny positive value", got)
	}
}

// TestDcsbmDeterminism: identical inputs, bit-identical outputs, thirty-two times
// (R4). Lambda and PoissonLowerTail are pure; this pins that no map-order or
// accumulation nondeterminism leaks in.
func TestDcsbmDeterminism(t *testing.T) {
	p := fixturePartition()
	pairs := []struct{ a, b cooccurrence.NodeID }{
		{nodeU1, nodeL1}, {nodeU2, nodeL1}, {nodeU3, nodeL2},
		{nodeU1, nodeL2}, {nodeU2, nodeL2}, {nodeU3, nodeL1},
	}
	weights := []float64{4, 2, 3, 1, 0, 0}

	type outcome struct {
		lambda, p float64
		fallback  bool
	}
	baseline := make([]outcome, len(pairs))
	for run := range 32 {
		for i, pr := range pairs {
			lambda, fb := cooccurrence.Lambda(p, pr.a, pr.b,
				fixtureDegrees[pr.a], fixtureDegrees[pr.b], fixtureTotal)
			got := outcome{lambda: lambda, p: cooccurrence.PoissonLowerTail(lambda, weights[i]), fallback: fb}
			if run == 0 {
				baseline[i] = got
				continue
			}
			if math.Float64bits(got.lambda) != math.Float64bits(baseline[i].lambda) ||
				math.Float64bits(got.p) != math.Float64bits(baseline[i].p) ||
				got.fallback != baseline[i].fallback {
				t.Fatalf("run %d pair %d: %+v differs from first run's %+v (R4)",
					run, i, got, baseline[i])
			}
		}
	}
}
