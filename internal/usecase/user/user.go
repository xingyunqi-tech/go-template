// Package user serves the current user and the user administration (platform conventions
// §11.2, §11.3).
package user

import (
	"context"
	"slices"
	"strconv"
	"time"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/internal/usecase/session"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
	"github.com/xingyunqi-tech/go-template/pkg/password"
	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
)

const (
	// _reauthWindow is how recent a sign-in a sensitive operation needs (platform conventions §5.4).
	_reauthWindow       = 10 * time.Minute
	_rulePasswordVerify = "auth-password-verify"
	_defaultSort        = "-createdAt"
)

// CodeVerifier consumes the SMS code of (scene, phone).
type CodeVerifier interface {
	Verify(ctx context.Context, scene, phone, code string) error
}

// WechatPhone resolves a getPhoneNumber code of the mini program to an E.164 number.
type WechatPhone interface {
	Phone(ctx context.Context, code string) (string, error)
}

// Deps are what the Service uses; a nil WechatPhone disables bindPhoneWithWechat.
type Deps struct {
	Users       repo.Users
	Codes       CodeVerifier
	WechatPhone WechatPhone
	Revocation  *session.Revocation
	Hasher      *password.Hasher
	Limits      usecase.Limits
	Now         func() time.Time
	// Outside detaches ctx from the request transaction; nil keeps ctx.
	Outside func(context.Context) context.Context
}

// Service implements usecase.Users.
type Service struct {
	Deps
}

var _ usecase.Users = (*Service)(nil)

// New returns a Service; a nil d.Now is time.Now.
func New(d *Deps) *Service {
	s := &Service{Deps: *d}
	if s.Now == nil {
		s.Now = time.Now
	}

	if s.Outside == nil {
		s.Outside = func(ctx context.Context) context.Context { return ctx }
	}

	return s
}

func (s *Service) get(ctx context.Context, id uuid.UUID, lock bool) (*repo.UserRecord, error) {
	get := s.Users.Get
	if lock {
		get = s.Users.GetForUpdate
	}

	rec, found, err := get(ctx, id)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if !found {
		return nil, entity.New(entity.KeyUserNotFound)
	}

	return rec, nil
}

// Me implements usecase.Users.
func (s *Service) Me(ctx context.Context, caller *usecase.Caller) (*entity.User, error) {
	rec, err := s.get(ctx, caller.UserID, false)
	if err != nil {
		return nil, err
	}

	return &rec.User, nil
}

// UpdateMe implements usecase.Users; an avatar file id names no file until files are uploaded.
func (s *Service) UpdateMe(ctx context.Context, caller *usecase.Caller, displayName *string, avatarFileID *uuid.UUID) (*entity.User, error) {
	if avatarFileID != nil {
		return nil, entity.Invalid("avatarFileId", entity.RuleExists, nil)
	}

	return s.update(ctx, caller.UserID, &repo.UserChanges{DisplayName: displayName, Status: nil})
}

func (s *Service) update(ctx context.Context, id uuid.UUID, c *repo.UserChanges) (*entity.User, error) {
	u, found, err := s.Users.Update(ctx, id, c)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if !found {
		return nil, entity.New(entity.KeyUserNotFound)
	}

	return u, nil
}

// ChangePassword implements usecase.Users: with a password set, the current one is checked under
// auth-password-verify; without one, the caller must have signed in recently. Other sessions are
// signed out. The stored hash is read outside the request transaction, which begins after both
// argon2 computations.
func (s *Service) ChangePassword(ctx context.Context, caller *usecase.Caller, c *usecase.PasswordChange) error {
	rec, err := s.get(s.Outside(ctx), caller.UserID, false)
	if err != nil {
		return err
	}

	if rec.PasswordHash != nil {
		if err := s.checkCurrent(ctx, caller, *rec.PasswordHash, c.Current); err != nil {
			return err
		}
	} else if s.Now().Sub(caller.AuthTime) > _reauthWindow {
		return entity.New(entity.KeyReauthRequired)
	}

	hash, err := s.Hasher.Hash(ctx, c.New)
	if err != nil {
		return err //nolint:wrapcheck // wrapped by password
	}

	if err := s.Users.SetPassword(ctx, caller.UserID, hash); err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	return s.Revocation.All(ctx, caller.UserID, &caller.SessionID) //nolint:wrapcheck // entity or repo errors
}

