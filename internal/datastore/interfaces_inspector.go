package datastore

import "github.com/tphakala/birdnet-go/internal/datastore/dbstats"

// DatabaseInspector provides engine-specific metadata, per-table breakdowns,
// and detection rate histograms for the database dashboard. This interface is
// separate from StoreInterface to keep concerns clean.
//
// Implemented by SQLiteStore and MySQLStore. The API handler type-asserts
// the store to DatabaseInspector; if the store doesn't implement it, the
// dashboard sections return null.
type DatabaseInspector interface {
	// GetEngineDetails returns engine-specific metadata.
	// For SQLite stores, EngineDetails.SQLite is populated.
	// For MySQL stores, EngineDetails.MySQL is populated.
	GetEngineDetails() (EngineDetails, error)

	// GetTableStats returns per-table row counts, sizes, and usage percentages.
	GetTableStats() ([]TableStats, error)

	// GetDetectionRate24h returns hourly detection counts for the last 24 hours.
	GetDetectionRate24h() ([]HourlyCount, error)

	// GetDetectionRateDaily returns daily detection counts for the specified
	// number of days. Used for the migration view's 30-day sparkline.
	GetDetectionRateDaily(days int) ([]DailyCount, error)
}

// EngineDetails holds engine-specific metadata. Exactly one of SQLite or MySQL
// is populated depending on the active database engine.
type EngineDetails struct {
	SQLite *SQLiteDetails `json:"sqlite,omitempty"`
	MySQL  *MySQLDetails  `json:"mysql,omitempty"`
}

// SQLiteDetails contains SQLite-specific engine metadata collected from
// PRAGMAs, file system stats, and atomic counters.
type SQLiteDetails struct {
	EngineVersion  string  `json:"engine_version"`  // sqlite_version()
	JournalMode    string  `json:"journal_mode"`    // PRAGMA journal_mode
	PageSize       int     `json:"page_size"`       // PRAGMA page_size (bytes)
	IntegrityCheck string  `json:"integrity_check"` // "ok" or error (cached, run daily)
	LastVacuumAt   *string `json:"last_vacuum_at"`  // ISO 8601 timestamp from _metadata table

	// WAL & Locks
	WALSizeBytes   int64 `json:"wal_size_bytes"`   // os.Stat on -wal file
	WALCheckpoints int64 `json:"wal_checkpoints"`  // from PRAGMA wal_checkpoint
	FreelistPages  int   `json:"freelist_pages"`   // PRAGMA freelist_count
	CacheSizeBytes int64 `json:"cache_size_bytes"` // PRAGMA cache_size converted to bytes
	BusyTimeouts   int64 `json:"busy_timeouts"`    // SQLITE_BUSY count from atomic counters
}

// MySQLDetails contains MySQL-specific engine metadata collected from
// sql.DB.Stats() and SHOW GLOBAL STATUS.
type MySQLDetails struct {
	// Connection Pool (from sql.DB.Stats() + SHOW GLOBAL STATUS)
	ActiveConnections int   `json:"active_connections"`
	IdleConnections   int   `json:"idle_connections"`
	MaxConnections    int   `json:"max_connections"`
	ThreadsIdle       int   `json:"threads_idle"`      // Threads_connected - Threads_running
	TotalCreated      int64 `json:"total_created"`     // Connections (cumulative)
	ConnectionErrors  int64 `json:"connection_errors"` // sum of Connection_errors_*
	ThreadsRunning    int   `json:"threads_running"`
	ThreadsCached     int   `json:"threads_cached"`

	// InnoDB & Locks (from SHOW GLOBAL STATUS)
	BufferPoolHitRate   float64 `json:"buffer_pool_hit_rate"`   // percentage
	BufferPoolSizeBytes int64   `json:"buffer_pool_size_bytes"` // Innodb_buffer_pool_bytes_data
	LockWaits           int64   `json:"lock_waits"`             // Innodb_row_lock_waits
	Deadlocks           int64   `json:"deadlocks"`              // Innodb_deadlocks (MySQL 8.0.18+)
	AvgLockWaitMs       float64 `json:"avg_lock_wait_ms"`       // Innodb_row_lock_time_avg
	TableLocksWaited    int64   `json:"table_locks_waited"`
	TableLocksImmediate int64   `json:"table_locks_immediate"`
}

// TableStats describes a single database table's size and row count.
type TableStats struct {
	Name      string  `json:"name"`
	RowCount  int64   `json:"row_count"`
	SizeBytes int64   `json:"size_bytes"`
	UsagePct  float64 `json:"usage_pct"` // percentage of total DB size
	Engine    string  `json:"engine"`    // MySQL only; empty for SQLite
}

// PerformanceStats contains current database performance metrics derived from
// the ring buffer and atomic counters.
type PerformanceStats struct {
	ReadLatencyAvgMs  float64 `json:"read_latency_avg_ms"`
	ReadLatencyMaxMs  float64 `json:"read_latency_max_ms"` // max from latest tick (reset-on-read)
	WriteLatencyAvgMs float64 `json:"write_latency_avg_ms"`
	WriteLatencyMaxMs float64 `json:"write_latency_max_ms"` // max from latest tick (reset-on-read)
	QueriesPerSec     float64 `json:"queries_per_sec"`
	QueriesLastHour   int64   `json:"queries_last_hour"` // sum of ring buffer db.queries_per_sec × 5s
	SlowQueryCount    int64   `json:"slow_query_count"`  // cumulative > 100ms
}

// DBCountersProvider is implemented by stores that expose atomic query counters.
// Used by the collector and API handler to access latency tracking data.
type DBCountersProvider interface {
	GetDBCounters() *dbstats.Counters
}

// HourlyCount represents the number of detections in a single hour.
type HourlyCount struct {
	Hour  string `json:"hour"` // ISO 8601: "2026-02-24T14:00:00Z"
	Count int64  `json:"count"`
}

// DailyCount represents the number of detections on a single day.
type DailyCount struct {
	Date  string `json:"date"` // "2026-02-24"
	Count int64  `json:"count"`
}
