package myaudio

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// RTSPHealthWatchdog monitors the health of all configured RTSP streams
type RTSPHealthWatchdog struct {
	checkInterval   time.Duration
	dataTimeout     time.Duration
	running         atomic.Bool
	done            chan struct{}
	mu              sync.RWMutex
	lastHealthCheck time.Time
	healthStats     map[string]*StreamHealthStats
}

// StreamHealthStats tracks health metrics for a single stream
type StreamHealthStats struct {
	URL                 string
	LastDataReceived    time.Time
	LastHealthCheck     time.Time
	ConsecutiveTimeouts int
	IsHealthy           bool
	RestartInProgress   bool
	mu                  sync.RWMutex
}

// NewRTSPHealthWatchdog creates a new health monitoring watchdog
func NewRTSPHealthWatchdog(checkInterval, dataTimeout time.Duration) *RTSPHealthWatchdog {
	return &RTSPHealthWatchdog{
		checkInterval: checkInterval,
		dataTimeout:   dataTimeout,
		done:          make(chan struct{}),
		healthStats:   make(map[string]*StreamHealthStats),
	}
}

// Start begins the health monitoring process
func (w *RTSPHealthWatchdog) Start() {
	if w.running.Load() {
		logging.Info("RTSP health watchdog already running",
			"service", "rtsp-health-watchdog")
		return
	}

	w.running.Store(true)
	go w.monitorLoop()
	logging.Info("RTSP health watchdog started",
		"service", "rtsp-health-watchdog",
		"check_interval", w.checkInterval,
		"data_timeout", w.dataTimeout)
}

// Stop halts the health monitoring process
func (w *RTSPHealthWatchdog) Stop() {
	if !w.running.Load() {
		return
	}

	close(w.done)
	w.running.Store(false)
	logging.Info("RTSP health watchdog stopped",
		"service", "rtsp-health-watchdog")
}

// monitorLoop is the main monitoring loop
func (w *RTSPHealthWatchdog) monitorLoop() {
	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()

	// Wait a moment before the initial check to allow processes to start
	time.Sleep(2 * time.Second)

	// Perform initial check
	w.performHealthCheck()

	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			w.performHealthCheck()
		}
	}
}

// performHealthCheck checks the health of all configured RTSP streams
func (w *RTSPHealthWatchdog) performHealthCheck() {
	settings := conf.Setting()
	if settings == nil || settings.Realtime.RTSP.URLs == nil {
		return
	}

	w.mu.Lock()
	w.lastHealthCheck = time.Now()
	w.mu.Unlock()

	// Get current configured URLs
	configuredURLs := make(map[string]bool)
	for _, url := range settings.Realtime.RTSP.URLs {
		configuredURLs[url] = true
		w.checkStreamHealth(url)
	}

	// Clean up stats for removed streams
	w.cleanupRemovedStreams(configuredURLs)
}

