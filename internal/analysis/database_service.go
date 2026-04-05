package analysis

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	apiv2 "github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/app"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2only"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/telemetry"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gorm_logger "gorm.io/gorm/logger"
)

// databaseServiceName is the service name used for logging and diagnostics.
const databaseServiceName = "database"

// DatabaseService manages the database lifecycle as an app.Service with TierCore shutdown.
// It owns both the primary datastore (v1) and the optional v2 database manager.
type DatabaseService struct {
	settings   *conf.Settings
	metrics    *observability.Metrics
	dataStore  datastore.Interface
	v2Manager  datastoreV2.Manager
	v2OnlyMode bool
}

// NewDatabaseService creates a new DatabaseService with the given settings and metrics.
// The service is not started; call Start() to initialize the databases.
func NewDatabaseService(settings *conf.Settings, metrics *observability.Metrics) *DatabaseService {
	return &DatabaseService{
		settings: settings,
		metrics:  metrics,
	}
}

// Name returns a human-readable identifier for logging and diagnostics.
func (d *DatabaseService) Name() string {
	return databaseServiceName
}

// ShutdownTier returns app.TierCore so the database is shut down last
// with a guaranteed independent timeout budget.
func (d *DatabaseService) ShutdownTier() app.ShutdownTier {
	return app.TierCore
}

// DataStore returns the primary datastore, or nil if not yet started.
func (d *DatabaseService) DataStore() datastore.Interface {
	return d.dataStore
}

// V2Manager returns the v2 database manager, or nil if not yet started.
func (d *DatabaseService) V2Manager() datastoreV2.Manager {
	return d.v2Manager
}

// IsV2OnlyMode returns whether the database is operating in v2-only mode.
func (d *DatabaseService) IsV2OnlyMode() bool {
	return d.v2OnlyMode
}

