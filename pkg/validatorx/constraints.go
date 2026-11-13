package validatorx

import (
	"errors"
	"math"
	"strings"
)

// class-validator 0.15 built-in constraints and the nestjs custom ones, keyed like the export.

type validateFunc func(value Value, constraints []Value) (bool, error)

type messageFunc func(value Value, constraints []Value, eachPrefix string) string

type constraint struct {
	validate validateFunc
	message  messageFunc
}

const (
	_maxMetadataJSONLength = 2048
	_md5HashLength         = 32
	_regionCodeLength      = 12
	_md5HashMessage        = "Password must be a valid MD5 hash (32 hexadecimal characters)"
)

var errReadLengthOfUndefined = errors.New("TypeError: Cannot read properties of undefined (reading 'length')")

func constraintAt(constraints []Value, i int) Value {
	if i < len(constraints) {
		return constraints[i]
	}

	return Undefined{}
}

func firstOptions(constraints []Value) options {
	return optionsOf(constraintAt(constraints, 0))
}

func simple(fn func(Value) bool) constraint {
	return constraint{validate: func(v Value, _ []Value) (bool, error) { return fn(v), nil }, message: nil}
}

func withConstraints(fn func(Value, []Value) bool) constraint {
	return constraint{validate: func(v Value, c []Value) (bool, error) { return fn(v, c), nil }, message: nil}
}

func stringCheck(fn func(string, []Value) (bool, error)) constraint {
	return constraint{validate: func(v Value, c []Value) (bool, error) {
		s, ok := v.(string)
		if !ok {
			return false, nil
		}

		return fn(s, c)
	}, message: nil}
}

func isInt(v Value) bool {
	f, ok := v.(float64)

	return ok && !math.IsInf(f, 0) && !math.IsNaN(f) && f == math.Trunc(f)
}

func isNumberConstraint(v Value, c []Value) (bool, error) {
	f, ok := v.(float64)
	if !ok {
		return false, nil
	}

	opts := firstOptions(c)

	switch {
	case math.IsInf(f, 0):
		return opts.truthy("allowInfinity", false), nil
	case math.IsNaN(f):
		return opts.truthy("allowNaN", false), nil
	}

	maxPlaces, hasMax := opts.number("maxDecimalPlaces")
	if !hasMax {
		return true, nil
	}

	places := 0

	if math.Mod(f, 1) != 0 {
		_, frac, found := strings.Cut(numberToString(f), ".")
		if !found {
			return false, errReadLengthOfUndefined
		}

		places = len(frac)
	}

	return float64(places) <= maxPlaces, nil
}

func enumIncludes(v Value, c []Value) bool {
	entity, ok := constraintAt(c, 0).(*Object)
	if !ok {
		return false
	}

	for _, k := range entity.Keys() {
		if item, _ := entity.Get(k); strictEquals(item, v) {
			return true
		}
	}

	return false
}

func isIn(v Value, c []Value) bool {
	possible, ok := constraintAt(c, 0).(*Array)
	if !ok {
		return false
	}

	for _, item := range possible.Items {
		if strictEquals(item, v) {
			return true
		}
	}

	return false
}

func compareBound(v Value, c []Value, cmp func(a, b float64) bool) bool {
	f, ok := v.(float64)
	bound, okBound := constraintAt(c, 0).(float64)

	return ok && okBound && cmp(f, bound)
}

func lengthOptions(pairs ...any) options {
	o := NewObject()

	for i := 0; i+1 < len(pairs); i += 2 {
		key, _ := pairs[i].(string)
		o.Set(key, pairs[i+1])
	}

	return options{obj: o}
}

func isLengthMessage(v Value, c []Value, eachPrefix string) string {
	low, high := constraintAt(c, 0), constraintAt(c, 1)
	lowN, _ := low.(float64)
	highN, hasHigh := high.(float64)

	length, hasLength := 0.0, false

	switch x := v.(type) {
	case string:
		length, hasLength = float64(utf16Len(x)), true
	case *Array:
		length, hasLength = float64(len(x.Items)), true
	}

	if !isNullish(low) && (!truthy(v) || (hasLength && length < lowN)) {
		return eachPrefix + "$property must be longer than or equal to $constraint1 characters"
	}

	if !isNullish(high) && hasHigh && hasLength && length > highN {
		return eachPrefix + "$property must be shorter than or equal to $constraint2 characters"
	}

	return eachPrefix + "$property must be longer than or equal to $constraint1 and shorter than or equal to $constraint2 characters"
}

func matches(s string, c []Value) (bool, error) {
	var source, flags string

	if pattern, ok := constraintAt(c, 0).(*Object); ok {
		re, _ := pattern.Get("regex")
		fl, _ := pattern.Get("flags")
		source, flags = toString(re), toString(fl)
	} else {
		source = toString(constraintAt(c, 0))
		if mod := constraintAt(c, 1); truthy(mod) {
			flags = toString(mod)
		}
	}

	re, err := compileJSRegExp(source, flags)
	if err != nil {
		return false, err
	}

	return re.test(s), nil
}

