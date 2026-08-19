package cooccurrence

import (
	"strings"

	"github.com/JohnPierman/ethogram/domain/event"
)

// PairFieldSeparator joins two field paths into the synthetic path a pairing is stored
// under. It is a character no field path may contain, so a synthetic path can never
// collide with a real one.
const PairFieldSeparator = "×" // ×

// PairValueSeparator joins two values into the synthetic value of a pairing.
const PairValueSeparator = "\x1f"

// PairField returns the synthetic field path for an unordered pair of fields, and
// PairValue the synthetic value for the pair of values, both in canonical order so that
// (a, b) and (b, a) name one thing however a caller enumerated them (R4).
//
// # Why a pairing is modelled as a value
//
// §8.1's signal is real: two values each individually familiar that have scarcely been
// seen *together*. Nothing else in the framework covers a relationship between fields,
// and on LANL 29 of the 549 labelled events are novel pairings and nothing else.
//
// What was wrong was the scope, not the question. The population co-occurrence null of §8
// asks how often the population's degree structure predicts these values should have been
// paired, which fires on every stable personal preference — an account that has always
// used one authentication type and never another is, under the configuration model,
// astronomically improbable on every event it produces. §7.6 disavows exactly that: an
// entity habitually departing from the population norm is not thereby anomalous. Measured,
// it put 18.4% of scored events below 1e−12, contributed nothing to detection, and once
// the underflow was repaired showed ln P reaching −39,278.
//
// Asking the same question of the entity's own history needs no new machinery and no new
// null. A pairing IS a value — of a field that happens to be composite — so Detector I's
// estimator scores it exactly as it scores any other value, with the same decay, the same
// reserved mass for the unseen, the same cold-start convention by which a first
// observation is never anomalous, and, where enabled, the same Good–Turing treatment of
// an open vocabulary. Pair vocabularies are open almost by construction, which makes that
// last point more than incidental.
//
// The synthetic path is opaque to everything downstream: the registry, the store and the
// evidence card handle it as a field path like any other, so no component learns that
// pairs exist. That is what keeps it field-agnostic (R2).
func PairField(a, b NodeID) event.FieldPath {
	if b.Field < a.Field || (b.Field == a.Field && b.Value < a.Value) {
		a, b = b, a
	}
	return event.FieldPath(string(a.Field) + PairFieldSeparator + string(b.Field))
}

// PairValue returns the synthetic value for a pair, in the same canonical order PairField
// uses, so that the (field, value) pair addresses one row.
func PairValue(a, b NodeID) string {
	if b.Field < a.Field || (b.Field == a.Field && b.Value < a.Value) {
		a, b = b, a
	}
	return a.Value + PairValueSeparator + b.Value
}

// SplitPairField recovers the two field paths from a synthetic one, for evidence and for
// a reader of a verdict card who should not have to know the encoding to read it (R5).
func SplitPairField(p event.FieldPath) (first, second event.FieldPath, ok bool) {
	a, b, found := strings.Cut(string(p), PairFieldSeparator)
	if !found {
		return "", "", false
	}
	return event.FieldPath(a), event.FieldPath(b), true
}

// SplitPairValue recovers the two values from a synthetic one.
func SplitPairValue(v string) (first, second string, ok bool) {
	a, b, found := strings.Cut(v, PairValueSeparator)
	if !found {
		return "", "", false
	}
	return a, b, true
}

// IsPairField reports whether a field path is synthetic, so that reporting can present
// pairings as pairings rather than as fields with an odd character in the name.
func IsPairField(p event.FieldPath) bool {
	return strings.Contains(string(p), PairFieldSeparator)
}
