// Package metrics provides myaudio buffer metrics for observability
package metrics

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

// MyAudioMetrics contains Prometheus metrics for myaudio buffer operations
type MyAudioMetrics struct {
	registry *prometheus.Registry

	// Buffer allocation metrics
	bufferAllocationsTotal   *prometheus.CounterVec
	bufferAllocationDuration *prometheus.HistogramVec
	bufferAllocationErrors   *prometheus.CounterVec
	bufferAllocationAttempts *prometheus.CounterVec  // Track all allocation attempts including blocked ones
	bufferAllocationSizes    *prometheus.HistogramVec // Track allocation sizes for memory usage patterns

	// Buffer capacity and utilization metrics
	bufferCapacityGauge    *prometheus.GaugeVec
	bufferUtilizationGauge *prometheus.GaugeVec
	bufferSizeGauge        *prometheus.GaugeVec

	// Buffer write operation metrics
	bufferWritesTotal     *prometheus.CounterVec
	bufferWriteDuration   *prometheus.HistogramVec
	bufferWriteErrors     *prometheus.CounterVec
	bufferWriteRetries    *prometheus.CounterVec
	bufferWriteBytesTotal *prometheus.CounterVec

	// Buffer read operation metrics
	bufferReadsTotal     *prometheus.CounterVec
	bufferReadDuration   *prometheus.HistogramVec
	bufferReadErrors     *prometheus.CounterVec
	bufferReadBytesTotal *prometheus.CounterVec

	// Buffer state metrics
	bufferOverflowsTotal   *prometheus.CounterVec
	bufferUnderrunsTotal   *prometheus.CounterVec
	bufferWraparoundsTotal *prometheus.CounterVec

	// Analysis buffer specific metrics
	analysisBufferProcessingDuration *prometheus.HistogramVec
	analysisBufferPollTotal          *prometheus.CounterVec
	analysisBufferDataDropsTotal     *prometheus.CounterVec

	// Capture buffer specific metrics
	captureBufferSegmentReadsTotal    *prometheus.CounterVec
	captureBufferSegmentReadDuration  *prometheus.HistogramVec
	captureBufferTimestampErrorsTotal *prometheus.CounterVec

	// Audio quality metrics
	audioDataValidationErrors *prometheus.CounterVec
	audioSilenceDetections    *prometheus.CounterVec
	audioDataCorruptionTotal  *prometheus.CounterVec

	// File operation metrics
	fileOperationsTotal   *prometheus.CounterVec
	fileOperationDuration *prometheus.HistogramVec
	fileOperationErrors   *prometheus.CounterVec
	fileSizesTotal        *prometheus.CounterVec
	audioFileInfoGauge    *prometheus.GaugeVec

	// Audio processing metrics
	audioProcessingTotal    *prometheus.CounterVec
	audioProcessingDuration *prometheus.HistogramVec
	audioProcessingErrors   *prometheus.CounterVec
	audioConversionsTotal   *prometheus.CounterVec
	audioConversionDuration *prometheus.HistogramVec
	audioConversionErrors   *prometheus.CounterVec
	audioInferenceDuration  *prometheus.HistogramVec
	audioDataSizeTotal      *prometheus.CounterVec
	audioSampleCountTotal   *prometheus.CounterVec
	birdnetResultsTotal     *prometheus.CounterVec
	audioQueueOperations    *prometheus.CounterVec
}

// NewMyAudioMetrics creates and registers new myaudio metrics
func NewMyAudioMetrics(registry *prometheus.Registry) (*MyAudioMetrics, error) {
	m := &MyAudioMetrics{registry: registry}
	if err := m.initMetrics(); err != nil {
		return nil, err
	}
	if err := registry.Register(m); err != nil {
		return nil, err
	}
	return m, nil
}

