package wechat_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/wechat"
)

func creds() wechat.Credentials {
	return wechat.Credentials{AppID: "wx1", AppSecret: "s1"}
}

func client(t *testing.T, h http.HandlerFunc) *wechat.Client {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return wechat.New(srv.URL, redis.NewCache(nil, "svc", nil), nil)
}

func reply(w http.ResponseWriter, v map[string]any) {
	_ = json.NewEncoder(w).Encode(v)
}

func TestMiniAppExchange(t *testing.T) {
	t.Parallel()

	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/sns/jscode2session", r.URL.Path)
		assert.Equal(t, "wx1", r.URL.Query().Get("appid"))
		assert.Equal(t, "s1", r.URL.Query().Get("secret"))
		assert.Equal(t, "c", r.URL.Query().Get("js_code"))
		reply(w, map[string]any{"openid": "o1", "unionid": "u1", "session_key": "k"})
	})

	id, err := wechat.NewMiniApp(c, creds()).Exchange(t.Context(), "c")
	require.NoError(t, err)
	assert.Equal(t, &entity.ExternalIdentity{Provider: "wechat_miniapp", Subject: "o1", UnionID: "u1"}, id)
}

func TestAppExchange(t *testing.T) {
	t.Parallel()

	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/sns/oauth2/access_token", r.URL.Path)
		assert.Equal(t, "c", r.URL.Query().Get("code"))
		reply(w, map[string]any{"openid": "o2", "access_token": "t"})
	})

	id, err := wechat.NewApp(c, creds()).Exchange(t.Context(), "c")
	require.NoError(t, err)
	assert.Equal(t, &entity.ExternalIdentity{Provider: "wechat_app", Subject: "o2", UnionID: ""}, id)
}

func TestExchangeErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status int
		body   string
		key    string
	}{
		{"invalid code", http.StatusOK, `{"errcode":40029,"errmsg":"invalid code"}`, entity.KeyProviderCodeInvalid},
		{"used code", http.StatusOK, `{"errcode":40163,"errmsg":"code been used"}`, entity.KeyProviderCodeInvalid},
		{"busy", http.StatusOK, `{"errcode":-1,"errmsg":"system busy"}`, entity.KeyBadGateway},
		{"no openid", http.StatusOK, `{}`, entity.KeyBadGateway},
		{"server error", http.StatusInternalServerError, `{}`, entity.KeyBadGateway},
		{"not json", http.StatusOK, `<html>`, entity.KeyBadGateway},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := client(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})

			_, err := wechat.NewMiniApp(c, creds()).Exchange(t.Context(), "c")
			require.ErrorIs(t, err, entity.New(tc.key))
		})
	}
}

func TestExchangeUnreachableIsBadGateway(t *testing.T) {
	t.Parallel()

	_, err := wechat.NewApp(wechat.New("http://127.0.0.1:1", redis.NewCache(nil, "svc", nil), nil), creds()).Exchange(t.Context(), "c")
	require.ErrorIs(t, err, entity.New(entity.KeyBadGateway))
}

func TestPhoneCachesAndRefreshesTheToken(t *testing.T) {
	t.Parallel()

	var tokens, calls atomic.Int32

	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/stable_token":
			reply(w, map[string]any{"access_token": "t" + strconv.Itoa(int(tokens.Add(1))), "expires_in": 7200})
		case "/wxa/business/getuserphonenumber":
			if calls.Add(1) == 2 {
				reply(w, map[string]any{"errcode": 40001, "errmsg": "invalid credential"})

				return
			}

			var body map[string]string

			_ = json.NewDecoder(r.Body).Decode(&body)
			assert.Equal(t, "pc", body["code"])
			reply(w, map[string]any{"errcode": 0, "phone_info": map[string]any{"purePhoneNumber": "13800138000", "countryCode": "86"}})
		}
	})

	m := wechat.NewMiniApp(c, creds())

	got, err := m.Phone(t.Context(), "pc")
	require.NoError(t, err)
	assert.Equal(t, "+8613800138000", got)
	assert.Equal(t, int32(1), tokens.Load())

	got, err = m.Phone(t.Context(), "pc")
	require.NoError(t, err)
	assert.Equal(t, "+8613800138000", got)
	assert.Equal(t, int32(2), tokens.Load())
	assert.Equal(t, int32(3), calls.Load())
}

func TestPhoneErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		key  string
	}{
		{"invalid code", `{"errcode":40029,"errmsg":"invalid code"}`, entity.KeyProviderCodeInvalid},
		{"other error", `{"errcode":45011,"errmsg":"api minute-quota reach limit"}`, entity.KeyBadGateway},
		{"no phone", `{"errcode":0}`, entity.KeyBadGateway},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := client(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/cgi-bin/stable_token" {
					reply(w, map[string]any{"access_token": "t", "expires_in": 7200})

					return
				}

				_, _ = w.Write([]byte(tc.body))
			})

			_, err := wechat.NewMiniApp(c, creds()).Phone(t.Context(), "pc")
			require.ErrorIs(t, err, entity.New(tc.key))
		})
	}
}

func TestPhoneTokenFailureIsBadGateway(t *testing.T) {
	t.Parallel()

	c := client(t, func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"errcode": 40013, "errmsg": "invalid appid"})
	})

	_, err := wechat.NewMiniApp(c, creds()).Phone(t.Context(), "pc")
	require.ErrorIs(t, err, entity.New(entity.KeyBadGateway))
}

func TestConcurrentInvalidTokensFetchOneNewToken(t *testing.T) {
	t.Parallel()

	var tokens atomic.Int32

	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/stable_token":
			reply(w, map[string]any{"access_token": "t" + strconv.Itoa(int(tokens.Add(1))), "expires_in": 7200})
		default:
			if r.URL.Query().Get("access_token") == "t1" {
				reply(w, map[string]any{"errcode": 42001, "errmsg": "access_token expired"})

				return
			}

			reply(w, map[string]any{"errcode": 0, "phone_info": map[string]any{"purePhoneNumber": "13800138000", "countryCode": "86"}})
		}
	})

	m := wechat.NewMiniApp(c, creds())

	_, err := m.Phone(t.Context(), "pc")
	require.NoError(t, err)

	var wg sync.WaitGroup

	for range 5 {
		wg.Go(func() {
			_, err := m.Phone(t.Context(), "pc")
			assert.NoError(t, err)
		})
	}

	wg.Wait()
	assert.Equal(t, int32(2), tokens.Load())
}

func TestTheSameInvalidTokenIsForcedToRefresh(t *testing.T) {
	t.Parallel()

	var forced atomic.Bool

	c := client(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cgi-bin/stable_token":
			var body map[string]any

			_ = json.NewDecoder(r.Body).Decode(&body)

			if body["force_refresh"] == true {
				forced.Store(true)
				reply(w, map[string]any{"access_token": "fresh", "expires_in": 7200})

				return
			}

			reply(w, map[string]any{"access_token": "stale", "expires_in": 7200})
		default:
			if r.URL.Query().Get("access_token") == "stale" {
				reply(w, map[string]any{"errcode": 40001, "errmsg": "invalid credential"})

				return
			}

			reply(w, map[string]any{"errcode": 0, "phone_info": map[string]any{"purePhoneNumber": "13800138000", "countryCode": "86"}})
		}
	})

	_, err := wechat.NewMiniApp(c, creds()).Phone(t.Context(), "pc")
	require.NoError(t, err)
	assert.True(t, forced.Load())
}
