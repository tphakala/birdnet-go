package analysis

import (
	"context"
	"time"

	apiv2 "github.com/tphakala/birdnet-go/internal/api/v2"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/migration"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/datastore/v2only"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// migrationSetupConfig holds configuration for migration infrastructure setup.
type migrationSetupConfig struct {
	manager     datastoreV2.Manager // Satisfies both SQLite and MySQL managers
	ds          datastore.Interface
	log         logger.Logger
	useV2Prefix bool   // true for MySQL migration (v2_ prefix), false for SQLite (separate file)
	opName      string // For log messages: "initialize_migration_infrastructure" or "initialize_mysql_migration"
}

// setupMigrationWorker performs the common setup after manager creation and initialization.
// It creates repositories, the migration worker, stores the manager for cleanup, and handles state recovery.
// The caller should close the manager if this function returns an error.
func setupMigrationWorker(cfg *migrationSetupConfig) error {
	// Get dialect from manager interface (avoids redundant parameter)
	isMySQL := cfg.manager.IsMySQL()

	// Create the state manager
	stateManager := datastoreV2.NewStateManager(cfg.manager.DB())

	// Create repositories for the migration worker
	v2DB := cfg.manager.DB()

	// Look up required lookup table IDs (seeded during Manager.Initialize())
	var speciesLabelType entities.LabelType
	if err := v2DB.Where("name = ?", "species").FirstOrCreate(&speciesLabelType, entities.LabelType{Name: "species"}).Error; err != nil {
		return errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "get_species_label_type").
			Build()
	}

	var avesClass entities.TaxonomicClass
	if err := v2DB.Where("name = ?", "Aves").FirstOrCreate(&avesClass, entities.TaxonomicClass{Name: "Aves"}).Error; err != nil {
		return errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "get_aves_taxonomic_class").
			Build()
	}

	// Get default model for related data migration (uses detection package constants)
	var defaultModel entities.AIModel
	if err := v2DB.Where("name = ? AND version = ? AND variant = ?",
		detection.DefaultModelName, detection.DefaultModelVersion, detection.DefaultModelVariant).
		FirstOrCreate(&defaultModel, entities.AIModel{
			Name:      detection.DefaultModelName,
			Version:   detection.DefaultModelVersion,
			Variant:   detection.DefaultModelVariant,
			ModelType: entities.ModelTypeBird,
		}).Error; err != nil {
		return errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "get_default_model").
			Build()
	}

	labelRepo := repository.NewLabelRepository(v2DB, cfg.useV2Prefix, isMySQL)
	modelRepo := repository.NewModelRepository(v2DB, cfg.useV2Prefix, isMySQL)
	sourceRepo := repository.NewAudioSourceRepository(v2DB, cfg.useV2Prefix, isMySQL)
	v2DetectionRepo := repository.NewDetectionRepository(v2DB, cfg.useV2Prefix, isMySQL)

	// Create repositories for auxiliary data migration
	weatherRepo := repository.NewWeatherRepository(v2DB, cfg.useV2Prefix, isMySQL)
	imageCacheRepo := repository.NewImageCacheRepository(v2DB, labelRepo, cfg.useV2Prefix, isMySQL)
	thresholdRepo := repository.NewDynamicThresholdRepository(v2DB, labelRepo, cfg.useV2Prefix, isMySQL)
	notificationRepo := repository.NewNotificationHistoryRepository(v2DB, labelRepo, cfg.useV2Prefix, isMySQL)

	// Create the legacy detection repository
	legacyRepo := datastore.NewDetectionRepository(cfg.ds, time.Local)

	// Determine batch size and sleep duration based on database type
	// MySQL handles larger batches and concurrent access better than SQLite
	batchSize := migration.DefaultBatchSize
	sleepBetweenBatches := migration.DefaultSleepBetweenBatches
	if isMySQL {
		batchSize = migration.MySQLBatchSize
		sleepBetweenBatches = migration.MySQLSleepBetweenBatches
	}

	// Use datastore logger for migration components (not analysis logger)
	migrationLogger := datastore.GetLogger()

	// Create the related data migrator for reviews, comments, locks, predictions
	// Use half of detection batch size since related data tables are typically smaller
	relatedDataBatchSize := batchSize / 2
	relatedMigrator := migration.NewRelatedDataMigrator(&migration.RelatedDataMigratorConfig{
		LegacyStore:        cfg.ds,
		DetectionRepo:      v2DetectionRepo,
		LabelRepo:          labelRepo,
		StateManager:       stateManager,
		Logger:             migrationLogger,
		BatchSize:          relatedDataBatchSize,
		DefaultModelID:     defaultModel.ID,
		SpeciesLabelTypeID: speciesLabelType.ID,
		AvesClassID:        &avesClass.ID,
	})

	// Create the auxiliary data migrator for weather, thresholds, image cache, notifications
	auxiliaryMigrator := migration.NewAuxiliaryMigrator(&migration.AuxiliaryMigratorConfig{
		LegacyStore:        cfg.ds,
		LabelRepo:          labelRepo,
		WeatherRepo:        weatherRepo,
		ImageCacheRepo:     imageCacheRepo,
		ThresholdRepo:      thresholdRepo,
		NotificationRepo:   notificationRepo,
		Logger:             migrationLogger,
		DefaultModelID:     defaultModel.ID,
		SpeciesLabelTypeID: speciesLabelType.ID,
		AvesClassID:        &avesClass.ID,
	})

	// Determine database type for telemetry
	dbType := "sqlite"
	if isMySQL {
		dbType = "mysql"
	}
	migrationTelemetry := migration.NewMigrationTelemetry(dbType)

	// Create the migration worker
	worker, err := migration.NewWorker(&migration.WorkerConfig{
		Legacy:              legacyRepo,
		V2Detection:         v2DetectionRepo,
		LabelRepo:           labelRepo,
		ModelRepo:           modelRepo,
		SourceRepo:          sourceRepo,
		StateManager:        stateManager,
		RelatedMigrator:     relatedMigrator,
		AuxiliaryMigrator:   auxiliaryMigrator,
		Logger:              migrationLogger,
		BatchSize:           batchSize,
		SleepBetweenBatches: sleepBetweenBatches,
		Timezone:            time.Local,
		UseBatchMode:        isMySQL, // Use efficient batch inserts for MySQL
		SpeciesLabelTypeID:  speciesLabelType.ID,
		AvesClassID:         &avesClass.ID,
		Telemetry:           migrationTelemetry,
	})
	if err != nil {
		return errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "create_migration_worker").
			Build()
	}

	// Inject dependencies into the API layer
	apiv2.SetMigrationDependencies(stateManager, worker)
	apiv2.SetMigrationTelemetry(migrationTelemetry)

	// Check for state recovery - resume migration if it was in progress
	state, err := stateManager.GetState()
	if err != nil {
		migrationLogger.Warn("failed to get migration state for recovery",
			logger.Error(err),
			logger.String("operation", cfg.opName))
	} else {
		migrationLogger.Info("migration infrastructure initialized",
			logger.String("state", string(state.State)),
			logger.Int64("migrated_records", state.MigratedRecords),
			logger.Int64("total_records", state.TotalRecords),
			logger.String("operation", cfg.opName))

		// Resume worker if migration was in progress, or if migration completed
		// but we're running in legacy mode due to unmigrated records from a crash.
		// In the COMPLETED case, the worker enters tail sync to drain stragglers.
		if state.State == entities.MigrationStatusDualWrite ||
			state.State == entities.MigrationStatusMigrating ||
			state.State == entities.MigrationStatusCompleted {
			migrationLogger.Info("resuming migration worker after restart",
				logger.String("state", string(state.State)),
				logger.String("operation", cfg.opName))
			// Create cancellable context for the worker - this allows graceful shutdown
			// to stop the worker by cancelling this context
			workerCtx, workerCancel := context.WithCancel(context.Background())
			apiv2.SetMigrationWorkerCancel(workerCancel)
			if startErr := worker.Start(workerCtx); startErr != nil {
				workerCancel() // Clean up on failure
				migrationLogger.Warn("failed to resume migration worker",
					logger.Error(startErr),
					logger.String("operation", cfg.opName))
			}
		}
	}

	return nil
}

