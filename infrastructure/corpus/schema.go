package corpus

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/JohnPierman/ethogram/domain/event"
)

// schemaFile is the JSON form of a Schema: the configuration artefact whose existence
// is what E6 measures. Onboarding a source means writing one of these files; the
// hypothesis' target is zero code changes, and this file is not code.
type schemaFile struct {
	Source          string   `json:"source"`
	Delimiter       string   `json:"delimiter"`
	TimeColumn      int      `json:"time_column"`
	TimeUnitSeconds int64    `json:"time_unit_seconds"`
	EntityColumn    int      `json:"entity_column"`
	Columns         []string `json:"columns"`
	MissingToken    string   `json:"missing_token"`

	// TimeLayout and EpochRFC3339 select formatted-timestamp parsing; empty
	// TimeLayout keeps the integer-tick reading.
	TimeLayout   string `json:"time_layout"`
	EpochRFC3339 string `json:"epoch_rfc3339"`

	// EntityAdmitRegex restricts the entity population declaratively; empty admits
	// every entity. The run's coverage statement records the pattern.
	EntityAdmitRegex string `json:"entity_admit_regex"`
}

// LoadSchema reads a schema configuration file.
func LoadSchema(path string) (Schema, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the configuration the flag names
	if err != nil {
		return Schema{}, err
	}
	var sf schemaFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return Schema{}, fmt.Errorf("schema %s: %w", path, err)
	}
	if sf.Source == "" || len(sf.Columns) == 0 {
		return Schema{}, fmt.Errorf("schema %s: source and columns are required", path)
	}
	if len(sf.Delimiter) != 1 {
		return Schema{}, fmt.Errorf("schema %s: delimiter must be one byte, got %q", path, sf.Delimiter)
	}
	if sf.TimeLayout == "" && sf.TimeUnitSeconds <= 0 {
		return Schema{}, fmt.Errorf("schema %s: time_unit_seconds must be positive "+
			"when no time_layout is given", path)
	}
	var epoch time.Time
	if sf.TimeLayout != "" {
		if sf.EpochRFC3339 == "" {
			return Schema{}, fmt.Errorf("schema %s: epoch_rfc3339 is required with a time_layout", path)
		}
		parsed, err := time.Parse(time.RFC3339, sf.EpochRFC3339)
		if err != nil {
			return Schema{}, fmt.Errorf("schema %s: epoch_rfc3339: %w", path, err)
		}
		epoch = parsed
	}

	columns := make([]event.FieldPath, len(sf.Columns))
	for i, c := range sf.Columns {
		columns[i] = event.FieldPath(c)
	}
	s := Schema{
		Source:       event.SourceID(sf.Source),
		Delimiter:    sf.Delimiter[0],
		TimeColumn:   sf.TimeColumn,
		TimeUnit:     event.Timestamp(sf.TimeUnitSeconds) * event.Second,
		EntityColumn: sf.EntityColumn,
		Columns:      columns,
		MissingToken: sf.MissingToken,
		TimeLayout:   sf.TimeLayout,
		Epoch:        epoch,
	}
	if sf.EntityAdmitRegex != "" {
		re, err := regexp.Compile(sf.EntityAdmitRegex)
		if err != nil {
			return Schema{}, fmt.Errorf("schema %s: entity_admit_regex: %w", path, err)
		}
		s.EntityFilter = re.MatchString
	}
	return s, nil
}
