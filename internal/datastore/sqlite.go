package datastore

import (
	"fmt"
	"log"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/conf"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// SQLiteStore implements DataStore for SQLite
type SQLiteStore struct {
	DataStore
	Settings *conf.Settings
}

func validateSQLiteConfig() error {
	// Add validation logic for SQLite configuration
	// Return an error if the configuration is invalid
	return nil
}

// InitializeDatabase sets up the SQLite database connection
func (store *SQLiteStore) Open() error {
	if err := validateSQLiteConfig(); err != nil {
		return err // validateSQLiteConfig returns a properly formatted error
	}

	dir, fileName := filepath.Split(store.Settings.Output.SQLite.Path)
	basePath := conf.GetBasePath(dir)
	absoluteFilePath := filepath.Join(basePath, fileName)

	// Create a new GORM logger
	newLogger := createGormLogger()

	// Open the SQLite database
	db, err := gorm.Open(sqlite.Open(absoluteFilePath), &gorm.Config{Logger: newLogger})
	if err != nil {
		return fmt.Errorf("failed to open SQLite database: %w", err)
	}

	// Enable foreign key constraint enforcement for SQLite
	if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		return fmt.Errorf("failed to enable foreign key support in SQLite: %w", err)
	}

	// Set SQLite to use MEMORY journal mode, reduces sdcard wear and improves performance
	if err := db.Exec("PRAGMA journal_mode = MEMORY").Error; err != nil {
		return fmt.Errorf("failed to enable MEMORY journal mode in SQLite: %w", err)
	}

	// Set SQLite to use NORMAL synchronous mode
	if err := db.Exec("PRAGMA synchronous = NORMAL").Error; err != nil {
		return fmt.Errorf("failed to set synchronous mode in SQLite: %w", err)
	}

	// Set SQLIte to use MEMORY temp store mode
	if err := db.Exec("PRAGMA temp_store = MEMORY").Error; err != nil {
		return fmt.Errorf("failed to set temp store mode in SQLite: %w", err)
	}

	// Increase cache size
	if err := db.Exec("PRAGMA cache_size = -4000").Error; err != nil {
		return fmt.Errorf("failed to set cache size in SQLite: %w", err)
	}

	store.DB = db
	return performAutoMigration(db, store.Settings.Debug, "SQLite", absoluteFilePath)
}

// Close the SQLite database connection
func (store *SQLiteStore) Close() error {
	if store.DB == nil {
		return fmt.Errorf("database connection is not initialized or already closed")
	}

	// Optimize database on close
	if err := store.DB.Exec("PRAGMA analysis_limit=400").Error; err != nil {
		log.Printf("Failed to set analysis_limit: %v\n", err)
	}
	if err := store.DB.Exec("PRAGMA optimize").Error; err != nil {
		log.Printf("Failed to optimize database: %v\n", err)
	}

	return nil
}
