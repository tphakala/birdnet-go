package audiocore

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// MetricsCollector provides metrics collection for audiocore components
type MetricsCollector struct {
	metrics       *metrics.AudioCoreMetrics
	mu            sync.RWMutex
	enabled       bool
	queueCapacity int // Configurable queue capacity for depth calculations
}

// globalMetrics is a package-level metrics instance
var (
	globalMetrics     atomic.Pointer[MetricsCollector]
	globalMetricsOnce sync.Once
	metricsLogger     *slog.Logger
)

// InitMetrics initializes the global metrics collector
func InitMetrics(metricsInstance *metrics.AudioCoreMetrics) {
	globalMetricsOnce.Do(func() {
		// Initialize logger
		metricsLogger = logging.ForService("audiocore")
		if metricsLogger == nil {
			metricsLogger = slog.Default()
		}
		metricsLogger = metricsLogger.With("component", "metrics")

		mc := &MetricsCollector{
			metrics:       metricsInstance,
			enabled:       metricsInstance != nil,
			queueCapacity: 100, // Default capacity, can be overridden
		}
		globalMetrics.Store(mc)

		if metricsInstance != nil {
			metricsLogger.Info("metrics collector initialized")
		} else {
			metricsLogger.Debug("metrics collector disabled")
		}
	})
}

// GetMetrics returns the global metrics collector
func GetMetrics() *MetricsCollector {
	mc := globalMetrics.Load()
	if mc == nil {
		// Return a no-op collector if metrics not initialized
		return &MetricsCollector{enabled: false}
	}
	return mc
}

// SetQueueCapacity sets the queue capacity for metrics calculations
func (mc *MetricsCollector) SetQueueCapacity(capacity int) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.queueCapacity = capacity
}

