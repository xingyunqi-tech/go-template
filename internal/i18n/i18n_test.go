package i18n_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
)

func translator(t *testing.T) *i18n.Translator {
	t.Helper()

	tr, err := i18n.New("zh")
	require.NoError(t, err)

	return tr
}

func TestResolveMatchesAcceptLanguage(t *testing.T) {
	t.Parallel()

	tr := translator(t)

	for header, want := range map[string]string{
		"":                        "zh",
		"en":                      "en",
		"en-US,en;q=0.9":          "en",
		"zh-CN":                   "zh",
		"fr-FR, en;q=0.5":         "en",
		"en;q=0.3, zh-TW;q=0.8":   "zh",
		"fr":                      "zh",
		"de;q=0.9, en;q=0.1, *":   "en",
		"not a language!!":        "zh",
		"zh-Hans-CN;q=0.2, en-GB": "en",
	} {
		assert.Equal(t, want, tr.Resolve(header), header)
	}
}

func TestMessageFillsTemplateAndFallsBack(t *testing.T) {
	t.Parallel()

	tr := translator(t)

	msg, ok := tr.Message("en", "validation.min", map[string]any{"min": 1})
	assert.True(t, ok)
	assert.Equal(t, "must be at least 1", msg)

	msg, ok = tr.Message("fr", "common.notFound", nil)
	assert.True(t, ok)
	assert.Equal(t, "资源不存在", msg)

	_, ok = tr.Message("en", "common.nope", nil)
	assert.False(t, ok)
}

func TestEveryErrorKeyAndRuleIsTranslated(t *testing.T) {
	t.Parallel()

	tr := translator(t)

	for key := range entity.Codes() {
		assert.True(t, tr.Has(key), key)
	}

	for _, rule := range []string{
		entity.RuleRequired, entity.RuleType, entity.RuleEmail, entity.RuleURL, entity.RuleUUID, entity.RuleMin,
		entity.RuleMax, entity.RuleMinLength, entity.RuleMaxLength, entity.RuleMinItems, entity.RuleMaxItems,
		entity.RulePattern, entity.RuleEnum, entity.RuleFormat, entity.RuleExists, entity.RuleContains,
		entity.RuleMinProperties,
	} {
		assert.True(t, tr.Has("validation."+rule), rule)
	}
}
