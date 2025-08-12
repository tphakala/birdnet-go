package myaudio

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"path/filepath"
	"strings"
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

// UpdateFFmpegLogLevel updates the logger level based on configuration
func UpdateFFmpegLogLevel() {
	if conf.Setting().Debug {
		integrationLevelVar.Set(slog.LevelDebug)
	} else {
		integrationLevelVar.Set(slog.LevelInfo)
	}
}

// registerSoundLevelProcessorIfEnabled registers a sound level processor for the given source
// if sound level processing is enabled in the configuration. This helper function ensures
// consistent registration behavior across different stream initialization paths.
// Returns an error if registration fails, nil if disabled or successful.
func registerSoundLevelProcessorIfEnabled(source string, logger *slog.Logger) error {
	settings := conf.Setting()
	if !settings.Realtime.Audio.SoundLevel.Enabled {
		return nil // Not enabled, no error
	}
	
	displayName := privacy.SanitizeRTSPUrl(source)
	if err := RegisterSoundLevelProcessor(source, displayName); err != nil {
		logger.Warn("failed to register sound level processor",
			"url", displayName,
			"error", err,
			"operation", "register_sound_level")
		log.Printf("⚠️ Error registering sound level processor for %s: %v", displayName, err)
		return errors.New(err).
			Component("ffmpeg-integration").
			Category(errors.CategorySystem).
			Context("operation", "register_sound_level_processor").
			Context("source", displayName).
			Build()
	} else if conf.Setting().Debug {
		logger.Debug("registered sound level processor",
			"url", displayName,
			"operation", "register_sound_level")
	}
	return nil
}

// getGlobalManager returns the global FFmpeg manager instance
func getGlobalManager() *FFmpegManager {
	managerOnce.Do(func() {
		managerMutex.Lock()
		defer managerMutex.Unlock()
		
		// Update logger level based on current configuration
		UpdateFFmpegLogLevel()
		
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
	// Update logger level based on current configuration
	UpdateFFmpegLogLevel()
	
	// Note: Sound level processor registration is handled by FFmpegManager.StartStream()
	// to avoid duplicate registrations and ensure proper lifecycle management

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

// StopAllRTSPStreamsAndWait stops all currently running RTSP streams and waits for completion
// Returns an error if any streams fail to stop. This provides proper synchronization
// to ensure all streams are fully stopped before proceeding.
func StopAllRTSPStreamsAndWait(timeout time.Duration) error {
	manager := getGlobalManager()
	if manager == nil {
		integrationLogger.Debug("No FFmpeg manager available, nothing to stop")
		return nil
	}
	
	// Get list of active streams
	activeStreams := manager.GetActiveStreams()
	if len(activeStreams) == 0 {
		integrationLogger.Debug("No active streams to stop")
		return nil
	}
	
	integrationLogger.Info("Stopping all RTSP streams",
		"count", len(activeStreams),
		"timeout", timeout,
		"component", "ffmpeg-integration",
		"operation", "stop_all_streams")
	
	// Channel to collect results
	type result struct {
		url string
		err error
	}
	results := make(chan result, len(activeStreams))
	
	// WaitGroup to track completion
	var wg sync.WaitGroup
	
	// Context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	
	// Stop all streams concurrently
	for _, url := range activeStreams {
		wg.Add(1)
		go func(streamURL string) {
			defer wg.Done()
			
			// Create a channel to signal completion
			done := make(chan error, 1)
			go func() {
				done <- manager.StopStream(streamURL)
			}()
			
			// Wait for stop or timeout
			select {
			case err := <-done:
				results <- result{url: streamURL, err: err}
				if err == nil {
					integrationLogger.Debug("Stopped RTSP stream",
						"url", privacy.SanitizeRTSPUrl(streamURL),
						"component", "ffmpeg-integration",
						"operation", "stop_stream")
				}
			case <-ctx.Done():
				results <- result{url: streamURL, err: errors.Newf("timeout stopping stream").
					Component("ffmpeg-integration").
					Category(errors.CategoryRTSP).
					Context("url", privacy.SanitizeRTSPUrl(streamURL)).
					Context("timeout", timeout).
					Build()}
			}
		}(url)
	}
	
	// Wait for all goroutines to complete
	go func() {
		wg.Wait()
		close(results)
	}()
	
	// Collect errors
	var errorList []string
	for r := range results {
		if r.err != nil {
			errorList = append(errorList, fmt.Sprintf("%s: %v", privacy.SanitizeRTSPUrl(r.url), r.err))
			integrationLogger.Warn("Failed to stop RTSP stream",
				"url", privacy.SanitizeRTSPUrl(r.url),
				"error", r.err,
				"component", "ffmpeg-integration",
				"operation", "stop_stream")
		}
	}
	
	if len(errorList) > 0 {
		return errors.Newf("failed to stop %d stream(s): %s", len(errorList), strings.Join(errorList, "; ")).
			Component("ffmpeg-integration").
			Category(errors.CategoryRTSP).
			Context("failed_count", len(errorList)).
			Context("total_count", len(activeStreams)).
			Build()
	}
	
	integrationLogger.Info("All RTSP streams stopped successfully",
		"count", len(activeStreams),
		"component", "ffmpeg-integration",
		"operation", "stop_all_streams_complete")
	
	return nil
}