// RecordManagerMetrics records metrics for the audio manager
func (mc *MetricsCollector) RecordManagerMetrics(managerID string, managerMetrics *ManagerMetrics) {
	if !mc.enabled || mc.metrics == nil {
		return
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	mc.metrics.UpdateActiveSources(managerID, managerMetrics.ActiveSources)
}

// RecordFrameProcessed records a successfully processed frame
func (mc *MetricsCollector) RecordFrameProcessed(managerID, sourceID string, duration time.Duration) {
	if !mc.enabled || mc.metrics == nil {
		return
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	mc.metrics.RecordProcessedFrame(managerID, sourceID)
	if duration > 0 {
		mc.metrics.RecordProcessingDuration(managerID, sourceID, duration.Seconds())
	}
}

// RecordFrameDropped records a dropped frame
func (mc *MetricsCollector) RecordFrameDropped(sourceID, reason string) {
	if !mc.enabled || mc.metrics == nil {
		return
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	mc.metrics.RecordAudioDataDropped(sourceID, reason)

	if metricsLogger != nil {
		metricsLogger.Warn("audio frame dropped",
			"source_id", sourceID,
			"reason", reason)
	}
}

// RecordProcessingError records a processing error
func (mc *MetricsCollector) RecordProcessingError(managerID, sourceID, errorType string) {
	if !mc.enabled || mc.metrics == nil {
		return
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	mc.metrics.RecordProcessingError(managerID, sourceID, errorType)
}

// RecordSourceStart records a source start event
func (mc *MetricsCollector) RecordSourceStart(sourceID, sourceType string, success bool) {
	if !mc.enabled || mc.metrics == nil {
		return
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	status := "success"
	if !success {
		status = "failure"
	}
	mc.metrics.RecordSourceStart(sourceID, sourceType, status)

	if metricsLogger != nil && metricsLogger.Enabled(context.TODO(), slog.LevelDebug) {
		metricsLogger.Debug("source start recorded",
			"source_id", sourceID,
			"source_type", sourceType,
			"status", status)
	}
}

// RecordSourceStop records a source stop event
func (mc *MetricsCollector) RecordSourceStop(sourceID, sourceType string, success bool) {
	if !mc.enabled || mc.metrics == nil {
		return
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	status := "success"
	if !success {
		status = "failure"
	}
	mc.metrics.RecordSourceStop(sourceID, sourceType, status)
}

// RecordSourceError records a source error
func (mc *MetricsCollector) RecordSourceError(sourceID, sourceType, errorType string) {
	if !mc.enabled || mc.metrics == nil {
		return
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	mc.metrics.RecordSourceError(sourceID, sourceType, errorType)

	if metricsLogger != nil {
		metricsLogger.Info("source error recorded",
			"source_id", sourceID,
			"source_type", sourceType,
			"error_type", errorType)
	}
}

// UpdateSourceGain updates the gain level metric for a source
func (mc *MetricsCollector) UpdateSourceGain(sourceID, sourceType string, gain float64) {
	if !mc.enabled || mc.metrics == nil {
		return
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	mc.metrics.UpdateSourceGainLevel(sourceID, sourceType, gain)
}

// RecordBufferPoolStats records buffer pool statistics for a specific tier
func (mc *MetricsCollector) RecordBufferPoolStats(tier string, stats BufferPoolStats) {
	if !mc.enabled || mc.metrics == nil {
		return
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	// Update buffer pool metrics based on stats for the specific tier
	mc.metrics.UpdateBuffersInUse(tier, stats.ActiveBuffers)
}

// RecordBufferAllocation records a buffer allocation
func (mc *MetricsCollector) RecordBufferAllocation(poolTier string, fromPool bool) {
	if !mc.enabled || mc.metrics == nil {
		return
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	allocationType := "pooled"
	if !fromPool {
		allocationType = "custom"
	}
	mc.metrics.RecordBufferAllocation(poolTier, allocationType)
}

// RecordProcessorExecution records processor execution metrics
func (mc *MetricsCollector) RecordProcessorExecution(processorID, processorType string, duration time.Duration, err error) {
	if !mc.enabled || mc.metrics == nil {
		return
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	status := "success"
	if err != nil {
		status = "error"
		mc.metrics.RecordProcessorError(processorID, processorType, "execution_failed")
	}

	mc.metrics.RecordProcessorExecution(processorID, processorType, status)
	if duration > 0 {
		mc.metrics.RecordProcessorDuration(processorID, processorType, duration.Seconds())
	}
}

// UpdateProcessorChainLength updates the processor chain length metric
func (mc *MetricsCollector) UpdateProcessorChainLength(sourceID string, length int) {
	if !mc.enabled || mc.metrics == nil {
		return
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	mc.metrics.UpdateProcessorChainLength(sourceID, length)
}

// RecordAudioData records audio data metrics
func (mc *MetricsCollector) RecordAudioData(sourceID string, bytes int, duration time.Duration, stage string) {
	if !mc.enabled || mc.metrics == nil {
		return
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	mc.metrics.RecordAudioDataBytes(sourceID, stage, bytes)
	if duration > 0 {
		mc.metrics.RecordAudioDataDuration(sourceID, duration.Seconds())
	}
}

// RecordGainProcessing records gain processing metrics
func (mc *MetricsCollector) RecordGainProcessing(processorID string, gainLevel float64, clippingOccurred bool, sampleFormat string) {
	if !mc.enabled || mc.metrics == nil {
		return
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	mc.metrics.RecordGainLevel(processorID, gainLevel)
	// Determine adjustment type
	adjustmentType := "no_change"
	if gainLevel > 1.0 {
		adjustmentType = "increase"
	} else if gainLevel < 1.0 {
		adjustmentType = "decrease"
	}
	mc.metrics.RecordGainAdjustment(processorID, adjustmentType)

	if clippingOccurred {
		mc.metrics.RecordGainClippingEvent(processorID, sampleFormat)
	}
}

// RecordQueueSubmission records a successful submission to the results queue
func (mc *MetricsCollector) RecordQueueSubmission(sourceID string) {
	if !mc.enabled || mc.metrics == nil {
		return
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	// Record as a processed frame since queue submission represents successful processing
	// Use "queue" as manager_id to distinguish from other processing metrics
	mc.metrics.RecordProcessedFrame("queue", sourceID)
	
	if metricsLogger != nil && metricsLogger.Enabled(context.TODO(), slog.LevelDebug) {
		metricsLogger.Debug("queue submission recorded",
			"source_id", sourceID)
	}
}

// UpdateQueueDepth updates the current queue depth metric
func (mc *MetricsCollector) UpdateQueueDepth(depth, capacity int) {
	if !mc.enabled || mc.metrics == nil {
		return
	}

	mc.mu.RLock()
	defer mc.mu.RUnlock()

	// Use provided capacity or fall back to configured default
	if capacity <= 0 {
		capacity = mc.queueCapacity
	}

	// Calculate utilization percentage
	utilization := float64(depth) / float64(capacity) * 100.0
	
	// TODO: This is a temporary workaround using RecordAudioDataBytes to track queue depth.
	// The queue depth is a count (number of items), not bytes, which creates a semantic mismatch.
	// A more appropriate metric recording method should be implemented in the future to accurately
	// represent queue depth and utilization metrics specifically for queues.
	mc.metrics.RecordAudioDataBytes("queue", "depth", depth)
	
	// Log warning if queue is above threshold (configurable, default 80%)
	warningThreshold := 80.0
	if metricsLogger != nil && utilization > warningThreshold {
		metricsLogger.Warn("results queue depth high",
			"depth", depth,
			"capacity", capacity,
			"utilization_percent", utilization)
	}
}
