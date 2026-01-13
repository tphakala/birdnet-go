package myaudio

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// Constants for stream health management
const (
	// minimumStreamRuntime is the minimum time a stream must be running before
	// it becomes eligible for health-based restarts. This prevents the manager
	// from restarting streams that are still establishing their connection or
	// experiencing temporary startup issues.
	minimumStreamRuntime = 2 * time.Minute

	// Watchdog thresholds for detecting and recovering stuck streams
	maxUnhealthyDuration  = 15 * time.Minute // Force reset after stream stuck unhealthy this long
	watchdogCheckInterval = 5 * time.Minute  // How often watchdog checks for stuck streams
	stopStartDelay        = 30 * time.Second // Wait time between stop and start during force reset
)

// getTimeSinceDataSeconds returns the time in seconds since data was last received,
// handling the case where LastDataReceived is zero (never received data).
// Returns 0 if LastDataReceived is zero to avoid confusing large numbers in logs.
func getTimeSinceDataSeconds(lastDataReceived time.Time) float64 {
	if lastDataReceived.IsZero() {
		return 0 // Never received data
	}
	return time.Since(lastDataReceived).Seconds()
}

// formatTimeSinceData returns a human-readable string for time since data was last received,
// handling the case where LastDataReceived is zero (never received data).
func formatTimeSinceData(lastDataReceived time.Time) string {
	if lastDataReceived.IsZero() {
		return "never received data"
	}
	return time.Since(lastDataReceived).String()
}

// getManagerLogger returns the logger for FFmpeg manager.
// Fetched dynamically to ensure it uses the current centralized logger.
func getManagerLogger() logger.Logger {
	return getIntegrationLogger()
}

// FFmpegManager manages all FFmpeg streams
type FFmpegManager struct {
	streams   map[string]*FFmpegStream
	streamsMu sync.RWMutex
	ctx       context.Context
	cancel    context.CancelCauseFunc // Changed to CancelCauseFunc for better diagnostics
	wg        sync.WaitGroup

	// Watchdog state tracking
	lastForceReset map[string]time.Time // Track when we last force-reset each stream
	forceResetMu   sync.Mutex

	// Audio channel reference for watchdog restarts
	// Stored when StartMonitoring() is called so watchdog can restart stuck streams
	audioChan   chan UnifiedAudioData
	audioChanMu sync.RWMutex
}

// NewFFmpegManager creates a new FFmpeg manager
func NewFFmpegManager() *FFmpegManager {
	// Use WithCancelCause for better cancellation diagnostics
	ctx, cancel := context.WithCancelCause(context.Background())
	return &FFmpegManager{
		streams:        make(map[string]*FFmpegStream),
		ctx:            ctx,
		cancel:         cancel,
		lastForceReset: make(map[string]time.Time),
	}
}

// StartStream starts a new FFmpeg stream for the given URL
func (m *FFmpegManager) StartStream(url, transport string, audioChan chan UnifiedAudioData) error {
	m.streamsMu.Lock()
	defer m.streamsMu.Unlock()

	// Check if stream already exists
	if _, exists := m.streams[url]; exists {
		return errors.New(fmt.Errorf("stream already exists for URL: %s", url)).
			Category(errors.CategoryValidation).
			Component("ffmpeg-manager").
			Context("operation", "start_stream").
			Context("url", privacy.SanitizeStreamUrl(url)).
			Context("transport", transport).
			Build()
	}

	// Create new stream first to get the source ID
	stream := NewFFmpegStream(url, transport, audioChan)

	// Initialize buffers for the stream using the source ID, not the raw URL
	if err := initializeBuffersForSource(stream.source.ID); err != nil {
		getManagerLogger().Error("failed to initialize buffers for stream",
			logger.String("url", privacy.SanitizeStreamUrl(url)),
			logger.String("sourceID", stream.source.ID),
			logger.Error(err),
			logger.String("operation", "start_stream_buffer_init"))
		return errors.New(fmt.Errorf("failed to initialize buffers: %w", err)).
			Category(errors.CategorySystem).
			Component("ffmpeg-manager").
			Context("operation", "start_stream").
			Context("url", privacy.SanitizeStreamUrl(url)).
			Context("source_id", stream.source.ID).
			Build()
	}

	// Initialize sound level processor if enabled
	if err := registerSoundLevelProcessorIfEnabled(url, getManagerLogger()); err != nil {
		getManagerLogger().Warn("sound level processor registration failed during stream start",
			logger.String("url", privacy.SanitizeStreamUrl(url)),
			logger.Error(err),
			logger.String("operation", "start_stream_sound_level_registration"))
		// Continue with stream start - provides graceful degradation
	}

	// Stream already created above
	m.streams[url] = stream

	// Start stream in goroutine
	m.wg.Go(func() {
		stream.Run(m.ctx)
	})

	getManagerLogger().Info("started FFmpeg stream",
		logger.String("url", privacy.SanitizeStreamUrl(url)),
		logger.String("transport", transport),
		logger.String("component", "ffmpeg-manager"),
		logger.String("operation", "start_stream"))

	return nil
}

