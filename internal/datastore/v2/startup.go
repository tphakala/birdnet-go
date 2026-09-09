package v2

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/go-sql-driver/mysql"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/diagnostics"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/telemetry"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// readOnlyDSN appends mode=ro to a SQLite path, using the correct query separator.
func readOnlyDSN(dbPath string) string {
	sep := "?"
	if strings.Contains(dbPath, "?") {
		sep = "&"
	}
	return dbPath + sep + "mode=ro"
}

// reportStartupError reports a startup state check failure to Sentry telemetry.
// The paths parameter lists file paths that should be anonymized in the error message.
func reportStartupError(dbType, operation string, err error, paths ...string) {
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", "datastore-startup")
		scope.SetTag("db_type", dbType)
		scope.SetTag("operation", operation)
		scope.SetFingerprint([]string{"datastore-startup", dbType, operation})

		scrubbedErr := scrubErrorWithPaths(err.Error(), paths...)
		scope.SetContext("startup_error", map[string]any{
			"db_type":   dbType,
			"operation": operation,
			"error":     scrubbedErr,
		})

		telemetry.CaptureMessage(
			fmt.Sprintf("Startup state check failed: %s (%s)", operation, dbType),
			sentry.LevelWarning,
			"datastore-startup",
		)
	})
}

// logStartupDecision logs the outcome of the startup migration state check.
func logStartupDecision(decision string, fields ...logger.Field) {
	logger.Global().Module("datastore").Info(
		"database startup: state determined",
		append([]logger.Field{logger.String("decision", decision)}, fields...)...,
	)
}

// dbStartupTimeout is the timeout for database startup operations.
const dbStartupTimeout = "5s"

// Startup decision labels. Each value is both logged by
// logStartupDecision and stored in StartupState.Decision so callers
// (diagnostics boot journal) can persist the exact decision.
const (
	// DecisionFreshInstall: neither configured DB nor v2 sidecar exists.
	DecisionFreshInstall = "fresh_install"
	// DecisionV2Restart: configured path already holds a completed v2 DB.
	DecisionV2Restart = "v2_restart"
	// DecisionLegacyMode: only a legacy database exists.
	DecisionLegacyMode = "legacy_mode"
	// DecisionStaleSidecarFallback: corrupt/stale sidecar removed, v2 at configured path.
	DecisionStaleSidecarFallback = "stale_sidecar_fallback"
	// DecisionV2Corrupted: v2 sidecar exists but is unreadable/incomplete.
	DecisionV2Corrupted = "v2_corrupted"
	// DecisionMigrationPrefix prefixes a migration-state decision, e.g. "migration_completed".
	DecisionMigrationPrefix = "migration_"
)

// Startup state checking errors.
var (
	// ErrV2DatabaseNotFound indicates the v2 database does not exist.
	ErrV2DatabaseNotFound = errors.NewStd("v2 database not found")
	// ErrV2DatabaseCorrupted indicates the v2 database is corrupted or unreadable.
	ErrV2DatabaseCorrupted = errors.NewStd("v2 database corrupted or unreadable")
	// ErrV2SchemaCorrupted indicates the v2 database has contaminated or invalid schema.
	// Callers can use errors.Is(err, ErrV2SchemaCorrupted) to trigger self-healing.
	ErrV2SchemaCorrupted = errors.NewStd("v2 database schema corrupted")
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
	// Decision is the startup decision label (Decision* constants), the
	// same string passed to logStartupDecision. Persisted by the
	// diagnostics boot journal.
	Decision string
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

	// Log all path check results for diagnostic visibility
	absConfigured, _ := filepath.Abs(configuredPath)
	absV2Migration, _ := filepath.Abs(v2MigrationPath)
	cwd, _ := os.Getwd()

	var configuredSize, v2MigrationSize int64
	if fi, err := os.Stat(configuredPath); err == nil {
		configuredSize = fi.Size()
	}
	if fi, err := os.Stat(v2MigrationPath); err == nil {
		v2MigrationSize = fi.Size()
	}

	logger.Global().Module("datastore").Info("database startup: path check",
		logger.String("cwd", cwd),
		logger.String("configured_path", configuredPath),
		logger.String("configured_abs", absConfigured),
		logger.Bool("configured_exists", configuredExists),
		logger.Int64("configured_size", configuredSize),
		logger.String("v2_migration_path", v2MigrationPath),
		logger.String("v2_migration_abs", absV2Migration),
		logger.Bool("v2_migration_exists", v2MigrationExists),
		logger.Int64("v2_migration_size", v2MigrationSize),
	)

	// Fresh install: neither database exists
	if !configuredExists && !v2MigrationExists {
		// Safety check: warn if .db files exist in the data directory.
		// Skip if path resolution failed (absConfigured empty) to avoid scanning ".".
		if absConfigured != "" {
			dir := filepath.Dir(absConfigured)
			if entries, err := os.ReadDir(dir); err == nil {
				for _, e := range entries {
					if e.IsDir() || !strings.HasSuffix(e.Name(), ".db") {
						continue
					}
					if info, infoErr := e.Info(); infoErr == nil {
						logger.Global().Module("datastore").Warn(
							"fresh install detected but database files found in data directory",
							logger.String("file", e.Name()),
							logger.Int64("size", info.Size()),
							logger.String("modified", info.ModTime().Format(time.RFC3339)),
						)
					}
				}
			}
		}
		logStartupDecision(DecisionFreshInstall)
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  false,
			FreshInstall:    true,
			Decision:        DecisionFreshInstall,
			Error:           nil,
		}
	}

	// If v2 migration database doesn't exist, check if configured path is a v2 DB
	if !v2MigrationExists {
		if configuredExists && CheckSQLiteHasV2Schema(configuredPath) {
			// Fresh v2 install (restart after initial fresh install)
			logStartupDecision(DecisionV2Restart)
			return completedV2StartupState(DecisionV2Restart)
		}
		// Configured path exists but is not v2 = legacy mode
		logStartupDecision(DecisionLegacyMode)
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			FreshInstall:    false,
			Decision:        DecisionLegacyMode,
			Error:           nil,
		}
	}

	// Open v2 database in read-only mode to check state
	dsn := readOnlyDSN(v2MigrationPath)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		reportStartupError("sqlite", "openV2Database", err, v2MigrationPath)
		if fallback, ok := fallbackConsolidatedV2State(configuredPath, v2MigrationPath); ok {
			logStartupDecision(DecisionStaleSidecarFallback, logger.String("trigger", "openV2Database"))
			return fallback
		}
		logStartupDecision(DecisionV2Corrupted, logger.String("trigger", "openV2Database"))
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			Decision:        DecisionV2Corrupted,
			Error:           fmt.Errorf("%w: %w", ErrV2DatabaseCorrupted, err),
		}
	}

	// Close the connection when done
	sqlDB, err := db.DB()
	if err != nil {
		reportStartupError("sqlite", "getUnderlyingDB", err, v2MigrationPath)
		if fallback, ok := fallbackConsolidatedV2State(configuredPath, v2MigrationPath); ok {
			logStartupDecision(DecisionStaleSidecarFallback, logger.String("trigger", "getUnderlyingDB"))
			return fallback
		}
		logStartupDecision(DecisionV2Corrupted, logger.String("trigger", "getUnderlyingDB"))
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			Decision:        DecisionV2Corrupted,
			Error:           fmt.Errorf("%w: failed to get underlying DB: %w", ErrV2DatabaseCorrupted, err),
		}
	}
	defer func() { _ = sqlDB.Close() }()

	// Read migration state - try both table names (plural is current, singular is pre-PR #2165)
	migrationTable := resolveSQLiteTableName(db, "migration_states", "migration_state")
	if migrationTable == "" {
		// Close the sidecar handle before the fallback may delete the file.
		// sql.DB.Close is idempotent, so the deferred close remains safe.
		_ = sqlDB.Close()
		if fallback, ok := fallbackConsolidatedV2State(configuredPath, v2MigrationPath); ok {
			logStartupDecision(DecisionStaleSidecarFallback, logger.String("trigger", "migrationTableNotFound"))
			return fallback
		}
		logStartupDecision(DecisionV2Corrupted, logger.String("trigger", "migrationTableNotFound"))
		reportStartupError("sqlite", "readMigrationState", fmt.Errorf("migration state table not found"), v2MigrationPath)
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     true,
			LegacyRequired:  true,
			Decision:        DecisionV2Corrupted,
			Error:           fmt.Errorf("%w: migration state table not found", ErrV2DatabaseCorrupted),
		}
	}
	var state entities.MigrationState
	if err := db.Table(migrationTable).First(&state).Error; err != nil {
		_ = sqlDB.Close()
		if fallback, ok := fallbackConsolidatedV2State(configuredPath, v2MigrationPath); ok {
			logStartupDecision(DecisionStaleSidecarFallback, logger.String("trigger", "readMigrationState"))
			return fallback
		}
		logStartupDecision(DecisionV2Corrupted, logger.String("trigger", "readMigrationState"))
		reportStartupError("sqlite", "readMigrationState", err, v2MigrationPath)
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     true,
			LegacyRequired:  true,
			Decision:        DecisionV2Corrupted,
			Error:           fmt.Errorf("%w: %w", ErrV2DatabaseCorrupted, err),
		}
	}

	// Determine if legacy is required based on migration status
	legacyRequired := state.State != entities.MigrationStatusCompleted

	decision := DecisionMigrationPrefix + string(state.State)
	logStartupDecision(decision,
		logger.Bool("legacy_required", legacyRequired),
	)

	return StartupState{
		MigrationStatus: state.State,
		V2Available:     true,
		LegacyRequired:  legacyRequired,
		Decision:        decision,
		Error:           nil,
	}
}

