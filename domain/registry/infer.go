package registry

import (
	"slices"
	"strings"
)

// Policy holds the thresholds governing inference. It is data, so a source can be
// onboarded by supplying a policy rather than by changing code, which is what E6
// measures.
type Policy struct {
	// MinObservations is the evidence required before a kind is asserted at all.
	// Below it the kind is unknown, and detectors abstain rather than guess.
	MinObservations int

	// IdentifierRatio is the ratio of distinct values to observations at or above
	// which a field is an identifier. §5.1 names this ratio as "the discriminating
	// statistic" and describes the target as a field taking a distinct value "on
	// essentially every event", so the threshold is near one rather than at it: a
	// GUID field that happens to repeat once in ten thousand events is still an
	// identifier.
	IdentifierRatio float64

	// IdentifierMinObservations is the separate, larger evidence bar for the
	// identifier verdict. Early in a field's life every value is new, so a small
	// sample makes any field look like an identifier; requiring more evidence here
	// than for the other kinds prevents a categorical field being permanently
	// misclassified on its first few events.
	IdentifierMinObservations int

	// BooleanCardinality is the exact distinct-value count at which a field with
	// recognised tokens is boolean. §6.1 states that a boolean field is the case
	// K = 2, so this is an equality rather than an upper bound: a field observed with
	// one value is a constant categorical field, not a boolean.
	BooleanCardinality int

	// NumericFraction is the fraction of observed values that must parse as numbers
	// for the field to be numeric. Below one, so that a mostly numeric field with a
	// few sentinel tokens is still numeric. What counts as a number is [ParseNumber]
	// and not strconv.ParseFloat, so that a sentinel spelled "NaN" counts against a
	// field being numeric exactly as one spelled "unknown" does.
	NumericFraction float64

	// DiscreteMaxRatio is the ratio of distinct values to observations at or below
	// which a numeric field is discrete rather than continuous — equivalently, one
	// over the average number of times a value recurs.
	//
	// The question it settles is whether the field presents a vocabulary worth
	// counting. Equation (4) estimates p̂(v) from n_v, and at an average multiplicity
	// of one that estimate is the smoothing prior and nothing else: every observation
	// is its own singleton and the count carries no information the prior did not
	// already assert. The default asks for an order of magnitude of repetition, so a
	// counted value has been seen about ten times before its count is relied upon.
	//
	// It is deliberately far below IdentifierRatio, because the two decisions differ
	// in what they cost when wrong. The identifier verdict removes a field from
	// scoring altogether, so it is made only at near-certainty; this one chooses
	// between two ways of scoring a field that is scored either way, so it can afford
	// to be decided at the point where counting stops being informative.
	DiscreteMaxRatio float64

	// MaxTrackedValues bounds the value set held per field, which §13.3 requires for
	// finite state. When the bound binds, the registry records that it did, because
	// beyond it the reserved novelty mass of equation (4) is no longer exact and
	// §13.3 requires the condition be reported rather than concealed.
	MaxTrackedValues int

	// ExcludedFields are field paths configuration keeps out of scoring entirely.
	ExcludedFields []string
}

// DefaultPolicy returns the thresholds used unless a source overrides them.
func DefaultPolicy() Policy {
	return Policy{
		MinObservations:           50,
		IdentifierRatio:           0.95,
		IdentifierMinObservations: 200,
		BooleanCardinality:        2,
		NumericFraction:           0.99,
		DiscreteMaxRatio:          0.1,
		MaxTrackedValues:          10_000,
	}
}

// booleanPairs are the matched opposites recognised as a binary field, lower-cased.
//
// LANL's success/failure field takes Success and Fail and CERT's activity field takes
// Logon and Logoff, so those pairs are included. The list is data rather than logic so
// that a new source's vocabulary is a configuration change; a pair of tokens not listed
// here classifies as categorical, which is harmless because equation (4) treats boolean
// as the case K = 2 anyway.
//
// # Pairs, not a set of tokens
//
// This was a flat set of recognised tokens, tested by asking whether every observed
// value belonged to it. That test cannot distinguish a binary field from a field whose
// two values happen each to be recognised: Success and Logoff passed it, and so did
// Fail and Enabled, though neither is a pair and neither reading of "the other value"
// means anything. Matching the pair asks the question the label actually claims, and it
// is what makes extending the vocabulary safe — every token added to a flat set widened
// the accidental matches multiplicatively, where a pair added here matches only itself.
var booleanPairs = [][2]string{
	{"true", "false"},
	{"t", "f"},
	{"yes", "no"},
	{"y", "n"},
	{"1", "0"},
	{"on", "off"},
	{"up", "down"},
	{"success", "fail"},
	{"success", "failure"},
	{"pass", "fail"},
	{"enabled", "disabled"},
	{"allow", "deny"},
	{"permit", "deny"},
	{"accept", "reject"},
	{"granted", "denied"},
	{"present", "absent"},
	{"active", "inactive"},
	{"logon", "logoff"},
	{"start", "end"},
}

