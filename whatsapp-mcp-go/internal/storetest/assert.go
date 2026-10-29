package storetest

import (
	"encoding/json"
	"strings"
	"testing"
)

// JSONOf is v (a JSON string or a value) as compact JSON with sorted
// keys, for comparisons.
func JSONOf(tb testing.TB, v any) string {
	tb.Helper()
	t := tb
	var decoded any
	if s, ok := v.(string); ok {
		if err := json.Unmarshal([]byte(s), &decoded); err != nil {
			t.Fatalf("bad JSON %s: %v", s, err)
		}
	} else {
		data, _ := json.Marshal(v)
		_ = json.Unmarshal(data, &decoded)
	}
	data, _ := json.Marshal(decoded)
	return string(data)
}

// AssertContains fails the test for every needle not in haystack.
func AssertContains(tb testing.TB, haystack string, needles ...string) {
	tb.Helper()
	t := tb
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			t.Errorf("%q not in:\n%s", n, haystack)
		}
	}
}

// AssertNotContains fails the test for every needle in haystack.
func AssertNotContains(tb testing.TB, haystack string, needles ...string) {
	tb.Helper()
	t := tb
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			t.Errorf("%q unexpectedly in:\n%s", n, haystack)
		}
	}
}

// IDsIn are the message ids of rendered message lines.
func IDsIn(text string) []string {
	var ids []string
	for line := range strings.SplitSeq(strings.TrimSpace(text), "\n") {
		if _, rest, ok := strings.Cut(line, "id: "); ok {
			id, _, _ := strings.Cut(rest, " ")
			ids = append(ids, id)
		}
	}
	return ids
}
