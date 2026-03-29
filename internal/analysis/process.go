// process.go provides the BirdNET analysis pipeline entry point.
// ProcessData converts PCM audio to float32, runs BirdNET inference,
// and enqueues results for downstream processing.
package analysis

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
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
	float32Pool         *Float32Pool            // Global pool for float32 conversion buffers
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

// overrunTracker is the package-level tracker instance.
var overrunTracker bufferOverrunTracker

// lastQueueOverflowReport tracks the last time a queue overflow was reported to Sentry.
var lastQueueOverflowReport atomic.Int64

// recordBufferOverrun records a buffer overrun event and reports to Sentry
// when the tumbling window expires with enough accumulated overruns.
func recordBufferOverrun(elapsed, bufferLen time.Duration) {
	overrunTracker.mu.Lock()
	defer overrunTracker.mu.Unlock()

	now := time.Now()

	// Initialize window on first overrun
	if overrunTracker.windowStart.IsZero() {
		overrunTracker.windowStart = now
	}

	// Check if window has expired
	if now.Sub(overrunTracker.windowStart) >= bufferOverrunReportCooldown {
		// Window expired — report if threshold met
		if overrunTracker.overrunCount >= bufferOverrunMinCount {
			reportBufferOverruns(
				overrunTracker.overrunCount,
				overrunTracker.maxElapsed,
				overrunTracker.bufferLength,
				now.Sub(overrunTracker.windowStart),
			)
		}
		// Reset window regardless of whether we reported
		overrunTracker.overrunCount = 0
		overrunTracker.maxElapsed = 0
		overrunTracker.windowStart = now
	}

	// Record this overrun
	overrunTracker.overrunCount++
	if elapsed > overrunTracker.maxElapsed {
		overrunTracker.maxElapsed = elapsed
		overrunTracker.bufferLength = bufferLen
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

// InitFloat32Pool initializes the global float32 pool for audio conversion.
// This should be called during application startup.
func InitFloat32Pool() error {
	var err error
	float32Pool, err = NewFloat32Pool(Float32BufferSize)
	if err != nil {
		return fmt.Errorf("failed to initialize float32 pool: %w", err)
	}

	return nil
}

// ReturnFloat32Buffer returns a float32 buffer to the pool if possible.
// This should be called after the buffer is no longer needed.
func ReturnFloat32Buffer(buffer []float32) {
	if float32Pool != nil && len(buffer) == Float32BufferSize {
		float32Pool.Put(buffer)
	}
}

// ProcessData processes the given audio data to detect bird species, logs the detected species
// and optionally saves the audio clip if a bird species is detected above the configured threshold.
func ProcessData(bn *classifier.BirdNET, data []byte, startTime, audioCapturedAt time.Time, source string) error {
	log := GetLogger()
	// get current time to track processing time
	predictStart := time.Now()

	// convert audio data to float32
	sampleData, err := convertToFloat32WithPool(data, conf.BitDepth)
	if err != nil {
		return errors.New(err).
			Component("analysis").
			Category(errors.CategoryAudio).
			Context("operation", "pcm_to_float32").
			Context("bit_depth", fmt.Sprintf("%d", conf.BitDepth)).
			Build()
	}

	// run BirdNET inference
	inferenceStart := time.Now()
	results, err := bn.Predict(sampleData)
	inferenceDuration := time.Since(inferenceStart)

	// Return float32 buffer to pool after prediction
	// This is safe because Predict copies the data to the input tensor
	if conf.BitDepth == 16 && len(sampleData) > 0 && len(sampleData[0]) == Float32BufferSize {
		ReturnFloat32Buffer(sampleData[0])
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
		recordBufferOverrun(elapsedTime, effectiveBufferDuration)

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

// convertToFloat32WithPool converts a byte slice representing samples to a 2D slice of float32 samples.
// For 16-bit audio, it uses the float32 pool when available for reduced allocations.
// For other bit depths, it delegates to audiocore/convert.ConvertToFloat32.
func convertToFloat32WithPool(sample []byte, bitDepth int) ([][]float32, error) {
	if bitDepth == 16 {
		return [][]float32{convert16BitToFloat32WithPool(sample)}, nil
	}
	// Delegate non-16-bit conversions to audiocore/convert
	return convert.ConvertToFloat32(sample, bitDepth)
}

// convert16BitToFloat32WithPool converts 16-bit samples to float32, using the pool when available.
func convert16BitToFloat32WithPool(sample []byte) []float32 {
	length := len(sample) / 2

	// Try to get buffer from pool if available
	var float32Data []float32
	if float32Pool != nil && length == Float32BufferSize {
		float32Data = float32Pool.Get()
	} else {
		// Fallback to allocation for non-standard sizes or if pool not initialized
		float32Data = make([]float32, length)
	}

	divisor := float32(32768.0)

	for i := range length {
		sample := int16(sample[i*2]) | int16(sample[i*2+1])<<8
		float32Data[i] = float32(sample) / divisor
	}

	return float32Data
}
