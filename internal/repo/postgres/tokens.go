package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

// Tokens implements repo.Tokens.
type Tokens struct {
	db *DB
}

// NewTokens returns Tokens.
func NewTokens(db *DB) *Tokens {
	return &Tokens{db: db}
}

var _ repo.Tokens = (*Tokens)(nil)

// Issue implements repo.Tokens.
func (r *Tokens) Issue(ctx context.Context, userID uuid.UUID, purpose string, hash []byte, expires time.Time) error {
	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		if err := q.InvalidateUserTokens(ctx, sqlcgen.InvalidateUserTokensParams{UserID: userID, Purpose: purpose}); err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		return q.CreateUserToken(ctx, sqlcgen.CreateUserTokenParams{ //nolint:wrapcheck // wrapped below
			ID: uuid.NewV7(), UserID: userID, Purpose: purpose, TokenHash: hash, ExpiresAt: expires,
		})
	})
	if err != nil {
		return fmt.Errorf("postgres: issue token: %w", err)
	}

	return nil
}

// Consume implements repo.Tokens.
func (r *Tokens) Consume(ctx context.Context, purpose string, hash []byte) (uuid.UUID, bool, error) {
	var userID uuid.UUID

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		userID, err = q.ConsumeUserToken(ctx, sqlcgen.ConsumeUserTokenParams{TokenHash: hash, Purpose: purpose})

		return err //nolint:wrapcheck // wrapped below
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return uuid.Nil(), false, nil
	case err != nil:
		return uuid.Nil(), false, fmt.Errorf("postgres: consume token: %w", err)
	}

	return userID, true, nil
}
