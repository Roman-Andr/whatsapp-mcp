package pyfmt

import "strings"

// Compact is json.dumps(value, ensure_ascii=False): ", " and ": " separators.
func Compact(v any) string {
	raw, err := Marshal(v)
	if err != nil {
		return "{}"
	}
	var b strings.Builder
	inString, escaped := false, false
	for _, c := range string(raw) {
		b.WriteRune(c)
		switch {
		case inString && escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case c == '"':
			inString = !inString
		case !inString && (c == ',' || c == ':'):
			b.WriteByte(' ')
		}
	}
	return b.String()
}
