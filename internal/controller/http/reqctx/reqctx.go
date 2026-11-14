// Package reqctx carries per-request values set by the HTTP middleware chain.
package reqctx

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase/auth"
	"github.com/xingyunqi-tech/go-template/internal/usecase/file"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

type (
	requestIDKey struct{}
	bodyKey      struct{}
	validKey     struct{}
	principalKey struct{}
	refreshKey   struct{}
	clientKey    struct{}
)

// Validated is the transformed request body and query after ValidationPipe.
type Validated struct {
	Body  validatorx.Value
	Query validatorx.Value
}

// WithValidated stores the validated request values.
func WithValidated(ctx context.Context, v *Validated) context.Context {
	return context.WithValue(ctx, validKey{}, v)
}

// ValidatedValues returns the validated request values, or empty ones.
func ValidatedValues(ctx context.Context) *Validated {
	if v, ok := ctx.Value(validKey{}).(*Validated); ok {
		return v
	}

	return &Validated{Body: validatorx.Undefined{}, Query: validatorx.Undefined{}}
}

// WithPrincipal stores the access-token caller.
func WithPrincipal(ctx context.Context, p *auth.Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// Principal returns the access-token caller, or nil.
func Principal(ctx context.Context) *auth.Principal {
	p, _ := ctx.Value(principalKey{}).(*auth.Principal)

	return p
}

// WithRefresh stores the refresh-token caller.
func WithRefresh(ctx context.Context, p *auth.RefreshPrincipal) context.Context {
	return context.WithValue(ctx, refreshKey{}, p)
}

// Refresh returns the refresh-token caller, or nil.
func Refresh(ctx context.Context) *auth.RefreshPrincipal {
	p, _ := ctx.Value(refreshKey{}).(*auth.RefreshPrincipal)

	return p
}

// WithClient stores the client user agent and IP.
func WithClient(ctx context.Context, c auth.Client) context.Context {
	return context.WithValue(ctx, clientKey{}, c)
}

// Client returns the client user agent and IP.
func Client(ctx context.Context) auth.Client {
	c, _ := ctx.Value(clientKey{}).(auth.Client)

	return c
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

type uploadKey struct{}

// Upload is the file received by the upload endpoint; Received is nil when the request had none.
type Upload struct {
	Purpose  file.Purpose
	Received *file.Received
	Sink     repo.Sink
}

// WithUpload stores the received upload.
func WithUpload(ctx context.Context, u *Upload) context.Context {
	return context.WithValue(ctx, uploadKey{}, u)
}

// UploadOf returns the received upload.
func UploadOf(ctx context.Context) *Upload {
	u, _ := ctx.Value(uploadKey{}).(*Upload)

	return u
}
