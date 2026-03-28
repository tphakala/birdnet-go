package ffmpeg

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// Constants for FFmpeg stream management.
const (
	// Buffer size for reading FFmpeg stdout.
	ffmpegBufferSize = 32768

	// Health check intervals and timeouts.
	healthCheckInterval  = 5 * time.Second
	silenceTimeout       = 90 * time.Second
	silenceCheckInterval = 10 * time.Second

	// Data rate calculation settings.
	dataRateWindowSize = 10 * time.Second
	dataRateMaxSamples = 100

	// Process management timeouts.
	processCleanupTimeout = 5 * time.Second
	processQuickExitTime  = 5 * time.Second

	// Backoff settings.
	defaultBackoffDuration = 5 * time.Second
	maxBackoffDuration     = 2 * time.Minute

	// Health check thresholds (defaults, can be overridden by StreamConfig).
	defaultHealthyDataThreshold   = 60 * time.Second
	defaultReceivingDataThreshold = 5 * time.Second
	defaultGracePeriod            = 30 * time.Second

	// Circuit breaker settings.
	circuitBreakerThreshold = 10
	circuitBreakerCooldown  = 30 * time.Second

	// Circuit breaker graduated failure thresholds.
	circuitBreakerImmediateThreshold = 3
	circuitBreakerRapidThreshold     = 5
	circuitBreakerQuickThreshold     = 8

	// Circuit breaker runtime thresholds.
	circuitBreakerImmediateRuntime = 1 * time.Second
	circuitBreakerRapidRuntime     = 5 * time.Second
	circuitBreakerQuickRuntime     = 30 * time.Second

	// Circuit breaker stability requirements.
	circuitBreakerMinStabilityTime  = 30 * time.Second
	circuitBreakerMinStabilityBytes = 100 * 1024

	// Drop logging settings.
	dropLogInterval = 30 * time.Second

	// Maximum safe exponent for bit shift to prevent overflow.
	maxBackoffExponent = 20

	// Restart jitter to prevent thundering herd effect.
	restartJitterPercentMax = 20

	// Timeout settings for FFmpeg streams.
	defaultTimeoutMicroseconds = 10000000
	minTimeoutMicroseconds     = 1000000

	// FFmpeg error tracking settings.
	maxErrorHistorySize       = 100
	maxErrorHistoryExposed    = 10
	earlyErrorDetectionWindow = 5 * time.Second

	// Maximum number of state transitions to keep in history.
	maxStateHistory = 100
)

// getStreamLogger returns the logger for FFmpeg stream operations.
func getStreamLogger() logger.Logger {
	return audiocore.GetLogger()
}

// ProcessState represents the current lifecycle state of an FFmpeg process.
type ProcessState int

const (
	// StateIdle indicates the stream is created but Run() has not been called yet.
	StateIdle ProcessState = iota
	// StateStarting indicates the FFmpeg process is being started.
	StateStarting
	// StateRunning indicates the FFmpeg process is running and processing audio.
	StateRunning
	// StateRestarting indicates a restart has been requested.
	StateRestarting
	// StateBackoff indicates the stream is waiting before restart (exponential backoff).
	StateBackoff
	// StateCircuitOpen indicates the circuit breaker is open (waiting for cooldown).
	StateCircuitOpen
	// StateStopped indicates the stream has been permanently stopped.
	StateStopped
)

// String returns a human-readable name for the process state.
func (s ProcessState) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateRestarting:
		return "restarting"
	case StateBackoff:
		return "backoff"
	case StateCircuitOpen:
		return "circuit_open"
	case StateStopped:
		return "stopped"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

// StateTransition records a transition between process states for debugging.
type StateTransition struct {
	From      ProcessState
	To        ProcessState
	Timestamp time.Time
	Reason    string
}

// validStateTransitions defines the allowed state transitions for validation.
var validStateTransitions = map[ProcessState][]ProcessState{
	StateIdle:        {StateStarting, StateStopped},
	StateStarting:    {StateRunning, StateBackoff, StateCircuitOpen, StateStopped},
	StateRunning:     {StateRestarting, StateBackoff, StateCircuitOpen, StateStopped},
	StateRestarting:  {StateStarting, StateBackoff, StateCircuitOpen, StateStopped},
	StateBackoff:     {StateStarting, StateCircuitOpen, StateStopped},
	StateCircuitOpen: {StateStarting, StateStopped},
	StateStopped:     {},
}

// isValidTransition checks if a state transition is allowed.
func isValidTransition(from, to ProcessState) bool {
	if from == to {
		return true
	}

	allowedTransitions, exists := validStateTransitions[from]
	if !exists {
		return false
	}

	return slices.Contains(allowedTransitions, to)
}

// StreamConfig carries all configuration needed by a Stream.
// It replaces direct reads from conf.Setting().
type StreamConfig struct {
	// URL is the stream URL (e.g., rtsp://host/stream).
	URL string

	// SourceID is the unique identifier for this source.
	SourceID string

	// SourceName is the human-readable display name.
	SourceName string

	// Type is the source type (e.g., "rtsp", "http", "hls").
	Type string

	// SampleRate in Hz (e.g., 48000).
	SampleRate int

	// BitDepth in bits (e.g., 16).
	BitDepth int

	// Channels count (e.g., 1 for mono).
	Channels int

	// FFmpegPath is the absolute path to the FFmpeg binary.
	FFmpegPath string

	// Transport is the RTSP transport protocol ("tcp" or "udp").
	Transport string

	// LogLevel is the FFmpeg log level (e.g., "error").
	LogLevel string

	// FFmpegParameters are additional parameters passed to FFmpeg.
	FFmpegParameters []string

	// HealthyDataThreshold is how long without data before marking unhealthy.
	// Zero uses defaultHealthyDataThreshold.
	HealthyDataThreshold time.Duration

	// Debug enables verbose debug logging.
	Debug bool
}

// safeURL returns a privacy-sanitised version of the stream URL.
func (c *StreamConfig) safeURL() string {
	return privacy.SanitizeStreamUrl(c.URL)
}

// sourceType returns the audiocore.SourceType derived from the config Type string.
func (c *StreamConfig) sourceType() audiocore.SourceType {
	switch strings.ToLower(c.Type) {
	case "rtsp":
		return audiocore.SourceTypeRTSP
	case "http", "https":
		return audiocore.SourceTypeHTTP
	case "hls":
		return audiocore.SourceTypeHLS
	case "rtmp":
		return audiocore.SourceTypeRTMP
	case "udp":
		return audiocore.SourceTypeUDP
	default:
		return audiocore.SourceTypeUnknown
	}
}

// healthyThreshold returns the configured healthy data threshold or the default.
func (c *StreamConfig) healthyThreshold() time.Duration {
	if c.HealthyDataThreshold > 0 {
		return c.HealthyDataThreshold
	}
	return defaultHealthyDataThreshold
}

// StreamHealth represents the health status of an FFmpeg stream.
type StreamHealth struct {
	IsHealthy          bool
	LastDataReceived   time.Time
	RestartCount       int
	Error              error
	TotalBytesReceived int64
	BytesPerSecond     float64
	IsReceivingData    bool
	ProcessState       ProcessState
	StateHistory       []StateTransition
	LastErrorContext   *ErrorContext
	ErrorHistory       []*ErrorContext
}

// dataRateCalculator tracks data rate over a sliding window.
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

