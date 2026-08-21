// Package routing assigns each entity to the detector whose null is best specified for it,
// rather than spending one budget on one detector for every entity.
//
// # Why routing and not weighting
//
// Expected detection is linear in a global allocation over detectors, so the best global
// allocation is always a single detector and no weighting can improve on it. Routing is not a
// global allocation: it chooses per entity, so it can send an account with two months of
// history to a per-entity null and an account with two days to a population one, and collect
// the detections of both. That is the only construction in this framework able to exceed the
// best single arm at equal cost, and the reason is complementarity — the population marginal's
// detections on the planted corpus overlap the per-entity arms' by zero.
//
// The recorded headroom is 460 to 535 labelled events against the best single arm's 384, the
// floor being what any per-entity policy matches by construction and the ceiling the union of
// every arm at full depth. Both are oracles and neither accounts for the alert cost of
// attaining it; they bound what a policy can be worth, not what one is worth.
//
// # What may be routed on
//
// Only frozen state: what the entity's history looked like at the burn-in boundary. Nothing
// here reads a label, a score, or any property of the event being classified, which is what
// separates a routing policy from a fitted combination rule — and section 5.5's oracle rows
// exist precisely to bound how much a rule that does read the labels could reach.
//
// The preference order is stated by the caller and never fitted. An order chosen to maximise
// detections on the evaluation labels would be an oracle, and would report the corpus back.
//
// # Abstention
//
// A [Policy] with no admitting arm returns no arm. Under R3 that is the required answer: an
// entity for which no detector's null is well specified has no opinion available, and routing
// it somewhere anyway would manufacture one. Abstention is a measurement and is reported.
package routing

import (
	"errors"
	"fmt"
)

// ErrPolicy reports a preference order that cannot be used.
var ErrPolicy = errors.New("routing: the preference order is malformed")

// Profile is the frozen per-entity state a policy may read: what the entity's history held at
// the boundary, and nothing about the event being scored.
type Profile struct {
	// Events is how many of the entity's own events the burn-in observed.
	Events int64
	// DistinctValues is the size of the entity's categorical vocabulary, which is what a
	// novelty null is estimated over.
	DistinctValues int64
	// CompletedPeriods is how many whole periods the volume posterior rests on.
	CompletedPeriods int64
	// HasTimingSpread is false for a perfectly regular account, which has no spread for a
	// standardised circular statistic to be formed against.
	HasTimingSpread bool
}

// Requirement is one detector's declared precondition for its null to be well specified.
//
// Declared rather than inferred. Each detector already abstains on its own inputs at scoring
// time; stating the same condition here lets the choice be made once per entity instead of
// discovered once per event, and makes the policy readable as a table.
type Requirement struct {
	Arm                 string
	MinEvents           int64
	MinDistinctValues   int64
	MinCompletedPeriods int64
	NeedsTimingSpread   bool
}

// Admits reports whether the entity's frozen state satisfies this detector's precondition.
func (r Requirement) Admits(p Profile) bool {
	switch {
	case p.Events < r.MinEvents:
		return false
	case p.DistinctValues < r.MinDistinctValues:
		return false
	case p.CompletedPeriods < r.MinCompletedPeriods:
		return false
	case r.NeedsTimingSpread && !p.HasTimingSpread:
		return false
	default:
		return true
	}
}

// Policy routes an entity to the first detector in a stated preference order whose null the
// entity's frozen state admits.
//
// A value object, validated on construction and compared by the order it holds.
type Policy struct {
	order []Requirement
}

// NewPolicy returns the policy that prefers the given requirements in the order supplied.
//
// The order is the policy. It is the caller's to state and to justify, and it is recorded with
// any run that uses it: a preference order is a modelling decision, and one arrived at by
// trying orders against the evaluation labels is an oracle wearing a policy's clothes.
func NewPolicy(order []Requirement) (Policy, error) {
	if len(order) == 0 {
		return Policy{}, fmt.Errorf("%w: no arms", ErrPolicy)
	}
	seen := make(map[string]bool, len(order))
	for _, r := range order {
		if r.Arm == "" {
			return Policy{}, fmt.Errorf("%w: an unnamed arm", ErrPolicy)
		}
		if seen[r.Arm] {
			return Policy{}, fmt.Errorf("%w: %q appears twice", ErrPolicy, r.Arm)
		}
		seen[r.Arm] = true
		if r.MinEvents < 0 || r.MinDistinctValues < 0 || r.MinCompletedPeriods < 0 {
			return Policy{}, fmt.Errorf(
				"%w: %q has a negative threshold", ErrPolicy, r.Arm)
		}
	}
	return Policy{order: append([]Requirement(nil), order...)}, nil
}

// Route returns the detector the entity is assigned to, or ok = false where no detector's null
// is well specified for it — an abstention under R3, not a fallback.
func (p Policy) Route(profile Profile) (arm string, ok bool) {
	for _, r := range p.order {
		if r.Admits(profile) {
			return r.Arm, true
		}
	}
	return "", false
}

// Admitted returns every detector whose null the entity admits, in preference order. Route
// takes the first; this is what a run records, so that the cost of the preference — how often
// a second admissible arm was passed over — is measurable rather than assumed.
func (p Policy) Admitted(profile Profile) []string {
	var out []string
	for _, r := range p.order {
		if r.Admits(profile) {
			out = append(out, r.Arm)
		}
	}
	return out
}

// Order returns the requirements in preference order, for a run to record.
func (p Policy) Order() []Requirement { return append([]Requirement(nil), p.order...) }
