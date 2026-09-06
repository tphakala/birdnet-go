package stream

import (
	"context"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/go-audio-stream/supervisor"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// dataRateWindow is the sliding window for the delivered-PCM rate meter.
	dataRateWindow = 5 * time.Second
	// receivingDataThreshold and healthyDataThreshold match the FFmpeg producer's
	// defaultReceivingDataThreshold and defaultHealthyDataThreshold so
	// IsReceivingData and IsHealthy compute identically.
	receivingDataThreshold = 5 * time.Second
	healthyDataThreshold   = 60 * time.Second
	// stateHistoryLen bounds the retained transition history, matching the FFmpeg
	// producer's maxStateHistoryExposed.
	stateHistoryLen = 10
	// errorHistoryLen bounds the retained error context history.
	errorHistoryLen = 10
	// audioOnlyFallbackStreak is how many consecutive audio-only sessions may
	// end without a frame before the stream latches to full-stream SETUP.
	audioOnlyFallbackStreak = 2
)

// stream owns one supervised source: its pipeline, its go-audio-stream
// supervisor, and its neutral health state. Health mutations happen on the
// supervisor goroutine (onState) and the reader goroutine (onDeliver); they do
// not overlap within a session because a session's delivery has stopped before
// its terminal transition fires, but both are guarded so a concurrent snapshot
// read is race-free.
type stream struct {
	spec *audiocore.StreamSpec
	opts *Options
	log  logger.Logger

	pl        *pipeline
	sup       *supervisor.Supervisor
	rateMeter *audiocore.DataRateMeter

	targetHost string
	targetPort int
	// healthyThreshold is the resolved per-source "no data before unhealthy"
	// window: spec.HealthyDataThreshold when set, else healthyDataThreshold,
	// mirroring the FFmpeg producer's StreamConfig.healthyThreshold().
	healthyThreshold time.Duration

	// deliveredSession is set by the reader goroutine when a frame is dispatched
	// and cleared in the RTSP factory at each session start (before routing
	// begins), so the audio-only fallback can tell whether a session ever
	// produced audio.
	deliveredSession  atomic.Bool
	fullStreamLatched atomic.Bool

	mu               sync.Mutex
	state            audiocore.StreamState
	stateDetail      string
	stateEntered     time.Time
	recovery         audiocore.RecoveryState
	recoveryEntered  time.Time
	lastData         time.Time
	totalBytes       int64
	restartCount     int
	reconnectAttempt int
	nextRetryIn      time.Duration
	lastErr          error
	lastErrCtx       *audiocore.StreamErrorContext
	errorHistory     []*audiocore.StreamErrorContext
	stateHistory     []audiocore.StateTransition
	audioOnlyStreak  int

	// Wire/payload byte-rate bookkeeping for the observability snapshot, computed
	// from cumulative session-stats deltas. Guarded by mu.
	lastStatsAt time.Time
	lastWire    uint64
	lastPayload uint64
	wireRate    float64
	payloadRate float64
}

// newStream builds a stream and its supervisor. The supervisor begins connecting
// immediately on a background goroutine, so onDeliver can fire before the first
// health read. deliver forwards dispatched frames into the manager's router
// callback.
func newStream(spec *audiocore.StreamSpec, opts *Options, deliver dispatchFunc, bufMgr *buffer.Manager, log logger.Logger) *stream {
	if log == nil {
		log = audiocore.GetLogger()
	}
	host, port := parseHostPort(spec.URL)
	healthyThreshold := spec.HealthyDataThreshold
	if healthyThreshold <= 0 {
		healthyThreshold = healthyDataThreshold
	}
	s := &stream{
		spec:             spec,
		opts:             opts,
		log:              log,
		rateMeter:        audiocore.NewDataRateMeter(dataRateWindow),
		targetHost:       host,
		targetPort:       port,
		healthyThreshold: healthyThreshold,
		state:            audiocore.StreamStateStarting,
		stateDetail:      detailStarting,
		stateEntered:     time.Now(),
		recovery:         audiocore.RecoveryInProgress,
		recoveryEntered:  time.Now(),
	}

	var pool bytePool
	if bufMgr != nil {
		pool = bufMgr.BytePoolFor(opts.ChunkBytes)
	}
	// The pipeline dispatches through onDeliver, which records liveness before
	// forwarding to the router.
	s.pl = newPipeline(spec, opts.ChunkBytes, pool, func(f audiocore.AudioFrame) {
		s.onDeliver(len(f.Data), f.Timestamp)
		deliver(f)
	}, log)

	s.sup = supervisor.New(supervisor.Config{
		Factory:   s.rtspFactory(),
		Backoff:   opts.Backoff,
		OnState:   s.onState,
		Retryable: s.retryable,
		Logger:    debugSlog(spec.Debug, log),
	})
	if opts.Metrics != nil {
		opts.Metrics.SetStreamEngine(spec.SourceID, audiocore.EngineNative)
	}
	return s
}

