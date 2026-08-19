// Package event implements the event model of Â§5.1, equation (3).
//
// Equation (3) defines an event as a partial function
//
//	e : F â‡€ â‹ƒ_f D_f,  with e(f) âˆˆ D_f where defined
//
// so an event is exactly a set of (field path, value) pairs together with the
// designated entity Îµ(e) and timestamp. The set of fields an event carries is
// dom(e), which Â§11 identifies with the mask M of the conformal missing-data
// literature.
//
// # Determinism (R4)
//
// This package is where Go's map-iteration randomisation would otherwise leak into
// scores. Two structural choices prevent it rather than relying on care at each
// call site:
//
//  1. The value map is unexported and is never returned. The only way to enumerate
//     an event's fields is [Event.All], which yields them in sorted field-path
//     order. Iterating in nondeterministic order is therefore not expressible.
//  2. Float accumulation over fields (equations (5), (18), and the (9) grid) is
//     consequently always performed in the same order, so sums are bit-identical
//     across runs. E8 asserts this.
//
// Timestamps are event time, never wall-clock time. No function in this package,
// or in any package reachable from the scoring path, may call time.Now; decay in
// Â§6.2 and Â§7.2 is driven by the event timestamp and the state row's own
// last-observed timestamp. An architecture test enforces the prohibition.
package event

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"iter"
	"maps"
	"slices"
)

// FieldPath identifies a field. Â§5.1: F is a countable set of field paths whose
// members are not known at design time. Paths are opaque to every detector; a
// detector that named a field would violate R2.
type FieldPath string

// EntityID identifies the entity Îµ(e) that an event concerns.
//
// Which field yields the entity is configuration, not code (Â§5.1: "A designated
// field yields the entity Îµ(e)"). For enterprise authentication telemetry the
// entity is the individual user account, so that a verdict answers whether this
// person acted out of character against their own persisted history. A user who
// habitually departs from the population norm is not thereby anomalous; Â§7.6 makes
// that argument for timing specifically, and Â§6 for categorical attributes.
// Population-scope structure lives in Â§8 instead, where role and peer similarity
// are discovered as block structure.
type EntityID string

// SourceID identifies the telemetry source. Field registries, calibration sets
// (Â§10.1) and co-occurrence graphs (Â§8.2) are all scoped per source, because
// neither score distributions nor block structure transfer across sources.
type SourceID string

// Timestamp is event time in microseconds, on the corpus's own epoch.
//
// It is deliberately a distinct type from any wall-clock representation so that a
// stray time.Now cannot be assigned to it. LANL's epoch starts at 1 with one-second
// resolution; the reader multiplies into microseconds so that corpora of finer
// resolution need no schema change.
type Timestamp int64

// Microseconds, and the derived units used by the decay factors of Â§6.2 and Â§7.2.
const (
	Microsecond Timestamp = 1
	Millisecond           = 1000 * Microsecond
	Second                = 1000 * Millisecond
	Minute                = 60 * Second
	Hour                  = 60 * Minute
	Day                   = 24 * Hour
)

// ID is a content-derived event identifier.
//
// It is a digest of the event's semantic content and nothing else: not of arrival
// order, not of the batch the event appeared in, and not of wall-clock time. Two
// byte-identical events therefore carry the same ID however they are batched, which
// is what lets E8 assert byte-identical scores across differing batch compositions.
// Corpora containing exact duplicate rows (LANL's redteam.txt has 34) legitimately
// produce repeated IDs.
type ID [32]byte

// String returns the hex encoding of the identifier.
func (id ID) String() string { return hex.EncodeToString(id[:]) }

// Event is an immutable realisation of equation (3).
//
// Construct one with [New]. The zero Event carries no fields and is not scoreable.
type Event struct {
	source     SourceID
	entity     EntityID
	occurredAt Timestamp

	// paths is sorted and is the single canonical iteration order for this event.
	paths  []FieldPath
	values map[FieldPath]Value

	// offset is the reader's position in the corpus. It exists for provenance and
	// for reproducing a run, and is excluded from the content digest so that it
	// cannot influence a score. Nothing in the scoring path may read it.
	offset int64

	id ID
}

// New builds an event from its fields.
//
// The supplied map is copied, so later mutation by the caller cannot alter the
// event. Field paths are sorted once here, fixing the canonical order used by every
// subsequent traversal and every float accumulation.
func New(source SourceID, entity EntityID, occurredAt Timestamp, fields map[FieldPath]Value, offset int64) Event {
	paths := make([]FieldPath, 0, len(fields))
	values := make(map[FieldPath]Value, len(fields))
	for p, v := range fields { // safe: result is sorted below before any use
		paths = append(paths, p)
		values[p] = v
	}
	slices.Sort(paths)

	e := Event{
		source:     source,
		entity:     entity,
		occurredAt: occurredAt,
		paths:      paths,
		values:     values,
		offset:     offset,
	}
	e.id = e.digest()
	return e
}

