package myaudio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// ffmpegProcesses keeps track of running FFmpeg processes for each URL
// This is used to ensure that only one FFmpeg process is running per RTSP source
// Moved to ffmpeg_monitor.go to avoid duplicate declaration
// var ffmpegProcesses = &sync.Map{}

// FFmpegConfig holds the configuration for the FFmpeg command
type FFmpegConfig struct {
	URL       string
	Transport string
}

// FFmpegProcess represents a running FFmpeg process
type FFmpegProcess struct {
	cmd            *exec.Cmd          // The FFmpeg command
	cancel         context.CancelFunc // The context cancellation function
	done           <-chan error       // The error channel for the FFmpeg process
	stdout         io.ReadCloser      // The stdout of the FFmpeg process
	restartTracker *FFmpegRestartTracker
	cleanupMutex   sync.Mutex     // Mutex to protect cleanup operations
	cleanupDone    bool           // Flag to track if cleanup has been performed
	startTime      time.Time      // Track when the process started
	stderrBuf      *BoundedBuffer // Buffer for stderr output
}

// audioWatchdog is a struct that keeps track of the last time data was received from the RTSP source
type audioWatchdog struct {
	lastDataTime time.Time
	mu           sync.Mutex
}

// FFmpegRestartTracker keeps track of restart information for each RTSP source
type FFmpegRestartTracker struct {
	restartCount   int
	lastRestartAt  time.Time
	recentRestarts []time.Time // Track recent restart times for rate limiting
	mu             sync.Mutex  // Protect concurrent access
}

// restartTrackers keeps track of restart information for each RTSP source URL
var restartTrackers sync.Map

// startupMutex prevents concurrent FFmpeg process starts for the same URL
var startupMutex sync.Map

// ffmpegPlaceholder is used as a placeholder in the ffmpegProcesses map
// to indicate that a process is being started for a URL
type ffmpegPlaceholder struct{}

// containsPrivateIP172Range checks if URL contains an IP in the 172.16.0.0/12 range (172.16.x.x through 172.31.x.x)
func containsPrivateIP172Range(url string) bool {
	// Check for 172.16 through 172.31
	for i := 16; i <= 31; i++ {
		if strings.Contains(url, fmt.Sprintf("172.%d.", i)) {
			return true
		}
	}
	return false
}

// categorizeStreamURL categorizes a stream URL for telemetry while preserving privacy
func categorizeStreamURL(url string) string {
	if strings.HasPrefix(url, "rtsp://") {
		if strings.Contains(url, "localhost") || strings.Contains(url, "127.0.0.1") {
			return "rtsp-local"
		}
		if strings.Contains(url, "192.168.") || strings.Contains(url, "10.") ||
			containsPrivateIP172Range(url) {
			return "rtsp-private"
		}
		return "rtsp-external"
	}
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return "http-stream"
	}
	return "other-stream"
}

// backoffStrategy implements an exponential backoff for retries
type backoffStrategy struct {
	attempt      int
	maxAttempts  int
	initialDelay time.Duration
	maxDelay     time.Duration
}

// BoundedBuffer is a thread-safe bounded buffer for storing the most recent data
// this is used to store the stderr output from FFmpeg
type BoundedBuffer struct {
	buffer bytes.Buffer
	mu     sync.Mutex
	size   int
}

// NewBoundedBuffer creates a new BoundedBuffer with the specified size
func NewBoundedBuffer(size int) *BoundedBuffer {
	return &BoundedBuffer{
		size: size,
	}
}

// Write implements the io.Writer interface
func (b *BoundedBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.buffer.Len()+len(p) > b.size {
		// If the new data would exceed the buffer size, trim the existing data
		b.buffer.Reset()
		if len(p) > b.size {
			// If the new data is larger than the buffer size, only keep the last 'size' bytes
			p = p[len(p)-b.size:]
		}
	}
	return b.buffer.Write(p)
}

