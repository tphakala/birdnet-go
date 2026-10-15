package processor

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference/vad"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

const (
	// vadLoadBackoff throttles retries after a failed VAD session load so a
	// missing model or absent ONNX Runtime library logs at most once per
	// interval instead of once per chunk.
	vadLoadBackoff = 30 * time.Second
	// vadSourceThrottle paces speech decisions per (source, model) stream: fresh
	// audio from every accepted chunk is buffered into the stream's streamer, but
	// the streamer is flushed (one inference over the accumulated fresh hops, one
	// gate decision) at most once per this span of chunk audio end time. This
	// keeps ORT dispatch at ~1 call/s/stream while every hop of audio is still
	// scored exactly once.
	vadSourceThrottle = time.Second
	// vadBytesPerSample is the size of one 16-bit PCM sample in the analysis
	// chunks the gate receives.
	vadBytesPerSample = 2
	// vadResetGap is how much a chunk's audio-end delta must exceed the chunk
	// duration before the gate treats it as a genuine discontinuity (dropped audio
	// or a source restart) and resets the streamer. Chunk audio ends are derived
	// from wall-clock write times and poll-quantized (~100 ms), not
	// sample-derived, so a tolerance well above that scheduling jitter keeps a
	// contiguous boundary at the default zero overlap (delta ~= chunkDur) from
	// being mistaken for a gap and thrashing the LSTM state every chunk.
	vadResetGap = time.Second
	// vadCoverageJoinTolerance is the largest time hole between two consecutive
	// chunks (audio-end delta minus chunk duration) that still counts as
	// contiguous scored audio, for producers that report no stream positions
	// (with positions, adjacency is judged on the positions instead). A larger hole, short of vadResetGap, keeps the LSTM
	// state but starts a new coverage span, because the audio inside the hole was
	// never scored. A hole under this tolerance is timing jitter; speech cannot
	// fit entirely inside one, though a syllable fragment can.
	vadCoverageJoinTolerance = 150 * time.Millisecond
	// vadMinThreshold and vadMaxThreshold clamp a misconfigured gate threshold.
	vadMinThreshold = 0.01
	vadMaxThreshold = 1.0
	// vadHitCap bounds the recent-speech-hit history feed shown on the dashboard.
	// Half the model cards' lastDetectionCap (20), since the VAD is a single stream.
	vadHitCap = 10
)

// vadStreamKey identifies one VAD stream: the audio of one analysis monitor,
// i.e. one source analyzed by one model. A stream sees one sample rate, one clip
// length and one FIFO timeline, so adding, removing or reloading another model
// on the same source never resets or re-times it.
type vadStreamKey struct {
	source string
	model  string
}

// vadScore is the result of one VAD inference for a chunk.
type vadScore struct {
	prob float32
	dur  time.Duration
	// span is the contiguous audio time the chunk's stream has scored so far,
	// ending at the chunk's audio end.
	span audioSpan
}

// vadGate lazily owns the shared Silero VAD session and the per-(source, model) streamers used by the privacy filter.
//
// All access is from the single results-consumer goroutine plus Shutdown. The
// mutex is held across the ENTIRE inference call (not just the session-pointer
// read) so a concurrent Shutdown cannot free the native ONNX session while
// inference is in flight, which would crash the ORT runtime.
type vadGate struct {
	mu sync.Mutex
	// newSession builds the one shared ONNX session for a config. Injectable so
	// tests can supply a fake without a real ONNX model; defaults to vad.NewSession.
	newSession func(vad.Config) (vad.SpeechSession, error)
	// newStreamer builds a session-less per-stream streamer. Injectable so tests
	// can supply a fake; defaults to a vad.NewStreamer with the default sustain.
	newStreamer func() vad.Streamer
	// sess is the one ONNX session shared across every source's streamer. The
	// LSTM state is threaded through each Flush, so a single session (and its ORT
	// thread pool) serves all streams. nil until the first successful load.
	sess vad.SpeechSession
	// streams holds one lazily-built streamer per (source, model) plus the
	// per-stream chunk/flush bookkeeping that drives overlap decoupling.
	streams map[vadStreamKey]*sourceStream
	// attemptedKey is the model-source cache key of the last load attempt
	// (success OR failure). A change in the requested key drops the shared session,
	// every streamer, and the failure backoff.
	attemptedKey string
	lastAttempt  time.Time
	failed       bool

	// Observability counters. These are lifetime totals for the dashboard and
	// are read lock-free from the API goroutine, so they are atomics rather than
	// mu-guarded fields; they are never reset when the session unloads (a source
	// change or a feature toggle), mirroring Prometheus counter semantics.
	invocations    atomic.Int64
	totalUs        atomic.Int64
	maxUs          atomic.Int64
	speechHits     atomic.Int64
	lastSpeechUnix atomic.Int64
	lastProbBits   atomic.Uint32 // math.Float32bits of the last speech-hit probability
	// snapshot describes the currently loaded session (nil when unloaded), so the
	// dashboard can report strategy/source/sample-rate without taking g.mu.
	snapshot atomic.Pointer[vadConfigSnapshot]
	// warnedNoModel makes the "enabled but no model available" log one-shot; it is
	// reset to false when the session loads so a later misconfiguration warns again.
	warnedNoModel atomic.Bool
	// recentHits is a newest-first ring of the last vadHitCap speech hits for the
	// dashboard's history feed. Guarded by histMu (a dedicated lock, not g.mu) so a
	// dashboard read never blocks on an in-flight inference holding g.mu.
	histMu     sync.Mutex
	recentHits []VADSpeechHit
}

