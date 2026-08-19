package detector

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"

	"github.com/JohnPierman/ethogram/domain/event"
)

// Errors returned when constructing a verdict.
var (
	ErrPValueRange     = errors.New("detector: p-value must lie in (0,1]")
	ErrNotAbstained    = errors.New("detector: status is not an abstained_* value")
	ErrStatusInvalid   = errors.New("detector: status is not one of the four §5.3 values")
	ErrNoEvidence      = errors.New("detector: evidence is required (R5)")
	ErrEmptyDetectorID = errors.New("detector: detector id is required")
)

// ID names a detector. It appears in result JSON keys and dashboard labels, and is
// used to look up a per-detector calibration set in §10.1.
type ID string

// Evidence carries the sufficient statistics that produced a verdict.
//
// R5 requires every verdict to carry evidence sufficient for an analyst to
// reconstruct it by hand, and E7 measures the proportion of verdicts that actually
// are reconstructable. The test is strict: the dashboard renders a verdict card from
// evidence alone, with no query back to the store, so anything the recomputation
// needs must be present here.
//
// Keys are sorted on serialisation, so evidence renders and hashes identically
// across runs.
type Evidence struct {
	// Equation records which numbered equations of the paper produced the verdict,
	// so a reader can hold the paper beside the evidence card.
	Equations []int

	// Stats are the named sufficient statistics, for instance n_v, N, K and alpha
	// for equations (4) and (5), or k_i, k_j, D_r, D_s, m_rs and w_ij for (14).
	Stats map[string]float64

	// Labels are non-numeric statistics, such as the observed value, block
	// identities, or the partition's seed and graph checksum.
	Labels map[string]string

	// Caveats record conditions that qualify the verdict rather than invalidate it:
	// that the (15) single-block fallback was used because no partition was
	// available, or that value-set pruning has made the reserved novelty mass of
	// equation (4) inexact, which §13.3 requires be reported and not concealed.
	Caveats []string
}

// NewEvidence returns an evidence record. Maps are copied so the caller cannot
// mutate a verdict after the fact.
func NewEvidence(equations []int, stats map[string]float64, labels map[string]string, caveats ...string) Evidence {
	return Evidence{
		Equations: slices.Clone(equations),
		Stats:     maps.Clone(stats),
		Labels:    maps.Clone(labels),
		Caveats:   slices.Clone(caveats),
	}
}

// StatNames returns the statistic names in sorted order, which is the order in
// which evidence must be serialised and rendered.
func (ev Evidence) StatNames() []string { return slices.Sorted(maps.Keys(ev.Stats)) }

// LabelNames returns the label names in sorted order.
func (ev Evidence) LabelNames() []string { return slices.Sorted(maps.Keys(ev.Labels)) }

// IsEmpty reports whether the evidence carries nothing at all, which R5 forbids.
func (ev Evidence) IsEmpty() bool {
	return len(ev.Stats) == 0 && len(ev.Labels) == 0
}

// Verdict is one element of the finite set returned by Score (§5.2).
//
// The p-value is unexported and reachable only through [Verdict.PValue], which
// reports it as absent unless the status is evaluated. An abstained verdict
// therefore has no representable p-value: the "0.5 because we do not know" that R3
// forbids cannot be constructed, rather than merely being discouraged by review.
type Verdict struct {
	detectorID ID
	target     Target
	status     Status
	p          float64
	// logP is ln p, carried alongside because p underflows and a floored p is a tie.
	//
	// This is the third time this codebase has met the same defect. The combination met
	// it first — past X² ≈ 1450 the χ² tail is below the least positive float64, so
	// every retained alert reported exactly zero and the budget was allocated by
	// timestamp. Volume met it second, in the negative-binomial tail. Detector III meets
	// it at the verdict boundary: its Poisson lower tail reaches subnormals, and 33 of
	// 262 labelled events on LANL days 7–8 came back at exactly 1.4e−322 or 2.77e−322 —
	// the float floor, times the Šidák factor. Every event past that point is tied with
	// every other, and no later stage can undo it: conformal calibration maps ties to
	// ties, and a minimum over tied values is a tie.
	//
	// So the log is the value and p is the reading of it. A detector that can compute
	// its tail in log space passes it through [NewEvaluatedLog]; one that cannot keeps
	// [NewEvaluated] and the log is taken of what it has, which is no worse than before.
	logP     float64
	evidence Evidence
	reason   string
}

