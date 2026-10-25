package v2

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
// dsnAppendParams appends SQLite DSN query parameters to a database path, choosing "?" or
// "&" depending on whether the path already carries a query string (e.g. a shared-cache
// in-memory DSN used in tests).
func dsnAppendParams(dbPath, params string) string {
	sep := "?"
	if strings.Contains(dbPath, "?") {
		sep = "&"
	}
	return dbPath + sep + params
}

func readOnlyDSN(dbPath string) string {
	return dsnAppendParams(dbPath, "mode=ro")
}

// migrationTableResolveErrors returns the error to report and the error for
// StartupState when the migration state table lookup fails. queryErr is the
// failed query's error, or nil when the query succeeded without finding a
// table; a query failure keeps its cause in both errors.
func migrationTableResolveErrors(queryErr error) (reportErr, stateErr error) {
	if queryErr != nil {
		return fmt.Errorf("could not resolve migration state table: %w", queryErr),
			fmt.Errorf("%w: could not resolve migration state table: %w", ErrV2DatabaseCorrupted, queryErr)
	}
	return fmt.Errorf("could not resolve migration state table"),
		fmt.Errorf("%w: migration state table not found", ErrV2DatabaseCorrupted)
}

// reportStartupError reports a startup state check failure to Sentry telemetry.
// The paths parameter lists file paths that should be anonymized in the error message.
// Connectivity failures (the database host could not be reached) are not sent:
// they are not code defects, and the open attempt that follows in the same boot
// reports them once through the rate-limited telemetry filter.
func reportStartupError(dbType, operation string, err error, paths ...string) {
	if errors.IsConnectivityError(err) {
		return
	}
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
	// DecisionMixedSchemaRefused: MySQL/MariaDB holds bare v2 tables beside a legacy
	// notes table and no side can be kept without losing rows (both sides hold data, the
	// chosen side's startup would drop rows, the enhanced side is not a completed install,
	// a table could not be read, or an empty table could not be removed). Startup drops
	// no table with rows and refuses.
	DecisionMixedSchemaRefused = "mixed_schema_refused"
	// DecisionMixedSchemaKeptV2: bare v2 tables beside empty legacy tables on a completed
	// enhanced install. Startup kept the enhanced database and dropped the empty legacy tables.
	DecisionMixedSchemaKeptV2 = "mixed_schema_kept_v2"
	// DecisionMixedSchemaFreshV2: bare v2 tables beside empty legacy tables, both sides empty
	// and the enhanced schema not yet marked completed. Startup kept the enhanced database.
	DecisionMixedSchemaFreshV2 = "mixed_schema_fresh_v2"
	// DecisionMixedSchemaKeptLegacy: legacy tables hold data and the bare v2 detection tables
	// are empty. Startup kept the legacy database and dropped the empty bare v2 detection tables.
	DecisionMixedSchemaKeptLegacy = "mixed_schema_kept_legacy"
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
	// ErrMixedSchemaDatabase indicates the MySQL/MariaDB database holds bare v2 tables
	// beside a legacy notes table and no side can be kept without losing rows (both sides
	// hold data, the kept side's startup would drop rows, a table could not be read, or an
	// empty table could not be removed). Callers must refuse to start (errors.Is), so that
	// no table with rows is dropped;
	// StartupState.Error carries the tables with rows, or the failed step when an empty
	// table could not be removed.
	ErrMixedSchemaDatabase = errors.NewStd("database holds both enhanced database (v2) tables and legacy tables")
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

	// Bare v2 tables beside a legacy notes table. Keep the side that holds data and drop
	// only the empty tables of the other, each verified empty under a lock; refuse when
	// no side can be kept without losing rows. See resolveMixedSchema.
	if freshV2Exists && !v2MigrationExists && legacyExists {
		return resolveMixedSchema(db, settings.Output.MySQL.Database, mysqlHost, mysqlDB, mysqlUser)
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
		reportErr, stateErr := migrationTableResolveErrors(err)
		reportStartupError("mysql", "resolveV2TableName", reportErr, mysqlHost, mysqlDB, mysqlUser)
		return StartupState{
			MigrationStatus: entities.MigrationStatusIdle,
			V2Available:     true,
			LegacyRequired:  true,
			Decision:        DecisionV2Corrupted,
			Error:           stateErr,
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

// HasUnmigratedLegacyRecords reports whether the migration is complete but some
// legacy records are not yet safely present in v2. This detects data loss from
// hard crashes (kill -9, power loss, OOM) during the tail sync window, and (issue
// #3991) distinguishes that genuine straggler case from the normal, harmless
// residue: legacy notes keep growing after completion, because new detections
// are still saved to the legacy database only until the install is promoted.
//
// It answers the question by primary key rather than by row count: because the v2
// Detection.ID equals the originating legacy notes.id, a legacy tail record is
// "unmigrated" only when it is actually absent from v2 (or still pending in the
// dirty-ID set). A busy install whose migration worker already tail-synced every
// record into v2 is therefore reported as fully reconciled and free to promote this boot.
//
// When it returns true, the caller should:
//  1. Skip database consolidation (keep files in place)
//  2. Override v2-only mode so the worker can tail-sync the stragglers
//
// It only inspects a COMPLETED migration; every earlier state and every
// missing-database case returns false (there is nothing to promote yet). Once the
// COMPLETED marker is confirmed, verification failures fail CLOSED (return true)
// so an unverifiable state stays in tail-sync mode rather than promoting blindly.
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

	// Until promotion, new detections are saved to legacy notes only and the
	// migration worker's tail sync copies them to v2 afterwards, so a raw count of
	// notes.id > LastMigratedID is routinely non-zero on a busy install and is NOT a
	// reliable signal of data loss (issue #3991). The v2 Detection.ID equals the
	// legacy notes.id, so the authoritative question is whether any legacy tail
	// record is actually ABSENT from v2. Answer it with a targeted primary-key
	// membership check plus the dirty-ID set. Only genuinely unreconciled records
	// defer promotion.
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

	// Same reasoning as the SQLite path (issue #3991): legacy notes keep growing
	// until promotion, so a raw count is meaningless. Verify the legacy tail
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
// migration watermark is already present in v2 AND no dirty IDs are still pending
// reconciliation. It is the safe precondition for promoting to v2-only and
// consolidating to v2 (issue #3991).
//
// legacyDB and v2DB may be the same handle (MySQL keeps both schemas in one
// database) or two handles (SQLite keeps v2 in a separate file). dirtyTable may be
// empty when the dirty-ID table does not exist (older schema), in which case only
// the membership check applies. It fails CLOSED: any verification error returns
// false so an unverifiable state stays in tail-sync mode rather than promoting.
func legacyTailReconciledInV2(legacyDB, v2DB *gorm.DB, lastMigratedID uint, notesTable, detectionsTable, dirtyTable string, log logger.Logger) bool {
	// Outstanding dirty IDs mean the migration worker failed to sync some records to
	// v2 and has not reconciled them yet. Tail sync can advance LastMigratedID past a
	// record it failed to migrate, so the membership check below would not see those;
	// the dirty-ID set catches them.
	if dirtyTable != "" {
		var dirty int64
		if err := v2DB.Table(dirtyTable).Count(&dirty).Error; err != nil {
			log.Warn("reconciliation: failed to count dirty IDs, deferring promotion", logger.Error(err))
			return false
		}
		if dirty > 0 {
			log.Warn("legacy tail not reconciled: dirty IDs pending sync to v2",
				logger.Int64("dirty_ids", dirty),
				logger.Uint64("last_migrated_id", uint64(lastMigratedID)))
			return false
		}
	}

	missing, err := legacyTailMissingFromV2(legacyDB, v2DB, lastMigratedID, notesTable, detectionsTable)
	if err != nil {
		log.Warn("reconciliation: failed to verify legacy tail against v2, deferring promotion", logger.Error(err))
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

	// Open with busy_timeout in the DSN so the checkpoint waits for a briefly-held lock (a
	// loaded runner or a slow shutdown) instead of failing immediately with SQLITE_BUSY. The
	// DSN applies the timeout to every pooled connection, matching the package-wide value used
	// for normal connections (manager.go).
	dsn := dsnAppendParams(dbPath, fmt.Sprintf("_busy_timeout=%d", sqliteBusyTimeoutMs))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
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

// sqliteSidecarSuffixes are the SQLite auxiliary file suffixes that travel with a database
// file (write-ahead log and shared-memory index). Shared so the consolidation preflight and
// moveSQLiteDBFiles agree on exactly which sidecars accompany a move.
var sqliteSidecarSuffixes = []string{"-wal", "-shm"}

// moveSQLiteDBFiles renames a SQLite database file together with its -wal and -shm
// sidecars, so no committed WAL data is left behind or discarded during consolidation.
// The move is all-or-nothing: the main file is renamed first, then each present sidecar;
// any failure (including a non-not-exist stat error on a sidecar, which may hide live WAL
// data) rolls back every completed rename and returns an error, so the caller never sees a
// half-moved database reported as success. It deliberately does NOT reject an existing
// destination, so it can also serve as the rollback restore path (which must be able to
// overwrite); a forward caller is responsible for ensuring its destination is free.
func moveSQLiteDBFiles(from, to string, log logger.Logger) error {
	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf("failed to rename SQLite database %q to %q: %w", from, to, err)
	}
	// Track completed renames so a later failure can undo them in reverse, keeping the move
	// all-or-nothing. The main file is renamed first, so any failure below must revert it;
	// otherwise the source database is stranded without its uncheckpointed WAL while the
	// caller still believes the pre-move state can be cleanly rolled back.
	type movedFile struct{ src, dst string }
	done := []movedFile{{src: from, dst: to}}
	revert := func() {
		for _, m := range slices.Backward(done) {
			if rbErr := os.Rename(m.dst, m.src); rbErr != nil {
				log.Error("failed to roll back SQLite file move; database files may be inconsistent",
					logger.String("from", m.dst),
					logger.String("to", m.src),
					logger.Error(rbErr))
			}
		}
	}
	for _, suffix := range sqliteSidecarSuffixes {
		src := from + suffix
		dst := to + suffix
		if _, err := os.Stat(src); err != nil {
			if os.IsNotExist(err) {
				// os.Stat follows symlinks, so a broken/dangling symlink also reports not-exist.
				// If the link entry itself exists, fail closed rather than treat it as "absent":
				// silently skipping it would leave it behind and break the all-or-nothing move.
				if _, lerr := os.Lstat(src); lerr == nil {
					revert()
					return fmt.Errorf("SQLite sidecar %q is a broken symlink; refusing to leave it behind", src)
				}
				// Source sidecar genuinely absent (a checkpoint truncated it and SQLite removed
				// the file). Remove any stale sidecar left at the destination so it is not wrongly
				// paired with the moved database when it is next opened.
				if rmErr := os.Remove(dst); rmErr != nil && !os.IsNotExist(rmErr) {
					revert()
					return fmt.Errorf("failed to remove stale destination sidecar %q: %w", dst, rmErr)
				}
				continue
			}
			// A non-not-exist stat error (permission, I/O) must not be treated as "absent": the
			// sidecar may still hold live WAL data. Fail closed and roll back.
			revert()
			return fmt.Errorf("failed to stat SQLite sidecar %q: %w", src, err)
		}
		if err := os.Rename(src, dst); err != nil {
			revert()
			return fmt.Errorf("failed to move SQLite sidecar %q to %q: %w", src, dst, err)
		}
		done = append(done, movedFile{src: src, dst: dst})
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

// tableHasAnyRow reports whether the named table holds at least one row. It uses SELECT
// EXISTS, not a full COUNT(*): a contaminated legacy table can hold hundreds of thousands
// of unmigrated rows (GitHub #3924), and counting all of them on every startup would scan
// the whole table and could approach the DB startup timeout. EXISTS short-circuits at the
// first row and yields an explicit 0/1, so it does not depend on GORM populating
// RowsAffected after Scan (which can vary by version/driver). The name is interpolated, so
// callers must pass a name from a fixed list, never user input. Backtick quoting works on
// both SQLite and MySQL (mirrors cleanupLegacySchemaContamination in this package).
func tableHasAnyRow(db *gorm.DB, name string) (bool, error) {
	var hasRow int
	if err := db.Raw("SELECT EXISTS(SELECT 1 FROM `" + name + "`)").Scan(&hasRow).Error; err != nil {
		return false, err
	}
	return hasRow != 0, nil
}

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
		hasRow, err := tableHasAnyRow(db, name)
		if err != nil {
			return false, err
		}
		if hasRow {
			return true, nil
		}
	}
	return false, nil
}

// orphanedBareV2Tables lists the bare v2 tables that do not share a name with a legacy
// table. When bare v2 tables sit beside a legacy notes table, startup checks each of
// them for rows to decide which side to keep. The order (children before parents) is
// the manual drop order of the wiki guide (doc/wiki/database-doctor.md,
// TestMixedSchemaGuideDropOrder); keep the guide, v2Entities (manager.go) and this list
// in sync (TestMixedSchemaTableLists_MatchEntities).
//
// Both naming forms are listed: the OLD singular forms (migration_state, alert_history)
// left by pre-PR #2165 code with TableName() overrides, and the current GORM-pluralized
// forms (migration_states, alert_histories). A name that does not exist in the database
// is skipped when probing.
func orphanedBareV2Tables() []string {
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
		"privacy_filter_hourlies",
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
	return orphanedTables
}

// reportMixedSchemaRefused sends one opt-in Sentry warning when startup refuses a
// MySQL/MariaDB database that holds bare v2 tables beside a legacy notes table.
func reportMixedSchemaRefused() {
	reportMixedSchemaOutcome("mixedSchemaRefused", "Bare v2 tables found alongside legacy tables, refusing to start")
}

// reportMixedSchemaReconciled sends one opt-in Sentry warning when startup keeps one side
// of a MySQL/MariaDB database that holds bare v2 tables beside a legacy notes table.
func reportMixedSchemaReconciled(decision string) {
	reportMixedSchemaOutcome(decision, "Bare v2 tables found alongside legacy tables, kept one side and removed the empty tables of the other")
}

// reportMixedSchemaOutcome sends one Sentry message for the mixed schema startup check.
// The fingerprint is the operation, so each outcome groups separately.
func reportMixedSchemaOutcome(operation, message string) {
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", "datastore-startup")
		scope.SetTag("db_type", "mysql")
		scope.SetTag("operation", operation)
		scope.SetFingerprint([]string{"datastore-startup", "mysql", operation})

		telemetry.CaptureMessage(message, sentry.LevelWarning, "datastore-startup")
	})
}

// mixedSchemaRowCountUnknown marks a table whose row count could not be read.
const mixedSchemaRowCountUnknown int64 = -1

// Names and limits the mixed schema check keys on.
const (
	mixedSchemaDetectionsTable = "detections"
	mixedSchemaNotesTable      = "notes"
	mixedSchemaImageCaches     = "image_caches"
	mixedSchemaLabelIDColumn   = "label_id"
	mixedSchemaNotNull         = "NO"
	// mixedSchemaLockWaitSeconds bounds the wait for the table locks of a drop. A
	// session that holds a write on one of the tables makes the drop fail, and the
	// startup check refuses, which is the safe outcome.
	mixedSchemaLockWaitSeconds = 3
)

// What a failed reconcile step was doing, for the refusal text.
const (
	stepDropLegacy     = "removing the empty legacy tables"
	stepDropDetections = "removing the empty enhanced detection tables"
	stepMarkCompleted  = "marking the empty enhanced database as completed"
)

// Fixed parts of the mixed schema error text and recovery hints.
const (
	mixedSchemaNone           = "none"
	mixedSchemaUnknownCount   = "unknown"
	hintBackupFirst           = "Back up the database before removing either side."
	hintCountsUnknown         = "Some tables could not be read. " + hintBackupFirst
	hintBothHoldData          = "Both sides hold data. Back up the database before removing either side; removing a side loses the rows listed for it."
	hintV2NotCompleted        = "The enhanced database holds detections but is not marked as a completed install. " + hintBackupFirst
	hintSharedTablesHoldData  = "The tables %s hold rows that the startup of the other side would remove. " + hintBackupFirst
	hintDropFailed            = "No table with rows was dropped and the next start decides again from what the database holds. If it keeps failing with a lock or privilege error, check that the database user may lock, update and drop tables and that no other session is using them."
	mixedSchemaDropFailedText = "database holds enhanced database (v2) tables beside a legacy notes table and BirdNET-Go could not remove the empty tables of one side: %s. %s"
	mixedSchemaErrorFormat    = "database holds enhanced database (v2) tables beside a legacy notes table and BirdNET-Go cannot keep one side without losing data. v2 tables with rows: %s. Legacy tables with rows: %s. %s"
)

// bareDetectionTables are the bare v2 tables that hold or reference detections: the
// detections table and the five tables with a foreign key to it, children first and
// detections last. Dropping them (each verified empty) is how the keep-legacy reconcile
// removes the state older releases and the prefix decision key on. Same order as the
// manual drop in the wiki guide.
var bareDetectionTables = []string{
	"detection_locks", "detection_comments", "detection_reviews",
	"detection_model_contributions", "detection_predictions", "detections",
}

// legacyDetectionTables are the legacy tables that keeping the bare v2 database drops:
// notes and its foreign key children, in drop order (children before parents). The wiki
// guide lists the same names. Counted only; this is not legacyDataTables, which
// hasCompleteFreshV2Schema relies on with a different meaning.
var legacyDetectionTables = []string{"note_locks", "note_comments", "note_reviews", "results", "notes"}

// legacyRepairedSharedTables are the tables the legacy store drops, rows included, when
// they have the v2 shape (repairV2ShapedLegacyTables drops those with a NOT NULL label_id,
// validateAndFixSchema drops image_caches with any label_id). Mirror of
// v2ShapedLegacyTables in internal/datastore/manage.go; keep in sync.
var legacyRepairedSharedTables = []string{"notification_histories", "dynamic_thresholds", "threshold_events", mixedSchemaImageCaches}

// v2DroppedThresholdTables are the tables the v2 Initialize drops, rows included, when
// they still have a label_id column (dropStaleThresholdTables in manager.go).
var v2DroppedThresholdTables = []string{"dynamic_thresholds", "threshold_events"}

// mixedSchemaMarkerTables are the tables that can hold the bare v2 migration marker:
// the current plural name and the singular one from releases before #2165.
var mixedSchemaMarkerTables = []string{"migration_states", "migration_state"}

// tableRowCount is what the probe learned about one table. hasRows is the EXISTS
// result and decides every verdict; rows is only for the message and is
// mixedSchemaRowCountUnknown when the COUNT failed.
type tableRowCount struct {
	table   string
	rows    int64
	hasRows bool
}

// sharedTableProbe is a table with a label_id column whose name the legacy and the v2
// schema share, such as notification_histories.
type sharedTableProbe struct {
	tableRowCount
	// labelNotNull is true when the label_id column is NOT NULL.
	labelNotNull bool
}

// mixedSchemaProbe is everything the classifier needs, read with SELECTs only.
type mixedSchemaProbe struct {
	// v2Tables are the existing orphanedBareV2Tables.
	v2Tables []tableRowCount
	// legacyTables are the existing legacyDetectionTables.
	legacyTables []tableRowCount
	// sharedTables are the existing legacyRepairedSharedTables that have a label_id column.
	sharedTables []sharedTableProbe
	// markerTable is the existing bare migration marker table, empty when there is none.
	markerTable string
	// v2Completed is true when the marker row says completed.
	v2Completed bool
	// unknown is true when any input could not be read.
	unknown bool
}

// mixedSchemaVerdict is the outcome of classifyMixedSchema. The zero value refuses.
type mixedSchemaVerdict int

const (
	// verdictRefuseUnknown: an input could not be read.
	verdictRefuseUnknown mixedSchemaVerdict = iota
	// verdictKeepV2: legacy tables empty, enhanced install completed.
	verdictKeepV2
	// verdictFreshV2: both sides empty, enhanced schema not yet marked completed.
	verdictFreshV2
	// verdictKeepLegacy: legacy tables hold data, the bare detection tables are empty.
	verdictKeepLegacy
	// verdictRefuseV2NotCompleted: the enhanced side holds detections without a completed
	// marker. Keeping it would start v2-only mode on the v2_ prefix (database_migration.go,
	// CheckMySQLHasFreshV2Schema needs a completed marker), which hides these detections.
	verdictRefuseV2NotCompleted
	// verdictRefuseBothHoldData: both sides hold detections or legacy data.
	verdictRefuseBothHoldData
	// verdictRefuseSharedTables: keeping a side would let its startup drop rows of a shared table.
	verdictRefuseSharedTables
	// verdictRefuseDropFailed: an empty table could not be removed.
	verdictRefuseDropFailed
)

// mixedSchemaError is the StartupState.Error of DecisionMixedSchemaRefused. It carries
// the tables with rows on both sides and unwraps to ErrMixedSchemaDatabase.
type mixedSchemaError struct {
	verdict mixedSchemaVerdict
	// v2Tables are the bare v2 tables and the v2-shaped shared tables, with their counts.
	v2Tables []tableRowCount
	// legacyTables are the existing legacy detection tables with their counts.
	legacyTables []tableRowCount
	// blocking are the shared tables with rows that the startup of the other side would drop.
	blocking []string
	// failure says which reconcile step failed, for verdictRefuseDropFailed.
	failure string
}

// newMixedSchemaError builds the refusal error for a verdict from the probe.
func newMixedSchemaError(p *mixedSchemaProbe, verdict mixedSchemaVerdict) *mixedSchemaError {
	e := &mixedSchemaError{verdict: verdict, v2Tables: slices.Clone(p.v2Tables), legacyTables: p.legacyTables}
	for _, s := range p.sharedTables {
		e.v2Tables = append(e.v2Tables, s.tableRowCount)
		if s.hasRows && (legacyDropsRows(s) || v2DropsRows(s)) {
			e.blocking = append(e.blocking, s.table)
		}
	}
	return e
}

// Error returns the deterministic message: tables in list order, only tables with rows
// or an unknown count, and none for a side with nothing to print. For a failed drop it
// names the failed step instead, because no table list explains that refusal.
func (e *mixedSchemaError) Error() string {
	if e.verdict == verdictRefuseDropFailed {
		return fmt.Sprintf(mixedSchemaDropFailedText, e.failure, hintDropFailed)
	}
	return fmt.Sprintf(mixedSchemaErrorFormat,
		formatTableRowCounts(e.v2Tables), formatTableRowCounts(e.legacyTables), mixedSchemaHint(e))
}

// Unwrap lets errors.Is match ErrMixedSchemaDatabase.
func (e *mixedSchemaError) Unwrap() error { return ErrMixedSchemaDatabase }

// formatTableRowCounts lists the tables with rows or an unknown count as name=count.
func formatTableRowCounts(counts []tableRowCount) string {
	parts := make([]string, 0, len(counts))
	for _, c := range counts {
		switch {
		case c.rows == mixedSchemaRowCountUnknown:
			parts = append(parts, c.table+"="+mixedSchemaUnknownCount)
		case c.rows > 0:
			parts = append(parts, fmt.Sprintf("%s=%d", c.table, c.rows))
		}
	}
	if len(parts) == 0 {
		return mixedSchemaNone
	}
	return strings.Join(parts, ", ")
}

// mixedSchemaHint returns the last sentence of the message: what the user may do by hand.
func mixedSchemaHint(e *mixedSchemaError) string {
	switch e.verdict {
	case verdictRefuseV2NotCompleted:
		return hintV2NotCompleted
	case verdictRefuseBothHoldData:
		return hintBothHoldData
	case verdictRefuseSharedTables:
		return fmt.Sprintf(hintSharedTablesHoldData, strings.Join(e.blocking, ", "))
	case verdictRefuseDropFailed:
		return hintDropFailed
	default:
		return hintCountsUnknown
	}
}

// legacyDropsRows reports whether the legacy store would drop the rows of s on its next
// start: a NOT NULL label_id (repairV2ShapedLegacyTables), or any label_id on image_caches
// (validateAndFixSchema).
func legacyDropsRows(s sharedTableProbe) bool {
	return slices.Contains(legacyRepairedSharedTables, s.table) && (s.labelNotNull || s.table == mixedSchemaImageCaches)
}

// v2DropsRows reports whether the v2 Initialize would drop the rows of s on its next
// start: a threshold table that still has a label_id column.
func v2DropsRows(s sharedTableProbe) bool {
	return slices.Contains(v2DroppedThresholdTables, s.table)
}

// detectionFamilyHasRows reports whether any bare detection table holds rows.
func detectionFamilyHasRows(p *mixedSchemaProbe) bool {
	return slices.ContainsFunc(p.v2Tables, func(c tableRowCount) bool {
		return c.hasRows && slices.Contains(bareDetectionTables, c.table)
	})
}

// anyRows reports whether any entry holds rows.
func anyRows(tables []tableRowCount) bool {
	return slices.ContainsFunc(tables, func(c tableRowCount) bool { return c.hasRows })
}

// classifyMixedSchema decides what to do with bare v2 tables beside a legacy notes
// table. It is pure: the verdict depends only on which tables hold rows, never on how
// many, and on nothing unreadable. First match wins.
//
//   - unknown input: refuse.
//   - legacy tables empty: keep the enhanced database. A completed install is kept; an
//     uncompleted one is kept only when it holds no detections. Refuse if the v2
//     startup would drop rows of a threshold table.
//   - legacy tables hold data: keep the legacy database when the bare detection
//     tables are empty and the legacy startup would drop no shared table rows.
func classifyMixedSchema(p *mixedSchemaProbe) mixedSchemaVerdict {
	if p.unknown {
		return verdictRefuseUnknown
	}
	if !anyRows(p.legacyTables) {
		if slices.ContainsFunc(p.sharedTables, func(s sharedTableProbe) bool { return s.hasRows && v2DropsRows(s) }) {
			return verdictRefuseSharedTables
		}
		if p.v2Completed {
			return verdictKeepV2
		}
		if detectionFamilyHasRows(p) {
			return verdictRefuseV2NotCompleted
		}
		return verdictFreshV2
	}
	if detectionFamilyHasRows(p) {
		return verdictRefuseBothHoldData
	}
	if slices.ContainsFunc(p.sharedTables, func(s sharedTableProbe) bool { return s.hasRows && legacyDropsRows(s) }) {
		return verdictRefuseSharedTables
	}
	return verdictKeepLegacy
}

// probeTableRows reads whether a table holds rows and, only when it does, how many. The
// bool is false when the EXISTS probe failed; the entry then reports rows as unknown
// so the table counts as non-empty. A COUNT failure never changes the verdict: an exact
// COUNT of a large table can pass the startup read timeout, and the count is only
// printed. The name comes from a fixed list (see tableHasAnyRow).
func probeTableRows(db *gorm.DB, name string) (tableRowCount, bool) {
	hasRow, err := tableHasAnyRow(db, name)
	if err != nil {
		return tableRowCount{table: name, rows: mixedSchemaRowCountUnknown, hasRows: true}, false
	}
	if !hasRow {
		return tableRowCount{table: name}, true
	}
	var rows int64
	if err := db.Raw("SELECT COUNT(*) FROM `" + name + "`").Scan(&rows).Error; err != nil {
		rows = mixedSchemaRowCountUnknown
	}
	return tableRowCount{table: name, rows: rows, hasRows: true}, true
}

// probeTables reads the existing tables of a fixed list, in list order, and marks the
// probe unknown when one of them cannot be read.
func (p *mixedSchemaProbe) probeTables(db *gorm.DB, existing map[string]struct{}, tables []string) []tableRowCount {
	probed := make([]tableRowCount, 0, len(tables))
	for _, name := range tables {
		if _, ok := existing[name]; !ok {
			continue
		}
		entry, ok := probeTableRows(db, name)
		if !ok {
			p.unknown = true
		}
		probed = append(probed, entry)
	}
	return probed
}

// listTableNames lists the tables of a database straight from information_schema as a set
// of lower-cased names. With baseTablesOnly, views are left out. It returns the error of
// the listing, so callers can fail closed.
func listTableNames(db *gorm.DB, database string, baseTablesOnly bool) (map[string]struct{}, error) {
	query := "SELECT table_name FROM information_schema.tables WHERE table_schema = ?"
	if baseTablesOnly {
		query += " AND table_type = 'BASE TABLE'"
	}
	var names []string
	if err := db.Raw(query, database).Scan(&names).Error; err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[strings.ToLower(name)] = struct{}{}
	}
	return set, nil
}

// probeMixedSchema reads both sides of a mixed schema database with SELECTs only. It
// lists the tables straight from information_schema (GORM's Migrator().GetTables()
// swallows the errors of its CurrentDatabase query on MySQL) and fails closed: when the
// listing fails or lacks detections or notes (which existed moments ago), or any
// existence probe fails, the result is marked unknown. It never returns an error.
func probeMixedSchema(db *gorm.DB, database string) *mixedSchemaProbe {
	p := &mixedSchemaProbe{}

	existing, listErr := listTableNames(db, database, false)
	_, hasDetections := existing[mixedSchemaDetectionsTable]
	_, hasNotes := existing[mixedSchemaNotesTable]
	if listErr != nil || !hasDetections || !hasNotes {
		p.unknown = true
		p.v2Tables = []tableRowCount{{table: mixedSchemaDetectionsTable, rows: mixedSchemaRowCountUnknown, hasRows: true}}
		p.legacyTables = []tableRowCount{{table: mixedSchemaNotesTable, rows: mixedSchemaRowCountUnknown, hasRows: true}}
		return p
	}

	p.v2Tables = p.probeTables(db, existing, orphanedBareV2Tables())
	p.legacyTables = p.probeTables(db, existing, legacyDetectionTables)
	p.probeSharedTables(db, database)
	p.probeMarker(db, existing)
	return p
}

// probeSharedTables finds the shared tables that have a label_id column (the v2 shape)
// and whether they hold rows.
func (p *mixedSchemaProbe) probeSharedTables(db *gorm.DB, database string) {
	var columns []struct {
		Name     string
		Nullable string
	}
	err := db.Raw("SELECT table_name AS name, is_nullable AS nullable FROM information_schema.columns "+
		"WHERE table_schema = ? AND column_name = ? AND table_name IN ?",
		database, mixedSchemaLabelIDColumn, legacyRepairedSharedTables).Scan(&columns).Error
	if err != nil {
		p.unknown = true
		return
	}
	for _, col := range columns {
		name := strings.ToLower(col.Name)
		entry, ok := probeTableRows(db, name)
		if !ok {
			p.unknown = true
		}
		p.sharedTables = append(p.sharedTables, sharedTableProbe{
			tableRowCount: entry,
			labelNotNull:  strings.EqualFold(col.Nullable, mixedSchemaNotNull),
		})
	}
}

// probeMarker reads the bare migration marker. A missing marker table or row is a
// known "not completed". A query error, or a state other than idle or completed (the
// bare schema never holds one), is unknown.
func (p *mixedSchemaProbe) probeMarker(db *gorm.DB, existing map[string]struct{}) {
	for _, name := range mixedSchemaMarkerTables {
		if _, ok := existing[name]; !ok {
			continue
		}
		p.markerTable = name
		var states []string
		if err := db.Raw("SELECT state FROM `" + name + "` LIMIT 1").Scan(&states).Error; err != nil {
			p.unknown = true
			return
		}
		if len(states) == 0 {
			return
		}
		switch entities.MigrationStatus(states[0]) {
		case entities.MigrationStatusCompleted:
			p.v2Completed = true
		case entities.MigrationStatusIdle:
		default:
			p.unknown = true
		}
		return
	}
}

// errMixedSchemaTableNotEmpty is returned by dropVerifiedEmptyTables when a table it
// was asked to drop holds a row; nothing is dropped in that call.
var errMixedSchemaTableNotEmpty = errors.NewStd("table holds rows")

// dropVerifiedEmptyTables drops the listed tables, and only if every one of them that
// exists is empty at the moment of the drop. On one pinned connection it turns foreign
// key checks off (before LOCK TABLES, so a DROP cannot fail on a foreign key while the
// tables are locked), takes a WRITE lock on every table present, checks each for
// a row, and drops them in list order. The lock keeps every other session's insert out
// from the check until after the drop. A row or any error aborts before the first DROP
// of this call. It returns the tables it dropped. Table names come from the fixed lists
// above, so the interpolation is safe.
func dropVerifiedEmptyTables(db *gorm.DB, database string, tables []string) ([]string, error) {
	var dropped []string
	err := db.Connection(func(tx *gorm.DB) (retErr error) {
		if err := tx.Exec("SET FOREIGN_KEY_CHECKS = 0").Error; err != nil {
			return fmt.Errorf("disable foreign key checks: %w", err)
		}
		// Restore the session through defers so the pooled connection is never
		// returned with checks off or locks held, even when a DROP fails.
		defer func() {
			if err := tx.Exec("SET FOREIGN_KEY_CHECKS = 1").Error; err != nil && retErr == nil {
				retErr = fmt.Errorf("restore foreign key checks: %w", err)
			}
		}()
		var previousWait int
		if err := tx.Raw("SELECT @@SESSION.lock_wait_timeout").Scan(&previousWait).Error; err != nil {
			return fmt.Errorf("read lock wait timeout: %w", err)
		}
		if err := tx.Exec(fmt.Sprintf("SET SESSION lock_wait_timeout = %d", mixedSchemaLockWaitSeconds)).Error; err != nil {
			return fmt.Errorf("set lock wait timeout: %w", err)
		}
		defer func() {
			if err := tx.Exec(fmt.Sprintf("SET SESSION lock_wait_timeout = %d", previousWait)).Error; err != nil && retErr == nil {
				retErr = fmt.Errorf("restore lock wait timeout: %w", err)
			}
		}()

		existing, listErr := listTableNames(tx, database, true)
		if listErr != nil {
			return fmt.Errorf("list tables: %w", listErr)
		}
		present := make([]string, 0, len(tables))
		for _, name := range tables {
			if _, ok := existing[name]; ok {
				present = append(present, name)
			}
		}
		if len(present) == 0 {
			return nil
		}

		locks := make([]string, len(present))
		for i, name := range present {
			locks[i] = "`" + name + "` WRITE"
		}
		if err := tx.Exec("LOCK TABLES " + strings.Join(locks, ", ")).Error; err != nil {
			return fmt.Errorf("lock tables: %w", err)
		}
		defer func() {
			if err := tx.Exec("UNLOCK TABLES").Error; err != nil && retErr == nil {
				retErr = fmt.Errorf("unlock tables: %w", err)
			}
		}()

		for _, name := range present {
			hasRow, err := tableHasAnyRow(tx, name)
			if err != nil {
				return fmt.Errorf("check table %s: %w", name, err)
			}
			if hasRow {
				return fmt.Errorf("%w: %s", errMixedSchemaTableNotEmpty, name)
			}
		}
		for _, name := range present {
			if err := tx.Exec("DROP TABLE `" + name + "`").Error; err != nil {
				return fmt.Errorf("drop table %s: %w", name, err)
			}
			dropped = append(dropped, name)
		}
		return nil
	})
	return dropped, err
}

// markEmptyBareSchemaCompleted marks an empty bare v2 schema as a completed install by
// updating its idle marker row. It reports true only when exactly one row changed; a
// missing marker table, a missing row or any other state returns false, and the caller
// then drops nothing. It is the only row write of the reconcile and changes the
// regenerable singleton marker only. The table name comes from mixedSchemaMarkerTables.
func markEmptyBareSchemaCompleted(db *gorm.DB, markerTable string) (bool, error) {
	if markerTable == "" {
		return false, nil
	}
	res := db.Exec("UPDATE `"+markerTable+"` SET state = ? WHERE id = 1 AND state = ?",
		string(entities.MigrationStatusCompleted), string(entities.MigrationStatusIdle))
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// StartupState shapes returned by the mixed schema check. They match the shapes the
// non-mixed branches of checkMySQLMigrationState return, so callers need no new case. The
// keep-v2 outcomes use completedV2StartupState.
func mixedSchemaLegacyState(decision string) StartupState {
	return StartupState{
		MigrationStatus: entities.MigrationStatusIdle,
		LegacyRequired:  true,
		Decision:        decision,
	}
}

func mixedSchemaFreshState(decision string) StartupState {
	return StartupState{
		MigrationStatus: entities.MigrationStatusIdle,
		FreshInstall:    true,
		Decision:        decision,
	}
}

func mixedSchemaRefusedState(err error) StartupState {
	return StartupState{
		MigrationStatus: entities.MigrationStatusIdle,
		Decision:        DecisionMixedSchemaRefused,
		Error:           err,
	}
}

// logMixedSchemaReconciled writes the one Warn line of a reconcile: what was found with
// counts, what was kept, what was dropped. Only fixed table names and counts appear.
func logMixedSchemaReconciled(msg, decision string, p *mixedSchemaProbe, dropped []string, extra ...logger.Field) {
	v2 := slices.Clone(p.v2Tables)
	for _, s := range p.sharedTables {
		v2 = append(v2, s.tableRowCount)
	}
	droppedText := mixedSchemaNone
	if len(dropped) > 0 {
		droppedText = strings.Join(dropped, ", ")
	}
	fields := append([]logger.Field{
		logger.String("decision", decision),
		logger.String("v2_tables_with_rows", formatTableRowCounts(v2)),
		logger.String("legacy_tables_with_rows", formatTableRowCounts(p.legacyTables)),
		logger.String("dropped_empty_tables", droppedText),
	}, extra...)
	if decision == DecisionMixedSchemaKeptLegacy {
		fields = append(fields, logger.String("left_in_place_with_rows", formatTableRowCounts(v2)))
	}
	logger.Global().Module("datastore").Warn(msg, fields...)
	reportMixedSchemaReconciled(decision)
}

// describeStepFailure names the failed step and, when the driver reports one, the
// MySQL error number, so the refusal names a cause without carrying driver text (which
// can hold the database name).
func describeStepFailure(step string, err error) string {
	if errors.Is(err, errMixedSchemaTableNotEmpty) {
		return step + " stopped because a table gained rows"
	}
	if myErr, ok := errors.AsType[*mysql.MySQLError](err); ok {
		return fmt.Sprintf("%s failed (MySQL error %d)", step, myErr.Number)
	}
	return step + " failed"
}

// resolveMixedSchema handles bare v2 tables beside a legacy notes table. It probes both
// sides, classifies, and either keeps one side (dropping only the empty tables of the
// other, each verified empty under a lock) or refuses. Refusing drops no table with
// rows. A failed drop or marker write refuses the boot too. A marker already written
// for the fresh shape stays; with no legacy rows the next start classifies the database
// as keep-v2 and retries the drop. The table the branch keys on is always dropped last.
func resolveMixedSchema(db *gorm.DB, database string, redact ...string) StartupState {
	p := probeMixedSchema(db, database)
	verdict := classifyMixedSchema(p)

	refuse := func(v mixedSchemaVerdict) StartupState {
		reportMixedSchemaRefused()
		return mixedSchemaRefusedState(newMixedSchemaError(p, v))
	}
	failed := func(operation, step string, err error) StartupState {
		reportStartupError("mysql", operation, err, redact...)
		reportMixedSchemaRefused()
		refusal := newMixedSchemaError(p, verdictRefuseDropFailed)
		refusal.failure = describeStepFailure(step, err)
		return mixedSchemaRefusedState(refusal)
	}

	switch verdict {
	case verdictKeepV2:
		dropped, err := dropVerifiedEmptyTables(db, database, legacyDetectionTables)
		if err != nil {
			return failed("mixedSchemaDropLegacy", stepDropLegacy, err)
		}
		logMixedSchemaReconciled("database held enhanced database (v2) tables beside empty legacy tables; kept the enhanced database and removed the empty legacy tables",
			DecisionMixedSchemaKeptV2, p, dropped)
		return completedV2StartupState(DecisionMixedSchemaKeptV2)

	case verdictFreshV2:
		marked, err := markEmptyBareSchemaCompleted(db, p.markerTable)
		if err != nil {
			return failed("mixedSchemaMarkCompleted", stepMarkCompleted, err)
		}
		if !marked {
			// No marker row to update: the fresh install path writes it. Dropping the
			// legacy tables over an unmarked schema would leave a state the next boot refuses.
			logMixedSchemaReconciled("database held empty enhanced database (v2) tables beside empty legacy tables; kept the enhanced database",
				DecisionMixedSchemaFreshV2, p, nil, logger.Bool("marked_completed", false))
			return mixedSchemaFreshState(DecisionMixedSchemaFreshV2)
		}
		dropped, err := dropVerifiedEmptyTables(db, database, legacyDetectionTables)
		if err != nil {
			// The completed marker stays. With no legacy rows the next start classifies this
			// database as keep-v2 and retries the drop, so there is nothing to undo.
			return failed("mixedSchemaDropLegacy", stepDropLegacy, err)
		}
		logMixedSchemaReconciled("database held empty enhanced database (v2) tables beside empty legacy tables; kept the enhanced database, marked it completed and removed the empty legacy tables",
			DecisionMixedSchemaFreshV2, p, dropped, logger.Bool("marked_completed", true))
		return completedV2StartupState(DecisionMixedSchemaFreshV2)

	case verdictKeepLegacy:
		dropped, err := dropVerifiedEmptyTables(db, database, bareDetectionTables)
		if err != nil {
			return failed("mixedSchemaDropDetections", stepDropDetections, err)
		}
		logMixedSchemaReconciled("database held legacy tables beside empty enhanced database (v2) detection tables; kept the legacy database and removed the empty detection tables, every other enhanced table was left in place",
			DecisionMixedSchemaKeptLegacy, p, dropped)
		return mixedSchemaLegacyState(DecisionMixedSchemaKeptLegacy)

	default:
		return refuse(verdict)
	}
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

	// Refuse to overwrite an existing backup destination (main file or a -wal/-shm sidecar)
	// before doing any work. GenerateBackupPath is timestamped, so a collision means a
	// same-second re-run; the legacy->backup rename would otherwise clobber a prior backup that
	// the rollback could not restore, so fail closed. moveSQLiteDBFiles itself stays
	// overwrite-capable so it can serve as the rollback restore path. Check the primary file
	// ("") plus each sidecar.
	for _, suffix := range append([]string{""}, sqliteSidecarSuffixes...) {
		if _, statErr := os.Lstat(backupPath + suffix); statErr == nil {
			return false, fmt.Errorf("consolidation aborted: backup destination %q already exists", backupPath+suffix)
		} else if !os.IsNotExist(statErr) {
			return false, fmt.Errorf("failed to stat backup destination %q: %w", backupPath+suffix, statErr)
		}
	}

	// Write consolidation state file
	state := &ConsolidationState{
		LegacyPath:     configuredPath,
		V2Path:         v2MigrationPath,
		BackupPath:     backupPath,
		ConfiguredPath: configuredPath,
		StartedAt:      time.Now(),
	}
	if err := WriteConsolidationState(dataDir, state, log); err != nil {
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
	} else if !os.IsNotExist(err) {
		// A non-not-exist stat error (permission/I/O) must not silently skip the legacy backup:
		// the v2 -> configured move below would then rename over the still-present legacy
		// database with no backup taken. Fail closed instead of clobbering it.
		reportConsolidationError("startupStatLegacy", err, configuredPath)
		_ = DeleteConsolidationState(dataDir)
		diagnostics.RecordConsolidation(j, v2MigrationPath, configuredPath, backupPath, "failed")
		return false, fmt.Errorf("failed to stat legacy database %q before backup: %w", configuredPath, err)
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
