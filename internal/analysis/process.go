// process.go provides the BirdNET analysis pipeline entry point.
// ProcessData converts PCM audio to float32, runs BirdNET inference,
// and enqueues results for downstream processing.
package analysis

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/audiocore/convert"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

var processMetrics atomic.Pointer[metrics.MyAudioMetrics]

// embUnavailableLogged throttles the "embeddings enabled but model can't extract"
// warning to once per enable-session, PER MODEL. Keyed by model ID so a capable
// model does not suppress the warning for a concurrent incapable model. The flag
// for a model is cleared when that model produces an embedding or runs with
// embeddings disabled, so re-enabling logs once more.
var embUnavailableLogged sync.Map // modelID(string) -> *atomic.Bool

// shouldLogEmbeddingUnavailable reports whether to emit the unavailable warning
// now for the given model. dim == 0 means the active model cannot extract embeddings.
func shouldLogEmbeddingUnavailable(modelID string, dim int) bool {
	v, _ := embUnavailableLogged.LoadOrStore(modelID, &atomic.Bool{})
	flag := v.(*atomic.Bool)
	if dim == 0 {
		return flag.CompareAndSwap(false, true)
	}
	flag.Store(false)
	return false
}

// resetEmbeddingUnavailableLog clears the per-model throttle so re-enabling
// embeddings logs the unavailable warning once more if the model still cannot extract.
func resetEmbeddingUnavailableLog(modelID string) {
	if v, ok := embUnavailableLogged.Load(modelID); ok {
		v.(*atomic.Bool).Store(false)
	}
}

const (
	// Float32BufferSize is the number of float32 samples in a standard buffer.
	// For 16-bit audio: conf.BufferSize / 2 (bytes per sample) = 144384 samples.
	Float32BufferSize = conf.BufferSize / 2

	// bufferOverrunReportCooldown is the tumbling window duration for aggregating overruns.
	// Hardware constraints don't change dynamically, so 1 hour keeps max 24 events/day per device.
	bufferOverrunReportCooldown = 1 * time.Hour

	// bufferOverrunMinCount is the minimum number of overruns in a window before reporting.
	bufferOverrunMinCount = 10
)

// bufferOverrunTracker tracks BirdNET processing buffer overruns using a tumbling window.
// When the window expires and enough overruns have accumulated, a single Sentry event is sent.
type bufferOverrunTracker struct {
	mu           sync.Mutex
	source       string
	modelID      string
	overrunCount int64
	windowStart  time.Time
	maxElapsed   time.Duration
	bufferLength time.Duration
}

// overrunTrackers maps "source:modelID" to per-model overrun trackers.
var (
	overrunTrackers   map[string]*bufferOverrunTracker
	overrunTrackersMu sync.Mutex
)

func init() {
	overrunTrackers = make(map[string]*bufferOverrunTracker)
}

// getOverrunTracker returns the overrun tracker for the given source and model,
// creating one if it doesn't exist yet.
func getOverrunTracker(source, modelID string) *bufferOverrunTracker {
	key := source + ":" + modelID
	overrunTrackersMu.Lock()
	defer overrunTrackersMu.Unlock()
	if t, ok := overrunTrackers[key]; ok {
		return t
	}
	t := &bufferOverrunTracker{source: source, modelID: modelID}
	overrunTrackers[key] = t
	return t
}

// CleanupOverrunTrackers removes tracker entries that have been idle longer than maxAge.
func CleanupOverrunTrackers(maxAge time.Duration) {
	now := time.Now()
	overrunTrackersMu.Lock()
	defer overrunTrackersMu.Unlock()
	for key, tracker := range overrunTrackers {
		tracker.mu.Lock()
		idle := !tracker.windowStart.IsZero() && now.Sub(tracker.windowStart) > maxAge
		noActivity := tracker.windowStart.IsZero() && tracker.overrunCount == 0
		tracker.mu.Unlock()
		if idle || noActivity {
			delete(overrunTrackers, key)
		}
	}
}

// ResetOverrunTrackers removes all tracker entries. Called when audio sources
// are reconfigured to prevent stale entries from accumulating.
func ResetOverrunTrackers() {
	overrunTrackersMu.Lock()
	clear(overrunTrackers)
	overrunTrackersMu.Unlock()
}

