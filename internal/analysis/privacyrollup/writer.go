package privacyrollup

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// privacyRollupFlushInterval is how often the writer folds its deltas into the database.
	privacyRollupFlushInterval = time.Minute
	// privacyRollupEventBuffer is the capacity of the event channel. It fills only
	// while a flush is stalled; further events are counted as dropped.
	privacyRollupEventBuffer = 4096
	// privacyRollupRetention is how long hourly rows are kept. If it ever becomes
	// a setting it must be read in the prune branch of every tick, not at start.
	privacyRollupRetention = 30 * 24 * time.Hour
	// privacyRollupPruneInterval is the time between prunes.
	privacyRollupPruneInterval = time.Hour
	// privacyRollupWriteTimeout bounds one resolve, upsert or prune.
	privacyRollupWriteTimeout = 10 * time.Second
	// privacyRollupShutdownTimeout bounds the final flush on Stop.
	privacyRollupShutdownTimeout = 5 * time.Second
)

// RowStore persists the rollup rows. The v2 PrivacyFilterHourlyRepository
// satisfies it.
type RowStore interface {
	AddDelta(ctx context.Context, row *entities.PrivacyFilterHourly) error
	DeleteBefore(ctx context.Context, cutoffHourStart int64) (int64, error)
}

// SourceResolver returns the audio_sources id of a source, creating the row the
// detection save path would create.
type SourceResolver func(ctx context.Context, sourceURI, nodeName string, displayName *string) (uint, error)

// Writer receives events without blocking, folds them in memory and writes
// additive deltas once a minute. A failed write drops its delta and is never
// retried, so a count is low rather than doubled.
type Writer struct {
	store   RowStore
	resolve SourceResolver
	log     logger.Logger

	events   chan Event
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once

	// acc is owned by the writer goroutine (or the caller of the flush helpers in tests).
	acc       accumulator
	lastPrune time.Time

	dropped    atomic.Int64 // events lost because the channel was full
	unresolved atomic.Int64 // events or rows lost because the source could not be resolved
	failed     atomic.Int64 // rows lost to a failed write

	reportedDropped, reportedUnresolved, reportedFailed int64
}

// NewWriter creates a Writer. A nil log uses the analysis logger.
func NewWriter(store RowStore, resolve SourceResolver, log logger.Logger) *Writer {
	if log == nil {
		log = logger.Global().Module("analysis").Module("privacyrollup")
	}
	return &Writer{
		store:   store,
		resolve: resolve,
		log:     log,
		events:  make(chan Event, privacyRollupEventBuffer),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// Record queues an event without blocking; a full buffer drops it. Safe on a nil
// Writer and after Stop (the channel is never closed).
//
//nolint:gocritic // hugeParam: events are small and rare, a copy is cheaper than an escaping pointer
func (w *Writer) Record(ev Event) {
	if w == nil {
		return
	}
	select {
	case w.events <- ev:
	default:
		w.dropped.Add(1)
	}
}

// Start launches the writer goroutine.
func (w *Writer) Start() {
	go w.run()
}

// Stop ends the goroutine after a final flush of what is queued, waiting for it
// until ctx ends. Calling it again is harmless.
func (w *Writer) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	w.stopOnce.Do(func() { close(w.stop) })
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Writer) run() {
	defer close(w.done)
	defer func() {
		if r := recover(); r != nil {
			w.log.Error("privacy rollup writer panicked", logger.Any("panic", r))
		}
	}()
	ticker := time.NewTicker(privacyRollupFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case ev := <-w.events:
			w.fold(ev)
		case now := <-ticker.C:
			w.tick(context.Background(), now)
		case <-w.stop:
			w.finalFlush()
			return
		}
	}
}

// finalFlush folds what is already queued and writes it, bounded by the shutdown timeout.
func (w *Writer) finalFlush() {
	w.drainQueued()
	ctx, cancel := context.WithTimeout(context.Background(), privacyRollupShutdownTimeout)
	defer cancel()
	w.flush(ctx)
}

// fold adds one event to the accumulator, dropping events without a source.
//
//nolint:gocritic // hugeParam: events are small and rare, a copy is cheaper than an escaping pointer
func (w *Writer) fold(ev Event) {
	if ev.SourceURI == "" {
		w.unresolved.Add(1)
		return
	}
	w.acc.add(ev)
}