// completedV2StartupState returns the canonical StartupState for a v2-only
// install with a fully consolidated (completed) migration. Used by both the
// fresh-v2-restart branch and the stale-sidecar fallback so the two paths
// cannot drift apart.
func completedV2StartupState(decision string) StartupState {
	return StartupState{
		MigrationStatus: entities.MigrationStatusCompleted,
		V2Available:     true,
		LegacyRequired:  false,
		Decision:        decision,
	}
}

// removeStaleV2Sidecar deletes a v2 migration sidecar (plus WAL/SHM) that is
// known to be stale because the configured path already holds a consolidated
// v2 database. Best-effort: ignores os errors so startup can proceed.
func removeStaleV2Sidecar(v2MigrationPath, configuredPath, operation string) {
	logger.Global().Module("datastore").Info(
		"removing stale v2 migration sidecar next to consolidated v2 database",
		logger.String("migration_path", v2MigrationPath),
		logger.String("configured_path", configuredPath),
		logger.String("operation", operation),
	)
	_ = os.Remove(v2MigrationPath)
	cleanupWALFiles(v2MigrationPath)
}

// fallbackConsolidatedV2State recovers from a stale or corrupt _v2.db sidecar
// sitting next to a configured path that is already a fully consolidated v2
// database (created by a deploy script, failed migration, or accidental
// touch). It removes the sidecar and returns the v2-only StartupState so the
// caller does not misroute into legacy-migration mode.
//
// Returns (state, true) when the configured path is a valid completed v2
// database; otherwise (zero, false) signalling the caller should fall
// through to its original legacy-mode result.
func fallbackConsolidatedV2State(configuredPath, v2MigrationPath string) (StartupState, bool) {
	if !CheckSQLiteHasV2Schema(configuredPath) {
		return StartupState{}, false
	}
	removeStaleV2Sidecar(v2MigrationPath, configuredPath, "fallback_consolidated_v2_state")
	return completedV2StartupState(DecisionStaleSidecarFallback), true
}

// buildMySQLStartupDSN builds a MySQL DSN for startup-time checks.
// Uses mysql.Config for proper credential escaping and timeouts.
// AllowNativePasswords must be explicitly true because mysql.Config{} struct
// literals default to false (Go zero value), while mysql.NewConfig() defaults
// to true. MariaDB and MySQL configured with mysql_native_password require this.
func buildMySQLStartupDSN(settings *conf.Settings) string {
	cfg := mysql.Config{
		User:                 settings.Output.MySQL.Username,
		Passwd:               settings.Output.MySQL.Password,
		Net:                  "tcp",
		Addr:                 fmt.Sprintf("%s:%s", settings.Output.MySQL.Host, settings.Output.MySQL.Port),
		DBName:               settings.Output.MySQL.Database,
		AllowNativePasswords: true,
		CheckConnLiveness:    true,
		Params: map[string]string{
			"charset":      "utf8mb4",
			"parseTime":    "True",
			"loc":          "Local",
			"timeout":      dbStartupTimeout,
			"readTimeout":  dbStartupTimeout,
			"writeTimeout": dbStartupTimeout,
		},
	}
	return cfg.FormatDSN()
}

