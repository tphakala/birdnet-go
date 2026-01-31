package datastore

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// SQLiteStore implements StoreInterface for SQLite databases
type SQLiteStore struct {
	Settings  *conf.Settings
	telemetry *DatastoreTelemetry
	DataStore
}

func validateSQLiteConfig() error {
	// Add validation logic for SQLite configuration
	// Return an error if the configuration is invalid
	return nil
}

// getDiskSpace returns available disk space for the given path using diskmanager
func getDiskSpace(path string) (uint64, error) {
	// Get directory containing the database file
	dir := filepath.Dir(path)

	// Use diskmanager for disk space check
	availableSpace, err := diskmanager.GetAvailableSpace(dir)
	if err != nil {
		return 0, errors.New(err).
			Component("datastore").
			Category(errors.CategorySystem).
			Context("operation", "get_disk_space").
			Context("path", dir).
			Build()
	}

	return availableSpace, nil
}

// checkWritePermission checks if we have write permission to the directory
func checkWritePermission(path string) error {
	// Create a temporary file to test write permissions
	tempFile := filepath.Join(filepath.Dir(path), ".tmp_write_test")
	f, err := os.OpenFile(tempFile, os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // G304: tempFile derived from database path
	if err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategorySystem).
			Context("operation", "check_write_permission").
			Context("directory", filepath.Dir(path)).
			Build()
	}
	if err := f.Close(); err != nil {
		// Log but don't fail permission check
		GetLogger().Warn("Failed to close temp file", logger.Error(err))
	}
	if err := os.Remove(tempFile); err != nil {
		// Log but don't fail permission check
		GetLogger().Warn("Failed to remove temp file", logger.Error(err))
	}
	return nil
}

// createBackup creates a timestamped backup of the SQLite database file
func (s *SQLiteStore) createBackup(dbPath string) error {
	// Check if source database exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil // No need to backup if database doesn't exist yet
	}

	// Get database file size
	dbInfo, err := os.Stat(dbPath)
	if err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategorySystem).
			Context("operation", "get_database_file_info").
			Context("db_path", dbPath).
			Build()
	}

	// Check available disk space
	availableSpace, err := getDiskSpace(dbPath)
	if err != nil {
		return err
	}

	requiredSpace := uint64(dbInfo.Size()) + 1024*1024 // #nosec G115 -- file size safe conversion, Add 1MB buffer

	if availableSpace < requiredSpace {
		return errors.Newf("insufficient disk space for backup").
			Component("datastore").
			Category(errors.CategorySystem).
			Context("operation", "create_backup").
			Context("required_bytes", fmt.Sprintf("%d", requiredSpace)).
			Context("available_bytes", fmt.Sprintf("%d", availableSpace)).
			Build()
	}

	// Check if we have write permissions in the backup directory
	if err := checkWritePermission(dbPath); err != nil {
		return err
	}

	// Create timestamp for backup file
	timestamp := time.Now().Format("20060102_150405")
	backupPath := fmt.Sprintf("%s.backup_%s", dbPath, timestamp)

	// Open source file
	source, err := os.Open(dbPath) //nolint:gosec // G304: dbPath is from application settings
	if err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategorySystem).
			Context("operation", "open_source_database").
			Context("db_path", dbPath).
			Build()
	}
	defer func() {
		if err := source.Close(); err != nil {
			GetLogger().Warn("Failed to close source database", logger.Error(err))
		}
	}()

	// Create backup file
	destination, err := os.Create(backupPath) //nolint:gosec // G304: backupPath derived from dbPath (application settings)
	if err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategorySystem).
			Context("operation", "create_backup_file").
			Context("backup_path", backupPath).
			Build()
	}
	defer func() {
		if err := destination.Close(); err != nil {
			GetLogger().Warn("Failed to close backup file", logger.Error(err))
		}
	}()

	// Copy the file
	if _, err := io.Copy(destination, source); err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategorySystem).
			Context("operation", "copy_database").
			Context("source", dbPath).
			Context("destination", backupPath).
			Build()
	}

	GetLogger().Info("Created database backup", logger.String("path", backupPath))
	return nil
}

