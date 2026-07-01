// Package v2 provides the v2 normalized database implementation.
package v2

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"github.com/tphakala/birdnet-go/internal/telemetry"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gorm_logger "gorm.io/gorm/logger"
)

// defaultGormSlowThreshold is the duration above which GORM queries are logged as slow.
// Set to 1 second to accommodate migration batch queries which can take 800-900ms.
const defaultGormSlowThreshold = 1 * time.Second

// sqliteBusyTimeoutMs is the SQLite busy_timeout pragma value in milliseconds.
// Matches the 30s timeout used by the legacy datastore.
const sqliteBusyTimeoutMs = 30_000

// sqliteMmapSizeBytes caps SQLite's memory-mapped I/O for the main DB file at
// 256 MiB. It is a max (SQLite maps at most the file size) and consumes virtual
// address space, not resident memory, so it is safe on small 64-bit hosts and a
// large win for cold reads on slow storage. Matches the legacy datastore.
const sqliteMmapSizeBytes = 256 * 1024 * 1024

// walCheckpointInterval is how often a periodic passive WAL checkpoint runs.
// SQLite's auto-checkpoint (1000 pages) may not fire reliably with connection
// pooling because the page counter is per-connection. A 5-minute interval
// prevents unbounded WAL growth while keeping I/O overhead minimal.
const walCheckpointInterval = 5 * time.Minute

// Manager defines the interface for v2 database operations.
type Manager interface {
	// Initialize creates the schema and seeds initial data.
	Initialize() error
	// DB returns the underlying GORM database.
	DB() *gorm.DB
	// Path returns the database location (file path for SQLite, connection string info for MySQL).
	Path() string
	// Close closes the database connection.
	Close() error
	// CheckpointWAL forces a WAL checkpoint to ensure all changes are written to the main database file.
	// This should be called before Close() for graceful shutdown.
	CheckpointWAL() error
	// Delete removes the v2 database (file for SQLite, tables for MySQL).
	Delete() error
	// Exists checks if the v2 database exists.
	Exists() bool
	// IsMySQL returns true if this is a MySQL manager.
	IsMySQL() bool
	// TablePrefix returns the prefix applied to every v2 table name. It is
	// non-empty (currently "v2_") only on MySQL deployments that are still
	// in the v1→v2 migration window — there, v2 tables coexist alongside
	// the legacy v1 schema and need the prefix to avoid name collisions.
	// On SQLite, on fresh-install MySQL, and on any future cutover where
	// the prefix is dropped, this returns "". Code that builds raw-SQL
	// table references MUST consult this to stay correct under all three
	// deployment modes.
	TablePrefix() string
}

// Config holds database configuration for the v2 manager.
type Config struct {
	// ConfiguredPath is the user's configured database path (e.g., /data/birdnet.db).
	// Used for migration mode to derive the v2 path (e.g., /data/birdnet_v2.db).
	// If DirectPath is set, ConfiguredPath is ignored.
	ConfiguredPath string
	// DirectPath specifies the exact database path to use (for fresh installs).
	// If set, this path is used directly instead of deriving from ConfiguredPath.
	// This allows fresh installations to use the configured path without _v2 suffix.
	DirectPath string
	// Debug enables verbose logging.
	Debug bool
	// Logger is the project logger for GORM to use.
	Logger logger.Logger

	// Deprecated: Use ConfiguredPath instead. DataDir is kept for backwards compatibility
	// during migration. When set, it's used to construct ConfiguredPath if not provided.
	DataDir string
}

// scrubErrorWithPaths scrubs an error message, replacing known file paths with
// anonymized versions before applying general scrubbing. This is necessary because
// privacy.ScrubMessage() does not handle file paths, but OS-level errors from
// os.Rename, gorm.Open, etc. embed absolute paths that may contain usernames.
func scrubErrorWithPaths(errMsg string, paths ...string) string {
	result := errMsg
	for _, p := range paths {
		if p != "" {
			result = strings.ReplaceAll(result, p, privacy.AnonymizePath(p))
		}
	}
	return privacy.ScrubMessage(result)
}

