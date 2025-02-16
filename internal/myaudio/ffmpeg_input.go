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
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// ffmpegProcesses keeps track of running FFmpeg processes for each URL
// This is used to ensure that only one FFmpeg process is running per RTSP source
var ffmpegProcesses sync.Map

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

// FFmpegRestartTracker keeps track of restart information for each RTSP source
type FFmpegRestartTracker struct {
	restartCount  int
	lastRestartAt time.Time
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
	// if the last restart was more than a minute ago, reset the restart count
	if now.Sub(p.restartTracker.lastRestartAt) > time.Minute {
		p.restartTracker.restartCount = 0
	}
	p.restartTracker.restartCount++
	p.restartTracker.lastRestartAt = now
}

// getRestartDelay returns the delay before the next restart attempt
func (p *FFmpegProcess) getRestartDelay() time.Duration {
	delay := time.Duration(p.restartTracker.restartCount) * 5 * time.Second
	if delay > 2*time.Minute {
		delay = 2 * time.Minute // Cap the maximum delay at 2 minutes
	}
	return delay
}

// Cleanup cleans up the FFmpeg process and removes it from the map
func (p *FFmpegProcess) Cleanup(url string) {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		ffmpegProcesses.Delete(url)
		return
	}

	// First close stdout to prevent blocking reads
	if p.stdout != nil {
		p.stdout.Close()
	}

	// Cancel the context to signal process termination
	p.cancel()

	// Use a timeout to wait for the process to finish
	done := make(chan struct{})
	go func() {
		<-p.done
		close(done)
	}()

	select {
	case <-done:
		log.Printf("🛑 FFmpeg process for RTSP source %s stopped normally", url)
		// Process finished normally
	case <-time.After(10 * time.Second):
		// Timeout occurred, forcefully kill the process
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

	ffmpegProcesses.Delete(url)
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

// processAudio reads audio data from FFmpeg's stdout and writes it to buffers
func (p *FFmpegProcess) processAudio(ctx context.Context, url string, restartChan chan struct{}, audioLevelChan chan AudioLevelData) error {
	// Create a buffer to store audio data
	buf := make([]byte, 32768)
	watchdog := &audioWatchdog{lastDataTime: time.Now()}

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
				// Trigger restart by sending signal to restartChan
				select {
				case restartChan <- struct{}{}:
					log.Printf("🔄 Watchdog triggered restart for RTSP source %s", url)
				default:
					log.Printf("❌ Restart channel full, dropping restart request for %s", url)
				}
				return fmt.Errorf("watchdog detected no data for RTSP source %s", url)
			} else {
				return nil
			}
		default:
			// Read audio data from FFmpeg's stdout
			n, err := p.stdout.Read(buf)
			if err != nil {
				<-watchdogDone // Wait for watchdog to finish
				// Check if this is a normal shutdown
				if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "file already closed") {
					return nil
				}
				// Only return error for unexpected failures
				return fmt.Errorf("error reading from ffmpeg: %w", err)
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

				// Calculate audio level with source information
				audioLevelData := calculateAudioLevel(buf[:n], url, "")

				// Send level to channel (non-blocking)
				select {
				case audioLevelChan <- audioLevelData:
					// Successfully sent data
				default:
					// Channel is full, clear it and send new data
					for len(audioLevelChan) > 0 {
						<-audioLevelChan
					}
					audioLevelChan <- audioLevelData
				}
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
	log.Printf("⬆️ Starting FFmpeg with command: %s", cmd.String())

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

func manageFfmpegLifecycle(ctx context.Context, config FFmpegConfig, restartChan chan struct{}, audioLevelChan chan AudioLevelData) error {
	// Create a new backoff strategy with 5 attempts, 5 seconds initial delay, and 2 minutes maximum delay
	backoff := newBackoffStrategy(5, 5*time.Second, 2*time.Minute)

	for {
		// Check if the stream is still configured before starting/restarting
		settings := conf.Setting()
		streamConfigured := false
		for _, url := range settings.Realtime.RTSP.URLs {
			if url == config.URL {
				streamConfigured = true
				break
			}
		}

		if !streamConfigured {
			// Remove the process from the map if it exists
			if process, exists := ffmpegProcesses.Load(config.URL); exists {
				if p, ok := process.(*FFmpegProcess); ok {
					p.Cleanup(config.URL)
				}
			}
			ffmpegProcesses.Delete(config.URL)
			return nil
		}

		// Start a new FFmpeg process
		process, err := startFFmpeg(ctx, config)
		if err != nil {
			// Clean up our nil placeholder from the ffmpegProcesses map
			ffmpegProcesses.Delete(config.URL)

			// Get the next delay duration and check if we should retry
			delay, retry := backoff.nextDelay()
			if !retry {
				// If we've exceeded our retry attempts, return an error
				return fmt.Errorf("failed to start FFmpeg after maximum attempts: %w", err)
			}

			// Log the failure and the next retry attempt
			log.Printf("⚠️ Failed to start FFmpeg for RTSP source %s: %v. Retrying in %v...", config.URL, err, delay)

			// Wait for either the context to be cancelled or the delay to pass
			select {
			case <-ctx.Done():
				// If the context is cancelled, return its error
				return ctx.Err()
			case <-time.After(delay):
				// If the delay has passed, continue to the next iteration
				continue
			}
		}

		// Reset backoff on successful start
		backoff.reset()

		// Store the process in the map
		ffmpegProcesses.Store(config.URL, process)

		// Start processing audio and wait for it to finish or for a restart signal
		processDone := make(chan error, 1)
		go func() {
			processDone <- process.processAudio(ctx, config.URL, restartChan, audioLevelChan)
		}()

		select {
		case <-ctx.Done():
			// Context cancelled, stop the FFmpeg process
			process.Cleanup(config.URL)
			return ctx.Err()

		case err := <-processDone:
			// FFmpeg process or audio processing ended
			process.Cleanup(config.URL)

			// Check if the stream is still configured before handling the error
			settings := conf.Setting()
			streamConfigured := false
			for _, url := range settings.Realtime.RTSP.URLs {
				if url == config.URL {
					streamConfigured = true
					break
				}
			}

			if !streamConfigured {
				ffmpegProcesses.Delete(config.URL)
				return nil
			}

			if err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("⚠️ FFmpeg process for RTSP source %s ended unexpectedly: %v", config.URL, err)
			}

		case <-restartChan:
			// Restart signal received
			log.Printf("🔄 Restart signal received, restarting FFmpeg for RTSP source %s.", config.URL)
			process.Cleanup(config.URL)
			backoff.reset()
		}

		// Check configuration again before waiting for restart
		settings = conf.Setting()
		streamConfigured = false
		for _, url := range settings.Realtime.RTSP.URLs {
			if url == config.URL {
				streamConfigured = true
				break
			}
		}

		if !streamConfigured {
			log.Printf("🛑 Stream %s is no longer configured, stopping lifecycle manager", config.URL)
			ffmpegProcesses.Delete(config.URL)
			return nil
		}

		// Update restart information and wait before attempting to restart
		process.updateRestartInfo()
		delay := process.getRestartDelay()

		// wait for either the context to be cancelled (user requested quit) or the delay to pass
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
			// Continue to next iteration after delay
		case <-restartChan:
			log.Printf("🔄 Restart signal received during restart delay, restarting FFmpeg for RTSP source %s immediately.", config.URL)
			continue
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

// CaptureAudioRTSP is the main function for capturing audio from an RTSP stream
func CaptureAudioRTSP(url, transport string, wg *sync.WaitGroup, quitChan <-chan struct{}, restartChan chan struct{}, audioLevelChan chan AudioLevelData) {
	// Return with error if FFmpeg path is not set
	if conf.GetFfmpegBinaryName() == "" {
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
	err := manageFfmpegLifecycle(ctx, config, restartChan, audioLevelChan)
	// If an error occurred and it's not due to context cancellation, log it and report to user
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("⚠️ FFmpeg lifecycle manager for RTSP source %s exited with error: %v", url, err)
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
