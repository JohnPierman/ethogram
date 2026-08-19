package detector

// Status is the four-valued verdict status of §5.3.
//
// Abstention is not a score. A detector lacking the inputs it needs must return one
// of the three abstained values, and §10.2 then drops the verdict from the degrees
// of freedom of equation (18) rather than admitting a fabricated p-value. A neutral
// "0.5 because we do not know" anywhere in this codebase is a defect: it asserts
// normality on no evidence, contrary to R3.
type Status uint8

const (
	// StatusUnknown is the zero value and is never a legitimate verdict status. It
	// exists so that a Verdict built by struct literal rather than by constructor
	// fails validation instead of silently reading as evaluated.
	StatusUnknown Status = iota

	// StatusEvaluated: the detector scored the event and the verdict carries a
	// p-value. Only this status contributes to J in §10.2.
	StatusEvaluated

	// StatusAbstainedStructural: the source does not produce these inputs at all.
	// Expected and uninformative; no Beta posterior on field presence is updated.
	StatusAbstainedStructural

	// StatusAbstainedUnexpected: the input is ordinarily present for this source but
	// is absent here. §5.3 updates a Beta posterior on field presence per
	// (source, f) for this case, so that a source silently ceasing to emit a field
	// is detected as such instead of manifesting as quietly degraded scores.
	StatusAbstainedUnexpected

	// StatusAbstainedUnusable: the input is present but not scoreable — below a
	// minimum observation count (§9), or uninterpretable, as LANL's literal "?".
	StatusAbstainedUnusable
)

// String returns the status name as it appears in result JSON and in dashboard
// labels, matching the wording of §5.3.
func (s Status) String() string {
	switch s {
	case StatusEvaluated:
		return "evaluated"
	case StatusAbstainedStructural:
		return "abstained_structural"
	case StatusAbstainedUnexpected:
		return "abstained_unexpected"
	case StatusAbstainedUnusable:
		return "abstained_unusable"
	default:
		return "unknown"
	}
}

// IsEvaluated reports whether the verdict carries a p-value and so counts toward J.
func (s Status) IsEvaluated() bool { return s == StatusEvaluated }

// IsAbstained reports whether the status is one of the three abstained values.
func (s Status) IsAbstained() bool {
	switch s {
	case StatusAbstainedStructural, StatusAbstainedUnexpected, StatusAbstainedUnusable:
		return true
	default:
		return false
	}
}

// IsValid reports whether the status is one of the four values of §5.3.
func (s Status) IsValid() bool { return s.IsEvaluated() || s.IsAbstained() }

// Statuses returns the four §5.3 statuses in a fixed order, for table T3's status
// distribution. Returned as a slice rather than iterated from a map so that report
// column order is deterministic.
func Statuses() []Status {
	return []Status{
		StatusEvaluated,
		StatusAbstainedStructural,
		StatusAbstainedUnexpected,
		StatusAbstainedUnusable,
	}
}