// reportInitFailure reports a schema initialization failure to Sentry telemetry.
// This covers AutoMigrate, seeding, and trigger creation failures.
// The paths parameter lists values (file paths, hostnames, database names) to scrub from error messages.
func reportInitFailure(dbType, operation string, err error, paths ...string) {
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", "datastore-init")
		scope.SetTag("db_type", dbType)
		scope.SetTag("operation", operation)
		scope.SetFingerprint([]string{"datastore-init", dbType, operation})

		scrubbedErr := scrubErrorWithPaths(err.Error(), paths...)
		scope.SetContext("init_failure", map[string]any{
			"db_type":   dbType,
			"operation": operation,
			"error":     scrubbedErr,
		})

		telemetry.CaptureMessage(
			fmt.Sprintf("Schema initialization failed: %s (%s)", operation, dbType),
			sentry.LevelError,
			"datastore-init",
		)
	})
}

// reportSchemaEvolution reports extra columns from schema evolution to Sentry telemetry.
// These are harmless leftovers from earlier entity versions that GORM never drops.
func reportSchemaEvolution(dbType, tableName string, unexpected []string, rowCount int64) {
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", "datastore-init")
		scope.SetTag("db_type", dbType)
		scope.SetTag("operation", "schema_evolution_detected")
		scope.SetTag("table", tableName)
		scope.SetFingerprint([]string{"schema-evolution", tableName})
		scope.SetContext("schema_evolution", map[string]any{
			"table":              tableName,
			"unexpected_columns": unexpected,
			"row_count":          rowCount,
		})
		telemetry.CaptureMessage(
			fmt.Sprintf("Schema evolution: table %s has %d extra column(s)", tableName, len(unexpected)),
			sentry.LevelWarning,
			"datastore-init",
		)
	})
}

// reportMissingColumns reports columns AutoMigrate failed to add. Distinct fingerprint
// from extra-column reports so the two failure modes group separately in Sentry.
func reportMissingColumns(dbType string, missing []missingColumnsEntry) {
	if len(missing) == 0 {
		return
	}
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", "datastore-init")
		scope.SetTag("db_type", dbType)
		scope.SetTag("operation", "missing_columns_detected")
		scope.SetFingerprint([]string{"missing-columns", dbType})

		detail := make([]map[string]any, 0, len(missing))
		tables := make([]string, 0, len(missing))
		totalCols := 0
		for _, m := range missing {
			tables = append(tables, m.table)
			totalCols += len(m.columns)
			detail = append(detail, map[string]any{
				"table":           m.table,
				"missing_columns": m.columns,
			})
		}
		scope.SetContext("missing_columns", map[string]any{
			"tables":        tables,
			"per_table":     detail,
			"total_missing": totalCols,
		})

		telemetry.CaptureMessage(
			fmt.Sprintf("Schema corruption: AutoMigrate did not add %d column(s) across %d table(s)",
				totalCols, len(missing)),
			sentry.LevelError,
			"datastore-init",
		)
	})
}

// SQLiteManager handles the v2 normalized database for SQLite.
type SQLiteManager struct {
	db     *gorm.DB
	dbPath string
	log    logger.Logger

	// WAL checkpoint lifecycle
	walMu     sync.Mutex // protects walCtx and walCancel
	walCtx    context.Context
	walCancel context.CancelFunc
	walWg     sync.WaitGroup
}

