// router.go - AudioRouter with non-blocking fan-out dispatch.
package audiocore

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	dspeq "github.com/tphakala/go-audio-dsp/equalizer"
	dspgain "github.com/tphakala/go-audio-dsp/gain"
	dsppcm "github.com/tphakala/go-audio-dsp/pcm"

	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/audiocore/resample"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// RouteInboxCapacity is the per-route buffered channel size.
	// A slow consumer drops frames after this many are queued.
	RouteInboxCapacity = 64

	// dropLogInterval controls how often drop warnings are emitted.
	// One log line per this many consecutive drops, per route.
	dropLogInterval = 100

	// errorLogInterval controls how often consumer write errors are logged.
	errorLogInterval = 100

	// realignmentLogInterval controls how often the router reports that it
	// started carrying a partial PCM16 sample between frames. It counts
	// carrying runs, not frames: realignment is normal and self correcting, so
	// errorLogInterval is reserved for genuine failures.
	realignmentLogInterval = 100

	// dropSentryThreshold is the total number of drops per route before
	// a single Sentry report is fired. This fires once; the log.Warn at
	// dropLogInterval provides ongoing visibility.
	dropSentryThreshold int64 = 1000

	// bitDepthPCM16 is the only bit depth the router's alignment, EQ and gain
	// paths interpret; frames with other depths skip those stages untouched.
	// The resampler is the exception: it assumes PCM16 by contract regardless
	// of the frame's declared bit depth, so a resampling route fed another
	// depth rejects odd-length frames loudly and garbles the rest. Nothing
	// gates that pairing, because AddRoute derives the resampler from a rate
	// mismatch alone.
	//
	// It does not bite today because bit depth is not a user setting: there is
	// no conf field for it, and the paths that build a source pass
	// conf.BitDepth, which is 16. The engine forwards whatever it is given,
	// substituting the default only for a non-positive value, so it does not
	// normalise a wrong value either. A non-16 source would therefore reach
	// FFmpeg's output format selection, this gate, and the resampler at once.
	bitDepthPCM16 = 16

	// bytesPerPCM16Sample is the size of one 16-bit PCM sample. Frames handed
	// to the resampler or to applyProcessing must hold a whole multiple of it.
	bytesPerPCM16Sample = 2
)

// drainerStopTimeout is the maximum time to wait for a drainer goroutine
// to exit after its done channel has been closed.
const drainerStopTimeout = 5 * time.Second

// Route holds the state for a single consumer subscription.
type Route struct {
	// SourceID is the audio source this route is subscribed to.
	SourceID string

	// Consumer is the downstream receiver of audio frames.
	Consumer AudioConsumer

	// sourceSampleRate is the sample rate of the source producing frames.
	sourceSampleRate int

	// gain scales the route's audio by a number of decibels. Nil means no gain
	// (0 dB) and skips the gain stage. Accessed atomically so UpdateGain can swap
	// it without rebuilding the route; the drainer loads it once per frame. A
	// Gain holds only its immutable factor, so routes may share one instance.
	// It is float32 inside (go-audio-dsp derives the linear factor with
	// dsp.FactorFromDB), so a gain-only route differs from a float64 math.Pow
	// factor by at most one LSB on a small fraction of samples.
	gain atomic.Pointer[dspgain.Gain]

	// resampler converts source-rate PCM to the consumer's expected rate.
	// Nil when no rate conversion is required.
	resampler *resample.Resampler

	// filterChain holds the current EQ filter chain for this route.
	// Accessed atomically so it can be swapped without rebuilding the route.
	// Nil means no EQ filtering is applied.
	filterChain atomic.Pointer[dspeq.FilterChain]

	// inbox is the bounded channel between Dispatch and the drainer goroutine.
	inbox chan AudioFrame

	// drops counts frames that could not be enqueued because inbox was full.
	drops atomic.Int64

	// errors counts all processing failures (resampler + Write) for this route.
	errors atomic.Int64

	// writeErrors counts only Consumer.Write failures, used for the
	// sustained-write-errors Sentry threshold separately from resampler errors.
	writeErrors atomic.Int64

	// realignments counts the times a partial trailing PCM16 sample was carried
	// out of a frame on this route. It is not a per-odd-frame count: a frame
	// that consumes a pending carry and ends on a sample boundary realigns the
	// stream without incrementing it. Kept separate from errors so
	// RouteInfo.Errors retains its "real error" semantics for operators; an
	// upstream that splits samples across reads is not an error, it does not
	// inflate that metric.
	realignments atomic.Int64

	// carry holds the bytes of a PCM16 sample that the previous frame ended
	// part-way through, and carryLen is how many of them are valid (0 or 1).
	// alignBuf is the scratch buffer the carry is joined with the next frame
	// in. All three are touched only by this route's drainer goroutine, via
	// alignPCM16, so they need no synchronisation.
	//
	// Known limitation: the carry is not cleared at a stream discontinuity. A
	// Route outlives an FFmpeg process restart, so a producer that ends one
	// stream generation mid-sample would have that byte prepended to the first
	// frame of the next. Clearing it correctly needs a marker travelling in
	// band with the frames, because an out-of-band signal is consumed by
	// whichever frame the drainer happens to dequeue next, which is still an
	// old-stream frame while the inbox is draining. No in-tree producer can
	// reach this: readStdout reassembles whole frames and its own carry is
	// goroutine-local, so it resets per process, and the malgo path emits whole
	// samples by construction.
	carry    [bytesPerPCM16Sample - 1]byte
	carryLen int
	alignBuf []byte

	// realignmentRuns counts how many times this route entered a carrying run,
	// i.e. held a partial sample when it previously held none. Realignment
	// latches: once a run starts, an even-length producer keeps re-arming the
	// carry on every frame, so realignments grows per frame while this grows
	// once per episode. Logging is gated on this so a latched route reports the
	// transition instead of repeating itself forever.
	realignmentRuns atomic.Int64

	// done is closed to signal the drainer goroutine to exit.
	done chan struct{}

	// stopped is closed by the drainer goroutine when it exits. Callers
	// must wait on this channel after closing done before touching the
	// resampler or consumer to avoid a data race.
	stopped chan struct{}

	// Pool caches: set lazily on first frame to avoid per-frame mutex
	// acquisition in BytePoolFor/Float32PoolFor. The cached pool is used
	// when the current frame size matches; on mismatch the fallback path
	// calls the mutex-guarded lookup.
	resamplePool    atomic.Pointer[buffer.BytePool]
	resamplePoolLen atomic.Int64
	float32Pool     atomic.Pointer[buffer.Float32Pool]
	float32PoolLen  atomic.Int64
	byteOutPool     atomic.Pointer[buffer.BytePool]
	byteOutPoolLen  atomic.Int64

	// stats tracks per-frame timing for diagnostics. Zero-value is ready to use.
	stats routeStats
}

