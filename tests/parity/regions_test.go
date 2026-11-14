//go:build parity

package parity_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestRegions(t *testing.T) {
	t.Parallel()

	paths := []string{
		"/provinces",
		"/cities/110000000000",
		"/cities/440000000000",
		"/cities/999999999999",
		"/cities/11",
		"/cities/abcdefghijkl",
		"/districts/110100000000",
		"/districts/440300000000",
		"/districts/1101000000000",
		"/code/110101000000",
		"/code/440305000000",
		"/code/999999999998",
		"/code/x",
		"/tree",
		"",
		"?keyword=%E5%8C%97%E4%BA%AC",
		"?keyword=%E5%8D%97",
		"?keyword=%25",
		"?keyword=_",
		"?keyword=" + strings.Repeat("x", 51),
		"?parentCode=110000000000",
		"?parentCode=",
		"?parentCode=1",
		"?level=1",
		"?level=3&parentCode=110100000000",
		"?level=4",
		"?level=abc",
		"?level=1.5",
		"?level=",
		"?level=0",
		"?keyword=",
		"?keyword=%E5%8C%97&level=1&parentCode=110000000000",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			for range 2 {
				requireSameBody(t, do(t, http.MethodGet, "/api/v1/regions"+path, withHeader("Accept-Language", "zh")))
			}
		})
	}
}