// NewSQLiteManager creates a new v2 SQLite database manager.
// If DirectPath is set, uses that exact path (for fresh installs).
// Otherwise, derives the v2 migration path from ConfiguredPath (for migration mode).
func NewSQLiteManager(cfg Config) (*SQLiteManager, error) {
	var dbPath string
	switch {
	case cfg.DirectPath != "":
		// Fresh install or post-consolidation: use exact path provided
		dbPath = cfg.DirectPath
	case cfg.ConfiguredPath != "":
		// Migration mode: derive v2 path from configured path
		dbPath = V2MigrationPathFromConfigured(cfg.ConfiguredPath)
	case cfg.DataDir != "":
		// Backwards compatibility: derive from legacy configured path pattern
		// Assumes configured path was DataDir/birdnet.db
		dbPath = V2MigrationPathFromConfigured(filepath.Join(cfg.DataDir, "birdnet.db"))
	default:
		return nil, fmt.Errorf("either DirectPath, ConfiguredPath, or DataDir must be set")
	}

	// Create GORM logger using the adapter if a logger is provided
	var gormLogger gorm_logger.Interface
	if cfg.Logger != nil {
		gormLogger = logger.NewGormLoggerAdapter(cfg.Logger, defaultGormSlowThreshold)
	} else {
		gormLogger = gorm_logger.Default.LogMode(gorm_logger.Silent)
	}

	// Build DSN with recommended SQLite pragmas.
	// All pragmas are set via DSN query parameters so they apply to every
	// connection created by the pool, not just the first one.
	// Use safe separator in case dbPath already contains query parameters
	// (e.g., "file::memory:?cache=shared" in tests).
	pragmas := fmt.Sprintf("_journal_mode=WAL&_busy_timeout=%d&_foreign_keys=ON&_synchronous=NORMAL&_cache_size=-16000", sqliteBusyTimeoutMs)
	sep := "?"
	if strings.Contains(dbPath, "?") {
		sep = "&"
	}
	dsn := dbPath + sep + pragmas

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open v2 database: %w", err)
	}

	// Limit to a single open connection to serialize all database access.
	// SQLite only supports one writer at a time; with Go's connection pool,
	// multiple goroutines can obtain separate connections and attempt
	// concurrent writes, causing "database is locked" errors even with
	// busy_timeout set.  A single connection eliminates this contention.
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying database: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)

	// Enable memory-mapped I/O for reads. mattn/go-sqlite3 has no _mmap_size DSN
	// param, so set it per-connection; with SetMaxOpenConns(1) and no connection
	// expiry there is a single long-lived connection, so this one Exec persists.
	// mmap maps the main DB file (up to the cap or its size) into the page cache,
	// avoiding read() syscalls; it is read-only and WAL/durability are unaffected.
	if err := db.Exec(fmt.Sprintf("PRAGMA mmap_size=%d", sqliteMmapSizeBytes)).Error; err != nil && cfg.Logger != nil {
		cfg.Logger.Warn("Failed to set SQLite mmap_size (continuing without memory-mapped reads)",
			logger.Error(err))
	}

	return &SQLiteManager{
		db:     db,
		dbPath: dbPath,
		log:    cfg.Logger,
	}, nil
}

// v2Entities returns the canonical list of all v2 entity pointers.
// This is the single source of truth used by both Initialize (AutoMigrate)
// and validateV2SchemaIntegrity.
func v2Entities() []any {
	return []any{
		// Lookup tables (must be created first due to FK constraints)
		&entities.LabelType{},
		&entities.TaxonomicClass{},
		// Core detection entities
		&entities.AIModel{},
		&entities.Label{},
		&entities.AudioSource{},
		&entities.Detection{},
		&entities.DetectionPrediction{},
		&entities.DetectionModelContribution{},
		&entities.DetectionReview{},
		&entities.DetectionComment{},
		&entities.DetectionLock{},
		&entities.MigrationState{},
		&entities.MigrationDirtyID{},
		// Auxiliary tables
		&entities.DailyEvents{},
		&entities.HourlyWeather{},
		&entities.ImageCache{},
		&entities.DynamicThreshold{},
		&entities.ThresholdEvent{},
		&entities.NotificationHistory{},
		// Alert rules engine
		&entities.AlertRule{},
		&entities.AlertCondition{},
		&entities.AlertAction{},
		&entities.AlertHistory{},
		// Application metadata
		&entities.AppMetadata{},
		// Application event log
		&entities.AppEvent{},
		// Managed species lists
		&entities.SpeciesList{},
		&entities.SpeciesListMember{},
	}
}

