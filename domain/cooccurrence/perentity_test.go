package cooccurrence_test

import (
	"testing"

	"github.com/JohnPierman/ethogram/domain/cooccurrence"
	"github.com/JohnPierman/ethogram/domain/event"
)

func node(f, v string) cooccurrence.NodeID {
	return cooccurrence.NodeID{Field: event.FieldPath(f), Value: v}
}

// TestPairAddressingIsCanonical: (a, b) and (b, a) are one pairing, however a caller
// enumerated them. Without this the same pairing accrues two histories, each half as
// established as the truth, and both look more novel than they are.
func TestPairAddressingIsCanonical(t *testing.T) {
	a := node("auth.authentication_type", "Kerberos")
	b := node("auth.destination_computer", "C625")

	if cooccurrence.PairField(a, b) != cooccurrence.PairField(b, a) {
		t.Error("field path depends on enumeration order")
	}
	if cooccurrence.PairValue(a, b) != cooccurrence.PairValue(b, a) {
		t.Error("value depends on enumeration order")
	}

	// Same field, different values: still one pairing, ordered by value.
	c := node("auth.destination_computer", "C1")
	d := node("auth.destination_computer", "C2")
	if cooccurrence.PairField(c, d) != cooccurrence.PairField(d, c) ||
		cooccurrence.PairValue(c, d) != cooccurrence.PairValue(d, c) {
		t.Error("same-field pairing depends on enumeration order")
	}
}

// TestPairRoundTrips: a verdict card must be readable without knowing the encoding (R5).
func TestPairRoundTrips(t *testing.T) {
	a := node("auth.authentication_type", "Kerberos")
	b := node("auth.destination_computer", "C625")

	f := cooccurrence.PairField(a, b)
	if !cooccurrence.IsPairField(f) {
		t.Fatalf("%q not recognised as a pair field", f)
	}
	f1, f2, ok := cooccurrence.SplitPairField(f)
	if !ok || f1 != a.Field || f2 != b.Field {
		t.Errorf("field round trip gave (%q, %q, %v)", f1, f2, ok)
	}

	v1, v2, ok := cooccurrence.SplitPairValue(cooccurrence.PairValue(a, b))
	if !ok || v1 != a.Value || v2 != b.Value {
		t.Errorf("value round trip gave (%q, %q, %v)", v1, v2, ok)
	}
}

// TestPairFieldCannotCollideWithARealField: the separator must be a character no real
// field path contains, or a synthetic row could overwrite a genuine one.
func TestPairFieldCannotCollideWithARealField(t *testing.T) {
	for _, real := range []event.FieldPath{
		"auth.authentication_type",
		"auth.destination_computer",
		"process.command_line",
		"a.b.c.d_e-f",
	} {
		if cooccurrence.IsPairField(real) {
			t.Errorf("%q reads as a synthetic pair field", real)
		}
	}
}
