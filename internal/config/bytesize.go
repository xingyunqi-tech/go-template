package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ByteSize is a byte count written as an integer with an optional B, KiB, MiB or GiB suffix.
type ByteSize int64

const _maxBytesShift = 62

var errByteSize = errors.New("byte size must be a positive integer with an optional B, KiB, MiB or GiB suffix")

// UnmarshalText implements encoding.TextUnmarshaler.
func (b *ByteSize) UnmarshalText(text []byte) error {
	s := strings.TrimSpace(string(text))
	multiplier := int64(1)

	for _, unit := range []struct {
		suffix string
		bytes  int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"B", 1}} {
		if rest, ok := strings.CutSuffix(s, unit.suffix); ok {
			s, multiplier = strings.TrimSpace(rest), unit.bytes

			break
		}
	}

	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 || n > (1<<_maxBytesShift)/multiplier {
		return fmt.Errorf("%w: %q", errByteSize, text)
	}

	*b = ByteSize(n * multiplier)

	return nil
}
