package main

import (
	"math"
	"sort"
)

// p x n under a weighted ranking (#15).
//
// # The statistic and why it is the one that matters
//
// For a first-ever value the novelty estimate reduces to the reserved mass α/(n + α(K+1)),
// which for α = 1 and n ≫ K is about 1/n. So p × n near one means the ranking is close to
// sorting accounts by event count -- history length is informative about the p-value's scale
// while being no part of the question asked, and that is the covariate problem #15 exists to
// address.
//
// The detection counts say whether the weighting helped. This says whether it moved the thing
// it was aimed at, which is a different question and the one that distinguishes a mechanism
// from a coincidence.
//
// # Why it is computed here rather than recorded by the replay
//
// Because it needs no second run. The weighting is a deterministic per-stratum multiplier and
// the table is in the result; the composite's labelled records carry each detector's own model
// p-value and the entity's history length at scoring time. Both the unweighted and the weighted
// product are therefore recoverable from one file, which also means the comparison is between
// two rankings of the same events rather than between two runs.
//
// The replay's own `labelled_history_product` block covers the composite, and the composite is
// deliberately unweighted -- so that block measures nothing about the weighting, and this is
// the block that does. Recording only the composite's was a mistake in the first version of
// #15's instrumentation.

// historyProductRow is one arm's p × n before and after its weighting.
type historyProductRow struct {
	Arm string `json:"arm"`
	// Events is how many labelled events contributed: those with a positive p-value under
	// this arm and a recorded history length. A p-value that underflowed to zero has no
	// representable product and is excluded rather than counted as zero, which would read
	// as a removed dependence instead of an unrepresentable one.
	Events int `json:"events"`
	// Degenerate records that the fit found no signal in any stratum, so the weights fell
	// back to uniform and the two columns below are identical by construction.
	Degenerate bool `json:"degenerate"`
	// FlooredStrata is how many strata had their learned weight raised off zero. A large
	// count means the "weighting" is closer to an exclusion of those strata, which is worth
	// knowing before reading the movement as a covariate correction.
	FlooredStrata int `json:"floored_strata"`

	UnweightedMedian float64 `json:"unweighted_median"`
	UnweightedP25    float64 `json:"unweighted_p25"`
	UnweightedP75    float64 `json:"unweighted_p75"`
	WeightedMedian   float64 `json:"weighted_median"`
	WeightedP25      float64 `json:"weighted_p25"`
	WeightedP75      float64 `json:"weighted_p75"`
	// Ratio is the weighted median over the unweighted one: how far the dependence moved.
	Ratio float64 `json:"median_ratio"`
}

// historyProduct recomputes p × n per weighted arm from a recorded run.
//
// It returns a block reporting nothing at all when the run carried no weighting, which is the
// correct reading for every run before #15: there is no weighted ranking to compare against.
func historyProduct(results map[string]any) map[string]any {
	weighting := mapOf(results, "history_weighting")
	mode := str(weighting, "mode")
	if mode == "" || mode == "none" {
		return map[string]any{
			"recorded": false,
			"note": "this run ranked on each arm's own p-value, unweighted, so there is no " +
				"weighted ranking to recompute p x n under",
		}
	}

	labelled := listOf(results, "red_team_scored")
	if len(labelled) == 0 {
		return map[string]any{
			"recorded": false,
			"note":     "the run recorded no labelled event, so p x n has no sample",
		}
	}

	arms := mapOf(weighting, "arms")
	names := make([]string, 0, len(arms))
	for name := range arms {
		names = append(names, name)
	}
	sort.Strings(names)

	rows := make([]historyProductRow, 0, len(names))
	for _, name := range names {
		entry := asMap(arms[name])
		bounds, weights, _ := weightTableFrom(entry)
		if len(weights) == 0 {
			continue
		}

		plain := make([]float64, 0, len(labelled))
		weighted := make([]float64, 0, len(labelled))
		for _, raw := range labelled {
			record := asMap(raw)
			history := num(record, "history_n")
			p := num(mapOf(record, "detectors"), name)
			if history <= 0 || p <= 0 {
				continue
			}
			w := weightFor(bounds, weights, history)
			if w <= 0 {
				continue
			}
			plain = append(plain, p*history)
			weighted = append(weighted, (p/w)*history)
		}
		if len(plain) == 0 {
			continue
		}
		sort.Float64s(plain)
		sort.Float64s(weighted)

		row := historyProductRow{
			Arm:              name,
			Events:           len(plain),
			Degenerate:       boolOf(entry, "degenerate"),
			FlooredStrata:    intOf(entry, "floored_strata"),
			UnweightedMedian: quantileAt(plain, 0.5),
			UnweightedP25:    quantileAt(plain, 0.25),
			UnweightedP75:    quantileAt(plain, 0.75),
			WeightedMedian:   quantileAt(weighted, 0.5),
			WeightedP25:      quantileAt(weighted, 0.25),
			WeightedP75:      quantileAt(weighted, 0.75),
		}
		if row.UnweightedMedian > 0 {
			row.Ratio = row.WeightedMedian / row.UnweightedMedian
		}
		rows = append(rows, row)
	}

	return map[string]any{
		"recorded": true,
		"statistic": "p x n over labelled events, where n is the entity's history length at " +
			"scoring time. Near one means the ranking is close to sorting accounts by size",
		"per_arm": rows,
		"note": "computed from one run rather than two: the weighting is a deterministic " +
			"per-stratum multiplier and the table is recorded, so both columns are two " +
			"rankings of the same events. The detection counts say whether the weighting " +
			"helped; this says whether it moved what it was aimed at",
		"reading": "a large floored_strata count means the arm's weighting is closer to an " +
			"exclusion of those strata than to a covariate correction, because Storey's " +
			"null-proportion estimate clamped to one there -- which is a statement about " +
			"that arm's calibration rather than about history length",
	}
}

