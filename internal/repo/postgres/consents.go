package postgres

import (
	"context"
	"fmt"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

// Consents implements repo.Consents.
type Consents struct {
	db *DB
}

// NewConsents returns the consent repository.
func NewConsents(db *DB) *Consents {
	return &Consents{db: db}
}

var _ repo.Consents = (*Consents)(nil)

func toConsent(c *sqlcgen.UserConsent) entity.UserConsent {
	return entity.UserConsent{
		ID: c.ID, UserID: int(c.UserId), Type: c.Type, Version: c.Version, AgreedAt: c.AgreedAt, IP: c.Ip,
	}
}

// InsertIfAbsent records the consent unless the same version is already recorded.
func (r *Consents) InsertIfAbsent(ctx context.Context, c *entity.UserConsent) error {
	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		return q.InsertConsentIfAbsent(ctx, sqlcgen.InsertConsentIfAbsentParams{
			ID: uuid.NewV7(), UserID: id32(c.UserID), Type: c.Type, Version: c.Version, Ip: c.IP,
		})
	})
	if err != nil {
		return fmt.Errorf("consents: insert: %w", err)
	}

	return nil
}

// Latest returns the most recent consent of each type.
func (r *Consents) Latest(ctx context.Context, userID int) ([]entity.UserConsent, error) {
	var out []entity.UserConsent

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		rows, err := q.ListLatestConsents(ctx, id32(userID))
		for _, row := range rows {
			out = append(out, toConsent(row))
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("consents: latest: %w", err)
	}

	return out, nil
}

// Get returns the consent to that version, or nil.
func (r *Consents) Get(ctx context.Context, userID int, consentType, version string) (*entity.UserConsent, error) {
	var out *entity.UserConsent

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		row, err := q.GetConsent(ctx, sqlcgen.GetConsentParams{UserID: id32(userID), Type: consentType, Version: version})
		if notFound(err) {
			return nil
		}

		if err == nil {
			c := toConsent(row)
			out = &c
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("consents: get: %w", err)
	}

	return out, nil
}

// Exists reports whether the user agreed to that version.
func (r *Consents) Exists(ctx context.Context, userID int, consentType, version string) (bool, error) {
	var out bool

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		out, err = q.ConsentExists(ctx, sqlcgen.ConsentExistsParams{UserID: id32(userID), Type: consentType, Version: version})

		return err
	})
	if err != nil {
		return false, fmt.Errorf("consents: exists: %w", err)
	}

	return out, nil
}
