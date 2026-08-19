package event_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/JohnPierman/ethogram/domain/event"
)

const (
	src    = event.SourceID("lanl.auth")
	entity = event.EntityID("U66@DOM1")

	fSrcUser = event.FieldPath("auth.source_user")
	fDstUser = event.FieldPath("auth.destination_user")
	fSrcComp = event.FieldPath("auth.source_computer")
	fDstComp = event.FieldPath("auth.destination_computer")
	fType    = event.FieldPath("auth.authentication_type")
	fSuccess = event.FieldPath("auth.success_failure")
)

// lanlRow mirrors the first documented example row of auth.txt:
// 1,C625$@DOM1,U147@DOM1,C625,C625,Negotiate,Batch,LogOn,Success
func lanlRow() map[event.FieldPath]event.Value {
	return map[event.FieldPath]event.Value{
		fSrcUser: event.NewValue("C625$@DOM1"),
		fDstUser: event.NewValue("U147@DOM1"),
		fSrcComp: event.NewValue("C625"),
		fDstComp: event.NewValue("C625"),
		fType:    event.NewValue("Negotiate"),
		fSuccess: event.NewValue("Success"),
	}
}

func TestMaskIsSortedAndIsDomE(t *testing.T) {
	e := event.New(src, entity, 1*event.Second, lanlRow(), 1)

	mask := e.Mask()
	if !slices.IsSorted(mask) {
		t.Fatalf("dom(e) must be sorted, got %v", mask)
	}
	if got, want := len(mask), 6; got != want {
		t.Fatalf("|dom(e)| = %d, want %d", got, want)
	}
	if got, want := e.Len(), 6; got != want {
		t.Fatalf("Len() = %d, want %d", got, want)
	}

	// The returned mask is a copy: mutating it must not disturb the event's
	// canonical order, on which every float accumulation depends.
	mask[0] = event.FieldPath("zzz.tampered")
	if again := e.Mask(); !slices.IsSorted(again) {
		t.Fatalf("Mask() must return a copy; event order was disturbed: %v", again)
	}
}

// TestAllYieldsSortedOrder covers trap 1. The iterator is the only enumeration the
// package offers, and it must be sorted, because (5), (18) and the (9) grid
// accumulate floats in exactly this order.
func TestAllYieldsSortedOrder(t *testing.T) {
	e := event.New(src, entity, 1*event.Second, lanlRow(), 1)

	var seen []event.FieldPath
	for f, v := range e.All() {
		seen = append(seen, f)
		if v.Text() == "" {
			t.Fatalf("field %q yielded an empty value", f)
		}
	}
	if !slices.IsSorted(seen) {
		t.Fatalf("All() must yield sorted field paths, got %v", seen)
	}
	if !slices.Equal(seen, e.Mask()) {
		t.Fatalf("All() order %v disagrees with Mask() %v", seen, e.Mask())
	}
}

// TestAllRespectsEarlyBreak confirms the iterator honours a caller stopping early,
// which range-over-func requires.
func TestAllRespectsEarlyBreak(t *testing.T) {
	e := event.New(src, entity, 1*event.Second, lanlRow(), 1)
	n := 0
	for range e.All() {
		n++
		break
	}
	if n != 1 {
		t.Fatalf("expected the loop to stop after one iteration, ran %d", n)
	}
}

