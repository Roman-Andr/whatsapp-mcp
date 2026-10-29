package tools

import (
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"testing"
)

func TestParseArgs(t *testing.T) {
	t.Parallel()
	spec := toolSpec{name: "demo", params: []param{
		{name: "s", typ: "string", required: true},
		{name: "i", typ: "integer", def: 20},
		{name: "f", typ: "number", nullable: true},
		{name: "b", typ: "boolean", def: true},
		{name: "l", typ: "array", nullable: true},
	}}
	defaults := Args{"s": "x", "i": 20, "f": nil, "b": true, "l": nil}
	with := func(k string, v any) Args {
		a := Args{}
		maps.Copy(a, defaults)
		a[k] = v
		return a
	}
	cases := []struct {
		name string
		raw  string
		want Args
		err  string
	}{
		{"defaults", `{"s": "x"}`, defaults, ""},
		{"unknown arguments are ignored", `{"s": "x", "zzz": 1}`, defaults, ""},
		{"integer from a string", `{"s": "x", "i": " 5 "}`, with("i", 5), ""},
		{"integer from a whole float", `{"s": "x", "i": 5.0}`, with("i", 5), ""},
		{"integer from a boolean", `{"s": "x", "i": true}`, with("i", 1), ""},
		{"integer from false", `{"s": "x", "i": false}`, with("i", 0), ""},
		{"number from a string", `{"s": "x", "f": "1.5"}`, with("f", 1.5), ""},
		{"number", `{"s": "x", "f": 2}`, with("f", 2.0), ""},
		{"null for a nullable", `{"s": "x", "f": null}`, defaults, ""},
		{"boolean from a word", `{"s": "x", "b": "Off"}`, with("b", false), ""},
		{"boolean from yes", `{"s": "x", "b": "yes"}`, with("b", true), ""},
		{"boolean from 0", `{"s": "x", "b": 0}`, with("b", false), ""},
		{"boolean from 1", `{"s": "x", "b": 1}`, with("b", true), ""},
		{"list", `{"s": "x", "l": ["a", "b"]}`, with("l", []string{"a", "b"}), ""},
		{"no arguments", ``, nil, "1 validation error for demoArguments\ns\n  Field required"},
		{"null arguments", `null`, nil, "1 validation error for demoArguments\ns\n  Field required"},
		{"not an object", `[1]`, nil, "1 validation error for demoArguments\narguments\n  Input should be an object"},
		{"not a string", `{"s": 1}`, nil, "1 validation error for demoArguments\ns\n  Input should be a valid string"},
		{"null for a string", `{"s": null}`, nil, "1 validation error for demoArguments\ns\n  Input should be a valid string"},
		{"not an integer", `{"s": "x", "i": 1.5}`, nil, "1 validation error for demoArguments\ni\n  Input should be a valid integer"},
		{"not an integer string", `{"s": "x", "i": "five"}`, nil, "1 validation error for demoArguments\ni\n  Input should be a valid integer"},
		{"not a number", `{"s": "x", "f": "abc"}`, nil, "1 validation error for demoArguments\nf\n  Input should be a valid number"},
		{"not a number type", `{"s": "x", "f": true}`, nil, "1 validation error for demoArguments\nf\n  Input should be a valid number"},
		{"not a boolean", `{"s": "x", "b": "maybe"}`, nil, "1 validation error for demoArguments\nb\n  Input should be a valid boolean"},
		{"not a boolean number", `{"s": "x", "b": 2}`, nil, "1 validation error for demoArguments\nb\n  Input should be a valid boolean"},
		{"not a list", `{"s": "x", "l": "a"}`, nil, "1 validation error for demoArguments\nl\n  Input should be a valid list"},
		{"not a list of strings", `{"s": "x", "l": [1]}`, nil, "1 validation error for demoArguments\nl\n  Input should be a valid list"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := spec.parseArgs(json.RawMessage(tc.raw))
			if tc.err != "" {
				if !errors.Is(err, ErrValidation) || err.Error() != tc.err {
					t.Errorf("got %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v %v, want %#v", got, err, tc.want)
			}
		})
	}
}

func TestArgsAccessors(t *testing.T) {
	t.Parallel()
	a := Args{"s": "x", "i": 3, "f": 1.5, "fi": 2, "b": true, "l": []string{"a"}, "null": nil}
	if a.String("s") != "x" || a.String("null") != "" || a.String("i") != "" {
		t.Error("String")
	}
	if p := a.OptString("s"); p == nil || *p != "x" || a.OptString("null") != nil {
		t.Error("OptString")
	}
	if a.Int("i") != 3 || a.Int("null") != 0 {
		t.Error("Int")
	}
	if a.Float("f") != 1.5 || a.Float("fi") != 2 || a.Float("null") != 0 {
		t.Error("Float")
	}
	if p := a.OptFloat("fi"); p == nil || *p != 2 || a.OptFloat("null") != nil {
		t.Error("OptFloat")
	}
	if !a.Bool("b") || a.Bool("null") {
		t.Error("Bool")
	}
	if p := a.OptBool("b"); p == nil || !*p || a.OptBool("null") != nil {
		t.Error("OptBool")
	}
	if !reflect.DeepEqual(a.StringSlice("l"), []string{"a"}) || a.StringSlice("null") != nil {
		t.Error("StringSlice")
	}
}
