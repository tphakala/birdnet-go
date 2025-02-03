package datastore

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"golang.org/x/sys/unix"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// SQLiteStore implements StoreInterface for SQLite databases
type SQLiteStore struct {
	Settings *conf.Settings
	DataStore
}

func validateSQLiteConfig() error {
	// Add validation logic for SQLite configuration
	// Return an error if the configuration is invalid
	return nil
}

// createBackup creates a timestamped backup of the SQLite database file
func (s *SQLiteStore) createBackup(dbPath string) error {
	// Check if source database exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil // No need to backup if database doesn't exist yet
	}

	// Get database file size
	dbInfo, err := os.Stat(dbPath)
	if err != nil {
		return fmt.Errorf("failed to get database file info: %w", err)
	}

	// Check available disk space
	var stat unix.Statfs_t
	if err := unix.Statfs(filepath.Dir(dbPath), &stat); err != nil {
		return fmt.Errorf("failed to get filesystem stats: %w", err)
	}

	// Available space in bytes
	availableSpace := stat.Bavail * uint64(stat.Bsize)
	requiredSpace := uint64(dbInfo.Size()) + 1024*1024 // Add 1MB buffer

	if availableSpace < requiredSpace {
		return fmt.Errorf("insufficient disk space for backup. Required: %d bytes, Available: %d bytes", requiredSpace, availableSpace)
	}

	// Check if we have write permissions in the backup directory
	backupDir := filepath.Dir(dbPath)
	if err := unix.Access(backupDir, unix.W_OK); err != nil {
		return fmt.Errorf("no write permission in backup directory: %w", err)
	}

	// Create timestamp for backup file
	timestamp := time.Now().Format("20060102_150405")
	backupPath := fmt.Sprintf("%s.backup_%s", dbPath, timestamp)

	// Open source file
	source, err := os.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open source database: %w", err)
	}
	defer source.Close()

	// Create backup file
	destination, err := os.Create(backupPath)
	if err != nil {
		return fmt.Errorf("failed to create backup file: %w", err)
	}
	defer destination.Close()

	// Copy the file
	if _, err := io.Copy(destination, source); err != nil {
		return fmt.Errorf("failed to copy database: %w", err)
	}

	log.Printf("Created database backup: %s", backupPath)
	return nil
}

// Open initializes the SQLite database connection
func (s *SQLiteStore) Open() error {
	// Get database path from settings
	dbPath := s.Settings.Output.SQLite.Path

	// Create database directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("failed to create database directory: %w", err)
	}

	// Configure GORM logger
	gormLogger := createGormLogger()

	// Open SQLite database with GORM
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		return fmt.Errorf("failed to open SQLite database: %w", err)
	}

	// Set SQLite pragmas for better performance
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying *sql.DB: %w", err)
	}

	// Set pragmas
	pragmas := []string{
		"PRAGMA foreign_keys=ON",    // required for foreign key constraints
		"PRAGMA journal_mode=WAL",   // faster writes
		"PRAGMA synchronous=NORMAL", // faster writes
		"PRAGMA cache_size=-4000",   // increase cache size
		"PRAGMA temp_store=MEMORY",  // faster writes
	}

	for _, pragma := range pragmas {
		if _, err := sqlDB.Exec(pragma); err != nil {
			log.Printf("Warning: Failed to set pragma %s: %v", pragma, err)
		}
	}

	// Store the database connection
	s.DB = db

	// Perform auto-migration
	if err := performAutoMigration(db, s.Settings.Debug, "SQLite", dbPath); err != nil {
		return err
	}

	return nil
}

// Close closes the SQLite database connection
func (s *SQLiteStore) Close() error {
	if s.DB != nil {
		sqlDB, err := s.DB.DB()
		if err != nil {
			return fmt.Errorf("failed to get underlying *sql.DB: %w", err)
		}
		return sqlDB.Close()
	}
	return nil
}

// UpdateNote updates specific fields of a note in SQLite
func (s *SQLiteStore) UpdateNote(id string, updates map[string]interface{}) error {
	return s.DB.Model(&Note{}).Where("id = ?", id).Updates(updates).Error
}
