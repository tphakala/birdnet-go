package myaudio

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"maps"
	"math/big"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/alerting"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// Constants for FFmpeg stream management
const (
	// Buffer size for reading FFmpeg output
	ffmpegBufferSize = 32768

	// Health check intervals and timeouts
	healthCheckInterval  = 5 * time.Second
	silenceTimeout       = 90 * time.Second // Increased from 60s to prevent false triggers
	silenceCheckInterval = 10 * time.Second

	// Data rate calculation settings
	dataRateWindowSize = 10 * time.Second // Responsive window for real-time rate feedback
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
	circuitBreakerThreshold = 10               // Number of consecutive failures before opening circuit (standard threshold)
	circuitBreakerCooldown  = 30 * time.Second // Cooldown period when circuit is open

	// Circuit breaker graduated failure thresholds
	// These thresholds open the circuit earlier for rapid failures to prevent resource waste
	circuitBreakerImmediateThreshold = 3 // Opens after 3 failures for processes failing < 1 second
	circuitBreakerRapidThreshold     = 5 // Opens after 5 failures for processes failing < 5 seconds
	circuitBreakerQuickThreshold     = 8 // Opens after 8 failures for processes failing < 30 seconds

	// Circuit breaker runtime thresholds for graduated failure detection
	circuitBreakerImmediateRuntime = 1 * time.Second  // Runtime below which failures are considered "immediate"
	circuitBreakerRapidRuntime     = 5 * time.Second  // Runtime below which failures are considered "rapid"
	circuitBreakerQuickRuntime     = 30 * time.Second // Runtime below which failures are considered "quick"

	// Circuit breaker stability requirements for resetting failures
	// These ensure the stream is genuinely stable before clearing failure history
	circuitBreakerMinStabilityTime  = 30 * time.Second // Minimum process runtime before considering stable
	circuitBreakerMinStabilityBytes = 100 * 1024       // Minimum data received (100KB) before considering stable

	// Drop logging settings
	dropLogInterval = 30 * time.Second // Minimum interval between drop log messages

	// Maximum safe exponent for bit shift to prevent overflow
	maxBackoffExponent = 20 // This allows up to 2^20 = ~1 million multiplier

	// Restart jitter to prevent thundering herd effect
	restartJitterPercentMax = 20 // Maximum jitter percentage (0-20% random addition to backoff)

	// Timeout settings for FFmpeg streams
	defaultTimeoutMicroseconds = 10000000 // 10 seconds in microseconds
	minTimeoutMicroseconds     = 1000000  // 1 second in microseconds

	// FFmpeg error tracking settings
	maxErrorHistorySize       = 100             // Maximum number of error contexts to store internally per stream
	maxErrorHistoryExposed    = 10              // Number of most recent errors exposed via StreamHealth API
	earlyErrorDetectionWindow = 5 * time.Second // Check stderr in first 5 seconds for early detection
)

// getStreamLogger returns the logger for FFmpeg stream.
// Fetched dynamically to ensure it uses the current centralized logger.
func getStreamLogger() logger.Logger {
	return getIntegrationLogger()
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

// getRate returns the current data rate in bytes per second.
// It gracefully handles edge cases by returning 0 for cosmetic display.
func (d *dataRateCalculator) getRate() float64 {
	d.samplesMu.RLock()
	defer d.samplesMu.RUnlock()

	if len(d.samples) == 0 {
		// No data yet - return 0 for clean display
		return 0
	}

	if len(d.samples) == 1 {
		// Single sample case: When only one recent sample exists, return an
		// instantaneous/burst rate estimate rather than a true averaged rate.
		// We treat the sample size as if spread over 1 second minimum to give
		// a bytes-per-second estimate. This is NOT a multi-sample average but
		// rather an instantaneous rate estimate for display purposes.
		sample := d.samples[0]
		timeSinceSample := time.Since(sample.timestamp)

		// If sample is recent (within 5 seconds), show instantaneous rate
		// This helps new streams show data rate immediately
		if timeSinceSample < 5*time.Second {
			// Return the burst size as bytes/second (instantaneous estimate)
			return float64(sample.bytes)
		}

		// Old single sample - no meaningful rate
		return 0
	}

	// Multiple samples - calculate average rate
	totalBytes := int64(0)
	for _, s := range d.samples {
		totalBytes += s.bytes
	}

	duration := d.samples[len(d.samples)-1].timestamp.Sub(d.samples[0].timestamp).Seconds()
	if duration <= 0 {
		// All samples at same timestamp (shouldn't happen) - return 0
		return 0
	}

	return float64(totalBytes) / duration
}

// secondsSinceOrZero returns seconds since t, or 0 if t is zero.
// This prevents huge durations (time since Unix epoch) in logs when lastData is never set.
func secondsSinceOrZero(t time.Time) float64 {
	if t.IsZero() {
		return 0
	}
	return time.Since(t).Seconds()
}

// formatLastDataDescription returns a human-readable description of when data was last received.
// Returns "never received data" for zero time, or "X.Xs ago" for recent data.
// This is used for user-facing log messages and error contexts.
func formatLastDataDescription(t time.Time) string {
	if t.IsZero() {
		return "never received data"
	}
	return fmt.Sprintf("%.1fs ago", time.Since(t).Seconds())
}

// ProcessState represents the current lifecycle state of an FFmpeg process
type ProcessState int

const (
	// StateIdle indicates the stream is created but Run() has not been called yet
	StateIdle ProcessState = iota
	// StateStarting indicates the FFmpeg process is being started (in startProcess())
	StateStarting
	// StateRunning indicates the FFmpeg process is running and processing audio
	StateRunning
	// StateRestarting indicates a restart has been requested
	StateRestarting
	// StateBackoff indicates the stream is waiting before restart (exponential backoff)
	StateBackoff
	// StateCircuitOpen indicates the circuit breaker is open (waiting for cooldown)
	StateCircuitOpen
	// StateStopped indicates the stream has been permanently stopped
	StateStopped
)

// String returns a human-readable name for the process state
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

// StateTransition records a transition between process states for debugging
type StateTransition struct {
	From      ProcessState
	To        ProcessState
	Timestamp time.Time
	Reason    string
}

// validStateTransitions defines the allowed state transitions for validation.
// This prevents invalid state transitions and makes the state machine behavior explicit.
var validStateTransitions = map[ProcessState][]ProcessState{
	StateIdle:        {StateStarting, StateStopped},                                   // Can start or be stopped
	StateStarting:    {StateRunning, StateBackoff, StateCircuitOpen, StateStopped},    // Can succeed, fail, or be stopped
	StateRunning:     {StateRestarting, StateBackoff, StateCircuitOpen, StateStopped}, // Can restart, fail, or be stopped
	StateRestarting:  {StateStarting, StateBackoff, StateCircuitOpen, StateStopped},   // Can attempt start or enter waiting state
	StateBackoff:     {StateStarting, StateCircuitOpen, StateStopped},                 // Can retry, open circuit, or be stopped
	StateCircuitOpen: {StateStarting, StateStopped},                                   // Can retry after cooldown or be stopped
	StateStopped:     {},                                                              // Terminal state - no transitions allowed
}

// isValidTransition checks if a state transition is allowed
func isValidTransition(from, to ProcessState) bool {
	// Always allow transitions to the same state (idempotent)
	if from == to {
		return true
	}

	allowedTransitions, exists := validStateTransitions[from]
	if !exists {
		return false
	}

	return slices.Contains(allowedTransitions, to)
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
	// Process state information
	ProcessState ProcessState      // Current process state
	StateHistory []StateTransition // Recent state transitions (last 10 for health checks)
	// FFmpeg error diagnostics
	// Note: Internally stores up to 100 errors for analysis, but only exposes the 10 most recent
	LastErrorContext *ErrorContext   // Most recent error detected
	ErrorHistory     []*ErrorContext // Recent error history (last 10 for diagnostics)
}

// FFmpegStream manages a single FFmpeg process for audio streaming.
// It handles process lifecycle, health monitoring, data tracking, and automatic recovery.
type FFmpegStream struct {
	source    *AudioSource
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
	cancel      context.CancelCauseFunc // Changed to CancelCauseFunc for better diagnostics
	cancelMu    sync.RWMutex            // Protect cancel function access
	restartChan chan struct{}
	stopChan    chan struct{}
	stopOnce    sync.Once // Ensure Stop() is idempotent
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

	// Process state tracking
	processState     ProcessState
	processStateMu   sync.RWMutex
	stateTransitions []StateTransition
	transitionsMu    sync.Mutex

	// FFmpeg error tracking for diagnostics
	errorContexts   []*ErrorContext // Ring buffer of last N errors
	errorContextsMu sync.RWMutex
	maxErrorHistory int // Maximum number of error contexts to store
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

// generateUniqueFallbackID generates a unique fallback ID to prevent collisions
func generateUniqueFallbackID() string {
	// Use timestamp + random component for uniqueness
	timestamp := time.Now().Unix()

	// Generate random component
	randomNum, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		// Fallback to just timestamp if random fails
		return fmt.Sprintf("fallback_stream_%d", timestamp)
	}

	return fmt.Sprintf("fallback_stream_%d_%d", timestamp, randomNum.Int64())
}

// NewFFmpegStream creates a new FFmpeg stream handler.
// The url parameter specifies the stream URL, transport specifies the transport protocol (for RTSP),
// and audioChan is the channel where processed audio data will be sent.
func NewFFmpegStream(url, transport string, audioChan chan UnifiedAudioData) *FFmpegStream {
	// Register or get existing source from registry
	registry := GetRegistry()
	var source *AudioSource

	if registry == nil {
		getStreamLogger().Warn("registry not available during startup, creating fallback source",
			logger.String("url", privacy.SanitizeStreamUrl(url)),
			logger.String("operation", "new_ffmpeg_stream"))
		// Create fallback source when registry is unavailable
		// Auto-detect source type from URL
		detectedType := detectSourceTypeFromString(url)
		fallbackID := generateUniqueFallbackID()
		source = &AudioSource{
			ID:               fallbackID,
			DisplayName:      "Stream (Fallback)",
			Type:             detectedType,
			connectionString: url,
			SafeString:       privacy.SanitizeStreamUrl(url),
			RegisteredAt:     time.Now(),
			IsActive:         true,
		}
	} else {
		// Use SourceTypeUnknown to let auto-detection determine the correct type from URL
		source = registry.GetOrCreateSource(url, SourceTypeUnknown)
		if source == nil {
			getStreamLogger().Error("failed to register stream source",
				logger.String("url", privacy.SanitizeStreamUrl(url)),
				logger.String("operation", "new_ffmpeg_stream"))
			// Create a fallback source for robustness with unique ID
			// Auto-detect source type from URL
			detectedType := detectSourceTypeFromString(url)
			fallbackID := generateUniqueFallbackID()
			source = &AudioSource{
				ID:               fallbackID,
				DisplayName:      "Stream (Fallback)",
				Type:             detectedType,
				connectionString: url,
				SafeString:       privacy.SanitizeStreamUrl(url),
				RegisteredAt:     time.Now(),
				IsActive:         true,
			}
		}
	}

	return &FFmpegStream{
		source:                         source,
		transport:                      transport,
		audioChan:                      audioChan,
		restartChan:                    make(chan struct{}, 1),
		stopChan:                       make(chan struct{}),
		backoffDuration:                defaultBackoffDuration,
		maxBackoff:                     maxBackoffDuration,
		lastDataTime:                   time.Time{}, // Zero time - no data received yet
		dataRateCalc:                   newDataRateCalculator(source.SafeString, dataRateWindowSize),
		lastDropLogTime:                time.Now(),
		lastSoundLevelNotRegisteredLog: time.Now().Add(-dropLogInterval),              // Allow immediate first log
		streamCreatedAt:                time.Now(),                                    // Track when stream was created
		processState:                   StateIdle,                                     // Initial state is idle
		stateTransitions:               make([]StateTransition, 0, 100),               // Pre-allocate for 100 transitions
		errorContexts:                  make([]*ErrorContext, 0, maxErrorHistorySize), // Pre-allocate error history
		maxErrorHistory:                maxErrorHistorySize,
	}
}

// transitionState safely transitions the process state and logs the change.
// It records the transition in history for debugging and emits structured logs.
// This method is thread-safe and can be called from any goroutine.
//
// Lenient mode: Invalid transitions are logged in debug mode but still applied
// to ensure operations don't get blocked. This makes the system more robust
// and user-friendly by allowing recovery from unexpected states.
//
// Idempotent transitions (same state to same state) are silently ignored to
// reduce log noise and avoid unnecessary history entries.
func (s *FFmpegStream) transitionState(to ProcessState, reason string) {
	s.processStateMu.Lock()
	from := s.processState

	// Skip idempotent transitions (no-op) to reduce log noise
	if from == to {
		s.processStateMu.Unlock()
		if conf.Setting().Debug {
			getStreamLogger().Debug("idempotent state transition ignored",
				logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
				logger.String("source_id", s.source.ID),
				logger.String("state", from.String()),
				logger.String("reason", reason),
				logger.String("component", "ffmpeg-stream"),
				logger.String("operation", "state_transition_noop"))
		}
		return
	}

	// Terminal state safeguard: never allow leaving StateStopped
	// This ensures Stop() remains truly terminal and prevents inconsistent state
	if from == StateStopped && to != StateStopped {
		s.processStateMu.Unlock()
		getStreamLogger().Warn("blocked transition out of terminal state",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.String("source_id", s.source.ID),
			logger.String("from", from.String()),
			logger.String("to", to.String()),
			logger.String("reason", reason),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "state_transition_blocked"))
		return
	}

	// Validate transition (lenient: warn in debug mode but still apply)
	if !isValidTransition(from, to) && conf.Setting().Debug {
		// Only log in debug mode to avoid noise in production
		getStreamLogger().Warn("invalid state transition detected (applying anyway for robustness)",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.String("source_id", s.source.ID),
			logger.String("from", from.String()),
			logger.String("to", to.String()),
			logger.String("reason", reason),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "state_transition_invalid"))
	}

	s.processState = to
	s.processStateMu.Unlock()

	// Record transition for debugging
	transition := StateTransition{
		From:      from,
		To:        to,
		Timestamp: time.Now(),
		Reason:    reason,
	}

	s.transitionsMu.Lock()
	s.stateTransitions = append(s.stateTransitions, transition)
	// Keep only last 100 transitions to prevent unbounded memory growth
	if len(s.stateTransitions) > 100 {
		// Efficiently remove oldest transitions by slicing
		s.stateTransitions = s.stateTransitions[len(s.stateTransitions)-100:]
	}
	s.transitionsMu.Unlock()

	// Log state transition with structured logging
	getStreamLogger().Info("process state transition",
		logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
		logger.String("source_id", s.source.ID),
		logger.String("from", from.String()),
		logger.String("to", to.String()),
		logger.String("reason", reason),
		logger.String("component", "ffmpeg-stream"),
		logger.String("operation", "state_transition"))

	// Publish alert events for key state transitions
	s.publishAlertEvent(from, to, reason)
}

