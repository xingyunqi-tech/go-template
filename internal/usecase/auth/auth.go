// Package auth implements the nestjs AuthService flows: email, phone, SMS, WeChat and Alipay
// sign-in, token refresh, profile updates, account deletion and session management.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase/session"
	"github.com/xingyunqi-tech/go-template/internal/usecase/user"
	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
)

const (
	_fingerprintLength = 16
	_roleEntity        = "RoleEntity"
)

// Secret is a signing secret with the token lifetime in milliseconds.
type Secret struct {
	Key           string
	ExpiresMillis float64
}

// Config holds the four token secrets.
type Config struct {
	Access       Secret
	Refresh      Secret
	Forgot       Secret
	ConfirmEmail Secret
}

// Mail is a templated email; Data holds the template variables.
type Mail struct {
	Template string
	To       string
	Data     map[string]any
}

// Mailer queues templated emails.
type Mailer interface {
	Send(ctx context.Context, m Mail) error
}

// DefaultSettings creates the default notification settings of a new user.
type DefaultSettings interface {
	InitializeDefaultSettings(ctx context.Context, userID int) error
}

// Verifier checks passwords and reports when the stored hash should be replaced.
type Verifier interface {
	user.Hasher
	VerifyAndUpdate(ctx context.Context, stored string, candidates ...string) (ok bool, updated string, err error)
}

// SMSCodes verifies one-time SMS codes.
type SMSCodes interface {
	Verify(ctx context.Context, phone, code, purpose string) (bool, error)
}

// Client describes the device signing in.
type Client struct {
	UserAgent *string
	IP        *string
}

// RoleClaim is the role in access tokens: {id, name, __entity} at sign-in, {id} on refresh.
type RoleClaim struct {
	ID     int    `json:"id"`
	Name   string `json:"name,omitempty"`
	Entity string `json:"__entity,omitempty"`
}

type accessClaims struct {
	ID        int        `json:"id"`
	Role      *RoleClaim `json:"role"`
	SessionID int        `json:"sessionId"`
}

type refreshClaims struct {
	SessionID int    `json:"sessionId"`
	Hash      string `json:"hash"`
}

// Tokens are the issued tokens; TokenExpires is in milliseconds since the epoch.
type Tokens struct {
	RefreshToken string
	Token        string
	TokenExpires int64
}

// Login is a sign-in result; Created is set for users created by the sign-in.
type Login struct {
	Tokens  Tokens
	User    *entity.User
	Created *user.Created
}

// Principal is the authenticated caller of an access-token route.
type Principal struct {
	UserID    int
	SessionID int
	RoleID    *int
}

// RefreshPrincipal is the caller of the refresh route.
type RefreshPrincipal struct {
	SessionID int
	Hash      string
}

// Service implements the auth flows.
type Service struct {
	cfg      Config
	users    *user.Service
	userRepo repo.Users
	sessions *session.Service
	sessRepo repo.Sessions
	cache    repo.Cache
	tx       repo.Transactions
	hasher   Verifier
	mailer   Mailer
	sms      SMSCodes
	log      *slog.Logger
	now      func() time.Time
	settings DefaultSettings
	sender   SMSSender
	wechat   Wechat
	alipay   Alipay
	bindings repo.UserBindings
}

// Deps are the Service dependencies.
type Deps struct {
	Config   Config
	Users    *user.Service
	UserRepo repo.Users
	Sessions *session.Service
	SessRepo repo.Sessions
	Cache    repo.Cache
	Tx       repo.Transactions
	Hasher   Verifier
	Mailer   Mailer
	SMS      SMSCodes
	Log      *slog.Logger
	Settings DefaultSettings
	Sender   SMSSender
	Wechat   Wechat
	Alipay   Alipay
	Bindings repo.UserBindings
}

// New returns a Service.
func New(d *Deps) *Service {
	return &Service{
		cfg: d.Config, users: d.Users, userRepo: d.UserRepo, sessions: d.Sessions, sessRepo: d.SessRepo,
		cache: d.Cache, tx: d.Tx, hasher: d.Hasher, mailer: d.Mailer, sms: d.SMS, log: d.Log, now: time.Now,
		settings: d.Settings,
		sender:   d.Sender, wechat: d.Wechat, alipay: d.Alipay, bindings: d.Bindings,
	}
}