// With returns a copy of the event carrying additional fields.
//
// It exists for derived fields: structure inferred inside a value, such as the /24 of an
// address or the parent domain of a hostname, is registered as a field beside the
// original so that every detector scores it unchanged and none of them learns that
// derivation happened.
//
// The copy carries its OWN identifier, because the identifier is a digest of content and
// the added fields are content. Two events differing in their fields must not claim to be
// the same event; a caller wanting the original's identity should keep the original.
//
// An added path that already exists is ignored rather than overwriting: derivation may
// only add to an event, never restate what a source said. Returning a copy keeps the
// receiver immutable, so an event already handed to a detector cannot change underneath
// it (R4).
func (e *Event) With(extra map[FieldPath]Value) Event {
	fields := make(map[FieldPath]Value, len(e.values)+len(extra))
	maps.Copy(fields, e.values)
	for p, v := range extra {
		if _, exists := fields[p]; exists {
			continue
		}
		fields[p] = v
	}
	return New(e.source, e.entity, e.occurredAt, fields, e.offset)
}

// Source returns the telemetry source that produced the event.
func (e *Event) Source() SourceID { return e.source }

// Entity returns Îµ(e).
func (e *Event) Entity() EntityID { return e.entity }

// OccurredAt returns the event timestamp, which is the only clock the scoring path
// is permitted to consult.
func (e *Event) OccurredAt() Timestamp { return e.occurredAt }

// Offset returns the reader's corpus position. Provenance and replay only; it is
// not part of the content digest and must not reach a score.
func (e *Event) Offset() int64 { return e.offset }

// ID returns the content-derived identifier.
func (e *Event) ID() ID { return e.id }

// Len returns |dom(e)|, the number of fields the event carries.
func (e *Event) Len() int { return len(e.paths) }

// Get returns the value of f and whether f âˆˆ dom(e).
func (e *Event) Get(f FieldPath) (Value, bool) {
	v, ok := e.values[f]
	return v, ok
}

// Has reports whether f âˆˆ dom(e).
func (e *Event) Has(f FieldPath) bool {
	_, ok := e.values[f]
	return ok
}

// Mask returns dom(e) in sorted order: the set of fields the event carries, which
// Â§11 identifies with the mask M. The returned slice is a copy, so a caller cannot
// disturb the canonical order.
func (e *Event) Mask() []FieldPath { return slices.Clone(e.paths) }

// All yields the event's fields in sorted field-path order.
//
// This is the only enumeration this package offers, which is what makes
// nondeterministic traversal inexpressible rather than merely discouraged.
func (e *Event) All() iter.Seq2[FieldPath, Value] {
	return func(yield func(FieldPath, Value) bool) {
		for _, p := range e.paths {
			if !yield(p, e.values[p]) {
				return
			}
		}
	}
}

// digest computes the content identifier.
//
// The encoding is built as a single buffer and hashed once, rather than streamed into
// the hash, so that the encoding is a pure function that can be tested and reasoned
// about independently of the digest.
//
// Fields are folded in sorted order and every variable-length component is
// length-prefixed, so no combination of delimiters in a value can forge a collision
// with a different event.
func (e *Event) digest() ID {
	buf := make([]byte, 0, 128+64*len(e.paths))

	buf = appendLenPrefixed(buf, []byte(e.source))
	buf = appendLenPrefixed(buf, []byte(e.entity))
	buf = appendUint64(buf, reinterpretInt64(int64(e.occurredAt)))
	buf = appendUint64(buf, reinterpretInt(len(e.paths)))

	for _, p := range e.paths {
		v := e.values[p]
		buf = appendLenPrefixed(buf, []byte(p))
		buf = appendLenPrefixed(buf, []byte(v.Text()))
		if v.IsUsable() {
			buf = append(buf, 1)
		} else {
			buf = append(buf, 0)
		}
	}

	return sha256.Sum256(buf)
}

func appendUint64(dst []byte, v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return append(dst, b[:]...)
}

func appendLenPrefixed(dst, b []byte) []byte {
	dst = appendUint64(dst, reinterpretInt(len(b)))
	return append(dst, b...)
}

// reinterpretInt and reinterpretInt64 reinterpret a signed value's bit pattern as
// unsigned for encoding.
//
// This is not arithmetic and cannot overflow in any sense that matters here: the
// encoding needs only to be injective, and preserving the bit pattern is injective by
// construction. Timestamps are event time on a corpus's own epoch, which LANL starts
// at 1, but a corpus whose epoch places some events before zero would still encode
// distinctly.
func reinterpretInt(v int) uint64 { return uint64(v) } //nolint:gosec // bit-pattern encoding, not arithmetic

func reinterpretInt64(v int64) uint64 { return uint64(v) } //nolint:gosec // bit-pattern encoding, not arithmetic
