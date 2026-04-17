package ffmpeg

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
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// Manager constants for watchdog and stream health management.
const (
	// managerMinStreamRuntime is the minimum time a stream must be running before
	// it is eligible for health-based restarts. This prevents restarting streams
	// that are still establishing their connection or experiencing startup issues.
	managerMinStreamRuntime = 2 * time.Minute

	// managerMaxUnhealthyDuration is the time a stream must be continuously unhealthy
	// before the watchdog forces a full reset.
	managerMaxUnhealthyDuration = 15 * time.Minute

	// managerWatchdogInterval is how often the watchdog checks for stuck streams.
	managerWatchdogInterval = 5 * time.Minute

	// managerStopStartDelay is the wait time between stop and start during a forced reset.
	managerStopStartDelay = 30 * time.Second

	// managerShutdownTimeout is the default graceful shutdown timeout.
	managerShutdownTimeout = 30 * time.Second
)

// FrameCallback is invoked for each chunk of audio data received from a stream.
// sourceID identifies the stream and data contains the raw audio bytes.
// FrameCallback is invoked by a Stream for every chunk of audio data received.
// The AudioFrame is fully populated with source metadata (ID, name, sample rate,
// bit depth, channels, timestamp).
type FrameCallback func(frame audiocore.AudioFrame)

// Manager orchestrates multiple FFmpeg streams.
// It maintains a map of sourceID → *Stream, provides Start/Stop/Restart
// for individual streams, runs a watchdog goroutine to detect stuck streams,
// and coordinates graceful shutdown of all streams.
type Manager struct {
	streams   map[string]*Stream
	mu        sync.RWMutex
	ctx       context.Context
	cancel    context.CancelCauseFunc
	wg        sync.WaitGroup
	onFrame   FrameCallback
	onFrameMu sync.RWMutex
	onReset   func(sourceID string)
	onResetMu sync.RWMutex
	logger    logger.Logger
	metrics   audiocore.StreamMetrics // optional, nil-safe

	// Optional buffer manager forwarded to each Stream so stdout reads can
	// pool their read buffers via FrameRef. May be nil (legacy allocation).
	bufMgr *buffer.Manager

	// Watchdog state: tracks when each stream was last force-reset.
	lastForceReset   map[string]time.Time
	lastForceResetMu sync.Mutex
}

// NewManager creates a new Manager.
//
// onFrame is invoked for each chunk of audio data received from any managed
// stream, allowing the caller to route data into analysis buffers. It may be nil,
// but a nil callback means all audio data will be silently discarded.
//
// onReset is called after a stream starts (or is restarted by the watchdog),
// allowing the analysis layer to ensure a buffer monitor is running for the
// given sourceID. It may be nil.
//
// log is used for structured logging. If nil, the audiocore module logger is used.
//
// bufMgr is an optional buffer manager forwarded to each Stream; when non-nil,
// stdout reads borrow their read buffer from a size-specific BytePool and
// attach a FrameRef whose release closure returns the slice. When nil, streams
// fall back to per-iteration allocation.
func NewManager(ctx context.Context, onFrame FrameCallback, onReset func(sourceID string), log logger.Logger, bufMgr *buffer.Manager) *Manager {
	if log == nil {
		log = audiocore.GetLogger()
	}
	mgrCtx, cancel := context.WithCancelCause(ctx)
	return &Manager{
		streams:        make(map[string]*Stream),
		ctx:            mgrCtx,
		cancel:         cancel,
		onFrame:        onFrame,
		onReset:        onReset,
		logger:         log,
		bufMgr:         bufMgr,
		lastForceReset: make(map[string]time.Time),
	}
}

// SetOnFrame registers a callback invoked for each chunk of audio data
// received from any managed stream. Thread-safe — can be called while the
// manager is running.
func (m *Manager) SetOnFrame(fn FrameCallback) {
	m.onFrameMu.Lock()
	defer m.onFrameMu.Unlock()
	m.onFrame = fn
}

