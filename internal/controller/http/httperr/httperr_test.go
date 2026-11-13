package httperr_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/httperr"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

func translate(t *testing.T, lang string) httperr.Translate {
	t.Helper()

	tr, err := i18n.New("en")
	require.NoError(t, err)

	return func(key string) (string, bool) { return tr.Translate(lang, key) }
}

func data(t *testing.T, b httperr.Body) string {
	t.Helper()

	if b.Data == nil {
		return "null"
	}

	s, ok := validatorx.Stringify(b.Data)
	require.True(t, ok)

	return s
}

func nested(t *testing.T, raw string) *validatorx.Object {
	t.Helper()

	v, err := validatorx.ParseJSON([]byte(raw))
	require.NoError(t, err)

	o, ok := v.(*validatorx.Object)
	require.True(t, ok)

	return o
}

func TestBuildFollowsAllExceptionsFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		err     error
		status  int
		message string
		data    string
	}{
		{"custom message without key", httperr.New(404, "Cannot GET /x"), 404, "Cannot GET /x", "null"},
		{"custom message with key", httperr.New(403, "notFound"), 403, "Not found", "null"},
		{"error key", httperr.Keyed(404, "sessionNotFound"), 404, "Session not found", "null"},
		{"default message", httperr.Status(401), 401, "Unauthorized", "null"},
		{"status without i18n key", httperr.Status(413), 413, "PAYLOAD_TOO_LARGE", "null"},
		{"unknown status", httperr.Status(599), 599, "Error", "null"},
		{"invalid id", httperr.InvalidID(), 400, "Bad request", `{"id":"Invalid id"}`},
		{
			"errors with nested values", httperr.WithErrors(422, nested(t, `{"email":"notFound","n":1,"x":{"y":"notFound"},"raw":"plain text"}`)),
			422, "Validation failed", `{"email":"Not found","n":1,"x":{"y":"notFound"},"raw":"plain text"}`,
		},
		{
			"validation error", &validatorx.ValidationError{Status: 422, Errors: httperr.Field("email", "email must be an email")},
			422, "Validation failed", `{"email":"email must be an email"}`,
		},
		{
			"business error", entity.NewBusinessError("sessionTakenOver", entity.Detail{Name: "sessionId", Value: "notFound"}),
			409, "The exam has been continued on another device", `{"sessionId":"notFound","errorKey":"sessionTakenOver"}`,
		},
		{"unregistered business error", entity.NewBusinessError("nope"), 500, "Internal server error", "null"},
		{"too many requests", &httperr.TooManyRequests{Headers: nil}, 429, "Too many requests, please try again later", "null"},
		{"unknown error", errors.New("boom"), 500, "Internal server error", "null"},
		{"wrapped http error", fmt.Errorf("wrap: %w", httperr.Status(403)), 403, "Forbidden", "null"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b := httperr.Build(tt.err, translate(t, "en"))
			assert.Equal(t, tt.status, b.Status)
			assert.Equal(t, tt.message, b.Message)
			assert.JSONEq(t, tt.data, data(t, b))
		})
	}
}

func TestBuildMapsUniqueViolations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		constraint string
		status     int
		message    string
		data       string
	}{
		{"user_phone_key", 422, "Validation failed", `{"phone":"Phone number already exists"}`},
		{"UQ_e12875dfb3b1d92d7d7c5377e22", 422, "Validation failed", `{"email":"Email already exists"}`},
		{"UQ_favorite_user_target", 409, "Already in favorites", "null"},
		{"UQ_order_orderNo", 500, "Internal server error", "null"},
		{"unregistered", 409, "Conflict", "null"},
		{"", 409, "Conflict", "null"},
	}

	for _, tt := range tests {
		t.Run(tt.constraint, func(t *testing.T) {
			t.Parallel()

			err := fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23505", ConstraintName: tt.constraint})
			b := httperr.Build(err, translate(t, "en"))
			assert.Equal(t, tt.status, b.Status)
			assert.Equal(t, tt.message, b.Message)
			assert.JSONEq(t, tt.data, data(t, b))
		})
	}

	_, ok := httperr.UniqueViolation(&pgconn.PgError{Code: "23503"})
	assert.False(t, ok)
}

func TestBuildUsesRequestLanguage(t *testing.T) {
	t.Parallel()

	b := httperr.Build(httperr.Keyed(404, "sessionNotFound"), translate(t, "zh"))
	assert.Equal(t, "会话不存在", b.Message)

	b = httperr.Build(httperr.Status(400), nil)
	assert.Equal(t, "BAD_REQUEST", b.Message)
}

func TestWriteSendsHeadersAndEnvelope(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	headers := http.Header{"Retry-After": []string{"3"}}

	err := httperr.Write(rec, httperr.Build(&httperr.TooManyRequests{Headers: headers}, translate(t, "en")))
	require.NoError(t, err)
	assert.Equal(t, 429, rec.Code)
	assert.Equal(t, "3", rec.Header().Get("Retry-After"))
	assert.JSONEq(t, `{"code":429,"message":"Too many requests, please try again later","data":null}`, rec.Body.String())
}

func TestErrorText(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "404 Cannot GET /", httperr.New(404, "Cannot GET /").Error())
	assert.Equal(t, "404 sessionNotFound", httperr.Keyed(404, "sessionNotFound").Error())
	assert.Equal(t, "401 Unauthorized", httperr.Status(401).Error())
	assert.Equal(t, "business error: x", entity.NewBusinessError("x").Error())
	assert.Contains(t, (&httperr.TooManyRequests{Headers: nil}).Error(), "Too Many Requests")
}
