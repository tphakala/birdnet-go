package v2

import (
	"fmt"
	"os"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// dbStartupTimeout is the timeout for database startup operations.
const dbStartupTimeout = "5s"

// Startup state checking errors.
var (
	// ErrV2DatabaseNotFound indicates the v2 database does not exist.
	ErrV2DatabaseNotFound = errors.NewStd("v2 database not found")
	// ErrV2DatabaseCorrupted indicates the v2 database is corrupted or unreadable.
	ErrV2DatabaseCorrupted = errors.NewStd("v2 database corrupted or unreadable")
)

// StartupState represents the result of checking migration state at startup.
type StartupState struct {
	// MigrationStatus is the current migration status (IDLE, COMPLETED, etc.)
	MigrationStatus entities.MigrationStatus
	// V2Available indicates whether the v2 database exists and is readable.
	V2Available bool
	// LegacyRequired indicates whether the legacy database is still needed.
	LegacyRequired bool
	// FreshInstall indicates no database exists (new installation).
	FreshInstall bool
	// Error contains any error that occurred during state checking.
	Error error
}

// CheckMigrationStateBeforeStartup determines the migration state without opening the legacy database.
// This allows the application to skip legacy database initialization when migration is complete.
//
// Returns:
//   - StartupState with MigrationStatus=COMPLETED, LegacyRequired=false if migration is done
//   - StartupState with LegacyRequired=true if legacy database is still needed
//   - StartupState with Error set if v2 database check fails
func CheckMigrationStateBeforeStartup(settings *conf.Settings) StartupState {
	if settings.Output.MySQL.Enabled {
		return checkMySQLMigrationState(settings)
	}
	return checkSQLiteMigrationState(settings)
}

// checkSQLiteMigrationState checks migration state for SQLite deployments.
func checkSQLiteMigrationState(settings *conf.Settings) StartupState {
	configuredPath := settings.Output.SQLite.Path
	v2MigrationPath := V2MigrationPathFromConfigured(configuredPath)

	// Check if configured database exists (could be legacy OR fresh v2)
	configuredExists := true
	if _, err := os.Stat(configuredPath); os.IsNotExist(err) {
		configuredExists = false
	}

	// Check if migration-era v2 database exists
	v2MigrationExists := true
	if _, err := os.Stat(v2MigrationPath); os.IsNotExist(err) {
		v2MigrationExists = false
	}

	// Fresh install: neither database exists
	if !configuredExists && !v2MigrationExists {
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  false,
			FreshInstall:    true,
			Error:           nil,
		}
	}

	// If v2 migration database doesn't exist, check if configured path is a v2 DB
	if !v2MigrationExists {
		if configuredExists {
			// Check if the configured database is a fresh v2 database
			if isV2Database := CheckSQLiteHasV2Schema(configuredPath); isV2Database {
				// This is a fresh v2 install (restart after initial fresh install)
				return StartupState{
					MigrationStatus: entities.MigrationStatusCompleted,
					V2Available:     true,
					LegacyRequired:  false,
					FreshInstall:    false,
					Error:           nil,
				}
			}
		}
		// Configured path exists but is not v2 = legacy mode
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			FreshInstall:    false,
			Error:           nil,
		}
	}

	// Open v2 database in read-only mode to check state
	dsn := v2MigrationPath + "?mode=ro"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			Error:           fmt.Errorf("%w: %w", ErrV2DatabaseCorrupted, err),
		}
	}

	// Close the connection when done
	sqlDB, err := db.DB()
	if err != nil {
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			Error:           fmt.Errorf("%w: failed to get underlying DB: %w", ErrV2DatabaseCorrupted, err),
		}
	}
	defer func() { _ = sqlDB.Close() }()

	// Read migration state
	var state entities.MigrationState
	if err := db.First(&state).Error; err != nil {
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     true,
			LegacyRequired:  true,
			Error:           fmt.Errorf("%w: %w", ErrV2DatabaseCorrupted, err),
		}
	}

	// Determine if legacy is required based on migration status
	legacyRequired := state.State != entities.MigrationStatusCompleted

	return StartupState{
		MigrationStatus: state.State,
		V2Available:     true,
		LegacyRequired:  legacyRequired,
		Error:           nil,
	}
}

