package timing

import (
	"math"

	"github.com/JohnPierman/ethogram/domain/event"
)

// PhaseOfTimestamp maps event time to an angle on the daily circle, §7.2:
// φ(t) = 2π · (t mod 24h) / 24h. Periodic by construction: cos and sin do not know
// where the day begins, which is why the wraparound defect of §7.1 is absent rather
// than mitigated.
func PhaseOfTimestamp(t event.Timestamp) float64 {
	tod := t % event.Day
	if tod < 0 {
		tod += event.Day
	}
	return 2 * math.Pi * float64(tod) / float64(event.Day)
}

// Moments is the fixed-size circular state of equation (6): decayed cosine and sine
// moments C_h, S_h for harmonics h = 1…H, and the decayed observation weight W.
//
// State is 2H + 1 numbers per entity regardless of how many events have been observed,
// which is the point of the representation: the paper's worked example is twenty-three
// stored numbers against the 336 required by 168 independent two-parameter cells.
type Moments struct {
	C []float64 // C[h-1] holds C_h
	S []float64 // S[h-1] holds S_h
	W float64
}

// NewMoments returns zeroed state for H harmonics. All moments zero is the cold-start
// state: equation (7) then reduces to the uniform density on the circle and equation
// (9) returns P = 1 for every time (§7.5).
func NewMoments(H int) *Moments {
	return &Moments{C: make([]float64, H), S: make([]float64, H)}
}

// H returns the harmonic truncation order.
func (m *Moments) H() int { return len(m.C) }

// Size returns the stored-number count, 2H + 1, which table T5 reports as the
// fixed-size-state measurement.
func (m *Moments) Size() int { return 2*len(m.C) + 1 }

// Observe folds one event at angle phi into the moments with discount delta, per
// equation (6):
//
//	C_h ← δ·C_h + cos(hφ),  S_h ← δ·S_h + sin(hφ),  W ← δ·W + 1
//
// delta is 2^(−Δt/T½) computed from the entity's previous observation to this one; the
// caller supplies it so that this package never consults any clock (R4). Called from
// the Observe half of §5.2 only, strictly after the event has been scored.
func (m *Moments) Observe(phi, delta float64) {
	for h := 1; h <= len(m.C); h++ {
		hphi := float64(h) * phi
		m.C[h-1] = delta*m.C[h-1] + math.Cos(hphi)
		m.S[h-1] = delta*m.S[h-1] + math.Sin(hphi)
	}
	m.W = delta*m.W + 1
}

// Blend returns the convex combination of entity and parent moments of §7.5:
// C_h^(blend) = w·C_h + (1−w)·C_h^(parent) with w = W/(W+τ) for shrinkage strength τ.
//
// Because (6) is a linear representation this is exact, and a convex combination of
// circular densities is a circular density, so the blend preserves the validity of (7).
// The blended weight is the same convex combination of W, keeping the effective sample
// size interpretation of §7.5 intact.
//
// Both inputs must share H; the receiver is not modified.
func (m *Moments) Blend(parent *Moments, tau float64) *Moments {
	w := 1.0
	if tau > 0 {
		w = m.W / (m.W + tau)
	}
	out := NewMoments(m.H())
	for i := range m.C {
		out.C[i] = w*m.C[i] + (1-w)*parent.C[i]
		out.S[i] = w*m.S[i] + (1-w)*parent.S[i]
	}
	out.W = w*m.W + (1-w)*parent.W
	return out
}
