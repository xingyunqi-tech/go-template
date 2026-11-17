//go:build integration

package app_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const _emptyProfile = `{"appName":null,"slogan":null,"companyName":null,"copyright":null,"icpLicense":null,` +
	`"servicePhone":null,"serviceHours":null,"serviceEmail":null,"shareTitle":null}`

func TestAppProfile(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	admin := a.login("admin@example.com", _secret00)
	john := a.login("john.doe@example.com", _secret00)

	assert.Equal(t, `{"code":200,"message":"success","data":`+_emptyProfile+`}`, a.do(http.MethodGet, "/api/v1/app-profile", "", nil).Raw)

	assert.JSONEq(t, `{"code":403,"message":"Missing permissions: system:config","data":null}`,
		a.do(http.MethodPut, "/api/v1/admin/app-profile", john.access, map[string]any{}).Raw)
	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodPut, "/api/v1/admin/app-profile", "", map[string]any{}).Status)

	invalid := a.do(http.MethodPut, "/api/v1/admin/app-profile", admin.access, map[string]any{
		"servicePhone": "   -  ", "serviceEmail": "  a@b.co ", "icpLicense": strings.Repeat("x", 51),
	})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{`+
		`"icpLicense":"icpLicense must be shorter than or equal to 50 characters",`+
		`"servicePhone":"servicePhone must match /^(?=.*\\d)[\\d\\-+ ]+$/ regular expression"}}`, invalid.Raw)

	updated := a.do(http.MethodPut, "/api/v1/admin/app-profile", admin.access, map[string]any{
		"appName": "  备考  ", "slogan": "   ", "shareTitle": nil, "servicePhone": " +86 400-123 4567 ",
		"serviceEmail": "help@example.com", "unknown": "x",
	})
	want := `{"code":200,"message":"success","data":{"appName":"备考","slogan":null,"companyName":null,"copyright":null,` +
		`"icpLicense":null,"servicePhone":"+86 400-123 4567","serviceHours":null,"serviceEmail":"help@example.com","shareTitle":null}}`
	assert.Equal(t, want, updated.Raw)
	assert.Equal(t, want, a.do(http.MethodGet, "/api/v1/app-profile", "", nil).Raw)

	stored := a.do(http.MethodGet, "/api/v1/config/admin/app.profile", admin.access, nil)
	assert.Contains(t, stored.Raw, `"isPublic":false`)
	assert.Equal(t, http.StatusNotFound, a.do(http.MethodGet, "/api/v1/config/app.profile", "", nil).Status)

	require.Equal(t, http.StatusOK, a.do(http.MethodPut, "/api/v1/admin/app-profile", admin.access, map[string]any{"slogan": "新"}).Status)
	assert.Equal(t, "新", a.do(http.MethodGet, "/api/v1/app-profile", "", nil).data()["slogan"])
	assert.Nil(t, a.do(http.MethodGet, "/api/v1/app-profile", "", nil).data()["appName"])
}

func TestAppProfileReadsStoredStringsOnly(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	a.exec(`INSERT INTO "config" ("key", "value") VALUES ('app.profile', '{"value": {"appName": " A ", "slogan": 1, "copyright": "  "}}')`)

	profile := a.do(http.MethodGet, "/api/v1/app-profile", "", nil).data()
	assert.Equal(t, "A", profile["appName"])
	assert.Nil(t, profile["slogan"])
	assert.Nil(t, profile["copyright"])
}

func consentOf(t *testing.T, r *result, consentType string) map[string]any {
	t.Helper()

	for _, s := range r.list() {
		if s["type"] == consentType {
			return s
		}
	}

	require.Fail(t, "missing consent type", consentType)

	return nil
}

func TestConsents(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	admin := a.login("admin@example.com", _secret00)
	john := a.login("john.doe@example.com", _secret00)
	path := "/api/v1/me/consents"

	unset := `{"currentVersion":null,"agreedVersion":null,"agreedAt":null,"agreed":false,"needsConsent":false}`
	assert.JSONEq(t, `{"code":200,"message":"success","data":[`+
		strings.Join([]string{
			`{"type":"PRIVACY_POLICY",` + unset[1:],
			`{"type":"USER_AGREEMENT",` + unset[1:],
			`{"type":"AI_SERVICE",` + unset[1:],
		}, ",")+`]}`, a.do(http.MethodGet, path, john.access, nil).Raw)

	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodGet, path, "", nil).Status)
	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodPost, path, "", map[string]any{}).Status)

	assert.JSONEq(t, `{"code":422,"message":"数据验证失败","data":{"version":"协议版本已更新，请重新查看后同意"}}`,
		a.do(http.MethodPost, path, john.access, map[string]any{"type": "PRIVACY_POLICY", "version": "2026-10-01"}, "Accept-Language", "zh").Raw)
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"type":"type must be one of the following values: PRIVACY_POLICY, USER_AGREEMENT, AI_SERVICE","version":"version should not be empty"}}`,
		a.do(http.MethodPost, path, john.access, map[string]any{"type": "NOPE", "version": ""}).Raw)

	setVersion := func(key, version string) {
		r := a.do(http.MethodPut, "/api/v1/config/"+key, admin.access, map[string]any{"value": map[string]any{"value": version}})
		require.Equal(t, http.StatusOK, r.Status, r.Raw)
	}

	setVersion("consent.privacyPolicyVersion", "v1")
	setVersion("consent.userAgreementVersion", "  ")

	pending := consentOf(t, a.do(http.MethodGet, path, john.access, nil), "PRIVACY_POLICY")
	assert.Equal(t, map[string]any{"type": "PRIVACY_POLICY", "currentVersion": "v1", "agreedVersion": nil, "agreedAt": nil, "agreed": false, "needsConsent": true}, pending)
	assert.Nil(t, consentOf(t, a.do(http.MethodGet, path, john.access, nil), "USER_AGREEMENT")["currentVersion"])

	agreed := a.do(http.MethodPost, path, john.access, map[string]any{"type": "PRIVACY_POLICY", "version": "v1"})
	require.Equal(t, http.StatusOK, agreed.Status, agreed.Raw)
	assert.Regexp(t, `^\{"code":200,"message":"success","data":\{"type":"PRIVACY_POLICY","currentVersion":"v1","agreedVersion":"v1","agreedAt":"[^"]+Z","agreed":true,"needsConsent":false\}\}$`, agreed.Raw)

	again := a.do(http.MethodPost, path, john.access, map[string]any{"type": "PRIVACY_POLICY", "version": "v1"})
	assert.Equal(t, agreed.Raw, again.Raw)

	var count int
	require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT count(*) FROM "user_consent" WHERE "type" = 'PRIVACY_POLICY'`).Scan(&count))

	var versions int
	require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT count(*) FROM "user_consent" WHERE substr("id"::text, 15, 1) <> '7'`).Scan(&versions))
	assert.Zero(t, versions, "consent ids are UUIDv7")
	assert.Equal(t, 1, count)

	setVersion("consent.privacyPolicyVersion", "v2")

	upgraded := consentOf(t, a.do(http.MethodGet, path, john.access, nil), "PRIVACY_POLICY")
	assert.Equal(t, "v2", upgraded["currentVersion"])
	assert.Equal(t, "v1", upgraded["agreedVersion"])
	assert.Equal(t, false, upgraded["agreed"])
	assert.Equal(t, true, upgraded["needsConsent"])

	require.Equal(t, http.StatusOK, a.do(http.MethodPost, path, john.access, map[string]any{"type": "PRIVACY_POLICY", "version": "v2"}).Status)
	setVersion("consent.privacyPolicyVersion", "v1")

	restored := consentOf(t, a.do(http.MethodGet, path, john.access, nil), "PRIVACY_POLICY")
	assert.Equal(t, "v2", restored["agreedVersion"])
	assert.Equal(t, true, restored["agreed"])
}
