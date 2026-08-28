package processor

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/classifier"
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
	// vadSourceThrottle paces speech decisions per source: fresh audio from every
	// accepted chunk is buffered into the source's streamer, but the streamer is
	// flushed (one inference over the accumulated fresh hops, one gate decision)
	// at most once per this span of chunk start time. This keeps ORT dispatch at
	// ~1 call/s/source while every hop of audio is still scored exactly once.
	vadSourceThrottle = time.Second
	// vadBytesPerSample is the size of one 16-bit PCM sample in the analysis
	// chunks the gate receives.
	vadBytesPerSample = 2
	// vadResetGap is how much a chunk's start-time delta must exceed the chunk
	// duration before the gate treats it as a genuine discontinuity (dropped audio
	// or a source restart) and resets the streamer. Chunk start times are
	// wall-clock and poll-quantized (~100 ms), not sample-derived, so a tolerance
	// well above that scheduling jitter keeps a contiguous boundary at the default
	// zero overlap (delta ~= chunkDur) from being mistaken for a gap and thrashing
	// the LSTM state every chunk.
	vadResetGap = time.Second
	// vadMinThreshold and vadMaxThreshold clamp a misconfigured gate threshold.
	vadMinThreshold = 0.01
	vadMaxThreshold = 1.0
	// vadHitCap bounds the recent-speech-hit history feed shown on the dashboard.
	// Half the model cards' lastDetectionCap (20), since the VAD is a single stream.
	vadHitCap = 10
)

// vadGate lazily owns the shared Silero VAD session and the per-source streamers used by the privacy filter.
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
	// newStreamer builds a session-less per-source streamer. Injectable so tests
	// can supply a fake; defaults to a vad.NewStreamer with the default sustain.
	newStreamer func() vad.Streamer
	// sess is the one ONNX session shared across every source's streamer. The
	// LSTM state is threaded through each Flush, so a single session (and its ORT
	// thread pool) serves all sources. nil until the first successful load.
	sess vad.SpeechSession
	// streams holds one lazily-built streamer per audio source plus the per-source
	// chunk/flush bookkeeping that drives overlap decoupling.
	streams map[string]*sourceStream
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

// sourceStream is one audio source's streamer plus the chunk bookkeeping that
// turns overlapping analysis chunks into a single non-overlapped stream. The
// streamer holds no ONNX session; it is flushed against the gate's shared session.
type sourceStream struct {
	s vad.Streamer
	// lastStart is the start time of the last accepted chunk for this source.
	lastStart time.Time
	// lastFlush is the start time of the last completed flush (decision); paired
	// with hasFlushed so the very first chunk flushes immediately.
	lastFlush  time.Time
	hasFlushed bool
	// sampleRate is the source's PCM rate; a change is a discontinuity that resets
	// the streamer.
	sampleRate int
}

