package media

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/content"
)

func TestAutoDownloadWants(t *testing.T) {
	t.Parallel()
	defaults := NewAutoDownloadConfig(true, 25<<20, 100<<20)
	cases := []struct {
		name  string
		cfg   AutoDownloadConfig
		media content.MediaInfo
		want  bool
	}{
		{"small image", defaults, content.MediaInfo{Type: "image", FileLength: 200 << 10}, true},
		{"voice message", defaults, content.MediaInfo{Type: "audio", FileLength: 30 << 10}, true},
		{"document", defaults, content.MediaInfo{Type: "document", FileLength: 5 << 20}, true},
		{"huge document", defaults, content.MediaInfo{Type: "document", FileLength: 500 << 20}, false},
		{"short video", defaults, content.MediaInfo{Type: "video", FileLength: 10 << 20}, true},
		{"long video", defaults, content.MediaInfo{Type: "video", FileLength: 26 << 20}, false},
		{"video of unknown size", defaults, content.MediaInfo{Type: "video"}, false},
		{"sticker", defaults, content.MediaInfo{Type: "sticker", FileLength: 10 << 10}, false},
		{"no media", defaults, content.MediaInfo{}, false},
		{"configured video limit", NewAutoDownloadConfig(true, 50<<20, 0), content.MediaInfo{Type: "video", FileLength: 40 << 20}, true},
		{"disabled", NewAutoDownloadConfig(false, 0, 100<<20), content.MediaInfo{Type: "image"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := c.cfg.Wants(c.media); got != c.want {
				t.Errorf("Wants(%s, %d) = %v", c.media.Type, c.media.FileLength, got)
			}
		})
	}
}

var image = content.MediaInfo{Type: "image", FileLength: 100}

func TestQueueOffersWithoutBlocking(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		var done atomic.Int32
		cfg := AutoDownloadConfig{Enabled: true, MaxFileBytes: 1 << 20, Workers: 1, QueueSize: 2}
		q := NewQueue(t.Context(), cfg, func(context.Context, Job) error {
			<-release
			done.Add(1)
			return nil
		}, nil)

		// One job in the worker, two in the queue; the fourth is dropped at once.
		queued := 0
		for range 4 {
			if q.Offer(Job{MessageID: "M"}, image) {
				queued++
			}
			synctest.Wait() // the worker takes the first job
		}
		if queued != 3 {
			t.Errorf("queued %d jobs, want 3", queued)
		}
		if q.Offer(Job{MessageID: "S"}, content.MediaInfo{Type: "sticker"}) {
			t.Error("unwanted media queued")
		}
		close(release)
		synctest.Wait()
		q.Close()
		if done.Load() != 3 {
			t.Errorf("%d downloads ran", done.Load())
		}
	})
}

func TestQueueBoundsEachDownload(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var took time.Duration
		cfg := AutoDownloadConfig{Enabled: true, MaxFileBytes: 1 << 20, Workers: 1, QueueSize: 1}
		q := NewQueue(t.Context(), cfg, func(ctx context.Context, _ Job) error {
			start := time.Now()
			<-ctx.Done()
			took = time.Since(start)
			return ctx.Err()
		}, nil)
		q.Offer(Job{MessageID: "SLOW"}, image)
		time.Sleep(JobTimeout + time.Second)
		synctest.Wait()
		q.Close()
		if took != JobTimeout {
			t.Errorf("download ran %s, want %s", took, JobTimeout)
		}
	})
}

func TestQueueCloseCancelsAndDrops(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var ran atomic.Int32
		cfg := AutoDownloadConfig{Enabled: true, MaxFileBytes: 1 << 20, Workers: 1, QueueSize: 4}
		q := NewQueue(t.Context(), cfg, func(ctx context.Context, _ Job) error {
			ran.Add(1)
			<-ctx.Done() // a download that only ends when the queue closes
			return ctx.Err()
		}, nil)
		for range 3 {
			q.Offer(Job{MessageID: "M"}, image)
		}
		synctest.Wait()
		q.Close()
		if ran.Load() != 1 {
			t.Errorf("%d downloads started; the queued ones must be dropped", ran.Load())
		}
		if q.Offer(Job{MessageID: "LATE"}, image) {
			t.Error("a closed queue took a job")
		}
		q.Close() // twice is fine
	})
}

func TestNilQueue(t *testing.T) {
	t.Parallel()
	var q *Queue
	if q.Offer(Job{}, image) {
		t.Error("nil queue accepted a job")
	}
	q.Close()
}

func TestKeyedMutexSerialisesPerKey(t *testing.T) {
	t.Parallel()
	var k keyedMutex
	var active, maxActive atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			unlock := k.Lock("chat/M1")
			n := active.Add(1)
			if n > maxActive.Load() {
				maxActive.Store(n)
			}
			time.Sleep(2 * time.Millisecond)
			active.Add(-1)
			unlock()
		})
	}
	// A different key is not held up.
	unlock := k.Lock("chat/M2")
	unlock()
	wg.Wait()
	if maxActive.Load() != 1 {
		t.Errorf("%d holders at once", maxActive.Load())
	}
	if len(k.locks) != 0 {
		t.Errorf("%d locks leaked", len(k.locks))
	}
}
