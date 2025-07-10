package myaudio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// Constants for FFmpeg stream management
const (
	// Buffer size for reading FFmpeg output
	ffmpegBufferSize = 32768

	// Health check intervals and timeouts
	healthCheckInterval  = 5 * time.Second
	silenceTimeout       = 60 * time.Second
	silenceCheckInterval = 10 * time.Second

	// Data rate calculation settings
	dataRateWindowSize = 30 * time.Second
	dataRateMaxSamples = 100

	// Process management timeouts
	processCleanupTimeout = 5 * time.Second
	processQuickExitTime  = 5 * time.Second

	// Backoff settings
	defaultBackoffDuration = 5 * time.Second
	maxBackoffDuration     = 2 * time.Minute

	// Health check thresholds (defaults, can be overridden by config)
	defaultHealthyDataThreshold   = 60 * time.Second
	defaultReceivingDataThreshold = 5 * time.Second

	// Circuit breaker settings
	circuitBreakerThreshold = 10              // Number of consecutive failures before opening circuit
	circuitBreakerCooldown  = 5 * time.Minute // Cooldown period when circuit is open

	// Drop logging settings
	dropLogInterval = 30 * time.Second // Minimum interval between drop log messages

	// Maximum safe exponent for bit shift to prevent overflow
	maxBackoffExponent = 20 // This allows up to 2^20 = ~1 million multiplier

	// Timeout settings for FFmpeg RTSP streams
	defaultTimeoutMicroseconds = 30000000 // 30 seconds in microseconds
	minTimeoutMicroseconds     = 1000000  // 1 second in microseconds
)

// Use shared logger from integration file
var streamLogger *slog.Logger

func init() {
	// Use the shared integration logger for consistency
	streamLogger = integrationLogger
}

// dataRateCalculator tracks data rate over a sliding window
type dataRateCalculator struct {
	samples    []dataSample
	samplesMu  sync.RWMutex
	windowSize time.Duration
	maxSamples int
}

type dataSample struct {
	timestamp time.Time
	bytes     int64
}

// newDataRateCalculator creates a new data rate calculator
func newDataRateCalculator(windowSize time.Duration) *dataRateCalculator {
	return &dataRateCalculator{
		samples:    make([]dataSample, 0, dataRateMaxSamples),
		windowSize: windowSize,
		maxSamples: dataRateMaxSamples,
	}
}

// addSample adds a new data sample
func (d *dataRateCalculator) addSample(numBytes int64) {
	d.samplesMu.Lock()
	defer d.samplesMu.Unlock()

	now := time.Now()
	d.samples = append(d.samples, dataSample{
		timestamp: now,
		bytes:     numBytes,
	})

	// Remove old samples outside the window
	cutoff := now.Add(-d.windowSize)
	i := 0
	for i < len(d.samples) && d.samples[i].timestamp.Before(cutoff) {
		i++
	}
	if i > 0 {
		d.samples = d.samples[i:]
	}

	// Limit max samples
	if len(d.samples) > d.maxSamples {
		d.samples = d.samples[len(d.samples)-d.maxSamples:]
	}
}

// getRate returns the current data rate in bytes per second and an error if insufficient data
func (d *dataRateCalculator) getRate() (float64, error) {
	d.samplesMu.RLock()
	defer d.samplesMu.RUnlock()

	if len(d.samples) == 0 {
		return 0, errors.Newf("no data samples available for rate calculation").
			Component("ffmpeg-stream").
			Category(errors.CategorySystem).
			Context("operation", "calculate_data_rate").
			Build()
	}

	if len(d.samples) < 2 {
		return 0, errors.Newf("insufficient data samples for rate calculation: %d samples", len(d.samples)).
			Component("ffmpeg-stream").
			Category(errors.CategorySystem).
			Context("operation", "calculate_data_rate").
			Context("sample_count", len(d.samples)).
			Build()
	}

	totalBytes := int64(0)
	for _, s := range d.samples {
		totalBytes += s.bytes
	}

	duration := d.samples[len(d.samples)-1].timestamp.Sub(d.samples[0].timestamp).Seconds()
	if duration <= 0 {
		return 0, errors.Newf("invalid duration for rate calculation: %f seconds", duration).
			Component("ffmpeg-stream").
			Category(errors.CategorySystem).
			Context("operation", "calculate_data_rate").
			Context("duration_seconds", duration).
			Build()
	}

	return float64(totalBytes) / duration, nil
}