// checkStreamHealth checks the health of a single stream
func (w *RTSPHealthWatchdog) checkStreamHealth(url string) {
	// Get or create health stats
	stats := w.getOrCreateStats(url)

	stats.mu.Lock()
	stats.LastHealthCheck = time.Now()
	previouslyHealthy := stats.IsHealthy
	stats.mu.Unlock()

	// Check if stream is already being initialized
	if _, isActive := activeStreams.Load(url); isActive {
		// Stream is active, check if FFmpeg process exists
		processInterface, exists := ffmpegProcesses.Load(url)
		if !exists {
			// Stream is active but process not yet registered - this is normal during startup
			// Give it some time before considering it missing
			if time.Since(stats.LastHealthCheck) < 10*time.Second {
				logging.Debug("RTSP health check: Stream active but process not yet registered",
					"service", "rtsp-health-watchdog",
					"url", url,
					"operation", "startup_grace_period")
				return
			}
			logging.Warn("RTSP health check: No FFmpeg process found after grace period",
				"service", "rtsp-health-watchdog",
				"url", url,
				"operation", "check_process_exists")
			w.handleMissingProcess(url, stats)
			return
		}

		// Type assert to FFmpegProcess
		process, ok := processInterface.(*FFmpegProcess)
		if !ok {
			logging.Error("RTSP health check: Invalid process type",
				"service", "rtsp-health-watchdog",
				"url", url,
				"operation", "type_assertion",
				"actual_type", fmt.Sprintf("%T", processInterface))
			return
		}

		// Check if process is alive
		if !w.isProcessAlive(process) {
			logging.Warn("RTSP health check: FFmpeg process dead",
				"service", "rtsp-health-watchdog",
				"url", url,
				"operation", "check_process_alive")
			w.handleDeadProcess(url, stats)
			return
		}
	} else {
		// Stream is not active at all - should not happen for configured streams
		logging.Warn("RTSP health check: Stream not active",
			"service", "rtsp-health-watchdog",
			"url", url,
			"operation", "check_active_streams")
		w.handleMissingProcess(url, stats)
		return
	}

	// Get process for later use
	var process *FFmpegProcess
	if processInterface, exists := ffmpegProcesses.Load(url); exists {
		process, _ = processInterface.(*FFmpegProcess)
	}

	// Check data reception status
	dataReceiving := w.checkDataReception(url)

	stats.mu.Lock()
	if dataReceiving {
		stats.LastDataReceived = time.Now()
		stats.ConsecutiveTimeouts = 0
		stats.IsHealthy = true
		if !previouslyHealthy {
			logging.Info("RTSP health restored",
				"service", "rtsp-health-watchdog",
				"url", url,
				"operation", "health_restored")
			telemetry.CaptureMessage(fmt.Sprintf("RTSP stream health restored: %s", categorizeStreamURL(url)),
				sentry.LevelInfo, "rtsp-health-restored")
		}
	} else {
		timeSinceLastData := time.Since(stats.LastDataReceived)
		if timeSinceLastData > w.dataTimeout {
			stats.ConsecutiveTimeouts++
			stats.IsHealthy = false
			logging.Warn("RTSP health check: No data received",
				"service", "rtsp-health-watchdog",
				"url", url,
				"operation", "data_timeout_detected",
				"time_since_last_data", timeSinceLastData,
				"consecutive_timeouts", stats.ConsecutiveTimeouts)

			// Only attempt restart if not already in progress and we have a valid process
			if !stats.RestartInProgress && process != nil {
				stats.RestartInProgress = true
				stats.mu.Unlock()
				w.handleUnhealthyStream(url, stats, process)
				return
			}
		}
	}
	stats.mu.Unlock()
}

// checkDataReception checks if a stream is receiving data
func (w *RTSPHealthWatchdog) checkDataReception(url string) bool {
	// Check if the process has an active audioWatchdog
	processInterface, exists := ffmpegProcesses.Load(url)
	if !exists {
		return false
	}

	process, ok := processInterface.(*FFmpegProcess)
	if !ok || process == nil {
		return false
	}

	// Check analysis buffer exists and has data
	abMutex.RLock()
	buffer, exists := analysisBuffers[url]
	abMutex.RUnlock()

	if !exists || buffer == nil {
		return false
	}

	// Check if buffer has any data (non-empty means it's receiving data)
	// The buffer.Length() method tells us how much data is available
	hasData := buffer.Length() > 0

	// Also check if we're actively processing data from this source
	// by checking if it exists in the active streams map
	_, isActive := activeStreams.Load(url)

	return hasData && isActive
}

// isProcessAlive checks if an FFmpeg process is still running
func (w *RTSPHealthWatchdog) isProcessAlive(process *FFmpegProcess) bool {
	if process == nil || process.cmd == nil || process.cmd.Process == nil {
		return false
	}

	// Check if process has exited
	select {
	case <-process.done:
		return false
	default:
		// Additional check: verify process still exists in system
		if process.cmd.ProcessState != nil {
			return false
		}
		return true
	}
}