// initMetrics initializes all Prometheus metrics
func (m *MyAudioMetrics) initMetrics() error {
	// Buffer allocation metrics
	m.bufferAllocationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_buffer_allocations_total",
			Help: "Total number of buffer allocations",
		},
		[]string{"buffer_type", "source", "status"}, // buffer_type: analysis, capture; status: success, error
	)

	m.bufferAllocationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "myaudio_buffer_allocation_duration_seconds",
			Help:    "Time taken for buffer allocations",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to ~1s
		},
		[]string{"buffer_type", "source"},
	)

	m.bufferAllocationErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_buffer_allocation_errors_total",
			Help: "Total number of buffer allocation errors",
		},
		[]string{"buffer_type", "source", "error_type"},
	)

	m.bufferAllocationAttempts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_buffer_allocation_attempts_total",
			Help: "Total number of buffer allocation attempts including successful and blocked",
		},
		[]string{"buffer_type", "source", "result"}, // result: first_allocation, repeated_blocked, error
	)

	m.bufferAllocationSizes = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "myaudio_buffer_allocation_size_bytes",
			Help:    "Size of buffer allocations in bytes",
			Buckets: prometheus.ExponentialBuckets(1024, 2, 20), // 1KB to ~1GB
		},
		[]string{"buffer_type", "source"},
	)

	// Buffer capacity metrics
	m.bufferCapacityGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "myaudio_buffer_capacity_bytes",
			Help: "Buffer capacity in bytes",
		},
		[]string{"buffer_type", "source"},
	)

	m.bufferUtilizationGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "myaudio_buffer_utilization_ratio",
			Help: "Buffer utilization ratio (0.0 to 1.0)",
		},
		[]string{"buffer_type", "source"},
	)

	m.bufferSizeGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "myaudio_buffer_size_bytes",
			Help: "Current buffer size in bytes",
		},
		[]string{"buffer_type", "source"},
	)

	// Buffer write metrics
	m.bufferWritesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_buffer_writes_total",
			Help: "Total number of buffer write operations",
		},
		[]string{"buffer_type", "source", "status"}, // status: success, error, partial
	)

	m.bufferWriteDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "myaudio_buffer_write_duration_seconds",
			Help:    "Time taken for buffer write operations",
			Buckets: prometheus.ExponentialBuckets(0.0001, 2, 12), // 0.1ms to ~400ms
		},
		[]string{"buffer_type", "source"},
	)

	m.bufferWriteErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_buffer_write_errors_total",
			Help: "Total number of buffer write errors",
		},
		[]string{"buffer_type", "source", "error_type"}, // error_type: full, timeout, invalid_data
	)

	m.bufferWriteRetries = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_buffer_write_retries_total",
			Help: "Total number of buffer write retries",
		},
		[]string{"buffer_type", "source", "retry_reason"},
	)

	m.bufferWriteBytesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_buffer_write_bytes_total",
			Help: "Total bytes written to buffers",
		},
		[]string{"buffer_type", "source"},
	)

	// Buffer read metrics
	m.bufferReadsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_buffer_reads_total",
			Help: "Total number of buffer read operations",
		},
		[]string{"buffer_type", "source", "status"}, // status: success, error, insufficient_data
	)

	m.bufferReadDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "myaudio_buffer_read_duration_seconds",
			Help:    "Time taken for buffer read operations",
			Buckets: prometheus.ExponentialBuckets(0.0001, 2, 12), // 0.1ms to ~400ms
		},
		[]string{"buffer_type", "source"},
	)

	m.bufferReadErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_buffer_read_errors_total",
			Help: "Total number of buffer read errors",
		},
		[]string{"buffer_type", "source", "error_type"},
	)

	m.bufferReadBytesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_buffer_read_bytes_total",
			Help: "Total bytes read from buffers",
		},
		[]string{"buffer_type", "source"},
	)

	// Buffer state metrics
	m.bufferOverflowsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_buffer_overflows_total",
			Help: "Total number of buffer overflows",
		},
		[]string{"buffer_type", "source"},
	)

	m.bufferUnderrunsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_buffer_underruns_total",
			Help: "Total number of buffer underruns",
		},
		[]string{"buffer_type", "source"},
	)

	m.bufferWraparoundsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_buffer_wraparounds_total",
			Help: "Total number of buffer wraparounds",
		},
		[]string{"buffer_type", "source"},
	)

	// Analysis buffer specific metrics
	m.analysisBufferProcessingDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "myaudio_analysis_buffer_processing_duration_seconds",
			Help:    "Time taken for analysis buffer processing",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15), // 1ms to ~32s
		},
		[]string{"source"},
	)

	m.analysisBufferPollTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_analysis_buffer_polls_total",
			Help: "Total number of analysis buffer polls",
		},
		[]string{"source", "result"}, // result: data_available, insufficient_data, error
	)

	m.analysisBufferDataDropsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_analysis_buffer_data_drops_total",
			Help: "Total number of analysis buffer data drops",
		},
		[]string{"source", "reason"}, // reason: full_buffer, write_failure, retry_exhausted
	)

	// Capture buffer specific metrics
	m.captureBufferSegmentReadsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_capture_buffer_segment_reads_total",
			Help: "Total number of capture buffer segment reads",
		},
		[]string{"source", "status"}, // status: success, error, timeout
	)

	m.captureBufferSegmentReadDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "myaudio_capture_buffer_segment_read_duration_seconds",
			Help:    "Time taken for capture buffer segment reads",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 12), // 1ms to ~4s
		},
		[]string{"source"},
	)

	m.captureBufferTimestampErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_capture_buffer_timestamp_errors_total",
			Help: "Total number of capture buffer timestamp errors",
		},
		[]string{"source", "error_type"}, // error_type: outside_timeframe, invalid_duration
	)

	// Audio quality metrics
	m.audioDataValidationErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_audio_data_validation_errors_total",
			Help: "Total number of audio data validation errors",
		},
		[]string{"source", "validation_type"}, // validation_type: alignment, size, range
	)

	m.audioSilenceDetections = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_audio_silence_detections_total",
			Help: "Total number of audio silence detections",
		},
		[]string{"source"},
	)

	m.audioDataCorruptionTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_audio_data_corruption_total",
			Help: "Total number of audio data corruption detections",
		},
		[]string{"source", "corruption_type"},
	)

	// File operation metrics
	m.fileOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_file_operations_total",
			Help: "Total number of file operations",
		},
		[]string{"operation", "format", "status"}, // operation: get_info, read_buffered, save_wav; format: wav, flac, mp3; status: success, error
	)

	m.fileOperationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "myaudio_file_operation_duration_seconds",
			Help:    "Time taken for file operations",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 12), // 1ms to ~4s
		},
		[]string{"operation", "format"},
	)

	m.fileOperationErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_file_operation_errors_total",
			Help: "Total number of file operation errors",
		},
		[]string{"operation", "format", "error_type"}, // error_type: empty_path, open_failed, read_failed, etc.
	)

	m.fileSizesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_file_sizes_bytes_total",
			Help: "Total bytes processed in file operations",
		},
		[]string{"operation", "format"},
	)

	m.audioFileInfoGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "myaudio_audio_file_info",
			Help: "Audio file information (sample rate, channels, bit depth, samples)",
		},
		[]string{"format", "metric_type"}, // metric_type: sample_rate, channels, bit_depth, total_samples
	)

	// Audio processing metrics
	m.audioProcessingTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_audio_processing_total",
			Help: "Total number of audio processing operations",
		},
		[]string{"operation", "source", "status"}, // operation: process_data, apply_filters; status: success, error
	)

	m.audioProcessingDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "myaudio_audio_processing_duration_seconds",
			Help:    "Time taken for audio processing operations",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15), // 1ms to ~32s
		},
		[]string{"operation", "source"},
	)

	m.audioProcessingErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_audio_processing_errors_total",
			Help: "Total number of audio processing errors",
		},
		[]string{"operation", "source", "error_type"},
	)

	m.audioConversionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_audio_conversions_total",
			Help: "Total number of audio format conversions",
		},
		[]string{"conversion_type", "bit_depth", "status"}, // conversion_type: to_float32, apply_filters
	)

	m.audioConversionDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "myaudio_audio_conversion_duration_seconds",
			Help:    "Time taken for audio format conversions",
			Buckets: prometheus.ExponentialBuckets(0.0001, 2, 12), // 0.1ms to ~400ms
		},
		[]string{"source", "conversion_type"},
	)

	m.audioConversionErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_audio_conversion_errors_total",
			Help: "Total number of audio conversion errors",
		},
		[]string{"conversion_type", "bit_depth", "error_type"},
	)

	m.audioInferenceDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "myaudio_audio_inference_duration_seconds",
			Help:    "Time taken for BirdNET inference operations",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15), // 1ms to ~32s
		},
		[]string{"source"},
	)

	m.audioDataSizeTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_audio_data_size_bytes_total",
			Help: "Total bytes of audio data processed",
		},
		[]string{"source"},
	)

	m.audioSampleCountTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_audio_sample_count_total",
			Help: "Total number of audio samples processed",
		},
		[]string{"source"},
	)

	m.birdnetResultsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_birdnet_results_total",
			Help: "Total number of BirdNET detection results",
		},
		[]string{"source"},
	)

	m.audioQueueOperations = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "myaudio_audio_queue_operations_total",
			Help: "Total number of audio queue operations",
		},
		[]string{"source", "operation", "status"}, // operation: enqueue, dequeue
	)

	return nil
}