// checkMySQLMigrationState checks migration state for MySQL deployments.
func checkMySQLMigrationState(settings *conf.Settings) StartupState {
	dsn := buildMySQLStartupDSN(settings)

	// Collect MySQL endpoint info for redaction from error messages
	mysqlHost := settings.Output.MySQL.Host
	mysqlDB := settings.Output.MySQL.Database
	mysqlUser := settings.Output.MySQL.Username

	db, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		reportStartupError("mysql", "openDatabase", err, mysqlHost, mysqlDB, mysqlUser)
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			Decision:        DecisionV2Corrupted,
			Error:           fmt.Errorf("%w: %w", ErrV2DatabaseCorrupted, err),
		}
	}

	sqlDB, err := db.DB()
	if err != nil {
		reportStartupError("mysql", "getUnderlyingDB", err, mysqlHost, mysqlDB, mysqlUser)
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			Decision:        DecisionV2Corrupted,
			Error:           fmt.Errorf("%w: failed to get underlying DB: %w", ErrV2DatabaseCorrupted, err),
		}
	}
	defer func() { _ = sqlDB.Close() }()

	// Check if legacy 'notes' table exists
	var legacyCount int64
	err = db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = 'notes'",
		settings.Output.MySQL.Database).Scan(&legacyCount).Error
	if err != nil {
		reportStartupError("mysql", "checkLegacyTables", err, mysqlHost, mysqlDB, mysqlUser)
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			Decision:        DecisionV2Corrupted,
			Error:           fmt.Errorf("failed to check legacy tables: %w", err),
		}
	}
	legacyExists := legacyCount > 0

	// Check if v2_migration_states table exists (migration-era v2 tables).
	// Also check for the old singular name v2_migration_state (pre-PR #2165).
	var v2MigrationCount int64
	tableNameNew := v2TablePrefix + "migration_states"
	tableNameOld := v2TablePrefix + "migration_state"
	err = db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name IN (?, ?)",
		settings.Output.MySQL.Database, tableNameNew, tableNameOld).Scan(&v2MigrationCount).Error
	if err != nil {
		reportStartupError("mysql", "checkV2Tables", err, mysqlHost, mysqlDB, mysqlUser)
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			Decision:        DecisionV2Corrupted,
			Error:           ErrV2DatabaseNotFound,
		}
	}
	v2MigrationExists := v2MigrationCount > 0

	// Check if fresh v2 schema exists (no prefix - detections table)
	var freshV2Count int64
	err = db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = 'detections'",
		settings.Output.MySQL.Database).Scan(&freshV2Count).Error
	if err != nil {
		reportStartupError("mysql", "checkFreshV2Tables", err, mysqlHost, mysqlDB, mysqlUser)
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			Decision:        DecisionV2Corrupted,
			Error:           fmt.Errorf("failed to check fresh v2 tables: %w", err),
		}
	}
	freshV2Exists := freshV2Count > 0

	// Fresh install: no tables exist at all
	if !legacyExists && !v2MigrationExists && !freshV2Exists {
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  false,
			FreshInstall:    true,
			Decision:        DecisionFreshInstall,
			Error:           nil,
		}
	}

	// Fresh v2 exists WITHOUT legacy = genuine fresh install restart
	if freshV2Exists && !v2MigrationExists && !legacyExists {
		return StartupState{
			MigrationStatus: entities.MigrationStatusCompleted,
			V2Available:     true,
			LegacyRequired:  false,
			FreshInstall:    false,
			Decision:        DecisionV2Restart,
			Error:           nil,
		}
	}

	// Fresh v2 exists WITH legacy = orphaned bare v2 tables from failed setup
	// Clean up orphaned tables so migration infrastructure can start fresh
	if freshV2Exists && !v2MigrationExists && legacyExists {
		cleanupOrphanedBareV2Tables(db, settings.Output.MySQL.Database)
		// After cleanup, treat as legacy-only mode
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     false,
			LegacyRequired:  true,
			FreshInstall:    false,
			Decision:        DecisionLegacyMode,
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
			Decision:        DecisionLegacyMode,
			Error:           nil,
		}
	}

	// Read migration state from v2 table - resolve actual table name (old singular or new plural)
	var v2MigrationTableName string
	err = db.Raw("SELECT table_name FROM information_schema.tables WHERE table_schema = ? AND table_name IN (?, ?) LIMIT 1",
		settings.Output.MySQL.Database, tableNameNew, tableNameOld).Scan(&v2MigrationTableName).Error
	if err != nil || v2MigrationTableName == "" {
		reportStartupError("mysql", "resolveV2TableName", fmt.Errorf("could not resolve migration state table"), mysqlHost, mysqlDB, mysqlUser)
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     true,
			LegacyRequired:  true,
			Decision:        DecisionV2Corrupted,
			Error:           fmt.Errorf("%w: migration state table not found", ErrV2DatabaseCorrupted),
		}
	}
	var state entities.MigrationState
	if err := db.Table(v2MigrationTableName).First(&state).Error; err != nil {
		reportStartupError("mysql", "readMigrationState", err, mysqlHost, mysqlDB, mysqlUser)
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     true,
			LegacyRequired:  true,
			Decision:        DecisionV2Corrupted,
			Error:           fmt.Errorf("%w: %w", ErrV2DatabaseCorrupted, err),
		}
	}

	legacyRequired := state.State != entities.MigrationStatusCompleted

	return StartupState{
		MigrationStatus: state.State,
		V2Available:     true,
		LegacyRequired:  legacyRequired,
		Decision:        DecisionMigrationPrefix + string(state.State),
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

// HasUnmigratedLegacyRecords reports whether the migration is complete but some
// legacy records are not yet safely present in v2. This detects data loss from
// hard crashes (kill -9, power loss, OOM) during the tail sync window, and (issue
// #3991) distinguishes that genuine straggler case from the normal, harmless
// dual-write residue that keeps legacy notes growing after completion.
//
// It answers the question by primary key rather than by row count: because the v2
// Detection.ID equals the originating legacy notes.id, a legacy tail record is
// "unmigrated" only when it is actually absent from v2 (or still pending in the
// dirty-ID set). A busy install that dual-wrote every tail record into v2 is
// therefore reported as fully reconciled and free to promote this boot.
//
// When it returns true, the caller should:
//  1. Skip database consolidation (keep files in place)
//  2. Override v2-only mode so the worker can tail-sync the stragglers
//
// It only inspects a COMPLETED migration; every earlier state and every
// missing-database case returns false (there is nothing to promote yet). Once the
// COMPLETED marker is confirmed, verification failures fail CLOSED (return true)
// so an unverifiable state keeps dual-write running rather than promoting blindly.
func HasUnmigratedLegacyRecords(settings *conf.Settings, log logger.Logger) bool {
	if settings.Output.MySQL.Enabled {
		return hasUnmigratedLegacyMySQL(settings, log)
	}
	if settings.Output.SQLite.Enabled {
		return hasUnmigratedLegacySQLite(settings, log)
	}
	return false
}

// hasUnmigratedLegacySQLite checks for unmigrated records in a SQLite legacy database.
func hasUnmigratedLegacySQLite(settings *conf.Settings, log logger.Logger) bool {
	configuredPath := settings.Output.SQLite.Path
	v2MigrationPath := V2MigrationPathFromConfigured(configuredPath)

	// Both databases must exist for there to be stragglers
	if _, err := os.Stat(v2MigrationPath); os.IsNotExist(err) {
		return false
	}
	if _, err := os.Stat(configuredPath); os.IsNotExist(err) {
		return false
	}

	// Open v2 migration database read-only to get LastMigratedID
	v2DSN := readOnlyDSN(v2MigrationPath)
	v2DB, err := gorm.Open(sqlite.Open(v2DSN), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		log.Warn("reconciliation: failed to open v2 migration database", logger.Error(err))
		return false
	}
	v2SQL, err := v2DB.DB()
	if err != nil {
		log.Warn("reconciliation: failed to get v2 underlying DB", logger.Error(err))
		return false
	}
	defer func() { _ = v2SQL.Close() }()

	// Resolve table name (migration_states or migration_state for pre-PR #2165)
	migrationTable := resolveSQLiteTableName(v2DB, "migration_states", "migration_state")
	if migrationTable == "" {
		return false
	}

	var state entities.MigrationState
	if err := v2DB.Table(migrationTable).First(&state).Error; err != nil {
		log.Warn("reconciliation: failed to read migration state", logger.Error(err))
		return false
	}

	// Only relevant when migration is completed
	if state.State != entities.MigrationStatusCompleted {
		return false
	}

	// Open legacy database read-only to count records beyond the watermark
	legacyDSN := readOnlyDSN(configuredPath)
	legacyDB, err := gorm.Open(sqlite.Open(legacyDSN), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		log.Warn("reconciliation: failed to open legacy database", logger.Error(err))
		return false
	}
	legacySQL, err := legacyDB.DB()
	if err != nil {
		log.Warn("reconciliation: failed to get legacy underlying DB", logger.Error(err))
		return false
	}
	defer func() { _ = legacySQL.Close() }()

	// A completed migration keeps dual-writing to legacy notes, so a raw count of
	// notes.id > LastMigratedID is always non-zero on a busy install and is NOT a
	// reliable signal of data loss (issue #3991). During dual-write every detection
	// is written to BOTH legacy and v2, and the v2 Detection.ID equals the legacy
	// notes.id, so the authoritative question is whether any legacy tail record is
	// actually ABSENT from v2. Answer it with a targeted primary-key membership check
	// plus the dirty-ID set. Only genuinely unreconciled records defer promotion.
	dirtyTable := resolveSQLiteTableName(v2DB, "migration_dirty_ids", "migration_dirty_id")
	return !legacyTailReconciledInV2(legacyDB, v2DB, state.LastMigratedID, "notes", "detections", dirtyTable, log)
}

// hasUnmigratedLegacyMySQL checks for unmigrated records in a MySQL legacy database.
func hasUnmigratedLegacyMySQL(settings *conf.Settings, log logger.Logger) bool {
	dsn := buildMySQLStartupDSN(settings)

	db, err := gorm.Open(gormmysql.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		log.Warn("reconciliation: failed to open MySQL database", logger.Error(err))
		return false
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Warn("reconciliation: failed to get MySQL underlying DB", logger.Error(err))
		return false
	}
	defer func() { _ = sqlDB.Close() }()

	// Check if v2 migration state table exists (try both plural and singular names)
	tableNameNew := v2TablePrefix + "migration_states"
	tableNameOld := v2TablePrefix + "migration_state"
	var v2MigrationTableName string
	if err := db.Raw("SELECT table_name FROM information_schema.tables WHERE table_schema = ? AND table_name IN (?, ?) LIMIT 1",
		settings.Output.MySQL.Database, tableNameNew, tableNameOld).Scan(&v2MigrationTableName).Error; err != nil || v2MigrationTableName == "" {
		return false
	}

	var state entities.MigrationState
	if err := db.Table(v2MigrationTableName).First(&state).Error; err != nil {
		log.Warn("reconciliation: failed to read MySQL migration state", logger.Error(err))
		return false
	}

	if state.State != entities.MigrationStatusCompleted {
		return false
	}

	// Check if legacy notes table exists
	var legacyCount int64
	if err := db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = 'notes'",
		settings.Output.MySQL.Database).Scan(&legacyCount).Error; err != nil || legacyCount == 0 {
		return false
	}

	// Same reasoning as the SQLite path (issue #3991): dual-write keeps legacy notes
	// growing after completion, so a raw count is meaningless. Verify the legacy tail
	// against v2 by primary key (v2 Detection.ID == legacy notes.id) and check the
	// dirty-ID set. MySQL keeps both schemas in one database, so a single connection
	// serves both the legacy and v2 queries; v2 tables carry the v2_ prefix.
	dirtyTable := resolveMySQLTableName(db, settings.Output.MySQL.Database,
		v2TablePrefix+"migration_dirty_ids", v2TablePrefix+"migration_dirty_id")
	return !legacyTailReconciledInV2(db, db, state.LastMigratedID, "notes", v2TablePrefix+"detections", dirtyTable, log)
}