// newDataRateCalculator creates a new data rate calculator.
func newDataRateCalculator(windowSize time.Duration) *dataRateCalculator {
	return &dataRateCalculator{
		samples:    make([]dataSample, 0, dataRateMaxSamples),
		windowSize: windowSize,
		maxSamples: dataRateMaxSamples,
	}
}

// addSample adds a new data sample.
func (d *dataRateCalculator) addSample(numBytes int64) {
	d.samplesMu.Lock()
	defer d.samplesMu.Unlock()

	now := time.Now()
	d.samples = append(d.samples, dataSample{
		timestamp: now,
		bytes:     numBytes,
	})

	// Remove old samples outside the window.
	cutoff := now.Add(-d.windowSize)
	i := 0
	for i < len(d.samples) && d.samples[i].timestamp.Before(cutoff) {
		i++
	}
	if i > 0 {
		d.samples = d.samples[i:]
	}

	// Limit max samples.
	if len(d.samples) > d.maxSamples {
		d.samples = d.samples[len(d.samples)-d.maxSamples:]
	}
}

// getRate returns the current data rate in bytes per second.
func (d *dataRateCalculator) getRate() float64 {
	d.samplesMu.RLock()
	defer d.samplesMu.RUnlock()

	if len(d.samples) == 0 {
		return 0
	}

	if len(d.samples) == 1 {
		sample := d.samples[0]
		timeSinceSample := time.Since(sample.timestamp)
		if timeSinceSample < 5*time.Second {
			return float64(sample.bytes)
		}
		return 0
	}

	totalBytes := int64(0)
	for _, s := range d.samples {
		totalBytes += s.bytes
	}

	duration := d.samples[len(d.samples)-1].timestamp.Sub(d.samples[0].timestamp).Seconds()
	if duration <= 0 {
		return 0
	}

	return float64(totalBytes) / duration
}

// secondsSinceOrZero returns seconds since t, or 0 if t is zero.
func secondsSinceOrZero(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return time.Since(t).Seconds()
}

// formatLastDataDescription returns a human-readable description of when data was last received.
func formatLastDataDescription(t time.Time) string {
	if t.IsZero() {
		return "never received data"
	}
	return fmt.Sprintf("%.1fs ago", time.Since(t).Seconds())
}

// formatDuration formats a time.Duration into a human-readable string.
func formatDuration(d time.Duration) string {
	if d < 0 {
		return fmt.Sprintf("-%s", formatDuration(-d))
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Round(time.Millisecond).Milliseconds())
	}
	if d < time.Minute {
		rounded := int(d.Round(time.Second).Seconds())
		if rounded >= 60 {
			return "1m 0s"
		}
		return fmt.Sprintf("%ds", rounded)
	}
	if d < time.Hour {
		d = d.Round(time.Second)
		minutes := int(d.Minutes())
		seconds := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	d = d.Round(time.Second)
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60
	return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
}

// threadSafeWriter wraps a bytes.Buffer with mutex protection for concurrent access.
type threadSafeWriter struct {
	buf *bytes.Buffer
	mu  *sync.RWMutex
}

// Write implements io.Writer with thread-safe access.
func (w *threadSafeWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

// Stream manages a single FFmpeg process for audio streaming.
// It handles process lifecycle, health monitoring, data tracking, and automatic recovery.
// Instead of writing to global buffers, it invokes callbacks for each data frame.
type Stream struct {
	config StreamConfig

	// Callbacks.
	onFrame func(frame audiocore.AudioFrame)
	onReset func(sourceID string)

	// Optional metrics.
	metrics audiocore.StreamMetrics

	// Process management.
	cmd      *exec.Cmd
	cmdMu    sync.Mutex
	stdout   io.ReadCloser
	stderr   bytes.Buffer
	stderrMu sync.RWMutex

	// State management.
	ctx         context.Context
	cancel      context.CancelCauseFunc
	cancelMu    sync.RWMutex
	restartChan chan struct{}
	stopChan    chan struct{}
	stopOnce    sync.Once
	stopped     bool
	stoppedMu   sync.RWMutex

	// Health tracking.
	lastDataTime   time.Time
	lastDataMu     sync.RWMutex
	restartCount   int
	restartCountMu sync.Mutex

	// Concurrent restart protection.
	restartInProgress bool
	restartMu         sync.Mutex

	// Process lifecycle metrics.
	totalProcessCount   int64
	shortLivedProcesses int64
	processMetricsMu    sync.Mutex

	// Data tracking.
	totalBytesReceived int64
	bytesReceivedMu    sync.RWMutex
	dataRateCalc       *dataRateCalculator

	// Process timing.
	processStartTime time.Time

	// Backoff for restarts.
	backoffDuration time.Duration
	maxBackoff      time.Duration

	// Circuit breaker state.
	consecutiveFailures int
	circuitOpenTime     time.Time
	circuitMu           sync.Mutex

	// Dropped data tracking.
	lastDropLogTime time.Time
	dropLogMu       sync.Mutex

	// Stream creation time for grace period calculation.
	streamCreatedAt   time.Time
	streamCreatedAtMu sync.RWMutex

	// Process state tracking.
	processState     ProcessState
	processStateMu   sync.RWMutex
	stateTransitions []StateTransition
	transitionsMu    sync.Mutex

	// FFmpeg error tracking for diagnostics.
	errorContexts   []*ErrorContext
	errorContextsMu sync.RWMutex
	maxErrorHistory int

	// Process exit info captured from cmd.Wait() for diagnostics.
	exitExitCode     int
	exitProcessState string
	exitWaitCalled   bool
	exitInfoMu       sync.Mutex
}

// NewStream creates a new FFmpeg stream handler.
// The onFrame callback is invoked for each chunk of audio data read from FFmpeg
// with a fully populated AudioFrame including source metadata.
// The onReset callback is invoked when the stream restarts, allowing consumers to reset state.
func NewStream(cfg *StreamConfig, onFrame func(frame audiocore.AudioFrame), onReset func(sourceID string), metrics audiocore.StreamMetrics) *Stream {
	return &Stream{
		config:           *cfg,
		onFrame:          onFrame,
		onReset:          onReset,
		metrics:          metrics,
		restartChan:      make(chan struct{}, 1),
		stopChan:         make(chan struct{}),
		backoffDuration:  defaultBackoffDuration,
		maxBackoff:       maxBackoffDuration,
		lastDataTime:     time.Time{},
		dataRateCalc:     newDataRateCalculator(dataRateWindowSize),
		lastDropLogTime:  time.Now(),
		streamCreatedAt:  time.Now(),
		processState:     StateIdle,
		stateTransitions: make([]StateTransition, 0, maxStateHistory),
		errorContexts:    make([]*ErrorContext, 0, maxErrorHistorySize),
		maxErrorHistory:  maxErrorHistorySize,
	}
}

// transitionState safely transitions the process state and logs the change.
// Lenient mode: invalid transitions are logged in debug mode but still applied.
// Idempotent transitions (same state) are silently ignored.
func (s *Stream) transitionState(to ProcessState, reason string) {
	s.processStateMu.Lock()
	from := s.processState

	if from == to {
		s.processStateMu.Unlock()
		if s.config.Debug {
			getStreamLogger().Debug("idempotent state transition ignored",
				logger.String("url", s.config.safeURL()),
				logger.String("source_id", s.config.SourceID),
				logger.String("state", from.String()),
				logger.String("reason", reason),
				logger.String("component", "ffmpeg-stream"),
				logger.String("operation", "state_transition_noop"))
		}
		return
	}

	// Terminal state safeguard: never allow leaving StateStopped.
	if from == StateStopped && to != StateStopped {
		s.processStateMu.Unlock()
		getStreamLogger().Warn("blocked transition out of terminal state",
			logger.String("url", s.config.safeURL()),
			logger.String("source_id", s.config.SourceID),
			logger.String("from", from.String()),
			logger.String("to", to.String()),
			logger.String("reason", reason),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "state_transition_blocked"))
		return
	}

	if !isValidTransition(from, to) && s.config.Debug {
		getStreamLogger().Warn("invalid state transition detected (applying anyway for robustness)",
			logger.String("url", s.config.safeURL()),
			logger.String("source_id", s.config.SourceID),
			logger.String("from", from.String()),
			logger.String("to", to.String()),
			logger.String("reason", reason),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "state_transition_invalid"))
	}

	s.processState = to
	s.processStateMu.Unlock()

	transition := StateTransition{
		From:      from,
		To:        to,
		Timestamp: time.Now(),
		Reason:    reason,
	}

	s.transitionsMu.Lock()
	s.stateTransitions = append(s.stateTransitions, transition)
	if len(s.stateTransitions) > maxStateHistory {
		s.stateTransitions = s.stateTransitions[len(s.stateTransitions)-maxStateHistory:]
	}
	s.transitionsMu.Unlock()

	getStreamLogger().Info("process state transition",
		logger.String("url", s.config.safeURL()),
		logger.String("source_id", s.config.SourceID),
		logger.String("from", from.String()),
		logger.String("to", to.String()),
		logger.String("reason", reason),
		logger.String("component", "ffmpeg-stream"),
		logger.String("operation", "state_transition"))
}

