// Package metrics provides audiocore metrics for observability
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// AudioCoreMetrics contains Prometheus metrics for audiocore operations
type AudioCoreMetrics struct {
	registry *prometheus.Registry

	// Manager metrics
	activeSources      *prometheus.GaugeVec
	processedFrames    *prometheus.CounterVec
	processingErrors   *prometheus.CounterVec
	processingDuration *prometheus.HistogramVec

	// Source metrics
	sourceStartTotal    *prometheus.CounterVec
	sourceStopTotal     *prometheus.CounterVec
	sourceErrors        *prometheus.CounterVec
	sourceDataRate      *prometheus.GaugeVec
	sourceUptime        *prometheus.GaugeVec
	sourceGainLevel     *prometheus.GaugeVec

	// Buffer pool metrics
	bufferPoolSize      *prometheus.GaugeVec
	bufferPoolHits      *prometheus.CounterVec
	bufferPoolMisses    *prometheus.CounterVec
	bufferPoolEvictions *prometheus.CounterVec
	bufferAllocations   *prometheus.CounterVec
	bufferInUse         *prometheus.GaugeVec

	// Processor chain metrics
	processorExecutions   *prometheus.CounterVec
	processorDuration     *prometheus.HistogramVec
	processorErrors       *prometheus.CounterVec
	processorChainLength  *prometheus.GaugeVec

	// Audio data metrics
	audioDataBytes     *prometheus.CounterVec
	audioDataDuration  *prometheus.CounterVec
	audioDataDropped   *prometheus.CounterVec
	audioFormatChanges *prometheus.CounterVec

	// FFmpeg process metrics (integration with existing FFmpeg manager)
	ffmpegProcesses     *prometheus.GaugeVec
	ffmpegRestarts      *prometheus.CounterVec
	ffmpegHealthChecks  *prometheus.CounterVec
	ffmpegDataReceived  *prometheus.CounterVec

	// Gain processor metrics
	gainAdjustments     *prometheus.CounterVec
	gainLevels          *prometheus.HistogramVec
	gainClippingEvents  *prometheus.CounterVec

	// collectors is a slice of all collectors for easier iteration
	collectors []prometheus.Collector
}

// NewAudioCoreMetrics creates and registers new audiocore metrics
func NewAudioCoreMetrics(registry *prometheus.Registry) (*AudioCoreMetrics, error) {
	m := &AudioCoreMetrics{registry: registry}
	if err := m.initMetrics(); err != nil {
		return nil, err
	}
	if err := registry.Register(m); err != nil {
		return nil, err
	}
	return m, nil
}

