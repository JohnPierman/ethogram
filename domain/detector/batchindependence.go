package detector

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/JohnPierman/ethogram/domain/event"
)

// This file implements the check behind evaluation hypothesis E8 (§12.3):
//
//	"Batch-independence holds. Identical events replayed in differing batch
//	 compositions must yield identical scores, a direct test of R1 and of (1)'s
//	 removal."
//
// It adjudicates the repair of the §3.2 defect. Under batch-relative
// standardisation, equation (1) makes a score a function of μ̂_B and σ̂_B, so
// co-resident traffic changes it; equation (2) shows the consequence exactly, a
// campaign event's own z-score being √((1−p)/p) and therefore strictly decreasing in
// the campaign's share of the batch. A framework scoring against persisted history
// must be wholly insensitive to batch composition, and this check is what
// establishes that it is.

// Errors returned by the E8 check.
var (
	ErrNoCases           = errors.New("e8: at least two cases are required")
	ErrProbeIndexRange   = errors.New("e8: probe index out of range")
	ErrProbeMismatch     = errors.New("e8: probe events differ across cases")
	ErrPrefixMismatch    = errors.New("e8: pre-probe prefixes differ across cases")
	ErrBatchIndependence = errors.New("e8: scores differ across batch compositions")
)

// DetectorFactory returns a detector backed by freshly initialised, empty state.
//
// Each E8 case is replayed against its own state, so that any leakage between cases
// shows up as a difference rather than being masked by shared history.
type DetectorFactory func() (Detector, error)

// BatchIndependenceCase is one batch composition in which the probe event is scored.
//
// Batch is the arrival order. ProbeIndex selects the event under test. Events before
// the probe are scored and committed, building the history the probe is judged
// against; events after it establish that co-resident traffic which the probe has
// not yet been compared with cannot influence the probe's score either. The latter
// is the direction that batch standardisation gets wrong, since μ̂_B is computed over
// the whole window including events that arrive after the one being scored.
type BatchIndependenceCase struct {
	Name       string
	Batch      []*event.Event
	ProbeIndex int
}

// probe returns the event under test.
func (c BatchIndependenceCase) probe() *event.Event { return c.Batch[c.ProbeIndex] }

// BatchIndependenceReport records the outcome of the check.
type BatchIndependenceReport struct {
	// DetectorID names the detector examined.
	DetectorID ID

	// CaseNames are the case names in evaluation order.
	CaseNames []string

	// BatchSizes gives each case's batch size, so the report shows that the
	// compositions really did differ, which is what makes a pass meaningful.
	BatchSizes []int

	// ProbeDigest is the SHA-256 of the probe's canonical verdicts, one per case.
	ProbeDigest [][32]byte

	// Identical reports whether every case produced byte-identical verdicts.
	Identical bool

	// ProbeEventID is the content identifier of the probe, identical by construction
	// across cases.
	ProbeEventID event.ID
}

// DigestHex returns the case digests as hex strings, for result JSON.
func (r BatchIndependenceReport) DigestHex() []string {
	out := make([]string, len(r.ProbeDigest))
	for i, d := range r.ProbeDigest {
		out[i] = hex.EncodeToString(d[:])
	}
	return out
}

