package validatorx

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// @Transform and @ValidateIf functions of the nestjs DTOs, registered by their normalized JS source.

type transformContext struct {
	obj        *Object
	key        string
	typeTarget string
	toInstance func(class string, value Value) (Value, error)
}

type transformFunc func(value Value, ctx *transformContext) (Value, error)

type conditionFunc func(obj *Object, value Value) bool

var (
	_whitespaceRun = regexp.MustCompile(`\s+`)
	_tagRe         = regexp.MustCompile(`<[^>]*>`)

	errToLowerCaseNotFunction = errors.New("TypeError: params.value?.toLowerCase is not a function")
	errMissingTypeTarget      = errors.New("JSON query transform without @Type target")
)

func normalizeSource(source string) string {
	return strings.TrimSpace(_whitespaceRun.ReplaceAllString(source, " "))
}

func pure(fn func(Value) Value) transformFunc {
	return func(v Value, _ *transformContext) (Value, error) { return fn(v), nil }
}

func numberOr(def float64) transformFunc {
	return pure(func(v Value) Value {
		if truthy(v) {
			return toNumber(v)
		}

		return def
	})
}

func mapString(fn func(string) string) transformFunc {
	return pure(func(v Value) Value {
		if s, ok := v.(string); ok {
			return fn(s)
		}

		return v
	})
}

func lowerTrimOptional(v Value, _ *transformContext) (Value, error) {
	if isNullish(v) {
		return Undefined{}, nil
	}

	s, ok := v.(string)
	if !ok {
		return nil, errToLowerCaseNotFunction
	}

	return jsTrim(jsLower(s)), nil
}

func lowerEach(v Value) Value {
	arr, ok := v.(*Array)
	if !ok {
		return v
	}

	out := make([]Value, len(arr.Items))
	for i, item := range arr.Items {
		if s, isString := item.(string); isString {
			out[i] = jsLower(s)
		} else {
			out[i] = item
		}
	}

	return &Array{Items: out}
}

func stringBool(v Value) Value {
	switch v {
	case "true":
		return true
	case "false":
		return false
	default:
		return v
	}
}

func trimOr(empty Value) transformFunc {
	return pure(func(v Value) Value {
		s, ok := v.(string)
		if !ok {
			return v
		}

		if t := jsTrim(s); t != "" {
			return t
		}

		return empty
	})
}

func jsonQuery(v Value, ctx *transformContext) (Value, error) {
	if !truthy(v) {
		return Undefined{}, nil
	}

	if ctx.typeTarget == "" {
		return nil, fmt.Errorf("%s: %w", ctx.key, errMissingTypeTarget)
	}

	parsed, err := ParseJSON([]byte(toString(v)))
	if err != nil {
		return v, nil //nolint:nilerr // nestjs returns the value unchanged when JSON.parse fails
	}

	return ctx.toInstance(ctx.typeTarget, parsed)
}

func splitList(v Value) Value {
	items := []Value{v}
	if arr, ok := v.(*Array); ok {
		items = arr.Items
	}

	out := []Value{}

	for _, item := range items {
		for part := range strings.SplitSeq(toString(item), ",") {
			if t := jsTrim(part); t != "" {
				out = append(out, t)
			}
		}
	}

	return &Array{Items: out}
}

func blankTo(def Value, nullBlank bool) transformFunc {
	return pure(func(v Value) Value {
		if isUndefined(v) || v == "" || (nullBlank && v == nil) {
			return def
		}

		return toNumber(v)
	})
}

func sanitize(s string) string {
	return jsTrim(_tagRe.ReplaceAllString(s, ""))
}

const (
	_defaultPage        = 1
	_defaultLimit       = 10
	_defaultAdminLimit  = 20
	_defaultLargeLimit  = 50
	_segmentDefaultSize = 10
)