// String returns the contents of the buffer as a string
func (b *BoundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// newBackoffStrategy creates a new backoff strategy with the given parameters
// If maxAttempts is -1, the strategy will retry indefinitely
func newBackoffStrategy(maxAttempts int, initialDelay, maxDelay time.Duration) *backoffStrategy {
	return &backoffStrategy{
		maxAttempts:  maxAttempts,
		initialDelay: initialDelay,
		maxDelay:     maxDelay,
	}
}

// nextDelay returns the next delay and a boolean indicating if the maximum number of attempts has been reached
// If maxAttempts is -1, it will retry indefinitely
func (b *backoffStrategy) nextDelay() (time.Duration, bool) {
	// If maxAttempts is -1, allow unlimited retries
	if b.maxAttempts > 0 && b.attempt >= b.maxAttempts {
		return 0, false
	}
	delay := b.initialDelay * time.Duration(1<<uint(b.attempt)) //nolint:gosec // G115: attempt bounded by retry logic
	if delay > b.maxDelay {
		delay = b.maxDelay
	}
	b.attempt++
	return delay, true
}

// reset resets the backoff strategy
func (b *backoffStrategy) reset() {
	b.attempt = 0
}

// getRestartTracker retrieves or creates a restart tracker for a given FFmpeg command
func getRestartTracker(cmd *exec.Cmd) *FFmpegRestartTracker {
	// Create a more unique key by including the command path, args, and process address
	// This prevents different RTSP streams from sharing restart trackers
	key := fmt.Sprintf("%s_%v_%p", cmd.Path, cmd.Args, cmd)
	tracker, ok := restartTrackers.Load(key)
	if !ok {
		tracker = &FFmpegRestartTracker{
			restartCount:  0,
			lastRestartAt: time.Now(),
		}
		restartTrackers.Store(key, tracker)
	}
	return tracker.(*FFmpegRestartTracker)
}

// updateRestartInfo updates the restart information for the given FFmpeg process
func (p *FFmpegProcess) updateRestartInfo() {
	if p == nil || p.restartTracker == nil || p.cmd == nil {
		log.Printf("⚠️ Attempted to update restart info on nil process or command")
		return
	}

	p.restartTracker.mu.Lock()
	defer p.restartTracker.mu.Unlock()

	now := time.Now()

	// Clean up old restart entries (older than 5 minutes)
	cutoff := now.Add(-5 * time.Minute)
	filtered := []time.Time{}
	for _, t := range p.restartTracker.recentRestarts {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	p.restartTracker.recentRestarts = filtered

	// Add current restart
	p.restartTracker.recentRestarts = append(p.restartTracker.recentRestarts, now)

	// if the last restart was more than a minute ago, reset the restart count
	if now.Sub(p.restartTracker.lastRestartAt) > time.Minute {
		p.restartTracker.restartCount = 0
	}
	p.restartTracker.restartCount++
	p.restartTracker.lastRestartAt = now
}

// isRestartStorm checks if we're experiencing too many restarts in a short period
func (p *FFmpegProcess) isRestartStorm() bool {
	if p == nil || p.restartTracker == nil {
		return false
	}

	p.restartTracker.mu.Lock()
	defer p.restartTracker.mu.Unlock()

	// Check if we have more than 5 restarts in the last minute
	oneMinuteAgo := time.Now().Add(-1 * time.Minute)
	recentCount := 0
	for _, t := range p.restartTracker.recentRestarts {
		if t.After(oneMinuteAgo) {
			recentCount++
		}
	}

	return recentCount > 5
}

// getRestartDelay returns the delay before the next restart attempt
func (p *FFmpegProcess) getRestartDelay() time.Duration {
	// Check for restart storm
	if p.isRestartStorm() {
		log.Printf("⚠️ Restart storm detected, applying extended backoff")
		return 5 * time.Minute // Extended delay during restart storm
	}

	delay := time.Duration(p.restartTracker.restartCount) * 5 * time.Second
	if delay > 2*time.Minute {
		delay = 2 * time.Minute // Cap the maximum delay at 2 minutes
	}
	return delay
}

// Cleanup cleans up the FFmpeg process and removes it from the map
// This method is thread-safe and prevents race conditions during concurrent cleanup calls
func (p *FFmpegProcess) Cleanup(url string) {
	p.CleanupWithDelete(url, true)
}

func (p *FFmpegProcess) CleanupWithDelete(url string, shouldDelete bool) {
	if p == nil {
		if shouldDelete {
			ffmpegProcesses.Delete(url)
		}
		return
	}

	// Log cleanup attempt with telemetry
	telemetry.CaptureMessage(fmt.Sprintf("Starting FFmpeg cleanup for %s", url),
		sentry.LevelInfo, "ffmpeg-cleanup-start")

	// Use mutex to ensure only one cleanup operation per process
	p.cleanupMutex.Lock()
	defer p.cleanupMutex.Unlock()

	// Check if cleanup has already been performed
	if p.cleanupDone {
		return
	}

	// Mark cleanup as in progress
	p.cleanupDone = true

	// Check if process exists before attempting cleanup
	if p.cmd == nil || p.cmd.Process == nil {
		if shouldDelete {
			ffmpegProcesses.Delete(url)
		}
		return
	}

	// First close stdout to prevent blocking reads
	if p.stdout != nil {
		if err := p.stdout.Close(); err != nil {
			log.Printf("Failed to close FFmpeg stdout: %v", err)
		}
	}

	// Cancel the context to signal process termination
	if p.cancel != nil {
		p.cancel()
	}

	// Use a timeout to wait for the process to finish
	done := make(chan struct{})
	go func() {
		<-p.done
		close(done)
	}()

	select {
	case <-done:
		log.Printf("🛑 FFmpeg process for RTSP source %s stopped normally", url)
		telemetry.CaptureMessage(fmt.Sprintf("FFmpeg process for %s stopped normally", url),
			sentry.LevelInfo, "ffmpeg-cleanup-normal")
		// Process finished normally
	case <-time.After(10 * time.Second):
		// Timeout occurred, forcefully kill the process
		telemetry.CaptureMessage(fmt.Sprintf("FFmpeg cleanup timeout for %s, forcing termination", url),
			sentry.LevelWarning, "ffmpeg-cleanup-timeout")
		if err := killProcessGroup(p.cmd); err != nil {
			// Only attempt direct process kill if killProcessGroup fails
			if err := p.cmd.Process.Kill(); err != nil {
				// Only log if both kill attempts fail and process still exists
				if !strings.Contains(err.Error(), "process already finished") {
					log.Printf("⚠️ Failed to kill FFmpeg process for %s: %v", url, err)
					telemetry.CaptureError(fmt.Errorf("failed to kill FFmpeg process for %s: %w", url, err), "ffmpeg-kill-failure")
				}
			}
		}

		// Verify the process is actually terminated
		verifyTimer := time.NewTimer(2 * time.Second)
		select {
		case <-done:
			verifyTimer.Stop()
			log.Printf("✅ FFmpeg process for %s confirmed terminated after force kill", url)
		case <-verifyTimer.C:
			log.Printf("❌ FFmpeg process for %s may still be running after force kill attempt", url)
			telemetry.CaptureMessage(fmt.Sprintf("FFmpeg process for %s possibly stuck after kill attempts", url),
				sentry.LevelError, "ffmpeg-zombie-process")
		}
	}

	// Clean up restart tracker to prevent memory leak
	if p.cmd != nil {
		key := fmt.Sprintf("%s_%v_%p", p.cmd.Path, p.cmd.Args, p.cmd)
		restartTrackers.Delete(key)
	}

	// Only delete from process map if requested
	if shouldDelete {
		ffmpegProcesses.Delete(url)
	}
}

// startWatchdog starts a goroutine that monitors the audio stream for inactivity
func (p *FFmpegProcess) startWatchdog(ctx context.Context, url string, watchdog *audioWatchdog) <-chan struct{} {
	watchdogDone := make(chan struct{})
	go func() {
		defer close(watchdogDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Check if the stream is still configured
				settings := conf.Setting()
				streamConfigured := false
				for _, configuredURL := range settings.Realtime.RTSP.URLs {
					if configuredURL == url {
						streamConfigured = true
						break
					}
				}

				// Only check watchdog timeout if the stream is still configured
				if streamConfigured && watchdog.timeSinceLastData() > 60*time.Second {
					log.Printf("⚠️ No data received from RTSP source %s for 60 seconds, triggering restart", url)
					return
				} else if !streamConfigured {
					return
				}
			}
		}
	}()
	return watchdogDone
}

// isStreamConfigured checks if the stream URL is still in configuration
func (p *FFmpegProcess) isStreamConfigured(url string) bool {
	settings := conf.Setting()
	for _, configuredURL := range settings.Realtime.RTSP.URLs {
		if configuredURL == url {
			return true
		}
	}
	return false
}

// sendRestartSignal sends restart signal with timeout and channel clearing
// This function is thread-safe and handles race conditions when multiple goroutines
// attempt to send restart signals simultaneously
func (p *FFmpegProcess) sendRestartSignal(restartChan chan struct{}, url, reason string) {
	// First attempt: try non-blocking send
	select {
	case restartChan <- struct{}{}:
		log.Printf("🔄 %s triggered restart for RTSP source %s", reason, url)
		return
	default:
		// Channel is full, proceed to drain and retry
	}

	// Second attempt: drain old signals and count them
	drainedCount := 0
	for {
		select {
		case <-restartChan:
			drainedCount++
		default:
			// Channel is now empty, break out of drain loop
			goto sendAttempt
		}
	}

sendAttempt:
	if drainedCount > 0 {
		log.Printf("🔄 Drained %d old restart signals for RTSP source %s", drainedCount, url)
	}

	// Third attempt: try to send after draining
	select {
	case restartChan <- struct{}{}:
		log.Printf("🔄 %s triggered restart for RTSP source %s (after draining %d signals)", reason, url, drainedCount)
	default:
		// Another goroutine filled the channel between draining and sending
		log.Printf("⚠️ Another restart signal was just sent for %s, skipping duplicate", url)
	}
}

// handleWatchdogTimeout handles timeout from watchdog
func (p *FFmpegProcess) handleWatchdogTimeout(url string, restartChan chan struct{}) error {
	if !p.isStreamConfigured(url) {
		return nil
	}

	// Capture watchdog timeout event
	telemetry.CaptureMessage(fmt.Sprintf("Watchdog timeout detected for RTSP stream %s", url),
		sentry.LevelWarning, "rtsp-watchdog-timeout")

	p.sendRestartSignal(restartChan, url, "Watchdog")
	return errors.New(fmt.Errorf("watchdog detected no data for RTSP source %s", url)).
		Category(errors.CategoryRTSP).
		Component("ffmpeg-watchdog").
		Context("url_type", categorizeStreamURL(url)).
		Build()
}

// processAudioData processes a chunk of audio data and handles buffer errors
// Returns an error if processing should stop, nil if it should continue
func (p *FFmpegProcess) processAudioData(url string, data []byte, bufferErrorCount *int, lastBufferErrorTime *time.Time, restartChan chan struct{}, unifiedAudioChan chan UnifiedAudioData) error {
	const maxBufferErrors = 10
	hasBufferError := false

	// Write the audio data to the analysis buffer
	if err := WriteToAnalysisBuffer(url, data); err != nil {
		log.Printf("❌ Error writing to analysis buffer for RTSP source %s: %v", url, err)
		telemetry.CaptureError(fmt.Errorf("analysis buffer write error for %s: %w", url, err), "rtsp-analysis-buffer-error")
		hasBufferError = true
	} else {
		// Update health watchdog that we received data
		UpdateStreamDataReceived(url)
	}

	// Write the audio data to the capture buffer
	if err := WriteToCaptureBuffer(url, data); err != nil {
		log.Printf("❌ Error writing to capture buffer for RTSP source %s: %v", url, err)
		telemetry.CaptureError(fmt.Errorf("capture buffer write error for %s: %w", url, err), "rtsp-capture-buffer-error")
		hasBufferError = true
	}

	// Handle buffer error accumulation
	if hasBufferError {
		*bufferErrorCount++
		*lastBufferErrorTime = time.Now()

		if *bufferErrorCount >= maxBufferErrors {
			log.Printf("⚠️ Too many buffer write errors (%d) for RTSP source %s since %v, triggering restart", *bufferErrorCount, url, lastBufferErrorTime.Format("15:04:05"))
			// Capture buffer error threshold exceeded
			telemetry.CaptureMessage(fmt.Sprintf("Buffer error threshold exceeded for %s: %d errors", url, *bufferErrorCount),
				sentry.LevelError, "rtsp-buffer-error-threshold")
			p.sendRestartSignal(restartChan, url, "Buffer error threshold")
			return errors.New(fmt.Errorf("too many buffer write errors for RTSP source %s", url)).
				Category(errors.CategoryAudio).
				Component("ffmpeg-buffer").
				Context("url_type", categorizeStreamURL(url)).
				Context("error_count", *bufferErrorCount).
				Build()
		}

		time.Sleep(1 * time.Second)
		return nil // Continue processing after temporary error
	}

	// Reset error count on successful buffer writes
	*bufferErrorCount = 0

	// Broadcast audio data to WebSocket clients
	BroadcastAudioData(url, data)

	// Calculate audio level
	audioLevelData := calculateAudioLevel(data, url, "")

	// Create unified audio data structure
	unifiedData := UnifiedAudioData{
		AudioLevel: audioLevelData,
		Timestamp:  time.Now(),
	}

	// Process sound level data if enabled - this may be nil if 10-second window isn't complete
	if conf.Setting().Realtime.Audio.SoundLevel.Enabled {
		if soundLevelData, err := ProcessSoundLevelData(url, data); err != nil {
			log.Printf("❌ Error processing sound level data for RTSP source %s: %v", url, err)
		} else if soundLevelData != nil {
			// Attach sound level data when available
			unifiedData.SoundLevel = soundLevelData
		}
	}

	// Send unified data to channel (non-blocking)
	select {
	case unifiedAudioChan <- unifiedData:
		// Successfully sent data
	default:
		// Channel is full, drop the data to avoid blocking audio processing
		// Audio data is not critical and can be dropped for RTSP processing
	}

	// Continue processing - return nil to indicate success
	return nil
}

// processAudio reads audio data from FFmpeg's stdout and writes it to buffers
func (p *FFmpegProcess) processAudio(ctx context.Context, url string, restartChan chan struct{}, unifiedAudioChan chan UnifiedAudioData) error {
	// Create a buffer to store audio data
	buf := make([]byte, 32768)
	watchdog := &audioWatchdog{lastDataTime: time.Now()}

	// Track buffer write errors to detect persistent failures
	var bufferErrorCount int
	var lastBufferErrorTime time.Time

	// Start watchdog goroutine
	watchdogDone := p.startWatchdog(ctx, url, watchdog)

	// Continuously process audio data
	for {
		select {
		case <-ctx.Done():
			log.Printf("Stopping audio processing for RTSP source: %s", url)
			<-watchdogDone // Wait for watchdog to finish
			return nil     // Return nil on normal shutdown
		case <-watchdogDone:
			return p.handleWatchdogTimeout(url, restartChan)
		default:
			// Read audio data from FFmpeg's stdout
			n, err := p.stdout.Read(buf)
			if err != nil {
				<-watchdogDone // Wait for watchdog to finish

				// Check if FFmpeg exited too quickly (within 5 seconds)
				runtime := time.Since(p.startTime)
				if runtime < 5*time.Second {
					// FFmpeg exited too quickly, likely a connection or configuration error
					stderrOutput := ""
					if p.stderrBuf != nil {
						stderrOutput = p.stderrBuf.String()
					}

					if stderrOutput != "" {
						log.Printf("⚠️ FFmpeg exited quickly (runtime: %v) with stderr: %s", runtime, stderrOutput)
						return errors.New(fmt.Errorf("FFmpeg exited too quickly (runtime: %v): %s", runtime, stderrOutput)).
							Category(errors.CategoryRTSP).
							Component("ffmpeg-quick-exit").
							Context("url_type", categorizeStreamURL(url)).
							Context("runtime_seconds", runtime.Seconds()).
							Build()
					} else {
						return errors.New(fmt.Errorf("FFmpeg exited too quickly (runtime: %v) - likely connection failure", runtime)).
							Category(errors.CategoryRTSP).
							Component("ffmpeg-quick-exit").
							Context("url_type", categorizeStreamURL(url)).
							Context("runtime_seconds", runtime.Seconds()).
							Build()
					}
				}

				// Check if this is a normal shutdown
				if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "file already closed") {
					return nil
				}
				// Only return error for unexpected failures
				return errors.New(fmt.Errorf("error reading from ffmpeg: %w", err)).
					Category(errors.CategoryRTSP).
					Component("ffmpeg-audio-read").
					Context("url_type", categorizeStreamURL(url)).
					Build()
			}

			// Ensure we don't process more data than we've read
			if n > 0 {
				watchdog.update() // Update the watchdog timestamp

				err := p.processAudioData(url, buf[:n], &bufferErrorCount, &lastBufferErrorTime, restartChan, unifiedAudioChan)
				if err != nil {
					return err
				}
				// nil error means continue processing
			}
		}
	}
}

