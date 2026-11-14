package msduration_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/msduration"
)

func TestParseFollowsNpmMs(t *testing.T) {
	t.Parallel()

	cases := map[string]float64{
		"100":        100,
		"1.5":        1.5,
		"-200":       -200,
		".5s":        500,
		"15m":        900_000,
		"30M":        1_800_000,
		"1 minute":   60_000,
		"2mins":      120_000,
		"1d":         86_400_000,
		"3650d":      315_360_000_000,
		"1 week":     604_800_000,
		"1y":         31_557_600_000,
		"2 hrs":      7_200_000,
		"5 msecs":    5,
		"1sec":       1000,
		"10 seconds": 10_000,
	}

	for in, want := range cases {
		got, err := msduration.Parse(in)
		require.NoError(t, err, in)
		assert.InDelta(t, want, got, 1e-9, in)
	}

	for _, in := range []string{"", "abc", "1 fortnight", "1.2.3", " 1s", strings.Repeat("1", 101)} {
		_, err := msduration.Parse(in)
		assert.ErrorIs(t, err, msduration.ErrInvalid, in)
	}
}

func TestValueUnmarshalsText(t *testing.T) {
	t.Parallel()

	var v msduration.Value
	require.NoError(t, v.UnmarshalText([]byte("15m")))
	assert.Equal(t, "15m", v.Text)
	assert.InDelta(t, 900_000, v.Millis, 0)
	assert.Error(t, v.UnmarshalText([]byte("nope")))
}
