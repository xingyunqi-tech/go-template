//go:build parity

package parity_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const _consentsURL = "/api/v1/me/consents"

func hideAgreedAt(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			if k == "agreedAt" && item != nil {
				out[k] = "<time>"
			} else {
				out[k] = hideAgreedAt(item)
			}
		}

		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = hideAgreedAt(item)
		}

		return out
	default:
		return v
	}
}

func requireSameConsents(t *testing.T, p pair) {
	t.Helper()

	require.Equal(t, p.Nestjs.Status, p.Go.Status, "go: %s\nnestjs: %s", p.Go.Body, p.Nestjs.Body)
	require.Equal(t, hideAgreedAt(p.Nestjs.JSON(t)), hideAgreedAt(p.Go.JSON(t)))
}

func TestConsents(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "consent")
	requireSameConsents(t, do(t, http.MethodGet, _consentsURL, withTokens(tokens)))

	bodies := []string{
		`{}`,
		`{"type":"PRIVACY_POLICY","version":"2026-10-01"}`,
		`{"type":"AI_SERVICE","version":"` + strings.Repeat("v", 51) + `"}`,
		`{"type":"NOPE","version":""}`,
		`{"type":1,"version":2}`,
		`{"type":"USER_AGREEMENT","version":"  "}`,
	}

	for _, lang := range []string{"en", "zh"} {
		for _, body := range bodies {
			requireSameConsents(t, do(t, http.MethodPost, _consentsURL, withTokens(tokens), rawJSON(body), withHeader("Accept-Language", lang)))
		}
	}
}

func TestConsentsUnauthenticated(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		requireSameConsents(t, do(t, method, _consentsURL, rawJSON(`{}`)))
	}
}