// Start initializes and opens the database connections.
// It handles three startup paths: v2-only (post-migration), fresh install, and legacy mode.
// Migration infrastructure is initialized when not in v2-only mode.
//
//nolint:gocognit,gocyclo // Database initialization requires handling multiple startup paths with detailed logging
func (d *DatabaseService) Start(_ context.Context) error {
	settings := d.settings

	// If Start fails after opening the database, clean up to prevent resource leaks.
	// The App framework only calls Stop() on services that started successfully ([:i]),
	// so the failing service must clean up after itself.
	startSucceeded := false
	defer func() {
		if !startSucceeded {
			log := GetLogger()
			d.closeV2Database(log)
			d.closeDataStore(log)
		}
	}()

	// Check for unmigrated legacy records from a potential hard crash during tail sync.
	// Must run BEFORE consolidation to prevent renaming the legacy DB while it still
	// has unmigrated records that the worker needs to sync.
	datastoreLog := logger.Global().Module("datastore")
	hasUnmigrated := datastoreV2.HasUnmigratedLegacyRecords(settings, datastoreLog)

	// Check for and perform database consolidation if needed (SQLite only)
	// Skip consolidation when unmigrated records found — let the worker tail-sync them first
	if settings.Output.SQLite.Enabled && !hasUnmigrated {
		consolidated, err := datastoreV2.CheckAndConsolidateAtStartup(settings.Output.SQLite.Path, datastoreLog)
		if err != nil {
			datastoreLog.Error("database consolidation failed", logger.Error(err))
			_ = errors.New(err).
				Component("analysis.database").
				Category(errors.CategoryDatabase).
				Context("operation", "database_consolidation").
				Build()
			// Continue with normal startup - consolidation can be retried
		} else if consolidated {
			datastoreLog.Info("database consolidation completed, continuing startup")
		}
	} else if hasUnmigrated {
		datastoreLog.Info("deferring database consolidation until unmigrated records are synced")
	}

	// Check migration state before initializing database
	// This allows us to skip the legacy database when migration is complete
	startupState := datastoreV2.CheckMigrationStateBeforeStartup(settings)
	d.v2OnlyMode = startupState.MigrationStatus == entities.MigrationStatusCompleted && startupState.V2Available
	freshInstall := startupState.FreshInstall

	// Override v2-only mode when unmigrated legacy records were found. This forces
	// the system to start with the legacy DB so the worker can tail-sync the stragglers.
	// Consolidation will complete on the next clean restart.
	if hasUnmigrated && d.v2OnlyMode {
		datastoreLog.Warn("deferring v2-only mode: unmigrated legacy records found, will sync via tail sync",
			logger.String("operation", "startup_reconciliation"))
		d.v2OnlyMode = false
		startupState.LegacyRequired = true
	}

	// Log startup mode detection
	switch {
	case d.v2OnlyMode:
		datastoreLog.Info("migration completed, starting in enhanced database mode",
			logger.String("migration_status", string(startupState.MigrationStatus)),
			logger.String("operation", "startup_mode_check"))
	case freshInstall:
		GetLogger().Info("fresh installation detected, initializing v2 schema",
			logger.String("database_path", settings.Output.SQLite.Path),
			logger.String("operation", "startup_mode_check"))
	default:
		GetLogger().Debug("migration state check completed",
			logger.String("migration_status", string(startupState.MigrationStatus)),
			logger.Bool("v2_available", startupState.V2Available),
			logger.Bool("legacy_required", startupState.LegacyRequired),
			logger.String("operation", "startup_mode_check"))
	}

	// Initialize database access based on startup state
	var v2OnlyDatastore *v2only.Datastore

	switch {
	case d.v2OnlyMode:
		// Post-migration: use birdnet_v2.db with V2OnlyDatastore
		var err error
		v2OnlyDatastore, err = initializeV2OnlyMode(settings)
		if err != nil {
			// Enhanced database mode failed, fall back to legacy startup
			datastoreLog.Warn("enhanced database mode initialization failed, falling back to legacy mode",
				logger.Error(err),
				logger.String("operation", "initialize_enhanced_database_mode"))
			d.dataStore = datastore.New(settings)
			d.v2OnlyMode = false
		} else {
			d.dataStore = v2OnlyDatastore
			// Set global enhanced database flag
			datastoreV2.SetEnhancedDatabaseMode()
			// Notify the API layer that we're in v2-only mode
			apiv2.SetV2OnlyMode()
			// Set the v2 database manager
			d.v2Manager = v2OnlyDatastore.Manager()
		}

	case freshInstall:
		// Fresh install: create at configured path with v2 schema
		// Load eBird taxonomy for species code lookups in analytics endpoints.
		_, freshSciIndex, _ := classifier.LoadTaxonomyData("")
		var err error
		v2OnlyDatastore, err = v2only.InitializeFreshInstall(settings, GetLogger(), freshSciIndex)
		if err != nil {
			// Fresh install failed, fall back to legacy mode
			GetLogger().Warn("fresh install failed, falling back to legacy mode",
				logger.Error(err),
				logger.String("operation", "initialize_fresh_install"))
			d.dataStore = datastore.New(settings)
		} else {
			d.dataStore = v2OnlyDatastore
			// Fresh install is now effectively v2-only mode
			d.v2OnlyMode = true
			// Set global enhanced database flag
			datastoreV2.SetEnhancedDatabaseMode()
			// Notify the API layer that we're in v2-only mode
			apiv2.SetV2OnlyMode()
			// Set the v2 database manager
			d.v2Manager = v2OnlyDatastore.Manager()
		}

	default:
		// Legacy mode: use legacy datastore
		d.dataStore = datastore.New(settings)
	}

	// Connect metrics to datastore before opening
	d.dataStore.SetMetrics(d.metrics.Datastore)
	d.dataStore.SetSunCalcMetrics(d.metrics.SunCalc)

	// Only validate disk space and open for legacy mode (v2-only mode already opened)
	if !d.v2OnlyMode {
		// Validate disk space before attempting to open the database
		// This prevents startup failures due to insufficient disk space
		// ValidateStartupDiskSpace already returns a fully structured error, so we return it directly
		if err := datastore.ValidateStartupDiskSpace(settings.Output.SQLite.Path); err != nil {
			GetLogger().Error("disk space validation failed",
				logger.Error(err),
				logger.String("operation", "validate_startup_disk_space"))
			return err
		}

		// Open a connection to the database and handle possible errors.
		if err := d.dataStore.Open(); err != nil {
			GetLogger().Error("failed to open database",
				logger.Error(err),
				logger.String("operation", "open_database"))
			return err // Return error to stop execution if database connection fails.
		}
	}

	// Set datastore schema version as a Sentry tag for telemetry
	telemetry.SetDatastoreSchemaTag(d.dataStore.SchemaVersion())

	// Initialize v2 migration infrastructure only if not in enhanced database mode
	// In enhanced database mode, migration is already complete - no need for migration infrastructure
	if !d.v2OnlyMode {
		// This sets up the StateManager and Worker for the database migration API.
		// Store the returned manager so Stop() can close the v2 database connection.
		migrationManager, err := initializeMigrationInfrastructure(settings, d.dataStore)
		if err != nil {
			// Migration infrastructure is optional - log warning but continue
			GetLogger().Warn("migration infrastructure initialization failed",
				logger.Error(err),
				logger.String("operation", "initialize_migration_infrastructure"))
		} else {
			d.v2Manager = migrationManager
		}
	} else {
		datastoreLog.Debug("skipping migration infrastructure in enhanced database mode",
			logger.String("operation", "initialize_migration_infrastructure"))
	}

	startSucceeded = true
	return nil
}