// StopStream stops a specific stream
func (m *FFmpegManager) StopStream(url string) error {
	m.streamsMu.Lock()

	stream, exists := m.streams[url]
	if !exists {
		m.streamsMu.Unlock()
		return errors.New(fmt.Errorf("no stream found for URL: %s", url)).
			Category(errors.CategoryValidation).
			Component("ffmpeg-manager").
			Context("operation", "stop_stream").
			Context("url", privacy.SanitizeStreamUrl(url)).
			Context("active_streams", len(m.streams)).
			Build()
	}

	// Stop the stream and remove from map while holding lock
	stream.Stop()
	delete(m.streams, url)

	// Unregister sound level processor while holding lock
	UnregisterSoundLevelProcessor(url)
	getManagerLogger().Debug("unregistered sound level processor",
		logger.String("url", privacy.SanitizeStreamUrl(url)),
		logger.String("operation", "stop_stream"))

	// Clean up watchdog tracking for this stream to prevent memory leak
	m.forceResetMu.Lock()
	delete(m.lastForceReset, url)
	m.forceResetMu.Unlock()

	// CRITICAL: Release mutex before time.Sleep to prevent deadlock in synctest
	// Go 1.25 Knowledge: In testing/synctest, holding a mutex during time.Sleep causes deadlock
	// because goroutines waiting on the mutex are not durably blocked, preventing time advancement
	m.streamsMu.Unlock()

	// Clean up buffers for the stream
	// Wait a short time for any in-flight writes to complete
	// This sleep is now safe for synctest since mutex is released
	time.Sleep(100 * time.Millisecond)

	if err := RemoveAnalysisBuffer(url); err != nil {
		getManagerLogger().Warn("failed to remove analysis buffer",
			logger.String("url", privacy.SanitizeStreamUrl(url)),
			logger.Error(err),
			logger.String("operation", "stop_stream_buffer_cleanup"))
	}

	if err := RemoveCaptureBuffer(url); err != nil {
		getManagerLogger().Warn("failed to remove capture buffer",
			logger.String("url", privacy.SanitizeStreamUrl(url)),
			logger.Error(err),
			logger.String("operation", "stop_stream_buffer_cleanup"))
	}

	getManagerLogger().Info("stopped FFmpeg stream",
		logger.String("url", privacy.SanitizeStreamUrl(url)),
		logger.String("operation", "stop_stream"))
	return nil
}

// RestartStream restarts a specific stream
func (m *FFmpegManager) RestartStream(url string) error {
	m.streamsMu.RLock()
	stream, exists := m.streams[url]
	activeStreamCount := len(m.streams)
	m.streamsMu.RUnlock()

	if !exists {
		return errors.New(fmt.Errorf("no stream found for URL: %s", url)).
			Category(errors.CategoryValidation).
			Component("ffmpeg-manager").
			Context("operation", "restart_stream").
			Context("url", privacy.SanitizeStreamUrl(url)).
			Context("active_streams", activeStreamCount).
			Build()
	}

	// Re-register sound level processor if sound level monitoring is enabled
	// This ensures processor registration survives stream restarts
	if err := registerSoundLevelProcessorIfEnabled(url, getManagerLogger()); err != nil {
		getManagerLogger().Warn("sound level processor registration failed during stream restart",
			logger.String("url", privacy.SanitizeStreamUrl(url)),
			logger.Error(err),
			logger.String("operation", "restart_stream_sound_level_registration"))
		// Continue with stream restart even if sound level registration fails
		// This provides graceful degradation - stream functionality is preserved
	}

	stream.Restart(false) // false = automatic restart (health-triggered)

	getManagerLogger().Info("restarted FFmpeg stream",
		logger.String("url", privacy.SanitizeStreamUrl(url)),
		logger.String("operation", "restart_stream"))

	return nil
}

