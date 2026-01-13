package myaudio

import (
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// Global FFmpeg manager instance
var (
	globalManager *FFmpegManager
	managerOnce   sync.Once
	managerMutex  sync.RWMutex

	// Monitoring is started separately when we have audioChan
	monitoringOnce sync.Once
)

// getIntegrationLogger returns the integration logger.
// Fetched dynamically to ensure it uses the current centralized logger.
func getIntegrationLogger() logger.Logger {
	return logger.Global().Module("audio").Module("ffmpeg")
}

// UpdateFFmpegLogLevel updates the logger level based on configuration
// Note: With the new logger, debug level is controlled by the global logger configuration
func UpdateFFmpegLogLevel() {
	// Debug level is now controlled by global logger configuration
	// This function is kept for API compatibility but is a no-op
}

// registerSoundLevelProcessorIfEnabled registers a sound level processor for the given source
// if sound level processing is enabled in the configuration. This helper function ensures
// consistent registration behavior across different stream initialization paths.
// Returns an error if registration fails, nil if disabled or successful.
func registerSoundLevelProcessorIfEnabled(source string, log logger.Logger) error {
	// Ensure we have a non-nil logger to prevent panics
	if log == nil {
		log = getIntegrationLogger()
	}

	settings := conf.Setting()
	if !settings.Realtime.Audio.SoundLevel.Enabled {
		return nil // Not enabled, no error
	}

	// Get or create the source in the registry to get proper ID and DisplayName
	registry := GetRegistry()
	// Guard against nil registry during initialization to prevent panic
	if registry == nil {
		log.Warn("registry not available during sound level processor registration",
			logger.String("url", privacy.SanitizeRTSPUrl(source)),
			logger.String("operation", "register_sound_level"))
		return errors.Newf("registry not available during initialization").
			Component("ffmpeg-integration").
			Category(errors.CategorySystem).
			Context("operation", "register_sound_level").
			Build()
	}

	// Use SourceTypeUnknown to let auto-detection determine the correct type from URL
	audioSource := registry.GetOrCreateSource(source, SourceTypeUnknown)
	if audioSource == nil {
		log.Warn("failed to get/create audio source for sound level processor",
			logger.String("url", privacy.SanitizeRTSPUrl(source)),
			logger.String("operation", "register_sound_level"))
		return errors.Newf("failed to get/create audio source").
			Component("ffmpeg-integration").
			Category(errors.CategorySystem).
			Context("operation", "register_sound_level_processor").
			Context("source", privacy.SanitizeRTSPUrl(source)).
			Build()
	}

	// Register the sound level processor using source ID and DisplayName
	if err := RegisterSoundLevelProcessor(audioSource.ID, audioSource.DisplayName); err != nil {
		log.Warn("failed to register sound level processor",
			logger.String("id", audioSource.ID),
			logger.String("display_name", audioSource.DisplayName),
			logger.Error(err),
			logger.String("operation", "register_sound_level"))
		return errors.New(err).
			Component("ffmpeg-integration").
			Category(errors.CategorySystem).
			Context("operation", "register_sound_level_processor").
			Context("source_id", audioSource.ID).
			Context("display_name", audioSource.DisplayName).
			Build()
	} else if conf.Setting().Debug {
		log.Debug("registered sound level processor",
			logger.String("id", audioSource.ID),
			logger.String("display_name", audioSource.DisplayName),
			logger.String("operation", "register_sound_level"))
	}
	return nil
}

// getGlobalManager returns the global FFmpeg manager instance.
// Note: Monitoring is started separately via startMonitoringOnce() when audioChan is available.
func getGlobalManager() *FFmpegManager {
	managerOnce.Do(func() {
		managerMutex.Lock()
		defer managerMutex.Unlock()

		// Update logger level based on current configuration
		UpdateFFmpegLogLevel()

		globalManager = NewFFmpegManager()
		// Monitoring will be started later when audioChan is available
	})

	managerMutex.RLock()
	defer managerMutex.RUnlock()

	// Return nil if manager was shut down
	return globalManager
}

// startMonitoringOnce starts the monitoring goroutines (health check + watchdog) exactly once.
// This is called when we have the audioChan available, which the watchdog needs for force-restarting stuck streams.
func startMonitoringOnce(manager *FFmpegManager, audioChan chan UnifiedAudioData) {
	// Check for nil audioChan BEFORE consuming the sync.Once guard
	// This ensures the Do block is only executed when a valid audioChan is available
	if audioChan == nil {
		getIntegrationLogger().Error("cannot start monitoring - audioChan is nil",
			logger.String("operation", "start_monitoring_once"))
		return
	}

	monitoringOnce.Do(func() {
		if manager == nil {
			getIntegrationLogger().Error("cannot start monitoring - manager is nil",
				logger.String("operation", "start_monitoring_once"))
			return
		}

		settings := conf.Setting()
		monitoringInterval := time.Duration(settings.Realtime.RTSP.Health.MonitoringInterval) * time.Second
		if monitoringInterval == 0 {
			monitoringInterval = 30 * time.Second // default fallback
		}

		getIntegrationLogger().Info("starting FFmpeg stream monitoring",
			logger.Float64("monitoring_interval_seconds", monitoringInterval.Seconds()),
			logger.Float64("watchdog_interval_seconds", watchdogCheckInterval.Seconds()),
			logger.String("operation", "start_monitoring_once"))

		manager.StartMonitoring(monitoringInterval, audioChan)
	})
}

// CaptureAudioRTSP provides backward compatibility with the old API
// This function now delegates to the new simplified FFmpeg manager
func CaptureAudioRTSP(url, transport string, wg *sync.WaitGroup, quitChan <-chan struct{}, restartChan chan struct{}, unifiedAudioChan chan UnifiedAudioData) {
	// Update logger level based on current configuration
	UpdateFFmpegLogLevel()

	// Note: Sound level processor registration is handled by FFmpegManager.StartStream()
	// to avoid duplicate registrations and ensure proper lifecycle management

	// Check FFmpeg availability
	if conf.GetFfmpegBinaryName() == "" {
		getIntegrationLogger().Error("FFmpeg not available",
			logger.String("url", privacy.SanitizeRTSPUrl(url)),
			logger.String("operation", "capture_audio_rtsp"))
		return
	}

	// Get the global manager
	manager := getGlobalManager()
	if manager == nil {
		getIntegrationLogger().Error("FFmpeg manager is not available",
			logger.String("url", privacy.SanitizeRTSPUrl(url)),
			logger.String("operation", "capture_audio_rtsp"))
		return
	}

	// Start monitoring (health check + watchdog) once we have the audioChan
	// This is done once across all streams via sync.Once
	startMonitoringOnce(manager, unifiedAudioChan)

	// Start the stream
	if err := manager.StartStream(url, transport, unifiedAudioChan); err != nil {
		getIntegrationLogger().Error("failed to start stream",
			logger.String("url", privacy.SanitizeRTSPUrl(url)),
			logger.Error(err),
			logger.String("transport", transport),
			logger.String("operation", "capture_audio_rtsp"))
		return
	}

	// Handle restart signals in a separate goroutine
	wg.Go(func() {
		for {
			select {
			case <-quitChan:
				// Stop the stream
				if err := manager.StopStream(url); err != nil {
					getIntegrationLogger().Warn("failed to stop stream",
						logger.String("url", privacy.SanitizeRTSPUrl(url)),
						logger.Error(err),
						logger.String("operation", "quit_signal"))
				}
				return
			case <-restartChan:
				// Restart the stream
				if err := manager.RestartStream(url); err != nil {
					getIntegrationLogger().Warn("failed to restart stream",
						logger.String("url", privacy.SanitizeRTSPUrl(url)),
						logger.Error(err),
						logger.String("operation", "restart_signal"))
				}
			}
		}
	})
}

// SyncStreamsWithConfig synchronizes running streams with configuration
// This is called when configuration changes to start/stop streams as needed
func SyncStreamsWithConfig(audioChan chan UnifiedAudioData) error {
	// Update logger level based on current configuration
	UpdateFFmpegLogLevel()

	manager := getGlobalManager()
	if manager == nil {
		return errors.Newf("FFmpeg manager is not available").
			Category(errors.CategorySystem).
			Component("ffmpeg-integration").
			Build()
	}
	return manager.SyncWithConfig(audioChan)
}

// GetStreamHealth returns health information for all streams
func GetStreamHealth() map[string]StreamHealth {
	manager := getGlobalManager()
	if manager == nil {
		return make(map[string]StreamHealth)
	}
	return manager.HealthCheck()
}

// ShutdownFFmpegManager gracefully shuts down the FFmpeg manager
func ShutdownFFmpegManager() {
	managerMutex.Lock()
	defer managerMutex.Unlock()

	if globalManager != nil {
		globalManager.Shutdown()
		globalManager = nil
		// Don't reset managerOnce or monitoringOnce to maintain consistent lifecycle semantics
		// Both the manager and its monitoring can only be initialized once per process
		// After shutdown, the manager cannot be recreated (getGlobalManager returns nil)
	}
}