// publishAlertEvent publishes alert events for key stream state transitions.
func (s *FFmpegStream) publishAlertEvent(from, to ProcessState, reason string) {
	props := map[string]any{
		alerting.PropertyStreamName: s.source.DisplayName,
		alerting.PropertyStreamURL:  privacy.SanitizeStreamUrl(s.source.SafeString),
	}

	switch {
	case to == StateRunning && from == StateStarting:
		alerting.TryPublish(&alerting.AlertEvent{
			ObjectType: alerting.ObjectTypeStream,
			EventName:  alerting.EventStreamConnected,
			Properties: props,
		})
	case (to == StateBackoff || to == StateCircuitOpen || to == StateStopped) && from == StateRunning:
		alerting.TryPublish(&alerting.AlertEvent{
			ObjectType: alerting.ObjectTypeStream,
			EventName:  alerting.EventStreamDisconnected,
			Properties: props,
		})
	case to == StateStopped && (from == StateBackoff || from == StateCircuitOpen):
		alerting.TryPublish(&alerting.AlertEvent{
			ObjectType: alerting.ObjectTypeStream,
			EventName:  alerting.EventStreamDisconnected,
			Properties: props,
		})
	}

	// If the reason looks like an error, also publish a stream error event.
	// Clone props to avoid mutating the map shared with previously published events.
	if to == StateBackoff || to == StateCircuitOpen {
		errProps := maps.Clone(props)
		errProps[alerting.PropertyError] = reason
		alerting.TryPublish(&alerting.AlertEvent{
			ObjectType: alerting.ObjectTypeStream,
			EventName:  alerting.EventStreamError,
			Properties: errProps,
		})
	}
}

// GetProcessState returns the current process state (thread-safe)
func (s *FFmpegStream) GetProcessState() ProcessState {
	s.processStateMu.RLock()
	defer s.processStateMu.RUnlock()
	return s.processState
}

// GetStateHistory returns recent state transitions for debugging (thread-safe)
// Returns a copy to avoid race conditions with ongoing transitions
func (s *FFmpegStream) GetStateHistory() []StateTransition {
	s.transitionsMu.Lock()
	defer s.transitionsMu.Unlock()

	// Return a copy to avoid race conditions
	return slices.Clone(s.stateTransitions)
}

