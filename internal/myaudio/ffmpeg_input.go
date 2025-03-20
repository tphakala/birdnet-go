package myaudio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
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
}

// audioWatchdog is a struct that keeps track of the last time data was received from the RTSP source
type audioWatchdog struct {
	lastDataTime time.Time
	mu           sync.Mutex
}

// silenceTimeout is the amount of time to wait before triggering a restart if no data is received
const silenceTimeout = 60

// FFmpegRestartTracker keeps track of restart information for each RTSP source
type FFmpegRestartTracker struct {
	restartCount  int
	lastRestartAt time.Time
	mu            sync.Mutex // Add mutex for thread safety
}

// restartTrackers keeps track of restart information for each RTSP source URL
var restartTrackers sync.Map

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
func newBackoffStrategy(maxAttempts int, initialDelay, maxDelay time.Duration) *backoffStrategy {
	return &backoffStrategy{
		maxAttempts:  maxAttempts,
		initialDelay: initialDelay,
		maxDelay:     maxDelay,
	}
}

// nextDelay returns the next delay and a boolean indicating if the maximum number of attempts has been reached
func (b *backoffStrategy) nextDelay() (time.Duration, bool) {
	if b.attempt >= b.maxAttempts {
		return 0, false
	}
	delay := b.initialDelay * time.Duration(1<<uint(b.attempt))
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
	key := fmt.Sprintf("%s", cmd.Args)
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
	now := time.Now()
	p.restartTracker.mu.Lock()         // Lock before accessing shared data
	defer p.restartTracker.mu.Unlock() // Ensure unlock happens

	// if the last restart was more than a minute ago, reset the restart count
	if now.Sub(p.restartTracker.lastRestartAt) > time.Minute {
		p.restartTracker.restartCount = 0
	}
	p.restartTracker.restartCount++
	p.restartTracker.lastRestartAt = now
}

// getRestartDelay returns the delay before the next restart attempt
func (p *FFmpegProcess) getRestartDelay() time.Duration {
	p.restartTracker.mu.Lock()         // Lock before accessing shared data
	defer p.restartTracker.mu.Unlock() // Ensure unlock happens

	delay := time.Duration(p.restartTracker.restartCount) * 5 * time.Second
	if delay > 2*time.Minute {
		delay = 2 * time.Minute // Cap the maximum delay at 2 minutes
	}
	return delay
}

// Cleanup cleans up the FFmpeg process and removes it from the map
func (p *FFmpegProcess) Cleanup(url string) {
	if p == nil {
		ffmpegProcesses.Delete(url)
		return
	}

	// Create a cleanup completed channel to prevent races during cleanup
	cleanupDone := make(chan struct{})
	defer close(cleanupDone)

	// Track whether we've done a process kill to avoid double-kill
	var killed atomic.Bool

	// First close stdout to prevent blocking reads
	if p.stdout != nil {
		GlobalCleanupManager.CloseReader(p.stdout, fmt.Sprintf("stdout for RTSP source %s", url))
		p.stdout = nil
	}

	// Only attempt to cancel the context and kill the process if we have a valid command
	if p.cmd != nil && p.cmd.Process != nil {
		// Cancel the context to signal process termination
		if p.cancel != nil {
			p.cancel()
			p.cancel = nil
		}

		// Use a timeout to wait for the process to finish
		done := make(chan struct{})
		go func() {
			defer close(done)
			select {
			case <-p.done:
				// Process finished normally
			case <-cleanupDone:
				// Cleanup is being canceled, exit goroutine
				return
			}
		}()

		// Wait for process with timeout
		if GlobalCleanupManager.WaitWithTimeout(done, 10*time.Second, fmt.Sprintf("FFmpeg process for %s", url)) {
			log.Printf("⏹️ FFmpeg process for RTSP source %s stopped normally", url)
		} else if !killed.Swap(true) {
			// Timeout occurred, forcefully kill the process if not already killed
			if err := killProcessGroup(p.cmd); err != nil {
				// Only attempt direct process kill if killProcessGroup fails
				if err := p.cmd.Process.Kill(); err != nil {
					// Only log if both kill attempts fail and process still exists
					if !strings.Contains(err.Error(), "process already finished") {
						log.Printf("⚠️ Failed to kill FFmpeg process for %s: %v", url, err)
					}
				}
			}
		}
	}

	// Always remove from the map at the end
	ffmpegProcesses.Delete(url)
}