// vadConfigSnapshot captures the session-bound facts of the currently loaded
// VAD model for the inference dashboard. It is replaced atomically on load and
// set to nil on unload.
type vadConfigSnapshot struct {
	strategy   string
	source     string // "embedded" or "path" (the coarse label, never the on-disk path)
	sampleRate int
}

// sourceStream is one (source, model) stream's streamer plus the chunk
// bookkeeping that turns overlapping analysis chunks into a single
// non-overlapped stream. The streamer holds no ONNX session; it is flushed
// against the gate's shared session.
type sourceStream struct {
	s vad.Streamer
	// lastEnd is the audio end of the last accepted chunk for this stream.
	lastEnd time.Time
	// lastFlush is the audio end at the last completed flush (decision); paired
	// with hasFlushed so the very first chunk flushes immediately.
	lastFlush  time.Time
	hasFlushed bool
	// sampleRate is the stream's PCM rate; a change is a discontinuity that
	// resets the streamer.
	sampleRate int
	// covFrom is the start of this stream's contiguous scored audio; a flush
	// publishes [covFrom, audioEnd] as covered. covPos is the stream position of
	// covFrom when the producer reports positions.
	covFrom time.Time
	covPos  int64
	// lastEndPos and lastGen are the producer's end position and generation of the
	// last accepted chunk, to tell a position hole from contiguous audio.
	lastEndPos int64
	lastGen    uint64
}

// newVADGate creates an empty gate; the shared session and per-stream streamers
// load lazily on first use.
func newVADGate() *vadGate {
	return &vadGate{
		newSession:  vad.NewSession,
		newStreamer: func() vad.Streamer { return vad.NewStreamer(0) },
		streams:     make(map[vadStreamKey]*sourceStream),
	}
}

// loaded reports whether the shared VAD session is currently held. It reads an
// atomic snapshot, so it never blocks on an in-flight inference.
func (g *vadGate) loaded() bool { return g.snapshot.Load() != nil }

// close releases the shared session and every streamer. It is idempotent and
// safe to call concurrently with score (the shared mutex serialises them, so it
// blocks until any in-flight inference completes).
func (g *vadGate) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closeAllLocked()
}

// closeAllLocked releases the shared session and drops every streamer; the
// caller must hold g.mu.
func (g *vadGate) closeAllLocked() {
	if g.sess != nil {
		_ = g.sess.Close()
		g.sess = nil
		// Only a real loaded -> unloaded transition reaches here, so log it once
		// (feature toggled off, model source changed, or an inference error drop).
		g.snapshot.Store(nil)
		GetLogger().Info("privacy VAD model unloaded",
			logger.String("operation", "privacy_filter_vad"))
	}
	clear(g.streams)
}

