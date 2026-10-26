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
	// privacyDiscardBuffer is the capacity of the discard channel, the queue between
	// flushes. A full channel drops the record and counts it.
	privacyDiscardBuffer = 1024
	// privacyDiscardRetention is how long discard rows are kept. If it ever becomes
	// a setting it must be read in the prune branch of every tick, not at start.
	privacyDiscardRetention = 30 * 24 * time.Hour
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

// DiscardStore persists the discard rows. The v2 PrivacyFilterDiscardRepository
// satisfies it.
type DiscardStore interface {
	Save(ctx context.Context, discard *entities.PrivacyFilterDiscard) error
	DeleteBefore(ctx context.Context, cutoffMillis int64) (int64, error)
}

// SourceResolver returns the audio_sources id of a source, creating the row the
// detection save path would create.
type SourceResolver func(ctx context.Context, sourceURI, nodeName string, displayName *string) (uint, error)

// Writer receives events without blocking, folds them in memory and writes
// additive deltas once a minute. A failed write drops its delta and is never
// retried, so a count is low rather than doubled. Discards queue in a bounded
// channel and are saved one row each in the same flush; a failed save drops that
// row and is never retried either.
type Writer struct {
	store        RowStore
	discardStore DiscardStore
	resolve      SourceResolver
	log          logger.Logger

	events   chan Event
	discards chan Discard
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once

	// acc is owned by the writer goroutine (or the caller of the flush helpers in tests).
	acc       accumulator
	lastPrune time.Time

	dropped    atomic.Int64 // events lost because the channel was full
	unresolved atomic.Int64 // events or rows lost because the source could not be resolved
	failed     atomic.Int64 // rows lost to a failed write

	discardsDropped atomic.Int64 // discards lost because the channel was full
	discardsFailed  atomic.Int64 // discards lost to a failed write

	reportedDropped, reportedUnresolved, reportedFailed int64
	reportedDiscardsDropped, reportedDiscardsFailed     int64
}