// Infer classifies a field from its observed statistics.
//
// The order of the rules is part of the specification, not an implementation detail,
// because several conditions can hold at once:
//
//  1. Configuration wins. An excluded field is excluded whatever its statistics say.
//  2. Insufficient evidence yields unknown, so nothing is asserted on a handful of
//     events.
//  3. The identifier guard runs before the categorical and boolean rules. An
//     identifier would otherwise look categorical, and if it is admitted on that
//     basis the guard has failed; §5.1 records the three consequences.
//  4. Boolean is the case K = 2 exactly, per §6.1, and is tested before numeric
//     because the tokens "0" and "1" satisfy both and the boolean reading is the
//     informative one.
//  5. Numeric, split into discrete and continuous by how often a value recurs, then
//     categorical as the residual.
//
// # Discrete and continuous are one rule, not two kinds of number
//
// Both are numeric; what separates them is whether the field's observed support is
// bounded enough to count. A status code recurs hundreds of times and is a vocabulary;
// a byte count is a fresh value on nearly every event and is not. The statistic is the
// same distinct-to-observation ratio §5.1 already names, read against a much lower
// threshold — see [Policy.DiscreteMaxRatio] for why the threshold differs from the
// identifier guard's rather than reusing it.
//
// Integrality deliberately plays no part. A field taking 0.5, 1.0 and 1.5 has a support
// of three values and should be counted, and a field of whole-byte counts should not be
// counted merely because its values are integers. Discreteness is a property of the
// support, and the ratio measures the support directly where integrality only correlates
// with it.
//
// # Why the identifier guard exempts numeric fields
//
// §5.1 gives the discriminating statistic for an identifier as the ratio of distinct
// values to observations. Taken alone that statistic does not separate an opaque
// high-cardinality token from a continuous measurement: a byte count or a duration
// takes a distinct value on nearly every event and so scores a ratio near one, exactly
// as a GUID does.
//
// Reading §5.1 together with §8.2 resolves it. §8.2 admits numeric fields to the
// co-occurrence graph "through quantile bins from a streaming digest", and that
// mechanism independently prevents both harms the guard exists to prevent: state stays
// bounded by the number of bins rather than by the number of distinct values, and graph
// nodes are bins rather than singletons, so the block structure survives. The guard is
// therefore unnecessary for a numeric field, and applying it there would be actively
// harmful, silently discarding a real signal by classifying a measurement as an
// identifier.
//
// The residual risk is a purely numeric surrogate key, an auto-incrementing event id,
// which this rule admits as numeric. That is the safe direction: quantile binning
// bounds its state and its graph degree, so it wastes a little capacity rather than
// dissolving the partition. Where such a field is known, configuration excludes it,
// which is why ExcludedFields exists.
func Infer(stats FieldStats, policy Policy) FieldKind {
	if slices.Contains(policy.ExcludedFields, stats.Path) {
		return KindExcluded
	}

	if stats.Observations < policy.MinObservations {
		return KindUnknown
	}

	isNumeric := stats.Observations > 0 &&
		float64(stats.NumericParses)/float64(stats.Observations) >= policy.NumericFraction

	// The identifier guard, ahead of the categorical and boolean rules but exempting
	// numeric measurements for the reason set out above.
	if !isNumeric &&
		stats.Observations >= policy.IdentifierMinObservations &&
		stats.DistinctRatio() >= policy.IdentifierRatio {
		return KindIdentifier
	}

	// §6.1 states that a boolean field is the case K = 2, so the test is equality, not
	// an upper bound. A field observed with a single value is a constant categorical
	// field: it contributes no novelty signal, since every observation is the known
	// value, but it is not a boolean and should not be labelled one.
	if stats.DistinctValues() == policy.BooleanCardinality && stats.IsBooleanPair() {
		return KindBoolean
	}

	if isNumeric {
		if stats.DistinctRatio() <= policy.DiscreteMaxRatio {
			return KindDiscrete
		}
		return KindContinuous
	}

	return KindCategorical
}