// tailScanBatchSize bounds how many legacy tail IDs are compared against v2 per
// round trip. Keyset pagination over this batch keeps memory flat regardless of how
// far the legacy tail runs past the migration watermark.
const tailScanBatchSize = 500

// tailScanMaxBatches caps the keyset scan as a defensive stop against a logic error;
// the scan terminates naturally at the end of the (static, read-only) legacy table
// long before this on any real database. Hitting the cap fails closed.
const tailScanMaxBatches = 100_000

// legacyTailReconciledInV2 reports whether every legacy detection beyond the
// migration watermark is already present in v2 AND no dual-write records are still
// pending reconciliation. It is the safe precondition for ending dual-write and
// consolidating to v2 (issue #3991).
//
// legacyDB and v2DB may be the same handle (MySQL keeps both schemas in one
// database) or two handles (SQLite keeps v2 in a separate file). dirtyTable may be
// empty when the dirty-ID table does not exist (older schema), in which case only
// the membership check applies. It fails CLOSED: any verification error returns
// false so an unverifiable state keeps dual-write running rather than promoting.
func legacyTailReconciledInV2(legacyDB, v2DB *gorm.DB, lastMigratedID uint, notesTable, detectionsTable, dirtyTable string, log logger.Logger) bool {
	// Outstanding dirty IDs mean dual-write failed to sync some records to v2 and the
	// worker has not reconciled them yet. Tail sync can advance LastMigratedID past a
	// record it failed to migrate, so the membership check below would not see those;
	// the dirty-ID set catches them.
	if dirtyTable != "" {
		var dirty int64
		if err := v2DB.Table(dirtyTable).Count(&dirty).Error; err != nil {
			log.Warn("reconciliation: failed to count dirty IDs, keeping dual-write", logger.Error(err))
			return false
		}
		if dirty > 0 {
			log.Warn("legacy tail not reconciled: dual-write records pending sync to v2",
				logger.Int64("dirty_ids", dirty),
				logger.Uint64("last_migrated_id", uint64(lastMigratedID)))
			return false
		}
	}

	missing, err := legacyTailMissingFromV2(legacyDB, v2DB, lastMigratedID, notesTable, detectionsTable)
	if err != nil {
		log.Warn("reconciliation: failed to verify legacy tail against v2, keeping dual-write", logger.Error(err))
		return false
	}
	if missing {
		log.Warn("legacy tail not reconciled: records absent from v2 after migration completed",
			logger.Uint64("last_migrated_id", uint64(lastMigratedID)))
		return false
	}
	return true
}