// initializeMigrationInfrastructure sets up the v2 database migration infrastructure.
// This creates the StateManager and Worker instances needed for the migration API.
// The function handles state recovery on restart and resumes migration if it was in progress.
func initializeMigrationInfrastructure(settings *conf.Settings, ds datastore.Interface) (datastoreV2.Manager, error) {
	log := GetLogger()

	// Get the database directory from the legacy database path
	var dataDir string
	switch {
	case settings.Output.SQLite.Enabled:
		dataDir = datastoreV2.GetDataDirFromLegacyPath(settings.Output.SQLite.Path)
	case settings.Output.MySQL.Enabled:
		// MySQL uses v2_ prefixed tables in the same database
		mgr, err := initializeMySQLMigrationInfrastructure(settings, ds, log)
		return mgr, err
	default:
		log.Debug("no database configured, skipping migration infrastructure",
			logger.String("operation", "initialize_migration_infrastructure"))
		return nil, nil //nolint:nilnil // nil manager is valid when no database is configured
	}

	// Check if dataDir is empty (in-memory database)
	if dataDir == "" {
		log.Debug("in-memory database detected, skipping migration infrastructure",
			logger.String("operation", "initialize_migration_infrastructure"))
		return nil, nil //nolint:nilnil // nil manager is valid for in-memory databases
	}

	// Create v2 database manager
	// Use ConfiguredPath to properly derive v2 migration path from configured filename
	v2Manager, err := datastoreV2.NewSQLiteManager(datastoreV2.Config{
		ConfiguredPath: settings.Output.SQLite.Path,
		Debug:          settings.Debug,
		Logger:         log,
	})
	if err != nil {
		return nil, errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "create_v2_database_manager").
			Build()
	}

	// Initialize the v2 database schema
	if err := v2Manager.Initialize(); err != nil {
		if closeErr := v2Manager.Close(); closeErr != nil {
			log.Warn("failed to close v2 manager after initialization failure",
				logger.Error(closeErr),
				logger.String("operation", "initialize_migration_infrastructure"))
		}
		return nil, errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "initialize_v2_database").
			Build()
	}

	// Setup the migration worker using the common helper
	if err := setupMigrationWorker(&migrationSetupConfig{
		manager:     v2Manager,
		ds:          ds,
		log:         log,
		useV2Prefix: false, // SQLite uses separate file, not prefixed tables
		opName:      "initialize_migration_infrastructure",
	}); err != nil {
		if closeErr := v2Manager.Close(); closeErr != nil {
			log.Warn("failed to close v2 manager after worker setup failure",
				logger.Error(closeErr),
				logger.String("operation", "initialize_migration_infrastructure"))
		}
		return nil, err
	}

	return v2Manager, nil
}

