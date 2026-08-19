// Package registry implements the field registry of §5.1.
//
// The registry is held as data, not as code. For each (source, field path) it records
// a kind together with the sufficient statistics supporting that classification.
// Detectors iterate the registry; no detector names a field. That is the mechanism
// discharging R2: no component requires advance knowledge of a field's type,
// cardinality, or value set, and a source carrying fields nobody anticipated is
// admitted without a change to any extractor.
//
// Inference is deterministic (R4). Given the same statistics it returns the same kind,
// with no sampling and no tie broken by map order.
//
// # A kind is inferred, never cast
//
// Corpus readers emit every value as text and it stays text. The registry records what
// a field's values have looked like and derives a kind from that record; it does not
// convert a value into a Go int64, bool, or float64, and no detector receives one.
//
// The distinction is not stylistic. Converting a column to a type means deciding its
// type from the rows in hand, and R1 forbids a score depending on the composition of
// the batch an event arrived in — the same value would parse as an integer beside one
// set of rows and as a string beside another. Text is also the identity the framework
// compares on ([github.com/JohnPierman/ethogram/domain/event.Value.Text]),
// so a conversion would silently merge values a source distinguishes: "007" and "7",
// "1.0" and "1", "TRUE" and "true". And it would be premature, because a field that has
// looked integral for forty-nine events can emit a sentinel on the fiftieth; statistics
// absorb that, a completed conversion cannot.
//
// What a detector needs is not a type but a representation it can score, which is what
// [FieldKind.Token] returns: the value's own text where the field's vocabulary is
// already bounded, and a fixed-boundary magnitude band where it is not.
package registry

// FieldKind is the classification of §5.1: one of categorical, boolean, discrete,
// continuous, identifier, excluded, or unknown.
type FieldKind uint8

const (
	// KindUnknown is the zero value: too little evidence to classify. An unknown
	// field is not eligible for the co-occurrence graph and yields
	// abstained_unusable from detectors that require a settled kind, rather than
	// being guessed at.
	KindUnknown FieldKind = iota

	// KindCategorical is the default for a field with a bounded, recurring value set.
	// Detector I (§6) scores these.
	KindCategorical

	// KindBoolean is the case K = 2 of equation (4), per §6.1. It is not a separate
	// estimator, only a separate label, which is why the paper notes that a boolean
	// field needs no special handling. What makes it that case is a matched pair of
	// recognised tokens, not two tokens that happen each to be recognised; see
	// [FieldStats.IsBooleanPair].
	KindBoolean

	// KindDiscrete is a numeric field whose observed support is bounded and
	// recurring: a status code, a logon type, an error number, a small count. Its
	// values are already a vocabulary, so equation (4) counts them exactly as it
	// counts a categorical field's, and the graph of §8.2 admits them unmodified.
	//
	// Discreteness here is a property of the support rather than of the spelling. A
	// field taking 0.5, 1.0 and 1.5 is a counted vocabulary of three values, and
	// classifying it by integrality would band it and collapse two of the three for
	// no gain. The discriminating statistic is therefore how often a value recurs —
	// [Policy.DiscreteMaxRatio].
	KindDiscrete

	// KindContinuous is a numeric field whose support is open: a byte count, a
	// duration, a score. Nearly every observation is a value never seen before, so
	// the field is scored two different ways and neither is the raw text.
	//
	// At population scope (§9) it is tailed against the streaming quantile digest,
	// which is the finest instrument available and needs no stable value identity
	// because it stores no per-value rows. At entity scope (§6, §7, §8) it enters
	// through [Band], because those detectors accumulate a decayed count per value
	// and a graph node per value, and raw measurements would saturate the first and
	// dissolve the second.
	KindContinuous

	// KindIdentifier is the load-bearing case. A field taking a distinct value on
	// essentially every event is formally maximally novel on every observation.
	// Untreated it saturates any novelty detector, induces unbounded state growth,
	// and in §8 renders every one of its graph nodes a singleton, dissolving the
	// block structure the detector depends on. An identifier field contributes no
	// state and no verdicts; the §12.5 identifier control asserts exactly that.
	KindIdentifier

	// KindExcluded is set by configuration, for a field deliberately kept out of
	// scoring. Distinguished from identifier so that the reason for exclusion is
	// visible in the registry rather than inferred.
	KindExcluded
)