// Run starts and manages the FFmpeg process lifecycle.
// It runs in a loop, automatically restarting the process on failures with exponential backoff.
// The function returns when the context is cancelled or Stop() is called.
func (s *FFmpegStream) Run(parentCtx context.Context) {
	// Nil check for critical fields
	if s.source == nil {
		getStreamLogger().Error("cannot start FFmpeg stream: source is nil",
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "run"))
		return
	}

	// Set context and cancel function with proper locking
	// Use WithCancelCause for better cancellation diagnostics
	func() {
		s.cancelMu.Lock()
		defer s.cancelMu.Unlock()
		s.ctx, s.cancel = context.WithCancelCause(parentCtx)
	}()

	defer func() {
		s.cancelMu.Lock()
		defer s.cancelMu.Unlock()
		if s.cancel != nil {
			s.cancel(fmt.Errorf("FFmpegStream: Run() loop exiting for %s", privacy.SanitizeStreamUrl(s.source.SafeString)))
		}
	}()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.stopChan:
			return
		default:
			// Start FFmpeg process
			// Check circuit breaker and wait only for remaining cooldown
			if remaining, open := s.circuitCooldownRemaining(); open {
				// Transition to circuit breaker state before waiting
				s.transitionState(StateCircuitOpen, fmt.Sprintf("circuit breaker cooldown: %s remaining", FormatDuration(remaining)))
				// Wait only the remaining cooldown before next attempt
				select {
				case <-time.After(remaining):
					continue
				case <-s.ctx.Done():
					return
				case <-s.stopChan:
					return
				}
			}

			// STATE TRANSITION: idle/backoff → starting (attempting to start FFmpeg process)
			s.transitionState(StateStarting, "initiating FFmpeg process start")

			if err := s.startProcess(); err != nil {
				getStreamLogger().Error("failed to start FFmpeg process",
					logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
					logger.Error(err),
					logger.String("component", "ffmpeg-stream"),
					logger.String("operation", "start_process"))
				s.recordFailure(0) // No runtime for startup failure
				// STATE TRANSITION: starting → backoff (start failed, entering backoff)
				// Apply backoff UNLESS circuit breaker was opened
				// If circuit breaker is open, let the Run() loop handle the cooldown wait at line 629-641
				currentState := s.GetProcessState()
				if currentState != StateCircuitOpen {
					s.handleRestartBackoff() // This will transition to StateBackoff internally
				} else if conf.Setting().Debug {
					getStreamLogger().Debug("skipping backoff transition - circuit breaker is open (startup failure)",
						logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
						logger.String("state", currentState.String()),
						logger.String("operation", "skip_backoff_circuit_open_startup"))
				}
				continue
			}

			// STATE TRANSITION: starting → running (FFmpeg successfully started)
			s.transitionState(StateRunning, "FFmpeg process started successfully")

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
			processStartTime := func() time.Time {
				s.cmdMu.Lock()
				defer s.cmdMu.Unlock()
				return s.processStartTime
			}()
			runtime := time.Since(processStartTime)
			if err != nil && !errors.Is(err, context.Canceled) {
				// Record failure for circuit breaker
				s.recordFailure(runtime)
				// Log process exit with sanitized error message
				errorMsg := err.Error()
				sanitizedError := privacy.SanitizeFFmpegError(errorMsg)

				// Check if this was a silence timeout
				isSilenceTimeout := strings.Contains(errorMsg, "silence timeout")

				getStreamLogger().Warn("FFmpeg process ended",
					logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
					logger.String("error", sanitizedError),
					logger.Float64("runtime_seconds", runtime.Seconds()),
					logger.String("component", "ffmpeg-stream"),
					logger.String("operation", "process_ended"))

				// Reset restart count for silence timeouts as they're expected
				if isSilenceTimeout {
					func() {
						s.restartCountMu.Lock()
						defer s.restartCountMu.Unlock()
						s.restartCount = 0
					}()
					// Don't count silence timeouts as failures for circuit breaker
					func() {
						s.circuitMu.Lock()
						defer s.circuitMu.Unlock()
						if s.consecutiveFailures > 0 {
							s.consecutiveFailures--
						}
					}()
				}
			} else {
				// Log normal exit
				getStreamLogger().Info("FFmpeg process ended normally",
					logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
					logger.Float64("runtime_seconds", runtime.Seconds()),
					logger.String("component", "ffmpeg-stream"),
					logger.String("operation", "process_ended"))
				// Reset failure count on successful run
				s.resetFailures()
			}

			// Always cleanup the process before restart
			if conf.Setting().Debug {
				getStreamLogger().Debug("calling cleanup after process exit",
					logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
					logger.Float64("runtime_seconds", runtime.Seconds()),
					logger.Bool("had_error", err != nil),
					logger.String("operation", "pre_restart_cleanup"))
			}
			s.cleanupProcess()

			// STATE TRANSITION: running → backoff (process ended, entering backoff before restart)
			// Apply backoff before restart UNLESS circuit breaker was opened
			// If circuit breaker is open, let the Run() loop handle the cooldown wait at line 629-641
			currentState := s.GetProcessState()
			if currentState != StateCircuitOpen {
				s.handleRestartBackoff() // This will transition to StateBackoff internally
			} else if conf.Setting().Debug {
				getStreamLogger().Debug("skipping backoff transition - circuit breaker is open",
					logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
					logger.String("state", currentState.String()),
					logger.String("operation", "skip_backoff_circuit_open"))
			}
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
		return errors.Newf("FFmpeg validation failed: %w", err).
			Category(errors.CategoryValidation).
			Component("ffmpeg-stream").
			Context("operation", "start_process").
			Context("ffmpeg_path", settings.FfmpegPath).
			Build()
	}

	// Get FFmpeg format settings
	sampleRate, numChannels, format := getFFmpegFormat(conf.SampleRate, conf.NumChannels, conf.BitDepth)

	// Validate input source before building command to prevent nil dereference in
	// buildFFmpegInputArgs (which accesses s.source.SafeString and s.source.Type).
	if s.source == nil {
		return errors.Newf("FFmpeg source is nil, cannot start process").
			Category(errors.CategoryValidation).
			Component("ffmpeg-stream").
			Context("operation", "start_process").
			Build()
	}

	// Get RTSP settings for custom FFmpeg parameters
	rtspSettings := conf.Setting().Realtime.RTSP

	// Build FFmpeg command arguments (protocol-aware)
	args := s.buildFFmpegInputArgs(rtspSettings.FFmpegParameters)

	// Get and validate connection string
	connStr, err := s.source.GetConnectionString()
	if err != nil {
		return errors.Newf("failed to get connection string: %w", err).
			Category(errors.CategoryValidation).
			Component("ffmpeg-stream").
			Context("operation", "start_process").
			Context("source_id", s.source.ID).
			Context("url", privacy.SanitizeStreamUrl(s.source.SafeString)).
			Build()
	}

	// Prevent FFmpeg from starting with empty input which causes hard-to-debug restart loops
	if connStr == "" {
		return errors.Newf("connection string is empty for source %s, cannot start FFmpeg", s.source.ID).
			Category(errors.CategoryValidation).
			Component("ffmpeg-stream").
			Context("operation", "start_process").
			Context("source_id", s.source.ID).
			Context("source_type", string(s.source.Type)).
			Context("url", privacy.SanitizeStreamUrl(s.source.SafeString)).
			Build()
	}

	// Add input and output parameters
	args = append(args,
		"-i", connStr,
		"-loglevel", "error",
		"-vn",
		"-f", format,
		"-ar", sampleRate,
		"-ac", numChannels,
		"-hide_banner",
		"pipe:1",
	)

	// Create FFmpeg command
	s.cmd = exec.CommandContext(s.ctx, settings.FfmpegPath, args...) //nolint:gosec // G204: FfmpegPath from validated settings, args built internally

	// Setup process group
	setupProcessGroup(s.cmd)

	// Capture stderr with thread-safe writer
	func() {
		s.stderrMu.Lock()
		defer s.stderrMu.Unlock()
		s.stderr.Reset()
	}()
	s.cmd.Stderr = &threadSafeWriter{buf: &s.stderr, mu: &s.stderrMu}

	// Get stdout pipe
	s.stdout, err = s.cmd.StdoutPipe()
	if err != nil {
		return errors.Newf("failed to create stdout pipe: %w", err).
			Category(errors.CategorySystem).
			Component("ffmpeg-stream").
			Context("operation", "start_process").
			Context("url", privacy.SanitizeStreamUrl(s.source.SafeString)).
			Build()
	}

	// Start process
	if err := s.cmd.Start(); err != nil {
		return errors.Newf("failed to start FFmpeg: %w", err).
			Category(errors.CategorySystem).
			Component("ffmpeg-stream").
			Context("operation", "start_process").
			Context("url", privacy.SanitizeStreamUrl(s.source.SafeString)).
			Context("transport", s.transport).
			Build()
	}

	// Record start time for runtime calculation
	s.processStartTime = time.Now()

	// Debug log process details
	if conf.Setting().Debug {
		getStreamLogger().Debug("FFmpeg process started with details",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.Int("pid", s.cmd.Process.Pid),
			logger.String("transport", s.transport),
			logger.String("ffmpeg_path", settings.FfmpegPath),
			logger.Int("args_count", len(args)),
			logger.String("operation", "start_process_debug"))
	}

	// Update process metrics
	currentTotal := func() int64 {
		s.processMetricsMu.Lock()
		defer s.processMetricsMu.Unlock()
		s.totalProcessCount++
		return s.totalProcessCount
	}()

	// NOTE: Removed premature failure reset - failures should only be reset
	// after the process has proven stable operation with actual data reception

	getStreamLogger().Info("FFmpeg process started",
		logger.String("source_id", s.source.ID),
		logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
		logger.Int("pid", s.cmd.Process.Pid),
		logger.String("transport", s.transport),
		logger.Int64("total_process_count", currentTotal),
		logger.String("component", "ffmpeg-stream"),
		logger.String("operation", "start_process"))

	return nil
}

