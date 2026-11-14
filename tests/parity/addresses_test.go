//go:build parity

package parity_test

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const _addressesURL = "/api/v1/addresses"

func beijingCodes() map[string]any {
	return map[string]any{"provinceCode": "110000000000", "cityCode": "110100000000", "districtCode": "110101000000"}
}

func withFields(base, extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}

	for k, v := range extra {
		out[k] = v
	}

	return out
}

// normalizeOwned also hides favoriteId, userId and a set deletedAt, which differ between the services.
func normalizeOwned(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = normalizeOwned(item, k)
		}

		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = normalizeOwned(item, "")
		}

		return out
	}

	if (key == "favoriteId" || key == "userId" || key == "deletedAt") && v != nil {
		return fmt.Sprintf("<%T>", v)
	}

	return normalize(v, key)
}

func requireSameOwned(t *testing.T, p pair) pair {
	t.Helper()

	require.Equal(t, p.Nestjs.Status, p.Go.Status, "go: %s\nnestjs: %s", p.Go.Body, p.Nestjs.Body)
	require.Equal(t, p.Nestjs.Header.Get("Content-Type"), p.Go.Header.Get("Content-Type"))
	require.Equal(t, normalizeOwned(p.Nestjs.JSON(t), ""), normalizeOwned(p.Go.JSON(t), ""),
		"go: %s\nnestjs: %s", p.Go.Body, p.Nestjs.Body)

	return p
}

// createdIDs returns the data.id of each side.
func createdIDs(t *testing.T, p pair) [2]int {
	t.Helper()

	goID, _ := dataOf(t, p.Go)["id"].(float64)
	nestID, _ := dataOf(t, p.Nestjs)["id"].(float64)

	return [2]int{int(goID), int(nestID)}
}

func itemTargets(base string, ids [2]int, suffix string) [2]string {
	return [2]string{base + "/" + strconv.Itoa(ids[0]) + suffix, base + "/" + strconv.Itoa(ids[1]) + suffix}
}

func TestAddressesRequireAuthentication(t *testing.T) {
	t.Parallel()

	routes := [][2]string{
		{http.MethodGet, ""},
		{http.MethodPost, ""},
		{http.MethodGet, "/default"},
		{http.MethodGet, "/1"},
		{http.MethodPut, "/1"},
		{http.MethodDelete, "/1"},
		{http.MethodPut, "/1/default"},
	}

	for _, r := range routes {
		t.Run(r[0]+r[1], func(t *testing.T) {
			t.Parallel()

			requireSameOwned(t, do(t, r[0], _addressesURL+r[1], withJSON(t, map[string]any{})))
		})
	}
}

func TestAddressCreateValidation(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "address.validation")
	bodies := []map[string]any{
		{},
		{"name": "A", "phone": "1", "address": "x"},
		{"name": "A", "phone": "13800138000", "provinceCode": "110000000000", "address": "x"},
		withFields(beijingCodes(), map[string]any{"name": "A", "phone": "13800138000", "provinceCode": "1100", "address": "x"}),
		{"name": 1, "phone": 2, "province": "", "city": strings.Repeat("c", 51), "district": "d", "address": ""},
		withFields(beijingCodes(), map[string]any{"name": "A", "phone": "13800138000", "address": "x", "isDefault": "yes"}),
	}

	for _, lang := range []string{"en", "zh"} {
		for i, body := range bodies {
			t.Run(lang+"/"+strconv.Itoa(i), func(t *testing.T) {
				t.Parallel()

				requireSameOwned(t, do(t, http.MethodPost, _addressesURL, withTokens(tokens), withJSON(t, body), withHeader("Accept-Language", lang)))
			})
		}
	}
}

func TestAddressListValidation(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "address.list")
	queries := []string{
		"?page=0&limit=abc",
		"?filters=%7Bbad",
		"?filters=%7B%22provinceCode%22%3A%22x%22%7D",
		"?sort=%5B%7B%22orderBy%22%3A%22x%22%2C%22order%22%3A%22up%22%7D%5D",
		"?sort=%7B%7D",
		"?page=1.5",
	}

	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			t.Parallel()

			requireSameOwned(t, do(t, http.MethodGet, _addressesURL+q, withTokens(tokens)))
		})
	}
}