// RouteInfo is the read-only snapshot returned by Routes().
type RouteInfo struct {
	// SourceID is the audio source this route belongs to.
	SourceID string

	// ConsumerID is the unique identifier of the consumer.
	ConsumerID string

	// Drops is the total number of frames dropped for this route.
	Drops int64

	// Errors is the total number of processing failures for this route,
	// counting resampler, conversion and Consumer.Write failures alike.
	Errors int64

	// QueueDepth is the current occupancy of the route inbox (len of the bounded channel).
	QueueDepth int
}

// AudioRouter dispatches audio frames to registered consumers using
// per-route goroutines and bounded channels. It implements AudioDispatcher.
type AudioRouter struct {
	// mu guards the routes map.
	mu sync.RWMutex

	// routes maps source ID -> slice of active routes.
	routes map[string][]*Route

	// log is the router's logger.
	log logger.Logger

	// bufMgr is the shared buffer manager used to obtain per-size pools on the
	// hot path. When nil (legacy constructions / test mockery), the router falls
	// back to plain make() allocations.
	bufMgr *buffer.Manager

	// ctx and cancel control the lifetime of all drainer goroutines.
	ctx    context.Context
	cancel context.CancelFunc

	// lastDispatch stores the most recent Dispatch timestamp per source ID.
	// Key: string (sourceID), Value: *atomic.Int64 (UnixNano).
	lastDispatch sync.Map
}

// NewAudioRouter creates an AudioRouter ready to accept routes and dispatch
// frames. bufMgr is the shared buffer.Manager used to obtain per-size pools on
// the hot path; when nil (legacy constructions or test mockery), the router
// falls back to plain make() allocations.
func NewAudioRouter(log logger.Logger, bufMgr *buffer.Manager) *AudioRouter {
	ctx, cancel := context.WithCancel(context.Background())
	return &AudioRouter{
		routes: make(map[string][]*Route),
		log:    log.With(logger.String("component", "audio_router")),
		bufMgr: bufMgr,
		ctx:    ctx,
		cancel: cancel,
	}
}

// AddRoute registers a consumer for the given source. A per-route drainer
// goroutine is started immediately. Returns ErrRouteExists if a consumer with
// the same ID is already registered for this source. When sourceSampleRate
// differs from the consumer's SampleRate, a resampler is created automatically
// so the consumer receives frames at its expected rate.
//
// Consumer IDs must be globally unique across all sources. The router uses
// the consumer ID for logging, metrics, and route lookup. Duplicate IDs on
// different sources will not cause an error but may produce confusing log
// output and make RemoveRoute ambiguous.
//
// gainDB is the route's initial gain (UpdateGain changes it later without
// rebuilding the route). It is applied by go-audio-dsp in float32; 0 dB adds no
// gain stage. AddRoute returns an error for a NaN, an infinity, or a gain whose
// linear factor overflows float32 (above about +770 dB). The settings layer limits gain to
// [-40, 40] dB, so the overflow case is unreachable from configuration.
func (r *AudioRouter) AddRoute(sourceID string, consumer AudioConsumer, sourceSampleRate int, gainDB float64, filterChain *dspeq.FilterChain) error {
	// Reject routes after the router has been closed.
	if r.ctx.Err() != nil {
		return fmt.Errorf("router is closed: %w", r.ctx.Err())
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Check for duplicate consumer ID on this source.
	for _, rt := range r.routes[sourceID] {
		if rt.Consumer.ID() != consumer.ID() {
			continue
		}
		return fmt.Errorf("%w: source=%s consumer=%s", ErrRouteExists, sourceID, consumer.ID())
	}

	routeGain, err := newRouteGain(gainDB)
	if err != nil {
		return fmt.Errorf("invalid gain value for source=%s consumer=%s: %w", sourceID, consumer.ID(), err)
	}

	route := &Route{
		SourceID:         sourceID,
		Consumer:         consumer,
		sourceSampleRate: sourceSampleRate,
		inbox:            make(chan AudioFrame, RouteInboxCapacity),
		done:             make(chan struct{}),
		stopped:          make(chan struct{}),
	}

	route.gain.Store(routeGain)
	route.filterChain.Store(filterChain)

	// Create a resampler when the source and consumer rates differ.
	if sourceSampleRate != consumer.SampleRate() {
		rs, err := resample.NewResampler(sourceSampleRate, consumer.SampleRate())
		if err != nil {
			return fmt.Errorf("create resampler for source=%s consumer=%s: %w", sourceID, consumer.ID(), err)
		}
		route.resampler = rs
	}

	r.routes[sourceID] = append(r.routes[sourceID], route)

	go r.drainRoute(route)

	r.log.Info("route added",
		logger.String("source_id", sourceID),
		logger.String("consumer_id", consumer.ID()))

	return nil
}

// RemoveRoute removes the route for the given consumer on the specified source.
// The consumer's Close method is called and the drainer goroutine is stopped.
// If the route does not exist, RemoveRoute is a no-op.
func (r *AudioRouter) RemoveRoute(sourceID, consumerID string) {
	r.mu.Lock()

	routes := r.routes[sourceID]
	var removed *Route
	for i, rt := range routes {
		if rt.Consumer.ID() != consumerID {
			continue
		}

		removed = rt
		// Remove from slice without preserving order.
		routes[i] = routes[len(routes)-1]
		routes[len(routes)-1] = nil // avoid memory leak
		r.routes[sourceID] = routes[:len(routes)-1]

		if len(r.routes[sourceID]) == 0 {
			delete(r.routes, sourceID)
		}
		break
	}

	r.mu.Unlock()

	if removed != nil {
		r.stopRoute(removed)
		r.log.Info("route removed",
			logger.String("source_id", sourceID),
			logger.String("consumer_id", consumerID))
	}
}

// RemoveAllRoutes removes every route for the given source, closing all
// consumers and stopping their drainer goroutines.
func (r *AudioRouter) RemoveAllRoutes(sourceID string) {
	r.mu.Lock()
	routes := r.routes[sourceID]
	delete(r.routes, sourceID)
	r.mu.Unlock()

	for _, rt := range routes {
		r.stopRoute(rt)
	}

	r.ClearDispatchTime(sourceID)

	if len(routes) > 0 {
		r.log.Info("all routes removed",
			logger.String("source_id", sourceID),
			logger.Int("count", len(routes)))
	}
}

// FilterChainBuilder creates a FilterChain for a given sample rate.
// Used by UpdateFilterChain to build per-route chains with correct
// coefficients and independent biquad state (avoiding data races).
type FilterChainBuilder func(sampleRate int) *dspeq.FilterChain

// UpdateFilterChain atomically replaces the EQ filter chain for every route
// on the given source. The builder is called once per route with the route's
// consumer sample rate, producing a fresh chain with independent biquad state.
// The drainer goroutine picks up the new chain on the next frame.
func (r *AudioRouter) UpdateFilterChain(sourceID string, build FilterChainBuilder) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	routes := r.routes[sourceID]
	for _, rt := range routes {
		chain := build(rt.Consumer.SampleRate())
		rt.filterChain.Store(chain)
		filterCount := 0
		if chain != nil {
			filterCount = chain.Len()
		}
		r.log.Debug("EQ filter chain updated",
			logger.String("source_id", sourceID),
			logger.String("consumer_id", rt.Consumer.ID()),
			logger.Int("active_filters", filterCount))
	}
	if len(routes) == 0 {
		r.log.Debug("EQ filter chain update: no routes found for source",
			logger.String("source_id", sourceID))
	}
}