// buildFFmpegInputArgs constructs the FFmpeg input arguments for this stream.
// RTSP-specific flags like -rtsp_transport are only added for RTSP streams;
// applying them to HTTP/HLS/RTMP/UDP streams causes FFmpeg to exit immediately
// with "Option rtsp_transport not found".
func (s *FFmpegStream) buildFFmpegInputArgs(ffmpegParameters []string) []string {
	args := []string{}

	// Only add -rtsp_transport for RTSP streams. HTTP, HLS, RTMP and UDP streams
	// do not support this option and FFmpeg will fail with "Option rtsp_transport not found".
	if s.source != nil && s.source.Type == SourceTypeRTSP {
		args = append(args, "-rtsp_transport", s.transport)
	}

	// Check if user has already provided a timeout parameter
	hasUserTimeout, userTimeoutValue := detectUserTimeout(ffmpegParameters)

	// Add default timeout if user hasn't provided one
	if !hasUserTimeout {
		args = append(args, "-timeout", strconv.FormatInt(defaultTimeoutMicroseconds, 10))
	}

	// Add custom FFmpeg parameters from configuration (before input)
	if len(ffmpegParameters) > 0 {
		if hasUserTimeout {
			if err := s.validateUserTimeout(userTimeoutValue); err != nil {
				// Invalid user timeout: log warning, use default, and filter out the bad flag.
				// Do NOT append the original parameters unmodified — the invalid -timeout value
				// would override the default we just added, causing FFmpeg to fail.
				getStreamLogger().Warn("invalid user timeout, using default",
					logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
					logger.String("user_timeout", userTimeoutValue),
					logger.Error(err),
					logger.String("component", "ffmpeg-stream"),
					logger.String("operation", "validate_timeout"))
				args = append(args, "-timeout", strconv.FormatInt(defaultTimeoutMicroseconds, 10))
				// Append all params except the invalid -timeout flag and its value.
				// Use a skip flag to drop both the flag name and the following value.
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
				// Valid user timeout: append everything as-is
				args = append(args, ffmpegParameters...)
			}
		} else {
			args = append(args, ffmpegParameters...)
		}
	}

	return args
}

// handleSilenceTimeout checks if stream has stopped producing data and triggers restart
// Returns an error if silence timeout is exceeded, nil otherwise
func (s *FFmpegStream) handleSilenceTimeout(startTime time.Time) error {
	s.lastDataMu.RLock()
	lastData := s.lastDataTime
	s.lastDataMu.RUnlock()

	// Determine effective "no-data" age: if never received any data, use process runtime
	effectiveAge := func() time.Duration {
		if lastData.IsZero() {
			s.cmdMu.Lock()
			ps := s.processStartTime
			s.cmdMu.Unlock()
			if ps.IsZero() {
				return 0 // no running process; skip
			}
			return time.Since(ps)
		}
		return time.Since(lastData)
	}()

	if effectiveAge > 0 && effectiveAge > silenceTimeout {
		// Format last data description for clearer logging
		lastDataDesc := formatLastDataDescription(lastData)

		getStreamLogger().Warn("no data received from stream source, triggering restart",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
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
			Priority(errors.PriorityMedium).
			Context("operation", "silence_timeout").
			Context("url", privacy.SanitizeStreamUrl(s.source.SafeString)).
			Context("timeout_seconds", silenceTimeout.Seconds()).
			Context("last_data", lastDataDesc).
			Build()
	}

	return nil
}

// handleEarlyErrorDetection checks stderr for early errors and takes appropriate action
// Returns an error if a permanent failure is detected, nil if no action needed
func (s *FFmpegStream) handleEarlyErrorDetection() error {
	errCtx := s.checkEarlyErrors()
	if errCtx == nil {
		return nil
	}

	// Record error context
	s.recordErrorContext(errCtx)

	// Take action based on error type
	if errCtx.ShouldOpenCircuit() {
		// Open circuit breaker immediately for permanent failures
		getStreamLogger().Error("early error triggers circuit breaker",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.String("error_type", errCtx.ErrorType),
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
			Priority(errors.PriorityMedium).
			Context("operation", "early_error_detection").
			Context("url", privacy.SanitizeStreamUrl(s.source.SafeString)).
			Context("error_type", errCtx.ErrorType).
			Build()
	}

	if errCtx.ShouldRestart() {
		// Transient error - trigger restart
		getStreamLogger().Warn("early error triggers restart",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.String("error_type", errCtx.ErrorType),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "early_error_restart"))

		s.cleanupProcess()
		return errors.Newf("early FFmpeg error (transient): %s", errCtx.PrimaryMessage).
			Category(errors.CategoryRTSP).
			Component("ffmpeg-stream").
			Priority(errors.PriorityMedium).
			Context("operation", "early_error_detection").
			Context("url", privacy.SanitizeStreamUrl(s.source.SafeString)).
			Context("error_type", errCtx.ErrorType).
			Build()
	}

	return nil
}

// handleQuickExitError processes quick exit scenarios (process exits within processQuickExitTime)
// and returns an appropriate error with error context extraction
func (s *FFmpegStream) handleQuickExitError(startTime time.Time) error {
	// Get stderr output safely
	s.stderrMu.RLock()
	stderrOutput := s.stderr.String()
	s.stderrMu.RUnlock()

	// Try to extract structured error context
	errCtx := ExtractErrorContext(stderrOutput)
	if errCtx != nil {
		// Record error context for diagnostics
		s.recordErrorContext(errCtx)

		// If this is a permanent failure, open circuit breaker
		if errCtx.ShouldOpenCircuit() {
			s.circuitMu.Lock()
			s.consecutiveFailures = circuitBreakerThreshold
			s.circuitOpenTime = time.Now()
			s.circuitMu.Unlock()

			s.transitionState(StateCircuitOpen, fmt.Sprintf("early exit with error: %s", errCtx.ErrorType))
		}

		// Return structured error
		return errors.Newf("FFmpeg process failed to start properly: %s", errCtx.PrimaryMessage).
			Category(errors.CategoryRTSP).
			Component("ffmpeg-stream").
			Priority(errors.PriorityMedium).
			Context("operation", "process_audio_quick_exit").
			Context("url", privacy.SanitizeStreamUrl(s.source.SafeString)).
			Context("transport", s.transport).
			Context("exit_time_seconds", time.Since(startTime).Seconds()).
			Context("error_type", errCtx.ErrorType).
			Build()
	}

	// No structured error context - fall back to generic error
	// Sanitize stderr output to remove sensitive data and memory addresses
	sanitizedOutput := privacy.SanitizeFFmpegError(stderrOutput)
	return errors.Newf("FFmpeg process failed to start properly: %s", sanitizedOutput).
		Category(errors.CategoryRTSP).
		Component("ffmpeg-stream").
		Priority(errors.PriorityMedium).
		Context("operation", "process_audio").
		Context("url", privacy.SanitizeStreamUrl(s.source.SafeString)).
		Context("transport", s.transport).
		Context("exit_time_seconds", time.Since(startTime).Seconds()).
		Context("error_detail", sanitizedOutput).
		Build()
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

	// Create a ticker for early error detection (first 5 seconds)
	// Check stderr every 500ms during early window to catch errors quickly
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

	// Reset data tracking for new process
	s.resetDataTracking()

	for {
		// Check if stream has been stopped before attempting to read
		// This prevents nil pointer dereference when Stop() is called during startup
		if s.GetProcessState() == StateStopped {
			return nil
		}

		// Safely get stdout reference to prevent nil pointer dereference
		// when Stop() calls cleanupProcess() during concurrent operation
		s.cmdMu.Lock()
		stdout := s.stdout
		s.cmdMu.Unlock()

		// If stdout is nil, the process was cleaned up (likely by Stop())
		if stdout == nil {
			return nil
		}

		// Read from FFmpeg stdout (exec pipes do not support read deadlines)
		n, err := stdout.Read(buf)
		if err != nil {
			// Check if process exited too quickly
			if time.Since(startTime) < processQuickExitTime {
				return s.handleQuickExitError(startTime)
			}

			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return nil // Normal shutdown
			}

			return errors.Newf("error reading from FFmpeg: %w", err).
				Category(errors.CategoryRTSP).
				Component("ffmpeg-stream").
				Priority(errors.PriorityMedium).
				Context("operation", "process_audio").
				Context("url", privacy.SanitizeStreamUrl(s.source.SafeString)).
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
				getStreamLogger().Warn("error processing audio data",
					logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
					logger.Error(err),
					logger.String("component", "ffmpeg-stream"),
					logger.String("operation", "process_audio_data"))
			}
		}

		// Check for restart signal and silence detection
		select {
		case <-s.restartChan:
			getStreamLogger().Info("restart requested",
				logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
				logger.String("component", "ffmpeg-stream"),
				logger.String("operation", "restart_requested"))
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
		case <-earlyErrorCheckTimer.C:
			// Disable early error checking after the window expires
			earlyErrorCheckEnabled = false
			earlyErrorCheckTicker.Stop()
			// Drain ticker channel to prevent goroutine leak
			select {
			case <-earlyErrorCheckTicker.C:
			default:
			}
			if conf.Setting().Debug {
				getStreamLogger().Debug("early error detection window closed",
					logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
					logger.Float64("duration_seconds", earlyErrorDetectionWindow.Seconds()),
					logger.String("operation", "early_error_window_close"))
			}
		case <-earlyErrorCheckTicker.C:
			// Check for early errors only if window is still open
			if earlyErrorCheckEnabled {
				if err := s.handleEarlyErrorDetection(); err != nil {
					return err
				}
			}
		case <-silenceCheckTicker.C:
			// Check for silence timeout
			if err := s.handleSilenceTimeout(startTime); err != nil {
				return err
			}
		default:
			// Continue processing
		}
	}
}