// TestIDIsContentDerived is the property E8 rests on. The identifier must depend on
// the event's content alone, so that the same event carries the same identity however
// it was batched, whatever order its fields were supplied in, and wherever it sat in
// the corpus.
func TestIDIsContentDerived(t *testing.T) {
	base := event.New(src, entity, 1*event.Second, lanlRow(), 1)

	t.Run("independent of map insertion order", func(t *testing.T) {
		// Build the same field set through a different insertion sequence. Go
		// randomises map iteration, so repeating this many times exercises many
		// internal orders.
		for range 64 {
			shuffled := make(map[event.FieldPath]event.Value)
			keys := slices.Collect(maps.Keys(lanlRow()))
			// Insert in reverse sorted order, and rely on map randomisation for the
			// rest.
			slices.Sort(keys)
			slices.Reverse(keys)
			row := lanlRow()
			for _, k := range keys {
				shuffled[k] = row[k]
			}
			other := event.New(src, entity, 1*event.Second, shuffled, 1)
			if base.ID() != other.ID() {
				t.Fatalf("ID depends on insertion order: %s != %s", base.ID(), other.ID())
			}
		}
	})

	t.Run("independent of corpus offset", func(t *testing.T) {
		// Offset is provenance, not content: a batch-position dependence here would
		// reintroduce exactly what R1 forbids.
		other := event.New(src, entity, 1*event.Second, lanlRow(), 999_999)
		if base.ID() != other.ID() {
			t.Fatalf("ID depends on corpus offset: %s != %s", base.ID(), other.ID())
		}
		if other.Offset() != 999_999 {
			t.Fatalf("Offset() = %d, want 999999", other.Offset())
		}
	})

	t.Run("sensitive to every content component", func(t *testing.T) {
		cases := map[string]event.Event{
			"different source":    event.New("lanl.proc", entity, 1*event.Second, lanlRow(), 1),
			"different entity":    event.New(src, "U3005@DOM1", 1*event.Second, lanlRow(), 1),
			"different timestamp": event.New(src, entity, 2*event.Second, lanlRow(), 1),
		}
		row := lanlRow()
		row[fType] = event.NewValue("Kerberos")
		cases["different value"] = event.New(src, entity, 1*event.Second, row, 1)

		fewer := lanlRow()
		delete(fewer, fSuccess)
		cases["different mask"] = event.New(src, entity, 1*event.Second, fewer, 1)

		usability := lanlRow()
		usability[fType] = event.UnusableValue("Negotiate")
		cases["different usability"] = event.New(src, entity, 1*event.Second, usability, 1)

		for name, other := range cases {
			if base.ID() == other.ID() {
				t.Errorf("%s: ID collided with the base event", name)
			}
		}
	})
}

// TestDigestResistsDelimiterForgery confirms the length-prefixed encoding: no
// arrangement of separators inside a value can make two different events collide.
func TestDigestResistsDelimiterForgery(t *testing.T) {
	a := event.New(src, entity, 1*event.Second, map[event.FieldPath]event.Value{
		fSrcComp: event.NewValue("C625"),
		fDstComp: event.NewValue("C653"),
	}, 1)
	b := event.New(src, entity, 1*event.Second, map[event.FieldPath]event.Value{
		fSrcComp: event.NewValue("C625C653"),
		fDstComp: event.NewValue(""),
	}, 1)
	if a.ID() == b.ID() {
		t.Fatal("length prefixing failed: concatenation forged a digest collision")
	}
}

func TestGetAndHas(t *testing.T) {
	e := event.New(src, entity, 1*event.Second, lanlRow(), 1)

	v, ok := e.Get(fType)
	if !ok {
		t.Fatalf("expected %q to be in dom(e)", fType)
	}
	if v.Text() != "Negotiate" {
		t.Fatalf("Get(%q) = %q, want Negotiate", fType, v.Text())
	}
	if !e.Has(fType) {
		t.Fatalf("Has(%q) = false", fType)
	}

	absent := event.FieldPath("auth.logon_type_not_supplied")
	if _, ok := e.Get(absent); ok {
		t.Fatalf("%q must not be in dom(e)", absent)
	}
	if e.Has(absent) {
		t.Fatalf("Has(%q) = true for an absent field", absent)
	}
}

func TestAccessors(t *testing.T) {
	e := event.New(src, entity, 42*event.Second, lanlRow(), 7)
	if e.Source() != src {
		t.Fatalf("Source() = %q", e.Source())
	}
	if e.Entity() != entity {
		t.Fatalf("Entity() = %q", e.Entity())
	}
	if e.OccurredAt() != 42*event.Second {
		t.Fatalf("OccurredAt() = %d", e.OccurredAt())
	}
	if len(e.ID().String()) != 64 {
		t.Fatalf("ID().String() length = %d, want 64 hex characters", len(e.ID().String()))
	}
}

// TestUnusableValueIsPresentButNotScoreable covers the §5.3 distinction that LANL's
// literal "?" makes concrete: the field is in dom(e), so its absence is not
// abstained_unexpected, but it cannot be scored, so it is abstained_unusable.
func TestUnusableValueIsPresentButNotScoreable(t *testing.T) {
	e := event.New(src, entity, 1*event.Second, map[event.FieldPath]event.Value{
		fType: event.UnusableValue("?"),
	}, 1)

	if !e.Has(fType) {
		t.Fatal("an unusable value must still be in dom(e), or the §5.3 distinction is lost")
	}
	v, _ := e.Get(fType)
	if v.IsUsable() {
		t.Fatal("UnusableValue must not report as usable")
	}
	if v.Text() != "?" {
		t.Fatalf("the observed text must be retained for evidence (R5), got %q", v.Text())
	}
}