// newVADGate creates an empty gate; the shared session and per-source streamers
// load lazily on first use.
func newVADGate() *vadGate {
	return &vadGate{
		newSession:  vad.NewSession,
		newStreamer: func() vad.Streamer { return vad.NewStreamer(0) },
		streams:     make(map[string]*sourceStream),
	}
}

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
// loading (or reloading when cacheKey changes) that session and the per-source
// streamer. cacheKey identifies the model source (e.g. "embedded" or
// "path:<file>"); an empty cacheKey means no model is available. It returns the
// speech probability and ran=true only when a fresh inference was performed;
// ran=false means no model is available, a load is in its failure backoff
// window, the chunk was buffered without a flush this interval (the throttle
// cadence), it was an out-of-order or duplicate chunk, or the flush had no
// complete hop yet. A non-nil error indicates a session load or inference
// failure (an inference failure tears the session down; a per-source data error
// drops only that streamer).
func (g *vadGate) score(cfg *vad.Config, cacheKey string, pcm []byte, sourceID string, startTime time.Time, sampleRate int) (prob float32, ran bool, dur time.Duration, err error) {
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
		return 0, false, 0, nil
	}

	if g.sess == nil {
		if g.failed && time.Since(g.lastAttempt) < vadLoadBackoff {
			return 0, false, 0, nil
		}
		g.lastAttempt = time.Now()
		sess, newErr := g.newSession(*cfg)
		if newErr != nil {
			g.failed = true
			return 0, false, 0, newErr
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

	entry, freshPCM, skip := g.prepareSourceStream(sourceID, pcm, startTime, sampleRate)
	if skip {
		return 0, false, 0, nil
	}
	if appendErr := entry.s.Append(freshPCM, sampleRate); appendErr != nil {
		return 0, false, 0, appendErr
	}
	// Advance the accepted-chunk timestamp only after Append succeeds, so a failed
	// Append does not leave lastStart pointing past audio that was never buffered
	// (which would make the next chunk's fresh-tail slice drop real audio).
	entry.lastStart = startTime

	// Flush (a decision) at most once per throttle span per source; between
	// flushes fresh audio is only buffered.
	//
	// Note: fresh audio Appended within the throttle window stays buffered until
	// the next flush (~1 s), which scores it. If a source stops entirely right
	// after a throttled chunk, the last sub-second of buffered audio is not
	// scored; that bounded stream-end tail is the accepted cost of pacing ONNX
	// dispatch to ~1 call/s/source on constrained hosts.
	if entry.hasFlushed && startTime.Sub(entry.lastFlush) < vadSourceThrottle {
		return 0, false, 0, nil
	}

	start := time.Now()
	prob, ok, _, flushErr := entry.s.Flush(g.sess)
	dur = time.Since(start)
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
			// A per-source data or resampling error: the shared session is healthy,
			// so drop only this source's streamer (discarding its bad buffer) and
			// keep serving every other source rather than tearing the gate down.
			entry.s.Reset()
			delete(g.streams, sourceID)
		}
		return 0, false, 0, flushErr
	}
	if !ok {
		// No complete hop was available yet; the cadence clock does not advance so
		// the next chunk can flush as soon as a hop accumulates.
		return 0, false, 0, nil
	}
	entry.lastFlush = startTime
	entry.hasFlushed = true
	g.recordInference(dur)
	return prob, true, dur, nil
}

// prepareSourceStream returns the source's streamer, the non-overlapped tail of
// this chunk to append, and skip=true when the chunk carries no new audio (an
// out-of-order or duplicate chunk). It resets the streamer on a discontinuity
// (a sample-rate change or a coverage gap larger than one chunk) and advances
// the source's last-chunk timestamp. The caller must hold g.mu.
func (g *vadGate) prepareSourceStream(sourceID string, pcm []byte, startTime time.Time, sampleRate int) (entry *sourceStream, freshPCM []byte, skip bool) {
	entry, ok := g.streams[sourceID]
	if !ok {
		entry = &sourceStream{s: g.newStreamer(), sampleRate: sampleRate}
		g.streams[sourceID] = entry
		return entry, pcm, false
	}

	chunkDur := pcmChunkDuration(len(pcm), sampleRate)
	delta := startTime.Sub(entry.lastStart)
	switch {
	case sampleRate != entry.sampleRate, delta > chunkDur+vadResetGap:
		// Discontinuity: a sample-rate change, or a gap well beyond one chunk (a
		// restart or dropped audio). Reset the LSTM state and aggregation and treat
		// the whole chunk as fresh. The vadResetGap tolerance above chunkDur absorbs
		// the wall-clock jitter in the chunk start times, so a contiguous boundary
		// at the default zero overlap (delta ~= chunkDur) is not mistaken for a gap.
		entry.s.Reset()
		entry.sampleRate = sampleRate
		entry.hasFlushed = false
		freshPCM = pcm
	case delta <= 0:
		// Out-of-order or duplicate chunk: its audio was already appended when the
		// earlier chunk arrived, so there is nothing new to score.
		return entry, nil, true
	default:
		// Overlap or a contiguous boundary: only the tail past the previous chunk's
		// start is new audio. freshTailBytes rounds to whole samples and clamps to
		// the whole chunk, so a contiguous chunk (delta ~= chunkDur) yields the
		// whole chunk with no dropped leading sample and preserves the LSTM state.
		freshPCM = pcm[len(pcm)-freshTailBytes(delta, sampleRate, len(pcm)):]
	}
	return entry, freshPCM, false
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
	var ee *errors.EnhancedError
	if errors.As(err, &ee) {
		return ee.GetCategory() == string(errors.CategoryModelLoad)
	}
	return false
}

