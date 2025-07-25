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

	// Create new stream
	stream := NewFFmpegStream(url, transport, audioChan)
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
	defer m.streamsMu.Unlock()

	stream, exists := m.streams[url]
	if !exists {
		return errors.New(fmt.Errorf("no stream found for URL: %s", url)).
			Category(errors.CategoryValidation).
			Component("ffmpeg-manager").
			Context("operation", "stop_stream").
			Context("url", privacy.SanitizeRTSPUrl(url)).
			Context("active_streams", len(m.streams)).
			Build()
	}

	stream.Stop()
	delete(m.streams, url)
	
	// Unregister sound level processor
	UnregisterSoundLevelProcessor(url)
	managerLogger.Debug("unregistered sound level processor",
		"url", privacy.SanitizeRTSPUrl(url),
		"operation", "stop_stream")
	
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

	// Build map of configured URLs
	for _, url := range settings.Realtime.RTSP.URLs {
		configuredURLs[url] = settings.Realtime.RTSP.Transport
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
			managerLogger.Warn("unhealthy stream detected",
				"url", privacy.SanitizeRTSPUrl(url),
				"last_data_ago_seconds", time.Since(h.LastDataReceived).Seconds(),
				"restart_count", h.RestartCount,
				"is_receiving_data", h.IsReceivingData,
				"bytes_per_second", h.BytesPerSecond,
				"total_bytes", h.TotalBytesReceived,
				"operation", "health_check")
			
			log.Printf("⚠️ Unhealthy stream detected: %s (last data: %v ago)", 
				privacy.SanitizeRTSPUrl(url), time.Since(h.LastDataReceived))
			
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
					Context("last_data_seconds_ago", time.Since(h.LastDataReceived).Seconds()).
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
				"last_data_ago_seconds", time.Since(h.LastDataReceived).Seconds(),
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