// Initialize creates the schema and seeds initial data.
func (m *SQLiteManager) Initialize() error {
	// Rename tables that changed names in PR #2165 (TableName() overrides removed).
	// This must run BEFORE AutoMigrate to avoid creating duplicate tables.
	if err := m.renamePrePR2165Tables(); err != nil {
		return err
	}

	// Remove orphaned columns added by legacy AutoMigrate when the app incorrectly
	// fell back to legacy mode due to the PR #2165 table name mismatch.
	if err := m.cleanupLegacySchemaContamination(); err != nil {
		if m.log != nil {
			m.log.Warn("legacy schema cleanup encountered errors",
				logger.Error(err),
				logger.String("operation", "cleanup_legacy_contamination"))
		}
	}

	// Run GORM auto-migrations for all entities
	err := m.db.AutoMigrate(v2Entities()...)
	if err != nil {
		reportInitFailure("sqlite", "AutoMigrate", err, m.dbPath)
		return fmt.Errorf("failed to migrate v2 schema: %w", err)
	}

	// Validate schema integrity AFTER AutoMigrate so missing columns indicate a real
	// silent failure (GitHub #3211: GORM AutoMigrate sometimes does not add
	// newly-introduced columns to an existing table, breaking every subsequent INSERT).
	// Extra columns from schema evolution remain tolerated; missing columns surface as
	// ErrV2SchemaCorrupted so callers can stop and not fall back to legacy.
	if err := m.validateV2SchemaIntegrity(); err != nil {
		return fmt.Errorf("v2 schema integrity check failed: %w", err)
	}

	// Fix SQLite foreign key constraints that GORM's AutoMigrate may not handle correctly.
	// SQLite requires ON DELETE clauses to be defined at table creation time.
	if err := m.fixSQLiteForeignKeys(); err != nil {
		reportInitFailure("sqlite", "fixForeignKeys", err, m.dbPath)
		return fmt.Errorf("failed to fix foreign key constraints: %w", err)
	}

	// Initialize migration state singleton using FirstOrCreate to handle race conditions
	state := entities.MigrationState{ID: 1, State: entities.MigrationStatusIdle}
	if err := m.db.FirstOrCreate(&state, entities.MigrationState{ID: 1}).Error; err != nil {
		reportInitFailure("sqlite", "initMigrationState", err, m.dbPath)
		return fmt.Errorf("failed to initialize migration state: %w", err)
	}

	// Seed lookup tables
	if err := m.seedLookupTables(); err != nil {
		reportInitFailure("sqlite", "seedLookupTables", err, m.dbPath)
		return fmt.Errorf("failed to seed lookup tables: %w", err)
	}

	// Seed default AI model (BirdNET)
	if err := m.seedDefaultModel(); err != nil {
		reportInitFailure("sqlite", "seedDefaultModel", err, m.dbPath)
		return err
	}
	return nil
}

// renamePrePR2165Tables renames tables whose names changed when TableName() overrides
// were removed in PR #2165. Only two tables actually changed:
//   - migration_state → migration_states
//   - alert_history → alert_histories
//
// This is safe to call on fresh databases (no-op if old tables don't exist).
func (m *SQLiteManager) renamePrePR2165Tables() error {
	renames := [][2]string{
		{"migration_state", "migration_states"},
		{"alert_history", "alert_histories"},
	}
	for _, r := range renames {
		oldName, newName := r[0], r[1]
		var count int64
		if err := m.db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", oldName).Scan(&count).Error; err != nil || count == 0 {
			continue
		}
		// Only rename if the new table doesn't already exist
		var newCount int64
		if err := m.db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", newName).Scan(&newCount).Error; err == nil && newCount > 0 {
			continue
		}
		if err := m.db.Exec("ALTER TABLE `" + oldName + "` RENAME TO `" + newName + "`").Error; err != nil {
			reportInitFailure("sqlite", "renameTable_"+oldName, err, m.dbPath)
			return fmt.Errorf("failed to rename table %s to %s: %w", oldName, newName, err)
		}
	}
	return nil
}

// cleanupLegacySchemaContamination removes columns that were erroneously added to v2
// tables when the app fell back to legacy mode due to the PR #2165 table name mismatch.
// Legacy AutoMigrate added NOT NULL columns (e.g. scientific_name) that don't exist in
// the v2 entity definitions, causing INSERT failures.
//
// Only image_caches was affected: it comes before dynamic_thresholds in the legacy
// migration order, and dynamic_thresholds is where the crash occurred (stopping further
// contamination).
//
// Strategy:
//  1. Try ALTER TABLE DROP COLUMN (SQLite 3.35.0+).
//  2. If DROP COLUMN fails and the table is empty, DROP TABLE entirely — AutoMigrate
//     will recreate it with the correct schema.
//  3. If DROP COLUMN fails and the table has data, return ErrV2SchemaCorrupted so the
//     caller can decide how to handle it (e.g. self-healing or user notification).
func (m *SQLiteManager) cleanupLegacySchemaContamination() error {
	contaminations := []struct {
		table  string
		column string
	}{
		{"image_caches", "scientific_name"},
	}

	for _, c := range contaminations {
		var colCount int64
		if err := m.db.Raw("SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?", c.table, c.column).Scan(&colCount).Error; err != nil || colCount == 0 {
			continue
		}

		// Try DROP COLUMN first (SQLite 3.35.0+).
		if err := m.db.Exec("ALTER TABLE `" + c.table + "` DROP COLUMN `" + c.column + "`").Error; err != nil {
			// DROP COLUMN not available — check if table is empty.
			var rowCount int64
			if countErr := m.db.Raw("SELECT COUNT(*) FROM `" + c.table + "`").Scan(&rowCount).Error; countErr != nil {
				reportInitFailure("sqlite", "countRows_"+c.table, countErr, m.dbPath)
				return fmt.Errorf("%w: cannot count rows in %s: %w", ErrV2SchemaCorrupted, c.table, countErr)
			}

			if rowCount == 0 {
				// Table is empty — safe to drop and let AutoMigrate recreate it.
				if m.log != nil {
					m.log.Info("dropping empty table with orphaned column, AutoMigrate will recreate",
						logger.String("table", c.table),
						logger.String("column", c.column))
				}
				if dropErr := m.db.Exec("DROP TABLE IF EXISTS `" + c.table + "`").Error; dropErr != nil {
					reportInitFailure("sqlite", "dropTable_"+c.table, dropErr, m.dbPath)
					return fmt.Errorf("%w: failed to drop empty table %s: %w", ErrV2SchemaCorrupted, c.table, dropErr)
				}
			} else {
				// Table has data and DROP COLUMN is unavailable — cannot clean up safely.
				reportInitFailure("sqlite", "dropOrphanedColumn_"+c.table+"_"+c.column, err, m.dbPath)
				return fmt.Errorf("%w: cannot remove orphaned column %s.%s: table has %d rows and DROP COLUMN unavailable",
					ErrV2SchemaCorrupted, c.table, c.column, rowCount)
			}
		}

		events.Emit(context.Background(), "database", "schema_repair", "Legacy schema contamination cleaned", map[string]any{
			"action": "drop_legacy_columns",
			"table":  c.table,
			"column": c.column,
		})
	}

	return nil
}

