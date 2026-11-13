package validatorx

import (
	"errors"
	"fmt"
)

const (
	_customValidation      = "customValidation"
	_nestedValidation      = "nestedValidation"
	_conditionalValidation = "conditionalValidation"
	_isDefined             = "isDefined"
	_whitelistValidation   = "whitelistValidation"
)

// ErrUnsupported reports a constraint, @Transform or @ValidateIf function in validation.json that is not implemented.
var ErrUnsupported = errors.New("unsupported validation metadata")

var errMalformed = errors.New("malformed validation.json")

type rule struct {
	property       string
	typ            string
	key            string
	constraints    []Value
	message        string
	defaultMessage string
	each           bool
	constraint     constraint
	condition      conditionFunc
}

type property struct {
	name       string
	typeTarget string
	transforms []transformFunc
	rules      []*rule
}

type classMeta struct {
	name          string
	initialValues *Object
	properties    map[string]*property
	ruled         []*property
}

type pipeOptions struct {
	whitelist           bool
	forbidUnknownValues bool
	errorStatus         int
}

func obj(v Value) *Object {
	o, _ := v.(*Object)
	if o == nil {
		return NewObject()
	}

	return o
}

func str(v Value) string {
	s, _ := v.(string)

	return s
}

func arr(v Value) []Value {
	a, _ := v.(*Array)
	if a == nil {
		return nil
	}

	return a.Items
}

func get(o *Object, key string) Value {
	v, _ := o.Get(key)

	return v
}

type registry struct {
	constraints map[string]constraint
	anonymous   map[string]constraint
	transforms  map[string]transformFunc
	conditions  map[string]conditionFunc
}

func parseMetadata(raw []byte) (pipeOptions, map[string]*classMeta, error) {
	root, err := ParseJSON(raw)
	if err != nil {
		return pipeOptions{}, nil, fmt.Errorf("%w: %w", errMalformed, err)
	}

	doc, ok := root.(*Object)
	if !ok {
		return pipeOptions{}, nil, errMalformed
	}

	pipe := obj(get(doc, "pipe"))
	status, _ := get(pipe, "errorHttpStatusCode").(float64)
	opts := pipeOptions{
		whitelist:           get(pipe, "whitelist") == true,
		forbidUnknownValues: get(pipe, "forbidUnknownValues") == true,
		errorStatus:         int(status),
	}

	reg := registry{
		constraints: builtinConstraints(),
		anonymous:   anonymousConstraints(),
		transforms:  transformFunctions(),
		conditions:  conditionFunctions(),
	}

	classes := map[string]*classMeta{}
	rawClasses := obj(get(doc, "classes"))

	var errs []error

	for _, name := range rawClasses.Keys() {
		meta, err := reg.class(name, obj(get(rawClasses, name)))
		if err != nil {
			errs = append(errs, err)

			continue
		}

		classes[name] = meta
	}

	if err := errors.Join(errs...); err != nil {
		return pipeOptions{}, nil, err
	}

	return opts, classes, nil
}

func (r registry) class(name string, raw *Object) (*classMeta, error) {
	meta := &classMeta{
		name:          name,
		initialValues: obj(get(raw, "initialValues")),
		properties:    map[string]*property{},
		ruled:         nil,
	}

	props := obj(get(raw, "properties"))

	var errs []error

	for _, pname := range props.Keys() {
		p, err := r.property(name, pname, obj(get(props, pname)))
		if err != nil {
			errs = append(errs, err)

			continue
		}

		meta.properties[pname] = p
		if len(p.rules) > 0 {
			meta.ruled = append(meta.ruled, p)
		}
	}

	return meta, errors.Join(errs...)
}

func (r registry) property(class, name string, raw *Object) (*property, error) {
	p := &property{name: name, typeTarget: "", transforms: nil, rules: nil}

	if t, ok := get(raw, "type").(*Object); ok {
		p.typeTarget = str(get(t, "target"))
	}

	var errs []error

	for _, t := range arr(get(raw, "transforms")) {
		source := str(get(obj(t), "source"))

		fn, ok := r.transforms[normalizeSource(source)]
		if !ok {
			errs = append(errs, fmt.Errorf("%w: %s.%s @Transform %s", ErrUnsupported, class, name, source))

			continue
		}

		p.transforms = append(p.transforms, fn)
	}

	for _, v := range arr(get(raw, "validations")) {
		rl, err := r.rule(class, name, obj(v))
		if err != nil {
			errs = append(errs, err)

			continue
		}

		p.rules = append(p.rules, rl)
	}

	return p, errors.Join(errs...)
}

func (r registry) rule(class, prop string, raw *Object) (*rule, error) {
	rl := &rule{
		property:       prop,
		typ:            str(get(raw, "type")),
		key:            str(get(raw, "key")),
		constraints:    arr(get(raw, "constraints")),
		message:        str(get(raw, "message")),
		defaultMessage: str(get(raw, "defaultMessage")),
		each:           get(raw, "each") == true,
		constraint:     constraint{validate: nil, message: nil},
		condition:      nil,
	}

	switch rl.typ {
	case _conditionalValidation:
		source := str(get(obj(constraintAt(rl.constraints, 0)), "source"))

		fn, ok := r.conditions[normalizeSource(source)]
		if !ok {
			return nil, fmt.Errorf("%w: %s.%s @ValidateIf %s", ErrUnsupported, class, prop, source)
		}

		rl.condition = fn
	case _nestedValidation, _whitelistValidation:
	default:
		c, ok := r.constraints[rl.key]
		if rl.key == _customValidation {
			c, ok = r.anonymous[rl.defaultMessage]
		}

		if !ok {
			return nil, fmt.Errorf("%w: %s.%s constraint %s %q", ErrUnsupported, class, prop, rl.key, rl.defaultMessage)
		}

		rl.constraint = c
	}

	return rl, nil
}
