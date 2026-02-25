package datastore

import (
	"fmt"
	"strconv"

	"gorm.io/gorm"
)

// Compile-time check that MySQLStore implements DatabaseInspector.
var _ DatabaseInspector = (*MySQLStore)(nil)

// GetEngineDetails returns MySQL-specific engine metadata collected from
// sql.DB.Stats() and SHOW GLOBAL STATUS.
func (s *MySQLStore) GetEngineDetails() (EngineDetails, error) {
	details := &MySQLDetails{}

	// Go connection pool stats from database/sql
	sqlDB, err := s.DB.DB()
	if err == nil {
		stats := sqlDB.Stats()
		details.ActiveConnections = stats.InUse
		details.IdleConnections = stats.Idle
		details.MaxConnections = stats.MaxOpenConnections
	}

	// SHOW GLOBAL STATUS — single query, parse into map
	statusVars, err := queryGlobalStatus(s.DB)
	if err != nil {
		return EngineDetails{MySQL: details}, fmt.Errorf("mysql: query global status: %w", err)
	}

	details.ThreadsRunning = parseStatusInt(statusVars["Threads_running"])
	details.ThreadsCached = parseStatusInt(statusVars["Threads_cached"])
	details.ThreadsIdle = max(parseStatusInt(statusVars["Threads_connected"])-details.ThreadsRunning, 0)
	details.TotalCreated = parseStatusInt64(statusVars["Connections"])
	details.ConnectionErrors = sumConnectionErrors(statusVars)

	// InnoDB & Locks
	details.LockWaits = parseStatusInt64(statusVars["Innodb_row_lock_waits"])
	details.AvgLockWaitMs = parseStatusFloat(statusVars["Innodb_row_lock_time_avg"])
	details.TableLocksWaited = parseStatusInt64(statusVars["Table_locks_waited"])
	details.TableLocksImmediate = parseStatusInt64(statusVars["Table_locks_immediate"])

	// InnoDB buffer pool hit rate
	readRequests := parseStatusFloat(statusVars["Innodb_buffer_pool_read_requests"])
	reads := parseStatusFloat(statusVars["Innodb_buffer_pool_reads"])
	if readRequests > 0 {
		details.BufferPoolHitRate = ((readRequests - reads) / readRequests) * 100
	}
	details.BufferPoolSizeBytes = parseStatusInt64(statusVars["Innodb_buffer_pool_bytes_data"])

	// Deadlocks (MySQL 8.0.18+, may not exist in older versions)
	details.Deadlocks = parseStatusInt64(statusVars["Innodb_deadlocks"])

	return EngineDetails{MySQL: details}, nil
}

// GetTableStats returns per-table row counts and sizes from information_schema.
func (s *MySQLStore) GetTableStats() ([]TableStats, error) {
	var results []TableStats
	err := s.DB.Raw(`
		SELECT
			table_name AS name,
			table_rows AS row_count,
			(data_length + index_length) AS size_bytes,
			engine
		FROM information_schema.tables
		WHERE table_schema = DATABASE()
		  AND table_type = 'BASE TABLE'
		ORDER BY size_bytes DESC
	`).Scan(&results).Error
	if err != nil {
		return nil, fmt.Errorf("mysql: get table stats: %w", err)
	}

	// Compute usage percentage
	var totalSize int64
	for i := range results {
		totalSize += results[i].SizeBytes
	}
	if totalSize > 0 {
		for i := range results {
			results[i].UsagePct = float64(results[i].SizeBytes) / float64(totalSize) * 100
		}
	}
	return results, nil
}

// GetDetectionRate24h returns hourly detection counts for the last 24 hours.
func (s *MySQLStore) GetDetectionRate24h() ([]HourlyCount, error) {
	var results []HourlyCount
	err := s.DB.Raw(`
		SELECT DATE_FORMAT(date, '%Y-%m-%dT%H:00:00Z') AS hour, COUNT(*) AS count
		FROM notes
		WHERE date > DATE_SUB(UTC_TIMESTAMP(), INTERVAL 24 HOUR)
		GROUP BY hour
		ORDER BY hour
	`).Scan(&results).Error
	if err != nil {
		return nil, fmt.Errorf("mysql: get 24h detection rate: %w", err)
	}
	return results, nil
}

// GetDetectionRateDaily returns daily detection counts for the specified number of days.
func (s *MySQLStore) GetDetectionRateDaily(days int) ([]DailyCount, error) {
	if days <= 0 {
		return nil, fmt.Errorf("mysql: days must be positive, got %d", days)
	}
	var results []DailyCount
	err := s.DB.Raw(`
		SELECT DATE_FORMAT(date, '%Y-%m-%d') AS date, COUNT(*) AS count
		FROM notes
		WHERE date > DATE_SUB(UTC_TIMESTAMP(), INTERVAL ? DAY)
		GROUP BY date
		ORDER BY date
	`, days).Scan(&results).Error
	if err != nil {
		return nil, fmt.Errorf("mysql: get daily detection rate: %w", err)
	}
	return results, nil
}

// --- MySQL status helpers ---

// queryGlobalStatus executes SHOW GLOBAL STATUS and returns a map of variable names to values.
func queryGlobalStatus(db *gorm.DB) (map[string]string, error) {
	type statusRow struct {
		VariableName string `gorm:"column:Variable_name"`
		Value        string `gorm:"column:Value"`
	}

	var rows []statusRow
	if err := db.Raw("SHOW GLOBAL STATUS").Scan(&rows).Error; err != nil {
		return nil, err
	}

	result := make(map[string]string, len(rows))
	for i := range rows {
		result[rows[i].VariableName] = rows[i].Value
	}
	return result, nil
}

// sumConnectionErrors sums all Connection_errors_* status variables.
func sumConnectionErrors(status map[string]string) int64 {
	prefixes := []string{
		"Connection_errors_accept",
		"Connection_errors_internal",
		"Connection_errors_max_connections",
		"Connection_errors_peer_address",
		"Connection_errors_select",
		"Connection_errors_tcpwrap",
	}
	var total int64
	for _, key := range prefixes {
		total += parseStatusInt64(status[key])
	}
	return total
}

// parseStatusInt parses a status variable string as int.
func parseStatusInt(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}

// parseStatusInt64 parses a status variable string as int64.
func parseStatusInt64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

// parseStatusFloat parses a status variable string as float64.
func parseStatusFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// Ensure MySQLStore satisfies gorm.DB access.
var _ interface{ GetDB() *gorm.DB } = (*MySQLStore)(nil)
