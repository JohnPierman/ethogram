package main

import (
	"testing"

	"github.com/JohnPierman/ethogram/domain/cooccurrence"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/timing"
	"github.com/JohnPierman/ethogram/domain/volume"
)

// The classification is what makes per-category reporting possible, so it is tested
// against the evidence shapes each detector actually emits rather than against a
// paraphrase of them. Every fixture below states its numbers explicitly and says which
// structural predicate they are meant to satisfy or miss.

func verdict(t *testing.T, id detector.ID, stats map[string]float64) detector.Verdict {
	t.Helper()
	ev := detector.NewEvidence(nil, stats, nil)
	v, err := detector.NewEvaluated(id, detector.Target{Entity: "U1"}, 0.5, ev)
	if err != nil {
		t.Fatalf("NewEvaluated(%s): %v", id, err)
	}
	return v
}

func TestClassifyNovelValueNeedsHistoryAndAbsence(t *testing.T) {
	cases := []struct {
		name  string
		stats map[string]float64
		want  bool
	}{
		{
			// The entity has 40 prior observations for the field and has never taken
			// this value: §3.1's proposition exactly.
			name:  "history present, value unseen",
			stats: map[string]float64{"N": 40, "n_v": 0},
			want:  true,
		},
		{
			// The entity has taken this value before, so nothing is novel about it.
			name:  "value seen before",
			stats: map[string]float64{"N": 40, "n_v": 3},
			want:  false,
		},
		{
			// A cold-start entity has no history, so every value is trivially unseen.
			// Counting it would fill the category with the first event of every
			// entity and say nothing about anomaly.
			name:  "no history at all",
			stats: map[string]float64{"N": 0, "n_v": 0},
			want:  false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classify(detector.Verdicts{verdict(t, novelty.DetectorID, c.stats)})
			if got[catNovelValue] != c.want {
				t.Errorf("novel_value = %v, want %v for %v", got[catNovelValue], c.want, c.stats)
			}
		})
	}
}

func TestClassifyOffHoursIsBelowUniformDensity(t *testing.T) {
	// 1/2π = 0.15915494309189535. A density below it means this phase is less likely
	// than chance for this entity, which is the predicate; above it means the entity's
	// own history makes this time more likely than chance, however late it looks.
	cases := []struct {
		name    string
		w       float64
		density float64
		want    bool
	}{
		{"below uniform with weight", 32, 0.02, true},
		{"above uniform with weight", 32, 0.9, false},
		{"exactly uniform", 32, uniformCircularDensity, false},
		// Below the weight floor the fitted density is not yet meaningful, and a
		// near-empty history would otherwise put every entity's early events in the
		// category.
		{"below uniform but too little weight", 3, 0.02, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classify(detector.Verdicts{verdict(t, timing.DetectorID,
				map[string]float64{"W": c.w, "density_at_phi": c.density})})
			if got[catOffHours] != c.want {
				t.Errorf("off_hours = %v, want %v (W=%v, density=%v)",
					got[catOffHours], c.want, c.w, c.density)
			}
		})
	}
}

func TestClassifyVolumeBurstIsAgainstTheEntitysOwnRate(t *testing.T) {
	cases := []struct {
		name           string
		kObs, expected float64
		want           bool
	}{
		{"above own expectation", 90, 12, true},
		{"at own expectation", 12, 12, false},
		{"below own expectation", 4, 12, false},
		{"no expectation yet", 90, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classify(detector.Verdicts{verdict(t, volume.DetectorID,
				map[string]float64{"k_obs": c.kObs, "expected_count": c.expected})})
			if got[catVolumeBurst] != c.want {
				t.Errorf("volume_burst = %v, want %v (k=%v, expected=%v)",
					got[catVolumeBurst], c.want, c.kObs, c.expected)
			}
		})
	}
}

func TestClassifyNovelPairNeedsAGraphWithMass(t *testing.T) {
	cases := []struct {
		name    string
		m, wMin float64
		want    bool
	}{
		{"pair never co-occurred in a populated graph", 5000, 0, true},
		{"pair has co-occurred", 5000, 7, false},
		// An empty graph makes every pair trivially unseen; that is a cold start, not
		// a conditional anomaly.
		{"empty graph", 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classify(detector.Verdicts{verdict(t, cooccurrence.DetectorID,
				map[string]float64{"m": c.m, "w_min_pair": c.wMin})})
			if got[catNovelPair] != c.want {
				t.Errorf("novel_pair = %v, want %v (m=%v, w_min=%v)",
					got[catNovelPair], c.want, c.m, c.wMin)
			}
		})
	}
}

