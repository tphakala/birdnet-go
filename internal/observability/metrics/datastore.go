// Package metrics provides datastore metrics for observability
package metrics

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// DatastoreMetrics contains Prometheus metrics for datastore operations
type DatastoreMetrics struct {
	registry *prometheus.Registry

	// Database operation metrics
	dbOperationsTotal      *prometheus.CounterVec
	dbOperationDuration    *prometheus.HistogramVec
	dbOperationErrorsTotal *prometheus.CounterVec

	// Transaction metrics
	dbTransactionsTotal       *prometheus.CounterVec
	dbTransactionDuration     *prometheus.HistogramVec
	dbTransactionRetriesTotal *prometheus.CounterVec
	dbTransactionErrorsTotal  *prometheus.CounterVec

	// Connection and performance metrics
	dbConnectionsActiveGauge prometheus.Gauge
	dbConnectionsIdleGauge   prometheus.Gauge
	dbConnectionsMaxGauge    prometheus.Gauge
	dbQueryResultSizeHist    *prometheus.HistogramVec

	// Note operations metrics
	noteOperationsTotal     *prometheus.CounterVec
	noteOperationDuration   *prometheus.HistogramVec
	noteLockOperationsTotal *prometheus.CounterVec
	noteLockDuration        *prometheus.HistogramVec

	// Search and query metrics
	searchOperationsTotal   *prometheus.CounterVec
	searchOperationDuration *prometheus.HistogramVec
	searchResultSizeHist    *prometheus.HistogramVec
	searchFilterComplexity  *prometheus.HistogramVec

	// Analytics metrics
	analyticsOperationsTotal   *prometheus.CounterVec
	analyticsOperationDuration *prometheus.HistogramVec
	analyticsQueryComplexity   *prometheus.HistogramVec

	// Cache metrics (for sun times cache)
	cacheOperationsTotal *prometheus.CounterVec
	cacheSizeGauge       prometheus.Gauge
	cacheHitRatio        prometheus.Gauge

	// Weather data metrics
	weatherDataOperationsTotal *prometheus.CounterVec
	weatherDataDuration        *prometheus.HistogramVec

	// Image cache metrics
	imageCacheOperationsTotal *prometheus.CounterVec
	imageCacheDuration        *prometheus.HistogramVec
	imageCacheSizeGauge       prometheus.Gauge

	// Database size and growth metrics
	dbSizeBytesGauge      prometheus.Gauge
	dbTableRowCountGauge  *prometheus.GaugeVec
	dbIndexSizeBytesGauge *prometheus.GaugeVec

	// Lock contention metrics
	lockContentionTotal   *prometheus.CounterVec
	lockWaitTimeHistogram *prometheus.HistogramVec
	activeLockCountGauge  prometheus.Gauge

	// Backup and maintenance metrics
	backupOperationsTotal      *prometheus.CounterVec
	backupDuration             *prometheus.HistogramVec
	maintenanceOperationsTotal *prometheus.CounterVec

	// collectors is a slice of all collectors for easier iteration
	collectors []prometheus.Collector
}

// NewDatastoreMetrics creates and registers new datastore metrics
func NewDatastoreMetrics(registry *prometheus.Registry) (*DatastoreMetrics, error) {
	m := &DatastoreMetrics{registry: registry}
	if err := m.initMetrics(); err != nil {
		return nil, err
	}
	if err := registry.Register(m); err != nil {
		return nil, err
	}
	return m, nil
}

