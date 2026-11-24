// Package auth registers users, signs them in with email and password, rotates refresh tokens
// and manages the one-time email tokens (platform conventions §5, §11.1).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/internal/usecase/session"
	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
	"github.com/xingyunqi-tech/go-template/pkg/password"
	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
)

const (
	_tokenBytes = 32
	// Rule names counted by this use case.
	_ruleLoginAccount = "auth-login-account"
	_ruleMailAccount  = "auth-mail-account"
	_ruleRefresh      = "auth-refresh"
)

// Signer signs access tokens.
type Signer interface {
	Sign(c *jwtx.Claims) (string, error)
}

// TxRunner runs fn in a transaction of its own.
type TxRunner interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// MailRenderer renders a mail of kind in locale.
type MailRenderer interface {
	Render(kind, locale string, data *MailData) (*repo.Mail, error)
}

// MailData fills the mail templates.
type MailData struct {
	DisplayName string
	Link        string
}

// Config are the token lifetimes and the base of the mail links.
type Config struct {
	AccessTTL   time.Duration
	RefreshTTL  time.Duration
	FrontendURL string
}

// Deps are what the Service uses.
type Deps struct {
	Users      repo.Users
	Identities repo.Identities
	Sessions   repo.Sessions
	Tokens     repo.Tokens
	MailQueue  repo.MailQueue
	Mailer     repo.Mailer
	Renderer   MailRenderer
	Grace      repo.RefreshGrace
	Revocation *session.Revocation
	Signer     Signer
	Codes      CodeVerifier
	Providers  []Provider
	Hasher     *password.Hasher
	Limits     usecase.Limits
	Tx         TxRunner
	Log        *slog.Logger
	Now        func() time.Time
}

// Service implements usecase.Auth.
type Service struct {
	Deps

	cfg       Config
	providers map[string]Provider
}

var _ usecase.Auth = (*Service)(nil)

// New returns a Service; a nil d.Now is time.Now.
func New(cfg Config, d *Deps) *Service {
	s := &Service{Deps: *d, cfg: cfg, providers: map[string]Provider{}}
	for _, p := range d.Providers {
		s.providers[p.ID()] = p
	}

	if s.Now == nil {
		s.Now = time.Now
	}

	return s
}

// randomToken returns a 32-byte random token, base64url, and its SHA-256.
func randomToken() (token string, hash []byte, err error) {
	raw := make([]byte, _tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("auth: random token: %w", err)
	}

	token = base64.RawURLEncoding.EncodeToString(raw)

	return token, hashToken(token), nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))

	return sum[:]
}

// pair is an issued access and refresh token.
type pair struct {
	Access    string `json:"access"`
	Refresh   string `json:"refresh"`
	ExpiresIn int64  `json:"expiresIn"`
}

func (s *Service) access(u *entity.User, sid uuid.UUID, authTime time.Time) (string, error) {
	iat, exp := jwtx.Lifetime(s.Now(), s.cfg.AccessTTL)

	token, err := s.Signer.Sign(&jwtx.Claims{
		Subject: u.ID, SessionID: sid, Roles: u.Roles, EmailVerified: u.EmailVerified, AuthTime: authTime,
		IssuedAt: iat, Expires: exp, ID: uuid.NewV7().String(),
	})
	if err != nil {
		return "", fmt.Errorf("auth: %w", err)
	}

	return token, nil
}

func (s *Service) result(p *pair, u *entity.User) *usecase.LoginResult {
	return &usecase.LoginResult{AccessToken: p.Access, RefreshToken: p.Refresh, ExpiresIn: p.ExpiresIn, User: u}
}

// signIn opens a session for u.
func (s *Service) signIn(ctx context.Context, c *usecase.Client, u *entity.User) (*usecase.LoginResult, error) {
	refresh, hash, err := randomToken()
	if err != nil {
		return nil, err
	}

	now := s.Now()
	sid := uuid.NewV7()

	if err := s.Sessions.Create(ctx, &repo.NewSession{
		ID: sid, UserID: u.ID, RefreshHash: hash, AuthTime: now, ExpiresAt: now.Add(s.cfg.RefreshTTL),
		UserAgent: c.UserAgent, IP: c.IP,
	}); err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	access, err := s.access(u, sid, now)
	if err != nil {
		return nil, err
	}

	return s.result(&pair{Access: access, Refresh: refresh, ExpiresIn: int64(s.cfg.AccessTTL.Seconds())}, u), nil
}

