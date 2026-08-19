// Package corpus implements the streaming corpus ingest of requirement R2 (§5.1).
//
// The reader emits generic field paths and leaves kind inference to the field
// registry of §5.1. No corpus's column layout appears in this package as code: the
// mapping from delimited columns to field paths is a [Schema] value, so onboarding
// a new source is a configuration change and nothing more, which is exactly what
// evaluation hypothesis E6 measures.
//
// §5.3 distinguishes a field that is absent from one that is present but unusable.
// An empty column (two adjacent delimiters) is absent and never enters dom(e); a
// column equal to the schema's missing token (LANL's literal "?") enters dom(e) as
// an unusable value, so detectors abstain over it rather than treating it as never
// observed.
//
// Ingest is deterministic (R4): the reader consults no wall clock and no source of
// randomness. Timestamps come from the row's own time column, multiplied into
// microseconds by the schema's unit.
package corpus

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/JohnPierman/ethogram/domain/event"
)

// Schema maps delimited columns to field paths. It is configuration, not code
// (R2, E6): a new source is onboarded by writing a Schema literal, never by
// changing the reader.
//
// The time column and the entity column are emitted as fields only if their
// Columns entry is non-empty. The entity column normally is also a field (LANL's
// auth.source_user, for example), because the co-occurrence graph of §8 wants it.
type Schema struct {
	Source     event.SourceID
	Delimiter  byte            // ',' for LANL
	TimeColumn int             // 0-based column carrying the integer timestamp
	TimeUnit   event.Timestamp // multiplier to microseconds; event.Second for LANL

	// TimeLayout, when non-empty, parses the time column as a formatted timestamp
	// using this Go reference layout instead of an integer tick count, and measures
	// event time from Epoch. Corpora differ in how they encode time — LANL counts
	// seconds from an arbitrary epoch, CERT writes "01/02/2010 02:24:51" — and that
	// is an encoding concern of the reader, not a property of a source's fields.
	// Supporting it here is a generic capability that serves any corpus using the
	// same encoding; E6's zero-code-change claim concerns admitting unseen FIELDS,
	// and a run whose onboarding required this capability records that fact.
	TimeLayout string

	// Epoch is the instant corresponding to event time zero when TimeLayout is set.
	// Event time is deliberately relative: the framework's decay and circular
	// timing depend only on differences and on time of day, and keeping the origin
	// explicit keeps replays reproducible.
	Epoch        time.Time
	EntityColumn int               // 0-based column whose value is the entity ε(e)
	Columns      []event.FieldPath // field path per column; "" = column not emitted as a field
	MissingToken string            // a value equal to this is present-but-unusable; "?" for LANL (§5.3)

	// EntityFilter, when non-nil, admits only rows whose entity value satisfies it,
	// before any event is constructed. This is an ingest-side application of the
	// run's entity-population restriction, which the run records in its coverage
	// statement; it exists because building and digesting events for rows the
	// population excludes costs the majority of a full-corpus pass. Filtered rows
	// are counted, not errors.
	EntityFilter func(string) bool
}

// RowError reports a single malformed row: wrong column count, an unparseable
// timestamp, or a missing entity. Malformed rows are surfaced, never silently
// dropped; the caller decides whether to log, count, or abort. [Reader.Next] may
// be called again after a RowError and resumes at the following row.
type RowError struct {
	// Line is the 1-based line number of the malformed row.
	Line int64
	// Reason describes why the row could not become an event.
	Reason string
}

// Error implements the error interface.
func (e *RowError) Error() string {
	return fmt.Sprintf("corpus row %d: %s", e.Line, e.Reason)
}

// scannerBufferSize caps a single line at 1 MiB. bufio.Scanner's default 64 KiB
// token limit must not be the failure mode on an unusually long row; no LANL row
// approaches 1 MiB.
const scannerBufferSize = 1 << 20

// Reader streams events from a delimited text stream. It never loads the corpus
// into memory — LANL's auth file is 7.6 GB compressed and over a billion rows —
// only the current line is ever held.
//
// encoding/csv is deliberately not used: LANL fields never contain quotes or
// embedded delimiters, and csv's quote handling would silently corrupt values
// containing a double quote.
type Reader struct {
	scanner   *bufio.Scanner
	schema    Schema
	delimiter string
	rows      int64
	malformed int64
	filtered  int64
}

// NewReader returns a Reader decoding r according to schema.
func NewReader(r io.Reader, schema Schema) *Reader {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, scannerBufferSize), scannerBufferSize)
	return &Reader{
		scanner:   scanner,
		schema:    schema,
		delimiter: string([]byte{schema.Delimiter}),
	}
}

