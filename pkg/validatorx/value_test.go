package validatorx

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNumberToStringFollowsECMAScript(t *testing.T) {
	t.Parallel()

	cases := map[float64]string{
		0:                    "0",
		-1.5:                 "-1.5",
		1e21:                 "1e+21",
		1e20:                 "100000000000000000000",
		0.000001:             "0.000001",
		0.0000001:            "1e-7",
		123.456:              "123.456",
		math.MaxFloat64:      "1.7976931348623157e+308",
		9007199254740993:     "9007199254740992",
		math.Inf(1):          "Infinity",
		math.Inf(-1):         "-Infinity",
		-0.00000123456789012: "-0.00000123456789012",
	}

	for in, want := range cases {
		require.Equal(t, want, numberToString(in), "%v", in)
	}

	a, b := 0.1, 0.2
	require.Equal(t, "0.30000000000000004", numberToString(a+b))
	require.Equal(t, "NaN", numberToString(math.NaN()))
}

func TestToNumberFollowsECMAScript(t *testing.T) {
	t.Parallel()

	cases := map[string]float64{
		"":          0,
		"  12  ":    12,
		"\u300012":  12,
		"0x1F":      31,
		"0b101":     5,
		"0o17":      15,
		"1.":        1,
		".5":        0.5,
		"1e3":       1000,
		"-Infinity": math.Inf(-1),
		"1e400":     math.Inf(1),
	}

	for in, want := range cases {
		require.InDelta(t, want, toNumber(in), 0, "%q", in)
	}

	for _, in := range []string{"abc", "1_000", "+0x10", ".", "0x", "1e"} {
		require.True(t, math.IsNaN(toNumber(in)), "%q", in)
	}

	require.InDelta(t, 0, toNumber(nil), 0)
	require.InDelta(t, 1, toNumber(true), 0)
	require.True(t, math.IsNaN(toNumber(Undefined{})))
	require.InDelta(t, 7, toNumber(&Array{Items: []Value{"7"}}), 0)
	require.True(t, math.IsNaN(toNumber(NewObject())))
}

func TestParseJSONKeepsFirstPositionAndLastValueForDuplicateKeys(t *testing.T) {
	t.Parallel()

	v, err := ParseJSON([]byte(`{"a":1,"b":2,"a":3}`))
	require.NoError(t, err)

	s, ok := Stringify(v)
	require.True(t, ok)
	require.Equal(t, `{"a":3,"b":2}`, s)
}

func TestObjectKeysListArrayIndexesFirst(t *testing.T) {
	t.Parallel()

	v, err := ParseJSON([]byte(`{"b":1,"10":2,"a":3,"2":4,"01":5}`))
	require.NoError(t, err)

	o, ok := v.(*Object)
	require.True(t, ok)
	require.Equal(t, []string{"2", "10", "b", "a", "01"}, o.Keys())
}

func TestParseJSONRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, in := range []string{``, `{`, `{"a":1}x`, `[1,]`, `NaN`, `01`} {
		_, err := ParseJSON([]byte(in))
		require.Error(t, err, "%q", in)
	}
}

func TestStringifyFollowsJSONStringify(t *testing.T) {
	t.Parallel()

	o := NewObject()
	o.Set("u", Undefined{})
	o.Set("f", Function{Name: "sort"})
	o.Set("n", math.NaN())
	o.Set("s", "<a>\u0001\u2028")
	o.Set("arr", &Array{Items: []Value{Undefined{}, 1.0, Function{Name: "x"}}})
	o.Set("d", NewDate("2024-01-02T03:04:05.678Z"))
	o.Set("bad", NewDate("x"))

	s, ok := Stringify(o)
	require.True(t, ok)
	require.Equal(t, `{"n":null,"s":"<a>\u0001`+"\u2028"+`","arr":[null,1,null],"d":"2024-01-02T03:04:05.678Z","bad":null}`, s)

	_, ok = Stringify(Undefined{})
	require.False(t, ok)
}

func TestDateParsesISOStringsAsUTC(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"2024-01-01":                "2024-01-01T00:00:00.000Z",
		"2024-01-01T10:00":          "2024-01-01T10:00:00.000Z",
		"2024-01-01T10:00:00+08:00": "2024-01-01T02:00:00.000Z",
		"2024-02-30":                "2024-03-01T00:00:00.000Z",
		"+020000-01-01":             "+020000-01-01T00:00:00.000Z",
		"-000001-01-01":             "-000001-01-01T00:00:00.000Z",
		"2024-01-01T24:00":          "2024-01-02T00:00:00.000Z",
	}

	for in, want := range cases {
		d := NewDate(in)
		require.True(t, d.Valid(), in)
		require.Equal(t, want, d.String(), in)
	}

	for _, in := range []string{"2024-13-01", "x", "-000000-01-01", "2024-01-01T24:01", "2024/01/01"} {
		require.False(t, NewDate(in).Valid(), in)
	}

	require.Equal(t, "1970-01-01T00:00:01.000Z", NewDate(1000.9).String())
	require.False(t, NewDate(9e15).Valid())
}

func TestJSRegExpFollowsJavaScriptSemantics(t *testing.T) {
	t.Parallel()

	cases := []struct {
		source, flags, input string
		want                 bool
	}{
		{`^a$`, "", "a\n", false},
		{`^.$`, "", "\u2028", false},
		{`^.$`, "s", "\n", true},
		{`a\b`, "", "a\u00e9", true},
		{`a\B`, "", "ab", true},
		{`^(a)?\1b$`, "", "b", true},
		{`^\s$`, "", "\ufeff", true},
		{`^\w$`, "", "\u00e9", false},
		{`^[^]$`, "", "x", true},
		{`^[\uD800-\uDBFF].$`, "", "\U0001F600", true},
		{`^.$`, "", "\U0001F600", false},
		{`\S`, "", " ", false},
	}

	for _, c := range cases {
		re, err := compileJSRegExp(c.source, c.flags)
		require.NoError(t, err)
		require.Equal(t, c.want, re.test(c.input), "/%s/%s on %q", c.source, c.flags, c.input)
	}
}

func TestJSReplaceExpandsSpecialPatterns(t *testing.T) {
	t.Parallel()

	require.Equal(t, "x $ y", jsReplace("x $value y", "$value", "$$"))
	require.Equal(t, "a[$p]b", jsReplace("a$pb", "$p", "[$&]"))
	require.Equal(t, "aa-a-bb", jsReplace("a$pb", "$p", "$`-$`-$'"))
	require.Equal(t, "1 and 1", jsReplace("$c and $c", "$c", "1"))
}

func TestJSLowerAndUpperUseFullCaseMapping(t *testing.T) {
	t.Parallel()

	require.Equal(t, "\u03b1\u03c2", jsLower("\u0391\u03a3"))
	require.Equal(t, "SS", jsUpper("\u00df"))
	require.Equal(t, "abc", jsTrim("\u00a0abc\u3000"))
	require.Equal(t, 2, utf16Len("\U0001F600"))
	require.Equal(t, "\U0001F600", fromUnits(toUnits("\U0001F600")))
}