// newRouteGain returns the gain stage for gainDB, or nil for 0 dB, which needs
// no stage. Any other value must be accepted by go-audio-dsp, which rejects NaN,
// infinities and a gain whose linear factor overflows float32.
func newRouteGain(gainDB float64) (*dspgain.Gain, error) {
	if gainDB == 0 {
		return nil, nil //nolint:nilnil // intentional: nil means "no gain stage" (0 dB)
	}
	return dspgain.New(gainDB)
}

// UpdateGain sets the gain of every route on sourceID to gainDB from each
// route's next frame, without rebuilding the routes, so consumers (live-audio
// listeners included) stay open. 0 dB removes the gain stage. The change is a
// step at a frame boundary with no ramp; a frame in flight finishes with the
// gain it started with. It returns an error and changes no route for a NaN, an
// infinity or a gain whose linear factor overflows float32. An unknown source is
// a no-op.
func (r *AudioRouter) UpdateGain(sourceID string, gainDB float64) error {
	// Build and validate the gain before taking the lock, so a rejected value is
	// an error whether or not the source has routes. A Gain holds only its
	// immutable factor, so one instance serves every route's drainer.
	g, err := newRouteGain(gainDB)
	if err != nil {
		return fmt.Errorf("invalid gain value for source=%s: %w", sourceID, err)
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	routes := r.routes[sourceID]
	for _, rt := range routes {
		rt.gain.Store(g)
		r.log.Debug("route gain updated",
			logger.String("source_id", sourceID),
			logger.String("consumer_id", rt.Consumer.ID()),
			logger.Float64("gain_db", gainDB))
	}
	if len(routes) == 0 {
		r.log.Debug("gain update: no routes found for source",
			logger.String("source_id", sourceID))
	}
	return nil
}

// Dispatch sends a frame to every consumer registered for the frame's source.
// If a consumer's inbox is full, the frame is dropped and the route's drop
// counter is incremented. Dispatch never blocks.
//
// The read lock is held for the entire iteration to prevent RemoveRoute from
// mutating the slice (swap-remove) while Dispatch is reading it. This is safe
// because the loop body only performs non-blocking channel sends and atomic
// operations, so the lock cannot cause deadlocks.
//
// Note: because Dispatch takes a read lock while RemoveRoute takes a write
// lock, a small number of in-flight frames may be lost during route removal.
// This is expected behaviour and not a bug.
//
// Pool ownership: when frame.Ref is non-nil, Dispatch calls Retain once per
// successful inbox enqueue. Each drainer calls Release after Consumer.Write
// returns (via a defer in handleRouteFrame). The producer retains one
// reference at frame creation and is responsible for calling Release once
// after Dispatch returns, so the pool slice is released exactly when the
// last holder is done.
//
// Ordering invariant: Retain MUST run before the non-blocking send. If it
// ran after a successful enqueue, the drainer could dequeue, Write, and
// Release before the retain lands, firing the release closure while the
// frame is still in flight. Do not "optimise" by moving Retain inside the
// success arm of the select.
func (r *AudioRouter) Dispatch(frame AudioFrame) { //nolint:gocritic // hugeParam: signature required by AudioDispatcher interface
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Record frame arrival for liveness monitoring.
	r.getOrCreateDispatchTimestamp(frame.SourceID).Store(time.Now().UnixNano())

	for _, rt := range r.routes[frame.SourceID] {
		// Retain BEFORE attempting the send so the drainer cannot observe a
		// stale zero count and release the slice prematurely. If the send
		// fails (inbox full), undo the retain so the drop path is balanced.
		frame.Ref.Retain()
		select {
		case rt.inbox <- frame:
			// Frame enqueued; drainer will Release after Write.
		default:
			frame.Ref.Release() // undo the retain we just performed
			drops := rt.drops.Add(1)
			if drops%dropLogInterval == 1 {
				r.log.Warn("frames dropped for consumer",
					logger.String("source_id", frame.SourceID),
					logger.String("consumer_id", rt.Consumer.ID()),
					logger.Int64("total_drops", drops),
					logger.Int("inbox_len", len(rt.inbox)),
					logger.Int("inbox_cap", cap(rt.inbox)))
			}
			if drops == dropSentryThreshold {
				avgFrameUs := int64(0)
				maxFrameUs := int64(0)
				totalFrames := rt.stats.frames.Load()
				if totalFrames > 0 {
					avgFrameUs = rt.stats.lifetimeTotalNs.Load() / totalFrames / 1000
					maxFrameUs = rt.stats.lifetimeMaxNs.Load() / 1000
				}
				_ = errors.Newf("consumer dropped %d frames, likely cannot keep up", drops).
					Component("audiocore.router").
					Category(errors.CategoryAudio).
					Context("operation", "sustained_frame_drops").
					Context("source_id", frame.SourceID).
					Context("source_type", sourceType(frame.SourceID)).
					Context("consumer_id", rt.Consumer.ID()).
					Context("consumer_type", consumerType(rt.Consumer.ID())).
					Context("sample_rate", rt.Consumer.SampleRate()).
					Context("source_sample_rate", rt.sourceSampleRate).
					Context("inbox_len", len(rt.inbox)).
					Context("inbox_cap", cap(rt.inbox)).
					Context("avg_frame_us", avgFrameUs).
					Context("max_frame_us", maxFrameUs).
					Context("total_frames_processed", totalFrames).
					Build()
			}
		}
	}
}

// HasConsumers reports whether at least one consumer is registered for the
// given source.
func (r *AudioRouter) HasConsumers(sourceID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.routes[sourceID]) > 0
}