// GetProcessState returns the current process state (thread-safe).
func (s *Stream) GetProcessState() ProcessState {
	s.processStateMu.RLock()
	defer s.processStateMu.RUnlock()
	return s.processState
}

// GetStateHistory returns recent state transitions for debugging (thread-safe).
func (s *Stream) GetStateHistory() []StateTransition {
	s.transitionsMu.Lock()
	defer s.transitionsMu.Unlock()
	return slices.Clone(s.stateTransitions)
}

// Run starts and manages the FFmpeg process lifecycle.
// It runs in a loop, automatically restarting the process on failures with exponential backoff.
// The function returns when the context is cancelled or Stop() is called.
func (s *Stream) Run(parentCtx context.Context) {
	func() {
		s.cancelMu.Lock()
		defer s.cancelMu.Unlock()
		s.ctx, s.cancel = context.WithCancelCause(parentCtx)
	}()

	defer func() {
		s.cancelMu.Lock()
		defer s.cancelMu.Unlock()
		if s.cancel != nil {
			s.cancel(fmt.Errorf("Stream: Run() loop exiting for %s", s.config.safeURL()))
		}
	}()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.stopChan:
			return
		default:
			// Check circuit breaker and wait only for remaining cooldown.
			if remaining, open := s.circuitCooldownRemaining(); open {
				s.transitionState(StateCircuitOpen, fmt.Sprintf("circuit breaker cooldown: %s remaining", formatDuration(remaining)))
				select {
				case <-time.After(remaining):
					continue
				case <-s.ctx.Done():
					return
				case <-s.stopChan:
					return
				}
			}

			s.transitionState(StateStarting, "initiating FFmpeg process start")

			if err := s.startProcess(); err != nil {
				getStreamLogger().Error("failed to start FFmpeg process",
					logger.String("url", s.config.safeURL()),
					logger.Error(err),
					logger.String("component", "ffmpeg-stream"),
					logger.String("operation", "start_process"))
				s.recordFailure(0)
				currentState := s.GetProcessState()
				if currentState != StateCircuitOpen {
					s.handleRestartBackoff()
				}
				continue
			}

			s.transitionState(StateRunning, "FFmpeg process started successfully")

			// Capture processStartTime before processAudio runs, because
			// processAudio may call cleanupProcess (on restart/cancel) which
			// zeros processStartTime before we can compute runtime metrics.
			processStartTime := func() time.Time {
				s.cmdMu.Lock()
				defer s.cmdMu.Unlock()
				return s.processStartTime
			}()

			err := s.processAudio()

			s.stoppedMu.RLock()
			stopped := s.stopped
			s.stoppedMu.RUnlock()

			if stopped {
				return
			}

			runtime := time.Since(processStartTime)

			if err != nil && !errors.Is(err, context.Canceled) {
				s.recordFailure(runtime)
				errorMsg := err.Error()
				sanitizedError := privacy.SanitizeFFmpegError(errorMsg)
				isSilenceTimeout := strings.Contains(errorMsg, "silence timeout")

				getStreamLogger().Warn("FFmpeg process ended",
					logger.String("url", s.config.safeURL()),
					logger.String("error", sanitizedError),
					logger.Float64("runtime_seconds", runtime.Seconds()),
					logger.String("component", "ffmpeg-stream"),
					logger.String("operation", "process_ended"))

				if isSilenceTimeout {
					func() {
						s.restartCountMu.Lock()
						defer s.restartCountMu.Unlock()
						s.restartCount = 0
					}()
					func() {
						s.circuitMu.Lock()
						defer s.circuitMu.Unlock()
						if s.consecutiveFailures > 0 {
							s.consecutiveFailures--
						}
					}()
				}
			} else {
				getStreamLogger().Info("FFmpeg process ended normally",
					logger.String("url", s.config.safeURL()),
					logger.Float64("runtime_seconds", runtime.Seconds()),
					logger.String("component", "ffmpeg-stream"),
					logger.String("operation", "process_ended"))
				s.resetFailures()
			}

			s.cleanupProcess()

			// Notify consumers of stream restart.
			if s.onReset != nil {
				s.onReset(s.config.SourceID)
			}

			currentState := s.GetProcessState()
			if currentState != StateCircuitOpen {
				s.handleRestartBackoff()
			}
		}
	}
}