// score scores one chunk against the shared session built from cfg, lazily
// loading (or reloading when cacheKey changes) that session and the chunk's
// (source, model) streamer. cacheKey identifies the model source (e.g.
// "embedded" or "path:<file>"); an empty cacheKey means no model is available.
// It returns the inference result (probability, duration, scored span) and
// ran=true only when a fresh inference was performed; ran=false means no model is available, a load is in its failure
// backoff window, the chunk was buffered without a flush this interval (the
// throttle cadence), it was an out-of-order or duplicate chunk, or the flush had
// no complete hop yet. When ran is true, span is the contiguous audio time this
// stream has scored so far, ending at the chunk's audio end, with the stream
// positions it covers when the producer reports them. The audio appended
// for the chunk is at least ch.freshBytes when the producer reports it. A non-nil error
// indicates a session load or inference failure (an inference failure tears the
// session down; a per-stream data error drops only that streamer).
func (g *vadGate) score(cfg *vad.Config, cacheKey string, ch *chunkAudio) (result vadScore, ran bool, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// The model source changed (or was cleared): drop the shared session and every
	// streamer and clear a prior load-failure backoff, so a corrected model source
	// is retried immediately instead of waiting out the failed source's backoff.
	if cacheKey != g.attemptedKey {
		g.closeAllLocked()
		g.failed = false
		g.attemptedKey = cacheKey
	}
	if cacheKey == "" {
		return vadScore{}, false, nil
	}

	if g.sess == nil {
		if g.failed && time.Since(g.lastAttempt) < vadLoadBackoff {
			return vadScore{}, false, nil
		}
		g.lastAttempt = time.Now()
		sess, newErr := g.newSession(*cfg)
		if newErr != nil {
			g.failed = true
			return vadScore{}, false, newErr
		}
		g.sess = sess
		g.failed = false
		// One-shot positive confirmation the model is up, with the facts an
		// operator needs to tell the strategy and embedded from an on-disk override
		// (journalctl -u birdnet-go | grep privacy_filter_vad).
		g.snapshot.Store(&vadConfigSnapshot{
			strategy:   sess.Strategy(),
			source:     vadSourceLabel(cacheKey),
			sampleRate: vad.SampleRate,
		})
		g.warnedNoModel.Store(false)
		GetLogger().Info("privacy VAD model loaded",
			logger.String("strategy", sess.Strategy()),
			logger.String("source", vadSourceLabel(cacheKey)),
			logger.Int("sample_rate", vad.SampleRate),
			logger.String("operation", "privacy_filter_vad"))
	}

	entry, freshPCM, skip := g.prepareSourceStream(ch)
	if skip {
		return vadScore{}, false, nil
	}
	if appendErr := entry.s.Append(freshPCM, ch.sampleRate); appendErr != nil {
		return vadScore{}, false, appendErr
	}
	// Advance the accepted-chunk timestamp only after Append succeeds, so a failed
	// Append does not leave lastEnd pointing past audio that was never buffered
	// (which would make the next chunk's fresh-tail slice drop real audio).
	if ch.span.to.After(entry.lastEnd) {
		entry.lastEnd = ch.span.to
	}
	entry.lastEndPos, entry.lastGen = ch.endPos, ch.generation

	// Flush (a decision) at most once per throttle span per stream; between
	// flushes fresh audio is only buffered.
	//
	// Note: fresh audio Appended within the throttle window stays buffered until
	// the next flush (~1 s), which scores it. If a source stops entirely right
	// after a throttled chunk, the last sub-second of buffered audio is not
	// scored; that bounded stream-end tail is the accepted cost of pacing ONNX
	// dispatch to ~1 call/s/stream on constrained hosts.
	if entry.hasFlushed && ch.span.to.Sub(entry.lastFlush) < vadSourceThrottle {
		return vadScore{}, false, nil
	}

	start := time.Now()
	prob, ok, _, flushErr := entry.s.Flush(g.sess)
	dur := time.Since(start)
	if flushErr != nil {
		if isInferenceError(flushErr) {
			// An ONNX inference failure likely reflects a bad session state. Drop the
			// shared session and every streamer and enter the failure backoff so
			// repeated failures do not re-run inference (and log a warning) on every
			// chunk; the session is rebuilt once the backoff elapses.
			g.closeAllLocked()
			g.failed = true
			g.lastAttempt = time.Now()
		} else {
			// A per-stream data or resampling error: the shared session is healthy,
			// so drop only this stream's streamer (discarding its bad buffer) and
			// keep serving every other stream rather than tearing the gate down.
			entry.s.Reset()
			delete(g.streams, ch.key)
		}
		return vadScore{}, false, flushErr
	}
	if !ok {
		// No complete hop was available yet; the cadence clock does not advance so
		// the next chunk can flush as soon as a hop accumulates.
		return vadScore{}, false, nil
	}
	entry.lastFlush = ch.span.to
	entry.hasFlushed = true
	g.recordInference(dur)
	span := audioSpan{from: entry.covFrom, to: ch.span.to}
	if ch.hasPositions() {
		span.startPos, span.endPos, span.gen = entry.covPos, ch.endPos, ch.generation
	}
	return vadScore{prob: prob, dur: dur, span: span}, true, nil
}

