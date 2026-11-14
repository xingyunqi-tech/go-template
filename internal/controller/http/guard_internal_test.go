package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/usecase/auth"
)

func TestBearerFollowsPassportJWT(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"Bearer abc":       "abc",
		"bearer abc":       "abc",
		"BEARER   abc def": "abc",
		"Basic abc":        "",
		"Bearer":           "",
		"":                 "",
	}

	for header, want := range cases {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		r.Header.Set("Authorization", header)
		assert.Equal(t, want, bearer(r), header)
	}
}

type stubAuth struct {
	principal *auth.Principal
	err       error
}

func (s stubAuth) Authenticate(context.Context, string) (*auth.Principal, error) {
	return s.principal, s.err
}

func (s stubAuth) AuthenticateRefresh(token string) (*auth.RefreshPrincipal, error) {
	if s.err != nil {
		return nil, s.err
	}

	return &auth.RefreshPrincipal{SessionID: 1, Hash: token}, nil
}

func request(t *testing.T, token string) *http.Request {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}

	return r
}

func TestAuthGuards(t *testing.T) {
	t.Parallel()

	role := 2
	ok := AuthGuards(stubAuth{principal: &auth.Principal{UserID: 1, SessionID: 2, RoleID: &role}, err: nil})
	denied := AuthGuards(stubAuth{principal: nil, err: entity.Unauthorized("Session expired")})

	r, err := ok[AuthJWT](request(t, "t"))
	require.NoError(t, err)
	assert.Equal(t, 1, reqctx.Principal(r.Context()).UserID)

	_, err = ok[AuthJWT](request(t, ""))
	require.Error(t, err)

	_, err = denied[AuthJWT](request(t, "t"))
	assert.EqualError(t, err, "Session expired")

	r, err = denied[AuthOptionalJWT](request(t, "t"))
	require.NoError(t, err)
	assert.Nil(t, reqctx.Principal(r.Context()))

	r, err = ok[AuthOptionalJWT](request(t, "t"))
	require.NoError(t, err)
	assert.NotNil(t, reqctx.Principal(r.Context()))

	r, err = ok[AuthOptionalJWT](request(t, ""))
	require.NoError(t, err)
	assert.Nil(t, reqctx.Principal(r.Context()))

	r, err = ok[AuthRefresh](request(t, "h"))
	require.NoError(t, err)
	assert.Equal(t, "h", reqctx.Refresh(r.Context()).Hash)

	_, err = ok[AuthRefresh](request(t, ""))
	require.Error(t, err)

	_, err = denied[AuthRefresh](request(t, "h"))
	require.Error(t, err)
}

type stubPermissions map[int][]string

func (s stubPermissions) ForRole(_ context.Context, role int) ([]string, error) {
	if role < 0 {
		return nil, errors.New("boom")
	}

	return s[role], nil
}

func TestRequirePermissions(t *testing.T) {
	t.Parallel()

	guard := RequirePermissions(stubPermissions{2: {"a", "b"}})
	withRole := func(role *int) *http.Request {
		r := request(t, "")

		return r.WithContext(reqctx.WithPrincipal(r.Context(), &auth.Principal{UserID: 1, SessionID: 1, RoleID: role}))
	}
	role, broken := 2, -1

	cases := []struct {
		name   string
		r      *http.Request
		policy Policy
		want   string
	}{
		{"granted", withRole(&role), Policy{Permissions: []string{"a", "b"}}, ""},
		{"missing", withRole(&role), Policy{Permissions: []string{"a", "c", "d"}}, "Missing permissions: c, d"},
		{"any granted", withRole(&role), Policy{Permissions: []string{"c", "b"}, AnyPermission: true}, ""},
		{"any missing", withRole(&role), Policy{Permissions: []string{"c", "d"}, AnyPermission: true}, "Missing any of permissions: c, d"},
		{"undeclared", withRole(&role), Policy{RBAC: true}, "No permission declared for this route"},
		{"anonymous", request(t, ""), Policy{Permissions: []string{"a"}}, "User not authenticated"},
		{"no role", withRole(nil), Policy{Permissions: []string{"a"}}, "User role not found"},
		{"lookup fails", withRole(&broken), Policy{Permissions: []string{"a"}}, "boom"},
	}

	for _, c := range cases {
		err := guard(c.r, c.policy)
		if c.want == "" {
			assert.NoError(t, err, c.name)
		} else {
			assert.EqualError(t, err, c.want, c.name)
		}
	}
}

func TestClientInfoFollowsExpressTrustProxy(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		hops      int
		forwarded string
		want      string
	}{
		{0, "1.1.1.1, 2.2.2.2", "10.0.0.1"},
		{1, "1.1.1.1, 2.2.2.2", "2.2.2.2"},
		{2, "1.1.1.1, 2.2.2.2", "1.1.1.1"},
		{5, "1.1.1.1, 2.2.2.2", "1.1.1.1"},
		{1, "", "10.0.0.1"},
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		r.RemoteAddr = "10.0.0.1:5000"
		r.Header.Set("X-Forwarded-For", c.forwarded)
		r.Header.Set("User-Agent", strings.Repeat("\u00e9", 600))

		var got auth.Client

		clientInfo(c.hops)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got = reqctx.Client(r.Context())
		})).ServeHTTP(httptest.NewRecorder(), r)

		require.NotNil(t, got.IP)
		assert.Equal(t, c.want, *got.IP)
		assert.Len(t, []rune(*got.UserAgent), 512)
	}
}

func TestTruncateUTF16CountsCodeUnits(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "abc", truncateUTF16("abc", 5))
	assert.Equal(t, "ab", truncateUTF16("abc", 2))
	assert.Equal(t, "\U0001F600", truncateUTF16("\U0001F600\U0001F600", 3))
	assert.Equal(t, 256, len([]rune(truncateUTF16(strings.Repeat("\U0001F600", 300), 512))))
}
