// Package jsontime encodes time values the way JavaScript Date.prototype.toISOString does.
package jsontime

import (
	"bytes"
	"fmt"
	"time"
)

// Layout is the toISOString format: UTC, millisecond precision, Z suffix.
const Layout = "2006-01-02T15:04:05.000Z"

// Time is a time.Time that marshals as toISOString.
type Time struct {
	time.Time
}

// New wraps t.
func New(t time.Time) Time {
	return Time{Time: t}
}

// String returns the toISOString form.
func (t Time) String() string {
	return t.UTC().Format(Layout)
}

// MarshalJSON implements json.Marshaler.
func (t Time) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, len(`""`)+len(Layout))
	b = append(b, '"')
	b = t.UTC().AppendFormat(b, Layout)
	b = append(b, '"')

	return b, nil
}

// UnmarshalJSON implements json.Unmarshaler; it accepts RFC 3339 with any fractional precision.
func (t *Time) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		return nil
	}

	var parsed time.Time
	if err := parsed.UnmarshalJSON(data); err != nil {
		return fmt.Errorf("jsontime: %w", err)
	}

	t.Time = parsed

	return nil
}
