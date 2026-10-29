package pyfmt

import (
	"testing"
	"time"
)

func TestParseTimeHandlesNanoseconds(t *testing.T) {
	t.Parallel()
	p := ParseStoredTime("2026-10-27 12:00:00.123456789+03:00")
	if p == nil || p.micro() != 123456 || p.offset() != "+03:00" {
		t.Fatalf("got %+v", p)
	}
	if got := p.Pydantic(); got != "2026-10-27T12:00:00.123456+03:00" {
		t.Errorf("pydantic %s", got)
	}
	utc := ParseStoredTime("2026-10-26 09:00:00+00:00")
	if utc.Pydantic() != "2026-10-26T09:00:00Z" || utc.ISOFormat(" ") != "2026-10-26 09:00:00+00:00" || utc.Display() != "2026-10-26 09:00:00 +00:00" {
		t.Errorf("utc %s / %s / %s", utc.Pydantic(), utc.ISOFormat(" "), utc.Display())
	}
	naive := ParseStoredTime("2026-10-27")
	if naive.Pydantic() != "2026-10-27T00:00:00" || naive.Display() != "2026-10-27 00:00:00" {
		t.Errorf("naive %s", naive.Pydantic())
	}
	for _, bad := range []string{"yesterday", "2026-13-01", "2026-02-30", "2026-10-27 25:00"} {
		if _, ok := ParseISO(bad, time.UTC); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestParseISO(t *testing.T) {
	t.Parallel()
	moscow := time.FixedZone("MSK", 3*3600)
	cases := []struct {
		in, want string
	}{
		// Without an offset a time stays naive, as fromisoformat's does.
		{"2026-10-27", "2026-10-27T00:00:00"},
		{"2026-10-27T12:05", "2026-10-27T12:05:00"},
		{"2026-10-27 12:05:30", "2026-10-27T12:05:30"},
		{"2026-10-27T12:05:30.5", "2026-10-27T12:05:30.500000"},
		{"2026-10-27T12:05:30Z", "2026-10-27T12:05:30Z"},
		{"2026-10-27T12:05:30+01:00", "2026-10-27T12:05:30+01:00"},
		{"20261027", "2026-10-27T00:00:00"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			p, ok := ParseISO(tc.in, moscow)
			if !ok {
				t.Fatal("not parsed")
			}
			if got := p.Pydantic(); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestFilterTime(t *testing.T) {
	t.Parallel()
	if _, err := FilterTime("soon", "after"); err == nil || err.Error() != "Invalid date format for 'after': soon. Please use ISO-8601 format." {
		t.Errorf("got %v", err)
	}
}

func TestPythonText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, got, want string
	}{
		{"repr", Repr("it's"), `"it's"`},
		{"repr plain", Repr("x"), "'x'"},
		{"str of None", Str(nil), "None"},
		{"str of True", Str(true), "True"},
		{"str of a float", Str(8.0), "8.0"},
		{"float", Float(1e-7), "1e-07"},
		{"float whole", Float(55), "55.0"},
		{"compact", Compact(NewOMap("a", 1, "b", []any{"x", nil})), `{"a": 1, "b": ["x", null]}`},
		{"indent", Indent(NewOMap("a", FloatValue(2)), "  "), "{\n  \"a\": 2.0\n}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

func TestDecodeOrderedKeepsKeyOrder(t *testing.T) {
	t.Parallel()
	v, err := DecodeOrdered([]byte(`{"b": 1, "a": {"d": [true, null, 1.5], "c": "x"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := Compact(v); got != `{"b": 1, "a": {"d": [true, null, 1.5], "c": "x"}}` {
		t.Errorf("got %s", got)
	}
	if _, err := DecodeOrdered([]byte(`{"a": `)); err == nil {
		t.Error("truncated JSON accepted")
	}
}
