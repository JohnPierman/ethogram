package memory_test

import (
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/JohnPierman/ethogram/domain/burst"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/timing"
	"github.com/JohnPierman/ethogram/domain/volume"
	"github.com/JohnPierman/ethogram/infrastructure/state/memory"
)

const (
	src = event.SourceID("lanl.auth")
	ent = event.EntityID("U66@DOM1")

	// testHalfLife is long enough that nothing decays over a test's span, so what a test
	// measures is the store's copying rather than the discount.
	testHalfLife = novelty.HalfLife(1 << 62)
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

// TestBurstStoreDoesNotShareItsSlice is the one thing this store must get right that the others
// do not have to think about (#53).
//
// burst.State holds a slice of recent arrivals, and the detector appends the event being scored
// to whatever it is handed before evaluating. A struct copy shares that backing array, so the
// append would write the scored event into stored state — observing before scoring while
// appearing to score before observing, which is exactly the silent failure §5.2 is built to
// prevent. The other stores here copy by assignment because their states are scalars only.
func TestBurstStoreDoesNotShareItsSlice(t *testing.T) {
	ctx := context.Background()
	store := memory.NewBurstStore()

	stored := &burst.State{}
	for i := 1; i <= 10; i++ {
		stored.Observe(event.Timestamp(i)*60*event.Second, testHalfLife)
	}
	if err := store.SaveState(ctx, "src", "alice", stored); err != nil {
		t.Fatalf("save: %v", err)
	}

	handed, ok, err := store.FindByEntity(ctx, "src", "alice")
	if err != nil || !ok {
		t.Fatalf("find: %v, ok=%v", err, ok)
	}
	before := len(stored.Recent)
	gapsBefore := stored.Gaps

	// Exactly what the detector does to what it is handed.
	handed.Observe(event.Timestamp(11)*60*event.Second, testHalfLife)

	again, _, err := store.FindByEntity(ctx, "src", "alice")
	if err != nil {
		t.Fatalf("refind: %v", err)
	}
	if len(again.Recent) != before || again.Gaps != gapsBefore {
		t.Errorf("appending to the handed-out state changed the store: %d held / %.0f gap-sum "+
			"became %d / %.0f", before, gapsBefore, len(again.Recent), again.Gaps)
	}
	// And the backing arrays must be distinct objects, not merely equal right now.
	if len(again.Recent) > 0 && &again.Recent[0] == &handed.Recent[0] {
		t.Error("the returned state shares its backing array with the stored one")
	}
}

// TestBurstStoreReportsEligibilitySeparately: an arm that found nothing and an arm that was never
// able to speak are different claims, and only the eligible count distinguishes them.
func TestBurstStoreReportsEligibilitySeparately(t *testing.T) {
	ctx := context.Background()
	store := memory.NewBurstStore()

	warm := &burst.State{}
	for i := 1; i <= burst.MinGaps+5; i++ {
		warm.Observe(event.Timestamp(i)*600*event.Second, testHalfLife)
	}
	cold := &burst.State{}
	cold.Observe(600*event.Second, testHalfLife)

	if err := store.SaveState(ctx, "src", "warm", warm); err != nil {
		t.Fatalf("save warm: %v", err)
	}
	if err := store.SaveState(ctx, "src", "cold", cold); err != nil {
		t.Fatalf("save cold: %v", err)
	}

	r := store.Report()
	if r.Entities != 2 {
		t.Errorf("entities %d, want 2", r.Entities)
	}
	if r.Eligible != 1 {
		t.Errorf("eligible %d, want 1: a single-arrival entity has no rate", r.Eligible)
	}
	if r.MaxWindow != burst.MaxWindow {
		t.Errorf("max window %d, want %d", r.MaxWindow, burst.MaxWindow)
	}
	// §13.3: the held timestamps must be bounded per entity whatever the event count.
	if r.TimestampsHeld > int64(burst.MaxWindow)*r.Entities {
		t.Errorf("holding %d timestamps across %d entities, above the per-entity bound of %d",
			r.TimestampsHeld, r.Entities, burst.MaxWindow)
	}
	// The median rate is over ELIGIBLE entities only: including a cold one would report a
	// rate formed from a handful of gaps as though it were an estimate.
	if want := 1.0 / 600; math.Abs(r.MedianRateHertz-want) > 0.05*want {
		t.Errorf("median rate %.3e, want about %.3e", r.MedianRateHertz, want)
	}

	if empty := memory.NewBurstStore().Report(); empty.Entities != 0 ||
		empty.MedianRateHertz != 0 {
		t.Errorf("an empty store reported %+v", empty)
	}
}
