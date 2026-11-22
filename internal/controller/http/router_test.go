package http_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	httpapi "github.com/xingyunqi-tech/go-template/internal/controller/http"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/usecase/health"
	"github.com/xingyunqi-tech/go-template/pkg/clientip"
	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
)

const (
	_bearer       = "Bearer "
	_metricsToken = "metrics-token-0123456789" //nolint:gosec // test token
)

// specJSON reads the test spec, with its $refs or with every $ref inlined.
func specJSON(t *testing.T, inline bool) []byte {
	t.Helper()

	raw, err := os.ReadFile("testdata/spec.yaml")
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &doc))

	var out any = doc
	if inline {
		out = inlineRefs(doc, doc)
	}

	b, err := json.Marshal(out)
	require.NoError(t, err)

	return b
}

func inlineRefs(root map[string]any, v any) any {
	switch t := v.(type) {
	case map[string]any:
		if ref, ok := t["$ref"].(string); ok {
			var cur any = root
			for _, token := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
				cur = cur.(map[string]any)[token] //nolint:forcetypeassert,errcheck // the test spec only refers to maps
			}

			return inlineRefs(root, cur)
		}

		out := map[string]any{}
		for k, val := range t {
			out[k] = inlineRefs(root, val)
		}

		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = inlineRefs(root, val)
		}

		return out
	default:
		return v
	}
}

type authorizer struct{}

func (authorizer) Granted(roles []string, permission string) bool {
	return permission == "item:admin" && len(roles) > 0 && roles[0] == "admin"
}

type harness struct {
	t       *testing.T
	handler http.Handler
	signer  *jwtx.Signer
	revoked *redis.Revocations
	now     time.Time
}

func testPolicies() map[string]httpapi.Policy {
	return map[string]httpapi.Policy{
		"createItem":  {Auth: httpapi.AuthOptional, RateLimits: []string{"auth-register"}},
		"getItem":     {Auth: httpapi.AuthPublic},
		"deleteItem":  {Auth: httpapi.AuthPublic},
		"getPrivate":  {Auth: httpapi.AuthJWT},
		"getOptional": {Auth: httpapi.AuthOptional},
		"getPublic":   {Auth: httpapi.AuthPublic},
		"getAdmin":    {Auth: httpapi.AuthJWT, Perms: httpapi.All("item:admin")},
		"postReauth":  {Auth: httpapi.AuthJWT, Reauth: true},
		"getVerified": {Auth: httpapi.AuthJWT, VerifiedEmail: true},
		"getBoom":     {Auth: httpapi.AuthPublic},
		"postEvent":   {Auth: httpapi.AuthPublic, Tx: true, Audit: &httpapi.Audit{Action: "event.create", Resource: "event"}},
	}
}

func envelope(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "message": "ok", "data": data})
}

func register(r chi.Router, writeError func(http.ResponseWriter, *http.Request, error)) {
	r.Post("/api/v1/items", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var in map[string]any

		_ = json.Unmarshal(body, &in)

		envelope(w, http.StatusCreated, map[string]any{"id": uuid.NewV7().String(), "title": in["title"]})
	})
	r.Get("/api/v1/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		envelope(w, http.StatusOK, map[string]any{"id": chi.URLParam(r, "id")})
	})
	r.Delete("/api/v1/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, errors.New("database exploded"))
	})

	for _, path := range []string{"/api/v1/private", "/api/v1/optional", "/api/v1/public", "/api/v1/admin", "/api/v1/verified"} {
		r.Get(path, func(w http.ResponseWriter, r *http.Request) {
			var sub any
			if p := reqctx.PrincipalOf(r.Context()); p != nil {
				sub = p.Subject.String()
			}

			envelope(w, http.StatusOK, map[string]any{"sub": sub})
		})
	}

	r.Post("/api/v1/reauth", func(w http.ResponseWriter, _ *http.Request) { envelope(w, http.StatusOK, nil) })
	r.Get("/api/v1/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	r.Post("/api/v1/events", eventHandler(writeError))
}

type readyStub struct{ ok bool }

