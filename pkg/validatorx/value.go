// Package validatorx reproduces the nestjs ValidationPipe (class-transformer + class-validator) from
// the exported validation.json.
package validatorx

import (
	"cmp"
	"errors"
	"math"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// Value is a JS value: nil (null), Undefined, bool, float64, string, *Object, *Array, *Date or Function.
type Value = any

// Undefined is undefined.
type Undefined struct{}

// Function is a built-in function value such as Array.prototype.sort, used only for typeof and truthiness.
type Function struct {
	Name string
}

// Array is a JS array.
type Array struct {
	Items []Value
}

// Object is an ordered JS object; Class is constructor.name, empty for plain objects.
type Object struct {
	Class  string
	keys   []string
	values map[string]Value
	// arrayLength >= 0 marks an array whose constructor is a DTO class.
	arrayLength int
}

const (
	_maxSafeInteger = 1<<53 - 1
	_maxArrayIndex  = 1<<32 - 2
	_jsWhitespace   = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a" +
		"\u2028\u2029\u202f\u205f\u3000\ufeff"
	_objectClass = "Object"
	_arrayClass  = "Array"
)

var (
	_decimalRe = regexp.MustCompile(`^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$`)
	_radixRe   = regexp.MustCompile(`^0([xXoObB])([0-9a-fA-F]+)$`)
	_lower     = cases.Lower(language.Und)
	_upper     = cases.Upper(language.Und)
)

// NewObject returns an empty plain object.
func NewObject() *Object {
	return &Object{Class: "", keys: nil, values: map[string]Value{}, arrayLength: -1}
}

func newInstance(class string) *Object {
	o := NewObject()
	o.Class = class

	return o
}

func newArrayInstance(class string, items []Value) *Object {
	o := newInstance(class)
	for i, item := range items {
		o.Set(strconv.Itoa(i), item)
	}

	o.arrayLength = len(items)

	return o
}

// Get returns an own property.
func (o *Object) Get(key string) (Value, bool) {
	v, ok := o.values[key]

	return v, ok
}

// Set writes a property; an existing key keeps its position.
func (o *Object) Set(key string, value Value) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}

	o.values[key] = value
}

// Delete removes a property.
func (o *Object) Delete(key string) {
	if _, ok := o.values[key]; !ok {
		return
	}

	delete(o.values, key)
	o.keys = slices.DeleteFunc(o.keys, func(k string) bool { return k == key })
}

// Len returns the number of own properties.
func (o *Object) Len() int {
	return len(o.keys)
}

// Keys returns keys in OrdinaryOwnPropertyKeys order: array indexes ascending, then insertion order.
func (o *Object) Keys() []string {
	var indexes []string

	others := make([]string, 0, len(o.keys))

	for _, k := range o.keys {
		if isArrayIndex(k) {
			indexes = append(indexes, k)
		} else {
			others = append(others, k)
		}
	}

	if len(indexes) == 0 {
		return others
	}

	slices.SortFunc(indexes, func(a, b string) int {
		x, _ := strconv.ParseUint(a, 10, 64)
		y, _ := strconv.ParseUint(b, 10, 64)

		return cmp.Compare(x, y)
	})

	return append(indexes, others...)
}

func (o *Object) isArrayInstance() bool {
	return o.arrayLength >= 0
}

// read reads a property; array instances also expose length and prototype methods.
func (o *Object) read(key string) Value {
	if v, ok := o.values[key]; ok {
		return v
	}

	if o.isArrayInstance() {
		if key == "length" {
			return float64(o.arrayLength)
		}

		if _arrayPrototypeMethods[key] {
			return Function{Name: key}
		}
	}

	return Undefined{}
}

func isArrayIndex(key string) bool {
	if key == "" || (len(key) > 1 && key[0] == '0') {
		return false
	}

	n, err := strconv.ParseUint(key, 10, 64)

	return err == nil && n <= _maxArrayIndex
}

func isUndefined(v Value) bool {
	_, ok := v.(Undefined)

	return ok
}

func isNullish(v Value) bool {
	return v == nil || isUndefined(v)
}

func isPrimitive(v Value) bool {
	switch v.(type) {
	case string, bool, float64:
		return true
	default:
		return false
	}
}

func truthy(v Value) bool {
	switch x := v.(type) {
	case nil, Undefined:
		return false
	case bool:
		return x
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	default:
		return true
	}
}

func jsTrim(s string) string {
	return strings.Trim(s, _jsWhitespace)
}

func jsLower(s string) string {
	return _lower.String(s)
}

func jsUpper(s string) string {
	return _upper.String(s)
}

// utf16Len is the JS string length in UTF-16 code units.
func utf16Len(s string) int {
	n := 0

	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}

	return n
}

func toNumber(v Value) float64 {
	switch x := v.(type) {
	case Undefined:
		return math.NaN()
	case nil:
		return 0
	case bool:
		if x {
			return 1
		}

		return 0
	case float64:
		return x
	case string:
		return stringToNumber(x)
	case *Array:
		return stringToNumber(toString(x))
	case *Date:
		return x.time
	default:
		return math.NaN()
	}
}