// Routes returns read-only information about all routes for the given source.
func (r *AudioRouter) Routes(sourceID string) []RouteInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	routes := r.routes[sourceID]
	infos := make([]RouteInfo, 0, len(routes))
	for _, rt := range routes {
		infos = append(infos, RouteInfo{
			SourceID:   rt.SourceID,
			ConsumerID: rt.Consumer.ID(),
			Drops:      rt.drops.Load(),
			Errors:     rt.errors.Load(),
			QueueDepth: len(rt.inbox),
		})
	}
	return infos
}

// LastDispatchTime returns the last time Dispatch was called for sourceID.
// Returns zero time if the source has never dispatched.
func (r *AudioRouter) LastDispatchTime(sourceID string) time.Time {
	if v, ok := r.lastDispatch.Load(sourceID); ok {
		nanos := v.(*atomic.Int64).Load()
		if nanos > 0 {
			return time.Unix(0, nanos)
		}
	}
	return time.Time{}
}

// ResetDispatchTime sets the last dispatch time for sourceID to now.
// Used when quiet hours end to avoid false alarms from stale timestamps.
func (r *AudioRouter) ResetDispatchTime(sourceID string) {
	ts := r.getOrCreateDispatchTimestamp(sourceID)
	ts.Store(time.Now().UnixNano())
}

// ClearDispatchTime removes the dispatch timestamp for sourceID.
// Called when a source is permanently removed.
func (r *AudioRouter) ClearDispatchTime(sourceID string) {
	r.lastDispatch.Delete(sourceID)
}

// ActiveSourceIDs returns the source IDs that have active routes.
func (r *AudioRouter) ActiveSourceIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return slices.Collect(maps.Keys(r.routes))
}

func (r *AudioRouter) getOrCreateDispatchTimestamp(sourceID string) *atomic.Int64 {
	if v, ok := r.lastDispatch.Load(sourceID); ok {
		return v.(*atomic.Int64)
	}
	ts := &atomic.Int64{}
	actual, _ := r.lastDispatch.LoadOrStore(sourceID, ts)
	return actual.(*atomic.Int64)
}

// Close stops all drainer goroutines and closes every registered consumer.
// It is safe to call Close multiple times: context.CancelFunc is idempotent,
// and the map is atomically swapped under the lock so subsequent calls see an
// empty map and close no done channels.
func (r *AudioRouter) Close() {
	// Clear the routes map BEFORE cancelling the context. Any concurrent
	// Dispatch holding the RLock completes its Retain+enqueue before Close
	// can acquire the write lock (RLock blocks the Lock). Once Close owns
	// the Lock and clears the map, any later Dispatch sees an empty map
	// and performs no Retain. Only then do we cancel the context; drainers
	// wake up, drain their inbox via drainInboxRefs, and exit with refs
	// balanced. The previous cancel-then-lock order allowed a drainer to
	// exit on ctx.Done while an in-flight Dispatch still held the old route
	// slice and enqueued a retain onto the dead drainer's inbox.
	r.mu.Lock()
	allRoutes := r.routes
	r.routes = make(map[string][]*Route)
	r.mu.Unlock()

	r.cancel()

	for _, routes := range allRoutes {
		for _, rt := range routes {
			r.stopRoute(rt)
		}
	}
}

// stopRoute signals the drainer goroutine to exit, waits for it to finish
// (with a timeout), and then closes the resampler and consumer. This ordering
// ensures the drainer is no longer using the resampler or consumer before they
// are closed.
func (r *AudioRouter) stopRoute(route *Route) {
	close(route.done)

	select {
	case <-route.stopped:
		// Drainer exited cleanly or via recovered panic. Close resources
		// with panic guards so a misbehaving Close() cannot crash the
		// caller (RemoveRoute, RemoveAllRoutes, or router.Close).
		if route.resampler != nil {
			func() {
				defer func() {
					if rp := recover(); rp != nil {
						r.log.Warn("panic closing resampler in stopRoute",
							logger.String("source_id", route.SourceID),
							logger.String("consumer_id", route.Consumer.ID()),
							logger.Any("panic", rp))
					}
				}()
				if err := route.resampler.Close(); err != nil {
					r.log.Debug("resampler close error",
						logger.String("source_id", route.SourceID),
						logger.String("consumer_id", route.Consumer.ID()),
						logger.Error(err))
				}
			}()
		}
		func() {
			defer func() {
				if rp := recover(); rp != nil {
					r.log.Warn("panic closing consumer in stopRoute",
						logger.String("source_id", route.SourceID),
						logger.String("consumer_id", route.Consumer.ID()),
						logger.Any("panic", rp))
				}
			}()
			if err := route.Consumer.Close(); err != nil {
				r.log.Debug("consumer close error",
					logger.String("source_id", route.SourceID),
					logger.String("consumer_id", route.Consumer.ID()),
					logger.Error(err))
			}
		}()
	case <-time.After(drainerStopTimeout):
		// Drainer is leaked and may still reference the resampler/consumer.
		// Do NOT close them, leave for GC to reclaim.
		r.log.Warn("drainer goroutine did not exit in time, leaking resources",
			logger.String("source_id", route.SourceID),
			logger.String("consumer_id", route.Consumer.ID()))
		_ = errors.Newf("drainer goroutine leaked after %s timeout", drainerStopTimeout).
			Component("audiocore.router").
			Category(errors.CategoryResource).
			Context("operation", "drainer_goroutine_leaked").
			Context("source_id", route.SourceID).
			Context("consumer_id", route.Consumer.ID()).
			Build()
	}
}