// prepareSourceStream returns the chunk's stream, the non-overlapped tail of
// this chunk to append, and skip=true when the chunk carries no new audio (an
// out-of-order or duplicate chunk). It resets the streamer on a discontinuity (a
// producer gap flag, a sample-rate change or a coverage gap larger than one
// chunk plus vadResetGap), restarts the coverage span at the start of the
// chunk's contiguous tail on a gap or on any unscored hole (a stream position
// hole, or a time hole beyond vadCoverageJoinTolerance when the producer reports
// no positions), and leaves lastEnd for the caller to advance after
// Append succeeds. All timing is on the chunk's audio end, which does not move
// with the PreCapture setting. The fresh audio appended is at
// least the producer's exact fresh byte count, never less, and with stream
// positions it is exactly what was added since the last accepted chunk, so
// timing jitter in the audio end cannot drop audio the span goes on to claim. The caller must hold g.mu.
func (g *vadGate) prepareSourceStream(ch *chunkAudio) (entry *sourceStream, freshPCM []byte, skip bool) {
	chunkDur := pcmChunkDuration(len(ch.pcm), ch.sampleRate)
	chunkStart := ch.span.from
	entry, ok := g.streams[ch.key]
	if !ok {
		// The first window of a buffer has a zero-filled overlap head: scored, but
		// not real audio, so only the contiguous tail is claimed as covered.
		entry = &sourceStream{s: g.newStreamer(), sampleRate: ch.sampleRate, covFrom: ch.scoredFrom(), covPos: ch.tailStartPos()}
		g.streams[ch.key] = entry
		return entry, ch.pcm, false
	}

	delta := ch.span.to.Sub(entry.lastEnd)
	switch {
	case ch.gap, ch.sampleRate != entry.sampleRate, delta > chunkDur+vadResetGap:
		// Discontinuity: lost audio, a sample-rate change, or a gap well beyond one
		// chunk (a restart or dropped audio). Reset the LSTM state and aggregation
		// and treat the whole chunk as fresh. The vadResetGap tolerance above
		// chunkDur absorbs the timing jitter in the chunk audio ends, so a
		// contiguous boundary at the default zero overlap (delta ~= chunkDur) is
		// not mistaken for a gap. A window spliced across lost audio is not
		// contiguous, so a producer gap claims none of it.
		entry.s.Reset()
		entry.sampleRate = ch.sampleRate
		entry.hasFlushed = false
		entry.covFrom, entry.covPos = ch.scoredFrom(), ch.tailStartPos()
		freshPCM = ch.pcm
	case delta <= 0 && ch.freshBytes <= 0:
		// Out-of-order or duplicate chunk: its audio was already appended when the
		// earlier chunk arrived, so there is nothing new to score. A chunk whose
		// producer reports fresh bytes is never a duplicate: delta can be zero or
		// negative from timing jitter while the window still carries new audio.
		return entry, nil, true
	default:
		if g.hasUnscoredHole(entry, ch, delta-chunkDur) {
			// A hole the LSTM state survives but the scorer never saw: the audio
			// before this chunk is not contiguous with what was scored.
			entry.covPos = ch.tailStartPos()
			entry.covFrom = chunkStart
			if ch.hasPositions() {
				entry.covFrom = ch.scoredFrom()
			}
		}
		// Overlap or a contiguous boundary: only the tail past the previous chunk's
		// end is new audio. freshTailBytes rounds to whole samples and clamps to
		// the whole chunk, so a contiguous chunk (delta ~= chunkDur) yields the
		// whole chunk with no dropped leading sample and preserves the LSTM state.
		//
		// The producer's exact fresh byte count is a floor: audio-end deltas carry
		// arrival jitter, and a delta shorter than the true step would drop real
		// audio from scoring while the span still claims it. The delta can only add
		// to the floor, which recovers the audio of a window the queue dropped from
		// this window's overlap head. When the producer reports stream positions
		// they replace the delta, so the audio appended is exactly the audio the
		// span claims.
		fresh := freshTailBytes(delta, ch.sampleRate, len(ch.pcm))
		if ch.hasPositions() && ch.generation == entry.lastGen {
			// With stream positions the bytes added since the last accepted chunk
			// are known exactly, whatever the timestamps say.
			fresh = int(min(max(ch.endPos-entry.lastEndPos, 0), int64(len(ch.pcm)))) &^ (vadBytesPerSample - 1)
		}
		if ch.freshBytes > fresh {
			fresh = min(ch.freshBytes, len(ch.pcm)) &^ (vadBytesPerSample - 1)
		}
		freshPCM = ch.pcm[len(ch.pcm)-fresh:]
	}
	return entry, freshPCM, false
}

