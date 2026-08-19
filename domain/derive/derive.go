// Package derive infers structure inside field values and registers a coarser field
// beside the original.
//
// # The problem
//
// Every value is an opaque token to the framework, so a novel `10.1.2.3` and a novel
// `10.1.2.99` are two unrelated firsts even though an analyst would read them as the same
// subnet. Likewise a novel `web07.corp.example.com` and a novel `web08.corp.example.com`,
// and a novel `11.0.3` against a novel `11.0.4`. On an open vocabulary — addresses,
// hostnames, versions — that granularity is the difference between a signal and a flood:
// a per-account address set is mostly singletons, so at exact granularity every event
// looks like a first, while at /24 granularity a genuinely new network stands out.
//
// A novel /24 is a different and usually stronger signal than a novel exact address, and
// today the framework cannot express it.
//
// # Why this does not violate R2
//
// R2 forbids requiring advance knowledge of a field's type, cardinality or value set. It
// does not forbid *inferring* structure from what has been observed, which is exactly
// what the §5.1 registry already does to decide a field's kind.
//
// Nothing here names a field. A fixed set of decompositions is tried against every
// field's observed values, and a derived field is registered only where the values
// consistently parse. `src_ip`, `dst_addr` and a vendor's `Properties.RemoteHost` are
// treated identically, because the decision is made from the values and never from the
// name.
//
// # What is derived
//
// The derived field is an ordinary field with an ordinary value, registered beside the
// original rather than replacing it. Both are scored: the exact value keeps the precise
// signal, the coarse value adds the one the exact value cannot express. Nothing
// downstream learns that derivation happened.
package derive

import (
	"strconv"
	"strings"

	"github.com/JohnPierman/ethogram/domain/event"
)

// Separator joins a field path to the name of the decomposition that produced it. It is
// the same character the pairing detector uses for its synthetic paths, and for the same
// reason: no real field path may contain it, so a derived path can never collide with one
// a source actually emits.
const Separator = "×"

// Decomposition coarsens a value, or declines.
//
// Declining is the whole mechanism: a decomposition that parses only some of a field's
// values is not the structure of that field, and the inference below counts exactly that.
type Decomposition interface {
	// Name identifies the decomposition in the derived field path and in evidence.
	Name() string

	// Coarsen returns the coarser form of a value, and whether the value parsed.
	Coarsen(value string) (string, bool)
}

// Decompositions is the fixed set tried against every field. It is deliberately small.
// Each entry must be unambiguous enough that a value parsing under it is strong evidence
// of the structure, because a decomposition that parses loosely would fire on fields it
// does not describe and manufacture a derived field with no meaning.
func Decompositions() []Decomposition {
	return []Decomposition{DottedQuad{}, FQDN{}, SemanticVersion{}}
}

// ---------------------------------------------------------------------------
// Dotted quad
// ---------------------------------------------------------------------------

// DottedQuad coarsens an IPv4 address to its /24 network.
//
// /24 rather than /16 or the exact host: it is the granularity at which an operator
// reasons about "somewhere new on the network", and it is where a compromised host's
// lateral movement first shows as a change of neighbourhood rather than of address.
type DottedQuad struct{}

// Name implements Decomposition.
func (DottedQuad) Name() string { return "net24" }

// Coarsen returns a.b.c.0/24 for a dotted quad, declining anything else.
//
// Each octet must be a plain decimal 0-255 with no sign, no leading plus and no padding
// beyond what a single value needs, so that a version string like `1.2.3.4` of a product
// that happens to look like an address is the only false positive available, and the
// inference below requires consistency across many values before believing it.
func (DottedQuad) Coarsen(value string) (string, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 4 {
		return "", false
	}
	for _, p := range parts {
		if p == "" || len(p) > 3 {
			return "", false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return "", false
		}
		if len(p) > 1 && p[0] == '0' {
			return "", false // 010 is not how an address is written
		}
	}
	return parts[0] + "." + parts[1] + "." + parts[2] + ".0/24", true
}

// ---------------------------------------------------------------------------
// FQDN
// ---------------------------------------------------------------------------

// FQDN coarsens a hostname to its parent domain: the host label is dropped and what
// remains is the network the host belongs to.
//
// `web07.corp.example.com` and `web08.corp.example.com` become one value, so an account
// reaching a new host inside a familiar domain is distinguishable from one reaching a
// domain it has never touched — which are different events and currently score alike.
type FQDN struct{}