// drainRoute reads frames from the route's inbox and delivers them to the
// consumer. When the route has a resampler, the frame data is converted to
// the consumer's sample rate before delivery. It exits when the route's done
// channel is closed or the router's context is cancelled.
func (r *AudioRouter) drainRoute(route *Route) {
	defer close(route.stopped)
	defer func() {
		if p := recover(); p != nil {
			var panicErr error
			if asErr, ok := p.(error); ok {
				panicErr = fmt.Errorf("panic in drainer goroutine: %w", asErr)
			} else {
				panicErr = fmt.Errorf("panic in drainer goroutine: %v", p)
			}
			r.log.Error("panic in drainer goroutine, route terminated",
				logger.String("source_id", route.SourceID),
				logger.String("consumer_id", route.Consumer.ID()),
				logger.Any("panic", p))
			_ = errors.New(panicErr).
				Component("audiocore.router").
				Category(errors.CategoryAudio).
				Context("operation", "drainer_goroutine_panic").
				Context("source_id", route.SourceID).
				Context("consumer_id", route.Consumer.ID()).
				Priority(errors.PriorityCritical).
				Build()
			// Remove the dead route from the map so HasConsumers/Dispatch
			// stop sending frames to an inbox nobody drains. Track whether
			// we found it: if router.Close() already snapshotted and cleared
			// the map, the route won't be here and stopRoute will handle
			// resource cleanup instead.
			removed := false
			r.mu.Lock()
			routes := r.routes[route.SourceID]
			for i, rt := range routes {
				if rt != route {
					continue
				}
				routes[i] = routes[len(routes)-1]
				routes[len(routes)-1] = nil
				r.routes[route.SourceID] = routes[:len(routes)-1]
				if len(r.routes[route.SourceID]) == 0 {
					delete(r.routes, route.SourceID)
				}
				removed = true
				break
			}
			r.mu.Unlock()
			// Release any pooled refs on frames buffered in the inbox so the
			// Retains performed by Dispatch are balanced even when the drainer
			// unwinds via panic. Without this, up to RouteInboxCapacity refs
			// per panicking route would stay outstanding (the slices still
			// GC, so this is pool-efficiency rather than a hard leak).
			drainInboxRefs(route.inbox)
			// Only close resources if we successfully removed the route.
			// If the route was not in the map (router.Close() already took
			// ownership), stopRoute will close them after route.stopped fires.
			if removed {
				if route.resampler != nil {
					func() {
						defer func() {
							if rp := recover(); rp != nil {
								r.log.Warn("secondary panic closing resampler after drainer panic",
									logger.String("source_id", route.SourceID),
									logger.String("consumer_id", route.Consumer.ID()),
									logger.Any("secondary_panic", rp))
							}
						}()
						_ = route.resampler.Close()
					}()
				}
				func() {
					defer func() {
						if rp := recover(); rp != nil {
							r.log.Warn("secondary panic closing consumer after drainer panic",
								logger.String("source_id", route.SourceID),
								logger.String("consumer_id", route.Consumer.ID()),
								logger.Any("secondary_panic", rp))
						}
					}()
					_ = route.Consumer.Close()
				}()
			}
		}
	}()
	for {
		select {
		case frame := <-route.inbox:
			r.handleRouteFrame(frame, route)
		case <-route.done:
			drainInboxRefs(route.inbox)
			return
		case <-r.ctx.Done():
			drainInboxRefs(route.inbox)
			return
		}
	}
}

// drainInboxRefs non-blockingly releases the pooled FrameRef on any frames
// still queued on an inbox at drainer exit. Dispatch retains once per
// successful enqueue; handleRouteFrame releases via defer. Frames that never
// reach handleRouteFrame (because the drainer exited via route.done or the
// router context was cancelled) would otherwise keep their retain outstanding
// forever, preventing the pool slice from being returned. Called from the
// drainer goroutine's exit paths so the balance of Retain calls from Dispatch
// is preserved on shutdown and route removal.
func drainInboxRefs(inbox <-chan AudioFrame) {
	for {
		select {
		case f := <-inbox:
			f.Ref.Release()
		default:
			return
		}
	}
}

