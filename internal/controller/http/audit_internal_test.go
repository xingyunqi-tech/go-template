package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/httperr"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase/auth"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

type recordingAuditor struct {
	logs []repo.AuditLog
}

func (a *recordingAuditor) Log(_ context.Context, l *repo.AuditLog) {
	a.logs = append(a.logs, *l)
}

func auditRequest(t *testing.T, params map[string]string, body *validatorx.Object) *http.Request {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/x", http.NoBody)
	r.Header.Set("User-Agent", "ua")
	r.Header.Set("X-Request-Id", "req-9")
	r.Header.Set("X-Forwarded-For", " 203.0.113.7 , 10.0.0.1")

	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}

	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	ctx = reqctx.WithPrincipal(ctx, &auth.Principal{UserID: 5, SessionID: 1, RoleID: nil})
	ctx = reqctx.WithValidated(ctx, &reqctx.Validated{Body: body, Query: validatorx.Undefined{}})

	return r.WithContext(ctx)
}

func TestAuditedRecordsSuccessfulOperations(t *testing.T) {
	t.Parallel()

	body := validatorx.NewObject()
	body.Set("name", "n")
	body.Set("password", "p")

	cases := []struct {
		name     string
		spec     Audit
		params   map[string]string
		resp     any
		entityID string
		value    map[string]any
	}{
		{
			"param id and body",
			Audit{Action: "UPDATE", EntityType: "User"},
			map[string]string{"id": "7"},
			map[string]any{"id": 8},
			"7",
			map[string]any{"name": "n", "password": "p"},
		},
		{
			"response id fallback",
			Audit{Action: "CREATE", EntityType: "User"},
			nil,
			map[string]any{"id": 8},
			"8",
			map[string]any{"name": "n", "password": "p"},
		},
		{
			"response first",
			Audit{Action: "CREATE", EntityType: "User", FromResponse: true},
			map[string]string{"id": "7"},
			map[string]any{"id": "u-1"},
			"u-1",
			map[string]any{"name": "n", "password": "p"},
		},
		{
			"joined params and response value",
			Audit{
				Action: "UPDATE", EntityType: "Exam", IDParams: []string{"examId", "id"}, IncludeResponse: true,
			},
			map[string]string{"examId": "a%2Fb", "id": "3"},
			map[string]any{"id": 1, "ok": true},
			"a/b:3",
			map[string]any{"id": float64(1), "ok": true},
		},
		{"no ids", Audit{Action: "DELETE", EntityType: "User", OmitBody: true}, nil, nil, "unknown", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := &recordingAuditor{}
			spec := tc.spec
			mw := audited(a, map[string]Policy{"op": {Audit: &spec}})

			handler := mw(func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) { return tc.resp, nil }, "op")
			_, err := handler(t.Context(), httptest.NewRecorder(), auditRequest(t, tc.params, body), nil)
			require.NoError(t, err)

			require.Len(t, a.logs, 1)
			l := a.logs[0]
			assert.Equal(t, tc.entityID, l.EntityID)
			assert.Equal(t, tc.value, l.NewValue)
			assert.Equal(t, spec.Action, l.Action)
			assert.Equal(t, "5", *l.UserID)
			assert.Equal(t, "203.0.113.7", *l.IPAddress)
			assert.Equal(t, "ua", *l.UserAgent)
			assert.Equal(t, "req-9", *l.RequestID)
			assert.Nil(t, l.OldValue)
		})
	}
}

func TestAuditedSkipsFailuresAndUnauditedOperations(t *testing.T) {
	t.Parallel()

	a := &recordingAuditor{}
	mw := audited(a, map[string]Policy{"op": {Audit: &Audit{Action: "DELETE", EntityType: "User"}}, "plain": {}})

	failing := mw(func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) {
		return nil, httperr.Status(http.StatusNotFound)
	}, "op")
	_, err := failing(t.Context(), httptest.NewRecorder(), auditRequest(t, nil, nil), nil)
	require.Error(t, err)

	plain := mw(func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) { return nil, nil }, "plain")
	_, err = plain(t.Context(), httptest.NewRecorder(), auditRequest(t, nil, nil), nil)
	require.NoError(t, err)

	assert.Empty(t, a.logs)
}
