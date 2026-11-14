// Package jwtx signs and verifies HS256 tokens the way jsonwebtoken does: the payload keys come
// first, followed by iat (seconds) and exp = iat + floor(expiresIn / 1000).
package jwtx

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	_header          = `{"alg":"HS256","typ":"JWT"}`
	_millisPerSecond = 1000
)

var (
	// ErrInvalid reports a malformed token, a bad signature or a disallowed algorithm.
	ErrInvalid = errors.New("jwtx: invalid token")
	// ErrExpired reports a token past its exp.
	ErrExpired = errors.New("jwtx: token expired")

	errNotObject = errors.New("jwtx: payload must be a JSON object")
)

// Sign returns a token for payload, a struct or map marshaling to a JSON object.
func Sign(payload any, secret string, expiresInMillis float64, now time.Time) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("jwtx: %w", err)
	}

	body = bytes.TrimSpace(body)
	if len(body) < 2 || body[0] != '{' || body[len(body)-1] != '}' {
		return "", errNotObject
	}

	iat := now.Unix()
	exp := int64(math.Floor(float64(iat) + expiresInMillis/_millisPerSecond))

	claims := body[:len(body)-1]
	if len(claims) > 1 {
		claims = append(claims, ',')
	}

	claims = append(claims, `"iat":`+strconv.FormatInt(iat, 10)+`,"exp":`+strconv.FormatInt(exp, 10)+`}`...)

	unsigned := base64.RawURLEncoding.EncodeToString([]byte(_header)) + "." + base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(unsigned))

	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Verify checks the HS256 signature and exp, and returns the claims.
func Verify(token, secret string) (jwt.MapClaims, error) {
	claims := jwt.MapClaims{}

	_, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))

	switch {
	case errors.Is(err, jwt.ErrTokenExpired):
		return nil, ErrExpired
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return claims, nil
}

// Int reads a numeric or numeric-string claim as an int.
func Int(claims jwt.MapClaims, key string) (int, bool) {
	switch v := claims[key].(type) {
	case float64:
		if v != math.Trunc(v) || v < math.MinInt32 || v > math.MaxInt32 {
			return 0, false
		}

		return int(v), true
	case string:
		n, err := strconv.Atoi(v)

		return n, err == nil
	default:
		return 0, false
	}
}

// String reads a string claim.
func String(claims jwt.MapClaims, key string) (string, bool) {
	s, ok := claims[key].(string)

	return s, ok
}
