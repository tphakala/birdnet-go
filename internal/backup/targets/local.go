// Package targets provides backup target implementations
package targets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/backup"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
)

// Constants for file operations
const (
	maxBackupSize    = 10 * 1024 * 1024 * 1024 // 10GB
	dirPermissions   = 0o700                   // rwx------ (owner only)
	filePermissions  = 0o600                   // rw------- (owner only)
	maxPathLength    = 255                     // Maximum path length
	copyBufferSize   = 32 * 1024               // 32KB buffer for file copies
	maxRetries       = 3                       // Maximum number of retries for transient errors
	baseBackoffDelay = 100 * time.Millisecond  // Base delay for exponential backoff
	currentVersion   = 1                       // Current metadata version
)

// Windows-specific reserved names
var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
}

// MetadataV1 represents version 1 of the backup metadata format
type MetadataV1 struct {
	Version      int       `json:"version"`
	ID           string    `json:"id"`
	Timestamp    time.Time `json:"timestamp"`
	Size         int64     `json:"size"`
	Type         string    `json:"type"`
	Source       string    `json:"source"`
	IsDaily      bool      `json:"is_daily"`
	ConfigHash   string    `json:"config_hash"`
	AppVersion   string    `json:"app_version"`
	Checksum     string    `json:"checksum,omitempty"`      // File checksum if available
	Compressed   bool      `json:"compressed,omitempty"`    // Whether the backup is compressed
	OriginalSize int64     `json:"original_size,omitempty"` // Original size before compression
}

// convertToVersionedMetadata converts backup.Metadata to MetadataV1
func convertToVersionedMetadata(m *backup.Metadata) MetadataV1 {
	return MetadataV1{
		Version:    currentVersion,
		ID:         m.ID,
		Timestamp:  m.Timestamp,
		Size:       m.Size,
		Type:       m.Type,
		Source:     m.Source,
		IsDaily:    m.IsDaily,
		ConfigHash: m.ConfigHash,
		AppVersion: m.AppVersion,
		Compressed: false, // Set based on actual compression status
		Checksum:   "",    // Calculate checksum if needed
	}
}

// convertFromVersionedMetadata converts MetadataV1 to backup.Metadata
func convertFromVersionedMetadata(m *MetadataV1) backup.Metadata {
	return backup.Metadata{
		ID:         m.ID,
		Timestamp:  m.Timestamp,
		Size:       m.Size,
		Type:       m.Type,
		Source:     m.Source,
		IsDaily:    m.IsDaily,
		ConfigHash: m.ConfigHash,
		AppVersion: m.AppVersion,
	}
}

// atomicWriteFile writes data to a temporary file and then renames it to the target path
func atomicWriteFile(targetPath, tempPattern string, perm os.FileMode, write func(*os.File) error) error {
	// Create temporary file in the same directory as the target
	dir := filepath.Dir(targetPath)
	tempFile, err := os.CreateTemp(dir, tempPattern)
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	tempPath := tempFile.Name()

	// Ensure the temporary file is removed in case of failure
	success := false
	defer func() {
		if !success {
			tempFile.Close()
			os.Remove(tempPath)
		}
	}()

	// Set file permissions
	if err := tempFile.Chmod(perm); err != nil {
		return fmt.Errorf("failed to set file permissions: %w", err)
	}

	// Write data
	if err := write(tempFile); err != nil {
		return err
	}

	// Ensure all data is written to disk
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync file: %w", err)
	}

	// Close the file before renaming
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temporary file: %w", err)
	}

	// Perform atomic rename
	if err := os.Rename(tempPath, targetPath); err != nil {
		return fmt.Errorf("failed to rename temporary file: %w", err)
	}

	success = true
	return nil
}

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

// isTransientError determines if an error is likely transient
func isTransientError(err error) bool {
	if err == nil {
		return false
	}

	// Check for specific error types that might be transient
	if os.IsTimeout(err) {
		return true
	}

	// Check error strings for common transient issues
	errStr := err.Error()
	return strings.Contains(errStr, "resource temporarily unavailable") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "broken pipe")
}