// Describe implements the Collector interface
func (m *MyAudioMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.bufferAllocationsTotal.Describe(ch)
	m.bufferAllocationDuration.Describe(ch)
	m.bufferAllocationErrors.Describe(ch)
	m.bufferAllocationAttempts.Describe(ch)
	m.bufferAllocationSizes.Describe(ch)
	m.bufferCapacityGauge.Describe(ch)
	m.bufferUtilizationGauge.Describe(ch)
	m.bufferSizeGauge.Describe(ch)
	m.bufferWritesTotal.Describe(ch)
	m.bufferWriteDuration.Describe(ch)
	m.bufferWriteErrors.Describe(ch)
	m.bufferWriteRetries.Describe(ch)
	m.bufferWriteBytesTotal.Describe(ch)
	m.bufferReadsTotal.Describe(ch)
	m.bufferReadDuration.Describe(ch)
	m.bufferReadErrors.Describe(ch)
	m.bufferReadBytesTotal.Describe(ch)
	m.bufferOverflowsTotal.Describe(ch)
	m.bufferUnderrunsTotal.Describe(ch)
	m.bufferWraparoundsTotal.Describe(ch)
	m.analysisBufferProcessingDuration.Describe(ch)
	m.analysisBufferPollTotal.Describe(ch)
	m.analysisBufferDataDropsTotal.Describe(ch)
	m.captureBufferSegmentReadsTotal.Describe(ch)
	m.captureBufferSegmentReadDuration.Describe(ch)
	m.captureBufferTimestampErrorsTotal.Describe(ch)
	m.audioDataValidationErrors.Describe(ch)
	m.audioSilenceDetections.Describe(ch)
	m.audioDataCorruptionTotal.Describe(ch)
	m.fileOperationsTotal.Describe(ch)
	m.fileOperationDuration.Describe(ch)
	m.fileOperationErrors.Describe(ch)
	m.fileSizesTotal.Describe(ch)
	m.audioFileInfoGauge.Describe(ch)
	m.audioProcessingTotal.Describe(ch)
	m.audioProcessingDuration.Describe(ch)
	m.audioProcessingErrors.Describe(ch)
	m.audioConversionsTotal.Describe(ch)
	m.audioConversionDuration.Describe(ch)
	m.audioConversionErrors.Describe(ch)
	m.audioInferenceDuration.Describe(ch)
	m.audioDataSizeTotal.Describe(ch)
	m.audioSampleCountTotal.Describe(ch)
	m.birdnetResultsTotal.Describe(ch)
	m.audioQueueOperations.Describe(ch)
}