// startProcess starts the FFmpeg process.
func (s *Stream) startProcess() error {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()

	if err := ValidateFFmpegPath(s.config.FFmpegPath); err != nil {
		return errors.Newf("FFmpeg validation failed: %w", err).
			Category(errors.CategoryValidation).
			Component("ffmpeg-stream").
			Context("operation", "start_process").
			Context("ffmpeg_path", s.config.FFmpegPath).
			Build()
	}

	sampleRate, numChannels, format := GetFFmpegFormat(s.config.SampleRate, s.config.Channels, s.config.BitDepth)

	args := s.buildFFmpegInputArgs(s.config.FFmpegParameters)

	connStr := s.config.URL
	if connStr == "" {
		return errors.Newf("connection string is empty for source %s, cannot start FFmpeg", s.config.SourceID).
			Category(errors.CategoryValidation).
			Component("ffmpeg-stream").
			Context("operation", "start_process").
			Context("source_id", s.config.SourceID).
			Build()
	}

	logLevel := s.config.LogLevel
	if logLevel == "" {
		logLevel = "error"
	}

	args = append(args,
		"-i", connStr,
		"-loglevel", logLevel,
		"-vn",
		"-f", format,
		"-ar", sampleRate,
		"-ac", numChannels,
		"-hide_banner",
		"pipe:1",
	)

	s.cmd = exec.CommandContext(s.ctx, s.config.FFmpegPath, args...) //nolint:gosec // G204: FFmpegPath from validated settings, args built internally

	setupProcessGroup(s.cmd)

	func() {
		s.stderrMu.Lock()
		defer s.stderrMu.Unlock()
		s.stderr.Reset()
	}()
	s.cmd.Stderr = &threadSafeWriter{buf: &s.stderr, mu: &s.stderrMu}

	var err error
	s.stdout, err = s.cmd.StdoutPipe()
	if err != nil {
		return errors.Newf("failed to create stdout pipe: %w", err).
			Category(errors.CategorySystem).
			Component("ffmpeg-stream").
			Context("operation", "start_process").
			Context("url", s.config.safeURL()).
			Build()
	}

	if err := s.cmd.Start(); err != nil {
		return errors.Newf("failed to start FFmpeg: %w", err).
			Category(errors.CategorySystem).
			Component("ffmpeg-stream").
			Context("operation", "start_process").
			Context("url", s.config.safeURL()).
			Context("transport", s.config.Transport).
			Build()
	}

	s.processStartTime = time.Now()

	currentTotal := func() int64 {
		s.processMetricsMu.Lock()
		defer s.processMetricsMu.Unlock()
		s.totalProcessCount++
		return s.totalProcessCount
	}()

	getStreamLogger().Info("FFmpeg process started",
		logger.String("source_id", s.config.SourceID),
		logger.String("url", s.config.safeURL()),
		logger.Int("pid", s.cmd.Process.Pid),
		logger.String("transport", s.config.Transport),
		logger.Int64("total_process_count", currentTotal),
		logger.String("component", "ffmpeg-stream"),
		logger.String("operation", "start_process"))

	return nil
}

// buildFFmpegInputArgs constructs the FFmpeg input arguments for this stream.
// RTSP-specific flags like -rtsp_transport are only added for RTSP streams.
func (s *Stream) buildFFmpegInputArgs(ffmpegParameters []string) []string {
	args := []string{}

	if s.config.sourceType() == audiocore.SourceTypeRTSP {
		args = append(args, "-rtsp_transport", s.config.Transport)
	}

	hasUserTimeout, userTimeoutValue := detectUserTimeout(ffmpegParameters)

	if !hasUserTimeout {
		args = append(args, "-timeout", strconv.FormatInt(defaultTimeoutMicroseconds, 10))
	}

	if len(ffmpegParameters) > 0 {
		if hasUserTimeout {
			if err := s.validateUserTimeout(userTimeoutValue); err != nil {
				getStreamLogger().Warn("invalid user timeout, using default",
					logger.String("url", s.config.safeURL()),
					logger.String("user_timeout", userTimeoutValue),
					logger.Error(err),
					logger.String("component", "ffmpeg-stream"),
					logger.String("operation", "validate_timeout"))
				args = append(args, "-timeout", strconv.FormatInt(defaultTimeoutMicroseconds, 10))
				skipNext := false
				for _, param := range ffmpegParameters {
					if skipNext {
						skipNext = false
						continue
					}
					if param == "-timeout" {
						skipNext = true
						continue
					}
					args = append(args, param)
				}
			} else {
				args = append(args, ffmpegParameters...)
			}
		} else {
			args = append(args, ffmpegParameters...)
		}
	}

	return args
}

// readResult carries the outcome of a single stdout.Read call from the
// dedicated reader goroutine back to the processAudio event loop.
type readResult struct {
	data []byte
	err  error
}

// processAudio reads and processes audio data from FFmpeg.
// A dedicated reader goroutine performs blocking stdout.Read calls and
// posts results to a channel, allowing the main event loop to remain
// responsive to restart, stop, and context-cancellation signals even
// when FFmpeg becomes unresponsive.
func (s *Stream) processAudio() error {
	startTime := time.Now()

	silenceCheckTicker := time.NewTicker(silenceCheckInterval)
	defer silenceCheckTicker.Stop()

	healthCheckDone := false
	healthCheckTimer := time.NewTimer(healthCheckInterval)
	defer func() {
		if !healthCheckTimer.Stop() {
			select {
			case <-healthCheckTimer.C:
			default:
			}
		}
	}()

	earlyErrorCheckTicker := time.NewTicker(500 * time.Millisecond)
	defer earlyErrorCheckTicker.Stop()

	earlyErrorCheckEnabled := true
	earlyErrorCheckTimer := time.NewTimer(earlyErrorDetectionWindow)
	defer func() {
		if !earlyErrorCheckTimer.Stop() {
			select {
			case <-earlyErrorCheckTimer.C:
			default:
			}
		}
	}()

	s.resetDataTracking()

	// Grab the stdout pipe once; closing it (via cleanupProcess) terminates the reader goroutine.
	s.cmdMu.Lock()
	stdout := s.stdout
	s.cmdMu.Unlock()

	if stdout == nil {
		// During intentional stop, cleanupProcess clears stdout before Run checks —
		// this is expected, not a warning condition.
		s.stoppedMu.RLock()
		stopped := s.stopped
		s.stoppedMu.RUnlock()
		if !stopped {
			getStreamLogger().Warn("stdout nil after successful process start, restarting",
				logger.String("source_id", s.config.SourceID))
		}
		return nil
	}

	// Channel through which the reader goroutine delivers read results.
	// Buffered by 1 so the reader doesn't block on send while the event
	// loop handles a control signal.
	readCh := make(chan readResult, 1)

	// readerDone is closed when the main event loop exits to unblock the
	// reader goroutine if readCh is full, preventing a goroutine leak.
	readerDone := make(chan struct{})
	defer close(readerDone)

	// Launch a dedicated reader goroutine. It exits when stdout is closed
	// (by cleanupProcess), on a read error, or when readerDone is closed.
	go s.readStdout(stdout, readCh, readerDone)

	for {
		if s.GetProcessState() == StateStopped {
			return nil
		}

		select {
		case result := <-readCh:
			if result.err != nil {
				return s.handleReadError(result.err, startTime)
			}
			s.dispatchAudioData(result.data)

		case <-s.restartChan:
			getStreamLogger().Info("restart requested",
				logger.String("url", s.config.safeURL()),
				logger.String("component", "ffmpeg-stream"),
				logger.String("operation", "restart_requested"))
			s.cleanupProcess()
			s.restartMu.Lock()
			s.restartInProgress = false
			s.restartMu.Unlock()
			return nil

		case <-s.ctx.Done():
			s.cleanupProcess()
			return s.ctx.Err()

		case <-healthCheckTimer.C:
			if !healthCheckDone {
				healthCheckDone = true
				s.logStreamHealth()
			}

		case <-earlyErrorCheckTimer.C:
			earlyErrorCheckEnabled = false
			earlyErrorCheckTicker.Stop()
			select {
			case <-earlyErrorCheckTicker.C:
			default:
			}

		case <-earlyErrorCheckTicker.C:
			if earlyErrorCheckEnabled {
				if err := s.handleEarlyErrorDetection(); err != nil {
					return err
				}
			}

		case <-silenceCheckTicker.C:
			if err := s.handleSilenceTimeout(startTime); err != nil {
				return err
			}
		}
	}
}

