package embedding

import (
	"context"
	"sync"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// Capture status labels. Keep this set small and static (low cardinality).
const (
	captureStatusPersisted   = "persisted"
	captureStatusDroppedFull = "dropped_queue_full"
	captureStatusErrorOpen   = "error_open"
	captureStatusErrorPut    = "error_put"
)

const (
	// defaultCaptureBufferSize bounds in-flight records before drop-on-full.
	defaultCaptureBufferSize = 256
	// defaultPruneEveryNWrites controls how often the writer prunes; pruning on
	// every write would issue a COUNT(*) per row.
	defaultPruneEveryNWrites = 100
)

// CaptureMetrics records capture outcomes. *metrics.BirdNETMetrics satisfies it.
type CaptureMetrics interface {
	RecordEmbeddingCapture(status string)
	RecordEmbeddingPrune(pruned int)
}

// Capture is an async, non-blocking sink that persists embedding Records to a
// lazily-opened Store via a single background writer goroutine. It is safe for
// concurrent use. The store and goroutine are created on the first non-empty
// Capture call, so a never-enabled feature costs nothing.
type Capture struct {
	resolve func() (path string, maxRows int)
	metrics CaptureMetrics
	log     logger.Logger
	bufSize int
	pruneN  int

	mu         sync.Mutex
	ch         chan Record
	store      *Store
	stop       chan struct{}
	done       chan struct{}
	started    bool
	openFailed bool
	closed     bool
}

// CaptureOption configures a Capture.
type CaptureOption func(*Capture)

// WithCaptureMetrics injects a metrics recorder; nil disables metrics.
func WithCaptureMetrics(m CaptureMetrics) CaptureOption {
	return func(c *Capture) { c.metrics = m }
}

// WithCaptureBufferSize overrides the buffered-channel capacity (test hook).
func WithCaptureBufferSize(n int) CaptureOption {
	return func(c *Capture) {
		if n > 0 {
			c.bufSize = n
		}
	}
}

// WithPruneInterval overrides how many writes occur between prunes (test hook).
func WithPruneInterval(n int) CaptureOption {
	return func(c *Capture) {
		if n > 0 {
			c.pruneN = n
		}
	}
}

// NewCapture builds a Capture. resolve is invoked once, at lazy open, to read
// the store path and row cap from live settings. resolve must not be nil.
func NewCapture(resolve func() (path string, maxRows int), opts ...CaptureOption) *Capture {
	c := &Capture{
		resolve: resolve,
		log:     logger.Global().Module("embedding"),
		bufSize: defaultCaptureBufferSize,
		pruneN:  defaultPruneEveryNWrites,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Capture enqueues rec for async persistence and returns immediately. It is a
// no-op when rec carries no vector. On first use it lazily opens the store and
// starts the writer. When the buffer is full it drops rec (counted) instead of
// blocking. It deliberately takes no caller context: the actual store.Put runs
// under context.Background() in the writer, so a request-scoped deadline (e.g.
// the CompositeAction timeout in DatabaseAction) can never cancel a buffered
// write.
func (c *Capture) Capture(rec Record) {
	if len(rec.Vector) == 0 {
		return
	}
	c.mu.Lock()
	if c.closed || c.openFailed {
		c.mu.Unlock()
		return
	}
	if !c.started {
		if err := c.openLocked(); err != nil {
			c.openFailed = true
			c.mu.Unlock()
			c.log.Error("failed to open embedding store; capture disabled", logger.Error(err))
			c.record(captureStatusErrorOpen)
			return
		}
	}
	ch := c.ch
	c.mu.Unlock()

	select {
	case ch <- rec:
	default:
		c.record(captureStatusDroppedFull)
	}
}

// openLocked opens the store and starts the writer goroutine. Caller holds c.mu.
// Establishes happens-before for all writer field reads because the goroutine
// is launched after the fields are set, under the lock.
func (c *Capture) openLocked() error {
	path, maxRows := c.resolve()
	store, err := NewStore(path, WithMaxRows(maxRows))
	if err != nil {
		return err
	}
	c.store = store
	c.ch = make(chan Record, c.bufSize)
	c.stop = make(chan struct{})
	c.done = make(chan struct{})
	c.started = true
	go c.writer()
	return nil
}

// writer is the single consumer. All store access happens here.
func (c *Capture) writer() {
	defer close(c.done)
	writes := 0
	for {
		select {
		case <-c.stop:
			c.drain()
			return
		case rec := <-c.ch:
			c.put(rec)
			writes++
			if writes%c.pruneN == 0 {
				c.prune()
			}
		}
	}
}

// drain flushes any buffered records, runs a final prune, and closes the store.
func (c *Capture) drain() {
	for {
		select {
		case rec := <-c.ch:
			c.put(rec)
		default:
			c.prune()
			if err := c.store.Close(); err != nil {
				c.log.Warn("failed to close embedding store", logger.Error(err))
			}
			return
		}
	}
}

// put persists a single record under a background context so no caller deadline
// can cancel it.
func (c *Capture) put(rec Record) {
	if err := c.store.Put(context.Background(), &rec); err != nil {
		c.log.Warn("failed to persist embedding", logger.Error(err))
		c.record(captureStatusErrorPut)
		return
	}
	c.record(captureStatusPersisted)
}

// prune enforces the rolling row cap and records how many rows were removed.
func (c *Capture) prune() {
	n, err := c.store.Prune(context.Background())
	if err != nil {
		c.log.Warn("failed to prune embedding store", logger.Error(err))
		return
	}
	if n > 0 && c.metrics != nil {
		c.metrics.RecordEmbeddingPrune(n)
	}
}

func (c *Capture) record(status string) {
	if c.metrics != nil {
		c.metrics.RecordEmbeddingCapture(status)
	}
}

// Close stops intake, flushes buffered writes, and closes the store. It honors
// ctx: if the deadline passes before the writer finishes draining, Close
// returns ctx.Err() while the writer completes its drain in the background.
// Close is idempotent and is a no-op when the store was never opened.
func (c *Capture) Close(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	if !c.started {
		c.mu.Unlock()
		return nil
	}
	stop, done := c.stop, c.done
	c.mu.Unlock()

	close(stop)
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