// validateV2SchemaIntegrity checks all v2 tables for unexpected columns that
// indicate legacy schema contamination or schema evolution leftovers. For each table:
//   - If the table doesn't exist yet, skip (AutoMigrate will create it).
//   - If unexpected columns are found and the table is empty, drop it so
//     AutoMigrate can recreate it with the correct schema.
//   - If unexpected columns are found and the table has data, log a warning
//     but continue. Extra columns are harmless: GORM ignores them when querying.
//     This prevents the cascade failure from Discussion #3210 where users upgrading
//     from older builds were blocked by columns that existed in earlier entity versions.
//
// columnLister returns the actual column names for a given table.
type columnLister func(db *gorm.DB, tableName string) ([]string, error)

// sqliteColumnLister queries SQLite pragma_table_info for column names.
func sqliteColumnLister(db *gorm.DB, tableName string) ([]string, error) {
	type pragmaCol struct {
		Name string
	}
	var cols []pragmaCol
	if err := db.Raw("SELECT name FROM pragma_table_info(?)", tableName).Scan(&cols).Error; err != nil {
		return nil, err
	}
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	return names, nil
}

// mysqlColumnLister queries information_schema for column names.
func mysqlColumnLister(db *gorm.DB, tableName string) ([]string, error) {
	type colInfo struct {
		ColumnName string `gorm:"column:COLUMN_NAME"`
	}
	var cols []colInfo
	if err := db.Raw(
		"SELECT COLUMN_NAME FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ?",
		tableName,
	).Scan(&cols).Error; err != nil {
		return nil, err
	}
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.ColumnName
	}
	return names, nil
}

// missingColumnsEntry records the missing columns for a single table during validation.
type missingColumnsEntry struct {
	table   string
	columns []string
}

