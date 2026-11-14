package view

import (
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
)

// Favorite is the nestjs Favorite in FavoriteMapper key order.
type Favorite struct {
	ID         int           `json:"id"`
	UserID     int           `json:"userId"`
	TargetType string        `json:"targetType"`
	TargetID   string        `json:"targetId"`
	Title      *string       `json:"title"`
	Image      *string       `json:"image"`
	Extra      *string       `json:"extra"`
	SubjectID  *string       `json:"subjectId"`
	CreatedAt  jsontime.Time `json:"createdAt"`
}

// NewFavorite returns the view of f.
func NewFavorite(f *entity.Favorite) Favorite {
	out := Favorite{
		ID: f.ID, UserID: f.UserID, TargetType: f.TargetType, TargetID: f.TargetID, Title: f.Title,
		Image: f.Image, Extra: f.Extra, SubjectID: nil, CreatedAt: jsontime.New(f.CreatedAt),
	}

	if f.SubjectID != nil {
		s := f.SubjectID.String()
		out.SubjectID = &s
	}

	return out
}

// FavoriteItem is a listed favorite followed by targetOffline.
type FavoriteItem struct {
	Favorite

	TargetOffline *bool `json:"targetOffline"`
}

// FavoritePage is the favorite list response.
type FavoritePage = pagination.Page[FavoriteItem]

// FavoriteCheck is {isFavorited, favoriteId}; favoriteId is absent when not favorited.
type FavoriteCheck struct {
	IsFavorited bool `json:"isFavorited"`
	FavoriteID  *int `json:"favoriteId,omitempty"`
}

// FavoriteToggle is {isFavorited, favorite}; favorite is absent after removal.
type FavoriteToggle struct {
	IsFavorited bool      `json:"isFavorited"`
	Favorite    *Favorite `json:"favorite,omitempty"`
}