// Collect implements the Collector interface
func (m *MyAudioMetrics) Collect(ch chan<- prometheus.Metric) {
	m.bufferAllocationsTotal.Collect(ch)
	m.bufferAllocationDuration.Collect(ch)
	m.bufferAllocationErrors.Collect(ch)
	m.bufferAllocationAttempts.Collect(ch)
	m.bufferAllocationSizes.Collect(ch)
	m.bufferCapacityGauge.Collect(ch)
	m.bufferUtilizationGauge.Collect(ch)
	m.bufferSizeGauge.Collect(ch)
	m.bufferWritesTotal.Collect(ch)
	m.bufferWriteDuration.Collect(ch)
	m.bufferWriteErrors.Collect(ch)
	m.bufferWriteRetries.Collect(ch)
	m.bufferWriteBytesTotal.Collect(ch)
	m.bufferReadsTotal.Collect(ch)
	m.bufferReadDuration.Collect(ch)
	m.bufferReadErrors.Collect(ch)
	m.bufferReadBytesTotal.Collect(ch)
	m.bufferOverflowsTotal.Collect(ch)
	m.bufferUnderrunsTotal.Collect(ch)
	m.bufferWraparoundsTotal.Collect(ch)
	m.analysisBufferProcessingDuration.Collect(ch)
	m.analysisBufferPollTotal.Collect(ch)
	m.analysisBufferDataDropsTotal.Collect(ch)
	m.captureBufferSegmentReadsTotal.Collect(ch)
	m.captureBufferSegmentReadDuration.Collect(ch)
	m.captureBufferTimestampErrorsTotal.Collect(ch)
	m.audioDataValidationErrors.Collect(ch)
	m.audioSilenceDetections.Collect(ch)
	m.audioDataCorruptionTotal.Collect(ch)
	m.fileOperationsTotal.Collect(ch)
	m.fileOperationDuration.Collect(ch)
	m.fileOperationErrors.Collect(ch)
	m.fileSizesTotal.Collect(ch)
	m.audioFileInfoGauge.Collect(ch)
	m.audioProcessingTotal.Collect(ch)
	m.audioProcessingDuration.Collect(ch)
	m.audioProcessingErrors.Collect(ch)
	m.audioConversionsTotal.Collect(ch)
	m.audioConversionDuration.Collect(ch)
	m.audioConversionErrors.Collect(ch)
	m.audioInferenceDuration.Collect(ch)
	m.audioDataSizeTotal.Collect(ch)
	m.audioSampleCountTotal.Collect(ch)
	m.birdnetResultsTotal.Collect(ch)
	m.audioQueueOperations.Collect(ch)
}