// handleAudioData processes a chunk of audio data
func (s *FFmpegStream) handleAudioData(data []byte) error {
	// Apply audio EQ filters if enabled
	// This must happen BEFORE writing to buffers so filtered audio is used for analysis
	if conf.Setting().Realtime.Audio.Equalizer.Enabled {
		// Ensure data length is even (required for 16-bit PCM samples)
		// io.Reader doesn't guarantee aligned reads, so handle odd lengths defensively
		filterLen := len(data)
		if filterLen%2 != 0 {
			filterLen-- // Truncate to even length; trailing byte remains unfiltered
		}
		if filterLen > 0 {
			if eqErr := ApplyFilters(data[:filterLen]); eqErr != nil {
				getStreamLogger().Warn("error applying audio EQ filters",
					logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
					logger.Error(eqErr),
					logger.String("component", "ffmpeg-stream"),
					logger.String("operation", "apply_filters"))
				// Non-fatal: continue processing with unfiltered audio
			}
		}
	}

	// Write to analysis buffer using source ID
	if err := WriteToAnalysisBuffer(s.source.ID, data); err != nil {
		return errors.Newf("failed to write to analysis buffer: %w", err).
			Category(errors.CategoryAudio).
			Component("ffmpeg-stream").
			Context("operation", "handle_audio_data").
			Context("url", privacy.SanitizeStreamUrl(s.source.SafeString)).
			Context("data_size", len(data)).
			Build()
	}

	// Write to capture buffer using source ID
	if err := WriteToCaptureBuffer(s.source.ID, data); err != nil {
		return errors.Newf("failed to write to capture buffer: %w", err).
			Category(errors.CategoryAudio).
			Component("ffmpeg-stream").
			Context("operation", "handle_audio_data").
			Context("url", privacy.SanitizeStreamUrl(s.source.SafeString)).
			Context("data_size", len(data)).
			Build()
	}

	// Broadcast to WebSocket clients using source ID
	broadcastAudioData(s.source.ID, data)

	// Calculate audio level using source ID and DisplayName
	audioLevel := calculateAudioLevel(data, s.source.ID, s.source.DisplayName)

	// Create unified audio data
	unifiedData := UnifiedAudioData{
		AudioLevel: audioLevel,
		Timestamp:  time.Now(),
	}

	// Process sound level if enabled
	if conf.Setting().Realtime.Audio.SoundLevel.Enabled {
		if soundLevel, err := ProcessSoundLevelData(s.source.ID, data); err != nil {
			// Log as warning if it's a registration issue, debug otherwise
			// Skip logging for normal conditions (interval incomplete, no data)
			if errors.Is(err, ErrSoundLevelProcessorNotRegistered) {
				// Rate limit this specific log message to prevent flooding
				s.soundLevelNotRegisteredLogMu.Lock()
				now := time.Now()
				if now.Sub(s.lastSoundLevelNotRegisteredLog) >= dropLogInterval {
					s.lastSoundLevelNotRegisteredLog = now
					getStreamLogger().Warn("sound level processor not registered (further messages suppressed)",
						logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
						logger.Error(err),
						logger.String("operation", "process_sound_level"))
				}
				s.soundLevelNotRegisteredLogMu.Unlock()
			} else if !errors.Is(err, ErrIntervalIncomplete) && !errors.Is(err, ErrNoAudioData) && conf.Setting().Debug {
				getStreamLogger().Debug("failed to process sound level data",
					logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
					logger.Error(err),
					logger.String("operation", "process_sound_level"))
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

		getStreamLogger().Warn("audio data dropped due to full channel",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "audio_data_drop"))

		// Report to Sentry with enhanced context
		errorWithContext := errors.Newf("audio processing channel full, data being dropped").
			Component("ffmpeg-stream").
			Category(errors.CategorySystem).
			Priority(errors.PriorityHigh).
			Context("operation", "audio_data_drop").
			Context("url", privacy.SanitizeStreamUrl(s.source.SafeString)).
			Context("channel_capacity", cap(s.audioChan)).
			Context("drop_log_interval_seconds", dropLogInterval.Seconds()).
			Build()
		// Report via telemetry if enabled, otherwise log for debugging
		if conf.Setting().Debug {
			getStreamLogger().Debug("audio data dropped from channel",
				logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
				logger.Int("channel_capacity", cap(s.audioChan)))
		}
		_ = errorWithContext // Keep for telemetry reporting when enabled
	}
}

// logContextCause logs the context cancellation cause if available.
// Extracted as a helper function to reduce cyclomatic complexity of cleanupProcess.
func (s *FFmpegStream) logContextCause(pid int) {
	// Acquire read lock to safely access s.ctx (protected by cancelMu)
	s.cancelMu.RLock()
	ctx := s.ctx
	s.cancelMu.RUnlock()

	if ctx == nil {
		return
	}

	cause := context.Cause(ctx)
	// Log only if cause exists and is not the standard context.Canceled sentinel
	if cause != nil && !errors.Is(cause, context.Canceled) {
		getStreamLogger().Debug("cleanup triggered by context cancellation",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.Int("pid", pid),
			logger.String("cause", cause.Error()),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "cleanup_process_cause"))
	}
}

// cleanupProcess cleans up the FFmpeg process
func (s *FFmpegStream) cleanupProcess() {
	// Narrow critical section: grab and clear references under lock, then operate on locals
	s.cmdMu.Lock()
	cmd := s.cmd
	stdout := s.stdout
	pid := 0
	if cmd != nil && cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	// Clear references so other observers see "no running process" immediately
	s.cmd = nil
	s.stdout = nil
	s.processStartTime = time.Time{} // Clear start time when tearing down
	s.cmdMu.Unlock()

	// Check if there was actually a process to clean
	if cmd == nil || cmd.Process == nil {
		if conf.Setting().Debug {
			getStreamLogger().Debug("cleanup called but no process to clean",
				logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
				logger.String("operation", "cleanup_process_skip"))
		}
		return
	}

	if conf.Setting().Debug {
		getStreamLogger().Debug("starting process cleanup",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.Int("pid", pid),
			logger.String("operation", "cleanup_process_start"))
	}

	// Log context cancellation cause for diagnostics
	s.logContextCause(pid)

	// Close stdout
	if stdout != nil {
		if err := stdout.Close(); err != nil && conf.Setting().Debug {
			// Log but don't fail - process cleanup is more important
			getStreamLogger().Debug("failed to close stdout",
				logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
				logger.Error(err),
				logger.String("operation", "cleanup_process"))
		}
	}

	// Kill process
	if err := killProcessGroup(cmd); err != nil {
		if conf.Setting().Debug {
			getStreamLogger().Debug("killProcessGroup failed, attempting direct kill",
				logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
				logger.Int("pid", pid),
				logger.Error(err),
				logger.String("operation", "cleanup_process_kill"))
		}

		if killErr := cmd.Process.Kill(); killErr != nil {
			// Only log if kill also fails
			getStreamLogger().Warn("failed to kill process directly",
				logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
				logger.Int("pid", pid),
				logger.Error(killErr),
				logger.String("operation", "cleanup_process_kill_direct"))
		}
	} else if conf.Setting().Debug {
		getStreamLogger().Debug("process group killed successfully",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.Int("pid", pid),
			logger.String("operation", "cleanup_process_kill_success"))
	}

	// Create a channel to signal when Wait() completes
	waitDone := make(chan error, 1)

	// Always call Wait() to reap the zombie - this is critical!
	// We do this in a goroutine that we may abandon if it takes too long,
	// but the goroutine will continue and eventually reap the process
	// cmd is already captured at the beginning of this function
	url := s.source.SafeString
	waitStartTime := time.Now()

	if conf.Setting().Debug {
		getStreamLogger().Debug("spawning wait goroutine for process reaping",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.Int("pid", pid),
			logger.String("operation", "cleanup_process_wait_start"))
	}

	go func() {
		waitErr := cmd.Wait()
		waitDuration := time.Since(waitStartTime)

		// Log completion even if we've already moved on
		if conf.Setting().Debug {
			if waitErr != nil && !strings.Contains(waitErr.Error(), "signal: killed") && !strings.Contains(waitErr.Error(), "signal: terminated") {
				getStreamLogger().Debug("process wait completed with error",
					logger.String("url", privacy.SanitizeStreamUrl(url)),
					logger.Error(waitErr),
					logger.Int("pid", pid),
					logger.Int64("wait_duration_ms", waitDuration.Milliseconds()),
					logger.String("operation", "cleanup_process_wait_error"))
			} else {
				getStreamLogger().Debug("process wait completed successfully",
					logger.String("url", privacy.SanitizeStreamUrl(url)),
					logger.Int("pid", pid),
					logger.Int64("wait_duration_ms", waitDuration.Milliseconds()),
					logger.String("operation", "cleanup_process_wait_success"))
			}
		}

		// Non-blocking send of the wait result.
		// If the buffer slot has already been consumed (or we timed out and moved on),
		// skip sending to avoid blocking; the goroutine will exit regardless.
		select {
		case waitDone <- waitErr:
		default:
			// Channel buffer full or already consumed - we timed out
			if conf.Setting().Debug {
				getStreamLogger().Debug("wait result not sent - cleanup already timed out",
					logger.String("url", privacy.SanitizeStreamUrl(url)),
					logger.Int("pid", pid),
					logger.String("operation", "cleanup_process_wait_abandoned"))
			}
		}
	}()

	// Wait for process cleanup with timeout
	select {
	case err := <-waitDone:
		// Process was successfully reaped
		if err != nil && !strings.Contains(err.Error(), "signal: killed") && !strings.Contains(err.Error(), "signal: terminated") {
			getStreamLogger().Warn("FFmpeg process wait error",
				logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
				logger.Error(err),
				logger.Int("pid", pid),
				logger.String("component", "ffmpeg-stream"),
				logger.String("operation", "process_wait"))
		}
		getStreamLogger().Info("FFmpeg process stopped",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.Int("pid", pid),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "cleanup_process"))

	case <-time.After(processCleanupTimeout):
		// Timeout occurred, but the goroutine will continue and eventually reap the process
		getStreamLogger().Warn("FFmpeg process cleanup timeout - process will be reaped asynchronously",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.Int("pid", pid),
			logger.Float64("timeout_seconds", processCleanupTimeout.Seconds()),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "cleanup_process_timeout"))

		// Important: We do NOT return here - we continue to clean up our state
		// The goroutine will eventually call Wait() and reap the zombie
		// This is a simple and correct approach - we ensure Wait() is always called
		// even if it takes longer than expected

		if conf.Setting().Debug {
			getStreamLogger().Debug("cleanup timeout - wait goroutine still running",
				logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
				logger.Int("pid", pid),
				logger.String("operation", "cleanup_process_timeout_detail"))
		}
	}

	// Command reference already cleared at the beginning of cleanup

	if conf.Setting().Debug {
		getStreamLogger().Debug("process cleanup completed",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.Int("pid", pid),
			logger.String("operation", "cleanup_process_complete"))
	}
}

