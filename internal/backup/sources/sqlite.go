// Package sources provides backup source implementations
package sources

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// SQLiteSource implements the backup.Source interface for SQLite databases
type SQLiteSource struct {
	config *conf.Settings
}

// NewSQLiteSource creates a new SQLite backup source
func NewSQLiteSource(config *conf.Settings) *SQLiteSource {
	return &SQLiteSource{
		config: config,
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

	// Create a temporary directory for the backup
	tempDir, err := os.MkdirTemp("", "sqlite-backup-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Generate backup filename with timestamp
	timestamp := time.Now().UTC().Format("20060102150405")
	backupFilename := fmt.Sprintf("birdnet-sqlite-%s.db", timestamp)
	backupPath := filepath.Join(tempDir, backupFilename)

	// Open source database with GORM in read-only mode
	sourceConfig := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
		DryRun: true, // Ensure read-only mode
	}
	sourceDB, err := gorm.Open(sqlite.Open(dbPath+"?mode=ro"), sourceConfig)
	if err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to open source database: %w", err)
	}

	// Get the underlying *sql.DB
	sqlDB, err := sourceDB.DB()
	if err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to get underlying database connection: %w", err)
	}
	defer sqlDB.Close()

	// Begin a transaction
	tx := sourceDB.WithContext(ctx).Begin()
	if tx.Error != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}
	defer tx.Rollback()

	// Perform backup using SQLite's backup API
	err = tx.Exec(fmt.Sprintf("VACUUM INTO '%s'", backupPath)).Error
	if err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to backup database: %w", err)
	}

	// Commit the transaction
	if err := tx.Commit().Error; err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to commit transaction: %w", err)
	}

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