// Name implements Decomposition.
func (FQDN) Name() string { return "parent" }

// Coarsen drops the leftmost label, requiring at least three labels so that a bare
// `example.com` is not coarsened to a public suffix, which would carry no information.
//
// Two exclusions, both of which exist so that one structure is never described by two
// decompositions. A field matching two is left undecided by [Inferrer.DecompositionFor],
// so an overlap does not produce a wrong answer — it produces no answer, which is a
// silent loss of a derived field that would otherwise have been correct.
//
//   - Anything parsing as a dotted quad is declined, leaving addresses to DottedQuad.
//   - The final label must be alphabetic. A top-level domain always is, and without this
//     a three-component numeric version like `10.0.3` parses as a three-label hostname
//     and collides with SemanticVersion on every version field.
func (FQDN) Coarsen(value string) (string, bool) {
	if _, isAddress := (DottedQuad{}).Coarsen(value); isAddress {
		return "", false
	}
	labels := strings.Split(value, ".")
	if len(labels) < 3 {
		return "", false
	}
	for _, l := range labels {
		if l == "" || !isHostLabel(l) {
			return "", false
		}
	}
	if !isAlphabetic(labels[len(labels)-1]) {
		return "", false
	}
	return strings.Join(labels[1:], "."), true
}

func isHostLabel(l string) bool {
	for _, r := range l {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// isAlphabetic reports whether a label is entirely letters, which every top-level domain
// is and no numeric version component is.
func isAlphabetic(l string) bool {
	if len(l) < 2 {
		return false
	}
	for _, r := range l {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !isLetter {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Semantic version
// ---------------------------------------------------------------------------

// SemanticVersion coarsens a version string to its major version.
//
// A novel build string is ordinary — builds change constantly — while a novel major
// version is a genuine change of platform. At exact granularity the first drowns the
// second.
type SemanticVersion struct{}

// Name implements Decomposition.
func (SemanticVersion) Name() string { return "major" }

// Coarsen returns the leading numeric component of a dotted version.
//
// Requires two or three components with the first two numeric, and declines four numeric
// components outright so that a dotted quad is never taken for a version.
func (SemanticVersion) Coarsen(value string) (string, bool) {
	core, _, _ := strings.Cut(value, "+")
	core, _, _ = strings.Cut(core, "-")
	parts := strings.Split(core, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return "", false
	}
	for i, p := range parts[:2] {
		if p == "" {
			return "", false
		}
		if _, err := strconv.Atoi(p); err != nil {
			return "", false
		}
		if i == 0 && len(p) > 1 && p[0] == '0' {
			return "", false
		}
	}
	return parts[0], true
}

// ---------------------------------------------------------------------------
// Inference
// ---------------------------------------------------------------------------

// Policy governs when an observed field is believed to have structure.
type Policy struct {
	// MinDistinctValues is how many distinct values must be seen before any decision.
	// Below it the field is undecided, on the same principle as the registry's own
	// KindUnknown: too little evidence is not evidence of absence.
	MinDistinctValues int

	// MinParseFraction is the share of distinct values that must parse under a
	// decomposition for the field to be believed to have that structure.
	//
	// Distinct values, not observations: a field with one address repeated a million
	// times and a thousand distinct hostnames is a hostname field, and counting
	// observations would call it an address field.
	MinParseFraction float64

	// MaxTrackedValues bounds the per-field sample. The fraction is estimated from the
	// first values seen rather than from all of them, so state stays bounded on exactly
	// the open vocabularies this exists to serve.
	MaxTrackedValues int
}

// DefaultPolicy is deliberately strict. A derived field that does not describe its
// source's structure is worse than none: it adds a detector's worth of noise to the
// combination, and §10.2 has already measured what uninformative detectors cost.
func DefaultPolicy() Policy {
	return Policy{
		MinDistinctValues: 50,
		MinParseFraction:  0.95,
		MaxTrackedValues:  2000,
	}
}

// fieldSample counts how many of a field's distinct values parse under each
// decomposition.
type fieldSample struct {
	seen     map[string]struct{}
	parsedBy map[string]int
	full     bool
}

// Inferrer observes values and reports which fields have structure.
//
// It is fed the same events the registry is fed, and like the registry it holds no
// per-entity state: structure is a property of a field, not of an account.
type Inferrer struct {
	policy  Policy
	samples map[string]*fieldSample // (source, path) -> sample
	decomps []Decomposition
}

// NewInferrer builds an inferrer over the standard decompositions.
func NewInferrer(policy Policy) *Inferrer {
	return &Inferrer{
		policy:  policy,
		samples: map[string]*fieldSample{},
		decomps: Decompositions(),
	}
}

func sampleKey(s event.SourceID, f event.FieldPath) string {
	return string(s) + "\x1f" + string(f)
}

// Observe records one field value.
func (in *Inferrer) Observe(source event.SourceID, f event.FieldPath, value string) {
	key := sampleKey(source, f)
	sample, ok := in.samples[key]
	if !ok {
		sample = &fieldSample{seen: map[string]struct{}{}, parsedBy: map[string]int{}}
		in.samples[key] = sample
	}
	if sample.full {
		return
	}
	if _, repeated := sample.seen[value]; repeated {
		return
	}
	sample.seen[value] = struct{}{}
	for _, d := range in.decomps {
		if _, ok := d.Coarsen(value); ok {
			sample.parsedBy[d.Name()]++
		}
	}
	if len(sample.seen) >= in.policy.MaxTrackedValues {
		sample.full = true
	}
}

// DecompositionFor returns the decomposition a field's values consistently parse under,
// or nil when the field has no inferred structure yet.
//
// At most one is returned. Where two would qualify the field is left undecided rather
// than being given an arbitrary winner: two decompositions both parsing 95% of a field's
// values means neither is describing it, and registering one would attach a meaning the
// evidence does not support.
func (in *Inferrer) DecompositionFor(source event.SourceID, f event.FieldPath) Decomposition {
	sample, ok := in.samples[sampleKey(source, f)]
	if !ok || len(sample.seen) < in.policy.MinDistinctValues {
		return nil
	}
	total := float64(len(sample.seen))
	var winner Decomposition
	for _, d := range in.decomps {
		if float64(sample.parsedBy[d.Name()])/total < in.policy.MinParseFraction {
			continue
		}
		if winner != nil {
			return nil // ambiguous; see the doc comment
		}
		winner = d
	}
	return winner
}

// DerivedPath is the field path a decomposition's output is registered under.
func DerivedPath(f event.FieldPath, d Decomposition) event.FieldPath {
	return event.FieldPath(string(f) + Separator + d.Name())
}

// IsDerived reports whether a path was produced by this package, so that reporting can
// present a derived field as one rather than as a field with an odd character in it.
func IsDerived(f event.FieldPath) bool {
	return strings.Contains(string(f), Separator)
}

// SplitDerived recovers the original field path and the decomposition name.
func SplitDerived(f event.FieldPath) (original event.FieldPath, decomposition string, ok bool) {
	a, b, found := strings.Cut(string(f), Separator)
	if !found {
		return "", "", false
	}
	return event.FieldPath(a), b, true
}

// Augment returns the derived fields an event should carry, keyed by derived path.
//
// It returns a map rather than mutating the event because an event is immutable by
// construction, and because the caller decides whether to admit the derived fields at
// all — a shadow arm may want to measure derivation without letting it into a run's
// scored composition.
func (in *Inferrer) Augment(e *event.Event) map[event.FieldPath]event.Value {
	var derived map[event.FieldPath]event.Value
	// All() is the event's only enumeration and it is sorted by field path, so the
	// derived set is built in a fixed order however the source emitted the fields (R4).
	for f, value := range e.All() {
		if !value.IsUsable() {
			continue
		}
		d := in.DecompositionFor(e.Source(), f)
		if d == nil {
			continue
		}
		coarse, ok := d.Coarsen(value.Text())
		if !ok {
			// The field has the structure but this value does not: the residue the
			// MinParseFraction threshold tolerates. No derived value is emitted, so the
			// derived field is simply absent on this event, which §5.3 already handles.
			continue
		}
		if derived == nil {
			derived = map[event.FieldPath]event.Value{}
		}
		derived[DerivedPath(f, d)] = event.NewValue(coarse)
	}
	return derived
}
