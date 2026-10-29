package tools

// Arguments: validation against a tool's parameters, coerced the way
// pydantic's lax mode does, and typed accessors for the handlers.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ErrValidation is a rejected argument. The error wrapping it reads like
// pydantic's validation errors, which clients are shown.
var ErrValidation = errors.New("validation error")

// validationError is an ErrValidation of one field.
type validationError struct {
	tool, field, msg string
}

func (e *validationError) Error() string {
	return fmt.Sprintf("1 validation error for %sArguments\n%s\n  %s", e.tool, e.field, e.msg)
}

func (e *validationError) Unwrap() error { return ErrValidation }

// Args holds a call's arguments after validation, defaults filled in. A
// value has the Go type of its parameter's JSON type: string, int,
// float64, bool or []string; or nil for a nullable parameter left out.
type Args map[string]any

// parseArgs validates raw arguments against the parameters, coercing the
// way pydantic's lax mode does ("5" is an integer, "true" a boolean).
// Unknown arguments are ignored.
func (t toolSpec) parseArgs(raw json.RawMessage) (Args, error) {
	in := map[string]json.RawMessage{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, &validationError{t.name, "arguments", "Input should be an object"}
		}
	}
	out := Args{}
	for _, p := range t.params {
		v, present := in[p.name]
		if !present {
			if p.required {
				return nil, &validationError{t.name, p.name, "Field required"}
			}
			out[p.name] = p.def
			continue
		}
		var decoded any
		dec := json.NewDecoder(strings.NewReader(string(v)))
		dec.UseNumber()
		if err := dec.Decode(&decoded); err != nil {
			return nil, &validationError{t.name, p.name, "Invalid JSON"}
		}
		if decoded == nil {
			if !p.nullable {
				return nil, &validationError{t.name, p.name, typeError(p.typ)}
			}
			out[p.name] = nil
			continue
		}
		value, ok := coerce(p.typ, decoded)
		if !ok {
			return nil, &validationError{t.name, p.name, typeError(p.typ)}
		}
		out[p.name] = value
	}
	return out, nil
}

func typeError(typ string) string {
	switch typ {
	case "integer":
		return "Input should be a valid integer"
	case "number":
		return "Input should be a valid number"
	case "boolean":
		return "Input should be a valid boolean"
	case "array":
		return "Input should be a valid list"
	}
	return "Input should be a valid string"
}

// coerce converts a decoded JSON value to the Go type of typ, the way
// pydantic's lax mode accepts it; false when it does not.
func coerce(typ string, v any) (any, bool) {
	switch typ {
	case "string":
		s, ok := v.(string)
		return s, ok
	case "integer":
		return coerceInt(v)
	case "number":
		return coerceFloat(v)
	case "boolean":
		return coerceBool(v)
	case "array":
		return coerceStrings(v)
	}
	return v, true
}

// coerceInt accepts integers, floats without a fraction, numeric strings
// and booleans.
func coerceInt(v any) (any, bool) {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return int(i), true
		}
		if f, err := x.Float64(); err == nil && f == math.Trunc(f) && !math.IsInf(f, 0) {
			return int(f), true
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(x)); err == nil {
			return i, true
		}
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	}
	return nil, false
}

// coerceFloat accepts numbers and numeric strings.
func coerceFloat(v any) (any, bool) {
	switch x := v.(type) {
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return nil, false
}

// coerceBool accepts booleans, 0 and 1, and pydantic's words for them.
func coerceBool(v any) (any, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case json.Number:
		switch x.String() {
		case "0":
			return false, true
		case "1":
			return true, true
		}
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "0", "off", "f", "false", "n", "no":
			return false, true
		case "1", "on", "t", "true", "y", "yes":
			return true, true
		}
	}
	return nil, false
}

// coerceStrings accepts a list of strings only.
func coerceStrings(v any) (any, bool) {
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, len(list))
	for i, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out[i] = s
	}
	return out, true
}

// String is a string argument, "" when null.
func (a Args) String(name string) string {
	s, _ := a[name].(string)
	return s
}

// OptString is a string argument, nil when null.
func (a Args) OptString(name string) *string {
	s, ok := a[name].(string)
	if !ok {
		return nil
	}
	return &s
}

// Int is an integer argument, 0 when null.
func (a Args) Int(name string) int {
	i, _ := a[name].(int)
	return i
}

// Float is a number argument, 0 when null.
func (a Args) Float(name string) float64 {
	switch x := a[name].(type) {
	case float64:
		return x
	case int:
		return float64(x)
	}
	return 0
}

// OptFloat is a number argument, nil when null.
func (a Args) OptFloat(name string) *float64 {
	if a[name] == nil {
		return nil
	}
	f := a.Float(name)
	return &f
}

// Bool is a boolean argument, false when null.
func (a Args) Bool(name string) bool {
	b, _ := a[name].(bool)
	return b
}

// OptBool is a boolean argument, nil when null.
func (a Args) OptBool(name string) *bool {
	b, ok := a[name].(bool)
	if !ok {
		return nil
	}
	return &b
}

// StringSlice is a list argument, nil when null.
func (a Args) StringSlice(name string) []string {
	s, _ := a[name].([]string)
	return s
}