func (s *Service) checkCurrent(ctx context.Context, caller *usecase.Caller, encoded string, current *string) error {
	if current == nil {
		return entity.Invalid("currentPassword", entity.RuleRequired, nil)
	}

	subject := ratelimit.UserSubject(caller.UserID.String())

	if err := s.Limits.Peek(ctx, _rulePasswordVerify, subject); err != nil {
		return err //nolint:wrapcheck // entity errors
	}

	ok, _, err := s.Hasher.Verify(ctx, encoded, *current)
	if err != nil {
		return err //nolint:wrapcheck // wrapped by password
	}

	if !ok {
		if err := s.Limits.Consume(ctx, _rulePasswordVerify, subject); err != nil {
			return err //nolint:wrapcheck // entity errors
		}

		return entity.New(entity.KeyPasswordIncorrect)
	}

	return s.Limits.Reset(ctx, _rulePasswordVerify, subject) //nolint:wrapcheck // entity errors
}

// List implements usecase.Users.
func (s *Service) List(ctx context.Context, q *usecase.UserQuery) (pagination.Page[entity.User], error) {
	sort, err := sortKeys(q.Sort)
	if err != nil {
		return pagination.Page[entity.User]{}, err
	}

	users, total, err := s.Users.List(ctx, &repo.UserFilter{
		Keyword: q.Keyword, Status: q.Status, Role: q.Role, Sort: sort,
		Limit: q.PageSize, Offset: pagination.Offset(q.Page, q.PageSize),
	})
	if err != nil {
		return pagination.Page[entity.User]{}, err //nolint:wrapcheck // repo errors are wrapped
	}

	return pagination.NewPage(users, int64(total), q.Page, q.PageSize), nil
}

// sortKeys reads the sort parameter of listUsers: distinct fields among createdAt and email.
func sortKeys(raw string) ([]string, error) {
	invalid := entity.Invalid("query.sort", entity.RuleFormat, map[string]any{"format": "sort"})

	sorts, err := pagination.ParseSort(raw, _defaultSort, []string{"createdAt", "email"})
	if err != nil {
		return nil, invalid
	}

	keys := make([]string, 0, len(sorts))
	seen := map[string]bool{}

	for _, s := range sorts {
		if seen[s.Field] {
			return nil, invalid
		}

		seen[s.Field] = true

		key := s.Field
		if s.Descending {
			key = "-" + key
		}

		keys = append(keys, key)
	}

	return keys, nil
}

// Create implements usecase.Users: at least an email or a phone; the new user is a user.
func (s *Service) Create(ctx context.Context, a *usecase.NewAccount) (*entity.User, error) {
	if a.Email == nil && a.Phone == nil {
		return nil, &entity.ValidationError{Errors: []entity.FieldError{
			{Field: "email", Rule: entity.RuleRequired, Params: nil},
			{Field: "phone", Rule: entity.RuleRequired, Params: nil},
		}}
	}

	nu := &repo.NewUser{
		ID: uuid.NewV7(), Email: nil, Phone: nil, PasswordHash: nil, DisplayName: "", EmailVerified: false,
		Roles: []string{entity.RoleUser},
	}

	if a.Email != nil {
		email := entity.NormalizeEmail(*a.Email)
		nu.Email = &email
	}

	if a.Phone != nil {
		e164, err := usecase.NormalizePhone("phone", *a.Phone)
		if err != nil {
			return nil, err
		}

		nu.Phone = &e164
	}

	if a.DisplayName != nil {
		nu.DisplayName = *a.DisplayName
	}

	if a.Password != nil {
		hash, err := s.Hasher.Hash(ctx, *a.Password)
		if err != nil {
			return nil, err //nolint:wrapcheck // wrapped by password
		}

		nu.PasswordHash = &hash
	}

	return s.Users.Create(ctx, nu) //nolint:wrapcheck // repo errors are wrapped
}

