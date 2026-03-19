// Package v2 provides the v2 normalized database implementation.
package v2

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
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

// SQLiteManager handles the v2 normalized database for SQLite.
type SQLiteManager struct {
	db     *gorm.DB
	dbPath string
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

	// Build DSN with recommended SQLite pragmas
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=%d&_foreign_keys=ON", dbPath, sqliteBusyTimeoutMs)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open v2 database: %w", err)
	}

	return &SQLiteManager{
		db:     db,
		dbPath: dbPath,
	}, nil
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
	m.cleanupLegacySchemaContamination()

	// Run GORM auto-migrations for all entities
	err := m.db.AutoMigrate(
		// Lookup tables (must be created first due to FK constraints)
		&entities.LabelType{},
		&entities.TaxonomicClass{},
		// Core detection entities
		&entities.AIModel{},
		&entities.Label{},
		&entities.AudioSource{},
		&entities.Detection{},
		&entities.DetectionPrediction{},
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
	)
	if err != nil {
		reportInitFailure("sqlite", "AutoMigrate", err, m.dbPath)
		return fmt.Errorf("failed to migrate v2 schema: %w", err)
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
// Requires SQLite 3.35.0+ (ALTER TABLE DROP COLUMN). Safe no-op on older versions.
func (m *SQLiteManager) cleanupLegacySchemaContamination() {
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
		if err := m.db.Exec("ALTER TABLE `" + c.table + "` DROP COLUMN `" + c.column + "`").Error; err != nil {
			reportInitFailure("sqlite", "dropOrphanedColumn_"+c.table+"_"+c.column, err, m.dbPath)
		}
	}
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
func (m *SQLiteManager) Close() error {
	sqlDB, err := m.db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying database: %w", err)
	}
	return sqlDB.Close()
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