// SetOnStreamReset registers a callback invoked after a stream starts or is
// force-reset by the watchdog. The callback receives the sourceID of the
// newly-started stream. Thread-safe — can be called while the manager is running.
func (m *Manager) SetOnStreamReset(fn func(sourceID string)) {
	m.onResetMu.Lock()
	defer m.onResetMu.Unlock()
	m.onReset = fn
}

// StartStream starts a new FFmpeg stream using the given config.
// cfg.SourceID is used as the map key; it must be non-empty and unique.
// The per-stream onFrame callback is taken from the manager's onFrame field,
// which is set via the constructor or SetOnFrame.
func (m *Manager) StartStream(cfg *StreamConfig) error {
	if cfg == nil {
		return errors.Newf("StreamConfig must not be nil").
			Category(errors.CategoryValidation).
			Component("ffmpeg-manager").
			Context("operation", "start_stream").
			Build()
	}
	if cfg.SourceID == "" {
		return errors.Newf("sourceID must not be empty").
			Category(errors.CategoryValidation).
			Component("ffmpeg-manager").
			Context("operation", "start_stream").
			Build()
	}

	// Hold the lock only for the map check and insertion.
	stream := NewStream(cfg, m.dispatchFrame, m.notifyReset, m.metrics, m.bufMgr)

	m.mu.Lock()
	if _, exists := m.streams[cfg.SourceID]; exists {
		m.mu.Unlock()
		return errors.Newf("stream already exists for sourceID: %s", cfg.SourceID).
			Category(errors.CategoryValidation).
			Component("ffmpeg-manager").
			Context("operation", "start_stream").
			Context("source_id", cfg.SourceID).
			Context("url", privacy.SanitizeStreamUrl(cfg.URL)).
			Build()
	}
	m.streams[cfg.SourceID] = stream
	m.mu.Unlock()

	// Launch goroutine and notify callback outside the lock.
	m.wg.Go(func() {
		stream.Run(m.ctx)
	})

	m.logger.Info("started FFmpeg stream",
		logger.String("source_id", cfg.SourceID),
		logger.String("url", privacy.SanitizeStreamUrl(cfg.URL)),
		logger.String("transport", cfg.Transport),
		logger.String("component", "ffmpeg-manager"),
		logger.String("operation", "start_stream"))

	// Notify analysis layer that a new source is available.
	m.onResetMu.RLock()
	cb := m.onReset
	m.onResetMu.RUnlock()
	if cb != nil {
		cb(cfg.SourceID)
	}

	return nil
}

// dispatchFrame is the per-stream onFrame callback. It forwards a fully-populated
// AudioFrame to the manager-level onFrame callback.
func (m *Manager) dispatchFrame(frame audiocore.AudioFrame) { //nolint:gocritic // hugeParam: matches AudioDispatcher/FrameCallback contract
	m.onFrameMu.RLock()
	cb := m.onFrame
	m.onFrameMu.RUnlock()
	if cb != nil {
		cb(frame)
	}
}

// notifyReset is the per-stream onReset callback. It forwards the sourceID to the
// manager-level onReset callback registered via SetOnStreamReset.
func (m *Manager) notifyReset(sourceID string) {
	m.onResetMu.RLock()
	cb := m.onReset
	m.onResetMu.RUnlock()
	if cb != nil {
		cb(sourceID)
	}
}

