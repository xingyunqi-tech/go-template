// Package i18n resolves the request language like nestjs-i18n's AcceptLanguageResolver and looks up
// the translation files copied from nestjs-template src/i18n.
package i18n

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

//go:embed locales
var _locales embed.FS

var errUnknownLanguage = errors.New("no translations for language")

var _floatPrefix = regexp.MustCompile(
	`^[\t\n\v\f\r \x{00a0}\x{feff}]*([+-]?(?:Infinity|\d+\.?\d*(?:[eE][+-]?\d+)?|\.\d+(?:[eE][+-]?\d+)?))`,
)

// Translator holds every language's namespaces.
type Translator struct {
	fallback     string
	languages    []string
	translations map[string]map[string]map[string]string
}

// New loads the embedded translation files; fallback is APP_FALLBACK_LANGUAGE.
func New(fallback string) (*Translator, error) {
	t := &Translator{fallback: fallback, languages: nil, translations: map[string]map[string]map[string]string{}}

	langs, err := fs.ReadDir(_locales, "locales")
	if err != nil {
		return nil, fmt.Errorf("i18n: %w", err)
	}

	for _, lang := range langs {
		if !lang.IsDir() {
			continue
		}

		namespaces, err := loadLanguage(path.Join("locales", lang.Name()))
		if err != nil {
			return nil, err
		}

		t.languages = append(t.languages, lang.Name())
		t.translations[lang.Name()] = namespaces
	}

	if _, ok := t.translations[fallback]; !ok {
		return nil, fmt.Errorf("i18n: %w: %s", errUnknownLanguage, fallback)
	}

	return t, nil
}

func loadLanguage(dir string) (map[string]map[string]string, error) {
	files, err := fs.ReadDir(_locales, dir)
	if err != nil {
		return nil, fmt.Errorf("i18n: %w", err)
	}

	namespaces := map[string]map[string]string{}

	for _, f := range files {
		name, ok := strings.CutSuffix(f.Name(), ".json")
		if !ok {
			continue
		}

		data, err := _locales.ReadFile(path.Join(dir, f.Name()))
		if err != nil {
			return nil, fmt.Errorf("i18n: %w", err)
		}

		entries := map[string]string{}
		if err := json.Unmarshal(data, &entries); err != nil {
			return nil, fmt.Errorf("i18n: %s/%s: %w", dir, f.Name(), err)
		}

		namespaces[name] = entries
	}

	return namespaces, nil
}

// Languages returns the supported languages in directory order.
func (t *Translator) Languages() []string {
	return slices.Clone(t.languages)
}

// Resolve picks the language for an Accept-Language header: an exact match on script and region
// first, then a match on the language code alone, else the fallback language.
func (t *Translator) Resolve(acceptLanguage string) string {
	if acceptLanguage != "" {
		if lang, ok := pick(t.languages, acceptLanguage, false); ok {
			return lang
		}

		if lang, ok := pick(t.languages, acceptLanguage, true); ok {
			return lang
		}
	}

	return t.fallback
}

// Translate looks up "namespace.key" in lang, then in the fallback language.
func (t *Translator) Translate(lang, key string) (string, bool) {
	ns, rest, ok := strings.Cut(key, ".")
	if !ok || strings.Contains(rest, ".") {
		return "", false
	}

	for _, candidate := range []string{lang, t.fallback} {
		if value := t.translations[candidate][ns][rest]; value != "" {
			return value, true
		}
	}

	return "", false
}

type tag struct {
	code    string
	script  string
	region  string
	quality float64
}

func parseTag(text string, quality float64) tag {
	bits := strings.Split(text, "-")
	t := tag{code: bits[0], script: "", region: "", quality: quality}

	const withScript = 3

	switch {
	case len(bits) == withScript:
		t.script, t.region = bits[1], bits[2]
	case len(bits) > 1:
		t.region = bits[1]
	}

	return t
}

func parseFloat(text string) float64 {
	m := _floatPrefix.FindStringSubmatch(text)
	if m == nil {
		return math.NaN()
	}

	f, err := strconv.ParseFloat(strings.Replace(m[1], "Infinity", "Inf", 1), 64)
	if err != nil {
		return math.NaN()
	}

	return f
}

func parseAcceptLanguage(header string) []tag {
	var tags []tag

	for part := range strings.SplitSeq(header, ",") {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}

		bits := strings.Split(item, ";")
		quality := 1.0

		if len(bits) > 1 && bits[1] != "" {
			pieces := strings.Split(bits[1], "=")

			quality = math.NaN()
			if len(pieces) > 1 {
				quality = parseFloat(pieces[1])
			}
		}

		tags = append(tags, parseTag(bits[0], quality))
	}

	sort.SliceStable(tags, func(i, j int) bool {
		diff := tags[j].quality - tags[i].quality

		return !math.IsNaN(diff) && diff < 0
	})

	return tags
}

func pick(supported []string, header string, loose bool) (string, bool) {
	options := make([]tag, len(supported))
	for i, s := range supported {
		options[i] = parseTag(s, 1)
	}

	for _, t := range parseAcceptLanguage(header) {
		for i, option := range options {
			if t.matches(option, loose) {
				return supported[i], true
			}
		}
	}

	return "", false
}

func (t tag) matches(option tag, loose bool) bool {
	if !strings.EqualFold(t.code, option.code) {
		return false
	}

	if loose {
		return true
	}

	return (t.script == "" || strings.EqualFold(t.script, option.script)) &&
		(t.region == "" || strings.EqualFold(t.region, option.region))
}

type languageKey struct{}

// WithLanguage stores the request language in ctx.
func WithLanguage(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, languageKey{}, lang)
}

// Language returns the request language stored by WithLanguage, or "".
func Language(ctx context.Context) string {
	lang, _ := ctx.Value(languageKey{}).(string)

	return lang
}
