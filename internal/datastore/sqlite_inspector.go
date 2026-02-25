package datastore

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
)

// Compile-time check that SQLiteStore implements DatabaseInspector.
var _ DatabaseInspector = (*SQLiteStore)(nil)

// GetEngineDetails returns SQLite-specific engine metadata collected from
// PRAGMAs, filesystem stats, and atomic counters.
func (s *SQLiteStore) GetEngineDetails() (EngineDetails, error) {
	details := &SQLiteDetails{}

	// Version query — if this fails, the connection is unusable
	if err := s.DB.Raw("SELECT sqlite_version()").Scan(&details.EngineVersion).Error; err != nil {
		return EngineDetails{}, fmt.Errorf("sqlite: query version: %w", err)
	}

	// Remaining cheap single-row PRAGMAs (best-effort)
	s.DB.Raw("PRAGMA journal_mode").Scan(&details.JournalMode)
	s.DB.Raw("PRAGMA page_size").Scan(&details.PageSize)
	s.DB.Raw("PRAGMA freelist_count").Scan(&details.FreelistPages)

	// Cache size: PRAGMA returns negative = KB, positive = pages
	var cacheSize int
	s.DB.Raw("PRAGMA cache_size").Scan(&cacheSize)
	if cacheSize < 0 {
		details.CacheSizeBytes = int64(-cacheSize) * 1024
	} else {
		details.CacheSizeBytes = int64(cacheSize) * int64(details.PageSize)
	}

	// WAL file size from filesystem
	dbPath := s.Settings.Output.SQLite.Path
	walPath := dbPath + "-wal"
	if fi, err := os.Stat(walPath); err == nil {
		details.WALSizeBytes = fi.Size()
	}

	// WAL checkpoint stats (passive — does not force a checkpoint)
	var checkpointed int
	if err := s.DB.Raw("PRAGMA wal_checkpoint(PASSIVE)").Row().Scan(
		new(int), new(int), &checkpointed,
	); err == nil {
		details.WALCheckpoints = int64(checkpointed)
	}

	// BusyTimeouts from atomic counters
	if c := s.dbCounters; c != nil {
		details.BusyTimeouts = c.BusyTimeouts.Load()
	}

	// Cached integrity check result
	s.integrityMu.RLock()
	if s.integrityResult != "" {
		details.IntegrityCheck = s.integrityResult
	}
	s.integrityMu.RUnlock()

	// Last vacuum timestamp from _metadata table
	var lastVacuum string
	if err := s.DB.Raw("SELECT value FROM _metadata WHERE key = ?", "last_vacuum_at").Scan(&lastVacuum).Error; err == nil && lastVacuum != "" {
		details.LastVacuumAt = &lastVacuum
	}

	return EngineDetails{SQLite: details}, nil
}

// GetTableStats returns per-table row counts and sizes for all user tables.
// Tries the dbstat virtual table first; falls back to row-count proportional estimation.
// Caches dbstat availability to avoid repeated WARN logs when the virtual table
// is not compiled in (requires SQLITE_ENABLE_DBSTAT_VTAB).
func (s *SQLiteStore) GetTableStats() ([]TableStats, error) {
	cached := atomic.LoadInt32(&s.dbstatAvailable)
	if cached == -1 {
		return s.getTableStatsEstimated()
	}

	stats, err := s.getTableStatsViaDBStat()
	if err == nil {
		atomic.StoreInt32(&s.dbstatAvailable, 1)
		return stats, nil
	}

	atomic.StoreInt32(&s.dbstatAvailable, -1)
	return s.getTableStatsEstimated()
}