// GetActiveStreams returns a list of active stream URLs
func (m *FFmpegManager) GetActiveStreams() []string {
	m.streamsMu.RLock()
	defer m.streamsMu.RUnlock()

	urls := make([]string, 0, len(m.streams))
	for url := range m.streams {
		urls = append(urls, url)
	}
	return urls
}

// HealthCheck performs a health check on all streams
func (m *FFmpegManager) HealthCheck() map[string]StreamHealth {
	m.streamsMu.RLock()
	defer m.streamsMu.RUnlock()

	health := make(map[string]StreamHealth)
	for url, stream := range m.streams {
		health[url] = stream.GetHealth()
	}
	return health
}

// SyncWithConfig synchronizes running streams with configuration
func (m *FFmpegManager) SyncWithConfig(audioChan chan UnifiedAudioData) error {
	settings := conf.Setting()
	configuredURLs := make(map[string]string) // url -> transport

	// Build map of configured URLs with their per-stream transport settings
	for _, stream := range settings.Realtime.RTSP.Streams {
		configuredURLs[stream.URL] = stream.Transport
	}

	// Check for transport changes in existing streams
	// This must be done before stopping unconfigured streams to detect the change
	m.streamsMu.RLock()
	var toRestart []struct {
		url          string
		oldTransport string
		newTransport string
	}
	for url, stream := range m.streams {
		if configTransport, configured := configuredURLs[url]; configured {
			// Check if transport setting has changed for this stream
			if stream.transport != configTransport {
				toRestart = append(toRestart, struct {
					url          string
					oldTransport string
					newTransport string
				}{
					url:          url,
					oldTransport: stream.transport,
					newTransport: configTransport,
				})
			}
		}
	}
	m.streamsMu.RUnlock()

	// Restart streams with changed transport settings
	// This is done before stopping unconfigured streams to provide clear log ordering
	for _, change := range toRestart {
		getManagerLogger().Info("transport setting changed, restarting stream",
			logger.String("url", privacy.SanitizeStreamUrl(change.url)),
			logger.String("old_transport", change.oldTransport),
			logger.String("new_transport", change.newTransport),
			logger.String("component", "ffmpeg-manager"),
			logger.String("operation", "sync_transport_change"))

		// Stop stream with old transport
		// StopStream() is synchronous and includes buffer cleanup delay
		if err := m.StopStream(change.url); err != nil {
			getManagerLogger().Error("failed to stop stream for transport change",
				logger.String("url", privacy.SanitizeStreamUrl(change.url)),
				logger.String("old_transport", change.oldTransport),
				logger.String("new_transport", change.newTransport),
				logger.Error(err),
				logger.String("component", "ffmpeg-manager"),
				logger.String("operation", "sync_transport_change_stop"))
			continue
		}

		// Verify stream was fully removed from manager
		// StopStream() should have already removed it, but verify to be defensive
		m.streamsMu.RLock()
		if _, stillExists := m.streams[change.url]; stillExists {
			m.streamsMu.RUnlock()
			getManagerLogger().Error("stream still exists after StopStream",
				logger.String("url", privacy.SanitizeStreamUrl(change.url)),
				logger.String("old_transport", change.oldTransport),
				logger.String("new_transport", change.newTransport),
				logger.String("component", "ffmpeg-manager"),
				logger.String("operation", "sync_transport_change_verify"))
			continue
		}
		m.streamsMu.RUnlock()

		// Start stream with new transport
		if err := m.StartStream(change.url, change.newTransport, audioChan); err != nil {
			getManagerLogger().Error("failed to restart stream with new transport",
				logger.String("url", privacy.SanitizeStreamUrl(change.url)),
				logger.String("old_transport", change.oldTransport),
				logger.String("new_transport", change.newTransport),
				logger.Error(err),
				logger.String("component", "ffmpeg-manager"),
				logger.String("operation", "sync_transport_change_start"))
			continue
		}

		getManagerLogger().Info("restarted stream with new transport",
			logger.String("url", privacy.SanitizeStreamUrl(change.url)),
			logger.String("transport", change.newTransport),
			logger.String("operation", "sync_transport_change_complete"))
	}

	// Stop streams that are no longer configured
	m.streamsMu.RLock()
	toStop := []string{}
	for url := range m.streams {
		if _, configured := configuredURLs[url]; !configured {
			toStop = append(toStop, url)
		}
	}
	m.streamsMu.RUnlock()

	for _, url := range toStop {
		if err := m.StopStream(url); err != nil {
			getManagerLogger().Warn("failed to stop unconfigured stream",
				logger.String("url", privacy.SanitizeStreamUrl(url)),
				logger.Error(err),
				logger.String("component", "ffmpeg-manager"),
				logger.String("operation", "sync_with_config"))
		}
	}

	// Start streams that are configured but not running
	for url, transport := range configuredURLs {
		m.streamsMu.RLock()
		_, running := m.streams[url]
		m.streamsMu.RUnlock()

		if !running {
			if err := m.StartStream(url, transport, audioChan); err != nil {
				getManagerLogger().Warn("failed to start configured stream",
					logger.String("url", privacy.SanitizeStreamUrl(url)),
					logger.Error(err),
					logger.String("transport", transport),
					logger.String("component", "ffmpeg-manager"),
					logger.String("operation", "sync_with_config"))
			}
		}
	}

	return nil
}