// readStdout is the body of the dedicated reader goroutine launched by
// processAudio. It allocates a single buffer and copies data before sending
// to avoid retaining references to a shared backing array. It exits when
// stdout is closed (by cleanupProcess), on a read error, or when readerDone
// is closed (main loop exited, preventing a goroutine leak).
func (s *Stream) readStdout(stdout io.ReadCloser, readCh chan<- readResult, readerDone <-chan struct{}) {
	buf := make([]byte, ffmpegBufferSize)
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			dataCopy := make([]byte, n)
			copy(dataCopy, buf[:n])
			select {
			case readCh <- readResult{data: dataCopy}:
			case <-readerDone:
				return
			}
		}
		if err != nil {
			select {
			case readCh <- readResult{err: err}:
			case <-readerDone:
			}
			return
		}
	}
}

// handleReadError processes an error returned by the reader goroutine.
// It distinguishes quick-exit scenarios from normal EOF/cancel and general errors.
func (s *Stream) handleReadError(readErr error, startTime time.Time) error {
	if time.Since(startTime) < processQuickExitTime {
		return s.handleQuickExitError(startTime)
	}

	if errors.Is(readErr, io.EOF) || errors.Is(readErr, context.Canceled) {
		return nil
	}

	return errors.Newf("error reading from FFmpeg: %w", readErr).
		Category(errors.CategoryRTSP).
		Component("ffmpeg-stream").
		Context("operation", "process_audio").
		Context("url", s.config.safeURL()).
		Context("runtime_seconds", time.Since(startTime).Seconds()).
		Build()
}

// dispatchAudioData processes a chunk of audio data received from the reader
// goroutine: updates tracking state, resets failure counters if stable, and
// invokes the onFrame callback.
func (s *Stream) dispatchAudioData(data []byte) {
	if len(data) == 0 {
		return
	}

	s.updateLastDataTime()

	n := len(data)
	s.bytesReceivedMu.Lock()
	s.totalBytesReceived += int64(n)
	totalReceived := s.totalBytesReceived
	s.bytesReceivedMu.Unlock()

	s.dataRateCalc.addSample(int64(n))

	s.conditionalFailureReset(totalReceived)

	// Invoke onFrame callback with a fully populated AudioFrame.
	if s.onFrame != nil {
		s.onFrame(audiocore.AudioFrame{
			SourceID:   s.config.SourceID,
			SourceName: s.config.SourceName,
			Data:       data,
			SampleRate: s.config.SampleRate,
			BitDepth:   s.config.BitDepth,
			Channels:   s.config.Channels,
			Timestamp:  time.Now(),
		})
	}

	// Update metrics if available.
	if s.metrics != nil {
		s.metrics.RecordDataRate(s.config.SourceID, s.dataRateCalc.getRate())
	}
}

// handleSilenceTimeout checks if stream has stopped producing data and triggers restart.
func (s *Stream) handleSilenceTimeout(startTime time.Time) error {
	s.lastDataMu.RLock()
	lastData := s.lastDataTime
	s.lastDataMu.RUnlock()

	effectiveAge := func() time.Duration {
		if lastData.IsZero() {
			s.cmdMu.Lock()
			ps := s.processStartTime
			s.cmdMu.Unlock()
			if ps.IsZero() {
				return 0
			}
			return time.Since(ps)
		}
		return time.Since(lastData)
	}()

	if effectiveAge > 0 && effectiveAge > silenceTimeout {
		lastDataDesc := formatLastDataDescription(lastData)

		getStreamLogger().Warn("no data received from stream source, triggering restart",
			logger.String("url", s.config.safeURL()),
			logger.Float64("timeout_seconds", silenceTimeout.Seconds()),
			logger.String("last_data", lastDataDesc),
			logger.Float64("effective_age_seconds", effectiveAge.Seconds()),
			logger.Float64("process_runtime_seconds", time.Since(startTime).Seconds()),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "silence_detected"))
		s.cleanupProcess()
		return errors.Newf("stream stopped producing data for %v seconds", silenceTimeout.Seconds()).
			Category(errors.CategoryRTSP).
			Component("ffmpeg-stream").
			Context("operation", "silence_timeout").
			Context("url", s.config.safeURL()).
			Context("timeout_seconds", silenceTimeout.Seconds()).
			Context("last_data", lastDataDesc).
			Build()
	}

	return nil
}

// handleEarlyErrorDetection checks stderr for early errors and takes appropriate action.
func (s *Stream) handleEarlyErrorDetection() error {
	errCtx := s.checkEarlyErrors()
	if errCtx == nil {
		return nil
	}

	s.recordErrorContext(errCtx)

	if errCtx.ShouldOpenCircuit() {
		getStreamLogger().Error("early error triggers circuit breaker",
			logger.String("url", s.config.safeURL()),
			logger.String("source_id", s.config.SourceID),
			logger.String("error_type", errCtx.ErrorType),
			logger.String("primary_message", errCtx.PrimaryMessage),
			logger.String("user_message", errCtx.UserFacingMsg),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "early_error_circuit_break"))

		s.circuitMu.Lock()
		s.consecutiveFailures = circuitBreakerThreshold
		s.circuitOpenTime = time.Now()
		s.circuitMu.Unlock()

		s.transitionState(StateCircuitOpen, fmt.Sprintf("early FFmpeg error: %s", errCtx.ErrorType))
		s.cleanupProcess()
		return errors.Newf("early FFmpeg error: %s", errCtx.PrimaryMessage).
			Category(errors.CategoryRTSP).
			Component("ffmpeg-stream").
			Context("operation", "early_error_detection").
			Context("url", s.config.safeURL()).
			Context("error_type", errCtx.ErrorType).
			Build()
	}

	if errCtx.ShouldRestart() {
		getStreamLogger().Warn("early error triggers restart",
			logger.String("url", s.config.safeURL()),
			logger.String("source_id", s.config.SourceID),
			logger.String("error_type", errCtx.ErrorType),
			logger.String("primary_message", errCtx.PrimaryMessage),
			logger.String("user_message", errCtx.UserFacingMsg),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "early_error_restart"))

		s.cleanupProcess()
		return errors.Newf("early FFmpeg error (transient): %s", errCtx.PrimaryMessage).
			Category(errors.CategoryRTSP).
			Component("ffmpeg-stream").
			Context("operation", "early_error_detection").
			Context("url", s.config.safeURL()).
			Context("error_type", errCtx.ErrorType).
			Build()
	}

	return nil
}