// handleMissingProcess handles the case where no FFmpeg process exists for a configured stream
func (w *RTSPHealthWatchdog) handleMissingProcess(url string, stats *StreamHealthStats) {
	stats.mu.Lock()
	stats.IsHealthy = false
	stats.ConsecutiveTimeouts++
	shouldRestart := !stats.RestartInProgress
	if shouldRestart {
		stats.RestartInProgress = true
	}
	stats.mu.Unlock()

	if shouldRestart {
		logging.Info("RTSP health watchdog: Starting missing FFmpeg process",
			"service", "rtsp-health-watchdog",
			"url", url,
			"operation", "start_missing_process")
		telemetry.CaptureMessage(fmt.Sprintf("RTSP health watchdog starting missing process: %s", categorizeStreamURL(url)),
			sentry.LevelWarning, "rtsp-health-missing-process")

		// Start a new FFmpeg process
		w.startNewProcess(url, stats)
	}
}

// handleDeadProcess handles the case where an FFmpeg process exists but is dead
func (w *RTSPHealthWatchdog) handleDeadProcess(url string, stats *StreamHealthStats) {
	stats.mu.Lock()
	stats.IsHealthy = false
	stats.ConsecutiveTimeouts++
	shouldRestart := !stats.RestartInProgress
	if shouldRestart {
		stats.RestartInProgress = true
	}
	stats.mu.Unlock()

	if shouldRestart {
		logging.Info("RTSP health watchdog: Restarting dead FFmpeg process",
			"service", "rtsp-health-watchdog",
			"url", url,
			"operation", "restart_dead_process")
		telemetry.CaptureMessage(fmt.Sprintf("RTSP health watchdog restarting dead process: %s", categorizeStreamURL(url)),
			sentry.LevelWarning, "rtsp-health-dead-process")

		// Clean up the dead process first
		if process, exists := ffmpegProcesses.Load(url); exists {
			if p, ok := process.(*FFmpegProcess); ok {
				p.Cleanup(url)
			}
		}

		// Wait a moment for cleanup
		time.Sleep(500 * time.Millisecond)

		// Start a new process
		w.startNewProcess(url, stats)
	}
}

// handleUnhealthyStream handles streams that are running but not receiving data
func (w *RTSPHealthWatchdog) handleUnhealthyStream(url string, stats *StreamHealthStats, process *FFmpegProcess) {
	// Check if this is a restart storm
	if process.isRestartStorm() {
		logging.Warn("RTSP health watchdog: Restart storm detected, skipping restart",
			"service", "rtsp-health-watchdog",
			"url", url,
			"operation", "restart_storm_detected")
		stats.mu.Lock()
		stats.RestartInProgress = false
		stats.mu.Unlock()
		return
	}

	// Send restart signal through the existing restart channel
	// This ensures coordination with the main restart logic
	restartChan := getRestartChannelForURL(url)
	if restartChan != nil {
		select {
		case restartChan <- struct{}{}:
			logging.Info("RTSP health watchdog: Sent restart signal for unhealthy stream",
				"service", "rtsp-health-watchdog",
				"url", url,
				"operation", "send_restart_signal")
			telemetry.CaptureMessage(fmt.Sprintf("RTSP health watchdog triggered restart: %s", categorizeStreamURL(url)),
				sentry.LevelWarning, "rtsp-health-triggered-restart")
		default:
			logging.Warn("RTSP health watchdog: Restart channel full",
				"service", "rtsp-health-watchdog",
				"url", url,
				"operation", "restart_channel_full")
		}
	}

	// Reset restart flag after a delay
	go func() {
		time.Sleep(10 * time.Second)
		stats.mu.Lock()
		stats.RestartInProgress = false
		stats.mu.Unlock()
	}()
}