// Buffer allocation recording methods

// RecordBufferAllocation records a buffer allocation
func (m *MyAudioMetrics) RecordBufferAllocation(bufferType, source, status string) {
	m.bufferAllocationsTotal.WithLabelValues(bufferType, source, status).Inc()
}

// RecordBufferAllocationDuration records the duration of a buffer allocation
func (m *MyAudioMetrics) RecordBufferAllocationDuration(bufferType, source string, duration float64) {
	m.bufferAllocationDuration.WithLabelValues(bufferType, source).Observe(duration)
}

// RecordBufferAllocationError records a buffer allocation error
func (m *MyAudioMetrics) RecordBufferAllocationError(bufferType, source, errorType string) {
	m.bufferAllocationErrors.WithLabelValues(bufferType, source, errorType).Inc()
}

// RecordBufferAllocationAttempt records any buffer allocation attempt
// result values:
// - "attempted" - initial attempt (always recorded first)
// - "first_allocation" - successful first allocation
// - "repeated_blocked" - allocation blocked due to existing buffer
// - "error" - allocation failed due to validation or system errors
//
// Example Prometheus query to find sources with repeated allocation attempts:
// sum by (source) (rate(myaudio_buffer_allocation_attempts_total{result="repeated_blocked"}[5m])) > 0
func (m *MyAudioMetrics) RecordBufferAllocationAttempt(bufferType, source, result string) {
	m.bufferAllocationAttempts.WithLabelValues(bufferType, source, result).Inc()
}

