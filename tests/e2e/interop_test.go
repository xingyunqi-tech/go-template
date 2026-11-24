//go:build e2e

package e2e_test

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// verifierURL is api-verifier of docker/docker-compose.ci.yaml, which verifies the tokens of the
// issuer with AUTH_JWKS_URL.
func verifierURL() string {
	if u := os.Getenv("VERIFIER_URL"); u != "" {
		return u
	}

	return "http://localhost:3001"
}

func TestTokensSignedByAnOldKeyStillVerify(t *testing.T) {
	t.Parallel()

	old := tokenSignedBy(t, oldKey())

	assert.Equal(t, http.StatusOK, call(t, http.MethodGet, "/api/v1/notes", old, "").status)
	assert.Equal(t, http.StatusOK, callAt(t, verifierURL(), http.MethodGet, "/api/v1/notes", old, "").status)
}

func TestTheVerifierAcceptsIssuedTokensUntilSignOut(t *testing.T) {
	t.Parallel()

	email := fmt.Sprintf("e2e-%s@example.com", uuid.NewV7())

	login := call(t, http.MethodPost, "/api/v1/auth/email/login", "", `{"email":"user@example.com","password":"`+_password+`"}`)
	require.Equal(t, http.StatusOK, login.status, string(login.raw))

	access, _ := loginData(t, login)

	note := callAt(t, verifierURL(), http.MethodPost, "/api/v1/notes", access, `{"title":"`+email+`"}`)
	require.Equal(t, http.StatusCreated, note.status, string(note.raw))

	require.Equal(t, http.StatusOK, call(t, http.MethodPost, "/api/v1/auth/logout", access, "").status)

	r := callAt(t, verifierURL(), http.MethodGet, "/api/v1/notes", access, "")
	assert.Equal(t, http.StatusUnauthorized, r.status, "the revocation key is shared")
}

func TestTheVerifierServesNoIdentityOperation(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/api/v1/me", "/api/v1/users", "/api/v1/auth/providers", "/.well-known/jwks.json"} {
		r := callAt(t, verifierURL(), http.MethodGet, path, token(t), "")
		assert.Equal(t, http.StatusNotFound, r.status, path)
	}

	doc := callAt(t, verifierURL(), http.MethodGet, "/openapi.json", "", "")
	require.Equal(t, http.StatusOK, doc.status)

	paths, _ := doc.body["paths"].(map[string]any)
	assert.Contains(t, paths, "/api/v1/notes")
	assert.NotContains(t, paths, "/api/v1/auth/email/login")
}