// CheckBatchIndependence replays one probe event inside several batch compositions
// and reports whether its verdicts are byte-identical.
//
// The check validates its own premises before drawing a conclusion. If the probe
// events were not identical, or if the events preceding the probe differed between
// cases, then a difference in the probe's score would be legitimate history rather
// than batch dependence, and a pass would be meaningless. Those setups are rejected
// as errors instead of being reported as results.
func CheckBatchIndependence(ctx context.Context, newDetector DetectorFactory, cases []BatchIndependenceCase) (BatchIndependenceReport, error) {
	var rep BatchIndependenceReport

	if len(cases) < 2 {
		return rep, ErrNoCases
	}
	for i, c := range cases {
		if c.ProbeIndex < 0 || c.ProbeIndex >= len(c.Batch) {
			return rep, fmt.Errorf("%w: case %q index %d, batch size %d",
				ErrProbeIndexRange, c.Name, c.ProbeIndex, len(c.Batch))
		}
		if i == 0 {
			continue
		}
		// The probe must be the same event in every case.
		if cases[i].probe().ID() != cases[0].probe().ID() {
			return rep, fmt.Errorf("%w: case %q vs %q", ErrProbeMismatch, cases[0].Name, c.Name)
		}
		// The history the probe is judged against must be the same in every case.
		if err := samePrefix(cases[0], cases[i]); err != nil {
			return rep, err
		}
	}

	rep.DetectorID = ""
	rep.ProbeEventID = cases[0].probe().ID()

	for _, c := range cases {
		d, err := newDetector()
		if err != nil {
			return rep, fmt.Errorf("e8: case %q: new detector: %w", c.Name, err)
		}
		if rep.DetectorID == "" {
			rep.DetectorID = d.ID()
		}

		var probeVerdicts Verdicts
		for i, e := range c.Batch {
			verdicts, obs, scoreErr := d.Score(ctx, e)
			if scoreErr != nil {
				return rep, fmt.Errorf("e8: case %q: score index %d: %w", c.Name, i, scoreErr)
			}
			if i == c.ProbeIndex {
				verdicts.SortCanonical()
				probeVerdicts = verdicts
			}
			// Observe strictly after Score, per §5.2. Events after the probe are
			// still committed, so that a detector peeking at future state would be
			// caught rather than accommodated.
			if obs != nil {
				if commitErr := obs.Commit(ctx); commitErr != nil {
					return rep, fmt.Errorf("e8: case %q: commit index %d: %w", c.Name, i, commitErr)
				}
			}
		}

		rep.CaseNames = append(rep.CaseNames, c.Name)
		rep.BatchSizes = append(rep.BatchSizes, len(c.Batch))
		rep.ProbeDigest = append(rep.ProbeDigest, probeVerdicts.Digest())
	}

	rep.Identical = true
	for i := 1; i < len(rep.ProbeDigest); i++ {
		if rep.ProbeDigest[i] != rep.ProbeDigest[0] {
			rep.Identical = false
			break
		}
	}
	if !rep.Identical {
		return rep, fmt.Errorf("%w: detector %s, digests %v",
			ErrBatchIndependence, rep.DetectorID, rep.DigestHex())
	}
	return rep, nil
}

// samePrefix verifies that the events preceding the probe are identical, so that the
// probe is judged against identical history in both cases.
func samePrefix(a, b BatchIndependenceCase) error {
	if a.ProbeIndex != b.ProbeIndex {
		return fmt.Errorf("%w: case %q probe at %d, case %q at %d",
			ErrPrefixMismatch, a.Name, a.ProbeIndex, b.Name, b.ProbeIndex)
	}
	for i := range a.ProbeIndex {
		if a.Batch[i].ID() != b.Batch[i].ID() {
			return fmt.Errorf("%w: case %q vs %q at index %d",
				ErrPrefixMismatch, a.Name, b.Name, i)
		}
	}
	return nil
}

// AssertDeterministicRepeat scores the same event twice against the same state and
// requires byte-identical verdicts.
//
// This is the R4 half of the property, separate from E8's batch half: identical event
// and identical state must yield identical output, reproducibly. It catches
// map-ordered float accumulation, which perturbs the low bits from run to run, and
// any residual wall-clock read in the scoring path.
func AssertDeterministicRepeat(ctx context.Context, newDetector DetectorFactory, history []*event.Event, probe *event.Event, repeats int) error {
	if repeats < 2 {
		repeats = 2
	}
	var first []byte
	for r := range repeats {
		d, err := newDetector()
		if err != nil {
			return fmt.Errorf("r4: repeat %d: new detector: %w", r, err)
		}
		for i, e := range history {
			_, obs, scoreErr := d.Score(ctx, e)
			if scoreErr != nil {
				return fmt.Errorf("r4: repeat %d: history score %d: %w", r, i, scoreErr)
			}
			if obs != nil {
				if commitErr := obs.Commit(ctx); commitErr != nil {
					return fmt.Errorf("r4: repeat %d: history commit %d: %w", r, i, commitErr)
				}
			}
		}
		verdicts, _, probeErr := d.Score(ctx, probe)
		if probeErr != nil {
			return fmt.Errorf("r4: repeat %d: probe score: %w", r, probeErr)
		}
		verdicts.SortCanonical()
		got := verdicts.CanonicalBytes()
		if r == 0 {
			first = got
			continue
		}
		if !bytes.Equal(first, got) {
			return fmt.Errorf("r4: repeat %d differs from repeat 0 for detector %s", r, d.ID())
		}
	}
	return nil
}
