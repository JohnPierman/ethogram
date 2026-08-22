package burst

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/JohnPierman/ethogram/domain/calibration"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
)

// The gap dispersion, and the measurement that put it there.
//
// The exponential null is calibrated on its own process — burst_test.go's
// TestTheNullIsCalibratedOnItsOwnProcess shows it conservative at every level on synthetic Poisson
// streams — and on 170,073 real scored LANL events it put 36.7% below 1e−12. That is the same
// defect that disqualified `volume` at 24.7% and the partitioned `cooccurrence` arm at 99.0%.
//
// The cause is that real authentication traffic is clustered rather than Poisson: a machine account
// authenticates several times in a few seconds and then not for an hour. Short spans are ordinary,
// and an exponential null finds the ordinary astronomically improbable. These tests pin the fix and,
// just as importantly, the counterfactual that makes the fix evidence rather than decoration.

const undecayed = novelty.HalfLife(1 << 62)

// clustered drives a two-component gap mixture through a state, evaluating after each arrival, and
// returns how many scans fell below each of two levels.
func clustered(seed uint64, clusters, perCluster int, within, between float64) (
	s *State, scans, belowMicro, belowPico int) {

	rng := rand.New(rand.NewPCG(seed, seed))
	s = &State{}
	at := event.Timestamp(0)
	for c := 0; c < clusters; c++ {
		for i := 0; i < perCluster; i++ {
			mean := within
			if i == 0 {
				mean = between
			}
			at += event.Timestamp(math.Ceil(rng.ExpFloat64()*mean)) * event.Second
			s.Observe(at, undecayed)
			scan, abstain := Evaluate(s)
			if abstain != AbstainNone {
				continue
			}
			scans++
			if scan.LogP <= math.Log(1e-6) {
				belowMicro++
			}
			if scan.LogP <= math.Log(1e-12) {
				belowPico++
			}
		}
	}
	return s, scans, belowMicro, belowPico
}

// TestClusteredTrafficDoesNotPileInTheDeepTail holds the fitted null to the bar the other arms
// were judged by. A share below 1e−12 that no correct null produces is a defect whatever else the
// arm can do, and it is the bar `volume` failed at 24.7%.
func TestClusteredTrafficDoesNotPileInTheDeepTail(t *testing.T) {
	s, scans, micro, pico := clustered(31, 4000, 6, 3, 3600)
	shape, scale, ok := s.Shape()
	t.Logf("fitted kappa %.4f, theta %.1f s (ok=%v); %d scans, %.3f%% below 1e-6, "+
		"%.4f%% below 1e-12", shape, scale, ok, scans,
		100*float64(micro)/float64(scans), 100*float64(pico)/float64(scans))

	if !ok {
		t.Fatal("no shape was fitted to a clustered stream")
	}
	if shape >= MaxShape {
		t.Errorf("kappa came back at the cap (%g) on deliberately clustered gaps: the "+
			"widening is not happening and the null is still exponential", shape)
	}
	if share := float64(pico) / float64(scans); share > 0.001 {
		t.Errorf("%.3f%% of scans on clustered background fall below 1e-12. That is the "+
			"defect that disqualified `volume` at 24.7%% and the partitioned "+
			"`cooccurrence` arm at 99.0%%, and no correct null produces it", 100*share)
	}
	if share := float64(micro) / float64(scans); share > 0.03 {
		t.Errorf("%.3f%% of scans on clustered background fall below 1e-6, which is near "+
			"where a realised alert cut lands: the arm would spend its whole budget on "+
			"ordinary clustered traffic", 100*share)
	}
}

