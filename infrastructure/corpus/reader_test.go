package corpus_test

import (
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/infrastructure/corpus"
)

// The publisher's documented example rows for auth.txt (see DATA.md). The schema
// below is a test fixture, not reader code: the reader must work for any Schema
// literal, which is what E6's zero-code-change claim rests on.
const (
	authRow1 = "1,C625$@DOM1,U147@DOM1,C625,C625,Negotiate,Batch,LogOn,Success"
	authRow2 = "1,C653$@DOM1,SYSTEM@C653,C653,C653,Negotiate,Service,LogOn,Success"
	authRow3 = "2,C1678$@DOM1,C1678$@DOM1,C1678,C1678,?,Network,LogOff,Success"
)

func lanlAuthSchema() corpus.Schema {
	return corpus.Schema{
		Source:       "lanl.auth",
		Delimiter:    ',',
		TimeColumn:   0,
		TimeUnit:     event.Second,
		EntityColumn: 1,
		Columns: []event.FieldPath{
			"",
			"auth.source_user",
			"auth.destination_user",
			"auth.source_computer",
			"auth.destination_computer",
			"auth.authentication_type",
			"auth.logon_type",
			"auth.authentication_orientation",
			"auth.success_failure",
		},
		MissingToken: "?",
	}
}

func lanlDNSSchema() corpus.Schema {
	return corpus.Schema{
		Source:       "lanl.dns",
		Delimiter:    ',',
		TimeColumn:   0,
		TimeUnit:     event.Second,
		EntityColumn: 1,
		Columns: []event.FieldPath{
			"",
			"dns.source_computer",
			"dns.computer_resolved",
		},
		MissingToken: "?",
	}
}

func mustNext(t *testing.T, r *corpus.Reader) *event.Event {
	t.Helper()
	e, err := r.Next()
	if err != nil {
		t.Fatalf("Next() = %v, want an event", err)
	}
	return e
}

func mustRowError(t *testing.T, r *corpus.Reader, wantLine int64) *corpus.RowError {
	t.Helper()
	_, err := r.Next()
	var rowErr *corpus.RowError
	if !errors.As(err, &rowErr) {
		t.Fatalf("Next() = %v, want *corpus.RowError", err)
	}
	if rowErr.Line != wantLine {
		t.Fatalf("RowError.Line = %d, want %d", rowErr.Line, wantLine)
	}
	if rowErr.Reason == "" {
		t.Fatal("RowError.Reason is empty; the caller must be told why the row was malformed")
	}
	return rowErr
}

func TestParseDocumentedAuthRow(t *testing.T) {
	r := corpus.NewReader(strings.NewReader(authRow1), lanlAuthSchema())
	e := mustNext(t, r)

	if got, want := e.OccurredAt(), 1*event.Second; got != want {
		t.Errorf("OccurredAt() = %d, want %d", got, want)
	}
	if got, want := e.Entity(), event.EntityID("C625$@DOM1"); got != want {
		t.Errorf("Entity() = %q, want %q", got, want)
	}
	if got, want := e.Source(), event.SourceID("lanl.auth"); got != want {
		t.Errorf("Source() = %q, want %q", got, want)
	}
	// The time column has Columns[0] == "" and is therefore not emitted as a
	// field, so dom(e) carries the remaining eight columns.
	if got, want := e.Len(), 8; got != want {
		t.Errorf("|dom(e)| = %d, want %d", got, want)
	}

	authType, ok := e.Get("auth.authentication_type")
	if !ok {
		t.Fatal("auth.authentication_type not in dom(e)")
	}
	if !authType.IsUsable() {
		t.Error("auth.authentication_type must be usable")
	}
	if got, want := authType.Text(), "Negotiate"; got != want {
		t.Errorf("auth.authentication_type = %q, want %q", got, want)
	}

	outcome, ok := e.Get("auth.success_failure")
	if !ok {
		t.Fatal("auth.success_failure not in dom(e)")
	}
	if got, want := outcome.Text(), "Success"; got != want {
		t.Errorf("auth.success_failure = %q, want %q", got, want)
	}
}

// TestMissingTokenIsPresentButUnusable covers the §5.3 distinction: a literal "?"
// is in dom(e) but not scoreable, and the observed text is retained for evidence.
func TestMissingTokenIsPresentButUnusable(t *testing.T) {
	row := "1,U66@DOM1,U66@DOM1,C506,C506,Kerberos,?,LogOn,Success"
	r := corpus.NewReader(strings.NewReader(row), lanlAuthSchema())
	e := mustNext(t, r)

	logonType, ok := e.Get("auth.logon_type")
	if !ok {
		t.Fatal("auth.logon_type must be in dom(e): present-but-unusable is not absent")
	}
	if logonType.IsUsable() {
		t.Error("auth.logon_type must be unusable")
	}
	if got, want := logonType.Text(), "?"; got != want {
		t.Errorf("auth.logon_type Text() = %q, want %q", got, want)
	}
}

// TestEmptyColumnIsAbsent covers the other half of §5.3: two adjacent delimiters
// mean the field was never present, so it must not enter dom(e) at all.
func TestEmptyColumnIsAbsent(t *testing.T) {
	row := "1,U66@DOM1,U66@DOM1,C506,C506,Kerberos,,LogOn,Success"
	r := corpus.NewReader(strings.NewReader(row), lanlAuthSchema())
	e := mustNext(t, r)

	if e.Has("auth.logon_type") {
		t.Error("an empty column must not enter dom(e)")
	}
	if got, want := e.Len(), 7; got != want {
		t.Errorf("|dom(e)| = %d, want %d", got, want)
	}
}