// startFFmpeg starts an FFmpeg process with the given configuration
func startFFmpeg(ctx context.Context, config FFmpegConfig) (*FFmpegProcess, error) {
	settings := conf.Setting().Realtime.Audio
	if err := validateFFmpegPath(settings.FfmpegPath); err != nil {
		enhancedErr := errors.New(fmt.Errorf("FFmpeg path validation failed for %s: %w", config.URL, err)).
			Category(errors.CategoryValidation).
			Component("ffmpeg-validation").
			Context("url_type", categorizeStreamURL(config.URL)).
			Build()
		telemetry.CaptureError(enhancedErr, "ffmpeg-path-validation")
		return nil, enhancedErr
	}

	// Create a new context with cancellation
	ctx, cancel := context.WithCancel(ctx)

	// Track when the process starts
	startTime := time.Now()

	// Get the FFmpeg-compatible values for sample rate, channels, and bit depth
	ffmpegSampleRate, ffmpegNumChannels, ffmpegFormat := getFFmpegFormat(conf.SampleRate, conf.NumChannels, conf.BitDepth)

	// Prepare the FFmpeg command with appropriate arguments
	cmd := exec.CommandContext(ctx, settings.FfmpegPath, // #nosec G204 -- FfmpegPath is validated via ValidateToolPath(), args are passed as separate parameters not shell-executed
		"-rtsp_transport", config.Transport, // Set RTSP transport protocol
		"-i", config.URL, // Input URL
		"-loglevel", "error", // Set log level to error
		"-vn",              // Disable video
		"-f", ffmpegFormat, // Set output format to signed 16-bit little-endian
		"-ar", ffmpegSampleRate, // Set audio sample rate to 48kHz
		"-ac", ffmpegNumChannels, // Set number of audio channels to 1 (mono)
		"-hide_banner", // Hide the banner
		"pipe:1",       // Output to stdout
	)

	// Set up platform-specific process group
	setupProcessGroup(cmd)

	// Create a bounded buffer for stderr with a 4KB limit
	stderrBuf := NewBoundedBuffer(4096)
	cmd.Stderr = stderrBuf

	// Create a pipe to read FFmpeg's stdout
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel() // Cancel the context if pipe creation fails
		enhancedErr := errors.New(fmt.Errorf("FFmpeg pipe creation failed for %s: %w", config.URL, err)).
			Category(errors.CategorySystem).
			Component("ffmpeg-pipe").
			Context("url_type", categorizeStreamURL(config.URL)).
			Build()
		telemetry.CaptureError(enhancedErr, "ffmpeg-pipe-creation")
		return nil, enhancedErr
	}

	// Log the FFmpeg command for debugging purposes
	log.Printf("⬆️ Starting FFmpeg with command: %s", cmd.String())

	// Start the FFmpeg process
	if err := cmd.Start(); err != nil {
		cancel() // Cancel the context if process start fails
		enhancedErr := errors.New(fmt.Errorf("FFmpeg process start failed for %s: %w", config.URL, err)).
			Category(errors.CategorySystem).
			Component("ffmpeg-process").
			Context("url_type", categorizeStreamURL(config.URL)).
			Build()
		telemetry.CaptureError(enhancedErr, "ffmpeg-process-start")
		return nil, enhancedErr
	} else {
		// Log successful FFmpeg start with telemetry
		telemetry.CaptureMessage(fmt.Sprintf("FFmpeg process started successfully for %s (PID: %d)", config.URL, cmd.Process.Pid),
			sentry.LevelInfo, "ffmpeg-process-started")
		log.Printf("✅ FFmpeg started successfully for RTSP source %s", config.URL)
	}

	// Create a channel to receive the exit status of the FFmpeg process
	done := make(chan error, 1)
	go func() {
		// Wait for the FFmpeg process to exit
		err := cmd.Wait()

		// Check if FFmpeg exited quickly (potential connection issue)
		runtime := time.Since(startTime)
		if runtime < 5*time.Second {
			// FFmpeg exited too quickly, log stderr regardless of error status
			stderrOutput := stderrBuf.String()
			if stderrOutput != "" {
				log.Printf("⚠️ FFmpeg exited quickly (runtime: %v) for RTSP source %s with stderr:\n%s", runtime, config.URL, stderrOutput)
				if err == nil {
					// Create an error if FFmpeg exited with status 0 but too quickly
					err = fmt.Errorf("FFmpeg exited too quickly (runtime: %v) with stderr: %s", runtime, stderrOutput)
				} else {
					err = fmt.Errorf("%w\nStderr: %s", err, stderrOutput)
				}
			} else {
				log.Printf("⚠️ FFmpeg exited quickly (runtime: %v) for RTSP source %s with no stderr output", runtime, config.URL)
				if err == nil {
					err = fmt.Errorf("FFmpeg exited too quickly (runtime: %v) - possible connection failure", runtime)
				}
			}
		} else if err != nil {
			// Normal error logging for longer-running processes
			if !strings.Contains(err.Error(), "signal: killed") && !errors.Is(err, context.Canceled) {
				log.Printf("⚠️ FFmpeg process for RTSP source %s exited with error: %v", config.URL, err)
				// Include stderr in the error if available
				if stderrBuf.String() != "" {
					log.Printf("⚠️ FFmpeg process stderr:\n%v", stderrBuf.String())
					err = fmt.Errorf("%w\nStderr: %s", err, stderrBuf.String())
				}
			}
		}
		done <- err
	}()

	// get the restart tracker for the FFmpeg command
	restartTracker := getRestartTracker(cmd)

	// Return a new FFmpegProcess struct with all necessary information
	return &FFmpegProcess{
		cmd:            cmd,
		cancel:         cancel,
		done:           done,
		stdout:         stdout,
		restartTracker: restartTracker,
		startTime:      startTime,
		stderrBuf:      stderrBuf,
	}, nil
}