// handleQuickExitError processes quick exit scenarios.
func (s *Stream) handleQuickExitError(startTime time.Time) error {
	exitCode := -1
	processState := "unavailable"

	s.cmdMu.Lock()
	cmd := s.cmd
	if cmd != nil {
		s.exitInfoMu.Lock()
		s.exitWaitCalled = true
		s.exitInfoMu.Unlock()
	}
	s.cmdMu.Unlock()

	if cmd != nil {
		waitDone := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			if cmd.ProcessState != nil {
				s.exitInfoMu.Lock()
				s.exitExitCode = cmd.ProcessState.ExitCode()
				s.exitProcessState = cmd.ProcessState.String()
				s.exitInfoMu.Unlock()
			}
			close(waitDone)
		}()

		select {
		case <-waitDone:
			s.exitInfoMu.Lock()
			exitCode = s.exitExitCode
			processState = s.exitProcessState
			s.exitInfoMu.Unlock()
		case <-time.After(processQuickExitTime):
			// Timeout; background goroutine will eventually reap.
		}
	}

	s.stderrMu.RLock()
	stderrOutput := s.stderr.String()
	s.stderrMu.RUnlock()

	errCtx := ExtractErrorContext(stderrOutput)
	if errCtx != nil {
		s.recordErrorContext(errCtx)

		if errCtx.ShouldOpenCircuit() {
			s.circuitMu.Lock()
			s.consecutiveFailures = circuitBreakerThreshold
			s.circuitOpenTime = time.Now()
			s.circuitMu.Unlock()

			s.transitionState(StateCircuitOpen, fmt.Sprintf("early exit with error: %s", errCtx.ErrorType))
		}

		return errors.Newf("FFmpeg process failed to start properly: %s", errCtx.PrimaryMessage).
			Category(errors.CategoryRTSP).
			Component("ffmpeg-stream").
			Context("operation", "process_audio_quick_exit").
			Context("url", s.config.safeURL()).
			Context("transport", s.config.Transport).
			Context("exit_time_seconds", time.Since(startTime).Seconds()).
			Context("error_type", errCtx.ErrorType).
			Context("exit_code", exitCode).
			Context("process_state", processState).
			Build()
	}

	sanitizedOutput := privacy.SanitizeFFmpegError(stderrOutput)
	return errors.Newf("FFmpeg process failed to start properly: %s", sanitizedOutput).
		Category(errors.CategoryRTSP).
		Component("ffmpeg-stream").
		Context("operation", "process_audio").
		Context("url", s.config.safeURL()).
		Context("transport", s.config.Transport).
		Context("exit_time_seconds", time.Since(startTime).Seconds()).
		Context("error_detail", sanitizedOutput).
		Context("exit_code", exitCode).
		Context("process_state", processState).
		Build()
}

// logContextCause logs the context cancellation cause if available.
func (s *Stream) logContextCause(pid int) {
	s.cancelMu.RLock()
	ctx := s.ctx
	s.cancelMu.RUnlock()

	if ctx == nil {
		return
	}

	cause := context.Cause(ctx)
	if cause != nil && !errors.Is(cause, context.Canceled) {
		getStreamLogger().Debug("cleanup triggered by context cancellation",
			logger.String("url", s.config.safeURL()),
			logger.Int("pid", pid),
			logger.String("cause", cause.Error()),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "cleanup_process_cause"))
	}
}

// cleanupProcess cleans up the FFmpeg process.
func (s *Stream) cleanupProcess() {
	s.cmdMu.Lock()
	cmd := s.cmd
	stdout := s.stdout
	pid := 0
	if cmd != nil && cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	s.cmd = nil
	s.stdout = nil
	s.processStartTime = time.Time{}
	s.cmdMu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return
	}

	s.logContextCause(pid)

	if stdout != nil {
		_ = stdout.Close()
	}

	if err := killProcessGroup(cmd); err != nil {
		if killErr := cmd.Process.Kill(); killErr != nil {
			getStreamLogger().Warn("failed to kill process directly",
				logger.String("url", s.config.safeURL()),
				logger.Int("pid", pid),
				logger.Error(killErr),
				logger.String("group_kill_error", err.Error()),
				logger.String("component", "ffmpeg-stream"),
				logger.String("operation", "cleanup_process_kill_direct"))
		}
	}

	s.exitInfoMu.Lock()
	alreadyWaited := s.exitWaitCalled
	s.exitInfoMu.Unlock()

	if !alreadyWaited {
		waitDone := make(chan error, 1)
		go func() {
			waitErr := cmd.Wait()
			select {
			case waitDone <- waitErr:
			default:
			}
		}()

		select {
		case err := <-waitDone:
			if err != nil && !strings.Contains(err.Error(), "signal: killed") && !strings.Contains(err.Error(), "signal: terminated") {
				getStreamLogger().Warn("FFmpeg process wait error",
					logger.String("url", s.config.safeURL()),
					logger.Error(err),
					logger.Int("pid", pid),
					logger.String("component", "ffmpeg-stream"),
					logger.String("operation", "process_wait"))
			}
		case <-time.After(processCleanupTimeout):
			getStreamLogger().Warn("FFmpeg process cleanup timeout - process will be reaped asynchronously",
				logger.String("url", s.config.safeURL()),
				logger.Int("pid", pid),
				logger.Float64("timeout_seconds", processCleanupTimeout.Seconds()),
				logger.String("component", "ffmpeg-stream"),
				logger.String("operation", "cleanup_process_timeout"))
		}
	}

	getStreamLogger().Info("FFmpeg process stopped",
		logger.String("url", s.config.safeURL()),
		logger.Int("pid", pid),
		logger.String("component", "ffmpeg-stream"),
		logger.String("operation", "cleanup_process"))

	s.exitInfoMu.Lock()
	s.exitWaitCalled = false
	s.exitExitCode = -1
	s.exitProcessState = ""
	s.exitInfoMu.Unlock()
}

// handleRestartBackoff handles exponential backoff for restarts.
func (s *Stream) handleRestartBackoff() {
	s.restartCountMu.Lock()
	s.restartCount++
	currentRestartCount := s.restartCount

	exponent := min(s.restartCount-1, maxBackoffExponent)
	backoff := min(s.backoffDuration*time.Duration(1<<uint(exponent)), s.maxBackoff) //nolint:gosec // G115: exponent is capped by maxBackoffExponent

	if currentRestartCount > 50 {
		additionalDelay := min(time.Duration(currentRestartCount-50)*10*time.Second, 5*time.Minute)
		backoff += additionalDelay
	}
	s.restartCountMu.Unlock()

	wait := backoff
	if backoff > 0 {
		factor := float64(restartJitterPercentMax) / 100.0
		jitterRange := time.Duration(float64(backoff) * factor)
		if jitterRange > 0 {
			if n, err := rand.Int(rand.Reader, big.NewInt(jitterRange.Nanoseconds())); err == nil {
				wait = backoff + time.Duration(n.Int64())
			}
		}
	}

	s.transitionState(StateBackoff, fmt.Sprintf("restart #%d: waiting %s (base backoff: %s)", currentRestartCount, formatDuration(wait), formatDuration(backoff)))

	getStreamLogger().Info("waiting before restart attempt",
		logger.String("url", s.config.safeURL()),
		logger.Float64("wait_seconds", wait.Seconds()),
		logger.Float64("backoff_seconds", backoff.Seconds()),
		logger.Int("restart_count", currentRestartCount),
		logger.String("component", "ffmpeg-stream"),
		logger.String("operation", "restart_wait"))

	select {
	case <-time.After(wait):
		// Continue with restart.
	case <-s.ctx.Done():
		// Context cancelled.
	case <-s.stopChan:
		// Stop requested.
	}
}

// Stop gracefully stops the FFmpeg stream.
// This method is idempotent - multiple calls are safe.
func (s *Stream) Stop() {
	s.stopOnce.Do(func() {
		s.transitionState(StateStopped, "Stop() called")

		s.stoppedMu.Lock()
		s.stopped = true
		s.stoppedMu.Unlock()

		close(s.stopChan)

		s.cancelMu.RLock()
		cancel := s.cancel
		s.cancelMu.RUnlock()

		if cancel != nil {
			cancel(fmt.Errorf("Stream: Stop() called for %s", s.config.safeURL()))
		}

		s.cleanupProcess()
	})
}