// legacyTailMissingFromV2 reports whether any legacy notesTable.id greater than
// lastMigratedID is absent from the v2 detectionsTable. Because the v2 Detection.ID
// is set to the originating legacy notes.id during migration, membership is a direct
// primary-key comparison. It iterates in keyset batches so it never loads the whole
// tail into memory, and short-circuits on the first missing id, so a genuinely
// behind install returns immediately. Table names come from fixed internal
// constants, never user input.
func legacyTailMissingFromV2(legacyDB, v2DB *gorm.DB, lastMigratedID uint, notesTable, detectionsTable string) (bool, error) {
	cursor := lastMigratedID
	for range tailScanMaxBatches {
		var ids []uint
		if err := legacyDB.Table(notesTable).
			Where("id > ?", cursor).
			Order("id ASC").
			Limit(tailScanBatchSize).
			Pluck("id", &ids).Error; err != nil {
			return false, err
		}
		if len(ids) == 0 {
			return false, nil // reached the end of the tail, nothing missing
		}

		var existing []uint
		if err := v2DB.Table(detectionsTable).
			Where("id IN ?", ids).
			Pluck("id", &existing).Error; err != nil {
			return false, err
		}
		if len(existing) < len(ids) {
			return true, nil // at least one tail record is not in v2
		}

		cursor = ids[len(ids)-1]
		if len(ids) < tailScanBatchSize {
			return false, nil // last (partial) batch, all present
		}
	}
	// Exceeded the defensive batch cap without confirming the tail is covered. Fail
	// closed: report missing so promotion is deferred rather than risking data loss.
	return true, nil
}

// resolveMySQLTableName returns the first of the given table names that exists in the
// MySQL database, or empty string if none do. It mirrors resolveSQLiteTableName for
// the MySQL information_schema, handling the migration_dirty_id -> migration_dirty_ids
// rename from PR #2165.
func resolveMySQLTableName(db *gorm.DB, database string, names ...string) string {
	for _, name := range names {
		var count int64
		if err := db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
			database, name).Scan(&count).Error; err == nil && count > 0 {
			return name
		}
	}
	return ""
}

// checkpointSQLiteWAL folds any pending write-ahead-log content back into the main
// database file at dbPath so a subsequent rename carries a complete database. It runs
// PRAGMA wal_checkpoint(TRUNCATE), which copies all WAL frames into the main file and
// truncates the WAL to zero bytes. It opens the database read-write (a TRUNCATE
// checkpoint needs write access) and closes it again, so it must only be called when
// no other connection holds the file, i.e. at startup before any manager opens it.
// A missing file is not an error (nothing to checkpoint).
func checkpointSQLiteWAL(dbPath string, log logger.Logger) error {
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := sqlDB.Close(); closeErr != nil {
			log.Warn("failed to close database after WAL checkpoint",
				logger.String("path", dbPath), logger.Error(closeErr))
		}
	}()

	if err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
		return fmt.Errorf("WAL checkpoint failed: %w", err)
	}
	return nil
}

// moveSQLiteDBFiles renames a SQLite database file together with its -wal and -shm
// sidecars, so no committed WAL data is left behind or discarded during consolidation.
// The main file rename is authoritative and its failure is returned; sidecar moves are
// best-effort and only logged, because after a successful checkpoint the sidecars are
// empty and after an unclean shutdown moving them alongside preserves their data.
func moveSQLiteDBFiles(from, to string, log logger.Logger) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		src := from + suffix
		if _, err := os.Stat(src); err != nil {
			// Source sidecar absent (e.g. checkpoint truncated it and SQLite removed the
			// file). Remove any stale sidecar left at the destination so it is not wrongly
			// paired with the moved database when it is next opened.
			_ = os.Remove(to + suffix)
			continue
		}
		if err := os.Rename(src, to+suffix); err != nil {
			log.Warn("failed to move SQLite sidecar file during consolidation",
				logger.String("from", src),
				logger.String("to", to+suffix),
				logger.Error(err))
		}
	}
	return nil
}

