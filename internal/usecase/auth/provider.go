package auth

import (
	"context"
	"errors"
	"slices"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
)

var errIdentityOrphan = errors.New("auth: identity of a missing user")

// Provider exchanges a provider code for the identity it vouches for: a rejected code is
// auth.providerCodeInvalid, other failures common.badGateway.
type Provider interface {
	ID() string
	Exchange(ctx context.Context, code string) (*entity.ExternalIdentity, error)
}

// CodeVerifier consumes the SMS code of (scene, phone).
type CodeVerifier interface {
	Verify(ctx context.Context, scene, phone, code string) error
}

// Providers implements usecase.Auth in the order of platform conventions §11.1.
func (s *Service) Providers() []string {
	order := []string{entity.ProviderWechatMiniApp, entity.ProviderWechatApp, entity.ProviderAlipay}

	ids := make([]string, 0, len(s.providers))
	for _, id := range order {
		if _, ok := s.providers[id]; ok {
			ids = append(ids, id)
		}
	}

	return ids
}

// LoginWithProvider implements usecase.Auth: the identity is matched by (provider, subject),
// then for WeChat by the unionid shared with the other WeChat provider; a missing unionid is
// filled in unless another account holds it. First sign-ins of one identity or unionid are
// serialized by transaction locks; an identity whose account was deleted meanwhile is matched
// again.
func (s *Service) LoginWithProvider(ctx context.Context, c *usecase.Client, provider, code string) (*usecase.LoginResult, error) {
	p, ok := s.providers[provider]
	if !ok {
		return nil, entity.New(entity.KeyProviderDisabled)
	}

	id, err := p.Exchange(ctx, code)
	if err != nil {
		return nil, err //nolint:wrapcheck // entity errors of the provider
	}

	var out *usecase.LoginResult

	err = s.Tx.InTx(ctx, func(ctx context.Context) error {
		rec, err := s.identityUser(ctx, id)
		if err != nil {
			return err
		}

		if !rec.User.Active() {
			return entity.New(entity.KeyAccountDisabled)
		}

		out, err = s.signIn(ctx, c, &rec.User)

		return err
	})

	return out, err //nolint:wrapcheck // entity or repo errors
}

// _matchAttempts covers an account deleted between the identity lookup and the user lock.
const _matchAttempts = 2

// identityUser returns the account of id with its row locked, creating it on a first sign-in.
// User rows are locked before identity rows are written, in the order account deletion uses.
func (s *Service) identityUser(ctx context.Context, id *entity.ExternalIdentity) (*repo.UserRecord, error) {
	wechat := slices.Contains(entity.WechatProviders(), id.Provider) && id.UnionID != ""

	if err := s.lockIdentity(ctx, id, wechat); err != nil {
		return nil, err
	}

	for range _matchAttempts {
		rec, err := s.knownIdentity(ctx, id, wechat)
		if err != nil || rec != nil {
			return rec, err
		}
	}

	return nil, errIdentityOrphan
}