// RecordBufferAllocationSize records the size of a buffer allocation
func (m *MyAudioMetrics) RecordBufferAllocationSize(bufferType, source string, sizeBytes int) {
	m.bufferAllocationSizes.WithLabelValues(bufferType, source).Observe(float64(sizeBytes))
}

// Buffer capacity recording methods

// UpdateBufferCapacity updates buffer capacity metrics
func (m *MyAudioMetrics) UpdateBufferCapacity(bufferType, source string, capacity int) {
	m.bufferCapacityGauge.WithLabelValues(bufferType, source).Set(float64(capacity))
}

// UpdateBufferUtilization updates buffer utilization metrics
func (m *MyAudioMetrics) UpdateBufferUtilization(bufferType, source string, utilization float64) {
	m.bufferUtilizationGauge.WithLabelValues(bufferType, source).Set(utilization)
}

// UpdateBufferSize updates current buffer size metrics
func (m *MyAudioMetrics) UpdateBufferSize(bufferType, source string, size int) {
	m.bufferSizeGauge.WithLabelValues(bufferType, source).Set(float64(size))
}

// Buffer write recording methods

// RecordBufferWrite records a buffer write operation
func (m *MyAudioMetrics) RecordBufferWrite(bufferType, source, status string) {
	m.bufferWritesTotal.WithLabelValues(bufferType, source, status).Inc()
}

// RecordBufferWriteDuration records the duration of a buffer write
func (m *MyAudioMetrics) RecordBufferWriteDuration(bufferType, source string, duration float64) {
	m.bufferWriteDuration.WithLabelValues(bufferType, source).Observe(duration)
}

// RecordBufferWriteError records a buffer write error
func (m *MyAudioMetrics) RecordBufferWriteError(bufferType, source, errorType string) {
	m.bufferWriteErrors.WithLabelValues(bufferType, source, errorType).Inc()
}

// RecordBufferWriteRetry records a buffer write retry
func (m *MyAudioMetrics) RecordBufferWriteRetry(bufferType, source, retryReason string) {
	m.bufferWriteRetries.WithLabelValues(bufferType, source, retryReason).Inc()
}

// RecordBufferWriteBytes records bytes written to buffer
func (m *MyAudioMetrics) RecordBufferWriteBytes(bufferType, source string, bytes int) {
	m.bufferWriteBytesTotal.WithLabelValues(bufferType, source).Add(float64(bytes))
}

// Buffer read recording methods

// RecordBufferRead records a buffer read operation
func (m *MyAudioMetrics) RecordBufferRead(bufferType, source, status string) {
	m.bufferReadsTotal.WithLabelValues(bufferType, source, status).Inc()
}

// RecordBufferReadDuration records the duration of a buffer read
func (m *MyAudioMetrics) RecordBufferReadDuration(bufferType, source string, duration float64) {
	m.bufferReadDuration.WithLabelValues(bufferType, source).Observe(duration)
}

// RecordBufferReadError records a buffer read error
func (m *MyAudioMetrics) RecordBufferReadError(bufferType, source, errorType string) {
	m.bufferReadErrors.WithLabelValues(bufferType, source, errorType).Inc()
}

