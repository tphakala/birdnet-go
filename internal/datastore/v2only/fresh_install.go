// Package v2only provides a datastore implementation using only the v2 schema.
package v2only

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
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
func InitializeFreshInstall(settings *conf.Settings, log logger.Logger) (*Datastore, error) {
	if log == nil {
		log = logger.Global().Module("v2only")
	}

	log.Info("initializing fresh installation in v2-only mode")

	var manager v2.Manager
	var err error

	switch {
	case settings.Output.SQLite.Enabled:
		// Fresh install: use configured path directly, NO _v2 suffix
		dbPath := settings.Output.SQLite.Path
		if dbPath == "" {
			return nil, fmt.Errorf("sqlite path is empty")
		}

		// Ensure directory exists
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
			return nil, fmt.Errorf("failed to create database directory: %w", err)
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
		return nil, fmt.Errorf("no database configured")
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create database manager: %w", err)
	}

	// Initialize database schema (creates v2 tables)
	if err := manager.Initialize(); err != nil {
		_ = manager.Close()
		return nil, fmt.Errorf("failed to initialize database schema: %w", err)
	}

	// Set migration state to COMPLETED (no migration needed for fresh install)
	if err := initializeMigrationStateAsCompleted(manager); err != nil {
		_ = manager.Close()
		return nil, fmt.Errorf("failed to set migration state: %w", err)
	}

	// Create repositories (no v2 prefix for fresh installs)
	db := manager.DB()
	useV2Prefix := false                      // Fresh installs use clean table names
	isMySQL := settings.Output.MySQL.Enabled  // Determine dialect from settings
	detectionRepo := repository.NewDetectionRepository(db, useV2Prefix, isMySQL)
	labelRepo := repository.NewLabelRepository(db, useV2Prefix, isMySQL)
	modelRepo := repository.NewModelRepository(db, useV2Prefix, isMySQL)
	sourceRepo := repository.NewAudioSourceRepository(db, useV2Prefix, isMySQL)
	weatherRepo := repository.NewWeatherRepository(db, useV2Prefix, isMySQL)
	imageCacheRepo := repository.NewImageCacheRepository(db, useV2Prefix, isMySQL)
	thresholdRepo := repository.NewDynamicThresholdRepository(db, useV2Prefix, isMySQL)
	notificationRepo := repository.NewNotificationHistoryRepository(db, useV2Prefix, isMySQL)

	ds, err := New(&Config{
		Manager:      manager,
		Detection:    detectionRepo,
		Label:        labelRepo,
		Model:        modelRepo,
		Source:       sourceRepo,
		Weather:      weatherRepo,
		ImageCache:   imageCacheRepo,
		Threshold:    thresholdRepo,
		Notification: notificationRepo,
		Logger:       log,
		Timezone:     time.Local,
	})
	if err != nil {
		_ = manager.Close()
		return nil, fmt.Errorf("failed to create datastore: %w", err)
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