// Stop gracefully shuts down the database connections with WAL checkpoints.
// It stops the migration worker, closes the v2 database, then closes the primary datastore.
func (d *DatabaseService) Stop(_ context.Context) error {
	log := GetLogger()

	// Stop the migration worker before closing databases.
	apiv2.StopMigrationWorker()

	// Close v2 database (only in migration mode, not v2-only).
	if !d.v2OnlyMode {
		d.closeV2Database(log)
	}

	// Close primary datastore.
	d.closeDataStore(log)

	return nil
}

// closeDataStore performs a WAL checkpoint and closes the primary datastore.
// Safe to call multiple times — nils out the reference after close.
func (d *DatabaseService) closeDataStore(log logger.Logger) {
	store := d.dataStore
	d.dataStore = nil
	if store == nil {
		return
	}

	// If this is an SQLite store, perform WAL checkpoint before closing.
	if sqliteStore, ok := store.(*datastore.SQLiteStore); ok {
		log.Info("performing SQLite WAL checkpoint",
			logger.String("operation", "wal_checkpoint_before_shutdown"))
		if err := sqliteStore.CheckpointWAL(); err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "database is closed") || strings.Contains(errStr, "nil pointer") {
				log.Warn("database already closed during WAL checkpoint",
					logger.String("operation", "wal_checkpoint"),
					logger.String("error_type", "database_closed"))
			} else {
				log.Warn("WAL checkpoint failed",
					logger.Error(err),
					logger.String("operation", "wal_checkpoint"),
					logger.Bool("continuing_shutdown", true))
			}
		}
	}

	if err := store.Close(); err != nil {
		log.Error("failed to close database",
			logger.Error(err),
			logger.String("operation", "close_database"))
	} else {
		log.Info("successfully closed database",
			logger.String("operation", "close_database"))
	}
}

// v2SchemaResetMarker is the filename written next to the v2 database after a
// self-healing deletion. If Initialize() fails again with ErrV2SchemaCorrupted
// and this file already exists, the self-healing loop is broken to prevent
// infinite crash-delete cycles.
const v2SchemaResetMarker = ".v2_schema_reset"

