package myaudio

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/conf"
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
		log.Println("🔍 RTSP health watchdog already running")
		return
	}

	w.running.Store(true)
	go w.monitorLoop()
	log.Printf("🔍 RTSP health watchdog started (check interval: %v, data timeout: %v)",
		w.checkInterval, w.dataTimeout)
}

// Stop halts the health monitoring process
func (w *RTSPHealthWatchdog) Stop() {
	if !w.running.Load() {
		return
	}

	close(w.done)
	w.running.Store(false)
	log.Println("🛑 RTSP health watchdog stopped")
}

// monitorLoop is the main monitoring loop
func (w *RTSPHealthWatchdog) monitorLoop() {
	ticker := time.NewTicker(w.checkInterval)
	defer ticker.Stop()

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

	// Check if FFmpeg process exists
	processInterface, exists := ffmpegProcesses.Load(url)
	if !exists {
		log.Printf("⚠️ RTSP health check: No FFmpeg process found for %s", url)
		w.handleMissingProcess(url, stats)
		return
	}

	// Type assert to FFmpegProcess
	process, ok := processInterface.(*FFmpegProcess)
	if !ok {
		log.Printf("⚠️ RTSP health check: Invalid process type for %s", url)
		return
	}

	// Check if process is alive
	if !w.isProcessAlive(process) {
		log.Printf("⚠️ RTSP health check: FFmpeg process dead for %s", url)
		w.handleDeadProcess(url, stats)
		return
	}

	// Check data reception status
	dataReceiving := w.checkDataReception(url)

	stats.mu.Lock()
	if dataReceiving {
		stats.LastDataReceived = time.Now()
		stats.ConsecutiveTimeouts = 0
		stats.IsHealthy = true
		if !previouslyHealthy {
			log.Printf("✅ RTSP health restored for %s", url)
			telemetry.CaptureMessage(fmt.Sprintf("RTSP stream health restored: %s", url),
				sentry.LevelInfo, "rtsp-health-restored")
		}
	} else {
		timeSinceLastData := time.Since(stats.LastDataReceived)
		if timeSinceLastData > w.dataTimeout {
			stats.ConsecutiveTimeouts++
			stats.IsHealthy = false
			log.Printf("⚠️ RTSP health check: No data from %s for %v (consecutive timeouts: %d)",
				url, timeSinceLastData, stats.ConsecutiveTimeouts)

			// Only attempt restart if not already in progress
			if !stats.RestartInProgress {
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
		log.Printf("🔄 RTSP health watchdog: Starting missing FFmpeg process for %s", url)
		telemetry.CaptureMessage(fmt.Sprintf("RTSP health watchdog starting missing process: %s", url),
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
		log.Printf("🔄 RTSP health watchdog: Restarting dead FFmpeg process for %s", url)
		telemetry.CaptureMessage(fmt.Sprintf("RTSP health watchdog restarting dead process: %s", url),
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
		log.Printf("⚠️ RTSP health watchdog: Restart storm detected for %s, skipping restart", url)
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
			log.Printf("🔄 RTSP health watchdog: Sent restart signal for unhealthy stream %s", url)
			telemetry.CaptureMessage(fmt.Sprintf("RTSP health watchdog triggered restart: %s", url),
				sentry.LevelWarning, "rtsp-health-triggered-restart")
		default:
			log.Printf("⚠️ RTSP health watchdog: Restart channel full for %s", url)
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
		log.Printf("⚠️ RTSP health watchdog: No restart channel found for %s", url)
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

		audioLevelChan := getAudioLevelChannelForURL(url)
		if err := manageFfmpegLifecycle(ctx, config, restartChan, audioLevelChan); err != nil {
			log.Printf("❌ RTSP health watchdog: Failed to start FFmpeg for %s: %v", url, err)
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
			log.Printf("🧹 RTSP health watchdog: Removed stats for %s", url)
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

// audioLevelChannels stores audio level channels for each URL
var audioLevelChannels sync.Map

// RegisterStreamChannels registers the restart and audio level channels for a URL
func RegisterStreamChannels(url string, restartChan chan struct{}, audioLevelChan chan AudioLevelData) {
	if restartChan != nil {
		restartChannels.Store(url, restartChan)
	}
	if audioLevelChan != nil {
		audioLevelChannels.Store(url, audioLevelChan)
	}
}

// UnregisterStreamChannels removes the channels for a URL
func UnregisterStreamChannels(url string) {
	restartChannels.Delete(url)
	audioLevelChannels.Delete(url)
}

// getRestartChannelForURL retrieves the restart channel for a URL
func getRestartChannelForURL(url string) chan struct{} {
	if ch, ok := restartChannels.Load(url); ok {
		return ch.(chan struct{})
	}
	return nil
}

// getAudioLevelChannelForURL retrieves the audio level channel for a URL
func getAudioLevelChannelForURL(url string) chan AudioLevelData {
	if ch, ok := audioLevelChannels.Load(url); ok {
		return ch.(chan AudioLevelData)
	}
	// Return a new channel if none exists (will be discarded but prevents nil)
	return make(chan AudioLevelData, 1)
}
