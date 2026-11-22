package auth

import (
	"context"
	"net/url"
	"strings"
	"time"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
)

// One-time token lifetimes (platform conventions §11.1).
const (
	_verifyTTL = 24 * time.Hour
	_resetTTL  = 30 * time.Minute
)

// mailKind is how a mail job issues its token and where the link points.
type mailKind struct {
	purpose string
	ttl     time.Duration
	path    string
}

func mailKinds() map[string]mailKind {
	return map[string]mailKind{
		repo.MailVerifyEmail:   {purpose: repo.TokenEmailVerify, ttl: _verifyTTL, path: "/verify-email"},
		repo.MailResetPassword: {purpose: repo.TokenPasswordReset, ttl: _resetTTL, path: "/reset-password"},
	}
}

// VerifyEmail implements usecase.Auth.
func (s *Service) VerifyEmail(ctx context.Context, token string) error {
	userID, found, err := s.Tokens.Consume(ctx, repo.TokenEmailVerify, hashToken(token))
	if err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	if !found {
		return entity.New(entity.KeyVerificationTokenInvalid)
	}

	return s.Users.MarkEmailVerified(ctx, userID) //nolint:wrapcheck // repo errors are wrapped
}

// ResendVerification implements usecase.Auth; a verified email gets no mail.
func (s *Service) ResendVerification(ctx context.Context, caller *usecase.Caller, locale string) error {
	rec, found, err := s.Users.Get(ctx, caller.UserID)
	if err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	if !found || rec.User.Email == nil || rec.User.EmailVerified {
		return nil
	}

	return s.MailQueue.Enqueue(ctx, &repo.MailJob{Kind: repo.MailVerifyEmail, UserID: caller.UserID, Locale: locale}) //nolint:wrapcheck // repo errors are wrapped
}

// ForgotPassword implements usecase.Auth: the answer is the same whether the email exists.
func (s *Service) ForgotPassword(ctx context.Context, c *usecase.Client, email string) error {
	email = entity.NormalizeEmail(email)

	if err := s.Limits.Consume(ctx, _ruleMailAccount, ratelimit.AccountSubject(email)); err != nil {
		return err //nolint:wrapcheck // entity errors
	}

	rec, found, err := s.Users.GetByEmail(ctx, email)
	if err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	if !found || !rec.User.Active() {
		return nil
	}

	return s.MailQueue.Enqueue(ctx, &repo.MailJob{Kind: repo.MailResetPassword, UserID: rec.User.ID, Locale: c.Locale}) //nolint:wrapcheck // repo errors are wrapped
}

// ResetPassword implements usecase.Auth: it signs every session of the user out. The password is
// hashed before the first statement of the request transaction.
func (s *Service) ResetPassword(ctx context.Context, token, pw string) (uuid.UUID, error) {
	hash, err := s.Hasher.Hash(ctx, pw)
	if err != nil {
		return uuid.Nil(), err //nolint:wrapcheck // wrapped by password
	}

	userID, found, err := s.Tokens.Consume(ctx, repo.TokenPasswordReset, hashToken(token))
	if err != nil {
		return uuid.Nil(), err //nolint:wrapcheck // repo errors are wrapped
	}

	if !found {
		return uuid.Nil(), entity.New(entity.KeyVerificationTokenInvalid)
	}

	if err := s.Users.SetPassword(ctx, userID, hash); err != nil {
		return uuid.Nil(), err //nolint:wrapcheck // repo errors are wrapped
	}

	return userID, s.Revocation.All(ctx, userID, nil) //nolint:wrapcheck // entity or repo errors
}

// DeliverMail runs a mail job: it issues a new one-time token, which invalidates the previous
// ones, and mails its link after the token is stored. Users without email, or with a verified
// email for a verification mail, get nothing.
func (s *Service) DeliverMail(ctx context.Context, job *repo.MailJob) error {
	kind, ok := mailKinds()[job.Kind]
	if !ok {
		s.Log.WarnContext(ctx, "unknown mail kind", "kind", job.Kind)

		return nil
	}

	var mail *repo.Mail

	err := s.Tx.InTx(ctx, func(ctx context.Context) error {
		var err error

		mail, err = s.prepareMail(ctx, job, &kind)

		return err
	})
	if err != nil || mail == nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	return s.Mailer.Send(ctx, mail) //nolint:wrapcheck // wrapped by the mailer
}

// prepareMail issues the token of job and renders its mail; nil when the user gets none. The
// user row stays locked until the token is stored.
func (s *Service) prepareMail(ctx context.Context, job *repo.MailJob, kind *mailKind) (*repo.Mail, error) {
	rec, found, err := s.Users.GetForUpdate(ctx, job.UserID)
	if err != nil || !found || rec.User.Email == nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if job.Kind == repo.MailVerifyEmail && rec.User.EmailVerified {
		return nil, nil //nolint:nilnil // nothing to send
	}

	token, hash, err := randomToken()
	if err != nil {
		return nil, err
	}

	if err := s.Tokens.Issue(ctx, job.UserID, kind.purpose, hash, s.Now().Add(kind.ttl)); err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	mail, err := s.Renderer.Render(job.Kind, job.Locale, &MailData{DisplayName: rec.User.DisplayName, Link: s.link(kind.path, token)})
	if err != nil {
		return nil, err //nolint:wrapcheck // wrapped by the renderer
	}

	mail.To = *rec.User.Email

	return mail, nil
}

func (s *Service) link(path, token string) string {
	return strings.TrimRight(s.cfg.FrontendURL, "/") + path + "?token=" + url.QueryEscape(token)
}
