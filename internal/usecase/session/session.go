// Package session lists and revokes the sessions of a user and writes the revocation keys every
// service checks (platform conventions §5.1).
package session

import (
	"context"
	"time"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
)

// _revocationSlack keeps a revocation key past the last access token of the session.
const _revocationSlack = 60 * time.Second

// Revoker writes the auth:revoked:sid:{sid} keys.
type Revoker interface {
	Revoke(ctx context.Context, sid uuid.UUID, ttl time.Duration) error
}

// Revocation revokes sessions in the database and in the revocation keys.
type Revocation struct {
	sessions repo.Sessions
	revoker  Revoker
	ttl      time.Duration
}

// NewRevocation returns a Revocation whose keys outlive access tokens of accessTTL.
func NewRevocation(sessions repo.Sessions, revoker Revoker, accessTTL time.Duration) *Revocation {
	return &Revocation{sessions: sessions, revoker: revoker, ttl: accessTTL + _revocationSlack}
}

// Key writes the revocation key of sid; a Redis failure is 503.
func (r *Revocation) Key(ctx context.Context, sid uuid.UUID) error {
	if err := r.revoker.Revoke(ctx, sid, r.ttl); err != nil {
		return entity.Wrap(entity.KeyServiceUnavailable, err)
	}

	return nil
}

// One revokes the active session sid of userID; found is false when there is none.
func (r *Revocation) One(ctx context.Context, userID, sid uuid.UUID) (bool, error) {
	found, err := r.sessions.Revoke(ctx, userID, sid)
	if err != nil || !found {
		return found, err //nolint:wrapcheck // repo errors are wrapped
	}

	return true, r.Key(ctx, sid)
}

// All revokes the active sessions of userID except keep.
func (r *Revocation) All(ctx context.Context, userID uuid.UUID, keep *uuid.UUID) error {
	ids, err := r.sessions.RevokeAll(ctx, userID, keep)
	if err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	for _, sid := range ids {
		if err := r.Key(ctx, sid); err != nil {
			return err
		}
	}

	return nil
}

// Service implements usecase.Sessions.
type Service struct {
	sessions   repo.Sessions
	revocation *Revocation
}

var _ usecase.Sessions = (*Service)(nil)

// New returns a Service.
func New(sessions repo.Sessions, revocation *Revocation) *Service {
	return &Service{sessions: sessions, revocation: revocation}
}

// List implements usecase.Sessions.
func (s *Service) List(ctx context.Context, caller *usecase.Caller) ([]usecase.SessionView, error) {
	sessions, err := s.sessions.ListActive(ctx, caller.UserID)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	out := make([]usecase.SessionView, 0, len(sessions))
	for i := range sessions {
		out = append(out, usecase.SessionView{Session: sessions[i], Current: sessions[i].ID == caller.SessionID})
	}

	return out, nil
}

// Revoke implements usecase.Sessions.
func (s *Service) Revoke(ctx context.Context, caller *usecase.Caller, id uuid.UUID) error {
	found, err := s.revocation.One(ctx, caller.UserID, id)
	if err != nil {
		return err
	}

	if !found {
		return entity.New(entity.KeySessionNotFound)
	}

	return nil
}

// RevokeOthers implements usecase.Sessions.
func (s *Service) RevokeOthers(ctx context.Context, caller *usecase.Caller) error {
	return s.revocation.All(ctx, caller.UserID, &caller.SessionID)
}
