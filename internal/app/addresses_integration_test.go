//go:build integration

package app_test

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const _addresses = "/api/v1/addresses"

func beijing() map[string]any {
	return map[string]any{"provinceCode": "110000000000", "cityCode": "110100000000", "districtCode": "110101000000"}
}

func addressBody(name string, extra map[string]any) map[string]any {
	body := map[string]any{"name": name, "phone": "13800138000", "address": "road 1"}
	for k, v := range extra {
		body[k] = v
	}

	return body
}

func idPath(base string, r *result) string {
	id, _ := r.data()["id"].(float64)

	return base + "/" + strconv.Itoa(int(id))
}

func pageOf(t *testing.T, r *result) (list []any, total float64) {
	t.Helper()
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	page := r.data()
	list, _ = page["list"].([]any)
	total, _ = page["total"].(float64)

	return list, total
}

func firstItem(t *testing.T, list []any) map[string]any {
	t.Helper()
	require.NotEmpty(t, list)

	item, ok := list[0].(map[string]any)
	require.True(t, ok, list[0])

	return item
}

func TestAddressLifecycle(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	john := a.login("john.doe@example.com", _secret00).access
	admin := a.login("admin@example.com", _secret00).access

	assert.JSONEq(t, `{"code":200,"message":"success","data":{"list":[],"total":0,"page":1,"limit":10,"totalPages":0}}`,
		a.do(http.MethodGet, _addresses, john, nil).Raw)
	assert.JSONEq(t, `{"code":200,"message":"success","data":null}`, a.do(http.MethodGet, _addresses+"/default", john, nil).Raw)

	first := a.do(http.MethodPost, _addresses, john, addressBody(" <b>Ann</b> ", map[string]any{
		"province": "P", "city": "C", "district": "D", "unknown": true,
	}))
	require.Equal(t, http.StatusOK, first.Status, first.Raw)
	assert.Regexp(t, `^\{"code":200,"message":"success","data":\{"id":\d+,"userId":\d+,"name":"Ann","phone":"13800138000",`+
		`"provinceCode":null,"cityCode":null,"districtCode":null,"province":"P","city":"C","district":"D","address":"road 1",`+
		`"isDefault":true,"createdAt":"[^"]+","updatedAt":"[^"]+","deletedAt":null\}\}$`, first.Raw)

	second := a.do(http.MethodPost, _addresses, john, addressBody("Bob", merge(beijing(), map[string]any{"province": "ignored", "isDefault": true})))
	require.Equal(t, http.StatusOK, second.Status, second.Raw)
	assert.Equal(t, "北京市", second.data()["province"])
	assert.Equal(t, "市辖区", second.data()["city"])
	assert.Equal(t, "东城区", second.data()["district"])
	assert.Equal(t, true, second.data()["isDefault"])

	def := a.do(http.MethodGet, _addresses+"/default", john, nil)
	assert.Equal(t, second.data()["id"], def.data()["id"])
	assert.Equal(t, false, a.do(http.MethodGet, idPath(_addresses, first), john, nil).data()["isDefault"])

	list, total := pageOf(t, a.do(http.MethodGet, _addresses, john, nil))
	assert.InDelta(t, 2, total, 0)
	require.Len(t, list, 2)
	assert.Equal(t, second.data()["id"], firstItem(t, list)["id"])

	updated := a.do(http.MethodPut, idPath(_addresses, first), john, map[string]any{"name": "Ann2", "isDefault": true})
	require.Equal(t, http.StatusOK, updated.Status, updated.Raw)
	assert.Equal(t, "Ann2", updated.data()["name"])
	assert.Equal(t, true, updated.data()["isDefault"])
	assert.Equal(t, false, a.do(http.MethodGet, idPath(_addresses, second), john, nil).data()["isDefault"])

	kept := a.do(http.MethodPut, idPath(_addresses, second), john, map[string]any{"province": "kept?", "cityCode": "110100000000"})
	require.Equal(t, http.StatusOK, kept.Status, kept.Raw)
	assert.Equal(t, "北京市", kept.data()["province"])

	assert.Equal(t, http.StatusNotFound, a.do(http.MethodGet, idPath(_addresses, first), admin, nil).Status)
	assert.Equal(t, http.StatusNotFound, a.do(http.MethodPut, idPath(_addresses, first), admin, map[string]any{}).Status)
	assert.Equal(t, http.StatusNotFound, a.do(http.MethodDelete, idPath(_addresses, first), admin, nil).Status)

	setDefault := a.do(http.MethodPut, idPath(_addresses, second)+"/default", john, nil)
	require.Equal(t, http.StatusOK, setDefault.Status, setDefault.Raw)
	assert.Equal(t, true, setDefault.data()["isDefault"])

	require.Equal(t, http.StatusOK, a.do(http.MethodDelete, idPath(_addresses, second), john, nil).Status)
	assert.Equal(t, first.data()["id"], a.do(http.MethodGet, _addresses+"/default", john, nil).data()["id"])
	assert.JSONEq(t, `{"code":404,"message":"Address not found","data":null}`, a.do(http.MethodGet, idPath(_addresses, second), john, nil).Raw)
	assert.Equal(t, http.StatusNotFound, a.do(http.MethodDelete, idPath(_addresses, second), john, nil).Status)
}

