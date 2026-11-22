// Package httperr writes errors as the error response of platform conventions §2.2.
package httperr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/entity"
)

const _serviceUnavailableRetry = 5

// ErrInvalidToken is the cause of a 401 for a request that carried an invalid or expired token.
var ErrInvalidToken = errors.New("invalid token")

// Translate returns the message of id in the request language with data filling it.
type Translate func(ctx context.Context, id string, data map[string]any) string

// Body is the error response.
type Body struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	ErrorKey  string `json:"errorKey"`
	Data      any    `json:"data"`
	RequestID string `json:"requestId"`
}

// ValidationData is the data of common.validationFailed.
type ValidationData struct {
	Errors []FieldError `json:"errors"`
}

// FieldError is one entry of ValidationData.
type FieldError struct {
	Field   string         `json:"field"`
	Rule    string         `json:"rule"`
	Message string         `json:"message"`
	Params  map[string]any `json:"params,omitempty"`
}

// Resolve maps err to its error key and response data: an entity.Error keeps its key, an
// entity.ValidationError becomes common.validationFailed, anything else common.internalError.
func Resolve(err error) (key string, data any) {
	var (
		domain     *entity.Error
		validation *entity.ValidationError
	)

	switch {
	case errors.As(err, &validation):
		return entity.KeyValidationFailed, validation
	case errors.As(err, &domain):
		if _, known := entity.Codes()[domain.Key]; known {
			return domain.Key, domain.Data
		}
	}

	return entity.KeyInternalError, nil
}

// Build returns the HTTP status and body of err.
func Build(ctx context.Context, err error, translate Translate) (int, *Body) {
	key, data := Resolve(err)
	code := entity.Codes()[key]

	if v, ok := data.(*entity.ValidationError); ok {
		data = validationData(ctx, v, translate)
	}

	return code.Status, &Body{
		Code: code.Code, Message: translate(ctx, key, nil), ErrorKey: key, Data: data, RequestID: reqctx.RequestID(ctx),
	}
}

func validationData(ctx context.Context, v *entity.ValidationError, translate Translate) ValidationData {
	out := ValidationData{Errors: make([]FieldError, 0, len(v.Errors))}

	for _, f := range v.Errors {
		out.Errors = append(out.Errors, FieldError{
			Field: f.Field, Rule: f.Rule, Message: translate(ctx, "validation."+f.Rule, f.Params), Params: f.Params,
		})
	}

	return out
}

// Write writes err with the headers its status requires: WWW-Authenticate for 401, Retry-After
// for 503 when the caller did not set one.
func Write(ctx context.Context, w http.ResponseWriter, err error, translate Translate) (int, error) {
	status, body := Build(ctx, err, translate)
	h := w.Header()

	switch status {
	case http.StatusUnauthorized:
		challenge := "Bearer"
		if errors.Is(err, ErrInvalidToken) {
			challenge += ` error="invalid_token"`
		}

		h.Set("WWW-Authenticate", challenge)
	case http.StatusServiceUnavailable:
		if h.Get("Retry-After") == "" {
			h.Set("Retry-After", strconv.Itoa(_serviceUnavailableRetry))
		}
	}

	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Del("Content-Length")
	w.WriteHeader(status)

	if encodeErr := json.NewEncoder(w).Encode(body); encodeErr != nil {
		return status, encodeErr //nolint:wrapcheck // the client went away
	}

	return status, nil
}
