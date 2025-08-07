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
	defaultGracePeriod            = 30 * time.Second // Grace period for new streams before marking unhealthy

	// Circuit breaker settings
	circuitBreakerThreshold = 10              // Number of consecutive failures before opening circuit
	circuitBreakerCooldown  = 5 * time.Minute // Cooldown period when circuit is open

	// Drop logging settings
	dropLogInterval = 30 * time.Second // Minimum interval between drop log messages

	// Maximum safe exponent for bit shift to prevent overflow
	maxBackoffExponent = 20 // This allows up to 2^20 = ~1 million multiplier

	// Timeout settings for FFmpeg RTSP streams
	defaultTimeoutMicroseconds = 10000000 // 10 seconds in microseconds
	minTimeoutMicroseconds     = 1000000  // 1 second in microseconds
)

// Use shared logger from integration file
var streamLogger *slog.Logger

func init() {
	// Use the shared integration logger for consistency
	streamLogger = integrationLogger
	if streamLogger == nil {
		// Fallback if integration logger not initialized
		streamLogger = slog.Default().With("component", "ffmpeg-stream")
	}
}

// dataRateCalculator tracks data rate over a sliding window
type dataRateCalculator struct {
	url        string // Store URL once to avoid passing it repeatedly
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
func newDataRateCalculator(url string, windowSize time.Duration) *dataRateCalculator {
	return &dataRateCalculator{
		url:        url,
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
		return 0, errors.Newf("no data received from RTSP source: %s", privacy.SanitizeRTSPUrl(d.url)).
			Component("ffmpeg-stream").
			Category(errors.CategoryAudioSource).
			Priority(errors.PriorityMedium).
			Context("operation", "calculate_data_rate").
			Context("url", privacy.SanitizeRTSPUrl(d.url)).
			Build()
	}

	if len(d.samples) < 2 {
		return 0, errors.Newf("insufficient data received from RTSP source: %s", privacy.SanitizeRTSPUrl(d.url)).
			Component("ffmpeg-stream").
			Category(errors.CategoryAudioSource).
			Priority(errors.PriorityLow).
			Context("operation", "calculate_data_rate").
			Context("sample_count", len(d.samples)).
			Context("url", privacy.SanitizeRTSPUrl(d.url)).
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
			Category(errors.CategoryRTSP).
			Priority(errors.PriorityLow).
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
	cmd      *exec.Cmd
	cmdMu    sync.Mutex
	stdout   io.ReadCloser
	stderr   bytes.Buffer
	stderrMu sync.RWMutex // Protect stderr buffer access

	// State management
	ctx         context.Context
	cancel      context.CancelFunc
	cancelMu    sync.RWMutex // Protect cancel function access
	restartChan chan struct{}
	stopChan    chan struct{}
	stopped     bool
	stoppedMu   sync.RWMutex

	// Health tracking
	lastDataTime   time.Time
	lastDataMu     sync.RWMutex
	restartCount   int
	restartCountMu sync.Mutex

	// Concurrent restart protection
	restartInProgress bool
	restartMu         sync.Mutex

	// Process lifecycle metrics
	totalProcessCount   int64
	shortLivedProcesses int64
	processMetricsMu    sync.Mutex

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

	// Sound level processor registration tracking
	soundLevelNotRegisteredLogMu   sync.Mutex
	lastSoundLevelNotRegisteredLog time.Time

	// Stream creation time for grace period calculation
	streamCreatedAt time.Time
}

// threadSafeWriter wraps a bytes.Buffer with mutex protection for concurrent access
type threadSafeWriter struct {
	buf *bytes.Buffer
	mu  *sync.RWMutex
}

// Write implements io.Writer interface with thread-safe access
func (w *threadSafeWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// NewFFmpegStream creates a new FFmpeg stream handler.
// The url parameter specifies the RTSP stream URL, transport specifies the RTSP transport protocol,
// and audioChan is the channel where processed audio data will be sent.
func NewFFmpegStream(url, transport string, audioChan chan UnifiedAudioData) *FFmpegStream {
	return &FFmpegStream{
		url:                            url,
		transport:                      transport,
		audioChan:                      audioChan,
		restartChan:                    make(chan struct{}, 1),
		stopChan:                       make(chan struct{}),
		backoffDuration:                defaultBackoffDuration,
		maxBackoff:                     maxBackoffDuration,
		lastDataTime:                   time.Time{}, // Zero time - no data received yet
		dataRateCalc:                   newDataRateCalculator(url, dataRateWindowSize),
		lastDropLogTime:                time.Now(),
		lastSoundLevelNotRegisteredLog: time.Now().Add(-dropLogInterval), // Allow immediate first log
		streamCreatedAt:                time.Now(), // Track when stream was created
	}
}

// Run starts and manages the FFmpeg process lifecycle.
// It runs in a loop, automatically restarting the process on failures with exponential backoff.
// The function returns when the context is cancelled or Stop() is called.
func (s *FFmpegStream) Run(parentCtx context.Context) {
	// Set context and cancel function with proper locking
	s.cancelMu.Lock()
	s.ctx, s.cancel = context.WithCancel(parentCtx)
	s.cancelMu.Unlock()

	defer func() {
		s.cancelMu.Lock()
		if s.cancel != nil {
			s.cancel()
		}
		s.cancelMu.Unlock()
	}()

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
				s.recordFailure(0) // No runtime for startup failure
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
				s.recordFailure(runtime)
				// Log process exit with sanitized error message
				errorMsg := err.Error()
				sanitizedError := privacy.SanitizeFFmpegError(errorMsg)

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

			// Always cleanup the process before restart
			if conf.Setting().Debug {
				streamLogger.Debug("calling cleanup after process exit",
					"url", privacy.SanitizeRTSPUrl(s.url),
					"runtime_seconds", runtime.Seconds(),
					"had_error", err != nil,
					"operation", "pre_restart_cleanup")
			}
			s.cleanupProcess()

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

	// Capture stderr with thread-safe writer
	s.stderrMu.Lock()
	s.stderr.Reset()
	s.stderrMu.Unlock()
	s.cmd.Stderr = &threadSafeWriter{buf: &s.stderr, mu: &s.stderrMu}

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

	// Debug log process details
	if conf.Setting().Debug {
		streamLogger.Debug("FFmpeg process started with details",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"pid", s.cmd.Process.Pid,
			"transport", s.transport,
			"ffmpeg_path", settings.FfmpegPath,
			"args_count", len(args),
			"has_user_timeout", hasUserTimeout,
			"operation", "start_process_debug")
	}

	// Update process metrics
	s.processMetricsMu.Lock()
	s.totalProcessCount++
	currentTotal := s.totalProcessCount
	s.processMetricsMu.Unlock()

	// NOTE: Removed premature failure reset - failures should only be reset
	// after the process has proven stable operation with actual data reception
	
	streamLogger.Info("FFmpeg process started",
		"url", privacy.SanitizeRTSPUrl(s.url),
		"pid", s.cmd.Process.Pid,
		"transport", s.transport,
		"total_process_count", currentTotal,
		"component", "ffmpeg-stream",
		"operation", "start_process")

	log.Printf("✅ FFmpeg started for %s (PID: %d, Process #%d, Restart #%d)",
		privacy.SanitizeRTSPUrl(s.url), s.cmd.Process.Pid, currentTotal, s.restartCount)
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
				// Get stderr output safely
				s.stderrMu.RLock()
				stderrOutput := s.stderr.String()
				s.stderrMu.RUnlock()
				// Sanitize stderr output to remove sensitive data and memory addresses
				sanitizedOutput := privacy.SanitizeFFmpegError(stderrOutput)
				return errors.Newf("FFmpeg process failed to start properly: %s", sanitizedOutput).
					Category(errors.CategoryRTSP).
					Component("ffmpeg-stream").
					Priority(errors.PriorityMedium).
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
				Priority(errors.PriorityMedium).
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
			totalReceived := s.totalBytesReceived
			s.bytesReceivedMu.Unlock()

			// Update data rate
			s.dataRateCalc.addSample(int64(n))

			// Check if we should reset failures after stable operation
			s.conditionalFailureReset(totalReceived)

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

			// Clear restart in progress flag
			s.restartMu.Lock()
			s.restartInProgress = false
			s.restartMu.Unlock()

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
					Priority(errors.PriorityMedium).
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
			// Skip logging for normal conditions (interval incomplete, no data)
			if errors.Is(err, ErrSoundLevelProcessorNotRegistered) {
				// Rate limit this specific log message to prevent flooding
				s.soundLevelNotRegisteredLogMu.Lock()
				now := time.Now()
				if now.Sub(s.lastSoundLevelNotRegisteredLog) >= dropLogInterval {
					s.lastSoundLevelNotRegisteredLog = now
					streamLogger.Warn("sound level processor not registered",
						"url", privacy.SanitizeRTSPUrl(s.url),
						"error", err,
						"operation", "process_sound_level")
					log.Printf("⚠️ Sound level processor not registered for %s: %v (further messages suppressed)", privacy.SanitizeRTSPUrl(s.url), err)
				}
				s.soundLevelNotRegisteredLogMu.Unlock()
			} else if !errors.Is(err, ErrIntervalIncomplete) && !errors.Is(err, ErrNoAudioData) {
				if conf.Setting().Debug {
					streamLogger.Debug("failed to process sound level data",
						"url", privacy.SanitizeRTSPUrl(s.url),
						"error", err,
						"operation", "process_sound_level")
				}
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
			Category(errors.CategorySystem).
			Priority(errors.PriorityHigh).
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
		if conf.Setting().Debug {
			streamLogger.Debug("cleanup called but no process to clean",
				"url", privacy.SanitizeRTSPUrl(s.url),
				"operation", "cleanup_process_skip")
		}
		return
	}

	pid := s.cmd.Process.Pid
	if conf.Setting().Debug {
		streamLogger.Debug("starting process cleanup",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"pid", pid,
			"operation", "cleanup_process_start")
	}

	// Close stdout
	if s.stdout != nil {
		if err := s.stdout.Close(); err != nil {
			// Log but don't fail - process cleanup is more important
			if conf.Setting().Debug {
				streamLogger.Debug("failed to close stdout",
					"url", privacy.SanitizeRTSPUrl(s.url),
					"error", err,
					"operation", "cleanup_process")
			}
			log.Printf("⚠️ Error closing stdout for %s: %v", privacy.SanitizeRTSPUrl(s.url), err)
		}
	}

	// Kill process
	if err := killProcessGroup(s.cmd); err != nil {
		if conf.Setting().Debug {
			streamLogger.Debug("killProcessGroup failed, attempting direct kill",
				"url", privacy.SanitizeRTSPUrl(s.url),
				"pid", pid,
				"error", err,
				"operation", "cleanup_process_kill")
		}

		if killErr := s.cmd.Process.Kill(); killErr != nil {
			// Only log if kill also fails
			streamLogger.Warn("failed to kill process directly",
				"url", privacy.SanitizeRTSPUrl(s.url),
				"pid", pid,
				"error", killErr,
				"operation", "cleanup_process_kill_direct")
			log.Printf("⚠️ Error killing process for %s: %v", privacy.SanitizeRTSPUrl(s.url), killErr)
		}
	} else if conf.Setting().Debug {
		streamLogger.Debug("process group killed successfully",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"pid", pid,
			"operation", "cleanup_process_kill_success")
	}

	// Create a channel to signal when Wait() completes
	waitDone := make(chan error, 1)

	// Always call Wait() to reap the zombie - this is critical!
	// We do this in a goroutine that we may abandon if it takes too long,
	// but the goroutine will continue and eventually reap the process
	// Capture cmd reference to avoid race condition
	cmd := s.cmd
	url := s.url
	waitStartTime := time.Now()

	if conf.Setting().Debug {
		streamLogger.Debug("spawning wait goroutine for process reaping",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"pid", pid,
			"operation", "cleanup_process_wait_start")
	}

	go func() {
		waitErr := cmd.Wait()
		waitDuration := time.Since(waitStartTime)

		// Log completion even if we've already moved on
		if conf.Setting().Debug {
			if waitErr != nil && !strings.Contains(waitErr.Error(), "signal: killed") && !strings.Contains(waitErr.Error(), "signal: terminated") {
				streamLogger.Debug("process wait completed with error",
					"url", privacy.SanitizeRTSPUrl(url),
					"error", waitErr,
					"pid", pid,
					"wait_duration_ms", waitDuration.Milliseconds(),
					"operation", "cleanup_process_wait_error")
			} else {
				streamLogger.Debug("process wait completed successfully",
					"url", privacy.SanitizeRTSPUrl(url),
					"pid", pid,
					"wait_duration_ms", waitDuration.Milliseconds(),
					"operation", "cleanup_process_wait_success")
			}
		}

		// Send result if channel is still open
		select {
		case waitDone <- waitErr:
		default:
			// Channel might be closed if we timed out
			if conf.Setting().Debug {
				streamLogger.Debug("wait result not sent - cleanup already timed out",
					"url", privacy.SanitizeRTSPUrl(url),
					"pid", pid,
					"operation", "cleanup_process_wait_abandoned")
			}
		}
	}()

	// Wait for process cleanup with timeout
	select {
	case err := <-waitDone:
		// Process was successfully reaped
		if err != nil && !strings.Contains(err.Error(), "signal: killed") && !strings.Contains(err.Error(), "signal: terminated") {
			streamLogger.Warn("FFmpeg process wait error",
				"url", privacy.SanitizeRTSPUrl(s.url),
				"error", err,
				"pid", pid,
				"component", "ffmpeg-stream",
				"operation", "process_wait")
			log.Printf("⚠️ Process wait error for %s (PID: %d): %v", privacy.SanitizeRTSPUrl(s.url), pid, err)
		}
		streamLogger.Info("FFmpeg process stopped",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"pid", pid,
			"component", "ffmpeg-stream",
			"operation", "cleanup_process")
		log.Printf("🛑 FFmpeg process stopped for %s (PID: %d)", privacy.SanitizeRTSPUrl(s.url), pid)

	case <-time.After(processCleanupTimeout):
		// Timeout occurred, but the goroutine will continue and eventually reap the process
		streamLogger.Warn("FFmpeg process cleanup timeout - process will be reaped asynchronously",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"pid", pid,
			"timeout_seconds", processCleanupTimeout.Seconds(),
			"component", "ffmpeg-stream",
			"operation", "cleanup_process_timeout")
		log.Printf("⚠️ FFmpeg process cleanup timeout for %s (PID: %d) - reaping asynchronously", privacy.SanitizeRTSPUrl(s.url), pid)

		// Important: We do NOT return here - we continue to clean up our state
		// The goroutine will eventually call Wait() and reap the zombie
		// This is a simple and correct approach - we ensure Wait() is always called
		// even if it takes longer than expected

		if conf.Setting().Debug {
			streamLogger.Debug("cleanup timeout - wait goroutine still running",
				"url", privacy.SanitizeRTSPUrl(s.url),
				"pid", pid,
				"operation", "cleanup_process_timeout_detail")
		}
	}

	// Clear the command reference
	s.cmd = nil

	if conf.Setting().Debug {
		streamLogger.Debug("process cleanup completed",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"pid", pid,
			"operation", "cleanup_process_complete")
	}
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

	// Add rate limiting for very high restart counts to prevent runaway loops
	if currentRestartCount > 50 {
		// Additional delay proportional to restart count beyond 50
		additionalDelay := time.Duration(currentRestartCount-50) * 10 * time.Second
		if additionalDelay > 5*time.Minute {
			additionalDelay = 5 * time.Minute // Cap at 5 minutes extra
		}
		backoff += additionalDelay
		streamLogger.Warn("high restart count detected - applying rate limiting",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"restart_count", currentRestartCount,
			"additional_delay_seconds", additionalDelay.Seconds(),
			"component", "ffmpeg-stream",
			"operation", "restart_rate_limit")
	}
	s.restartCountMu.Unlock()

	if conf.Setting().Debug {
		streamLogger.Debug("applying restart backoff",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"backoff_ms", backoff.Milliseconds(),
			"restart_count", currentRestartCount,
			"operation", "restart_backoff")
	}

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

	// Cancel context with proper locking
	s.cancelMu.RLock()
	cancel := s.cancel
	s.cancelMu.RUnlock()

	if cancel != nil {
		cancel()
	}

	// Cleanup process
	s.cleanupProcess()
}

// Restart requests a stream restart.
// If manual is true, resets the restart count (user-initiated restart).
// If manual is false, keeps the restart count intact (automatic health-triggered restart).
// If a restart is already pending, this call is ignored.
func (s *FFmpegStream) Restart(manual bool) {
	// Check if restart is already in progress
	s.restartMu.Lock()
	if s.restartInProgress {
		s.restartMu.Unlock()
		if conf.Setting().Debug {
			streamLogger.Debug("restart already in progress, ignoring request",
				"url", privacy.SanitizeRTSPUrl(s.url),
				"manual", manual,
				"component", "ffmpeg-stream",
				"operation", "restart_ignored")
		}
		return
	}
	s.restartInProgress = true
	s.restartMu.Unlock()

	// Reset restart count only on manual restart
	if manual {
		s.restartCountMu.Lock()
		s.restartCount = 0
		s.restartCountMu.Unlock()
	}

	// Send restart signal (non-blocking)
	select {
	case s.restartChan <- struct{}{}:
		// Signal sent successfully
		if conf.Setting().Debug {
			streamLogger.Debug("restart signal sent",
				"url", privacy.SanitizeRTSPUrl(s.url),
				"manual", manual,
				"component", "ffmpeg-stream",
				"operation", "restart_requested")
		}
	default:
		// Channel full, reset the in-progress flag
		s.restartMu.Lock()
		s.restartInProgress = false
		s.restartMu.Unlock()
		if conf.Setting().Debug {
			streamLogger.Debug("restart channel full, restart already pending",
				"url", privacy.SanitizeRTSPUrl(s.url),
				"manual", manual,
				"component", "ffmpeg-stream",
				"operation", "restart_pending")
		}
	}
}

// GetHealth returns the current health status of the stream.
// It includes information about data reception, restart count, and data rate statistics.
func (s *FFmpegStream) GetHealth() StreamHealth {
	// Get current process PID for debugging
	var currentPID int
	s.cmdMu.Lock()
	if s.cmd != nil && s.cmd.Process != nil {
		currentPID = s.cmd.Process.Pid
	}
	s.cmdMu.Unlock()
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
		if conf.Setting().Debug {
			streamLogger.Debug("failed to calculate data rate",
				"url", privacy.SanitizeRTSPUrl(s.url),
				"error", err,
				"component", "ffmpeg-stream")
		}
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

	// Handle case where no data has ever been received (zero time)
	var isHealthy, isReceivingData bool
	if lastData.IsZero() {
		// Never received any data - check if we're in grace period
		gracePeriod := defaultGracePeriod
		timeSinceCreation := time.Since(s.streamCreatedAt)
		
		if timeSinceCreation < gracePeriod {
			// Still in grace period - don't mark as unhealthy yet
			isHealthy = false // Not healthy, but not critically unhealthy either
			isReceivingData = false
			
			if conf.Setting().Debug {
				streamLogger.Debug("stream in grace period",
					"url", privacy.SanitizeRTSPUrl(s.url),
					"time_since_creation_seconds", timeSinceCreation.Seconds(),
					"grace_period_seconds", gracePeriod.Seconds(),
					"remaining_grace_seconds", (gracePeriod - timeSinceCreation).Seconds(),
					"component", "ffmpeg-stream")
			}
		} else {
			// Grace period expired and still no data - definitely not healthy
			isHealthy = false
			isReceivingData = false
		}
	} else {
		// Consider unhealthy if no data for configured threshold
		isHealthy = time.Since(lastData) < healthyDataThreshold
		// Stream is receiving data if we got data within the threshold
		isReceivingData = time.Since(lastData) < defaultReceivingDataThreshold
	}

	// Debug log health check details
	if conf.Setting().Debug {
		streamLogger.Debug("health check performed",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"pid", currentPID,
			"is_healthy", isHealthy,
			"is_receiving_data", isReceivingData,
			"last_data_seconds_ago", time.Since(lastData).Seconds(),
			"healthy_threshold_seconds", healthyDataThreshold.Seconds(),
			"total_bytes", totalBytes,
			"bytes_per_second", dataRate,
			"restart_count", restarts,
			"has_process", currentPID > 0,
			"operation", "get_health")
	}

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
		if conf.Setting().Debug {
			streamLogger.Debug("failed to calculate data rate for health log",
				"url", privacy.SanitizeRTSPUrl(s.url),
				"error", err,
				"component", "ffmpeg-stream")
		}
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

	// Check if circuit was opened (circuitOpenTime is set) and we're still in cooldown
	if !s.circuitOpenTime.IsZero() && time.Since(s.circuitOpenTime) < circuitBreakerCooldown {
		streamLogger.Warn("circuit breaker is open",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"consecutive_failures", s.consecutiveFailures,
			"cooldown_remaining", circuitBreakerCooldown-time.Since(s.circuitOpenTime),
			"component", "ffmpeg-stream")
		return true
	}
	
	// Reset after cooldown if needed
	if !s.circuitOpenTime.IsZero() && time.Since(s.circuitOpenTime) >= circuitBreakerCooldown {
		previousFailures := s.consecutiveFailures
		s.consecutiveFailures = 0
		openDuration := time.Since(s.circuitOpenTime)
		s.circuitOpenTime = time.Time{} // Reset the open time
		
		// Log circuit breaker closure
		streamLogger.Info("circuit breaker closed after cooldown",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"previous_failures", previousFailures,
			"open_duration_seconds", openDuration.Seconds(),
			"component", "ffmpeg-stream")
		
		// Report circuit breaker closure to telemetry
		errorWithContext := errors.Newf("RTSP stream circuit breaker closed after cooldown").
			Component("ffmpeg-stream").
			Category(errors.CategoryRTSP).
			Priority(errors.PriorityLow).
			Context("operation", "circuit_breaker_close").
			Context("url", privacy.SanitizeRTSPUrl(s.url)).
			Context("transport", s.transport).
			Context("previous_failures", previousFailures).
			Context("open_duration_seconds", openDuration.Seconds()).
			Context("cooldown_seconds", circuitBreakerCooldown.Seconds()).
			Build()
		// This will be reported via event bus if telemetry is enabled
		_ = errorWithContext
	}
	
	return false
}

// recordFailure records a failure for the circuit breaker with runtime consideration
func (s *FFmpegStream) recordFailure(runtime time.Duration) {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()

	s.consecutiveFailures++

	// Track short-lived processes for metrics
	if runtime < 5*time.Second {
		s.processMetricsMu.Lock()
		s.shortLivedProcesses++
		shortLived := s.shortLivedProcesses
		total := s.totalProcessCount
		s.processMetricsMu.Unlock()

		if conf.Setting().Debug {
			streamLogger.Debug("short-lived process detected",
				"url", privacy.SanitizeRTSPUrl(s.url),
				"runtime_seconds", runtime.Seconds(),
				"short_lived_count", shortLived,
				"total_count", total,
				"short_lived_percentage", float64(shortLived)/float64(total)*100,
				"component", "ffmpeg-stream",
				"operation", "process_metrics")
		}
	}

	// Enhanced circuit breaker logic that considers rapid failures
	// Open circuit breaker faster if processes are failing very quickly
	shouldOpenCircuit := false
	var reason string

	switch {
	case runtime < 1*time.Second && s.consecutiveFailures >= 3:
		// Immediate failures (< 1 second) - open circuit after just 3 failures
		shouldOpenCircuit = true
		reason = "immediate connection failures"
	case runtime < 5*time.Second && s.consecutiveFailures >= 5:
		// Rapid failures (< 5 seconds) - open circuit after just 5 failures
		shouldOpenCircuit = true
		reason = "rapid process failures"
	case runtime < 30*time.Second && s.consecutiveFailures >= 8:
		// Quick failures (< 30 seconds) - open circuit after 8 failures
		shouldOpenCircuit = true
		reason = "quick process failures"
	case s.consecutiveFailures >= circuitBreakerThreshold:
		// Standard threshold reached
		shouldOpenCircuit = true
		reason = "consecutive failure threshold"
	}

	if shouldOpenCircuit {
		s.circuitOpenTime = time.Now()
		streamLogger.Error("circuit breaker opened",
			"url", privacy.SanitizeRTSPUrl(s.url),
			"consecutive_failures", s.consecutiveFailures,
			"runtime_seconds", runtime.Seconds(),
			"reason", reason,
			"component", "ffmpeg-stream")
		log.Printf("🔒 Circuit breaker opened for %s after %d consecutive failures (%s, runtime: %v)",
			privacy.SanitizeRTSPUrl(s.url), s.consecutiveFailures, reason, runtime)

		// Report to Sentry with enhanced context
		errorWithContext := errors.Newf("RTSP stream circuit breaker opened: %s (runtime: %v)", reason, runtime).
			Component("ffmpeg-stream").
			Category(errors.CategoryRTSP).
			Context("operation", "circuit_breaker_open").
			Context("url", privacy.SanitizeRTSPUrl(s.url)).
			Context("transport", s.transport).
			Context("consecutive_failures", s.consecutiveFailures).
			Context("runtime_seconds", runtime.Seconds()).
			Context("reason", reason).
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

// conditionalFailureReset resets failures only after the process has proven
// stable operation with substantial data reception
func (s *FFmpegStream) conditionalFailureReset(totalBytesReceived int64) {
	// Get process start time safely
	s.cmdMu.Lock()
	processStartTime := s.processStartTime
	s.cmdMu.Unlock()
	
	// Only proceed if we have a running process
	if processStartTime.IsZero() {
		return
	}
	
	// Define minimum stability requirements
	const minStabilityTime = 30 * time.Second
	const minResetBytes = 100 * 1024 // 100KB
	
	// Check if process has been stable long enough and received sufficient data
	if time.Since(processStartTime) >= minStabilityTime && totalBytesReceived >= minResetBytes {
		s.circuitMu.Lock()
		if s.consecutiveFailures > 0 {
			previousFailures := s.consecutiveFailures
			s.consecutiveFailures = 0
			s.circuitMu.Unlock()
			
			streamLogger.Info("resetting failure count after successful stable operation",
				"url", privacy.SanitizeRTSPUrl(s.url),
				"runtime_seconds", time.Since(processStartTime).Seconds(),
				"total_bytes", totalBytesReceived,
				"previous_failures", previousFailures,
				"min_stability_seconds", minStabilityTime.Seconds(),
				"min_reset_bytes", minResetBytes,
				"component", "ffmpeg-stream")
			
			// Report failure reset to telemetry
			errorWithContext := errors.Newf("RTSP stream failures reset after stable operation").
				Component("ffmpeg-stream").
				Category(errors.CategoryRTSP).
				Priority(errors.PriorityLow).
				Context("operation", "failure_reset").
				Context("url", privacy.SanitizeRTSPUrl(s.url)).
				Context("runtime_seconds", time.Since(processStartTime).Seconds()).
				Context("total_bytes", totalBytesReceived).
				Context("previous_failures", previousFailures).
				Context("min_stability_seconds", minStabilityTime.Seconds()).
				Context("min_reset_bytes", minResetBytes).
				Build()
			// This will be reported via event bus if telemetry is enabled
			_ = errorWithContext
		} else {
			s.circuitMu.Unlock()
		}
	}
}
