package main

import (
	"fmt"
	"math"
	"sort"

	"github.com/JohnPierman/ethogram/domain/calibration"
	"github.com/JohnPierman/ethogram/domain/detector"
)

// History-length weighting of the selection (#15).
//
// # What is being corrected
//
// For a first-ever value the novelty estimate of equation (4) reduces to the reserved mass
// α/(n + α(K+1)), which for α = 1 and n ≫ K is about 1/n. Measured across 32 planted
// victims spanning 263 to 20,666 events of history, p × n has median 1.15 and lies in
// [0.50, 7.22]. Among novel events the ranking is therefore close to sorting accounts by
// event count, and clearing the realised cut needs roughly 117,000 events of history where
// the busiest planted victim had 20,666. No attack on an ordinary account can win a slot
// however it behaves.
//
// History length is informative about the p-value's scale while being no part of the
// question being asked. That is a covariate problem, and [calibration.StratifiedWeights]
// is the instrument for it.
//
// # Why the weights are frozen at the burn-in boundary rather than cross-fitted in place
//
// A weight vector learned from the p-values it then ranks over-rejects, and the domain
// package measures how badly: at Storey's λ = 0.02 in-sample weighting realises twice the
// false discovery rate it was asked for. Cross-fitting is that package's defence when
// weights and p-values must come from one sample.
//
// Here they need not. The burn-in window is disjoint from the scoring window by
// construction — the boundary was fixed before any measurement — so weights fitted on
// burn-in are independent of every p-value they are applied to, which is stronger than
// cross-fitting and is the same discipline the weighted arm already follows. The fit
// therefore runs with one fold: cross-fitting inside an already-disjoint sample would
// defend against nothing.
//
// It also keeps the transformation deployable. Every quantity read at scoring time is a
// property of the single event or of frozen state, so the same arithmetic that reranks a
// batch here thresholds a stream in production.
//
// # Why the weighted score replaces the log p-value rather than sitting beside it
//
// Because that is what the run's score now is, and the codebase already has this case:
// conformal calibration replaces LogP and keeps the model value in ModelLogP. History
// weighting does the same, so every histogram, ledger and detection table reports the score
// the run actually ranked on, and `-weighting none` leaves all of it untouched.
//
// # What is not weighted, and why it cannot be
//
// The composite, the min-p arm, and the E4 and E9 ablations. No combined score exists
// before the burn-in boundary — the covariance and conformal models are not frozen until
// it, which is what makes burn-in a clean fitting window in the first place — so a combined
// arm has no burn-in sample, and the only weights available to it would be ones fitted on
// the p-values they then rank. That is the failure mode this design exists to avoid, so
// those arms run unweighted and the result records that they did.
//
// It costs less than it appears to. #15's premise is about equation (4)'s 1/n, novelty is
// the arm that carries it, and the per-detector arms are where the paper's headline table
// lives. A combined arm dominated by novelty inherits the dependence rather than
// originating it.

// weightingMode is what the -weighting flag selects.
type weightingMode string

const (
	weightingNone    weightingMode = "none"
	weightingHistory weightingMode = "history"
	weightingAsset   weightingMode = "asset"
)

// parseWeighting resolves the flag.
//
// `asset` is named in #15 alongside `history` and is refused rather than accepted as a
// synonym for something else: weighting by asset criticality needs a criticality per
// entity, and this corpus carries no such field for any of its entities. Refusing with the
// reason is the honest failure; silently falling back to history weighting under an
// `-weighting asset` flag would record a run whose provenance block lies about what it did.
func parseWeighting(s string) (weightingMode, error) {
	switch weightingMode(s) {
	case weightingNone:
		return weightingNone, nil
	case weightingHistory:
		return weightingHistory, nil
	case weightingAsset:
		return "", fmt.Errorf("weighting %q needs a criticality per entity and this "+
			"corpus carries none: authentication records name accounts and computers, "+
			"not their importance. Supply an asset inventory first", s)
	default:
		return "", fmt.Errorf("unknown weighting %q: want none, history or asset", s)
	}
}

// historyWeightingCap is how many burn-in observations per arm the fit is allowed to
// retain.
//
// The burn-in window is millions of events across seven arms, which cannot all be held. The
// cap is a sample size rather than a memory budget: 200,000 observations spread over five
// strata leave 40,000 per stratum, and Storey's estimator on 40,000 draws has a standard
// error near 0.005 — far finer than the differences between strata that the weighting is
// there to exploit. Raising it buys precision that changes no weight.
const historyWeightingCap = 200_000

