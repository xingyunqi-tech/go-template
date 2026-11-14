package entity

import (
	"time"
	"uuid"
)

// Favorite target types.
const (
	FavoriteProduct  = "product"
	FavoriteArticle  = "article"
	FavoriteStore    = "store"
	FavoriteQuestion = "question"
	FavoritePaper    = "paper"
)

// Favorite is an item a user saved.
type Favorite struct {
	ID         int
	UserID     int
	TargetType string
	TargetID   string
	Title      *string
	Image      *string
	Extra      *string
	SubjectID  *uuid.UUID
	CreatedAt  time.Time
}