// FieldStats are the sufficient statistics supporting a classification, per §5.1.
//
// They are accumulated by observation and are themselves the evidence the registry
// records, so a reader can see why a field was classified as it was rather than
// having to trust the label.
type FieldStats struct {
	// Path is the field path these statistics describe.
	Path string

	// Observations is the number of events in which the field was present and usable.
	Observations int

	// NumericParses is how many of those values parsed as a number.
	NumericParses int

	// UnusableObservations counts values present but not interpretable, such as
	// LANL's literal "?". They are excluded from Observations so that a field which
	// is mostly "?" is not classified from its few real values, and are reported
	// separately because a rising rate is itself a signal (§5.3).
	UnusableObservations int

	// values is the tracked value set, bounded by Policy.MaxTrackedValues.
	values map[string]struct{}

	// truncated records that the value-set bound bound. Beyond it the distinct count
	// is a lower bound rather than exact, and §13.3 requires that this be reported.
	truncated bool

	// FirstSeen and LastSeen are event-time microseconds, never wall clock.
	FirstSeen int64
	LastSeen  int64
}

// NewFieldStats returns empty statistics for a path.
func NewFieldStats(path string) *FieldStats {
	return &FieldStats{Path: path, values: make(map[string]struct{})}
}

// Observe folds one usable value into the statistics.
//
// at is the event timestamp. No wall clock is consulted, so that replaying a corpus
// reproduces the same registry (R4).
func (s *FieldStats) Observe(value string, at int64, maxTracked int) {
	s.Observations++

	if _, ok := ParseNumber(value); ok {
		s.NumericParses++
	}

	if _, known := s.values[value]; !known {
		if maxTracked > 0 && len(s.values) >= maxTracked {
			s.truncated = true
		} else {
			s.values[value] = struct{}{}
		}
	}

	if s.FirstSeen == 0 || at < s.FirstSeen {
		s.FirstSeen = at
	}
	if at > s.LastSeen {
		s.LastSeen = at
	}
}

// ObserveUnusable records a value that was present but not interpretable.
func (s *FieldStats) ObserveUnusable(at int64) {
	s.UnusableObservations++
	if s.FirstSeen == 0 || at < s.FirstSeen {
		s.FirstSeen = at
	}
	if at > s.LastSeen {
		s.LastSeen = at
	}
}

// DistinctValues returns the number of distinct values tracked. When the value-set
// bound has bound this is a lower bound; [FieldStats.IsTruncated] reports that.
func (s *FieldStats) DistinctValues() int { return len(s.values) }

// DistinctRatio is the discriminating statistic of §5.1: distinct values over
// observations. It is 0 with no observations, so an empty field is never an
// identifier.
//
// When the value set has been truncated the ratio is computed from the bound, which
// understates it. That is the safe direction: it can only fail to classify something
// as an identifier, never misclassify an ordinary field as one. A field hitting the
// bound is reported so the condition is visible.
func (s *FieldStats) DistinctRatio() float64 {
	if s.Observations == 0 {
		return 0
	}
	return float64(len(s.values)) / float64(s.Observations)
}

// IsBooleanPair reports whether the field's two observed values are a matched pair of
// recognised opposites, per §6.1's K = 2. It is false for any other distinct count, so
// a constant field is not a binary one and neither is a three-valued field.
//
// The comparison is case-insensitive and order-independent: a source may emit either
// value of a pair first, and map iteration order must not be able to reach the answer
// (R4), so both orders are tested rather than the values being sorted. The value set is
// read directly rather than through [FieldStats.Values], which allocates — this runs
// once per field per event.
func (s *FieldStats) IsBooleanPair() bool {
	if len(s.values) != 2 {
		return false
	}

	// Indexed rather than sentinel-checked: the empty string is a legitimate field
	// value, so "have I filled the first slot yet" cannot be asked of its contents.
	var observed [2]string
	n := 0
	for v := range s.values {
		observed[n] = strings.ToLower(v)
		n++
	}

	for _, pair := range booleanPairs {
		if observed[0] == pair[0] && observed[1] == pair[1] {
			return true
		}
		if observed[0] == pair[1] && observed[1] == pair[0] {
			return true
		}
	}
	return false
}

// IsTruncated reports whether the value-set bound bound, in which case the reserved
// novelty mass of equation (4) is no longer exact (§13.3).
func (s *FieldStats) IsTruncated() bool { return s.truncated }

// Values returns the tracked value set in sorted order.
//
// Sorted because the result feeds evidence rendering and the float accumulation of
// equation (5), and map order would make both nondeterministic.
func (s *FieldStats) Values() []string {
	out := make([]string, 0, len(s.values))
	for v := range s.values {
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}