// TestTheExponentialNullWouldFailThisSameStream is the counterfactual, and without it the test
// above proves nothing: a null could pass it by being uniformly inert. The identical windows,
// scored as the exponential null this arm started with would have scored them, must pile up.
func TestTheExponentialNullWouldFailThisSameStream(t *testing.T) {
	rng := rand.New(rand.NewPCG(31, 31))
	s := &State{}
	at := event.Timestamp(0)
	fitted, exponential, scans := 0, 0, 0
	for c := 0; c < 2000; c++ {
		for i := 0; i < 6; i++ {
			mean := 3.0
			if i == 0 {
				mean = 3600
			}
			at += event.Timestamp(math.Ceil(rng.ExpFloat64()*mean)) * event.Second
			s.Observe(at, undecayed)
			scan, abstain := Evaluate(s)
			if abstain != AbstainNone {
				continue
			}
			scans++
			if scan.LogP <= math.Log(1e-12) {
				fitted++
			}
			// The same window under kappa = 1 and theta = the mean gap, which is exactly the
			// Poisson null: Gamma(k-1, mean).
			meanGap := s.Gaps / s.Count
			poisson := calibration.SidakLog(
				calibration.GammaLowerTailLog(float64(scan.Window-1),
					scan.SpanSeconds/meanGap), scan.Windows)
			if poisson <= math.Log(1e-12) {
				exponential++
			}
		}
	}
	t.Logf("%d scans: fitted null %.4f%% below 1e-12, exponential null %.2f%%", scans,
		100*float64(fitted)/float64(scans), 100*float64(exponential)/float64(scans))
	if exponential <= fitted {
		t.Errorf("the exponential null put %d scans below 1e-12 against the fitted null's "+
			"%d. The widening is meant to be the difference between them, so if the "+
			"exponential null is no worse on this stream then the stream is not the shape "+
			"the real measurement found and the comparison establishes nothing",
			exponential, fitted)
	}
}

// TestTheWideningCostsPowerAndTheCostDependsOnTheVictim measures what the widening costs rather
// than asserting a threshold it cannot meet, because the cost is real and it is not uniform.
//
// The plant is twelve arrivals ninety seconds apart. Against a genuinely quiet account — one whose
// own gaps average twenty minutes with little clustering — that is a rhythm the account never
// produces and the arm finds it easily. Against an account that already authenticates in pairs a
// minute apart, ninety-second gaps are LONGER than its short ones, so a run of them is close to
// what it does anyway and the same plant is far less surprising.
//
// That is the per-entity null behaving as designed, not a defect: the reference set for an event is
// the account that produced it, and the plant genuinely is unremarkable for a bursty account. But
// it bounds what this arm can deliver, so the numbers are recorded here instead of a bar being
// asserted that the mechanism cannot clear.
//
// The first version of this test asserted 1e-4 for both, which was a guess rather than a
// prediction, and the clustered account measured 6e-4.
func TestTheWideningCostsPowerAndTheCostDependsOnTheVictim(t *testing.T) {
	// A quiet account: single arrivals, twenty minutes apart on average.
	quietBase := func(s *State, rng *rand.Rand, at event.Timestamp) event.Timestamp {
		for i := 0; i < 800; i++ {
			at += event.Timestamp(math.Ceil(rng.ExpFloat64()*1200)) * event.Second
			s.Observe(at, undecayed)
		}
		return at
	}
	// A clustered account: pairs a minute apart, an hour between pairs.
	clusteredBase := func(s *State, rng *rand.Rand, at event.Timestamp) event.Timestamp {
		for i := 0; i < 400; i++ {
			at += event.Timestamp(math.Ceil(rng.ExpFloat64()*3600)) * event.Second
			s.Observe(at, undecayed)
			at += event.Timestamp(math.Ceil(rng.ExpFloat64()*60)) * event.Second
			s.Observe(at, undecayed)
		}
		return at
	}

	type outcome struct {
		name            string
		shape           float64
		baseline, plant float64
	}
	var results []outcome

	for _, victim := range []struct {
		name string
		base func(*State, *rand.Rand, event.Timestamp) event.Timestamp
	}{
		{"quiet", quietBase},
		{"clustered", clusteredBase},
	} {
		rng := rand.New(rand.NewPCG(41, 41))
		s := &State{}
		at := victim.base(s, rng, 0)
		baseline, abstain := Evaluate(s)
		if abstain != AbstainNone {
			t.Fatalf("%s: abstained on an established account: %v", victim.name, abstain)
		}
		// The plant, exactly as cmd/inject writes it: twelve arrivals ninety seconds apart.
		var last Scan
		for i := 0; i < 12; i++ {
			at += 90 * event.Second
			s.Observe(at, undecayed)
			last, abstain = Evaluate(s)
			if abstain != AbstainNone {
				t.Fatalf("%s: abstained mid-burst: %v", victim.name, abstain)
			}
		}
		results = append(results, outcome{victim.name, last.Shape, baseline.LogP, last.LogP})

		if !(last.LogP < baseline.LogP) {
			t.Errorf("%s: the burst scored ln p %g against a baseline of %g; whatever the "+
				"power, a burst must be more surprising than the stream it interrupts",
				victim.name, last.LogP, baseline.LogP)
		}
	}

	for _, r := range results {
		t.Logf("%-10s kappa %.3f: baseline ln p %7.2f, twelve-event 90 s burst ln p %8.2f "+
			"(p = %.2e)", r.name, r.shape, r.baseline, r.plant, math.Exp(r.plant))
	}

	quiet, clus := results[0], results[1]
	// The quiet account must still clear a level an alert budget can reach. The realised cut
	// at a thousand alerts a day on this corpus is near 1e-5, so 1e-6 is the working bar.
	if quiet.plant > math.Log(1e-6) {
		t.Errorf("on a quiet account the plant scored ln p %g, above 1e-6: the widening has "+
			"cost the arm its purpose rather than merely some of its power", quiet.plant)
	}
	// And the cost must run in the stated direction: the clustered victim is the harder one.
	if !(clus.plant > quiet.plant) {
		t.Errorf("the clustered victim scored ln p %g and the quiet one %g. The stated "+
			"limitation is that a bursty account hides the plant, so if the clustered "+
			"victim is the EASIER of the two then the explanation in this comment is wrong",
			clus.plant, quiet.plant)
	}
	if clus.shape >= quiet.shape {
		t.Errorf("the clustered account fitted kappa %g against the quiet account's %g; the "+
			"clustered one must be the more over-dispersed or these two fixtures are not "+
			"the shapes they are named for", clus.shape, quiet.shape)
	}
}