// covariateSample is a deterministic decimating sample of one arm's burn-in observations.
//
// It keeps every stride-th observation and, when full, halves itself by retaining every
// second entry and doubling the stride. The result covers the whole burn-in window evenly
// rather than its beginning — which a simple prefix would, and which would fit the weights
// on the days when every history is shortest, exactly the regime the covariate is about.
//
// No randomness (R4): what is kept is a function of arrival index alone, so the sample is
// identical on every run.
type covariateSample struct {
	stride    int64
	seen      int64
	logP      []float64
	covariate []float64
}

func (s *covariateSample) add(logP, covariate float64) {
	if s.stride == 0 {
		s.stride = 1
	}
	s.seen++
	if (s.seen-1)%s.stride != 0 {
		return
	}
	s.logP = append(s.logP, logP)
	s.covariate = append(s.covariate, covariate)
	if len(s.logP) < historyWeightingCap {
		return
	}
	// Full: keep every second retained observation and halve the sampling rate. Both
	// slices are compacted in place, in one fixed order.
	kept := 0
	for i := 0; i < len(s.logP); i += 2 {
		s.logP[kept] = s.logP[i]
		s.covariate[kept] = s.covariate[i]
		kept++
	}
	s.logP = s.logP[:kept]
	s.covariate = s.covariate[:kept]
	s.stride *= 2
}

// weightTable is one arm's frozen weighting: ascending covariate cut points and the log
// weight of each stratum they define.
type weightTable struct {
	bounds []float64
	// logWeight is ln of the stratum's weight, and it is always finite: see freeze for why
	// a learned weight of zero is floored here rather than allowed to exclude a stratum.
	logWeight []float64
	// floored marks the strata whose learned weight was zero and was raised to the floor,
	// so the substitution shows up in the result rather than only in its effect.
	floored []bool
	report  calibration.StratifiedReport
}

// logWeightFor returns the log weight for a covariate value, or zero when the table is
// empty — an empty table is the identity transform, not an error.
func (t weightTable) logWeightFor(covariate float64) float64 {
	if len(t.logWeight) == 0 {
		return 0
	}
	g := sort.SearchFloat64s(t.bounds, covariate)
	if g >= len(t.logWeight) {
		g = len(t.logWeight) - 1
	}
	return t.logWeight[g]
}

// historyWeighting carries the covariate, the burn-in sample and the frozen tables.
type historyWeighting struct {
	mode weightingMode

	// entityEvents is each entity's history length: how many of its events have been seen
	// so far. Incremented after an event is scored, so the value read while scoring is
	// history strictly before it — the covariate #15 names, and one that a live system
	// has at the same moment.
	entityEvents map[string]int64

	sample map[detector.ID]*covariateSample
	table  map[detector.ID]weightTable
	frozen bool
}

func newHistoryWeighting(mode weightingMode) *historyWeighting {
	return &historyWeighting{
		mode:         mode,
		entityEvents: make(map[string]int64),
		sample:       make(map[detector.ID]*covariateSample),
		table:        make(map[detector.ID]weightTable),
	}
}

// on reports whether any reweighting is being applied. Every call site is guarded by it, so
// `-weighting none` runs the unmodified path.
func (h *historyWeighting) on() bool { return h != nil && h.mode == weightingHistory }

// covariateFor is the covariate at scoring time: ln of one plus the entity's history
// length.
//
// The logarithm because the quantity being corrected for is multiplicative — the novelty
// p-value goes as 1/n, so equal ratios of history matter equally, and a linear covariate
// would put every account below a few thousand events into one stratum. The one plus so a
// first-ever event has a defined covariate of zero rather than a negative infinity.
func (h *historyWeighting) covariateFor(entity string) float64 {
	return math.Log1p(float64(h.entityEvents[entity]))
}

// seen records one event against its entity's history. Called for burn-in and scored events
// alike, after the event has been used, so the covariate is always history strictly before
// the event it weights.
func (h *historyWeighting) seen(entity string) {
	if h == nil || h.mode == weightingNone {
		return
	}
	h.entityEvents[entity]++
}

// observeBurnIn files one arm's burn-in observation into the fitting sample.
func (h *historyWeighting) observeBurnIn(id detector.ID, logP, covariate float64) {
	if !h.on() || h.frozen {
		return
	}
	s, ok := h.sample[id]
	if !ok {
		s = &covariateSample{}
		h.sample[id] = s
	}
	s.add(logP, covariate)
}

// observeBurnIn files one burn-in event into every arm's fitting sample and counts it
// against its entity's history.
//
// It reads the same per-arm minimum over verdicts that the scoring path ranks on, because a
// weight fitted under one ranking and applied under another is fitted on nothing.
func (h *historyWeighting) observeBurnInEvent(entity string, verdicts detector.Verdicts) {
	if !h.on() || h.frozen {
		return
	}
	covariate := h.covariateFor(entity)

	best := map[detector.ID]float64{}
	for _, v := range verdicts {
		logP, ok := v.LogPValue()
		if !ok {
			continue
		}
		if prev, seen := best[v.DetectorID()]; !seen || logP < prev {
			best[v.DetectorID()] = logP
		}
	}
	ids := make([]detector.ID, 0, len(best))
	for id := range best {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		h.observeBurnIn(id, best[id], covariate)
	}
	h.seen(entity)
}

