package validatorx

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	_unknownValueMessage   = "an unknown value was passed to the validate function"
	_objectExpectedMessage = "Validation failed (object is expected)"
)

var (
	// ErrUnknownDTO reports a DTO missing from validation.json.
	ErrUnknownDTO = errors.New("dto not in validation.json")

	_primitiveTargets = map[string]bool{"String": true, "Number": true, "Boolean": true, "Date": true}
	_protoKeys        = []string{"__proto__", "prototype", "constructor"}
	_objectProtoFuncs = map[string]bool{
		"constructor": true, "__defineGetter__": true, "__defineSetter__": true, "__lookupGetter__": true,
		"__lookupSetter__": true, "hasOwnProperty": true, "isPrototypeOf": true, "propertyIsEnumerable": true,
		"toLocaleString": true, "toString": true, "valueOf": true,
	}
)

// ValidationError is a ValidationPipe failure: Status is errorHttpStatusCode, Errors maps fields to messages or nested errors.
type ValidationError struct {
	Status int
	Errors *Object
}

func (e *ValidationError) Error() string {
	s, _ := Stringify(e.Errors)

	return "validation failed: " + s
}

// Validator holds the parsed validation.json.
type Validator struct {
	pipe    pipeOptions
	classes map[string]*classMeta
}

// New parses validation.json and returns ErrUnsupported when it uses anything not implemented.
func New(raw []byte) (*Validator, error) {
	pipe, classes, err := parseMetadata(raw)
	if err != nil {
		return nil, err
	}

	return &Validator{pipe: pipe, classes: classes}, nil
}

// Has reports whether dto is in validation.json.
func (v *Validator) Has(dto string) bool {
	_, ok := v.classes[dto]

	return ok
}

// Run is ValidationPipe.transform for value (paramType is body or query): it returns
// *ValidationError on failed constraints, another error when a transform throws, else the transformed value.
func (v *Validator) Run(dto, paramType string, value Value) (Value, error) {
	meta, ok := v.classes[dto]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownDTO, dto)
	}

	original := value
	if isNullish(value) {
		value = NewObject()
	}

	primitive := isPrimitive(value)
	stripProtoKeys(value)

	entity, err := v.transform(value, dto)
	if err != nil {
		return nil, err
	}

	instance, replaced := entity.(*Object)
	replaced = !replaced || instance.Class == ""

	if replaced {
		if a, isArray := entity.(*Array); isArray {
			instance = newArrayInstance(dto, a.Items)
		} else {
			instance = newInstance(dto)
		}
	}

	errs, err := v.validate(instance)
	if err != nil {
		return nil, err
	}

	if errs.Len() > 0 {
		return nil, &ValidationError{Status: v.pipe.errorStatus, Errors: errs}
	}

	_, isArray := value.(*Array)
	if replaced && (primitive || isArray) && len(meta.ruled) > 0 {
		out := NewObject()
		out.Set(paramType, _objectExpectedMessage)

		return nil, &ValidationError{Status: v.pipe.errorStatus, Errors: out}
	}

	if primitive {
		return original, nil
	}

	if instance.isArrayInstance() {
		items := make([]Value, instance.arrayLength)
		for i := range items {
			item, ok := instance.Get(strconv.Itoa(i))
			if !ok {
				item = nil
			}

			items[i] = item
		}

		return &Array{Items: items}, nil
	}

	return instance, nil
}

func stripProtoKeys(value Value) {
	stack := []Value{value}

	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		switch x := current.(type) {
		case *Array:
			stack = append(stack, x.Items...)
		case *Object:
			for _, k := range _protoKeys {
				x.Delete(k)
			}

			for _, k := range x.keys {
				stack = append(stack, x.values[k])
			}
		}
	}
}

func (v *Validator) transform(value Value, target string) (Value, error) {
	if a, ok := value.(*Array); ok {
		out := make([]Value, len(a.Items))

		for i, item := range a.Items {
			t, err := v.transform(item, target)
			if err != nil {
				return nil, err
			}

			out[i] = t
		}

		return &Array{Items: out}, nil
	}

	if _primitiveTargets[target] {
		return transformPrimitive(value, target), nil
	}

	src, ok := value.(*Object)
	if !ok {
		return value, nil
	}

	meta := v.classes[target]

	result := NewObject()
	if target != "" {
		result.Class = target
	}

	if meta != nil {
		for _, k := range meta.initialValues.Keys() {
			result.Set(k, meta.initialValues.values[k])
		}
	}

	for _, key := range src.Keys() {
		if key == "__proto__" || _objectProtoFuncs[key] {
			continue
		}

		final, err := v.transformProperty(src, key, meta)
		if err != nil {
			return nil, err
		}

		result.Set(key, final)
	}

	return result, nil
}

