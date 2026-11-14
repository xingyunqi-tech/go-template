// Package session manages sessions: rotation with a grace window for the replaced hash, and
// invalidation of the access-token cache.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
)

const (
	// RotatedHashGrace is how long a replaced refresh hash is still recognized (nestjs ROTATED_HASH_GRACE_MS).
	RotatedHashGrace = 60 * time.Second
	_hashBytes       = 32
)

// AuthCacheKey is the access-token guard cache key of a session.
func AuthCacheKey(sessionID int) string {
	return "jwt:session:" + strconv.Itoa(sessionID)
}

func rotatedKey(sessionID int) string {
	return "session:rotated:" + strconv.Itoa(sessionID)
}

// NewHash returns a random session hash.
func NewHash() (string, error) {
	b := make([]byte, _hashBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("session: random: %w", err)
	}

	sum := sha256.Sum256(b)

	return hex.EncodeToString(sum[:]), nil
}

// Service manages sessions.
type Service struct {
	repo  repo.Sessions
	cache repo.Cache
	tx    repo.Transactions
	log   *slog.Logger
}

// New returns a Service.
func New(sessions repo.Sessions, cache repo.Cache, tx repo.Transactions, log *slog.Logger) *Service {
	return &Service{repo: sessions, cache: cache, tx: tx, log: log}
}

// Create starts a session for the user.
func (s *Service) Create(ctx context.Context, userID int, hash string, userAgent, ip *string) (*entity.Session, error) {
	return s.repo.Create(ctx, userID, hash, userAgent, ip) //nolint:wrapcheck // repo errors are wrapped
}

// Get returns the active session, or nil.
func (s *Service) Get(ctx context.Context, id int) (*entity.Session, error) {
	return s.repo.Get(ctx, id) //nolint:wrapcheck // repo errors are wrapped
}

// ListActive returns the user's sessions.
func (s *Service) ListActive(ctx context.Context, userID int) ([]entity.Session, error) {
	return s.repo.ListActive(ctx, userID) //nolint:wrapcheck // repo errors are wrapped
}

// Rotate replaces current with next and remembers current for RotatedHashGrace.
func (s *Service) Rotate(ctx context.Context, id int, current, next string) (bool, error) {
	ok, err := s.repo.Rotate(ctx, id, current, next)
	if err != nil || !ok {
		return ok, err //nolint:wrapcheck // repo errors are wrapped
	}

	if err := s.cache.Set(ctx, rotatedKey(id), current, RotatedHashGrace); err != nil {
		return true, fmt.Errorf("session: remember rotated hash: %w", err)
	}

	return true, nil
}

// WasRecentlyRotatedFrom reports whether hash is the one most recently replaced.
func (s *Service) WasRecentlyRotatedFrom(ctx context.Context, id int, hash string) (bool, error) {
	var stored string

	found, err := s.cache.Get(ctx, rotatedKey(id), &stored)
	if err != nil {
		return false, fmt.Errorf("session: read rotated hash: %w", err)
	}

	return found && stored == hash, nil
}

// Forget clears the guard cache of the sessions now and again after the transaction commits.
func (s *Service) Forget(ctx context.Context, ids []int) error {
	forget := func(ctx context.Context) error {
		for _, id := range ids {
			if err := s.cache.Delete(ctx, AuthCacheKey(id)); err != nil {
				return fmt.Errorf("session: forget: %w", err)
			}
		}

		return nil
	}

	if err := forget(ctx); err != nil {
		return err
	}

	s.tx.AfterCommit(ctx, func(ctx context.Context) {
		if err := forget(ctx); err != nil {
			s.log.WarnContext(ctx, "clear session cache after commit failed", "error", err.Error())
		}
	})

	return nil
}

// RevokeForUser deletes the user's session and reports whether it existed.
func (s *Service) RevokeForUser(ctx context.Context, id, userID int) (bool, error) {
	ok, err := s.repo.DeleteForUser(ctx, id, userID)
	if err != nil || !ok {
		return ok, err //nolint:wrapcheck // repo errors are wrapped
	}

	return true, s.Forget(ctx, []int{id})
}

// Delete deletes the session.
func (s *Service) Delete(ctx context.Context, id int) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	return s.Forget(ctx, []int{id})
}

// DeleteAllForUser deletes the user's sessions, keeping except when set.
func (s *Service) DeleteAllForUser(ctx context.Context, userID int, except *int) error {
	ids, err := s.repo.DeleteAllForUser(ctx, userID, except)
	if err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	return s.Forget(ctx, ids)
}
