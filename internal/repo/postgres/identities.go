package postgres

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

// Identities implements repo.Identities.
type Identities struct {
	db *DB
}

// NewIdentities returns Identities.
func NewIdentities(db *DB) *Identities {
	return &Identities{db: db}
}

var _ repo.Identities = (*Identities)(nil)

func (r *Identities) exec(ctx context.Context, op string, fn func(q *sqlcgen.Queries) error) error {
	if err := r.db.queries(ctx, fn); err != nil {
		return fmt.Errorf("postgres: %s identity: %w", op, err)
	}

	return nil
}

// Lock implements repo.Identities.
func (r *Identities) Lock(ctx context.Context, key string) error {
	return r.exec(ctx, "lock", func(q *sqlcgen.Queries) error { return q.LockIdentityKey(ctx, "identity:"+key) })
}

// Find implements repo.Identities.
func (r *Identities) Find(ctx context.Context, provider, subject string) (userID uuid.UUID, missingUnion, found bool, err error) {
	var row *sqlcgen.UserIdentity

	err = r.exec(ctx, "get", func(q *sqlcgen.Queries) error {
		var err error

		row, err = q.GetIdentity(ctx, sqlcgen.GetIdentityParams{Provider: provider, Subject: subject})

		return err //nolint:wrapcheck // wrapped by exec
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return uuid.Nil(), false, false, nil
	case err != nil:
		return uuid.Nil(), false, false, err
	}

	return row.UserID, row.UnionID == nil, true, nil
}

// FindByUnion implements repo.Identities.
func (r *Identities) FindByUnion(ctx context.Context, unionID string, providers []string) (uuid.UUID, bool, error) {
	var row *sqlcgen.UserIdentity

	err := r.exec(ctx, "get", func(q *sqlcgen.Queries) error {
		var err error

		row, err = q.GetIdentityByUnionID(ctx, sqlcgen.GetIdentityByUnionIDParams{UnionID: &unionID, Providers: providers})

		return err //nolint:wrapcheck // wrapped by exec
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return uuid.Nil(), false, nil
	case err != nil:
		return uuid.Nil(), false, err
	}

	return row.UserID, true, nil
}

func optional(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}

// Create implements repo.Identities.
func (r *Identities) Create(ctx context.Context, userID uuid.UUID, id *entity.ExternalIdentity) error {
	return r.exec(ctx, "create", func(q *sqlcgen.Queries) error {
		return q.CreateIdentity(ctx, sqlcgen.CreateIdentityParams{
			ID: uuid.NewV7(), UserID: userID, Provider: id.Provider, Subject: id.Subject, UnionID: optional(id.UnionID),
		})
	})
}

// SetUnion implements repo.Identities.
func (r *Identities) SetUnion(ctx context.Context, provider, subject, unionID string) error {
	return r.exec(ctx, "update", func(q *sqlcgen.Queries) error {
		return q.SetIdentityUnionID(ctx, sqlcgen.SetIdentityUnionIDParams{Provider: provider, Subject: subject, UnionID: &unionID})
	})
}

// DeleteAll implements repo.Identities.
func (r *Identities) DeleteAll(ctx context.Context, userID uuid.UUID) error {
	return r.exec(ctx, "delete", func(q *sqlcgen.Queries) error { return q.DeleteUserIdentities(ctx, userID) })
}