// StreamHealth represents the health status of an FFmpeg stream.
// It provides metrics about data reception, restart attempts, and overall stream health.
type StreamHealth struct {
	IsHealthy        bool
	LastDataReceived time.Time
	RestartCount     int
	Error            error
	// Data statistics
	TotalBytesReceived int64
	BytesPerSecond     float64
	IsReceivingData    bool
}

// FFmpegStream manages a single FFmpeg process for audio streaming.
// It handles process lifecycle, health monitoring, data tracking, and automatic recovery.
type FFmpegStream struct {
	url       string
	transport string
	audioChan chan UnifiedAudioData

	// Process management
	cmd    *exec.Cmd
	cmdMu  sync.Mutex
	stdout io.ReadCloser
	stderr bytes.Buffer

	// State management
	ctx         context.Context
	cancel      context.CancelFunc
	restartChan chan struct{}
	stopChan    chan struct{}
	stopped     bool
	stoppedMu   sync.RWMutex

	// Health tracking
	lastDataTime   time.Time
	lastDataMu     sync.RWMutex
	restartCount   int
	restartCountMu sync.Mutex

	// Data tracking
	totalBytesReceived int64
	bytesReceivedMu    sync.RWMutex
	dataRateCalc       *dataRateCalculator

	// Process timing
	processStartTime time.Time

	// Backoff for restarts
	backoffDuration time.Duration
	maxBackoff      time.Duration

	// Circuit breaker state
	consecutiveFailures int
	circuitOpenTime     time.Time
	circuitMu           sync.Mutex

	// Dropped data tracking
	lastDropLogTime time.Time
	dropLogMu       sync.Mutex
}

// NewFFmpegStream creates a new FFmpeg stream handler.
// The url parameter specifies the RTSP stream URL, transport specifies the RTSP transport protocol,
// and audioChan is the channel where processed audio data will be sent.
func NewFFmpegStream(url, transport string, audioChan chan UnifiedAudioData) *FFmpegStream {
	return &FFmpegStream{
		url:             url,
		transport:       transport,
		audioChan:       audioChan,
		restartChan:     make(chan struct{}, 1),
		stopChan:        make(chan struct{}),
		backoffDuration: defaultBackoffDuration,
		maxBackoff:      maxBackoffDuration,
		lastDataTime:    time.Now(),
		dataRateCalc:    newDataRateCalculator(dataRateWindowSize),
		lastDropLogTime: time.Now(),
	}
}

// Run starts and manages the FFmpeg process lifecycle.
// It runs in a loop, automatically restarting the process on failures with exponential backoff.
// The function returns when the context is cancelled or Stop() is called.
func (s *FFmpegStream) Run(parentCtx context.Context) {
	s.ctx, s.cancel = context.WithCancel(parentCtx)
	defer s.cancel()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.stopChan:
			return
		default:
			// Start FFmpeg process
			// Check circuit breaker
			if s.isCircuitOpen() {
				// Wait before next attempt
				select {
				case <-time.After(30 * time.Second):
					continue
				case <-s.ctx.Done():
					return
				case <-s.stopChan:
					return
				}
			}

			if err := s.startProcess(); err != nil {
				streamLogger.Error("failed to start FFmpeg process",
					"url", privacy.SanitizeRTSPUrl(s.url),
					"error", err,
					"component", "ffmpeg-stream",
					"operation", "start_process")
				log.Printf("❌ Failed to start FFmpeg for %s: %v", privacy.SanitizeRTSPUrl(s.url), err)
				s.recordFailure()
				s.handleRestartBackoff()
				continue
			}

			// Process audio data
			err := s.processAudio()

			// Check if we should stop
			s.stoppedMu.RLock()
			stopped := s.stopped
			s.stoppedMu.RUnlock()

			if stopped {
				return
			}

			// Handle process exit
			// Get process start time safely
			s.cmdMu.Lock()
			processStartTime := s.processStartTime
			s.cmdMu.Unlock()
			runtime := time.Since(processStartTime)
			if err != nil && !errors.Is(err, context.Canceled) {
				// Record failure for circuit breaker
				s.recordFailure()
				// Log process exit with sanitized error message
				errorMsg := err.Error()
				sanitizedError := privacy.SanitizeRTSPUrls(errorMsg)

				// Check if this was a silence timeout
				isSilenceTimeout := strings.Contains(errorMsg, "silence timeout")

				streamLogger.Warn("FFmpeg process ended",
					"url", privacy.SanitizeRTSPUrl(s.url),
					"error", sanitizedError,
					"runtime_seconds", runtime.Seconds(),
					"component", "ffmpeg-stream",
					"operation", "process_ended")
				log.Printf("⚠️ FFmpeg process ended for %s after %v: %v", privacy.SanitizeRTSPUrl(s.url), runtime, sanitizedError)

				// Reset restart count for silence timeouts as they're expected
				if isSilenceTimeout {
					s.restartCountMu.Lock()
					s.restartCount = 0
					s.restartCountMu.Unlock()
					// Don't count silence timeouts as failures for circuit breaker
					s.circuitMu.Lock()
					if s.consecutiveFailures > 0 {
						s.consecutiveFailures--
					}
					s.circuitMu.Unlock()
				}
			} else {
				// Log normal exit
				streamLogger.Info("FFmpeg process ended normally",
					"url", privacy.SanitizeRTSPUrl(s.url),
					"runtime_seconds", runtime.Seconds(),
					"component", "ffmpeg-stream",
					"operation", "process_ended")
				log.Printf("✅ FFmpeg process ended normally for %s after %v", privacy.SanitizeRTSPUrl(s.url), runtime)
				// Reset failure count on successful run
				s.resetFailures()
			}

			// Apply backoff before restart
			s.handleRestartBackoff()
		}
	}
}