// lifecycleManager handles the complete lifecycle of an FFmpeg process
type lifecycleManager struct {
	config           FFmpegConfig
	backoff          *backoffStrategy
	restartChan      chan struct{}
	unifiedAudioChan chan UnifiedAudioData
}

// newLifecycleManager creates a new lifecycle manager with unlimited retries
func newLifecycleManager(config FFmpegConfig, restartChan chan struct{}, unifiedAudioChan chan UnifiedAudioData) *lifecycleManager {
	return &lifecycleManager{
		config:           config,
		backoff:          newBackoffStrategy(-1, 5*time.Second, 2*time.Minute), // Unlimited retries
		restartChan:      restartChan,
		unifiedAudioChan: unifiedAudioChan,
	}
}

// isStreamConfigured checks if the stream URL is still configured in settings
func (lm *lifecycleManager) isStreamConfigured() bool {
	settings := conf.Setting()
	for _, url := range settings.Realtime.RTSP.URLs {
		if url == lm.config.URL {
			return true
		}
	}
	return false
}

// cleanupProcessFromMap removes and cleans up a process from the global map
func (lm *lifecycleManager) cleanupProcessFromMap() {
	if process, loaded := ffmpegProcesses.LoadAndDelete(lm.config.URL); loaded {
		if p, ok := process.(*FFmpegProcess); ok {
			// Use CleanupWithDelete(false) since we already removed it with LoadAndDelete
			p.CleanupWithDelete(lm.config.URL, false)
		}
	}
}