// onDeliver records the liveness and byte counters for a dispatched frame of
// dataLen bytes at ts. It runs on the reader goroutine.
func (s *stream) onDeliver(dataLen int, ts time.Time) {
	s.deliveredSession.Store(true)
	n := int64(dataLen)
	s.mu.Lock()
	s.lastData = ts
	s.totalBytes += n
	s.mu.Unlock()
	s.rateMeter.AddSample(n)
	if s.opts.Metrics != nil {
		s.opts.Metrics.RecordDataRate(s.spec.SourceID, s.rateMeter.Rate())
	}
}

// onState maps a supervisor transition onto the neutral health model. It runs on
// the supervisor goroutine, serialized with the reader goroutine by the session
// boundary (a terminated session's delivery has stopped before its terminal
// transition fires).
func (s *stream) onState(sc supervisor.StateChange) {
	state, detail, recovery := mapState(sc)

	switch sc.State {
	case supervisor.StateReconnecting:
		s.pl.flush()
		s.noteSessionEndedWithoutAudio()
	case supervisor.StateClosed, supervisor.StateFailed:
		s.pl.flush()
	case supervisor.StateConnecting, supervisor.StateConnected:
		// StateConnecting adds nothing beyond the health update below.
		// StateConnected: onReset fires once at StartStream (parity with the
		// FFmpeg producer and the shared contract), not per reconnect; the
		// delivered flag is cleared in the factory before routing begins.
	}

	s.mu.Lock()

	if s.state != state || s.stateDetail != detail {
		s.appendHistory(state, detail, causeString(sc.Err))
	}
	s.state = state
	s.stateDetail = detail
	s.stateEntered = time.Now()
	// recoveryEntered advances only when the recovery intent itself changes, not on
	// every transition: the supervisor cycles Connecting<->Reconnecting (both
	// RecoveryInProgress) during one outage, and the liveness watchdog measures how
	// long recovery has been continuously in progress against its ceiling.
	if s.recovery != recovery {
		s.recoveryEntered = time.Now()
	}
	s.recovery = recovery

	errored := false
	switch sc.State {
	case supervisor.StateReconnecting:
		s.restartCount++
		s.reconnectAttempt = sc.Attempt
		s.nextRetryIn = sc.Backoff
		errored = s.recordError(sc.Err)
	case supervisor.StateFailed:
		errored = s.recordError(sc.Err)
	case supervisor.StateConnected:
		s.reconnectAttempt = 0
		s.nextRetryIn = 0
	case supervisor.StateConnecting, supervisor.StateClosed:
		// no counter changes
	}

	s.mu.Unlock()

	// Emit metrics outside s.mu, matching onDeliver and snapshot, so a metrics
	// implementation that reads stream health back cannot deadlock (AB-BA). The
	// error count is emitted before the health gauge to preserve the previous
	// under-lock ordering. state is a local and s.spec.SourceID is immutable, so
	// both are safe to read after the unlock.
	if s.opts.Metrics != nil {
		if errored {
			s.opts.Metrics.IncStreamErrors(s.spec.SourceID)
		}
		s.opts.Metrics.SetStreamHealth(s.spec.SourceID, state == audiocore.StreamStateConnected)
	}
}