// SessionAuthTTL is how long the guard caches a session's status and role.
const SessionAuthTTL = 30 * time.Second

type sessionAuth struct {
	StatusID *int `json:"statusId,omitempty"`
	RoleID   *int `json:"roleId"`
}

// Authenticate validates an access token like the nestjs JwtStrategy.
func (s *Service) Authenticate(ctx context.Context, token string) (*Principal, error) {
	userID, sessionID, err := s.accessClaims(token)
	if err != nil {
		return nil, err
	}

	var cached sessionAuth

	found, err := s.cache.Get(ctx, session.AuthCacheKey(sessionID), &cached)
	if err != nil {
		return nil, fmt.Errorf("auth: cache: %w", err)
	}

	if found {
		return cachedPrincipal(&cached, userID, sessionID)
	}

	row, err := s.sessRepo.Authenticate(ctx, sessionID)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if row == nil || row.UserID != userID {
		return nil, entity.Unauthorized("Session expired")
	}

	if !entity.CanAuthenticate(row.StatusID) {
		return nil, entity.Unauthorized("User is not active")
	}

	status := row.StatusID
	if err := s.cache.Set(ctx, session.AuthCacheKey(sessionID), sessionAuth{StatusID: &status, RoleID: row.RoleID}, SessionAuthTTL); err != nil {
		return nil, fmt.Errorf("auth: cache: %w", err)
	}

	return &Principal{UserID: userID, SessionID: sessionID, RoleID: row.RoleID}, nil
}

func cachedPrincipal(cached *sessionAuth, userID, sessionID int) (*Principal, error) {
	if cached.StatusID == nil || !entity.CanAuthenticate(*cached.StatusID) {
		return nil, entity.Unauthorized("User is not active")
	}

	return &Principal{UserID: userID, SessionID: sessionID, RoleID: cached.RoleID}, nil
}

func (s *Service) accessClaims(token string) (userID, sessionID int, err error) {
	claims, err := jwtx.Verify(token, s.cfg.Access.Key)
	if err != nil {
		return 0, 0, entity.Unauthorized("")
	}

	userID, okUser := jwtx.Int(claims, "id")
	sessionID, okSession := jwtx.Int(claims, "sessionId")

	if !okUser || !okSession || userID == 0 || sessionID == 0 {
		return 0, 0, entity.Unauthorized("")
	}

	return userID, sessionID, nil
}

// AuthenticateRefresh validates a refresh token like the nestjs JwtRefreshStrategy.
func (s *Service) AuthenticateRefresh(token string) (*RefreshPrincipal, error) {
	claims, err := jwtx.Verify(token, s.cfg.Refresh.Key)
	if err != nil {
		return nil, entity.Unauthorized("")
	}

	sessionID, ok := jwtx.Int(claims, "sessionId")
	if !ok || sessionID == 0 {
		return nil, entity.Unauthorized("")
	}

	hash, _ := jwtx.String(claims, "hash")

	return &RefreshPrincipal{SessionID: sessionID, Hash: hash}, nil
}

func roleClaim(u *entity.User) *RoleClaim {
	if u.Role == nil {
		return nil
	}

	return &RoleClaim{ID: u.Role.ID, Name: u.Role.Name, Entity: _roleEntity}
}

func (s *Service) tokens(userID int, role *RoleClaim, sessionID int, hash string) (Tokens, error) {
	now := s.now()

	token, err := jwtx.Sign(accessClaims{ID: userID, Role: role, SessionID: sessionID}, s.cfg.Access.Key, s.cfg.Access.ExpiresMillis, now)
	if err != nil {
		return Tokens{}, fmt.Errorf("auth: sign: %w", err)
	}

	refresh, err := jwtx.Sign(refreshClaims{SessionID: sessionID, Hash: hash}, s.cfg.Refresh.Key, s.cfg.Refresh.ExpiresMillis, now)
	if err != nil {
		return Tokens{}, fmt.Errorf("auth: sign: %w", err)
	}

	return Tokens{RefreshToken: refresh, Token: token, TokenExpires: now.UnixMilli() + int64(s.cfg.Access.ExpiresMillis)}, nil
}