// validateSchemaIntegrity checks all v2 tables for column drift in either direction.
// It must run AFTER AutoMigrate; calling it before AutoMigrate on a database from an
// older version would misreport pending schema additions as corruption.
// For each table:
//   - If the table doesn't exist, skip. Post-AutoMigrate this should be impossible
//     on a healthy run, so the skip is defensive only; expected-but-missing tables
//     surface elsewhere via AutoMigrate's own error reporting.
//   - MISSING columns (expected by the entity but not present in the database)
//     indicate AutoMigrate silently failed to apply the new schema. These are real
//     corruption: GORM-generated INSERTs reference the column by name and will fail
//     with "table has no column named X". All missing columns are accumulated and
//     reported as ErrV2SchemaCorrupted so callers can surface the failure instead of
//     silently falling back.
//   - EXTRA columns (present in the database but not in the entity) are tolerated:
//     GORM ignores them on read/write. Per the additive-only rule from PR #3222,
//     extras are normal schema-evolution residue and must not block startup. Empty
//     tables with extras are still dropped so a fresh AutoMigrate run can recreate
//     them cleanly; populated tables are logged + reported to Sentry only.
func validateSchemaIntegrity(db *gorm.DB, log logger.Logger, dbType string, listColumns columnLister) error {
	migrator := db.Migrator()

	var missing []missingColumnsEntry

	for _, entity := range v2Entities() {
		if !migrator.HasTable(entity) {
			continue
		}

		stmt := &gorm.Statement{DB: db}
		if parseErr := stmt.Parse(entity); parseErr != nil {
			if log != nil {
				log.Warn("entity parse failed; cannot validate columns for this entity",
					logger.String("entity_type", fmt.Sprintf("%T", entity)),
					logger.Error(parseErr),
					logger.String("operation", "validate_schema_integrity"))
			}
			continue
		}
		tableName := stmt.Schema.Table

		expectedNames := make(map[string]bool, len(stmt.Schema.DBNames))
		for _, dbName := range stmt.Schema.DBNames {
			expectedNames[dbName] = true
		}

		actualCols, err := listColumns(db, tableName)
		if err != nil {
			// Listing columns is the gatekeeper for missing-column detection. If it
			// fails we cannot prove the schema is healthy, so log loudly so the
			// failure shows up in support dumps instead of silently degrading the
			// post-AutoMigrate guarantee. Continue rather than fail closed: a single
			// transient SQLite/MySQL hiccup must not block startup.
			if log != nil {
				log.Warn("column listing failed; skipping schema validation for this table",
					logger.String("table", tableName),
					logger.Error(err),
					logger.String("operation", "validate_schema_integrity"))
			}
			continue
		}

		actualNames := make(map[string]bool, len(actualCols))
		unexpected := make([]string, 0, len(actualCols))
		for _, colName := range actualCols {
			actualNames[colName] = true
			if !expectedNames[colName] {
				unexpected = append(unexpected, colName)
			}
		}

		missingCols := make([]string, 0, len(stmt.Schema.DBNames))
		for _, dbName := range stmt.Schema.DBNames {
			if !actualNames[dbName] {
				missingCols = append(missingCols, dbName)
			}
		}

		if len(missingCols) > 0 {
			missing = append(missing, missingColumnsEntry{table: tableName, columns: missingCols})
			if log != nil {
				log.Error("table is missing expected columns; GORM writes will fail",
					logger.String("table", tableName),
					logger.Any("missing_columns", missingCols),
					logger.String("db_type", dbType),
					logger.String("operation", "validate_schema_integrity"))
			}
			// Skip the extras-handling branch below: a table with missing columns
			// must not be dropped or downgraded to a warning.
			continue
		}

		if len(unexpected) == 0 {
			continue
		}

		// Check if table is empty before dropping to avoid data loss
		var rowCount int64
		if countErr := db.Raw("SELECT COUNT(*) FROM `" + tableName + "`").Scan(&rowCount).Error; countErr != nil {
			if log != nil {
				log.Warn("skipping schema validation for table due to query error",
					logger.String("table", tableName),
					logger.Error(countErr),
					logger.String("operation", "validate_schema_integrity"))
			}
			continue
		}

		if rowCount == 0 {
			if dropErr := db.Exec("DROP TABLE IF EXISTS `" + tableName + "`").Error; dropErr != nil {
				return fmt.Errorf("%w: failed to drop contaminated table %s: %w",
					ErrV2SchemaCorrupted, tableName, dropErr)
			}
			if log != nil {
				log.Info("dropped empty contaminated table for recreation",
					logger.String("table", tableName),
					logger.Any("unexpected_columns", unexpected),
					logger.String("operation", "validate_schema_integrity"))
			}
			continue
		}

		// Populated table with extra columns: warn but continue.
		// Extra columns from schema evolution are harmless; GORM ignores them.
		if log != nil {
			log.Warn("table has extra columns from schema evolution, continuing",
				logger.String("table", tableName),
				logger.Any("unexpected_columns", unexpected),
				logger.Int64("row_count", rowCount),
				logger.String("operation", "validate_schema_integrity"))
		}

		reportSchemaEvolution(dbType, tableName, unexpected, rowCount)
	}

	if len(missing) > 0 {
		reportMissingColumns(dbType, missing)
		return fmt.Errorf("%w: %s", ErrV2SchemaCorrupted, formatMissingColumns(missing))
	}
	return nil
}

// formatMissingColumns renders accumulated missing-column entries into a deterministic
// message suitable for logs and error chains. Tables and columns appear in input order
// to keep the output stable when there is a single source of truth (v2Entities()).
func formatMissingColumns(missing []missingColumnsEntry) string {
	parts := make([]string, 0, len(missing))
	for _, m := range missing {
		parts = append(parts, fmt.Sprintf("%s missing columns [%s]", m.table, strings.Join(m.columns, ", ")))
	}
	return "AutoMigrate did not apply: " + strings.Join(parts, "; ")
}

