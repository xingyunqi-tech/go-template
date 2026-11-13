package validatorx

import (
	"strings"
	"unicode/utf8"
)

// utf8Bounds returns how many continuation bytes a lead byte needs and the range allowed for the
// first of them; need is 0 for bytes that cannot start a sequence.
func utf8Bounds(lead byte) (need int, lo, hi byte) {
	switch {
	case lead >= 0xC2 && lead <= 0xDF:
		return 1, 0x80, 0xBF
	case lead == 0xE0:
		return 2, 0xA0, 0xBF
	case lead == 0xED:
		return 2, 0x80, 0x9F
	case lead >= 0xE1 && lead <= 0xEF:
		return 2, 0x80, 0xBF
	case lead == 0xF0:
		return 3, 0x90, 0xBF
	case lead >= 0xF1 && lead <= 0xF3:
		return 3, 0x80, 0xBF
	case lead == 0xF4:
		return 3, 0x80, 0x8F
	default:
		return 0, 0, 0
	}
}

// DecodeUTF8Lossy decodes like Node's Buffer.toString: each maximal ill-formed subpart becomes one
// U+FFFD (WHATWG UTF-8 decoder).
func DecodeUTF8Lossy(b []byte) string {
	var out strings.Builder

	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if r != utf8.RuneError || size > 1 {
			out.WriteRune(r)

			i += size

			continue
		}

		need, lo, hi := utf8Bounds(b[i])
		j := i + 1

		for k := 0; k < need && j < len(b); k++ {
			if b[j] < lo || b[j] > hi {
				break
			}

			lo, hi = 0x80, 0xBF

			j++
		}

		out.WriteRune(utf8.RuneError)

		i = j
	}

	return out.String()
}
