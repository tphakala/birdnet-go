// process.go provides the BirdNET analysis pipeline entry point.
// ProcessData converts PCM audio to float32, runs BirdNET inference,
// and enqueues results for downstream processing.
package analysis

import (
	"context"
	"fmt"
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

var (
	processMetrics      *metrics.MyAudioMetrics // Global metrics instance for audio processing operations
	processMetricsMutex sync.RWMutex            // Mutex for thread-safe access to processMetrics
	processMetricsOnce  sync.Once               // Ensures metrics are only set once
)

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
	t := &bufferOverrunTracker{}
	overrunTrackers[key] = t
	return t
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
		// Window expired — report if threshold met
		if tracker.overrunCount >= bufferOverrunMinCount {
			reportBufferOverruns(
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
func reportBufferOverruns(count int64, maxElapsed, bufferLen, window time.Duration) {
	if !telemetry.IsTelemetryEnabled() {
		return
	}

	extras := map[string]any{
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
// This function is thread-safe and ensures metrics are only set once per process lifetime.
// Subsequent calls will be ignored due to sync.Once (idempotent behavior).
func SetProcessMetrics(myAudioMetrics *metrics.MyAudioMetrics) {
	processMetricsOnce.Do(func() {
		processMetricsMutex.Lock()
		defer processMetricsMutex.Unlock()
		processMetrics = myAudioMetrics
	})
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

	// Run inference on the specified model via the Orchestrator.
	inferenceStart := time.Now()
	results, err := bn.PredictModel(ctx, modelID, sampleData)
	inferenceDuration := time.Since(inferenceStart)

	// Return float32 buffer to pool after prediction. The Manager's lazy
	// per-size pool map routes the slice back to the pool sized for its
	// actual length, so non-standard sizes are handled too.
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
			pool.Put(sampleData[0])
		}
	}

	// Record inference duration metric (always, even on error)
	processMetricsMutex.RLock()
	pm := processMetrics
	processMetricsMutex.RUnlock()
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

	// get elapsed time (includes conversion + inference for overrun check)
	elapsedTime := time.Since(predictStart)

	// Record result count metric
	if pm != nil {
		pm.RecordBirdNETResults(source, len(results))
	}

	// DEBUG print all BirdNET results
	if conf.Setting().BirdNET.Debug {
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

	// Get the current settings
	settings := conf.Setting()

	// Calculate the effective buffer duration
	bufferDuration := 3 * time.Second // base duration
	overlapDuration := time.Duration(settings.BirdNET.Overlap * float64(time.Second))
	effectiveBufferDuration := bufferDuration - overlapDuration

	// Check if processing time exceeds effective buffer duration
	if elapsedTime > effectiveBufferDuration {
		log.Warn("BirdNET processing time exceeded buffer length",
			logger.Duration("elapsed_time", elapsedTime),
			logger.Duration("buffer_length", effectiveBufferDuration),
			logger.String("source", source))
		recordBufferOverrun(getOverrunTracker(source, modelID), elapsedTime, effectiveBufferDuration)

		// Record Prometheus metrics for observability
		processMetricsMutex.RLock()
		m := processMetrics
		processMetricsMutex.RUnlock()
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

	// Create a Results message to be sent through queue to processor
	resultsMessage := classifier.Results{
		StartTime:       startTime,
		AudioCapturedAt: audioCapturedAt,
		ElapsedTime:     elapsedTime,
		PCMdata:         data,
		Results:         results,
		Source:          audioSource,
		ModelID:         modelID,
	}

	// Send the results to the queue
	// Note: No copy needed - ownership transfers to the queue consumer
	select {
	case classifier.ResultsQueue <- resultsMessage:
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