// handleRouteFrame applies optional resampling and processing to frame, then
// writes it to the route's consumer. It is called from drainRoute's select loop
// and is extracted to keep drainRoute under the cognitive-complexity limit.
//
// A PCM16 frame is processed when the route has an EQ chain or a gain; both are
// per-frame snapshots, loaded once, so UpdateGain and UpdateFilterChain take
// effect on a route's next frame. A route
// that resamples and processes hands the resampler's float32 output straight to
// the processing stage (applyProcessingResampled), so the audio is quantised to
// int16 once instead of twice and no resample byte buffer is taken. A route that
// resamples without processing, or whose frame is not PCM16, resamples to bytes
// as before. A resampler that emits no samples for a non-empty input (it can,
// while its filter primes) produces no write on either branch: consumers are not
// documented to accept a zero-length frame.
//
// Pool discipline (one common release point):
//   - The function-level defer is the single release point. It runs
//     procResult.release(), a no-op on a zero-value result, and returns the
//     resample byte buffer while resamplePool is still non-nil. Being deferred,
//     it also runs when Consumer.Write panics; drainRoute's recover would
//     otherwise strand both buffers in the panicking frame.
//   - The resample byte buffer and the processing result are mutually exclusive:
//     a processed route takes no resample byte buffer (float32 handoff or
//     decode), and an unprocessed one takes no processing result. Neither can
//     be put back twice, and the resample buffer never backs a processed frame.
//   - applyProcessing and applyProcessingResampled release their own float32
//     scratch and output buffers internally on error and return a zero-value
//     result, so the deferred procResult.release() stays a no-op on that path.
func (r *AudioRouter) handleRouteFrame(frame AudioFrame, route *Route) { //nolint:gocritic // hugeParam: AudioFrame is large but copying is intentional
	// Balance the Retain performed by Dispatch for this route. Runs even if
	// consumer.Write panics (drainRoute's recover catches the panic).
	defer frame.Ref.Release()

	frameStart := time.Now()
	var resampleDur, processingDur time.Duration

	// Resampling, EQ and gain all reinterpret Data as a stream of 16-bit
	// samples, and so does every consumer of a PCM16 route, so every PCM16
	// frame is aligned to whole samples here regardless of what this route
	// does with it afterwards; any partial sample is carried into the next
	// frame. Aligning only the routes that resample or process would leave the
	// analysis consumers, which read a pass-through route, enforcing the
	// invariant nowhere. Non-PCM16 frames are left untouched: a 2-byte carry
	// would regroup their frame boundaries and permanently withhold a trailing
	// byte at stream end. The resampler assumes PCM16 by contract: ResampleTo
	// rejects an odd byte length but cannot tell another depth from PCM16 at an
	// even length, so a resampling route is only ever fed PCM16 sources.
	// The chain and the gain are each loaded once, so a concurrent update
	// cannot split the process gate from the stages within this frame.
	chain := route.filterChain.Load()
	g := route.gain.Load()
	if frame.BitDepth == bitDepthPCM16 {
		frame.Data = r.alignPCM16(frame.Data, route)
		if len(frame.Data) == 0 {
			// Every byte of this frame is now held in the carry (or the frame
			// was empty to begin with); there is nothing to hand downstream.
			return
		}
	}

	// EQ and gain both require 16-bit PCM; other formats skip them.
	process := frame.BitDepth == bitDepthPCM16 && (chain != nil || g != nil)

	var resamplePool *buffer.BytePool // nil when not pooled

	// procResult holds the processing output and pool handles. Zero value is
	// safe: release() is a no-op when all pool pointers are nil.
	var procResult processingResult

	var resampleBuf []byte // full-length pooled buffer for Put; nil when no byte resampling

	// Release the router's own pooled buffers on every exit from this function.
	// A single deferred release keeps the accounting in one place, and it also
	// covers a panicking Consumer.Write, where a release at the tail would be
	// skipped. That leak was bounded rather than unbounded, since drainRoute's
	// recover retires the route, but the buffers are cheap to return correctly.
	// Releasing during the unwind is safe for the same reason it is safe on the
	// normal path: AudioConsumer.Write forbids retaining frame.Data past return,
	// and Go runs the consumer's own defers before this one.
	//
	// No path can Put twice: resamplePool is set only on the byte resample
	// branch, which runs only for frames that are not processed, and
	// procResult.release is a no-op on a zero value.
	defer func() {
		procResult.release()
		if resamplePool != nil {
			resamplePool.Put(resampleBuf)
		}
	}()

	// resampled holds the resampler's float32 output on the handoff branch. It
	// aliases the resampler's own scratch, valid until the next call on this
	// route, which only this route's drainer goroutine makes.
	var resampled []float32
	if route.resampler != nil {
		resampleStart := time.Now()
		consumerRate := route.Consumer.SampleRate()
		if process {
			samples, ok := r.resampleToFloat32(frame.Data, route)
			if !ok {
				return
			}
			resampled = samples
		} else {
			outSize := route.resampler.EstimateOutputBytes(len(frame.Data))
			resamplePool = r.cachedBytePool(&route.resamplePool, &route.resamplePoolLen, outSize)
			if resamplePool != nil {
				resampleBuf = resamplePool.Get()
			} else {
				resampleBuf = make([]byte, outSize)
			}
			n, err := route.resampler.ResampleTo(frame.Data, resampleBuf)
			if err != nil {
				// The deferred release returns resampleBuf to its pool.
				r.recordResampleError(route, err)
				return
			}
			if n == 0 && len(frame.Data) > 0 {
				return
			}
			frame.Data = resampleBuf[:n]
		}
		// The frame no longer describes the producer's pooled buffer, so it must
		// not carry that buffer's Ref to the consumer.
		frame.Ref = nil
		frame.SampleRate = consumerRate
		resampleDur = time.Since(resampleStart)
	}

	// Apply per-route EQ filtering and/or gain adjustment.
	if process {
		procStart := time.Now()
		var result processingResult
		var err error
		if route.resampler != nil {
			result, err = r.applyProcessingResampled(frame, route, chain, g, resampled)
		} else {
			result, err = r.applyProcessing(frame, route, chain, g)
		}
		if err != nil {
			// The apply functions already released their own pool buffers on
			// error; the deferred release has nothing left to return.
			return
		}
		frame = result.Frame
		procResult = result
		processingDur = time.Since(procStart)
	}
	// See AudioConsumer.Write for the buffer ownership contract the
	// consumer must honour so the pool recycling below is safe.
	writeStart := time.Now()
	if err := route.Consumer.Write(frame); err != nil {
		errCount := route.errors.Add(1)
		writeErrCount := route.writeErrors.Add(1)
		if errCount%errorLogInterval == 1 {
			r.log.Warn("consumer write error",
				logger.String("source_id", route.SourceID),
				logger.String("consumer_id", route.Consumer.ID()),
				logger.Int64("total_errors", errCount),
				logger.Error(err))
		}
		if writeErrCount == dropSentryThreshold {
			_ = errors.Newf("consumer has %d write errors", writeErrCount).
				Component("audiocore.router").
				Category(errors.CategoryAudio).
				Context("operation", "sustained_write_errors").
				Context("source_id", route.SourceID).
				Context("source_type", sourceType(route.SourceID)).
				Context("consumer_id", route.Consumer.ID()).
				Context("consumer_type", consumerType(route.Consumer.ID())).
				Context("latest_error", err.Error()).
				Build()
		}
	}
	writeDur := time.Since(writeStart)

	route.stats.record(resampleDur, processingDur, writeDur, time.Since(frameStart))
	route.stats.checkAndLog(r.log, route.SourceID, route.Consumer.ID())
}

// resampleToFloat32 resamples PCM16 bytes into the resampler's float32 scratch
// for a route that goes on to process the audio. It returns ok false when the
// frame must not continue: the resampler failed (counted and logged) or it
// emitted no samples for a non-empty input, which leaves nothing to hand to the
// processing stage or the consumer.
func (r *AudioRouter) resampleToFloat32(data []byte, route *Route) (samples []float32, ok bool) {
	samples, err := route.resampler.ResampleFloat32Into(data)
	if err != nil {
		r.recordResampleError(route, err)
		return nil, false
	}
	if len(samples) == 0 && len(data) > 0 {
		return nil, false
	}
	return samples, true
}

// recordResampleError counts a resampler failure on the route and logs it at
// the usual errorLogInterval.
func (r *AudioRouter) recordResampleError(route *Route, err error) {
	errCount := route.errors.Add(1)
	if errCount%errorLogInterval == 1 {
		r.log.Warn("resampler error",
			logger.String("source_id", route.SourceID),
			logger.String("consumer_id", route.Consumer.ID()),
			logger.Int64("total_errors", errCount),
			logger.Error(err))
	}
}

// alignPCM16 returns data trimmed to a whole number of 16-bit samples, with any
// partial sample left over from the previous frame prepended and any new partial
// sample retained in route.carry for the next frame.
//
// The trailing byte of an odd-length frame is half a sample. Discarding it does
// not merely shorten the audio: it shifts every following byte by one, so each
// subsequent sample decodes with its halves swapped and the stream turns into
// broadband noise until the framing happens to realign. Carrying the byte forward
// keeps the sample stream intact.
//
// The in-tree producers no longer deliver odd lengths themselves: the FFmpeg
// reader reassembles whole frames before dispatch, and the malgo capture path
// emits whole 16-bit samples by construction. The carry here is the router's own
// enforcement of the whole-sample invariant, one hop before the consumers, so it
// holds for every producer, present or future, and keeps a producer-side
// regression from turning into corrupted audio downstream.
//
// The returned slice aliases either data or route.alignBuf, and route.alignBuf is
// overwritten by the next frame on this route. That is safe for the same reason
// the producer's pooled slice is: AudioConsumer.Write forbids retaining
// frame.Data past return, and the next overwrite happens on this same goroutine,
// after Write has returned. A pass-through route hands alignBuf to the consumer
// directly, so the no-retain contract, not an intervening copy, is what makes
// this sound.
//
// Called only from the route's drainer goroutine, so route.carry, route.carryLen
// and route.alignBuf are unsynchronised by design.
func (r *AudioRouter) alignPCM16(data []byte, route *Route) []byte {
	if route.carryLen == 0 && len(data)%bytesPerPCM16Sample == 0 {
		return data
	}

	frameBytes := len(data)
	// Whether this frame starts a new carrying run. Realignment latches: once a
	// run starts, joining the carry makes an even-length frame odd again, so
	// every following frame re-arms the carry. Logging per frame would then
	// repeat forever, so only the transition into a run is reported.
	startsRun := route.carryLen == 0

	if route.carryLen > 0 {
		route.alignBuf = append(route.alignBuf[:0], route.carry[:route.carryLen]...)
		route.alignBuf = append(route.alignBuf, data...)
		data = route.alignBuf
		route.carryLen = 0
	}

	rem := len(data) % bytesPerPCM16Sample
	if rem == 0 {
		return data
	}

	// Copy into the fixed-size array before re-slicing: the source may be
	// route.alignBuf, which the next call overwrites.
	route.carryLen = copy(route.carry[:], data[len(data)-rem:])
	data = data[:len(data)-rem]

	count := route.realignments.Add(1)
	if !startsRun {
		return data
	}
	runs := route.realignmentRuns.Add(1)
	if runs%realignmentLogInterval == 1 {
		r.log.Info("partial PCM16 sample carried to next frame",
			logger.String("source_id", route.SourceID),
			logger.String("consumer_id", route.Consumer.ID()),
			logger.Int("frame_bytes", frameBytes),
			logger.Int64("total_realignments", count),
			logger.Int64("realignment_runs", runs))
	}
	return data
}

