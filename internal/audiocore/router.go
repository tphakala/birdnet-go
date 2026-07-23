// router.go - AudioRouter with non-blocking fan-out dispatch.
package audiocore

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/audiocore/convert"
	"github.com/tphakala/birdnet-go/internal/audiocore/equalizer"
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

	// dropSentryThreshold is the total number of drops per route before
	// a single Sentry report is fired. This fires once; the log.Warn at
	// dropLogInterval provides ongoing visibility.
	dropSentryThreshold int64 = 1000

	// bitDepthPCM16 is the only bit depth the router's resampling and
	// EQ/gain paths can interpret; other depths are passed through untouched.
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

	// gainLinear is the linear gain multiplier derived from the dB value.
	// 1.0 means no gain (0 dB). Set at route creation time and immutable.
	gainLinear float64

	// resampler converts source-rate PCM to the consumer's expected rate.
	// Nil when no rate conversion is required.
	resampler *resample.Resampler

	// filterChain holds the current EQ filter chain for this route.
	// Accessed atomically so it can be swapped without rebuilding the route.
	// Nil means no EQ filtering is applied.
	filterChain atomic.Pointer[equalizer.FilterChain]

	// inbox is the bounded channel between Dispatch and the drainer goroutine.
	inbox chan AudioFrame

	// drops counts frames that could not be enqueued because inbox was full.
	drops atomic.Int64

	// errors counts all processing failures (resampler + Write) for this route.
	errors atomic.Int64

	// writeErrors counts only Consumer.Write failures, used for the
	// sustained-write-errors Sentry threshold separately from resampler errors.
	writeErrors atomic.Int64

	// realignments counts odd-length PCM16 frames observed on this route.
	// Kept separate from errors so RouteInfo.Errors retains its "real error"
	// semantics for operators; an upstream that splits samples across reads
	// is not an error, it does not inflate that metric.
	realignments atomic.Int64

	// carry holds the bytes of a PCM16 sample that the previous frame ended
	// part-way through, and carryLen is how many of them are valid (0 or 1).
	// alignBuf is the scratch buffer the carry is joined with the next frame
	// in. All three are touched only by this route's drainer goroutine, via
	// alignPCM16, so they need no synchronisation.
	carry    [bytesPerPCM16Sample - 1]byte
	carryLen int
	alignBuf []byte

	// done is closed to signal the drainer goroutine to exit.
	done chan struct{}

	// stopped is closed by the drainer goroutine when it exits. Callers
	// must wait on this channel after closing done before touching the
	// resampler or consumer to avoid a data race.
	stopped chan struct{}

	// Pool caches: set lazily on first frame to avoid per-frame mutex
	// acquisition in BytePoolFor/Float64PoolFor. The cached pool is used
	// when the current frame size matches; on mismatch the fallback path
	// calls the mutex-guarded lookup.
	resamplePool    atomic.Pointer[buffer.BytePool]
	resamplePoolLen atomic.Int64
	float64Pool     atomic.Pointer[buffer.Float64Pool]
	float64PoolLen  atomic.Int64
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

	// Errors is the total number of Write errors for this route.
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
func (r *AudioRouter) AddRoute(sourceID string, consumer AudioConsumer, sourceSampleRate int, gainDB float64, filterChain *equalizer.FilterChain) error {
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

	// Reject NaN/Inf gain values before conversion.
	if math.IsNaN(gainDB) || math.IsInf(gainDB, 0) {
		return fmt.Errorf("invalid gain value for source=%s consumer=%s: %f", sourceID, consumer.ID(), gainDB)
	}

	// Convert dB to linear gain. 0 dB -> 1.0 (no change).
	gainLinear := math.Pow(10, gainDB/20)

	route := &Route{
		SourceID:         sourceID,
		Consumer:         consumer,
		sourceSampleRate: sourceSampleRate,
		gainLinear:       gainLinear,
		inbox:            make(chan AudioFrame, RouteInboxCapacity),
		done:             make(chan struct{}),
		stopped:          make(chan struct{}),
	}

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
type FilterChainBuilder func(sampleRate int) *equalizer.FilterChain

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
			filterCount = chain.Length()
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
// Pool discipline (three release sites, seven scenarios):
//   - Resample buffer: allocated before resampling, returned after Write (or on
//     error). When processing follows, the resample buffer is returned before
//     frame.Data is reassigned so the wrong (processed) slice is never put back
//     to the resample pool; resamplePool is nilled so the trailing guard is a
//     no-op.
//   - Processing float64 scratch and output byte buffers: allocated inside
//     applyProcessing, bundled in procResult. procResult.release() is called
//     unconditionally in the trailing guards; it is a no-op on a zero-value
//     result (processing never ran or errored with internal cleanup).
//   - procResult.OutPool and resamplePool cannot both be non-nil at the trailing
//     guards by construction, so their Put calls are mutually exclusive.
func (r *AudioRouter) handleRouteFrame(frame AudioFrame, route *Route) { //nolint:gocritic // hugeParam: AudioFrame is large but copying is intentional
	// Balance the Retain performed by Dispatch for this route. Runs even if
	// consumer.Write panics (drainRoute's recover catches the panic).
	defer frame.Ref.Release()

	frameStart := time.Now()
	var resampleDur, processingDur time.Duration

	// Resampling and EQ/gain both reinterpret Data as a stream of 16-bit
	// samples, so both need whole samples. Align once here, ahead of the first
	// of them, and carry any partial sample into the next frame. A pass-through
	// route is left untouched: it hands the producer's bytes to the consumer
	// without interpreting them. The resampler assumes PCM16 by contract
	// regardless of the frame's declared bit depth, so its presence alone
	// requires the alignment.
	chain := route.filterChain.Load()
	if route.resampler != nil || (frame.BitDepth == bitDepthPCM16 && (chain != nil || route.gainLinear != 1.0)) {
		frame.Data = r.alignPCM16(frame.Data, route)
		if len(frame.Data) == 0 {
			// Every byte of this frame is now held in the carry (or the frame
			// was empty to begin with); there is nothing to hand downstream.
			return
		}
	}

	var resamplePool *buffer.BytePool // nil when not pooled

	// procResult holds the processing output and pool handles. Zero value is
	// safe: release() is a no-op when all pool pointers are nil.
	var procResult processingResult

	var resampleBuf []byte // full-length pooled buffer for Put; nil when no resampling

	// Apply per-route resampling when rates differ.
	if route.resampler != nil {
		resampleStart := time.Now()
		outSize := route.resampler.EstimateOutputBytes(len(frame.Data))
		resamplePool = r.cachedBytePool(&route.resamplePool, &route.resamplePoolLen, outSize)
		if resamplePool != nil {
			resampleBuf = resamplePool.Get()
		} else {
			resampleBuf = make([]byte, outSize)
		}
		n, err := route.resampler.ResampleTo(frame.Data, resampleBuf)
		if err != nil {
			if resamplePool != nil {
				resamplePool.Put(resampleBuf)
			}
			errCount := route.errors.Add(1)
			if errCount%errorLogInterval == 1 {
				r.log.Warn("resampler error",
					logger.String("source_id", route.SourceID),
					logger.String("consumer_id", route.Consumer.ID()),
					logger.Int64("total_errors", errCount),
					logger.Error(err))
			}
			return
		}
		frame = AudioFrame{
			SourceID:   frame.SourceID,
			SourceName: frame.SourceName,
			Data:       resampleBuf[:n],
			SampleRate: route.Consumer.SampleRate(),
			BitDepth:   frame.BitDepth,
			Channels:   frame.Channels,
			Timestamp:  frame.Timestamp,
		}
		resampleDur = time.Since(resampleStart)
	}
	// Apply per-route EQ filtering and/or gain adjustment.
	// Both require 16-bit PCM; skip for other formats.
	if frame.BitDepth == bitDepthPCM16 && (chain != nil || route.gainLinear != 1.0) {
		procStart := time.Now()
		result, err := r.applyProcessing(frame, route, chain)
		if err != nil {
			// applyProcessing already released its own pool buffers on error.
			// Release the resample buffer.
			if resamplePool != nil {
				resamplePool.Put(resampleBuf)
			}
			return
		}
		// Release the resample buffer BEFORE frame.Data is reassigned to the
		// processing output. Nil out the pointer so the trailing guard is a no-op.
		if resamplePool != nil {
			resamplePool.Put(resampleBuf)
			resamplePool = nil
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

	// Release processing buffers (output byte slice and float64 scratch).
	// Safe to call unconditionally: no-op when procResult is zero value.
	// All in-tree consumers copy synchronously, so recycling is safe here.
	procResult.release()
	// Release resample buffer when processing did NOT run (processing branch
	// nilled resamplePool before reassigning frame.Data). When processing ran,
	// resamplePool is nil here and this is a no-op.
	if resamplePool != nil {
		resamplePool.Put(resampleBuf)
	}

	route.stats.record(resampleDur, processingDur, writeDur, time.Since(frameStart))
	route.stats.checkAndLog(r.log, route.SourceID, route.Consumer.ID())
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
// Odd lengths are not an upstream bug. internal/audiocore/ffmpeg reads its source
// from a pipe, and a pipe read returns whatever bytes happen to be available.
//
// The returned slice aliases either data or route.alignBuf. Both stay valid for
// the rest of the frame's journey through handleRouteFrame, which reads them into
// a separate output buffer before handing anything to the consumer.
//
// Called only from the route's drainer goroutine, so route.carry, route.carryLen
// and route.alignBuf are unsynchronised by design.
func (r *AudioRouter) alignPCM16(data []byte, route *Route) []byte {
	if route.carryLen == 0 && len(data)%bytesPerPCM16Sample == 0 {
		return data
	}

	frameBytes := len(data)

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
	if count%errorLogInterval == 1 {
		r.log.Info("odd-length PCM16 frame realigned",
			logger.String("source_id", route.SourceID),
			logger.String("consumer_id", route.Consumer.ID()),
			logger.Int("frame_bytes", frameBytes),
			logger.Int64("total_realignments", count))
	}
	return data
}

// processingResult carries an EQ / gain processed frame plus the pooled
// buffers that must be returned to their pools after the consumer has
// synchronously observed the frame. A zero-value result (all pools nil)
// is safe: release() is a no-op.
type processingResult struct {
	Frame     AudioFrame
	FloatPool *buffer.Float64Pool
	FloatBuf  []float64
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

// applyProcessing applies EQ filtering and/or gain scaling to a frame in a
// single float64 conversion pass. The chain may be nil (EQ disabled); gain
// at 1.0 means no scaling. At least one must be active for this to be called.
//
// Return contract: on success, the caller owns the returned processingResult
// and must call release() after Consumer.Write returns. On error, applyProcessing
// releases both buffers internally and returns a zero-value result, so
// release() on the zero value is always safe (it is a no-op).
//
// When bufMgr is nil or a pool lookup returns nil, the function falls back to
// make() with no corresponding Put, preserving legacy behaviour for unit tests
// that construct routers with a nil Manager.
func (r *AudioRouter) applyProcessing(frame AudioFrame, route *Route, chain *equalizer.FilterChain) (processingResult, error) { //nolint:gocritic // hugeParam: AudioFrame is large but copying is intentional
	// frame.Data holds a whole number of samples: handleRouteFrame runs it
	// through alignPCM16 first, which carries any partial sample into the next
	// frame. The floor below is a bounds guard for callers that build a frame by
	// hand (the benchmarks in this package), not a truncation policy. Dropping a
	// byte mid-stream desynchronises every sample that follows it.
	evenLen := len(frame.Data) &^ (bytesPerPCM16Sample - 1)
	sampleCount := evenLen / bytesPerPCM16Sample

	floatPool := r.cachedFloat64Pool(&route.float64Pool, &route.float64PoolLen, sampleCount)
	var floatBuf []float64
	if floatPool != nil {
		floatBuf = floatPool.Get()
	} else {
		floatBuf = make([]float64, sampleCount)
	}

	// The deferred closure below guards both pool buffers via the
	// releaseFloat / releaseOut flags. It returns them on any early exit;
	// the success path disarms both flags before returning so the caller
	// owns the buffers (to be released after Consumer.Write via
	// processingResult.release()).
	releaseFloat := true
	releaseOut := true
	var outPool *buffer.BytePool
	var out []byte
	defer func() {
		if releaseFloat && floatPool != nil {
			floatPool.Put(floatBuf)
		}
		if releaseOut && outPool != nil {
			outPool.Put(out)
		}
	}()

	convert.BytesToFloat64PCM16Into(floatBuf, frame.Data[:evenLen])

	// EQ first - filters operate on the original signal shape.
	if chain != nil {
		chain.ApplyBatch(floatBuf)
	}

	// Gain second - scales the (possibly filtered) signal.
	if route.gainLinear != 1.0 {
		convert.ScaleFloat64Slice(floatBuf, route.gainLinear)
	}

	outLen := sampleCount * 2
	outPool = r.cachedBytePool(&route.byteOutPool, &route.byteOutPoolLen, outLen)
	if outPool != nil {
		out = outPool.Get()
	} else {
		out = make([]byte, outLen)
	}

	if convErr := convert.Float64ToBytesPCM16(floatBuf, out); convErr != nil {
		errCount := route.errors.Add(1)
		if errCount%errorLogInterval == 1 {
			r.log.Warn("audio processing conversion error",
				logger.String("source_id", route.SourceID),
				logger.String("consumer_id", route.Consumer.ID()),
				logger.Int64("total_errors", errCount),
				logger.Error(convErr))
		}
		// defer releases both buffers; caller sees zero-value result.
		return processingResult{}, convErr
	}

	// Success: disarm the defer so the caller owns the buffers.
	releaseFloat = false
	releaseOut = false
	return processingResult{
		Frame: AudioFrame{
			SourceID:   frame.SourceID,
			SourceName: frame.SourceName,
			Data:       out,
			SampleRate: frame.SampleRate,
			BitDepth:   frame.BitDepth,
			Channels:   frame.Channels,
			Timestamp:  frame.Timestamp,
		},
		FloatPool: floatPool,
		FloatBuf:  floatBuf,
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

// cachedFloat64Pool returns a Float64Pool for the given sample count, using
// the cached pointer if the count matches. On miss, falls back to
// bufMgr.Float64PoolFor() and caches the result. Returns nil when bufMgr
// is nil.
func (r *AudioRouter) cachedFloat64Pool(cached *atomic.Pointer[buffer.Float64Pool], cachedLen *atomic.Int64, size int) *buffer.Float64Pool {
	if r.bufMgr == nil || size <= 0 {
		return nil
	}
	if p := cached.Load(); p != nil && cachedLen.Load() == int64(size) {
		return p
	}
	p := r.bufMgr.Float64PoolFor(size)
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
