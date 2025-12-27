// Package datastore provides monitoring functions for database operations
package datastore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// startConnectionPoolMonitoring starts a goroutine that periodically monitors
// database connection pool statistics
func (ds *DataStore) startConnectionPoolMonitoring(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				getLogger().Info("Connection pool monitoring stopped due to context cancellation")
				return
			case <-ticker.C:
				sqlDB, err := ds.DB.DB()
				if err != nil {
					getLogger().Error("Failed to get SQL DB for monitoring",
						logger.Error(err))
					continue
				}
				
				stats := sqlDB.Stats()
				
				// Update metrics
				ds.metricsMu.RLock()
				metrics := ds.metrics
				ds.metricsMu.RUnlock()
				
				if metrics != nil {
					metrics.UpdateConnectionMetrics(
						stats.InUse,
						stats.Idle,
						stats.MaxOpenConnections,
					)
					
					if stats.WaitCount > 0 {
						metrics.RecordLockContention("connection_pool", "wait_for_connection")
						metrics.RecordLockWaitTime("connection_pool", stats.WaitDuration.Seconds())
					}
				}
				
				getLogger().Info("Connection pool statistics",
					logger.Int("open_connections", stats.OpenConnections),
					logger.Int("in_use", stats.InUse),
					logger.Int("idle", stats.Idle),
					logger.Int64("wait_count", stats.WaitCount),
					logger.Duration("wait_duration", stats.WaitDuration),
					logger.Int64("max_idle_closed", stats.MaxIdleClosed),
					logger.Int64("max_lifetime_closed", stats.MaxLifetimeClosed))

				// Warn if pool is exhausted
				if stats.WaitCount > 0 {
					getLogger().Warn("Connection pool experiencing waits",
						logger.Int64("wait_count", stats.WaitCount),
						logger.Duration("total_wait_duration", stats.WaitDuration))
				}
			}
		}
	}()
}

// startDatabaseMonitoring starts a goroutine that periodically monitors
// database size and table statistics
func (ds *DataStore) startDatabaseMonitoring(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				getLogger().Info("Database monitoring stopped due to context cancellation")
				return
			case <-ticker.C:
				// Get metrics reference once to avoid multiple lock acquisitions
				ds.metricsMu.RLock()
				metrics := ds.metrics
				ds.metricsMu.RUnlock()
				
				// Update database size metrics
				if dbSize, err := ds.getDatabaseSize(); err == nil && metrics != nil {
					metrics.UpdateDatabaseSize(dbSize)
				} else if err != nil {
					getLogger().Error("Failed to get database size",
						logger.Error(err))
				}

				// Update table row counts
				tables := []string{"notes", "results", "image_caches", "note_reviews", "note_locks"}
				for _, table := range tables {
					if count, err := ds.getTableRowCount(table); err == nil && metrics != nil {
						metrics.UpdateTableRowCount(table, count)
					} else if err != nil {
						getLogger().Error("Failed to get table row count",
							logger.String("table", table),
							logger.Error(err))
					}
				}

				// Update active lock count
				if lockCount, err := ds.getActiveLockCount(); err == nil && metrics != nil {
					metrics.UpdateActiveLockCount(lockCount)
				} else if err != nil {
					getLogger().Error("Failed to get active lock count",
						logger.Error(err))
				}
			}
		}
	}()
}

// getDatabaseSize returns the total size of the database in bytes
func (ds *DataStore) getDatabaseSize() (int64, error) {
	var size int64
	
	// SQLite-specific query
	if strings.ToLower(ds.DB.Name()) == "sqlite" {
		// For SQLite, we use page_count * page_size
		err := ds.DB.Raw("SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()").Row().Scan(&size)
		if err != nil {
			return 0, fmt.Errorf("failed to get SQLite database size: %w", err)
		}
		return size, nil
	}
	
	// MySQL-specific query
	if strings.ToLower(ds.DB.Name()) == "mysql" {
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
	ds.monitoringMu.Lock()
	defer ds.monitoringMu.Unlock()
	
	// Stop any existing monitoring first
	if ds.monitoringCancel != nil {
		ds.monitoringCancel()
	}
	
	// Create new context for monitoring lifecycle
	ds.monitoringCtx, ds.monitoringCancel = context.WithCancel(context.Background())
	
	if connectionPoolInterval > 0 {
		ds.startConnectionPoolMonitoring(ds.monitoringCtx, connectionPoolInterval)
		getLogger().Info("Started connection pool monitoring",
			logger.Duration("interval", connectionPoolInterval))
	}

	if databaseStatsInterval > 0 {
		ds.startDatabaseMonitoring(ds.monitoringCtx, databaseStatsInterval)
		getLogger().Info("Started database statistics monitoring",
			logger.Duration("interval", databaseStatsInterval))
	}
}

// StopMonitoring cancels all monitoring goroutines and ensures clean shutdown
func (ds *DataStore) StopMonitoring() {
	ds.monitoringMu.Lock()
	defer ds.monitoringMu.Unlock()
	
	if ds.monitoringCancel != nil {
		getLogger().Info("Stopping all monitoring activities")
		ds.monitoringCancel()
		ds.monitoringCancel = nil
		ds.monitoringCtx = nil
		getLogger().Info("All monitoring activities stopped")
	}
}

// IsMonitoringActive returns true if monitoring is currently active
func (ds *DataStore) IsMonitoringActive() bool {
	ds.monitoringMu.Lock()
	defer ds.monitoringMu.Unlock()
	
	return ds.monitoringCtx != nil && ds.monitoringCtx.Err() == nil
}