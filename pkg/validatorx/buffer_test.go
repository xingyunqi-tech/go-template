package validatorx

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDecodeUTF8LossyReplacesMaximalSubparts(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"\xff\xff":         "\ufffd\ufffd",
		"\xc0\x80":         "\ufffd\ufffd",
		"\xf0\x9f\x98":     "\ufffd",
		"\xf0\x9f\x98A":    "\ufffdA",
		"\xed\xa0\x80":     "\ufffd\ufffd\ufffd",
		"a\xe4\xb8\xadb":   "a中b",
		"\xef\xbf\xbd":     "\ufffd",
		"\xe0\x80":         "\ufffd\ufffd",
		"\xf4\x90\x80\x80": "\ufffd\ufffd\ufffd\ufffd",
	}

	for in, want := range cases {
		assert.Equal(t, want, DecodeUTF8Lossy([]byte(in)), "%q", in)
	}
}