func (s *Service) signIn(ctx context.Context, u *entity.User, client Client) (*Login, error) {
	hash, err := session.NewHash()
	if err != nil {
		return nil, err //nolint:wrapcheck // session errors are wrapped
	}

	sess, err := s.sessions.Create(ctx, u.ID, hash, client.UserAgent, client.IP)
	if err != nil {
		return nil, err //nolint:wrapcheck // session errors are wrapped
	}

	tokens, err := s.tokens(u.ID, roleClaim(u), sess.ID, hash)
	if err != nil {
		return nil, err
	}

	return &Login{Tokens: tokens, User: u, Created: nil}, nil
}

func (s *Service) verifyPassword(ctx context.Context, u *entity.User, md5 string) (bool, error) {
	if u.Password == nil || *u.Password == "" {
		return false, nil
	}

	candidates := []string{md5}
	if upper := strings.ToUpper(md5); upper != md5 {
		candidates = append(candidates, upper)
	}

	ok, updated, err := s.hasher.VerifyAndUpdate(ctx, *u.Password, candidates...)
	if err != nil {
		return false, err //nolint:wrapcheck // hasher errors are wrapped
	}

	if ok && updated != "" {
		if err := s.userRepo.RehashPassword(ctx, u.ID, *u.Password, updated); err != nil {
			return false, err //nolint:wrapcheck // repo errors are wrapped
		}
	}

	return ok, nil
}

// LoginWithEmail is POST auth/email/login.
func (s *Service) LoginWithEmail(ctx context.Context, email, password string, client Client) (*Login, error) {
	u, err := s.users.FindByEmail(ctx, email)
	if err != nil {
		return nil, err //nolint:wrapcheck // user errors are wrapped
	}

	if u == nil {
		return nil, entity.Invalid("email", "notFound")
	}

	ok, err := s.verifyPassword(ctx, u, password)
	if err != nil {
		return nil, err
	}

	if !ok {
		return nil, entity.Invalid("password", "incorrectPassword")
	}

	if !entity.CanAuthenticate(u.StatusID()) {
		return nil, entity.Invalid("email", "userNotActive")
	}

	return s.signIn(ctx, u, client)
}

// sendMail queues m in the request transaction, so it is sent only once the transaction commits.
func (s *Service) sendMail(ctx context.Context, m Mail) error {
	return s.mailer.Send(ctx, m) //nolint:wrapcheck // queue errors are wrapped
}

func (s *Service) sign(payload map[string]any, secret Secret) (string, error) {
	token, err := jwtx.Sign(payload, secret.Key, secret.ExpiresMillis, s.now())
	if err != nil {
		return "", fmt.Errorf("auth: sign: %w", err)
	}

	return token, nil
}

// Register is POST auth/email/register: an inactive user and a confirmation email.
func (s *Service) Register(ctx context.Context, in *user.Input) error {
	in.Role = user.Ref{Set: true, Null: false, ID: fmt.Sprint(entity.RoleUser)}
	in.Status = user.Ref{Set: true, Null: false, ID: fmt.Sprint(entity.StatusInactive)}

	u, _, err := s.users.Create(ctx, in)
	if err != nil {
		return err //nolint:wrapcheck // user errors are wrapped
	}

	s.initializeSettings(ctx, u.ID)

	hash, err := s.sign(map[string]any{"confirmEmailUserId": u.ID}, s.cfg.ConfirmEmail)
	if err != nil {
		return err
	}

	return s.sendMail(ctx, Mail{Template: "activation", To: deref(in.Email.Value), Data: map[string]any{"hash": hash}})
}

// initializeSettings creates the default notification settings; a failure only logs.
func (s *Service) initializeSettings(ctx context.Context, userID int) {
	if s.settings == nil {
		return
	}

	if err := s.settings.InitializeDefaultSettings(ctx, userID); err != nil {
		s.log.WarnContext(ctx, "failed to initialize notification settings", "user_id", userID, "error", err.Error())
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}

	return *p
}

func (s *Service) decode(hash string, secret Secret) (map[string]any, error) {
	claims, err := jwtx.Verify(hash, secret.Key)

	switch {
	case errors.Is(err, jwtx.ErrExpired):
		return nil, entity.Invalid("hash", "tokenExpired")
	case err != nil:
		return nil, entity.Invalid("hash", "invalidHash")
	}

	return claims, nil
}

func (s *Service) userFromClaim(ctx context.Context, claims map[string]any, key string) (*entity.User, error) {
	id, _ := jwtx.Int(claims, key)

	return s.users.FindByID(ctx, id) //nolint:wrapcheck // user errors are wrapped
}