// initMetrics initializes all Prometheus metrics
func (m *AudioCoreMetrics) initMetrics() error {
	// Manager metrics
	m.activeSources = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "audiocore_active_sources",
			Help: "Number of active audio sources",
		},
		[]string{"manager_id"},
	)

	m.processedFrames = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_processed_frames_total",
			Help: "Total number of audio frames processed",
		},
		[]string{"manager_id", "source_id"},
	)

	m.processingErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_processing_errors_total",
			Help: "Total number of processing errors",
		},
		[]string{"manager_id", "source_id", "error_type"},
	)

	m.processingDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "audiocore_processing_duration_seconds",
			Help:    "Time taken to process audio frames",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 12), // 1ms to ~4s
		},
		[]string{"manager_id", "source_id"},
	)

	// Source metrics
	m.sourceStartTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_source_start_total",
			Help: "Total number of source start operations",
		},
		[]string{"source_id", "source_type", "status"},
	)

	m.sourceStopTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_source_stop_total",
			Help: "Total number of source stop operations",
		},
		[]string{"source_id", "source_type", "status"},
	)

	m.sourceErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_source_errors_total",
			Help: "Total number of source errors",
		},
		[]string{"source_id", "source_type", "error_type"},
	)

	m.sourceDataRate = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "audiocore_source_data_rate_bytes_per_second",
			Help: "Current data rate from audio sources",
		},
		[]string{"source_id", "source_type"},
	)

	m.sourceUptime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "audiocore_source_uptime_seconds",
			Help: "Time since source was started",
		},
		[]string{"source_id", "source_type"},
	)

	m.sourceGainLevel = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "audiocore_source_gain_level",
			Help: "Current gain level for audio source",
		},
		[]string{"source_id", "source_type"},
	)

	// Buffer pool metrics
	m.bufferPoolSize = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "audiocore_buffer_pool_size",
			Help: "Number of buffers in pool",
		},
		[]string{"pool_tier"}, // small, medium, large
	)

	m.bufferPoolHits = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_buffer_pool_hits_total",
			Help: "Total number of buffer pool hits",
		},
		[]string{"pool_tier"},
	)

	m.bufferPoolMisses = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_buffer_pool_misses_total",
			Help: "Total number of buffer pool misses",
		},
		[]string{"pool_tier"},
	)

	m.bufferPoolEvictions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_buffer_pool_evictions_total",
			Help: "Total number of buffer evictions",
		},
		[]string{"pool_tier", "reason"},
	)

	m.bufferAllocations = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_buffer_allocations_total",
			Help: "Total number of buffer allocations",
		},
		[]string{"pool_tier", "allocation_type"}, // pooled, custom
	)

	m.bufferInUse = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "audiocore_buffers_in_use",
			Help: "Number of buffers currently in use",
		},
		[]string{"pool_tier"},
	)

	// Processor chain metrics
	m.processorExecutions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_processor_executions_total",
			Help: "Total number of processor executions",
		},
		[]string{"processor_id", "processor_type", "status"},
	)

	m.processorDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "audiocore_processor_duration_seconds",
			Help:    "Time taken by audio processors",
			Buckets: prometheus.ExponentialBuckets(0.0001, 2, 12), // 0.1ms to ~400ms
		},
		[]string{"processor_id", "processor_type"},
	)

	m.processorErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_processor_errors_total",
			Help: "Total number of processor errors",
		},
		[]string{"processor_id", "processor_type", "error_type"},
	)

	m.processorChainLength = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "audiocore_processor_chain_length",
			Help: "Number of processors in chain",
		},
		[]string{"source_id"},
	)

	// Audio data metrics
	m.audioDataBytes = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_audio_data_bytes_total",
			Help: "Total bytes of audio data processed",
		},
		[]string{"source_id", "stage"}, // stage: input, output
	)

	m.audioDataDuration = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_audio_data_duration_seconds_total",
			Help: "Total duration of audio data processed",
		},
		[]string{"source_id"},
	)

	m.audioDataDropped = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_audio_data_dropped_total",
			Help: "Total audio data dropped due to buffer overflow",
		},
		[]string{"source_id", "reason"},
	)

	m.audioFormatChanges = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_audio_format_changes_total",
			Help: "Total number of audio format changes",
		},
		[]string{"source_id", "from_format", "to_format"},
	)

	// FFmpeg process metrics
	m.ffmpegProcesses = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "audiocore_ffmpeg_processes",
			Help: "Number of active FFmpeg processes",
		},
		[]string{"manager_id"},
	)

	m.ffmpegRestarts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_ffmpeg_restarts_total",
			Help: "Total number of FFmpeg process restarts",
		},
		[]string{"process_id", "reason"},
	)

	m.ffmpegHealthChecks = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_ffmpeg_health_checks_total",
			Help: "Total number of FFmpeg health checks",
		},
		[]string{"process_id", "status"},
	)

	m.ffmpegDataReceived = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_ffmpeg_data_received_bytes_total",
			Help: "Total bytes received from FFmpeg processes",
		},
		[]string{"process_id"},
	)

	// Gain processor metrics
	m.gainAdjustments = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_gain_adjustments_total",
			Help: "Total number of gain adjustments",
		},
		[]string{"processor_id", "adjustment_type"}, // increase, decrease, no_change
	)

	m.gainLevels = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "audiocore_gain_levels",
			Help:    "Distribution of gain levels applied",
			Buckets: prometheus.LinearBuckets(0, 0.1, 21), // 0.0 to 2.0 in 0.1 steps
		},
		[]string{"processor_id"},
	)

	m.gainClippingEvents = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "audiocore_gain_clipping_events_total",
			Help: "Total number of audio clipping events during gain processing",
		},
		[]string{"processor_id", "sample_format"},
	)

	// Initialize collectors slice with all metrics
	m.collectors = []prometheus.Collector{
		m.activeSources,
		m.processedFrames,
		m.processingErrors,
		m.processingDuration,
		m.sourceStartTotal,
		m.sourceStopTotal,
		m.sourceErrors,
		m.sourceDataRate,
		m.sourceUptime,
		m.sourceGainLevel,
		m.bufferPoolSize,
		m.bufferPoolHits,
		m.bufferPoolMisses,
		m.bufferPoolEvictions,
		m.bufferAllocations,
		m.bufferInUse,
		m.processorExecutions,
		m.processorDuration,
		m.processorErrors,
		m.processorChainLength,
		m.audioDataBytes,
		m.audioDataDuration,
		m.audioDataDropped,
		m.audioFormatChanges,
		m.ffmpegProcesses,
		m.ffmpegRestarts,
		m.ffmpegHealthChecks,
		m.ffmpegDataReceived,
		m.gainAdjustments,
		m.gainLevels,
		m.gainClippingEvents,
	}

	return nil
}