// checkMySQLMigrationState checks migration state for MySQL deployments.
func checkMySQLMigrationState(settings *conf.Settings) StartupState {
	// Build MySQL DSN using mysql.Config for proper credential escaping and timeouts
	cfg := mysql.Config{
		User:   settings.Output.MySQL.Username,
		Passwd: settings.Output.MySQL.Password,
		Net:    "tcp",
		Addr:   fmt.Sprintf("%s:%s", settings.Output.MySQL.Host, settings.Output.MySQL.Port),
		DBName: settings.Output.MySQL.Database,
		Params: map[string]string{
			"charset":      "utf8mb4",
			"parseTime":    "True",
			"loc":          "Local",
			"timeout":      dbStartupTimeout,
			"readTimeout":  dbStartupTimeout,
			"writeTimeout": dbStartupTimeout,
		},
	}
	dsn := cfg.FormatDSN()

	db, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			Error:           fmt.Errorf("%w: %w", ErrV2DatabaseCorrupted, err),
		}
	}

	sqlDB, err := db.DB()
	if err != nil {
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			Error:           fmt.Errorf("%w: failed to get underlying DB: %w", ErrV2DatabaseCorrupted, err),
		}
	}
	defer func() { _ = sqlDB.Close() }()

	// Check if legacy 'notes' table exists
	var legacyCount int64
	err = db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = 'notes'",
		settings.Output.MySQL.Database).Scan(&legacyCount).Error
	if err != nil {
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			Error:           fmt.Errorf("failed to check legacy tables: %w", err),
		}
	}
	legacyExists := legacyCount > 0

	// Check if v2_migration_state table exists (migration-era v2 tables)
	var v2MigrationCount int64
	tableName := v2TablePrefix + "migration_state"
	err = db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		settings.Output.MySQL.Database, tableName).Scan(&v2MigrationCount).Error
	if err != nil {
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			Error:           ErrV2DatabaseNotFound,
		}
	}
	v2MigrationExists := v2MigrationCount > 0

	// Check if fresh v2 schema exists (no prefix - detections table)
	var freshV2Count int64
	err = db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = 'detections'",
		settings.Output.MySQL.Database).Scan(&freshV2Count).Error
	if err != nil {
		freshV2Count = 0 // Ignore error, just means no fresh v2 tables
	}
	freshV2Exists := freshV2Count > 0

	// Fresh install: no tables exist at all
	if !legacyExists && !v2MigrationExists && !freshV2Exists {
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  false,
			FreshInstall:    true,
			Error:           nil,
		}
	}

	// Fresh v2 exists (restart after fresh install) - no migration tables needed
	if freshV2Exists && !v2MigrationExists {
		return StartupState{
			MigrationStatus: entities.MigrationStatusCompleted,
			V2Available:     true,
			LegacyRequired:  false,
			FreshInstall:    false,
			Error:           nil,
		}
	}

	// No v2 tables at all but legacy exists = legacy mode
	if !v2MigrationExists && !freshV2Exists {
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			FreshInstall:    false,
			Error:           nil,
		}
	}

	// Read migration state from v2 table
	var state entities.MigrationState
	if err := db.Table(tableName).First(&state).Error; err != nil {
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     true,
			LegacyRequired:  true,
			Error:           fmt.Errorf("%w: %w", ErrV2DatabaseCorrupted, err),
		}
	}

	legacyRequired := state.State != entities.MigrationStatusCompleted

	return StartupState{
		MigrationStatus: state.State,
		V2Available:     true,
		LegacyRequired:  legacyRequired,
		Error:           nil,
	}
}

// IsV2OnlyModeAvailable returns true if the system can run in v2-only mode.
// This is true when migration is completed and v2 database is available.
func IsV2OnlyModeAvailable(settings *conf.Settings) bool {
	state := CheckMigrationStateBeforeStartup(settings)
	return state.MigrationStatus == entities.MigrationStatusCompleted && state.V2Available
}