// ConfirmEmail is POST auth/email/confirm.
func (s *Service) ConfirmEmail(ctx context.Context, hash string) error {
	claims, err := s.decode(hash, s.cfg.ConfirmEmail)
	if err != nil {
		return err
	}

	u, err := s.userFromClaim(ctx, claims, "confirmEmailUserId")
	if err != nil {
		return err
	}

	if u == nil {
		return entity.NotFoundKey("notFound")
	}

	if u.StatusID() != entity.StatusInactive {
		return entity.Invalid("hash", "alreadyConfirmed")
	}

	_, err = s.users.Update(ctx, u.ID, &user.Input{Status: user.Ref{Set: true, Null: false, ID: fmt.Sprint(entity.StatusActive)}})

	return err //nolint:wrapcheck // user errors are wrapped
}

// ConfirmNewEmail is POST auth/email/confirm/new.
func (s *Service) ConfirmNewEmail(ctx context.Context, hash string) error {
	claims, err := s.decode(hash, s.cfg.ConfirmEmail)
	if err != nil {
		return err
	}

	u, err := s.userFromClaim(ctx, claims, "confirmEmailUserId")
	if err != nil {
		return err
	}

	if u == nil {
		return entity.NotFoundKey("notFound")
	}

	in := user.Input{Email: repo.Optional[string]{Set: false, Value: nil}}

	switch email, present := claims["newEmail"]; {
	case !present:
	case email == nil:
		in.Email = repo.Null[string]()
	default:
		in.Email = repo.Some(fmt.Sprint(email))
	}

	if u.StatusID() == entity.StatusInactive {
		in.Status = user.Ref{Set: true, Null: false, ID: fmt.Sprint(entity.StatusActive)}
	}

	_, err = s.users.Update(ctx, u.ID, &in)

	return err //nolint:wrapcheck // user errors are wrapped
}

func fingerprint(hash *string) string {
	sum := sha256.Sum256([]byte(deref(hash)))

	return hex.EncodeToString(sum[:])[:_fingerprintLength]
}

// ForgotPassword is POST auth/forgot/password.
func (s *Service) ForgotPassword(ctx context.Context, email string) error {
	u, err := s.users.FindByEmail(ctx, email)
	if err != nil {
		return err //nolint:wrapcheck // user errors are wrapped
	}

	if u == nil {
		return entity.Invalid("email", "emailNotExists")
	}

	expires := s.now().UnixMilli() + int64(s.cfg.Forgot.ExpiresMillis)

	hash, err := s.sign(map[string]any{"forgotUserId": u.ID, "pwd": fingerprint(u.Password)}, s.cfg.Forgot)
	if err != nil {
		return err
	}

	return s.sendMail(ctx, Mail{Template: "reset-password", To: email, Data: map[string]any{"hash": hash, "tokenExpires": expires}})
}

// ResetPassword is POST auth/reset/password.
func (s *Service) ResetPassword(ctx context.Context, hash, password string) error {
	claims, err := s.decode(hash, s.cfg.Forgot)
	if err != nil {
		return err
	}

	u, err := s.userFromClaim(ctx, claims, "forgotUserId")
	if err != nil {
		return err
	}

	if u == nil {
		return entity.Invalid("hash", "notFound")
	}

	if pwd, _ := jwtx.String(claims, "pwd"); pwd != fingerprint(u.Password) {
		return entity.Invalid("hash", "invalidHash")
	}

	fresh, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return err //nolint:wrapcheck // hasher errors are wrapped
	}

	ok, err := s.userRepo.ReplacePassword(ctx, u.ID, u.Password, fresh)
	if err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	if !ok {
		return entity.Invalid("hash", "invalidHash")
	}

	return s.sessions.DeleteAllForUser(ctx, u.ID, nil) //nolint:wrapcheck // session errors are wrapped
}

// Me is GET auth/me.
func (s *Service) Me(ctx context.Context, p *Principal) (*entity.User, error) {
	return s.users.FindByID(ctx, p.UserID) //nolint:wrapcheck // user errors are wrapped
}