// startWatchdog starts a goroutine that monitors the audio stream for inactivity
func (p *FFmpegProcess) startWatchdog(ctx context.Context, url string, watchdog *audioWatchdog) <-chan struct{} {
	watchdogCtx, watchdogCancel := context.WithCancel(ctx)
	watchdogDone := make(chan struct{})

	go func() {
		defer close(watchdogDone)
		defer watchdogCancel() // Ensure context is canceled when goroutine exits

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-watchdogCtx.Done():
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

				// Exit if stream is no longer configured
				if !streamConfigured {
					log.Printf("ℹ️ Stream %s is no longer configured, stopping watchdog", url)
					return
				}

				// Check if we've gone too long without data
				timeout := watchdog.timeSinceLastData() > time.Duration(silenceTimeout)*time.Second
				if timeout {
					log.Printf("⚠️ No data received from RTSP source %s for %d seconds, triggering restart", url, silenceTimeout)
					return
				}
			}
		}
	}()
	return watchdogDone
}

// processAudio reads audio data from FFmpeg's stdout and writes it to buffers
func (p *FFmpegProcess) processAudio(ctx context.Context, url string, restartChan chan struct{}, audioLevelChan chan AudioLevelData) error {
	// Create a buffer to store audio data
	buf := make([]byte, 32768)
	watchdog := &audioWatchdog{lastDataTime: time.Now()}

	// Create a context that can be canceled when the function returns
	audioCtx, audioCancel := context.WithCancel(ctx)
	defer audioCancel() // Ensure context gets canceled

	// Start watchdog goroutine
	watchdogDone := p.startWatchdog(audioCtx, url, watchdog)

	// Continuously process audio data
	for {
		select {
		case <-ctx.Done():
			log.Printf("Stopping audio processing for RTSP source: %s", url)
			// Wait for watchdog to finish with a timeout
			GlobalCleanupManager.WaitWithTimeout(watchdogDone, 5*time.Second,
				fmt.Sprintf("watchdog for RTSP source %s", url))
			return nil // Return nil on normal shutdown
		case <-watchdogDone:
			// Check if the stream is still configured before triggering restart
			settings := conf.Setting()
			streamConfigured := false
			for _, configuredURL := range settings.Realtime.RTSP.URLs {
				if configuredURL == url {
					streamConfigured = true
					break
				}
			}

			if streamConfigured {
				// Trigger restart by sending signal to restartChan with non-blocking send
				if GlobalCleanupManager.SendNonBlocking(restartChan, fmt.Sprintf("restart channel for %s", url)) {
					log.Printf("🔄 Watchdog triggered restart for RTSP source %s", url)
				}
				return fmt.Errorf("watchdog detected no data for RTSP source %s", url)
			} else {
				log.Printf("ℹ️ Stream %s is no longer configured, stopping audio processing", url)
				return nil
			}
		default:
			// Use a timeout on the read to avoid blocking indefinitely
			readCtx, readCancel := context.WithTimeout(ctx, 30*time.Second)
			var n int
			var err error

			// Execute the read with a timeout
			readErr := GlobalCleanupManager.ExecuteWithTimeout(readCtx, 30*time.Second,
				func() error {
					var readErr error
					n, readErr = p.stdout.Read(buf)
					return readErr
				},
				fmt.Sprintf("read from FFmpeg stdout for %s", url))

			readCancel() // Always cancel the context to prevent leaks

			if readErr != nil {
				// Wait for watchdog to finish with a timeout
				GlobalCleanupManager.WaitWithTimeout(watchdogDone, 5*time.Second,
					fmt.Sprintf("watchdog for RTSP source %s", url))

				// Check if this is a normal shutdown
				if errors.Is(readErr, io.EOF) || strings.Contains(readErr.Error(), "file already closed") {
					return nil
				}

				// Check if this is a timeout
				if strings.Contains(readErr.Error(), "timeout") || errors.Is(readErr, context.DeadlineExceeded) {
					log.Printf("⚠️ Read timeout for RTSP source %s, triggering restart", url)
					return fmt.Errorf("read timeout for RTSP source %s", url)
				}

				// Only return error for unexpected failures
				return fmt.Errorf("error reading from ffmpeg: %w", readErr)
			}

			// Ensure we don't process more data than we've read
			if n > 0 {
				watchdog.update() // Update the watchdog timestamp

				// Write the audio data to the analysis buffer
				err = WriteToAnalysisBuffer(url, buf[:n])
				if err != nil {
					log.Printf("❌ Error writing to analysis buffer for RTSP source %s: %v", url, err)
					time.Sleep(1 * time.Second)
					continue
				}

				// Write the audio data to the capture buffer
				err = WriteToCaptureBuffer(url, buf[:n])
				if err != nil {
					log.Printf("❌ Error writing to capture buffer for RTSP source %s: %v", url, err)
					time.Sleep(1 * time.Second)
					continue
				}

				// Process audio level data
				ProcessAudioLevel(buf[:n], url, "", audioLevelChan)
			}
		}
	}
}