// CheckSQLiteHasV2Schema checks if a SQLite database at the given path is a fully initialized v2 database.
// This is used to distinguish between a legacy database and a fresh v2 database.
// Returns true only if the database has:
//  1. The migration_states table (v2 schema indicator)
//  2. A migration state record with COMPLETED status
//
// This prevents false positives from partially initialized databases.
//
// Unlike CheckMySQLHasFreshV2Schema, this intentionally does NOT also require the
// detections data table to exist. SQLite stores the v2 schema in a dedicated file
// (not prefix-shared tables), and initializeV2OnlyMode's SQLite branch always runs
// Initialize()/AutoMigrate on the chosen file in both outcomes, so a marker-without-data
// file is repaired in place rather than wedging the app. The #3575 wedge is MySQL-only.
func CheckSQLiteHasV2Schema(dbPath string) bool {
	// Check if file exists first - GORM/SQLite with mode=ro may still create an empty file
	// even when opening in read-only mode, which causes issues when checking non-existent
	// legacy databases (they get created as empty files during the check).
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return false
	}

	dsn := readOnlyDSN(dbPath)
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

	// Check if migration_states table exists (v2 schema indicator).
	// Also check for the old singular name "migration_state" which existed before PR #2165
	// removed TableName() overrides (GORM now auto-pluralizes to "migration_states").
	var tableCount int64
	err = db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('migration_states', 'migration_state')").Scan(&tableCount).Error
	if err != nil || tableCount == 0 {
		return false
	}

	// Determine which table name exists and query it directly
	var tableName string
	err = db.Raw("SELECT name FROM sqlite_master WHERE type='table' AND name IN ('migration_states', 'migration_state') LIMIT 1").Scan(&tableName).Error
	if err != nil || tableName == "" {
		return false
	}

	// Check if migration state is COMPLETED (fully initialized)
	var state entities.MigrationState
	err = db.Table(tableName).First(&state).Error
	if err != nil {
		return false
	}

	if state.State != entities.MigrationStatusCompleted {
		return false
	}

	// A COMPLETED marker together with a *populated* legacy data table ('results' or 'notes')
	// means real user data is still stranded in the legacy schema: a PR #2165-contaminated
	// legacy database masquerading as v2 (GitHub #3924). Treating it as v2 lets
	// CheckAndConsolidateAtStartup delete the real migrated sidecar, so reject it.
	//
	// An EMPTY legacy table, by contrast, is harmless residue: a fully migrated v2 database can
	// still carry the old 'notes'/'results' tables (GORM never drops tables) with no rows left in
	// them. Such databases MUST still be recognised as v2. Keying on table existence alone (as
	// PR #3926 first did) forced these healthy databases into legacy mode, where legacy AutoMigrate
	// then crashes with "Cannot add a NOT NULL column with default value NULL" while adding a
	// legacy column to an already-populated table. Discriminate on row count, not mere table
	// existence.
	//
	// Fail closed on probe error: this function gates destructive startup actions, so if we
	// cannot prove the legacy tables are empty, return false rather than risk misclassifying a
	// contaminated or corrupt database as v2 (matches the migration_states probe above).
	hasLegacyData, err := legacyDataPresent(db)
	if err != nil || hasLegacyData {
		return false
	}

	return true
}

// CheckMySQLHasFreshV2Schema reports whether a MySQL database holds a complete,
// usable fresh (no v2_ prefix) v2 schema. It drives the prefix decision in
// initializeV2OnlyMode: no prefix for fresh installs, v2_ prefix for migration mode.
//
// It returns true only when BOTH conditions hold:
//  1. a no-prefix migration_states row exists with state == COMPLETED, AND
//  2. the no-prefix `detections` data table physically exists.
//
// Requiring the real data table (not just the completed marker) prevents a stale
// marker left by a backend switch (e.g. SQLite -> MySQL) or a partial/aborted init
// from wedging the app in enhanced mode against a missing `detections` table
// (GitHub #3575).
func CheckMySQLHasFreshV2Schema(settings *conf.Settings) bool {
	dsn := buildMySQLStartupDSN(settings)

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

	return hasCompleteFreshV2Schema(db)
}

// hasCompleteFreshV2Schema is the dialect-agnostic core behind CheckMySQLHasFreshV2Schema.
// It reports whether the connected database holds a complete, usable fresh (no v2_
// prefix) v2 schema: a no-prefix migration_states (or pre-PR #2165 singular
// migration_state) row with state == COMPLETED AND a physical no-prefix `detections`
// data table.
//
// It uses GORM's migrator for table existence so the same code path can be unit-tested
// against SQLite (in CI, where MySQL is unavailable) while production calls it for MySQL.
func hasCompleteFreshV2Schema(db *gorm.DB) bool {
	// Resolve the migration-state table name. Plural is current; the singular
	// "migration_state" predates PR #2165 (which removed TableName() overrides).
	migrationTable := ""
	for _, name := range []string{"migration_states", "migration_state"} {
		if db.Migrator().HasTable(name) {
			migrationTable = name
			break
		}
	}
	if migrationTable == "" {
		return false
	}

	// The migration must be marked COMPLETED.
	var state entities.MigrationState
	if err := db.Table(migrationTable).First(&state).Error; err != nil {
		return false
	}
	if state.State != entities.MigrationStatusCompleted {
		return false
	}

	// A COMPLETED marker alone is NOT sufficient evidence of a usable fresh v2 schema:
	// the real `detections` data table must also exist. A stale marker can survive a
	// backend switch (e.g. SQLite -> MySQL) or a partial/aborted init, and without this
	// guard the prefix decision in initializeV2OnlyMode would pick the no-prefix schema
	// so every v2 query targets a bare `detections` table that was never created,
	// wedging the app in enhanced mode (GitHub #3575). Returning false makes
	// initializeV2OnlyMode fall back to the v2_ prefixed schema, whose tables are
	// (re)created by the subsequent Initialize()/AutoMigrate.
	//
	// A COMPLETED marker alongside a *populated* legacy data table ('results' or 'notes')
	// means real user data is still stranded in the legacy schema: a PR #2165-contaminated
	// legacy database, not a fresh no-prefix v2 schema (GitHub #3924). Reject it. An EMPTY
	// legacy table is harmless residue that a fully migrated v2 database can still carry and
	// must NOT disqualify the schema; keying on table existence alone (as PR #3926 first did)
	// wrongly forced healthy v2 databases into legacy mode. Mirror the same row-based guard
	// used by CheckSQLiteHasV2Schema, failing closed on a row-count probe error.
	if hasLegacyData, err := legacyDataPresent(db); err != nil || hasLegacyData {
		return false
	}
	return db.Migrator().HasTable("detections")
}