// startProcessWithRetry attempts to start FFmpeg with backoff retry logic
func (lm *lifecycleManager) startProcessWithRetry(ctx context.Context) (*FFmpegProcess, error) {
	for {
		// Check if stream is still configured before each attempt
		if !lm.isStreamConfigured() {
			lm.cleanupProcessFromMap()
			return nil, fmt.Errorf("stream %s no longer configured", lm.config.URL)
		}

		// Double-check if a process already exists (race condition protection)
		if existing, exists := ffmpegProcesses.Load(lm.config.URL); exists {
			// Check if it's an actual running process
			if p, ok := existing.(*FFmpegProcess); ok && p.cmd != nil && p.cmd.Process != nil {
				log.Printf("⚠️ FFmpeg process already exists during retry for URL %s (PID: %d)", lm.config.URL, p.cmd.Process.Pid)
				return nil, fmt.Errorf("FFmpeg process already running for URL: %s", lm.config.URL)
			}
			// Note: We skip placeholder checks here because this function is called by the same
			// goroutine that created the placeholder, and needs to proceed to replace it with the actual process
		}

		// Attempt to start FFmpeg process
		process, err := startFFmpeg(ctx, lm.config)
		if err != nil {
			// Clean up any placeholders from failed attempts
			ffmpegProcesses.Delete(lm.config.URL)

			// Get next delay and check if we should retry
			delay, retry := lm.backoff.nextDelay()
			if !retry {
				// This should never happen with unlimited retries (-1), but keep as safeguard
				log.Printf("⚠️ Backoff strategy unexpectedly returned no retry for RTSP source %s: %v", lm.config.URL, err)
				telemetry.CaptureError(fmt.Errorf("FFmpeg backoff exhausted for %s: %w", lm.config.URL, err), "ffmpeg-backoff-exhausted")
				return nil, fmt.Errorf("failed to start FFmpeg after maximum attempts: %w", err)
			}

			log.Printf("⚠️ Failed to start FFmpeg for RTSP source %s: %v. Retrying in %v...", lm.config.URL, err, delay)
			// Track retry attempts with telemetry
			telemetry.CaptureMessage(fmt.Sprintf("FFmpeg retry attempt for %s: delay=%v",
				lm.config.URL, delay), sentry.LevelWarning, "ffmpeg-retry-attempt")

			// Wait for delay, context cancellation, or restart signal
			if waitErr := lm.waitWithInterrupts(ctx, delay); waitErr != nil {
				return nil, waitErr
			}
			continue
		}

		// Success - reset backoff and return process
		lm.backoff.reset()
		// Log successful connection after retries
		telemetry.CaptureMessage(fmt.Sprintf("FFmpeg connection established for %s after retries", lm.config.URL),
			sentry.LevelInfo, "ffmpeg-connection-success")
		return process, nil
	}
}