// startNewProcess starts a new FFmpeg process for a URL
func (w *RTSPHealthWatchdog) startNewProcess(url string, stats *StreamHealthStats) {
	settings := conf.Setting()
	if settings == nil {
		stats.mu.Lock()
		stats.RestartInProgress = false
		stats.mu.Unlock()
		return
	}

	// Find the restart channel for this URL
	restartChan := getRestartChannelForURL(url)
	if restartChan == nil {
		logging.Warn("RTSP health watchdog: No restart channel found",
			"service", "rtsp-health-watchdog",
			"url", url,
			"operation", "find_restart_channel")
		stats.mu.Lock()
		stats.RestartInProgress = false
		stats.mu.Unlock()
		return
	}

	// Create a context for the new process
	ctx := context.Background()
	config := FFmpegConfig{
		URL:       url,
		Transport: settings.Realtime.RTSP.Transport,
	}

	// Start the FFmpeg lifecycle manager
	go func() {
		defer func() {
			stats.mu.Lock()
			stats.RestartInProgress = false
			stats.mu.Unlock()
		}()

		// For health watchdog, create a dummy unified audio channel since it's not used for actual data processing
		unifiedAudioChan := make(chan UnifiedAudioData, 10)
		go func() {
			// Drain the unified audio channel to prevent blocking
			for {
				select {
				case <-ctx.Done():
					// Exit when context is cancelled
					return
				case _, ok := <-unifiedAudioChan:
					if !ok {
						// Channel closed, exit
						return
					}
					// Discard audio data in health watchdog context
				}
			}
		}()
		if err := manageFfmpegLifecycle(ctx, config, restartChan, unifiedAudioChan); err != nil {
			enhancedErr := errors.New(err).
				Component("rtsp-health-watchdog").
				Category(errors.CategoryRTSP).
				Context("operation", "start_ffmpeg_lifecycle").
				Context("url", url).
				Build()
			telemetry.CaptureError(enhancedErr, "rtsp-health-start-failure")
			logging.Error("RTSP health watchdog: Failed to start FFmpeg",
				"service", "rtsp-health-watchdog",
				"url", url,
				"operation", "start_ffmpeg_lifecycle",
				"error", err)
		}
	}()
}

// getOrCreateStats gets or creates health stats for a URL
func (w *RTSPHealthWatchdog) getOrCreateStats(url string) *StreamHealthStats {
	w.mu.Lock()
	defer w.mu.Unlock()

	if stats, exists := w.healthStats[url]; exists {
		return stats
	}

	stats := &StreamHealthStats{
		URL:              url,
		LastDataReceived: time.Now(), // Assume healthy on creation
		LastHealthCheck:  time.Now(),
		IsHealthy:        true,
	}
	w.healthStats[url] = stats
	return stats
}

// cleanupRemovedStreams removes stats for streams no longer in configuration
func (w *RTSPHealthWatchdog) cleanupRemovedStreams(configuredURLs map[string]bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for url := range w.healthStats {
		if !configuredURLs[url] {
			delete(w.healthStats, url)
			logging.Info("RTSP health watchdog: Removed stats for unconfigured stream",
				"service", "rtsp-health-watchdog",
				"url", url,
				"operation", "cleanup_removed_stream")
		}
	}
}

// GetHealthReport returns a report of all stream health statuses
func (w *RTSPHealthWatchdog) GetHealthReport() map[string]StreamHealthReport {
	w.mu.RLock()
	defer w.mu.RUnlock()

	report := make(map[string]StreamHealthReport)
	for url, stats := range w.healthStats {
		stats.mu.RLock()
		report[url] = StreamHealthReport{
			URL:                 stats.URL,
			IsHealthy:           stats.IsHealthy,
			LastDataReceived:    stats.LastDataReceived,
			LastHealthCheck:     stats.LastHealthCheck,
			ConsecutiveTimeouts: stats.ConsecutiveTimeouts,
			RestartInProgress:   stats.RestartInProgress,
		}
		stats.mu.RUnlock()
	}
	return report
}

// StreamHealthReport is a thread-safe copy of stream health information
type StreamHealthReport struct {
	URL                 string
	IsHealthy           bool
	LastDataReceived    time.Time
	LastHealthCheck     time.Time
	ConsecutiveTimeouts int
	RestartInProgress   bool
}

// Global RTSP health watchdog instance
var rtspHealthWatchdog *RTSPHealthWatchdog

// InitRTSPHealthWatchdog initializes the global RTSP health watchdog
func InitRTSPHealthWatchdog() {
	if rtspHealthWatchdog == nil {
		// Default: check every 30 seconds, timeout after 90 seconds
		rtspHealthWatchdog = NewRTSPHealthWatchdog(30*time.Second, 90*time.Second)
	}
}

// StartRTSPHealthWatchdog starts the global RTSP health watchdog
func StartRTSPHealthWatchdog() {
	InitRTSPHealthWatchdog()
	rtspHealthWatchdog.Start()
}

