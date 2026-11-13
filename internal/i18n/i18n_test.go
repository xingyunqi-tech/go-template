package i18n

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveFollowsAcceptLanguageResolver(t *testing.T) {
	t.Parallel()

	tr, err := New("en")
	require.NoError(t, err)
	assert.Equal(t, []string{"en", "zh"}, tr.Languages())

	cases := map[string]string{
		"":                            "en",
		"zh":                          "zh",
		"zh-CN,zh;q=0.9,en;q=0.8":     "zh",
		"ZH-cn":                       "zh",
		"en;q=0.5, zh;q=0.8":          "zh",
		"fr, zh-Hant-TW;q=0.4":        "zh",
		"fr, de":                      "en",
		"zh;q=abc, en;q=0.1":          "zh",
		"en-US;q=0, zh;q=0.0001":      "zh",
		"*":                           "en",
		" , ,zh":                      "zh",
		"zh;q":                        "zh",
		"en;q=Infinity, zh;q=1000000": "en",
	}

	for header, want := range cases {
		assert.Equal(t, want, tr.Resolve(header), header)
	}

	zh, err := New("zh")
	require.NoError(t, err)
	assert.Equal(t, "zh", zh.Resolve("fr"))

	_, err = New("fr")
	require.ErrorIs(t, err, errUnknownLanguage)
}

func TestTranslateFallsBackToDefaultLanguage(t *testing.T) {
	t.Parallel()

	tr, err := New("en")
	require.NoError(t, err)

	v, ok := tr.Translate("zh", "common.notFound")
	assert.True(t, ok)
	assert.Equal(t, "资源不存在", v)

	v, ok = tr.Translate("fr", "common.notFound")
	assert.True(t, ok)
	assert.Equal(t, "Not found", v)

	v, ok = tr.Translate("zh", "confirm-email.text1")
	assert.True(t, ok)
	assert.NotEmpty(t, v)

	for _, key := range []string{"common.missing", "nope.notFound", "common", "common.not.found", "common.Cannot GET /a.b"} {
		_, ok := tr.Translate("en", key)
		assert.False(t, ok, key)
	}
}

func TestParseFloatReadsLeadingNumber(t *testing.T) {
	t.Parallel()

	assert.InDelta(t, 0.8, parseFloat(" 0.8abc"), 1e-9)
	assert.InDelta(t, 5, parseFloat("5e0"), 1e-9)
	assert.True(t, math.IsInf(parseFloat("Infinity"), 1))
	assert.True(t, math.IsNaN(parseFloat("abc")))
}

func TestLanguageContext(t *testing.T) {
	t.Parallel()

	assert.Empty(t, Language(context.Background()))
	assert.Equal(t, "zh", Language(WithLanguage(context.Background(), "zh")))
}
