// Package reqctx carries the per-request values set by the HTTP middleware chain.
package reqctx

import (
	"context"
	"time"
	"uuid"
)

type (
	requestIDKey struct{}
	principalKey struct{}
	operationKey struct{}
)

// Principal is the caller authenticated by an access token.
type Principal struct {
	Subject       uuid.UUID
	SessionID     uuid.UUID
	Roles         []string
	EmailVerified bool
	AuthTime      time.Time
}

// WithRequestID stores the request id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request id, or "".
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)

	return id
}

// WithPrincipal stores the authenticated caller.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalOf returns the authenticated caller, or nil for an anonymous request.
func PrincipalOf(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)

	return p
}

// WithOperation stores the operationId of the matched route.
func WithOperation(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, operationKey{}, id)
}

// Operation returns the operationId of the matched route, or "".
func Operation(ctx context.Context) string {
	id, _ := ctx.Value(operationKey{}).(string)

	return id
}
