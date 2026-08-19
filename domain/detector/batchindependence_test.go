package detector_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"testing"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
)

// ---------------------------------------------------------------------------
// Fixtures
//
// fixtureNovelty is a deliberately small history-relative detector, standing in for
// Detector I (§6) so that E8 can be exercised before §6 exists. It computes the
// smoothed predictive of equation (4) with α = 1 and no decay, and the discrete tail
// mass of equation (5). The production implementation adds decay (§6.2), lazy row
// updates, and full evidence.
//
// Two determinism properties are load-bearing here and both are deliberate:
//   - values are accumulated in sorted order, never in map order, so the float sum in
//     (5) is bit-identical run to run;
//   - state is read during Score and written only by the returned Observation, so
//     Score cannot destroy the novelty it is measuring.
// ---------------------------------------------------------------------------

const fixtureAlpha = 1.0

type fixtureNovelty struct {
	// counts[entity][field][value] = observed count.
	counts map[event.EntityID]map[event.FieldPath]map[string]float64
}

func newFixtureNovelty() (detector.Detector, error) {
	return &fixtureNovelty{
		counts: make(map[event.EntityID]map[event.FieldPath]map[string]float64),
	}, nil
}

func (d *fixtureNovelty) ID() detector.ID { return "fixture.novelty" }

func (d *fixtureNovelty) NullHypothesis() string {
	return "e(f) is drawn from the entity's historical distribution over D_f, per §6.1 equation (4)."
}

func (d *fixtureNovelty) Score(_ context.Context, e *event.Event) (detector.Verdicts, detector.Observation, error) {
	var out detector.Verdicts

	// Iteration is over the event's sorted field order, the only order this API
	// offers, so verdict order is fixed without further sorting.
	for f, v := range e.All() {
		target := detector.Target{Event: e.ID(), Entity: e.Entity(), Fields: []event.FieldPath{f}}

		if !v.IsUsable() {
			// Present but not scoreable: abstain, never a neutral score (R3).
			ab, err := detector.NewAbstained(d.ID(), target,
				detector.StatusAbstainedUnusable, "value is not interpretable",
				detector.NewEvidence([]int{4}, nil, map[string]string{"observed": v.Text()}))
			if err != nil {
				return nil, nil, err
			}
			out = append(out, ab)
			continue
		}

		p, stats := d.tailMass(e.Entity(), f, v.Text())
		ev := detector.NewEvidence([]int{4, 5}, stats, map[string]string{
			"field":    string(f),
			"observed": v.Text(),
		})
		verdict, err := detector.NewEvaluated(d.ID(), target, p, ev)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, verdict)
	}

	return out, &fixtureObservation{d: d, e: e}, nil
}

// tailMass evaluates equations (4) and (5).
//
// N = Σ n_v and K = |{v : n_v > 0}| are taken over the entity's history for this
// field only. With no history N = 0 and K = 0, so (4) places unit mass on the
// reserved category ∅ and (5) returns exactly 1: a first observation is never
// anomalous (§6.2).
func (d *fixtureNovelty) tailMass(entity event.EntityID, f event.FieldPath, value string) (float64, map[string]float64) {
	hist := d.counts[entity][f]

	// Sorted keys: the float accumulation below must not depend on map order.
	values := make([]string, 0, len(hist))
	for v := range hist {
		values = append(values, v)
	}
	slices.Sort(values)

	var n float64
	for _, v := range values {
		n += hist[v]
	}
	k := float64(len(values))
	denom := n + fixtureAlpha*(k+1)

	pHatUnseen := fixtureAlpha / denom
	pHatObserved := pHatUnseen
	if c, seen := hist[value]; seen {
		pHatObserved = (c + fixtureAlpha) / denom
	}

	// Equation (5): tail mass over values no more probable than the observed one,
	// including the reserved category ∅ whose mass is p̂(∅). Accumulated in sorted
	// order for bit-identical reproducibility.
	tail := 0.0
	if pHatUnseen <= pHatObserved {
		tail += pHatUnseen
	}
	for _, v := range values {
		pv := (hist[v] + fixtureAlpha) / denom
		if pv <= pHatObserved {
			tail += pv
		}
	}
	if tail > 1 {
		tail = 1
	}

	return tail, map[string]float64{
		"n_v":       hist[value],
		"N":         n,
		"K":         k,
		"alpha":     fixtureAlpha,
		"p_hat":     pHatObserved,
		"p_hat_nil": pHatUnseen,
	}
}