// Describe implements the Collector interface
func (m *AudioCoreMetrics) Describe(ch chan<- *prometheus.Desc) {
	for _, collector := range m.collectors {
		collector.Describe(ch)
	}
}

// Collect implements the Collector interface
func (m *AudioCoreMetrics) Collect(ch chan<- prometheus.Metric) {
	for _, collector := range m.collectors {
		collector.Collect(ch)
	}
}

// Manager metrics recording methods

// UpdateActiveSources updates the number of active sources
func (m *AudioCoreMetrics) UpdateActiveSources(managerID string, count int) {
	m.activeSources.WithLabelValues(managerID).Set(float64(count))
}

// RecordProcessedFrame records a processed audio frame
func (m *AudioCoreMetrics) RecordProcessedFrame(managerID, sourceID string) {
	m.processedFrames.WithLabelValues(managerID, sourceID).Inc()
}

// RecordProcessingError records a processing error
func (m *AudioCoreMetrics) RecordProcessingError(managerID, sourceID, errorType string) {
	m.processingErrors.WithLabelValues(managerID, sourceID, errorType).Inc()
}

// RecordProcessingDuration records the duration of frame processing
func (m *AudioCoreMetrics) RecordProcessingDuration(managerID, sourceID string, duration float64) {
	m.processingDuration.WithLabelValues(managerID, sourceID).Observe(duration)
}

// Source metrics recording methods

// RecordSourceStart records a source start operation
func (m *AudioCoreMetrics) RecordSourceStart(sourceID, sourceType, status string) {
	m.sourceStartTotal.WithLabelValues(sourceID, sourceType, status).Inc()
}

// RecordSourceStop records a source stop operation
func (m *AudioCoreMetrics) RecordSourceStop(sourceID, sourceType, status string) {
	m.sourceStopTotal.WithLabelValues(sourceID, sourceType, status).Inc()
}

// RecordSourceError records a source error
func (m *AudioCoreMetrics) RecordSourceError(sourceID, sourceType, errorType string) {
	m.sourceErrors.WithLabelValues(sourceID, sourceType, errorType).Inc()
}

// UpdateSourceDataRate updates the data rate for a source
func (m *AudioCoreMetrics) UpdateSourceDataRate(sourceID, sourceType string, bytesPerSecond float64) {
	m.sourceDataRate.WithLabelValues(sourceID, sourceType).Set(bytesPerSecond)
}

// UpdateSourceUptime updates the uptime for a source
func (m *AudioCoreMetrics) UpdateSourceUptime(sourceID, sourceType string, seconds float64) {
	m.sourceUptime.WithLabelValues(sourceID, sourceType).Set(seconds)
}

// UpdateSourceGainLevel updates the gain level for a source
func (m *AudioCoreMetrics) UpdateSourceGainLevel(sourceID, sourceType string, gain float64) {
	m.sourceGainLevel.WithLabelValues(sourceID, sourceType).Set(gain)
}

// Buffer pool metrics recording methods

// UpdateBufferPoolSize updates the size of a buffer pool
func (m *AudioCoreMetrics) UpdateBufferPoolSize(poolTier string, size int) {
	m.bufferPoolSize.WithLabelValues(poolTier).Set(float64(size))
}

// RecordBufferPoolHit records a buffer pool hit
func (m *AudioCoreMetrics) RecordBufferPoolHit(poolTier string) {
	m.bufferPoolHits.WithLabelValues(poolTier).Inc()
}

// RecordBufferPoolMiss records a buffer pool miss
func (m *AudioCoreMetrics) RecordBufferPoolMiss(poolTier string) {
	m.bufferPoolMisses.WithLabelValues(poolTier).Inc()
}