// startProcess starts the FFmpeg process
func (s *FFmpegStream) startProcess() error {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()

	// Validate FFmpeg path
	settings := conf.Setting().Realtime.Audio
	if err := validateFFmpegPath(settings.FfmpegPath); err != nil {
		return errors.New(fmt.Errorf("FFmpeg validation failed: %w", err)).
			Category(errors.CategoryValidation).
			Component("ffmpeg-stream").
			Context("operation", "start_process").
			Context("ffmpeg_path", settings.FfmpegPath).
			Build()
	}

	// Get FFmpeg format settings
	sampleRate, numChannels, format := getFFmpegFormat(conf.SampleRate, conf.NumChannels, conf.BitDepth)

	// Build FFmpeg command arguments
	args := []string{
		"-rtsp_transport", s.transport,
	}
	
	// Get RTSP settings
	rtspSettings := conf.Setting().Realtime.RTSP
	
	// Check if user has already provided a timeout parameter
	hasUserTimeout, userTimeoutValue := detectUserTimeout(rtspSettings.FFmpegParameters)
	
	// Add default timeout if user hasn't provided one
	if !hasUserTimeout {
		args = append(args, "-timeout", strconv.FormatInt(defaultTimeoutMicroseconds, 10))
	}
	
	// Add custom FFmpeg parameters from configuration (before input)
	if len(rtspSettings.FFmpegParameters) > 0 {
		// Validate user timeout if provided
		if hasUserTimeout {
			if err := s.validateUserTimeout(userTimeoutValue); err != nil {
				// Log warning but continue - prefer working stream with default timeout
				// over failing completely due to user configuration error
				streamLogger.Warn("invalid user timeout, using default",
					"url", privacy.SanitizeRTSPUrl(s.url),
					"user_timeout", userTimeoutValue,
					"error", err,
					"component", "ffmpeg-stream",
					"operation", "validate_timeout")
				// Add default timeout before user parameters
				args = append(args, "-timeout", strconv.FormatInt(defaultTimeoutMicroseconds, 10))
			}
		}
		args = append(args, rtspSettings.FFmpegParameters...)
	}
	
	// Add input and output parameters
	args = append(args,
		"-i", s.url,
		"-loglevel", "error",
		"-vn",
		"-f", format,
		"-ar", sampleRate,
		"-ac", numChannels,
		"-hide_banner",
		"pipe:1",
	)

	// Create FFmpeg command
	s.cmd = exec.CommandContext(s.ctx, settings.FfmpegPath, args...)

	// Setup process group
	setupProcessGroup(s.cmd)

	// Capture stderr
	s.stderr.Reset()
	s.cmd.Stderr = &s.stderr

	// Get stdout pipe
	var err error
	s.stdout, err = s.cmd.StdoutPipe()
	if err != nil {
		return errors.New(fmt.Errorf("failed to create stdout pipe: %w", err)).
			Category(errors.CategorySystem).
			Component("ffmpeg-stream").
			Context("operation", "start_process").
			Context("url", privacy.SanitizeRTSPUrl(s.url)).
			Build()
	}

	// Start process
	if err := s.cmd.Start(); err != nil {
		return errors.New(fmt.Errorf("failed to start FFmpeg: %w", err)).
			Category(errors.CategorySystem).
			Component("ffmpeg-stream").
			Context("operation", "start_process").
			Context("url", privacy.SanitizeRTSPUrl(s.url)).
			Context("transport", s.transport).
			Build()
	}

	// Record start time for runtime calculation
	s.processStartTime = time.Now()

	// Reset failure count on successful start
	s.resetFailures()

	streamLogger.Info("FFmpeg process started",
		"url", privacy.SanitizeRTSPUrl(s.url),
		"pid", s.cmd.Process.Pid,
		"transport", s.transport,
		"component", "ffmpeg-stream",
		"operation", "start_process")

	log.Printf("✅ FFmpeg started for %s (PID: %d)", privacy.SanitizeRTSPUrl(s.url), s.cmd.Process.Pid)
	return nil
}

