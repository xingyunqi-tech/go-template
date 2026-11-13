// Package reqctx carries per-request values set by the HTTP middleware chain.
package reqctx

import (
	"context"

	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

type (
	requestIDKey struct{}
	bodyKey      struct{}
)

// WithRequestID stores the request id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request id, or "".
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)

	return id
}

// WithBody stores the parsed application/json request body.
func WithBody(ctx context.Context, body validatorx.Value) context.Context {
	return context.WithValue(ctx, bodyKey{}, body)
}

// Body returns the parsed application/json request body, or undefined for other requests.
func Body(ctx context.Context) validatorx.Value {
	if v := ctx.Value(bodyKey{}); v != nil {
		return v
	}

	return validatorx.Undefined{}
}