// StopStream stops the stream identified by sourceID and removes it from the map.
func (m *Manager) StopStream(sourceID string) error {
	m.mu.Lock()
	stream, exists := m.streams[sourceID]
	if !exists {
		activeCount := len(m.streams)
		m.mu.Unlock()
		return errors.Newf("no stream found for sourceID: %s", sourceID).
			Category(errors.CategoryValidation).
			Component("ffmpeg-manager").
			Context("operation", "stop_stream").
			Context("source_id", sourceID).
			Context("active_streams", activeCount).
			Build()
	}
	delete(m.streams, sourceID)
	m.mu.Unlock()

	// Stop the stream outside the lock — Stop() can block.
	stream.Stop()

	// Clean up watchdog tracking.
	m.lastForceResetMu.Lock()
	delete(m.lastForceReset, sourceID)
	m.lastForceResetMu.Unlock()

	m.logger.Info("stopped FFmpeg stream",
		logger.String("source_id", sourceID),
		logger.String("operation", "stop_stream"),
		logger.String("component", "ffmpeg-manager"))

	return nil
}

// RestartStream requests an in-place restart of the stream identified by sourceID.
// The stream remains in the map; only the underlying FFmpeg process is restarted.
func (m *Manager) RestartStream(sourceID string) error {
	m.mu.RLock()
	stream, exists := m.streams[sourceID]
	activeCount := len(m.streams)
	m.mu.RUnlock()

	if !exists {
		return errors.Newf("no stream found for sourceID: %s", sourceID).
			Category(errors.CategoryValidation).
			Component("ffmpeg-manager").
			Context("operation", "restart_stream").
			Context("source_id", sourceID).
			Context("active_streams", activeCount).
			Build()
	}

	stream.Restart(true)

	m.logger.Info("restarted FFmpeg stream",
		logger.String("source_id", sourceID),
		logger.String("operation", "restart_stream"),
		logger.String("component", "ffmpeg-manager"))

	return nil
}

// ReconfigureStream stops the existing stream for sourceID and starts a new one
// using the provided config. The sourceID in cfg must match the one supplied as
// the first argument.
func (m *Manager) ReconfigureStream(sourceID string, cfg *StreamConfig) error {
	if cfg == nil {
		return errors.Newf("StreamConfig must not be nil").
			Category(errors.CategoryValidation).
			Component("ffmpeg-manager").
			Context("operation", "reconfigure_stream").
			Build()
	}
	if sourceID != cfg.SourceID {
		return errors.Newf("sourceID mismatch: argument %q != cfg.SourceID %q", sourceID, cfg.SourceID).
			Category(errors.CategoryValidation).
			Component("ffmpeg-manager").
			Context("operation", "reconfigure_stream").
			Build()
	}

	if err := m.StopStream(sourceID); err != nil {
		return errors.New(err).
			Component("ffmpeg-manager").
			Context("operation", "reconfigure_stream_stop").
			Build()
	}

	if err := m.StartStream(cfg); err != nil {
		return errors.New(err).
			Component("ffmpeg-manager").
			Context("operation", "reconfigure_stream_start").
			Build()
	}

	m.logger.Info("reconfigured FFmpeg stream",
		logger.String("source_id", sourceID),
		logger.String("url", privacy.SanitizeStreamUrl(cfg.URL)),
		logger.String("transport", cfg.Transport),
		logger.String("component", "ffmpeg-manager"),
		logger.String("operation", "reconfigure_stream"))

	return nil
}

// StreamHealth returns health information for the stream identified by sourceID.
func (m *Manager) StreamHealth(sourceID string) (*StreamHealth, error) {
	m.mu.RLock()
	stream, exists := m.streams[sourceID]
	m.mu.RUnlock()

	if !exists {
		return nil, errors.Newf("no stream found for sourceID: %s", sourceID).
			Category(errors.CategoryValidation).
			Component("ffmpeg-manager").
			Context("operation", "stream_health").
			Context("source_id", sourceID).
			Build()
	}

	h := stream.GetHealth()
	return &h, nil
}

// AllStreamHealth returns health information for all active streams.
func (m *Manager) AllStreamHealth() map[string]*StreamHealth {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*StreamHealth, len(m.streams))
	for id, stream := range m.streams {
		h := stream.GetHealth()
		result[id] = &h
	}
	return result
}

// GetActiveStreamIDs returns the sourceIDs of all currently tracked streams.
func (m *Manager) GetActiveStreamIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Collect(maps.Keys(m.streams))
}

