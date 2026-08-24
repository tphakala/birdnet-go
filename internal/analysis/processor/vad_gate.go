package processor

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/inference/vad"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

const (
	// vadLoadBackoff throttles retries after a failed VAD detector load so a
	// missing model or absent ONNX Runtime library logs at most once per
	// interval instead of once per chunk.
	vadLoadBackoff = 30 * time.Second
	// vadSourceThrottle deduplicates overlapping multi-model chunks: each source
	// is scored at most once per this span of chunk start time.
	vadSourceThrottle = time.Second
	// vadMinThreshold and vadMaxThreshold clamp a misconfigured gate threshold.
	vadMinThreshold = 0.01
	vadMaxThreshold = 1.0
	// vadHitCap bounds the recent-speech-hit history feed shown on the dashboard.
	// Half the model cards' lastDetectionCap (20), since the VAD is a single stream.
	vadHitCap = 10
)

// vadGate lazily owns the Silero VAD detector used by the privacy filter.
//
// All access is from the single results-consumer goroutine plus Shutdown. The
// mutex is held across the ENTIRE inference call (not just the detector-pointer
// read) so a concurrent Shutdown cannot free the native ONNX session while
// inference is in flight, which would crash the ORT runtime.
type vadGate struct {
	mu sync.Mutex
	// newDetector builds a detector for a config. Injectable so tests can supply
	// a fake without a real ONNX model; defaults to vad.New.
	newDetector func(vad.Config) (vad.Detector, error)
	det         vad.Detector
	// attemptedKey is the model-source cache key of the last load attempt
	// (success OR failure). A change in the requested key drops any stale
	// detector and clears the failure backoff.
	attemptedKey string
	lastAttempt  time.Time
	failed       bool
	lastRun      map[string]time.Time

	// Observability counters. These are lifetime totals for the dashboard and
	// are read lock-free from the API goroutine, so they are atomics rather than
	// mu-guarded fields; they are never reset when the detector unloads (a source
	// change or a feature toggle), mirroring Prometheus counter semantics.
	invocations    atomic.Int64
	totalUs        atomic.Int64
	maxUs          atomic.Int64
	speechHits     atomic.Int64
	lastSpeechUnix atomic.Int64
	lastProbBits   atomic.Uint32 // math.Float32bits of the last speech-hit probability
	// snapshot describes the currently loaded detector (nil when unloaded), so the
	// dashboard can report strategy/source/sample-rate without taking g.mu.
	snapshot atomic.Pointer[vadConfigSnapshot]
	// warnedNoModel makes the "enabled but no model available" log one-shot; it is
	// reset to false when a detector loads so a later misconfiguration warns again.
	warnedNoModel atomic.Bool
	// recentHits is a newest-first ring of the last vadHitCap speech hits for the
	// dashboard's history feed. Guarded by histMu (a dedicated lock, not g.mu) so a
	// dashboard read never blocks on an in-flight inference holding g.mu.
	histMu     sync.Mutex
	recentHits []VADSpeechHit
}

// vadConfigSnapshot captures the detector-bound facts of the currently loaded
// VAD detector for the inference dashboard. It is replaced atomically on load
// and set to nil on unload.
type vadConfigSnapshot struct {
	strategy   string
	source     string // "embedded" or "path" (the coarse label, never the on-disk path)
	sampleRate int
}

// newVADGate creates an empty gate; the detector loads lazily on first use.
func newVADGate() *vadGate {
	return &vadGate{newDetector: vad.New, lastRun: make(map[string]time.Time)}
}

// close releases the detector. It is idempotent and safe to call concurrently
// with score (the shared mutex serialises them, so it blocks until any in-flight
// inference completes).
func (g *vadGate) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closeLocked()
}

// closeLocked releases the detector; the caller must hold g.mu.
func (g *vadGate) closeLocked() {
	if g.det != nil {
		_ = g.det.Close()
		g.det = nil
		// Only a real loaded -> unloaded transition reaches here, so log it once
		// (feature toggled off, model source changed, or an inference error drop).
		g.snapshot.Store(nil)
		GetLogger().Info("privacy VAD model unloaded",
			logger.String("operation", "privacy_filter_vad"))
	}
}

// score scores one chunk with the detector built from cfg, lazily loading (or
// reloading when cacheKey changes) the detector. cacheKey identifies the model
// source (e.g. "embedded" or "path:<file>"); an empty cacheKey means no model is
// available. It returns the speech probability and ran=true only when a fresh
// inference was performed; ran=false means the call was throttled, no model is
// available, or a load is in its failure backoff window. A non-nil error
// indicates a detector load or inference failure.
func (g *vadGate) score(cfg *vad.Config, cacheKey string, pcm []byte, sourceID string, startTime time.Time, sampleRate int) (prob float32, ran bool, dur time.Duration, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Per-source throttle on chunk start time: overlapping chunks from multiple
	// models covering the same audio window are scored once.
	last, seen := g.lastRun[sourceID]
	if seen && startTime.Sub(last) >= 0 && startTime.Sub(last) < vadSourceThrottle {
		return 0, false, 0, nil
	}

	// The model source changed (or was cleared): drop any stale detector and
	// clear a prior load-failure backoff, so a corrected model source is retried
	// immediately instead of waiting out the failed source's backoff window.
	if cacheKey != g.attemptedKey {
		g.closeLocked()
		g.failed = false
		g.attemptedKey = cacheKey
	}
	if cacheKey == "" {
		return 0, false, 0, nil
	}

	if g.det == nil {
		if g.failed && time.Since(g.lastAttempt) < vadLoadBackoff {
			return 0, false, 0, nil
		}
		g.lastAttempt = time.Now()
		det, newErr := g.newDetector(*cfg)
		if newErr != nil {
			g.failed = true
			return 0, false, 0, newErr
		}
		g.det = det
		g.failed = false
		// One-shot positive confirmation the model is up, with the facts an
		// operator needs to tell recurrent from segment-batched and embedded from
		// an on-disk override (journalctl -u birdnet-go | grep privacy_filter_vad).
		g.snapshot.Store(&vadConfigSnapshot{
			strategy:   det.Strategy(),
			source:     vadSourceLabel(cacheKey),
			sampleRate: sampleRate,
		})
		g.warnedNoModel.Store(false)
		GetLogger().Info("privacy VAD model loaded",
			logger.String("strategy", det.Strategy()),
			logger.String("source", vadSourceLabel(cacheKey)),
			logger.Int("sample_rate", sampleRate),
			logger.String("operation", "privacy_filter_vad"))
	}

	start := time.Now()
	prob, err = g.det.SpeechProbability(pcm, sampleRate)
	dur = time.Since(start)
	if err != nil {
		// An inference failure likely reflects a bad session state. Drop the
		// detector and enter the same failure backoff as a failed load, so
		// repeated failures do not re-run inference (and log a warning) on every
		// chunk; the detector is rebuilt once the backoff elapses.
		g.closeLocked()
		g.failed = true
		g.lastAttempt = time.Now()
		return 0, false, 0, err
	}
	// Advance the throttle timestamp only forward, so an out-of-order (earlier)
	// chunk cannot regress it and trigger a cascade of redundant inferences.
	if !seen || startTime.After(last) {
		g.lastRun[sourceID] = startTime
	}
	g.recordInference(dur)
	return prob, true, dur, nil
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
		// Enabled but no model resolves (a noembed build with no modelpath set):
		// warn once so the misconfiguration is visible rather than silently inert.
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
// returns the detector config and a stable cache key ("path:<file>" or
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