func (r readyStub) Ready(context.Context) health.Report {
	status := health.StatusOK
	if !r.ok {
		status = health.StatusError
	}

	return health.Report{Ready: r.ok, Checks: map[string]string{"database": status}}
}

func newHarness(t *testing.T, inline bool, mutate func(*httpapi.Options)) *harness {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	pub, ok := priv.Public().(ed25519.PublicKey)
	require.True(t, ok)

	keys, err := jwtx.NewStaticKeys(pub)
	require.NoError(t, err)

	signer, err := jwtx.NewSigner(priv, "iss", "aud")
	require.NoError(t, err)

	translator, err := i18n.New("en")
	require.NoError(t, err)

	h := &harness{t: t, signer: signer, revoked: redis.NewRevocations(nil), now: time.Now()}

	proxies, err := clientip.Parse("1")
	require.NoError(t, err)

	opts := &httpapi.Options{
		Log: slog.New(slog.DiscardHandler), Translator: translator, ServiceName: "svc",
		ValidationDocument: specJSON(t, inline), Document: []byte(`{"openapi":"3.1.0"}`), DocsEnabled: true,
		CORSOrigins: []string{"https://app.example.com"}, AnyOrigin: false, BodyLimit: 1024, TrustedProxies: proxies,
		Policies: testPolicies(), Register: register,
		Verifier: jwtx.NewVerifier(keys, "iss", "aud"), Revocations: h.revoked, Authorizer: authorizer{},
		Limiter: ratelimit.New(nil), RateLimits: false, Idempotency: redis.NewIdempotency(nil),
		Transactor: nil, Auditor: nil, Readiness: readyStub{ok: true},
		Metrics:      http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("metrics")) }),
		MetricsToken: _metricsToken, ReportError: nil, Now: func() time.Time { return h.now },
	}

	if mutate != nil {
		mutate(opts)
	}

	router, err := httpapi.NewRouter(t.Context(), opts)
	require.NoError(t, err)

	h.handler = router

	return h
}

func (h *harness) token(roles []string, verified bool, authTime time.Time) string {
	h.t.Helper()

	iat, exp := jwtx.Lifetime(time.Now(), 15*time.Minute)

	token, err := h.signer.Sign(&jwtx.Claims{
		Subject: uuid.NewV7(), SessionID: uuid.NewV7(), Roles: roles, EmailVerified: verified,
		AuthTime: authTime, IssuedAt: iat, Expires: exp, ID: "j",
	})
	require.NoError(h.t, err)

	return token
}

type response struct {
	*httptest.ResponseRecorder

	body map[string]any
}

func data(t *testing.T, r *response) map[string]any {
	t.Helper()

	d, ok := r.body["data"].(map[string]any)
	require.True(t, ok, r.Body.String())

	return d
}

func fieldErrors(t *testing.T, r *response) []map[string]any {
	t.Helper()

	raw, ok := data(t, r)["errors"].([]any)
	require.True(t, ok, r.Body.String())

	out := make([]map[string]any, 0, len(raw))

	for _, e := range raw {
		m, ok := e.(map[string]any)
		require.True(t, ok)

		out = append(out, m)
	}

	return out
}

func (r *response) key() string {
	k, _ := r.body["errorKey"].(string)

	return k
}

func (h *harness) do(method, target, body string, headers ...string) *response {
	h.t.Helper()

	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequestWithContext(h.t.Context(), method, target, reader)
	req.RemoteAddr = "10.0.0.1:1234"

	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)

	out := &response{ResponseRecorder: rec, body: map[string]any{}}
	_ = json.Unmarshal(rec.Body.Bytes(), &out.body)

	return out
}

func claimsAt(iat, exp time.Time) *jwtx.Claims {
	return &jwtx.Claims{
		Subject: uuid.NewV7(), SessionID: uuid.NewV7(), Roles: []string{"user"}, EmailVerified: true,
		AuthTime: iat, IssuedAt: iat.Truncate(time.Second), Expires: exp.Truncate(time.Second), ID: "j",
	}
}