// RemoveOverrunTrackers removes all tracker entries for the given source ID.
// Called when a source is removed to prevent unbounded map growth.
func RemoveOverrunTrackers(sourceID string) {
	prefix := sourceID + ":"
	overrunTrackersMu.Lock()
	defer overrunTrackersMu.Unlock()
	for key := range overrunTrackers {
		if strings.HasPrefix(key, prefix) {
			delete(overrunTrackers, key)
		}
	}
}

// lastQueueOverflowReport tracks the last time a queue overflow was reported to Sentry.
var lastQueueOverflowReport atomic.Int64

// recordBufferOverrun records a buffer overrun event and reports to Sentry
// when the tumbling window expires with enough accumulated overruns.
func recordBufferOverrun(tracker *bufferOverrunTracker, elapsed, bufferLen time.Duration) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	now := time.Now()

	// Initialize window on first overrun
	if tracker.windowStart.IsZero() {
		tracker.windowStart = now
	}

	// Check if window has expired
	if now.Sub(tracker.windowStart) >= bufferOverrunReportCooldown {
		// Window expired; report if threshold met
		if tracker.overrunCount >= bufferOverrunMinCount {
			reportBufferOverruns(
				tracker.source,
				tracker.modelID,
				tracker.overrunCount,
				tracker.maxElapsed,
				tracker.bufferLength,
				now.Sub(tracker.windowStart),
			)
		}
		// Reset window regardless of whether we reported
		tracker.overrunCount = 0
		tracker.maxElapsed = 0
		tracker.windowStart = now
	}

	// Record this overrun
	tracker.overrunCount++
	if elapsed > tracker.maxElapsed {
		tracker.maxElapsed = elapsed
		tracker.bufferLength = bufferLen
	}
}

// reportBufferOverruns sends a rate-limited Sentry event with overrun statistics.
func reportBufferOverruns(source, modelID string, count int64, maxElapsed, bufferLen, window time.Duration) {
	if !telemetry.IsTelemetryEnabled() {
		return
	}

	extras := map[string]any{
		"source":                   source,
		"model_id":                 modelID,
		"overrun_count":            count,
		"max_elapsed_ms":           maxElapsed.Milliseconds(),
		"buffer_length_ms":         bufferLen.Milliseconds(),
		"reporting_window_minutes": int(window.Minutes()),
	}
	telemetry.FastCaptureMessageWithExtras(
		"sustained BirdNET processing buffer overruns detected",
		sentry.LevelWarning,
		"analysis",
		extras,
	)
}

// SetProcessMetrics sets the metrics instance for audio processing operations.
// Thread-safe; only the first call wins (subsequent calls are no-ops).
func SetProcessMetrics(myAudioMetrics *metrics.MyAudioMetrics) {
	processMetrics.CompareAndSwap(nil, myAudioMetrics)
}