type fixtureObservation struct {
	d *fixtureNovelty
	e *event.Event
}

func (o *fixtureObservation) EventID() event.ID       { return o.e.ID() }
func (o *fixtureObservation) DetectorID() detector.ID { return o.d.ID() }

func (o *fixtureObservation) Commit(context.Context) error {
	for f, v := range o.e.All() {
		if !v.IsUsable() {
			continue
		}
		byEntity, ok := o.d.counts[o.e.Entity()]
		if !ok {
			byEntity = make(map[event.FieldPath]map[string]float64)
			o.d.counts[o.e.Entity()] = byEntity
		}
		byField, ok := byEntity[f]
		if !ok {
			byField = make(map[string]float64)
			byEntity[f] = byField
		}
		byField[v.Text()]++
	}
	return nil
}

// ---------------------------------------------------------------------------
// Negative control: a detector exhibiting the §3.2 defect.
//
// This standardises against the batch under evaluation, equation (1), reading a
// batch handed to it out of band. It exists to prove the E8 check has teeth: a
// harness that cannot fail is not evidence of anything. Equation (2) predicts its
// behaviour exactly, the campaign event's own z-score being √((1−p)/p) and so
// strictly decreasing in the campaign's share of the batch.
// ---------------------------------------------------------------------------

type fixtureBatchRelative struct {
	batch *[]*event.Event // shared with the test, standing for μ̂_B and σ̂_B
	field event.FieldPath
}

func (d *fixtureBatchRelative) ID() detector.ID { return "fixture.batch_relative" }

func (d *fixtureBatchRelative) NullHypothesis() string {
	return "the standardised feature z(e) of equation (1) is drawn from the batch distribution; " +
		"this is the formulation §3.2 rejects."
}