// initMetrics initializes all Prometheus metrics
func (m *DatastoreMetrics) initMetrics() error {
	// Database operation metrics
	m.dbOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "datastore_db_operations_total",
			Help: "Total number of database operations",
		},
		[]string{"operation", "table", "status"}, // operation: save, get, delete, update; status: success, error
	)

	m.dbOperationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "datastore_db_operation_duration_seconds",
			Help:    "Time taken for database operations",
			Buckets: prometheus.ExponentialBuckets(BucketStart1ms, BucketFactor2, BucketCount15), // 1ms to ~32s
		},
		[]string{"operation", "table"},
	)

	m.dbOperationErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "datastore_db_operation_errors_total",
			Help: "Total number of database operation errors",
		},
		[]string{"operation", "table", "error_type"},
	)

	// Transaction metrics
	m.dbTransactionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "datastore_db_transactions_total",
			Help: "Total number of database transactions",
		},
		[]string{"status"}, // status: committed, rollback, timeout
	)

	m.dbTransactionDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "datastore_db_transaction_duration_seconds",
			Help:    "Time taken for database transactions",
			Buckets: prometheus.ExponentialBuckets(BucketStart1ms, BucketFactor2, BucketCount15), // 1ms to ~32s
		},
		[]string{"operation"},
	)

	m.dbTransactionRetriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "datastore_db_transaction_retries_total",
			Help: "Total number of transaction retries",
		},
		[]string{"operation", "retry_reason"},
	)

	m.dbTransactionErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "datastore_db_transaction_errors_total",
			Help: "Total number of transaction errors",
		},
		[]string{"operation", "error_type"},
	)

	// Connection metrics
	m.dbConnectionsActiveGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "datastore_db_connections_active",
		Help: "Number of active database connections",
	})

	m.dbConnectionsIdleGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "datastore_db_connections_idle",
		Help: "Number of idle database connections",
	})

	m.dbConnectionsMaxGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "datastore_db_connections_max",
		Help: "Maximum number of database connections",
	})

	m.dbQueryResultSizeHist = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "datastore_db_query_result_size_rows",
			Help:    "Number of rows returned by database queries",
			Buckets: []float64{1, 10, 50, 100, 500, 1000, 5000, 10000, 50000},
		},
		[]string{"operation", "table"},
	)

	// Note operations metrics
	m.noteOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "datastore_note_operations_total",
			Help: "Total number of note operations",
		},
		[]string{"operation", "status"}, // operation: save, get, delete, update
	)

	m.noteOperationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "datastore_note_operation_duration_seconds",
			Help:    "Time taken for note operations",
			Buckets: prometheus.ExponentialBuckets(BucketStart1ms, BucketFactor2, BucketCount12), // 1ms to ~4s
		},
		[]string{"operation"},
	)

	m.noteLockOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "datastore_note_lock_operations_total",
			Help: "Total number of note lock operations",
		},
		[]string{"operation", "status"}, // operation: lock, unlock, check
	)

	m.noteLockDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "datastore_note_lock_duration_seconds",
			Help:    "Time taken for note lock operations",
			Buckets: prometheus.ExponentialBuckets(BucketStart1ms, BucketFactor2, BucketCount10), // 1ms to ~1s
		},
		[]string{"operation"},
	)

	// Search and query metrics
	m.searchOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "datastore_search_operations_total",
			Help: "Total number of search operations",
		},
		[]string{"search_type", "status"},
	)

	m.searchOperationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "datastore_search_operation_duration_seconds",
			Help:    "Time taken for search operations",
			Buckets: prometheus.ExponentialBuckets(BucketStart10ms, BucketFactor2, BucketCount12), // 10ms to ~40s
		},
		[]string{"search_type"},
	)

	m.searchResultSizeHist = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "datastore_search_result_size_rows",
			Help:    "Number of results returned by search operations",
			Buckets: []float64{1, 10, 50, 100, 500, 1000, 5000, 10000},
		},
		[]string{"search_type"},
	)

	m.searchFilterComplexity = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "datastore_search_filter_complexity",
			Help:    "Complexity score of search filters applied",
			Buckets: []float64{1, 2, 3, 5, 8, 13, 21}, // Fibonacci sequence for complexity
		},
		[]string{"search_type"},
	)

	// Analytics metrics
	m.analyticsOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "datastore_analytics_operations_total",
			Help: "Total number of analytics operations",
		},
		[]string{"analytics_type", "status"},
	)

	m.analyticsOperationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "datastore_analytics_operation_duration_seconds",
			Help:    "Time taken for analytics operations",
			Buckets: prometheus.ExponentialBuckets(BucketStart10ms, BucketFactor2, BucketCount15), // 10ms to ~5min
		},
		[]string{"analytics_type"},
	)

	m.analyticsQueryComplexity = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "datastore_analytics_query_complexity",
			Help:    "Complexity score of analytics queries",
			Buckets: []float64{1, 2, 3, 5, 8, 13, 21, 34}, // Extended Fibonacci
		},
		[]string{"analytics_type"},
	)

	// Cache metrics
	m.cacheOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "datastore_cache_operations_total",
			Help: "Total number of cache operations",
		},
		[]string{"cache_type", "operation", "result"}, // result: hit, miss
	)

	m.cacheSizeGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "datastore_cache_size_entries",
		Help: "Current number of entries in caches",
	})

	m.cacheHitRatio = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "datastore_cache_hit_ratio",
		Help: "Cache hit ratio (0.0 to 1.0)",
	})

	// Weather data metrics
	m.weatherDataOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "datastore_weather_data_operations_total",
			Help: "Total number of weather data operations",
		},
		[]string{"operation", "status"},
	)

	m.weatherDataDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "datastore_weather_data_duration_seconds",
			Help:    "Time taken for weather data operations",
			Buckets: prometheus.ExponentialBuckets(BucketStart1ms, BucketFactor2, BucketCount10), // 1ms to ~1s
		},
		[]string{"operation"},
	)

	// Image cache metrics
	m.imageCacheOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "datastore_image_cache_operations_total",
			Help: "Total number of image cache operations",
		},
		[]string{"operation", "status"},
	)

	m.imageCacheDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "datastore_image_cache_duration_seconds",
			Help:    "Time taken for image cache operations",
			Buckets: prometheus.ExponentialBuckets(BucketStart1ms, BucketFactor2, BucketCount10), // 1ms to ~1s
		},
		[]string{"operation"},
	)

	m.imageCacheSizeGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "datastore_image_cache_size_entries",
		Help: "Current number of entries in image cache",
	})

	// Database size metrics
	m.dbSizeBytesGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "datastore_db_size_bytes",
		Help: "Total database size in bytes",
	})

	m.dbTableRowCountGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "datastore_db_table_row_count",
		Help: "Number of rows in database tables",
	}, []string{"table"})

	m.dbIndexSizeBytesGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "datastore_db_index_size_bytes",
		Help: "Size of database indexes in bytes",
	}, []string{"table", "index"})

	// Lock contention metrics
	m.lockContentionTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "datastore_lock_contention_total",
			Help: "Total number of lock contentions",
		},
		[]string{"lock_type", "contention_reason"},
	)

	m.lockWaitTimeHistogram = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "datastore_lock_wait_time_seconds",
			Help:    "Time spent waiting for locks",
			Buckets: prometheus.ExponentialBuckets(BucketStart1ms, BucketFactor2, BucketCount12), // 1ms to ~4s
		},
		[]string{"lock_type"},
	)

	m.activeLockCountGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "datastore_active_locks_count",
		Help: "Current number of active locks",
	})

	// Backup and maintenance metrics
	m.backupOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "datastore_backup_operations_total",
			Help: "Total number of backup operations",
		},
		[]string{"operation", "status"},
	)

	m.backupDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "datastore_backup_duration_seconds",
			Help:    "Time taken for backup operations",
			Buckets: prometheus.ExponentialBuckets(BucketStart100ms, BucketFactor2, BucketCount15), // 100ms to ~54min
		},
		[]string{"operation"},
	)

	m.maintenanceOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "datastore_maintenance_operations_total",
			Help: "Total number of maintenance operations",
		},
		[]string{"operation", "status"},
	)

	// Initialize collectors slice with all metrics
	m.collectors = []prometheus.Collector{
		m.dbOperationsTotal,
		m.dbOperationDuration,
		m.dbOperationErrorsTotal,
		m.dbTransactionsTotal,
		m.dbTransactionDuration,
		m.dbTransactionRetriesTotal,
		m.dbTransactionErrorsTotal,
		m.dbConnectionsActiveGauge,
		m.dbConnectionsIdleGauge,
		m.dbConnectionsMaxGauge,
		m.dbQueryResultSizeHist,
		m.noteOperationsTotal,
		m.noteOperationDuration,
		m.noteLockOperationsTotal,
		m.noteLockDuration,
		m.searchOperationsTotal,
		m.searchOperationDuration,
		m.searchResultSizeHist,
		m.searchFilterComplexity,
		m.analyticsOperationsTotal,
		m.analyticsOperationDuration,
		m.analyticsQueryComplexity,
		m.cacheOperationsTotal,
		m.cacheSizeGauge,
		m.cacheHitRatio,
		m.weatherDataOperationsTotal,
		m.weatherDataDuration,
		m.imageCacheOperationsTotal,
		m.imageCacheDuration,
		m.imageCacheSizeGauge,
		m.dbSizeBytesGauge,
		m.dbTableRowCountGauge,
		m.dbIndexSizeBytesGauge,
		m.lockContentionTotal,
		m.lockWaitTimeHistogram,
		m.activeLockCountGauge,
		m.backupOperationsTotal,
		m.backupDuration,
		m.maintenanceOperationsTotal,
	}

	return nil
}

