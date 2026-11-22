package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"time"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/pkg/clientip"
	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
)

const (
	_idempotencyTTL  = 24 * time.Hour
	_defaultClaimTTL = time.Minute
)

var errRolledBack = errors.New("transaction rolled back by the database")

// buffered holds a response until the wrapping middleware decides to send it.
type buffered struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBuffered(w http.ResponseWriter) *buffered {
	return &buffered{header: w.Header().Clone(), status: 0, body: bytes.Buffer{}}
}

func (b *buffered) Header() http.Header { return b.header }

func (b *buffered) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}

func (b *buffered) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}

	return b.body.Write(p) //nolint:wrapcheck // in-memory buffer
}

func (b *buffered) statusOr200() int {
	if b.status == 0 {
		return http.StatusOK
	}

	return b.status
}

// flush sends the buffered response to w.
func (b *buffered) flush(w http.ResponseWriter) {
	h := w.Header()
	for k := range h {
		delete(h, k)
	}

	maps.Copy(h, b.header)
	w.WriteHeader(b.statusOr200())
	_, _ = w.Write(b.body.Bytes())
}

// idempotent replays the first 2xx or 4xx response of a repeated Idempotency-Key.
func (p *pipeline) idempotent(op *operation, next http.Handler) http.Handler {
	if !op.idempotent || p.opts.Idempotency == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Idempotency-Key")
		if header == "" {
			next.ServeHTTP(w, r)

			return
		}

		key := p.idempotencyKey(r, header)
		sum := sha256.Sum256(bodyOf(r.Context()))
		hash := hex.EncodeToString(sum[:])

		owner := uuid.NewV7().String()

		existing, err := p.opts.Idempotency.Begin(r.Context(), key, hash, owner, p.opts.IdempotencyClaimTTL)
		if err != nil {
			p.errs.write(w, r, entity.Wrap(entity.KeyServiceUnavailable, err))

			return
		}

		if existing == nil {
			p.runIdempotent(w, r, next, key, hash, owner)

			return
		}

		p.replay(w, r, existing, hash)
	})
}

// replay answers a repeated key: the stored response, or why it cannot be replayed.
func (p *pipeline) replay(w http.ResponseWriter, r *http.Request, existing *repo.IdempotencyRecord, hash string) {
	switch {
	case existing.Hash != hash:
		p.errs.write(w, r, entity.New(entity.KeyIdempotencyKeyMismatch))
	case !existing.Done:
		p.errs.write(w, r, entity.New(entity.KeyIdempotencyInProgress))
	default:
		w.Header().Set("Content-Type", existing.ContentType)
		w.WriteHeader(existing.Status)
		_, _ = w.Write(existing.Body) //nolint:gosec // replays the response this service produced for the key
	}
}

// runIdempotent stores a 2xx or 4xx response for the key and releases it after a 5xx or a panic;
// a response that fails to be stored keeps the claim until its TTL.
func (p *pipeline) runIdempotent(w http.ResponseWriter, r *http.Request, next http.Handler, key, hash, owner string) {
	buf := newBuffered(w)
	finished := false

	defer func() {
		if !finished {
			p.releaseKey(r, key, owner)
		}
	}()

	next.ServeHTTP(buf, r)

	finished = true

	if status := buf.statusOr200(); status < http.StatusInternalServerError {
		rec := &repo.IdempotencyRecord{
			Hash: hash, Owner: "", Done: true, Status: status, ContentType: buf.header.Get("Content-Type"), Body: buf.body.Bytes(),
		}

		if err := p.opts.Idempotency.Complete(context.WithoutCancel(r.Context()), key, owner, rec, _idempotencyTTL); err != nil {
			p.opts.Log.WarnContext(r.Context(), "store idempotency key", "error", err.Error())
		}
	} else {
		p.releaseKey(r, key, owner)
	}

	buf.flush(w)
}

func (p *pipeline) releaseKey(r *http.Request, key, owner string) {
	if err := p.opts.Idempotency.Release(context.WithoutCancel(r.Context()), key, owner); err != nil {
		p.opts.Log.WarnContext(r.Context(), "release idempotency key", "error", err.Error())
	}
}

func (p *pipeline) idempotencyKey(r *http.Request, header string) string {
	subject := ratelimit.IPSubject(clientip.Subject(p.opts.TrustedProxies.ClientIP(r)))
	if principal := reqctx.PrincipalOf(r.Context()); principal != nil {
		subject = ratelimit.UserSubject(principal.Subject.String())
	}

	return p.opts.ServiceName + ":idem:" + subject + ":" + r.Method + ":" + r.URL.Path + ":" + header
}

// transactional runs the handler in a request transaction, committed for a 2xx or 4xx response
// and rolled back for a 5xx; the response is sent after the commit. A 4xx whose transaction the
// database aborted is sent as is.
func (p *pipeline) transactional(op *operation, next http.Handler) http.Handler {
	if !op.policy.Tx || p.opts.Transactor == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, tx, err := p.opts.Transactor.Begin(r.Context())
		if err != nil {
			p.errs.write(w, r, entity.Wrap(entity.KeyServiceUnavailable, err))

			return
		}

		buf := newBuffered(w)
		done := false

		defer func() {
			if !done {
				tx.Rollback(ctx)
			}
		}()

		next.ServeHTTP(buf, r.WithContext(ctx))

		done = true

		if buf.statusOr200() >= http.StatusInternalServerError {
			tx.Rollback(ctx)
			buf.flush(w)

			return
		}

		committed, err := tx.Commit(ctx)
		if err == nil && !committed && buf.statusOr200() < http.StatusBadRequest {
			err = errRolledBack
		}

		if err != nil {
			p.errs.write(w, r, err)

			return
		}

		buf.flush(w)
	})
}

// audited writes the audit log entry of a 2xx response, in the request transaction when there
// is one.
func (p *pipeline) audited(op *operation, next http.Handler) http.Handler {
	if op.policy.Audit == nil || p.opts.Auditor == nil {
		return next
	}

	spec := op.policy.Audit

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := newBuffered(w)
		target := &reqctx.AuditTarget{ResourceID: uuid.Nil(), ActorSub: uuid.Nil()}
		ctx := reqctx.WithAuditTarget(r.Context(), target)
		r = r.WithContext(ctx)

		next.ServeHTTP(buf, r)

		if status := buf.statusOr200(); status >= http.StatusOK && status < http.StatusMultipleChoices {
			l := p.auditLog(r, op, spec, buf.body.Bytes())
			target.Apply(l)

			if err := p.opts.Auditor.Log(ctx, l); err != nil {
				p.errs.write(w, r, err)

				return
			}
		}

		buf.flush(w)
	})
}

func (p *pipeline) auditLog(r *http.Request, op *operation, spec *Audit, body []byte) *repo.AuditLog {
	l := &repo.AuditLog{
		ActorSub:     nil,
		Action:       spec.Action,
		ResourceType: spec.Resource,
		ResourceID:   pathParam(r.Context(), "id"),
		RequestID:    reqctx.RequestID(r.Context()),
		IP:           p.opts.TrustedProxies.ClientIP(r),
		Data:         map[string]any{"operationId": op.id},
	}

	if principal := reqctx.PrincipalOf(r.Context()); principal != nil {
		sub := principal.Subject
		l.ActorSub = &sub
	}

	if l.ResourceID == "" {
		var envelope struct {
			Data struct {
				ID uuid.UUID `json:"id"`
			} `json:"data"`
		}

		if json.Unmarshal(body, &envelope) == nil && envelope.Data.ID != uuid.Nil() {
			l.ResourceID = envelope.Data.ID.String()
		}
	}

	return l
}