// Next returns the next event, or io.EOF at the end of the stream.
//
// Malformed rows (wrong column count, unparseable time, missing entity) are not
// silently dropped: they are returned as *RowError carrying the line number and
// the reason; the caller decides. Next can be called again after a *RowError,
// resuming at the next row.
func (r *Reader) Next() (*event.Event, error) {
	for {
		if !r.scanner.Scan() {
			if err := r.scanner.Err(); err != nil {
				return nil, fmt.Errorf("reading corpus stream after row %d: %w", r.rows, err)
			}
			return nil, io.EOF
		}
		r.rows++

		if r.schema.EntityFilter != nil {
			if admit, ok := r.entityAdmitted(r.scanner.Text()); ok && !admit {
				r.filtered++
				continue
			}
			// A row whose entity column cannot even be located falls through to
			// parseRow, which reports the malformation properly.
		}

		e, rowErr := r.parseRow(r.scanner.Text())
		if rowErr != nil {
			r.malformed++
			return nil, rowErr
		}
		return e, nil
	}
}

// entityAdmitted locates the entity column without building the event. ok is false
// when the row is too short to tell, in which case the caller lets parseRow classify
// the malformation.
func (r *Reader) entityAdmitted(line string) (admit, ok bool) {
	col := 0
	start := 0
	for i := 0; i <= len(line); i++ {
		if i == len(line) || line[i] == r.schema.Delimiter {
			if col == r.schema.EntityColumn {
				return r.schema.EntityFilter(line[start:i]), true
			}
			col++
			start = i + 1
		}
	}
	return false, false
}

// FilteredByEntity returns the count of rows the entity filter excluded.
func (r *Reader) FilteredByEntity() int64 { return r.filtered }

// Rows returns the count of rows read so far (including malformed and filtered).
func (r *Reader) Rows() int64 { return r.rows }

// Malformed returns the count of rows that produced a *RowError so far.
func (r *Reader) Malformed() int64 { return r.malformed }

func (r *Reader) parseRow(line string) (*event.Event, *RowError) {
	columns := strings.Split(line, r.delimiter)
	if len(columns) != len(r.schema.Columns) {
		return nil, r.rowError("expected %d columns, got %d", len(r.schema.Columns), len(columns))
	}

	occurredAt, rowErr := r.parseTime(columns)
	if rowErr != nil {
		return nil, rowErr
	}
	entity, rowErr := r.parseEntity(columns)
	if rowErr != nil {
		return nil, rowErr
	}

	// The offset is the 1-based line number: provenance only, excluded from the
	// event's content digest.
	e := event.New(r.schema.Source, entity, occurredAt, r.parseFields(columns), r.rows)
	return &e, nil
}

func (r *Reader) parseTime(columns []string) (event.Timestamp, *RowError) {
	if r.schema.TimeColumn < 0 || r.schema.TimeColumn >= len(columns) {
		return 0, r.rowError("time column %d outside the %d-column row", r.schema.TimeColumn, len(columns))
	}
	raw := columns[r.schema.TimeColumn]
	if r.schema.TimeLayout != "" {
		at, err := time.Parse(r.schema.TimeLayout, raw)
		if err != nil {
			return 0, r.rowError("unparseable time %q: does not match layout %q",
				raw, r.schema.TimeLayout)
		}
		return event.Timestamp(at.Sub(r.schema.Epoch).Microseconds()), nil
	}
	ticks, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, r.rowError("unparseable time %q: not an integer", raw)
	}
	return event.Timestamp(ticks) * r.schema.TimeUnit, nil
}

// parseEntity rejects a row whose entity column is empty or carries the missing
// token: an event without an entity cannot be scored per-entity.
func (r *Reader) parseEntity(columns []string) (event.EntityID, *RowError) {
	if r.schema.EntityColumn < 0 || r.schema.EntityColumn >= len(columns) {
		return "", r.rowError("entity column %d outside the %d-column row", r.schema.EntityColumn, len(columns))
	}
	raw := columns[r.schema.EntityColumn]
	if raw == "" || raw == r.schema.MissingToken {
		return "", r.rowError("entity column %d is %q, and an event without an entity cannot be scored per-entity", r.schema.EntityColumn, raw)
	}
	return event.EntityID(raw), nil
}

// parseFields builds dom(e), preserving the §5.3 distinction: an empty column is
// absent and never enters dom(e), whereas a column equal to the missing token
// enters dom(e) as a present-but-unusable value.
func (r *Reader) parseFields(columns []string) map[event.FieldPath]event.Value {
	fields := make(map[event.FieldPath]event.Value, len(columns))
	for i, raw := range columns {
		path := r.schema.Columns[i]
		if path == "" || raw == "" {
			continue
		}
		if raw == r.schema.MissingToken {
			fields[path] = event.UnusableValue(raw)
			continue
		}
		fields[path] = event.NewValue(raw)
	}
	return fields
}

func (r *Reader) rowError(format string, args ...any) *RowError {
	return &RowError{Line: r.rows, Reason: fmt.Sprintf(format, args...)}
}
