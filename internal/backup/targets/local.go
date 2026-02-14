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
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

// Constants for file operations - use shared constants from common.go
// Local-specific constants only
const (
	localBaseBackoffDelay = 100 * time.Millisecond // Base delay for exponential backoff
)

// Windows-specific reserved names
var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true,
	"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
	"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// LocalTarget implements the backup.Target interface for local filesystem storage
type LocalTarget struct {
	path  string
	debug bool
	log   logger.Logger
	sfs   *securefs.SecureFS // OS-level sandboxed filesystem (Go 1.24+)
}

// LocalTargetConfig holds configuration for the local filesystem target
type LocalTargetConfig struct {
	Path  string
	Debug bool
}

// atomicWriteSecure writes data to a temporary file and renames it atomically using securefs.
// The relativePath should be relative to the LocalTarget's backup directory.
func (t *LocalTarget) atomicWriteSecure(relativePath string, perm os.FileMode, write func(*os.File) error) error {
	// Generate unique temp file name
	tempName := fmt.Sprintf(".tmp-%d-%s", time.Now().UnixNano(), filepath.Base(relativePath))

	// Create temp file using securefs (sandboxed to backup directory)
	tempFile, err := t.sfs.OpenFile(tempName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}

	// Track success for cleanup
	success := false
	defer func() {
		if !success {
			if err := tempFile.Close(); err != nil {
				t.log.Debug("local: failed to close temp file", logger.Error(err))
			}
			if err := t.sfs.Remove(tempName); err != nil {
				t.log.Debug("local: failed to remove temp file", logger.Error(err))
			}
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

	// Perform atomic rename within the securefs sandbox (Go 1.25+)
	if err := t.sfs.Rename(tempName, relativePath); err != nil {
		return fmt.Errorf("failed to rename temporary file: %w", err)
	}

	success = true
	return nil
}

// isTransientError determines if an error is likely transient
// Delegates to the shared implementation in common.go
func isTransientError(err error) bool {
	return IsTransientError(err)
}

// backoffDuration calculates exponential backoff duration
func backoffDuration(attempt int) time.Duration {
	return localBaseBackoffDelay * time.Duration(1<<uint(attempt)) // #nosec G115 -- attempt is bounded by retry logic, safe conversion
}

// withRetry executes an operation with retry logic for transient errors
func (t *LocalTarget) withRetry(op func() error) error {
	var lastErr error
	for i := range DefaultMaxRetries {
		if err := op(); err == nil {
			return nil
		} else if !isTransientError(err) {
			return err
		} else {
			lastErr = err
			if t.debug {
				t.log.Info(fmt.Sprintf("Retrying operation after error: %v (attempt %d/%d)", err, i+1, DefaultMaxRetries))
			}
		}
		time.Sleep(backoffDuration(i))
	}
	return errors.New(lastErr).
		Component("backup").
		Category(errors.CategoryFileIO).
		Context("operation", "retry_operation").
		Context("max_retries", DefaultMaxRetries).
		Build()
}

// copyFile performs an optimized file copy operation
func copyFile(dst, src *os.File) error {
	buf := make([]byte, CopyBufferSize)
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
	if len(path) > MaxComponentLength {
		return errors.Newf("path length exceeds maximum allowed (%d characters)", MaxComponentLength).
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "validate_path").
			Context("path_length", len(path)).
			Context("max_length", MaxComponentLength).
			Build()
	}

	// Clean and normalize the path
	cleanPath := filepath.Clean(path)

	// Use filepath.IsLocal for comprehensive path validation (prevents CVE-2023-45284, CVE-2023-45283)
	if !filepath.IsLocal(cleanPath) {
		return errors.Newf("path is not local or contains traversal sequences").
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
		parts := strings.SplitSeq(cleanPath, string(os.PathSeparator))
		for part := range parts {
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
func NewLocalTarget(config LocalTargetConfig, lg logger.Logger) (*LocalTarget, error) {
	// Validate and clean the path
	if err := validatePath(config.Path); err != nil {
		return nil, err
	}
	cleanPath := filepath.Clean(config.Path)

	if lg == nil {
		lg = logger.Global().Module("backup")
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
	if err := os.MkdirAll(absPath, PermDir); err != nil {
		return nil, errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "create_backup_directory").
			Context("path", absPath).
			Build()
	}

	// Ensure directory has correct permissions even if it already existed
	if err := os.Chmod(absPath, PermDir); err != nil {
		return nil, errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "set_directory_permissions").
			Context("path", absPath).
			Context("permissions", fmt.Sprintf("%o", PermDir)).
			Build()
	}

	// Create OS-level sandboxed filesystem using Go 1.24's os.Root
	sfs, err := securefs.New(absPath)
	if err != nil {
		return nil, errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "create_secure_filesystem").
			Context("path", absPath).
			Build()
	}

	return &LocalTarget{
		path:  absPath,
		debug: config.Debug,
		log:   lg.Module("local"),
		sfs:   sfs,
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
		t.log.Info(fmt.Sprintf("🔄 Storing backup from %s to local target", sourcePath))
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
	if srcInfo.Size() > MaxBackupSizeBytes {
		return errors.Newf("backup file too large: %d bytes (max %d bytes)", srcInfo.Size(), MaxBackupSizeBytes).
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "validate_file_size").
			Context("file_size", srcInfo.Size()).
			Context("max_size", MaxBackupSizeBytes).
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
	requiredSpace := uint64(float64(srcInfo.Size()) * backup.SpaceBufferMultiplier)
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

	// Copy the backup file with retries and atomic operations using securefs
	destFileName := filepath.Base(sourcePath)
	err = t.withRetry(func() error {
		return t.atomicWriteSecure(destFileName, PermFile, func(tempFile *os.File) error {
			// Open source file (from trusted internal backup manager temp directory)
			srcFile, err := os.Open(sourcePath) //nolint:gosec // G304 - sourcePath is a trusted internal temp path from backup manager
			if err != nil {
				return errors.New(err).
					Component("backup").
					Category(errors.CategoryFileIO).
					Context("operation", "open_source_file").
					Context("source_path", sourcePath).
					Build()
			}
			defer func() {
				if err := srcFile.Close(); err != nil {
					t.log.Debug("local: failed to close source file", logger.String("path", sourcePath), logger.Error(err))
				}
			}()

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
						Context("dest_file", destFileName).
						Build()
				}
			}

			return nil
		})
	})

	if err != nil {
		return err
	}

	// Store metadata with retries and atomic operations using securefs
	metadataFileName := destFileName + ".meta"
	err = t.withRetry(func() error {
		return t.atomicWriteSecure(metadataFileName, PermFile, func(tempFile *os.File) error {
			_, err := tempFile.Write(metadataBytes)
			return err
		})
	})

	if err != nil {
		return err
	}

	// Verify the backup (construct full path for verification)
	dstPath := filepath.Join(t.path, destFileName)
	if err := t.verifyBackup(ctx, dstPath, srcInfo.Size()); err != nil {
		return err
	}

	if t.debug {
		t.log.Info(fmt.Sprintf("✅ Successfully stored backup %s with metadata", filepath.Base(sourcePath)))
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
		t.log.Info("🔄 Listing backups in local target")
	}

	// Use securefs for directory listing (sandboxed to backup path)
	entries, err := t.sfs.ReadDir(".")
	if err != nil {
		return nil, errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "list_backups").
			Context("path", t.path).
			Build()
	}

	backups := make([]backup.BackupInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta") {
			continue
		}

		// Get the backup file name by removing .meta suffix
		backupName := strings.TrimSuffix(entry.Name(), ".meta")

		// Check if the corresponding backup file exists (using securefs)
		if _, err := t.sfs.Stat(backupName); err != nil {
			if t.debug {
				t.log.Info(fmt.Sprintf("⚠️ Skipping orphaned metadata file %s: backup file not found", entry.Name()))
			}
			continue
		}

		// Read metadata file using securefs (sandboxed access)
		metadataFile, err := t.sfs.Open(entry.Name())
		if err != nil {
			t.log.Info(fmt.Sprintf("⚠️ Skipping backup %s: %v", backupName, err))
			continue
		}

		var metadata backup.Metadata
		decoder := json.NewDecoder(metadataFile)
		if err := decoder.Decode(&metadata); err != nil {
			if err := metadataFile.Close(); err != nil {
				t.log.Info(fmt.Sprintf("local: failed to close metadata file %s: %v", entry.Name(), err))
			}
			t.log.Info(fmt.Sprintf("⚠️ Invalid metadata in backup %s: %v", backupName, err))
			continue
		}
		if err := metadataFile.Close(); err != nil {
			t.log.Info(fmt.Sprintf("local: failed to close metadata file %s: %v", entry.Name(), err))
		}

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
		t.log.Info(fmt.Sprintf("🔄 Deleting backup %s from local target", backupID))
	}

	// Delete both the backup file and its metadata using securefs (sandboxed)
	metadataName := backupID + ".meta"

	// Delete backup file
	if err := t.sfs.Remove(backupID); err != nil && !os.IsNotExist(err) {
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "delete_backup_file").
			Context("backup_id", backupID).
			Build()
	}

	// Delete metadata file
	if err := t.sfs.Remove(metadataName); err != nil && !os.IsNotExist(err) {
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "delete_metadata_file").
			Context("backup_id", backupID).
			Build()
	}

	if t.debug {
		t.log.Info(fmt.Sprintf("✅ Successfully deleted backup %s", backupID))
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

	// Check if path is writable using securefs (sandboxed)
	testFileName := ".write_test"
	f, err := t.sfs.OpenFile(testFileName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, PermFile)
	if err != nil {
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "validate_writability").
			Context("path", t.path).
			Build()
	}
	if err := f.Close(); err != nil {
		t.log.Debug("local: failed to close test file", logger.Error(err))
	}
	if err := t.sfs.Remove(testFileName); err != nil {
		t.log.Debug("local: failed to remove test file", logger.Error(err))
	}

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
	availableGB := float64(availableBytes) / backup.GB

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

	if t.log != nil {
		t.log.Info(fmt.Sprintf("💾 Available disk space at backup location: %.1f GB", availableGB))
	}

	return nil
}
