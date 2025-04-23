package datastore

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
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

// getDiskSpace returns available disk space for the given path
func getDiskSpace(path string) (uint64, error) {
	var availableSpace uint64

	// OS-specific disk space check
	dir := filepath.Dir(path)

	// Get directory information using OS-agnostic method
	var err error
	availableSpace, err = getDiskFreeSpace(dir)
	if err != nil {
		return 0, fmt.Errorf("failed to get disk space: %w", err)
	}

	return availableSpace, nil
}

// checkWritePermission checks if we have write permission to the directory
func checkWritePermission(path string) error {
	// Create a temporary file to test write permissions
	tempFile := filepath.Join(filepath.Dir(path), ".tmp_write_test")
	f, err := os.OpenFile(tempFile, os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		return fmt.Errorf("no write permission in directory: %w", err)
	}
	f.Close()
	os.Remove(tempFile)
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
	availableSpace, err := getDiskSpace(dbPath)
	if err != nil {
		return err
	}

	requiredSpace := uint64(dbInfo.Size()) + 1024*1024 // Add 1MB buffer

	if availableSpace < requiredSpace {
		return fmt.Errorf("insufficient disk space for backup. Required: %d bytes, Available: %d bytes", requiredSpace, availableSpace)
	}

	// Check if we have write permissions in the backup directory
	if err := checkWritePermission(dbPath); err != nil {
		return err
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

// GetAllLockedNotes returns all notes that have an entry in the note_locks table
func (s *SQLiteStore) GetAllLockedNotes() ([]Note, error) {
	var notes []Note
	
	// Use a join between notes and note_locks tables to find all locked notes
	err := s.DB.Joins("JOIN note_locks ON notes.id = note_locks.note_id").Find(&notes).Error
	if err != nil {
		return nil, fmt.Errorf("error retrieving locked notes: %w", err)
	}
	
	return notes, nil
}