// Shutdown gracefully stops all streams and waits for goroutines to finish,
// with a default 30-second timeout.
func (m *Manager) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), managerShutdownTimeout)
	defer cancel()
	return m.ShutdownWithContext(ctx)
}

// ShutdownWithContext gracefully stops all streams respecting the provided
// context deadline instead of the default 30-second timeout.
func (m *Manager) ShutdownWithContext(ctx context.Context) error {
	start := time.Now()

	m.mu.RLock()
	activeStreams := len(m.streams)
	m.mu.RUnlock()

	m.logger.Info("shutting down FFmpeg manager",
		logger.Int("active_streams", activeStreams),
		logger.String("component", "ffmpeg-manager"),
		logger.String("operation", "shutdown"))

	// Cancel the manager context so all stream goroutines exit.
	m.cancel(fmt.Errorf("Manager: shutdown initiated"))

	// Collect keys before iterating to avoid holding the lock while calling StopStream.
	m.mu.Lock()
	sourceIDs := slices.Collect(maps.Keys(m.streams))
	m.mu.Unlock()

	for i, id := range sourceIDs {
		if ctx.Err() != nil {
			m.logger.Warn("skipping remaining stream stops — context expired",
				logger.Int("remaining", len(sourceIDs)-i),
				logger.String("component", "ffmpeg-manager"),
				logger.String("operation", "shutdown"))
			break
		}
		if err := m.StopStream(id); err != nil {
			m.logger.Warn("failed to stop stream during shutdown",
				logger.String("source_id", id),
				logger.Error(err),
				logger.String("component", "ffmpeg-manager"),
				logger.String("operation", "shutdown"))
		}
	}

	// Wait for all goroutines to finish.
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		m.logger.Info("FFmpeg manager shutdown complete",
			logger.Int64("duration_ms", time.Since(start).Milliseconds()),
			logger.Int("stopped_streams", activeStreams),
			logger.String("component", "ffmpeg-manager"),
			logger.String("operation", "shutdown"))
		return nil
	case <-ctx.Done():
		m.logger.Warn("FFmpeg manager shutdown timed out",
			logger.Int64("duration_ms", time.Since(start).Milliseconds()),
			logger.Int("active_streams", activeStreams),
			logger.String("component", "ffmpeg-manager"),
			logger.String("operation", "shutdown"))
		return ctx.Err()
	}
}

// StartWatchdog starts a background goroutine that periodically checks for
// streams that have been stuck unhealthy longer than managerMaxUnhealthyDuration
// and forces a full stop/start cycle to recover them.
//
// The manager context cancellation stops the watchdog automatically.
func (m *Manager) StartWatchdog() {
	m.wg.Go(func() {
		ticker := time.NewTicker(managerWatchdogInterval)
		defer ticker.Stop()

		m.logger.Info("started watchdog",
			logger.Float64("interval_seconds", managerWatchdogInterval.Seconds()),
			logger.Float64("max_unhealthy_seconds", managerMaxUnhealthyDuration.Seconds()),
			logger.String("component", "ffmpeg-manager"),
			logger.String("operation", "start_watchdog"))

		for {
			select {
			case <-m.ctx.Done():
				m.logger.Info("watchdog stopped",
					logger.String("component", "ffmpeg-manager"),
					logger.String("operation", "watchdog_stop"))
				return
			case <-ticker.C:
				m.checkForStuckStreams()
			}
		}
	})
}

