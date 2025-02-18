// Package sources provides backup source implementations
package sources

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
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

	// Open source database
	srcFile, err := os.Open(dbPath)
	if err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to open source database: %w", err)
	}
	defer srcFile.Close()

	// Create backup file
	dstFile, err := os.Create(backupPath)
	if err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("failed to create backup file: %w", err)
	}
	defer dstFile.Close()

	// Copy the database file in chunks
	buf := make([]byte, 1024*1024) // 1MB buffer
	for {
		select {
		case <-ctx.Done():
			os.RemoveAll(tempDir)
			return "", ctx.Err()
		default:
			n, err := srcFile.Read(buf)
			if err != nil && !errors.Is(err, io.EOF) {
				os.RemoveAll(tempDir)
				return "", fmt.Errorf("error reading source database: %w", err)
			}
			if n == 0 {
				break
			}

			if _, err := dstFile.Write(buf[:n]); err != nil {
				os.RemoveAll(tempDir)
				return "", fmt.Errorf("error writing to backup file: %w", err)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}

	// Sync the backup file to disk
	if err := dstFile.Sync(); err != nil {
		os.RemoveAll(tempDir)
		return "", fmt.Errorf("error syncing backup file: %w", err)
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

	return nil
}
