package myaudio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"
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
		b.buffer.Truncate(0)
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
	if p != nil && p.cmd.Process != nil {
		// Cancel the context to signal process termination
		p.cancel()

		// Use a timeout to wait for the process to finish
		select {
		case <-p.done:
			// Process finished normally
		case <-time.After(10 * time.Second):
			// Timeout occurred, forcefully kill the process
			log.Printf("FFmpeg process for %s did not exit gracefully, forcefully terminating", url)
			if err := p.cmd.Process.Kill(); err != nil {
				log.Printf("Failed to kill FFmpeg process for %s: %v", url, err)
			}
		}
	}
	ffmpegProcesses.Delete(url)
}

// processAudio reads audio data from FFmpeg's stdout and writes it to buffers
func (p *FFmpegProcess) processAudio(ctx context.Context, url string, audioLevelChan chan AudioLevelData) error {
	// Create a buffer to store audio data
	buf := make([]byte, 65535)

	// Continuously process audio data
	for {
		select {
		// Check if the context has been cancelled
		case <-ctx.Done():
			// If so, return the context error
			return ctx.Err()
		default:
			// Read audio data from FFmpeg's stdout
			n, err := p.stdout.Read(buf)
			if err != nil {
				// Error occurred while reading from ffmpeg, this covers EOF and other errors
				return fmt.Errorf("error reading from ffmpeg: %w", err)
			}

			// Ensure we don't process more data than we've read
			if n > 0 {
				// Write the audio data to the analysis buffer
				WriteToAnalysisBuffer(url, buf[:n])
				// Write the audio data to the capture buffer
				WriteToCaptureBuffer(url, buf[:n])

				// Calculate audio level
				audioLevelData := calculateAudioLevel(buf[:n])

				// Send level to channel (non-blocking)
				select {
				case audioLevelChan <- audioLevelData:
					// Data sent successfully
				default:
					// Channel is full, clear the channel
					for len(audioLevelChan) > 0 {
						<-audioLevelChan
					}
					// Try to send the new data
					audioLevelChan <- audioLevelData
				}
			}
		}
	}
}

// checkFFmpegAvailability checks if FFmpeg is installed and available
func checkFFmpegAvailability() error {
	cmd := exec.Command("ffmpeg", "-version")
	err := cmd.Run()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("FFmpeg is not installed or not in the system PATH")
		}
		return fmt.Errorf("error checking FFmpeg availability: %w", err)
	}
	return nil
}

// startFFmpeg starts an FFmpeg process with the given configuration
func startFFmpeg(ctx context.Context, config FFmpegConfig) (*FFmpegProcess, error) {
	// Create a new context with cancellation
	ctx, cancel := context.WithCancel(ctx)

	// Prepare the FFmpeg command with appropriate arguments
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-rtsp_transport", config.Transport, // Set RTSP transport protocol
		"-i", config.URL, // Input URL
		"-loglevel", "error", // Set log level to error
		"-vn",         // Disable video
		"-f", "s16le", // Set output format to signed 16-bit little-endian
		"-ar", "48000", // Set audio sample rate to 48kHz
		"-ac", "1", // Set number of audio channels to 1 (mono)
		"pipe:1", // Output to stdout
	)

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
	log.Println("Starting ffmpeg with command:", cmd.String())

	// Start the FFmpeg process
	if err := cmd.Start(); err != nil {
		cancel() // Cancel the context if process start fails
		return nil, fmt.Errorf("error starting FFmpeg: %w", err)
	}

	// Create a channel to receive the exit status of the FFmpeg process
	done := make(chan error, 1)
	go func() {
		// Wait for the FFmpeg process to exit
		err := cmd.Wait()
		if err != nil {
			log.Printf("FFmpeg process for RTSP source %s exited with error: %v", config.URL, err)
			// Include stderr in the error if available
			if stderrBuf.String() != "" {
				log.Printf("FFmpeg process stderr:\n%v", stderrBuf.String())
				err = fmt.Errorf("%w\nStderr: %s", err, stderrBuf.String())
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

func manageFfmpegLifecycle(ctx context.Context, config FFmpegConfig, restartChan <-chan struct{}, audioLevelChan chan AudioLevelData) error {
	// Create a new backoff strategy with 5 attempts, 5 seconds initial delay, and 2 minutes maximum delay
	backoff := newBackoffStrategy(5, 5*time.Second, 2*time.Minute)

	for {
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
			log.Printf("Failed to start FFmpeg for RTSP source %s: %v. Retrying in %v...", config.URL, err, delay)

			// Wait for either the context to be cancelled or the delay to pass
			select {
			case <-ctx.Done():
				// If the context is cancelled, return its error
				return ctx.Err()
			case <-time.After(delay):
				// If the delay has passed, continue to the next iteration of the loop
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
			processDone <- process.processAudio(ctx, config.URL, audioLevelChan)
		}()

		select {
		case <-ctx.Done():
			// Context cancelled, stop the FFmpeg process
			log.Printf("Context cancelled, stopping FFmpeg for RTSP source %s.", config.URL)
			process.Cleanup(config.URL)
			return ctx.Err()

		case err := <-processDone:
			// FFmpeg process or audio processing ended
			process.Cleanup(config.URL)
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("FFmpeg process for RTSP source %s ended unexpectedly: %v", config.URL, err)
			} else {
				log.Printf("FFmpeg process for RTSP source %s ended", config.URL)
			}

		case <-restartChan:
			// Restart signal received
			log.Printf("Restart signal received, restarting FFmpeg for RTSP source %s.", config.URL)
			process.Cleanup(config.URL)
			backoff.reset()
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
			log.Printf("Restart signal received during restart delay, restarting FFmpeg for RTSP source %s immediately.", config.URL)
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
func CaptureAudioRTSP(url string, transport string, wg *sync.WaitGroup, quitChan <-chan struct{}, restartChan chan struct{}, audioLevelChan chan AudioLevelData) {
	// Ensure the WaitGroup is decremented when the function exits
	defer wg.Done()

	// Check FFmpeg availability before starting the capture process
	if err := checkFFmpegAvailability(); err != nil {
		log.Printf("Error: %v. Unable to start audio capture for RTSP source %s.", err, url)
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
		log.Printf("Quit signal received, stopping FFmpeg for RTSP source %s.", url)
		// Cancel the context to stop all operations
		cancel()
	}()

	// Manage the FFmpeg lifecycle
	err := manageFfmpegLifecycle(ctx, config, restartChan, audioLevelChan)
	// If an error occurred and it's not due to context cancellation, log it and report to user
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("FFmpeg lifecycle manager for RTSP source %s exited with error: %v", url, err)
	}
}