// legacyDataTables lists the v1 data tables. After a completed in-place migration these tables
// are commonly left behind EMPTY (GORM never drops tables); that residue is harmless. Only rows
// still sitting in them indicate real unmigrated legacy data, i.e. a PR #2165-contaminated
// database masquerading as a completed v2 schema (GitHub #3924).
var legacyDataTables = []string{"results", "notes"}

// legacyDataPresent reports whether any legacy data table exists AND still holds at least one row.
// Table existence alone is not contamination: a genuine completed migration leaves empty legacy
// tables behind. The bool is meaningful only when the returned error is nil; because this decision
// gates destructive startup actions, callers should fail closed (treat as not-clean-v2) on error.
func legacyDataPresent(db *gorm.DB) (bool, error) {
	// Resolve which tables exist with an error-returning probe. db.Migrator().HasTable swallows
	// the underlying metadata-query error and just reports false, which would let a probe failure
	// be misread as "no legacy data" (fail open) on a database this function is meant to fail
	// closed for. GetTables surfaces the error so the caller can reject the database instead.
	tables, err := db.Migrator().GetTables()
	if err != nil {
		return false, err
	}
	existing := make(map[string]struct{}, len(tables))
	for _, t := range tables {
		existing[t] = struct{}{}
	}

	for _, name := range legacyDataTables {
		if _, ok := existing[name]; !ok {
			continue
		}
		// Existence probe via SELECT EXISTS, not a full COUNT(*): a contaminated legacy table can
		// hold hundreds of thousands of unmigrated rows (GitHub #3924), and counting all of them on
		// every startup would scan the whole table and could approach the DB startup timeout. EXISTS
		// short-circuits at the first row and yields an explicit 0/1, so it does not depend on GORM
		// populating RowsAffected after Scan (which can vary by version/driver). The table name comes
		// from the fixed legacyDataTables allowlist, so the interpolation is safe. Backtick quoting
		// works on both SQLite and MySQL (mirrors cleanupLegacySchemaContamination in this package).
		var hasRow int
		if err := db.Raw("SELECT EXISTS(SELECT 1 FROM `" + name + "`)").Scan(&hasRow).Error; err != nil {
			return false, err
		}
		if hasRow != 0 {
			return true, nil
		}
	}
	return false, nil
}