// StopRTSPHealthWatchdog stops the global RTSP health watchdog
func StopRTSPHealthWatchdog() {
	if rtspHealthWatchdog != nil {
		rtspHealthWatchdog.Stop()
	}
}

// GetRTSPHealthReport returns the current health report
func GetRTSPHealthReport() map[string]StreamHealthReport {
	if rtspHealthWatchdog == nil {
		return make(map[string]StreamHealthReport)
	}
	return rtspHealthWatchdog.GetHealthReport()
}

// UpdateStreamDataReceived updates the last data received time for a stream
func UpdateStreamDataReceived(url string) {
	if rtspHealthWatchdog == nil {
		return
	}

	stats := rtspHealthWatchdog.getOrCreateStats(url)
	stats.mu.Lock()
	stats.LastDataReceived = time.Now()
	stats.mu.Unlock()
}

// restartChannels stores restart channels for each URL
var restartChannels sync.Map

// unifiedAudioChannels stores unified audio channels for each URL
var unifiedAudioChannels sync.Map

// audioLevelConverters stores converter channels for each URL to prevent goroutine leaks
var audioLevelConverters sync.Map

// RegisterStreamChannels registers the restart and audio level channels for a URL
func RegisterStreamChannels(url string, restartChan chan struct{}, unifiedAudioChan chan UnifiedAudioData) {
	if restartChan != nil {
		restartChannels.Store(url, restartChan)
	}
	if unifiedAudioChan != nil {
		unifiedAudioChannels.Store(url, unifiedAudioChan)
	}
}

// UnregisterStreamChannels removes the channels for a URL
func UnregisterStreamChannels(url string) {
	restartChannels.Delete(url)
	unifiedAudioChannels.Delete(url)
	// Also delete the converter channel to prevent leaks
	if ch, ok := audioLevelConverters.Load(url); ok {
		audioLevelConverters.Delete(url)
		// The goroutine will exit when the unified channel closes
		// We don't close the converter channel here as the goroutine handles that
		_ = ch // Avoid unused variable warning
	}
}

// getRestartChannelForURL retrieves the restart channel for a URL
func getRestartChannelForURL(url string) chan struct{} {
	if ch, ok := restartChannels.Load(url); ok {
		return ch.(chan struct{})
	}
	return nil
}

// getAudioLevelChannelForURL retrieves a simple audio level channel for a URL
// This creates a converter channel only once per URL to prevent goroutine leaks
func getAudioLevelChannelForURL(url string) chan AudioLevelData {
	// Check if we already have a converter for this URL
	if existingCh, ok := audioLevelConverters.Load(url); ok {
		return existingCh.(chan AudioLevelData)
	}

	// Check if we have a unified channel for this URL
	if unifiedCh, ok := unifiedAudioChannels.Load(url); ok {
		// Create a converter channel
		audioLevelCh := make(chan AudioLevelData, 10)
		
		// Try to store it atomically
		if actual, loaded := audioLevelConverters.LoadOrStore(url, audioLevelCh); loaded {
			// Another goroutine created one already, use that instead
			close(audioLevelCh)
			return actual.(chan AudioLevelData)
		}

		// Start the converter goroutine only once
		go func() {
			defer func() {
				// Clean up when unified channel closes
				audioLevelConverters.Delete(url)
				close(audioLevelCh)
			}()

			// Convert unified audio data to simple audio level data for health monitoring
			unifiedChannel := unifiedCh.(chan UnifiedAudioData)
			for unifiedData := range unifiedChannel {
				select {
				case audioLevelCh <- unifiedData.AudioLevel:
					// Sent successfully
				default:
					// Channel full, just discard for health monitoring
				}
			}
		}()
		return audioLevelCh
	}
	
	// Log missing channel registration to help diagnose configuration issues
	logging.Debug("No unified audio channel registered for URL, returning fallback channel",
		"service", "rtsp-health-watchdog",
		"url", url,
		"operation", "get_audio_level_channel",
		"action", "fallback_channel_created")
	// Return a new channel if none exists (will be discarded but prevents nil)
	return make(chan AudioLevelData, 1)
}
