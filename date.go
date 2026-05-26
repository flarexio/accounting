package accounting

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Date is a calendar date with no time-of-day or timezone, used for business
// dates (journal effective date, period boundaries) whose meaning is "what day".
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// NewDate constructs a Date from year, month, day with no normalization.
func NewDate(year int, month time.Month, day int) Date {
	return Date{Year: year, Month: month, Day: day}
}

// DateOf returns the calendar date of t in loc.
func DateOf(t time.Time, loc *time.Location) Date {
	if loc == nil {
		loc = time.UTC
	}
	t = t.In(loc)
	return Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}
}

// IsZero reports whether d is the zero Date.
func (d Date) IsZero() bool { return d == Date{} }

// Before reports whether d precedes other.
func (d Date) Before(other Date) bool {
	if d.Year != other.Year {
		return d.Year < other.Year
	}
	if d.Month != other.Month {
		return d.Month < other.Month
	}
	return d.Day < other.Day
}

// After reports whether d follows other.
func (d Date) After(other Date) bool { return other.Before(d) }

// Equal reports whether d and other are the same calendar date.
func (d Date) Equal(other Date) bool { return d == other }

// String returns the ISO 8601 calendar date "YYYY-MM-DD".
func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

// Time returns midnight on d in loc; for SQL boundaries that take a time.Time.
func (d Date) Time(loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, loc)
}

// MarshalJSON encodes Date as "YYYY-MM-DD".
func (d Date) MarshalJSON() ([]byte, error) {
	return []byte(`"` + d.String() + `"`), nil
}

// UnmarshalJSON parses a JSON string into d; accepts "YYYY-MM-DD" or RFC3339
// (the date part is taken in UTC, so callers should pass dates as YYYY-MM-DD
// when ambiguity matters).
func (d *Date) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*d = Date{}
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("accounting: Date JSON must be a string: %w", err)
	}
	return d.parseString(s)
}

// MarshalYAML encodes Date as a YAML string "YYYY-MM-DD".
func (d Date) MarshalYAML() (any, error) { return d.String(), nil }

// UnmarshalYAML accepts a scalar node whose value is "YYYY-MM-DD" or RFC3339.
func (d *Date) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("accounting: Date YAML must be a scalar, got kind %d", node.Kind)
	}
	return d.parseString(node.Value)
}

func (d *Date) parseString(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		*d = Date{}
		return nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		*d = Date{Year: t.Year(), Month: t.Month(), Day: t.Day()}
		return nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		u := t.UTC()
		*d = Date{Year: u.Year(), Month: u.Month(), Day: u.Day()}
		return nil
	}
	return fmt.Errorf("accounting: parse date %q: expected YYYY-MM-DD or RFC3339", s)
}
