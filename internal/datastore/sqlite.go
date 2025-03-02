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
func createBackup(dbPath string) error {
	// Only create a backup if the file exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil
	}

	// Create timestamp for backup filename
	timestamp := time.Now().Format("20060102_150405")
	backupPath := fmt.Sprintf("%s.backup.%s", dbPath, timestamp)

	// Open source file
	source, err := os.Open(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer source.Close()

	// Create destination file
	destination, err := os.Create(backupPath)
	if err != nil {
		return fmt.Errorf("failed to create backup file: %w", err)
	}
	defer destination.Close()

	// Copy the contents
	_, err = io.Copy(destination, source)
	if err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}

	return nil
}

// Open initializes the SQLite database connection
func (store *SQLiteStore) Open() error {
	if err := validateSQLiteConfig(); err != nil {
		return err
	}

	// Get a component-specific logger
	sqliteLogger := store.getDbLogger("sqlite")

	dbPath := store.Settings.Output.SQLite.Path
	if dbPath == "" {
		// Use default path: config directory + filename
		configDirs, err := conf.GetDefaultConfigPaths()
		if err != nil {
			if sqliteLogger != nil {
				sqliteLogger.Error("Failed to get config paths", "error", err)
			}
			return fmt.Errorf("failed to get config paths: %w", err)
		}

		if len(configDirs) == 0 {
			return fmt.Errorf("no config directories found")
		}

		dbPath = filepath.Join(configDirs[0], "birdnet.db")
	}

	// Make sure the parent directory exists
	parentDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		if sqliteLogger != nil {
			sqliteLogger.Error("Failed to create parent directory",
				"path", parentDir,
				"error", err)
		}
		return fmt.Errorf("failed to create parent directory %s: %w", parentDir, err)
	}

	// Check if we have write permissions
	if err := checkWritePermission(dbPath); err != nil {
		if sqliteLogger != nil {
			sqliteLogger.Error("No write permission",
				"path", dbPath,
				"error", err)
		}
		return fmt.Errorf("no write permission for %s: %w", dbPath, err)
	}

	// Check if there is enough disk space
	requiredSpace := uint64(100 * 1024 * 1024) // 100 MB
	availableSpace, err := getDiskSpace(dbPath)
	if err != nil {
		if sqliteLogger != nil {
			sqliteLogger.Warn("Failed to get disk space",
				"path", dbPath,
				"error", err)
		} else {
			log.Printf("Warning: Failed to get disk space: %v", err)
		}
	} else if availableSpace < requiredSpace {
		if sqliteLogger != nil {
			sqliteLogger.Warn("Low disk space",
				"path", dbPath,
				"available_mb", availableSpace/1024/1024,
				"required_mb", requiredSpace/1024/1024)
		} else {
			log.Printf("Warning: Low disk space (%d MB available, %d MB required)", availableSpace/1024/1024, requiredSpace/1024/1024)
		}
	}

	// Create a backup of the database if it exists and is larger than 1 MB
	if _, err := os.Stat(dbPath); err == nil {
		fi, err := os.Stat(dbPath)
		if err == nil && fi.Size() > 1024*1024 {
			if err := createBackup(dbPath); err != nil {
				if sqliteLogger != nil {
					sqliteLogger.Warn("Failed to create database backup",
						"path", dbPath,
						"error", err)
				} else {
					log.Printf("Warning: Failed to create database backup: %v", err)
				}
			}
		}
	}

	// Create a new GORM logger
	newLogger := createGormLogger(store.Logger)

	// Configure the SQLite database
	config := &gorm.Config{
		Logger: newLogger,
	}

	// Open the SQLite database
	db, err := gorm.Open(sqlite.Open(dbPath+"?_journal=WAL&_timeout=5000"), config)
	if err != nil {
		if sqliteLogger != nil {
			sqliteLogger.Error("Failed to open SQLite database",
				"path", dbPath,
				"error", err)
		} else {
			log.Printf("Failed to open SQLite database: %v\n", err)
		}
		return fmt.Errorf("failed to open SQLite database: %w", err)
	}

	// Set connection pool settings
	sqlDB, err := db.DB()
	if err != nil {
		if sqliteLogger != nil {
			sqliteLogger.Error("Failed to get database connection",
				"error", err)
		} else {
			log.Printf("Failed to get database connection: %v\n", err)
		}
		return fmt.Errorf("failed to get database connection: %w", err)
	}

	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(50)
	sqlDB.SetConnMaxLifetime(time.Hour)

	store.DB = db
	return performAutoMigration(db, store.Settings.Debug, "SQLite", dbPath, store.Logger)
}

// Close closes the SQLite database connection
func (store *SQLiteStore) Close() error {
	// Get a component-specific logger
	sqliteLogger := store.getDbLogger("sqlite")

	// Ensure that the store's DB field is not nil to avoid a panic
	if store.DB == nil {
		if sqliteLogger != nil {
			sqliteLogger.Error("Database connection is not initialized")
		}
		return fmt.Errorf("database connection is not initialized")
	}

	// Retrieve the generic database object from the GORM DB object
	sqlDB, err := store.DB.DB()
	if err != nil {
		if sqliteLogger != nil {
			sqliteLogger.Error("Failed to retrieve generic DB object", "error", err)
		} else {
			log.Printf("Failed to retrieve generic DB object: %v\n", err)
		}
		return err
	}

	// Close the generic database object, which closes the underlying SQL database connection
	if err := sqlDB.Close(); err != nil {
		if sqliteLogger != nil {
			sqliteLogger.Error("Failed to close SQLite database", "error", err)
		} else {
			log.Printf("Failed to close SQLite database: %v\n", err)
		}
		return err
	}

	if sqliteLogger != nil && store.Settings.Debug {
		sqliteLogger.Debug("SQLite database connection closed successfully")
	}
	return nil
}

// UpdateNote updates specific fields of a note in SQLite
func (s *SQLiteStore) UpdateNote(id string, updates map[string]interface{}) error {
	return s.DB.Model(&Note{}).Where("id = ?", id).Updates(updates).Error
}
