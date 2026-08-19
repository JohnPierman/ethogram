package registry

import (
	"slices"

	"github.com/JohnPierman/ethogram/domain/event"
)

// Entry is one registry record: a kind for a (source, field path), together with the
// statistics that produced it.
type Entry struct {
	Source event.SourceID
	Path   event.FieldPath
	Kind   FieldKind
	Stats  *FieldStats

	// Presence is the Beta posterior on field presence of §5.3, per (source, f).
	// It is what lets a source silently ceasing to emit a field be detected as
	// abstained_unexpected, rather than manifesting as quietly degraded scores.
	Presence Beta
}

// Beta is a Beta posterior on the probability that a field is present in an event
// from a given source (§5.3).
type Beta struct {
	// Alpha counts events in which the field was present, plus the prior.
	Alpha float64
	// Beta counts events in which it was absent, plus the prior.
	Beta float64
}

// NewBeta returns a uniform prior, which asserts nothing about presence.
func NewBeta() Beta { return Beta{Alpha: 1, Beta: 1} }

// Mean is the posterior probability of presence.
func (b Beta) Mean() float64 { return b.Alpha / (b.Alpha + b.Beta) }

// Observations is the evidence behind the posterior, excluding the prior.
func (b Beta) Observations() float64 { return b.Alpha + b.Beta - 2 }

// IsOrdinarilyPresent reports whether the field is present often enough that its
// absence is unexpected rather than structural.
//
// This is the distinction §5.3 draws between abstained_structural, where the source
// does not produce these inputs at all, and abstained_unexpected, where the input is
// ordinarily present but absent here.
func (b Beta) IsOrdinarilyPresent(threshold float64) bool {
	return b.Mean() >= threshold
}

// Registry holds the entries for all sources.
//
// Iteration is always in sorted order, never map order: the registry drives which
// fields each detector examines, so a nondeterministic traversal would reorder the
// float accumulations of equations (5) and (18) and break R4.
type Registry struct {
	policy  Policy
	entries map[event.SourceID]map[event.FieldPath]*Entry

	// sorted caches each source's field paths in ascending order, maintained on
	// insertion. ObserveEvent runs once per corpus event, and re-sorting the paths
	// there is measurable at a billion rows; the cache turns it into an amortised
	// insertion.
	sorted map[event.SourceID][]event.FieldPath

	// presenceThreshold separates abstained_structural from abstained_unexpected.
	presenceThreshold float64
}

// New returns an empty registry governed by policy.
func New(policy Policy) *Registry {
	return &Registry{
		policy:            policy,
		entries:           make(map[event.SourceID]map[event.FieldPath]*Entry),
		sorted:            make(map[event.SourceID][]event.FieldPath),
		presenceThreshold: 0.5,
	}
}

// Policy returns the governing thresholds.
func (r *Registry) Policy() Policy { return r.policy }

// ObserveEvent folds an event into the registry.
//
// This is registry maintenance, not scoring: it is called from the Observe half of
// §5.2, after the event has been scored, so that a field's first-ever value is still
// novel at the moment it is scored.
func (r *Registry) ObserveEvent(e *event.Event) {
	byField, ok := r.entries[e.Source()]
	if !ok {
		byField = make(map[event.FieldPath]*Entry)
		r.entries[e.Source()] = byField
	}

	// Fields present in this event.
	for f, v := range e.All() {
		entry, ok := byField[f]
		if !ok {
			entry = &Entry{
				Source:   e.Source(),
				Path:     f,
				Stats:    NewFieldStats(string(f)),
				Presence: NewBeta(),
			}
			byField[f] = entry
			r.insertSorted(e.Source(), f)
		}
		if v.IsUsable() {
			entry.Stats.Observe(v.Text(), int64(e.OccurredAt()), r.policy.MaxTrackedValues)
		} else {
			entry.Stats.ObserveUnusable(int64(e.OccurredAt()))
		}
		entry.Presence.Alpha++
		entry.Kind = Infer(*entry.Stats, r.policy)
	}

	// Fields this source has produced before but which are absent here. Updating the
	// Beta posterior in this direction is what makes a source ceasing to emit a field
	// visible (§5.3).
	for _, f := range r.sortedPaths(e.Source()) {
		if e.Has(f) {
			continue
		}
		byField[f].Presence.Beta++
	}
}

// KindOf returns the kind recorded for a (source, field), and whether the registry has
// seen the field at all.
func (r *Registry) KindOf(source event.SourceID, f event.FieldPath) (FieldKind, bool) {
	entry, ok := r.entries[source][f]
	if !ok {
		return KindUnknown, false
	}
	return entry.Kind, true
}

// FindBySource returns every entry for a source, ordered by field path.
func (r *Registry) FindBySource(source event.SourceID) []*Entry {
	paths := r.sortedPaths(source)
	out := make([]*Entry, 0, len(paths))
	for _, p := range paths {
		out = append(out, r.entries[source][p])
	}
	return out
}

// FindEligibleBySource returns the entries admissible to the co-occurrence graph of
// §8.2: those whose kind is neither identifier nor excluded, and whose kind has
// settled. This is F_elig, and its size for one event is F_e of §8.5.
func (r *Registry) FindEligibleBySource(source event.SourceID) []*Entry {
	all := r.FindBySource(source)
	out := make([]*Entry, 0, len(all))
	for _, e := range all {
		if e.Kind.IsEligible() {
			out = append(out, e)
		}
	}
	return out
}

// HasSource reports whether the registry has seen the source.
func (r *Registry) HasSource(source event.SourceID) bool {
	_, ok := r.entries[source]
	return ok
}

// Sources returns the known sources in sorted order.
func (r *Registry) Sources() []event.SourceID {
	out := make([]event.SourceID, 0, len(r.entries))
	for s := range r.entries {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// StatusForAbsent classifies why a field is absent from an event, returning the §5.3
// status that a detector must report.
//
// A field the source ordinarily emits is abstained_unexpected; one it has rarely or
// never emitted is abstained_structural. The distinction is drawn from the Beta
// posterior rather than declared, so no configuration is required to detect a source
// changing its behaviour.
func (r *Registry) StatusForAbsent(source event.SourceID, f event.FieldPath) AbsenceKind {
	entry, ok := r.entries[source][f]
	if !ok {
		return AbsenceStructural
	}
	if entry.Presence.IsOrdinarilyPresent(r.presenceThreshold) {
		return AbsenceUnexpected
	}
	return AbsenceStructural
}

// AbsenceKind distinguishes the two abstention reasons of §5.3 that concern absence.
type AbsenceKind uint8

const (
	// AbsenceStructural: the source does not produce these inputs.
	AbsenceStructural AbsenceKind = iota
	// AbsenceUnexpected: ordinarily present for this source, but absent here.
	AbsenceUnexpected
)

// sortedPaths returns the field paths known for a source, in sorted order, from the
// insertion-maintained cache.
func (r *Registry) sortedPaths(source event.SourceID) []event.FieldPath {
	return r.sorted[source]
}

// insertSorted places a new path into the source's sorted cache.
func (r *Registry) insertSorted(source event.SourceID, f event.FieldPath) {
	paths := r.sorted[source]
	idx, _ := slices.BinarySearch(paths, f)
	paths = append(paths, "")
	copy(paths[idx+1:], paths[idx:])
	paths[idx] = f
	r.sorted[source] = paths
}
