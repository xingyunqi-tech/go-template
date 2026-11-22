// Package i18n holds the zh and en messages and resolves the request language from
// Accept-Language.
package i18n

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

//go:embed locales/*.json
var _locales embed.FS

var errNoMessages = errors.New("i18n: no messages")

// Supported lists the languages with a message file.
func Supported() []string {
	return []string{"zh", "en"}
}

// Translator looks up messages by id in the request language.
type Translator struct {
	bundle     *goi18n.Bundle
	localizers map[string]*goi18n.Localizer
	fallback   string
}

// New loads the embedded message files; fallback is APP_DEFAULT_LOCALE.
func New(fallback string) (*Translator, error) {
	fallbackTag, err := language.Parse(fallback)
	if err != nil {
		return nil, fmt.Errorf("i18n: %w", err)
	}

	bundle := goi18n.NewBundle(fallbackTag)
	bundle.RegisterUnmarshalFunc("json", json.Unmarshal)

	t := &Translator{bundle: bundle, localizers: map[string]*goi18n.Localizer{}, fallback: fallback}

	for _, lang := range Supported() {
		data, err := fs.ReadFile(_locales, path.Join("locales", lang+".json"))
		if err != nil {
			return nil, fmt.Errorf("i18n: %w", err)
		}

		if _, err := bundle.ParseMessageFileBytes(data, lang+".json"); err != nil {
			return nil, fmt.Errorf("i18n: %s: %w", lang, err)
		}

		t.localizers[lang] = goi18n.NewLocalizer(bundle, lang, fallback)
	}

	if _, ok := t.localizers[fallback]; !ok {
		return nil, fmt.Errorf("%w: %q", errNoMessages, fallback)
	}

	return t, nil
}

// Resolve picks the first language of an Accept-Language header, by q value, whose base language
// is supported, so that region and script subtags match loosely; otherwise the fallback.
func (t *Translator) Resolve(acceptLanguage string) string {
	desired, _, err := language.ParseAcceptLanguage(acceptLanguage)
	if err != nil {
		return t.fallback
	}

	for _, tag := range desired {
		base, _ := tag.Base()
		if _, ok := t.localizers[base.String()]; ok {
			return base.String()
		}
	}

	return t.fallback
}

// Message returns the message id in lang, then in the fallback language, with data filling the
// template; ok is false when no language has it.
func (t *Translator) Message(lang, id string, data map[string]any) (string, bool) {
	localizer, found := t.localizers[lang]
	if !found {
		localizer = t.localizers[t.fallback]
	}

	msg, err := localizer.Localize(&goi18n.LocalizeConfig{ //nolint:exhaustruct // defaults
		MessageID:    id,
		TemplateData: data,
	})
	if err != nil || msg == "" {
		return "", false
	}

	return msg, true
}

// Has reports whether every supported language has its own message id.
func (t *Translator) Has(id string) bool {
	for _, lang := range Supported() {
		_, tag, err := t.localizers[lang].LocalizeWithTag(&goi18n.LocalizeConfig{MessageID: id}) //nolint:exhaustruct // defaults
		if base, _ := tag.Base(); err != nil || base.String() != lang {
			return false
		}
	}

	return true
}

type languageKey struct{}

// WithLanguage stores the request language.
func WithLanguage(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, languageKey{}, lang)
}

// Language returns the request language, or "" outside a request.
func Language(ctx context.Context) string {
	lang, _ := ctx.Value(languageKey{}).(string)

	return strings.TrimSpace(lang)
}
