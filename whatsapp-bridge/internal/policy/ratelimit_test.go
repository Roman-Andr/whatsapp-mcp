package policy

import (
	"testing"
	"time"
)

func TestRateLimiterRefillsOverTime(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0)
	l := NewRateLimiter(60, 2) // one per second after a burst of two
	l.Now = func() time.Time { return now }
	for i := range 2 {
		if ok, _ := l.Take(); !ok {
			t.Fatalf("burst call %d refused", i)
		}
	}
	ok, wait := l.Take()
	if ok || wait <= 0 {
		t.Fatalf("third call: ok=%v wait=%v", ok, wait)
	}
	now = now.Add(1500 * time.Millisecond)
	if ok, _ := l.Take(); !ok {
		t.Error("not refilled after 1.5s")
	}
	if ok, _ := l.Take(); ok {
		t.Error("refilled more than the rate")
	}
}

func TestNewRateLimiter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name             string
		perMinute, burst int
		unlimited        bool
		rate, wantBurst  float64
	}{
		{"defaults", 10, 3, false, 10.0 / 60, 3},
		{"zero rate turns the limit off", 0, 5, true, 0, 0},
		{"burst at least one", 5, 0, false, 5.0 / 60, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			l := NewRateLimiter(c.perMinute, c.burst)
			if (l == nil) != c.unlimited {
				t.Fatalf("limiter %v", l)
			}
			if rate, burst := l.Limits(); rate != c.rate || burst != c.wantBurst {
				t.Errorf("limits %v %v", rate, burst)
			}
		})
	}
	t.Run("nil limiter allows", func(t *testing.T) {
		t.Parallel()
		var off *RateLimiter
		if ok, _ := off.Take(); !ok {
			t.Error("nil limiter refused")
		}
	})
}