// processingResult carries an EQ / gain processed frame plus the pooled
// buffers that must be returned to their pools after the consumer has
// synchronously observed the frame. A zero-value result (all pools nil)
// is safe: release() is a no-op.
type processingResult struct {
	Frame     AudioFrame
	FloatPool *buffer.Float32Pool
	FloatBuf  []float32
	OutPool   *buffer.BytePool
}

// release returns the processing pool buffers to their pools. Safe to call
// on a zero-value result. Callers invoke this after the consumer's Write
// returns so the recycled slices are not concurrently read.
func (p *processingResult) release() {
	if p.FloatPool != nil {
		p.FloatPool.Put(p.FloatBuf)
	}
	if p.OutPool != nil {
		p.OutPool.Put(p.Frame.Data)
	}
}

// floatScratch is a float32 buffer taken for one processing call, with the
// pool it came from (nil when it was allocated with make). It is returned to
// its pool on every exit that does not hand it to a processingResult.
type floatScratch struct {
	pool *buffer.Float32Pool
	buf  []float32
	// handedOff is set once the buffer belongs to a processingResult, whose
	// release() then returns it after Consumer.Write.
	handedOff bool
}

// putBack returns the buffer to its pool unless it was handed off.
func (s *floatScratch) putBack() {
	if !s.handedOff && s.pool != nil {
		s.pool.Put(s.buf)
	}
}

// getFloatScratch takes a float32 buffer of exactly size samples from the
// route's cached pool, or allocates one when there is no buffer manager. A
// pooled buffer is not zeroed: callers must overwrite every sample before
// reading it, because the pool is shared by size with other packages.
func (r *AudioRouter) getFloatScratch(route *Route, size int) floatScratch {
	pool := r.cachedFloat32Pool(&route.float32Pool, &route.float32PoolLen, size)
	if pool != nil {
		return floatScratch{pool: pool, buf: pool.Get()}
	}
	return floatScratch{buf: make([]float32, size)}
}

// getOutBytes takes a byte buffer of exactly size bytes from the route's cached
// pool, or allocates one when there is no buffer manager. The returned pool is
// nil when the buffer was allocated.
func (r *AudioRouter) getOutBytes(route *Route, size int) (pool *buffer.BytePool, out []byte) {
	pool = r.cachedBytePool(&route.byteOutPool, &route.byteOutPoolLen, size)
	if pool != nil {
		out = pool.Get()
		return pool, out
	}
	return nil, make([]byte, size)
}

// processedFrame builds the output frame for out, carrying frame's metadata.
func processedFrame(frame AudioFrame, out []byte) AudioFrame { //nolint:gocritic // hugeParam: AudioFrame is large but copying is intentional
	return AudioFrame{
		SourceID:   frame.SourceID,
		SourceName: frame.SourceName,
		Data:       out,
		SampleRate: frame.SampleRate,
		BitDepth:   frame.BitDepth,
		Channels:   frame.Channels,
		Timestamp:  frame.Timestamp,
	}
}

// recordProcessingError counts a processing failure on the route, logs it at
// the usual errorLogInterval, and returns err for the caller to propagate.
func (r *AudioRouter) recordProcessingError(route *Route, err error) error {
	errCount := route.errors.Add(1)
	if errCount%errorLogInterval == 1 {
		r.log.Warn("audio processing error",
			logger.String("source_id", route.SourceID),
			logger.String("consumer_id", route.Consumer.ID()),
			logger.Int64("total_errors", errCount),
			logger.Error(err))
	}
	return err
}

// applyProcessing applies EQ filtering and/or gain scaling to a PCM16 frame
// whose Data is the route's own (not resampled-to-float) input. The chain may
// be nil (EQ disabled) and g may be nil (0 dB); at least one must be
// active when this is called.
//
// Arithmetic is float32 on go-audio-dsp: samples are decoded to float32 (exact
// for int16), run through the EQ chain (float64 filter state, float32 between
// sections), then the gain, and encoded back to int16 scaled by 32768 with
// round half to even and saturation, so -32768 survives and an identity chain
// is lossless. A gain-only route skips the float stage entirely and scales the
// bytes with Gain.ApplyBytesTo, which gives the same bytes as the float path
// because scaling by 1/32768 and 32768 is exact.
//
// Return contract: on success, the caller owns the returned processingResult
// and must call release() after Consumer.Write returns. On error, applyProcessing
// releases its buffers internally and returns a zero-value result, so
// release() on the zero value is always safe (it is a no-op).
//
// When bufMgr is nil or a pool lookup returns nil, the function falls back to
// make() with no corresponding Put, preserving legacy behaviour for unit tests
// that construct routers with a nil Manager.
func (r *AudioRouter) applyProcessing(frame AudioFrame, route *Route, chain *dspeq.FilterChain, g *dspgain.Gain) (processingResult, error) { //nolint:gocritic // hugeParam: AudioFrame is large but copying is intentional
	// frame.Data holds a whole number of samples: handleRouteFrame aligns every
	// PCM16 frame before either processing stage runs, and only PCM16 frames
	// reach this function, so the align gate covers a strict superset of the
	// frames seen here. The floor below is a bounds guard for callers that build
	// a frame by hand (the tests and benchmarks in this package), not a
	// truncation policy. Dropping a byte mid-stream desynchronises every sample
	// that follows it, so narrowing the align gate in handleRouteFrame below
	// this function's own gate would silently reintroduce that truncation.
	evenLen := len(frame.Data) &^ (bytesPerPCM16Sample - 1)

	if chain == nil {
		return r.applyGainBytes(frame, route, g, evenLen)
	}

	scratch := r.getFloatScratch(route, evenLen/bytesPerPCM16Sample)
	defer scratch.putBack()

	if _, err := dsppcm.BytesToFloat32(scratch.buf, frame.Data[:evenLen]); err != nil {
		return processingResult{}, r.recordProcessingError(route, err)
	}
	return r.encodeProcessed(frame, route, chain, g, scratch.buf, &scratch)
}

