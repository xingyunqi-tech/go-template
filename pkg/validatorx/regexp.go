package validatorx

import (
	"fmt"
	"strings"
	"sync"

	"github.com/dlclark/regexp2"
)

// jsRegExp is a JS RegExp: regexp2 in ECMAScript mode over UTF-16 code units; test searches.
type jsRegExp struct {
	re *regexp2.Regexp
}

const (
	_asciiWord    = `[A-Za-z0-9_]`
	_wordBoundary = `(?:(?<=` + _asciiWord + `)(?!` + _asciiWord + `)|(?<!` + _asciiWord + `)(?=` + _asciiWord + `))`
	_nonBoundary  = `(?:(?<=` + _asciiWord + `)(?=` + _asciiWord + `)|(?<!` + _asciiWord + `)(?!` + _asciiWord + `))`
	_dotNoDotAll  = `[^\n\r\u2028\u2029]`
)

var _regexpCache sync.Map

// compileJSRegExp compiles a JS regular expression, cached by source and flags.
func compileJSRegExp(source, flags string) (*jsRegExp, error) {
	key := flags + "/" + source
	if cached, ok := _regexpCache.Load(key); ok {
		re, _ := cached.(*jsRegExp)

		return re, nil
	}

	opts := regexp2.RegexOptions(regexp2.ECMAScript)
	dotAll := strings.Contains(flags, "s")

	if strings.Contains(flags, "i") {
		opts |= regexp2.IgnoreCase
	}

	if strings.Contains(flags, "m") {
		opts |= regexp2.Multiline
	}

	if dotAll {
		opts |= regexp2.Singleline
	}

	re, err := regexp2.Compile(translateJSRegExp(source, dotAll), opts)
	if err != nil {
		return nil, fmt.Errorf("compile /%s/%s: %w", source, flags, err)
	}

	compiled := &jsRegExp{re: re}
	_regexpCache.Store(key, compiled)

	return compiled, nil
}

func mustJSRegExp(source, flags string) *jsRegExp {
	re, err := compileJSRegExp(source, flags)
	if err != nil {
		panic(err)
	}

	return re
}

// translateJSRegExp rewrites `.`, `\b` and `\B`, whose regexp2 meaning differs from JS.
func translateJSRegExp(source string, dotAll bool) string {
	var b strings.Builder

	inClass := false

	for i := 0; i < len(source); i++ {
		c := source[i]

		switch {
		case c == '\\' && i+1 < len(source):
			next := source[i+1]

			switch {
			case !inClass && next == 'b':
				b.WriteString(_wordBoundary)
			case !inClass && next == 'B':
				b.WriteString(_nonBoundary)
			default:
				b.WriteByte(c)
				b.WriteByte(next)
			}

			i++
		case inClass:
			inClass = c != ']'

			b.WriteByte(c)
		case c == '[':
			inClass = true

			b.WriteByte(c)

			if i+1 < len(source) && source[i+1] == '^' {
				b.WriteByte('^')

				i++
			}

			if i+1 < len(source) && source[i+1] == ']' {
				b.WriteByte(']')

				i++

				inClass = false
			}
		case c == '.' && !dotAll:
			b.WriteString(_dotNoDotAll)
		default:
			b.WriteByte(c)
		}
	}

	return b.String()
}

func (r *jsRegExp) test(s string) bool {
	ok, err := r.re.MatchRunes(toUnits(s))

	return err == nil && ok
}

// exec returns the groups of the first match; matched is false for groups that did not participate.
func (r *jsRegExp) exec(s string) (texts []string, matched []bool) {
	m, err := r.re.FindRunesMatch(toUnits(s))
	if err != nil || m == nil {
		return nil, nil
	}

	groups := m.Groups()
	texts = make([]string, len(groups))
	matched = make([]bool, len(groups))

	for i := range groups {
		g := &groups[i]
		if len(g.Captures) > 0 {
			texts[i] = fromUnits(g.Runes())
			matched[i] = true
		}
	}

	return texts, matched
}
