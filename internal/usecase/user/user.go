// Package user creates, updates and removes users like the nestjs UsersService.
package user

import (
	"context"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
)

// Ref is a {id} relation in a request: Set is false when absent, Null for an explicit null, and
// ID is empty for a falsy id.
type Ref struct {
	Set  bool
	Null bool
	ID   string
}

// Input is a user create or update request; unset fields are left alone.
type Input struct {
	Email         repo.Optional[string]
	Password      repo.Optional[string]
	Provider      repo.Optional[string]
	FirstName     repo.Optional[string]
	LastName      repo.Optional[string]
	Phone         repo.Optional[string]
	Nickname      repo.Optional[string]
	Gender        repo.Optional[int]
	Birthday      repo.Optional[time.Time]
	Photo         Ref
	Role          Ref
	Status        Ref
	WechatOpenID  repo.Optional[string]
	WechatUnionID repo.Optional[string]
	AlipayOpenID  repo.Optional[string]
}

// Created describes the relations a new user was saved with; nestjs returns them as {id} only.
type Created struct {
	RoleID   *int
	StatusID *int
	Photo    *entity.File
}

// Hasher hashes passwords.
type Hasher interface {
	Hash(ctx context.Context, secret string) (string, error)
}

// Sessions deletes sessions of a user.
type Sessions interface {
	DeleteAllForUser(ctx context.Context, userID int, except *int) error
}

// AccountCleanup schedules the cleanup of a removed account.
type AccountCleanup interface {
	Enqueue(ctx context.Context, userID int) error
}

// Service manages users.
type Service struct {
	users    repo.Users
	files    repo.Files
	sessions Sessions
	hasher   Hasher
	cleanup  AccountCleanup
	log      *slog.Logger
}

// New returns a Service.
func New(users repo.Users, files repo.Files, sessions Sessions, hasher Hasher, cleanup AccountCleanup, log *slog.Logger) *Service {
	return &Service{users: users, files: files, sessions: sessions, hasher: hasher, cleanup: cleanup, log: log}
}

func given(o repo.Optional[string]) bool {
	return o.Set && o.Value != nil && *o.Value != ""
}

func validID(id string, allowed ...int) (int, bool) {
	for _, a := range allowed {
		if fmt.Sprint(a) == id {
			return a, true
		}
	}

	return 0, false
}

func roleID(r Ref) (*int, error) {
	if !r.Set || r.Null || r.ID == "" {
		return nil, nil //nolint:nilnil // no role change
	}

	id, ok := validID(r.ID, entity.RoleAdmin, entity.RoleUser, entity.RoleEditor, entity.RoleReviewer)
	if !ok {
		return nil, entity.Invalid("role", "roleNotExists")
	}

	return &id, nil
}

func statusID(r Ref) (*int, error) {
	if !r.Set || r.Null || r.ID == "" {
		return nil, nil //nolint:nilnil // no status change
	}

	id, ok := validID(r.ID, entity.StatusActive, entity.StatusInactive)
	if !ok {
		return nil, entity.Invalid("status", "statusNotExists")
	}

	return &id, nil
}

// photo resolves the photo reference: (nil, false) leaves it unchanged, (nil, true) clears it.
func (s *Service) photo(ctx context.Context, r Ref, owner *int) (*entity.File, bool, error) {
	if !r.Set {
		return nil, false, nil
	}

	if r.Null {
		return nil, true, nil
	}

	if r.ID == "" {
		return nil, false, nil
	}

	f, err := s.referenceableFile(ctx, r.ID, owner)

	return f, f != nil, err
}