// backoffDuration calculates exponential backoff duration
func backoffDuration(attempt int) time.Duration {
	return baseBackoffDelay * time.Duration(1<<uint(attempt))
}

// withRetry executes an operation with retry logic for transient errors
func (t *LocalTarget) withRetry(op func() error) error {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		if err := op(); err == nil {
			return nil
		} else if !isTransientError(err) {
			return err
		} else {
			lastErr = err
			if t.debug {
				t.logger.Printf("Retrying operation after error: %v (attempt %d/%d)", err, i+1, maxRetries)
			}
		}
		time.Sleep(backoffDuration(i))
	}
	return backup.NewError(backup.ErrIO, "operation failed after retries", lastErr)
}

// copyFile performs an optimized file copy operation
func copyFile(dst, src *os.File) error {
	buf := make([]byte, copyBufferSize)
	_, err := io.CopyBuffer(dst, src, buf)
	return err
}

// validatePath performs comprehensive path validation
func validatePath(path string) error {
	if path == "" {
		return backup.NewError(backup.ErrValidation, "path is required for local target", nil)
	}

	// Check path length
	if len(path) > maxPathLength {
		return backup.NewError(backup.ErrValidation,
			fmt.Sprintf("path length exceeds maximum allowed (%d characters)", maxPathLength),
			nil)
	}

	// Clean and normalize the path
	cleanPath := filepath.Clean(path)

	// Check for directory traversal attempts
	if strings.Contains(cleanPath, "..") {
		return backup.NewError(backup.ErrValidation, "path must not contain directory traversal sequences", nil)
	}

	// Convert to absolute path for validation
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return backup.NewError(backup.ErrValidation, "failed to resolve absolute path", err)
	}

	// Check for symlinks
	fi, err := os.Lstat(absPath)
	if err == nil && (fi.Mode()&os.ModeSymlink) != 0 {
		return backup.NewError(backup.ErrValidation, "symlinks are not allowed in backup path", nil)
	}

	// Windows-specific checks
	if runtime.GOOS == "windows" {
		// Check for reserved names
		parts := strings.Split(cleanPath, string(os.PathSeparator))
		for _, part := range parts {
			baseName := strings.ToUpper(strings.Split(part, ".")[0])
			if windowsReservedNames[baseName] {
				return backup.NewError(backup.ErrValidation,
					fmt.Sprintf("path contains reserved name: %s", part),
					nil)
			}
		}

		// Check for Windows-specific restricted paths
		restricted := []string{
			"C:\\Windows",
			"C:\\Program Files",
			"C:\\Program Files (x86)",
			"C:\\System32",
		}
		for _, r := range restricted {
			if strings.HasPrefix(strings.ToLower(absPath), strings.ToLower(r)) {
				return backup.NewError(backup.ErrValidation,
					fmt.Sprintf("path is within restricted directory: %s", r),
					nil)
			}
		}
	}

	return nil
}