// waitWithInterrupts waits for a duration while allowing interruption by context or restart signals
func (lm *lifecycleManager) waitWithInterrupts(ctx context.Context, duration time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(duration):
		return nil // Normal completion
	case <-lm.restartChan:
		log.Printf("🔄 Restart signal received during wait, restarting FFmpeg for RTSP source %s immediately.", lm.config.URL)
		lm.backoff.reset()
		return nil // Continue with immediate restart
	}
}

// runProcessAndWait runs the process and waits for completion or restart signals
func (lm *lifecycleManager) runProcessAndWait(ctx context.Context, process *FFmpegProcess) (processEnded, wasManualRestart bool, err error) {
	// Store the process in the map
	ffmpegProcesses.Store(lm.config.URL, process)

	// Start processing audio in a separate goroutine
	processDone := make(chan error, 1)
	go func() {
		processDone <- process.processAudio(ctx, lm.config.URL, lm.restartChan, lm.unifiedAudioChan)
	}()

	// Wait for process completion, context cancellation, or restart signal
	select {
	case <-ctx.Done():
		process.Cleanup(lm.config.URL)
		return false, false, ctx.Err()

	case err := <-processDone:
		process.Cleanup(lm.config.URL)

		// Check if stream is still configured after process ends
		if !lm.isStreamConfigured() {
			lm.cleanupProcessFromMap()
			return false, false, fmt.Errorf("stream %s no longer configured", lm.config.URL)
		}

		if err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("⚠️ FFmpeg process for RTSP source %s ended unexpectedly: %v", lm.config.URL, err)
			// Return the error so the lifecycle manager can handle it with proper backoff
			return true, false, err
		}
		return true, false, nil // Process ended normally

	case <-lm.restartChan:
		log.Printf("🔄 Restart signal received, restarting FFmpeg for RTSP source %s.", lm.config.URL)
		// Update restart info BEFORE cleanup to ensure valid process state
		process.updateRestartInfo()
		process.Cleanup(lm.config.URL)
		lm.backoff.reset()
		return true, true, nil // Manual restart
	}
}