// Restart requests a stream restart.
// If manual is true, resets the restart count and consecutive failure counter
// (operator-initiated restart), preventing accumulated backoff from delaying
// the next attempt unnecessarily.
func (s *Stream) Restart(manual bool) {
	s.restartMu.Lock()
	if s.restartInProgress {
		s.restartMu.Unlock()
		return
	}
	s.restartInProgress = true
	s.restartMu.Unlock()

	restartType := "automatic"
	if manual {
		restartType = "manual"
	}
	s.transitionState(StateRestarting, fmt.Sprintf("%s restart requested", restartType))

	if manual {
		s.restartCountMu.Lock()
		s.restartCount = 0
		s.restartCountMu.Unlock()

		s.circuitMu.Lock()
		s.consecutiveFailures = 0
		s.circuitOpenTime = time.Time{}
		s.circuitMu.Unlock()
	}

	select {
	case s.restartChan <- struct{}{}:
		// Signal sent.
	default:
		s.restartMu.Lock()
		s.restartInProgress = false
		s.restartMu.Unlock()
	}
}

// IsRestarting checks if the stream is currently in the process of restarting.
func (s *Stream) IsRestarting() bool {
	state := s.GetProcessState()
	return state == StateRestarting ||
		state == StateBackoff ||
		state == StateCircuitOpen ||
		state == StateStarting
}

// GetProcessStartTime returns the start time of the current FFmpeg process.
func (s *Stream) GetProcessStartTime() time.Time {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()

	if s.cmd != nil && s.cmd.Process != nil && s.cmd.ProcessState == nil {
		return s.processStartTime
	}
	return time.Time{}
}

// GetHealth returns the current health status of the stream.
func (s *Stream) GetHealth() StreamHealth {
	s.lastDataMu.RLock()
	lastData := s.lastDataTime
	s.lastDataMu.RUnlock()

	s.restartCountMu.Lock()
	restarts := s.restartCount
	s.restartCountMu.Unlock()

	s.bytesReceivedMu.RLock()
	totalBytes := s.totalBytesReceived
	s.bytesReceivedMu.RUnlock()

	dataRate := s.dataRateCalc.getRate()

	healthyDataThreshold := s.config.healthyThreshold()
	const maxHealthyDataThreshold = 30 * time.Minute
	if healthyDataThreshold <= 0 || healthyDataThreshold > maxHealthyDataThreshold {
		healthyDataThreshold = defaultHealthyDataThreshold
	}

	var isHealthy, isReceivingData bool
	if lastData.IsZero() {
		// No data ever received: unhealthy regardless of grace period.
		// (Grace period only suppresses external alarms, not the health flag.)
		isHealthy = false
		isReceivingData = false
	} else {
		isHealthy = time.Since(lastData) < healthyDataThreshold
		isReceivingData = time.Since(lastData) < defaultReceivingDataThreshold
	}

	// Update metrics if available.
	if s.metrics != nil {
		s.metrics.SetStreamHealth(s.config.SourceID, isHealthy)
	}

	state := s.GetProcessState()

	allHistory := s.GetStateHistory()
	var recentHistory []StateTransition
	if len(allHistory) > 10 {
		recentHistory = allHistory[len(allHistory)-10:]
	} else {
		recentHistory = allHistory
	}

	allErrors := s.getErrorContexts()
	var recentErrors []*ErrorContext
	if len(allErrors) > maxErrorHistoryExposed {
		recentErrors = allErrors[len(allErrors)-maxErrorHistoryExposed:]
	} else {
		recentErrors = allErrors
	}

	lastError := s.getLastErrorContext()

	return StreamHealth{
		IsHealthy:          isHealthy,
		LastDataReceived:   lastData,
		RestartCount:       restarts,
		TotalBytesReceived: totalBytes,
		BytesPerSecond:     dataRate,
		IsReceivingData:    isReceivingData,
		ProcessState:       state,
		StateHistory:       recentHistory,
		LastErrorContext:   lastError,
		ErrorHistory:       recentErrors,
	}
}

// updateLastDataTime updates the last data received timestamp.
func (s *Stream) updateLastDataTime() {
	s.lastDataMu.Lock()
	s.lastDataTime = time.Now()
	s.lastDataMu.Unlock()
}

// resetDataTracking resets all data tracking state for a new process.
// It also refreshes streamCreatedAt so that health checks and watchdog
// calculations reference the current process lifetime, not the original
// stream creation time.
func (s *Stream) resetDataTracking() {
	s.lastDataMu.Lock()
	s.lastDataTime = time.Time{}
	s.lastDataMu.Unlock()

	s.bytesReceivedMu.Lock()
	s.totalBytesReceived = 0
	s.bytesReceivedMu.Unlock()

	s.streamCreatedAtMu.Lock()
	s.streamCreatedAt = time.Now()
	s.streamCreatedAtMu.Unlock()
}

// logStreamHealth logs the current stream health status.
func (s *Stream) logStreamHealth() {
	health := s.GetHealth()

	s.bytesReceivedMu.RLock()
	totalBytes := s.totalBytesReceived
	s.bytesReceivedMu.RUnlock()

	dataRate := health.BytesPerSecond

	if health.IsReceivingData {
		getStreamLogger().Info("stream health check - receiving data",
			logger.String("url", s.config.safeURL()),
			logger.Bool("is_healthy", health.IsHealthy),
			logger.Bool("is_receiving_data", health.IsReceivingData),
			logger.Int64("total_bytes_received", totalBytes),
			logger.Float64("bytes_per_second", dataRate),
			logger.Float64("last_data_ago_seconds", secondsSinceOrZero(health.LastDataReceived)),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "health_check"))
	} else {
		getStreamLogger().Warn("stream health check - no data received",
			logger.String("url", s.config.safeURL()),
			logger.Bool("is_healthy", health.IsHealthy),
			logger.Bool("is_receiving_data", health.IsReceivingData),
			logger.Int64("total_bytes_received", totalBytes),
			logger.Float64("last_data_ago_seconds", secondsSinceOrZero(health.LastDataReceived)),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "health_check"))
	}
}

// circuitCooldownRemaining returns (remaining, true) if the circuit is open, or (0, false) otherwise.
// When the cooldown has expired it resets the failure counter and circuit open time so the next
// retry starts with a clean slate.
func (s *Stream) circuitCooldownRemaining() (time.Duration, bool) {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()

	if s.circuitOpenTime.IsZero() {
		return 0, false
	}

	elapsed := time.Since(s.circuitOpenTime)
	if elapsed >= circuitBreakerCooldown {
		// Reset failure state so the retry begins with a clean backoff.
		s.consecutiveFailures = 0
		s.circuitOpenTime = time.Time{}
		return 0, false
	}

	return circuitBreakerCooldown - elapsed, true
}

// isCircuitOpen returns true if the circuit breaker is currently open.
func (s *Stream) isCircuitOpen() bool {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()

	if !s.circuitOpenTime.IsZero() && time.Since(s.circuitOpenTime) < circuitBreakerCooldown {
		return true
	}

	if !s.circuitOpenTime.IsZero() && time.Since(s.circuitOpenTime) >= circuitBreakerCooldown {
		s.consecutiveFailures = 0
		s.circuitOpenTime = time.Time{}
		getStreamLogger().Info("circuit breaker closed after cooldown",
			logger.String("url", s.config.safeURL()),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "circuit_breaker_close"))
	}

	return false
}

