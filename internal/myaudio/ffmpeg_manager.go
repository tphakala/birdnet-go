package myaudio

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// Constants for stream health management
const (
	// minimumStreamRuntime is the minimum time a stream must be running before
	// it becomes eligible for health-based restarts. This prevents the manager
	// from restarting streams that are still establishing their connection or
	// experiencing temporary startup issues.
	minimumStreamRuntime = 2 * time.Minute
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

// Use shared logger from integration file
var managerLogger *slog.Logger

func init() {
	// Use the shared integration logger for consistency
	managerLogger = integrationLogger
}

// FFmpegManager manages all FFmpeg streams
type FFmpegManager struct {
	streams   map[string]*FFmpegStream
	streamsMu sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// NewFFmpegManager creates a new FFmpeg manager
func NewFFmpegManager() *FFmpegManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &FFmpegManager{
		streams: make(map[string]*FFmpegStream),
		ctx:     ctx,
		cancel:  cancel,
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
			Context("url", privacy.SanitizeRTSPUrl(url)).
			Context("transport", transport).
			Build()
	}

	// Create new stream first to get the source ID
	stream := NewFFmpegStream(url, transport, audioChan)

	// Initialize buffers for the stream using the source ID, not the raw URL
	if err := initializeBuffersForSource(stream.source.ID); err != nil {
		managerLogger.Error("failed to initialize buffers for stream",
			"url", privacy.SanitizeRTSPUrl(url),
			"sourceID", stream.source.ID,
			"error", err,
			"operation", "start_stream_buffer_init")
		return errors.New(fmt.Errorf("failed to initialize buffers: %w", err)).
			Category(errors.CategorySystem).
			Component("ffmpeg-manager").
			Context("operation", "start_stream").
			Context("url", privacy.SanitizeRTSPUrl(url)).
			Context("source_id", stream.source.ID).
			Build()
	}

	// Initialize sound level processor if enabled
	if err := registerSoundLevelProcessorIfEnabled(url, managerLogger); err != nil {
		managerLogger.Warn("sound level processor registration failed during stream start",
			"url", privacy.SanitizeRTSPUrl(url),
			"error", err,
			"operation", "start_stream_sound_level_registration")
		log.Printf("⚠️ Warning: Sound level processor registration failed for %s: %v",
			privacy.SanitizeRTSPUrl(url), err)
		// Continue with stream start - provides graceful degradation
	}

	// Stream already created above
	m.streams[url] = stream

	// Start stream in goroutine
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		stream.Run(m.ctx)
	}()

	managerLogger.Info("started FFmpeg stream",
		"url", privacy.SanitizeRTSPUrl(url),
		"transport", transport,
		"component", "ffmpeg-manager",
		"operation", "start_stream")

	log.Printf("✅ Started FFmpeg stream for %s", privacy.SanitizeRTSPUrl(url))
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
			Context("url", privacy.SanitizeRTSPUrl(url)).
			Context("active_streams", len(m.streams)).
			Build()
	}

	// Stop the stream and remove from map while holding lock
	stream.Stop()
	delete(m.streams, url)

	// Unregister sound level processor while holding lock
	UnregisterSoundLevelProcessor(url)
	managerLogger.Debug("unregistered sound level processor",
		"url", privacy.SanitizeRTSPUrl(url),
		"operation", "stop_stream")

	// CRITICAL: Release mutex before time.Sleep to prevent deadlock in synctest
	// Go 1.25 Knowledge: In testing/synctest, holding a mutex during time.Sleep causes deadlock
	// because goroutines waiting on the mutex are not durably blocked, preventing time advancement
	m.streamsMu.Unlock()

	// Clean up buffers for the stream
	// Wait a short time for any in-flight writes to complete
	// This sleep is now safe for synctest since mutex is released
	time.Sleep(100 * time.Millisecond)

	if err := RemoveAnalysisBuffer(url); err != nil {
		managerLogger.Warn("failed to remove analysis buffer",
			"url", privacy.SanitizeRTSPUrl(url),
			"error", err,
			"operation", "stop_stream_buffer_cleanup")
		log.Printf("⚠️ Warning: failed to remove analysis buffer for %s: %v", privacy.SanitizeRTSPUrl(url), err)
	}

	if err := RemoveCaptureBuffer(url); err != nil {
		managerLogger.Warn("failed to remove capture buffer",
			"url", privacy.SanitizeRTSPUrl(url),
			"error", err,
			"operation", "stop_stream_buffer_cleanup")
		log.Printf("⚠️ Warning: failed to remove capture buffer for %s: %v", privacy.SanitizeRTSPUrl(url), err)
	}

	managerLogger.Info("stopped FFmpeg stream",
		"url", privacy.SanitizeRTSPUrl(url),
		"operation", "stop_stream")

	log.Printf("🛑 Stopped FFmpeg stream for %s", privacy.SanitizeRTSPUrl(url))
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
			Context("url", privacy.SanitizeRTSPUrl(url)).
			Context("active_streams", activeStreamCount).
			Build()
	}

	// Re-register sound level processor if sound level monitoring is enabled
	// This ensures processor registration survives stream restarts
	if err := registerSoundLevelProcessorIfEnabled(url, managerLogger); err != nil {
		managerLogger.Warn("sound level processor registration failed during stream restart",
			"url", privacy.SanitizeRTSPUrl(url),
			"error", err,
			"operation", "restart_stream_sound_level_registration")
		log.Printf("⚠️ Warning: Sound level processor registration failed during restart of %s: %v",
			privacy.SanitizeRTSPUrl(url), err)
		// Continue with stream restart even if sound level registration fails
		// This provides graceful degradation - stream functionality is preserved
	}

	stream.Restart(false) // false = automatic restart (health-triggered)

	managerLogger.Info("restarted FFmpeg stream",
		"url", privacy.SanitizeRTSPUrl(url),
		"operation", "restart_stream")

	log.Printf("🔄 Restarted FFmpeg stream for %s", privacy.SanitizeRTSPUrl(url))
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

	// Build map of configured URLs with their transport settings
	for _, url := range settings.Realtime.RTSP.URLs {
		configuredURLs[url] = settings.Realtime.RTSP.Transport
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
		managerLogger.Info("transport setting changed, restarting stream",
			"url", privacy.SanitizeRTSPUrl(change.url),
			"old_transport", change.oldTransport,
			"new_transport", change.newTransport,
			"component", "ffmpeg-manager",
			"operation", "sync_transport_change")

		log.Printf("🔄 Transport changed for %s: %s → %s (restarting stream)",
			privacy.SanitizeRTSPUrl(change.url),
			change.oldTransport,
			change.newTransport)

		// Stop stream with old transport
		// StopStream() is synchronous and includes buffer cleanup delay
		if err := m.StopStream(change.url); err != nil {
			managerLogger.Error("failed to stop stream for transport change",
				"url", privacy.SanitizeRTSPUrl(change.url),
				"old_transport", change.oldTransport,
				"new_transport", change.newTransport,
				"error", err,
				"component", "ffmpeg-manager",
				"operation", "sync_transport_change_stop")
			log.Printf("❌ Failed to stop %s for transport change: %v",
				privacy.SanitizeRTSPUrl(change.url), err)
			continue
		}

		// Verify stream was fully removed from manager
		// StopStream() should have already removed it, but verify to be defensive
		m.streamsMu.RLock()
		if _, stillExists := m.streams[change.url]; stillExists {
			m.streamsMu.RUnlock()
			managerLogger.Error("stream still exists after StopStream",
				"url", privacy.SanitizeRTSPUrl(change.url),
				"old_transport", change.oldTransport,
				"new_transport", change.newTransport,
				"component", "ffmpeg-manager",
				"operation", "sync_transport_change_verify")
			log.Printf("❌ Failed to properly stop %s - stream still exists",
				privacy.SanitizeRTSPUrl(change.url))
			continue
		}
		m.streamsMu.RUnlock()

		// Start stream with new transport
		if err := m.StartStream(change.url, change.newTransport, audioChan); err != nil {
			managerLogger.Error("failed to restart stream with new transport",
				"url", privacy.SanitizeRTSPUrl(change.url),
				"old_transport", change.oldTransport,
				"new_transport", change.newTransport,
				"error", err,
				"component", "ffmpeg-manager",
				"operation", "sync_transport_change_start")
			log.Printf("❌ Failed to restart %s with transport %s: %v",
				privacy.SanitizeRTSPUrl(change.url), change.newTransport, err)
			continue
		}

		log.Printf("✅ Restarted %s with new transport: %s",
			privacy.SanitizeRTSPUrl(change.url), change.newTransport)
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
			managerLogger.Warn("failed to stop unconfigured stream",
				"url", privacy.SanitizeRTSPUrl(url),
				"error", err,
				"component", "ffmpeg-manager",
				"operation", "sync_with_config")
			log.Printf("⚠️ Error stopping unconfigured stream %s: %v", url, err)
		}
	}

	// Start streams that are configured but not running
	for url, transport := range configuredURLs {
		m.streamsMu.RLock()
		_, running := m.streams[url]
		m.streamsMu.RUnlock()

		if !running {
			if err := m.StartStream(url, transport, audioChan); err != nil {
				managerLogger.Warn("failed to start configured stream",
					"url", privacy.SanitizeRTSPUrl(url),
					"error", err,
					"transport", transport,
					"component", "ffmpeg-manager",
					"operation", "sync_with_config")
				log.Printf("⚠️ Error starting configured stream %s: %v", url, err)
			}
		}
	}

	return nil
}