// processAudio reads and processes audio data from FFmpeg
func (s *FFmpegStream) processAudio() error {
	buf := make([]byte, ffmpegBufferSize)
	startTime := time.Now()

	// Create a ticker for silence detection
	silenceCheckTicker := time.NewTicker(silenceCheckInterval)
	defer silenceCheckTicker.Stop()

	// Create a timer for initial health check
	healthCheckDone := false
	healthCheckTimer := time.NewTimer(healthCheckInterval)
	defer func() {
		// Ensure timer is stopped to prevent goroutine leak
		if !healthCheckTimer.Stop() {
			// Drain the channel if timer already fired
			select {
			case <-healthCheckTimer.C:
			default:
			}
		}
	}()

	// Reset data counters
	s.bytesReceivedMu.Lock()
	s.totalBytesReceived = 0
	s.bytesReceivedMu.Unlock()

	for {
		// Set read deadline for timeout handling
		n, err := s.stdout.Read(buf)
		if err != nil {
			// Check if process exited too quickly
			if time.Since(startTime) < processQuickExitTime {
				// Get stderr output safely (process has exited at this point)
				s.cmdMu.Lock()
				stderrOutput := s.stderr.String()
				s.cmdMu.Unlock()
				// Sanitize stderr output to remove sensitive data
				sanitizedOutput := privacy.SanitizeRTSPUrls(stderrOutput)
				return errors.Newf("FFmpeg process failed to start properly: %s", sanitizedOutput).
					Category(errors.CategoryRTSP).
					Component("ffmpeg-stream").
					Context("operation", "process_audio").
					Context("url", privacy.SanitizeRTSPUrl(s.url)).
					Context("transport", s.transport).
					Context("exit_time_seconds", time.Since(startTime).Seconds()).
					Context("error_detail", sanitizedOutput).
					Build()
			}

			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return nil // Normal shutdown
			}

			return errors.New(fmt.Errorf("error reading from FFmpeg: %w", err)).
				Category(errors.CategoryRTSP).
				Component("ffmpeg-stream").
				Context("operation", "process_audio").
				Context("url", privacy.SanitizeRTSPUrl(s.url)).
				Context("runtime_seconds", time.Since(startTime).Seconds()).
				Build()
		}

		if n > 0 {
			// Update last data time
			s.updateLastDataTime()

			// Update data tracking
			s.bytesReceivedMu.Lock()
			s.totalBytesReceived += int64(n)
			s.bytesReceivedMu.Unlock()

			// Update data rate
			s.dataRateCalc.addSample(int64(n))

			// Process the audio data
			if err := s.handleAudioData(buf[:n]); err != nil {
				log.Printf("⚠️ Error processing audio data for %s: %v", privacy.SanitizeRTSPUrl(s.url), err)
			}
		}

		// Check for restart signal and silence detection
		select {
		case <-s.restartChan:
			streamLogger.Info("restart requested",
				"url", privacy.SanitizeRTSPUrl(s.url),
				"component", "ffmpeg-stream",
				"operation", "restart_requested")
			log.Printf("🔄 Restart requested for %s", privacy.SanitizeRTSPUrl(s.url))
			s.cleanupProcess()
			return nil
		case <-s.ctx.Done():
			s.cleanupProcess()
			return s.ctx.Err()
		case <-healthCheckTimer.C:
			// Log initial health status after 5 seconds
			if !healthCheckDone {
				healthCheckDone = true
				s.logStreamHealth()
			}
		case <-silenceCheckTicker.C:
			// Check for silence timeout
			s.lastDataMu.RLock()
			lastData := s.lastDataTime
			s.lastDataMu.RUnlock()

			if time.Since(lastData) > silenceTimeout {
				streamLogger.Warn("no data received from RTSP source, triggering restart",
					"url", privacy.SanitizeRTSPUrl(s.url),
					"timeout_seconds", silenceTimeout.Seconds(),
					"last_data_ago_seconds", time.Since(lastData).Seconds(),
					"component", "ffmpeg-stream",
					"operation", "silence_detected")
				log.Printf("⚠️ No data from %s for %v, restarting stream", privacy.SanitizeRTSPUrl(s.url), time.Since(lastData))
				s.cleanupProcess()
				return errors.Newf("stream stopped producing data for %v seconds", silenceTimeout.Seconds()).
					Category(errors.CategoryRTSP).
					Component("ffmpeg-stream").
					Context("operation", "silence_timeout").
					Context("url", privacy.SanitizeRTSPUrl(s.url)).
					Context("timeout_seconds", silenceTimeout.Seconds()).
					Context("last_data_seconds_ago", time.Since(lastData).Seconds()).
					Build()
			}
		default:
			// Continue processing
		}
	}
}

