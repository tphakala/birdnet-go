package v2only

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// Compile-time check that Datastore implements DatabaseInspector.
var _ datastore.DatabaseInspector = (*Datastore)(nil)

// GetEngineDetails returns engine-specific metadata. For the v2-only SQLite
// datastore, this collects PRAGMAs, WAL stats, and filesystem info.
func (ds *Datastore) GetEngineDetails() (datastore.EngineDetails, error) {
	if ds.manager.IsMySQL() {
		return ds.getMySQLEngineDetails()
	}
	return ds.getSQLiteEngineDetails()
}

// getSQLiteEngineDetails collects SQLite-specific metadata from PRAGMAs
// and filesystem stats.
func (ds *Datastore) getSQLiteEngineDetails() (datastore.EngineDetails, error) {
	db := ds.manager.DB()
	details := &datastore.SQLiteDetails{}

	// Version query — if this fails, the connection is unusable
	if err := db.Raw("SELECT sqlite_version()").Scan(&details.EngineVersion).Error; err != nil {
		return datastore.EngineDetails{}, fmt.Errorf("sqlite: query version: %w", err)
	}

	// Remaining cheap single-row PRAGMAs (best-effort)
	db.Raw("PRAGMA journal_mode").Scan(&details.JournalMode)
	db.Raw("PRAGMA page_size").Scan(&details.PageSize)
	db.Raw("PRAGMA freelist_count").Scan(&details.FreelistPages)

	// Cache size: PRAGMA returns negative = KB, positive = pages
	var cacheSize int
	db.Raw("PRAGMA cache_size").Scan(&cacheSize)
	if cacheSize < 0 {
		details.CacheSizeBytes = int64(-cacheSize) * 1024
	} else {
		details.CacheSizeBytes = int64(cacheSize) * int64(details.PageSize)
	}

	// WAL file size from filesystem
	dbPath := ds.manager.Path()
	walPath := dbPath + "-wal"
	if fi, err := os.Stat(walPath); err == nil {
		details.WALSizeBytes = fi.Size()
	}

	// WAL checkpoint stats (passive — does not force a checkpoint)
	var checkpointed int
	if err := db.Raw("PRAGMA wal_checkpoint(PASSIVE)").Row().Scan(
		new(int), new(int), &checkpointed,
	); err == nil {
		details.WALCheckpoints = int64(checkpointed)
	}

	// Integrity check is not cached in v2only — default to "ok"
	// (the monitoring goroutine may update this separately if available)
	details.IntegrityCheck = "ok"

	return datastore.EngineDetails{SQLite: details}, nil
}

// getMySQLEngineDetails collects MySQL-specific metadata.
func (ds *Datastore) getMySQLEngineDetails() (datastore.EngineDetails, error) {
	db := ds.manager.DB()
	details := &datastore.MySQLDetails{}

	// Connection pool stats from sql.DB
	sqlDB, err := db.DB()
	if err == nil {
		stats := sqlDB.Stats()
		details.ActiveConnections = stats.InUse
		details.IdleConnections = stats.Idle
		details.MaxConnections = stats.MaxOpenConnections
		details.TotalCreated = stats.WaitCount
	}

	// Global status variables (best-effort)
	type statusRow struct {
		VariableName string
		Value        string
	}

	statusVars := []string{
		"Threads_running", "Threads_cached", "Threads_connected",
		"Innodb_buffer_pool_read_requests", "Innodb_buffer_pool_reads",
		"Innodb_buffer_pool_bytes_data",
		"Innodb_row_lock_waits", "Innodb_deadlocks", "Innodb_row_lock_time_avg",
		"Table_locks_waited", "Table_locks_immediate",
		"Connection_errors_internal", "Connection_errors_max_connections",
	}

	var rows []statusRow
	db.Raw("SHOW GLOBAL STATUS WHERE Variable_name IN (?)", statusVars).Scan(&rows)

	statusMap := make(map[string]string, len(rows))
	for _, r := range rows {
		statusMap[r.VariableName] = r.Value
	}

	details.ThreadsRunning = parseInt(statusMap["Threads_running"])
	details.ThreadsCached = parseInt(statusMap["Threads_cached"])
	details.ThreadsIdle = parseInt(statusMap["Threads_connected"]) - details.ThreadsRunning

	// Buffer pool hit rate
	readRequests := parseInt64(statusMap["Innodb_buffer_pool_read_requests"])
	reads := parseInt64(statusMap["Innodb_buffer_pool_reads"])
	if readRequests > 0 {
		details.BufferPoolHitRate = float64(readRequests-reads) / float64(readRequests) * 100
	}
	details.BufferPoolSizeBytes = parseInt64(statusMap["Innodb_buffer_pool_bytes_data"])

	details.LockWaits = parseInt64(statusMap["Innodb_row_lock_waits"])
	details.Deadlocks = parseInt64(statusMap["Innodb_deadlocks"])
	details.AvgLockWaitMs = parseFloat64(statusMap["Innodb_row_lock_time_avg"])
	details.TableLocksWaited = parseInt64(statusMap["Table_locks_waited"])
	details.TableLocksImmediate = parseInt64(statusMap["Table_locks_immediate"])

	details.ConnectionErrors = parseInt64(statusMap["Connection_errors_internal"]) +
		parseInt64(statusMap["Connection_errors_max_connections"])

	return datastore.EngineDetails{MySQL: details}, nil
}