// hasUnscoredHole reports whether audio between the stream's last accepted chunk
// and the start of ch was never scored. With stream positions it is a position
// hole (or a new stream generation); the producer's positions do not depend on
// timestamp jitter. Without positions it is a time hole beyond
// vadCoverageJoinTolerance (extra is the audio-end delta minus the chunk length).
func (g *vadGate) hasUnscoredHole(entry *sourceStream, ch *chunkAudio, extra time.Duration) bool {
	if ch.hasPositions() {
		return ch.generation != entry.lastGen || ch.tailStartPos() > entry.lastEndPos
	}
	return extra > vadCoverageJoinTolerance
}

// pcmChunkDuration returns the wall-clock duration of a 16-bit mono PCM chunk of
// pcmLen bytes at sampleRate Hz.
func pcmChunkDuration(pcmLen, sampleRate int) time.Duration {
	if sampleRate <= 0 {
		return 0
	}
	samples := pcmLen / vadBytesPerSample
	return time.Duration(samples) * time.Second / time.Duration(sampleRate) //nolint:durationcheck // intentional: converts a sample count at a Hz rate into a duration
}

// isInferenceError reports whether err is an ONNX inference/model failure (which
// warrants tearing down and rebuilding the shared session) rather than a
// per-source data or resampling error (which leaves the session healthy). The
// VAD session wraps its Run failures with CategoryModelLoad; resampling and
// validation failures carry other categories.
func isInferenceError(err error) bool {
	if ee, ok := errors.AsType[*errors.EnhancedError](err); ok {
		return ee.GetCategory() == string(errors.CategoryModelLoad)
	}
	return false
}

// freshTailBytes converts the audio-end delta between two overlapping chunks
// into the byte length of the newest (non-overlapped) tail of the current chunk,
// clamped to [0, pcmLen] and aligned to whole PCM samples.
func freshTailBytes(delta time.Duration, sampleRate, pcmLen int) int {
	if delta <= 0 {
		return 0
	}
	// Round to the nearest whole sample so a contiguous chunk (delta ~= chunkDur)
	// maps to the whole chunk rather than truncating a leading sample off it.
	freshSamples := int((delta*time.Duration(sampleRate) + time.Second/2) / time.Second) //nolint:durationcheck // intentional: converts a duration at a Hz rate into a rounded sample count
	freshBytes := freshSamples * vadBytesPerSample
	if freshBytes > pcmLen {
		return pcmLen
	}
	return freshBytes
}