// applyProcessingResampled is applyProcessing for a route that resamples: src
// is the resampler's float32 output, so the audio is not re-decoded from bytes
// or quantised to int16 between the resampler and the EQ and gain stages. src
// is only read (it aliases the resampler's scratch) and must be non-empty. The
// return contract and fallbacks are those of applyProcessing, and g may be nil
// (0 dB) as there; frame supplies the output metadata and its Data is ignored.
func (r *AudioRouter) applyProcessingResampled(frame AudioFrame, route *Route, chain *dspeq.FilterChain, g *dspgain.Gain, src []float32) (processingResult, error) { //nolint:gocritic // hugeParam: AudioFrame is large but copying is intentional
	scratch := r.getFloatScratch(route, len(src))
	defer scratch.putBack()

	return r.encodeProcessed(frame, route, chain, g, src, &scratch)
}

// applyGainBytes is the gain-only path: it scales the first evenLen bytes of
// frame.Data straight into a pooled output buffer. No float buffer is taken. A
// nil g copies the bytes unchanged.
func (r *AudioRouter) applyGainBytes(frame AudioFrame, route *Route, g *dspgain.Gain, evenLen int) (processingResult, error) { //nolint:gocritic // hugeParam: AudioFrame is large but copying is intentional
	outPool, out := r.getOutBytes(route, evenLen)

	if g != nil {
		if err := g.ApplyBytesTo(out, frame.Data[:evenLen]); err != nil {
			if outPool != nil {
				outPool.Put(out)
			}
			return processingResult{}, r.recordProcessingError(route, err)
		}
	} else {
		copy(out, frame.Data[:evenLen])
	}

	return processingResult{Frame: processedFrame(frame, out), OutPool: outPool}, nil
}

// encodeProcessed runs the EQ chain and then the gain over src, writing into
// scratch.buf (src is only read, and may be scratch.buf itself), and encodes
// the result into a pooled byte buffer. At least one stage must be active, so
// scratch.buf is always written before it is read. On success it marks scratch
// as handed off, so the caller's deferred putBack leaves the float buffer to
// the returned result; on error it returns the output buffer to its pool and
// leaves the float buffer to the caller's putBack. chain and g may each be nil;
// they are the caller's per-frame snapshots, so a concurrent UpdateGain cannot
// change them mid-frame.
func (r *AudioRouter) encodeProcessed(frame AudioFrame, route *Route, chain *dspeq.FilterChain, g *dspgain.Gain, src []float32, scratch *floatScratch) (processingResult, error) { //nolint:gocritic // hugeParam: AudioFrame is large but copying is intentional
	x := src
	if chain != nil {
		// EQ first - filters operate on the original signal shape.
		if _, err := chain.ProcessInto(x, scratch.buf); err != nil {
			return processingResult{}, r.recordProcessingError(route, err)
		}
		x = scratch.buf
	}
	if g != nil {
		// Gain second - scales the (possibly filtered) signal.
		if _, err := g.ProcessInto(x, scratch.buf); err != nil {
			return processingResult{}, r.recordProcessingError(route, err)
		}
		x = scratch.buf
	}

	outPool, out := r.getOutBytes(route, len(scratch.buf)*bytesPerPCM16Sample)
	if _, err := dsppcm.Float32ToBytes(out, x); err != nil {
		if outPool != nil {
			outPool.Put(out)
		}
		return processingResult{}, r.recordProcessingError(route, err)
	}

	scratch.handedOff = true
	return processingResult{
		Frame:     processedFrame(frame, out),
		FloatPool: scratch.pool,
		FloatBuf:  scratch.buf,
		OutPool:   outPool,
	}, nil
}

// cachedBytePool returns a BytePool for the given size, using the cached
// pointer if the size matches. On miss, falls back to bufMgr.BytePoolFor()
// and caches the result. Returns nil when bufMgr is nil.
func (r *AudioRouter) cachedBytePool(cached *atomic.Pointer[buffer.BytePool], cachedLen *atomic.Int64, size int) *buffer.BytePool {
	if r.bufMgr == nil || size <= 0 {
		return nil
	}
	if p := cached.Load(); p != nil && cachedLen.Load() == int64(size) {
		return p
	}
	p := r.bufMgr.BytePoolFor(size)
	if p != nil {
		cached.Store(p)
		cachedLen.Store(int64(size))
	}
	return p
}

// cachedFloat32Pool returns a Float32Pool for the given sample count, using
// the cached pointer if the count matches. On miss, falls back to
// bufMgr.Float32PoolFor() and caches the result. Returns nil when bufMgr
// is nil.
func (r *AudioRouter) cachedFloat32Pool(cached *atomic.Pointer[buffer.Float32Pool], cachedLen *atomic.Int64, size int) *buffer.Float32Pool {
	if r.bufMgr == nil || size <= 0 {
		return nil
	}
	if p := cached.Load(); p != nil && cachedLen.Load() == int64(size) {
		return p
	}
	p := r.bufMgr.Float32PoolFor(size)
	if p != nil {
		cached.Store(p)
		cachedLen.Store(int64(size))
	}
	return p
}

// consumerType extracts the consumer type prefix from an ID like
// "soundlevel_rtsp_fefc1d2d" -> "soundlevel" or "buffer_audio_card_..." -> "buffer".
// Returns the full ID if no underscore prefix is found.
func consumerType(consumerID string) string {
	if idx := strings.Index(consumerID, "_"); idx > 0 {
		return consumerID[:idx]
	}
	return consumerID
}

// sourceType extracts the source type prefix from an ID like
// "rtsp_fefc1d2d" -> "rtsp" or "audio_card_22a9b331" -> "audio_card".
// Handles the two-word "audio_card" prefix.
func sourceType(sourceID string) string {
	for _, prefix := range []string{"audio_card", "rtsp"} {
		if strings.HasPrefix(sourceID, prefix) {
			return prefix
		}
	}
	if idx := strings.Index(sourceID, "_"); idx > 0 {
		return sourceID[:idx]
	}
	return sourceID
}