func uuidConstraint(s string, c []Value) (bool, error) {
	version := constraintAt(c, 0)

	arr, ok := version.(*Array)
	if !ok {
		return isUUID(s, version)
	}

	for _, v := range arr.Items {
		if ok, err := isUUID(s, v); ok || err != nil {
			return ok, err
		}
	}

	return false, nil
}

func arraySize(v Value, c []Value, cmp func(n, bound float64) bool) bool {
	arr, ok := v.(*Array)
	bound, okBound := constraintAt(c, 0).(float64)

	return ok && okBound && cmp(float64(len(arr.Items)), bound)
}

func arrayUnique(v Value) bool {
	arr, ok := v.(*Array)
	if !ok {
		return false
	}

	unique := 0

	for i, item := range arr.Items {
		for j, other := range arr.Items {
			if strictEquals(other, item) {
				if j == i {
					unique++
				}

				break
			}
		}
	}

	return unique == len(arr.Items)
}

func isRegionCode(v Value) bool {
	if isNullish(v) {
		return true
	}

	s, ok := v.(string)
	if !ok || len(s) != _regionCodeLength {
		return false
	}

	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}

	return true
}

func maxJSONLength(v Value, _ []Value) (bool, error) {
	if isUndefined(v) {
		return false, errReadLengthOfUndefined
	}

	s, ok := Stringify(v)
	if !ok {
		return false, errReadLengthOfUndefined
	}

	return utf16Len(s) <= _maxMetadataJSONLength, nil
}

func md5Password(v Value) bool {
	s, ok := v.(string)
	if !ok || len(s) != _md5HashLength {
		return false
	}

	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}

	return true
}

func isString(v Value) bool {
	_, ok := v.(string)

	return ok
}

func isBoolean(v Value) bool {
	_, ok := v.(bool)

	return ok
}

func isArray(v Value) bool {
	_, ok := v.(*Array)

	return ok
}

func isValidDate(v Value) bool {
	d, ok := v.(*Date)

	return ok && d.Valid()
}

func arrayNotEmpty(v Value) bool {
	a, ok := v.(*Array)

	return ok && len(a.Items) > 0
}

func builtinConstraints() map[string]constraint {
	return map[string]constraint{
		"isDefined":  simple(func(v Value) bool { return !isNullish(v) }),
		"isNotEmpty": simple(func(v Value) bool { return v != "" && !isNullish(v) }),
		"isString":   simple(isString),
		"isInt":      simple(isInt),
		"isNumber":   {validate: isNumberConstraint, message: nil},
		"isBoolean":  simple(isBoolean),
		"isArray":    simple(isArray),
		"isObject": simple(func(v Value) bool {
			switch v.(type) {
			case *Object, *Date, Function:
				return true
			default:
				return false
			}
		}),
		"isDate": simple(isValidDate),
		"isEnum": withConstraints(enumIncludes),
		"isIn":   withConstraints(isIn),
		"min": withConstraints(func(v Value, c []Value) bool {
			return compareBound(v, c, func(a, b float64) bool { return a >= b })
		}),
		"max": withConstraints(func(v Value, c []Value) bool {
			return compareBound(v, c, func(a, b float64) bool { return a <= b })
		}),
		"maxLength": stringCheck(func(s string, c []Value) (bool, error) {
			return isLength(s, lengthOptions("min", 0.0, "max", constraintAt(c, 0))), nil
		}),
		"minLength": stringCheck(func(s string, c []Value) (bool, error) {
			return isLength(s, lengthOptions("min", constraintAt(c, 0))), nil
		}),
		"isLength": {
			validate: stringCheck(func(s string, c []Value) (bool, error) {
				return isLength(s, lengthOptions("min", constraintAt(c, 0), "max", constraintAt(c, 1))), nil
			}).validate,
			message: isLengthMessage,
		},
		"matches": stringCheck(matches),
		"isUuid":  stringCheck(uuidConstraint),
		"isEmail": stringCheck(func(s string, c []Value) (bool, error) { return isEmail(s, firstOptions(c)) }),
		"isUrl": stringCheck(func(s string, c []Value) (bool, error) {
			return isURL(s, firstOptions(c)), nil
		}),
		"isIso8601": stringCheck(func(s string, c []Value) (bool, error) {
			return isISO8601(s, firstOptions(c)), nil
		}),
		"isDateString": stringCheck(func(s string, c []Value) (bool, error) {
			return isISO8601(s, firstOptions(c)), nil
		}),
		"arrayMaxSize": withConstraints(func(v Value, c []Value) bool {
			return arraySize(v, c, func(n, b float64) bool { return n <= b })
		}),
		"arrayMinSize": withConstraints(func(v Value, c []Value) bool {
			return arraySize(v, c, func(n, b float64) bool { return n >= b })
		}),
		"arrayNotEmpty": simple(arrayNotEmpty),
		"arrayUnique":   simple(arrayUnique),
		"isRegionCode":  simple(isRegionCode),
		"maxJsonLength": {validate: maxJSONLength, message: nil},
	}
}

func anonymousConstraints() map[string]constraint {
	return map[string]constraint{
		_md5HashMessage: simple(md5Password),
	}
}