// Target identifies what the verdict is about: the event, the entity, and where
// applicable the field or field pair under test. §8.5 emits one verdict per event
// rather than per edge, so a co-occurrence verdict names the minimising pair.
//
// Event is part of the target because a verdict is a statement about a specific
// event, which R5 requires be recoverable from the verdict alone, and because
// (detector, event, entity, fields) is the total order that makes a canonical
// reduction of concurrently produced verdicts well defined.
type Target struct {
	Event  event.ID
	Entity event.EntityID
	Fields []event.FieldPath
}

// NewEvaluatedLog returns an evaluated verdict from ln of its p-value, for a detector
// whose tail is computed in log space.
//
// It exists so that a tail below the least positive float64 survives. [NewEvaluated]
// cannot represent one: the caller has already lost it by the time it arrives, and the
// (0,1] guard would reject the zero it arrives as. Here the logarithm is the value —
// arbitrarily negative, and still ordered — while p is derived for display and floors
// at the smallest positive float64 without taking the ordering down with it.
//
// logP must be ≤ 0, since a p-value cannot exceed one, and must not be NaN. Negative
// infinity is rejected: it asserts an impossibility rather than an extreme, and no null
// in §6–§9 excludes the outcome it observed.
func NewEvaluatedLog(id ID, target Target, logP float64, ev Evidence) (Verdict, error) {
	if id == "" {
		return Verdict{}, ErrEmptyDetectorID
	}
	if math.IsNaN(logP) || math.IsInf(logP, -1) || logP > 0 {
		return Verdict{}, fmt.Errorf("%w: got ln p = %v", ErrPValueRange, logP)
	}
	if ev.IsEmpty() {
		return Verdict{}, ErrNoEvidence
	}
	p := math.Exp(logP)
	if p <= 0 {
		p = math.SmallestNonzeroFloat64
	}
	return Verdict{
		detectorID: id,
		target:     target,
		status:     StatusEvaluated,
		p:          p,
		logP:       logP,
		evidence:   ev,
	}, nil
}

// NewEvaluated returns an evaluated verdict carrying a p-value.
//
// The p-value must lie in (0,1]. Zero is rejected: every null in §6–§9 is a discrete
// or continuous tail mass that includes the observed outcome, so an exact zero
// indicates a computation error, and equation (18) takes its logarithm.
func NewEvaluated(id ID, target Target, p float64, ev Evidence) (Verdict, error) {
	if id == "" {
		return Verdict{}, ErrEmptyDetectorID
	}
	if math.IsNaN(p) || p <= 0 || p > 1 {
		return Verdict{}, fmt.Errorf("%w: got %v", ErrPValueRange, p)
	}
	if ev.IsEmpty() {
		return Verdict{}, ErrNoEvidence
	}
	return Verdict{
		detectorID: id,
		target:     target,
		status:     StatusEvaluated,
		p:          p,
		logP:       math.Log(p),
		evidence:   ev,
	}, nil
}

// NewAbstained returns an abstained verdict.
//
// It takes no p-value argument, so there is no syntax for attaching one. The status
// must be one of the three abstained values; passing StatusEvaluated is an error.
func NewAbstained(id ID, target Target, status Status, reason string, ev Evidence) (Verdict, error) {
	if id == "" {
		return Verdict{}, ErrEmptyDetectorID
	}
	if !status.IsValid() {
		return Verdict{}, fmt.Errorf("%w: %d", ErrStatusInvalid, status)
	}
	if !status.IsAbstained() {
		return Verdict{}, fmt.Errorf("%w: %s", ErrNotAbstained, status)
	}
	return Verdict{
		detectorID: id,
		target:     target,
		status:     status,
		reason:     reason,
		evidence:   ev,
	}, nil
}