// ShouldSkipLegacyDatabase returns true if the legacy database should not be opened.
// This happens when migration is complete and we're running in v2-only mode.
func ShouldSkipLegacyDatabase(settings *conf.Settings) bool {
	state := CheckMigrationStateBeforeStartup(settings)
	return !state.LegacyRequired && state.MigrationStatus == entities.MigrationStatusCompleted
}

// CheckSQLiteHasV2Schema checks if a SQLite database at the given path is a fully initialized v2 database.
// This is used to distinguish between a legacy database and a fresh v2 database.
// Returns true only if the database has:
//  1. The migration_state table (v2 schema indicator)
//  2. A migration state record with COMPLETED status
//
// This prevents false positives from partially initialized databases.
func CheckSQLiteHasV2Schema(dbPath string) bool {
	// Check if file exists first - GORM/SQLite with mode=ro may still create an empty file
	// even when opening in read-only mode, which causes issues when checking non-existent
	// legacy databases (they get created as empty files during the check).
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return false
	}

	dsn := dbPath + "?mode=ro"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return false
	}

	// Ensure cleanup even if db.DB() fails
	sqlDB, err := db.DB()
	if err != nil {
		// Can't get underlying connection, but we need to try closing GORM's session
		return false
	}
	defer func() { _ = sqlDB.Close() }()

	// Check if migration_state table exists (v2 schema indicator)
	var tableCount int64
	err = db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='migration_state'").Scan(&tableCount).Error
	if err != nil || tableCount == 0 {
		return false
	}

	// Check if migration state is COMPLETED (fully initialized)
	var state entities.MigrationState
	err = db.First(&state).Error
	if err != nil {
		return false
	}

	// Only return true if the database is fully initialized (COMPLETED)
	return state.State == entities.MigrationStatusCompleted
}

// CheckMySQLHasFreshV2Schema checks if a MySQL database has fresh v2 tables (without v2_ prefix).
// This is used to determine whether to use v2_ prefix for migration mode or no prefix for fresh installs.
// Returns true if the fresh v2 schema exists (migration_state table without prefix).
func CheckMySQLHasFreshV2Schema(settings *conf.Settings) bool {
	// Build MySQL DSN
	cfg := mysql.Config{
		User:   settings.Output.MySQL.Username,
		Passwd: settings.Output.MySQL.Password,
		Net:    "tcp",
		Addr:   fmt.Sprintf("%s:%s", settings.Output.MySQL.Host, settings.Output.MySQL.Port),
		DBName: settings.Output.MySQL.Database,
		Params: map[string]string{
			"charset":      "utf8mb4",
			"parseTime":    "True",
			"loc":          "Local",
			"timeout":      dbStartupTimeout,
			"readTimeout":  dbStartupTimeout,
			"writeTimeout": dbStartupTimeout,
		},
	}
	dsn := cfg.FormatDSN()

	db, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return false
	}

	sqlDB, err := db.DB()
	if err != nil {
		return false
	}
	defer func() { _ = sqlDB.Close() }()

	// Check if fresh v2 migration_state table exists (no prefix)
	var tableCount int64
	err = db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = 'migration_state'",
		settings.Output.MySQL.Database).Scan(&tableCount).Error
	if err != nil || tableCount == 0 {
		return false
	}

	// Check if migration state is COMPLETED
	var state entities.MigrationState
	if err := db.Table("migration_state").First(&state).Error; err != nil {
		return false
	}

	return state.State == entities.MigrationStatusCompleted
}