func TestClassifyPopulationRareIsAThousandthOfTheFieldsMass(t *testing.T) {
	cases := []struct {
		name  string
		n, nV float64
		want  bool
	}{
		// 40 in 100,000 is four parts in ten thousand: below the threshold.
		{"four parts in ten thousand", 100000, 40, true},
		// 400 in 100,000 is four parts in a thousand: above it.
		{"four parts in a thousand", 100000, 400, false},
		{"no population yet", 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classify(detector.Verdicts{verdict(t, marginalDetectorID,
				map[string]float64{"N": c.n, "n_v": c.nV})})
			if got[catPopulationRare] != c.want {
				t.Errorf("population_rare = %v, want %v (N=%v, n_v=%v)",
					got[catPopulationRare], c.want, c.n, c.nV)
			}
		})
	}
}

func TestClassifyIsIndependentOfWhichDetectorScoredLowest(t *testing.T) {
	// The whole defence of the per-category tables is that the partition does not
	// depend on our own detectors' p-values. Two verdict sets with identical evidence
	// but opposite p-values must classify identically.
	stats := map[string]float64{"N": 40, "n_v": 0}
	ev := detector.NewEvidence(nil, stats, nil)

	low, err := detector.NewEvaluated(novelty.DetectorID, detector.Target{Entity: "U1"}, 1e-9, ev)
	if err != nil {
		t.Fatal(err)
	}
	high, err := detector.NewEvaluated(novelty.DetectorID, detector.Target{Entity: "U1"}, 1.0, ev)
	if err != nil {
		t.Fatal(err)
	}

	a := classify(detector.Verdicts{low})
	b := classify(detector.Verdicts{high})
	if a[catNovelValue] != b[catNovelValue] {
		t.Errorf("classification changed with the p-value: %v vs %v",
			a[catNovelValue], b[catNovelValue])
	}
	if !a[catNovelValue] {
		t.Error("expected novel_value on both, got neither")
	}
}

func TestClassifyAbstainedVerdictsContributeNothing(t *testing.T) {
	// An abstention is a refusal to make a statement, so it cannot place an event in
	// a category. Evidence that would otherwise qualify must be ignored.
	ev := detector.NewEvidence(nil, map[string]float64{"N": 40, "n_v": 0}, nil)
	v, err := detector.NewAbstained(novelty.DetectorID, detector.Target{Entity: "U1"},
		detector.StatusAbstainedUnusable, "unusable", ev)
	if err != nil {
		t.Fatal(err)
	}
	if got := classify(detector.Verdicts{v}); got[catNovelValue] {
		t.Error("an abstained verdict placed the event in novel_value")
	}
}

func TestClassifyAccumulatesAcrossDetectors(t *testing.T) {
	// One event may exhibit several categories at once; the tables are overlapping
	// subsets and the classifier must not stop at the first match.
	got := classify(detector.Verdicts{
		verdict(t, novelty.DetectorID, map[string]float64{"N": 40, "n_v": 0}),
		verdict(t, timing.DetectorID, map[string]float64{"W": 32, "density_at_phi": 0.01}),
		verdict(t, cooccurrence.DetectorID, map[string]float64{"m": 5000, "w_min_pair": 0}),
	})
	for _, want := range []anomalyCategory{catNovelValue, catOffHours, catNovelPair} {
		if !got[want] {
			t.Errorf("missing category %s", want)
		}
	}
	if got[catVolumeBurst] {
		t.Error("volume_burst set with no volume verdict")
	}
}

func TestAllCategoriesIsStableAndComplete(t *testing.T) {
	// The reporting order is part of the output contract, and a category present in
	// the classifier but missing from the order would never be reported.
	want := []anomalyCategory{
		catPopulationRare, catNovelValue, catOffHours, catVolumeBurst, catNovelPair,
	}
	got := allCategories()
	if len(got) != len(want) {
		t.Fatalf("allCategories has %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d is %s, want %s", i, got[i], want[i])
		}
	}
}
