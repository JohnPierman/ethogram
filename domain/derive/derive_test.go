package derive_test

import (
	"fmt"
	"testing"

	"github.com/JohnPierman/ethogram/domain/derive"
	"github.com/JohnPierman/ethogram/domain/event"
)

const (
	src    = event.SourceID("vendor.telemetry")
	fAddr  = event.FieldPath("Properties.RemoteAddress")
	fHost  = event.FieldPath("Properties.RemoteHost")
	fVer   = event.FieldPath("Properties.AgentVersion")
	fNoise = event.FieldPath("Properties.Opaque")
)

// ---------------------------------------------------------------------------
// The decompositions themselves
// ---------------------------------------------------------------------------

func TestDottedQuadCoarsensToTheNetwork(t *testing.T) {
	cases := map[string]string{
		"10.1.2.3":        "10.1.2.0/24",
		"192.168.0.255":   "192.168.0.0/24",
		"0.0.0.0":         "0.0.0.0/24",
		"255.255.255.255": "255.255.255.0/24",
	}
	for value, want := range cases {
		got, ok := (derive.DottedQuad{}).Coarsen(value)
		if !ok || got != want {
			t.Errorf("Coarsen(%q) = %q, %v; want %q, true", value, got, ok, want)
		}
	}

	// Declining is the mechanism the inference counts, so the boundaries matter.
	for _, value := range []string{
		"10.1.2", "10.1.2.3.4", "10.1.2.256", "10.1.2.-1", "10.1.2.", "",
		"10.1.2.010", // padded octets are not how an address is written
		"a.b.c.d", "10.1.2.3/24",
	} {
		if got, ok := (derive.DottedQuad{}).Coarsen(value); ok {
			t.Errorf("Coarsen(%q) = %q, true; want it declined", value, got)
		}
	}
}

func TestFQDNCoarsensToTheParentDomain(t *testing.T) {
	got, ok := (derive.FQDN{}).Coarsen("web07.corp.example.com")
	if !ok || got != "corp.example.com" {
		t.Errorf("Coarsen = %q, %v; want corp.example.com", got, ok)
	}

	for _, value := range []string{
		"example.com", // two labels would coarsen to a public suffix, carrying nothing
		"localhost",
		"10.1.2.3", // an address is DottedQuad's, or two derived fields describe one structure
		"web..example.com",
		"web07.corp example.com",
		// A three-component numeric version is not a hostname. Without the alphabetic-TLD
		// rule it parses as one, collides with SemanticVersion on every version field,
		// and the ambiguity guard then silently withholds a derived field that should
		// have been produced.
		"10.0.3",
		"2.14.7",
	} {
		if got, ok := (derive.FQDN{}).Coarsen(value); ok {
			t.Errorf("Coarsen(%q) = %q, true; want it declined", value, got)
		}
	}
}

func TestSemanticVersionCoarsensToTheMajor(t *testing.T) {
	cases := map[string]string{
		"11.0.3": "11", "11.0": "11", "2.14.7-rc1": "2", "3.1.0+build.55": "3",
	}
	for value, want := range cases {
		got, ok := (derive.SemanticVersion{}).Coarsen(value)
		if !ok || got != want {
			t.Errorf("Coarsen(%q) = %q, %v; want %q", value, got, ok, want)
		}
	}
	for _, value := range []string{"11", "10.1.2.3", "v11.0.3", "x.y", ""} {
		if got, ok := (derive.SemanticVersion{}).Coarsen(value); ok {
			t.Errorf("Coarsen(%q) = %q, true; want it declined", value, got)
		}
	}
}

// ---------------------------------------------------------------------------
// Inference
// ---------------------------------------------------------------------------

func feed(in *derive.Inferrer, f event.FieldPath, values ...string) {
	for _, v := range values {
		in.Observe(src, f, v)
	}
}

func addresses(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("10.%d.%d.%d", i/250, i%250, (i*7)%256))
	}
	return out
}

func TestAFieldWhoseValuesParseGetsADerivedField(t *testing.T) {
	in := derive.NewInferrer(derive.DefaultPolicy())
	feed(in, fAddr, addresses(80)...)

	d := in.DecompositionFor(src, fAddr)
	if d == nil {
		t.Fatal("a field of 80 distinct addresses was not recognised as addresses")
	}
	if d.Name() != "net24" {
		t.Errorf("decomposition = %q, want net24", d.Name())
	}
	if got := derive.DerivedPath(fAddr, d); !derive.IsDerived(got) {
		t.Errorf("derived path %q is not recognisable as derived", got)
	}
}

func TestAFieldIsUndecidedUntilEnoughDistinctValues(t *testing.T) {
	// The same principle as the registry's KindUnknown: too little evidence is not
	// evidence of absence, and guessing early is how a derived field acquires a meaning
	// the data never supported.
	in := derive.NewInferrer(derive.DefaultPolicy())
	feed(in, fAddr, addresses(10)...)

	if d := in.DecompositionFor(src, fAddr); d != nil {
		t.Errorf("decided on %q after 10 distinct values; the policy requires 50", d.Name())
	}
}

func TestDistinctValuesAreCountedNotObservations(t *testing.T) {
	// A field with one address repeated endlessly and many distinct hostnames is a
	// hostname field. Counting observations would call it an address field.
	in := derive.NewInferrer(derive.DefaultPolicy())
	for range 5000 {
		in.Observe(src, fHost, "10.1.2.3")
	}
	for i := range 80 {
		in.Observe(src, fHost, fmt.Sprintf("web%02d.corp.example.com", i))
	}

	d := in.DecompositionFor(src, fHost)
	if d == nil {
		t.Fatal("field undecided")
	}
	if d.Name() != "parent" {
		t.Errorf("decomposition = %q, want parent: 80 distinct hostnames against a single "+
			"address repeated 5000 times is a hostname field", d.Name())
	}
}

