package event

// Value is an immutable field value.
//
// Corpus readers emit values as text; the field registry of §5.1 infers the kind
// (categorical, boolean, discrete, continuous, identifier, excluded, unknown). This
// ordering is what discharges R2: nothing in the ingest path needs advance knowledge of
// a field's type, cardinality, or value set.
//
// The text is never converted to the inferred type. A kind selects how a value is
// *scored*, not what it is stored as, and the registry's package documentation records
// why: converting would make the reading depend on the batch (R1), would merge values a
// source distinguishes, and would commit before the evidence is in.
//
// A value may be present but not interpretable. LANL encodes this as a literal "?"
// (see DATA.md). §5.3 distinguishes that case from absence: an absent field is not
// in dom(e) at all, whereas a present-but-uninterpretable field is in dom(e) and
// yields status abstained_unusable. Collapsing the two would lose the distinction
// the four-valued status exists to preserve.
type Value struct {
	text     string
	unusable bool
}

// NewValue returns a usable text value.
func NewValue(text string) Value {
	return Value{text: text}
}

// UnusableValue returns a value that is present in dom(e) but not scoreable. The
// text is retained so that evidence can report what was actually observed (R5).
func UnusableValue(text string) Value {
	return Value{text: text, unusable: true}
}

// Text returns the value's canonical text representation. It is the identity used
// for equality throughout the framework, so that no detector needs to know a
// field's type in order to compare values.
func (v Value) Text() string { return v.text }

// IsUsable reports whether the value can be scored. A false result must produce an
// abstained_unusable verdict, never a neutral score (R3).
func (v Value) IsUsable() bool { return !v.unusable }

// IsEmpty reports whether the value carries no text at all.
func (v Value) IsEmpty() bool { return v.text == "" }