// handleRestartDelay handles the delay between process restarts
func (lm *lifecycleManager) handleRestartDelay(ctx context.Context, process *FFmpegProcess, wasManualRestart bool) error {
	// Check if stream is still configured before restart delay
	if !lm.isStreamConfigured() {
		log.Printf("🛑 Stream %s is no longer configured, stopping lifecycle manager", lm.config.URL)
		lm.cleanupProcessFromMap()
		return fmt.Errorf("stream %s no longer configured", lm.config.URL)
	}

	// Update restart information and get delay (only if not already updated for manual restart)
	if !wasManualRestart {
		process.updateRestartInfo()
	}
	delay := process.getRestartDelay()

	// Wait for delay, context cancellation, or restart signal
	return lm.waitWithInterrupts(ctx, delay)
}

// manageFfmpegLifecycle manages the complete lifecycle of an FFmpeg process with simplified logic
func manageFfmpegLifecycle(ctx context.Context, config FFmpegConfig, restartChan chan struct{}, unifiedAudioChan chan UnifiedAudioData) error {
	// Get or create a mutex for this URL to prevent concurrent starts
	mutexInterface, _ := startupMutex.LoadOrStore(config.URL, &sync.Mutex{})
	mutex := mutexInterface.(*sync.Mutex)

	// Lock to prevent concurrent starts for the same URL
	mutex.Lock()

	// Check if a process is already running for this URL
	if existing, exists := ffmpegProcesses.Load(config.URL); exists {
		// Check if it's a placeholder
		if _, isPlaceholder := existing.(*ffmpegPlaceholder); isPlaceholder {
			log.Printf("⚠️ FFmpeg process is already being started for URL %s", config.URL)
			mutex.Unlock()
			return fmt.Errorf("FFmpeg process already being started for URL: %s", config.URL)
		}
		// Check if it's an actual process
		if p, ok := existing.(*FFmpegProcess); ok && p.cmd != nil && p.cmd.Process != nil {
			log.Printf("⚠️ FFmpeg process already exists for URL %s (PID: %d), not starting duplicate", config.URL, p.cmd.Process.Pid)
			mutex.Unlock()
			return fmt.Errorf("FFmpeg process already running for URL: %s", config.URL)
		}
	}

	// Store a placeholder to prevent other goroutines from starting
	placeholder := &ffmpegPlaceholder{}
	ffmpegProcesses.Store(config.URL, placeholder)
	mutex.Unlock()

	manager := newLifecycleManager(config, restartChan, unifiedAudioChan)

	for {
		// Check if stream is configured before starting
		if !manager.isStreamConfigured() {
			manager.cleanupProcessFromMap()
			return nil
		}

		// Start FFmpeg process with retry logic
		process, err := manager.startProcessWithRetry(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			// For non-context errors, continue the lifecycle loop
			continue
		}

		// Run the process and wait for completion or restart
		processEnded, wasManualRestart, err := manager.runProcessAndWait(ctx, process)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			// For stream-no-longer-configured errors, return
			if strings.Contains(err.Error(), "no longer configured") {
				return nil
			}
			// For FFmpeg quick exit errors, update restart info and apply backoff
			if strings.Contains(err.Error(), "FFmpeg exited too quickly") {
				process.updateRestartInfo()
				// Handle restart delay before next iteration
				if delayErr := manager.handleRestartDelay(ctx, process, false); delayErr != nil {
					if errors.Is(delayErr, context.Canceled) {
						return delayErr
					}
					if strings.Contains(delayErr.Error(), "no longer configured") {
						return nil
					}
				}
			}
			// For other errors, continue the lifecycle loop
			continue
		}

		// If process didn't end (context was cancelled), return
		if !processEnded {
			return nil
		}

		// Handle restart delay before next iteration
		if delayErr := manager.handleRestartDelay(ctx, process, wasManualRestart); delayErr != nil {
			if errors.Is(delayErr, context.Canceled) {
				return delayErr
			}
			// For stream-no-longer-configured errors, return
			if strings.Contains(delayErr.Error(), "no longer configured") {
				return nil
			}
			// For other errors, continue to next iteration
		}
	}
}