// Describe implements the Collector interface
func (m *DatastoreMetrics) Describe(ch chan<- *prometheus.Desc) {
	for _, collector := range m.collectors {
		collector.Describe(ch)
	}
}

// Collect implements the Collector interface
func (m *DatastoreMetrics) Collect(ch chan<- prometheus.Metric) {
	for _, collector := range m.collectors {
		collector.Collect(ch)
	}
}

// Database operation recording methods

// RecordDbOperation records a database operation
func (m *DatastoreMetrics) RecordDbOperation(operation, table, status string) {
	m.dbOperationsTotal.WithLabelValues(operation, table, status).Inc()
}

// RecordDbOperationDuration records the duration of a database operation
func (m *DatastoreMetrics) RecordDbOperationDuration(operation, table string, duration float64) {
	m.dbOperationDuration.WithLabelValues(operation, table).Observe(duration)
}

// RecordDbOperationError records a database operation error
func (m *DatastoreMetrics) RecordDbOperationError(operation, table, errorType string) {
	m.dbOperationErrorsTotal.WithLabelValues(operation, table, errorType).Inc()
}

// Transaction recording methods

// RecordTransaction records a database transaction
func (m *DatastoreMetrics) RecordTransaction(status string) {
	m.dbTransactionsTotal.WithLabelValues(status).Inc()
}