// handleRestartBackoff handles exponential backoff for restarts
func (s *FFmpegStream) handleRestartBackoff() {
	s.restartCountMu.Lock()
	s.restartCount++
	currentRestartCount := s.restartCount

	// Cap the exponent to prevent integer overflow
	exponent := min(s.restartCount-1, maxBackoffExponent)

	backoff := min(s.backoffDuration*time.Duration(1<<uint(exponent)), s.maxBackoff) //nolint:gosec // G115: exponent is capped by maxBackoffExponent (line 1610), no overflow risk

	// Add rate limiting for very high restart counts to prevent runaway loops
	if currentRestartCount > 50 {
		// Additional delay proportional to restart count beyond 50
		additionalDelay := min(time.Duration(currentRestartCount-50)*10*time.Second,
			// Cap at 5 minutes extra
			5*time.Minute)
		backoff += additionalDelay
		getStreamLogger().Warn("high restart count detected - applying rate limiting",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.Int("restart_count", currentRestartCount),
			logger.Float64("additional_delay_seconds", additionalDelay.Seconds()),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "restart_rate_limit"))
	}
	s.restartCountMu.Unlock()

	// Add jitter to avoid synchronized restarts across many streams (thundering herd)
	wait := backoff
	if backoff > 0 {
		// Compute jitter factor from constant
		factor := float64(restartJitterPercentMax) / 100.0
		jitterRange := time.Duration(float64(backoff) * factor)
		if jitterRange > 0 {
			if n, err := rand.Int(rand.Reader, big.NewInt(jitterRange.Nanoseconds())); err == nil {
				wait = backoff + time.Duration(n.Int64())
			}
		}
	}

	// STATE TRANSITION: * → backoff (entering backoff period before restart)
	s.transitionState(StateBackoff, fmt.Sprintf("restart #%d: waiting %s (base backoff: %s)", currentRestartCount, FormatDuration(wait), FormatDuration(backoff)))

	if conf.Setting().Debug {
		getStreamLogger().Debug("applying restart backoff",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.Int64("backoff_ms", backoff.Milliseconds()),
			logger.Int64("wait_ms", wait.Milliseconds()),
			logger.Int("jitter_percent_max", restartJitterPercentMax),
			logger.Int("restart_count", currentRestartCount),
			logger.String("operation", "restart_backoff"))
	}

	// Log with both formats for compatibility and support dumps
	getStreamLogger().Info("waiting before restart attempt",
		logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
		logger.Float64("wait_seconds", wait.Seconds()),
		logger.Float64("backoff_seconds", backoff.Seconds()),
		logger.Int("jitter_percent_max", restartJitterPercentMax),
		logger.Int("restart_count", currentRestartCount),
		logger.String("component", "ffmpeg-stream"),
		logger.String("operation", "restart_wait"))

	select {
	case <-time.After(wait):
		// Continue with restart
	case <-s.ctx.Done():
		// Context cancelled
	case <-s.stopChan:
		// Stop requested
	}
}

// Stop gracefully stops the FFmpeg stream.
// It signals the stream to stop, cancels the context, and cleans up the FFmpeg process.
// This method is idempotent - multiple calls are safe and will not panic.
func (s *FFmpegStream) Stop() {
	s.stopOnce.Do(func() {
		// STATE TRANSITION: * → stopped (Stop() called, permanently stopping stream)
		s.transitionState(StateStopped, "Stop() called")

		s.stoppedMu.Lock()
		s.stopped = true
		s.stoppedMu.Unlock()

		// Signal stop
		close(s.stopChan)

		// Cancel context with reason using proper locking
		s.cancelMu.RLock()
		cancel := s.cancel
		s.cancelMu.RUnlock()

		if cancel != nil {
			cancel(fmt.Errorf("FFmpegStream: Stop() called for %s", privacy.SanitizeStreamUrl(s.source.SafeString)))
		}

		// Cleanup process
		s.cleanupProcess()
	})
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
			getStreamLogger().Debug("restart already in progress, ignoring request",
				logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
				logger.Bool("manual", manual),
				logger.String("component", "ffmpeg-stream"),
				logger.String("operation", "restart_ignored"))
		}
		return
	}
	s.restartInProgress = true
	s.restartMu.Unlock()

	// STATE TRANSITION: * → restarting (Restart() called)
	restartType := "automatic"
	if manual {
		restartType = "manual"
	}
	s.transitionState(StateRestarting, fmt.Sprintf("%s restart requested", restartType))

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
			getStreamLogger().Debug("restart signal sent",
				logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
				logger.Bool("manual", manual),
				logger.String("component", "ffmpeg-stream"),
				logger.String("operation", "restart_requested"))
		}
	default:
		// Channel full, reset the in-progress flag
		s.restartMu.Lock()
		s.restartInProgress = false
		s.restartMu.Unlock()
		if conf.Setting().Debug {
			getStreamLogger().Debug("restart channel full, restart already pending",
				logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
				logger.Bool("manual", manual),
				logger.String("component", "ffmpeg-stream"),
				logger.String("operation", "restart_pending"))
		}
	}
}

// IsRestarting checks if the stream is currently in the process of restarting.
// With the state machine, this is now simpler: check if state indicates restart-related activity.
// This includes streams in: StateRestarting, StateBackoff, StateCircuitOpen, or StateStarting.
//
// This method helps prevent the manager from interfering with streams that are
// already handling their own restart cycle, avoiding premature restarts that
// would break the exponential backoff mechanism.
func (s *FFmpegStream) IsRestarting() bool {
	state := s.GetProcessState()

	// Stream is restarting if in any of these states:
	// - StateRestarting: explicit restart requested
	// - StateBackoff: waiting before retry
	// - StateCircuitOpen: circuit breaker cooldown
	// - StateStarting: in process of starting (transitional state)
	return state == StateRestarting ||
		state == StateBackoff ||
		state == StateCircuitOpen ||
		state == StateStarting
}