// StartMonitoring starts periodic monitoring of streams
func (m *FFmpegManager) StartMonitoring(interval time.Duration) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				m.checkStreamHealth()
			}
		}
	}()
}

// checkStreamHealth checks health of all streams
func (m *FFmpegManager) checkStreamHealth() {
	health := m.HealthCheck()

	if conf.Setting().Debug {
		managerLogger.Debug("performing health check on all streams",
			"stream_count", len(health),
			"operation", "check_stream_health")
	}

	for url, h := range health {
		if !h.IsHealthy {
			// Get the stream to check if it's already restarting
			m.streamsMu.RLock()
			stream, exists := m.streams[url]
			m.streamsMu.RUnlock()

			// Skip if stream doesn't exist (shouldn't happen but be defensive)
			if !exists {
				managerLogger.Debug("unhealthy stream not found in streams map",
					"url", privacy.SanitizeRTSPUrl(url),
					"operation", "health_check")
				continue
			}

			// Check if stream is already in the process of restarting
			if stream.IsRestarting() {
				if conf.Setting().Debug {
					managerLogger.Debug("skipping restart for stream already in restart process",
						"url", privacy.SanitizeRTSPUrl(url),
						"last_data_ago_seconds", getTimeSinceDataSeconds(h.LastDataReceived),
						"restart_count", h.RestartCount,
						"operation", "health_check_skip_restart")
				}
				continue // Don't interfere with ongoing restart/backoff
			}

			// Check if stream is too new to restart (give it time to establish)
			processStartTime := stream.GetProcessStartTime()
			if !processStartTime.IsZero() {
				timeSinceStart := time.Since(processStartTime)
				if timeSinceStart < minimumStreamRuntime {
					if conf.Setting().Debug {
						managerLogger.Debug("skipping restart for new stream still establishing",
							"url", privacy.SanitizeRTSPUrl(url),
							"runtime_seconds", timeSinceStart.Seconds(),
							"minimum_runtime_seconds", minimumStreamRuntime.Seconds(),
							"last_data_ago_seconds", getTimeSinceDataSeconds(h.LastDataReceived),
							"operation", "health_check_skip_new_stream")
					}
					continue // Give new streams time to stabilize
				}
			}

			managerLogger.Warn("unhealthy stream detected",
				"url", privacy.SanitizeRTSPUrl(url),
				"last_data_ago_seconds", getTimeSinceDataSeconds(h.LastDataReceived),
				"restart_count", h.RestartCount,
				"is_receiving_data", h.IsReceivingData,
				"bytes_per_second", h.BytesPerSecond,
				"total_bytes", h.TotalBytesReceived,
				"operation", "health_check")

			log.Printf("⚠️ Unhealthy stream detected: %s (last data: %s ago)",
				privacy.SanitizeRTSPUrl(url), formatTimeSinceData(h.LastDataReceived))

			// Restart unhealthy streams
			if conf.Setting().Debug {
				managerLogger.Debug("attempting to restart unhealthy stream",
					"url", privacy.SanitizeRTSPUrl(url),
					"operation", "health_check_restart_attempt")
			}

			if err := m.RestartStream(url); err != nil {
				managerLogger.Error("failed to restart unhealthy stream",
					"url", privacy.SanitizeRTSPUrl(url),
					"error", err,
					"operation", "health_check_restart")
				log.Printf("❌ Failed to restart unhealthy stream %s: %v", url, err)

				// Report to Sentry with enhanced context
				errorWithContext := errors.New(err).
					Component("ffmpeg-manager").
					Category(errors.CategoryRTSP).
					Context("operation", "health_check_restart").
					Context("url", privacy.SanitizeRTSPUrl(url)).
					Context("last_data_seconds_ago", getTimeSinceDataSeconds(h.LastDataReceived)).
					Context("restart_count", h.RestartCount).
					Context("health_status", "unhealthy").
					Build()
				// This will be reported via event bus if telemetry is enabled
				_ = errorWithContext
			} else if conf.Setting().Debug {
				managerLogger.Debug("successfully initiated restart for unhealthy stream",
					"url", privacy.SanitizeRTSPUrl(url),
					"operation", "health_check_restart_success")
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
			managerLogger.Debug("stream is healthy",
				"url", privacy.SanitizeRTSPUrl(url),
				"pid", currentPID,
				"is_receiving_data", h.IsReceivingData,
				"bytes_per_second", h.BytesPerSecond,
				"last_data_ago_seconds", getTimeSinceDataSeconds(h.LastDataReceived),
				"operation", "health_check_healthy")
		}
	}
}

// Shutdown gracefully shuts down all streams
func (m *FFmpegManager) Shutdown() {
	start := time.Now()

	// Get active stream count safely
	m.streamsMu.RLock()
	activeStreams := len(m.streams)
	m.streamsMu.RUnlock()

	managerLogger.Info("shutting down FFmpeg manager",
		"active_streams", activeStreams,
		"operation", "shutdown")

	log.Printf("🛑 Shutting down FFmpeg manager...")

	// Cancel context to signal shutdown
	m.cancel()

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
			managerLogger.Warn("failed to stop stream during shutdown",
				"url", privacy.SanitizeRTSPUrl(url),
				"error", err,
				"operation", "shutdown")
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
		managerLogger.Info("FFmpeg manager shutdown complete",
			"duration_ms", time.Since(start).Milliseconds(),
			"stopped_streams", activeStreams,
			"operation", "shutdown")
		log.Printf("✅ FFmpeg manager shutdown complete")
	case <-time.After(30 * time.Second):
		managerLogger.Warn("FFmpeg manager shutdown timeout",
			"duration_ms", time.Since(start).Milliseconds(),
			"active_streams", activeStreams,
			"operation", "shutdown")
		log.Printf("⚠️ FFmpeg manager shutdown timeout")
	}
}
