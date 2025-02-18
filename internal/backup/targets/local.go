// Package targets provides backup target implementations
package targets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tphakala/birdnet-go/internal/backup"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
)

// LocalTarget implements the backup.Target interface for local filesystem storage
type LocalTarget struct {
	path   string
	debug  bool
	logger backup.Logger
}

// LocalTargetConfig holds configuration for the local filesystem target
type LocalTargetConfig struct {
	Path  string
	Debug bool
}

// NewLocalTarget creates a new local filesystem target
func NewLocalTarget(config LocalTargetConfig, logger backup.Logger) (*LocalTarget, error) {
	if config.Path == "" {
		return nil, fmt.Errorf("path is required for local target")
	}

	if logger == nil {
		logger = backup.DefaultLogger()
	}

	// Create backup directory if it doesn't exist
	if err := os.MkdirAll(config.Path, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create backup directory: %w", err)
	}

	return &LocalTarget{
		path:   config.Path,
		debug:  config.Debug,
		logger: logger,
	}, nil
}

// Name returns the name of this target
func (t *LocalTarget) Name() string {
	return "local"
}

// Store stores a backup file in the local filesystem
func (t *LocalTarget) Store(ctx context.Context, sourcePath string, metadata *backup.Metadata) error {
	if t.debug {
		t.logger.Printf("Storing backup from %s to local target", sourcePath)
	}

	// Create timestamp-based directory for this backup
	timestamp := time.Now().UTC().Format("20060102150405")
	backupDir := filepath.Join(t.path, timestamp)
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Copy the backup file
	srcFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer srcFile.Close()

	dstPath := filepath.Join(backupDir, filepath.Base(sourcePath))
	dstFile, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy backup file: %w", err)
	}

	// Store metadata
	metadataPath := filepath.Join(backupDir, "metadata.json")
	metadataFile, err := os.Create(metadataPath)
	if err != nil {
		return fmt.Errorf("failed to create metadata file: %w", err)
	}
	defer metadataFile.Close()

	if err := json.NewEncoder(metadataFile).Encode(metadata); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	if t.debug {
		t.logger.Printf("Successfully stored backup in %s", backupDir)
	}

	return nil
}

// List returns a list of available backups
func (t *LocalTarget) List(ctx context.Context) ([]backup.BackupInfo, error) {
	if t.debug {
		t.logger.Printf("Listing backups in local target")
	}

	entries, err := os.ReadDir(t.path)
	if err != nil {
		return nil, fmt.Errorf("failed to read backup directory: %w", err)
	}

	var backups []backup.BackupInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		metadataPath := filepath.Join(t.path, entry.Name(), "metadata.json")
		metadataFile, err := os.Open(metadataPath)
		if err != nil {
			t.logger.Printf("Warning: skipping backup %s: %v", entry.Name(), err)
			continue
		}

		var metadata backup.Metadata
		if err := json.NewDecoder(metadataFile).Decode(&metadata); err != nil {
			metadataFile.Close()
			t.logger.Printf("Warning: invalid metadata in backup %s: %v", entry.Name(), err)
			continue
		}
		metadataFile.Close()

		backupInfo := backup.BackupInfo{
			Metadata: metadata,
			Target:   t.Name(),
		}
		backups = append(backups, backupInfo)
	}

	// Sort backups by timestamp (newest first)
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].ID > backups[j].ID
	})

	return backups, nil
}

// Delete removes a backup
func (t *LocalTarget) Delete(ctx context.Context, backupID string) error {
	if t.debug {
		t.logger.Printf("Deleting backup %s from local target", backupID)
	}

	backupPath := filepath.Join(t.path, backupID)
	if err := os.RemoveAll(backupPath); err != nil {
		return fmt.Errorf("failed to delete backup: %w", err)
	}

	return nil
}

// Validate checks if the target configuration is valid
func (t *LocalTarget) Validate() error {
	// Check if path is absolute
	if !filepath.IsAbs(t.path) {
		absPath, err := filepath.Abs(t.path)
		if err != nil {
			return fmt.Errorf("failed to resolve absolute path: %w", err)
		}
		t.path = absPath
	}

	// Check if path exists and is a directory
	if info, err := os.Stat(t.path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("backup path does not exist: %w", err)
		}
		return fmt.Errorf("failed to check backup path: %w", err)
	} else if !info.IsDir() {
		return fmt.Errorf("backup path is not a directory")
	}

	// Check if path is writable
	tmpFile := filepath.Join(t.path, ".write_test")
	f, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("backup path is not writable: %w", err)
	}
	f.Close()
	os.Remove(tmpFile)

	// Check available disk space
	availableBytes, err := diskmanager.GetAvailableSpace(t.path)
	if err != nil {
		return fmt.Errorf("failed to check disk space: %w", err)
	}

	// Convert available bytes to gigabytes
	availableGB := float64(availableBytes) / (1024 * 1024 * 1024)

	// Ensure at least 1GB free space is available
	if availableGB < 1.0 {
		return fmt.Errorf("insufficient disk space: %.1f GB available, minimum 1 GB required", availableGB)
	}

	if t.logger != nil {
		t.logger.Printf("Available disk space at backup location: %.1f GB", availableGB)
	}

	return nil
}