// GetProcessStartTime returns the start time of the current FFmpeg process.
// Returns zero time if no process is currently running.
// This is used by the manager to determine if a stream has been running
// long enough to be eligible for health-based restarts.
func (s *FFmpegStream) GetProcessStartTime() time.Time {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()

	// Only return start time if we have a truly running process (not exited)
	// Check ProcessState to ensure the process hasn't exited
	if s.cmd != nil && s.cmd.Process != nil && s.cmd.ProcessState == nil {
		return s.processStartTime
	}
	return time.Time{} // Zero time indicates no running process
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
	dataRate := s.dataRateCalc.getRate()

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
				getStreamLogger().Debug("stream in grace period, no data received yet",
					logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
					logger.Float64("time_since_creation_seconds", timeSinceCreation.Seconds()),
					logger.Float64("grace_period_seconds", gracePeriod.Seconds()),
					logger.Float64("remaining_grace_seconds", (gracePeriod-timeSinceCreation).Seconds()),
					logger.String("last_data", "never"),
					logger.String("component", "ffmpeg-stream"),
					logger.String("operation", "get_health"))
			}
		} else {
			// Grace period expired and still no data - definitely not healthy
			isHealthy = false
			isReceivingData = false

			if conf.Setting().Debug {
				getStreamLogger().Debug("stream has never received data (grace period expired)",
					logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
					logger.Float64("time_since_creation_seconds", timeSinceCreation.Seconds()),
					logger.Float64("grace_period_seconds", gracePeriod.Seconds()),
					logger.String("last_data", "never"),
					logger.String("component", "ffmpeg-stream"),
					logger.String("operation", "get_health"))
			}
		}
	} else {
		// Consider unhealthy if no data for configured threshold
		isHealthy = time.Since(lastData) < healthyDataThreshold
		// Stream is receiving data if we got data within the threshold
		isReceivingData = time.Since(lastData) < defaultReceivingDataThreshold
	}

	// Get current process state
	state := s.GetProcessState()

	// Get state history (last 10 transitions for health reporting)
	allHistory := s.GetStateHistory()
	var recentHistory []StateTransition
	if len(allHistory) > 10 {
		recentHistory = allHistory[len(allHistory)-10:]
	} else {
		recentHistory = allHistory
	}

	// Get error history (last 10 errors for diagnostics)
	// Note: We store up to maxErrorHistorySize (100) internally for comprehensive analysis,
	// but only expose the most recent maxErrorHistoryExposed (10) via the health API
	// to keep API responses manageable while maintaining full history for debugging
	allErrors := s.getErrorContexts()
	var recentErrors []*ErrorContext
	if len(allErrors) > maxErrorHistoryExposed {
		recentErrors = allErrors[len(allErrors)-maxErrorHistoryExposed:]
	} else {
		recentErrors = allErrors
	}

	// Get last error context
	lastError := s.getLastErrorContext()

	// Debug log health check details
	if conf.Setting().Debug {
		getStreamLogger().Debug("health check performed",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.Int("pid", currentPID),
			logger.Bool("is_healthy", isHealthy),
			logger.Bool("is_receiving_data", isReceivingData),
			logger.Float64("last_data_seconds_ago", secondsSinceOrZero(lastData)),
			logger.Float64("healthy_threshold_seconds", healthyDataThreshold.Seconds()),
			logger.Int64("total_bytes", totalBytes),
			logger.Float64("bytes_per_second", dataRate),
			logger.Int("restart_count", restarts),
			logger.Bool("has_process", currentPID > 0),
			logger.String("process_state", state.String()),
			logger.Int("error_count", len(allErrors)),
			logger.String("last_error_type", func() string {
				if lastError != nil {
					return lastError.ErrorType
				}
				return "none"
			}()),
			logger.String("operation", "get_health"))
	}

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

// updateLastDataTime updates the last data received timestamp
func (s *FFmpegStream) updateLastDataTime() {
	s.lastDataMu.Lock()
	s.lastDataTime = time.Now()
	s.lastDataMu.Unlock()
}

// resetDataTracking resets all data tracking state for a new process.
// This prevents confusing "inactive for 0 seconds" logs and ensures
// clean state for each new FFmpeg process instance.
func (s *FFmpegStream) resetDataTracking() {
	s.lastDataMu.Lock()
	s.lastDataTime = time.Time{} // Explicitly reset to zero time
	s.lastDataMu.Unlock()

	s.bytesReceivedMu.Lock()
	s.totalBytesReceived = 0
	s.bytesReceivedMu.Unlock()
}

// logStreamHealth logs the current stream health status
func (s *FFmpegStream) logStreamHealth() {
	health := s.GetHealth()

	// Get current data statistics
	s.bytesReceivedMu.RLock()
	totalBytes := s.totalBytesReceived
	s.bytesReceivedMu.RUnlock()

	// Reuse the already calculated data rate from health object
	dataRate := health.BytesPerSecond

	if health.IsReceivingData {
		getStreamLogger().Info("stream health check - receiving data",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.Bool("is_healthy", health.IsHealthy),
			logger.Bool("is_receiving_data", health.IsReceivingData),
			logger.Int64("total_bytes_received", totalBytes),
			logger.Float64("bytes_per_second", dataRate),
			logger.Float64("last_data_ago_seconds", secondsSinceOrZero(health.LastDataReceived)),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "health_check"))
	} else {
		getStreamLogger().Warn("stream health check - no data received",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.Bool("is_healthy", health.IsHealthy),
			logger.Bool("is_receiving_data", health.IsReceivingData),
			logger.Int64("total_bytes_received", totalBytes),
			logger.Float64("last_data_ago_seconds", secondsSinceOrZero(health.LastDataReceived)),
			logger.String("component", "ffmpeg-stream"),
			logger.String("operation", "health_check"))
	}
}

// isCircuitOpen checks if the circuit breaker is open
// isCircuitOpenSilent checks if the circuit breaker is open without logging.
// This is used by IsRestarting() to avoid log spam during health checks.
func (s *FFmpegStream) isCircuitOpenSilent() bool {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()

	// Check if circuit was opened and we're still in cooldown
	return !s.circuitOpenTime.IsZero() && time.Since(s.circuitOpenTime) < circuitBreakerCooldown
}

// circuitCooldownRemaining returns (remaining, true) if the circuit is open, or (0, false) otherwise.
// This allows waiting only for the remaining cooldown period instead of the full duration.
func (s *FFmpegStream) circuitCooldownRemaining() (time.Duration, bool) {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()

	if s.circuitOpenTime.IsZero() {
		return 0, false
	}

	elapsed := time.Since(s.circuitOpenTime)
	if elapsed >= circuitBreakerCooldown {
		return 0, false
	}

	return circuitBreakerCooldown - elapsed, true
}

func (s *FFmpegStream) isCircuitOpen() bool {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()

	// Check if circuit was opened (circuitOpenTime is set) and we're still in cooldown
	if !s.circuitOpenTime.IsZero() && time.Since(s.circuitOpenTime) < circuitBreakerCooldown {
		remaining := circuitBreakerCooldown - time.Since(s.circuitOpenTime)
		getStreamLogger().Warn("circuit breaker is open",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.Int("consecutive_failures", s.consecutiveFailures),
			logger.String("cooldown_remaining", FormatDuration(remaining)),
			logger.String("cooldown_total", FormatDuration(circuitBreakerCooldown)),
			logger.String("component", "ffmpeg-stream"))
		return true
	}

	// Reset after cooldown if needed
	if !s.circuitOpenTime.IsZero() && time.Since(s.circuitOpenTime) >= circuitBreakerCooldown {
		previousFailures := s.consecutiveFailures
		s.consecutiveFailures = 0
		openDuration := time.Since(s.circuitOpenTime)
		s.circuitOpenTime = time.Time{} // Reset the open time

		// Log circuit breaker closure
		getStreamLogger().Info("circuit breaker closed after cooldown",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.Int("previous_failures", previousFailures),
			logger.String("open_duration", FormatDuration(openDuration)),
			logger.String("cooldown_period", FormatDuration(circuitBreakerCooldown)),
			logger.String("component", "ffmpeg-stream"))

		// Report circuit breaker closure to telemetry
		errorWithContext := errors.Newf("stream circuit breaker closed after cooldown").
			Component("ffmpeg-stream").
			Category(errors.CategoryRTSP).
			Priority(errors.PriorityLow).
			Context("operation", "circuit_breaker_close").
			Context("url", privacy.SanitizeStreamUrl(s.source.SafeString)).
			Context("transport", s.transport).
			Context("previous_failures", previousFailures).
			Context("open_duration_seconds", openDuration.Seconds()).
			Context("cooldown_seconds", circuitBreakerCooldown.Seconds()).
			Build()
		// Report via telemetry if enabled, otherwise log for debugging
		if conf.Setting().Debug {
			getStreamLogger().Debug("circuit breaker closed after cooldown",
				logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
				logger.Int("previous_failures", previousFailures),
				logger.String("open_duration", FormatDuration(openDuration)),
				logger.String("cooldown_period", FormatDuration(circuitBreakerCooldown)))
		}
		_ = errorWithContext // Keep for telemetry reporting when enabled
	}

	return false
}

