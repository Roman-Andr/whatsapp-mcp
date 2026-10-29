package wa

import (
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestReconnectThrottles(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var cycles atomic.Int32
		r := NewReconnector(func() bool { return true }, func() error { cycles.Add(1); return nil })
		if !r.LastAt().IsZero() {
			t.Error("LastAt set before any reconnect")
		}
		if err := r.Reconnect(); err != nil {
			t.Fatal(err)
		}
		first := r.LastAt()
		time.Sleep(10 * time.Second)
		var tooSoon *TooSoonError
		err := r.Reconnect()
		if !errors.Is(err, ErrTooSoon) || !errors.As(err, &tooSoon) || tooSoon.Wait != 20*time.Second {
			t.Fatalf("second reconnect: %v", err)
		}
		if err.Error() != "reconnected less than 30s ago; try again in 20s" {
			t.Errorf("text %q", err)
		}
		time.Sleep(20 * time.Second)
		if err := r.Reconnect(); err != nil {
			t.Fatalf("after the interval: %v", err)
		}
		if cycles.Load() != 2 || !r.LastAt().Equal(first.Add(MinReconnectInterval)) {
			t.Errorf("%d cycles, last %s", cycles.Load(), r.LastAt())
		}
	})
}

func TestReconnectCountsFailures(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		boom := errors.New("connect failed")
		r := NewReconnector(func() bool { return true }, func() error { return boom })
		if err := r.Reconnect(); !errors.Is(err, boom) {
			t.Fatalf("err %v", err)
		}
		if err := r.Reconnect(); !errors.Is(err, ErrTooSoon) {
			t.Errorf("a failed attempt does not throttle: %v", err)
		}
	})
}

func TestReconnectNeedsALinkedDevice(t *testing.T) {
	t.Parallel()
	r := NewReconnector(func() bool { return false }, func() error { t.Error("cycled"); return nil })
	if err := r.Reconnect(); !errors.Is(err, ErrNotLinked) {
		t.Errorf("err %v", err)
	}
}

func TestLastAtDoesNotWaitForAReconnect(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		r := NewReconnector(func() bool { return true }, func() error { <-release; return nil })
		go r.Reconnect()
		synctest.Wait() // the reconnect is running and holds the lock
		if r.LastAt().IsZero() {
			t.Error("LastAt does not show the running reconnect")
		}
		close(release)
	})
}
