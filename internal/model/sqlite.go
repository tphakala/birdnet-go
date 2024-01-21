package model

import (
	"fmt"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/config"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// SQLiteStore implements DataStore for SQLite
type SQLiteStore struct {
	DataStore
}

func validateSQLiteConfig(ctx *config.Context) error {
	// Add validation logic for SQLite configuration
	// Return an error if the configuration is invalid
	return nil
}

// InitializeDatabase sets up the SQLite database connection
func (store *SQLiteStore) Open(ctx *config.Context) error {
	if err := validateSQLiteConfig(ctx); err != nil {
		return err // validateSQLiteConfig returns a properly formatted error
	}

	dir, fileName := filepath.Split(ctx.Settings.Output.SQLite.Path)
	basePath := config.GetBasePath(dir)
	absoluteFilePath := filepath.Join(basePath, fileName)

	db, err := gorm.Open(sqlite.Open(absoluteFilePath), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("failed to open SQLite database: %v", err)
	}

	store.DB = db
	return performAutoMigration(db, ctx.Settings.Debug, "SQLite", absoluteFilePath)
}

// SaveToDatabase inserts a new Note record into the SQLite database
func (store *SQLiteStore) Save(ctx *config.Context, note Note) error {
	if store.DB == nil {
		return fmt.Errorf("database connection is not initialized")
	}

	if err := store.DB.Create(&note).Error; err != nil {
		logger.Error("main", "Failed to save note: %v\n", err)
		return err
	}

	logger.Debug("main", "Saved note: %v\n", note)
	return nil
}

func (store *SQLiteStore) Close() error {
	// Handle close specific to SQLite
	return nil
}