// startFFmpeg starts an FFmpeg process with the given configuration
func startFFmpeg(ctx context.Context, config FFmpegConfig) (*FFmpegProcess, error) {
	settings := conf.Setting().Realtime.Audio
	if err := validateFFmpegPath(settings.FfmpegPath); err != nil {
		return nil, err
	}

	// Create a new context with cancellation
	ctx, cancel := context.WithCancel(ctx)

	// Get the FFmpeg-compatible values for sample rate, channels, and bit depth
	ffmpegSampleRate, ffmpegNumChannels, ffmpegFormat := getFFmpegFormat(conf.SampleRate, conf.NumChannels, conf.BitDepth)

	// Prepare the FFmpeg command with appropriate arguments
	cmd := exec.CommandContext(ctx, settings.FfmpegPath,
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
		return nil, fmt.Errorf("error creating ffmpeg pipe: %w", err)
	}

	// Log the FFmpeg command for debugging purposes
	log.Printf("▶️ Starting FFmpeg with command: %s", cmd.String())

	// Start the FFmpeg process
	if err := cmd.Start(); err != nil {
		cancel() // Cancel the context if process start fails
		return nil, fmt.Errorf("error starting FFmpeg: %w", err)
	} else {
		log.Printf("✅ FFmpeg started successfully for RTSP source %s", config.URL)
	}

	// Create a channel to receive the exit status of the FFmpeg process
	done := make(chan error, 1)
	go func() {
		// Wait for the FFmpeg process to exit
		err := cmd.Wait()
		if err != nil {
			// Don't log if process was killed (normal shutdown) or context was cancelled
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
	}, nil
}

// isStreamConfigured checks if the URL is still in the configured RTSP URLs
func isStreamConfigured(url string) bool {
	settings := conf.Setting()
	for _, configuredURL := range settings.Realtime.RTSP.URLs {
		if configuredURL == url {
			return true
		}
	}
	return false
}

// handleFFmpegStartFailure handles the case when FFmpeg fails to start
func handleFFmpegStartFailure(ctx context.Context, backoff *backoffStrategy, url string, err error) (bool, error) {
	delay, retry := backoff.nextDelay()
	if !retry {
		return false, fmt.Errorf("failed to start FFmpeg after maximum attempts: %w", err)
	}

	log.Printf("⚠️ Failed to start FFmpeg for RTSP source %s: %v. Retrying in %v...", url, err, delay)

	// Wait for either the context to be cancelled or the delay to pass
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-time.After(delay):
		return true, nil
	}
}