// CheckAndConsolidateAtStartup checks for and performs database consolidation at startup.
// This should be called BEFORE CheckMigrationStateBeforeStartup to ensure the database
// is at the configured path after migration.
//
// The function handles two scenarios:
// 1. Interrupted consolidation: Resume if state file exists
// 2. Pending consolidation: V2 at migration path with COMPLETED status but configured path is legacy
//
// For SQLite only - MySQL doesn't need consolidation (uses table prefixes).
//
// Parameters:
//   - configuredPath: The user's configured database path
//   - log: Logger for progress messages
//
// Returns:
//   - consolidated: true if consolidation was performed
//   - error: any error that occurred
func CheckAndConsolidateAtStartup(configuredPath string, log logger.Logger) (consolidated bool, err error) {
	dataDir := GetDataDirFromLegacyPath(configuredPath)
	if dataDir == "" {
		// In-memory database, no consolidation needed
		return false, nil
	}

	// Step 1: Check for interrupted consolidation
	resumed, newPath, err := ResumeConsolidation(dataDir, log)
	if err != nil {
		return false, fmt.Errorf("failed to check/resume consolidation: %w", err)
	}
	if resumed {
		log.Info("resumed interrupted consolidation",
			logger.String("path", newPath))
		return true, nil
	}

	// Step 2: Check if consolidation is needed
	v2MigrationPath := V2MigrationPathFromConfigured(configuredPath)

	// Check if v2 migration database exists
	if _, err := os.Stat(v2MigrationPath); os.IsNotExist(err) {
		// No v2 at migration path, nothing to consolidate
		return false, nil
	}

	// Check if v2 migration database has COMPLETED status
	if !CheckSQLiteHasV2Schema(v2MigrationPath) {
		// V2 exists but migration not complete, no consolidation
		return false, nil
	}

	// Check if configured path already has v2 schema (already consolidated)
	if CheckSQLiteHasV2Schema(configuredPath) {
		// Already consolidated, clean up orphaned migration database
		log.Info("configured path already has v2 schema, cleaning up orphaned migration database",
			logger.String("migration_path", v2MigrationPath))
		// Best effort removal of orphaned v2 migration database
		_ = os.Remove(v2MigrationPath)
		cleanupWALFiles(v2MigrationPath)
		return false, nil
	}

	// Step 3: Perform consolidation
	log.Info("performing database consolidation at startup",
		logger.String("v2_migration_path", v2MigrationPath),
		logger.String("configured_path", configuredPath))

	// Generate backup path for legacy database
	backupPath := GenerateBackupPath(configuredPath)

	// Write consolidation state file
	state := &ConsolidationState{
		LegacyPath:     configuredPath,
		V2Path:         v2MigrationPath,
		BackupPath:     backupPath,
		ConfiguredPath: configuredPath,
		StartedAt:      time.Now(),
	}
	if err := WriteConsolidationState(dataDir, state); err != nil {
		return false, fmt.Errorf("failed to write consolidation state: %w", err)
	}

	// Clean up any WAL/SHM files (defensive)
	cleanupWALFiles(configuredPath)
	cleanupWALFiles(v2MigrationPath)

	// Rename legacy → backup (if legacy exists)
	if _, err := os.Stat(configuredPath); err == nil {
		log.Debug("renaming legacy database to backup",
			logger.String("from", configuredPath),
			logger.String("to", backupPath))
		if err := os.Rename(configuredPath, backupPath); err != nil {
			_ = DeleteConsolidationState(dataDir)
			return false, fmt.Errorf("failed to rename legacy database: %w", err)
		}
	}

	// Rename v2 → configured path
	log.Debug("renaming v2 database to configured path",
		logger.String("from", v2MigrationPath),
		logger.String("to", configuredPath))
	if err := os.Rename(v2MigrationPath, configuredPath); err != nil {
		// Rollback: restore legacy from backup if it existed
		if _, statErr := os.Stat(backupPath); statErr == nil {
			log.Warn("v2 rename failed, rolling back",
				logger.Error(err))
			if rollbackErr := os.Rename(backupPath, configuredPath); rollbackErr != nil {
				log.Error("rollback failed - manual intervention required",
					logger.Error(rollbackErr))
				// Return without deleting state file to allow recovery on next boot
				return false, fmt.Errorf("failed to rename v2 database (rollback also failed: %w): %w", rollbackErr, err)
			}
		}
		_ = DeleteConsolidationState(dataDir)
		return false, fmt.Errorf("failed to rename v2 database: %w", err)
	}

	// Delete consolidation state file
	if err := DeleteConsolidationState(dataDir); err != nil {
		log.Warn("failed to delete consolidation state file", logger.Error(err))
	}

	log.Info("database consolidation completed at startup",
		logger.String("database_path", configuredPath),
		logger.String("backup_path", backupPath))

	return true, nil
}