// RecordTransactionDuration records the duration of a transaction
func (m *DatastoreMetrics) RecordTransactionDuration(operation string, duration float64) {
	m.dbTransactionDuration.WithLabelValues(operation).Observe(duration)
}

// RecordTransactionRetry records a transaction retry
func (m *DatastoreMetrics) RecordTransactionRetry(operation, retryReason string) {
	m.dbTransactionRetriesTotal.WithLabelValues(operation, retryReason).Inc()
}

// RecordTransactionError records a transaction error
func (m *DatastoreMetrics) RecordTransactionError(operation, errorType string) {
	m.dbTransactionErrorsTotal.WithLabelValues(operation, errorType).Inc()
}

// Connection metrics

// UpdateConnectionMetrics updates database connection metrics
func (m *DatastoreMetrics) UpdateConnectionMetrics(active, idle, maxConn int) {
	m.dbConnectionsActiveGauge.Set(float64(active))
	m.dbConnectionsIdleGauge.Set(float64(idle))
	m.dbConnectionsMaxGauge.Set(float64(maxConn))
}

// RecordQueryResultSize records the size of query results
func (m *DatastoreMetrics) RecordQueryResultSize(operation, table string, resultSize int) {
	m.dbQueryResultSizeHist.WithLabelValues(operation, table).Observe(float64(resultSize))
}

// Note operation methods

// RecordNoteOperation records a note operation
func (m *DatastoreMetrics) RecordNoteOperation(operation, status string) {
	m.noteOperationsTotal.WithLabelValues(operation, status).Inc()
}

// RecordNoteOperationDuration records the duration of a note operation
func (m *DatastoreMetrics) RecordNoteOperationDuration(operation string, duration float64) {
	m.noteOperationDuration.WithLabelValues(operation).Observe(duration)
}

// RecordNoteLockOperation records a note lock operation
func (m *DatastoreMetrics) RecordNoteLockOperation(operation, status string) {
	m.noteLockOperationsTotal.WithLabelValues(operation, status).Inc()
}

