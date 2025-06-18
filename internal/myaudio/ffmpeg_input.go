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

const (
	DefaultSilenceTimeout     = 60 * time.Second
	DefaultProcessKillTimeout = 5 * time.Second
	DefaultReadTimeout        = 30 * time.Second
	DefaultRestartMaxDelay    = 2 * time.Minute
)

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

	delay := time.Duration(p.restartTracker.restartCount) * DefaultRestartMaxDelay
	if delay > DefaultRestartMaxDelay {
		delay = DefaultRestartMaxDelay
	}
	return delay
}

func (p *FFmpegProcess) Cleanup(url string) {
	if p == nil {
		ffmpegProcesses.Delete(url)
		return
	}

	// First close stdout to prevent blocking reads
	if p.stdout != nil {
		GlobalCleanupManager.CloseReader(p.stdout, fmt.Sprintf("stdout for RTSP source %s", url))
		p.stdout = nil
	}

	// Cancel the context first to signal termination to all goroutines
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}

	// Only attempt to kill if we have a valid process
	if p.cmd != nil && p.cmd.Process != nil {
		// Create a timeout context for the kill operation
		killCtx, killCancel := context.WithTimeout(context.Background(), DefaultProcessKillTimeout)
		defer killCancel()

		// Create a channel to signal when process has exited
		processExited := make(chan struct{})

		// Wait for the process to exit naturally
		go func() {
			defer close(processExited)
			select {
			case <-p.done:
				// Process exited normally
			case <-killCtx.Done():
				// Timeout reached, will force kill below
			}
		}()

		// Wait with timeout
		select {
		case <-processExited:
			log.Printf("⏹️ FFmpeg process for RTSP source %s stopped normally", url)
		case <-killCtx.Done():
			// Force kill the process group
			log.Printf("⚠️ Timeout waiting for FFmpeg process for %s, forcing kill", url)
			killErr := killProcessGroup(p.cmd)
			if killErr != nil && !strings.Contains(killErr.Error(), "process already finished") {
				log.Printf("❌ Failed to kill process group for %s: %v", url, killErr)
				// Fall back to direct process kill
				if err := p.cmd.Process.Kill(); err != nil &&
					!strings.Contains(err.Error(), "process already finished") {
					log.Printf("❌ Failed to kill FFmpeg process for %s: %v", url, err)
				}
			}
		}
	}

	// Always remove from the map at the end
	ffmpegProcesses.Delete(url)
}

// In ffmpeg_input.go
func (p *FFmpegProcess) startWatchdog(ctx context.Context, url string, watchdog *audioWatchdog) <-chan struct{} {
	watchdogCtx, watchdogCancel := context.WithCancel(ctx)
	watchdogDone := make(chan struct{})

	go func() {
		defer close(watchdogDone)
		defer watchdogCancel()

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-watchdogCtx.Done():
				return
			case <-ticker.C:
				// Take a snapshot of the configuration for this tick
				isConfigured := isStreamConfigured(url)
				timeSinceData := watchdog.timeSinceLastData()

				if !isConfigured {
					log.Printf("ℹ️ Stream %s is no longer configured, stopping watchdog", url)
					return
				}

				if timeSinceData > DefaultSilenceTimeout {
					log.Printf("⚠️ No data received from %s for %v, triggering restart",
						url, DefaultSilenceTimeout)
					return
				}
			}
		}
	}()
	return watchdogDone
}

func (p *FFmpegProcess) processAudio(ctx context.Context, url string,
	restartChan chan struct{},
	audioLevelChan chan AudioLevelData) error {
	buf := make([]byte, 32768)
	watchdog := &audioWatchdog{lastDataTime: time.Now()}

	audioCtx, audioCancel := context.WithCancel(ctx)
	defer audioCancel()

	watchdogDone := p.startWatchdog(audioCtx, url, watchdog)
	defer func() {
		// Always ensure watchdog is properly stopped
		audioCancel()
		GlobalCleanupManager.WaitWithTimeout(watchdogDone, 5*time.Second,
			fmt.Sprintf("watchdog for %s", url))
	}()

	for {
		// First check if we should exit
		select {
		case <-ctx.Done():
			return nil
		case <-watchdogDone:
			if isStreamConfigured(url) {
				GlobalCleanupManager.SendNonBlocking(restartChan,
					fmt.Sprintf("restart channel for %s", url))
				return fmt.Errorf("watchdog detected no data for %s", url)
			}
			return nil
		default:
			// Continue processing
		}

		// Read with timeout
		n, err := readWithTimeout(ctx, p.stdout, buf, url)

		if err != nil {
			if isGracefulExit(err) {
				return nil
			}

			if isTimeoutError(err) {
				log.Printf("⚠️ Read timeout for %s, triggering restart", url)
				return fmt.Errorf("read timeout for %s", url)
			}

			return fmt.Errorf("error reading from ffmpeg: %w", err)
		}

		if n > 0 {
			watchdog.update()

			// Process audio data with better error handling
			if err := processAudioData(url, buf[:n], audioLevelChan); err != nil {
				log.Printf("❌ Error processing audio for %s: %v", url, err)
				// Continue instead of failing the whole process on data processing errors
				time.Sleep(1 * time.Second) // Add backoff
			}
		}
	}
}

// Helper function to read with timeout
func readWithTimeout(ctx context.Context, reader io.Reader, buf []byte, url string) (int, error) {
	readCtx, readCancel := context.WithTimeout(ctx, DefaultReadTimeout)
	defer readCancel()

	var n int
	err := GlobalCleanupManager.ExecuteWithTimeout(readCtx, DefaultReadTimeout,
		func() error {
			var readErr error
			n, readErr = reader.Read(buf)
			return readErr
		}, fmt.Sprintf("read from FFmpeg for %s", url))

	return n, err
}

// Helper function to check if this is a graceful exit
func isGracefulExit(err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, context.Canceled) ||
		strings.Contains(err.Error(), "file already closed")
}

// Helper function to check if this is a timeout error
func isTimeoutError(err error) bool {
	return strings.Contains(err.Error(), "timeout") ||
		errors.Is(err, context.DeadlineExceeded)
}

// Helper function to process audio data
func processAudioData(url string, data []byte, audioLevelChan chan AudioLevelData) error {
	// Write to analysis buffer
	if err := WriteToAnalysisBuffer(url, data); err != nil {
		return fmt.Errorf("analysis buffer error: %w", err)
	}

	// Write to capture buffer
	if err := WriteToCaptureBuffer(url, data); err != nil {
		return fmt.Errorf("capture buffer error: %w", err)
	}

	// Process audio level data
	ProcessAudioLevel(data, url, "", audioLevelChan)

	return nil
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