// Open initializes the SQLite database connection
func (s *SQLiteStore) Open() error {
	// Get database path from settings
	dbPath := s.Settings.Output.SQLite.Path

	// Initialize telemetry integration
	telemetryEnabled := s.Settings != nil && s.Settings.Sentry.Enabled
	s.telemetry = NewDatastoreTelemetry(telemetryEnabled, dbPath)

	// Validate system resources before opening database
	if err := ValidateResourceAvailability(dbPath, "open_database"); err != nil {
		if s.telemetry != nil {
			s.telemetry.CaptureEnhancedError(err, "open_database", s)
		}
		return err
	}

	// Log database opening
	GetLogger().Info("Opening SQLite database",
		logger.String("path", dbPath))

	// Create database directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategorySystem).
			Context("operation", "create_database_directory").
			Context("directory", filepath.Dir(dbPath)).
			Build()
	}

	// Configure GORM logger with metrics if available
	var gormLogger gormlogger.Interface
	if s.Settings.Debug {
		// Use debug log level with lower slow threshold
		gormLogger = NewGormLogger(500*time.Millisecond, gormlogger.Info, s.metrics)
	} else {
		// Use default settings with metrics
		gormLogger = NewGormLogger(500*time.Millisecond, gormlogger.Warn, s.metrics)
	}

	// Open SQLite database with GORM
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		enhancedErr := errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "open_sqlite_database").
			Context("db_path", dbPath).
			Build()

		// Send to telemetry with enhanced context
		if s.telemetry != nil {
			s.telemetry.CaptureEnhancedError(enhancedErr, "open_sqlite_database", s)
		}

		return enhancedErr
	}

	// Set SQLite pragmas for better performance
	sqlDB, err := db.DB()
	if err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_underlying_sqldb").
			Build()
	}

	// Set pragmas
	pragmas := []string{
		"PRAGMA foreign_keys=ON",    // required for foreign key constraints
		"PRAGMA journal_mode=WAL",   // faster writes
		"PRAGMA synchronous=NORMAL", // faster writes
		"PRAGMA cache_size=-4000",   // increase cache size
		"PRAGMA temp_store=MEMORY",  // faster writes
		"PRAGMA busy_timeout=30000", // wait up to 30s for locks (critical for concurrent access)
	}

	for _, pragma := range pragmas {
		if _, err := sqlDB.Exec(pragma); err != nil {
			GetLogger().Warn("Failed to set pragma",
				logger.String("pragma", pragma),
				logger.Error(err))
		}
	}

	// Store the database connection
	s.DB = db

	// Log successful connection
	GetLogger().Info("SQLite database opened successfully",
		logger.String("path", dbPath),
		logger.String("journal_mode", "WAL"),
		logger.String("synchronous", "NORMAL"))

	// Validate resources before migration
	if err := ValidateResourceAvailability(dbPath, "migration"); err != nil {
		if s.telemetry != nil {
			s.telemetry.CaptureEnhancedError(err, "pre_migration_validation", s)
		}
		return err
	}

	// Perform auto-migration
	if err := performAutoMigration(db, s.Settings.Debug, "SQLite", dbPath); err != nil {
		// Send migration error to telemetry with enhanced context
		if s.telemetry != nil {
			s.telemetry.CaptureEnhancedError(err, "auto_migration", s)
		}
		return err
	}

	// Start monitoring if metrics are available
	if s.metrics != nil {
		// Monitoring intervals:
		// - 30s for connection pool: Provides timely visibility into connection usage patterns
		//   and potential exhaustion without overwhelming the metrics system
		// - 5m for database stats: Table sizes and row counts change less frequently,
		//   so a longer interval reduces overhead while still capturing growth trends
		s.StartMonitoring(30*time.Second, 5*time.Minute)
	}

	return nil
}

// Close closes the SQLite database connection
func (s *SQLiteStore) Close() error {
	if s.DB != nil {
		// Stop monitoring before closing database
		s.StopMonitoring()

		// Log database closing
		GetLogger().Info("Closing SQLite database",
			logger.String("path", s.Settings.Output.SQLite.Path))

		sqlDB, err := s.DB.DB()
		if err != nil {
			return errors.New(err).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "get_underlying_sqldb").
				Build()
		}

		if err := sqlDB.Close(); err != nil {
			GetLogger().Error("Failed to close SQLite database",
				logger.String("path", s.Settings.Output.SQLite.Path),
				logger.Error(err))
			return err
		}

		// Log successful closure
		GetLogger().Info("SQLite database closed successfully",
			logger.String("path", s.Settings.Output.SQLite.Path))
		return nil
	}
	return nil
}

