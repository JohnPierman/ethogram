package timing_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/timing"
)

// abstentionReason drives an entity through the given schedule and returns the reason the last
// event's verdict carried, plus whether it was evaluated at all.
func abstentionReason(t *testing.T, at []event.Timestamp) (reason string, evaluated bool) {
	t.Helper()
	ctx := context.Background()
	store := &memoryStates{states: map[string]*timing.State{}}
	d := timing.NewDetector(store, 1.5, novelty.HalfLife(7*event.Day), true)

	for i, ts := range at {
		e := event.New(tSrc, tEntity, ts, map[event.FieldPath]event.Value{
			"auth.authentication_type": event.NewValue("Kerberos"),
		}, int64(i))
		verdicts, obs, err := d.Score(ctx, &e)
		if err != nil {
			t.Fatalf("Score %d: %v", i, err)
		}
		if err := obs.Commit(ctx); err != nil {
			t.Fatalf("Commit %d: %v", i, err)
		}
		if i == len(at)-1 {
			v := verdicts[0]
			if _, ok := v.PValue(); ok {
				return "", true
			}
			if v.Status() != detector.StatusAbstainedUnusable {
				t.Fatalf("last verdict is %s, want an abstention or an evaluation",
					v.Status())
			}
			return v.Reason(), false
		}
	}
	t.Fatal("no events scored")
	return "", false
}

// TestAbstentionCausesAreDistinguishable is #37's first requirement: the two reasons an entity
// cannot be standardised point opposite ways, so a run must be able to tell them apart.
//
// Too little history is a warm-up that passes. No spread is a property of the account that
// never will, and it falls on exactly the most regular accounts -- the ones for which an event
// on the far side of the clock is most remarkable. Reporting a single abstention total conceals
// which of those a run is dominated by, which is why nothing is changed here on the strength of
// the objection until the split has been measured.
func TestAbstentionCausesAreDistinguishable(t *testing.T) {
	// Too little history: a handful of events, below MinStandardiseWeight.
	few := make([]event.Timestamp, 0, 4)
	for i := 0; i < 4; i++ {
		few = append(few, event.Timestamp(int64(i)*int64(event.Day)+10*int64(event.Hour)))
	}
	reason, evaluated := abstentionReason(t, few)
	if evaluated {
		t.Fatal("four events were enough to standardise; the minimum weight has moved")
	}
	if !strings.Contains(reason, "too little") {
		t.Errorf("with four events the reason is %q, want the history cause", reason)
	}

	// A once-daily account, sixty days of it. This is the case that reframes #37 and the one
	// the gate fix serves. Its DISCOUNTED weight saturates at 10.6 against a minimum of 20,
	// so while the gate counted that weight it abstained for want of history forever -- after
	// sixty days, after six hundred. Counting undiscounted observations, sixty days is sixty
	// observations and the account is standardisable.
	var daily []event.Timestamp
	for i := 0; i < 60; i++ {
		daily = append(daily,
			event.Timestamp(int64(i)*int64(event.Day)+10*int64(event.Hour)))
	}
	reason, evaluated = abstentionReason(t, daily)
	if !evaluated && strings.Contains(reason, "too little") {
		t.Errorf("a once-daily account with sixty observations still abstains for want of" +
			" history; the sample-size gate is still counting the discount")
	}

	// The no-spread cause needs an account dense enough to clear the weight gate while
	// still landing at one phase, which the coverage boundary below makes precise.
	var dense []event.Timestamp
	for i := 0; i < 400; i++ {
		// Six events an hour apart within one day, repeated: gaps well inside the boundary,
		// so the weight clears 20, and every event at the same six phases.
		day := int64(i / 6)
		hour := int64(9 + i%6)
		dense = append(dense,
			event.Timestamp(day*int64(event.Day)+hour*int64(event.Hour)))
	}
	reason, evaluated = abstentionReason(t, dense)
	if !evaluated && !strings.Contains(reason, "no spread") {
		t.Errorf("a dense single-window account abstained for %q; if the weight gate is"+
			" cleared the only remaining cause is the spread", reason)
	}
}

// TestStandardiseCoverageIsStated is the other half of #37, and the half the issue does not
// name: the minimum weight is not a warm-up but a claim about which accounts the arm can serve
// at all.
//
// The weight is discounted per event, so it saturates at 1/(1-delta). At the framework's
// seven-day half-life a once-daily account saturates at 10.6 against a minimum of 20, so the
// standardised statistic is unavailable to it permanently. The boundary is about 12.4 hours
// between events, and it is asserted so that a change to either constant cannot silently
// narrow the arm's coverage.
func TestStandardiseCoverageIsStated(t *testing.T) {
	hours := timing.StandardiseCoverageHours(novelty.HalfLife(7 * event.Day))
	if hours < 12.0 || hours > 13.0 {
		t.Errorf("coverage boundary is %.2f hours, expected about 12.4", hours)
	}
	// A once-daily account is outside it, which is the case measured above.
	if hours >= 24 {
		t.Errorf("coverage boundary %.2f hours would admit a once-daily account, which"+
			" contradicts the measurement in TestAbstentionCausesAreDistinguishable", hours)
	}
}

// TestAbstentionCauseStringsAreDistinct. The two reasons are counted by their text in a run's
// `abstain_causes` block, so they must not collide and must not be empty.
func TestAbstentionCauseStringsAreDistinct(t *testing.T) {
	history := timing.CauseTooLittleHistory.String()
	spread := timing.CauseNoSpread.String()
	none := timing.CauseNone.String()

	for name, s := range map[string]string{"history": history, "spread": spread} {
		if s == "" {
			t.Errorf("%s cause has an empty string, so it cannot be counted", name)
		}
	}
	if history == spread {
		t.Error("the two causes share a string and cannot be told apart in a run")
	}
	if none == history || none == spread {
		t.Error("CauseNone collides with a real cause")
	}
}
