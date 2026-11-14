// Package favorite manages favorites like the nestjs FavoritesService and FavoriteTargetsService;
// question and paper targets are handled as missing targets.
package favorite

import (
	"context"
	"regexp"
	"strings"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
)

const _cleanerOrder = 70

var _uuid = regexp.MustCompile(`(?i)^(?:[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}` +
	`|00000000-0000-0000-0000-000000000000|ffffffff-ffff-ffff-ffff-ffffffffffff)$`)

// Input is a create or toggle request.
type Input struct {
	TargetType string
	TargetID   string
	Title      *string
	Image      *string
	Extra      *string
}

// Item is a listed favorite; TargetOffline is set for question and paper targets only.
type Item struct {
	entity.Favorite

	TargetOffline *bool
}

// Service manages favorites.
type Service struct {
	favorites repo.Favorites
}

// New returns a Service.
func New(favorites repo.Favorites) *Service {
	return &Service{favorites: favorites}
}

func examTarget(targetType string) bool {
	return targetType == entity.FavoriteQuestion || targetType == entity.FavoritePaper
}

// NormalizeTargetID lowercases question and paper UUIDs.
func NormalizeTargetID(targetType, targetID string) string {
	if examTarget(targetType) && _uuid.MatchString(targetID) {
		return strings.ToLower(targetID)
	}

	return targetID
}

func notFound() *entity.Error {
	return entity.NotFoundKey("favoriteNotFound")
}

func resolve(in *Input) (*repo.NewFavorite, error) {
	switch in.TargetType {
	case entity.FavoriteQuestion:
		return nil, entity.NewBusinessError("questionNotAccessible")
	case entity.FavoritePaper:
		return nil, entity.NotFoundKey("paperNotFound")
	default:
		return &repo.NewFavorite{
			UserID: 0, TargetType: in.TargetType, TargetID: in.TargetID, Title: in.Title, Image: in.Image,
			Extra: in.Extra, SubjectID: nil,
		}, nil
	}
}

// ByTarget returns the user's favorite of the target, or nil.
func (s *Service) ByTarget(ctx context.Context, userID int, targetType, targetID string) (*entity.Favorite, error) {
	return s.favorites.GetByTarget(ctx, userID, targetType, NormalizeTargetID(targetType, targetID)) //nolint:wrapcheck // repo errors are wrapped
}

// Create saves a favorite; a target the user already saved is a 409 alreadyFavorited.
func (s *Service) Create(ctx context.Context, userID int, in *Input) (*entity.Favorite, error) {
	f, err := resolve(in)
	if err != nil {
		return nil, err
	}

	existing, err := s.ByTarget(ctx, userID, f.TargetType, f.TargetID)
	if err != nil {
		return nil, err
	}

	if existing != nil {
		return nil, &entity.Error{Status: entity.StatusConflict, Message: "", ErrorKey: "alreadyFavorited", Fields: nil}
	}

	f.UserID = userID
	f.TargetID = NormalizeTargetID(f.TargetType, f.TargetID)

	created, err := s.favorites.Create(ctx, f)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	return created, nil
}

// Toggle removes the user's favorite of the target, or creates it; favorited reports the new state.
func (s *Service) Toggle(ctx context.Context, userID int, in *Input) (favorited bool, created *entity.Favorite, err error) {
	existing, err := s.ByTarget(ctx, userID, in.TargetType, in.TargetID)
	if err != nil {
		return false, nil, err
	}

	if existing != nil {
		return false, nil, s.Remove(ctx, existing.ID, userID)
	}

	created, err = s.Create(ctx, userID, in)
	if err != nil {
		return false, nil, err
	}

	return true, created, nil
}

// Remove deletes a favorite of the user; another user's favorite is a 404 favoriteNotFound.
func (s *Service) Remove(ctx context.Context, id, userID int) error {
	f, err := s.favorites.Get(ctx, id)
	if err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	if f == nil || f.UserID != userID {
		return notFound()
	}

	if err := s.favorites.Delete(ctx, id); err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	return nil
}

// List returns a page of the user's favorites and their total; question and paper targets are
// reported offline.
func (s *Service) List(ctx context.Context, f repo.FavoriteFilter, page, limit float64) ([]Item, int, error) {
	offset, take := pagination.OffsetLimit(page, limit)

	data, err := s.favorites.List(ctx, f, offset, take)
	if err != nil {
		return nil, 0, err //nolint:wrapcheck // repo errors are wrapped
	}

	total, err := s.favorites.Count(ctx, f)
	if err != nil {
		return nil, 0, err //nolint:wrapcheck // repo errors are wrapped
	}

	items := make([]Item, len(data))
	offline := true

	for i := range data {
		items[i] = Item{Favorite: data[i], TargetOffline: nil}
		if examTarget(data[i].TargetType) {
			items[i].TargetOffline = &offline
		}
	}

	return items, total, nil
}

// Cleaner deletes the favorites of a removed account.
type Cleaner struct {
	favorites repo.Favorites
}

// NewCleaner returns the favorites account cleaner (order 70).
func NewCleaner(favorites repo.Favorites) *Cleaner {
	return &Cleaner{favorites: favorites}
}

// Name implements accountcleanup.Cleaner.
func (*Cleaner) Name() string { return "favorites" }

// Order implements accountcleanup.Cleaner.
func (*Cleaner) Order() int { return _cleanerOrder }

// Clean implements accountcleanup.Cleaner.
func (c *Cleaner) Clean(ctx context.Context, userID int) error {
	return c.favorites.RemoveByUser(ctx, userID) //nolint:wrapcheck // repo errors are wrapped
}