func TestAnInconsistentFieldGetsNoDerivedField(t *testing.T) {
	// A derived field that does not describe its source's structure is worse than none:
	// it adds a detector's worth of noise, and §10.2 has measured what an uninformative
	// detector costs a combination.
	in := derive.NewInferrer(derive.DefaultPolicy())
	values := addresses(40)
	for i := range 40 {
		values = append(values, fmt.Sprintf("opaque-token-%d", i))
	}
	feed(in, fNoise, values...)

	if d := in.DecompositionFor(src, fNoise); d != nil {
		t.Errorf("a field only half of whose values parse was given decomposition %q; "+
			"the policy requires 95%%", d.Name())
	}
}

func TestAFieldMatchingTwoDecompositionsIsLeftUndecided(t *testing.T) {
	// Two decompositions both parsing a field's values means neither describes it.
	// Registering one would attach a meaning the evidence does not support, so the
	// field is left alone rather than given an arbitrary winner.
	in := derive.NewInferrer(derive.Policy{
		MinDistinctValues: 4, MinParseFraction: 0.5, MaxTrackedValues: 100,
	})
	// Deliberately ambiguous under a loosened policy: addresses and versions together.
	feed(in, fNoise, "10.1.2.3", "10.1.2.4", "11.0.3", "2.14.7")

	if d := in.DecompositionFor(src, fNoise); d != nil {
		t.Errorf("an ambiguous field was given decomposition %q instead of being left "+
			"undecided", d.Name())
	}
}

func TestTheSampleIsBounded(t *testing.T) {
	// State must stay bounded on exactly the open vocabularies this exists to serve, so
	// the fraction is estimated from a capped sample rather than from every value.
	policy := derive.Policy{MinDistinctValues: 10, MinParseFraction: 0.95, MaxTrackedValues: 100}
	in := derive.NewInferrer(policy)
	feed(in, fAddr, addresses(5000)...)

	if d := in.DecompositionFor(src, fAddr); d == nil || d.Name() != "net24" {
		t.Fatalf("bounding the sample changed the decision: %v", d)
	}
}

// ---------------------------------------------------------------------------
// Augmentation
// ---------------------------------------------------------------------------

func TestAugmentEmitsTheCoarseValueBesideTheOriginal(t *testing.T) {
	in := derive.NewInferrer(derive.DefaultPolicy())
	feed(in, fAddr, addresses(80)...)
	feed(in, fVer, versions(60)...)

	e := event.New(src, "U1@DOM1", event.Hour, map[event.FieldPath]event.Value{
		fAddr: event.NewValue("10.9.9.42"),
		fVer:  event.NewValue("11.0.3"),
	}, 1)

	derived := in.Augment(&e)
	wantAddr := derive.DerivedPath(fAddr, derive.DottedQuad{})
	if got := derived[wantAddr]; got.Text() != "10.9.9.0/24" {
		t.Errorf("derived %q = %q, want 10.9.9.0/24", wantAddr, got.Text())
	}
	wantVer := derive.DerivedPath(fVer, derive.SemanticVersion{})
	if got := derived[wantVer]; got.Text() != "11" {
		t.Errorf("derived %q = %q, want 11", wantVer, got.Text())
	}

	// The original is untouched: the exact value keeps the precise signal, the coarse
	// value adds the one the exact value cannot express.
	if v, _ := e.Get(fAddr); v.Text() != "10.9.9.42" {
		t.Error("the original value was altered")
	}
}

func TestAValueThatDoesNotParseSimplyHasNoDerivedField(t *testing.T) {
	// The residue the parse-fraction threshold tolerates. An absent derived field is
	// already handled by §5.3; inventing a value for it would not be.
	in := derive.NewInferrer(derive.DefaultPolicy())
	feed(in, fAddr, addresses(80)...)

	e := event.New(src, "U1@DOM1", event.Hour, map[event.FieldPath]event.Value{
		fAddr: event.NewValue("not-an-address"),
	}, 1)

	if derived := in.Augment(&e); len(derived) != 0 {
		t.Errorf("a value that does not parse produced derived fields: %v", derived)
	}
}

func TestAugmentNamesNoField(t *testing.T) {
	// R2. The decision is made from the values; two fields with identical values must
	// derive identically however they are named.
	in := derive.NewInferrer(derive.DefaultPolicy())
	const other = event.FieldPath("wholly.different.name")
	feed(in, fAddr, addresses(80)...)
	feed(in, other, addresses(80)...)

	a := in.DecompositionFor(src, fAddr)
	b := in.DecompositionFor(src, other)
	if a == nil || b == nil || a.Name() != b.Name() {
		t.Errorf("two fields with identical values derived differently: %v against %v", a, b)
	}
}

func TestSplitDerivedRecoversTheOriginal(t *testing.T) {
	// R5: a reader of a verdict must be able to see which field a derived value came
	// from without knowing the encoding.
	path := derive.DerivedPath(fAddr, derive.DottedQuad{})
	original, name, ok := derive.SplitDerived(path)
	if !ok || original != fAddr || name != "net24" {
		t.Errorf("SplitDerived(%q) = %q, %q, %v", path, original, name, ok)
	}
	if _, _, ok := derive.SplitDerived(fAddr); ok {
		t.Error("an ordinary field path was reported as derived")
	}
}

func versions(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("%d.%d.%d", 10+i%3, i%7, i))
	}
	return out
}