// recordFailure records a failure for the circuit breaker with runtime consideration.
//
// The function implements a graduated threshold system that opens the circuit breaker
// earlier for rapid failures, preventing resource waste on streams that fail quickly:
//
//   - Immediate failures (< 1 second): Opens after 3 failures
//     These are typically connection refused or DNS resolution failures
//   - Rapid failures (< 5 seconds): Opens after 5 failures
//     These indicate authentication issues or immediate stream errors
//   - Quick failures (< 30 seconds): Opens after 8 failures
//     These suggest configuration problems or unstable streams
//   - Normal failures (any runtime): Opens after 10 failures
//     Standard threshold for streams that run longer before failing
//
// This graduated approach prevents infinite restart loops while allowing genuine
// recovery attempts for temporarily unavailable streams.
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
			getStreamLogger().Debug("short-lived process detected",
				logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
				logger.Float64("runtime_seconds", runtime.Seconds()),
				logger.Int64("short_lived_count", shortLived),
				logger.Int64("total_count", total),
				logger.Float64("short_lived_percentage", float64(shortLived)/float64(total)*100),
				logger.String("component", "ffmpeg-stream"),
				logger.String("operation", "process_metrics"))
		}
	}

	// Enhanced circuit breaker logic that considers rapid failures
	// Open circuit breaker faster if processes are failing very quickly
	shouldOpenCircuit := false
	var reason string

	switch {
	case runtime < circuitBreakerImmediateRuntime && s.consecutiveFailures >= circuitBreakerImmediateThreshold:
		// Immediate failures (< 1 second) - open circuit after just 3 failures
		shouldOpenCircuit = true
		reason = "immediate connection failures"
	case runtime < circuitBreakerRapidRuntime && s.consecutiveFailures >= circuitBreakerRapidThreshold:
		// Rapid failures (< 5 seconds) - open circuit after just 5 failures
		shouldOpenCircuit = true
		reason = "rapid process failures"
	case runtime < circuitBreakerQuickRuntime && s.consecutiveFailures >= circuitBreakerQuickThreshold:
		// Quick failures (< 30 seconds) - open circuit after 8 failures
		shouldOpenCircuit = true
		reason = "quick process failures"
	case s.consecutiveFailures >= circuitBreakerThreshold:
		// Standard threshold reached
		shouldOpenCircuit = true
		reason = "consecutive failure threshold"
	}

	if shouldOpenCircuit {
		currentFailures := s.consecutiveFailures
		s.circuitOpenTime = time.Now()
		// Unlock circuit mutex before calling transitionState to avoid nested lock acquisition
		s.circuitMu.Unlock()

		// STATE TRANSITION: * → circuit_open (circuit breaker opened due to failures)
		s.transitionState(StateCircuitOpen, fmt.Sprintf("circuit breaker opened: %s (failures: %d, runtime: %s)", reason, currentFailures, FormatDuration(runtime)))

		// Re-lock for remaining operations
		s.circuitMu.Lock()

		getStreamLogger().Error("circuit breaker opened",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.Int("consecutive_failures", currentFailures),
			logger.String("runtime", FormatDuration(runtime)),
			logger.String("reason", reason),
			logger.String("cooldown_period", FormatDuration(circuitBreakerCooldown)),
			logger.String("component", "ffmpeg-stream"))

		// Report to Sentry with enhanced context
		errorWithContext := errors.Newf("stream circuit breaker opened: %s (runtime: %v)", reason, runtime).
			Component("ffmpeg-stream").
			Category(errors.CategoryRTSP).
			Context("operation", "circuit_breaker_open").
			Context("url", privacy.SanitizeStreamUrl(s.source.SafeString)).
			Context("transport", s.transport).
			Context("consecutive_failures", currentFailures).
			Context("runtime_seconds", runtime.Seconds()).
			Context("reason", reason).
			Context("cooldown_seconds", circuitBreakerCooldown.Seconds()).
			Build()
		// Report via telemetry if enabled, otherwise log for debugging
		// Note: We already log at WARN level above, so only add debug if not already logged
		_ = errorWithContext // Keep for telemetry reporting when enabled
	}
}

// resetFailures resets the failure count
func (s *FFmpegStream) resetFailures() {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()

	if s.consecutiveFailures > 0 {
		getStreamLogger().Info("resetting failure count after successful run",
			logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
			logger.Int("previous_failures", s.consecutiveFailures),
			logger.String("component", "ffmpeg-stream"))
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

// recordErrorContext stores an error context in the history buffer.
// It maintains a ring buffer of the last N errors for diagnostics.
func (s *FFmpegStream) recordErrorContext(ctx *ErrorContext) {
	if ctx == nil {
		return
	}

	s.errorContextsMu.Lock()
	defer s.errorContextsMu.Unlock()

	// Add to history
	s.errorContexts = append(s.errorContexts, ctx)

	// Maintain ring buffer size
	if len(s.errorContexts) > s.maxErrorHistory {
		// Remove oldest entry
		s.errorContexts = s.errorContexts[1:]
	}

	// Log the error context for visibility
	// SECURITY: Defensive sanitization - strip any @ prefix from target_host
	// in case extraction logic failed to properly sanitize credentials
	targetHost := ctx.TargetHost
	if strings.Contains(targetHost, "@") {
		// If @ is present, take everything after the last @
		if idx := strings.LastIndex(targetHost, "@"); idx != -1 {
			targetHost = targetHost[idx+1:]
		}
	}

	getStreamLogger().Error("FFmpeg error detected",
		logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
		logger.String("error_type", ctx.ErrorType),
		logger.String("primary_message", ctx.PrimaryMessage),
		logger.String("target_host", targetHost), // Use defensively sanitized host
		logger.Int("target_port", ctx.TargetPort),
		logger.Bool("should_open_circuit", ctx.ShouldOpenCircuit()),
		logger.Bool("should_restart", ctx.ShouldRestart()),
		logger.String("component", "ffmpeg-stream"),
		logger.String("operation", "error_detection"))
}

// getErrorContexts returns a copy of the error history for diagnostics.
// This is thread-safe and can be called from any goroutine.
func (s *FFmpegStream) getErrorContexts() []*ErrorContext {
	s.errorContextsMu.RLock()
	defer s.errorContextsMu.RUnlock()

	// Return a copy to avoid race conditions
	result := make([]*ErrorContext, len(s.errorContexts))
	copy(result, s.errorContexts)
	return result
}

// getLastErrorContext returns the most recent error context, or nil if no errors.
func (s *FFmpegStream) getLastErrorContext() *ErrorContext {
	s.errorContextsMu.RLock()
	defer s.errorContextsMu.RUnlock()

	if len(s.errorContexts) == 0 {
		return nil
	}
	return s.errorContexts[len(s.errorContexts)-1]
}

// checkEarlyErrors checks FFmpeg stderr for errors during the early detection window.
// This is called periodically during the first 5 seconds after process start.
// Returns the error context if an error is detected, nil otherwise.
func (s *FFmpegStream) checkEarlyErrors() *ErrorContext {
	// Read stderr output safely
	s.stderrMu.RLock()
	stderrOutput := s.stderr.String()
	s.stderrMu.RUnlock()

	// Extract error context if present
	return ExtractErrorContext(stderrOutput)
}

// conditionalFailureReset resets failures only after the process has proven
// stable operation with substantial data reception.
// This prevents premature failure resets that could lead to infinite restart loops.
func (s *FFmpegStream) conditionalFailureReset(totalBytesReceived int64) {
	// Get process start time safely and calculate time since start atomically
	// to avoid race condition where process could change between check and use
	s.cmdMu.Lock()
	processStartTime := s.processStartTime
	if processStartTime.IsZero() {
		s.cmdMu.Unlock()
		return // No running process
	}
	timeSinceStart := time.Since(processStartTime)
	s.cmdMu.Unlock()

	// Check if process has been stable long enough and received sufficient data
	if timeSinceStart >= circuitBreakerMinStabilityTime && totalBytesReceived >= circuitBreakerMinStabilityBytes {
		s.circuitMu.Lock()
		if s.consecutiveFailures > 0 {
			previousFailures := s.consecutiveFailures
			s.consecutiveFailures = 0
			s.circuitMu.Unlock()

			getStreamLogger().Info("resetting failure count after successful stable operation",
				logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
				logger.Float64("runtime_seconds", timeSinceStart.Seconds()),
				logger.Int64("total_bytes", totalBytesReceived),
				logger.Int("previous_failures", previousFailures),
				logger.Float64("min_stability_seconds", circuitBreakerMinStabilityTime.Seconds()),
				logger.Int64("min_reset_bytes", circuitBreakerMinStabilityBytes),
				logger.String("component", "ffmpeg-stream"))

			// Report failure reset to telemetry
			errorWithContext := errors.Newf("stream failures reset after stable operation").
				Component("ffmpeg-stream").
				Category(errors.CategoryRTSP).
				Priority(errors.PriorityLow).
				Context("operation", "failure_reset").
				Context("url", privacy.SanitizeStreamUrl(s.source.SafeString)).
				Context("runtime_seconds", timeSinceStart.Seconds()).
				Context("total_bytes", totalBytesReceived).
				Context("previous_failures", previousFailures).
				Context("min_stability_seconds", circuitBreakerMinStabilityTime.Seconds()).
				Context("min_reset_bytes", circuitBreakerMinStabilityBytes).
				Build()
			// Report via telemetry if enabled, otherwise log for debugging
			if conf.Setting().Debug {
				getStreamLogger().Debug("failures reset after stable operation",
					logger.String("url", privacy.SanitizeStreamUrl(s.source.SafeString)),
					logger.Float64("runtime_seconds", timeSinceStart.Seconds()),
					logger.Int64("total_bytes", totalBytesReceived),
					logger.Int("previous_failures", previousFailures))
			}
			_ = errorWithContext // Keep for telemetry reporting when enabled
		} else {
			s.circuitMu.Unlock()
		}
	}
}