// handleProcessCleanup performs cleanup operations for a process
func handleProcessCleanup(processCancel context.CancelFunc,
	restartForwarderDone chan struct{}, processDone chan error, url string) {

	// Cancel the process context
	processCancel()

	// Wait for restart forwarder to finish
	GlobalCleanupManager.WaitWithTimeout(restartForwarderDone, 2*time.Second,
		fmt.Sprintf("restart forwarder for %s", url))

	// Wait for process to clean up with a timeout
	err, _ := GlobalCleanupManager.WaitForErrorWithTimeout(processDone, 5*time.Second,
		fmt.Sprintf("process cleanup for %s", url))
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("⚠️ Process for %s reported error during cleanup: %v", url, err)
	}
}

// cleanupProcess safely cleans up an FFmpeg process
func cleanupProcess(url string) {
	if process, exists := ffmpegProcesses.Load(url); exists {
		if p, ok := process.(*FFmpegProcess); ok && p != nil {
			p.Cleanup(url)
		}
	}
	ffmpegProcesses.Delete(url)
}

func manageFfmpegLifecycle(ctx context.Context, config FFmpegConfig, restartChan chan struct{}, audioLevelChan chan AudioLevelData) error {
	// Create a new backoff strategy with 5 attempts, 5 seconds initial delay, and 2 minutes maximum delay
	backoff := newBackoffStrategy(5, 5*time.Second, 2*time.Minute)

	for {
		// Check if context is done before proceeding
		select {
		case <-ctx.Done():
			cleanupProcess(config.URL)
			return ctx.Err()
		default:
			// Continue with normal operation
		}

		// Check if the stream is still configured
		if !isStreamConfigured(config.URL) {
			log.Printf("ℹ️ Stream %s is no longer configured, stopping lifecycle manager", config.URL)
			cleanupProcess(config.URL)
			return nil
		}

		// Start a new FFmpeg process
		process, err := startFFmpeg(ctx, config)
		if err != nil {
			cleanupProcess(config.URL)
			continueRetry, retryErr := handleFFmpegStartFailure(ctx, backoff, config.URL, err)
			if !continueRetry {
				return retryErr
			}
			continue
		}

		// Reset backoff on successful start
		backoff.reset()

		// Store the process in the map
		ffmpegProcesses.Store(config.URL, process)

		// Start processing audio and wait for it to finish or for a restart signal
		processDone := make(chan error, 1)
		processCtx, processCancel := context.WithCancel(ctx)

		go func() {
			defer close(processDone)
			processDone <- process.processAudio(processCtx, config.URL, restartChan, audioLevelChan)
		}()

		var needsRestart bool

		// Create a channel for restart signals specific to this lifecycle iteration
		localRestartChan := make(chan struct{}, 1)

		// Start a goroutine to forward restart signals to our local channel
		restartForwarderDone := make(chan struct{})
		go func() {
			defer close(restartForwarderDone)
			for {
				select {
				case <-processCtx.Done():
					return
				case <-restartChan:
					// Forward restart signal to local channel
					GlobalCleanupManager.SendNonBlocking(localRestartChan,
						fmt.Sprintf("local restart channel for %s", config.URL))
				}
			}
		}()

		// Wait for process to finish or for a signal
		select {
		case <-ctx.Done():
			// Context cancelled, stop the FFmpeg process
			handleProcessCleanup(processCancel, restartForwarderDone, processDone, config.URL)
			cleanupProcess(config.URL)
			return ctx.Err()

		case err := <-processDone:
			// FFmpeg process or audio processing ended
			processCancel() // Cancel the process context even though it's already done
			GlobalCleanupManager.WaitWithTimeout(restartForwarderDone, 2*time.Second,
				fmt.Sprintf("restart forwarder for %s", config.URL))
			cleanupProcess(config.URL)

			if !isStreamConfigured(config.URL) {
				log.Printf("ℹ️ Stream %s is no longer configured after process finished", config.URL)
				return nil
			}

			if err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("⚠️ FFmpeg process for RTSP source %s ended unexpectedly: %v", config.URL, err)
				needsRestart = true
			}

		case <-localRestartChan:
			// Restart signal received
			log.Printf("🔄 Restart signal received, restarting FFmpeg for RTSP source %s.", config.URL)
			handleProcessCleanup(processCancel, restartForwarderDone, processDone, config.URL)
			cleanupProcess(config.URL)
			backoff.reset()
			needsRestart = true
		}

		// Check configuration again before waiting for restart
		if !isStreamConfigured(config.URL) {
			log.Printf("🛑 Stream %s is no longer configured, stopping lifecycle manager", config.URL)
			return nil
		}

		if needsRestart {
			// Update restart information and wait before attempting to restart
			if process != nil {
				process.updateRestartInfo()
				delay := process.getRestartDelay()

				// Wait for restart with cancellation and restart signal handling
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(delay):
					// Continue to next iteration after delay
				case <-restartChan:
					log.Printf("🔄 Restart signal received during restart delay, restarting FFmpeg for RTSP source %s immediately.", config.URL)
				}
			}
		}
	}
}