// ProcessData processes the given audio data to detect bird species, logs the
// detected species and optionally saves the audio clip if a bird species is
// detected above the configured threshold.
//
// ctx is propagated to the model inference call for cancellation and deadlines.
// bufMgr is the audiocore buffer manager owning the Float32Pool that the
// 16-bit conversion hot path draws from. Must be non-nil; callers that reach
// ProcessData without a manager have a plumbing bug.
func ProcessData(ctx context.Context, bn *classifier.Orchestrator, bufMgr *buffer.Manager, data []byte, startTime, audioCapturedAt time.Time, source, modelID string) error {
	if bufMgr == nil {
		return errors.Newf("buffer manager must not be nil").
			Component("analysis").
			Category(errors.CategoryValidation).
			Context("operation", "process_data").
			Build()
	}
	log := GetLogger()
	// get current time to track processing time
	predictStart := time.Now()

	// convert audio data to float32
	sampleData, err := convertToFloat32WithPool(bufMgr, data, conf.BitDepth)
	if err != nil {
		return errors.New(err).
			Component("analysis").
			Category(errors.CategoryAudio).
			Context("operation", "pcm_to_float32").
			Context("bit_depth", fmt.Sprintf("%d", conf.BitDepth)).
			Build()
	}

	// Defer pool return so the buffer is reclaimed even if PredictModel panics.
	// The Manager's lazy per-size pool map routes the slice back to the pool
	// sized for its actual length, so non-standard sizes are handled too.
	//
	// INVARIANT: bn.PredictModel must copy the samples into the model's
	// input tensor before returning; it must not retain a reference to
	// sampleData past Predict. The pool may hand the same backing array to
	// another caller as soon as Put returns, so any retained reference
	// would become a use-after-free. Classifier backends plugged into the
	// Orchestrator are required to honour this contract; see
	// internal/classifier for the implementing side.
	if conf.BitDepth == 16 && len(sampleData) > 0 {
		if pool := bufMgr.Float32PoolFor(len(sampleData[0])); pool != nil {
			defer pool.Put(sampleData[0])
		}
	}

	// Run inference on the specified model via the Orchestrator.
	// Snapshot settings once per call (read live every window for hot-reload,
	// but consistently within the window) and reuse the snapshot below.
	settings := conf.Setting()
	embEnabled := settings.Embeddings.Enabled

	var (
		results   []datastore.Results
		embedding []float32
	)
	inferenceStart := time.Now()
	if embEnabled {
		results, embedding, err = bn.PredictModelWithEmbeddings(ctx, modelID, sampleData)
	} else {
		results, err = bn.PredictModel(ctx, modelID, sampleData)
		// Reset the per-model throttle so re-enabling logs once more if the model is still incapable.
		resetEmbeddingUnavailableLog(modelID)
	}
	inferenceDuration := time.Since(inferenceStart)

	// Record inference duration metric (always, even on error)
	pm := processMetrics.Load()
	if pm != nil {
		pm.RecordAudioInferenceDuration(source, inferenceDuration.Seconds())
	}

	if err != nil {
		return errors.New(err).
			Component("analysis").
			Category(errors.CategoryAudioAnalysis).
			Context("operation", "birdnet_predict").
			Build()
	}

	log.Debug("ProcessData inference complete",
		logger.String("source", source),
		logger.String("model_id", modelID),
		logger.Int("result_count", len(results)),
		logger.Duration("inference_duration", inferenceDuration),
		logger.Int("sample_bytes", len(data)))

	if embEnabled {
		if shouldLogEmbeddingUnavailable(modelID, len(embedding)) {
			log.Warn("embeddings enabled but active model cannot extract them; needs an ONNX embeddings model",
				logger.String("model_id", modelID))
		} else if len(embedding) > 0 && settings.BirdNET.Debug {
			log.Debug("embedding extracted",
				logger.String("model_id", modelID),
				logger.Int("dim", len(embedding)))
		}
	}

	// get elapsed time (includes conversion + inference for overrun check)
	elapsedTime := time.Since(predictStart)

	// Record result count metric
	if pm != nil {
		pm.RecordBirdNETResults(source, len(results))
	}

	// DEBUG print all BirdNET results
	if settings.BirdNET.Debug {
		debugThreshold := float32(0) // set to 0 for now, maybe add a config option later
		hasHighConfidenceResults := false
		for _, result := range results {
			if result.Confidence > debugThreshold {
				hasHighConfidenceResults = true
				break
			}
		}

		if hasHighConfidenceResults {
			log.Debug("birdnet results",
				logger.String("source", source))
			for _, result := range results {
				if result.Confidence > debugThreshold {
					log.Debug("birdnet result",
						logger.Float64("confidence", float64(result.Confidence)),
						logger.String("species", result.Species))
				}
			}
		}
	}

	// Derive the analysis buffer interval from the model's spec. If
	// inference exceeds this interval the pipeline falls behind real-time.
	effectiveBufferDuration := 3 * time.Second / 2 // fallback: BirdNET v2.4
	if spec, ok := bn.ModelSpecFor(modelID); ok {
		effectiveBufferDuration = spec.BufferInterval()
	}

	if elapsedTime > effectiveBufferDuration {
		log.Warn("processing time exceeded buffer interval",
			logger.Duration("elapsed_time", elapsedTime),
			logger.Duration("buffer_interval", effectiveBufferDuration),
			logger.String("model_id", modelID),
			logger.String("source", source))
		recordBufferOverrun(getOverrunTracker(source, modelID), elapsedTime, effectiveBufferDuration)

		m := processMetrics.Load()
		if m != nil {
			m.RecordBirdNETProcessingOverrun(source, elapsedTime.Seconds(), effectiveBufferDuration.Seconds())
		}
	}

	// Build AudioSource for the Results message.
	// Source metadata (display name, safe string) will be enriched by the
	// AudioEngine's source registry in a future refactor. For now, use the
	// raw source ID which is sufficient for detection processing.
	audioSource := datastore.AudioSource{
		ID:          source,
		SafeString:  source,
		DisplayName: source,
	}

	// The data slice is owned by the pooled AnalysisBuffer.Read path and is
	// returned to its BytePool as soon as ProcessData returns (via the
	// monitor's defer release()). The queue consumer retains PCMdata through
	// filter evaluation and clip export, which outlive ProcessData. Without
	// this copy a subsequent Read could hand the same backing array to a new
	// caller while the consumer goroutine is still reading from it.
	pcmCopy := make([]byte, len(data))
	copy(pcmCopy, data)

	// Create a Results message to be sent through queue to processor
	resultsMessage := classifier.Results{
		StartTime:       startTime,
		AudioCapturedAt: audioCapturedAt,
		ElapsedTime:     elapsedTime,
		PCMdata:         pcmCopy,
		Results:         results,
		Source:          audioSource,
		ModelID:         modelID,
		Embeddings:      embedding, // nil when disabled or unavailable
	}

	// Send the results to the queue. PCMdata is the independently owned copy
	// made above; per classifier.ResultsQueue's ownership contract the sender
	// must not mutate it after the send.
	select {
	case classifier.ResultsQueue <- resultsMessage:
		log.Debug("ProcessData queued results",
			logger.String("source", source),
			logger.String("model_id", modelID),
			logger.Int("result_count", len(results)))
		if pm != nil {
			pm.RecordAudioQueueOperation(source, "enqueue", "success")
		}
	default:
		log.Error("results queue is full",
			logger.String("source", source))
		if pm != nil {
			pm.RecordAudioQueueOperation(source, "enqueue", "dropped")
		}
		// Rate-limit queue overflow telemetry to prevent Sentry floods under sustained backpressure.
		now := time.Now().Unix()
		last := lastQueueOverflowReport.Load()
		if telemetry.IsTelemetryEnabled() && (now-last >= int64(bufferOverrunReportCooldown.Seconds())) {
			if lastQueueOverflowReport.CompareAndSwap(last, now) {
				telemetry.FastCaptureMessageWithExtras(
					"results queue full, detections dropped",
					sentry.LevelWarning,
					"analysis",
					map[string]any{
						"source":     source,
						"queue_size": len(classifier.ResultsQueue),
					},
				)
			}
		}
	}
	return nil
}