// CaptureAudioRTSP is the main function for capturing audio from an RTSP stream
func CaptureAudioRTSP(url, transport string, wg *sync.WaitGroup, quitChan <-chan struct{}, restartChan chan struct{}, unifiedAudioChan chan UnifiedAudioData) {
	// Register the channels for this stream
	RegisterStreamChannels(url, restartChan, unifiedAudioChan)
	defer UnregisterStreamChannels(url)

	// Initialize sound level processor for this RTSP source if enabled
	settings := conf.Setting()
	displayName := conf.SanitizeRTSPUrl(url)
	if settings.Realtime.Audio.SoundLevel.Enabled {
		if err := RegisterSoundLevelProcessor(url, displayName); err != nil {
			log.Printf("❌ Error initializing sound level processor for RTSP source %s: %v", url, err)
		}
		defer UnregisterSoundLevelProcessor(url)
	}

	// Return with error if FFmpeg path is not set
	if conf.GetFfmpegBinaryName() == "" {
		err := fmt.Errorf("FFmpeg not available for RTSP source %s", url)
		log.Printf("❌ FFmpeg is not available, cannot capture audio from RTSP source %s.", url)
		log.Printf("⚠️ Please make sure FFmpeg is installed and included in system PATH.")
		telemetry.CaptureError(err, "rtsp-ffmpeg-unavailable")
		return
	}

	// Log RTSP connection attempt with telemetry
	telemetry.CaptureMessage(fmt.Sprintf("Starting RTSP capture for %s with transport %s", url, transport),
		sentry.LevelInfo, "rtsp-connection-start")

	// Create a configuration for FFmpeg
	config := FFmpegConfig{
		URL:       url,
		Transport: transport,
	}

	// Create a new context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	// Ensure the cancel function is called when the function exits
	defer cancel()

	// Start a goroutine to handle the quit signal
	go func() {
		// Wait for a signal on the quit channel
		<-quitChan
		// Log that a quit signal was received
		log.Printf("🔴 Quit signal received, stopping FFmpeg for RTSP source %s.", url)
		// Cancel the context to stop all operations
		cancel()
	}()

	// Manage the FFmpeg lifecycle
	err := manageFfmpegLifecycle(ctx, config, restartChan, unifiedAudioChan)
	// If an error occurred and it's not due to context cancellation, log it and report to user
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("⚠️ FFmpeg lifecycle manager for RTSP source %s exited with error: %v", url, err)
		telemetry.CaptureError(fmt.Errorf("RTSP lifecycle error for %s: %w", url, err), "rtsp-lifecycle-error")
	}
}

// audioWatchdog is a struct that keeps track of the last time data was received from the RTSP source
func (w *audioWatchdog) update() {
	w.mu.Lock()
	w.lastDataTime = time.Now()
	w.mu.Unlock()
}

// timeSinceLastData returns the time since the last data was received from the RTSP source
func (w *audioWatchdog) timeSinceLastData() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return time.Since(w.lastDataTime)
}
