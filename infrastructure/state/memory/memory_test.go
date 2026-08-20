package memory_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/timing"
	"github.com/JohnPierman/ethogram/domain/volume"
	"github.com/JohnPierman/ethogram/infrastructure/state/memory"
)

const (
	src = event.SourceID("lanl.auth")
	ent = event.EntityID("U66@DOM1")
)

// TestTimingStoreRoundTripsEveryField is a guard against a whole class of silent defect,
// not a test of a value. A store that names each field it copies drops any field added
// afterwards, and the detector then reads zeroes it cannot distinguish from a cold start.
// That cost a full replay in which the timing detector abstained on all 4,190,603 events
// because two accumulators never came back out of this store.
//
// Reflection over the struct is deliberate: it fails when a field is ADDED and not copied,
// which is precisely the moment a hand-written comparison would still pass.
func TestTimingStoreRoundTripsEveryField(t *testing.T) {
	ctx := context.Background()
	store := memory.NewTimingStore()

	want := &timing.State{
		Moments:   &timing.Moments{C: []float64{0.25, -0.5}, S: []float64{0.75, 0.125}, W: 41},
		LastSeen:  event.Timestamp(1234567),
		LogUSum:   -37.5,
		LogUSumSq: 91.25,
	}
	if err := store.SaveState(ctx, src, ent, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.FindByEntity(ctx, src, ent)
	if err != nil || !ok {
		t.Fatalf("state missing: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("state did not round-trip\n got %+v\nwant %+v", got, want)
	}

	// And the copy must be isolated, or Score could mutate stored state (§5.2).
	got.Moments.C[0] = 99
	got.LogUSum = 99
	again, _, err := store.FindByEntity(ctx, src, ent)
	if err != nil {
		t.Fatal(err)
	}
	if again.Moments.C[0] == 99 || again.LogUSum == 99 {
		t.Fatal("the returned state aliases the stored one; a scorer could mutate it")
	}
}

// TestVolumeStoreRoundTripsEveryField holds the volume store to the same standard, since
// its state gained a field for the same reason.
func TestVolumeStoreRoundTripsEveryField(t *testing.T) {
	ctx := context.Background()
	store := memory.NewVolumeStore()

	want := &volume.State{
		Rate:              volume.GammaPosterior{A: 12.5, B: 7.25},
		PeriodIndex:       9,
		PeriodCount:       3,
		CompletedPeriods:  8,
		WindowIndex:       217,
		WindowCount:       5,
		WindowExpected:    2.5,
		DispersionWindows: 6.5,
		DispersionSum:     11.75,
		LastSeen:          event.Timestamp(987654),
	}
	if err := store.SaveState(ctx, src, ent, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.FindByEntity(ctx, src, ent)
	if err != nil || !ok {
		t.Fatalf("state missing: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("state did not round-trip\n got %+v\nwant %+v", got, want)
	}
}
