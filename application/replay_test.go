package application_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/JohnPierman/ethogram/application"
	"github.com/JohnPierman/ethogram/domain/calibration"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/registry"
	"github.com/JohnPierman/ethogram/domain/timing"
	"github.com/JohnPierman/ethogram/domain/volume"
	"github.com/JohnPierman/ethogram/infrastructure/state/memory"
)

const (
	src   = event.SourceID("lanl.auth")
	human = event.EntityID("U66@DOM1")
	robot = event.EntityID("C625$@DOM1")
)

// sliceSource yields a fixed sequence of events.
type sliceSource struct {
	events []*event.Event
	next   int
}

func (s *sliceSource) Next() (*event.Event, error) {
	if s.next >= len(s.events) {
		return nil, io.EOF
	}
	e := s.events[s.next]
	s.next++
	return e, nil
}

func mkEvent(entity event.EntityID, at event.Timestamp, authType, dst string, offset int64) *event.Event {
	e := event.New(src, entity, at, map[event.FieldPath]event.Value{
		"auth.authentication_type":  event.NewValue(authType),
		"auth.destination_computer": event.NewValue(dst),
	}, offset)
	return &e
}

// wireFramework assembles the three detectors over fresh in-memory state, exactly as
// cmd/replay does.
func wireFramework(t *testing.T) (*detector.Registry, *registry.Registry) {
	t.Helper()
	const halfLife = novelty.HalfLife(7 * event.Day)

	fieldRegistry := registry.New(registry.DefaultPolicy())
	novStore := memory.NewNoveltyStore(halfLife)
	timStore := memory.NewTimingStore()
	volStore := memory.NewVolumeStore()

	detectors := detector.NewRegistry()
	for _, d := range []detector.Detector{
		novelty.NewDetector(novStore, fieldRegistry, 1.0, halfLife),
		timing.NewDetector(timStore, 1.5, halfLife, timing.DefaultStandardise),
		volume.NewDetector(volStore, timStore, 1.5, halfLife, volume.DefaultMinPeriods),
	} {
		if err := detectors.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	return detectors, fieldRegistry
}

// corpus builds a two-week synthetic stream: burn-in habit for one human entity plus
// machine noise, then a scored window containing one habitual event and one
// out-of-character event.
func corpusEvents() (events []*event.Event, habitualIdx, anomalousIdx int) {
	offset := int64(0)
	add := func(e *event.Event) int {
		events = append(events, e)
		offset++
		return len(events) - 1
	}
	// Ten days of an 09:00 Kerberos-to-C625 habit, with machine noise around it.
	for day := range 10 {
		at := event.Timestamp(day)*event.Day + 9*event.Hour
		add(mkEvent(human, at, "Kerberos", "C625", offset))
		add(mkEvent(robot, at+event.Minute, "Negotiate", "C625", offset))
	}
	// Scored window (burn-in ends at day 10): one habitual, one out of character.
	habitualIdx = add(mkEvent(human, 10*event.Day+9*event.Hour, "Kerberos", "C625", offset))
	anomalousIdx = add(mkEvent(human, 10*event.Day+3*event.Hour, "NTLM", "C17693", offset))
	return events, habitualIdx, anomalousIdx
}

func TestReplayEndToEnd(t *testing.T) {
	detectors, fieldRegistry := wireFramework(t)
	events, habitualIdx, anomalousIdx := corpusEvents()

	var sunk []application.ScoredEvent
	cmd := &application.ReplayCorpusCommand{
		Source:        &sliceSource{events: events},
		Detectors:     detectors,
		FieldRegistry: fieldRegistry,
		BurnInEnd:     10 * event.Day,
		IncludeEntity: func(e event.EntityID) bool { return e[0] == 'U' },
		Sink: func(se application.ScoredEvent) error {
			sunk = append(sunk, se)
			return nil
		},
	}

	report, err := cmd.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// The machine account is excluded by the entity population filter.
	if report.EventsSkipped != 10 {
		t.Errorf("EventsSkipped = %d, want the 10 machine events", report.EventsSkipped)
	}
	// Ten burn-in human events warmed state without being sunk.
	if report.EventsWarmed != 10 {
		t.Errorf("EventsWarmed = %d, want 10", report.EventsWarmed)
	}
	if report.EventsScored != 2 || len(sunk) != 2 {
		t.Fatalf("EventsScored = %d, sunk %d, want 2", report.EventsScored, len(sunk))
	}

	habitual, anomalous := sunk[0], sunk[1]
	if habitual.Event.ID() != events[habitualIdx].ID() || anomalous.Event.ID() != events[anomalousIdx].ID() {
		t.Fatal("sink order does not follow corpus order")
	}

	// Both events carry a combination: J > 0 because at least timing and volume
	// always evaluate.
	for _, se := range sunk {
		if se.Combined == nil {
			t.Fatal("scored event carries no combination despite evaluated verdicts")
		}
		if se.Combined.P <= 0 || se.Combined.P > 1 {
			t.Fatalf("combined P = %v out of range", se.Combined.P)
		}
	}

	// The out-of-character event must be far more anomalous than the habitual one:
	// novel value, unusual hour, all at once.
	if anomalous.Combined.P*100 > habitual.Combined.P {
		t.Errorf("combined P: anomalous %v vs habitual %v; separation too weak",
			anomalous.Combined.P, habitual.Combined.P)
	}
	t.Logf("combined P: habitual=%.4f (J=%d) anomalous=%.3e (J=%d)",
		habitual.Combined.P, habitual.Combined.J,
		anomalous.Combined.P, anomalous.Combined.J)
}

// TestConformalDoesNotInheritBrownsScale guards a defect that produces a plausible
// number rather than an error, which is the kind that survives review.
//
// Brown's covariance (19) is estimated during burn-in from the detectors' own p-values,
// because those are what the combination consumed when the estimate was made. Under
// conformal calibration the combination consumes ranks instead, and the two live on
// different scales — −2 ln P runs to thousands on a miscalibrated model tail and to tens
// on a rank. Dividing the calibrated statistic by a scale measured on the other one
// yields a c that means nothing, and nothing about the output looks wrong.
//
// So the calibrated statistic degrades to Fisher, which §10.2 requires, while ModelLogP
// keeps the correction because it is computed on the p-values the covariance was
// measured on.
func TestConformalDoesNotInheritBrownsScale(t *testing.T) {
	detectors, fieldRegistry := wireFramework(t)
	events, _, _ := corpusEvents()

	var sunk []application.ScoredEvent
	cmd := &application.ReplayCorpusCommand{
		Source:        &sliceSource{events: events},
		Detectors:     detectors,
		FieldRegistry: fieldRegistry,
		BurnInEnd:     10 * event.Day,
		IncludeEntity: func(e event.EntityID) bool { return e[0] == 'U' },
		// Both estimates are asked for, which is the configuration the defect needs.
		Correlations: calibration.NewCorrelations(1),
		Conformal:    calibration.NewConformal(1),
		Sink: func(se application.ScoredEvent) error {
			sunk = append(sunk, se)
			return nil
		},
	}
	if _, err := cmd.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sunk) == 0 {
		t.Fatal("nothing scored")
	}

	calibrated := 0
	for _, se := range sunk {
		if se.Combined == nil || !se.Combined.Conformal {
			continue
		}
		calibrated++
		if se.Combined.Corrected {
			t.Error("a conformally calibrated score reports Brown as applied; the " +
				"covariance was measured on the model p-values and does not transfer")
		}
		if se.Combined.C != 1 {
			t.Errorf("Brown's scale c = %v on a calibrated statistic, want exactly 1",
				se.Combined.C)
		}
		if se.Combined.ModelLogP == se.Combined.LogP {
			t.Error("ModelLogP equals LogP on a calibrated score; the tie-break must be " +
				"the combination over the detectors' own p-values, not a copy")
		}
	}
	if calibrated == 0 {
		t.Fatal("no score was conformally calibrated; the test proved nothing")
	}
}

