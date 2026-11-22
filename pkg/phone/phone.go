// Package phone normalizes phone numbers to E.164 (platform conventions §4).
package phone

import (
	"errors"
	"regexp"
	"strings"
)

var (
	// ErrInvalid reports input that is neither E.164 nor an 11-digit mainland China number.
	ErrInvalid = errors.New("phone: invalid number")

	_e164     = regexp.MustCompile(`^\+[1-9]\d{6,14}$`)
	_mainland = regexp.MustCompile(`^1\d{10}$`)
)

// Normalize returns the E.164 form of an E.164 number or of an 11-digit mainland China number.
func Normalize(input string) (string, error) {
	s := strings.TrimSpace(input)

	switch {
	case _mainland.MatchString(s):
		return "+86" + s, nil
	case _e164.MatchString(s):
		return s, nil
	default:
		return "", ErrInvalid
	}
}