// NewLocalTarget creates a new local filesystem target
func NewLocalTarget(config LocalTargetConfig, logger backup.Logger) (*LocalTarget, error) {
	// Validate and clean the path
	if err := validatePath(config.Path); err != nil {
		return nil, err
	}
	cleanPath := filepath.Clean(config.Path)

	if logger == nil {
		logger = backup.DefaultLogger()
	}

	// Convert to absolute path
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return nil, backup.NewError(backup.ErrValidation, "failed to resolve absolute path", err)
	}

	// Create backup directory if it doesn't exist with restrictive permissions
	if err := os.MkdirAll(absPath, dirPermissions); err != nil {
		return nil, backup.NewError(backup.ErrIO, "failed to create backup directory", err)
	}

	// Ensure directory has correct permissions even if it already existed
	if err := os.Chmod(absPath, dirPermissions); err != nil {
		return nil, backup.NewError(backup.ErrIO, "failed to set directory permissions", err)
	}

	return &LocalTarget{
		path:   absPath,
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
	// Check context cancellation
	if err := ctx.Err(); err != nil {
		return backup.NewError(backup.ErrCanceled, "backup operation cancelled", err)
	}

	if t.debug {
		t.logger.Printf("Storing backup from %s to local target", sourcePath)
	}

	// Validate source file
	srcInfo, err := os.Stat(sourcePath)
	if err != nil {
		return backup.NewError(backup.ErrIO, "failed to stat source file", err)
	}

	// Check file size limit
	if srcInfo.Size() > maxBackupSize {
		return backup.NewError(backup.ErrValidation,
			fmt.Sprintf("backup file too large: %d bytes (max %d bytes)", srcInfo.Size(), maxBackupSize),
			nil)
	}

	// Check available space
	availableBytes, err := diskmanager.GetAvailableSpace(t.path)
	if err != nil {
		return backup.NewError(backup.ErrMedia, "failed to check available space", err)
	}

	// Ensure we have enough space (file size + 10% buffer)
	requiredSpace := uint64(float64(srcInfo.Size()) * 1.1)
	if availableBytes < requiredSpace {
		return backup.NewError(backup.ErrInsufficientSpace,
			fmt.Sprintf("insufficient disk space: need %d bytes, have %d bytes", requiredSpace, availableBytes),
			nil)
	}

	// Create timestamp-based directory for this backup
	timestamp := time.Now().UTC().Format("20060102150405")
	backupDir := filepath.Join(t.path, timestamp)
	if err := os.MkdirAll(backupDir, dirPermissions); err != nil {
		return backup.NewError(backup.ErrIO, "failed to create backup directory", err)
	}

	// Setup cleanup in case of errors
	success := false
	defer func() {
		if !success {
			if err := os.RemoveAll(backupDir); err != nil {
				t.logger.Printf("Warning: failed to cleanup backup directory after error: %v", err)
			}
		}
	}()

	// Copy the backup file with retries and atomic operations
	dstPath := filepath.Join(backupDir, filepath.Base(sourcePath))
	err = t.withRetry(func() error {
		return atomicWriteFile(dstPath, "backup-*.tmp", filePermissions, func(tempFile *os.File) error {
			srcFile, err := os.Open(sourcePath)
			if err != nil {
				return backup.NewError(backup.ErrIO, "failed to open source file", err)
			}
			defer srcFile.Close()

			// Create a buffered copy with context cancellation
			copyDone := make(chan error, 1)
			go func() {
				copyDone <- copyFile(tempFile, srcFile)
			}()

			select {
			case <-ctx.Done():
				return backup.NewError(backup.ErrCanceled, "backup operation cancelled during file copy", ctx.Err())
			case err := <-copyDone:
				if err != nil {
					return backup.NewError(backup.ErrIO, "failed to copy backup file", err)
				}
			}

			return nil
		})
	})

	if err != nil {
		return err
	}

	// Create versioned metadata
	versionedMetadata := convertToVersionedMetadata(metadata)

	// Store metadata with retries and atomic operations
	metadataPath := filepath.Join(backupDir, "metadata.json")
	err = t.withRetry(func() error {
		return atomicWriteFile(metadataPath, "metadata-*.tmp", filePermissions, func(tempFile *os.File) error {
			encoder := json.NewEncoder(tempFile)
			encoder.SetIndent("", "  ") // Pretty print JSON for better readability
			return encoder.Encode(versionedMetadata)
		})
	})

	if err != nil {
		return err
	}

	// Verify the backup
	if err := t.verifyBackup(ctx, dstPath, srcInfo.Size()); err != nil {
		return err
	}

	success = true
	if t.debug {
		t.logger.Printf("Successfully stored backup in %s", backupDir)
	}

	return nil
}