func stringToNumber(text string) float64 {
	text = jsTrim(text)

	switch text {
	case "":
		return 0
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}

	if m := _radixRe.FindStringSubmatch(text); m != nil {
		base := map[byte]int{'x': 16, 'o': 8, 'b': 2}[strings.ToLower(m[1])[0]]

		n, ok := new(big.Int).SetString(m[2], base)
		if !ok {
			return math.NaN()
		}

		f, _ := new(big.Float).SetInt(n).Float64()

		return f
	}

	if _decimalRe.MatchString(text) {
		f, err := strconv.ParseFloat(text, 64)
		if err != nil && !isRangeError(err) {
			return math.NaN()
		}

		return f
	}

	return math.NaN()
}

func isRangeError(err error) bool {
	ne, ok := err.(*strconv.NumError) //nolint:errorlint // strconv returns *NumError directly

	return ok && errors.Is(ne.Err, strconv.ErrRange)
}

// numberToString is ECMAScript Number::toString(10).
func numberToString(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	}

	sign := ""
	if f < 0 {
		sign = "-"
		f = -f
	}

	mantissa, expText, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	exp, _ := strconv.Atoi(expText)
	k := len(digits)
	n := exp + 1

	const maxPlain = 21

	switch {
	case k <= n && n <= maxPlain:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= maxPlain:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}

	e := n - 1
	expSign := "+"

	if e < 0 {
		expSign = "-"
		e = -e
	}

	m := digits
	if k > 1 {
		m = digits[:1] + "." + digits[1:]
	}

	return sign + m + "e" + expSign + strconv.Itoa(e)
}

func toString(v Value) string {
	switch x := v.(type) {
	case Undefined:
		return "undefined"
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return numberToString(x)
	case string:
		return x
	case *Array:
		parts := make([]string, len(x.Items))
		for i, item := range x.Items {
			if !isNullish(item) {
				parts[i] = toString(item)
			}
		}

		return strings.Join(parts, ",")
	case *Date:
		return x.String()
	case Function:
		return "function " + x.Name + "() { [native code] }"
	default:
		return "[object Object]"
	}
}

func strictEquals(a, b Value) bool {
	switch x := a.(type) {
	case float64:
		y, ok := b.(float64)

		return ok && x == y
	case string:
		y, ok := b.(string)

		return ok && x == y
	case bool:
		y, ok := b.(bool)

		return ok && x == y
	case nil:
		return b == nil
	case Undefined:
		return isUndefined(b)
	case Function:
		y, ok := b.(Function)

		return ok && x == y
	default:
		return a == b
	}
}

func className(v Value) string {
	switch x := v.(type) {
	case *Object:
		if x.Class == "" {
			return _objectClass
		}

		return x.Class
	case *Array:
		return _arrayClass
	default:
		return _objectClass
	}
}

// fromUnits joins UTF-16 code units (one rune each) into a string; lone surrogates become U+FFFD.
func fromUnits(units []rune) string {
	var b strings.Builder

	for i := 0; i < len(units); i++ {
		u := units[i]
		if u >= 0xD800 && u <= 0xDBFF && i+1 < len(units) && units[i+1] >= 0xDC00 && units[i+1] <= 0xDFFF {
			b.WriteRune((u-0xD800)<<10 + (units[i+1] - 0xDC00) + 0x10000)
			i++

			continue
		}

		if u >= 0xD800 && u <= 0xDFFF {
			u = utf8.RuneError
		}

		b.WriteRune(u)
	}

	return b.String()
}

// toUnits splits s into UTF-16 code units, one rune each, for JS regular expression matching.
func toUnits(s string) []rune {
	units := make([]rune, 0, len(s))

	for _, r := range s {
		if r > 0xFFFF {
			r -= 0x10000
			units = append(units, 0xD800+(r>>10), 0xDC00+(r&0x3FF))
		} else {
			units = append(units, r)
		}
	}

	return units
}

var _arrayPrototypeMethods = map[string]bool{
	"at": true, "concat": true, "copyWithin": true, "entries": true, "every": true, "fill": true,
	"filter": true, "find": true, "findIndex": true, "findLast": true, "findLastIndex": true,
	"flat": true, "flatMap": true, "forEach": true, "includes": true, "indexOf": true, "join": true,
	"keys": true, "lastIndexOf": true, "map": true, "pop": true, "push": true, "reduce": true,
	"reduceRight": true, "reverse": true, "shift": true, "slice": true, "some": true, "sort": true,
	"splice": true, "toLocaleString": true, "toReversed": true, "toSorted": true, "toSpliced": true,
	"toString": true, "unshift": true, "values": true, "with": true, "constructor": true,
	"hasOwnProperty": true, "isPrototypeOf": true, "propertyIsEnumerable": true, "valueOf": true,
}

// ToString converts v like String(v).
func ToString(v Value) string {
	return toString(v)
}