// initializeMySQLMigrationInfrastructure sets up migration infrastructure for MySQL.
// Unlike SQLite which uses a separate file, MySQL shares the same database.
// V2 tables use the v2_ prefix to avoid collisions with legacy auxiliary tables
// (e.g., dynamic_thresholds, image_caches) that share the same base names.
func initializeMySQLMigrationInfrastructure(settings *conf.Settings, ds datastore.Interface, log logger.Logger) (datastoreV2.Manager, error) {
	// Create v2 MySQL manager with v2_ prefix to avoid collisions with legacy
	// auxiliary tables that share the same base names. TableName() methods have
	// been removed so NamingStrategy.TablePrefix now takes effect.
	v2Manager, err := datastoreV2.NewMySQLManager(&datastoreV2.MySQLConfig{
		Host:        settings.Output.MySQL.Host,
		Port:        settings.Output.MySQL.Port,
		Username:    settings.Output.MySQL.Username,
		Password:    settings.Output.MySQL.Password,
		Database:    settings.Output.MySQL.Database,
		UseV2Prefix: true, // v2_ prefix avoids collisions with legacy auxiliary tables
		Debug:       settings.Debug,
	})
	if err != nil {
		return nil, errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "create_mysql_v2_manager").
			Build()
	}

	// Initialize the v2 schema (creates tables with v2_ prefix)
	if err := v2Manager.Initialize(); err != nil {
		if closeErr := v2Manager.Close(); closeErr != nil {
			log.Warn("failed to close MySQL v2 manager after initialization failure",
				logger.Error(closeErr),
				logger.String("operation", "initialize_mysql_migration"))
		}
		return nil, errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "initialize_mysql_v2_schema").
			Build()
	}

	// Setup the migration worker using the common helper.
	// useV2Prefix is true so the migration worker creates v2_ prefixed tables,
	// avoiding collisions with legacy auxiliary tables that share the same base names.
	if err := setupMigrationWorker(&migrationSetupConfig{
		manager:     v2Manager,
		ds:          ds,
		log:         log,
		useV2Prefix: true, // v2_ prefix avoids collisions with legacy auxiliary tables
		opName:      "initialize_mysql_migration",
	}); err != nil {
		if closeErr := v2Manager.Close(); closeErr != nil {
			log.Warn("failed to close MySQL v2 manager after worker setup failure",
				logger.Error(closeErr),
				logger.String("operation", "initialize_mysql_migration"))
		}
		return nil, err
	}

	return v2Manager, nil
}