// getExitCode returns the exit code of the FFmpeg process
func getExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
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

// CaptureAudioRTSP is the main function for capturing audio from an RTSP stream
func CaptureAudioRTSP(url, transport string, wg *sync.WaitGroup, quitChan <-chan struct{}, restartChan chan struct{}, audioLevelChan chan AudioLevelData) {
	// Use sync.Once to ensure we call Done() exactly once
	var doneOnce sync.Once

	// Mark the WaitGroup as done when this function returns
	defer func() {
		doneOnce.Do(func() {
			if wg != nil {
				wg.Done()
			}
		})
	}()

	// Clean up throttle map entries when the function exits
	defer func() {
		// Use a separate goroutine for cleanup to avoid blocking
		go CleanupAudioLevelTrackers()
	}()

	// Return with error if FFmpeg path is not set
	ffmpegPath := conf.GetFfmpegBinaryName()
	if ffmpegPath == "" {
		log.Printf("❌ FFmpeg is not available, cannot capture audio from RTSP source %s.", url)
		log.Printf("⚠️ Please make sure FFmpeg is installed and included in system PATH.")
		return
	}

	// Create a configuration for FFmpeg
	config := FFmpegConfig{
		URL:       url,
		Transport: transport,
	}

	// Create a new context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	// Ensure the cancel function is called when the function exits
	defer cancel()

	// Create buffered restart channel if not buffered already to prevent blocking
	bufferedRestartChan := restartChan
	if cap(restartChan) == 0 {
		// Create a buffered channel to handle restart signals without blocking
		bufferedRestartChan = make(chan struct{}, 5)
		// Forward any signals from the original channel to our buffered channel
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case _, ok := <-restartChan:
					if !ok {
						return
					}
					select {
					case bufferedRestartChan <- struct{}{}:
						// Successfully forwarded
					default:
						// Channel is full, log and continue
						log.Printf("⚠️ Buffered restart channel for RTSP source %s is full", url)
					}
				}
			}
		}()
	}

	// Start a goroutine to handle the quit signal
	go func() {
		// Wait for a signal on the quit channel
		select {
		case <-quitChan:
			// Log that a quit signal was received
			log.Printf("🔴 Quit signal received, stopping FFmpeg for RTSP source %s.", url)
			// Cancel the context to stop all operations
			cancel()
		case <-ctx.Done():
			// Context already cancelled, nothing to do
			return
		}
	}()

	// Start a ticker to periodically clean up the throttle map
	cleanupTicker := time.NewTicker(10 * time.Minute)
	defer cleanupTicker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-cleanupTicker.C:
				CleanupAudioLevelTrackers()
			}
		}
	}()

	// Manage the FFmpeg lifecycle
	err := manageFfmpegLifecycle(ctx, config, bufferedRestartChan, audioLevelChan)
	// If an error occurred and it's not due to context cancellation, log it and report to user
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("⚠️ FFmpeg lifecycle manager for RTSP source %s exited with error: %v", url, err)
	} else {
		log.Printf("ℹ️ FFmpeg lifecycle manager for RTSP source %s exited normally", url)
	}
}
