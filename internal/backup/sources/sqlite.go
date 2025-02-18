// Package sources provides backup source implementations
package sources

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/backup"
	"github.com/tphakala/birdnet-go/internal/conf"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// SQLiteSource implements the backup.Source interface for SQLite databases
type SQLiteSource struct {
	config *conf.Settings
	logger backup.Logger
}

// NewSQLiteSource creates a new SQLite backup source
func NewSQLiteSource(config *conf.Settings) *SQLiteSource {
	return &SQLiteSource{
		config: config,
		logger: backup.DefaultLogger(),
	}
}

// Name returns the name of this source
func (s *SQLiteSource) Name() string {
	return "sqlite"
}

// Backup performs a backup of the SQLite database
func (s *SQLiteSource) Backup(ctx context.Context) (string, error) {
	if !s.config.Output.SQLite.Enabled {
		return "", fmt.Errorf("sqlite is not enabled")
	}

	dbPath := s.config.Output.SQLite.Path
	if dbPath == "" {
		return "", fmt.Errorf("sqlite path is not configured")
	}

	s.logger.Printf("SQLite backup starting for database: %s", dbPath)

	// Convert to absolute path if necessary
	if !filepath.IsAbs(dbPath) {
		absPath, err := filepath.Abs(dbPath)
		if err != nil {
			return "", fmt.Errorf("failed to resolve absolute database path: %w", err)
		}
		dbPath = absPath
		s.logger.Printf("Converted database path to absolute: %s", dbPath)
	}

	// Verify the database exists and is accessible
	if _, err := os.Stat(dbPath); err != nil {
		return "", fmt.Errorf("database file not accessible: %w", err)
	}
	s.logger.Printf("Verified database file exists and is accessible")

	// Create a temporary directory for the backup
	s.logger.Printf("Creating temporary directory for backup...")
	tempDir, err := os.MkdirTemp("", "sqlite-backup-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	s.logger.Printf("Created temporary directory: %s", tempDir)

	// Generate backup filename with timestamp
	timestamp := time.Now().UTC().Format("20060102150405")
	backupFilename := fmt.Sprintf("birdnet-sqlite-%s.db", timestamp)
	backupPath := filepath.Join(tempDir, backupFilename)
	s.logger.Printf("Generated backup path: %s", backupPath)

	// Open source database with GORM in read-only mode to ensure it's valid
	s.logger.Printf("Opening source database in read-only mode...")
	sourceConfig := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		DryRun: true, // Ensure read-only mode
	}

	// Open the database in read-only mode
	sourceDB, err := gorm.Open(sqlite.Open(dbPath+"?mode=ro"), sourceConfig)
	if err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to open source database: %w", err)
	}
	s.logger.Printf("Successfully opened source database")

	// Get the underlying *sql.DB
	sqlDB, err := sourceDB.DB()
	if err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to get underlying database connection: %w", err)
	}

	// Close the database connection before copying
	sqlDB.Close()
	s.logger.Printf("Closed database connection")

	// Open the source database file
	s.logger.Printf("Opening source database file for copying...")
	srcFile, err := os.Open(dbPath)
	if err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to open source database file: %w", err)
	}
	defer srcFile.Close()

	// Create the destination file
	s.logger.Printf("Creating destination backup file...")
	dstFile, err := os.Create(backupPath)
	if err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to create backup file: %w", err)
	}
	defer dstFile.Close()

	// Copy the database file
	s.logger.Printf("Copying database file...")
	written, err := io.Copy(dstFile, srcFile)
	if err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to copy database: %w", err)
	}
	s.logger.Printf("Successfully copied %d bytes", written)

	// Sync to ensure the file is written to disk
	s.logger.Printf("Syncing backup file to disk...")
	if err := dstFile.Sync(); err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to sync backup file: %w", err)
	}

	// Verify the backup file was created and is readable
	s.logger.Printf("Verifying backup file...")
	if info, err := os.Stat(backupPath); err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("backup file was not created successfully: %w", err)
	} else {
		s.logger.Printf("Verified backup file, size: %d bytes", info.Size())
	}

	s.logger.Printf("SQLite backup completed successfully")
	return backupPath, nil
}

// Validate checks if the source configuration is valid
func (s *SQLiteSource) Validate() error {
	if !s.config.Output.SQLite.Enabled {
		return fmt.Errorf("sqlite is not enabled")
	}

	dbPath := s.config.Output.SQLite.Path
	if dbPath == "" {
		return fmt.Errorf("sqlite path is not configured")
	}

	// Check if the source database exists
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("source database does not exist: %w", err)
	}

	// Try to open the database with GORM to verify it's valid
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// Get the underlying *sql.DB to close it properly
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying database connection: %w", err)
	}
	defer sqlDB.Close()

	// Verify we can query the database
	if err := sqlDB.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	return nil
}
