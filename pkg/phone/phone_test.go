package phone_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/phone"
)

func TestNormalize(t *testing.T) {
	t.Parallel()

	for _, c := range [][2]string{
		{"13800000000", "+8613800000000"},
		{"\t13800000000\n", "+8613800000000"},
		{"+8613800000000", "+8613800000000"},
		{"+14155552671", "+14155552671"},
	} {
		got, err := phone.Normalize(c[0])
		require.NoError(t, err, c[0])
		assert.Equal(t, c[1], got, c[0])
	}

	for _, in := range []string{"", "1380000000", "23800000000", "+0123456789", "+86 138 0000 0000", "8613800000000", "+1234567890123456"} {
		_, err := phone.Normalize(in)
		require.ErrorIs(t, err, phone.ErrInvalid, in)
	}
}