// initializeV2WithSelfHealing wraps v2 Manager.Initialize() with automatic
// recovery for empty, schema-corrupted databases. When Initialize() returns
// ErrV2SchemaCorrupted and the database contains no user data, the database
// file (plus WAL/SHM) is deleted so the caller can recreate the manager and
// retry. A marker file prevents infinite crash loops.
//
// Returns nil when the database was successfully deleted (caller must recreate
// the manager and call Initialize again), or the original/wrapped error when
// self-healing is not possible.
func initializeV2WithSelfHealing(manager datastoreV2.Manager, v2Path string, log logger.Logger) error {
	// Attempt normal initialization.
	if err := manager.Initialize(); err == nil {
		// Success — clean up any stale marker from a previous recovery.
		markerPath := filepath.Join(filepath.Dir(v2Path), v2SchemaResetMarker)
		if removeErr := os.Remove(markerPath); removeErr != nil && !os.IsNotExist(removeErr) {
			log.Warn("failed to remove stale schema reset marker",
				logger.Error(removeErr),
				logger.String("marker", markerPath))
		}
		return nil
	} else if !errors.Is(err, datastoreV2.ErrV2SchemaCorrupted) {
		// Not a schema corruption error — return as-is.
		return err
	} else {
		// Schema corrupted — attempt self-healing below.
		log.Warn("v2 schema corruption detected, evaluating self-healing",
			logger.Error(err),
			logger.String("path", v2Path))
	}

	// Guard: if the marker already exists, a previous reset did not help.
	markerPath := filepath.Join(filepath.Dir(v2Path), v2SchemaResetMarker)
	if _, statErr := os.Stat(markerPath); statErr == nil {
		log.Error("v2 schema reset marker already exists, aborting self-healing to prevent crash loop",
			logger.String("marker", markerPath))
		return fmt.Errorf("v2 schema still corrupted after previous reset (marker %s exists): %w",
			v2SchemaResetMarker, datastoreV2.ErrV2SchemaCorrupted)
	}

	// Guard: only delete if the database has no user data.
	if !isV2DatabaseSafeToDelete(v2Path, log) {
		log.Error("v2 database contains user data, cannot auto-delete",
			logger.String("path", v2Path))
		return fmt.Errorf("v2 schema corrupted but database contains user data, manual cleanup required: %w",
			datastoreV2.ErrV2SchemaCorrupted)
	}

	// Write the marker BEFORE deleting so a crash mid-delete still prevents loops.
	markerContent := fmt.Sprintf("reset at %s\n", time.Now().UTC().Format(time.RFC3339))
	if writeErr := os.WriteFile(markerPath, []byte(markerContent), 0o600); writeErr != nil {
		log.Error("failed to write schema reset marker",
			logger.Error(writeErr),
			logger.String("marker", markerPath))
		return fmt.Errorf("cannot write reset marker: %w", writeErr)
	}

	// Close the manager before deleting the file.
	if closeErr := manager.Close(); closeErr != nil {
		log.Warn("failed to close v2 manager before self-healing delete",
			logger.Error(closeErr))
	}

	// Delete the database file and its WAL/SHM companions.
	// The main database file MUST be removed — if it fails, return an error
	// because the manager is already closed and the caller would proceed
	// with a closed manager.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		path := v2Path + suffix
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			if suffix == "" {
				// Main database file removal failed — this is fatal for self-healing
				return fmt.Errorf("failed to remove corrupt v2 database file %s: %w", path, removeErr)
			}
			log.Warn("failed to remove v2 companion file during self-healing",
				logger.Error(removeErr),
				logger.String("path", path))
		}
	}

	log.Info("deleted corrupted empty v2 database for recreation",
		logger.String("path", v2Path),
		logger.String("operation", "self_healing_schema_reset"))

	return nil
}