// StartMonitoring starts periodic monitoring of streams with health checks and watchdog.
// The audioChan parameter is stored for use by the watchdog when force-restarting stuck streams.
func (m *FFmpegManager) StartMonitoring(interval time.Duration, audioChan chan UnifiedAudioData) {
	// Validate audioChan is provided - watchdog requires it for force-restarting streams
	if audioChan == nil {
		getManagerLogger().Error("cannot start monitoring - audioChan is nil",
			logger.String("component", "ffmpeg-manager"),
			logger.String("operation", "start_monitoring"))
		return
	}

	// Store audioChan reference for watchdog use
	m.audioChanMu.Lock()
	m.audioChan = audioChan
	m.audioChanMu.Unlock()

	m.wg.Add(2) // Starting 2 goroutines: health check + watchdog

	// Health check goroutine (existing functionality)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		getManagerLogger().Info("started health check monitoring",
			logger.Float64("interval_seconds", interval.Seconds()),
			logger.String("component", "ffmpeg-manager"),
			logger.String("operation", "start_monitoring"))

		for {
			select {
			case <-m.ctx.Done():
				getManagerLogger().Info("stopping health check monitoring",
					logger.String("component", "ffmpeg-manager"),
					logger.String("operation", "stop_monitoring"))
				return
			case <-ticker.C:
				m.checkStreamHealth()
			}
		}
	}()

	// Watchdog goroutine (new - detects and recovers stuck streams)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(watchdogCheckInterval)
		defer ticker.Stop()

		getManagerLogger().Info("started watchdog monitoring for stuck streams",
			logger.Float64("interval_seconds", watchdogCheckInterval.Seconds()),
			logger.Float64("max_unhealthy_duration_seconds", maxUnhealthyDuration.Seconds()),
			logger.String("component", "ffmpeg-manager"),
			logger.String("operation", "start_watchdog"))

		for {
			select {
			case <-m.ctx.Done():
				getManagerLogger().Info("stopping watchdog monitoring",
					logger.String("component", "ffmpeg-manager"),
					logger.String("operation", "stop_watchdog"))
				return
			case <-ticker.C:
				m.checkForStuckStreams()
			}
		}
	}()
}