// DetectorID returns the detector that produced the verdict.
func (v Verdict) DetectorID() ID { return v.detectorID }

// Target returns what the verdict is about.
func (v Verdict) Target() Target { return v.target }

// Status returns the §5.3 status.
func (v Verdict) Status() Status { return v.status }

// Reason returns the human-readable explanation for an abstention, empty for an
// evaluated verdict.
func (v Verdict) Reason() string { return v.reason }

// Evidence returns the sufficient statistics behind the verdict (R5).
func (v Verdict) Evidence() Evidence { return v.evidence }

// PValue returns the p-value and whether one exists.
//
// The second result is false for every abstained verdict. Callers must not
// substitute a default when it is false: §10.2 reduces the degrees of freedom
// instead, which is the whole mechanism by which a variable number of available
// fields is handled correctly.
func (v Verdict) PValue() (float64, bool) {
	if v.status != StatusEvaluated {
		return 0, false
	}
	return v.p, true
}

// LogPValue returns ln of the p-value and whether one exists.
//
// It is the quantity to rank, threshold and combine on; [Verdict.PValue] is the
// quantity to show on a verdict card. Where the two disagree the logarithm is right:
// p is floored at the least positive float64 so that (0,1] can hold it, and every
// verdict past that floor reads as the same number, while the logarithm goes on
// separating them. See the note on Verdict.logP for the three places this has bitten.
func (v Verdict) LogPValue() (float64, bool) {
	if v.status != StatusEvaluated {
		return 0, false
	}
	return v.logP, true
}

// Verdicts is the finite set of verdicts returned by one Score call (§5.2).
type Verdicts []Verdict

// Evaluated returns the subset with status evaluated, preserving order.
//
// Its length is J of §10.2, distinct from the graph edge weight m of §8.3.
func (vs Verdicts) Evaluated() Verdicts {
	out := make(Verdicts, 0, len(vs))
	for _, v := range vs {
		if v.status.IsEvaluated() {
			out = append(out, v)
		}
	}
	return out
}

// CountByStatus returns the number of verdicts holding each of the four §5.3
// statuses, for table T3.
func (vs Verdicts) CountByStatus() map[Status]int {
	counts := make(map[Status]int, 4)
	for _, s := range Statuses() {
		counts[s] = 0
	}
	for _, v := range vs {
		counts[v.status]++
	}
	return counts
}

// SortCanonical imposes a total order: detector id, then event, then target entity,
// then the joined field paths, and finally the verdict's own canonical bytes.
//
// Combination in §10.2 sums logarithms, and floating-point addition is not
// associative, so the summation order is part of the answer. Scoring may run
// concurrently provided results are put back into this order before combination;
// concurrency is permitted, nondeterministic reduction is not.
//
// The final comparison on canonical bytes is what makes the order total rather than
// merely stable. Without it, two verdicts agreeing on every key component but
// carrying different p-values would retain their arrival order, and a parallel
// reduction would not reproduce the sequential one.
func (vs Verdicts) SortCanonical() {
	slices.SortFunc(vs, func(a, b Verdict) int {
		if a.detectorID != b.detectorID {
			return cmpString(string(a.detectorID), string(b.detectorID))
		}
		if a.target.Event != b.target.Event {
			return bytes.Compare(a.target.Event[:], b.target.Event[:])
		}
		if a.target.Entity != b.target.Entity {
			return cmpString(string(a.target.Entity), string(b.target.Entity))
		}
		if c := cmpString(joinFields(a.target.Fields), joinFields(b.target.Fields)); c != 0 {
			return c
		}
		return bytes.Compare(a.CanonicalBytes(), b.CanonicalBytes())
	})
}

func joinFields(fs []event.FieldPath) string {
	out := make([]byte, 0, 32)
	for i, f := range fs {
		if i > 0 {
			out = append(out, 0x1f) // unit separator: cannot occur in a field path
		}
		out = append(out, f...)
	}
	return string(out)
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
