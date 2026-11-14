// Package msduration parses durations like the npm ms package (2.x).
package msduration

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

const (
	_maxLength = 100
	_second    = 1000
	_minute    = 60 * _second
	_hour      = 60 * _minute
	_day       = 24 * _hour
	_week      = 7 * _day
	_year      = 365.25 * _day
)

// ErrInvalid reports a string ms cannot parse.
var ErrInvalid = errors.New("invalid duration")

var _pattern = regexp.MustCompile(
	`(?i)^(-?(?:\d+)?\.?\d+) *(milliseconds?|msecs?|ms|seconds?|secs?|s|minutes?|mins?|m|hours?|hrs?|h|days?|d|weeks?|w|years?|yrs?|y)?$`,
)

// Parse returns the duration in milliseconds; a number without a unit is milliseconds.
func Parse(value string) (float64, error) {
	if len(value) > _maxLength {
		return 0, ErrInvalid
	}

	m := _pattern.FindStringSubmatch(value)
	if m == nil {
		return 0, ErrInvalid
	}

	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, ErrInvalid
	}

	return n * unitMillis(strings.ToLower(m[2])), nil
}

func unitMillis(unit string) float64 {
	if unit == "" || unit == "ms" || strings.HasPrefix(unit, "msec") || strings.HasPrefix(unit, "millisecond") {
		return 1
	}

	units := []struct {
		prefix string
		millis float64
	}{{"s", _second}, {"m", _minute}, {"h", _hour}, {"d", _day}, {"w", _week}}

	for _, u := range units {
		if strings.HasPrefix(unit, u.prefix) {
			return u.millis
		}
	}

	return _year
}

// Value is a configuration value holding the original text and its milliseconds.
type Value struct {
	Text   string
	Millis float64
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (v *Value) UnmarshalText(text []byte) error {
	ms, err := Parse(string(text))
	if err != nil {
		return err
	}

	v.Text, v.Millis = string(text), ms

	return nil
}