// Optimize performs database optimization operations (VACUUM and ANALYZE)
func (s *SQLiteStore) Optimize(ctx context.Context) error {
	if s.DB == nil {
		return errors.Newf("database connection is not initialized").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "optimize").
			Build()
	}

	optimizeStart := time.Now()
	optimizeLogger := GetLogger().With(logger.String("operation", "optimize"), logger.String("db_type", "SQLite"))

	optimizeLogger.Info("Starting database optimization")

	// Check for context cancellation
	select {
	case <-ctx.Done():
		return errors.New(ctx.Err()).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "optimize").
			Context("stage", "initialization").
			Context("reason", "context_cancelled").
			Build()
	default:
	}

	// Get database size before optimization
	var sizeBefore int64
	if err := s.DB.WithContext(ctx).Raw("SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()").Row().Scan(&sizeBefore); err != nil {
		enhancedErr := errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_db_size_before").
			Build()
		optimizeLogger.Warn("Failed to get database size before optimization", logger.Error(enhancedErr))
	}

	// Run ANALYZE to update SQLite's internal statistics
	analyzeStart := time.Now()
	optimizeLogger.Debug("Running ANALYZE to update query planner statistics")

	// Check for context cancellation before ANALYZE
	select {
	case <-ctx.Done():
		return errors.New(ctx.Err()).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "optimize").
			Context("stage", "before_analyze").
			Context("reason", "context_cancelled").
			Build()
	default:
	}

	if err := s.DB.WithContext(ctx).Exec("ANALYZE").Error; err != nil {
		enhancedErr := errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "analyze").
			Context("stage", "sqlite_analyze").
			Build()
		optimizeLogger.Error("ANALYZE failed", logger.Error(enhancedErr))
		return enhancedErr
	}
	optimizeLogger.Info("ANALYZE completed", logger.Duration("duration", time.Since(analyzeStart)))

	// Run VACUUM to reclaim unused space
	vacuumStart := time.Now()
	optimizeLogger.Debug("Running VACUUM to reclaim unused space")

	// Check for context cancellation before VACUUM
	select {
	case <-ctx.Done():
		return errors.New(ctx.Err()).
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "optimize").
			Context("stage", "before_vacuum").
			Context("reason", "context_cancelled").
			Build()
	default:
	}

	if err := s.DB.WithContext(ctx).Exec("VACUUM").Error; err != nil {
		enhancedErr := errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "vacuum").
			Context("stage", "sqlite_vacuum").
			Build()
		optimizeLogger.Error("VACUUM failed", logger.Error(enhancedErr))
		return enhancedErr
	}

	// Get database size after optimization
	var sizeAfter int64
	if err := s.DB.WithContext(ctx).Raw("SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()").Row().Scan(&sizeAfter); err != nil {
		enhancedErr := errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_db_size_after").
			Build()
		optimizeLogger.Warn("Failed to get database size after optimization", logger.Error(enhancedErr))
	}

	// Calculate space saved
	spaceSaved := sizeBefore - sizeAfter
	percentSaved := float64(0)
	if sizeBefore > 0 {
		percentSaved = float64(spaceSaved) / float64(sizeBefore) * 100
	}

	optimizeLogger.Info("Database optimization completed",
		logger.Duration("total_duration", time.Since(optimizeStart)),
		logger.Duration("vacuum_duration", time.Since(vacuumStart)),
		logger.Int64("size_before_bytes", sizeBefore),
		logger.Int64("size_after_bytes", sizeAfter),
		logger.Int64("space_saved_bytes", spaceSaved),
		logger.String("space_saved_percent", fmt.Sprintf("%.2f%%", percentSaved)))

	return nil
}

// UpdateNote updates specific fields of a note in SQLite
func (s *SQLiteStore) UpdateNote(id string, updates map[string]any) error {
	return s.DB.Model(&Note{}).Where("id = ?", id).Updates(updates).Error
}

// GetDBPath returns the database file path for telemetry integration
func (s *SQLiteStore) GetDBPath() string {
	if s.Settings != nil && s.Settings.Output.SQLite.Path != "" {
		return s.Settings.Output.SQLite.Path
	}
	return ""
}

// CheckpointWAL forces a checkpoint of the Write-Ahead Log to ensure all changes are written to the main database file.
// This is important for graceful shutdown to prevent data loss.
func (s *SQLiteStore) CheckpointWAL() error {
	// Check for nil DB connection
	if s.DB == nil {
		return errors.Newf("database connection is nil").
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "wal_checkpoint").
			Context("error_type", "nil_connection").
			Build()
	}

	// PRAGMA wal_checkpoint(TRUNCATE) will:
	// 1. Copy all frames from WAL to the database file
	// 2. Truncate the WAL file to zero bytes
	// 3. Ensure all changes are persisted
	if err := s.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
		// Return error without logging (caller will handle logging)
		return errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "wal_checkpoint").
			Context("mode", "truncate").
			Build()
	}

	GetLogger().Info("SQLite WAL checkpoint completed successfully")
	return nil
}

// GetDatabaseStats returns basic runtime statistics about the SQLite database.
// Returns partial stats with ErrDBNotConnected if the database is unreachable.
// The Connected field in the returned stats indicates if the DB is reachable.
func (s *SQLiteStore) GetDatabaseStats() (*DatabaseStats, error) {
	// Defensive guard for nil Settings (e.g., in custom test setups)
	location := ""
	if s.Settings != nil {
		location = s.Settings.Output.SQLite.Path
	}

	stats := &DatabaseStats{
		Type:      DialectSQLite,
		Connected: false,
		Location:  location,
	}

	// Check connection - return partial stats with error if unavailable
	if s.DB == nil {
		return stats, ErrDBNotConnected
	}

	sqlDB, err := s.DB.DB()
	if err != nil {
		return stats, ErrDBNotConnected
	}

	if err := sqlDB.Ping(); err != nil {
		return stats, ErrDBNotConnected
	}
	stats.Connected = true

	// Get database size (ignore errors, size stays 0)
	if size, sizeErr := s.getDatabaseSize(); sizeErr == nil {
		stats.SizeBytes = size
	}

	// Get total detections (ignore errors, count stays 0)
	if count, countErr := s.getTableRowCount("notes"); countErr == nil {
		stats.TotalDetections = count
	}

	return stats, nil
}