// RecordBufferReadBytes records bytes read from buffer
func (m *MyAudioMetrics) RecordBufferReadBytes(bufferType, source string, bytes int) {
	m.bufferReadBytesTotal.WithLabelValues(bufferType, source).Add(float64(bytes))
}

// Buffer state recording methods

// RecordBufferOverflow records a buffer overflow
func (m *MyAudioMetrics) RecordBufferOverflow(bufferType, source string) {
	m.bufferOverflowsTotal.WithLabelValues(bufferType, source).Inc()
}

// RecordBufferUnderrun records a buffer underrun
func (m *MyAudioMetrics) RecordBufferUnderrun(bufferType, source string) {
	m.bufferUnderrunsTotal.WithLabelValues(bufferType, source).Inc()
}

// RecordBufferWraparound records a buffer wraparound
func (m *MyAudioMetrics) RecordBufferWraparound(bufferType, source string) {
	m.bufferWraparoundsTotal.WithLabelValues(bufferType, source).Inc()
}

// Analysis buffer specific recording methods

// RecordAnalysisBufferProcessingDuration records analysis buffer processing duration
func (m *MyAudioMetrics) RecordAnalysisBufferProcessingDuration(source string, duration float64) {
	m.analysisBufferProcessingDuration.WithLabelValues(source).Observe(duration)
}

// RecordAnalysisBufferPoll records an analysis buffer poll
func (m *MyAudioMetrics) RecordAnalysisBufferPoll(source, result string) {
	m.analysisBufferPollTotal.WithLabelValues(source, result).Inc()
}

// RecordAnalysisBufferDataDrop records an analysis buffer data drop
func (m *MyAudioMetrics) RecordAnalysisBufferDataDrop(source, reason string) {
	m.analysisBufferDataDropsTotal.WithLabelValues(source, reason).Inc()
}

// Capture buffer specific recording methods

// RecordCaptureBufferSegmentRead records a capture buffer segment read
func (m *MyAudioMetrics) RecordCaptureBufferSegmentRead(source, status string) {
	m.captureBufferSegmentReadsTotal.WithLabelValues(source, status).Inc()
}

// RecordCaptureBufferSegmentReadDuration records capture buffer segment read duration
func (m *MyAudioMetrics) RecordCaptureBufferSegmentReadDuration(source string, duration float64) {
	m.captureBufferSegmentReadDuration.WithLabelValues(source).Observe(duration)
}

// RecordCaptureBufferTimestampError records a capture buffer timestamp error
func (m *MyAudioMetrics) RecordCaptureBufferTimestampError(source, errorType string) {
	m.captureBufferTimestampErrorsTotal.WithLabelValues(source, errorType).Inc()
}

// Audio quality recording methods

// RecordAudioDataValidationError records an audio data validation error
func (m *MyAudioMetrics) RecordAudioDataValidationError(source, validationType string) {
	m.audioDataValidationErrors.WithLabelValues(source, validationType).Inc()
}

// RecordAudioSilenceDetection records an audio silence detection
func (m *MyAudioMetrics) RecordAudioSilenceDetection(source string) {
	m.audioSilenceDetections.WithLabelValues(source).Inc()
}

// RecordAudioDataCorruption records audio data corruption detection
func (m *MyAudioMetrics) RecordAudioDataCorruption(source, corruptionType string) {
	m.audioDataCorruptionTotal.WithLabelValues(source, corruptionType).Inc()
}

// File operation recording methods

// RecordFileOperation records a file operation
func (m *MyAudioMetrics) RecordFileOperation(operation, format, status string) {
	m.fileOperationsTotal.WithLabelValues(operation, format, status).Inc()
}

// RecordFileOperationDuration records the duration of a file operation
func (m *MyAudioMetrics) RecordFileOperationDuration(operation, format string, duration float64) {
	m.fileOperationDuration.WithLabelValues(operation, format).Observe(duration)
}

