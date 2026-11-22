//go:build integration

package mail_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/mail"
	"github.com/xingyunqi-tech/go-template/internal/testutil"
)

func TestSMTPRequiresSTARTTLSUnlessPlainTextIsAllowed(t *testing.T) {
	t.Parallel()

	server := testutil.StartSMTP(t)
	m := &repo.Mail{To: "to@example.com", Subject: "Hello", Text: "text", HTML: "<p>html</p>"}

	plain, err := mail.NewSMTP(server.URL, "from@example.com")
	require.NoError(t, err)
	require.NoError(t, plain.Send(t.Context(), m))
	assert.Equal(t, "Hello", server.Next(t, "to@example.com").Subject)

	strict, err := mail.NewSMTP(strings.TrimSuffix(server.URL, "?tls=none"), "from@example.com")
	require.NoError(t, err)
	assert.Error(t, strict.Send(t.Context(), m))
}
