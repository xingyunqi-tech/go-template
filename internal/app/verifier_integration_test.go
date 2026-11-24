//go:build integration

package app_test

import (
	"crypto/ed25519"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/app"
	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/testutil"
	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
	pgpool "github.com/xingyunqi-tech/go-template/pkg/postgres"
)

// serveJWKS publishes the public key of the test signing key.
func serveJWKS(t *testing.T) string {
	t.Helper()

	priv, err := jwtx.ParsePrivateKey(testutil.SigningKeyPEM())
	require.NoError(t, err)

	pub, ok := priv.Public().(ed25519.PublicKey)
	require.True(t, ok)

	keys, err := jwtx.NewStaticKeys(pub)
	require.NoError(t, err)

	doc, err := json.Marshal(keys.Set())
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(doc)
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

func newVerifierApp(t *testing.T) *testApp {
	t.Helper()

	jwks := serveJWKS(t)
	redisDB := _redis.Borrow(t)

	return newApp(t, func(c *config.Config) {
		c.Auth.SigningKey, c.Auth.JWKSURL = "", jwks
		c.Redis.URL = config.Secret(redisDB.URL())
		c.Storage.LocalRoot = t.TempDir()
	}, nil)
}

func TestAVerifierServesOnlyTheCoreOperations(t *testing.T) {
	t.Parallel()

	a := newVerifierApp(t)
	require.Equal(t, config.RoleVerifier, a.cfg.Auth.Role())

	token := testutil.IssueAccessToken(t, &testutil.TokenOptions{Roles: []string{"user"}})

	r := a.do(http.MethodPost, "/api/v1/notes", token, `{"title":"t"}`)
	require.Equal(t, http.StatusCreated, r.Status, r.Raw)
	assert.Equal(t, http.StatusOK, a.do(http.MethodGet, "/api/v1/notes", token, "").Status)

	for _, path := range []string{"/api/v1/me", "/api/v1/users", "/api/v1/auth/providers", "/.well-known/jwks.json"} {
		r := a.get(path, token)
		assert.Equal(t, http.StatusNotFound, r.Status, path)
	}

	r = a.get("/api/v1/auth/email/login", "")
	assert.Equal(t, http.StatusNotFound, r.Status)

	doc := a.get("/openapi.json", "")
	require.Equal(t, http.StatusOK, doc.Status)

	paths, _ := doc.Body["paths"].(map[string]any)
	assert.Contains(t, paths, "/api/v1/notes")
	assert.Contains(t, paths, "/api/v1/files")
	assert.Contains(t, paths, "/api/v1/audit-logs")
	assert.NotContains(t, paths, "/api/v1/me")
	assert.NotContains(t, paths, "/api/v1/auth/email/login")
}

func TestAVerifierHonorsTheRevocationKeys(t *testing.T) {
	t.Parallel()

	a := newVerifierApp(t)
	sid := uuid.NewV7()
	token := testutil.IssueAccessToken(t, &testutil.TokenOptions{Roles: []string{"user"}, SessionID: sid})

	require.Equal(t, http.StatusOK, a.get("/api/v1/notes", token).Status)
	require.NoError(t, a.infra.Redis.Set(t.Context(), "auth:revoked:sid:"+sid.String(), "1", 0).Err())
	assert.Equal(t, http.StatusUnauthorized, a.get("/api/v1/notes", token).Status)
}

func TestAVerifierWorkerPurgesItsOwnData(t *testing.T) {
	t.Parallel()

	a := newVerifierApp(t)
	sub := uuid.NewV7()
	token := testutil.IssueAccessToken(t, &testutil.TokenOptions{Subject: sub, Roles: []string{"user"}})

	require.Equal(t, http.StatusCreated, a.do(http.MethodPost, "/api/v1/notes", token, `{"title":"t"}`).Status)

	client, err := app.NewWorkerClient(a.cfg, slog.New(slog.DiscardHandler), a.infra)
	require.NoError(t, err)
	require.NoError(t, client.Start(t.Context()))
	t.Cleanup(func() { _ = client.Stop(t.Context()) })

	_, err = client.Insert(t.Context(), postgres.PurgeArgs{UserID: sub}, nil)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		var n int

		require.NoError(t, a.infra.Pool.QueryRow(t.Context(), `SELECT count(*) FROM note WHERE owner_sub = $1`, sub).Scan(&n))

		return n == 0
	}, 10*time.Second, 50*time.Millisecond)
}

func TestAVerifierMigratesOnlyTheCoreTables(t *testing.T) {
	t.Parallel()

	cfg := loadConfig(t, _db.Clone(t))
	cfg.Auth.SigningKey, cfg.Auth.JWKSURL = "", "https://issuer.example.com/.well-known/jwks.json"
	cfg.Database.Schema = "verifier_test"

	require.NoError(t, app.Migrate(false)(t.Context(), cfg, slog.New(slog.DiscardHandler), nil))

	pool, err := pgpool.New(t.Context(), cfg.Database.DSN(), pgpool.Options{MaxConns: 1, Schema: "verifier_test", Tracer: nil})
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	tables := tablesIn(t, pool, "verifier_test")
	assert.Contains(t, tables, "note")
	assert.Contains(t, tables, "file")
	assert.Contains(t, tables, "river_job")
	assert.NotContains(t, tables, "app_user")
	assert.NotContains(t, tables, "goose_identity_version")
}
