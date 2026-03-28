// router.go - AudioRouter with non-blocking fan-out dispatch.
package audiocore

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/resample"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// routeInboxCapacity is the per-route buffered channel size.
	// A slow consumer drops frames after this many are queued.
	routeInboxCapacity = 64

	// dropLogInterval controls how often drop warnings are emitted.
	// One log line per this many consecutive drops, per route.
	dropLogInterval = 100

	// errorLogInterval controls how often consumer write errors are logged.
	errorLogInterval = 100
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

	// resampler converts source-rate PCM to the consumer's expected rate.
	// Nil when no rate conversion is required.
	resampler *resample.Resampler

	// inbox is the bounded channel between Dispatch and the drainer goroutine.
	inbox chan AudioFrame

	// drops counts frames that could not be enqueued because inbox was full.
	drops atomic.Int64

	// errors counts Write failures reported by the consumer.
	errors atomic.Int64

	// done is closed to signal the drainer goroutine to exit.
	done chan struct{}

	// stopped is closed by the drainer goroutine when it exits. Callers
	// must wait on this channel after closing done before touching the
	// resampler or consumer to avoid a data race.
	stopped chan struct{}
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

	// ctx and cancel control the lifetime of all drainer goroutines.
	ctx    context.Context
	cancel context.CancelFunc
}

// NewAudioRouter creates an AudioRouter ready to accept routes and dispatch frames.
func NewAudioRouter(log logger.Logger) *AudioRouter {
	ctx, cancel := context.WithCancel(context.Background())
	return &AudioRouter{
		routes: make(map[string][]*Route),
		log:    log.With(logger.String("component", "audio_router")),
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
func (r *AudioRouter) AddRoute(sourceID string, consumer AudioConsumer, sourceSampleRate int) error {
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

	route := &Route{
		SourceID:         sourceID,
		Consumer:         consumer,
		sourceSampleRate: sourceSampleRate,
		inbox:            make(chan AudioFrame, routeInboxCapacity),
		done:             make(chan struct{}),
		stopped:          make(chan struct{}),
	}

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

	if len(routes) > 0 {
		r.log.Info("all routes removed",
			logger.String("source_id", sourceID),
			logger.Int("count", len(routes)))
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
func (r *AudioRouter) Dispatch(frame AudioFrame) { //nolint:gocritic // hugeParam: signature required by AudioDispatcher interface
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, rt := range r.routes[frame.SourceID] {
		select {
		case rt.inbox <- frame:
			// Frame enqueued successfully.
		default:
			drops := rt.drops.Add(1)
			if drops%dropLogInterval == 1 {
				r.log.Warn("frames dropped for consumer",
					logger.String("source_id", frame.SourceID),
					logger.String("consumer_id", rt.Consumer.ID()),
					logger.Int64("total_drops", drops))
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
		})
	}
	return infos
}

// Close stops all drainer goroutines and closes every registered consumer.
// It is safe to call Close multiple times: context.CancelFunc is idempotent,
// and the map is atomically swapped under the lock so subsequent calls see an
// empty map and close no done channels.
func (r *AudioRouter) Close() {
	r.cancel()

	r.mu.Lock()
	allRoutes := r.routes
	r.routes = make(map[string][]*Route)
	r.mu.Unlock()

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
		// Drainer exited cleanly — safe to close resources.
		if route.resampler != nil {
			if err := route.resampler.Close(); err != nil {
				r.log.Debug("resampler close error",
					logger.String("source_id", route.SourceID),
					logger.String("consumer_id", route.Consumer.ID()),
					logger.Error(err))
			}
		}
		if err := route.Consumer.Close(); err != nil {
			r.log.Debug("consumer close error",
				logger.String("source_id", route.SourceID),
				logger.String("consumer_id", route.Consumer.ID()),
				logger.Error(err))
		}
	case <-time.After(drainerStopTimeout):
		// Drainer is leaked and may still reference the resampler/consumer.
		// Do NOT close them — leave for GC to reclaim.
		r.log.Warn("drainer goroutine did not exit in time, leaking resources",
			logger.String("source_id", route.SourceID),
			logger.String("consumer_id", route.Consumer.ID()))
	}
}

// drainRoute reads frames from the route's inbox and delivers them to the
// consumer. When the route has a resampler, the frame data is converted to
// the consumer's sample rate before delivery. It exits when the route's done
// channel is closed or the router's context is cancelled.
func (r *AudioRouter) drainRoute(route *Route) {
	defer close(route.stopped)
	for {
		select {
		case frame := <-route.inbox:
			// Apply per-route resampling when rates differ.
			if route.resampler != nil {
				resampled, err := route.resampler.ResampleInto(frame.Data)
				if err != nil {
					errCount := route.errors.Add(1)
					if errCount%errorLogInterval == 1 {
						r.log.Warn("resampler error",
							logger.String("source_id", route.SourceID),
							logger.String("consumer_id", route.Consumer.ID()),
							logger.Int64("total_errors", errCount),
							logger.Error(err))
					}
					continue
				}
				// Copy resampled bytes: the Resampler reuses its internal buffer,
				// so we must not hand the slice to the consumer directly.
				out := make([]byte, len(resampled))
				copy(out, resampled)
				frame = AudioFrame{
					SourceID:   frame.SourceID,
					SourceName: frame.SourceName,
					Data:       out,
					SampleRate: route.Consumer.SampleRate(),
					BitDepth:   frame.BitDepth,
					Channels:   frame.Channels,
					Timestamp:  frame.Timestamp,
				}
			}
			if err := route.Consumer.Write(frame); err != nil {
				errCount := route.errors.Add(1)
				if errCount%errorLogInterval == 1 {
					r.log.Warn("consumer write error",
						logger.String("source_id", route.SourceID),
						logger.String("consumer_id", route.Consumer.ID()),
						logger.Int64("total_errors", errCount),
						logger.Error(err))
				}
			}
		case <-route.done:
			return
		case <-r.ctx.Done():
			return
		}
	}
}
