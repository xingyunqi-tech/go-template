package auth

import (
	"context"
	"fmt"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase/session"
	"github.com/xingyunqi-tech/go-template/internal/usecase/sms"
	"github.com/xingyunqi-tech/go-template/internal/usecase/user"
)

// SMSSender issues verification codes.
type SMSSender interface {
	SendCode(ctx context.Context, phone, purpose string) (sms.SendResult, error)
}

func (s *Service) findBy(ctx context.Context, q repo.UserQuery) (*entity.User, error) {
	return s.userRepo.Get(ctx, q) //nolint:wrapcheck // repo errors are wrapped
}

func (s *Service) findByPhone(ctx context.Context, phone string) (*entity.User, error) {
	if phone == "" {
		return nil, nil //nolint:nilnil // empty phones match nobody
	}

	return s.findBy(ctx, repo.UserQuery{Phone: &phone})
}

// ensureCanAuthenticate is nestjs validateUserStatusForAuth.
func (s *Service) ensureCanAuthenticate(ctx context.Context, u *entity.User, field string) error {
	if entity.CanAuthenticate(u.StatusID()) {
		return nil
	}

	s.log.WarnContext(ctx, "Auth failed - user status not allowed", "user_id", u.ID, "status_id", u.StatusID())

	return entity.Invalid(field, "userNotActive")
}

// signInCreated signs in a user created by the request; its token role is {id} like the entity
// nestjs save() returns.
func (s *Service) signInCreated(ctx context.Context, u *entity.User, created *user.Created, client Client) (*Login, error) {
	hash, err := session.NewHash()
	if err != nil {
		return nil, err //nolint:wrapcheck // session errors are wrapped
	}

	sess, err := s.sessions.Create(ctx, u.ID, hash, client.UserAgent, client.IP)
	if err != nil {
		return nil, err //nolint:wrapcheck // session errors are wrapped
	}

	var role *RoleClaim
	if created.RoleID != nil {
		role = &RoleClaim{ID: *created.RoleID, Name: "", Entity: ""}
	}

	tokens, err := s.tokens(u.ID, role, sess.ID, hash)
	if err != nil {
		return nil, err
	}

	return &Login{Tokens: tokens, User: u, Created: created}, nil
}

func (s *Service) verifySMS(ctx context.Context, phone, code, purpose string) error {
	ok, err := s.sms.Verify(ctx, phone, code, purpose)
	if err != nil {
		return err //nolint:wrapcheck // sms errors are wrapped
	}

	if !ok {
		s.log.WarnContext(ctx, "SMS verification failed", "phone", entity.MaskPhone(phone))

		return entity.Invalid("code", "invalidCode")
	}

	return nil
}

// SendCode is POST auth/phone/send-code.
func (s *Service) SendCode(ctx context.Context, phone, purpose string) error {
	s.log.InfoContext(ctx, "Send SMS code request", "phone", entity.MaskPhone(phone), "type", purpose)

	if purpose == sms.PurposeChangePhone {
		return entity.Invalid("type", "unsupportedCodeType")
	}

	u, err := s.findByPhone(ctx, phone)
	if err != nil {
		return err
	}

	switch {
	case purpose == sms.PurposeRegister && u != nil:
		return entity.Invalid("phone", "phoneAlreadyExists")
	case purpose != sms.PurposeRegister && u == nil:
		return entity.Invalid("phone", "phoneNotFound")
	}

	result, err := s.sender.SendCode(ctx, phone, purpose)
	if err != nil {
		return err //nolint:wrapcheck // sms errors are wrapped
	}

	if result.Sent {
		return nil
	}

	s.log.WarnContext(ctx, "Failed to send SMS code", "phone", entity.MaskPhone(phone))

	if result.RetryAfter > 0 {
		return entity.InvalidFields(
			entity.Detail{Name: "phone", Value: "rateLimited"},
			entity.Detail{Name: "retryAfter", Value: float64(result.RetryAfter)},
		)
	}

	return entity.Invalid("phone", "sendFailed")
}

// LoginWithPhone is POST auth/phone/login.
func (s *Service) LoginWithPhone(ctx context.Context, phone, password string, client Client) (*Login, error) {
	u, err := s.findByPhone(ctx, phone)
	if err != nil {
		return nil, err
	}

	ok := false
	if u != nil {
		if ok, err = s.verifyPassword(ctx, u, password); err != nil {
			return nil, err
		}
	}

	if !ok {
		return nil, entity.Invalid("credentials", "incorrectPhoneOrPassword")
	}

	if err := s.ensureCanAuthenticate(ctx, u, "phone"); err != nil {
		return nil, err
	}

	return s.signIn(ctx, u, client)
}

// LoginWithSMS is POST auth/phone/sms-login; unknown phones are not registered.
func (s *Service) LoginWithSMS(ctx context.Context, phone, code string, client Client) (*Login, error) {
	if err := s.verifySMS(ctx, phone, code, sms.PurposeLogin); err != nil {
		return nil, err
	}

	u, err := s.findByPhone(ctx, phone)
	if err != nil {
		return nil, err
	}

	if u == nil {
		return nil, entity.Invalid("phone", "notFound")
	}

	if err := s.ensureCanAuthenticate(ctx, u, "phone"); err != nil {
		return nil, err
	}

	return s.signIn(ctx, u, client)
}

func active(provider string) *user.Input {
	return &user.Input{
		Provider: repo.Some(provider),
		Role:     user.Ref{Set: true, Null: false, ID: fmt.Sprint(entity.RoleUser)},
		Status:   user.Ref{Set: true, Null: false, ID: fmt.Sprint(entity.StatusActive)},
	}
}

// RegisterWithPhone is POST auth/phone/register: an active user, signed in.
func (s *Service) RegisterWithPhone(ctx context.Context, phone, code, password string, nickname *string, client Client) (*Login, error) {
	if err := s.verifySMS(ctx, phone, code, sms.PurposeRegister); err != nil {
		return nil, err
	}

	existing, err := s.findByPhone(ctx, phone)
	if err != nil {
		return nil, err
	}

	if existing != nil {
		return nil, entity.Invalid("phone", "phoneAlreadyExists")
	}

	in := active(entity.ProviderPhone)
	in.Phone = repo.Some(phone)
	in.Password = repo.Some(password)

	if nickname != nil {
		in.Nickname = repo.Some(*nickname)
	}

	u, created, err := s.users.Create(ctx, in)
	if err != nil {
		return nil, err //nolint:wrapcheck // user errors are wrapped
	}

	s.initializeSettings(ctx, u.ID)

	return s.signInCreated(ctx, u, created, client)
}

// ResetPasswordByPhone is POST auth/phone/reset-password; every session is signed out.
func (s *Service) ResetPasswordByPhone(ctx context.Context, phone, code, password string) error {
	ok, err := s.sms.Verify(ctx, phone, code, sms.PurposeResetPassword)
	if err != nil {
		return err //nolint:wrapcheck // sms errors are wrapped
	}

	if !ok {
		return entity.Invalid("code", "invalidCode")
	}

	u, err := s.findByPhone(ctx, phone)
	if err != nil {
		return err
	}

	if u == nil {
		return entity.Invalid("phone", "phoneNotExists")
	}

	if err := s.ensureCanAuthenticate(ctx, u, "phone"); err != nil {
		return err
	}

	if _, err := s.users.Update(ctx, u.ID, &user.Input{Password: repo.Some(password)}); err != nil {
		return err //nolint:wrapcheck // user errors are wrapped
	}

	return s.sessions.DeleteAllForUser(ctx, u.ID, nil) //nolint:wrapcheck // session errors are wrapped
}
