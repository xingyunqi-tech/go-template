package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/pkg/clientip"
	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
)

const (
	// _gracePeriod is how long a rotated refresh token still returns the pair it was rotated to.
	_gracePeriod = 30 * time.Second
	_graceInfo   = "refresh-grace"
	_aesKeyBytes = 32
)

var errSealed = errors.New("auth: sealed token pair is malformed")

// graceKey names the grace entry of a rotated refresh token.
func graceKey(hash []byte) string {
	return hex.EncodeToString(hash)
}

// sealKey derives the AES-256 key of a grace entry from the refresh token it replaces.
func sealKey(refresh string) ([]byte, error) {
	key, err := hkdf.Key(sha256.New, []byte(refresh), nil, _graceInfo, _aesKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("auth: hkdf: %w", err)
	}

	return key, nil
}

func gcm(refresh string) (cipher.AEAD, error) {
	key, err := sealKey(refresh)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("auth: aes: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("auth: gcm: %w", err)
	}

	return aead, nil
}

func seal(refresh string, p *pair) ([]byte, error) {
	aead, err := gcm(refresh)
	if err != nil {
		return nil, err
	}

	plain, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("auth: seal: %w", err)
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("auth: nonce: %w", err)
	}

	return aead.Seal(nonce, nonce, plain, nil), nil
}

func unseal(refresh string, sealed []byte) (*pair, error) {
	aead, err := gcm(refresh)
	if err != nil {
		return nil, err
	}

	if len(sealed) < aead.NonceSize() {
		return nil, errSealed
	}

	plain, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], nil)
	if err != nil {
		return nil, errors.Join(errSealed, err)
	}

	var p pair
	if err := json.Unmarshal(plain, &p); err != nil {
		return nil, errors.Join(errSealed, err)
	}

	return &p, nil
}

func (s *Service) usable(rec *repo.SessionRecord) bool {
	return rec.RevokedAt == nil && s.Now().Before(rec.Session.ExpiresAt)
}

// Refresh implements usecase.Auth (platform conventions §5.2): it runs in the request
// transaction, which holds the session row lock until the new pair and its grace entry are
// stored.
func (s *Service) Refresh(ctx context.Context, c *usecase.Client, refresh string) (*usecase.LoginResult, error) {
	hash := hashToken(refresh)

	rec, found, err := s.Sessions.ByRefreshForUpdate(ctx, hash)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if found {
		if err := s.Limits.Consume(ctx, _ruleRefresh, ratelimit.SessionSubject(rec.Session.ID.String())); err != nil {
			return nil, err //nolint:wrapcheck // entity errors
		}

		return s.rotate(ctx, rec, refresh, hash)
	}

	history, found, err := s.Sessions.History(ctx, hash)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if !found {
		if err := s.Limits.Consume(ctx, _ruleRefresh, ratelimit.IPSubject(clientip.Subject(c.IP))); err != nil {
			return nil, err //nolint:wrapcheck // entity errors
		}

		return nil, entity.New(entity.KeyRefreshTokenInvalid)
	}

	if err := s.Limits.Consume(ctx, _ruleRefresh, ratelimit.SessionSubject(history.SessionID.String())); err != nil {
		return nil, err //nolint:wrapcheck // entity errors
	}

	return s.rotated(ctx, history, refresh, hash)
}

func (s *Service) user(ctx context.Context, rec *repo.SessionRecord) (*entity.User, error) {
	u, found, err := s.Users.Get(ctx, rec.Session.UserID)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if !found {
		return nil, entity.New(entity.KeyRefreshTokenInvalid)
	}

	if !u.User.Active() {
		return nil, entity.New(entity.KeyAccountDisabled)
	}

	return &u.User, nil
}

func (s *Service) rotate(ctx context.Context, rec *repo.SessionRecord, refresh string, hash []byte) (*usecase.LoginResult, error) {
	if !s.usable(rec) {
		return nil, entity.New(entity.KeyRefreshTokenInvalid)
	}

	u, err := s.user(ctx, rec)
	if err != nil {
		return nil, err
	}

	next, nextHash, err := randomToken()
	if err != nil {
		return nil, err
	}

	access, err := s.access(u, rec.Session.ID, rec.Session.AuthTime)
	if err != nil {
		return nil, err
	}

	p := &pair{Access: access, Refresh: next, ExpiresIn: int64(s.cfg.AccessTTL.Seconds())}

	if err := s.Sessions.Rotate(ctx, rec.Session.ID, hash, nextHash, s.Now()); err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	sealed, err := seal(refresh, p)
	if err != nil {
		return nil, err
	}

	if err := s.Grace.Put(ctx, graceKey(hash), sealed, _gracePeriod); err != nil {
		return nil, entity.Wrap(entity.KeyServiceUnavailable, err)
	}

	return s.result(p, u), nil
}

// rotated answers a refresh token already rotated out: within the grace period the pair it was
// rotated to, later a reuse that revokes the session.
func (s *Service) rotated(ctx context.Context, h *repo.RefreshHistory, refresh string, hash []byte) (*usecase.LoginResult, error) {
	rec, found, err := s.Sessions.ForUpdate(ctx, h.SessionID)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if !found || !s.usable(rec) {
		return nil, entity.New(entity.KeyRefreshTokenInvalid)
	}

	if s.Now().Sub(h.RotatedAt) > _gracePeriod {
		if _, err := s.Revocation.One(ctx, rec.Session.UserID, rec.Session.ID); err != nil {
			return nil, err //nolint:wrapcheck // entity or repo errors
		}

		return nil, entity.New(entity.KeyRefreshTokenReused)
	}

	sealed, found, err := s.Grace.Get(ctx, graceKey(hash))
	if err != nil {
		return nil, entity.Wrap(entity.KeyServiceUnavailable, err)
	}

	if !found {
		return nil, entity.New(entity.KeyRefreshTokenInvalid)
	}

	p, err := unseal(refresh, sealed)
	if err != nil {
		return nil, entity.Wrap(entity.KeyRefreshTokenInvalid, err)
	}

	u, err := s.user(ctx, rec)
	if err != nil {
		return nil, err
	}

	return s.result(p, u), nil
}