// handleAudioData processes a chunk of audio data
func (s *FFmpegStream) handleAudioData(data []byte) error {
	// Write to analysis buffer
	if err := WriteToAnalysisBuffer(s.url, data); err != nil {
		return errors.New(fmt.Errorf("failed to write to analysis buffer: %w", err)).
			Category(errors.CategoryAudio).
			Component("ffmpeg-stream").
			Context("operation", "handle_audio_data").
			Context("url", privacy.SanitizeRTSPUrl(s.url)).
			Context("data_size", len(data)).
			Build()
	}

	// Write to capture buffer
	if err := WriteToCaptureBuffer(s.url, data); err != nil {
		return errors.New(fmt.Errorf("failed to write to capture buffer: %w", err)).
			Category(errors.CategoryAudio).
			Component("ffmpeg-stream").
			Context("operation", "handle_audio_data").
			Context("url", privacy.SanitizeRTSPUrl(s.url)).
			Context("data_size", len(data)).
			Build()
	}

	// Broadcast to WebSocket clients
	broadcastAudioData(s.url, data)

	// Calculate audio level
	audioLevel := calculateAudioLevel(data, s.url, "")

	// Create unified audio data
	unifiedData := UnifiedAudioData{
		AudioLevel: audioLevel,
		Timestamp:  time.Now(),
	}

	// Process sound level if enabled
	if conf.Setting().Realtime.Audio.SoundLevel.Enabled {
		if soundLevel, err := ProcessSoundLevelData(s.url, data); err != nil {
			// Log as warning if it's a registration issue, debug otherwise
			if errors.Is(err, ErrSoundLevelProcessorNotRegistered) {
				streamLogger.Warn("sound level processor not registered",
					"url", privacy.SanitizeRTSPUrl(s.url),
					"error", err,
					"operation", "process_sound_level")
				log.Printf("⚠️ Sound level processor not registered for %s: %v", privacy.SanitizeRTSPUrl(s.url), err)
			} else {
				streamLogger.Debug("failed to process sound level data",
					"url", privacy.SanitizeRTSPUrl(s.url),
					"error", err,
					"operation", "process_sound_level")
				log.Printf("⚠️ Error processing sound level for %s: %v", privacy.SanitizeRTSPUrl(s.url), err)
			}
		} else if soundLevel != nil {
			unifiedData.SoundLevel = soundLevel
		}
	}

	// Send to audio channel (non-blocking)
	select {
	case s.audioChan <- unifiedData:
		// Data sent successfully
	default:
		// Channel full, drop data to avoid blocking
		s.logDroppedData()
	}

	return nil
}

