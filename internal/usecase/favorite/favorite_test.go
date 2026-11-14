package favorite_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase/favorite"
)

type fakeFavorites struct {
	repo.Favorites

	rows []entity.Favorite
}

func (f *fakeFavorites) Create(_ context.Context, n *repo.NewFavorite) (*entity.Favorite, error) {
	row := entity.Favorite{ID: len(f.rows) + 1, UserID: n.UserID, TargetType: n.TargetType, TargetID: n.TargetID}
	f.rows = append(f.rows, row)

	return &row, nil
}

func (f *fakeFavorites) Get(_ context.Context, id int) (*entity.Favorite, error) {
	for i := range f.rows {
		if f.rows[i].ID == id {
			return &f.rows[i], nil
		}
	}

	return nil, nil //nolint:nilnil // not found
}

func (f *fakeFavorites) GetByTarget(_ context.Context, userID int, targetType, targetID string) (*entity.Favorite, error) {
	for i := range f.rows {
		if r := f.rows[i]; r.UserID == userID && r.TargetType == targetType && r.TargetID == targetID {
			return &f.rows[i], nil
		}
	}

	return nil, nil //nolint:nilnil // not found
}

func (f *fakeFavorites) Delete(_ context.Context, id int) error {
	for i := range f.rows {
		if f.rows[i].ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)

			break
		}
	}

	return nil
}

func TestNormalizeTargetIDLowercasesExamUUIDs(t *testing.T) {
	t.Parallel()

	upper := "00000000-0000-4000-8000-00000000000A"
	assert.Equal(t, "00000000-0000-4000-8000-00000000000a", favorite.NormalizeTargetID(entity.FavoriteQuestion, upper))
	assert.Equal(t, "00000000-0000-4000-8000-00000000000a", favorite.NormalizeTargetID(entity.FavoritePaper, upper))
	assert.Equal(t, upper, favorite.NormalizeTargetID(entity.FavoriteProduct, upper))
	assert.Equal(t, "Q1", favorite.NormalizeTargetID(entity.FavoriteQuestion, "Q1"))
}

func TestToggleAndOwnership(t *testing.T) {
	t.Parallel()

	f := &fakeFavorites{Favorites: nil, rows: nil}
	s := favorite.New(f)
	in := &favorite.Input{TargetType: entity.FavoriteStore, TargetID: "s1"}

	on, created, err := s.Toggle(t.Context(), 1, in)
	require.NoError(t, err)
	assert.True(t, on)
	require.NotNil(t, created)

	_, err = s.Create(t.Context(), 1, in)

	var conflict *entity.Error
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, "alreadyFavorited", conflict.ErrorKey)

	var notFound *entity.Error
	require.ErrorAs(t, s.Remove(t.Context(), created.ID, 2), &notFound)
	assert.Equal(t, "favoriteNotFound", notFound.ErrorKey)

	off, removed, err := s.Toggle(t.Context(), 1, in)
	require.NoError(t, err)
	assert.False(t, off)
	assert.Nil(t, removed)
	assert.Empty(t, f.rows)
}

func TestExamTargetsCannotBeCreated(t *testing.T) {
	t.Parallel()

	s := favorite.New(&fakeFavorites{Favorites: nil, rows: nil})

	_, err := s.Create(t.Context(), 1, &favorite.Input{TargetType: entity.FavoriteQuestion, TargetID: "q"})

	var business *entity.BusinessError
	require.ErrorAs(t, err, &business)
	assert.Equal(t, "questionNotAccessible", business.Key)

	_, _, err = s.Toggle(t.Context(), 1, &favorite.Input{TargetType: entity.FavoritePaper, TargetID: "p"})

	var missing *entity.Error
	require.ErrorAs(t, err, &missing)
	assert.Equal(t, "paperNotFound", missing.ErrorKey)
}