// runVADGate runs the dedicated Silero VAD speech gate for one chunk and, on a
// speech hit, records it in LastHumanDetection exactly as the label-based path
// does, stamped with the end of the audio it scored (the stored time never moves
// backward, see recordHumanDetection). This AUGMENTS the label
// match: shouldDiscardDetection consults the same map, so a bird detection is
// discarded if EITHER the VAD or the label trigger fires. When the VAD is
// disabled or its model is absent, behaviour is unchanged.
//
// It returns the span of audio the chunk's (source, model) stream has scored
// when this call ran an inference (ok=true); the caller publishes it as coverage
// once the chunk's label hits are also written (publishPrivacyCoverage publishes
// nothing for a chunk with unknown timing). The chunk's fresh byte count sets how
// much of its audio is scored.
func (p *Processor) runVADGate(settings *conf.Settings, chunk *chunkAudio) (span audioSpan, ok bool) {
	pf := &settings.Realtime.PrivacyFilter
	if !pf.Enabled || !pf.VAD.Enabled {
		// Lazily unload if the feature was just turned off (no restart needed).
		p.vadGate.close()
		return audioSpan{}, false
	}

	cfg, cacheKey := resolveVADModel(&pf.VAD, settings.BirdNET.ONNXRuntimePath)
	if cacheKey == "" {
		// Enabled but no model resolves. The embedded model is compiled into every
		// build and an unset modelpath falls through to it, so this branch is now
		// effectively unreachable and stays only as defensive handling. If it is ever
		// hit, unload any held session so a removed model source does not leak its ONNX
		// session, then warn once so the misconfiguration is visible rather than
		// silently inert.
		p.vadGate.close()
		if p.vadGate.warnedNoModel.CompareAndSwap(false, true) {
			GetLogger().Warn("privacy VAD enabled but no model available; speech gate inert",
				logger.String("hint", "set realtime.privacyfilter.vad.modelpath to a Silero sequence-export .onnx, or unset it to use the embedded model"),
				logger.String("operation", "privacy_filter_vad"))
		}
		return audioSpan{}, false
	}

	result, ran, err := p.vadGate.score(&cfg, cacheKey, chunk)
	if err != nil {
		GetLogger().Warn("privacy VAD unavailable; speech gate inactive this interval",
			logger.String("error", err.Error()),
			logger.String("model_path", pf.VAD.ModelPath),
			logger.String("operation", "privacy_filter_vad"))
		return audioSpan{}, false
	}
	if !ran {
		return audioSpan{}, false
	}

	// Prometheus (opt-in via telemetry). The dashboard stats above are always on;
	// these mirror them for scraping when telemetry is enabled. The recorder
	// methods are nil-safe, so only the group pointer needs a guard.
	if settings.Realtime.Telemetry.Enabled && p.Metrics != nil {
		p.Metrics.PrivacyFilter.ObserveVADInference(result.dur.Seconds())
	}

	source := chunk.key.source
	threshold := clampVADThreshold(pf.VAD.Threshold)
	if pf.Debug {
		GetLogger().Debug("privacy VAD scored chunk",
			logger.Float32("speech_probability", result.prob),
			logger.Float64("threshold", threshold),
			logger.String("source", p.getDisplayNameForSource(source)),
			logger.String("operation", "privacy_filter_vad"))
	}
	if float64(result.prob) < threshold {
		return result.span, true
	}

	GetLogger().Info("human speech detected by VAD; privacy filter engaged",
		logger.Float32("speech_probability", result.prob),
		logger.Float64("threshold", threshold),
		logger.String("source", p.getDisplayNameForSource(source)),
		logger.String("operation", "privacy_filter_vad"))

	audioEnd := chunk.span.to
	p.vadGate.recordSpeechHit(audioEnd, result.prob, p.getDisplayNameForSource(source))
	if settings.Realtime.Telemetry.Enabled && p.Metrics != nil {
		p.Metrics.PrivacyFilter.RecordVADSpeechHit()
		p.Metrics.PrivacyFilter.SetVADLastSpeechProbability(float64(result.prob))
	}

	p.recordHumanDetection(source, audioEnd, metrics.TriggerVAD)
	return result.span, true
}

// resolveVADModel selects the VAD model source. An explicit ModelPath override
// takes precedence; otherwise the model embedded in the binary is used. It
// returns the session config and a stable cache key ("path:<file>" or
// "embedded") used to detect a source change; an empty key means no model source
// is available (only reachable if the embedded model is absent and no modelpath
// is configured).
func resolveVADModel(cfg *conf.VADSettings, libraryPath string) (detCfg vad.Config, cacheKey string) {
	if cfg.ModelPath != "" {
		return vad.Config{ModelPath: cfg.ModelPath, LibraryPath: libraryPath}, "path:" + cfg.ModelPath
	}
	if data := vad.EmbeddedModelData(); len(data) > 0 {
		return vad.Config{ModelData: data, LibraryPath: libraryPath}, "embedded"
	}
	return vad.Config{}, ""
}

// clampVADThreshold bounds a configured gate threshold to a sane range so a
// misconfigured value cannot disable the gate (0) or make it unreachable (>1).
func clampVADThreshold(v float64) float64 {
	if v < vadMinThreshold {
		return vadMinThreshold
	}
	if v > vadMaxThreshold {
		return vadMaxThreshold
	}
	return v
}
