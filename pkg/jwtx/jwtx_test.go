package jwtx_test

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
)

type role struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Entity string `json:"__entity"`
}

type access struct {
	ID        int  `json:"id"`
	Role      role `json:"role"`
	SessionID int  `json:"sessionId"`
}

func payload(t *testing.T, token string) string {
	t.Helper()

	parts := strings.Split(token, ".")
	require.Len(t, parts, 3)

	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)

	return string(raw)
}

func TestSignOrdersClaimsLikeJsonwebtoken(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 999_000_000)
	token, err := jwtx.Sign(access{ID: 1, Role: role{ID: 2, Name: "User", Entity: "RoleEntity"}, SessionID: 7}, "secret", 900_500, now)
	require.NoError(t, err)

	assert.Equal(t,
		`{"id":1,"role":{"id":2,"name":"User","__entity":"RoleEntity"},"sessionId":7,"iat":1700000000,"exp":1700000900}`,
		payload(t, token))
	header, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[0])
	require.NoError(t, err)
	assert.JSONEq(t, `{"alg":"HS256","typ":"JWT"}`, string(header))

	empty, err := jwtx.Sign(map[string]any{}, "secret", 1000, now)
	require.NoError(t, err)
	assert.Equal(t, `{"iat":1700000000,"exp":1700000001}`, payload(t, empty))

	_, err = jwtx.Sign([]int{1}, "secret", 1000, now)
	require.Error(t, err)
}

func TestVerify(t *testing.T) {
	t.Parallel()

	token, err := jwtx.Sign(map[string]any{"sessionId": 3, "hash": "h"}, "secret", 60_000, time.Now())
	require.NoError(t, err)

	claims, err := jwtx.Verify(token, "secret")
	require.NoError(t, err)

	id, ok := jwtx.Int(claims, "sessionId")
	assert.True(t, ok)
	assert.Equal(t, 3, id)

	hash, ok := jwtx.String(claims, "hash")
	assert.True(t, ok)
	assert.Equal(t, "h", hash)

	_, err = jwtx.Verify(token, "other")
	require.ErrorIs(t, err, jwtx.ErrInvalid)

	expired, err := jwtx.Sign(map[string]any{"id": 1}, "secret", 1000, time.Now().Add(-time.Hour))
	require.NoError(t, err)

	_, err = jwtx.Verify(expired, "secret")
	require.ErrorIs(t, err, jwtx.ErrExpired)

	hs512, err := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.MapClaims{"id": 1}).SignedString([]byte("secret"))
	require.NoError(t, err)

	_, err = jwtx.Verify(hs512, "secret")
	require.ErrorIs(t, err, jwtx.ErrInvalid)

	_, err = jwtx.Verify("garbage", "secret")
	require.ErrorIs(t, err, jwtx.ErrInvalid)
}

func TestIntClaim(t *testing.T) {
	t.Parallel()

	claims := jwt.MapClaims{"a": 1.0, "b": "2", "c": 1.5, "d": "x", "e": true}

	for key, want := range map[string]int{"a": 1, "b": 2} {
		got, ok := jwtx.Int(claims, key)
		assert.True(t, ok, key)
		assert.Equal(t, want, got)
	}

	for _, key := range []string{"c", "d", "e", "missing"} {
		_, ok := jwtx.Int(claims, key)
		assert.False(t, ok, key)
	}
}
