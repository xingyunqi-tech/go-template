package view

import (
	"encoding/json"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
)

const (
	_roleEntity   = "RoleEntity"
	_statusEntity = "StatusEntity"
	_dateLayout   = "2006-01-02"
)

// FileURLs builds the public URL of a file like nestjs buildFileUrl for the local driver.
type FileURLs struct {
	BackendDomain string
	APIPrefix     string
}

// URL returns the file's URL: BACKEND_DOMAIN + path for public files, the private download route
// otherwise.
func (u FileURLs) URL(f *entity.File) string {
	if f.Visibility == entity.VisibilityPrivate {
		return u.BackendDomain + "/" + u.APIPrefix + "/v1/files/private/" + f.ID.String()
	}

	return u.BackendDomain + f.Path
}

// File is the {id, path, visibility, mimeType, size} file shape.
type File struct {
	ID         string  `json:"id"`
	Path       string  `json:"path"`
	Visibility string  `json:"visibility"`
	MimeType   *string `json:"mimeType"`
	Size       *int    `json:"size"`
}

// NewFile returns the view of f.
func NewFile(f *entity.File, urls FileURLs) *File {
	visibility := f.Visibility
	if visibility == "" {
		visibility = entity.VisibilityPublic
	}

	return &File{ID: f.ID.String(), Path: urls.URL(f), Visibility: visibility, MimeType: f.MimeType, Size: f.Size}
}

// Relation is a role or status key that may be absent, null, {id} or {id, name, __entity}.
type Relation struct {
	present bool
	value   any
}

// IsZero makes omitzero drop absent relations.
func (r Relation) IsZero() bool {
	return !r.present
}

// MarshalJSON implements json.Marshaler.
func (r Relation) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.value) //nolint:wrapcheck // plain values
}

type namedEntity struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Entity string `json:"__entity"`
}

type idOnly struct {
	ID int `json:"id"`
}

func loaded(n *entity.Named, kind string) Relation {
	if n == nil {
		return Relation{present: true, value: nil}
	}

	return Relation{present: true, value: namedEntity{ID: n.ID, Name: n.Name, Entity: kind}}
}

func createdRelation(id *int) Relation {
	if id == nil {
		return Relation{present: false, value: nil}
	}

	return Relation{present: true, value: idOnly{ID: *id}}
}

type userJSON struct {
	ID            int            `json:"id"`
	Email         *string        `json:"email"`
	Provider      string         `json:"provider"`
	FirstName     *string        `json:"firstName"`
	LastName      *string        `json:"lastName"`
	Phone         *string        `json:"phone"`
	Nickname      *string        `json:"nickname"`
	Gender        *int           `json:"gender"`
	Birthday      *string        `json:"birthday"`
	Photo         *File          `json:"photo"`
	Role          Relation       `json:"role,omitzero"`
	Status        Relation       `json:"status,omitzero"`
	CreatedAt     jsontime.Time  `json:"createdAt"`
	UpdatedAt     jsontime.Time  `json:"updatedAt"`
	DeletedAt     *jsontime.Time `json:"deletedAt"`
	WechatOpenID  *string        `json:"wechatOpenId"`
	WechatUnionID *string        `json:"wechatUnionId"`
	AlipayOpenID  *string        `json:"alipayOpenId"`
}

// User is a user as nestjs ClassSerializerInterceptor writes it for the me and admin groups;
// the zero User is written as null.
type User struct {
	data *userJSON
}

// MarshalJSON implements json.Marshaler.
func (u User) MarshalJSON() ([]byte, error) {
	if u.data == nil {
		return []byte("null"), nil
	}

	return json.Marshal(u.data) //nolint:wrapcheck // plain values
}

// Created describes the relations a new user was saved with; nestjs returns them as {id}.
type Created struct {
	RoleID   *int
	StatusID *int
	Photo    *entity.File
}

func timePtr(t *time.Time) *jsontime.Time {
	if t == nil {
		return nil
	}

	v := jsontime.New(*t)

	return &v
}

// NewUser returns the view of u; created switches the relations to the saved {id} form.
func NewUser(u *entity.User, urls FileURLs, created *Created) User {
	if u == nil {
		return User{data: nil}
	}

	data := &userJSON{
		ID: u.ID, Email: u.Email, Provider: u.Provider, FirstName: u.FirstName, LastName: u.LastName,
		Phone: u.Phone, Nickname: u.Nickname, Gender: u.Gender, Birthday: nil, Photo: nil,
		Role: loaded(u.Role, _roleEntity), Status: loaded(u.Status, _statusEntity),
		CreatedAt: jsontime.New(u.CreatedAt), UpdatedAt: jsontime.New(u.UpdatedAt), DeletedAt: timePtr(u.DeletedAt),
		WechatOpenID: u.WechatOpenID, WechatUnionID: u.WechatUnionID, AlipayOpenID: u.AlipayOpenID,
	}

	if u.Birthday != nil {
		d := u.Birthday.Format(_dateLayout)
		data.Birthday = &d
	}

	photo := u.Photo

	if created != nil {
		photo = created.Photo
		data.Role = createdRelation(created.RoleID)
		data.Status = createdRelation(created.StatusID)
	}

	if photo != nil {
		data.Photo = NewFile(photo, urls)
	}

	return User{data: data}
}

// Login is {refreshToken, token, tokenExpires, user}.
type Login struct {
	RefreshToken string `json:"refreshToken"`
	Token        string `json:"token"`
	TokenExpires int64  `json:"tokenExpires"`
	User         User   `json:"user"`
}

// Refresh is {token, refreshToken, tokenExpires}.
type Refresh struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refreshToken"`
	TokenExpires int64  `json:"tokenExpires"`
}

// Session is one signed-in device.
type Session struct {
	ID           int            `json:"id"`
	UserAgent    *string        `json:"userAgent"`
	IP           *string        `json:"ip"`
	CreatedAt    jsontime.Time  `json:"createdAt"`
	LastActiveAt *jsontime.Time `json:"lastActiveAt"`
	Current      bool           `json:"current"`
}

// NewSession returns the view of s; current marks the caller's session.
func NewSession(s *entity.Session, current int) Session {
	return Session{
		ID: s.ID, UserAgent: s.UserAgent, IP: s.IP, CreatedAt: jsontime.New(s.CreatedAt),
		LastActiveAt: timePtr(s.LastActiveAt), Current: s.ID == current,
	}
}

// UserPage is the infinite-scroll user list {data, hasNextPage}.
type UserPage struct {
	Data        []User `json:"data"`
	HasNextPage bool   `json:"hasNextPage"`
}