// isV2DatabaseSafeToDelete opens the v2 database read-only and checks whether
// it contains any user data. Returns true only when all user-facing tables are
// empty and no migration is in progress.
func isV2DatabaseSafeToDelete(dbPath string, log logger.Logger) bool {
	// Open database in read-only mode.
	dsn := dbPath + "?mode=ro&_journal_mode=WAL&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gorm_logger.Default.LogMode(gorm_logger.Silent),
	})
	if err != nil {
		log.Warn("cannot open v2 database for safety check",
			logger.Error(err),
			logger.String("path", dbPath))
		return false
	}
	defer func() {
		if sqlDB, dbErr := db.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	}()

	// Check user-data tables: if ANY has rows, the database is not safe to delete.
	userTables := []string{"detections", "alert_rules", "detection_reviews", "dynamic_thresholds"}
	for _, table := range userTables {
		var exists int64
		// Check if the table exists before querying it.
		if err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&exists).Error; err != nil || exists == 0 {
			continue // Table doesn't exist — OK.
		}
		var rowCount int64
		if err := db.Raw("SELECT COUNT(*) FROM `" + table + "`").Scan(&rowCount).Error; err != nil {
			log.Warn("failed to count rows in table during safety check",
				logger.Error(err),
				logger.String("table", table))
			return false // Cannot confirm empty — be safe.
		}
		if rowCount > 0 {
			log.Warn("v2 database has user data, not safe to delete",
				logger.String("table", table),
				logger.Int64("rows", rowCount))
			return false
		}
	}

	// Check migration state: if a migration is active, do not delete.
	// Check both current (migration_states) and pre-PR#2165 (migration_state) table names
	// in case corruption happened before the rename could run.
	var migrationTableName string
	if err := db.Raw("SELECT name FROM sqlite_master WHERE type='table' AND name IN ('migration_states', 'migration_state') LIMIT 1").Scan(&migrationTableName).Error; err == nil && migrationTableName != "" {
		var state string
		if err := db.Raw("SELECT state FROM `" + migrationTableName + "` WHERE id = 1").Scan(&state).Error; err == nil {
			unsafeStates := map[string]bool{
				string(entities.MigrationStatusMigrating):  true,
				string(entities.MigrationStatusCompleted):  true,
				string(entities.MigrationStatusDualWrite):  true,
				string(entities.MigrationStatusValidating): true,
				string(entities.MigrationStatusCutover):    true,
				string(entities.MigrationStatusPaused):     true,
			}
			if unsafeStates[state] {
				log.Warn("v2 database has active migration state, not safe to delete",
					logger.String("migration_state", state))
				return false
			}
		}
	}

	return true
}

// closeV2Database performs a WAL checkpoint and closes the v2 database.
// Safe to call multiple times — nils out the reference after close.
func (d *DatabaseService) closeV2Database(log logger.Logger) {
	manager := d.v2Manager
	d.v2Manager = nil
	if manager == nil {
		return
	}

	// Determine database type for logging.
	dbType := "SQLite"
	if manager.IsMySQL() {
		dbType = "MySQL"
	}

	// Stash path before close (may not be available after).
	dbPath := manager.Path()

	// Perform WAL checkpoint before closing (SQLite only, no-op for MySQL).
	if !manager.IsMySQL() {
		log.Info("performing v2 SQLite WAL checkpoint",
			logger.String("operation", "v2_wal_checkpoint_before_shutdown"))

		if err := manager.CheckpointWAL(); err != nil {
			log.Warn("v2 WAL checkpoint failed",
				logger.Error(err),
				logger.String("operation", "v2_wal_checkpoint"))
		}
	}

	log.Info("closing v2 database",
		logger.String("type", dbType),
		logger.String("path", dbPath),
		logger.String("operation", "v2_database_close"))

	if err := manager.Close(); err != nil {
		log.Error("failed to close v2 database",
			logger.Error(err),
			logger.String("type", dbType),
			logger.String("operation", "v2_database_close"))
	} else {
		log.Info("v2 database closed successfully",
			logger.String("type", dbType),
			logger.String("path", dbPath))
	}
}