// checkForStuckStreams inspects all active streams and force-resets those that
// have been continuously unhealthy longer than managerMaxUnhealthyDuration.
func (m *Manager) checkForStuckStreams() {
	health := m.AllStreamHealth()
	now := time.Now()

	for id, h := range health {
		if h.IsHealthy {
			continue
		}

		m.mu.RLock()
		stream, exists := m.streams[id]
		m.mu.RUnlock()
		if !exists {
			continue
		}

		if stream.IsRestarting() {
			continue
		}

		// Determine how long the stream has been unhealthy.
		var unhealthyFor time.Duration
		if h.LastDataReceived.IsZero() {
			stream.streamCreatedAtMu.RLock()
			createdAt := stream.streamCreatedAt
			stream.streamCreatedAtMu.RUnlock()
			unhealthyFor = time.Since(createdAt)
		} else {
			unhealthyFor = time.Since(h.LastDataReceived)
		}

		if unhealthyFor < managerMaxUnhealthyDuration {
			continue
		}

		// Claim the reset slot to prevent concurrent resets of the same stream.
		m.lastForceResetMu.Lock()
		if last, seen := m.lastForceReset[id]; seen && time.Since(last) < managerMaxUnhealthyDuration {
			m.lastForceResetMu.Unlock()
			continue
		}
		m.lastForceReset[id] = now
		m.lastForceResetMu.Unlock()

		// Capture the config before stopping the stream.
		m.mu.RLock()
		liveStream, stillExists := m.streams[id]
		m.mu.RUnlock()
		if !stillExists {
			continue
		}

		// Copy the config so the pointer we pass to StartStream is independent of liveStream.
		cfgCopy := liveStream.config

		m.logger.Error("stream stuck unhealthy, forcing full reset",
			logger.String("source_id", id),
			logger.String("url", privacy.SanitizeStreamUrl(cfgCopy.URL)),
			logger.Float64("unhealthy_seconds", unhealthyFor.Seconds()),
			logger.Int("restart_count", h.RestartCount),
			logger.String("process_state", h.ProcessState.String()),
			logger.String("component", "ffmpeg-manager"),
			logger.String("operation", "watchdog_force_reset"))

		if err := m.StopStream(id); err != nil {
			m.logger.Error("watchdog: failed to stop stuck stream",
				logger.String("source_id", id),
				logger.Error(err),
				logger.String("component", "ffmpeg-manager"),
				logger.String("operation", "watchdog_stop"))
			_ = errors.New(err).
				Component("ffmpeg-manager").
				Category(errors.CategoryRTSP).
				Context("operation", "watchdog_stop_failed").
				Context("source_id", id).
				Context("restart_count", h.RestartCount).
				Build()
			continue
		}

		// Brief delay between stop and start to allow OS resources to be released.
		// Use select so shutdown can cancel the wait.
		timer := time.NewTimer(managerStopStartDelay)
		select {
		case <-timer.C:
			// Delay elapsed, proceed with restart.
		case <-m.ctx.Done():
			timer.Stop()
			return
		}

		if err := m.StartStream(&cfgCopy); err != nil {
			m.logger.Error("watchdog: failed to restart stuck stream",
				logger.String("source_id", id),
				logger.String("url", privacy.SanitizeStreamUrl(cfgCopy.URL)),
				logger.Error(err),
				logger.String("component", "ffmpeg-manager"),
				logger.String("operation", "watchdog_restart"))
			_ = errors.New(err).
				Component("ffmpeg-manager").
				Category(errors.CategoryRTSP).
				Context("operation", "watchdog_restart_failed").
				Context("source_id", id).
				Context("restart_count", h.RestartCount).
				Build()
			continue
		}

		m.logger.Info("watchdog successfully force-reset stuck stream",
			logger.String("source_id", id),
			logger.Float64("unhealthy_seconds", unhealthyFor.Seconds()),
			logger.String("component", "ffmpeg-manager"),
			logger.String("operation", "watchdog_reset_complete"))

		// Report successful watchdog force-reset to Sentry for visibility.
		_ = errors.Newf("ffmpeg watchdog forced stream reset after %v unhealthy", unhealthyFor).
			Component("ffmpeg-manager").
			Category(errors.CategoryRTSP).
			Context("operation", "watchdog_reset").
			Context("source_id", id).
			Context("restart_count", h.RestartCount).
			Build()
	}
}
