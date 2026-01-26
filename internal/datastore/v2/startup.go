package v2

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-sql-driver/mysql"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
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
	ErrV2DatabaseNotFound = errors.New("v2 database not found")
	// ErrV2DatabaseCorrupted indicates the v2 database is corrupted or unreadable.
	ErrV2DatabaseCorrupted = errors.New("v2 database corrupted or unreadable")
)

// StartupState represents the result of checking migration state at startup.
type StartupState struct {
	// MigrationStatus is the current migration status (IDLE, COMPLETED, etc.)
	MigrationStatus entities.MigrationStatus
	// V2Available indicates whether the v2 database exists and is readable.
	V2Available bool
	// LegacyRequired indicates whether the legacy database is still needed.
	LegacyRequired bool
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
	dataDir := filepath.Dir(settings.Output.SQLite.Path)
	v2Path := filepath.Join(dataDir, "birdnet_v2.db")

	// Check if v2 database file exists
	if _, err := os.Stat(v2Path); os.IsNotExist(err) {
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			Error:           nil,
		}
	}

	// Open v2 database in read-only mode to check state
	dsn := v2Path + "?mode=ro"
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

	// Check if v2_migration_state table exists
	var count int64
	tableName := v2TablePrefix + "migration_state"
	err = db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		settings.Output.MySQL.Database, tableName).Scan(&count).Error
	if err != nil {
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			Error:           ErrV2DatabaseNotFound,
		}
	}
	if count == 0 {
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
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