// weightTableFrom reads a recorded per-arm table: ascending cut points, the weight actually
// applied per stratum, and how many were floored.
//
// The applied weight rather than the learned one, where they differ. A floored stratum's
// learned weight is zero and the applied one is the estimator's resolution limit, and it is
// the applied weight the ranking used.
func weightTableFrom(entry map[string]any) (bounds, weights []float64, floored int) {
	degenerate := boolOf(entry, "degenerate")
	for _, raw := range listOf(entry, "strata") {
		row := asMap(raw)
		if upto, ok := row["covariate_upto"]; ok {
			bounds = append(bounds, toFloat(upto))
		}
		weight := num(row, "weight")
		if applied, ok := row["applied_weight"]; ok {
			weight = toFloat(applied)
			floored++
		}
		if degenerate {
			weight = 1
		}
		weights = append(weights, weight)
	}
	return bounds, weights, floored
}

// weightFor is the weight a history length falls under, on the same covariate the replay used:
// ln(1 + n), with a value exactly on a cut point taking the lower stratum.
func weightFor(bounds, weights []float64, history float64) float64 {
	covariate := math.Log1p(history)
	stratum := sort.SearchFloat64s(bounds, covariate)
	if stratum >= len(weights) {
		stratum = len(weights) - 1
	}
	return weights[stratum]
}

// quantileAt is the linear-interpolated quantile of an ascending slice.
func quantileAt(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := lo + 1
	if hi >= len(sorted) {
		return sorted[len(sorted)-1]
	}
	return sorted[lo]*(1-(pos-float64(lo))) + sorted[hi]*(pos-float64(lo))
}

// The JSON accessors below are local to this command. cmd/dashboard and cmd/methodtable each
// carry their own set, deliberately: they are three separate readers of the same result files,
// and a shared helper package would couple three commands that have no other reason to move
// together.

func asMap(raw any) map[string]any {
	m, _ := raw.(map[string]any)
	return m
}

func mapOf(node map[string]any, key string) map[string]any {
	if node == nil {
		return nil
	}
	return asMap(node[key])
}

func listOf(node map[string]any, key string) []any {
	if node == nil {
		return nil
	}
	list, _ := node[key].([]any)
	return list
}

func str(node map[string]any, key string) string {
	if node == nil {
		return ""
	}
	s, _ := node[key].(string)
	return s
}

func num(node map[string]any, key string) float64 {
	if node == nil {
		return 0
	}
	return toFloat(node[key])
}

func intOf(node map[string]any, key string) int {
	return int(num(node, key))
}

func boolOf(node map[string]any, key string) bool {
	if node == nil {
		return false
	}
	b, _ := node[key].(bool)
	return b
}

// toFloat reads a JSON number. json.Unmarshal into `any` yields float64 for every number, so
// the other cases are for a caller passing something already typed.
func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}
