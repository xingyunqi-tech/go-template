package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
)

func TestHealthcheckRejectsUnknownFlags(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	assert.Equal(t, exitUsage, run([]string{"healthcheck", "--bogus"}, strings.NewReader(""), &stdout, &stderr))
}

func TestUnknownCommandPrintsUsage(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	assert.Equal(t, exitUsage, run([]string{"nope"}, strings.NewReader(""), &stdout, &stderr))
	assert.Contains(t, stderr.String(), "healthcheck [--ready]")
}

func TestVersionPrintsTheBuildVersion(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	assert.Equal(t, exitOK, run([]string{"version"}, strings.NewReader(""), &stdout, &stderr))
	assert.Equal(t, "dev\n", stdout.String())
}

func TestKeysGeneratePrintsAUsableSigningKey(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	require.Equal(t, exitOK, run([]string{"keys", "generate"}, strings.NewReader(""), &stdout, &stderr))

	priv, err := jwtx.ParsePrivateKey(stdout.String())
	require.NoError(t, err)

	_, public, ok := strings.Cut(stdout.String(), "-----END PRIVATE KEY-----")
	require.True(t, ok)

	pubs, err := jwtx.ParsePublicKeys(public)
	require.NoError(t, err)
	require.Len(t, pubs, 1)
	assert.Equal(t, priv.Public(), pubs[0])

	kid, err := jwtx.KeyID(pubs[0])
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "# kid "+kid)
}

func TestCreateSuperuserNeedsAnEmailAndAValidPassword(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	assert.Equal(t, exitUsage, run([]string{"createsuperuser"}, strings.NewReader("Passw0rd!\n"), &stdout, &stderr))
	assert.Equal(t, exitUsage, run([]string{"createsuperuser", "--email", "not an email"}, strings.NewReader("Passw0rd!\n"), &stdout, &stderr))
	assert.Equal(t, exitUsage, run([]string{"createsuperuser", "--email", "a@example.com"}, strings.NewReader("short\n"), &stdout, &stderr))
	assert.Contains(t, stderr.String(), "8 to 128 characters")
}
