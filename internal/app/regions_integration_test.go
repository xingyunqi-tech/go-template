//go:build integration

package app_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (r *result) list() []map[string]any {
	items, _ := r.Body["data"].([]any)
	out := make([]map[string]any, 0, len(items))

	for _, item := range items {
		m, _ := item.(map[string]any)
		out = append(out, m)
	}

	return out
}

func codes(items []map[string]any) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i], _ = item["code"].(string)
	}

	return out
}

func TestRegionLists(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)

	provinces := a.do(http.MethodGet, "/api/v1/regions/provinces", "", nil)
	require.Equal(t, http.StatusOK, provinces.Status, provinces.Raw)
	assert.Regexp(t, `^\{"code":200,"message":"success","data":\[\{"id":\d+,"code":"110000000000","name":"北京市","level":1,"parentCode":null,"sort":0,"createdAt":"[^"]+Z","updatedAt":"[^"]+Z"\},`, provinces.Raw)
	assert.Len(t, provinces.list(), 31)

	cities := a.do(http.MethodGet, "/api/v1/regions/cities/110000000000", "", nil)
	assert.Equal(t, []string{"110100000000"}, codes(cities.list()))

	districts := a.do(http.MethodGet, "/api/v1/regions/districts/110100000000", "", nil).list()
	require.NotEmpty(t, districts)
	assert.Equal(t, "110101000000", districts[0]["code"])
	assert.Equal(t, "110100000000", districts[0]["parentCode"])

	assert.JSONEq(t, `{"code":200,"message":"success","data":[]}`, a.do(http.MethodGet, "/api/v1/regions/cities/999999999999", "", nil).Raw)

	one := a.do(http.MethodGet, "/api/v1/regions/code/110101000000", "", nil)
	require.Equal(t, http.StatusOK, one.Status, one.Raw)
	assert.Equal(t, "东城区", one.data()["name"])

	assert.JSONEq(t, `{"code":404,"message":"Region with code 999999999998 not found","data":null}`,
		a.do(http.MethodGet, "/api/v1/regions/code/999999999998", "", nil).Raw)
}

func TestRegionParamValidation(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)

	cases := map[string]string{
		"/cities/11":                          `{"provinceCode":"provinceCode must be a 12-digit region code"}`,
		"/cities/abcdefghijkl":                `{"provinceCode":"provinceCode must be a 12-digit region code"}`,
		"/districts/1101000000000":            `{"cityCode":"cityCode must be a 12-digit region code"}`,
		"/code/x":                             `{"code":"code must be a 12-digit region code"}`,
		"?keyword=" + strings.Repeat("x", 51): `{"keyword":"keyword too long"}`,
		"?parentCode=":                        `{"parentCode":"parentCode must be a 12-digit region code"}`,
		"?level=4":                            `{"level":"level must not be greater than 3"}`,
		"?level=1.5":                          `{"level":"level must be an integer number"}`,
		"?level=":                             `{"level":"level must not be less than 1"}`,
	}

	for path, data := range cases {
		r := a.do(http.MethodGet, "/api/v1/regions"+path, "", nil, "Accept-Language", "zh")
		assert.JSONEq(t, `{"code":422,"message":"数据验证失败","data":`+data+`}`, r.Raw, path)
	}
}

func TestRegionQuery(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)

	assert.Equal(t, []string{"110000000000"}, codes(a.do(http.MethodGet, "/api/v1/regions?keyword=%E5%8C%97%E4%BA%AC", "", nil).list()))
	assert.Len(t, a.do(http.MethodGet, "/api/v1/regions?keyword=%E5%8D%97", "", nil).list(), 20)
	assert.Empty(t, a.do(http.MethodGet, "/api/v1/regions?keyword=%25", "", nil).list())
	assert.Empty(t, a.do(http.MethodGet, "/api/v1/regions?keyword=_", "", nil).list())

	byParent := a.do(http.MethodGet, "/api/v1/regions?level=3&parentCode=110000000000", "", nil).list()
	assert.Equal(t, []string{"110100000000"}, codes(byParent))

	keywordFirst := a.do(http.MethodGet, "/api/v1/regions?keyword=%E5%8C%97&level=1&parentCode=110000000000", "", nil).list()
	assert.Equal(t, "110000000000", keywordFirst[0]["code"])
	assert.Greater(t, len(keywordFirst), 1)

	provinces := a.do(http.MethodGet, "/api/v1/regions?level=1", "", nil).list()
	assert.Len(t, provinces, 31)

	all := a.do(http.MethodGet, "/api/v1/regions?keyword=", "", nil).list()
	assert.Greater(t, len(all), 3000)
	assert.Equal(t, codes(provinces), codes(all[:31]))
}

func TestRegionTree(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)

	tree := a.do(http.MethodGet, "/api/v1/regions/tree", "", nil)
	require.Equal(t, http.StatusOK, tree.Status)
	assert.Contains(t, tree.Raw, `"updatedAt":"`)

	roots := tree.list()
	require.Len(t, roots, 31)

	cities, _ := roots[0]["children"].([]any)
	require.Len(t, cities, 1)

	city, _ := cities[0].(map[string]any)
	districts, _ := city["children"].([]any)
	require.NotEmpty(t, districts)

	district, _ := districts[0].(map[string]any)
	assert.Equal(t, []any{}, district["children"])
}

func TestRegionsAreCached(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	tree := a.do(http.MethodGet, "/api/v1/regions/tree", "", nil).Raw

	require.Len(t, a.do(http.MethodGet, "/api/v1/regions/provinces", "", nil).list(), 31)
	require.Equal(t, http.StatusOK, a.do(http.MethodGet, "/api/v1/regions/code/110101000000", "", nil).Status)
	require.Equal(t, http.StatusNotFound, a.do(http.MethodGet, "/api/v1/regions/code/999999999998", "", nil).Status)
	require.Empty(t, a.do(http.MethodGet, "/api/v1/regions/cities/999999999999", "", nil).list())

	a.exec(`UPDATE "region" SET "name" = 'Renamed' WHERE "code" IN ('110000000000', '110101000000')`)
	a.exec(`INSERT INTO "region" ("code", "name", "level", "parentCode") VALUES ('999999999998', 'New', 1, NULL), ('999999999997', 'Child', 2, '999999999999'), ('999999999999', 'Parent', 1, NULL)`)

	assert.Equal(t, "北京市", a.do(http.MethodGet, "/api/v1/regions/provinces", "", nil).list()[0]["name"])
	assert.Equal(t, "东城区", a.do(http.MethodGet, "/api/v1/regions/code/110101000000", "", nil).data()["name"])
	assert.Equal(t, http.StatusNotFound, a.do(http.MethodGet, "/api/v1/regions/code/999999999998", "", nil).Status)
	assert.Equal(t, tree, a.do(http.MethodGet, "/api/v1/regions/tree", "", nil).Raw)
	assert.Equal(t, []string{"999999999997"}, codes(a.do(http.MethodGet, "/api/v1/regions/cities/999999999999", "", nil).list()))
	assert.Equal(t, "Renamed", a.do(http.MethodGet, "/api/v1/regions?level=1", "", nil).list()[0]["name"])
}
