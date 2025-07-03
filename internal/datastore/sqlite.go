package datastore

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// SQLiteStore implements StoreInterface for SQLite databases
type SQLiteStore struct {
	Settings *conf.Settings
	DataStore
}

func validateSQLiteConfig() error {
	// Add validation logic for SQLite configuration
	// Return an error if the configuration is invalid
	return nil
}

// getDiskSpace returns available disk space for the given path
func getDiskSpace(path string) (uint64, error) {
	var availableSpace uint64

	// OS-specific disk space check
	dir := filepath.Dir(path)

	// Get directory information using OS-agnostic method
	var err error
	availableSpace, err = getDiskFreeSpace(dir)
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
	f, err := os.OpenFile(tempFile, os.O_CREATE|os.O_WRONLY, 0o666)
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
		log.Printf("Failed to close temp file: %v", err)
	}
	if err := os.Remove(tempFile); err != nil {
		// Log but don't fail permission check
		log.Printf("Failed to remove temp file: %v", err)
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
	source, err := os.Open(dbPath)
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
			log.Printf("Failed to close source database: %v", err)
		}
	}()

	// Create backup file
	destination, err := os.Create(backupPath)
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
			log.Printf("Failed to close backup file: %v", err)
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

	log.Printf("Created database backup: %s", backupPath)
	return nil
}

// Open initializes the SQLite database connection
func (s *SQLiteStore) Open() error {
	// Get database path from settings
	dbPath := s.Settings.Output.SQLite.Path
	
	// Log database opening
	getLogger().Info("Opening SQLite database",
		"path", dbPath)

	// Create database directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategorySystem).
			Context("operation", "create_database_directory").
			Context("directory", filepath.Dir(dbPath)).
			Build()
	}

	// Configure GORM logger with metrics if available
	var gormLogger logger.Interface
	if s.Settings.Debug {
		// Use debug log level with lower slow threshold
		gormLogger = NewGormLogger(100*time.Millisecond, logger.Info, s.metrics)
		datastoreLevelVar.Set(slog.LevelDebug)
	} else {
		// Use default settings with metrics
		gormLogger = NewGormLogger(200*time.Millisecond, logger.Warn, s.metrics)
	}

	// Open SQLite database with GORM
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		return errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "open_sqlite_database").
			Context("db_path", dbPath).
			Build()
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
	}

	for _, pragma := range pragmas {
		if _, err := sqlDB.Exec(pragma); err != nil {
			log.Printf("Warning: Failed to set pragma %s: %v", pragma, err)
		}
	}

	// Store the database connection
	s.DB = db
	
	// Log successful connection
	getLogger().Info("SQLite database opened successfully",
		"path", dbPath,
		"journal_mode", "WAL",
		"synchronous", "NORMAL")

	// Perform auto-migration
	if err := performAutoMigration(db, s.Settings.Debug, "SQLite", dbPath); err != nil {
		return err
	}

	// Start monitoring if metrics are available
	if s.metrics != nil {
		// Default intervals: 30s for connection pool, 5m for database stats
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
		getLogger().Info("Closing SQLite database",
			"path", s.Settings.Output.SQLite.Path)
		
		sqlDB, err := s.DB.DB()
		if err != nil {
			return errors.New(err).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "get_underlying_sqldb").
				Build()
		}
		
		if err := sqlDB.Close(); err != nil {
			getLogger().Error("Failed to close SQLite database",
				"path", s.Settings.Output.SQLite.Path,
				"error", err)
			return err
		}
		
		// Log successful closure
		getLogger().Info("SQLite database closed successfully",
			"path", s.Settings.Output.SQLite.Path)
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
	optimizeLogger := getLogger().With("operation", "optimize", "db_type", "SQLite")
	
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
		optimizeLogger.Warn("Failed to get database size before optimization", "error", enhancedErr)
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
		optimizeLogger.Error("ANALYZE failed", "error", enhancedErr)
		return enhancedErr
	}
	optimizeLogger.Info("ANALYZE completed", "duration", time.Since(analyzeStart))
	
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
		optimizeLogger.Error("VACUUM failed", "error", enhancedErr)
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
		optimizeLogger.Warn("Failed to get database size after optimization", "error", enhancedErr)
	}
	
	// Calculate space saved
	spaceSaved := sizeBefore - sizeAfter
	percentSaved := float64(0)
	if sizeBefore > 0 {
		percentSaved = float64(spaceSaved) / float64(sizeBefore) * 100
	}
	
	optimizeLogger.Info("Database optimization completed",
		"total_duration", time.Since(optimizeStart),
		"vacuum_duration", time.Since(vacuumStart),
		"size_before_bytes", sizeBefore,
		"size_after_bytes", sizeAfter,
		"space_saved_bytes", spaceSaved,
		"space_saved_percent", fmt.Sprintf("%.2f%%", percentSaved))
	
	return nil
}

// UpdateNote updates specific fields of a note in SQLite
func (s *SQLiteStore) UpdateNote(id string, updates map[string]interface{}) error {
	return s.DB.Model(&Note{}).Where("id = ?", id).Updates(updates).Error
}
