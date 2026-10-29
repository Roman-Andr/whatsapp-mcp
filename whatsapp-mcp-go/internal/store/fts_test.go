package store

import "testing"

func TestFTSQueryQuotesWords(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in, want string }{
		{"words become prefixes", `plow "route" OR`, `"plow"* "route"* "OR"*`},
		{"no words", "!!!", ""},
		{"every script", "Давид", `"Давид"*`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ftsQuery(tc.in); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}
