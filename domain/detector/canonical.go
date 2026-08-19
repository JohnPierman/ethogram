package detector

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
)

// CanonicalBytes returns a byte-exact encoding of the verdict.
//
// Floating-point quantities are encoded as their IEEE-754 bit patterns via
// math.Float64bits, not as formatted decimal text. E8 asserts that scores are
// byte-identical across differing batch compositions, and formatting would hide a
// discrepancy in the low bits — precisely the discrepancy that map-ordered float
// accumulation produces. Comparing bit patterns makes the assertion mean what it
// says.
//
// Every variable-length component is length-prefixed, and map-valued evidence is
// emitted in sorted key order.
func (v Verdict) CanonicalBytes() []byte {
	buf := make([]byte, 0, 256)

	buf = appendLenPrefixed(buf, []byte(v.detectorID))
	buf = append(buf, v.target.Event[:]...)
	buf = appendLenPrefixed(buf, []byte(v.target.Entity))
	buf = appendUint64(buf, reinterpretInt(len(v.target.Fields)))
	for _, f := range v.target.Fields {
		buf = appendLenPrefixed(buf, []byte(f))
	}

	buf = append(buf, byte(v.status))

	// An abstained verdict has no p-value; emit a discriminator rather than a
	// sentinel number, so that no reader can mistake a placeholder for a score.
	if v.status.IsEvaluated() {
		buf = append(buf, 1)
		buf = appendUint64(buf, math.Float64bits(v.p))
	} else {
		buf = append(buf, 0)
	}

	buf = appendLenPrefixed(buf, []byte(v.reason))

	buf = appendUint64(buf, reinterpretInt(len(v.evidence.Equations)))
	for _, eq := range v.evidence.Equations {
		buf = appendUint64(buf, reinterpretInt(eq))
	}

	names := v.evidence.StatNames()
	buf = appendUint64(buf, reinterpretInt(len(names)))
	for _, n := range names {
		buf = appendLenPrefixed(buf, []byte(n))
		buf = appendUint64(buf, math.Float64bits(v.evidence.Stats[n]))
	}

	labels := v.evidence.LabelNames()
	buf = appendUint64(buf, reinterpretInt(len(labels)))
	for _, n := range labels {
		buf = appendLenPrefixed(buf, []byte(n))
		buf = appendLenPrefixed(buf, []byte(v.evidence.Labels[n]))
	}

	buf = appendUint64(buf, reinterpretInt(len(v.evidence.Caveats)))
	for _, c := range v.evidence.Caveats {
		buf = appendLenPrefixed(buf, []byte(c))
	}

	return buf
}

// CanonicalBytes returns a byte-exact encoding of the verdict set in its current
// order. Callers comparing across runs should call [Verdicts.SortCanonical] first
// unless the order itself is under test.
func (vs Verdicts) CanonicalBytes() []byte {
	buf := make([]byte, 0, 256*len(vs)+8)
	buf = appendUint64(buf, reinterpretInt(len(vs)))
	for _, v := range vs {
		b := v.CanonicalBytes()
		buf = appendUint64(buf, reinterpretInt(len(b)))
		buf = append(buf, b...)
	}
	return buf
}

// Digest returns a SHA-256 over [Verdicts.CanonicalBytes], for compact comparison
// and for recording in result JSON.
func (vs Verdicts) Digest() [32]byte { return sha256.Sum256(vs.CanonicalBytes()) }

func appendUint64(dst []byte, v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return append(dst, b[:]...)
}

func appendLenPrefixed(dst, b []byte) []byte {
	dst = appendUint64(dst, reinterpretInt(len(b)))
	return append(dst, b...)
}

// reinterpretInt reinterprets a signed int's bit pattern as unsigned for encoding.
//
// This is not arithmetic. The encoding needs only to be injective, and preserving the
// bit pattern is injective by construction; lengths and equation numbers are small
// positive values in any case.
func reinterpretInt(v int) uint64 { return uint64(v) } //nolint:gosec // bit-pattern encoding, not arithmetic
