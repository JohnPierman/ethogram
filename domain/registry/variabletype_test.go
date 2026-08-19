package registry_test

import (
	"fmt"
	"testing"

	"github.com/JohnPierman/ethogram/domain/registry"
)

// observe folds a value repeatedly into fresh statistics, at monotone event times.
func observe(path string, values []string, repeats int) *registry.FieldStats {
	stats := registry.NewFieldStats(path)
	at := int64(1)
	for range repeats {
		for _, v := range values {
			stats.Observe(v, at, 10_000)
			at++
		}
	}
	return stats
}

// ---------------------------------------------------------------------------
// The discrete / continuous split
// ---------------------------------------------------------------------------

// TestDiscreteInference: a numeric field whose values recur is a bounded vocabulary,
// which is what equation (4) counts. LANL's logon-type codes have this shape.
func TestDiscreteInference(t *testing.T) {
	stats := observe("auth.logon_type_code", []string{"2", "3", "4", "5", "7", "8", "9", "10", "11"}, 60)

	got := registry.Infer(*stats, registry.DefaultPolicy())
	if got != registry.KindDiscrete {
		t.Fatalf("nine codes over 540 events: got %s, want discrete", got)
	}
	if !got.IsScoreable() {
		t.Error("a bounded numeric vocabulary is what equation (4) counts; it must be scoreable")
	}
	if !got.IsEligible() {
		t.Error("a bounded numeric vocabulary makes a bounded set of graph nodes; it must be eligible")
	}
	if got.UsesNumericMarginal() {
		t.Error("a discrete field is counted by equations (4) and (5), not tailed against a sketch")
	}
}

// TestDiscreteAdmitsFractionalVocabulary: discreteness is a property of the support,
// not of the spelling. A field taking three fractional values is a counted vocabulary,
// and banding it would collapse 1.0 and 1.5 into one label for no reason.
func TestDiscreteAdmitsFractionalVocabulary(t *testing.T) {
	stats := observe("risk.weight", []string{"0.5", "1.0", "1.5"}, 200)

	if got := registry.Infer(*stats, registry.DefaultPolicy()); got != registry.KindDiscrete {
		t.Fatalf("three recurring fractional values: got %s, want discrete", got)
	}
}

// TestContinuousInference: a numeric field taking a fresh value on nearly every event
// has an open support. Counting it would make every observation novel, so it is banded
// for the per-entity detectors and tailed against a sketch at population scope.
func TestContinuousInference(t *testing.T) {
	stats := registry.NewFieldStats("flows.byte_count")
	for i := range 300 {
		stats.Observe(fmt.Sprintf("%d", 92+i*7), int64(i), 10_000)
	}

	got := registry.Infer(*stats, registry.DefaultPolicy())
	if got != registry.KindContinuous {
		t.Fatalf("300 distinct measurements over 300 events: got %s, want continuous", got)
	}
	if !got.UsesNumericMarginal() {
		t.Error("section 9 scores a continuous field against the quantile sketch")
	}
	if !got.IsScoreable() || !got.IsEligible() {
		t.Error("a continuous field takes part through its band, so it is admitted, not withheld")
	}
}

// TestDiscreteContinuousBoundary pins the threshold rather than leaving it to be
// rediscovered: the split is the average multiplicity of a value, and the policy field
// is where it is set.
func TestDiscreteContinuousBoundary(t *testing.T) {
	policy := registry.DefaultPolicy()

	// 20 distinct values over 1000 events: each seen 50 times, comfortably counted.
	values := make([]string, 20)
	for i := range values {
		values[i] = fmt.Sprintf("%d", i)
	}
	if got := registry.Infer(*observe("a", values, 50), policy); got != registry.KindDiscrete {
		t.Errorf("multiplicity 50: got %s, want discrete", got)
	}

	// 400 distinct values over 800 events: each seen twice, too thin to count.
	values = make([]string, 400)
	for i := range values {
		values[i] = fmt.Sprintf("%d", i)
	}
	if got := registry.Infer(*observe("b", values, 2), policy); got != registry.KindContinuous {
		t.Errorf("multiplicity 2: got %s, want continuous", got)
	}
}