// TestTheShapeIsNeverTighterThanExponential is the cap, and it is the direction that matters. A
// heartbeat has gaps more regular than exponential, so a measured kappa above one; honouring it
// would make the null narrower than Poisson and one early beat wildly surprising.
func TestTheShapeIsNeverTighterThanExponential(t *testing.T) {
	beat := &State{}
	at := event.Timestamp(0)
	for i := 0; i < 200; i++ {
		at += 60 * event.Second
		beat.Observe(at, undecayed)
	}
	shape, scale, ok := beat.Shape()
	if !ok {
		t.Fatal("no shape fitted to a heartbeat")
	}
	if shape > MaxShape {
		t.Errorf("kappa is %g, above the %g cap: the null is now tighter than exponential "+
			"and an ordinary early beat becomes evidence", shape, MaxShape)
	}
	if got, want := shape*scale, 60.0; math.Abs(got-want) > 1e-9*want {
		t.Errorf("kappa*theta is %g, want the mean gap %g: the identity must survive the cap",
			got, want)
	}

	at += 50 * event.Second
	beat.Observe(at, undecayed)
	scan, abstain := Evaluate(beat)
	if abstain != AbstainNone {
		t.Fatalf("abstained: %v", abstain)
	}
	t.Logf("a 50 s gap in a 60 s heartbeat: ln p %.2f (kappa %.2f)", scan.LogP, scan.Shape)
	if scan.LogP < math.Log(1e-6) {
		t.Errorf("ln p %g for one early beat in a heartbeat; capping kappa at one is what "+
			"stops this being certainty", scan.LogP)
	}
}

// TestTheShapeIdentityHolds pins kappa*theta = mean at both clamps and in between. The scale is
// derived from that identity rather than from variance/mean precisely so it survives a clamp, and
// a reader is entitled to know the fitted null keeps the entity's own mean gap.
func TestTheShapeIdentityHolds(t *testing.T) {
	rng := rand.New(rand.NewPCG(77, 77))
	for _, spread := range []float64{0, 1, 30, 300, 100000} {
		s := &State{}
		at := event.Timestamp(0)
		for i := 0; i < 300; i++ {
			step := 600.0
			if spread > 0 {
				step = math.Max(1, 600+rng.NormFloat64()*spread)
			}
			at += event.Timestamp(math.Ceil(step)) * event.Second
			s.Observe(at, undecayed)
		}
		shape, scale, ok := s.Shape()
		if !ok {
			t.Errorf("spread %g: no shape fitted", spread)
			continue
		}
		mean := s.Gaps / s.Count
		if math.Abs(shape*scale-mean) > 1e-9*mean {
			t.Errorf("spread %g: kappa*theta = %g, want the mean gap %g",
				spread, shape*scale, mean)
		}
		if shape < MinShape || shape > MaxShape {
			t.Errorf("spread %g: kappa %g is outside [%g, %g]",
				spread, shape, MinShape, MaxShape)
		}
	}
}

