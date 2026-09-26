package stream

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// shutdownTimeout bounds the default Shutdown of every stream.
const shutdownTimeout = 10 * time.Second

// errManagerShutDown is the cause recorded when the manager context is cancelled.
var errManagerShutDown = errors.Newf("native stream manager shut down").
	Component("native-stream").Category(errors.CategoryState).Build()

// Manager is the pure-Go network stream producer. It implements
// audiocore.StreamManager on top of go-audio-stream supervisors, so the engine
// drives it through the same seam as ffmpeg.Manager. Phase 2 handles RTSP only;
// every other source type hard-fails from StartStream.
type Manager struct {
	ctx    context.Context
	cancel context.CancelCauseFunc

	deliver dispatchFunc
	log     logger.Logger
	bufMgr  *buffer.Manager
	opts    Options

	onResetMu sync.RWMutex
	onReset   func(sourceID string)

	mu      sync.RWMutex
	streams map[string]*stream

	closeOnce sync.Once
}

// Manager implements the producer-neutral seam.
var _ audiocore.StreamManager = (*Manager)(nil)

// NewManager creates a native stream Manager. onFrame receives every dispatched
// AudioFrame (the engine forwards it to the router); onReset fires once when a
// stream is (re)started; bufMgr, when non-nil, pools dispatched chunks. opts may be nil
// for all defaults; it is copied and defaulted, so the caller's value is not
// mutated. The manager starts no work until StartStream is called.
func NewManager(ctx context.Context, onFrame dispatchFunc, onReset func(sourceID string), log logger.Logger, bufMgr *buffer.Manager, opts *Options) *Manager {
	if log == nil {
		log = audiocore.GetLogger()
	}
	if onFrame == nil {
		// Defense-in-depth for external callers: the engine always supplies a
		// delivery callback, but a nil one would panic on the first frame.
		onFrame = func(audiocore.AudioFrame) {}
	}
	resolved := Options{}
	if opts != nil {
		resolved = *opts
	}
	resolved.applyDefaults()

	mgrCtx, cancel := context.WithCancelCause(ctx)
	return &Manager{
		ctx:     mgrCtx,
		cancel:  cancel,
		deliver: onFrame,
		onReset: onReset,
		log:     log.With(logger.String("component", "native-stream")),
		bufMgr:  bufMgr,
		opts:    resolved,
		streams: make(map[string]*stream),
	}
}

// StartStream begins capturing spec.SourceID. RTSP is the only supported type in
// this phase; any other returns ErrUnsupportedType. A duplicate ID errors.
func (m *Manager) StartStream(spec *audiocore.StreamSpec) error {
	if spec == nil {
		return errors.Newf("nil stream spec").Component("native-stream").Category(errors.CategoryValidation).Build()
	}
	if spec.SourceID == "" {
		return errors.Newf("stream spec has an empty source ID").
			Component("native-stream").Category(errors.CategoryValidation).Build()
	}
	if spec.Type != audiocore.SourceTypeRTSP {
		return fmt.Errorf("%w: %s", ErrUnsupportedType, spec.Type)
	}

	m.mu.Lock()
	if err := context.Cause(m.ctx); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("start stream %s: %w", spec.SourceID, err)
	}
	if _, exists := m.streams[spec.SourceID]; exists {
		m.mu.Unlock()
		return errors.Newf("stream %s is already running", spec.SourceID).
			Component("native-stream").Category(errors.CategoryConflict).Build()
	}
	s := newStream(spec, &m.opts, m.deliver, m.bufMgr, m.log)
	m.streams[spec.SourceID] = s
	m.mu.Unlock()

	// Fire onReset once at (re)start, matching the FFmpeg producer and the shared
	// contract's caseOnResetFires. Fire outside the lock so the callback cannot
	// deadlock against a manager method.
	m.fireReset(spec.SourceID)
	m.log.Info("started native stream",
		logger.String("source_id", spec.SourceID),
		logger.String("ingest_engine", "native"),
		logger.String("operation", "start_stream"))
	return nil
}

// StopStream stops capture for sourceID and forgets it. An unknown ID errors and
// reports the active count.
func (m *Manager) StopStream(sourceID string) error {
	m.mu.Lock()
	s, ok := m.streams[sourceID]
	if !ok {
		active := len(m.streams)
		m.mu.Unlock()
		return errors.Newf("stream %s not found (%d active)", sourceID, active).
			Component("native-stream").Category(errors.CategoryNotFound).Build()
	}
	delete(m.streams, sourceID)
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	s.close(ctx)
	// Remove the stream's metric series after it is fully closed, so a stopped
	// source stops being exported instead of lingering as stale series.
	if m.opts.Metrics != nil {
		m.opts.Metrics.DeleteStream(sourceID)
	}
	m.log.Info("stopped native stream",
		logger.String("source_id", sourceID),
		logger.String("ingest_engine", "native"),
		logger.String("operation", "stop_stream"))
	return nil
}

// StreamHealth returns a point-in-time snapshot for one stream.
func (m *Manager) StreamHealth(sourceID string) (*audiocore.StreamHealth, error) {
	m.mu.RLock()
	s, ok := m.streams[sourceID]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("native stream health: %w: %s", audiocore.ErrSourceNotFound, sourceID)
	}
	return s.snapshot(), nil
}

// AllStreamHealth returns snapshots for every tracked stream, keyed by ID.
func (m *Manager) AllStreamHealth() map[string]*audiocore.StreamHealth {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]*audiocore.StreamHealth, len(m.streams))
	for id, s := range m.streams {
		out[id] = s.snapshot()
	}
	return out
}

// GetActiveStreamIDs lists the currently tracked source IDs.
func (m *Manager) GetActiveStreamIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Collect(maps.Keys(m.streams))
}

// SetOnStreamReset registers the callback invoked after a stream starts or is
// fully reset. It applies to existing streams too, which invoke it indirectly
// through fireReset.
func (m *Manager) SetOnStreamReset(fn func(sourceID string)) {
	m.onResetMu.Lock()
	m.onReset = fn
	m.onResetMu.Unlock()
}

// Shutdown stops every stream with the manager's default timeout.
func (m *Manager) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return m.ShutdownWithContext(ctx)
}

// ShutdownWithContext stops every stream honouring ctx.
func (m *Manager) ShutdownWithContext(ctx context.Context) error {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		// Cancel under the lock BEFORE releasing it, so a StartStream that races
		// this shutdown either ran fully before the swap (and its stream is in
		// the captured set) or observes the cancelled context and is refused.
		// Cancelling after wg.Wait() would let such a StartStream store a stream
		// into the fresh map that shutdown never closes, leaking its goroutines.
		m.cancel(errManagerShutDown)
		streams := m.streams
		m.streams = make(map[string]*stream)
		m.mu.Unlock()

		var wg sync.WaitGroup
		for _, s := range streams {
			wg.Add(1)
			go func(s *stream) {
				defer wg.Done()
				s.close(ctx)
				// Remove metric series on shutdown too, matching StopStream, so a
				// manager stopped while the process/registry outlives it (engine
				// restart, tests) does not leak stale series.
				if m.opts.Metrics != nil {
					m.opts.Metrics.DeleteStream(s.spec.SourceID)
				}
			}(s)
		}
		wg.Wait()
	})
	return nil
}

// fireReset invokes the current reset callback, read under lock so
// SetOnStreamReset reaches streams started earlier.
func (m *Manager) fireReset(sourceID string) {
	m.onResetMu.RLock()
	fn := m.onReset
	m.onResetMu.RUnlock()
	if fn != nil {
		fn(sourceID)
	}
}
