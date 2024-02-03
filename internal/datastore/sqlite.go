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
	Ctx *conf.Context
}

func validateSQLiteConfig(ctx *conf.Context) error {
	// Add validation logic for SQLite configuration
	// Return an error if the configuration is invalid
	return nil
}

// InitializeDatabase sets up the SQLite database connection
func (store *SQLiteStore) Open() error {
	if err := validateSQLiteConfig(store.Ctx); err != nil {
		return err // validateSQLiteConfig returns a properly formatted error
	}

	dir, fileName := filepath.Split(store.Ctx.Settings.Output.SQLite.Path)
	basePath := conf.GetBasePath(dir)
	absoluteFilePath := filepath.Join(basePath, fileName)

	db, err := gorm.Open(sqlite.Open(absoluteFilePath), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("failed to open SQLite database: %v", err)
	}

	store.DB = db
	return performAutoMigration(db, store.Ctx.Settings.Debug, "SQLite", absoluteFilePath)
}

// SaveToDatabase inserts a new Note record into the SQLite database
func (store *SQLiteStore) Save(note Note) error {
	if store.DB == nil {
		return fmt.Errorf("database connection is not initialized")
	}

	if err := store.DB.Create(&note).Error; err != nil {
		log.Printf("Failed to save note: %v\n", err)
		return err
	}

	return nil
}

func (store *SQLiteStore) Close() error {
	// Handle close specific to SQLite
	return nil
}