// recordFailure records a failure for the circuit breaker with runtime consideration.
// Graduated threshold system opens the circuit breaker earlier for rapid failures.
func (s *Stream) recordFailure(runtime time.Duration) {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()

	s.consecutiveFailures++

	if runtime < 5*time.Second {
		s.processMetricsMu.Lock()
		s.shortLivedProcesses++
		s.processMetricsMu.Unlock()
	}

	if s.metrics != nil {
		s.metrics.IncStreamErrors(s.config.SourceID)
	}

	shouldOpenCircuit := false
	var reason string

	switch {
	case runtime < circuitBreakerImmediateRuntime && s.consecutiveFailures >= circuitBreakerImmediateThreshold:
		shouldOpenCircuit = true
		reason = "immediate connection failures"
	case runtime < circuitBreakerRapidRuntime && s.consecutiveFailures >= circuitBreakerRapidThreshold:
		shouldOpenCircuit = true
		reason = "rapid process failures"
	case runtime < circuitBreakerQuickRuntime && s.consecutiveFailures >= circuitBreakerQuickThreshold:
		shouldOpenCircuit = true
		reason = "quick process failures"
	case s.consecutiveFailures >= circuitBreakerThreshold:
		shouldOpenCircuit = true
		reason = "consecutive failure threshold"
	}

	if shouldOpenCircuit {
		currentFailures := s.consecutiveFailures
		s.circuitOpenTime = time.Now()
		s.circuitMu.Unlock()

		s.transitionState(StateCircuitOpen, fmt.Sprintf("circuit breaker opened: %s (failures: %d, runtime: %s)", reason, currentFailures, formatDuration(runtime)))

		s.circuitMu.Lock()

		getStreamLogger().Error("circuit breaker opened",
			logger.String("url", s.config.safeURL()),
			logger.Int("consecutive_failures", currentFailures),
			logger.String("runtime", formatDuration(runtime)),
			logger.String("reason", reason),
			logger.String("cooldown_period", formatDuration(circuitBreakerCooldown)),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "circuit_breaker_open"))

		// Report circuit breaker activation to Sentry for visibility into
		// streams that are entering a degraded state.
		_ = errors.Newf("stream circuit breaker activated: %s", reason).
			Component("ffmpeg-stream").
			Category(errors.CategoryRTSP).
			Context("operation", "circuit_breaker").
			Context("source_id", s.config.SourceID).
			Context("consecutive_failures", currentFailures).
			Context("runtime", formatDuration(runtime)).
			Build()
	}
}

// resetFailures resets the failure count.
func (s *Stream) resetFailures() {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()

	if s.consecutiveFailures > 0 {
		getStreamLogger().Info("resetting failure count after successful run",
			logger.String("url", s.config.safeURL()),
			logger.Int("previous_failures", s.consecutiveFailures),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "reset_failures"))
		s.consecutiveFailures = 0
	}
}

// conditionalFailureReset resets failures only after the process has proven
// stable operation with substantial data reception.
func (s *Stream) conditionalFailureReset(totalBytesReceived int64) {
	s.cmdMu.Lock()
	processStartTime := s.processStartTime
	if processStartTime.IsZero() {
		s.cmdMu.Unlock()
		return
	}
	timeSinceStart := time.Since(processStartTime)
	s.cmdMu.Unlock()

	if timeSinceStart >= circuitBreakerMinStabilityTime && totalBytesReceived >= circuitBreakerMinStabilityBytes {
		s.circuitMu.Lock()
		if s.consecutiveFailures > 0 {
			previousFailures := s.consecutiveFailures
			s.consecutiveFailures = 0
			s.circuitMu.Unlock()

			getStreamLogger().Info("resetting failure count after successful stable operation",
				logger.String("url", s.config.safeURL()),
				logger.Float64("runtime_seconds", timeSinceStart.Seconds()),
				logger.Int64("total_bytes", totalBytesReceived),
				logger.Int("previous_failures", previousFailures),
				logger.String("component", "ffmpeg-stream"),
				logger.String("operation", "conditional_failure_reset"))
		} else {
			s.circuitMu.Unlock()
		}
	}
}

// detectUserTimeout scans FFmpeg parameters for an existing timeout setting.
func detectUserTimeout(params []string) (found bool, value string) {
	for i, param := range params {
		if param == "-timeout" && i+1 < len(params) {
			return true, params[i+1]
		}
	}
	return false, ""
}

// validateUserTimeout validates a user-provided timeout value.
func (s *Stream) validateUserTimeout(timeoutStr string) error {
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

// recordErrorContext stores an error context in the history buffer.
func (s *Stream) recordErrorContext(ctx *ErrorContext) {
	if ctx == nil {
		return
	}

	s.errorContextsMu.Lock()
	defer s.errorContextsMu.Unlock()

	s.errorContexts = append(s.errorContexts, ctx)
	if len(s.errorContexts) > s.maxErrorHistory {
		s.errorContexts = s.errorContexts[1:]
	}

	targetHost := ctx.TargetHost
	if strings.Contains(targetHost, "@") {
		if idx := strings.LastIndex(targetHost, "@"); idx != -1 {
			targetHost = targetHost[idx+1:]
		}
	}

	log := getStreamLogger()
	log.Error("FFmpeg error detected",
		logger.String("url", s.config.safeURL()),
		logger.String("source_id", s.config.SourceID),
		logger.String("error_type", ctx.ErrorType),
		logger.String("primary_message", ctx.PrimaryMessage),
		logger.String("user_message", ctx.UserFacingMsg),
		logger.String("target_host", targetHost),
		logger.Int("target_port", ctx.TargetPort),
		logger.Bool("should_open_circuit", ctx.ShouldOpenCircuit()),
		logger.Bool("should_restart", ctx.ShouldRestart()),
		logger.String("component", "ffmpeg-stream"),
		logger.String("operation", "error_detection"))

	// Log troubleshooting steps at Info so operators see them without debug mode.
	// Raw FFmpeg output is logged at Debug to avoid noise in production.
	if len(ctx.TroubleShooting) > 0 {
		log.Info("FFmpeg troubleshooting steps",
			logger.String("url", s.config.safeURL()),
			logger.String("source_id", s.config.SourceID),
			logger.String("error_type", ctx.ErrorType),
			logger.String("target_host", targetHost),
			logger.Int("target_port", ctx.TargetPort),
			logger.String("steps", strings.Join(ctx.TroubleShooting, "; ")),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "error_troubleshooting"))
	}
	if ctx.RawFFmpegOutput != "" {
		log.Debug("FFmpeg raw error output",
			logger.String("source_id", s.config.SourceID),
			logger.String("error_type", ctx.ErrorType),
			logger.String("target_host", targetHost),
			logger.Int("target_port", ctx.TargetPort),
			logger.String("ffmpeg_output", ctx.RawFFmpegOutput),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "error_raw_output"))
	}
}

// getErrorContexts returns a copy of the error history.
func (s *Stream) getErrorContexts() []*ErrorContext {
	s.errorContextsMu.RLock()
	defer s.errorContextsMu.RUnlock()

	result := make([]*ErrorContext, len(s.errorContexts))
	copy(result, s.errorContexts)
	return result
}

// getLastErrorContext returns the most recent error context, or nil if no errors.
func (s *Stream) getLastErrorContext() *ErrorContext {
	s.errorContextsMu.RLock()
	defer s.errorContextsMu.RUnlock()

	if len(s.errorContexts) == 0 {
		return nil
	}
	return s.errorContexts[len(s.errorContexts)-1]
}

// checkEarlyErrors checks FFmpeg stderr for errors during the early detection window.
func (s *Stream) checkEarlyErrors() *ErrorContext {
	s.stderrMu.RLock()
	stderrOutput := s.stderr.String()
	s.stderrMu.RUnlock()

	return ExtractErrorContext(stderrOutput)
}