func (d *fixtureBatchRelative) Score(_ context.Context, e *event.Event) (detector.Verdicts, detector.Observation, error) {
	xs := make([]float64, 0, len(*d.batch))
	for _, be := range *d.batch {
		if v, ok := be.Get(d.field); ok {
			xs = append(xs, float64(len(v.Text())))
		}
	}
	mean := 0.0
	for _, x := range xs {
		mean += x
	}
	if len(xs) > 0 {
		mean /= float64(len(xs))
	}
	varsum := 0.0
	for _, x := range xs {
		varsum += (x - mean) * (x - mean)
	}
	sd := 0.0
	if len(xs) > 0 {
		sd = math.Sqrt(varsum / float64(len(xs))) // population sd, as §3.2 specifies
	}

	v, ok := e.Get(d.field)
	if !ok {
		ab, err := detector.NewAbstained(d.ID(), detector.Target{Event: e.ID(), Entity: e.Entity()},
			detector.StatusAbstainedStructural, "field absent",
			detector.NewEvidence([]int{1}, nil, map[string]string{"field": string(d.field)}))
		return detector.Verdicts{ab}, detector.NoObservation{Event: e.ID(), Detector: d.ID()}, err
	}

	z := 0.0
	if sd > 0 {
		z = (float64(len(v.Text())) - mean) / sd
	}
	p := math.Exp(-math.Abs(z)) // monotone in |z|; a stand-in tail, not a real null
	if p <= 0 {
		p = math.SmallestNonzeroFloat64
	}
	verdict, err := detector.NewEvaluated(d.ID(), detector.Target{Event: e.ID(), Entity: e.Entity()}, p,
		detector.NewEvidence([]int{1, 2}, map[string]float64{"z": z, "mu_B": mean, "sigma_B": sd}, nil))
	if err != nil {
		return nil, nil, err
	}
	return detector.Verdicts{verdict}, detector.NoObservation{Event: e.ID(), Detector: d.ID()}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const (
	testSource      = event.SourceID("test.auth")
	fAuthType       = event.FieldPath("auth.type")
	fDstComputer    = event.FieldPath("auth.destination_computer")
	entityUnderTest = event.EntityID("U66@DOM1")
	otherEntity     = event.EntityID("U3005@DOM1")
)

func mkEvent(entity event.EntityID, at event.Timestamp, authType, dst string, offset int64) *event.Event {
	e := event.New(testSource, entity, at, map[event.FieldPath]event.Value{
		fAuthType:    event.NewValue(authType),
		fDstComputer: event.NewValue(dst),
	}, offset)
	return &e
}

// history is the state the probe is judged against, identical in every case.
func history() []*event.Event {
	return []*event.Event{
		mkEvent(entityUnderTest, 1*event.Hour, "Negotiate", "C625", 1),
		mkEvent(entityUnderTest, 2*event.Hour, "Negotiate", "C625", 2),
		mkEvent(entityUnderTest, 3*event.Hour, "Kerberos", "C653", 3),
	}
}

// probe is the event whose score must not move.
func probe() *event.Event {
	return mkEvent(entityUnderTest, 4*event.Hour, "NTLM", "C17693", 4)
}

// filler generates co-resident traffic. Under equation (1) this traffic changes the
// probe's score; under a history-relative formulation it cannot.
func filler(n int, at event.Timestamp) []*event.Event {
	out := make([]*event.Event, 0, n)
	for i := range n {
		out = append(out, mkEvent(otherEntity, at+event.Timestamp(i)*event.Minute,
			"NTLM", fmt.Sprintf("C%d", 20000+i), int64(1000+i)))
	}
	return out
}

// cases builds batch compositions that share a prefix and probe but differ in every
// other respect: batch size from 4 to 504, and co-resident traffic that is absent,
// scarce, or overwhelming. Equation (2) says a batch-relative score must move across
// these; R1 says ours must not.
func cases() []detector.BatchIndependenceCase {
	h, p := history(), probe()
	build := func(name string, tail []*event.Event) detector.BatchIndependenceCase {
		batch := make([]*event.Event, 0, len(h)+1+len(tail))
		batch = append(batch, h...)
		batch = append(batch, p)
		batch = append(batch, tail...)
		return detector.BatchIndependenceCase{Name: name, Batch: batch, ProbeIndex: len(h)}
	}
	return []detector.BatchIndependenceCase{
		build("probe_alone", nil),
		build("one_co_resident", filler(1, 5*event.Hour)),
		build("campaign_p_0.02", filler(50, 5*event.Hour)),
		build("campaign_p_0.002", filler(500, 5*event.Hour)),
	}
}

// ---------------------------------------------------------------------------
// E8
// ---------------------------------------------------------------------------

// TestE8BatchIndependence is evaluation hypothesis E8 (§12.3): identical events
// replayed in differing batch compositions must yield identical scores. It is the
// test that adjudicates the repair of the §3.2 defect, and it gates every other
// hypothesis.
func TestE8BatchIndependence(t *testing.T) {
	rep, err := detector.CheckBatchIndependence(context.Background(), newFixtureNovelty, cases())
	if err != nil {
		t.Fatalf("E8 failed: %v", err)
	}
	if !rep.Identical {
		t.Fatalf("E8: digests differ: %v", rep.DigestHex())
	}
	if got, want := len(rep.CaseNames), 4; got != want {
		t.Fatalf("E8: evaluated %d cases, want %d", got, want)
	}
	// A pass is only meaningful if the compositions genuinely differed.
	if slices.Max(rep.BatchSizes) == slices.Min(rep.BatchSizes) {
		t.Fatalf("E8: batch sizes did not vary: %v", rep.BatchSizes)
	}
	t.Logf("E8 pass: probe %s, batch sizes %v, digest %s",
		rep.ProbeEventID.String()[:12], rep.BatchSizes, rep.DigestHex()[0][:16])
}

// TestE8DetectsBatchDependence is the negative control for E8 itself. A detector
// standardising against the batch, equation (1), must be caught. Without this, a
// passing E8 would be consistent with a check that cannot fail.
func TestE8DetectsBatchDependence(t *testing.T) {
	cs := cases()
	var shared []*event.Event
	factory := func() (detector.Detector, error) {
		return &fixtureBatchRelative{batch: &shared, field: fDstComputer}, nil
	}

	// Hand each case its own batch, which is exactly what equation (1) reads.
	// CheckBatchIndependence replays cases in order, so set the batch per case by
	// wrapping the factory.
	idx := 0
	factoryPerCase := func() (detector.Detector, error) {
		shared = cs[idx].Batch
		idx++
		return factory()
	}

	_, err := detector.CheckBatchIndependence(context.Background(), factoryPerCase, cs)
	if !errors.Is(err, detector.ErrBatchIndependence) {
		t.Fatalf("expected ErrBatchIndependence for a batch-relative detector, got %v", err)
	}
	t.Logf("negative control held: batch-relative scoring was detected (%v)", err)
}

// TestE8RejectsDifferingHistory confirms the check validates its own premises. If the
// events preceding the probe differed, a change in the probe's score would be
// legitimate history rather than batch dependence, and a pass would prove nothing.
func TestE8RejectsDifferingHistory(t *testing.T) {
	h, p := history(), probe()
	good := detector.BatchIndependenceCase{
		Name: "a", Batch: append(slices.Clone(h), p), ProbeIndex: len(h),
	}
	tampered := slices.Clone(h)
	tampered[0] = mkEvent(entityUnderTest, 1*event.Hour, "Kerberos", "C999", 1) // differs
	bad := detector.BatchIndependenceCase{
		Name: "b", Batch: append(tampered, p), ProbeIndex: len(h),
	}

	_, err := detector.CheckBatchIndependence(context.Background(), newFixtureNovelty,
		[]detector.BatchIndependenceCase{good, bad})
	if !errors.Is(err, detector.ErrPrefixMismatch) {
		t.Fatalf("expected ErrPrefixMismatch, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// R4
// ---------------------------------------------------------------------------

// TestR4DeterministicRepeat is the R4 half of the property: identical event and
// identical state yield identical output, reproducibly. Repeated enough times to
// surface map-ordered float accumulation, whose effect appears in the low bits and
// varies run to run.
func TestR4DeterministicRepeat(t *testing.T) {
	if err := detector.AssertDeterministicRepeat(
		context.Background(), newFixtureNovelty, history(), probe(), 32); err != nil {
		t.Fatalf("R4 failed: %v", err)
	}
}

// TestR4ConcurrentScoringIsDeterministic covers the third Go trap: concurrency is
// permitted, but the reduction must be put back into canonical order before
// combination. Scores are computed in parallel, re-sorted, and required to match the
// sequential result byte for byte.
func TestR4ConcurrentScoringIsDeterministic(t *testing.T) {
	ctx := context.Background()

	d, err := newFixtureNovelty()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range history() {
		_, obs, err := d.Score(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		if err := obs.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}

	events := append(filler(64, 10*event.Hour), probe())

	sequential := make(detector.Verdicts, 0, len(events))
	for _, e := range events {
		vs, _, err := d.Score(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		sequential = append(sequential, vs...)
	}
	sequential.SortCanonical()

	// Concurrent scoring: results collected out of order, then re-sorted.
	var (
		mu       sync.Mutex
		parallel detector.Verdicts
		wg       sync.WaitGroup
	)
	for _, e := range events {
		wg.Add(1)
		go func(e *event.Event) {
			defer wg.Done()
			vs, _, err := d.Score(ctx, e)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			parallel = append(parallel, vs...)
			mu.Unlock()
		}(e)
	}
	wg.Wait()
	parallel.SortCanonical()

	if a, b := sequential.Digest(), parallel.Digest(); a != b {
		t.Fatalf("concurrent reduction was not canonicalised: %x != %x", a[:8], b[:8])
	}
}

// ---------------------------------------------------------------------------
// R3 and §6.2 cold start
// ---------------------------------------------------------------------------

// TestR3AbstentionIsNotAZeroScore requires that an unusable input abstains and
// carries no p-value at all. A neutral score would assert normality on no evidence.
func TestR3AbstentionIsNotAZeroScore(t *testing.T) {
	e := event.New(testSource, entityUnderTest, event.Hour, map[event.FieldPath]event.Value{
		fAuthType: event.UnusableValue("?"), // LANL's missing-value encoding
	}, 1)

	d, _ := newFixtureNovelty()
	vs, _, err := d.Score(context.Background(), &e)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("want 1 verdict, got %d", len(vs))
	}
	v := vs[0]
	if v.Status() != detector.StatusAbstainedUnusable {
		t.Fatalf("want abstained_unusable, got %s", v.Status())
	}
	if p, ok := v.PValue(); ok {
		t.Fatalf("abstained verdict must carry no p-value, got %v", p)
	}
	if len(vs.Evaluated()) != 0 {
		t.Fatal("abstained verdict must not count toward J of §10.2")
	}
}

// TestColdStartReturnsExactlyOne covers §6.2: with no history N = 0 and K = 0, so
// equation (4) places unit mass on the reserved category ∅ and equation (5) returns
// P = 1 exactly. A first observation is never anomalous, and this is the correct
// verdict rather than a special case.
func TestColdStartReturnsExactlyOne(t *testing.T) {
	d, _ := newFixtureNovelty()
	vs, _, err := d.Score(context.Background(), probe())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vs {
		p, ok := v.PValue()
		if !ok {
			t.Fatalf("field %v: expected an evaluated verdict", v.Target().Fields)
		}
		if p != 1.0 {
			t.Fatalf("field %v: cold start must give exactly P = 1, got %v (bits %#x)",
				v.Target().Fields, p, math.Float64bits(p))
		}
	}
}

// TestVerdictConstructorsRejectInvalidInput pins the R3 guarantees at the type
// boundary: no p-value outside (0,1], and no abstained status carrying a score.
func TestVerdictConstructorsRejectInvalidInput(t *testing.T) {
	tgt := detector.Target{Entity: entityUnderTest}
	ev := detector.NewEvidence([]int{4}, map[string]float64{"N": 1}, nil)

	for _, p := range []float64{0, -0.1, 1.5, math.NaN()} {
		if _, err := detector.NewEvaluated("d", tgt, p, ev); !errors.Is(err, detector.ErrPValueRange) {
			t.Fatalf("p = %v: want ErrPValueRange, got %v", p, err)
		}
	}
	if _, err := detector.NewEvaluated("d", tgt, 0.5, detector.Evidence{}); !errors.Is(err, detector.ErrNoEvidence) {
		t.Fatalf("want ErrNoEvidence for empty evidence, got %v", err)
	}
	if _, err := detector.NewAbstained("d", tgt, detector.StatusEvaluated, "", ev); !errors.Is(err, detector.ErrNotAbstained) {
		t.Fatalf("want ErrNotAbstained, got %v", err)
	}
	if _, err := detector.NewAbstained("d", tgt, detector.StatusUnknown, "", ev); !errors.Is(err, detector.ErrStatusInvalid) {
		t.Fatalf("want ErrStatusInvalid for the zero status, got %v", err)
	}
}