// validateV2SchemaIntegrity delegates to the shared validation with SQLite column listing.
func (m *SQLiteManager) validateV2SchemaIntegrity() error {
	return validateSchemaIntegrity(m.db, m.log, "sqlite", sqliteColumnLister)
}

// fixSQLiteForeignKeys ensures ON DELETE SET NULL behavior for SQLite.
// GORM's AutoMigrate doesn't always correctly apply ON DELETE clauses for SQLite.
// We use a trigger-based approach which is idempotent and doesn't conflict with GORM.
func (m *SQLiteManager) fixSQLiteForeignKeys() error {
	// Create a trigger to implement ON DELETE SET NULL for source_id.
	// This is more reliable than table recreation and works with GORM's AutoMigrate.
	// The trigger fires BEFORE DELETE and sets source_id to NULL for affected detections.
	triggerSQL := `
		CREATE TRIGGER IF NOT EXISTS trg_audio_source_delete_set_null
		BEFORE DELETE ON audio_sources
		FOR EACH ROW
		BEGIN
			UPDATE detections SET source_id = NULL WHERE source_id = OLD.id;
		END
	`
	return m.db.Exec(triggerSQL).Error
}

// seedLookupTables seeds the label_types and taxonomic_classes tables with default values.
func (m *SQLiteManager) seedLookupTables() error {
	return seedLookupTablesDB(m.db)
}

// seedDefaultModel ensures the default BirdNET model exists in the registry.
func (m *SQLiteManager) seedDefaultModel() error {
	return seedDefaultModelDB(m.db)
}

// seedLookupTablesDB seeds the label_types and taxonomic_classes tables with default values.
// Shared implementation used by both SQLiteManager and MySQLManager.
func seedLookupTablesDB(db *gorm.DB) error {
	for _, lt := range entities.DefaultLabelTypes() {
		if err := db.Where("name = ?", lt.Name).FirstOrCreate(&lt).Error; err != nil {
			return fmt.Errorf("failed to seed label type %q: %w", lt.Name, err)
		}
	}

	for _, tc := range entities.DefaultTaxonomicClasses() {
		if err := db.Where("name = ?", tc.Name).FirstOrCreate(&tc).Error; err != nil {
			return fmt.Errorf("failed to seed taxonomic class %q: %w", tc.Name, err)
		}
	}

	return nil
}

// seedDefaultModelDB ensures the default BirdNET model exists in the registry.
// Shared implementation used by both SQLiteManager and MySQLManager.
func seedDefaultModelDB(db *gorm.DB) error {
	model := entities.AIModel{
		Name:      detection.DefaultModelName,
		Version:   detection.DefaultModelVersion,
		Variant:   detection.DefaultModelVariant,
		ModelType: entities.ModelTypeBird,
	}
	result := db.Where("name = ? AND version = ? AND variant = ?", model.Name, model.Version, model.Variant).FirstOrCreate(&model)
	if result.Error != nil {
		return fmt.Errorf("failed to seed default model: %w", result.Error)
	}
	return nil
}

// DB returns the underlying GORM database.
func (m *SQLiteManager) DB() *gorm.DB {
	return m.db
}

// Path returns the database file path.
func (m *SQLiteManager) Path() string {
	return m.dbPath
}

// Close closes the database connection.
// Stops the periodic WAL checkpoint goroutine if running.
func (m *SQLiteManager) Close() error {
	m.StopPeriodicCheckpoint()

	if m.db == nil {
		return nil
	}

	sqlDB, err := m.db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying database: %w", err)
	}
	err = sqlDB.Close()
	m.db = nil
	return err
}

// CheckpointWAL forces a checkpoint of the Write-Ahead Log to ensure all changes
// are written to the main database file. This is important for graceful shutdown
// to prevent data loss and clean up WAL/SHM files.
func (m *SQLiteManager) CheckpointWAL() error {
	if m.db == nil {
		return fmt.Errorf("database connection is nil")
	}

	// PRAGMA wal_checkpoint(TRUNCATE) will:
	// 1. Copy all frames from WAL to the database file
	// 2. Truncate the WAL file to zero bytes
	// 3. Ensure all changes are persisted
	if err := m.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error; err != nil {
		return fmt.Errorf("WAL checkpoint failed: %w", err)
	}

	return nil
}

