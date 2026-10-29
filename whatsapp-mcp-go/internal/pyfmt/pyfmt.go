package pyfmt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// The server hands the bridge's JSON answers back to the client with their
// keys in the order the bridge wrote them. OMap keeps that order; Indent
// renders in the pydantic style (no HTML escaping, two-space indent).

// OMap is a JSON object that remembers the order of its keys.
type OMap struct {
	keys []string
	vals map[string]any
}

// NewOMap builds a map from alternating keys and values.
func NewOMap(kv ...any) *OMap {
	m := &OMap{vals: map[string]any{}}
	for i := 0; i+1 < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			panic(fmt.Sprintf("pyfmt.NewOMap: key %v is not a string", kv[i]))
		}
		m.Set(key, kv[i+1])
	}
	return m
}

// Set replaces a key in place, or appends it.
func (m *OMap) Set(key string, value any) *OMap {
	if _, ok := m.vals[key]; !ok {
		m.keys = append(m.keys, key)
	}
	m.vals[key] = value
	return m
}

// Get is the value of key, nil when missing.
func (m *OMap) Get(key string) any {
	if m == nil {
		return nil
	}
	return m.vals[key]
}

// Has says whether key is set.
func (m *OMap) Has(key string) bool {
	if m == nil {
		return false
	}
	_, ok := m.vals[key]
	return ok
}

// Keys are the keys in insertion order.

// MarshalJSON writes the keys in order.
func (m *OMap) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range m.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := Marshal(k)
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		vb, err := Marshal(m.vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// Marshal is json.Marshal without HTML escaping or a trailing newline.
func Marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// Indent renders v as indented JSON the way pydantic and json.dumps do.
func Indent(v any, indent string) string {
	raw, err := Marshal(v)
	if err != nil {
		return fmt.Sprintf("%q", err.Error())
	}
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", indent); err != nil {
		return string(raw)
	}
	return out.String()
}

// DecodeOrdered parses JSON keeping key order and number literals.
func DecodeOrdered(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("extra data after JSON value")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := NewOMap()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				key, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("unexpected key %v", kt)
				}
				m.Set(key, v)
			}
			_, err := dec.Token()
			return m, err
		case '[':
			list := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				list = append(list, v)
			}
			_, err := dec.Token()
			return list, err
		}
		return nil, fmt.Errorf("unexpected %v", t)
	default:
		return t, nil
	}
}

// Truthy is Python's bool(value) for decoded JSON.
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		f, err := x.Float64()
		return err != nil || f != 0
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case *OMap:
		return x != nil && len(x.keys) > 0
	}
	return true
}

// Str is Python's str(value) for decoded JSON, as f-strings print it.
func Str(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		return x
	case json.Number:
		return x.String()
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return Float(x)
	}
	return Indent(v, "")
}