// noteSessionEndedWithoutAudio advances the audio-only fallback latch. Only the
// "auto" media mode falls back (matching the FFmpeg path's usesAudioOnlyFallback):
// audio-only never falls back and full-stream already requests video. A session
// that produced audio resets the consecutive streak; a session that produced
// none advances it, and once it reaches the threshold the stream latches to
// full-stream SETUP for its remaining life.
func (s *stream) noteSessionEndedWithoutAudio() {
	if s.fullStreamLatched.Load() {
		return
	}
	if conf.MediaMode(s.spec.MediaMode).Canonical() != conf.MediaModeAuto {
		return
	}
	if s.deliveredSession.Load() {
		s.mu.Lock()
		s.audioOnlyStreak = 0
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	s.audioOnlyStreak++
	streak := s.audioOnlyStreak
	s.mu.Unlock()
	if streak >= audioOnlyFallbackStreak {
		s.fullStreamLatched.Store(true)
		s.log.Info("latching stream to full-stream SETUP after audio-only sessions produced no audio",
			logger.String("source_id", s.spec.SourceID),
			logger.String("operation", "audio_only_fallback"))
	}
}

// retryable wraps the library default, marking the terminal native causes so the
// supervisor stops rather than reconnecting into the same failure. A nil error
// (a clean session end) is not terminal, so it falls through to the default.
func (s *stream) retryable(err error) bool {
	if isTerminalCause(err) {
		return false
	}
	return supervisor.DefaultRetryable(err)
}

// appendHistory records a transition, trimming to the retained window. Caller
// holds mu.
func (s *stream) appendHistory(to audiocore.StreamState, toDetail, reason string) {
	s.stateHistory = append(s.stateHistory, audiocore.StateTransition{
		From:       s.state,
		To:         to,
		FromDetail: s.stateDetail,
		ToDetail:   toDetail,
		Timestamp:  time.Now(),
		Reason:     reason,
	})
	if len(s.stateHistory) > stateHistoryLen {
		s.stateHistory = s.stateHistory[len(s.stateHistory)-stateHistoryLen:]
	}
}

// recordError classifies err and stores it as the last error plus history entry.
// It returns true when an error context was recorded, so the caller can emit the
// IncStreamErrors metric after releasing the lock (the metric must not be emitted
// under s.mu; see onState). Caller holds mu.
func (s *stream) recordError(err error) bool {
	if err == nil {
		return false
	}
	s.lastErr = err
	ctx := classifyError(err, s.targetHost, s.targetPort)
	s.lastErrCtx = ctx
	if ctx == nil {
		return false
	}
	s.errorHistory = append(s.errorHistory, ctx)
	if len(s.errorHistory) > errorHistoryLen {
		s.errorHistory = s.errorHistory[len(s.errorHistory)-errorHistoryLen:]
	}
	return true
}

// snapshot returns a copy of the current health, computing the derived liveness
// fields against the current time. The returned slices are freshly copied so the
// caller may read them without holding the lock.
func (s *stream) snapshot() *audiocore.StreamHealth {
	// Read the live session stats and codec geometry OUTSIDE s.mu: the supervisor
	// may invoke onState (which takes s.mu) while holding its own lock, so calling
	// s.sup.Stats() under s.mu would risk a lock-order inversion.
	stats := s.sup.Stats()
	codec, srcRate, _ := s.pl.codecInfo()
	agg := aggregateTrackStats(stats)

	s.mu.Lock()

	now := time.Now()
	receiving := !s.lastData.IsZero() && now.Sub(s.lastData) < receivingDataThreshold
	healthy := s.state == audiocore.StreamStateConnected &&
		!s.lastData.IsZero() && now.Sub(s.lastData) < s.healthyThreshold

	recomputed := s.updateWireRates(stats.CapturedAt, agg.wire, agg.payload)

	h := &audiocore.StreamHealth{
		State:              s.state,
		StateDetail:        s.stateDetail,
		StateEntered:       s.stateEntered,
		Recovery:           s.recovery,
		RecoveryEntered:    s.recoveryEntered,
		IsHealthy:          healthy,
		LastDataReceived:   s.lastData,
		RestartCount:       s.restartCount,
		Error:              s.lastErr,
		TotalBytesReceived: s.totalBytes,
		BytesPerSecond:     s.rateMeter.Rate(),
		IsReceivingData:    receiving,
		LastErrorContext:   s.lastErrCtx,
		SourceChannels:     s.spec.SourceChannels,

		Engine:                audiocore.EngineNative,
		Codec:                 codec,
		SourceSampleRate:      srcRate,
		Transport:             s.spec.Transport,
		WireBytesPerSecond:    s.wireRate,
		PayloadBytesPerSecond: s.payloadRate,
		Packets:               agg.packets,
		SeqGaps:               agg.seqGaps,
		Duplicates:            agg.duplicates,
		Malformed:             agg.malformed,
		SSRCResets:            agg.ssrcResets,
		LastFrameAt:           agg.lastFrameAt,
		SenderClockValid:      agg.senderClockValid,
		SenderClockAge:        agg.senderClockAge,
		ReconnectAttempt:      s.reconnectAttempt,
		NextRetryIn:           s.nextRetryIn,
	}
	h.StateHistory = slices.Clone(s.stateHistory)
	h.ErrorHistory = slices.Clone(s.errorHistory)
	wireRate := s.wireRate
	s.mu.Unlock()

	// Emit the wire rate outside s.mu, matching onDeliver's RecordDataRate, so a
	// metrics implementation that calls back into the stream cannot deadlock.
	// The wire rate is sampled at health-read cadence (its API/dump consumer);
	// continuous emission would need a dedicated sampler.
	if recomputed && s.opts.Metrics != nil {
		s.opts.Metrics.RecordWireRate(s.spec.SourceID, wireRate)
	}
	return h
}

// minWireRateInterval smooths the wire/payload rate against health-poll cadence:
// the rate is recomputed only when at least this much has elapsed since the last
// sample, so back-to-back snapshots do not divide by a tiny interval.
const minWireRateInterval = 1 * time.Second

// updateWireRates recomputes the wire and payload byte rates from cumulative
// session-stats deltas and reports whether it recomputed this call (so the
// caller can emit the rate to metrics OUTSIDE the lock). The caller holds mu.
func (s *stream) updateWireRates(capturedAt time.Time, wire, payload uint64) bool {
	// A zero capture time (no live session yet) carries no usable delta; ignore it
	// so it never becomes a stale baseline that a later valid sample measures a
	// negative interval against. Mirrors the guard in aggregateTrackStats.
	if capturedAt.IsZero() {
		return false
	}
	if s.lastStatsAt.IsZero() {
		s.lastStatsAt = capturedAt
		s.lastWire = wire
		s.lastPayload = payload
		return false
	}
	dt := capturedAt.Sub(s.lastStatsAt).Seconds()
	if dt < minWireRateInterval.Seconds() {
		return false
	}
	s.wireRate = deltaRate(wire, s.lastWire, dt)
	s.payloadRate = deltaRate(payload, s.lastPayload, dt)
	s.lastStatsAt = capturedAt
	s.lastWire = wire
	s.lastPayload = payload
	return true
}

// deltaRate returns (cur-prev)/dt, guarding a counter reset (cur<prev) or a
// non-positive interval as a zero rate.
func deltaRate(cur, prev uint64, dt float64) float64 {
	if cur < prev || dt <= 0 {
		return 0
	}
	return float64(cur-prev) / dt
}

// close stops the supervisor, waits for it to fully stop so no reader goroutine
// is in flight, then flushes the pipeline's final partial chunk.
func (s *stream) close(ctx context.Context) {
	_ = s.sup.Close()
	// Wait always blocks until the supervisor's reader goroutine has fully
	// stopped (it drains the done channel even when ctx cancels), so pl.close
	// below never races a live reader. ctx bounds only how long Close's own
	// cancellation is given to propagate, so the StopStream/Shutdown timeout is
	// best-effort: a reader wedged in a syscall that ignores cancellation can
	// still exceed it.
	_ = s.sup.Wait(ctx)
	s.pl.close()
}

// parseHostPort extracts the sanitized host and port from a stream URL for the
// error context. Credentials in the userinfo are never returned.
func parseHostPort(raw string) (host string, port int) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", 0
	}
	host = u.Hostname()
	if p := u.Port(); p != "" {
		if n, convErr := strconv.Atoi(p); convErr == nil {
			port = n
		}
	}
	return host, port
}

// causeString renders an error for the transition reason, "" for nil.
func causeString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// isTerminalCause reports whether err is a native failure that a reconnect
// cannot resolve: no audio track, or an undecodable codec. Auth failures stay
// retryable so a rotated password recovers on the next poll.
func isTerminalCause(err error) bool {
	return errors.Is(err, ErrNoAudioTrack) || errors.Is(err, ErrUnsupportedCodec)
}