// Register implements usecase.Auth: the password is hashed before the transaction that stores
// the user, its session and the verification mail job.
func (s *Service) Register(ctx context.Context, c *usecase.Client, r *usecase.Registration) (*usecase.LoginResult, error) {
	hash, err := s.Hasher.Hash(ctx, r.Password)
	if err != nil {
		return nil, err //nolint:wrapcheck // wrapped by password
	}

	email := entity.NormalizeEmail(r.Email)

	var out *usecase.LoginResult

	err = s.Tx.InTx(ctx, func(ctx context.Context) error {
		nu := newUser(&email, nil)
		nu.PasswordHash, nu.DisplayName = &hash, r.DisplayName

		u, err := s.Users.Create(ctx, nu)
		if err != nil {
			return err //nolint:wrapcheck // repo errors are wrapped
		}

		if err := s.MailQueue.Enqueue(ctx, &repo.MailJob{Kind: repo.MailVerifyEmail, UserID: u.ID, Locale: c.Locale}); err != nil {
			return err //nolint:wrapcheck // repo errors are wrapped
		}

		out, err = s.signIn(ctx, c, u)

		return err
	})

	return out, err //nolint:wrapcheck // entity or repo errors
}

// Login implements usecase.Auth: wrong credentials count against auth-login-account, a
// successful login clears it. The password is checked before the transaction that opens the
// session.
func (s *Service) Login(ctx context.Context, c *usecase.Client, email, pw string) (*usecase.LoginResult, error) {
	email = entity.NormalizeEmail(email)
	subject := ratelimit.AccountSubject(email)

	if err := s.Limits.Peek(ctx, _ruleLoginAccount, subject); err != nil {
		return nil, err //nolint:wrapcheck // entity errors
	}

	rec, ok, rehash, err := s.credentials(ctx, email, pw)
	if err != nil {
		return nil, err
	}

	if !ok {
		if err := s.Limits.Consume(ctx, _ruleLoginAccount, subject); err != nil {
			return nil, err //nolint:wrapcheck // entity errors
		}

		return nil, entity.New(entity.KeyInvalidCredentials)
	}

	if err := s.Limits.Reset(ctx, _ruleLoginAccount, subject); err != nil {
		return nil, err //nolint:wrapcheck // entity errors
	}

	if !rec.User.Active() {
		return nil, entity.New(entity.KeyAccountDisabled)
	}

	return s.openSession(ctx, c, rec, pw, rehash)
}

// openSession stores the rehashed password when the parameters changed and opens the session
// in one transaction.
func (s *Service) openSession(ctx context.Context, c *usecase.Client, rec *repo.UserRecord, pw string, rehash bool) (*usecase.LoginResult, error) {
	var newHash *string

	if rehash {
		hash, err := s.Hasher.Hash(ctx, pw)
		if err != nil {
			return nil, err //nolint:wrapcheck // wrapped by password
		}

		newHash = &hash
	}

	var out *usecase.LoginResult

	err := s.Tx.InTx(ctx, func(ctx context.Context) error {
		if newHash != nil {
			if err := s.Users.SetPassword(ctx, rec.User.ID, *newHash); err != nil {
				return err //nolint:wrapcheck // repo errors are wrapped
			}
		}

		var err error

		out, err = s.signIn(ctx, c, &rec.User)

		return err
	})

	return out, err //nolint:wrapcheck // entity or repo errors
}

// credentials checks pw against the account of email; an unknown account is checked against
// the dummy hash and never matches.
func (s *Service) credentials(ctx context.Context, email, pw string) (rec *repo.UserRecord, ok, rehash bool, err error) {
	rec, found, err := s.Users.GetByEmail(ctx, email)
	if err != nil {
		return nil, false, false, err //nolint:wrapcheck // repo errors are wrapped
	}

	encoded := ""
	if found && rec.PasswordHash != nil {
		encoded = *rec.PasswordHash
	}

	ok, rehash, err = s.Hasher.Verify(ctx, encoded, pw)
	if err != nil {
		return nil, false, false, err //nolint:wrapcheck // wrapped by password
	}

	return rec, ok, rehash, nil
}

// Logout implements usecase.Auth.
func (s *Service) Logout(ctx context.Context, caller *usecase.Caller) error {
	_, err := s.Revocation.One(ctx, caller.UserID, caller.SessionID)

	return err //nolint:wrapcheck // entity or repo errors
}