func (s *Service) verifyIdentity(ctx context.Context, u *entity.User, oldPassword, smsCode *string) error {
	if u.Password != nil && *u.Password != "" {
		return s.checkOldPassword(ctx, u, oldPassword)
	}

	if u.Phone == nil || *u.Phone == "" {
		return entity.Invalid("phone", "phoneRequiredForVerification")
	}

	if smsCode == nil || *smsCode == "" {
		return entity.Invalid("smsCode", "missingSmsCode")
	}

	ok, err := s.sms.Verify(ctx, *u.Phone, *smsCode, "verify_identity")
	if err != nil {
		return err //nolint:wrapcheck // sms errors are wrapped
	}

	if !ok {
		return entity.Invalid("code", "invalidCode")
	}

	return nil
}

// UpdateMe is PUT auth/me.
func (s *Service) UpdateMe(ctx context.Context, p *Principal, in *user.Input, oldPassword, smsCode *string) (*entity.User, error) {
	u, err := s.users.FindByID(ctx, p.UserID)
	if err != nil {
		return nil, err //nolint:wrapcheck // user errors are wrapped
	}

	if u == nil {
		return nil, entity.Invalid("user", "userNotFound")
	}

	if err := s.prepareProfileUpdate(ctx, p, u, in, oldPassword, smsCode); err != nil {
		return nil, err
	}

	if _, err := s.users.Update(ctx, p.UserID, in); err != nil {
		return nil, err //nolint:wrapcheck // user errors are wrapped
	}

	return s.users.FindByID(ctx, p.UserID) //nolint:wrapcheck // user errors are wrapped
}

func nonEmpty(o repo.Optional[string]) bool {
	return o.Set && o.Value != nil && *o.Value != ""
}

// prepareProfileUpdate checks the old password for a password change, signing out the other
// sessions, and turns an email change into a confirmation email.
func (s *Service) prepareProfileUpdate(ctx context.Context, p *Principal, u *entity.User, in *user.Input, oldPassword, smsCode *string) error {
	if nonEmpty(in.Password) {
		if err := s.checkOldPassword(ctx, u, oldPassword); err != nil {
			return err
		}

		if err := s.sessions.DeleteAllForUser(ctx, u.ID, &p.SessionID); err != nil {
			return err //nolint:wrapcheck // session errors are wrapped
		}
	}

	if nonEmpty(in.Email) && (u.Email == nil || *in.Email.Value != *u.Email) {
		if err := s.requestEmailChange(ctx, u, *in.Email.Value, oldPassword, smsCode); err != nil {
			return err
		}

		in.Email = repo.Optional[string]{Set: false, Value: nil}
	}

	return nil
}

func (s *Service) checkOldPassword(ctx context.Context, u *entity.User, oldPassword *string) error {
	if oldPassword == nil || *oldPassword == "" {
		return entity.Invalid("oldPassword", "missingOldPassword")
	}

	ok, err := s.verifyPassword(ctx, u, *oldPassword)
	if err != nil {
		return err
	}

	if !ok {
		return entity.Invalid("oldPassword", "incorrectOldPassword")
	}

	return nil
}

func (s *Service) requestEmailChange(ctx context.Context, u *entity.User, email string, oldPassword, smsCode *string) error {
	existing, err := s.users.FindByEmail(ctx, email)
	if err != nil {
		return err //nolint:wrapcheck // user errors are wrapped
	}

	if existing != nil && existing.ID != u.ID {
		return entity.Invalid("email", "emailExists")
	}

	if err := s.verifyIdentity(ctx, u, oldPassword, smsCode); err != nil {
		return err
	}

	hash, err := s.sign(map[string]any{"confirmEmailUserId": u.ID, "newEmail": email}, s.cfg.ConfirmEmail)
	if err != nil {
		return err
	}

	return s.sendMail(ctx, Mail{Template: "confirm-new-email", To: email, Data: map[string]any{"hash": hash}})
}

// Refresh is POST auth/refresh.
func (s *Service) Refresh(ctx context.Context, p *RefreshPrincipal) (*Tokens, error) {
	sess, owner, err := s.sessionOwner(ctx, p.SessionID)
	if err != nil {
		return nil, err
	}

	if sess.Hash != p.Hash {
		return nil, s.rejectStaleHash(ctx, sess.ID, p.Hash)
	}

	if owner.Role == nil {
		return nil, entity.Unauthorized("")
	}

	if !entity.CanAuthenticate(owner.StatusID()) {
		return nil, entity.Invalid("refresh", "userNotActive")
	}

	hash, err := session.NewHash()
	if err != nil {
		return nil, err //nolint:wrapcheck // session errors are wrapped
	}

	tokens, err := s.tokens(owner.ID, &RoleClaim{ID: owner.Role.ID, Name: "", Entity: ""}, sess.ID, hash)
	if err != nil {
		return nil, err
	}

	ok, err := s.sessions.Rotate(ctx, sess.ID, p.Hash, hash)
	if err != nil {
		return nil, err //nolint:wrapcheck // session errors are wrapped
	}

	if !ok {
		return nil, entity.Unauthorized("")
	}

	return &tokens, nil
}