// referenceableFile loads a photo the owner may reference: missing files are 422 imageNotExists,
// other users' private files 403 fileNotAccessible.
func (s *Service) referenceableFile(ctx context.Context, rawID string, owner *int) (*entity.File, error) {
	id, err := uuid.Parse(rawID)
	if err != nil {
		return nil, fmt.Errorf("user: photo id: %w", err)
	}

	files, err := s.files.GetMany(ctx, []uuid.UUID{id})
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if len(files) == 0 {
		return nil, entity.Invalid("photo", "imageNotExists")
	}

	f := files[0]
	if f.Visibility == entity.VisibilityPrivate && (owner == nil || f.OwnerUserID == nil || *f.OwnerUserID != *owner) {
		return nil, &entity.Error{Status: entity.StatusForbidden, Message: "", ErrorKey: "fileNotAccessible", Fields: nil}
	}

	return &f, nil
}

func (s *Service) ensureFree(ctx context.Context, q repo.UserQuery, field, key string, current int) error {
	existing, err := s.users.Get(ctx, q)
	if err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	if existing != nil && existing.ID != current {
		return entity.Invalid(field, key)
	}

	return nil
}

func value[T any](o repo.Optional[T]) *T {
	if !o.Set {
		return nil
	}

	return o.Value
}

// Create creates a user.
func (s *Service) Create(ctx context.Context, in *Input) (*entity.User, *Created, error) {
	row := repo.NewUser{
		FirstName: value(in.FirstName), LastName: value(in.LastName), Nickname: value(in.Nickname),
		Birthday: value(in.Birthday), Provider: entity.ProviderEmail,
		WechatOpenID: value(in.WechatOpenID), WechatUnionID: value(in.WechatUnionID), AlipayOpenID: value(in.AlipayOpenID),
		Email: nil, Phone: nil, Password: nil, Gender: nil, PhotoID: nil, RoleID: nil, StatusID: nil,
	}

	if given(in.Provider) {
		row.Provider = *in.Provider.Value
	}

	zero := 0
	row.Gender = &zero

	if g := value(in.Gender); g != nil {
		row.Gender = g
	}

	if err := s.credentials(ctx, in, &row); err != nil {
		return nil, nil, err
	}

	photo, _, err := s.photo(ctx, in.Photo, nil)
	if err != nil {
		return nil, nil, err
	}

	if photo != nil {
		row.PhotoID = &photo.ID
	}

	if row.RoleID, err = roleID(in.Role); err != nil {
		return nil, nil, err
	}

	if row.StatusID, err = statusID(in.Status); err != nil {
		return nil, nil, err
	}

	id, err := s.users.Create(ctx, &row)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	u, err := s.users.Get(ctx, repo.UserQuery{ID: &id})
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	return u, &Created{RoleID: row.RoleID, StatusID: row.StatusID, Photo: photo}, nil
}

// credentials hashes the password and claims the email and phone of a new user.
func (s *Service) credentials(ctx context.Context, in *Input, row *repo.NewUser) error {
	if given(in.Password) {
		hash, err := s.hasher.Hash(ctx, *in.Password.Value)
		if err != nil {
			return err //nolint:wrapcheck // hasher errors are wrapped
		}

		row.Password = &hash
	}

	if given(in.Email) {
		if err := s.ensureFree(ctx, repo.UserQuery{Email: in.Email.Value}, "email", "emailAlreadyExists", 0); err != nil {
			return err
		}

		row.Email = in.Email.Value
	}

	if given(in.Phone) {
		if err := s.ensureFree(ctx, repo.UserQuery{Phone: in.Phone.Value}, "phone", "phoneAlreadyExists", 0); err != nil {
			return err
		}

		row.Phone = in.Phone.Value
	}

	return nil
}

// FindByID returns the active user, or nil.
func (s *Service) FindByID(ctx context.Context, id int) (*entity.User, error) {
	return s.users.Get(ctx, repo.UserQuery{ID: &id}) //nolint:wrapcheck // repo errors are wrapped
}

// FindByEmail returns the active user, or nil.
func (s *Service) FindByEmail(ctx context.Context, email string) (*entity.User, error) {
	if email == "" {
		return nil, nil //nolint:nilnil // empty emails match nobody
	}

	return s.users.Get(ctx, repo.UserQuery{Email: &email}) //nolint:wrapcheck // repo errors are wrapped
}