// NewWriter creates a Writer. A nil log uses the analysis logger.
func NewWriter(store RowStore, discards DiscardStore, resolve SourceResolver, log logger.Logger) *Writer {
	if log == nil {
		log = logger.Global().Module("analysis").Module("privacyrollup")
	}
	return &Writer{
		store:        store,
		discardStore: discards,
		resolve:      resolve,
		log:          log,
		events:       make(chan Event, privacyRollupEventBuffer),
		discards:     make(chan Discard, privacyDiscardBuffer),
		stop:         make(chan struct{}),
		done:         make(chan struct{}),
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

// RecordDiscard queues a discarded detection without blocking; a full buffer
// drops it. Safe on a nil Writer and after Stop (the channel is never closed).
// The writer goroutine does not read the channel between flushes, so the channel
// is the whole queue.
//
//nolint:gocritic // hugeParam: discards are rare, a copy is cheaper than an escaping pointer
func (w *Writer) RecordDiscard(d Discard) {
	if w == nil {
		return
	}
	select {
	case w.discards <- d:
	default:
		w.discardsDropped.Add(1)
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
	// be resolved, so it is tried once per flush. Rollup rows and discards share it.
	ids := make(map[sourceKey]uint)
	for key, d := range taken.rows {
		id := w.sourceID(ctx, ids, key.source, taken.display[key.source])
		if id == 0 {
			w.unresolved.Add(1)
			continue
		}
		if err := w.write(ctx, id, &key, d); err != nil {
			w.failed.Add(1)
		}
	}
	w.flushDiscards(ctx, ids)
	w.reportLosses()
}

// sourceID returns the audio_sources id of src from the per-flush cache ids,
// resolving and caching it on first use; 0 means the source could not be resolved.
func (w *Writer) sourceID(ctx context.Context, ids map[sourceKey]uint, src sourceKey, display string) uint {
	id, seen := ids[src]
	if !seen {
		id = w.resolveSource(ctx, src, display)
		ids[src] = id
	}
	return id
}

// flushDiscards saves the discards queued when the flush started, one
// transaction each. After the first failed save the rest of that batch is counted
// failed without being resolved or written, so a locked database costs at most
// one write timeout per flush here. A discard whose source cannot be resolved is
// stored with a NULL source, as the detection save path does.
func (w *Writer) flushDiscards(ctx context.Context, ids map[sourceKey]uint) {
	stalled := false
	for range len(w.discards) {
		d := <-w.discards
		if stalled {
			w.discardsFailed.Add(1)
			continue
		}
		var sourceID *uint
		if d.SourceURI != "" {
			if id := w.sourceID(ctx, ids, sourceKey{d.SourceURI, d.NodeName}, d.DisplayName); id != 0 {
				sourceID = &id
			}
		}
		if err := w.saveDiscard(ctx, discardRow(&d, sourceID)); err != nil {
			w.discardsFailed.Add(1)
			stalled = true
		}
	}
}

func (w *Writer) saveDiscard(ctx context.Context, row *entities.PrivacyFilterDiscard) error {
	wctx, cancel := context.WithTimeout(ctx, privacyRollupWriteTimeout)
	defer cancel()
	return w.discardStore.Save(wctx, row)
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
	discardsDropped, discardsFailed := w.discardsDropped.Load(), w.discardsFailed.Load()
	if dropped == w.reportedDropped && unresolved == w.reportedUnresolved && failed == w.reportedFailed &&
		discardsDropped == w.reportedDiscardsDropped && discardsFailed == w.reportedDiscardsFailed {
		return
	}
	w.log.Warn("privacy filter activity lost some records",
		logger.Int64("dropped_new", dropped-w.reportedDropped),
		logger.Int64("unresolved_new", unresolved-w.reportedUnresolved),
		logger.Int64("failed_new", failed-w.reportedFailed),
		logger.Int64("discards_dropped_new", discardsDropped-w.reportedDiscardsDropped),
		logger.Int64("discards_failed_new", discardsFailed-w.reportedDiscardsFailed),
		logger.Int64("dropped_total", dropped),
		logger.Int64("unresolved_total", unresolved),
		logger.Int64("failed_total", failed),
		logger.Int64("discards_dropped_total", discardsDropped),
		logger.Int64("discards_failed_total", discardsFailed),
		logger.String("operation", "privacy_rollup_flush"))
	w.reportedDropped, w.reportedUnresolved, w.reportedFailed = dropped, unresolved, failed
	w.reportedDiscardsDropped, w.reportedDiscardsFailed = discardsDropped, discardsFailed
}

// maybePrune deletes expired rows on the first tick and then once an hour. The
// cutoffs are the wall clock, not truncated to the hour, so an expired row does
// not linger. Both prunes run every time; a failure of either leaves lastPrune
// alone, so the next tick retries both (a repeated successful delete is harmless).
func (w *Writer) maybePrune(ctx context.Context, now time.Time) {
	if !w.lastPrune.IsZero() && now.Sub(w.lastPrune) < privacyRollupPruneInterval {
		return
	}
	rollupOK := w.prune(ctx, "rollup", func(pctx context.Context) (int64, error) {
		return w.store.DeleteBefore(pctx, now.Add(-privacyRollupRetention).Unix())
	})
	discardsOK := w.prune(ctx, "discard", func(pctx context.Context) (int64, error) {
		return w.discardStore.DeleteBefore(pctx, now.Add(-privacyDiscardRetention).UnixMilli())
	})
	if rollupOK && discardsOK {
		w.lastPrune = now
	}
}

// prune runs one delete under the write timeout and logs its outcome; it reports
// whether the delete succeeded. kind names the table group in the log.
func (w *Writer) prune(ctx context.Context, kind string, del func(context.Context) (int64, error)) bool {
	pctx, cancel := context.WithTimeout(ctx, privacyRollupWriteTimeout)
	defer cancel()
	deleted, err := del(pctx)
	if err != nil {
		w.log.Warn("privacy filter prune failed",
			logger.String("kind", kind),
			logger.Error(err),
			logger.String("operation", "privacy_rollup_prune"))
		return false
	}
	if deleted > 0 {
		w.log.Debug("privacy filter pruned old rows",
			logger.String("kind", kind),
			logger.Int64("deleted", deleted),
			logger.String("operation", "privacy_rollup_prune"))
	}
	return true
}