// RecordNoteLockDuration records the duration of a note lock operation
func (m *DatastoreMetrics) RecordNoteLockDuration(operation string, duration float64) {
	m.noteLockDuration.WithLabelValues(operation).Observe(duration)
}

// Search operation methods

// RecordSearchOperation records a search operation
func (m *DatastoreMetrics) RecordSearchOperation(searchType, status string) {
	m.searchOperationsTotal.WithLabelValues(searchType, status).Inc()
}

// RecordSearchDuration records the duration of a search operation
func (m *DatastoreMetrics) RecordSearchDuration(searchType string, duration float64) {
	m.searchOperationDuration.WithLabelValues(searchType).Observe(duration)
}

// RecordSearchResultSize records the size of search results
func (m *DatastoreMetrics) RecordSearchResultSize(searchType string, resultSize int) {
	m.searchResultSizeHist.WithLabelValues(searchType).Observe(float64(resultSize))
}

// RecordSearchComplexity records the complexity of search filters
func (m *DatastoreMetrics) RecordSearchComplexity(searchType string, complexity float64) {
	m.searchFilterComplexity.WithLabelValues(searchType).Observe(complexity)
}

// Analytics operation methods

// RecordAnalyticsOperation records an analytics operation
func (m *DatastoreMetrics) RecordAnalyticsOperation(analyticsType, status string) {
	m.analyticsOperationsTotal.WithLabelValues(analyticsType, status).Inc()
}

// RecordAnalyticsDuration records the duration of an analytics operation
func (m *DatastoreMetrics) RecordAnalyticsDuration(analyticsType string, duration float64) {
	m.analyticsOperationDuration.WithLabelValues(analyticsType).Observe(duration)
}

// RecordAnalyticsComplexity records the complexity of analytics queries
func (m *DatastoreMetrics) RecordAnalyticsComplexity(analyticsType string, complexity float64) {
	m.analyticsQueryComplexity.WithLabelValues(analyticsType).Observe(complexity)
}

// Cache operation methods

// RecordCacheOperation records a cache operation
func (m *DatastoreMetrics) RecordCacheOperation(cacheType, operation, result string) {
	m.cacheOperationsTotal.WithLabelValues(cacheType, operation, result).Inc()
}

// UpdateCacheMetrics updates cache size and hit ratio metrics
func (m *DatastoreMetrics) UpdateCacheMetrics(size int, hitRatio float64) {
	m.cacheSizeGauge.Set(float64(size))
	m.cacheHitRatio.Set(hitRatio)
}

// Weather data methods

// RecordWeatherDataOperation records a weather data operation
func (m *DatastoreMetrics) RecordWeatherDataOperation(operation, status string) {
	m.weatherDataOperationsTotal.WithLabelValues(operation, status).Inc()
}

// RecordWeatherDataDuration records the duration of weather data operations
func (m *DatastoreMetrics) RecordWeatherDataDuration(operation string, duration float64) {
	m.weatherDataDuration.WithLabelValues(operation).Observe(duration)
}

// Image cache methods

// RecordImageCacheOperation records an image cache operation
func (m *DatastoreMetrics) RecordImageCacheOperation(operation, status string) {
	m.imageCacheOperationsTotal.WithLabelValues(operation, status).Inc()
}

// RecordImageCacheDuration records the duration of image cache operations
func (m *DatastoreMetrics) RecordImageCacheDuration(operation string, duration float64) {
	m.imageCacheDuration.WithLabelValues(operation).Observe(duration)
}

// UpdateImageCacheSize updates the image cache size
func (m *DatastoreMetrics) UpdateImageCacheSize(size int) {
	m.imageCacheSizeGauge.Set(float64(size))
}

// Database size methods

// UpdateDatabaseSize updates database size metrics
func (m *DatastoreMetrics) UpdateDatabaseSize(sizeBytes int64) {
	m.dbSizeBytesGauge.Set(float64(sizeBytes))
}

