package mail_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/mail"
)

func TestSMTPURLs(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"smtp://mail.example.com", "smtp://user:pass@mail.example.com:2525", "smtp://localhost:1025?tls=none", "smtps://mail.example.com",
	} {
		_, err := mail.NewSMTP(raw, "no-reply@example.com")
		require.NoError(t, err, raw)
	}

	for _, raw := range []string{"http://mail.example.com", "smtp://mail.example.com?tls=maybe", "smtp://mail.example.com:port"} {
		_, err := mail.NewSMTP(raw, "no-reply@example.com")
		assert.Error(t, err, raw)
	}
}