// TestContinuousKeepsTheIdentifierExemption. Section 5.1 exempts numeric fields from the
// identifier guard on the grounds that binning contains them, and the binning now
// exists. A purely numeric surrogate key is therefore still admitted as a measurement:
// its band is bounded, so it wastes a little capacity rather than dissolving the
// partition, and the alternative -- calling a high-precision measurement an identifier --
// would silently discard real signal.
func TestContinuousKeepsTheIdentifierExemption(t *testing.T) {
	stats := registry.NewFieldStats("event.sequence")
	for i := range 1000 {
		stats.Observe(fmt.Sprintf("%d", 1_000_000+i), int64(i), 10_000)
	}

	if got := registry.Infer(*stats, registry.DefaultPolicy()); got != registry.KindContinuous {
		t.Fatalf("an auto-incrementing key is a measurement by the section 5.1 exemption, got %s", got)
	}
}

// TestOpaqueIdentifierStillCaught: the exemption is for numbers only. A GUID field must
// still be caught by the guard, or it saturates every novelty detector.
func TestOpaqueIdentifierStillCaught(t *testing.T) {
	stats := registry.NewFieldStats("event.guid")
	for i := range 1000 {
		stats.Observe(fmt.Sprintf("f47ac10b-58cc-4372-a567-%012d", i), int64(i), 10_000)
	}

	if got := registry.Infer(*stats, registry.DefaultPolicy()); got != registry.KindIdentifier {
		t.Fatalf("an opaque high-cardinality token is an identifier, got %s", got)
	}
}

// ---------------------------------------------------------------------------
// Projection to a scoring token
// ---------------------------------------------------------------------------

// TestTokenIsIdentityForCountedKinds: nothing is cast, rewritten, or normalised. The
// value's text is its identity throughout the framework, and "007" must not become "7".
func TestTokenIsIdentityForCountedKinds(t *testing.T) {
	for _, kind := range []registry.FieldKind{
		registry.KindCategorical, registry.KindBoolean, registry.KindDiscrete,
	} {
		for _, text := range []string{"007", "Success", "1.0", "", "?"} {
			got, ok := kind.Token(text)
			if !ok {
				t.Errorf("%s.Token(%q): declined, want the text unchanged", kind, text)
				continue
			}
			if got != text {
				t.Errorf("%s.Token(%q) = %q, want the text unchanged", kind, text, got)
			}
		}
	}
}

// TestTokenBandsContinuous: the projection, and the only place a continuous value is
// coarsened for a counted-vocabulary detector.
func TestTokenBandsContinuous(t *testing.T) {
	got, ok := registry.KindContinuous.Token("1234")
	if !ok {
		t.Fatal("a finite measurement must project")
	}
	if want := registry.Band(1234); got != want {
		t.Fatalf("Token = %q, want %q", got, want)
	}
}

// TestTokenDeclinesUnmeasurableContinuous: a sentinel surviving the NumericFraction
// residue must abstain, never be counted as a token of its own -- that would make the
// sentinel a value in the vocabulary and let its rate masquerade as novelty (R3).
func TestTokenDeclinesUnmeasurableContinuous(t *testing.T) {
	for _, text := range []string{"unknown", "?", "NaN", "", "Inf"} {
		if got, ok := registry.KindContinuous.Token(text); ok {
			t.Errorf("Token(%q) = %q, want declined", text, got)
		}
	}
}

// TestTokenDeclinesForKindsThatCarryNoVocabulary keeps the projection honest about the
// kinds that contribute nothing: asking for a token must fail rather than return one.
func TestTokenDeclinesForKindsThatCarryNoVocabulary(t *testing.T) {
	for _, kind := range []registry.FieldKind{
		registry.KindUnknown, registry.KindIdentifier, registry.KindExcluded,
	} {
		if got, ok := kind.Token("anything"); ok {
			t.Errorf("%s.Token = %q, want declined", kind, got)
		}
	}
}

// TestBandedContinuousDoesNotSaturate is the property the whole projection exists for.
// Unbanded, 2,000 distinct measurements are 2,000 vocabulary items and 2,000 graph
// nodes, and section 5.1 records the three consequences.
func TestBandedContinuousDoesNotSaturate(t *testing.T) {
	tokens := map[string]struct{}{}
	for i := 1; i <= 2000; i++ {
		token, ok := registry.KindContinuous.Token(fmt.Sprintf("%d", i*3571))
		if !ok {
			t.Fatalf("%d did not project", i*3571)
		}
		tokens[token] = struct{}{}
	}
	if len(tokens) > 32 {
		t.Fatalf("2,000 measurements produced %d tokens; the vocabulary is not bounded", len(tokens))
	}
}

