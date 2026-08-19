package novelty_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/registry"
)

// fByteCount is a continuous field: a fresh measurement on nearly every event.
const fByteCount = event.FieldPath("flows.byte_count")

// warmRegistryWithBytes settles fByteCount as continuous alongside the categorical
// fields, by feeding it a distinct measurement on every event.
func warmRegistryWithBytes() *registry.Registry {
	reg := registry.New(registry.DefaultPolicy())
	for i := range warmEvents {
		fields := map[event.FieldPath]event.Value{
			fAuthType:  event.NewValue([]string{"Negotiate", "Kerberos", "NTLM"}[i%3]),
			fByteCount: event.NewValue(fmt.Sprintf("%d", 40_000_000+i*7919)),
		}
		reg.ObserveEvent(mkEvent("warm", event.Timestamp(i+1)*event.Second, fields, int64(i)))
	}
	return reg
}

func requireContinuous(t *testing.T, reg *registry.Registry) {
	t.Helper()
	kind, known := reg.KindOf(src, fByteCount)
	if !known || kind != registry.KindContinuous {
		t.Fatalf("fixture: fByteCount settled as %s, want continuous", kind)
	}
}

// TestContinuousFieldIsScored is the change in one assertion. A continuous field used
// to induce no verdict from Detector I at all — the per-entity question, which is the
// framework's central commitment, was simply not asked of any measurement.
func TestContinuousFieldIsScored(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistryWithBytes()
	requireContinuous(t, reg)
	d, _ := newWiredDetector(reg)

	e := mkEvent(entityU66, event.Hour, map[event.FieldPath]event.Value{
		fByteCount: event.NewValue("1500"),
	}, 1)

	verdicts, _, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range verdicts.Evaluated() {
		if len(v.Target().Fields) == 1 && v.Target().Fields[0] == fByteCount {
			return
		}
	}
	t.Fatal("a continuous field induced no evaluated verdict from Detector I")
}

