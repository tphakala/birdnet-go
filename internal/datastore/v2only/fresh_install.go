// Package v2only provides a datastore implementation using only the v2 schema.
package v2only

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// InitializeFreshInstall creates a new v2-only datastore for fresh installations.
// It creates the database at the configured path with v2 schema.
// NO _v2 suffix for SQLite, NO v2_ prefix for MySQL tables.
//
// This function should be called when:
//   - No database exists (fresh installation)
//   - User wants to start fresh with v2 schema
//
// The function also sets the global enhanced database flag via v2.SetEnhancedDatabaseMode().
func InitializeFreshInstall(settings *conf.Settings, log logger.Logger, speciesCodeMap map[string]string) (*Datastore, error) {
	if log == nil {
		log = logger.Global().Module("datastore")
	}

	log.Info("initializing fresh installation in enhanced database mode")

	var manager v2.Manager
	var err error

	switch {
	case settings.Output.SQLite.Enabled:
		// Fresh install: use configured path directly, NO _v2 suffix
		dbPath := settings.Output.SQLite.Path
		if dbPath == "" {
			return nil, errors.Newf("sqlite path is empty").
				Component("datastore").
				Category(errors.CategoryConfiguration).
				Context("operation", "fresh_install").
				Build()
		}

		// Ensure directory exists
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
			return nil, errors.New(err).
				Component("datastore").
				Category(errors.CategoryConfiguration).
				Context("operation", "fresh_install").
				Context("step", "create_database_directory").
				Build()
		}

		manager, err = v2.NewSQLiteManager(v2.Config{
			DirectPath: dbPath, // Use exact configured path
			Debug:      settings.Debug,
			Logger:     log,
		})

	case settings.Output.MySQL.Enabled:
		// Fresh install: use clean table names, NO v2_ prefix
		manager, err = v2.NewMySQLManager(&v2.MySQLConfig{
			Host:        settings.Output.MySQL.Host,
			Port:        settings.Output.MySQL.Port,
			Username:    settings.Output.MySQL.Username,
			Password:    settings.Output.MySQL.Password,
			Database:    settings.Output.MySQL.Database,
			UseV2Prefix: false, // NO prefix for fresh installs
			Debug:       settings.Debug,
		})

	default:
		return nil, errors.Newf("no database configured").
			Component("datastore").
			Category(errors.CategoryConfiguration).
			Context("operation", "fresh_install").
			Build()
	}

	if err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "fresh_install").
			Context("step", "create_database_manager").
			Build()
	}

	// Initialize database schema (creates v2 tables)
	if err := manager.Initialize(); err != nil {
		_ = manager.Close()
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "fresh_install").
			Context("step", "initialize_schema").
			Build()
	}

	// Set migration state to COMPLETED (no migration needed for fresh install)
	if err := initializeMigrationStateAsCompleted(manager); err != nil {
		_ = manager.Close()
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "fresh_install").
			Context("step", "set_migration_state").
			Build()
	}

	// Create repositories (no v2 prefix for fresh installs)
	db := manager.DB()
	useV2Prefix := false         // Fresh installs use clean table names
	isMySQL := manager.IsMySQL() // Derive dialect from actual manager, not settings
	detectionRepo := repository.NewDetectionRepository(db, nil, useV2Prefix, isMySQL)
	labelRepo := repository.NewLabelRepository(db, nil, useV2Prefix, isMySQL)
	modelRepo := repository.NewModelRepository(db, nil, useV2Prefix, isMySQL)
	sourceRepo := repository.NewAudioSourceRepository(db, nil, useV2Prefix, isMySQL)
	weatherRepo := repository.NewWeatherRepository(db, nil, useV2Prefix, isMySQL)
	imageCacheRepo := repository.NewImageCacheRepository(db, nil, labelRepo, useV2Prefix, isMySQL)
	thresholdRepo := repository.NewDynamicThresholdRepository(db, nil, labelRepo, useV2Prefix, isMySQL)
	notificationRepo := repository.NewNotificationHistoryRepository(db, nil, labelRepo, useV2Prefix, isMySQL)
	labelTypeRepo := repository.NewLabelTypeRepository(db, nil, useV2Prefix)
	taxClassRepo := repository.NewTaxonomicClassRepository(db, nil, useV2Prefix)

	// Get or create required lookup table entries and cache their IDs
	ctx := context.Background()
	speciesLabelType, err := labelTypeRepo.GetOrCreate(ctx, "species")
	if err != nil {
		_ = manager.Close()
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "fresh_install").
			Context("step", "get_species_label_type").
			Build()
	}

	avesClass, err := taxClassRepo.GetOrCreate(ctx, "Aves")
	if err != nil {
		_ = manager.Close()
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "fresh_install").
			Context("step", "get_aves_taxonomic_class").
			Build()
	}
	avesClassID := avesClass.ID

	// Get the default model (seeded by Initialize)
	defaultModel, err := modelRepo.GetByNameVersionVariant(ctx, "BirdNET", "2.4", "default")
	if err != nil {
		_ = manager.Close()
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "fresh_install").
			Context("step", "get_default_model").
			Build()
	}

	ds, err := New(&Config{
		Manager:            manager,
		Detection:          detectionRepo,
		Label:              labelRepo,
		Model:              modelRepo,
		Source:             sourceRepo,
		Weather:            weatherRepo,
		ImageCache:         imageCacheRepo,
		Threshold:          thresholdRepo,
		Notification:       notificationRepo,
		Logger:             log,
		Timezone:           time.Local,
		DefaultModelID:     defaultModel.ID,
		SpeciesLabelTypeID: speciesLabelType.ID,
		AvesClassID:        &avesClassID,
		Labels:             settings.BirdNET.Labels, // Required for locale-specific common name resolution
		SpeciesCodeMap:     speciesCodeMap,
	})
	if err != nil {
		_ = manager.Close()
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "fresh_install").
			Context("step", "create_datastore").
			Build()
	}

	// Set global enhanced database flag
	v2.SetEnhancedDatabaseMode()

	log.Info("fresh installation initialized successfully",
		logger.String("database_path", manager.Path()))

	return ds, nil
}

// initializeMigrationStateAsCompleted sets the migration state to COMPLETED.
// For fresh installs, migration is effectively "complete" since there's nothing to migrate.
func initializeMigrationStateAsCompleted(manager v2.Manager) error {
	db := manager.DB()

	now := time.Now()
	state := entities.MigrationState{
		ID:              1,
		State:           entities.MigrationStatusCompleted,
		StartedAt:       &now,
		CompletedAt:     &now,
		TotalRecords:    0,
		MigratedRecords: 0,
		LastMigratedID:  0,
		ErrorMessage:    "",
	}

	return db.Save(&state).Error
}