// TestNoShapeIsAnAbstentionNotAFallback is the lesson the volume arm learned the hard way: an
// entity whose dispersion cannot be measured must abstain, because the only null left is the
// narrowest the arm has and falling back to it is what produced the pile-up.
func TestNoShapeIsAnAbstentionNotAFallback(t *testing.T) {
	few := &State{}
	at := event.Timestamp(0)
	for i := 0; i < MinGaps-1; i++ {
		at += 600 * event.Second
		few.Observe(at, undecayed)
	}
	if _, abstain := Evaluate(few); abstain != AbstainTooFewGaps {
		t.Errorf("with %d gaps the arm gave %v, want the too-few-gaps abstention",
			few.Observed, abstain)
	}
	// Every abstention must carry a stated cause, since that is what an evidence card shows in
	// place of a number (R3).
	for _, a := range []Abstention{AbstainTooFewGaps, AbstainTooFewArrivals, AbstainNoRate,
		AbstainNoShape} {
		if a.String() == "" {
			t.Errorf("abstention %d has no stated cause", a)
		}
	}
	if AbstainNone.String() != "" {
		t.Error("AbstainNone carries a cause; it is not an abstention")
	}
}

// TestTheSecondMomentSurvivesTheDecay checks that GapsSquared is discounted on the same schedule
// as Gaps and Count. If it were not, the ratio the shape is formed from would drift with the
// half-life and an entity's null would slowly narrow or widen for no reason in the data.
func TestTheSecondMomentSurvivesTheDecay(t *testing.T) {
	// Constant gaps, so the truth is kappa at the cap and theta the gap, whatever the decay.
	for _, halfLife := range []novelty.HalfLife{
		novelty.HalfLife(600 * event.Second),
		novelty.HalfLife(7 * 24 * 3600 * event.Second),
		undecayed,
	} {
		s := &State{}
		at := event.Timestamp(0)
		for i := 0; i < 300; i++ {
			at += 600 * event.Second
			s.Observe(at, halfLife)
		}
		shape, scale, ok := s.Shape()
		if !ok {
			t.Errorf("half-life %d: no shape fitted", halfLife)
			continue
		}
		if math.Abs(scale-600) > 1e-6*600 {
			t.Errorf("half-life %d: theta %g, want 600. The second moment must decay on the "+
				"same schedule as the first, or the shape drifts with the half-life rather "+
				"than with the data", halfLife, scale)
		}
		if shape != MaxShape {
			t.Errorf("half-life %d: kappa %g on constant gaps, want the cap %g",
				halfLife, shape, MaxShape)
		}
	}
}

// TestEligibleAgreesWithEvaluate is the consistency the run record depends on. The report counts
// eligible entities so a reader can tell "this arm found nothing" from "this arm was never able to
// speak", and an entity counted eligible while Evaluate abstains would inflate the coverage the
// run claims.
func TestEligibleAgreesWithEvaluate(t *testing.T) {
	rng := rand.New(rand.NewPCG(63, 63))
	// A spread of shapes: cold, tied-only, regular, clustered, and one gap short of the gate.
	states := map[string]*State{}

	states["cold"] = &State{}

	tied := &State{}
	for i := 0; i < 100; i++ {
		tied.Observe(1000*event.Second, undecayed)
	}
	states["tied only"] = tied

	oneShort := &State{}
	at := event.Timestamp(0)
	for i := 0; i < MinGaps-1; i++ {
		at += 600 * event.Second
		oneShort.Observe(at, undecayed)
	}
	states["one gap short"] = oneShort

	regular := &State{}
	at = 0
	for i := 0; i < 200; i++ {
		at += 600 * event.Second
		regular.Observe(at, undecayed)
	}
	states["regular"] = regular

	clumpy, _, _, _ := clustered(63, 300, 5, 2, 1800)
	states["clustered"] = clumpy

	poisson := &State{}
	at = 0
	for i := 0; i < 300; i++ {
		at += event.Timestamp(math.Ceil(rng.ExpFloat64()*300)) * event.Second
		poisson.Observe(at, undecayed)
	}
	states["poisson"] = poisson

	for name, st := range states {
		_, abstain := Evaluate(st)
		speaks := abstain == AbstainNone
		if st.Eligible() != speaks {
			t.Errorf("%s: Eligible() is %v but Evaluate %s (%v). The report would then "+
				"claim coverage the arm does not have", name, st.Eligible(),
				map[bool]string{true: "speaks", false: "abstains"}[speaks], abstain)
		}
	}
	// And a nil state is not eligible, since a nil report row must not be counted as coverage.
	var none *State
	if none.Eligible() {
		t.Error("a nil state reported itself eligible")
	}
}