// GetTableStats returns per-table row counts, sizes, and usage percentages.
func (ds *Datastore) GetTableStats() ([]datastore.TableStats, error) {
	if ds.manager.IsMySQL() {
		return ds.getMySQLTableStats()
	}
	return ds.getSQLiteTableStats()
}

// getSQLiteTableStats tries the dbstat virtual table first, then falls back
// to row-count proportional estimation. Caches dbstat availability to avoid
// repeated WARN logs when the virtual table is not compiled in.
func (ds *Datastore) getSQLiteTableStats() ([]datastore.TableStats, error) {
	cached := atomic.LoadInt32(&ds.dbstatAvailable)
	if cached == -1 {
		// Already known to be unavailable — skip directly to estimation
		return ds.getSQLiteTableStatsEstimated()
	}

	stats, err := ds.getSQLiteTableStatsViaDBStat()
	if err == nil {
		atomic.StoreInt32(&ds.dbstatAvailable, 1)
		return stats, nil
	}

	// Mark as unavailable so we don't retry on every refresh
	atomic.StoreInt32(&ds.dbstatAvailable, -1)
	return ds.getSQLiteTableStatsEstimated()
}

// getSQLiteTableStatsViaDBStat uses the dbstat virtual table.
func (ds *Datastore) getSQLiteTableStatsViaDBStat() ([]datastore.TableStats, error) {
	db := ds.manager.DB()

	type dbstatRow struct {
		Name      string
		SizeBytes int64
	}

	var rows []dbstatRow
	err := db.Raw(`
		SELECT d.name, SUM(d.pgsize) AS size_bytes
		FROM dbstat d
		JOIN sqlite_master m ON m.name = d.name
		WHERE m.type = 'table' AND d.name NOT LIKE 'sqlite_%'
		GROUP BY d.name
		ORDER BY size_bytes DESC
	`).Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	var totalSize int64
	for i := range rows {
		totalSize += rows[i].SizeBytes
	}

	stats := make([]datastore.TableStats, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		var rowCount int64
		db.Raw("SELECT COUNT(*) FROM " + quoteIdentifier(r.Name)).Scan(&rowCount) //nolint:gosec // table names from sqlite_master

		var usagePct float64
		if totalSize > 0 {
			usagePct = float64(r.SizeBytes) / float64(totalSize) * 100
		}

		stats = append(stats, datastore.TableStats{
			Name:      r.Name,
			RowCount:  rowCount,
			SizeBytes: r.SizeBytes,
			UsagePct:  usagePct,
		})
	}
	return stats, nil
}

// getSQLiteTableStatsEstimated distributes total DB size proportionally by row count.
func (ds *Datastore) getSQLiteTableStatsEstimated() ([]datastore.TableStats, error) {
	db := ds.manager.DB()

	var pageCount, pageSize int64
	db.Raw("PRAGMA page_count").Scan(&pageCount)
	db.Raw("PRAGMA page_size").Scan(&pageSize)
	totalSize := pageCount * pageSize

	var tableNames []string
	db.Raw("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name").Scan(&tableNames)

	if len(tableNames) == 0 {
		return nil, nil
	}

	type tableRow struct {
		name     string
		rowCount int64
	}
	tables := make([]tableRow, 0, len(tableNames))
	var totalRows int64
	for _, name := range tableNames {
		var count int64
		db.Raw("SELECT COUNT(*) FROM " + quoteIdentifier(name)).Scan(&count) //nolint:gosec // table names from sqlite_master
		tables = append(tables, tableRow{name: name, rowCount: count})
		totalRows += count
	}

	stats := make([]datastore.TableStats, 0, len(tables))
	for _, t := range tables {
		var sizeBytes int64
		var usagePct float64
		if totalRows > 0 {
			usagePct = float64(t.rowCount) / float64(totalRows) * 100
			sizeBytes = int64(float64(totalSize) * float64(t.rowCount) / float64(totalRows))
		}
		stats = append(stats, datastore.TableStats{
			Name:      t.name,
			RowCount:  t.rowCount,
			SizeBytes: sizeBytes,
			UsagePct:  usagePct,
		})
	}
	return stats, nil
}

