package registry

import (
	"math"
	"strconv"
	"strings"
)

// ParseNumber parses a field value as a finite decimal number, declining anything
// else. It is the single definition of "this value is a measurement" that both the
// inference site (§5.1) and the scoring sites (§8.2, §9) use.
//
// # Why not strconv.ParseFloat directly
//
// ParseFloat is a parser for Go source literals, and a corpus field is not Go source.
// It accepts "NaN", "Inf", "infinity", the hexadecimal form "0x1p-2", and the
// underscore grouping "1_000" — none of which is a measurement a corpus is reporting,
// and the first three of which are not orderable at all.
//
// Admitting them split the framework against itself. Inference counted such a value as
// evidence that the field is numeric, while the scoring path rejects a non-finite value
// and abstains on it (see [github.com/JohnPierman/ethogram/domain/marginal.Detector.Score]).
// A field whose missing marker happened to be spelled "NaN" was therefore classified
// numeric on the strength of values no detector would ever score, which is permanent
// abstention wearing a kind. A sentinel is a token; it must count against numeric
// inference exactly as "unknown" or "?" does, and now it does.
//
// The screen is over bytes rather than a regular expression because it runs once per
// field value per event, which is a billion times on the corpora of §12.
func ParseNumber(text string) (float64, bool) {
	if text == "" {
		return 0, false
	}
	for i := range len(text) {
		switch c := text[i]; {
		case c >= '0' && c <= '9':
		case c == '.', c == '+', c == '-', c == 'e', c == 'E':
		default:
			return 0, false
		}
	}
	x, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsInf(x, 0) || math.IsNaN(x) {
		return 0, false
	}
	return x, true
}

// Band returns the fixed-boundary magnitude band containing x, as a label.
//
// This is the coarsening that lets a continuous field take part in the per-entity
// detectors at all. Those detectors count a vocabulary and the co-occurrence graph of
// §8.2 makes each value a node, so a field whose values are nearly all distinct
// saturates the first and dissolves the second — the identifier-like behaviour §5.1's
// guard exists to prevent, reached by a measurement rather than by a key. A band is a
// bounded vocabulary over an unbounded field: "this account has never moved a volume of
// data in the gigabyte range" is expressible, where "this account has never moved
// exactly 4,294,967,296 bytes" is noise.
//
// # Fixed boundaries, not the quantile bins of §8.2
//
// §8.2 specifies quantile bins from a streaming digest. That is the right mechanism for
// a graph rebuilt from scratch on a schedule, and the wrong one here, because a band is
// a *persisted identity*: Detector I accumulates a decayed count per (entity, field,
// value) over a seven-day half-life, and the graph's edge weights accumulate the same
// way. An adaptive boundary moves as the distribution shifts, so the label "bin 3"
// silently changes meaning while the counts filed under it do not, and the history
// becomes a sum over incomparable quantities. There is no repair short of rewriting
// every stored count on every boundary move.
//
// A fixed boundary cannot drift, so a count accumulated under a band a year ago still
// means what it meant. The cost is that the bands are not equal-occupancy: a field
// whose values all fall inside one band contributes nothing. That is a real loss and
// the honest trade — it forfeits resolution on narrowly-spread fields to keep the
// counts of every other field meaningful. §8.2's adaptive binning remains unimplemented
// and is recorded as such.
//
// # The series
//
// Boundaries follow the 1-2-5 preferred-number series, so there are three bands per
// decade: about 3.3 times the resolution of a plain decade, at the same bounded
// cardinality. The label states the half-open interval it denotes rather than an
// opaque index, which is what R5 requires of anything appearing in evidence — a reader
// can check the assignment by hand. Negative values keep their sign and their order:
// x in [lo, hi) maps to -x in (-hi, -lo].
//
// A label never parses under [ParseNumber], which matters because a derived value is
// re-observed by the registry: were "1e3" the label, the band field would itself be
// classified continuous and banded again, and the second banding would be of an index.
func Band(x float64) string {
	if x == 0 {
		return "[0]"
	}

	mantissa, exponent := decimalParts(math.Abs(x))

	// The 1-2-5 series. The topmost band of a decade closes at the next decade's one.
	lower, upper, upperExponent := 5, 1, exponent+1
	switch {
	case mantissa < 2:
		lower, upper, upperExponent = 1, 2, exponent
	case mantissa < 5:
		lower, upper, upperExponent = 2, 5, exponent
	}

	low, high := decade(lower, exponent), decade(upper, upperExponent)
	if x < 0 {
		return "(-" + high + ",-" + low + "]"
	}
	return "[" + low + "," + high + ")"
}

// decimalParts returns a's mantissa in [1, 10) and its decimal exponent.
//
// It reads them off the shortest round-tripping scientific form rather than computing
// them with a logarithm. math.Log10(1000) is 2.9999999999999996 on some platforms, so
// the logarithm would put 1000 in the band below itself; that is not a rounding
// nuisance but a boundary that differs between platforms, which R4 forbids outright.
// The decimal exponent of a formatted float is exact by construction.
func decimalParts(a float64) (mantissa float64, exponent int) {
	mantissaText, exponentText, _ := strings.Cut(strconv.FormatFloat(a, 'e', -1, 64), "e")
	mantissa, _ = strconv.ParseFloat(mantissaText, 64)
	exponent, _ = strconv.Atoi(exponentText)
	return mantissa, exponent
}

// decade renders one boundary, for instance (2, -3) as "2e-3".
func decade(coefficient, exponent int) string {
	return strconv.Itoa(coefficient) + "e" + strconv.Itoa(exponent)
}