// Float is Python's repr of a float (8.0, 0.1, 1e+20).
func Float(f float64) string {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	// Python switches to exponent notation at the same points, and writes
	// it the same way (1e+20, 1.5e-05).
	if abs := math.Abs(f); abs != 0 && (abs >= 1e16 || abs < 1e-4) {
		return strconv.FormatFloat(f, 'e', -1, 64)
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	return s
}

// FloatValue marshals as Python's json does: 8.0, not 8.
type FloatValue float64

// MarshalJSON writes the float as Python's repr.
func (f FloatValue) MarshalJSON() ([]byte, error) { return []byte(Float(float64(f))), nil }

// Repr quotes a string the way Python's repr does for plain text.
func Repr(s string) string {
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", `\'`) + "'"
}

// ---------------------------------------------------------------------------
// Times

// Time is a Python datetime: a wall-clock time that may lack an offset.
type Time struct {
	t     time.Time
	aware bool
}

// NewTime is t as a datetime, with its offset when aware.
func NewTime(t time.Time, aware bool) Time { return Time{t: t, aware: aware} }

func (p Time) micro() int { return p.t.Nanosecond() / 1000 }

func (p Time) offset() string {
	_, off := p.t.Zone()
	sign := '+'
	if off < 0 {
		sign = '-'
		off = -off
	}
	s := fmt.Sprintf("%c%02d:%02d", sign, off/3600, off/60%60)
	if off%60 != 0 {
		s += fmt.Sprintf(":%02d", off%60)
	}
	return s
}

// ISOFormat is datetime.isoformat(sep).
func (p Time) ISOFormat(sep string) string {
	s := p.t.Format("2006-01-02") + sep + p.t.Format("15:04:05")
	if us := p.micro(); us != 0 {
		s += fmt.Sprintf(".%06d", us)
	}
	if p.aware {
		s += p.offset()
	}
	return s
}

// Pydantic is how pydantic serializes a datetime: like isoformat, but Z for UTC.
func (p Time) Pydantic() string {
	s := p.ISOFormat("T")
	if p.aware && strings.HasSuffix(s, "+00:00") {
		s = strings.TrimSuffix(s, "+00:00") + "Z"
	}
	return s
}

// Display is the time as message lines show it: "2026-10-27 12:00:00 +03:00".
func (p Time) Display() string {
	s := p.t.Format("2006-01-02 15:04:05")
	if p.aware {
		off := strings.ReplaceAll(p.offset(), ":", "")
		s += " " + off[:3] + ":" + off[3:]
	}
	return s
}

// ParseISO is datetime.fromisoformat of Python 3.11 for the forms people
// and SQLite write: a date, optionally a time after any one separator
// character, optionally an offset (Z, +HH, +HHMM, +HH:MM[:SS]).
func ParseISO(text string, loc *time.Location) (Time, bool) {
	s := &isoScanner{r: []rune(text)}
	year, month, day, ok := s.date()
	if !ok {
		return Time{}, false
	}
	var hour, minute, sec, nanos, offset int
	aware := false
	if s.more() {
		s.pos++ // any separator character
		if hour, minute, sec, nanos, ok = s.clock(); !ok {
			return Time{}, false
		}
		if offset, aware, ok = s.offset(); !ok || s.more() {
			return Time{}, false
		}
	}
	if month < 1 || month > 12 || day < 1 || hour > 23 || minute > 59 || sec > 59 {
		return Time{}, false
	}
	zone := loc
	if aware {
		zone = time.FixedZone("", offset)
	}
	if zone == nil {
		zone = time.UTC
	}
	t := time.Date(year, time.Month(month), day, hour, minute, sec, nanos, zone)
	if t.Day() != day {
		return Time{}, false // 2026-02-30
	}
	return Time{t: t, aware: aware}, true
}

// isoScanner reads the parts of an ISO-8601 text, rune by rune. Each
// method returns false when the text is malformed where it reads.
type isoScanner struct {
	r   []rune
	pos int
}

func (s *isoScanner) more() bool { return s.pos < len(s.r) }

func (s *isoScanner) digitNext() bool { return s.more() && s.r[s.pos] >= '0' && s.r[s.pos] <= '9' }

// skip consumes c when it comes next.
func (s *isoScanner) skip(c rune) bool {
	if s.more() && s.r[s.pos] == c {
		s.pos++
		return true
	}
	return false
}

// int reads exactly n digits.
func (s *isoScanner) int(n int) (int, bool) {
	if s.pos+n > len(s.r) {
		return 0, false
	}
	v := 0
	for _, c := range s.r[s.pos : s.pos+n] {
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + int(c-'0')
	}
	s.pos += n
	return v, true
}

// field reads an optional two-digit field after an optional colon:
// present is false when neither a colon nor a digit follows.
func (s *isoScanner) field() (v int, present, ok bool) {
	if !s.skip(':') && !s.digitNext() {
		return 0, false, true
	}
	v, ok = s.int(2)
	return v, true, ok
}

// date reads YYYY-MM-DD or YYYYMMDD.
func (s *isoScanner) date() (year, month, day int, ok bool) {
	if year, ok = s.int(4); !ok {
		return 0, 0, 0, false
	}
	extended := s.skip('-')
	if month, ok = s.int(2); !ok {
		return 0, 0, 0, false
	}
	if extended && !s.skip('-') {
		return 0, 0, 0, false
	}
	if day, ok = s.int(2); !ok {
		return 0, 0, 0, false
	}
	return year, month, day, true
}

// clock reads HH[[:]MM[[:]SS[.ffffff]]].
func (s *isoScanner) clock() (hour, minute, sec, nanos int, ok bool) {
	if hour, ok = s.int(2); !ok {
		return 0, 0, 0, 0, false
	}
	var present bool
	if minute, present, ok = s.field(); !ok || !present {
		return hour, 0, 0, 0, ok
	}
	if sec, present, ok = s.field(); !ok || !present {
		return hour, minute, 0, 0, ok
	}
	if s.skip('.') || s.skip(',') {
		if nanos, ok = s.fraction(); !ok {
			return 0, 0, 0, 0, false
		}
	}
	return hour, minute, sec, nanos, true
}

// fraction reads the digits of a fraction of a second, kept to
// microseconds like Python.
func (s *isoScanner) fraction() (int, bool) {
	start := s.pos
	for s.digitNext() {
		s.pos++
	}
	digits := string(s.r[start:s.pos])
	if digits == "" {
		return 0, false
	}
	if len(digits) > 6 {
		digits = digits[:6]
	}
	for len(digits) < 6 {
		digits += "0"
	}
	us, _ := strconv.Atoi(digits)
	return us * 1000, true
}

// offset reads the zone, Z or +HH[[:]MM[[:]SS]], in seconds east of UTC;
// aware is false when the text ends without one.
func (s *isoScanner) offset() (seconds int, aware, ok bool) {
	if !s.more() {
		return 0, false, true
	}
	sign := 1
	switch s.r[s.pos] {
	case 'Z':
		s.pos++
		return 0, true, true
	case '-':
		sign = -1
	case '+':
	default:
		return 0, false, false
	}
	s.pos++
	hours, ok := s.int(2)
	if !ok {
		return 0, false, false
	}
	minutes, present, ok := s.field()
	if !ok {
		return 0, false, false
	}
	secs := 0
	if present {
		if secs, _, ok = s.field(); !ok {
			return 0, false, false
		}
	}
	if hours > 23 || minutes > 59 || secs > 59 {
		return 0, false, false
	}
	return sign * (hours*3600 + minutes*60 + secs), true, true
}

// ParseStoredTime reads a time column; nil when empty or unreadable.
func ParseStoredTime(v any) *Time {
	s := AsString(v)
	if s == "" {
		return nil
	}
	p, ok := ParseISO(s, nil)
	if !ok {
		return nil
	}
	return &p
}

// FilterTime turns an ISO-8601 filter into a UTC time string SQLite
// understands. Times without an offset are taken as local time.
func FilterTime(value, name string) (string, error) {
	p, ok := ParseISO(value, time.Local)
	if !ok {
		//nolint:staticcheck,revive // the message clients already see, kept verbatim
		return "", fmt.Errorf("Invalid date format for '%s': %s. Please use ISO-8601 format.", name, value)
	}
	u := p.t.UTC()
	return u.Format("2006-01-02 15:04:05") + fmt.Sprintf(".%06d+00:00", u.Nanosecond()/1000), nil
}

// AsString converts a scanned column or decoded JSON value to a string.
func AsString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []byte:
		return string(x)
	case json.Number:
		return x.String()
	}
	return fmt.Sprint(v)
}

// Ptr turns a nil pointer into a JSON null and dereferences the rest.
func Ptr[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}