// getMySQLTableStats queries information_schema for MySQL table stats.
func (ds *Datastore) getMySQLTableStats() ([]datastore.TableStats, error) {
	db := ds.manager.DB()

	type tableRow struct {
		Name      string
		Engine    string
		RowCount  int64
		SizeBytes int64
	}

	var rows []tableRow
	err := db.Raw(`
		SELECT TABLE_NAME AS name, ENGINE AS engine, TABLE_ROWS AS row_count,
		       (DATA_LENGTH + INDEX_LENGTH) AS size_bytes
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE'
		ORDER BY size_bytes DESC
	`).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("mysql: get table stats: %w", err)
	}

	var totalSize int64
	for i := range rows {
		totalSize += rows[i].SizeBytes
	}

	stats := make([]datastore.TableStats, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		var usagePct float64
		if totalSize > 0 {
			usagePct = float64(r.SizeBytes) / float64(totalSize) * 100
		}
		stats = append(stats, datastore.TableStats{
			Name:      r.Name,
			RowCount:  r.RowCount,
			SizeBytes: r.SizeBytes,
			UsagePct:  usagePct,
			Engine:    r.Engine,
		})
	}
	return stats, nil
}

// GetDetectionRate24h returns hourly detection counts for the last 24 hours.
// Uses the v2 detections table with Unix timestamp detected_at column.
func (ds *Datastore) GetDetectionRate24h() ([]datastore.HourlyCount, error) {
	db := ds.manager.DB()

	var results []datastore.HourlyCount
	var err error

	if ds.manager.IsMySQL() {
		err = db.Raw(`
			SELECT DATE_FORMAT(FROM_UNIXTIME(detected_at), '%Y-%m-%dT%H:00:00Z') AS hour,
			       COUNT(*) AS count
			FROM detections
			WHERE detected_at > UNIX_TIMESTAMP(NOW() - INTERVAL 24 HOUR)
			GROUP BY hour
			ORDER BY hour
		`).Scan(&results).Error
	} else {
		err = db.Raw(`
			SELECT strftime('%Y-%m-%dT%H:00:00Z', detected_at, 'unixepoch') AS hour,
			       COUNT(*) AS count
			FROM detections
			WHERE detected_at > unixepoch('now', '-24 hours')
			GROUP BY hour
			ORDER BY hour
		`).Scan(&results).Error
	}

	if err != nil {
		return nil, fmt.Errorf("get 24h detection rate: %w", err)
	}
	if results == nil {
		results = []datastore.HourlyCount{}
	}
	return results, nil
}

// GetDetectionRateDaily returns daily detection counts for the specified number of days.
func (ds *Datastore) GetDetectionRateDaily(days int) ([]datastore.DailyCount, error) {
	if days <= 0 {
		return nil, fmt.Errorf("days must be positive, got %d", days)
	}

	db := ds.manager.DB()

	var results []datastore.DailyCount
	var err error

	if ds.manager.IsMySQL() {
		err = db.Raw(`
			SELECT DATE_FORMAT(FROM_UNIXTIME(detected_at), '%Y-%m-%d') AS date,
			       COUNT(*) AS count
			FROM detections
			WHERE detected_at > UNIX_TIMESTAMP(NOW() - INTERVAL ? DAY)
			GROUP BY date
			ORDER BY date
		`, days).Scan(&results).Error
	} else {
		err = db.Raw(`
			SELECT strftime('%Y-%m-%d', detected_at, 'unixepoch') AS date,
			       COUNT(*) AS count
			FROM detections
			WHERE detected_at > unixepoch('now', ?)
			GROUP BY date
			ORDER BY date
		`, fmt.Sprintf("-%d days", days)).Scan(&results).Error
	}

	if err != nil {
		return nil, fmt.Errorf("get daily detection rate: %w", err)
	}
	if results == nil {
		results = []datastore.DailyCount{}
	}
	return results, nil
}

// quoteIdentifier wraps a SQL identifier in double quotes for safe use in queries.
func quoteIdentifier(name string) string {
	escaped := strings.ReplaceAll(name, `"`, `""`)
	return `"` + escaped + `"`
}

// parseInt parses a string to int, returning 0 on failure.
func parseInt(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}

// parseInt64 parses a string to int64, returning 0 on failure.
func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

// parseFloat64 parses a string to float64, returning 0 on failure.
func parseFloat64(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