// getTableStatsViaDBStat uses the dbstat virtual table (requires SQLITE_ENABLE_DBSTAT_VTAB).
func (s *SQLiteStore) getTableStatsViaDBStat() ([]TableStats, error) {
	type dbstatRow struct {
		Name      string
		SizeBytes int64
	}

	var rows []dbstatRow
	err := s.DB.Raw(`
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

	// Compute total size for usage percentage
	var totalSize int64
	for i := range rows {
		totalSize += rows[i].SizeBytes
	}

	stats := make([]TableStats, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		var rowCount int64
		s.DB.Raw("SELECT COUNT(*) FROM " + quoteIdentifier(r.Name)).Scan(&rowCount) //nolint:gosec // table names from sqlite_master

		var usagePct float64
		if totalSize > 0 {
			usagePct = float64(r.SizeBytes) / float64(totalSize) * 100
		}

		stats = append(stats, TableStats{
			Name:      r.Name,
			RowCount:  rowCount,
			SizeBytes: r.SizeBytes,
			UsagePct:  usagePct,
		})
	}
	return stats, nil
}

// getTableStatsEstimated distributes total DB size proportionally by row count.
func (s *SQLiteStore) getTableStatsEstimated() ([]TableStats, error) {
	// Get total DB size from page_count * page_size
	var pageCount, pageSize int64
	s.DB.Raw("PRAGMA page_count").Scan(&pageCount)
	s.DB.Raw("PRAGMA page_size").Scan(&pageSize)
	totalSize := pageCount * pageSize

	// Get all user table names
	var tableNames []string
	s.DB.Raw("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name").Scan(&tableNames)

	if len(tableNames) == 0 {
		return nil, nil
	}

	// Count rows per table
	type tableRow struct {
		name     string
		rowCount int64
	}
	tables := make([]tableRow, 0, len(tableNames))
	var totalRows int64
	for _, name := range tableNames {
		var count int64
		s.DB.Raw("SELECT COUNT(*) FROM " + quoteIdentifier(name)).Scan(&count) //nolint:gosec // table names from sqlite_master
		tables = append(tables, tableRow{name: name, rowCount: count})
		totalRows += count
	}

	// Distribute size proportionally by row count
	stats := make([]TableStats, 0, len(tables))
	for _, t := range tables {
		var sizeBytes int64
		var usagePct float64
		if totalRows > 0 {
			usagePct = float64(t.rowCount) / float64(totalRows) * 100
			sizeBytes = int64(float64(totalSize) * float64(t.rowCount) / float64(totalRows))
		}
		stats = append(stats, TableStats{
			Name:      t.name,
			RowCount:  t.rowCount,
			SizeBytes: sizeBytes,
			UsagePct:  usagePct,
		})
	}
	return stats, nil
}

// GetDetectionRate24h returns hourly detection counts for the last 24 hours.
func (s *SQLiteStore) GetDetectionRate24h() ([]HourlyCount, error) {
	var results []HourlyCount
	err := s.DB.Raw(`
		SELECT strftime('%Y-%m-%dT%H:00:00Z', date) AS hour, COUNT(*) AS count
		FROM notes
		WHERE date > datetime('now', '-24 hours')
		GROUP BY hour
		ORDER BY hour
	`).Scan(&results).Error
	if err != nil {
		return nil, fmt.Errorf("sqlite: get 24h detection rate: %w", err)
	}
	return results, nil
}

// GetDetectionRateDaily returns daily detection counts for the specified number of days.
func (s *SQLiteStore) GetDetectionRateDaily(days int) ([]DailyCount, error) {
	if days <= 0 {
		return nil, fmt.Errorf("sqlite: days must be positive, got %d", days)
	}
	var results []DailyCount
	err := s.DB.Raw(`
		SELECT strftime('%Y-%m-%d', date) AS date, COUNT(*) AS count
		FROM notes
		WHERE date > datetime('now', ?)
		GROUP BY date
		ORDER BY date
	`, fmt.Sprintf("-%d days", days)).Scan(&results).Error
	if err != nil {
		return nil, fmt.Errorf("sqlite: get daily detection rate: %w", err)
	}
	return results, nil
}

// quoteIdentifier wraps a SQL identifier in double quotes for safe use in queries.
func quoteIdentifier(name string) string {
	// Double any existing double quotes to escape them
	escaped := strings.ReplaceAll(name, `"`, `""`)
	return `"` + escaped + `"`
}

// RunIntegrityCheck executes PRAGMA quick_check and caches the result.
// Called by the monitoring goroutine on a daily schedule.
func (s *SQLiteStore) RunIntegrityCheck() {
	var results []string
	if err := s.DB.Raw("PRAGMA quick_check").Scan(&results).Error; err != nil {
		results = []string{err.Error()}
	}
	result := strings.Join(results, "; ")
	if result == "" {
		result = "ok"
	}
	s.integrityMu.Lock()
	s.integrityResult = result
	s.integrityMu.Unlock()
}

// EnsureMetadataTable creates the _metadata key-value table if it doesn't exist.
// Called during store initialization.
func (s *SQLiteStore) EnsureMetadataTable() error {
	return s.DB.Exec("CREATE TABLE IF NOT EXISTS _metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)").Error
}

// RecordVacuumTimestamp stores the current UTC time as the last vacuum timestamp.
// Called after a successful VACUUM operation.
func (s *SQLiteStore) RecordVacuumTimestamp() error {
	return s.DB.Exec(
		"INSERT OR REPLACE INTO _metadata (key, value) VALUES (?, ?)",
		"last_vacuum_at",
		time.Now().UTC().Format(time.RFC3339),
	).Error
}

// Ensure SQLiteStore also satisfies gorm.DB access for the inspector queries above.
var _ interface{ GetDB() *gorm.DB } = (*SQLiteStore)(nil)