// knownIdentity returns the locked live account of id, or a new one when id is unknown; nil when
// the account of id was deleted meanwhile.
func (s *Service) knownIdentity(ctx context.Context, id *entity.ExternalIdentity, wechat bool) (*repo.UserRecord, error) {
	userID, missingUnion, found, err := s.Identities.Find(ctx, id.Provider, id.Subject)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if !found {
		return s.addIdentity(ctx, id, wechat)
	}

	rec, live, err := s.Users.GetForUpdate(ctx, userID)
	if err != nil || !live {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if missingUnion && wechat {
		if err := s.fillUnion(ctx, id, userID); err != nil {
			return nil, err
		}
	}

	return rec, nil
}

// lockIdentity takes the unionid lock before the identity lock, so two first sign-ins never
// wait on each other in opposite orders.
func (s *Service) lockIdentity(ctx context.Context, id *entity.ExternalIdentity, wechat bool) error {
	if wechat {
		if err := s.Identities.Lock(ctx, "union:"+id.UnionID); err != nil {
			return err //nolint:wrapcheck // repo errors are wrapped
		}
	}

	return s.Identities.Lock(ctx, id.Provider+":"+id.Subject) //nolint:wrapcheck // repo errors are wrapped
}

// fillUnion stores the unionid of an identity unless another account already holds it.
func (s *Service) fillUnion(ctx context.Context, id *entity.ExternalIdentity, userID uuid.UUID) error {
	owner, taken, err := s.Identities.FindByUnion(ctx, id.UnionID, entity.WechatProviders())
	if err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	if taken && owner != userID {
		s.Log.WarnContext(ctx, "unionid belongs to another account, identity left without it",
			"provider", id.Provider, "user_id", userID, "union_owner", owner)

		return nil
	}

	return s.Identities.SetUnion(ctx, id.Provider, id.Subject, id.UnionID) //nolint:wrapcheck // repo errors are wrapped
}

// addIdentity stores a new identity for the live account of the same unionid, else for a new
// account.
func (s *Service) addIdentity(ctx context.Context, id *entity.ExternalIdentity, wechat bool) (*repo.UserRecord, error) {
	rec, err := s.unionAccount(ctx, id, wechat)
	if err != nil {
		return nil, err
	}

	if rec == nil {
		u, err := s.Users.Create(ctx, newUser(nil, nil))
		if err != nil {
			return nil, err //nolint:wrapcheck // repo errors are wrapped
		}

		rec = &repo.UserRecord{User: *u, PasswordHash: nil}
	}

	return rec, s.Identities.Create(ctx, rec.User.ID, id) //nolint:wrapcheck // repo errors are wrapped
}

// unionAccount locks the account holding the unionid of id; nil when there is none or it was
// deleted meanwhile.
func (s *Service) unionAccount(ctx context.Context, id *entity.ExternalIdentity, wechat bool) (*repo.UserRecord, error) {
	if !wechat {
		return nil, nil //nolint:nilnil // no unionid to share
	}

	owner, found, err := s.Identities.FindByUnion(ctx, id.UnionID, entity.WechatProviders())
	if err != nil || !found {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	rec, live, err := s.Users.GetForUpdate(ctx, owner)
	if err != nil || !live {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	return rec, nil
}

func newUser(email, phone *string) *repo.NewUser {
	return &repo.NewUser{
		ID: uuid.NewV7(), Email: email, Phone: phone, PasswordHash: nil, DisplayName: "", EmailVerified: false,
		Roles: []string{entity.RoleUser},
	}
}

// _phoneAttempts covers a concurrent first sign-in of the same phone taking it first.
const _phoneAttempts = 2

// LoginWithSMS implements usecase.Auth: wrong codes count against auth-login-account, a
// successful sign-in clears it.
func (s *Service) LoginWithSMS(ctx context.Context, c *usecase.Client, phone, code string) (*usecase.LoginResult, error) {
	e164, err := usecase.NormalizePhone("phone", phone)
	if err != nil {
		return nil, err //nolint:wrapcheck // entity errors
	}

	subject := ratelimit.AccountSubject(e164)

	if err := s.Limits.Peek(ctx, _ruleLoginAccount, subject); err != nil {
		return nil, err //nolint:wrapcheck // entity errors
	}

	if err := s.Codes.Verify(ctx, entity.SMSSceneLogin, e164, code); err != nil {
		if errors.Is(err, entity.New(entity.KeyVerificationCodeInvalid)) {
			if err := s.Limits.Consume(ctx, _ruleLoginAccount, subject); err != nil {
				return nil, err //nolint:wrapcheck // entity errors
			}
		}

		return nil, err //nolint:wrapcheck // entity errors
	}

	if err := s.Limits.Reset(ctx, _ruleLoginAccount, subject); err != nil {
		return nil, err //nolint:wrapcheck // entity errors
	}

	for attempt := 1; ; attempt++ {
		out, err := s.phoneSignIn(ctx, c, e164)
		if attempt < _phoneAttempts && errors.Is(err, entity.New(entity.KeyPhoneTaken)) {
			continue
		}

		return out, err
	}
}

func (s *Service) phoneSignIn(ctx context.Context, c *usecase.Client, e164 string) (*usecase.LoginResult, error) {
	var out *usecase.LoginResult

	err := s.Tx.InTx(ctx, func(ctx context.Context) error {
		rec, found, err := s.phoneUser(ctx, e164)
		if err != nil {
			return err
		}

		var u *entity.User

		switch {
		case !found:
			if u, err = s.Users.Create(ctx, newUser(nil, &e164)); err != nil {
				return err //nolint:wrapcheck // repo errors are wrapped
			}
		case !rec.User.Active():
			return entity.New(entity.KeyAccountDisabled)
		default:
			u = &rec.User
		}

		out, err = s.signIn(ctx, c, u)

		return err
	})

	return out, err //nolint:wrapcheck // entity or repo errors
}

// phoneUser returns the user of a phone with its row locked; a user deleted or rebound to another
// phone meanwhile counts as not found.
func (s *Service) phoneUser(ctx context.Context, e164 string) (*repo.UserRecord, bool, error) {
	rec, found, err := s.Users.GetByPhone(ctx, e164)
	if err != nil || !found {
		return nil, false, err //nolint:wrapcheck // repo errors are wrapped
	}

	rec, found, err = s.Users.GetForUpdate(ctx, rec.User.ID)
	if err != nil || !found || rec.User.Phone == nil || *rec.User.Phone != e164 {
		return nil, false, err //nolint:wrapcheck // repo errors are wrapped
	}

	return rec, true, nil
}