func convertToFloat32WithPool(bufMgr *buffer.Manager, sample []byte, bitDepth int) ([][]float32, error) {
	if bitDepth == 16 {
		return [][]float32{convert16BitToFloat32WithPool(bufMgr, sample)}, nil
	}
	// Delegate non-16-bit conversions to audiocore/convert (no pooling needed here;
	// non-16-bit paths are cold and handled separately by PR B2).
	return convert.ConvertToFloat32(sample, bitDepth)
}

// convert16BitToFloat32WithPool converts 16-bit PCM samples to float32 using
// the size-specific pool from bufMgr. The Manager lazily creates a per-size
// Float32Pool on first use, so any length the pipeline produces is pooled.
func convert16BitToFloat32WithPool(bufMgr *buffer.Manager, sample []byte) []float32 {
	length := len(sample) / 2
	pool := bufMgr.Float32PoolFor(length)

	var float32Data []float32
	if pool != nil {
		float32Data = pool.Get()
	} else {
		// length == 0 (or exceedingly rare pool construction failure) falls through to a fresh slice.
		float32Data = make([]float32, length)
	}

	const divisor = float32(32768.0)
	for i := range length {
		s := int16(sample[i*2]) | int16(sample[i*2+1])<<8
		float32Data[i] = float32(s) / divisor
	}
	return float32Data
}