func (v *Validator) transformProperty(src *Object, key string, meta *classMeta) (Value, error) {
	var prop *property
	if meta != nil {
		prop = meta.properties[key]
	}

	subTarget := ""
	if prop != nil {
		subTarget = prop.typeTarget
	}

	raw, _ := src.Get(key)

	final, err := v.transform(raw, subTarget)
	if err != nil || prop == nil {
		return final, err
	}

	ctx := &transformContext{obj: src, key: key, typeTarget: subTarget, toInstance: v.plainToInstance}

	for _, fn := range prop.transforms {
		if final, err = fn(final, ctx); err != nil {
			return nil, err
		}
	}

	return final, nil
}

func (v *Validator) plainToInstance(class string, value Value) (Value, error) {
	return v.transform(value, class)
}

func transformPrimitive(value Value, target string) Value {
	if isNullish(value) {
		return value
	}

	switch target {
	case "String":
		return toString(value)
	case "Number":
		return toNumber(value)
	case "Date":
		return NewDate(value)
	default:
		return truthy(value)
	}
}

type fieldError struct {
	property    string
	constraints *Object
	children    []*fieldError
}

func newFieldError(property string) *fieldError {
	return &fieldError{property: property, constraints: NewObject(), children: nil}
}

func (v *Validator) validate(entity *Object) (*Object, error) {
	var errs []*fieldError

	if err := v.execute(entity, &errs); err != nil {
		return nil, err
	}

	return generate(strip(errs)), nil
}

func (v *Validator) execute(value Value, errs *[]*fieldError) error {
	var ruled []*property

	if meta := v.classes[className(value)]; meta != nil {
		ruled = meta.ruled
	}

	if len(ruled) == 0 && v.pipe.forbidUnknownValues {
		e := newFieldError("undefined")
		e.constraints.Set("unknownValue", _unknownValueMessage)
		*errs = append(*errs, e)

		return nil
	}

	o, isObject := value.(*Object)
	if v.pipe.whitelist && isObject {
		allowed := make(map[string]bool, len(ruled))
		for _, p := range ruled {
			allowed[p.name] = true
		}

		for _, k := range o.Keys() {
			if !allowed[k] {
				o.Delete(k)
			}
		}
	}

	for _, p := range ruled {
		var (
			defined []*rule
			rest    []*rule
		)

		for _, r := range p.rules {
			switch r.typ {
			case _isDefined:
				defined = append(defined, r)
			case _whitelistValidation:
			default:
				rest = append(rest, r)
			}
		}

		if err := v.perform(value, readField(value, p.name), p.name, defined, rest, errs); err != nil {
			return err
		}
	}

	return nil
}

func readField(value Value, name string) Value {
	if o, ok := value.(*Object); ok {
		return o.read(name)
	}

	return Undefined{}
}

func (v *Validator) perform(obj, value Value, name string, defined, rules []*rule, errs *[]*fieldError) error {
	e := newFieldError(name)
	*errs = append(*errs, e)

	target, _ := obj.(*Object)

	var custom, nested []*rule

	for _, r := range rules {
		switch r.typ {
		case _conditionalValidation:
			if !r.condition(target, value) {
				return nil
			}
		case _customValidation:
			custom = append(custom, r)
		case _nestedValidation:
			nested = append(nested, r)
		}
	}

	if err := v.custom(obj, value, defined, e); err != nil {
		return err
	}

	if err := v.custom(obj, value, custom, e); err != nil {
		return err
	}

	return v.nested(value, nested, e)
}

func (v *Validator) custom(obj, value Value, rules []*rule, e *fieldError) error {
	for _, r := range rules {
		valid, err := checkRule(r, value)
		if err != nil {
			return err
		}

		if !valid {
			e.constraints.Set(r.key, message(obj, value, r))
		}
	}

	return nil
}