// TestReplayBatchIndependenceEndToEnd is E8 at system level: the probe's combined
// score must be byte-identical whatever co-resident traffic follows it in the stream,
// across the whole wired framework rather than one detector.
func TestReplayBatchIndependenceEndToEnd(t *testing.T) {
	run := func(fillerCount int) []byte {
		detectors, fieldRegistry := wireFramework(t)
		events, _, anomalousIdx := corpusEvents()
		// Append filler AFTER the probe: under equation (1) this traffic would move
		// the probe's score; under history-relative scoring it cannot.
		for i := range fillerCount {
			events = append(events, mkEvent("U999@DOM1",
				10*event.Day+4*event.Hour+event.Timestamp(i)*event.Second,
				"NTLM", fmt.Sprintf("C%d", 30000+i), int64(9000+i)))
		}

		var probe *application.ScoredEvent
		cmd := &application.ReplayCorpusCommand{
			Source:        &sliceSource{events: events},
			Detectors:     detectors,
			FieldRegistry: fieldRegistry,
			BurnInEnd:     10 * event.Day,
			IncludeEntity: func(e event.EntityID) bool { return e[0] == 'U' },
			Sink: func(se application.ScoredEvent) error {
				if se.Event.ID() == events[anomalousIdx].ID() {
					c := se
					probe = &c
				}
				return nil
			},
		}
		if _, err := cmd.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		if probe == nil {
			t.Fatal("probe was not sunk")
		}
		buf := probe.Verdicts.CanonicalBytes()
		return append(buf, fmt.Sprintf("|%x|%d", probe.Combined.P, probe.Combined.J)...)
	}

	base := run(0)
	for _, n := range []int{1, 50, 500} {
		if got := run(n); !bytes.Equal(base, got) {
			t.Fatalf("E8 (system level): %d filler events changed the probe's scores", n)
		}
	}
}