// checkStreamHealth checks health of all streams
func (m *FFmpegManager) checkStreamHealth() {
	health := m.HealthCheck()

	if conf.Setting().Debug {
		getManagerLogger().Debug("performing health check on all streams",
			logger.Int("stream_count", len(health)),
			logger.String("operation", "check_stream_health"))
	}

	for url := range health {
		h := health[url]
		if !h.IsHealthy {
			// Get the stream to check if it's already restarting
			m.streamsMu.RLock()
			stream, exists := m.streams[url]
			m.streamsMu.RUnlock()

			// Skip if stream doesn't exist (shouldn't happen but be defensive)
			if !exists {
				getManagerLogger().Debug("unhealthy stream not found in streams map",
					logger.String("url", privacy.SanitizeStreamUrl(url)),
					logger.String("operation", "health_check"))
				continue
			}

			// Check if stream is already in the process of restarting
			if stream.IsRestarting() {
				if conf.Setting().Debug {
					getManagerLogger().Debug("skipping restart for stream already in restart process",
						logger.String("url", privacy.SanitizeStreamUrl(url)),
						logger.Float64("last_data_ago_seconds", getTimeSinceDataSeconds(h.LastDataReceived)),
						logger.Int("restart_count", h.RestartCount),
						logger.String("operation", "health_check_skip_restart"))
				}
				continue // Don't interfere with ongoing restart/backoff
			}

			// Check if stream is too new to restart (give it time to establish)
			processStartTime := stream.GetProcessStartTime()
			if !processStartTime.IsZero() {
				timeSinceStart := time.Since(processStartTime)
				if timeSinceStart < minimumStreamRuntime {
					if conf.Setting().Debug {
						getManagerLogger().Debug("skipping restart for new stream still establishing",
							logger.String("url", privacy.SanitizeStreamUrl(url)),
							logger.Float64("runtime_seconds", timeSinceStart.Seconds()),
							logger.Float64("minimum_runtime_seconds", minimumStreamRuntime.Seconds()),
							logger.Float64("last_data_ago_seconds", getTimeSinceDataSeconds(h.LastDataReceived)),
							logger.String("operation", "health_check_skip_new_stream"))
					}
					continue // Give new streams time to stabilize
				}
			}

			getManagerLogger().Warn("unhealthy stream detected",
				logger.String("url", privacy.SanitizeStreamUrl(url)),
				logger.Float64("last_data_ago_seconds", getTimeSinceDataSeconds(h.LastDataReceived)),
				logger.Int("restart_count", h.RestartCount),
				logger.Bool("is_receiving_data", h.IsReceivingData),
				logger.Float64("bytes_per_second", h.BytesPerSecond),
				logger.Int64("total_bytes", h.TotalBytesReceived),
				logger.String("operation", "health_check"))

			// Restart unhealthy streams
			if conf.Setting().Debug {
				getManagerLogger().Debug("attempting to restart unhealthy stream",
					logger.String("url", privacy.SanitizeStreamUrl(url)),
					logger.String("operation", "health_check_restart_attempt"))
			}

			if err := m.RestartStream(url); err != nil {
				getManagerLogger().Error("failed to restart unhealthy stream",
					logger.String("url", privacy.SanitizeStreamUrl(url)),
					logger.Error(err),
					logger.String("operation", "health_check_restart"))

				// Report to Sentry with enhanced context
				errorWithContext := errors.New(err).
					Component("ffmpeg-manager").
					Category(errors.CategoryRTSP).
					Context("operation", "health_check_restart").
					Context("url", privacy.SanitizeStreamUrl(url)).
					Context("last_data_seconds_ago", getTimeSinceDataSeconds(h.LastDataReceived)).
					Context("restart_count", h.RestartCount).
					Context("health_status", "unhealthy").
					Build()
				// This will be reported via event bus if telemetry is enabled
				_ = errorWithContext
			} else if conf.Setting().Debug {
				getManagerLogger().Debug("successfully initiated restart for unhealthy stream",
					logger.String("url", privacy.SanitizeStreamUrl(url)),
					logger.String("operation", "health_check_restart_success"))
			}
		} else if conf.Setting().Debug {
			// Get current PID for the stream
			var currentPID int
			m.streamsMu.RLock()
			if stream, exists := m.streams[url]; exists {
				stream.cmdMu.Lock()
				if stream.cmd != nil && stream.cmd.Process != nil {
					currentPID = stream.cmd.Process.Pid
				}
				stream.cmdMu.Unlock()
			}
			m.streamsMu.RUnlock()

			// Log healthy streams at debug level
			getManagerLogger().Debug("stream is healthy",
				logger.String("url", privacy.SanitizeStreamUrl(url)),
				logger.Int("pid", currentPID),
				logger.Bool("is_receiving_data", h.IsReceivingData),
				logger.Float64("bytes_per_second", h.BytesPerSecond),
				logger.Float64("last_data_ago_seconds", getTimeSinceDataSeconds(h.LastDataReceived)),
				logger.String("operation", "health_check_healthy"))
		}
	}
}