// RecordFileOperationError records a file operation error
func (m *MyAudioMetrics) RecordFileOperationError(operation, format, errorType string) {
	m.fileOperationErrors.WithLabelValues(operation, format, errorType).Inc()
}

// RecordFileSize records the size of files processed
func (m *MyAudioMetrics) RecordFileSize(operation, format string, sizeBytes int64) {
	m.fileSizesTotal.WithLabelValues(operation, format).Add(float64(sizeBytes))
}

// RecordAudioFileInfo records audio file information
func (m *MyAudioMetrics) RecordAudioFileInfo(format string, sampleRate, numChannels, bitDepth, totalSamples int) {
	m.audioFileInfoGauge.WithLabelValues(format, "sample_rate").Set(float64(sampleRate))
	m.audioFileInfoGauge.WithLabelValues(format, "channels").Set(float64(numChannels))
	m.audioFileInfoGauge.WithLabelValues(format, "bit_depth").Set(float64(bitDepth))
	m.audioFileInfoGauge.WithLabelValues(format, "total_samples").Set(float64(totalSamples))
}

// Audio processing recording methods

// RecordAudioProcessing records an audio processing operation
func (m *MyAudioMetrics) RecordAudioProcessing(operation, source, status string) {
	m.audioProcessingTotal.WithLabelValues(operation, source, status).Inc()
}

// RecordAudioProcessingDuration records the duration of an audio processing operation
func (m *MyAudioMetrics) RecordAudioProcessingDuration(operation, source string, duration float64) {
	m.audioProcessingDuration.WithLabelValues(operation, source).Observe(duration)
}

// RecordAudioProcessingError records an audio processing error
func (m *MyAudioMetrics) RecordAudioProcessingError(operation, source, errorType string) {
	m.audioProcessingErrors.WithLabelValues(operation, source, errorType).Inc()
}

// RecordAudioConversion records an audio format conversion
func (m *MyAudioMetrics) RecordAudioConversion(conversionType string, bitDepth int, status string) {
	m.audioConversionsTotal.WithLabelValues(conversionType, strconv.Itoa(bitDepth), status).Inc()
}

// RecordAudioConversionDuration records the duration of an audio conversion
func (m *MyAudioMetrics) RecordAudioConversionDuration(source, conversionType string, duration float64) {
	m.audioConversionDuration.WithLabelValues(source, conversionType).Observe(duration)
}

// RecordAudioConversionError records an audio conversion error
func (m *MyAudioMetrics) RecordAudioConversionError(conversionType string, bitDepth int, errorType string) {
	m.audioConversionErrors.WithLabelValues(conversionType, strconv.Itoa(bitDepth), errorType).Inc()
}

// RecordAudioInferenceDuration records the duration of BirdNET inference
func (m *MyAudioMetrics) RecordAudioInferenceDuration(source string, duration float64) {
	m.audioInferenceDuration.WithLabelValues(source).Observe(duration)
}

// RecordAudioDataSize records the size of audio data processed
func (m *MyAudioMetrics) RecordAudioDataSize(source string, sizeBytes int) {
	m.audioDataSizeTotal.WithLabelValues(source).Add(float64(sizeBytes))
}

// RecordAudioSampleCount records the number of audio samples processed
func (m *MyAudioMetrics) RecordAudioSampleCount(source string, sampleCount int) {
	m.audioSampleCountTotal.WithLabelValues(source).Add(float64(sampleCount))
}

// RecordBirdNETResults records the number of BirdNET detection results
func (m *MyAudioMetrics) RecordBirdNETResults(source string, resultCount int) {
	m.birdnetResultsTotal.WithLabelValues(source).Add(float64(resultCount))
}

// RecordAudioQueueOperation records an audio queue operation
func (m *MyAudioMetrics) RecordAudioQueueOperation(source, operation, status string) {
	m.audioQueueOperations.WithLabelValues(source, operation, status).Inc()
}