// TestReplayNoOpinionIsNotAScore: an event whose every verdict abstains yields a nil
// combination and counts as no-opinion, never a fabricated score (R3, §10.2).
func TestReplayNoOpinionIsNotAScore(t *testing.T) {
	// A detector registry containing only Detector I, and an event whose only fields
	// are unusable: J = 0.
	const halfLife = novelty.HalfLife(7 * event.Day)
	fieldRegistry := registry.New(registry.DefaultPolicy())
	novStore := memory.NewNoveltyStore(halfLife)
	detectors := detector.NewRegistry()
	if err := detectors.Register(novelty.NewDetector(novStore, fieldRegistry, 1.0, halfLife)); err != nil {
		t.Fatal(err)
	}

	// Warm the registry so the field kind settles, then send a "?" value.
	events := make([]*event.Event, 0, 61)
	for i := range 60 {
		events = append(events, mkEvent(human, event.Timestamp(i)*event.Minute,
			[]string{"Kerberos", "NTLM"}[i%2], "C625", int64(i)))
	}
	probe := event.New(src, human, 2*event.Hour, map[event.FieldPath]event.Value{
		"auth.authentication_type":  event.UnusableValue("?"),
		"auth.destination_computer": event.UnusableValue("?"),
	}, 999)
	events = append(events, &probe)

	var sunk []application.ScoredEvent
	cmd := &application.ReplayCorpusCommand{
		Source:        &sliceSource{events: events},
		Detectors:     detectors,
		FieldRegistry: fieldRegistry,
		BurnInEnd:     event.Hour, // events after the first hour are scored
		Sink:          func(se application.ScoredEvent) error { sunk = append(sunk, se); return nil },
	}
	report, err := cmd.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.NoOpinion == 0 {
		t.Fatal("the all-unusable event must be recorded as no-opinion")
	}
	last := sunk[len(sunk)-1]
	if last.Event.ID() != probe.ID() {
		t.Fatal("probe not last in sink")
	}
	if last.Combined != nil {
		t.Fatalf("no-opinion event carries a combination: %+v", last.Combined)
	}
	// The verdicts still exist, abstained, with reasons: the record shows why there
	// is no opinion.
	if len(last.Verdicts) == 0 {
		t.Fatal("abstained verdicts must still be recorded")
	}
	for _, v := range last.Verdicts {
		if !v.Status().IsAbstained() {
			t.Fatalf("expected abstention, got %s", v.Status())
		}
	}
}