// checkForStuckStreams detects streams stuck in unhealthy states and forces reset.
// This is the watchdog that runs periodically to recover streams that the normal
// health check and restart mechanisms couldn't fix.
func (m *FFmpegManager) checkForStuckStreams() {
	health := m.HealthCheck()
	now := time.Now()

	if conf.Setting().Debug {
		getManagerLogger().Debug("watchdog checking for stuck streams",
			logger.Int("total_streams", len(health)),
			logger.String("operation", "watchdog_check"))
	}

	for url := range health {
		h := health[url]
		// Skip healthy streams
		if h.IsHealthy {
			continue
		}

		// Check if stream is already restarting (health check may be handling it)
		// This prevents watchdog from interfering with ongoing health check restarts
		m.streamsMu.RLock()
		stream, exists := m.streams[url]
		m.streamsMu.RUnlock()

		if !exists {
			continue
		}

		if stream.IsRestarting() {
			if conf.Setting().Debug {
				getManagerLogger().Debug("skipping watchdog check - stream already restarting",
					logger.String("url", privacy.SanitizeStreamUrl(url)),
					logger.String("operation", "watchdog_check_skip_restarting"))
			}
			continue
		}

		// Calculate how long stream has been unhealthy
		// We already have the stream from the IsRestarting() check above
		var unhealthyDuration time.Duration
		if h.LastDataReceived.IsZero() {
			// Never received data - use stream creation time
			unhealthyDuration = time.Since(stream.streamCreatedAt)
		} else {
			unhealthyDuration = time.Since(h.LastDataReceived)
		}

		// Check if exceeded threshold
		if unhealthyDuration < maxUnhealthyDuration {
			if conf.Setting().Debug {
				getManagerLogger().Debug("unhealthy stream not yet at watchdog threshold",
					logger.String("url", privacy.SanitizeStreamUrl(url)),
					logger.Float64("unhealthy_duration_seconds", unhealthyDuration.Seconds()),
					logger.Float64("threshold_seconds", maxUnhealthyDuration.Seconds()),
					logger.Float64("remaining_seconds", (maxUnhealthyDuration-unhealthyDuration).Seconds()),
					logger.String("operation", "watchdog_check"))
			}
			continue
		}

		// Check cooldown to prevent rapid force-resets
		// We use maxUnhealthyDuration as the cooldown period to ensure we don't
		// force-reset the same stream more than once per unhealthy period.
		// This prevents watchdog thrashing when a stream is persistently broken.
		// IMPORTANT: We claim the reset slot immediately to prevent race conditions
		// where multiple checks could pass the cooldown test simultaneously.
		m.forceResetMu.Lock()
		lastReset, exists := m.lastForceReset[url]
		if exists && time.Since(lastReset) < maxUnhealthyDuration {
			m.forceResetMu.Unlock()
			if conf.Setting().Debug {
				getManagerLogger().Debug("skipping force reset due to cooldown",
					logger.String("url", privacy.SanitizeStreamUrl(url)),
					logger.Float64("time_since_last_reset_seconds", time.Since(lastReset).Seconds()),
					logger.Float64("cooldown_seconds", maxUnhealthyDuration.Seconds()),
					logger.String("operation", "watchdog_cooldown"))
			}
			continue
		}
		// Claim the reset slot immediately to prevent concurrent resets
		m.lastForceReset[url] = now
		m.forceResetMu.Unlock()

		// Get transport and audioChan before stopping
		m.streamsMu.RLock()
		stream, streamExists := m.streams[url]
		transport := ""
		if streamExists {
			transport = stream.transport
		}
		m.streamsMu.RUnlock()

		// Get audioChan reference
		m.audioChanMu.RLock()
		audioChan := m.audioChan
		m.audioChanMu.RUnlock()

		if !streamExists {
			getManagerLogger().Warn("stream disappeared during watchdog check",
				logger.String("url", privacy.SanitizeStreamUrl(url)),
				logger.String("operation", "watchdog_stream_missing"))
			continue
		}

		if audioChan == nil {
			getManagerLogger().Error("cannot restart stuck stream - no audioChan available",
				logger.String("url", privacy.SanitizeStreamUrl(url)),
				logger.String("operation", "watchdog_no_audiochan"))
			continue
		}

		// Force full reset
		getManagerLogger().Error("stream stuck unhealthy, forcing full reset",
			logger.String("url", privacy.SanitizeStreamUrl(url)),
			logger.Float64("unhealthy_duration_seconds", unhealthyDuration.Seconds()),
			logger.Float64("threshold_seconds", maxUnhealthyDuration.Seconds()),
			logger.String("last_data", formatTimeSinceData(h.LastDataReceived)),
			logger.Int("restart_count", h.RestartCount),
			logger.String("process_state", h.ProcessState.String()),
			logger.String("component", "ffmpeg-manager"),
			logger.String("operation", "watchdog_force_reset"))

		// Stop stream completely
		if err := m.StopStream(url); err != nil {
			getManagerLogger().Error("failed to stop stuck stream",
				logger.String("url", privacy.SanitizeStreamUrl(url)),
				logger.Error(err),
				logger.String("operation", "watchdog_stop"))
			continue
		}

		// Verify stream was removed - if not, force-remove it to ensure clean state
		m.streamsMu.Lock()
		if _, stillExists := m.streams[url]; stillExists {
			getManagerLogger().Warn("stream still exists after watchdog stop, force-removing entry",
				logger.String("url", privacy.SanitizeStreamUrl(url)),
				logger.String("operation", "watchdog_stop_verification"))

			// Force-remove the stream entry to ensure clean state
			delete(m.streams, url)

			getManagerLogger().Info("force-removed stuck stream entry",
				logger.String("url", privacy.SanitizeStreamUrl(url)),
				logger.String("operation", "watchdog_force_cleanup"))
		}
		m.streamsMu.Unlock()

		// Wait for cleanup to complete
		time.Sleep(stopStartDelay)

		// Restart stream with fresh state
		if err := m.StartStream(url, transport, audioChan); err != nil {
			getManagerLogger().Error("failed to restart stuck stream after watchdog stop",
				logger.String("url", privacy.SanitizeStreamUrl(url)),
				logger.String("transport", transport),
				logger.Error(err),
				logger.String("operation", "watchdog_restart"))
			continue
		}

		getManagerLogger().Info("watchdog successfully force-reset stuck stream",
			logger.String("url", privacy.SanitizeStreamUrl(url)),
			logger.String("transport", transport),
			logger.Float64("unhealthy_duration_seconds", unhealthyDuration.Seconds()),
			logger.String("operation", "watchdog_reset_complete"))
	}
}

