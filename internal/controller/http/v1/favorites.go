package v1

import (
	"context"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase/favorite"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

func favoriteInput(o *validatorx.Object) *favorite.Input {
	return &favorite.Input{
		TargetType: truthyString(o, "targetType"), TargetID: truthyString(o, "targetId"),
		Title: stringPtr(o, "title"), Image: stringPtr(o, "image"), Extra: stringPtr(o, "extra"),
	}
}

// GetFavorites handles GET favorites.
func (s *Server) GetFavorites(ctx context.Context, _ gen.GetFavoritesRequestObject) (gen.GetFavoritesResponseObject, error) {
	q := query(ctx)
	page, limit := pageAndLimit(q)
	filter := repo.FavoriteFilter{UserID: callerID(ctx), TargetType: truthyString(q, "targetType"), SubjectID: nil}

	if subject := truthyString(q, "subjectId"); subject != "" {
		id, err := uuid.Parse(subject)
		if err != nil {
			return nil, err //nolint:wrapcheck // validated as a UUID by the query DTO
		}

		filter.SubjectID = &id
	}

	items, total, err := s.favorites.List(ctx, filter, page, limit)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	out := make([]view.FavoriteItem, len(items))
	for i := range items {
		out[i] = view.FavoriteItem{Favorite: view.NewFavorite(&items[i].Favorite), TargetOffline: items[i].TargetOffline}
	}

	return gen.GetFavorites200JSONResponse(pagination.New(out, float64(total), page, limit)), nil
}

// CreateFavorite handles POST favorites.
func (s *Server) CreateFavorite(ctx context.Context, _ gen.CreateFavoriteRequestObject) (gen.CreateFavoriteResponseObject, error) {
	f, err := s.favorites.Create(ctx, callerID(ctx), favoriteInput(body(ctx)))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.CreateFavorite201JSONResponse(view.NewFavorite(f)), nil
}

// CheckFavorite handles POST favorites/check.
func (s *Server) CheckFavorite(ctx context.Context, _ gen.CheckFavoriteRequestObject) (gen.CheckFavoriteResponseObject, error) {
	in := favoriteInput(body(ctx))

	f, err := s.favorites.ByTarget(ctx, callerID(ctx), in.TargetType, in.TargetID)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	out := view.FavoriteCheck{IsFavorited: f != nil, FavoriteID: nil}
	if f != nil {
		out.FavoriteID = &f.ID
	}

	return gen.CheckFavorite200JSONResponse(out), nil
}

// ToggleFavorite handles POST favorites/toggle.
func (s *Server) ToggleFavorite(ctx context.Context, _ gen.ToggleFavoriteRequestObject) (gen.ToggleFavoriteResponseObject, error) {
	favorited, f, err := s.favorites.Toggle(ctx, callerID(ctx), favoriteInput(body(ctx)))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	if f == nil {
		return gen.ToggleFavorite200JSONResponse{IsFavorited: favorited, Favorite: nil}, nil
	}

	v := view.NewFavorite(f)

	return gen.ToggleFavorite200JSONResponse{IsFavorited: favorited, Favorite: &v}, nil
}

// DeleteFavorite handles DELETE favorites/:id.
func (s *Server) DeleteFavorite(ctx context.Context, req gen.DeleteFavoriteRequestObject) (gen.DeleteFavoriteResponseObject, error) {
	if err := s.favorites.Remove(ctx, int(req.Id), callerID(ctx)); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.DeleteFavorite200Response{Body: nil}, nil
}