// freeze fits one weight table per arm from the burn-in sample and closes the fit. It is
// called once, at the boundary, before the first scored event.
func (h *historyWeighting) freeze() error {
	if !h.on() || h.frozen {
		return nil
	}
	h.frozen = true

	ids := make([]detector.ID, 0, len(h.sample))
	for id := range h.sample {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	opts := calibration.DefaultStratifiedOptions()
	// One fold: the burn-in and scoring windows are already disjoint, so the weights are
	// independent of every p-value they will rank. See this file's header.
	opts.Folds = 1

	for _, id := range ids {
		s := h.sample[id]
		pValues := make([]float64, len(s.logP))
		for i, lp := range s.logP {
			// exp underflows to zero deep in the tail, which is correct here: Storey's
			// estimator counts p-values above λ = 0.5, and an underflowed p is below it
			// under any reading.
			pValues[i] = math.Exp(lp)
		}
		_, report, err := calibration.StratifiedWeights(pValues, s.covariate, opts)
		if err != nil {
			return fmt.Errorf("history weighting: fitting %s: %w", id, err)
		}
		logWeight := make([]float64, len(report.Weight))
		floored := make([]bool, len(report.Weight))
		for g, w := range report.Weight {
			// A degenerate fit found no signal in any stratum, so every learned weight is
			// zero and [calibration.StratifiedWeights] falls back to uniform. The table
			// has to follow that fallback rather than read the raw zeros out of the
			// report, or the arm is silenced outright.
			//
			// Not hypothetical: the first run of this code made `noveltyrate` and
			// `pairing` disappear from the result entirely, because Storey's estimate
			// clamped to one in all five of their strata and the table took the zeros at
			// face value.
			if report.Degenerate {
				logWeight[g] = 0
				continue
			}
			if w <= 0 {
				w = zeroWeightFloor(report.Counts[g])
				floored[g] = true
			}
			logWeight[g] = math.Log(w)
		}
		h.table[id] = weightTable{
			bounds: report.Bounds, logWeight: logWeight, floored: floored, report: report,
		}
	}
	return nil
}

// zeroWeightFloor is the smallest weight a stratum may carry, given how many burn-in
// observations it was estimated from.
//
// Grouped Benjamini–Hochberg gives a stratum the weight (1−π̂₀)/π̂₀, so a stratum whose
// estimated null proportion clamps to exactly one gets weight zero. Under weighted
// Benjamini–Hochberg that is meaningful and correct: a zero weight excludes the stratum from
// discovery, which is what "no signal here" should imply for a threshold procedure, and the
// domain package implements it that way.
//
// This is not a threshold procedure. The selection here is the day's top B alerts, and a
// budget is a capacity to fill: dropping an event because its stratum was down-weighted
// leaves an analyst slot empty rather than giving it to the next-best event, which no
// operator would choose and which is not what the weighting is for. So a zero-weighted
// stratum is put at the back of the queue instead of out of it.
//
// The floor is where the estimator itself runs out of resolution: π̂₀ = 1 − 1/(2n) is the
// most extreme null proportion a stratum of n observations can distinguish from one, giving
// a weight of 1/(2n−1). A finite sample cannot support "exactly zero non-nulls" any more
// precisely than that, so this is the estimate's own limit rather than a chosen constant.
//
// It matters in practice. On the first measured run `marginal`'s longest-history stratum
// clamped to π̂₀ = 1 and its weight to zero, which silenced 231,060 scored events — the
// busiest accounts on the corpus, excluded from that arm entirely by an estimate that a
// sample of 40,000 cannot make that sharply.
func zeroWeightFloor(observations int) float64 {
	if observations < 1 {
		return 1
	}
	return 1 / (2*float64(observations) - 1)
}

// adjust returns the arm's weighted log p-value for one event.
//
// The weighted score is ln(p/w) = ln p − ln w, so an up-weighted stratum moves towards the
// head of the queue and a down-weighted one towards the back. Every weight is finite, so
// every event remains rankable and the budget is always fillable; see [zeroWeightFloor].
func (h *historyWeighting) adjust(id detector.ID, entity string, logP float64) float64 {
	if !h.on() {
		return logP
	}
	table, ok := h.table[id]
	if !ok {
		// No burn-in evidence about this arm: it scored nothing before the boundary, so
		// there is nothing to weight by and the arm runs unweighted. Visible in the result
		// as a missing table rather than passed over silently.
		return logP
	}
	return logP - table.logWeightFor(h.covariateFor(entity))
}

// record is the weighting's block in the result file: enough to recompute any alert's
// ranking key by hand from the recorded numbers (R5).
func (h *historyWeighting) record() map[string]any {
	if h == nil || h.mode == weightingNone {
		return map[string]any{
			"mode": string(weightingNone),
			"note": "the selection is ranked on each arm's own p-value, unweighted. This " +
				"is the configuration every earlier run used and the baseline the " +
				"weighted runs are compared against",
		}
	}

	ids := make([]detector.ID, 0, len(h.table))
	for id := range h.table {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	arms := make(map[string]any, len(ids))
	for _, id := range ids {
		t := h.table[id]
		strata := make([]map[string]any, 0, len(t.logWeight))
		for g := range t.logWeight {
			row := map[string]any{
				"stratum":         g,
				"burn_in_events":  t.report.Counts[g],
				"null_proportion": t.report.NullProportion[g],
				"weight":          t.report.Weight[g],
			}
			if g > 0 {
				row["covariate_above"] = t.bounds[g-1]
			}
			if g < len(t.bounds) {
				row["covariate_upto"] = t.bounds[g]
			}
			if g < len(t.floored) && t.floored[g] {
				row["floored"] = true
				row["applied_weight"] = math.Exp(t.logWeight[g])
				row["note"] = "the burn-in window found this stratum entirely null, so " +
					"grouped Benjamini-Hochberg weighted it to zero. A zero weight would " +
					"drop the stratum's events instead of ranking them last, which for a " +
					"top-B selection leaves budget unspent, so the weight is floored at " +
					"the estimator's own resolution limit"
			}
			strata = append(strata, row)
		}
		burnIn := 0
		floored := 0
		for g, c := range t.report.Counts {
			burnIn += c
			if g < len(t.floored) && t.floored[g] {
				floored++
			}
		}
		arms[string(id)] = map[string]any{
			"strata":          strata,
			"degenerate":      t.report.Degenerate,
			"floored_strata":  floored,
			"burn_in_sampled": burnIn,
		}
	}

	return map[string]any{
		"mode":      string(h.mode),
		"covariate": "ln(1 + the entity's history length at scoring time)",
		"lambda":    calibration.DefaultStratifiedOptions().Lambda,
		"strata":    calibration.DefaultStratifiedOptions().Strata,
		"folds":     1,
		"entities":  len(h.entityEvents),
		"arms":      arms,
		"estimator": "grouped Benjamini-Hochberg weights (1-pi0)/pi0 from Storey's pi0",
		"fitted_on": "the burn-in window, disjoint from every scored event by construction",
		"applied_as": "ln p - ln w, replacing the arm's log p-value as the ranking key, " +
			"with the unweighted value retained as model_log_p",
		"note": "weights are fitted on burn-in and frozen at the boundary, so no weight " +
			"reads a p-value it then ranks. Cross-fitting is the defence when weights and " +
			"p-values must come from one sample; disjoint windows are stronger, so the fit " +
			"runs with one fold",
	}
}

// historyOfLabelled reports p x n for the labelled events, which is the statistic #15's
// premise rests on: if the weighting has done anything, this is where it shows.
//
// The median of p x n was 1.15 across the planted victims under the unweighted ranking,
// which is the sense in which the ranking was sorting accounts by size. Recomputing it
// under whatever ranking the run used says whether that dependence actually moved, rather
// than assuming the weighting removed it because it was designed to.
func historyOfLabelled(scores []redTeamScore) map[string]any {
	products := make([]float64, 0, len(scores))
	for _, s := range scores {
		if s.HistoryN <= 0 || s.P <= 0 {
			continue
		}
		products = append(products, s.P*float64(s.HistoryN))
	}
	if len(products) == 0 {
		return map[string]any{
			"recorded": false,
			"note": "no labelled event carried both a positive p-value and a history " +
				"length: p x n is undefined where the p-value underflowed to zero, and " +
				"reporting zero for it would read as a removed dependence rather than an " +
				"unrepresentable one",
		}
	}
	sort.Float64s(products)
	return map[string]any{
		"recorded": true,
		"n":        len(products),
		"median":   quantileOf(products, 0.5),
		"min":      products[0],
		"max":      products[len(products)-1],
		"p25":      quantileOf(products, 0.25),
		"p75":      quantileOf(products, 0.75),
		"note": "p x n over labelled events with a representable p-value. Near one means " +
			"the ranking is close to sorting accounts by history length, which is the " +
			"dependence #15 sets out to break; far from one means it has moved",
	}
}

// quantileOf is the linear-interpolated quantile of an ascending slice.
func quantileOf(sorted []float64, q float64) float64 {
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
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}
