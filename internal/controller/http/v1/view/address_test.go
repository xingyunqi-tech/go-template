package view_test

import (
	"encoding/json"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/entity"
)

func TestAddressKeyOrderAndNull(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	code := "110000000000"
	a := &entity.Address{
		ID: 1, UserID: 2, Name: "N", Phone: "13800138000", ProvinceCode: &code, Province: "P", City: "C",
		District: "D", Address: "A", IsDefault: true, CreatedAt: at, UpdatedAt: at,
	}

	assert.Equal(t, `{"id":1,"userId":2,"name":"N","phone":"13800138000","provinceCode":"110000000000","cityCode":null,`+
		`"districtCode":null,"province":"P","city":"C","district":"D","address":"A","isDefault":true,`+
		`"createdAt":"2026-01-02T03:04:05.000Z","updatedAt":"2026-01-02T03:04:05.000Z","deletedAt":null}`,
		marshal(t, view.NewAddress(a)))
	assert.Equal(t, "null", marshal(t, view.NewAddress(nil)))
}

func TestFavoriteViews(t *testing.T) {
	t.Parallel()

	subject := uuid.MustParse("00000000-0000-4000-8000-00000000000a")
	f := &entity.Favorite{ID: 1, UserID: 2, TargetType: "question", TargetID: "q", SubjectID: &subject, CreatedAt: time.Unix(0, 0)}
	offline := true

	assert.Equal(t, `{"id":1,"userId":2,"targetType":"question","targetId":"q","title":null,"image":null,"extra":null,`+
		`"subjectId":"00000000-0000-4000-8000-00000000000a","createdAt":"1970-01-01T00:00:00.000Z","targetOffline":true}`,
		marshal(t, view.FavoriteItem{Favorite: view.NewFavorite(f), TargetOffline: &offline}))
	assert.Equal(t, `{"isFavorited":false}`, marshal(t, view.FavoriteToggle{IsFavorited: false, Favorite: nil}))
	assert.Equal(t, `{"isFavorited":false}`, marshal(t, view.FavoriteCheck{IsFavorited: false, FavoriteID: nil}))
}

func TestFeedbackImagesAreAbsentWhenNull(t *testing.T) {
	t.Parallel()

	f := &entity.Feedback{ID: 1, Type: "bug", Content: "c", Status: "pending", Images: json.RawMessage("null")}
	out := marshal(t, view.NewFeedback(f))
	assert.Contains(t, out, `"content":"c","contact":null,`)
	assert.Contains(t, out, `"messageSnapshot":null,`)

	f.Images = json.RawMessage(`["a", "b"]`)
	assert.Contains(t, marshal(t, view.NewFeedback(f)), `"content":"c","images":["a","b"],"contact":null,`)
}
