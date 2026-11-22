package entity

import (
	"time"
	"uuid"
)

// Note is a text note owned by one user.
type Note struct {
	ID        uuid.UUID
	OwnerSub  uuid.UUID
	Title     string
	Content   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NoteChanges is a partial update; nil fields keep their value.
type NoteChanges struct {
	Title   *string
	Content *string
}
