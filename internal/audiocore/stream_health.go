package audiocore

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// StreamState is the connection-oriented lifecycle of a network stream,
// independent of how the producer implements it. It replaces the FFmpeg
// process vocabulary (idle, starting, running, restarting, backoff,
// circuit_open, stopped) at the neutral seam: a producer maps its own
// sub-states onto these and carries the sub-state name in
// StreamHealth.StateDetail for the legacy process_state API field.
type StreamState int

const (
	// StreamStateStarting means the first connect is in flight.
	StreamStateStarting StreamState = iota
	// StreamStateConnected means a session is live and may deliver audio.
	StreamStateConnected
	// StreamStateReconnecting means a session ended and the producer is retrying
	// or waiting out backoff.
	StreamStateReconnecting
	// StreamStateStopped means the stream was stopped on request (StopStream,
	// quiet hours, shutdown).
	StreamStateStopped
	// StreamStateFailed means the producer gave up on a terminal, non-retryable
	// cause.
	StreamStateFailed
)

// Neutral StreamState names. Distinct from the legacy process_state strings
// (see LegacyProcessName): "connected" and "reconnecting" have no legacy
// equivalent because the legacy vocabulary was process-oriented.
const (
	streamStateNameStarting     = "starting"
	streamStateNameConnected    = "connected"
	streamStateNameReconnecting = "reconnecting"
	streamStateNameStopped      = "stopped"
	streamStateNameFailed       = "failed"
)