// cleanupOrphanedBareV2Tables removes bare v2 tables that were created by a broken nightly
// alongside existing legacy tables. These orphaned tables prevent the migration infrastructure
// from starting fresh. Only v2-specific tables are dropped; legacy tables with real user data
// (dynamic_thresholds, threshold_events, notification_histories, image_caches, daily_events,
// hourly_weathers) are preserved.
func cleanupOrphanedBareV2Tables(db *gorm.DB, database string) {
	// Report discovery of orphaned tables to Sentry
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", "datastore-startup")
		scope.SetTag("db_type", "mysql")
		scope.SetTag("operation", "cleanupOrphanedBareV2Tables")
		scope.SetFingerprint([]string{"datastore-startup", "mysql", "cleanupOrphanedBareV2Tables"})

		telemetry.CaptureMessage(
			"Orphaned bare v2 tables found alongside legacy data, cleaning up",
			sentry.LevelWarning,
			"datastore-startup",
		)
	})

	// Bare v2-only tables that do NOT collide with legacy tables.
	// Drop in reverse dependency order (children before parents).
	// Both naming forms are listed: the OLD singular forms (migration_state,
	// alert_history) left by pre-PR #2165 code with TableName() overrides, and the
	// current GORM-pluralized forms (migration_states, alert_histories) that a more
	// recent broken nightly would leave behind. FK checks are disabled below, so an
	// entry that does not exist is a harmless no-op (DROP TABLE IF EXISTS).
	orphanedTables := []string{
		// Alert children first, then parent
		"alert_actions",
		"alert_conditions",
		"alert_history",
		"alert_histories",
		"alert_rules",
		// Detection children first, then parent
		"detection_locks",
		"detection_comments",
		"detection_reviews",
		"detection_model_contributions",
		"detection_predictions",
		"detections",
		// Labels before its reference tables (labels has FKs to ai_models, label_types, taxonomic_classes)
		"labels",
		"audio_sources",
		"ai_models",
		"taxonomic_classes",
		"label_types",
		// Application metadata and event log (no FK dependencies)
		"app_events",
		"app_metadata",
		// Migration tracking
		"migration_dirty_ids",
		"migration_state",
		"migration_states",
	}

	// Use db.Connection to pin all operations to a single pooled connection,
	// ensuring SET FOREIGN_KEY_CHECKS applies to the same connection as the DROPs.
	// FK checks must be disabled because preserved legacy tables (dynamic_thresholds,
	// notification_histories, etc.) may have FK constraints pointing to orphaned tables.
	if err := db.Connection(func(tx *gorm.DB) (retErr error) {
		if err := tx.Exec("SET FOREIGN_KEY_CHECKS = 0").Error; err != nil {
			reportStartupError("mysql", "cleanupOrphanedTables_disableFK", err, database)
			return err
		}
		// Re-enable FK checks via defer so the pooled connection is never returned
		// with referential integrity disabled, even if a DROP panics. On a clean run,
		// surface a re-enable failure as the closure error (skipping the success
		// telemetry below), preserving the original inline behavior.
		defer func() {
			if err := tx.Exec("SET FOREIGN_KEY_CHECKS = 1").Error; err != nil {
				reportStartupError("mysql", "cleanupOrphanedTables_enableFK", err, database)
				if retErr == nil {
					retErr = err
				}
			}
		}()
		for _, table := range orphanedTables {
			if err := tx.Exec(fmt.Sprintf("DROP TABLE IF EXISTS `%s`", table)).Error; err != nil {
				reportStartupError("mysql", "cleanupOrphanedTable_"+table, err, database)
			}
		}
		return nil
	}); err != nil {
		return
	}

	// Report successful cleanup
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", "datastore-startup")
		scope.SetTag("db_type", "mysql")
		scope.SetTag("operation", "cleanupOrphanedBareV2TablesComplete")
		scope.SetFingerprint([]string{"datastore-startup", "mysql", "cleanupOrphanedBareV2TablesComplete"})

		telemetry.CaptureMessage(
			"Orphaned bare v2 tables cleanup completed successfully",
			sentry.LevelInfo,
			"datastore-startup",
		)
	})
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

	// Journal handle for consolidation outcomes (best-effort, DB-independent).
	j := diagnostics.Default()

	// Step 1: Check for interrupted consolidation
	resumed, newPath, err := ResumeConsolidation(dataDir, log)
	if err != nil {
		reportConsolidationError("resumeConsolidation", err, configuredPath)
		// Journal the failed resume of an interrupted consolidation: it is
		// exactly the kind of event the boot journal exists to capture. The
		// v2 sidecar/backup paths are not resolved yet at this point, so the
		// record carries only the configured path.
		diagnostics.RecordConsolidation(j, "", configuredPath, "", "failed")
		return false, fmt.Errorf("failed to check/resume consolidation: %w", err)
	}
	if resumed {
		log.Info("resumed interrupted consolidation",
			logger.String("path", newPath))
		diagnostics.RecordConsolidation(j, "", newPath, "", "resumed")
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
		// Sidecar exists but is empty, corrupt, or has an incomplete migration.
		// When the configured path is already a consolidated v2 database the
		// sidecar is stale (deploy script, crashed init, accidental touch) and
		// must be removed so the subsequent startup-state check does not
		// misroute into legacy-migration mode.
		if CheckSQLiteHasV2Schema(configuredPath) {
			removeStaleV2Sidecar(v2MigrationPath, configuredPath, "consolidate_at_startup")
		}
		// V2 sidecar is not a completed migration DB; nothing to consolidate
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
		reportConsolidationError("writeStateFile", err, configuredPath, v2MigrationPath)
		diagnostics.RecordConsolidation(j, v2MigrationPath, configuredPath, backupPath, "failed")
		return false, fmt.Errorf("failed to write consolidation state: %w", err)
	}

	// Fold any pending WAL content into the main database files BEFORE renaming, so a
	// rename carries every committed transaction. Blindly deleting -wal/-shm (the old
	// behaviour here) discards data written since the last checkpoint after an unclean
	// shutdown, and this promotion fix makes consolidation actually run on busy 24/7
	// installs where an uncheckpointed WAL is likely (issue #3991). Best-effort: log
	// and continue, because the sidecar-preserving rename below is the real safety net.
	if err := checkpointSQLiteWAL(configuredPath, log); err != nil {
		log.Warn("failed to checkpoint legacy WAL before consolidation", logger.Error(err))
	}
	if err := checkpointSQLiteWAL(v2MigrationPath, log); err != nil {
		log.Warn("failed to checkpoint v2 WAL before consolidation", logger.Error(err))
	}

	// Rename legacy → backup (if legacy exists), moving its -wal/-shm sidecars along so
	// no committed WAL data is left behind even if the checkpoint above did not fully
	// drain it.
	if _, err := os.Stat(configuredPath); err == nil {
		log.Debug("renaming legacy database to backup",
			logger.String("from", configuredPath),
			logger.String("to", backupPath))
		if err := moveSQLiteDBFiles(configuredPath, backupPath, log); err != nil {
			reportConsolidationError("startupRenameLegacy", err, configuredPath, backupPath)
			_ = DeleteConsolidationState(dataDir)
			diagnostics.RecordConsolidation(j, v2MigrationPath, configuredPath, backupPath, "failed")
			return false, fmt.Errorf("failed to rename legacy database: %w", err)
		}
	}

	// Rename v2 → configured path, moving its -wal/-shm sidecars so the promoted
	// database carries any transactions still resident in its WAL.
	log.Debug("renaming v2 database to configured path",
		logger.String("from", v2MigrationPath),
		logger.String("to", configuredPath))
	if err := moveSQLiteDBFiles(v2MigrationPath, configuredPath, log); err != nil {
		reportConsolidationError("startupRenameV2", err, v2MigrationPath, configuredPath)
		rolledBack := false
		// Rollback: restore legacy from backup if it existed
		if _, statErr := os.Stat(backupPath); statErr == nil {
			log.Warn("v2 rename failed, rolling back",
				logger.Error(err))
			if rollbackErr := moveSQLiteDBFiles(backupPath, configuredPath, log); rollbackErr != nil {
				reportConsolidationError("rollbackFailed", rollbackErr, backupPath, configuredPath)
				log.Error("rollback failed - manual intervention required",
					logger.Error(rollbackErr))
				// Return without deleting state file to allow recovery on next boot
				diagnostics.RecordConsolidation(j, v2MigrationPath, configuredPath, backupPath, "failed")
				return false, fmt.Errorf("failed to rename v2 database (rollback also failed: %w): %w", rollbackErr, err)
			}
			rolledBack = true
		}
		_ = DeleteConsolidationState(dataDir)
		if rolledBack {
			// The legacy database was restored: one rolled_back record, no
			// additional generic failure record.
			diagnostics.RecordConsolidation(j, v2MigrationPath, configuredPath, backupPath, "rolled_back")
		} else {
			diagnostics.RecordConsolidation(j, v2MigrationPath, configuredPath, backupPath, "failed")
		}
		return false, fmt.Errorf("failed to rename v2 database: %w", err)
	}

	// Delete consolidation state file
	if err := DeleteConsolidationState(dataDir); err != nil {
		log.Warn("failed to delete consolidation state file", logger.Error(err))
	}

	log.Info("database consolidation completed at startup",
		logger.String("database_path", configuredPath),
		logger.String("backup_path", backupPath))

	diagnostics.RecordConsolidation(j, v2MigrationPath, configuredPath, backupPath, "success")

	return true, nil
}

// resolveSQLiteTableName checks which of the given SQLite table names exists.
// Returns the first match or empty string if none exist.
// This handles the migration_state→migration_states and alert_history→alert_histories
// rename from PR #2165 where TableName() overrides were removed.
func resolveSQLiteTableName(db *gorm.DB, names ...string) string {
	for _, name := range names {
		var count int64
		if err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&count).Error; err == nil && count > 0 {
			return name
		}
	}
	return ""
}