// logDroppedData logs dropped audio data with rate limiting
func (s *FFmpegStream) logDroppedData() {
	s.dropLogMu.Lock()
	defer s.dropLogMu.Unlock()
	
	now := time.Now()
	if now.Sub(s.lastDropLogTime) >= dropLogInterval {
		s.lastDropLogTime = now
		
		streamLogger.Warn("audio data dropped due to full channel",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"component", "ffmpeg-stream",
			"operation", "audio_data_drop")
		
		log.Printf("⚠️ Audio data dropped for %s - channel full", privacy.SanitizeRTSPUrl(s.url))
		
		// Report to Sentry with enhanced context
		errorWithContext := errors.Newf("audio processing channel full, data being dropped").
			Component("ffmpeg-stream").
			Category(errors.CategoryAudio).
			Context("operation", "audio_data_drop").
			Context("url", privacy.SanitizeRTSPUrl(s.url)).
			Context("channel_capacity", cap(s.audioChan)).
			Context("drop_log_interval_seconds", dropLogInterval.Seconds()).
			Build()
		// This will be reported via event bus if telemetry is enabled
		_ = errorWithContext
	}
}

// cleanupProcess cleans up the FFmpeg process
func (s *FFmpegStream) cleanupProcess() {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()

	if s.cmd == nil || s.cmd.Process == nil {
		return
	}

	// Close stdout
	if s.stdout != nil {
		if err := s.stdout.Close(); err != nil {
			// Log but don't fail - process cleanup is more important
			streamLogger.Debug("failed to close stdout",
				"url", privacy.SanitizeRTSPUrl(s.url),
				"error", err,
				"operation", "cleanup_process")
			log.Printf("⚠️ Error closing stdout for %s: %v", privacy.SanitizeRTSPUrl(s.url), err)
		}
	}

	// Kill process
	if err := killProcessGroup(s.cmd); err != nil {
		if killErr := s.cmd.Process.Kill(); killErr != nil {
			// Only log if kill also fails
			log.Printf("⚠️ Error killing process for %s: %v", privacy.SanitizeRTSPUrl(s.url), killErr)
		}
	}

	// Wait for process to exit
	done := make(chan struct{})
	go func() {
		if err := s.cmd.Wait(); err != nil {
			// This is expected when we kill the process
			// Only log if it's not an expected exit status
			if !strings.Contains(err.Error(), "signal: killed") {
				streamLogger.Warn("FFmpeg process wait error",
					"url", privacy.SanitizeRTSPUrl(s.url),
					"error", err,
					"component", "ffmpeg-stream",
					"operation", "process_wait")
				log.Printf("⚠️ Process wait error for %s: %v", privacy.SanitizeRTSPUrl(s.url), err)
			}
		}
		close(done)
	}()

	select {
	case <-done:
		streamLogger.Info("FFmpeg process stopped",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"component", "ffmpeg-stream",
			"operation", "cleanup_process")
		log.Printf("🛑 FFmpeg process stopped for %s", privacy.SanitizeRTSPUrl(s.url))
	case <-time.After(processCleanupTimeout):
		streamLogger.Warn("FFmpeg process cleanup timeout",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"component", "ffmpeg-stream",
			"operation", "cleanup_process")
		log.Printf("⚠️ FFmpeg process cleanup timeout for %s", privacy.SanitizeRTSPUrl(s.url))
	}

	s.cmd = nil
}

// handleRestartBackoff handles exponential backoff for restarts
func (s *FFmpegStream) handleRestartBackoff() {
	s.restartCountMu.Lock()
	s.restartCount++
	currentRestartCount := s.restartCount
	
	// Cap the exponent to prevent integer overflow
	exponent := s.restartCount - 1
	if exponent > maxBackoffExponent {
		exponent = maxBackoffExponent
	}
	
	backoff := s.backoffDuration * time.Duration(1<<uint(exponent))
	if backoff > s.maxBackoff {
		backoff = s.maxBackoff
	}
	s.restartCountMu.Unlock()

	streamLogger.Debug("applying restart backoff",
		"url", privacy.SanitizeRTSPUrl(s.url),
		"backoff_ms", backoff.Milliseconds(),
		"restart_count", currentRestartCount,
		"operation", "restart_backoff")

	log.Printf("⏳ Waiting %v before restart attempt #%d for %s", backoff, currentRestartCount, privacy.SanitizeRTSPUrl(s.url))

	select {
	case <-time.After(backoff):
		// Continue with restart
	case <-s.ctx.Done():
		// Context cancelled
	case <-s.stopChan:
		// Stop requested
	}
}

