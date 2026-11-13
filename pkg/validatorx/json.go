package validatorx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// ErrTrailingData reports text after the JSON value.
var ErrTrailingData = errors.New("unexpected data after JSON value")

// ParseJSON is JSON.parse: numbers are float64 (out of range becomes Infinity or -Infinity), and a
// repeated key keeps its first position with the last value.
func ParseJSON(data []byte) (Value, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("parse json: %w", err)
		}

		return nil, ErrTrailingData
	}

	return v, nil
}

func parseValue(dec *json.Decoder) (Value, error) {
	tok, err := dec.Token()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.ErrUnexpectedEOF
		}

		return nil, fmt.Errorf("parse json: %w", err)
	}

	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			return parseObject(dec)
		}

		return parseArray(dec)
	case json.Number:
		f, err := strconv.ParseFloat(string(t), 64)
		if err != nil && !isRangeError(err) {
			return nil, fmt.Errorf("parse json number: %w", err)
		}

		return f, nil
	default:
		return t, nil
	}
}

func parseObject(dec *json.Decoder) (Value, error) {
	obj := NewObject()

	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("parse json: %w", err)
		}

		key, _ := tok.(string)

		v, err := parseValue(dec)
		if err != nil {
			return nil, err
		}

		obj.Set(key, v)
	}

	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}

	return obj, nil
}

func parseArray(dec *json.Decoder) (Value, error) {
	arr := &Array{Items: []Value{}}

	for dec.More() {
		v, err := parseValue(dec)
		if err != nil {
			return nil, err
		}

		arr.Items = append(arr.Items, v)
	}

	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}

	return arr, nil
}

// Stringify is JSON.stringify; ok is false when v itself is undefined or a function.
func Stringify(v Value) (string, bool) {
	var b strings.Builder

	ok := writeJSON(&b, v)

	return b.String(), ok
}

func writeJSON(b *strings.Builder, v Value) bool {
	switch x := v.(type) {
	case Undefined, Function:
		return false
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			b.WriteString("null")
		} else {
			b.WriteString(numberToString(x))
		}
	case string:
		writeJSONString(b, x)
	case *Date:
		if !x.Valid() {
			b.WriteString("null")
		} else {
			writeJSONString(b, x.String())
		}
	case *Array:
		writeJSONArray(b, x.Items)
	case *Object:
		writeJSONObject(b, x)
	default:
		b.WriteString("{}")
	}

	return true
}

func writeJSONArray(b *strings.Builder, items []Value) {
	b.WriteByte('[')

	for i, item := range items {
		if i > 0 {
			b.WriteByte(',')
		}

		if !writeJSON(b, item) {
			b.WriteString("null")
		}
	}

	b.WriteByte(']')
}

func writeJSONObject(b *strings.Builder, o *Object) {
	b.WriteByte('{')

	first := true

	for _, k := range o.Keys() {
		v := o.values[k]
		if isUndefined(v) {
			continue
		}

		if _, ok := v.(Function); ok {
			continue
		}

		if !first {
			b.WriteByte(',')
		}

		first = false

		writeJSONString(b, k)
		b.WriteByte(':')
		writeJSON(b, v)
	}

	b.WriteByte('}')
}

func writeJSONString(b *strings.Builder, s string) {
	const hex = "0123456789abcdef"

	b.WriteByte('"')

	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00`)
				b.WriteByte(hex[r>>4])
				b.WriteByte(hex[r&0xF])
			} else {
				b.WriteRune(r)
			}
		}
	}

	b.WriteByte('"')
}

// MarshalJSON encodes the object like JSON.stringify.
func (o *Object) MarshalJSON() ([]byte, error) {
	s, _ := Stringify(o)

	return []byte(s), nil
}

// MarshalJSON encodes the array like JSON.stringify.
func (a *Array) MarshalJSON() ([]byte, error) {
	s, _ := Stringify(a)

	return []byte(s), nil
}