// ---------------------------------------------------------------------------
// Binary fields
// ---------------------------------------------------------------------------

// TestBooleanRequiresAMatchedPair closes a hole in the old token-set test, which asked
// only whether each value was *some* recognised boolean token. A field taking Success
// and Logoff satisfied that and was labelled boolean, though the two tokens come from
// different vocabularies and their pairing means nothing.
func TestBooleanRequiresAMatchedPair(t *testing.T) {
	mismatched := observe("auth.mixed", []string{"Success", "Logoff"}, 150)
	if got := registry.Infer(*mismatched, registry.DefaultPolicy()); got != registry.KindCategorical {
		t.Fatalf("tokens from two different pairs are not a binary field, got %s", got)
	}
}

// TestBooleanVocabulary: binary fields arrive spelled many ways, and the list is data so
// that a new source's spelling is a configuration change rather than a code change.
func TestBooleanVocabulary(t *testing.T) {
	for _, pair := range [][2]string{
		{"true", "false"}, {"TRUE", "FALSE"}, {"t", "f"},
		{"yes", "no"}, {"y", "n"},
		{"1", "0"},
		{"Success", "Fail"}, {"success", "failure"},
		{"on", "off"}, {"up", "down"},
		{"enabled", "disabled"},
		{"allow", "deny"}, {"permit", "deny"},
		{"accept", "reject"}, {"granted", "denied"},
		{"Logon", "Logoff"},
	} {
		stats := observe("flag", []string{pair[0], pair[1]}, 150)
		if got := registry.Infer(*stats, registry.DefaultPolicy()); got != registry.KindBoolean {
			t.Errorf("%q/%q: got %s, want boolean", pair[0], pair[1], got)
		}
		// Order must not matter: a source may emit either value first.
		reversed := observe("flag", []string{pair[1], pair[0]}, 150)
		if got := registry.Infer(*reversed, registry.DefaultPolicy()); got != registry.KindBoolean {
			t.Errorf("%q/%q reversed: got %s, want boolean", pair[1], pair[0], got)
		}
	}
}

// TestSingleValuedFieldIsNotBoolean: section 6.1 gives boolean as the case K = 2
// exactly, so a field observed with one value is a constant categorical field.
func TestSingleValuedFieldIsNotBoolean(t *testing.T) {
	got := registry.Infer(*observe("flag", []string{"true"}, 300), registry.DefaultPolicy())
	if got != registry.KindCategorical {
		t.Fatalf("one value is K = 1, not a binary field, got %s", got)
	}
}

// TestBooleanPrecedesDiscrete keeps the rule ordering of section 5.1: "0" and "1"
// satisfy both the binary and the numeric test, and the binary reading is the
// informative one.
func TestBooleanPrecedesDiscrete(t *testing.T) {
	got := registry.Infer(*observe("flag", []string{"0", "1"}, 150), registry.DefaultPolicy())
	if got != registry.KindBoolean {
		t.Fatalf("0/1 is a binary field, got %s", got)
	}
}

// ---------------------------------------------------------------------------
// Sentinels
// ---------------------------------------------------------------------------

// TestSentinelSpelledAsNaNIsNotAMeasurement: the inference site and the scoring site
// must agree on what a number is, or a field is classified into permanent abstention.
func TestSentinelSpelledAsNaNIsNotAMeasurement(t *testing.T) {
	stats := registry.NewFieldStats("flows.duration")
	for i := range 300 {
		v := "NaN"
		if i%3 != 0 {
			v = fmt.Sprintf("%d.%d", i, i)
		}
		stats.Observe(v, int64(i), 10_000)
	}

	// A third of the values are sentinels, well below NumericFraction, so the field is
	// not numeric at all -- and specifically is not classified numeric on the strength
	// of values Detector IV would refuse to score.
	if got := registry.Infer(*stats, registry.DefaultPolicy()); got.UsesNumericMarginal() {
		t.Fatalf("a field one third sentinels must not be scored as a measurement, got %s", got)
	}
}