// Stop gracefully stops the FFmpeg stream.
// It signals the stream to stop, cancels the context, and cleans up the FFmpeg process.
func (s *FFmpegStream) Stop() {
	s.stoppedMu.Lock()
	s.stopped = true
	s.stoppedMu.Unlock()

	// Signal stop
	close(s.stopChan)

	// Cancel context
	if s.cancel != nil {
		s.cancel()
	}

	// Cleanup process
	s.cleanupProcess()
}

// Restart requests a stream restart.
// If manual is true, resets the restart count (user-initiated restart).
// If manual is false, keeps the restart count intact (automatic health-triggered restart).
// If a restart is already pending, this call is ignored.
func (s *FFmpegStream) Restart(manual bool) {
	// Reset restart count only on manual restart
	if manual {
		s.restartCountMu.Lock()
		s.restartCount = 0
		s.restartCountMu.Unlock()
	}

	// Send restart signal (non-blocking)
	select {
	case s.restartChan <- struct{}{}:
		// Signal sent
	default:
		// Channel full, restart already pending
	}
}

// GetHealth returns the current health status of the stream.
// It includes information about data reception, restart count, and data rate statistics.
func (s *FFmpegStream) GetHealth() StreamHealth {
	s.lastDataMu.RLock()
	lastData := s.lastDataTime
	s.lastDataMu.RUnlock()

	s.restartCountMu.Lock()
	restarts := s.restartCount
	s.restartCountMu.Unlock()

	s.bytesReceivedMu.RLock()
	totalBytes := s.totalBytesReceived
	s.bytesReceivedMu.RUnlock()

	// Get current data rate
	dataRate, err := s.dataRateCalc.getRate()
	if err != nil {
		// Log error but don't fail health check
		streamLogger.Debug("failed to calculate data rate",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"error", err,
			"component", "ffmpeg-stream")
		dataRate = 0
	}

	// Get configurable thresholds
	settings := conf.Setting()
	healthyDataThreshold := time.Duration(settings.Realtime.RTSP.Health.HealthyDataThreshold) * time.Second
	
	// Validate threshold: must be positive and within reasonable limits (max 30 minutes)
	const maxHealthyDataThreshold = 30 * time.Minute
	if healthyDataThreshold <= 0 || healthyDataThreshold > maxHealthyDataThreshold {
		healthyDataThreshold = defaultHealthyDataThreshold
	}
	
	// Consider unhealthy if no data for configured threshold
	isHealthy := time.Since(lastData) < healthyDataThreshold
	// Stream is receiving data if we got data within the threshold
	isReceivingData := time.Since(lastData) < defaultReceivingDataThreshold

	return StreamHealth{
		IsHealthy:          isHealthy,
		LastDataReceived:   lastData,
		RestartCount:       restarts,
		TotalBytesReceived: totalBytes,
		BytesPerSecond:     dataRate,
		IsReceivingData:    isReceivingData,
	}
}

// updateLastDataTime updates the last data received timestamp
func (s *FFmpegStream) updateLastDataTime() {
	s.lastDataMu.Lock()
	s.lastDataTime = time.Now()
	s.lastDataMu.Unlock()
}

// logStreamHealth logs the current stream health status
func (s *FFmpegStream) logStreamHealth() {
	health := s.GetHealth()

	// Get current data statistics
	s.bytesReceivedMu.RLock()
	totalBytes := s.totalBytesReceived
	s.bytesReceivedMu.RUnlock()

	dataRate, err := s.dataRateCalc.getRate()
	if err != nil {
		// Log error but continue with zero rate for display
		streamLogger.Debug("failed to calculate data rate for health log",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"error", err,
			"component", "ffmpeg-stream")
		dataRate = 0
	}

	if health.IsReceivingData {
		streamLogger.Info("stream health check - receiving data",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"is_healthy", health.IsHealthy,
			"is_receiving_data", health.IsReceivingData,
			"total_bytes_received", totalBytes,
			"bytes_per_second", dataRate,
			"last_data_ago_seconds", time.Since(health.LastDataReceived).Seconds(),
			"component", "ffmpeg-stream",
			"operation", "health_check")
		log.Printf("✅ Stream %s is healthy and receiving data (%.1f KB/s)",
			privacy.SanitizeRTSPUrl(s.url), dataRate/1024)
	} else {
		streamLogger.Warn("stream health check - no data received",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"is_healthy", health.IsHealthy,
			"is_receiving_data", health.IsReceivingData,
			"total_bytes_received", totalBytes,
			"last_data_ago_seconds", time.Since(health.LastDataReceived).Seconds(),
			"component", "ffmpeg-stream",
			"operation", "health_check")
		log.Printf("⚠️ Stream %s is not receiving data", privacy.SanitizeRTSPUrl(s.url))
	}
}

