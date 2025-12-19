// Package metrics provides disk management metrics for observability
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// DiskManagerMetrics contains Prometheus metrics for disk management operations
type DiskManagerMetrics struct {
	registry *prometheus.Registry

	// Disk usage metrics
	diskUsageBytes            prometheus.Gauge
	diskTotalBytes            prometheus.Gauge
	diskUtilizationPercentage prometheus.Gauge
	diskCheckDurationSeconds  prometheus.Histogram

	// Cleanup operation metrics
	cleanupOperationsTotal *prometheus.CounterVec
	cleanupErrorsTotal     *prometheus.CounterVec
	filesDeletedTotal      *prometheus.CounterVec
	bytesFreedTotal        *prometheus.CounterVec
	cleanupDurationSeconds *prometheus.HistogramVec

	// File processing metrics
	filesProcessedTotal    *prometheus.CounterVec
	fileParsingErrorsTotal *prometheus.CounterVec
}

// NewDiskManagerMetrics creates and registers new disk manager metrics
func NewDiskManagerMetrics(registry *prometheus.Registry) (*DiskManagerMetrics, error) {
	m := &DiskManagerMetrics{registry: registry}
	if err := m.initMetrics(); err != nil {
		return nil, err
	}
	if err := registry.Register(m); err != nil {
		return nil, err
	}
	return m, nil
}

// initMetrics initializes all Prometheus metrics
func (m *DiskManagerMetrics) initMetrics() error {
	// Disk usage metrics
	m.diskUsageBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "diskmanager_disk_usage_bytes",
		Help: "Current disk usage in bytes",
	})

	m.diskTotalBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "diskmanager_disk_total_bytes",
		Help: "Total disk space in bytes",
	})

	m.diskUtilizationPercentage = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "diskmanager_disk_utilization_percentage",
		Help: "Current disk utilization as a percentage",
	})

	m.diskCheckDurationSeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "diskmanager_disk_check_duration_seconds",
		Help:    "Time taken to check disk usage",
		Buckets: prometheus.ExponentialBuckets(BucketStart1ms, BucketFactor2, BucketCount10), // 1ms to ~1s
	})

	// Cleanup operation metrics
	m.cleanupOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "diskmanager_cleanup_operations_total",
			Help: "Total number of cleanup operations performed",
		},
		[]string{"policy", "status"}, // status: success, error
	)

	m.cleanupErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "diskmanager_cleanup_errors_total",
			Help: "Total number of cleanup errors",
		},
		[]string{"policy", "error_type"},
	)

	m.filesDeletedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "diskmanager_files_deleted_total",
			Help: "Total number of files deleted by cleanup operations",
		},
		[]string{"policy"},
	)

	m.bytesFreedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "diskmanager_bytes_freed_total",
			Help: "Total bytes freed by cleanup operations",
		},
		[]string{"policy"},
	)

	m.cleanupDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "diskmanager_cleanup_duration_seconds",
			Help:    "Time taken for cleanup operations",
			Buckets: prometheus.ExponentialBuckets(BucketStart100ms, BucketFactor2, BucketCount10), // 100ms to ~100s
		},
		[]string{"policy"},
	)

	// File processing metrics
	m.filesProcessedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "diskmanager_files_processed_total",
			Help: "Total number of files processed during cleanup",
		},
		[]string{"policy", "action"}, // action: deleted, skipped, error
	)

	m.fileParsingErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "diskmanager_file_parsing_errors_total",
			Help: "Total number of file parsing errors",
		},
		[]string{"error_type"},
	)

	return nil
}

// Describe implements the Collector interface
func (m *DiskManagerMetrics) Describe(ch chan<- *prometheus.Desc) {
	m.diskUsageBytes.Describe(ch)
	m.diskTotalBytes.Describe(ch)
	m.diskUtilizationPercentage.Describe(ch)
	m.diskCheckDurationSeconds.Describe(ch)
	m.cleanupOperationsTotal.Describe(ch)
	m.cleanupErrorsTotal.Describe(ch)
	m.filesDeletedTotal.Describe(ch)
	m.bytesFreedTotal.Describe(ch)
	m.cleanupDurationSeconds.Describe(ch)
	m.filesProcessedTotal.Describe(ch)
	m.fileParsingErrorsTotal.Describe(ch)
}

// Collect implements the Collector interface
func (m *DiskManagerMetrics) Collect(ch chan<- prometheus.Metric) {
	m.diskUsageBytes.Collect(ch)
	m.diskTotalBytes.Collect(ch)
	m.diskUtilizationPercentage.Collect(ch)
	m.diskCheckDurationSeconds.Collect(ch)
	m.cleanupOperationsTotal.Collect(ch)
	m.cleanupErrorsTotal.Collect(ch)
	m.filesDeletedTotal.Collect(ch)
	m.bytesFreedTotal.Collect(ch)
	m.cleanupDurationSeconds.Collect(ch)
	m.filesProcessedTotal.Collect(ch)
	m.fileParsingErrorsTotal.Collect(ch)
}

// UpdateDiskUsage updates disk usage metrics
func (m *DiskManagerMetrics) UpdateDiskUsage(usedBytes, totalBytes uint64) {
	m.diskUsageBytes.Set(float64(usedBytes))
	m.diskTotalBytes.Set(float64(totalBytes))

	var utilizationPercentage float64
	if totalBytes > 0 {
		utilizationPercentage = float64(usedBytes) / float64(totalBytes) * PercentageFactor
	}
	m.diskUtilizationPercentage.Set(utilizationPercentage)
}

// RecordDiskCheckDuration records the time taken to check disk usage
func (m *DiskManagerMetrics) RecordDiskCheckDuration(duration float64) {
	m.diskCheckDurationSeconds.Observe(duration)
}

// RecordCleanupOperation records a cleanup operation
func (m *DiskManagerMetrics) RecordCleanupOperation(policy, status string) {
	m.cleanupOperationsTotal.WithLabelValues(policy, status).Inc()
}

// RecordCleanupError records a cleanup error
func (m *DiskManagerMetrics) RecordCleanupError(policy, errorType string) {
	m.cleanupErrorsTotal.WithLabelValues(policy, errorType).Inc()
}

// RecordFilesDeleted records the number of files deleted
func (m *DiskManagerMetrics) RecordFilesDeleted(policy string, count float64) {
	m.filesDeletedTotal.WithLabelValues(policy).Add(count)
}

// RecordBytesFreed records the number of bytes freed
func (m *DiskManagerMetrics) RecordBytesFreed(policy string, bytes float64) {
	m.bytesFreedTotal.WithLabelValues(policy).Add(bytes)
}

// RecordCleanupDuration records the duration of a cleanup operation
func (m *DiskManagerMetrics) RecordCleanupDuration(policy string, duration float64) {
	m.cleanupDurationSeconds.WithLabelValues(policy).Observe(duration)
}

// RecordFileProcessed records a file being processed
func (m *DiskManagerMetrics) RecordFileProcessed(policy, action string) {
	m.filesProcessedTotal.WithLabelValues(policy, action).Inc()
}

// RecordFileParsingError records a file parsing error
func (m *DiskManagerMetrics) RecordFileParsingError(errorType string) {
	m.fileParsingErrorsTotal.WithLabelValues(errorType).Inc()
}