func checkRule(r *rule, value Value) (bool, error) {
	a, isArray := value.(*Array)
	if !r.each || !isArray {
		return r.constraint.validate(value, r.constraints)
	}

	valid := true

	for _, item := range a.Items {
		ok, err := r.constraint.validate(item, r.constraints)
		if err != nil {
			return false, err
		}

		valid = valid && ok
	}

	return valid, nil
}

func (v *Validator) nested(value Value, rules []*rule, e *fieldError) error {
	if isUndefined(value) {
		return nil
	}

	for _, r := range rules {
		switch x := value.(type) {
		case *Array:
			for i, item := range x.Items {
				if err := v.perform(value, item, strconv.Itoa(i), nil, rules, &e.children); err != nil {
					return err
				}
			}
		case *Object, Function:
			if err := v.execute(value, &e.children); err != nil {
				return err
			}
		default:
			e.constraints.Set(r.key, message(nil, value, r))
		}
	}

	return nil
}

func message(obj, value Value, r *rule) string {
	var msg string

	switch {
	case r.message != "":
		msg = r.message
	case r.constraint.message != nil:
		prefix := ""
		if r.each {
			prefix = "each value in "
		}

		msg = r.constraint.message(value, r.constraints, prefix)
	default:
		msg = r.defaultMessage
	}

	for i, c := range r.constraints {
		msg = jsReplace(msg, "$constraint"+strconv.Itoa(i+1), constraintToString(c))
	}

	if isPrimitive(value) {
		msg = jsReplace(msg, "$value", toString(value))
	}

	msg = jsReplace(msg, "$property", r.property)

	target := "Function"
	if obj != nil {
		target = className(obj)
	}

	return jsReplace(msg, "$target", target)
}

func constraintToString(v Value) string {
	if a, ok := v.(*Array); ok {
		parts := make([]string, len(a.Items))
		for i, item := range a.Items {
			parts[i] = toString(constraintValue(item))
		}

		return strings.Join(parts, ", ")
	}

	return toString(constraintValue(v))
}

func constraintValue(v Value) Value {
	o, ok := v.(*Object)
	if !ok {
		return v
	}

	re, hasRegex := o.Get("regex")
	flags, hasFlags := o.Get("flags")

	if hasRegex && hasFlags {
		return "/" + toString(re) + "/" + toString(flags)
	}

	source, hasSource := o.Get("source")
	if _, hasFunction := o.Get("function"); hasSource && hasFunction {
		return source
	}

	return v
}

// jsReplace is text.replace(new RegExp(pattern, 'g'), replacement) with pattern matched literally.
func jsReplace(text, pattern, replacement string) string {
	if pattern == "" || !strings.Contains(text, pattern) {
		return text
	}

	var b strings.Builder

	rest := 0

	for {
		i := strings.Index(text[rest:], pattern)
		if i < 0 {
			break
		}

		start := rest + i
		end := start + len(pattern)

		b.WriteString(text[rest:start])
		b.WriteString(substitution(replacement, pattern, text[:start], text[end:]))

		rest = end
	}

	b.WriteString(text[rest:])

	return b.String()
}

func substitution(replacement, matched, before, after string) string {
	if !strings.Contains(replacement, "$") {
		return replacement
	}

	var b strings.Builder

	for i := 0; i < len(replacement); i++ {
		c := replacement[i]
		if c == '$' && i+1 < len(replacement) {
			switch replacement[i+1] {
			case '$':
				b.WriteByte('$')
			case '&':
				b.WriteString(matched)
			case '`':
				b.WriteString(before)
			case '\'':
				b.WriteString(after)
			default:
				b.WriteByte(c)

				continue
			}

			i++

			continue
		}

		b.WriteByte(c)
	}

	return b.String()
}

func strip(errs []*fieldError) []*fieldError {
	kept := make([]*fieldError, 0, len(errs))

	for _, e := range errs {
		e.children = strip(e.children)
		if e.constraints.Len() == 0 && len(e.children) == 0 {
			continue
		}

		kept = append(kept, e)
	}

	return kept
}

func generate(errs []*fieldError) *Object {
	out := NewObject()

	for _, e := range errs {
		if len(e.children) > 0 {
			out.Set(e.property, generate(e.children))

			continue
		}

		parts := make([]string, 0, e.constraints.Len())
		for _, k := range e.constraints.keys {
			parts = append(parts, str(e.constraints.values[k]))
		}

		out.Set(e.property, strings.Join(parts, ", "))
	}

	return out
}