func transformFunctions() map[string]transformFunc {
	lower := mapString(jsLower)
	upper := mapString(jsUpper)
	trim := mapString(jsTrim)
	boolean := pure(stringBool)

	entries := map[string]transformFunc{
		"({ value }) => (value ? Number(value) : 1)":  numberOr(_defaultPage),
		"({ value }) => (value ? Number(value) : 10)": numberOr(_defaultLimit),
		"({ value }) => (value ? Number(value) : 20)": numberOr(_defaultAdminLimit),
		"({ value }) => (value ? Number(value) : 50)": numberOr(_defaultLargeLimit),

		"({ value }) => (typeof value === 'string' ? value.toLowerCase() : value)": lower,
		"({ value }) => typeof value === 'string' ? value.toLowerCase() : value":   lower,
		"(params) => params.value?.toLowerCase().trim()":                           lowerTrimOptional,
		"(params) => { if (typeof params.value !== 'string') { return params.value; } " +
			"return params.value .replace(/<[^>]*>/g, '') .trim(); }": mapString(sanitize),
		"({ value }) => Array.isArray(value) ? value.map((item) => " +
			"(typeof item === 'string' ? item.toLowerCase() : item)) : value": pure(lowerEach),
		"({ value }) => (typeof value === 'string' ? value.trim() : value)":            trim,
		"({ value }) => value === 'true' ? true : value === 'false' ? false : value":   boolean,
		"({ value }) => (value === 'true' ? true : value === 'false' ? false : value)": boolean,
		"({ value }) => { if (value === 'true') return true; " +
			"if (value === 'false') return false; return value; }": boolean,
		"({ value }) => typeof value === 'string' ? value.trim() || undefined : value":   trimOr(Undefined{}),
		"({ value }) => (typeof value === 'string' ? value.trim() || undefined : value)": trimOr(Undefined{}),
		"({ value }) => typeof value === 'string' ? value.trim() || null : value":        trimOr(nil),
		"({ value }) => { if (!value) { return undefined; } try { return " +
			"(0, class_transformer_1.plainToInstance)(cls, JSON.parse(value)); } " +
			"catch { return value; } }": jsonQuery,
		"({ value }) => [] .concat(value) .flatMap((item) => String(item).split(',')) " +
			".map((item) => item.trim()) .filter(Boolean)": pure(splitList),
		"({ value }) => (value === undefined || value === '' ? 0 : Number(value))": blankTo(0.0, false),
		"({ value }) => (value === undefined || value === '' ? " +
			"exports.SEGMENT_LIMITS.defaultSize : Number(value))": blankTo(float64(_segmentDefaultSize), false),
		"({ value }) => value === '' || value === undefined || value === null " +
			"? undefined : Number(value)": blankTo(Undefined{}, true),
		"({ value }) => value === undefined || value === '' ? undefined : Number(value)": blankTo(Undefined{}, false),
		"({ value }) => typeof value === 'string' ? value.toUpperCase() : value":         upper,
	}

	out := make(map[string]transformFunc, len(entries))
	for source, fn := range entries {
		out[normalizeSource(source)] = fn
	}

	return out
}

func field(obj *Object, key string) Value {
	if obj == nil {
		return Undefined{}
	}

	return obj.read(key)
}

func conditionFunctions() map[string]conditionFunc {
	notNullish := func(_ *Object, v Value) bool { return !isNullish(v) }

	entries := map[string]conditionFunc{
		"(object, value) => { return object[propertyName] !== null " +
			"&& object[propertyName] !== undefined; }": notNullish,
		"(_, value) => value !== undefined":                   func(_ *Object, v Value) bool { return !isUndefined(v) },
		"(_, value) => value !== null":                        func(_ *Object, v Value) bool { return v != nil },
		"(_, value) => value !== undefined && value !== null": notNullish,
		"(dto) => dto.knowledgePointIds !== undefined": func(o *Object, _ Value) bool {
			return !isUndefined(field(o, "knowledgePointIds"))
		},
		"(obj) => !hasAllRegionCodes(obj)": func(o *Object, _ Value) bool {
			return !truthy(field(o, "provinceCode")) || !truthy(field(o, "cityCode")) || !truthy(field(o, "districtCode"))
		},
		"(dto) => dto.reason === feedback_1.QuestionReportReason.OTHER || dto.content !== undefined": func(o *Object, _ Value) bool {
			return field(o, "reason") == "other" || !isUndefined(field(o, "content"))
		},
		"(dto) => dto.feedback !== null": func(o *Object, _ Value) bool { return field(o, "feedback") != nil },
		"(dto) => dto.errataId !== null": func(o *Object, _ Value) bool { return field(o, "errataId") != nil },
	}

	out := make(map[string]conditionFunc, len(entries))
	for source, fn := range entries {
		out[normalizeSource(source)] = fn
	}

	return out
}