func TestAddressUnknownOrInvalidID(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "address.ids")

	for _, path := range []string{"/abc", "/0", "/2147483648", "/999999", "/999999/default"} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			if path == "/999999/default" && method != http.MethodPut {
				continue
			}

			requireSameOwned(t, do(t, method, _addressesURL+path, withTokens(tokens), withJSON(t, map[string]any{})))
		}
	}
}

func TestAddressLifecycle(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "address.lifecycle")
	call := func(method, target string, options ...option) pair {
		return requireSameOwned(t, do(t, method, target, append([]option{withTokens(tokens)}, options...)...))
	}
	item := func(method string, ids [2]int, suffix string, options ...option) {
		targets := itemTargets(_addressesURL, ids, suffix)

		call(method, targets[0], append([]option{withTargets(targets)}, options...)...)
	}

	call(http.MethodGet, _addressesURL)
	call(http.MethodGet, _addressesURL+"/default")

	first := createdIDs(t, call(http.MethodPost, _addressesURL, withJSON(t, map[string]any{
		"name": " <b>Ann</b> ", "phone": "13800138000", "province": "P", "city": "C", "district": "D",
		"address": "<i>road</i> 1", "unknown": true,
	})))
	second := createdIDs(t, call(http.MethodPost, _addressesURL, withJSON(t, withFields(beijingCodes(), map[string]any{
		"name": "Bob", "phone": "13900139000", "province": "ignored", "address": "x", "isDefault": true,
	}))))
	third := createdIDs(t, call(http.MethodPost, _addressesURL, withJSON(t, withFields(beijingCodes(), map[string]any{
		"name": "Cat", "phone": "13700137000", "address": "y",
	}))))
	call(http.MethodPost, _addressesURL, withJSON(t, withFields(beijingCodes(), map[string]any{
		"name": "Bad", "phone": "13700137000", "cityCode": "120100000000", "address": "z",
	})))
	call(http.MethodGet, _addressesURL)
	call(http.MethodGet, _addressesURL+"/default")
	item(http.MethodGet, first, "")

	for _, q := range []string{
		"?filters=%7B%22provinceCode%22%3A%22110000000000%22%7D",
		"?filters=%7B%22province%22%3A%22P%22%2C%22city%22%3A%22%22%7D",
		"?sort=%5B%7B%22orderBy%22%3A%22name%22%2C%22order%22%3A%22desc%22%7D%5D",
		"?sort=%5B%7B%22orderBy%22%3A%22id%22%2C%22order%22%3A%22ASC%22%7D%5D&page=2&limit=2",
		"?limit=60",
	} {
		call(http.MethodGet, _addressesURL+q)
	}

	for _, body := range []map[string]any{
		{"name": "Ann2", "isDefault": true},
		{"cityCode": "110100000000"},
		withFields(beijingCodes(), map[string]any{"cityCode": "120100000000"}),
		withFields(beijingCodes(), map[string]any{"province": "kept?"}),
		{"province": "NewP", "isDefault": false},
		{"phone": "123"},
		{},
	} {
		item(http.MethodPut, first, "", withJSON(t, body))
	}

	item(http.MethodPut, third, "/default")
	call(http.MethodGet, _addressesURL)
	item(http.MethodDelete, third, "")
	call(http.MethodGet, _addressesURL+"/default")
	item(http.MethodGet, third, "")
	item(http.MethodDelete, third, "")
	item(http.MethodDelete, first, "")
	item(http.MethodDelete, second, "")
	call(http.MethodGet, _addressesURL)
	call(http.MethodGet, _addressesURL+"/default")
}

func TestAddressLimit(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "address.limit")
	body := map[string]any{"name": "N", "phone": "13800138000", "province": "P", "city": "C", "district": "D"}

	for i := range 20 {
		requireSameOwned(t, do(t, http.MethodPost, _addressesURL, withTokens(tokens), withJSON(t, withFields(body, map[string]any{"address": strconv.Itoa(i)}))))
	}

	for _, lang := range []string{"en", "zh"} {
		requireSameOwned(t, do(t, http.MethodPost, _addressesURL, withTokens(tokens),
			withJSON(t, withFields(body, map[string]any{"address": "x"})), withHeader("Accept-Language", lang)))
	}

	requireSameOwned(t, do(t, http.MethodGet, _addressesURL+"?limit=5&page=3", withTokens(tokens)))
}