// UpdateTableRowCount updates table row count metrics
func (m *DatastoreMetrics) UpdateTableRowCount(table string, rowCount int64) {
	m.dbTableRowCountGauge.WithLabelValues(table).Set(float64(rowCount))
}

// UpdateIndexSize updates index size metrics
func (m *DatastoreMetrics) UpdateIndexSize(table, index string, sizeBytes int64) {
	m.dbIndexSizeBytesGauge.WithLabelValues(table, index).Set(float64(sizeBytes))
}

// Lock contention methods

// RecordLockContention records lock contention
func (m *DatastoreMetrics) RecordLockContention(lockType, contentionReason string) {
	m.lockContentionTotal.WithLabelValues(lockType, contentionReason).Inc()
}

// RecordLockWaitTime records time spent waiting for locks
func (m *DatastoreMetrics) RecordLockWaitTime(lockType string, waitTime float64) {
	m.lockWaitTimeHistogram.WithLabelValues(lockType).Observe(waitTime)
}

// UpdateActiveLockCount updates the number of active locks
func (m *DatastoreMetrics) UpdateActiveLockCount(count int) {
	m.activeLockCountGauge.Set(float64(count))
}

// Backup and maintenance methods

// RecordBackupOperation records a backup operation
func (m *DatastoreMetrics) RecordBackupOperation(operation, status string) {
	m.backupOperationsTotal.WithLabelValues(operation, status).Inc()
}

// RecordBackupDuration records backup operation duration
func (m *DatastoreMetrics) RecordBackupDuration(operation string, duration float64) {
	m.backupDuration.WithLabelValues(operation).Observe(duration)
}

// RecordMaintenanceOperation records a maintenance operation
func (m *DatastoreMetrics) RecordMaintenanceOperation(operation, status string) {
	m.maintenanceOperationsTotal.WithLabelValues(operation, status).Inc()
}

// parseTableFromOperation extracts table name from operations like "db_query:notes"
// Returns the operation and table separately, or "unknown" if no table specified
func parseTableFromOperation(operation string) (op, table string) {
	parts := strings.SplitN(operation, ":", SplitPartsCount)
	if len(parts) == SplitPartsCount {
		return parts[0], parts[1]
	}
	// Default table names for specific operations
	switch operation {
	case OpNoteCreate, OpNoteUpdate, OpNoteDelete, OpNoteGet:
		return operation, "notes"
	default:
		return operation, "unknown"
	}
}

// RecordOperation implements the Recorder interface.
// It records various datastore operations with their status.
// For database operations, use format "operation:table" (e.g., "db_query:notes")
// Supported operations: "db_query", "db_insert", "db_update", "db_delete", "transaction",
// "note_create", "note_update", "note_delete", "note_get", "search", "analytics",
// "cache_get", "cache_set", "cache_delete", "weather_data", "image_cache", "backup", "maintenance"
// Status values: "success", "error"
func (m *DatastoreMetrics) RecordOperation(operation, status string) {
	// Parse table from operation for database operations
	op, table := parseTableFromOperation(operation)
	
	// Map generic operations to specific datastore operations
	switch op {
	case OpDbQuery, OpDbInsert, OpDbUpdate, OpDbDelete:
		m.dbOperationsTotal.WithLabelValues(op, table, status).Inc()
	case OpTransaction:
		m.dbTransactionsTotal.WithLabelValues(status).Inc()
	case OpNoteCreate, OpNoteUpdate, OpNoteDelete, OpNoteGet:
		m.noteOperationsTotal.WithLabelValues(op, status).Inc()
	case OpSearch:
		m.searchOperationsTotal.WithLabelValues(OpSearch, status).Inc()
	case OpAnalytics:
		m.analyticsOperationsTotal.WithLabelValues(LabelQuery, status).Inc()
	case OpCacheGet, OpCacheSet, OpCacheDelete:
		m.cacheOperationsTotal.WithLabelValues(LabelSuntimes, op, status).Inc()
	case OpWeatherData:
		m.weatherDataOperationsTotal.WithLabelValues(LabelFetch, status).Inc()
	case OpImageCache:
		m.imageCacheOperationsTotal.WithLabelValues(LabelGet, status).Inc()
	case OpBackup:
		m.backupOperationsTotal.WithLabelValues(LabelCreate, status).Inc()
	case OpMaintenance:
		m.maintenanceOperationsTotal.WithLabelValues(LabelVacuum, status).Inc()
	}
}