// String returns the kind name as it appears in §5.1, in result JSON, and in
// dashboard labels.
func (k FieldKind) String() string {
	switch k {
	case KindCategorical:
		return "categorical"
	case KindBoolean:
		return "boolean"
	case KindDiscrete:
		return "discrete"
	case KindContinuous:
		return "continuous"
	case KindIdentifier:
		return "identifier"
	case KindExcluded:
		return "excluded"
	default:
		return "unknown"
	}
}

// IsEligible reports whether the field may participate in the co-occurrence graph of
// §8.2, which admits fields "whose registry kind is neither identifier nor excluded".
// An unknown kind is also withheld: admitting a field before its kind has settled
// would let an identifier in by default, which is the failure this guard exists to
// prevent.
//
// # Numeric fields enter as bands, and that is a deviation from §8.2's mechanism
//
// §8.2 admits numeric fields to the graph after binning them into quantiles from a
// streaming digest. Adaptive quantile binning is still not implemented, and what
// stands in its place is the fixed-boundary banding of [Band]. The reason is recorded
// there in full and is not a matter of convenience: a graph edge weight and a
// Detector I count are both persisted per value, so an adaptive boundary would change
// what a stored label means while the counts filed under it stayed put.
//
// What the earlier state of this code did instead was withhold numeric fields
// altogether, on the correct reasoning that admitting them *unbinned* is not a partial
// version of §8.2 but the failure §8.2 prevents — the node is the value's text, so
// every distinct measurement becomes its own node and a byte count behaves exactly as
// an identifier does. That reasoning is unchanged. What has changed is that a binning
// now exists, so the choice is no longer between an unbinned field and no field.
func (k FieldKind) IsEligible() bool {
	switch k {
	case KindCategorical, KindBoolean, KindDiscrete, KindContinuous:
		return true
	default:
		return false
	}
}

// IsScoreable reports whether Detector I (§6) should score the field — equivalently,
// whether the field presents a vocabulary that can be counted per entity.
//
// Every kind for which this holds has a [FieldKind.Token], and a detector must score
// that token rather than the value's text: for a continuous field the two differ, and
// counting the text is the saturation [FieldKind.IsEligible] describes.
func (k FieldKind) IsScoreable() bool {
	switch k {
	case KindCategorical, KindBoolean, KindDiscrete, KindContinuous:
		return true
	default:
		return false
	}
}

// UsesNumericMarginal reports whether Detector IV (§9) should score the field against
// the streaming quantile digest rather than against the discrete tail of equation (5).
//
// Only a continuous field. A discrete field's support is a bounded vocabulary, and
// equation (5) over the vocabulary itself is strictly finer than a tail over bands of
// it — there is nothing to gain by discarding the exact counts. The asymmetry with
// entity scope is deliberate: §9 keeps no per-value rows, so it is free to use the
// finest instrument, while §6 must keep an identity stable across a seven-day
// half-life and cannot.
func (k FieldKind) UsesNumericMarginal() bool { return k == KindContinuous }

// Token returns the representation a counted-vocabulary detector must use for a value
// of a field of this kind, and whether the value can be represented at all.
//
// This is the whole of the framework's answer to mixed variable types, and it is a
// projection rather than a conversion. For every kind whose vocabulary is already
// bounded — categorical, boolean, discrete — the token is the value's own text,
// unaltered, because that text is the identity the framework compares on and coercing
// it would merge values a source distinguishes. For a continuous field the token is the
// value's magnitude band, because the raw measurement is not a vocabulary item.
//
// A false result means the value cannot be scored by this detector and the caller must
// abstain as unusable (R3), never substitute a neutral score and never count the
// unrepresentable text as a token of its own — a sentinel admitted to the vocabulary
// would let its own rate masquerade as novelty. The kinds that contribute no vocabulary
// at all — unknown, identifier, excluded — decline everything, so a caller that reaches
// here without checking [FieldKind.IsScoreable] fails closed.
func (k FieldKind) Token(text string) (string, bool) {
	switch k {
	case KindCategorical, KindBoolean, KindDiscrete:
		return text, true
	case KindContinuous:
		x, ok := ParseNumber(text)
		if !ok {
			return "", false
		}
		return Band(x), true
	default:
		return "", false
	}
}

// Kinds returns the seven kinds in a fixed order, for the status and kind distributions
// reported in the tables. Returned as a slice rather than iterated from a map so that
// report column order is deterministic.
func Kinds() []FieldKind {
	return []FieldKind{
		KindCategorical, KindBoolean, KindDiscrete, KindContinuous,
		KindIdentifier, KindExcluded, KindUnknown,
	}
}