// TestMalformedRowsSurfaceAndReaderResumes exercises every malformed shape in one
// stream: the reader must return each as a *RowError with its line number, resume
// at the next row, and keep honest counts.
func TestMalformedRowsSurfaceAndReaderResumes(t *testing.T) {
	input := strings.Join([]string{
		"1,C625$@DOM1,U147@DOM1,C625",                                       // too few columns
		"soon,C625$@DOM1,U147@DOM1,C625,C625,Negotiate,Batch,LogOn,Success", // non-integer time
		"1,?,U147@DOM1,C625,C625,Negotiate,Batch,LogOn,Success",             // entity is the missing token
		authRow1, // a good row the reader must still reach
	}, "\n")
	r := corpus.NewReader(strings.NewReader(input), lanlAuthSchema())

	_ = mustRowError(t, r, 1) // the RowError details are asserted inside the helper
	_ = mustRowError(t, r, 2)
	_ = mustRowError(t, r, 3)

	e := mustNext(t, r)
	if got, want := e.Entity(), event.EntityID("C625$@DOM1"); got != want {
		t.Errorf("Entity() after resuming = %q, want %q", got, want)
	}

	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() at end of stream = %v, want io.EOF", err)
	}
	if got, want := r.Rows(), int64(4); got != want {
		t.Errorf("Rows() = %d, want %d", got, want)
	}
	if got, want := r.Malformed(), int64(3); got != want {
		t.Errorf("Malformed() = %d, want %d", got, want)
	}
}

// TestMultiRowStreamSequenceAndOffsets asserts the exact Next() sequence over a
// stream with a malformed row in the middle, and that offsets are 1-based line
// numbers (the malformed line still occupies its line number).
func TestMultiRowStreamSequenceAndOffsets(t *testing.T) {
	input := strings.Join([]string{
		authRow1,
		authRow2,
		authRow3,
		"4,C625$@DOM1,U147@DOM1", // malformed: too few columns
		"5,U66@DOM1,U66@DOM1,C506,C506,Kerberos,Network,LogOn,Success",
	}, "\n")
	r := corpus.NewReader(strings.NewReader(input), lanlAuthSchema())

	for _, wantOffset := range []int64{1, 2, 3} {
		e := mustNext(t, r)
		if got := e.Offset(); got != wantOffset {
			t.Errorf("Offset() = %d, want %d", got, wantOffset)
		}
	}

	_ = mustRowError(t, r, 4)

	e := mustNext(t, r)
	if got, want := e.Offset(), int64(5); got != want {
		t.Errorf("Offset() after the malformed row = %d, want %d", got, want)
	}

	if _, err := r.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("Next() at end of stream = %v, want io.EOF", err)
	}
}

// TestSecondSourceIsASchemaLiteral is E6 in miniature: onboarding the LANL dns
// file is a Schema literal in this test, with zero changes to the reader.
func TestSecondSourceIsASchemaLiteral(t *testing.T) {
	r := corpus.NewReader(strings.NewReader("31,C161,C2109"), lanlDNSSchema())
	e := mustNext(t, r)

	if got, want := e.Source(), event.SourceID("lanl.dns"); got != want {
		t.Errorf("Source() = %q, want %q", got, want)
	}
	if got, want := e.Entity(), event.EntityID("C161"); got != want {
		t.Errorf("Entity() = %q, want %q", got, want)
	}
	if got, want := e.OccurredAt(), 31*event.Second; got != want {
		t.Errorf("OccurredAt() = %d, want %d", got, want)
	}
	if got, want := e.Len(), 2; got != want {
		t.Errorf("|dom(e)| = %d, want %d", got, want)
	}
	resolved, ok := e.Get("dns.computer_resolved")
	if !ok {
		t.Fatal("dns.computer_resolved not in dom(e)")
	}
	if got, want := resolved.Text(), "C2109"; got != want {
		t.Errorf("dns.computer_resolved = %q, want %q", got, want)
	}
}

// TestParsingIsDeterministic parses the same input twice and requires
// bit-identical event IDs, the property E8's byte-identical scores rest on.
func TestParsingIsDeterministic(t *testing.T) {
	input := strings.Join([]string{authRow1, authRow2, authRow3}, "\n")

	parse := func() []event.ID {
		r := corpus.NewReader(strings.NewReader(input), lanlAuthSchema())
		var ids []event.ID
		for {
			e, err := r.Next()
			if errors.Is(err, io.EOF) {
				return ids
			}
			if err != nil {
				t.Fatalf("Next() = %v", err)
			}
			ids = append(ids, e.ID())
		}
	}

	first, second := parse(), parse()
	if !slices.Equal(first, second) {
		t.Fatalf("event IDs differ across parses of identical input:\n%v\n%v", first, second)
	}
}

// TestLargeLineWithinBuffer guards the scanner's buffer sizing: a 100 KB line must
// parse rather than fail on bufio.Scanner's default 64 KiB token limit.
func TestLargeLineWithinBuffer(t *testing.T) {
	long := strings.Repeat("A", 100*1024)
	row := "1,U66@DOM1,U66@DOM1,C506," + long + ",Kerberos,Network,LogOn,Success"
	r := corpus.NewReader(strings.NewReader(row), lanlAuthSchema())

	e := mustNext(t, r)
	v, ok := e.Get("auth.destination_computer")
	if !ok {
		t.Fatal("auth.destination_computer not in dom(e)")
	}
	if got, want := len(v.Text()), len(long); got != want {
		t.Errorf("auth.destination_computer length = %d, want %d", got, want)
	}
}
