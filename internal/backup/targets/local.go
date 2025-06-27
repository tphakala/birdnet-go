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
	"github.com/tphakala/birdnet-go/internal/errors"
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
	"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
	"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
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
	return errors.New(lastErr).
		Component("backup").
		Category(errors.CategoryFileIO).
		Context("operation", "retry_operation").
		Context("max_retries", maxRetries).
		Build()
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
		return errors.Newf("path is required for local target").
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "validate_path").
			Build()
	}

	// Check path length
	if len(path) > maxPathLength {
		return errors.Newf("path length exceeds maximum allowed (%d characters)", maxPathLength).
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "validate_path").
			Context("path_length", len(path)).
			Context("max_length", maxPathLength).
			Build()
	}

	// Clean and normalize the path
	cleanPath := filepath.Clean(path)

	// Check for directory traversal attempts
	if strings.Contains(cleanPath, "..") {
		return errors.Newf("path must not contain directory traversal sequences").
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "validate_path").
			Context("path", cleanPath).
			Build()
	}

	// Convert to absolute path for validation
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "resolve_absolute_path").
			Context("path", cleanPath).
			Build()
	}

	// Check for symlinks
	fi, err := os.Lstat(absPath)
	if err == nil && (fi.Mode()&os.ModeSymlink) != 0 {
		return errors.Newf("symlinks are not allowed in backup path").
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "validate_path").
			Context("path", absPath).
			Build()
	}

	// Windows-specific checks
	if runtime.GOOS == "windows" {
		// Check for reserved names
		parts := strings.Split(cleanPath, string(os.PathSeparator))
		for _, part := range parts {
			baseName := strings.ToUpper(strings.Split(part, ".")[0])
			if windowsReservedNames[baseName] {
				return errors.Newf("path contains reserved name: %s", part).
					Component("backup").
					Category(errors.CategoryValidation).
					Context("operation", "validate_path").
					Context("reserved_name", part).
					Build()
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
				return errors.Newf("path is within restricted directory: %s", r).
					Component("backup").
					Category(errors.CategoryValidation).
					Context("operation", "validate_path").
					Context("restricted_directory", r).
					Context("path", absPath).
					Build()
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
		return nil, errors.New(err).
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "create_local_target").
			Context("path", cleanPath).
			Build()
	}

	// Create backup directory if it doesn't exist with restrictive permissions
	if err := os.MkdirAll(absPath, dirPermissions); err != nil {
		return nil, errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "create_backup_directory").
			Context("path", absPath).
			Build()
	}

	// Ensure directory has correct permissions even if it already existed
	if err := os.Chmod(absPath, dirPermissions); err != nil {
		return nil, errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "set_directory_permissions").
			Context("path", absPath).
			Context("permissions", fmt.Sprintf("%o", dirPermissions)).
			Build()
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
		return errors.New(err).
			Component("backup").
			Category(errors.CategorySystem).
			Context("operation", "store_backup").
			Context("error_type", "cancelled").
			Build()
	}

	if t.debug {
		t.logger.Printf("🔄 Storing backup from %s to local target", sourcePath)
	}

	// Validate source file
	srcInfo, err := os.Stat(sourcePath)
	if err != nil {
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "stat_source_file").
			Context("source_path", sourcePath).
			Build()
	}

	// Check file size limit
	if srcInfo.Size() > maxBackupSize {
		return errors.Newf("backup file too large: %d bytes (max %d bytes)", srcInfo.Size(), maxBackupSize).
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "validate_file_size").
			Context("file_size", srcInfo.Size()).
			Context("max_size", maxBackupSize).
			Build()
	}

	// Check available space
	availableBytes, err := diskmanager.GetAvailableSpace(t.path)
	if err != nil {
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryDiskUsage).
			Context("operation", "check_available_space").
			Context("path", t.path).
			Build()
	}

	// Ensure we have enough space (file size + 10% buffer)
	requiredSpace := uint64(float64(srcInfo.Size()) * 1.1)
	if availableBytes < requiredSpace {
		return errors.Newf("insufficient disk space: need %d bytes, have %d bytes", requiredSpace, availableBytes).
			Component("backup").
			Category(errors.CategoryDiskUsage).
			Context("operation", "check_disk_space").
			Context("required_bytes", requiredSpace).
			Context("available_bytes", availableBytes).
			Build()
	}

	// Marshal metadata
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "marshal_metadata").
			Build()
	}

	// Copy the backup file with retries and atomic operations
	dstPath := filepath.Join(t.path, filepath.Base(sourcePath))
	err = t.withRetry(func() error {
		return atomicWriteFile(dstPath, "backup-*.tmp", filePermissions, func(tempFile *os.File) error {
			srcFile, err := os.Open(sourcePath)
			if err != nil {
				return errors.New(err).
					Component("backup").
					Category(errors.CategoryFileIO).
					Context("operation", "open_source_file").
					Context("source_path", sourcePath).
					Build()
			}
			defer srcFile.Close()

			// Create a buffered copy with context cancellation
			copyDone := make(chan error, 1)
			go func() {
				copyDone <- copyFile(tempFile, srcFile)
			}()

			select {
			case <-ctx.Done():
				return errors.New(ctx.Err()).
					Component("backup").
					Category(errors.CategorySystem).
					Context("operation", "copy_backup_file").
					Context("error_type", "cancelled").
					Build()
			case err := <-copyDone:
				if err != nil {
					return errors.New(err).
						Component("backup").
						Category(errors.CategoryFileIO).
						Context("operation", "copy_backup_file").
						Context("source_path", sourcePath).
						Context("dest_path", dstPath).
						Build()
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
		return errors.New(err).
			Component("backup").
			Category(errors.CategorySystem).
			Context("operation", "verify_backup").
			Context("error_type", "cancelled").
			Build()
	}

	// Verify file exists and has correct size
	info, err := os.Stat(backupPath)
	if err != nil {
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "verify_backup_file").
			Context("backup_path", backupPath).
			Build()
	}

	if info.Size() != expectedSize {
		return errors.Newf("backup file size mismatch: expected %d, got %d", expectedSize, info.Size()).
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "verify_backup_size").
			Context("expected_size", expectedSize).
			Context("actual_size", info.Size()).
			Build()
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
		return nil, errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "list_backups").
			Context("path", t.path).
			Build()
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
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "delete_backup_file").
			Context("backup_id", backupID).
			Context("path", backupPath).
			Build()
	}

	// Delete metadata file
	if err := os.Remove(metadataPath); err != nil && !os.IsNotExist(err) {
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "delete_metadata_file").
			Context("backup_id", backupID).
			Context("path", metadataPath).
			Build()
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
			return errors.New(err).
				Component("backup").
				Category(errors.CategoryValidation).
				Context("operation", "validate_target").
				Context("path", t.path).
				Build()
		}
		t.path = absPath
	}

	// Check if path exists and is a directory
	if info, err := os.Stat(t.path); err != nil {
		if os.IsNotExist(err) {
			return errors.Newf("backup path does not exist: %s", t.path).
				Component("backup").
				Category(errors.CategoryValidation).
				Context("operation", "validate_target").
				Context("path", t.path).
				Build()
		}
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "validate_target_stat").
			Context("path", t.path).
			Build()
	} else if !info.IsDir() {
		return errors.Newf("backup path is not a directory: %s", t.path).
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "validate_target").
			Context("path", t.path).
			Build()
	}

	// Check if path is writable
	tmpFile := filepath.Join(t.path, "write_test")
	f, err := os.Create(tmpFile)
	if err != nil {
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "validate_target_writable").
			Context("path", t.path).
			Build()
	}
	f.Close()
	os.Remove(tmpFile)

	// Check available disk space
	availableBytes, err := diskmanager.GetAvailableSpace(t.path)
	if err != nil {
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryDiskUsage).
			Context("operation", "check_disk_space_validation").
			Context("path", t.path).
			Build()
	}

	// Convert available bytes to gigabytes
	availableGB := float64(availableBytes) / (1024 * 1024 * 1024)

	// Ensure at least 1GB free space is available
	if availableGB < 1.0 {
		return errors.Newf("insufficient disk space: %.1f GB available, minimum 1 GB required", availableGB).
			Component("backup").
			Category(errors.CategoryDiskUsage).
			Context("operation", "validate_disk_space").
			Context("available_gb", availableGB).
			Context("required_gb", 1.0).
			Build()
	}

	if t.logger != nil {
		t.logger.Printf("💾 Available disk space at backup location: %.1f GB", availableGB)
	}

	return nil
}
