package entity

import (
	"slices"
	"time"
	"uuid"
)

// File purposes and the content types they accept (platform conventions §11.5).
const (
	PurposeAvatar     = "avatar"
	PurposeAttachment = "attachment"

	TypeJPEG = "image/jpeg"
	TypePNG  = "image/png"
	TypeWebP = "image/webp"
	TypePDF  = "application/pdf"

	_avatarMaxSize     = 5 << 20
	_attachmentMaxSize = 20 << 20
)

// File is an uploaded file; ObjectKey names it in Storage (local or s3).
type File struct {
	ID          uuid.UUID
	OwnerSub    uuid.UUID
	Purpose     string
	Name        string
	ContentType string
	Size        int64
	SHA256      []byte
	Storage     string
	ObjectKey   string
	CreatedAt   time.Time
}

// Image reports whether the file is an image.
func (f *File) Image() bool {
	return slices.Contains([]string{TypeJPEG, TypePNG, TypeWebP}, f.ContentType)
}

// Purpose is what a file is uploaded for: its content types and size limit.
type Purpose struct {
	Name    string
	Types   []string
	MaxSize int64
}

// Purposes lists the purposes accepted by uploadFile.
func Purposes() []Purpose {
	images := []string{TypeJPEG, TypePNG, TypeWebP}

	return []Purpose{
		{Name: PurposeAvatar, Types: images, MaxSize: _avatarMaxSize},
		{Name: PurposeAttachment, Types: append(slices.Clone(images), TypePDF), MaxSize: _attachmentMaxSize},
	}
}

// PurposeOf returns the purpose name.
func PurposeOf(name string) (Purpose, bool) {
	for _, p := range Purposes() {
		if p.Name == name {
			return p, true
		}
	}

	return Purpose{}, false
}

// MaxFileSize is the largest size limit of the purposes.
func MaxFileSize() int64 {
	var size int64

	for _, p := range Purposes() {
		size = max(size, p.MaxSize)
	}

	return size
}