// TestContinuousIsCountedByBandNotByMeasurement is the property the projection exists
// for, and the one that makes admitting a continuous field safe at all.
//
// Two measurements that are not equal but lie in the same band are the same vocabulary
// item: the second is not a first sighting. Counting the raw text instead would make
// every measurement novel, which is the saturation §5.1 records as the reason numeric
// fields were withheld in the first place.
func TestContinuousIsCountedByBandNotByMeasurement(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistryWithBytes()
	requireContinuous(t, reg)
	d, _ := newWiredDetector(reg)

	first := mkEvent(entityU66, event.Hour, map[event.FieldPath]event.Value{
		fByteCount: event.NewValue("1500"),
	}, 1)
	_, obs, err := d.Score(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if commitErr := obs.Commit(ctx); commitErr != nil {
		t.Fatal(commitErr)
	}

	// 1800 is a different measurement in the same band as 1500.
	same := mkEvent(entityU66, 2*event.Hour, map[event.FieldPath]event.Value{
		fByteCount: event.NewValue("1800"),
	}, 2)
	verdicts, _, err := d.Score(ctx, same)
	if err != nil {
		t.Fatal(err)
	}
	v := byteCountVerdict(t, verdicts)
	if got := v.Evidence().Stats["n_v"]; got <= 0 {
		t.Errorf("1800 after 1500: n_v = %v, want a positive count — the band is the "+
			"vocabulary item, so this is not a first sighting", got)
	}
	if got := v.Evidence().Stats["K"]; got != 1 {
		t.Errorf("K = %v after two measurements in one band, want 1", got)
	}

	// A measurement four decades away is a genuinely new band, and must be novel.
	far := mkEvent(entityU66, 3*event.Hour, map[event.FieldPath]event.Value{
		fByteCount: event.NewValue("18000000"),
	}, 3)
	verdicts, _, err = d.Score(ctx, far)
	if err != nil {
		t.Fatal(err)
	}
	v = byteCountVerdict(t, verdicts)
	if got := v.Evidence().Stats["n_v"]; got != 0 {
		t.Errorf("a measurement in an unseen band: n_v = %v, want 0", got)
	}
}

// TestContinuousStateIsBoundedByTheBands: §13.3 requires finite state. Unbanded, one
// entity's thousand measurements are a thousand stored rows and a thousand-term sum in
// equation (5) on every subsequent event.
func TestContinuousStateIsBoundedByTheBands(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistryWithBytes()
	requireContinuous(t, reg)
	d, repo := newWiredDetector(reg)

	for i := range 1000 {
		e := mkEvent(entityU66, event.Timestamp(i+1)*event.Minute, map[event.FieldPath]event.Value{
			fByteCount: event.NewValue(fmt.Sprintf("%d", 1+i*104_729)),
		}, int64(i))
		_, obs, err := d.Score(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		if err := obs.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := repo.FindAllByEntityField(ctx, src, entityU66, fByteCount, 1001*event.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) > 32 {
		t.Fatalf("1,000 distinct measurements stored %d rows; state is not bounded", len(rows))
	}
	if len(rows) < 2 {
		t.Fatalf("eight decades of measurements collapsed to %d rows; no signal survives", len(rows))
	}
}

// TestContinuousEvidenceCarriesTheMeasurement: R5 requires a verdict carry evidence
// sufficient to reconstruct it by hand. For a banded field that means both halves — the
// band the count was taken against, and the measurement the band was derived from.
// Without the measurement a reader cannot check the assignment; without the band the
// count cannot be found.
func TestContinuousEvidenceCarriesTheMeasurement(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistryWithBytes()
	requireContinuous(t, reg)
	d, _ := newWiredDetector(reg)

	e := mkEvent(entityU66, event.Hour, map[event.FieldPath]event.Value{
		fByteCount: event.NewValue("1500"),
	}, 1)
	verdicts, _, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}

	labels := byteCountVerdict(t, verdicts).Evidence().Labels
	if got, want := labels["observed"], registry.Band(1500); got != want {
		t.Errorf("observed = %q, want the band %q", got, want)
	}
	if got := labels["measured"]; got != "1500" {
		t.Errorf("measured = %q, want the raw measurement %q", got, "1500")
	}
}

// TestContinuousAbstainsOnAnUnmeasurableValue: NumericFraction admits a residue of
// sentinels, so a continuous field can carry a value that is not a number. It must
// abstain (R3), never be counted as a vocabulary item of its own — a sentinel in the
// vocabulary would let its own rate masquerade as novelty.
func TestContinuousAbstainsOnAnUnmeasurableValue(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistryWithBytes()
	requireContinuous(t, reg)
	d, repo := newWiredDetector(reg)

	e := mkEvent(entityU66, event.Hour, map[event.FieldPath]event.Value{
		fByteCount: event.NewValue("unknown"),
	}, 1)
	verdicts, obs, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, v := range verdicts {
		if len(v.Target().Fields) != 1 || v.Target().Fields[0] != fByteCount {
			continue
		}
		found = true
		if _, evaluated := v.PValue(); evaluated {
			t.Error("an unmeasurable value on a continuous field must not produce a p-value")
		}
	}
	if !found {
		t.Fatal("an unmeasurable value must produce an abstention, not silence (R3)")
	}

	if commitErr := obs.Commit(ctx); commitErr != nil {
		t.Fatal(commitErr)
	}
	rows, err := repo.FindAllByEntityField(ctx, src, entityU66, fByteCount, 2*event.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("an unmeasurable value contributed %d rows of state, want none", len(rows))
	}
}

// byteCountVerdict returns the evaluated verdict naming fByteCount, failing if there is
// none.
func byteCountVerdict(t *testing.T, verdicts detector.Verdicts) detector.Verdict {
	t.Helper()
	for _, v := range verdicts.Evaluated() {
		if len(v.Target().Fields) == 1 && v.Target().Fields[0] == fByteCount {
			return v
		}
	}
	t.Fatal("no evaluated verdict names the byte-count field")
	return detector.Verdict{}
}