// Get implements usecase.Users.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*entity.User, error) {
	rec, err := s.get(ctx, id, false)
	if err != nil {
		return nil, err
	}

	return &rec.User, nil
}

// KeepAdmin rejects a change that leaves no active admin when u loses the admin role or stops
// being active; it holds the admin lock until the transaction ends.
func KeepAdmin(ctx context.Context, users repo.Users, u *entity.User) error {
	if !u.Active() || !slices.Contains(u.Roles, entity.RoleAdmin) {
		return nil
	}

	if err := users.LockAdmins(ctx); err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	others, err := users.CountActiveAdmins(ctx, u.ID)
	if err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	if others == 0 {
		return entity.New(entity.KeyLastAdmin)
	}

	return nil
}

// Update implements usecase.Users: disabling a user signs all its sessions out.
func (s *Service) Update(ctx context.Context, id uuid.UUID, displayName, status *string) (*entity.User, error) {
	disabling := status != nil && *status == entity.UserDisabled

	if disabling {
		rec, err := s.get(ctx, id, true)
		if err != nil {
			return nil, err
		}

		if err := KeepAdmin(ctx, s.Users, &rec.User); err != nil {
			return nil, err
		}
	}

	u, err := s.update(ctx, id, &repo.UserChanges{DisplayName: displayName, Status: status})
	if err != nil {
		return nil, err
	}

	if disabling {
		if err := s.Revocation.All(ctx, id, nil); err != nil {
			return nil, err //nolint:wrapcheck // entity or repo errors
		}
	}

	return u, nil
}

// SetRoles implements usecase.Users: the roles must include user and be known; the user's
// sessions are signed out so the new roles apply at the next sign-in.
func (s *Service) SetRoles(ctx context.Context, id uuid.UUID, roles []string) (*entity.User, error) {
	if !slices.Contains(roles, entity.RoleUser) {
		return nil, entity.Invalid("roles", entity.RuleContains, map[string]any{"value": entity.RoleUser})
	}

	for i, role := range roles {
		if !entity.KnownRole(role) {
			return nil, entity.Invalid("roles["+strconv.Itoa(i)+"]", entity.RuleExists, nil)
		}
	}

	roles = slices.Compact(slices.Sorted(slices.Values(roles)))

	rec, err := s.get(ctx, id, true)
	if err != nil {
		return nil, err
	}

	if !slices.Contains(roles, entity.RoleAdmin) {
		if err := KeepAdmin(ctx, s.Users, &rec.User); err != nil {
			return nil, err
		}
	}

	if err := s.Users.SetRoles(ctx, id, roles); err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if err := s.Revocation.All(ctx, id, nil); err != nil {
		return nil, err //nolint:wrapcheck // entity or repo errors
	}

	return s.Get(ctx, id)
}

// BindPhone implements usecase.Users: the code is the bind_phone code sent to phone.
func (s *Service) BindPhone(ctx context.Context, caller *usecase.Caller, phone, code string) (*entity.User, error) {
	e164, err := usecase.NormalizePhone("phone", phone)
	if err != nil {
		return nil, err //nolint:wrapcheck // entity errors
	}

	if err := s.Codes.Verify(ctx, entity.SMSSceneBindPhone, e164, code); err != nil {
		return nil, err //nolint:wrapcheck // entity errors
	}

	return s.setPhone(ctx, caller.UserID, e164)
}

// BindWechatPhone implements usecase.Users; without the wechat_miniapp provider it is
// auth.providerDisabled.
func (s *Service) BindWechatPhone(ctx context.Context, caller *usecase.Caller, code string) (*entity.User, error) {
	if s.WechatPhone == nil {
		return nil, entity.New(entity.KeyProviderDisabled)
	}

	e164, err := s.WechatPhone.Phone(ctx, code)
	if err != nil {
		return nil, err //nolint:wrapcheck // entity errors of the provider
	}

	return s.setPhone(ctx, caller.UserID, e164)
}

func (s *Service) setPhone(ctx context.Context, id uuid.UUID, e164 string) (*entity.User, error) {
	u, found, err := s.Users.SetPhone(ctx, id, e164)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if !found {
		return nil, entity.New(entity.KeyUserNotFound)
	}

	return u, nil
}