// drainQueued folds what is already queued, without waiting for more.
func (w *Writer) drainQueued() {
	for {
		select {
		case ev := <-w.events:
			w.fold(ev)
		default:
			return
		}
	}
}

// tick is one flush interval: write the deltas, then prune when due.
func (w *Writer) tick(ctx context.Context, now time.Time) {
	w.flush(ctx)
	w.maybePrune(ctx, now)
}

// flush writes the accumulated deltas. The accumulator is replaced before the
// writes, and nothing is merged back after a failure.
func (w *Writer) flush(ctx context.Context) {
	taken := w.acc
	w.acc = accumulator{}

	// ids holds the audio_sources id of each source; 0 marks one that could not
	// be resolved, so it is tried once per flush.
	ids := make(map[sourceKey]uint)
	for key, d := range taken.rows {
		id, seen := ids[key.source]
		if !seen {
			id = w.resolveSource(ctx, key.source, taken.display[key.source])
			ids[key.source] = id
		}
		if id == 0 {
			w.unresolved.Add(1)
			continue
		}
		if err := w.write(ctx, id, &key, d); err != nil {
			w.failed.Add(1)
		}
	}
	w.reportLosses()
}

// resolveSource returns the audio_sources id of src, or 0 when it cannot be
// resolved.
func (w *Writer) resolveSource(ctx context.Context, src sourceKey, display string) uint {
	rctx, cancel := context.WithTimeout(ctx, privacyRollupWriteTimeout)
	defer cancel()
	var name *string
	if display != "" {
		name = &display
	}
	id, err := w.resolve(rctx, src.uri, src.node, name)
	if err != nil {
		return 0
	}
	return id
}

func (w *Writer) write(ctx context.Context, sourceID uint, key *rowKey, d *counters) error {
	wctx, cancel := context.WithTimeout(ctx, privacyRollupWriteTimeout)
	defer cancel()
	return w.store.AddDelta(wctx, &entities.PrivacyFilterHourly{
		HourStart: key.hourStart, SourceID: sourceID, ModelID: key.modelID,
		TriggerType: key.trigger, ThresholdMilli: key.thresholdMilli,
		Hits: d.hits, Held: d.held, Ignored: d.ignored, Confirmed: d.confirmed,
		Discards: d.discards, UnscannedSaves: d.unscanned, MaxScore: d.maxScore,
		MarginLt50: d.lt50, MarginLt100: d.lt100, MarginLt200: d.lt200, MarginGe200: d.ge200,
	})
}

// reportLosses logs one Warn with counts only when any loss counter rose since
// the last report. It never names sources or rows.
func (w *Writer) reportLosses() {
	dropped, unresolved, failed := w.dropped.Load(), w.unresolved.Load(), w.failed.Load()
	if dropped == w.reportedDropped && unresolved == w.reportedUnresolved && failed == w.reportedFailed {
		return
	}
	w.log.Warn("privacy filter rollup lost some counts",
		logger.Int64("dropped_new", dropped-w.reportedDropped),
		logger.Int64("unresolved_new", unresolved-w.reportedUnresolved),
		logger.Int64("failed_new", failed-w.reportedFailed),
		logger.Int64("dropped_total", dropped),
		logger.Int64("unresolved_total", unresolved),
		logger.Int64("failed_total", failed),
		logger.String("operation", "privacy_rollup_flush"))
	w.reportedDropped, w.reportedUnresolved, w.reportedFailed = dropped, unresolved, failed
}

// maybePrune deletes expired rows on the first tick and then once an hour. The
// cutoff is the wall clock, not truncated to the hour, so an expired row does
// not linger. A failed prune leaves lastPrune alone, so the next tick retries.
func (w *Writer) maybePrune(ctx context.Context, now time.Time) {
	if !w.lastPrune.IsZero() && now.Sub(w.lastPrune) < privacyRollupPruneInterval {
		return
	}
	pctx, cancel := context.WithTimeout(ctx, privacyRollupWriteTimeout)
	defer cancel()
	deleted, err := w.store.DeleteBefore(pctx, now.Add(-privacyRollupRetention).Unix())
	if err != nil {
		w.log.Warn("privacy filter rollup prune failed",
			logger.Error(err),
			logger.String("operation", "privacy_rollup_prune"))
		return
	}
	w.lastPrune = now
	if deleted > 0 {
		w.log.Debug("privacy filter rollup pruned old rows",
			logger.Int64("deleted", deleted),
			logger.String("operation", "privacy_rollup_prune"))
	}
}