func merge(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}

	for k, v := range b {
		out[k] = v
	}

	return out
}

func TestAddressRegionErrors(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	john := a.login("john.doe@example.com", _secret00).access

	incomplete := a.do(http.MethodPost, _addresses, john, addressBody("A", map[string]any{"provinceCode": "110000000000", "province": "P", "city": "C", "district": "D"}))
	assert.JSONEq(t, `{"code":400,"message":"Must provide all three region codes (province, city, district) or none at all","data":null}`, incomplete.Raw)

	invalid := a.do(http.MethodPost, _addresses, john, addressBody("A", merge(beijing(), map[string]any{"cityCode": "120100000000"})))
	assert.JSONEq(t, `{"code":400,"message":"Invalid province/city/district combination","data":null}`, invalid.Raw)

	nullCode := a.do(http.MethodPost, _addresses, john, addressBody("A", map[string]any{
		"provinceCode": nil, "province": "P", "city": "C", "district": "D",
	}))
	assert.JSONEq(t, `{"code":400,"message":"Must provide all three region codes (province, city, district) or none at all","data":null}`, nullCode.Raw)

	plain := a.do(http.MethodPost, _addresses, john, addressBody("A", map[string]any{"province": "P", "city": "C", "district": "D"}))
	require.Equal(t, http.StatusOK, plain.Status, plain.Raw)

	partial := a.do(http.MethodPut, idPath(_addresses, plain), john, map[string]any{"cityCode": "110100000000"})
	assert.JSONEq(t, `{"code":400,"message":"Must provide all three region codes (province, city, district) or none at all","data":null}`, partial.Raw)

	cleared := a.do(http.MethodPut, idPath(_addresses, plain), john, map[string]any{"provinceCode": nil, "isDefault": false})
	require.Equal(t, http.StatusOK, cleared.Status, cleared.Raw)
	assert.Equal(t, "P", cleared.data()["province"])
	assert.Equal(t, false, cleared.data()["isDefault"])

	filled := a.do(http.MethodPut, idPath(_addresses, plain), john, beijing())
	require.Equal(t, http.StatusOK, filled.Status, filled.Raw)
	assert.Equal(t, "东城区", filled.data()["district"])
	assert.Equal(t, "110101000000", filled.data()["districtCode"])

	assert.Equal(t, http.StatusUnprocessableEntity, a.do(http.MethodPut, idPath(_addresses, plain), john, map[string]any{"phone": "123"}).Status)
	assert.Equal(t, http.StatusInternalServerError, a.do(http.MethodPut, idPath(_addresses, plain), john, map[string]any{"name": nil}).Status)
}

func TestAddressListAndLimit(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	john := a.login("john.doe@example.com", _secret00).access
	names := map[string]any{"province": "P", "city": "C", "district": "D"}

	for i := range 20 {
		r := a.do(http.MethodPost, _addresses, john, addressBody("N"+strconv.Itoa(i), names))
		require.Equal(t, http.StatusOK, r.Status, r.Raw)
	}

	full := a.do(http.MethodPost, _addresses, john, addressBody("X", names), "Accept-Language", "zh")
	assert.JSONEq(t, `{"code":403,"message":"Maximum 20 addresses allowed per user","data":null}`, full.Raw)

	list, total := pageOf(t, a.do(http.MethodGet, _addresses+"?limit=5&page=3", john, nil))
	assert.Len(t, list, 5)
	assert.InDelta(t, 20, total, 0)

	page := a.do(http.MethodGet, _addresses+"?limit=60", john, nil)
	assert.Contains(t, page.Raw, `"total":20,"page":1,"limit":50,"totalPages":1}`)

	sorted := a.do(http.MethodGet, _addresses+"?sort="+url.QueryEscape(`[{"orderBy":"name","order":"desc"}]`), john, nil)
	list, _ = pageOf(t, sorted)
	assert.Equal(t, "N9", firstItem(t, list)["name"])

	filtered := a.do(http.MethodGet, _addresses+"?filters="+url.QueryEscape(`{"province":"other","city":""}`), john, nil)
	list, total = pageOf(t, filtered)
	assert.Empty(t, list)
	assert.InDelta(t, 0, total, 0)

	assert.Equal(t, http.StatusUnprocessableEntity, a.do(http.MethodGet, _addresses+"?page=1.5", john, nil).Status)
	assert.Equal(t, http.StatusUnprocessableEntity, a.do(http.MethodGet, _addresses+"?sort=%7B%7D", john, nil).Status)
	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodGet, _addresses, "", nil).Status)
	assert.Equal(t, http.StatusBadRequest, a.do(http.MethodGet, _addresses+"/abc", john, nil).Status)
}
