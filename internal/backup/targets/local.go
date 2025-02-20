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
)

// Windows-specific reserved names
var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
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
		t.logger.Printf("🔄 Storing backup from %s to local target", sourcePath)
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

	// Marshal metadata
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return backup.NewError(backup.ErrIO, "failed to marshal metadata", err)
	}

	// Copy the backup file with retries and atomic operations
	dstPath := filepath.Join(t.path, filepath.Base(sourcePath))
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

	// Store metadata with retries and atomic operations
	metadataPath := dstPath + ".meta"
	err = t.withRetry(func() error {
		return atomicWriteFile(metadataPath, "metadata-*.tmp", filePermissions, func(tempFile *os.File) error {
			_, err := tempFile.Write(metadataBytes)
			return err
		})
	})

	if err != nil {
		return err
	}

	// Verify the backup
	if err := t.verifyBackup(ctx, dstPath, srcInfo.Size()); err != nil {
		return err
	}

	if t.debug {
		t.logger.Printf("✅ Successfully stored backup %s with metadata", filepath.Base(sourcePath))
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

// List returns a list of available backups
func (t *LocalTarget) List(ctx context.Context) ([]backup.BackupInfo, error) {
	if t.debug {
		t.logger.Printf("🔄 Listing backups in local target")
	}

	entries, err := os.ReadDir(t.path)
	if err != nil {
		return nil, backup.NewError(backup.ErrIO, "failed to read backup directory", err)
	}

	var backups []backup.BackupInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta") {
			continue
		}

		// Get the backup file name by removing .meta suffix
		backupName := strings.TrimSuffix(entry.Name(), ".meta")
		backupPath := filepath.Join(t.path, backupName)

		// Check if the corresponding backup file exists
		if _, err := os.Stat(backupPath); err != nil {
			if t.debug {
				t.logger.Printf("⚠️ Skipping orphaned metadata file %s: backup file not found", entry.Name())
			}
			continue
		}

		// Read metadata file
		metadataPath := filepath.Join(t.path, entry.Name())
		metadataFile, err := os.Open(metadataPath)
		if err != nil {
			t.logger.Printf("⚠️ Skipping backup %s: %v", backupName, err)
			continue
		}

		var metadata backup.Metadata
		decoder := json.NewDecoder(metadataFile)
		if err := decoder.Decode(&metadata); err != nil {
			metadataFile.Close()
			t.logger.Printf("⚠️ Invalid metadata in backup %s: %v", backupName, err)
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
		t.logger.Printf("🔄 Deleting backup %s from local target", backupID)
	}

	// Delete both the backup file and its metadata
	backupPath := filepath.Join(t.path, backupID)
	metadataPath := backupPath + ".meta"

	// Delete backup file
	if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
		return backup.NewError(backup.ErrIO, "failed to delete backup file", err)
	}

	// Delete metadata file
	if err := os.Remove(metadataPath); err != nil && !os.IsNotExist(err) {
		return backup.NewError(backup.ErrIO, "failed to delete metadata file", err)
	}

	if t.debug {
		t.logger.Printf("✅ Successfully deleted backup %s", backupID)
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
	tmpFile := filepath.Join(t.path, "write_test")
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
		t.logger.Printf("💾 Available disk space at backup location: %.1f GB", availableGB)
	}

	return nil
}