// Shutdown gracefully shuts down all streams
func (m *FFmpegManager) Shutdown() {
	start := time.Now()

	// Get active stream count safely
	m.streamsMu.RLock()
	activeStreams := len(m.streams)
	m.streamsMu.RUnlock()

	getManagerLogger().Info("shutting down FFmpeg manager",
		logger.Int("active_streams", activeStreams),
		logger.String("operation", "shutdown"))

	// Cancel context to signal shutdown with reason
	m.cancel(fmt.Errorf("FFmpegManager: shutdown initiated"))

	// Stop all streams
	m.streamsMu.Lock()
	urls := make([]string, 0, len(m.streams))
	for url := range m.streams {
		urls = append(urls, url)
	}
	m.streamsMu.Unlock()

	// Stop each stream using StopStream which handles unregistration
	for _, url := range urls {
		if err := m.StopStream(url); err != nil {
			getManagerLogger().Warn("failed to stop stream during shutdown",
				logger.String("url", privacy.SanitizeStreamUrl(url)),
				logger.Error(err),
				logger.String("operation", "shutdown"))
		}
	}

	// Wait for all goroutines to finish
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	// Wait with timeout
	select {
	case <-done:
		getManagerLogger().Info("FFmpeg manager shutdown complete",
			logger.Int64("duration_ms", time.Since(start).Milliseconds()),
			logger.Int("stopped_streams", activeStreams),
			logger.String("operation", "shutdown"))
	case <-time.After(30 * time.Second):
		getManagerLogger().Warn("FFmpeg manager shutdown timeout",
			logger.Int64("duration_ms", time.Since(start).Milliseconds()),
			logger.Int("active_streams", activeStreams),
			logger.String("operation", "shutdown"))
	}
}
