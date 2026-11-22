// Package reqctx carries the per-request values set by the HTTP middleware chain.
package reqctx

import (
	"context"
	"time"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

type (
	requestIDKey struct{}
	principalKey struct{}
	operationKey struct{}
	clientKey    struct{}
	auditKey     struct{}
)

// AuditTarget lets a handler name the user an audited operation acted on when neither the path
// nor the response carries it.
type AuditTarget struct {
	ResourceID uuid.UUID
	ActorSub   uuid.UUID
}

// WithAuditTarget stores the target an audited operation fills.
func WithAuditTarget(ctx context.Context, t *AuditTarget) context.Context {
	return context.WithValue(ctx, auditKey{}, t)
}

// SetAuditTarget records the resource and, for an anonymous caller, the actor of the audit
// entry; uuid.Nil keeps the default. Outside an audited operation it does nothing.
func SetAuditTarget(ctx context.Context, resource, actor uuid.UUID) {
	if t, ok := ctx.Value(auditKey{}).(*AuditTarget); ok {
		t.ResourceID, t.ActorSub = resource, actor
	}
}

// Apply overrides the resource id and the missing actor of l with the recorded target.
func (t *AuditTarget) Apply(l *repo.AuditLog) {
	if t.ResourceID != uuid.Nil() {
		l.ResourceID = t.ResourceID.String()
	}

	if l.ActorSub == nil && t.ActorSub != uuid.Nil() {
		actor := t.ActorSub
		l.ActorSub = &actor
	}
}

// Client is the address and user agent of the caller.
type Client struct {
	IP        string
	UserAgent string
}

// WithClient stores the caller's address and user agent.
func WithClient(ctx context.Context, c *Client) context.Context {
	return context.WithValue(ctx, clientKey{}, c)
}

// ClientOf returns the caller's address and user agent; empty outside the pipeline.
func ClientOf(ctx context.Context) Client {
	if c, ok := ctx.Value(clientKey{}).(*Client); ok {
		return *c
	}

	return Client{IP: "", UserAgent: ""}
}

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