// freshTailBytes converts the start-time delta between two overlapping chunks
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
// does. This AUGMENTS the label match: shouldDiscardDetection consults the same
// map, so a bird detection is discarded if EITHER the VAD or the label trigger
// fires. When the VAD is disabled or its model is absent, behaviour is unchanged.
func (p *Processor) runVADGate(settings *conf.Settings, item *classifier.Results) {
	pf := &settings.Realtime.PrivacyFilter
	if !pf.Enabled || !pf.VAD.Enabled {
		// Lazily unload if the feature was just turned off (no restart needed).
		p.vadGate.close()
		return
	}
	// The VAD runs at 16 kHz on speech; skip ultrasonic bat chunks entirely.
	if item.ModelID == classifier.RegistryIDBat || len(item.PCMdata) == 0 {
		return
	}
	spec, ok := classifier.GetModelSpec(item.ModelID)
	if !ok {
		return
	}
	sampleRate := spec.EffectiveSampleRate()
	if sampleRate <= 0 {
		return
	}

	cfg, cacheKey := resolveVADModel(&pf.VAD, settings.BirdNET.ONNXRuntimePath)
	if cacheKey == "" {
		// Enabled but no model resolves (a noembed build with no modelpath set, or
		// a modelpath that was cleared after the session had loaded). Unload any
		// held session so a removed model source does not leak its ONNX session,
		// then warn once so the misconfiguration is visible rather than silently inert.
		p.vadGate.close()
		if p.vadGate.warnedNoModel.CompareAndSwap(false, true) {
			GetLogger().Warn("privacy VAD enabled but no model available; speech gate inert",
				logger.String("hint", "set realtime.privacyfilter.vad.modelpath or use a build with the embedded model"),
				logger.String("operation", "privacy_filter_vad"))
		}
		return
	}

	prob, ran, dur, err := p.vadGate.score(
		&cfg, cacheKey, item.PCMdata, item.Source.ID, item.StartTime, sampleRate,
	)
	if err != nil {
		GetLogger().Warn("privacy VAD unavailable; speech gate inactive this interval",
			logger.String("error", err.Error()),
			logger.String("model_path", pf.VAD.ModelPath),
			logger.String("operation", "privacy_filter_vad"))
		return
	}
	if !ran {
		return
	}

	// Prometheus (opt-in via telemetry). The dashboard stats above are always on;
	// these mirror them for scraping when telemetry is enabled. The recorder
	// methods are nil-safe, so only the group pointer needs a guard.
	if settings.Realtime.Telemetry.Enabled && p.Metrics != nil {
		p.Metrics.PrivacyFilter.ObserveVADInference(dur.Seconds())
	}

	threshold := clampVADThreshold(pf.VAD.Threshold)
	if pf.Debug {
		GetLogger().Debug("privacy VAD scored chunk",
			logger.Float32("speech_probability", prob),
			logger.Float64("threshold", threshold),
			logger.String("source", p.getDisplayNameForSource(item.Source.ID)),
			logger.String("operation", "privacy_filter_vad"))
	}
	if float64(prob) < threshold {
		return
	}

	GetLogger().Info("human speech detected by VAD; privacy filter engaged",
		logger.Float32("speech_probability", prob),
		logger.Float64("threshold", threshold),
		logger.String("source", p.getDisplayNameForSource(item.Source.ID)),
		logger.String("operation", "privacy_filter_vad"))

	p.vadGate.recordSpeechHit(item.StartTime, prob, p.getDisplayNameForSource(item.Source.ID))
	if settings.Realtime.Telemetry.Enabled && p.Metrics != nil {
		p.Metrics.PrivacyFilter.RecordVADSpeechHit()
		p.Metrics.PrivacyFilter.SetVADLastSpeechProbability(float64(prob))
	}

	p.detectionMutex.Lock()
	p.LastHumanDetection[item.Source.ID] = HumanDetection{Time: item.StartTime, Trigger: metrics.TriggerVAD}
	p.detectionMutex.Unlock()
}

// resolveVADModel selects the VAD model source. An explicit ModelPath override
// takes precedence; otherwise the model embedded in the binary is used. It
// returns the session config and a stable cache key ("path:<file>" or
// "embedded") used to detect a source change; an empty key means no model is
// available (a noembed build with no configured path).
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