// List returns a page of users.
func (s *Service) List(ctx context.Context, q *repo.UserList) ([]entity.User, error) {
	return s.users.List(ctx, q) //nolint:wrapcheck // repo errors are wrapped
}

// Update applies in to the user and returns it reloaded.
func (s *Service) Update(ctx context.Context, id int, in *Input) (*entity.User, error) {
	changes := repo.UserChanges{
		FirstName: in.FirstName, LastName: in.LastName, Nickname: in.Nickname, Gender: in.Gender,
		Birthday: in.Birthday, Provider: in.Provider,
		Email: repo.Optional[string]{Set: false, Value: nil}, Phone: repo.Optional[string]{Set: false, Value: nil},
		Password: repo.Optional[string]{Set: false, Value: nil}, PhotoID: repo.Optional[uuid.UUID]{Set: false, Value: nil},
		RoleID: nil, StatusID: nil,
	}

	if err := s.updateChanges(ctx, id, in, &changes); err != nil {
		return nil, err
	}

	ok, err := s.users.Update(ctx, id, &changes)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	u, err := s.users.Get(ctx, repo.UserQuery{ID: &id})
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if !ok || u == nil {
		return nil, entity.NotFound("User not found")
	}

	return u, nil
}

// updateChanges fills the password, unique columns, photo, role and status of an update.
func (s *Service) updateChanges(ctx context.Context, id int, in *Input, changes *repo.UserChanges) error {
	if given(in.Password) {
		hash, err := s.hasher.Hash(ctx, *in.Password.Value)
		if err != nil {
			return err //nolint:wrapcheck // hasher errors are wrapped
		}

		changes.Password = repo.Some(hash)
	}

	var err error

	if changes.Email, err = s.uniqueChange(ctx, in.Email, id, "email", "emailAlreadyExists"); err != nil {
		return err
	}

	if changes.Phone, err = s.uniqueChange(ctx, in.Phone, id, "phone", "phoneAlreadyExists"); err != nil {
		return err
	}

	photo, change, err := s.photo(ctx, in.Photo, &id)
	if err != nil {
		return err
	}

	if change {
		changes.PhotoID = repo.Null[uuid.UUID]()
		if photo != nil {
			changes.PhotoID = repo.Some(photo.ID)
		}
	}

	if changes.RoleID, err = roleID(in.Role); err != nil {
		return err
	}

	changes.StatusID, err = statusID(in.Status)

	return err
}

// uniqueChange turns a requested email or phone into a change: a non-empty value must be free,
// null clears it, anything else leaves it.
func (s *Service) uniqueChange(ctx context.Context, o repo.Optional[string], id int, field, key string) (repo.Optional[string], error) {
	switch {
	case given(o):
		q := repo.UserQuery{Email: o.Value}
		if field == "phone" {
			q = repo.UserQuery{Phone: o.Value}
		}

		if err := s.ensureFree(ctx, q, field, key, id); err != nil {
			return o, err
		}

		return o, nil
	case o.Set && o.Value == nil:
		return o, nil
	default:
		return repo.Optional[string]{Set: false, Value: nil}, nil
	}
}

// UpdateByAdmin updates the user and signs it out everywhere when the password changed.
func (s *Service) UpdateByAdmin(ctx context.Context, id int, in *Input) (*entity.User, error) {
	u, err := s.Update(ctx, id, in)
	if err != nil {
		return nil, err
	}

	if given(in.Password) {
		if err := s.sessions.DeleteAllForUser(ctx, id, nil); err != nil {
			return nil, err //nolint:wrapcheck // session errors are wrapped
		}
	}

	return u, nil
}

// Remove signs the user out, soft-deletes it and schedules the account cleanup.
func (s *Service) Remove(ctx context.Context, id int) error {
	if err := s.sessions.DeleteAllForUser(ctx, id, nil); err != nil {
		return err //nolint:wrapcheck // session errors are wrapped
	}

	if err := s.users.Remove(ctx, id); err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	return s.cleanup.Enqueue(ctx, id) //nolint:wrapcheck // cleanup errors are wrapped
}