// RecordBufferPoolEviction records a buffer eviction
func (m *AudioCoreMetrics) RecordBufferPoolEviction(poolTier, reason string) {
	m.bufferPoolEvictions.WithLabelValues(poolTier, reason).Inc()
}

// RecordBufferAllocation records a buffer allocation
func (m *AudioCoreMetrics) RecordBufferAllocation(poolTier, allocationType string) {
	m.bufferAllocations.WithLabelValues(poolTier, allocationType).Inc()
}

// UpdateBuffersInUse updates the number of buffers in use
func (m *AudioCoreMetrics) UpdateBuffersInUse(poolTier string, count int) {
	m.bufferInUse.WithLabelValues(poolTier).Set(float64(count))
}

// Processor metrics recording methods

// RecordProcessorExecution records a processor execution
func (m *AudioCoreMetrics) RecordProcessorExecution(processorID, processorType, status string) {
	m.processorExecutions.WithLabelValues(processorID, processorType, status).Inc()
}

// RecordProcessorDuration records the duration of processor execution
func (m *AudioCoreMetrics) RecordProcessorDuration(processorID, processorType string, duration float64) {
	m.processorDuration.WithLabelValues(processorID, processorType).Observe(duration)
}

// RecordProcessorError records a processor error
func (m *AudioCoreMetrics) RecordProcessorError(processorID, processorType, errorType string) {
	m.processorErrors.WithLabelValues(processorID, processorType, errorType).Inc()
}

// UpdateProcessorChainLength updates the length of a processor chain
func (m *AudioCoreMetrics) UpdateProcessorChainLength(sourceID string, length int) {
	m.processorChainLength.WithLabelValues(sourceID).Set(float64(length))
}

// Audio data metrics recording methods

// RecordAudioDataBytes records bytes of audio data processed
func (m *AudioCoreMetrics) RecordAudioDataBytes(sourceID, stage string, bytes int) {
	m.audioDataBytes.WithLabelValues(sourceID, stage).Add(float64(bytes))
}

// RecordAudioDataDuration records duration of audio data processed
func (m *AudioCoreMetrics) RecordAudioDataDuration(sourceID string, seconds float64) {
	m.audioDataDuration.WithLabelValues(sourceID).Add(seconds)
}

// RecordAudioDataDropped records dropped audio data
func (m *AudioCoreMetrics) RecordAudioDataDropped(sourceID, reason string) {
	m.audioDataDropped.WithLabelValues(sourceID, reason).Inc()
}

// RecordAudioFormatChange records an audio format change
func (m *AudioCoreMetrics) RecordAudioFormatChange(sourceID, fromFormat, toFormat string) {
	m.audioFormatChanges.WithLabelValues(sourceID, fromFormat, toFormat).Inc()
}

// FFmpeg metrics recording methods

// UpdateFFmpegProcesses updates the number of FFmpeg processes
func (m *AudioCoreMetrics) UpdateFFmpegProcesses(managerID string, count int) {
	m.ffmpegProcesses.WithLabelValues(managerID).Set(float64(count))
}

// RecordFFmpegRestart records an FFmpeg process restart
func (m *AudioCoreMetrics) RecordFFmpegRestart(processID, reason string) {
	m.ffmpegRestarts.WithLabelValues(processID, reason).Inc()
}

// RecordFFmpegHealthCheck records an FFmpeg health check
func (m *AudioCoreMetrics) RecordFFmpegHealthCheck(processID, status string) {
	m.ffmpegHealthChecks.WithLabelValues(processID, status).Inc()
}

// RecordFFmpegDataReceived records data received from FFmpeg
func (m *AudioCoreMetrics) RecordFFmpegDataReceived(processID string, bytes int) {
	m.ffmpegDataReceived.WithLabelValues(processID).Add(float64(bytes))
}

// Gain processor metrics recording methods

// RecordGainAdjustment records a gain adjustment
func (m *AudioCoreMetrics) RecordGainAdjustment(processorID, adjustmentType string) {
	m.gainAdjustments.WithLabelValues(processorID, adjustmentType).Inc()
}

// RecordGainLevel records the gain level applied
func (m *AudioCoreMetrics) RecordGainLevel(processorID string, level float64) {
	m.gainLevels.WithLabelValues(processorID).Observe(level)
}

// RecordGainClippingEvent records an audio clipping event
func (m *AudioCoreMetrics) RecordGainClippingEvent(processorID, sampleFormat string) {
	m.gainClippingEvents.WithLabelValues(processorID, sampleFormat).Inc()
}