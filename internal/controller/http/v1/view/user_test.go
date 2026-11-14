package view_test

import (
	"encoding/json"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/entity"
)

func marshal(t *testing.T, v any) string {
	t.Helper()

	raw, err := json.Marshal(v)
	require.NoError(t, err)

	return string(raw)
}

func sampleUser() *entity.User {
	email, first := "a@example.com", "A"
	gender := 0
	at := time.Date(2026, 1, 2, 3, 4, 5, 678_000_000, time.UTC)
	birthday := time.Date(1990, 2, 3, 0, 0, 0, 0, time.UTC)
	mime, size := "image/png", 10
	id := uuid.MustParse("0b1fd3c4-6d1a-4b56-9d79-2a4b8c6f7e10")

	return &entity.User{
		ID: 7, Email: &email, Provider: "email", FirstName: &first, Gender: &gender, Birthday: &birthday,
		Photo:     &entity.File{ID: id, Path: "/api/v1/files/a.png", Visibility: entity.VisibilityPublic, MimeType: &mime, Size: &size},
		Role:      &entity.Named{ID: 2, Name: "User"},
		Status:    nil,
		CreatedAt: at, UpdatedAt: at,
	}
}

func TestUserMatchesNestjsSerialization(t *testing.T) {
	t.Parallel()

	urls := view.FileURLs{BackendDomain: "http://localhost:3000", APIPrefix: "api"}

	assert.JSONEq(t, `{"id":7,"email":"a@example.com","provider":"email","firstName":"A","lastName":null,"phone":null,
		"nickname":null,"gender":0,"birthday":"1990-02-03",
		"photo":{"id":"0b1fd3c4-6d1a-4b56-9d79-2a4b8c6f7e10","path":"http://localhost:3000/api/v1/files/a.png","visibility":"PUBLIC","mimeType":"image/png","size":10},
		"role":{"id":2,"name":"User","__entity":"RoleEntity"},"status":null,
		"createdAt":"2026-01-02T03:04:05.678Z","updatedAt":"2026-01-02T03:04:05.678Z","deletedAt":null,
		"wechatOpenId":null,"wechatUnionId":null,"alipayOpenId":null}`, marshal(t, view.NewUser(sampleUser(), urls, nil)))

	got := marshal(t, view.NewUser(sampleUser(), urls, nil))
	assert.Regexp(t, `^\{"id":7,"email":"a@example.com","provider":"email","firstName":"A","lastName":null,"phone":null,"nickname":null,"gender":0,"birthday":"1990-02-03","photo":\{.*\},"role":\{.*\},"status":null,"createdAt":`, got)
	assert.Equal(t, "null", marshal(t, view.NewUser(nil, urls, nil)))
}

func TestCreatedUserHasIDOnlyRelations(t *testing.T) {
	t.Parallel()

	role := 2
	got := marshal(t, view.NewUser(sampleUser(), view.FileURLs{BackendDomain: "", APIPrefix: "api"}, &view.Created{RoleID: &role, StatusID: nil, Photo: nil}))

	assert.Contains(t, got, `"photo":null,"role":{"id":2},"createdAt"`)
	assert.NotContains(t, got, `"status"`)
}

func TestPrivateFileURLUsesDownloadRoute(t *testing.T) {
	t.Parallel()

	f := &entity.File{ID: uuid.MustParse("0b1fd3c4-6d1a-4b56-9d79-2a4b8c6f7e10"), Path: "x", Visibility: entity.VisibilityPrivate}

	assert.Equal(t, "http://h/api/v1/files/private/0b1fd3c4-6d1a-4b56-9d79-2a4b8c6f7e10", view.FileURLs{BackendDomain: "http://h", APIPrefix: "api"}.URL(f))

	f.Visibility = ""
	assert.Equal(t, "PUBLIC", view.NewFile(f, view.FileURLs{BackendDomain: "", APIPrefix: "api"}).Visibility)
}

func TestSessionMarksCurrentDevice(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	agent := "UA"
	s := &entity.Session{ID: 3, UserAgent: &agent, CreatedAt: at}

	assert.JSONEq(t, `{"id":3,"userAgent":"UA","ip":null,"createdAt":"2026-01-02T03:04:05.000Z","lastActiveAt":null,"current":true}`,
		marshal(t, view.NewSession(s, 3)))
	assert.False(t, view.NewSession(s, 4).Current)
}

func TestLoginKeyOrder(t *testing.T) {
	t.Parallel()

	got := marshal(t, view.Login{RefreshToken: "r", Token: "t", TokenExpires: 1, User: view.NewUser(nil, view.FileURLs{BackendDomain: "", APIPrefix: ""}, nil)})
	assert.Equal(t, `{"refreshToken":"r","token":"t","tokenExpires":1,"user":null}`, got)
	assert.Equal(t, `{"token":"t","refreshToken":"r","tokenExpires":1}`, marshal(t, view.Refresh{Token: "t", RefreshToken: "r", TokenExpires: 1}))
}
