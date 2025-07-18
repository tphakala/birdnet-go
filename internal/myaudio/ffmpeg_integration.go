package myaudio

import (
	"log"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// Global FFmpeg manager instance
var (
	globalManager *FFmpegManager
	managerOnce   sync.Once
	managerMutex  sync.RWMutex
	
	integrationLogger *slog.Logger
	integrationLevelVar = new(slog.LevelVar)
	closeIntegrationLogger func() error
)

func init() {
	var err error
	// Define log file path relative to working directory - use ffmpeg-input.log as requested
	logFilePath := filepath.Join("logs", "ffmpeg-input.log")
	initialLevel := slog.LevelInfo // Set desired initial level
	integrationLevelVar.Set(initialLevel)

	// Initialize the service-specific file logger
	integrationLogger, closeIntegrationLogger, err = logging.NewFileLogger(logFilePath, "ffmpeg-input", integrationLevelVar)
	if err != nil {
		// Fallback: Log error to standard log and use default logger
		log.Printf("Failed to initialize ffmpeg-input file logger at %s: %v. Using default logger.", logFilePath, err)
		integrationLogger = slog.Default().With("service", "ffmpeg-input")
		closeIntegrationLogger = func() error { return nil } // No-op closer
	}
}

// registerSoundLevelProcessorIfEnabled registers a sound level processor for the given source
// if sound level processing is enabled in the configuration. This helper function ensures
// consistent registration behavior across different stream initialization paths.
func registerSoundLevelProcessorIfEnabled(source string, logger *slog.Logger) {
	settings := conf.Setting()
	if !settings.Realtime.Audio.SoundLevel.Enabled {
		return
	}
	
	displayName := privacy.SanitizeRTSPUrl(source)
	if err := RegisterSoundLevelProcessor(source, displayName); err != nil {
		logger.Warn("failed to register sound level processor",
			"url", displayName,
			"error", err,
			"operation", "register_sound_level")
		log.Printf("⚠️ Error registering sound level processor for %s: %v", displayName, err)
	} else {
		logger.Debug("registered sound level processor",
			"url", displayName,
			"operation", "register_sound_level")
	}
}

// getGlobalManager returns the global FFmpeg manager instance
func getGlobalManager() *FFmpegManager {
	managerOnce.Do(func() {
		managerMutex.Lock()
		defer managerMutex.Unlock()
		
		globalManager = NewFFmpegManager()
		// Start monitoring with configurable interval
		settings := conf.Setting()
		monitoringInterval := time.Duration(settings.Realtime.RTSP.Health.MonitoringInterval) * time.Second
		if monitoringInterval == 0 {
			monitoringInterval = 30 * time.Second // default fallback
		}
		globalManager.StartMonitoring(monitoringInterval)
	})
	
	managerMutex.RLock()
	defer managerMutex.RUnlock()
	
	// Return nil if manager was shut down
	return globalManager
}

// CaptureAudioRTSP provides backward compatibility with the old API
// This function now delegates to the new simplified FFmpeg manager
func CaptureAudioRTSP(url, transport string, wg *sync.WaitGroup, quitChan <-chan struct{}, restartChan chan struct{}, unifiedAudioChan chan UnifiedAudioData) {
	// Initialize sound level processor if enabled
	registerSoundLevelProcessorIfEnabled(url, integrationLogger)
	defer UnregisterSoundLevelProcessor(url)

	// Check FFmpeg availability
	if conf.GetFfmpegBinaryName() == "" {
		integrationLogger.Error("FFmpeg not available",
			"url", privacy.SanitizeRTSPUrl(url),
			"operation", "capture_audio_rtsp")
		log.Printf("❌ FFmpeg is not available, cannot capture audio from %s", url)
		return
	}

	// Get the global manager
	manager := getGlobalManager()
	if manager == nil {
		integrationLogger.Error("FFmpeg manager is not available",
			"url", privacy.SanitizeRTSPUrl(url),
			"operation", "capture_audio_rtsp")
		log.Printf("❌ FFmpeg manager is not available for %s", url)
		return
	}

	// Start the stream
	if err := manager.StartStream(url, transport, unifiedAudioChan); err != nil {
		integrationLogger.Error("failed to start stream",
			"url", privacy.SanitizeRTSPUrl(url),
			"error", err,
			"transport", transport,
			"operation", "capture_audio_rtsp")
		log.Printf("❌ Failed to start stream for %s: %v", url, err)
		return
	}

	// Handle restart signals in a separate goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-quitChan:
				// Stop the stream
				if err := manager.StopStream(url); err != nil {
					integrationLogger.Warn("failed to stop stream",
						"url", privacy.SanitizeRTSPUrl(url),
						"error", err,
						"operation", "quit_signal")
					log.Printf("⚠️ Error stopping stream %s: %v", url, err)
				}
				return
			case <-restartChan:
				// Restart the stream
				if err := manager.RestartStream(url); err != nil {
					integrationLogger.Warn("failed to restart stream",
						"url", privacy.SanitizeRTSPUrl(url),
						"error", err,
						"operation", "restart_signal")
					log.Printf("⚠️ Error restarting stream %s: %v", url, err)
				}
			}
		}
	}()
}

// SyncRTSPStreamsWithConfig synchronizes running RTSP streams with configuration
// This is called when configuration changes to start/stop streams as needed
func SyncRTSPStreamsWithConfig(audioChan chan UnifiedAudioData) error {
	manager := getGlobalManager()
	if manager == nil {
		return errors.Newf("FFmpeg manager is not available").
			Category(errors.CategorySystem).
			Component("ffmpeg-integration").
			Build()
	}
	return manager.SyncWithConfig(audioChan)
}

// GetRTSPStreamHealth returns health information for all RTSP streams
func GetRTSPStreamHealth() map[string]StreamHealth {
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
		// Don't reset managerOnce to avoid race conditions
		// The manager can only be initialized once per process
	}
}

