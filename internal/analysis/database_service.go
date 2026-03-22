package analysis

import (
	"context"
	"strings"

	apiv2 "github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/app"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability"
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
// This is a placeholder that will be filled when code is extracted from the monolith.
func (d *DatabaseService) Start(_ context.Context) error {
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