// sessionOwner loads the session and its user; either missing is a 401.
func (s *Service) sessionOwner(ctx context.Context, sessionID int) (*entity.Session, *entity.User, error) {
	sess, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // session errors are wrapped
	}

	if sess == nil || sess.UserID == nil {
		return nil, nil, entity.Unauthorized("")
	}

	owner, err := s.users.FindByID(ctx, *sess.UserID)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // user errors are wrapped
	}

	if owner == nil {
		return nil, nil, entity.Unauthorized("")
	}

	return sess, owner, nil
}

// rejectStaleHash revokes the session unless hash was replaced within the grace window.
func (s *Service) rejectStaleHash(ctx context.Context, sessionID int, hash string) error {
	recent, err := s.sessions.WasRecentlyRotatedFrom(ctx, sessionID, hash)
	if err != nil {
		return err //nolint:wrapcheck // session errors are wrapped
	}

	if !recent {
		if err := s.sessions.Delete(ctx, sessionID); err != nil {
			return err //nolint:wrapcheck // session errors are wrapped
		}
	}

	return entity.Unauthorized("")
}

// DeleteMe is DELETE auth/me.
func (s *Service) DeleteMe(ctx context.Context, p *Principal, oldPassword, smsCode *string) error {
	u, err := s.users.FindByID(ctx, p.UserID)
	if err != nil {
		return err //nolint:wrapcheck // user errors are wrapped
	}

	if u == nil {
		return entity.Invalid("user", "userNotFound")
	}

	if err := s.verifyIdentity(ctx, u, oldPassword, smsCode); err != nil {
		return err
	}

	return s.users.Remove(ctx, p.UserID) //nolint:wrapcheck // user errors are wrapped
}

// ChangePassword is PUT auth/password.
func (s *Service) ChangePassword(ctx context.Context, p *Principal, oldPassword, newPassword string) error {
	u, err := s.users.FindByID(ctx, p.UserID)
	if err != nil {
		return err //nolint:wrapcheck // user errors are wrapped
	}

	if u == nil {
		return entity.Invalid("user", "userNotFound")
	}

	ok, err := s.verifyPassword(ctx, u, oldPassword)
	if err != nil {
		return err
	}

	if !ok {
		return entity.Invalid("oldPassword", "incorrectOldPassword")
	}

	if _, err := s.users.Update(ctx, p.UserID, &user.Input{Password: repo.Some(newPassword)}); err != nil {
		return err //nolint:wrapcheck // user errors are wrapped
	}

	return s.sessions.DeleteAllForUser(ctx, p.UserID, &p.SessionID) //nolint:wrapcheck // session errors are wrapped
}

// Sessions is GET auth/sessions.
func (s *Service) Sessions(ctx context.Context, p *Principal) ([]entity.Session, error) {
	return s.sessions.ListActive(ctx, p.UserID) //nolint:wrapcheck // session errors are wrapped
}

// RevokeSession is DELETE auth/sessions/:id.
func (s *Service) RevokeSession(ctx context.Context, p *Principal, id int) error {
	ok, err := s.sessions.RevokeForUser(ctx, id, p.UserID)
	if err != nil {
		return err //nolint:wrapcheck // session errors are wrapped
	}

	if !ok {
		return entity.NotFoundKey("sessionNotFound")
	}

	return nil
}

// RevokeOtherSessions is POST auth/sessions/revoke-others.
func (s *Service) RevokeOtherSessions(ctx context.Context, p *Principal) error {
	return s.sessions.DeleteAllForUser(ctx, p.UserID, &p.SessionID) //nolint:wrapcheck // session errors are wrapped
}

// Logout is POST auth/logout.
func (s *Service) Logout(ctx context.Context, p *Principal) error {
	return s.sessions.Delete(ctx, p.SessionID) //nolint:wrapcheck // session errors are wrapped
}
