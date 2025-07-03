// Package datastore provides monitoring functions for database operations
package datastore

import (
	"fmt"
	"time"
)

// startConnectionPoolMonitoring starts a goroutine that periodically monitors
// database connection pool statistics
func (ds *DataStore) startConnectionPoolMonitoring(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for range ticker.C {
			sqlDB, err := ds.DB.DB()
			if err != nil {
				datastoreLogger.Error("Failed to get SQL DB for monitoring",
					"error", err)
				continue
			}
			
			stats := sqlDB.Stats()
			
			// Update metrics
			if ds.metrics != nil {
				ds.metrics.UpdateConnectionMetrics(
					stats.InUse,
					stats.Idle,
					stats.MaxOpenConnections,
				)
				
				if stats.WaitCount > 0 {
					ds.metrics.RecordLockContention("connection_pool", "wait_for_connection")
					ds.metrics.RecordLockWaitTime("connection_pool", stats.WaitDuration.Seconds())
				}
			}
			
			datastoreLogger.Info("Connection pool statistics",
				"open_connections", stats.OpenConnections,
				"in_use", stats.InUse,
				"idle", stats.Idle,
				"wait_count", stats.WaitCount,
				"wait_duration", stats.WaitDuration,
				"max_idle_closed", stats.MaxIdleClosed,
				"max_lifetime_closed", stats.MaxLifetimeClosed)
				
			// Warn if pool is exhausted
			if stats.WaitCount > 0 {
				datastoreLogger.Warn("Connection pool experiencing waits",
					"wait_count", stats.WaitCount,
					"total_wait_duration", stats.WaitDuration)
			}
		}
	}()
}

// startDatabaseMonitoring starts a goroutine that periodically monitors
// database size and table statistics
func (ds *DataStore) startDatabaseMonitoring(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for range ticker.C {
			// Update database size metrics
			if dbSize, err := ds.getDatabaseSize(); err == nil && ds.metrics != nil {
				ds.metrics.UpdateDatabaseSize(dbSize)
			} else if err != nil {
				datastoreLogger.Error("Failed to get database size",
					"error", err)
			}
			
			// Update table row counts
			tables := []string{"notes", "results", "image_caches", "note_reviews", "note_locks"}
			for _, table := range tables {
				if count, err := ds.getTableRowCount(table); err == nil && ds.metrics != nil {
					ds.metrics.UpdateTableRowCount(table, count)
				} else if err != nil {
					datastoreLogger.Error("Failed to get table row count",
						"table", table,
						"error", err)
				}
			}
			
			// Update active lock count
			if lockCount, err := ds.getActiveLockCount(); err == nil && ds.metrics != nil {
				ds.metrics.UpdateActiveLockCount(lockCount)
			} else if err != nil {
				datastoreLogger.Error("Failed to get active lock count",
					"error", err)
			}
		}
	}()
}

// getDatabaseSize returns the total size of the database in bytes
func (ds *DataStore) getDatabaseSize() (int64, error) {
	var size int64
	
	// SQLite-specific query
	if ds.DB.Name() == "sqlite" {
		// For SQLite, we use page_count * page_size
		err := ds.DB.Raw("SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()").Row().Scan(&size)
		if err != nil {
			return 0, fmt.Errorf("failed to get SQLite database size: %w", err)
		}
		return size, nil
	}
	
	// MySQL-specific query
	if ds.DB.Name() == "mysql" {
		var dbName string
		if err := ds.DB.Raw("SELECT DATABASE()").Scan(&dbName).Error; err != nil {
			return 0, fmt.Errorf("failed to get current database name: %w", err)
		}
		
		err := ds.DB.Raw(`
			SELECT SUM(data_length + index_length) 
			FROM information_schema.tables 
			WHERE table_schema = ?
		`, dbName).Scan(&size).Error
		if err != nil {
			return 0, fmt.Errorf("failed to get MySQL database size: %w", err)
		}
		return size, nil
	}
	
	return 0, fmt.Errorf("unsupported database type: %s", ds.DB.Name())
}

// getTableRowCount returns the number of rows in a specific table
func (ds *DataStore) getTableRowCount(table string) (int64, error) {
	var count int64
	err := ds.DB.Table(table).Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("failed to count rows in table %s: %w", table, err)
	}
	return count, nil
}

// getActiveLockCount returns the number of active note locks
func (ds *DataStore) getActiveLockCount() (int, error) {
	var count int64
	err := ds.DB.Model(&NoteLock{}).Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("failed to count active locks: %w", err)
	}
	return int(count), nil
}

// StartMonitoring initializes all monitoring routines for the datastore
func (ds *DataStore) StartMonitoring(connectionPoolInterval, databaseStatsInterval time.Duration) {
	if connectionPoolInterval > 0 {
		ds.startConnectionPoolMonitoring(connectionPoolInterval)
		datastoreLogger.Info("Started connection pool monitoring",
			"interval", connectionPoolInterval)
	}
	
	if databaseStatsInterval > 0 {
		ds.startDatabaseMonitoring(databaseStatsInterval)
		datastoreLogger.Info("Started database statistics monitoring",
			"interval", databaseStatsInterval)
	}
}