// verifyBackup verifies the integrity of the stored backup
func (t *LocalTarget) verifyBackup(ctx context.Context, backupPath string, expectedSize int64) error {
	// Check context cancellation
	if err := ctx.Err(); err != nil {
		return backup.NewError(backup.ErrCanceled, "backup verification cancelled", err)
	}

	// Verify file exists and has correct size
	info, err := os.Stat(backupPath)
	if err != nil {
		return backup.NewError(backup.ErrCorruption, "failed to verify backup file", err)
	}

	if info.Size() != expectedSize {
		return backup.NewError(backup.ErrCorruption,
			fmt.Sprintf("backup file size mismatch: expected %d, got %d", expectedSize, info.Size()),
			nil)
	}

	return nil
}

// List returns a list of available backups with versioned metadata support
func (t *LocalTarget) List(ctx context.Context) ([]backup.BackupInfo, error) {
	if t.debug {
		t.logger.Printf("Listing backups in local target")
	}

	entries, err := os.ReadDir(t.path)
	if err != nil {
		return nil, backup.NewError(backup.ErrIO, "failed to read backup directory", err)
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

		// Try to decode as versioned metadata first
		var versionedMetadata MetadataV1
		decoder := json.NewDecoder(metadataFile)
		if err := decoder.Decode(&versionedMetadata); err != nil {
			// If that fails, try legacy format
			if _, err := metadataFile.Seek(0, 0); err != nil {
				metadataFile.Close()
				t.logger.Printf("Warning: failed to seek in metadata file for backup %s: %v", entry.Name(), err)
				continue
			}
			var legacyMetadata backup.Metadata
			if err := decoder.Decode(&legacyMetadata); err != nil {
				metadataFile.Close()
				t.logger.Printf("Warning: invalid metadata in backup %s: %v", entry.Name(), err)
				continue
			}
			versionedMetadata = convertToVersionedMetadata(&legacyMetadata)
		}
		metadataFile.Close()

		backupInfo := backup.BackupInfo{
			Metadata: convertFromVersionedMetadata(&versionedMetadata),
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
		return backup.NewError(backup.ErrIO, "failed to delete backup", err)
	}

	return nil
}

// Validate checks if the target configuration is valid
func (t *LocalTarget) Validate() error {
	// Check if path is absolute
	if !filepath.IsAbs(t.path) {
		absPath, err := filepath.Abs(t.path)
		if err != nil {
			return backup.NewError(backup.ErrValidation, "failed to resolve absolute path", err)
		}
		t.path = absPath
	}

	// Check if path exists and is a directory
	if info, err := os.Stat(t.path); err != nil {
		if os.IsNotExist(err) {
			return backup.NewError(backup.ErrValidation, "backup path does not exist", err)
		}
		return backup.NewError(backup.ErrValidation, "failed to check backup path", err)
	} else if !info.IsDir() {
		return backup.NewError(backup.ErrValidation, "backup path is not a directory", nil)
	}

	// Check if path is writable
	tmpFile := filepath.Join(t.path, ".write_test")
	f, err := os.Create(tmpFile)
	if err != nil {
		return backup.NewError(backup.ErrValidation, "backup path is not writable", err)
	}
	f.Close()
	os.Remove(tmpFile)

	// Check available disk space
	availableBytes, err := diskmanager.GetAvailableSpace(t.path)
	if err != nil {
		return backup.NewError(backup.ErrMedia, "failed to check disk space", err)
	}

	// Convert available bytes to gigabytes
	availableGB := float64(availableBytes) / (1024 * 1024 * 1024)

	// Ensure at least 1GB free space is available
	if availableGB < 1.0 {
		return backup.NewError(backup.ErrInsufficientSpace, fmt.Sprintf("insufficient disk space: %.1f GB available, minimum 1 GB required", availableGB), nil)
	}

	if t.logger != nil {
		t.logger.Printf("Available disk space at backup location: %.1f GB", availableGB)
	}

	return nil
}