func TestValueSemantics(t *testing.T) {
	usable := event.NewValue("Negotiate")
	if !usable.IsUsable() {
		t.Fatal("NewValue must be usable")
	}
	if usable.IsEmpty() {
		t.Fatal("NewValue(\"Negotiate\") must not be empty")
	}
	if event.NewValue("").IsEmpty() != true {
		t.Fatal("an empty text value must report IsEmpty")
	}
	if event.UnusableValue("?").IsUsable() {
		t.Fatal("UnusableValue must not be usable")
	}
}

func TestEmptyEventIsWellFormed(t *testing.T) {
	e := event.New(src, entity, 1*event.Second, nil, 0)
	if e.Len() != 0 {
		t.Fatalf("Len() = %d for an empty event", e.Len())
	}
	if len(e.Mask()) != 0 {
		t.Fatalf("Mask() = %v for an empty event", e.Mask())
	}
	for range e.All() {
		t.Fatal("All() must yield nothing for an empty event")
	}
	// Still identifiable, so that an event carrying no eligible fields can be logged.
	if e.ID() == (event.ID{}) {
		t.Fatal("an empty event must still receive a digest")
	}
}

// ---------------------------------------------------------------------------
// With: the copy used for derived fields.
// ---------------------------------------------------------------------------

// derivedPath is what domain/derive produces; spelled out here so this package's tests
// depend on no other.
const derivedPath = event.FieldPath("a.addr×net24")

// TestWithAddsFieldsWithoutMutatingTheOriginal guards R4 at the point derivation touches:
// an event already handed to a detector must not change underneath it.
func TestWithAddsFieldsWithoutMutatingTheOriginal(t *testing.T) {
	original := event.New("s", "u", event.Hour, map[event.FieldPath]event.Value{
		"a.addr": event.NewValue("10.1.2.3"),
	}, 7)

	augmented := original.With(map[event.FieldPath]event.Value{
		derivedPath: event.NewValue("10.1.2.0/24"),
	})

	if original.Len() != 1 {
		t.Errorf("the original grew to %d fields; With must copy", original.Len())
	}
	if augmented.Len() != 2 {
		t.Errorf("the copy has %d fields, want 2", augmented.Len())
	}
	if v, ok := augmented.Get(derivedPath); !ok || v.Text() != "10.1.2.0/24" {
		t.Error("derived field missing from the copy")
	}
	if augmented.Offset() != original.Offset() {
		t.Error("the corpus offset must survive: it is provenance, not content")
	}
}

// TestWithChangesTheIdentifier states the property, which is not a limitation. The
// identifier is a digest of content and derived fields are content, so two events
// differing in their fields must not claim to be the same event.
func TestWithChangesTheIdentifier(t *testing.T) {
	original := event.New("s", "u", event.Hour, map[event.FieldPath]event.Value{
		"a.addr": event.NewValue("10.1.2.3"),
	}, 7)
	augmented := original.With(map[event.FieldPath]event.Value{
		derivedPath: event.NewValue("10.1.2.0/24"),
	})
	if augmented.ID() == original.ID() {
		t.Error("an event with different content carries the same identifier")
	}

	// And it remains a digest of content alone: the same augmentation twice agrees.
	again := original.With(map[event.FieldPath]event.Value{
		derivedPath: event.NewValue("10.1.2.0/24"),
	})
	if again.ID() != augmented.ID() {
		t.Error("the same augmentation produced two identifiers; the digest is not " +
			"content-derived, and E8's batch-independence claim would not survive it")
	}
}

// TestWithNeverOverwritesWhatTheSourceSaid keeps derivation additive. Restating a
// source's own field would let inferred structure silently replace observed fact.
func TestWithNeverOverwritesWhatTheSourceSaid(t *testing.T) {
	original := event.New("s", "u", event.Hour, map[event.FieldPath]event.Value{
		"a.addr": event.NewValue("10.1.2.3"),
	}, 7)
	augmented := original.With(map[event.FieldPath]event.Value{
		"a.addr": event.NewValue("tampered"),
	})
	if v, _ := augmented.Get("a.addr"); v.Text() != "10.1.2.3" {
		t.Errorf("a source's own value was overwritten by derivation: %q", v.Text())
	}
}

// TestTimeUnits pins the duration constants, which the decay factors of §6.2 and §7.2
// depend on. LANL's resolution is one second; the framework stores microseconds so
// that a finer-grained corpus needs no schema change.
func TestTimeUnits(t *testing.T) {
	if event.Second != 1_000_000*event.Microsecond {
		t.Fatalf("Second = %d microseconds", event.Second)
	}
	if event.Day != 24*event.Hour {
		t.Fatalf("Day = %d, want 24 hours", event.Day)
	}
	if event.Hour != 3_600*event.Second {
		t.Fatalf("Hour = %d, want 3600 seconds", event.Hour)
	}
}