// initializeV2OnlyMode creates a V2OnlyDatastore when migration is complete.
// This allows the application to run without opening the legacy database.
// It handles both:
//   - Fresh installs: v2 schema at configured path (no _v2 suffix, no v2_ prefix)
//   - Post-migration: v2 schema at migration path (_v2 suffix, v2_ prefix)
func initializeV2OnlyMode(settings *conf.Settings) (*v2only.Datastore, error) {
	log := logger.Global().Module("datastore")
	log.Info("initializing enhanced database mode",
		logger.String("operation", "initialize_enhanced_database_mode"))

	// Determine configuration based on database type
	var v2Manager datastoreV2.Manager
	var useV2Prefix bool
	var err error

	switch {
	case settings.Output.SQLite.Enabled:
		configuredPath := settings.Output.SQLite.Path
		migrationPath := datastoreV2.V2MigrationPathFromConfigured(configuredPath)

		// Determine if v2 schema is at configured path (fresh/post-consolidation) or migration path
		if datastoreV2.CheckSQLiteHasV2Schema(configuredPath) {
			// Fresh install restart or post-consolidation: use configured path directly
			log.Debug("v2 schema found at configured path",
				logger.String("path", configuredPath))
			v2Manager, err = datastoreV2.NewSQLiteManager(datastoreV2.Config{
				DirectPath: configuredPath,
				Debug:      settings.Debug,
				Logger:     log,
			})
			useV2Prefix = false
		} else {
			// Migration mode: use derived v2 migration path
			log.Debug("using migration v2 database path",
				logger.String("path", migrationPath))
			v2Manager, err = datastoreV2.NewSQLiteManager(datastoreV2.Config{
				ConfiguredPath: configuredPath,
				Debug:          settings.Debug,
				Logger:         log,
			})
			useV2Prefix = false
		}

	case settings.Output.MySQL.Enabled:
		// Check if fresh v2 tables exist (no prefix) or migration tables (v2_ prefix)
		isFreshV2 := datastoreV2.CheckMySQLHasFreshV2Schema(settings)
		useV2Prefix = !isFreshV2

		log.Debug("MySQL v2 mode configuration",
			logger.Bool("use_v2_prefix", useV2Prefix),
			logger.Bool("is_fresh_v2", isFreshV2))

		v2Manager, err = datastoreV2.NewMySQLManager(&datastoreV2.MySQLConfig{
			Host:        settings.Output.MySQL.Host,
			Port:        settings.Output.MySQL.Port,
			Username:    settings.Output.MySQL.Username,
			Password:    settings.Output.MySQL.Password,
			Database:    settings.Output.MySQL.Database,
			UseV2Prefix: useV2Prefix,
			Debug:       settings.Debug,
		})

	default:
		return nil, errors.Newf("no database configured").
			Component("analysis").
			Category(errors.CategoryConfiguration).
			Context("operation", "initialize_v2_only_mode").
			Build()
	}

	if err != nil {
		return nil, errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "create_v2_database_manager").
			Build()
	}

	// Initialize the v2 database schema (ensures auxiliary tables exist)
	if err := v2Manager.Initialize(); err != nil {
		_ = v2Manager.Close()
		return nil, errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "initialize_v2_database").
			Build()
	}

	// Create repositories
	v2DB := v2Manager.DB()
	isMySQL := settings.Output.MySQL.Enabled // Determine dialect from settings
	detectionRepo := repository.NewDetectionRepository(v2DB, useV2Prefix, isMySQL)
	labelRepo := repository.NewLabelRepository(v2DB, useV2Prefix, isMySQL)
	modelRepo := repository.NewModelRepository(v2DB, useV2Prefix, isMySQL)
	sourceRepo := repository.NewAudioSourceRepository(v2DB, useV2Prefix, isMySQL)
	weatherRepo := repository.NewWeatherRepository(v2DB, useV2Prefix, isMySQL)
	imageCacheRepo := repository.NewImageCacheRepository(v2DB, labelRepo, useV2Prefix, isMySQL)
	thresholdRepo := repository.NewDynamicThresholdRepository(v2DB, labelRepo, useV2Prefix, isMySQL)
	notificationRepo := repository.NewNotificationHistoryRepository(v2DB, labelRepo, useV2Prefix, isMySQL)

	// Load eBird taxonomy for species code lookups in analytics endpoints.
	_, scientificIndex, taxonomyErr := birdnet.LoadTaxonomyData("")
	if taxonomyErr != nil {
		log.Warn("failed to load taxonomy data for species codes",
			logger.String("error", taxonomyErr.Error()))
	}

	// Create V2OnlyDatastore
	ds, err := v2only.New(&v2only.Config{
		Manager:        v2Manager,
		Detection:      detectionRepo,
		Label:          labelRepo,
		Model:          modelRepo,
		Source:         sourceRepo,
		Weather:        weatherRepo,
		ImageCache:     imageCacheRepo,
		Threshold:      thresholdRepo,
		Notification:   notificationRepo,
		Logger:         log,
		Timezone:       time.Local,
		Labels:         settings.BirdNET.Labels, // For GetThresholdEvents workaround (#1907)
		SpeciesCodeMap: scientificIndex,
	})
	if err != nil {
		_ = v2Manager.Close()
		return nil, errors.New(err).
			Component("analysis").
			Category(errors.CategoryDatabase).
			Context("operation", "create_v2_only_datastore").
			Build()
	}

	log.Info("enhanced database mode initialized successfully",
		logger.String("operation", "initialize_enhanced_database_mode"))

	return ds, nil
}
