package postgres

import (
	"context"
	"fmt"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

// Favorites implements repo.Favorites.
type Favorites struct {
	db *DB
}

// NewFavorites returns the favorites repository.
func NewFavorites(db *DB) *Favorites {
	return &Favorites{db: db}
}

var _ repo.Favorites = (*Favorites)(nil)

func toFavorite(f *sqlcgen.Favorite) *entity.Favorite {
	return &entity.Favorite{
		ID: int(f.ID), UserID: int(f.UserId), TargetType: f.TargetType, TargetID: f.TargetId,
		Title: f.Title, Image: f.Image, Extra: f.Extra, SubjectID: f.SubjectId, CreatedAt: f.CreatedAt,
	}
}

// Create implements repo.Favorites.
func (r *Favorites) Create(ctx context.Context, f *repo.NewFavorite) (*entity.Favorite, error) {
	var out *entity.Favorite

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		row, err := q.CreateFavorite(ctx, sqlcgen.CreateFavoriteParams{
			UserID: id32(f.UserID), TargetType: f.TargetType, TargetID: f.TargetID,
			Title: f.Title, Image: f.Image, Extra: f.Extra, SubjectID: f.SubjectID,
		})
		if err == nil {
			out = toFavorite(row)
		}

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return nil, fmt.Errorf("favorites: create: %w", err)
	}

	return out, nil
}

// List implements repo.Favorites.
func (r *Favorites) List(ctx context.Context, f repo.FavoriteFilter, offset, limit int64) ([]entity.Favorite, error) {
	var out []entity.Favorite

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		rows, err := q.ListFavorites(ctx, sqlcgen.ListFavoritesParams{
			UserID: id32(f.UserID), TargetType: nonEmpty(f.TargetType), SubjectID: f.SubjectID,
			RowOffset: offset, RowLimit: limit,
		})
		for _, row := range rows {
			out = append(out, *toFavorite(row))
		}

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return nil, fmt.Errorf("favorites: list: %w", err)
	}

	return out, nil
}

// Count implements repo.Favorites.
func (r *Favorites) Count(ctx context.Context, f repo.FavoriteFilter) (int, error) {
	var n int64

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		n, err = q.CountFavorites(ctx, sqlcgen.CountFavoritesParams{
			UserID: id32(f.UserID), TargetType: nonEmpty(f.TargetType), SubjectID: f.SubjectID,
		})

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return 0, fmt.Errorf("favorites: count: %w", err)
	}

	return int(n), nil
}

func (r *Favorites) one(ctx context.Context, get func(q *sqlcgen.Queries) (*sqlcgen.Favorite, error)) (*entity.Favorite, error) {
	out, err := findRow(ctx, r.db, get, toFavorite)
	if err != nil {
		return nil, fmt.Errorf("favorites: get: %w", err)
	}

	return out, nil
}

// Get implements repo.Favorites.
func (r *Favorites) Get(ctx context.Context, id int) (*entity.Favorite, error) {
	return r.one(ctx, func(q *sqlcgen.Queries) (*sqlcgen.Favorite, error) {
		return q.GetFavorite(ctx, id32(id)) //nolint:wrapcheck // wrapped by one
	})
}

// GetByTarget implements repo.Favorites.
func (r *Favorites) GetByTarget(ctx context.Context, userID int, targetType, targetID string) (*entity.Favorite, error) {
	return r.one(ctx, func(q *sqlcgen.Queries) (*sqlcgen.Favorite, error) {
		return q.GetFavoriteByTarget(ctx, sqlcgen.GetFavoriteByTargetParams{ //nolint:wrapcheck // wrapped by one
			UserID: id32(userID), TargetType: targetType, TargetID: targetID,
		})
	})
}

// Delete implements repo.Favorites.
func (r *Favorites) Delete(ctx context.Context, id int) error {
	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error { return q.DeleteFavorite(ctx, id32(id)) })
	if err != nil {
		return fmt.Errorf("favorites: delete: %w", err)
	}

	return nil
}

// RemoveByUser implements repo.Favorites.
func (r *Favorites) RemoveByUser(ctx context.Context, userID int) error {
	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error { return q.DeleteUserFavorites(ctx, id32(userID)) })
	if err != nil {
		return fmt.Errorf("favorites: remove by user: %w", err)
	}

	return nil
}