// StartPeriodicCheckpoint starts a background goroutine that runs a passive
// WAL checkpoint every walCheckpointInterval. This prevents unbounded WAL
// growth that can occur when SQLite's per-connection auto-checkpoint doesn't
// fire reliably with connection pooling.
//
// Use PASSIVE mode (not TRUNCATE) for the periodic checkpoint because it
// never blocks writers — it only checkpoints pages that are not in active
// use. The TRUNCATE mode used in CheckpointWAL() is reserved for shutdown
// where we want to fully clean up the WAL file.
func (m *SQLiteManager) StartPeriodicCheckpoint() {
	m.walMu.Lock()
	// Guard against double-start — would leak the previous goroutine.
	if m.walCancel != nil {
		m.walMu.Unlock()
		return
	}
	m.walCtx, m.walCancel = context.WithCancel(context.Background())
	m.walMu.Unlock()

	m.walWg.Go(func() {
		ticker := time.NewTicker(walCheckpointInterval)
		defer ticker.Stop()
		for {
			select {
			case <-m.walCtx.Done():
				return
			case <-ticker.C:
				if err := m.db.WithContext(m.walCtx).Exec("PRAGMA wal_checkpoint(PASSIVE)").Error; err != nil {
					// Suppress expected context.Canceled errors during shutdown
					if m.log != nil && !errors.Is(err, context.Canceled) {
						m.log.Warn("periodic WAL checkpoint failed",
							logger.Error(err),
							logger.String("operation", "periodic_wal_checkpoint"))
					}
				}
			}
		}
	})

	if m.log != nil {
		m.log.Debug("started periodic WAL checkpoint",
			logger.String("interval", walCheckpointInterval.String()),
			logger.String("mode", "PASSIVE"))
	}
}

// StopPeriodicCheckpoint stops the background WAL checkpoint goroutine.
func (m *SQLiteManager) StopPeriodicCheckpoint() {
	m.walMu.Lock()
	cancel := m.walCancel
	m.walCancel = nil
	m.walMu.Unlock()

	if cancel != nil {
		cancel()
		m.walWg.Wait()
	}
}

// Delete removes the v2 database file.
// This should only be called during rollback or cleanup.
func (m *SQLiteManager) Delete() error {
	if err := m.Close(); err != nil {
		return fmt.Errorf("failed to close database before deletion: %w", err)
	}

	// Remove main database file
	if err := os.Remove(m.dbPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete database file: %w", err)
	}

	// Also remove WAL and SHM files if they exist.
	// Errors are ignored since these files may not exist and cleanup
	// failures for auxiliary files shouldn't block database deletion.
	for _, suffix := range []string{"-wal", "-shm"} {
		walPath := m.dbPath + suffix
		_ = os.Remove(walPath)
	}

	return nil
}

// Exists checks if the v2 database file exists.
func (m *SQLiteManager) Exists() bool {
	_, err := os.Stat(m.dbPath)
	return err == nil
}

// IsMySQL returns false for SQLite manager.
func (m *SQLiteManager) IsMySQL() bool {
	return false
}

// TablePrefix returns the table prefix for v2 tables.
// For SQLite, this is empty since we use a separate database file.
// For MySQL, this would return "v2_".
func (m *SQLiteManager) TablePrefix() string {
	return ""
}

// ExistsFromPath checks if a v2 migration database exists for the given configured path.
// This derives the v2 migration path and checks if that file exists.
// This is a helper function for detecting database state before creating a manager.
func ExistsFromPath(configuredPath string) bool {
	v2Path := V2MigrationPathFromConfigured(configuredPath)
	_, err := os.Stat(v2Path)
	return err == nil
}

// ExistsFromDataDir checks if a v2 migration database exists at the given data directory.
// This is a backwards-compatible helper that assumes configured path is DataDir/birdnet.db.
//
// Deprecated: Use ExistsFromPath with the actual configured path instead.
func ExistsFromDataDir(dataDir string) bool {
	configuredPath := filepath.Join(dataDir, "birdnet.db")
	return ExistsFromPath(configuredPath)
}

// GetDataDirFromLegacyPath extracts the data directory from a legacy database path.
// For example, "/data/birdnet.db" -> "/data"
func GetDataDirFromLegacyPath(legacyPath string) string {
	// Handle in-memory database for testing
	if legacyPath == ":memory:" || strings.HasPrefix(legacyPath, "file::memory:") {
		return ""
	}
	return filepath.Dir(legacyPath)
}