// isCircuitOpen checks if the circuit breaker is open
func (s *FFmpegStream) isCircuitOpen() bool {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()

	if s.consecutiveFailures >= circuitBreakerThreshold {
		// Check if we're still in cooldown
		if time.Since(s.circuitOpenTime) < circuitBreakerCooldown {
			streamLogger.Warn("circuit breaker is open",
				"url", privacy.SanitizeRTSPUrl(s.url),
				"consecutive_failures", s.consecutiveFailures,
				"cooldown_remaining", circuitBreakerCooldown-time.Since(s.circuitOpenTime),
				"component", "ffmpeg-stream")
			return true
		}
		// Reset after cooldown
		s.consecutiveFailures = 0
	}
	return false
}

// recordFailure records a failure for the circuit breaker
func (s *FFmpegStream) recordFailure() {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()

	s.consecutiveFailures++
	if s.consecutiveFailures == circuitBreakerThreshold {
		s.circuitOpenTime = time.Now()
		streamLogger.Error("circuit breaker opened due to repeated failures",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"consecutive_failures", s.consecutiveFailures,
			"component", "ffmpeg-stream")
		log.Printf("🔒 Circuit breaker opened for %s after %d consecutive failures",
			privacy.SanitizeRTSPUrl(s.url), s.consecutiveFailures)
		
		// Report to Sentry with enhanced context
		errorWithContext := errors.Newf("RTSP stream circuit breaker opened after %d consecutive failures", s.consecutiveFailures).
			Component("ffmpeg-stream").
			Category(errors.CategoryRTSP).
			Context("operation", "circuit_breaker_open").
			Context("url", privacy.SanitizeRTSPUrl(s.url)).
			Context("transport", s.transport).
			Context("consecutive_failures", s.consecutiveFailures).
			Context("cooldown_seconds", circuitBreakerCooldown.Seconds()).
			Build()
		// This will be reported via event bus if telemetry is enabled
		_ = errorWithContext
	}
}

// resetFailures resets the failure count
func (s *FFmpegStream) resetFailures() {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()

	if s.consecutiveFailures > 0 {
		streamLogger.Info("resetting failure count after successful run",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"previous_failures", s.consecutiveFailures,
			"component", "ffmpeg-stream")
		s.consecutiveFailures = 0
	}
}

// detectUserTimeout scans FFmpeg parameters for an existing timeout setting
// Returns true and the timeout value if found, false and empty string otherwise
func detectUserTimeout(params []string) (found bool, value string) {
	for i, param := range params {
		if param == "-timeout" && i+1 < len(params) {
			return true, params[i+1]
		}
	}
	return false, ""
}

// validateUserTimeout validates a user-provided timeout value
// The timeout should be in microseconds and at least 1 second
func (s *FFmpegStream) validateUserTimeout(timeoutStr string) error {
	timeout, err := strconv.ParseInt(timeoutStr, 10, 64)
	if err != nil {
		return errors.Newf("invalid timeout format: %s (must be a number in microseconds)", timeoutStr).
			Component("ffmpeg-stream").
			Category(errors.CategoryValidation).
			Context("operation", "validate_user_timeout").
			Context("timeout_value", timeoutStr).
			Build()
	}

	if timeout < minTimeoutMicroseconds {
		return errors.Newf("timeout too short: %d microseconds (minimum: %d microseconds = 1 second)", timeout, minTimeoutMicroseconds).
			Component("ffmpeg-stream").
			Category(errors.CategoryValidation).
			Context("operation", "validate_user_timeout").
			Context("timeout_microseconds", timeout).
			Context("minimum_microseconds", minTimeoutMicroseconds).
			Build()
	}

	return nil
}