// String returns the neutral connection-state name.
func (s StreamState) String() string {
	switch s {
	case StreamStateStarting:
		return streamStateNameStarting
	case StreamStateConnected:
		return streamStateNameConnected
	case StreamStateReconnecting:
		return streamStateNameReconnecting
	case StreamStateStopped:
		return streamStateNameStopped
	case StreamStateFailed:
		return streamStateNameFailed
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// Legacy process_state strings the health API emits so the existing frontend,
// which switches on these literals, keeps working. A producer that does not
// set StreamHealth.StateDetail falls back to this fixed mapping.
const (
	legacyProcessStateStarting   = "starting"
	legacyProcessStateRunning    = "running"
	legacyProcessStateRestarting = "restarting"
	legacyProcessStateStopped    = "stopped"
	legacyProcessStateFailed     = "failed"
)

// LegacyProcessName returns the legacy process_state string this state maps to
// when a producer supplies no finer-grained StateDetail. Connected maps to the
// historical "running" and Reconnecting to "restarting"; the FFmpeg producer
// always sets StateDetail, so this fallback only serves producers that report
// the neutral state alone.
func (s StreamState) LegacyProcessName() string {
	switch s {
	case StreamStateStarting:
		return legacyProcessStateStarting
	case StreamStateConnected:
		return legacyProcessStateRunning
	case StreamStateReconnecting:
		return legacyProcessStateRestarting
	case StreamStateStopped:
		return legacyProcessStateStopped
	case StreamStateFailed:
		return legacyProcessStateFailed
	default:
		return s.String()
	}
}

// RecoveryState is what the liveness watchdog asks a producer before it decides
// to tear a silent source down. Phase 1 producers report RecoveryUnknown; the
// watchdog coordination that consumes the other values arrives in Phase 2.
type RecoveryState int

const (
	// RecoveryUnknown means the producer does not report recovery intent, so the
	// legacy watchdog behaviour applies.
	RecoveryUnknown RecoveryState = iota
	// RecoveryIdle means the stream is connected and any silence is a media stall
	// the watchdog owns.
	RecoveryIdle
	// RecoveryInProgress means the producer is reconnecting and the watchdog
	// should defer.
	RecoveryInProgress
	// RecoveryGivenUp means the producer failed terminally and the watchdog should
	// escalate now.
	RecoveryGivenUp
)

// Recovery state names for logs and diagnostics.
const (
	recoveryNameUnknown    = "unknown"
	recoveryNameIdle       = "idle"
	recoveryNameInProgress = "in_progress"
	recoveryNameGivenUp    = "given_up"
)

// String returns a human-readable name for the recovery state.
func (r RecoveryState) String() string {
	switch r {
	case RecoveryUnknown:
		return recoveryNameUnknown
	case RecoveryIdle:
		return recoveryNameIdle
	case RecoveryInProgress:
		return recoveryNameInProgress
	case RecoveryGivenUp:
		return recoveryNameGivenUp
	default:
		return fmt.Sprintf("unknown(%d)", int(r))
	}
}

// Stream error type constants categorise a failure for retry and circuit
// breaker decisions and for the user-facing health API. They are the neutral
// vocabulary both the FFmpeg stderr classifier and the future native typed-error
// mapper populate.
//
// Transient errors (may recover on retry):
//   - ErrTypeConnectionTimeout - host unreachable or slow network
//   - ErrTypeNetworkUnreachable - network transition, interface coming up
//   - ErrTypeRTSP503 - server overloaded, may recover
//   - ErrTypeInvalidData - stream corruption, may be temporary
//   - ErrTypeEOF - stream ended unexpectedly, may restart
//
// Permanent errors (require configuration fix, no retry):
//   - ErrTypeRTSP404 - stream path does not exist
//   - ErrTypeConnectionRefused - no server listening on port
//   - ErrTypeAuthFailed - invalid credentials (401)
//   - ErrTypeAuthForbidden - insufficient permissions (403)
//   - ErrTypeNoRoute - routing table problem for specific host
//   - ErrTypeOperationNotPermit - firewall/SELinux blocking
//   - ErrTypeSSLError - certificate or TLS configuration issue
//   - ErrTypeDNSResolutionFailed - hostname does not resolve
//   - ErrTypeProtocolError - unsupported protocol in URL
//
// See ShouldOpenCircuit and ShouldRestart for how these drive circuit breaker
// and restart behaviour.
const (
	ErrTypeConnectionTimeout   = "connection_timeout"
	ErrTypeNetworkUnreachable  = "network_unreachable"
	ErrTypeRTSP503             = "rtsp_503"
	ErrTypeInvalidData         = "invalid_data"
	ErrTypeEOF                 = "eof"
	ErrTypeRTSP404             = "rtsp_404"
	ErrTypeConnectionRefused   = "connection_refused"
	ErrTypeAuthFailed          = "auth_failed"
	ErrTypeAuthForbidden       = "auth_forbidden"
	ErrTypeNoRoute             = "no_route"
	ErrTypeOperationNotPermit  = "operation_not_permitted"
	ErrTypeSSLError            = "ssl_error"
	ErrTypeDNSResolutionFailed = "dns_resolution_failed"
	ErrTypeProtocolError       = "protocol_error"
)

// StreamErrorContext contains rich, producer-neutral diagnostics extracted from
// a failed stream connection. The FFmpeg stderr classifier and the native
// typed-error mapper both populate it; the health API renders it. Field set is
// unchanged from the historical ffmpeg.ErrorContext (the classifier moved with
// the struct).
type StreamErrorContext struct {
	ErrorType       string        // "connection_timeout", "rtsp_404", "auth_failed", etc.
	PrimaryMessage  string        // Main error message
	TargetHost      string        // Extracted host/IP (sanitized - no credentials)
	TargetPort      int           // Extracted port
	TimeoutDuration time.Duration // Extracted timeout (if applicable)
	HTTPStatus      int           // HTTP/RTSP status code (if applicable)
	RTSPMethod      string        // RTSP method that failed (if applicable)
	// RawProducerOutput stores the sanitized producer stderr/log output for
	// debugging. SECURITY: this field is sanitized by the producer (for FFmpeg,
	// privacy.SanitizeFFmpegError) to remove credentials from stream URLs. The
	// json:"-" tag prevents accidental credential leakage via JSON marshaling.
	RawProducerOutput string    `json:"-"` // Full producer output for debugging (sanitized)
	UserFacingMsg     string    // Friendly message for user
	TroubleShooting   []string  // List of troubleshooting steps
	Timestamp         time.Time // When this error was detected
}

// FormatForConsole renders the user-facing message plus troubleshooting steps
// for console/log output.
func (ctx *StreamErrorContext) FormatForConsole() string {
	var sb strings.Builder

	// User-facing message.
	sb.WriteString(ctx.UserFacingMsg)
	sb.WriteString("\n")

	// Troubleshooting steps.
	if len(ctx.TroubleShooting) > 0 {
		sb.WriteString("\n   Troubleshooting steps:\n")
		for _, step := range ctx.TroubleShooting {
			fmt.Fprintf(&sb, "   • %s\n", step)
		}
	}

	return sb.String()
}

// ShouldOpenCircuit determines if this error should immediately open the circuit breaker.
// Returns true for permanent failures (404, auth, connection refused, DNS errors, etc.)
//
// Network unreachable handling:
// ENETUNREACH is treated as transient because it often occurs during network transitions
// (interface coming up, gateway being configured, switching networks). Unlike EHOSTUNREACH
// (no_route), which indicates a specific routing table problem, ENETUNREACH suggests
// the entire network is currently unavailable but may recover. We allow retry with
// exponential backoff via the existing circuit breaker graduated failure thresholds,
// which will eventually open the circuit if the network remains unreachable.
func (ctx *StreamErrorContext) ShouldOpenCircuit() bool {
	switch ctx.ErrorType {
	case ErrTypeRTSP404, ErrTypeAuthFailed, ErrTypeAuthForbidden, ErrTypeConnectionRefused,
		ErrTypeNoRoute, ErrTypeProtocolError, ErrTypeDNSResolutionFailed,
		ErrTypeOperationNotPermit, ErrTypeSSLError:
		return true // Permanent failures - require configuration fix.
	case ErrTypeConnectionTimeout, ErrTypeInvalidData, ErrTypeEOF, ErrTypeNetworkUnreachable, ErrTypeRTSP503:
		return false // Transient failures - allow retry with backoff.
	default:
		return false
	}
}

// ShouldRestart determines if this error should trigger an automatic restart.
// Returns true for transient failures that might recover on retry.
//
// Network unreachable is treated as transient with bounded retry:
//   - Allows restart (returns true) to handle network transitions.
//   - Circuit breaker's graduated failure thresholds provide bounded retry.
//   - After circuitBreakerRapidThreshold (5) failures in < 5 seconds, circuit opens.
//   - This prevents infinite restarts while allowing recovery from brief network issues.
func (ctx *StreamErrorContext) ShouldRestart() bool {
	switch ctx.ErrorType {
	case ErrTypeConnectionTimeout, ErrTypeInvalidData, ErrTypeEOF, ErrTypeNetworkUnreachable, ErrTypeRTSP503:
		return true // Transient failures - might recover with bounded retry.
	default:
		return false
	}
}

// StateTransition records a lifecycle transition for the health snapshot's
// StateHistory. From and To are the neutral connection states; FromDetail and
// ToDetail carry the producer's sub-state names (for FFmpeg: idle, backoff,
// circuit_open, etc.) so the legacy process_state history stays byte-identical.
type StateTransition struct {
	From       StreamState
	To         StreamState
	FromDetail string
	ToDetail   string
	Timestamp  time.Time
	Reason     string
}

// FromName returns the legacy process_state name for the From state: the
// producer detail when set, otherwise the fixed StreamState mapping.
func (t *StateTransition) FromName() string {
	return detailOrLegacyName(t.FromDetail, t.From)
}

// ToName returns the legacy process_state name for the To state: the producer
// detail when set, otherwise the fixed StreamState mapping.
func (t *StateTransition) ToName() string {
	return detailOrLegacyName(t.ToDetail, t.To)
}

// detailOrLegacyName returns detail when non-empty, else the state's legacy name.
func detailOrLegacyName(detail string, s StreamState) string {
	if detail != "" {
		return detail
	}
	return s.LegacyProcessName()
}

// Engine labels for StreamHealth.Engine, naming the ingest producer.
const (
	// EngineNative is the pure-Go go-audio-stream producer.
	EngineNative = "native"
	// EngineFFmpeg is the FFmpeg subprocess producer.
	EngineFFmpeg = "ffmpeg"
)

// StreamHealth is the producer-neutral health snapshot for one network stream,
// consumed by the health API and the support dump. State plus StateDetail carry
// the connection state and the producer's sub-state; every other field matches
// the historical ffmpeg.StreamHealth so the API stays unchanged.
type StreamHealth struct {
	// State is the neutral connection lifecycle state.
	State StreamState
	// StateDetail is the producer-specific sub-state name for the legacy
	// process_state API field (FFmpeg: idle, backoff, circuit_open, restarting).
	// Empty means the API falls back to State.LegacyProcessName().
	StateDetail string
	// StateEntered is when the current state was entered.
	StateEntered time.Time
	// Recovery is the producer's recovery intent for the liveness watchdog.
	Recovery RecoveryState
	// RecoveryEntered is when the current Recovery value was entered. It advances
	// only when Recovery changes, not on every state transition, so the liveness
	// watchdog can measure how long a producer has been continuously recovering
	// (bounded by LivenessConfig.ProducerRecoveryCeiling). Zero when the producer
	// does not report recovery intent (FFmpeg).
	RecoveryEntered time.Time

	IsHealthy          bool
	LastDataReceived   time.Time
	RestartCount       int
	Error              error
	TotalBytesReceived int64
	BytesPerSecond     float64
	IsReceivingData    bool
	StateHistory       []StateTransition
	LastErrorContext   *StreamErrorContext
	ErrorHistory       []*StreamErrorContext
	SourceChannels     int

	// Observability fields (additive). The native producer populates them from the
	// go-audio-stream session stats; the FFmpeg producer leaves the RTP-specific
	// values zero, so they omit from the API and support dump under the ffmpeg gate.

	// Engine names the ingest producer ("native" or "ffmpeg").
	Engine string
	// Codec is the decoded source codec label (e.g. "aac-lc", "opus", "pcmu").
	Codec string
	// SourceSampleRate is the codec's native sample rate in Hz before resampling.
	SourceSampleRate int
	// Transport is the negotiated (or configured) stream transport (e.g. "tcp").
	Transport string

	// WireBytesPerSecond and PayloadBytesPerSecond are the wire and RTP-payload
	// data rates from the session stats, distinct from BytesPerSecond (the decoded
	// PCM rate).
	WireBytesPerSecond    float64
	PayloadBytesPerSecond float64

	// Per-session RTP counters, summed across the tracks of the live session.
	Packets    uint64
	SeqGaps    uint64
	Duplicates uint64
	Malformed  uint64
	SSRCResets uint64

	// LastFrameAt is the wall-clock arrival of the most recent media frame.
	LastFrameAt time.Time
	// SenderClockValid reports whether an RTCP Sender Report has supplied a valid
	// RTP-to-wall-clock mapping; SenderClockAge is how old that report is.
	SenderClockValid bool
	SenderClockAge   time.Duration

	// ReconnectAttempt is the current reconnect attempt (0 while connected) and
	// NextRetryIn is the backoff wait before the next attempt.
	ReconnectAttempt int
	NextRetryIn      time.Duration
}

// ProcessStateName returns the legacy process_state string for this snapshot:
// StateDetail when the producer set it, otherwise the fixed State mapping. This
// is the single source of the process_state value the health API and the
// support dump emit, so the FFmpeg-era JSON stays byte-identical.
func (h *StreamHealth) ProcessStateName() string {
	if h.StateDetail != "" {
		return h.StateDetail
	}
	return h.State.LegacyProcessName()
}

// Data rate sliding-window parameters.
const (
	// DataRateWindowSize is the default sliding window over which a DataRateMeter
	// averages the delivered byte rate.
	DataRateWindowSize = 10 * time.Second
	// dataRateMaxSamples caps the number of retained rate samples.
	dataRateMaxSamples = 100
	// dataRateSingleSampleWindow is how recent a lone sample must be for the meter
	// to report its instantaneous byte count rather than zero.
	dataRateSingleSampleWindow = 5 * time.Second
)

// DataRateMeter tracks a delivered-byte rate over a sliding time window so every
// producer reports StreamHealth.BytesPerSecond identically. It is safe for
// concurrent use.
type DataRateMeter struct {
	samples    []dataRateSample
	samplesMu  sync.RWMutex
	windowSize time.Duration
	maxSamples int
}

type dataRateSample struct {
	timestamp time.Time
	bytes     int64
}

// NewDataRateMeter creates a DataRateMeter that averages over windowSize.
func NewDataRateMeter(windowSize time.Duration) *DataRateMeter {
	return &DataRateMeter{
		samples:    make([]dataRateSample, 0, dataRateMaxSamples),
		windowSize: windowSize,
		maxSamples: dataRateMaxSamples,
	}
}

// AddSample records numBytes delivered at the current time and evicts samples
// older than the window.
func (d *DataRateMeter) AddSample(numBytes int64) {
	d.samplesMu.Lock()
	defer d.samplesMu.Unlock()

	now := time.Now()
	d.samples = append(d.samples, dataRateSample{
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

// Rate returns the current data rate in bytes per second.
func (d *DataRateMeter) Rate() float64 {
	d.samplesMu.RLock()
	defer d.samplesMu.RUnlock()

	if len(d.samples) == 0 {
		return 0
	}

	if len(d.samples) == 1 {
		sample := d.samples[0]
		timeSinceSample := time.Since(sample.timestamp)
		if timeSinceSample < dataRateSingleSampleWindow {
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