// RecordDuration implements the Recorder interface.
// It records the duration of various datastore operations.
// For database operations, use format "operation:table" (e.g., "db_query:notes")
func (m *DatastoreMetrics) RecordDuration(operation string, seconds float64) {
	// Parse table from operation for database operations
	op, table := parseTableFromOperation(operation)
	
	switch op {
	case OpDbQuery, OpDbInsert, OpDbUpdate, OpDbDelete:
		m.dbOperationDuration.WithLabelValues(op, table).Observe(seconds)
	case OpTransaction:
		m.dbTransactionDuration.WithLabelValues(LabelCommit).Observe(seconds)
	case OpNoteLock:
		m.noteLockDuration.WithLabelValues(LabelExclusive).Observe(seconds)
	case OpNoteOperation:
		m.noteOperationDuration.WithLabelValues(LabelUpdate).Observe(seconds)
	case OpSearch:
		m.searchOperationDuration.WithLabelValues(LabelQuery).Observe(seconds)
	case OpAnalytics:
		m.analyticsOperationDuration.WithLabelValues(LabelQuery).Observe(seconds)
	case OpWeatherData:
		m.weatherDataDuration.WithLabelValues(LabelFetch).Observe(seconds)
	case OpImageCache:
		m.imageCacheDuration.WithLabelValues(LabelGet).Observe(seconds)
	case OpBackup:
		m.backupDuration.WithLabelValues(LabelCreate).Observe(seconds)
	case OpLockWait:
		m.lockWaitTimeHistogram.WithLabelValues(LabelNote).Observe(seconds)
	}
}

// RecordError implements the Recorder interface.
// It records errors for various datastore operations.
// For database operations, use format "operation:table" (e.g., "db_query:notes")
func (m *DatastoreMetrics) RecordError(operation, errorType string) {
	// Parse table from operation for database operations
	op, table := parseTableFromOperation(operation)
	
	switch op {
	case OpDbQuery, OpDbInsert, OpDbUpdate, OpDbDelete:
		m.dbOperationErrorsTotal.WithLabelValues(op, table, errorType).Inc()
		// Also increment operation counter with error status
		m.dbOperationsTotal.WithLabelValues(op, table, "error").Inc()
	case OpTransaction:
		m.dbTransactionErrorsTotal.WithLabelValues(LabelCommit, errorType).Inc()
		// Also increment transaction counter with error status
		m.dbTransactionsTotal.WithLabelValues("error").Inc()
	case OpNoteLock:
		// Record as error in note operations
		m.noteLockOperationsTotal.WithLabelValues(LabelExclusive, "error").Inc()
	case OpNoteCreate, OpNoteUpdate, OpNoteDelete, OpNoteGet:
		// Record as error in note operations
		m.noteOperationsTotal.WithLabelValues(op, "error").Inc()
	case OpSearch:
		// Record as error in search operations
		m.searchOperationsTotal.WithLabelValues(OpSearch, "error").Inc()
	case OpAnalytics:
		// Record as error in analytics operations
		m.analyticsOperationsTotal.WithLabelValues(LabelQuery, "error").Inc()
	case "cache", OpCacheGet, OpCacheSet, OpCacheDelete:
		// Record as error in cache operations
		m.cacheOperationsTotal.WithLabelValues(LabelSuntimes, op, "error").Inc()
	case OpWeatherData:
		// Record as error in weather data operations
		m.weatherDataOperationsTotal.WithLabelValues(LabelFetch, "error").Inc()
	case OpImageCache:
		// Record as error in image cache operations
		m.imageCacheOperationsTotal.WithLabelValues(LabelGet, "error").Inc()
	case OpBackup:
		// Record as error in backup operations
		m.backupOperationsTotal.WithLabelValues(LabelCreate, "error").Inc()
	case OpMaintenance:
		// Record as error in maintenance operations
		m.maintenanceOperationsTotal.WithLabelValues(LabelVacuum, "error").Inc()
	}
}
