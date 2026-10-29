package media

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/content"
)

// AutoDownloadConfig decides which attachments are saved as soon as they
// arrive. WhatsApp's CDN forgets media after a few weeks, and re-requesting it
// from the phone often fails (see download.go), so fetching it while it is
// fresh is the only reliable way to keep it.
type AutoDownloadConfig struct {
	Enabled       bool
	MaxVideoBytes uint64 // videos are large; only short ones are fetched
	MaxFileBytes  uint64 // limit for images, voice messages and documents
	Workers       int
	QueueSize     int
}

// Auto-download queue sizes: two workers keep up with a busy account
// without competing with download_media calls, and 256 waiting jobs cover a
// burst of attachments (a forwarded album, a history sync) without growing
// without bound.
const (
	autoDownloadWorkers = 2
	autoDownloadQueue   = 256
)

// JobTimeout bounds one automatic download, including a re-upload request,
// so a stuck download frees its worker.
const JobTimeout = 3 * time.Minute

// NewAutoDownloadConfig adds the queue's fixed sizes to the configured
// limits (WHATSAPP_AUTO_DOWNLOAD and its _MAX_VIDEO_MB, _MAX_MB).
func NewAutoDownloadConfig(enabled bool, maxVideoBytes, maxFileBytes uint64) AutoDownloadConfig {
	return AutoDownloadConfig{
		Enabled: enabled, MaxVideoBytes: maxVideoBytes, MaxFileBytes: maxFileBytes,
		Workers: autoDownloadWorkers, QueueSize: autoDownloadQueue,
	}
}

// Wants reports whether an attachment should be downloaded on arrival.
// Stickers are skipped (decorative), videos of unknown size too.
func (c AutoDownloadConfig) Wants(m content.MediaInfo) bool {
	if !c.Enabled {
		return false
	}
	switch m.Type {
	case "image", "audio", "document":
		return m.FileLength <= c.MaxFileBytes
	case "video":
		return m.FileLength > 0 && m.FileLength <= c.MaxVideoBytes
	}
	return false
}

// Job is one attachment to download.
type Job struct {
	MessageID string
	ChatJID   string
}

// Queue downloads attachments in the background. Offer never blocks: the
// event handler that calls it must keep up with incoming messages, so when
// the queue is full the job is dropped (download_media still works later,
// while the CDN has the file). The queue owns its workers; Close stops them.
type Queue struct {
	config   AutoDownloadConfig
	download func(ctx context.Context, job Job) error
	log      *slog.Logger
	timeout  time.Duration

	// cancel ends the context of every download, so a running download
	// ends with the queue.
	cancel context.CancelFunc

	mu     sync.Mutex // guards closed and sending on jobs
	closed bool
	jobs   chan Job
	wg     sync.WaitGroup
}

// NewQueue starts the workers, which call download for each job with a
// context derived from ctx and bounded by JobTimeout. log, which may be
// nil, receives failed and dropped downloads.
func NewQueue(ctx context.Context, config AutoDownloadConfig, download func(ctx context.Context, job Job) error, log *slog.Logger) *Queue {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(ctx)
	q := &Queue{
		config:   config,
		jobs:     make(chan Job, max(config.QueueSize, 1)),
		download: download,
		log:      log,
		timeout:  JobTimeout,
		cancel:   cancel,
	}
	for range max(config.Workers, 1) {
		q.wg.Go(func() { q.work(ctx) })
	}
	return q
}

func (q *Queue) work(parent context.Context) {
	for job := range q.jobs {
		if parent.Err() != nil {
			// Closing: the jobs still queued are dropped.
			continue
		}
		ctx, cancel := context.WithTimeout(parent, q.timeout)
		if err := q.download(ctx, job); err != nil {
			q.log.Warn("auto-download failed", "message_id", job.MessageID, "chat", job.ChatJID, "err", err)
		}
		cancel()
	}
}

// Offer queues the attachment of a just received message if the
// configuration wants it, and reports whether it was queued. A nil or
// closed queue takes nothing.
func (q *Queue) Offer(job Job, media content.MediaInfo) bool {
	if q == nil || !q.config.Wants(media) {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	select {
	case q.jobs <- job:
		return true
	default:
		q.log.Warn("auto-download queue full, skipping", "message_id", job.MessageID)
		return false
	}
}

// Close stops accepting jobs, cancels the running downloads, drops the
// queued ones and waits for the workers. It is safe to call more than once
// and on a nil queue.
func (q *Queue) Close() {
	if q == nil {
		return
	}
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		q.cancel()
		close(q.jobs)
	}
	q.mu.Unlock()
	q.wg.Wait()
}

// keyedMutex serialises work per key (here: per message), so an automatic
// download and a download_media call for the same file cannot write it at
// the same time.
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*keyedLock
}

type keyedLock struct {
	sync.Mutex
	refs int
}

// Lock holds key until the returned func is called.
func (k *keyedMutex) Lock(key string) func() {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = make(map[string]*keyedLock)
	}
	l := k.locks[key]
	if l == nil {
		l = &keyedLock{}
		k.locks[key] = l
	}
	l.refs++
	k.mu.Unlock()

	l.Lock()
	return func() {
		l.Unlock()
		k.mu.Lock()
		if l.refs--; l.refs == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}
