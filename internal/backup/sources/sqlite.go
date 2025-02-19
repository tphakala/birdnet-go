// Package sources provides backup source implementations
package sources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/tphakala/birdnet-go/internal/backup"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
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

// Operation represents a backup operation with tracking information
type Operation struct {
	ID        string
	StartTime time.Time
}

// NewOperation creates a new operation with a unique ID
func NewOperation() *Operation {
	return &Operation{
		ID:        fmt.Sprintf("op-%d", time.Now().UnixNano()),
		StartTime: time.Now(),
	}
}

// logf logs a message with operation context
func (s *SQLiteSource) logf(op *Operation, format string, args ...interface{}) {
	if op != nil {
		elapsed := time.Since(op.StartTime).Round(time.Millisecond)
		s.logger.Printf("[%s +%v] %s", op.ID, elapsed, fmt.Sprintf(format, args...))
	} else {
		s.logger.Printf(format, args...)
	}
}

// validateTempDir validates the temporary directory configuration
func (s *SQLiteSource) validateTempDir(tempDir string) error {
	info, err := os.Stat(tempDir)
	if err != nil {
		if os.IsNotExist(err) {
			return backup.NewError(backup.ErrConfig, "temporary directory does not exist", err)
		}
		return backup.NewError(backup.ErrConfig, "failed to access temporary directory", err)
	}
	if !info.IsDir() {
		return backup.NewError(backup.ErrConfig, "specified temporary directory path is not a directory", nil)
	}
	// Check if directory is writable by attempting to create a test file
	testFile := filepath.Join(tempDir, fmt.Sprintf(".test-%d", time.Now().UnixNano()))
	f, err := os.OpenFile(testFile, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return backup.NewError(backup.ErrConfig, "temporary directory is not writable", err)
	}
	f.Close()
	os.Remove(testFile)
	return nil
}

// validateConfig checks if SQLite backup is enabled and properly configured
func (s *SQLiteSource) validateConfig() (string, error) {
	if !s.config.Output.SQLite.Enabled {
		return "", backup.NewError(backup.ErrConfig, "sqlite is not enabled", nil)
	}

	dbPath := s.config.Output.SQLite.Path
	if dbPath == "" {
		return "", backup.NewError(backup.ErrConfig, "sqlite path is not configured", nil)
	}

	// Convert to absolute path if necessary
	if !filepath.IsAbs(dbPath) {
		absPath, err := filepath.Abs(dbPath)
		if err != nil {
			return "", backup.NewError(backup.ErrConfig, "failed to resolve absolute database path", err)
		}
		dbPath = absPath
		s.logger.Printf("Converted database path to absolute: %s", dbPath)
	}

	// Validate temp directory if specified
	if s.config.Output.SQLite.TempDir != "" {
		if err := s.validateTempDir(s.config.Output.SQLite.TempDir); err != nil {
			return "", err
		}
	}

	return dbPath, nil
}

// verifySourceDatabase checks if the source database exists and is accessible
func (s *SQLiteSource) verifySourceDatabase(dbPath string) error {
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return backup.NewError(backup.ErrNotFound, "database file not found", err)
		}
		if isMediaError(err) {
			return backup.NewError(backup.ErrMedia, "database file not accessible", err)
		}
		return backup.NewError(backup.ErrIO, "database file not accessible", err)
	}
	s.logger.Printf("Verified database file exists and is accessible")
	return nil
}

// createBackupDirectory creates a temporary directory for the backup
func (s *SQLiteSource) createBackupDirectory() (string, error) {
	s.logger.Printf("Creating temporary directory for backup...")

	// Use configured temp directory if available
	baseDir := ""
	if s.config.Output.SQLite.TempDir != "" {
		baseDir = s.config.Output.SQLite.TempDir
		// Ensure the directory exists
		if err := os.MkdirAll(baseDir, 0o755); err != nil {
			return "", fmt.Errorf("failed to create temp directory: %w", err)
		}
	}

	tempDir, err := os.MkdirTemp(baseDir, "birdnet-go-backup-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	s.logger.Printf("Created temporary directory: %s", tempDir)
	return tempDir, nil
}

// generateBackupPath generates the backup file path with timestamp
func (s *SQLiteSource) generateBackupPath(tempDir string) string {
	timestamp := time.Now().UTC().Format("20060102150405")
	backupFilename := fmt.Sprintf("birdnet-go-sqlite-%s.db", timestamp)
	backupPath := filepath.Join(tempDir, backupFilename)
	s.logger.Printf("Generated backup path: %s", backupPath)
	return backupPath
}

// DatabaseConnection represents a managed database connection
type DatabaseConnection struct {
	DB     *gorm.DB
	sqlDB  *sql.DB
	closed bool
}

// Close safely closes the database connection
func (dc *DatabaseConnection) Close() error {
	if dc.closed {
		return nil
	}
	if dc.sqlDB != nil {
		if err := dc.sqlDB.Close(); err != nil {
			return fmt.Errorf("failed to close database connection: %w", err)
		}
		dc.closed = true
	}
	return nil
}

// openDatabase opens a database connection with the given path
func (s *SQLiteSource) openDatabase(dbPath string, readOnly bool) (*DatabaseConnection, error) {
	s.logger.Printf("Opening database: %s", dbPath)

	dsn := dbPath
	if readOnly {
		dsn += "?mode=ro"
	}

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		if isMediaError(err) {
			return nil, backup.NewError(backup.ErrMedia, "failed to open database", err)
		}
		return nil, backup.NewError(backup.ErrDatabase, "failed to open database", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, backup.NewError(backup.ErrDatabase, "failed to get underlying database connection", err)
	}

	// Verify connection
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		if isMediaError(err) {
			return nil, backup.NewError(backup.ErrMedia, "failed to verify database connection", err)
		}
		return nil, backup.NewError(backup.ErrDatabase, "failed to verify database connection", err)
	}

	return &DatabaseConnection{
		DB:    db,
		sqlDB: sqlDB,
	}, nil
}

// verifyDatabaseIntegrity checks database integrity
func (s *SQLiteSource) verifyDatabaseIntegrity(db *gorm.DB) error {
	var result string
	if err := db.Raw("PRAGMA integrity_check").Scan(&result).Error; err != nil {
		if isMediaError(err) {
			return backup.NewError(backup.ErrMedia, "integrity check failed", err)
		}
		return backup.NewError(backup.ErrDatabase, "integrity check failed", err)
	}

	if result != "ok" {
		return backup.NewError(backup.ErrCorruption, fmt.Sprintf("integrity check failed: %s", result), nil)
	}
	return nil
}

// verifyDiskSpace checks if there's enough space for the backup
func (s *SQLiteSource) verifyDiskSpace(dbPath, tempDir string) error {
	// Get the size of the source database
	dbInfo, err := os.Stat(dbPath)
	if err != nil {
		if isMediaError(err) {
			return backup.NewError(backup.ErrMedia, "failed to get database file info", err)
		}
		return backup.NewError(backup.ErrIO, "failed to get database file info", err)
	}
	dbSize := dbInfo.Size()

	// Get available space in the temp directory
	availableSpace, err := diskmanager.GetAvailableSpace(tempDir)
	if err != nil {
		if isMediaError(err) {
			return backup.NewError(backup.ErrMedia, "failed to get available disk space", err)
		}
		return backup.NewError(backup.ErrIO, "failed to get available disk space", err)
	}

	// We need slightly more than 1x the database size for VACUUM INTO
	// Adding 10% margin for safety
	requiredSpace := uint64(float64(dbSize) * 1.1)

	if availableSpace < requiredSpace {
		return backup.NewError(backup.ErrInsufficientSpace,
			fmt.Sprintf("insufficient disk space: need %d bytes, have %d bytes available",
				requiredSpace, availableSpace), nil)
	}

	// If available space is less than 2x database size, log a warning
	if availableSpace < uint64(dbSize)*2 {
		s.logger.Printf("WARNING: Available disk space (%d bytes) is less than 2x database size (%d bytes). "+
			"This might cause issues with future backups.", availableSpace, dbSize)
	}

	return nil
}

// BackupError represents a specific backup error type
type BackupError struct {
	Op      string // Operation that failed
	Path    string // Path related to the error
	Err     error  // Original error
	IsMedia bool   // Whether it's a media-related error (SD card, etc.)
}

func (e *BackupError) Error() string {
	if e.IsMedia {
		return fmt.Sprintf("media error during %s at %s: %v", e.Op, e.Path, e.Err)
	}
	return fmt.Sprintf("error during %s at %s: %v", e.Op, e.Path, e.Err)
}

// isMediaError checks if an error is related to media issues (SD card, etc.)
func isMediaError(err error) bool {
	if err == nil {
		return false
	}

	// Check for specific error types that indicate media issues
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		// Platform-specific error detection
		if runtime.GOOS == "windows" {
			var errno syscall.Errno
			if errors.As(pathErr.Err, &errno) {
				// Windows error codes from syscall/types_windows.go
				const (
					ERROR_NOT_READY      syscall.Errno = 21
					ERROR_NO_MEDIA       syscall.Errno = 55
					ERROR_WRITE_PROTECT  syscall.Errno = 19
					ERROR_DISK_FULL      syscall.Errno = 112
					ERROR_DEVICE_REMOVED syscall.Errno = 1617
				)

				switch errno {
				case ERROR_NOT_READY, // Device not ready
					ERROR_NO_MEDIA,       // No media
					ERROR_WRITE_PROTECT,  // Write protected
					ERROR_DISK_FULL,      // Disk full
					ERROR_DEVICE_REMOVED: // Device removed
					return true
				}
			}
		} else {
			// Unix-like systems (Linux, macOS)
			var errno syscall.Errno
			if errors.As(pathErr.Err, &errno) {
				switch errno {
				case syscall.EIO, // I/O error
					syscall.ENOSPC, // No space left on device
					syscall.EROFS,  // Read-only file system
					syscall.ENODEV, // No such device
					syscall.ENXIO:  // No such device or address
					return true
				}

				// Linux-specific error detection
				if runtime.GOOS == "linux" {
					// ENOMEDIUM is Linux-specific
					if errno == 0x7B { // ENOMEDIUM constant
						return true
					}
				}
			}
		}
	}

	// Check error string for common SD card issues (platform-independent)
	errStr := strings.ToLower(err.Error())
	mediaErrors := []string{
		"input/output error",
		"no space left",
		"read-only",
		"device not ready",
		"no medium",
		"media removed",
		"device has been removed",
	}

	for _, mediaErr := range mediaErrors {
		if strings.Contains(errStr, mediaErr) {
			return true
		}
	}

	return false
}

// createLockFile creates a lock file to track backup in progress
func (s *SQLiteSource) createLockFile(backupPath string) (string, error) {
	lockPath := backupPath + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			// Check if the lock file is stale (older than 1 hour)
			if info, err := os.Stat(lockPath); err == nil {
				if time.Since(info.ModTime()) > time.Hour {
					// Remove stale lock file
					os.Remove(lockPath)
					return s.createLockFile(backupPath)
				}
			}
			return "", backup.NewError(backup.ErrLocked, "backup already in progress", nil)
		}
		if isMediaError(err) {
			return "", backup.NewError(backup.ErrMedia, "failed to create lock file", err)
		}
		return "", backup.NewError(backup.ErrIO, "failed to create lock file", err)
	}
	defer lockFile.Close()

	// Write PID to lock file
	if _, err := fmt.Fprintf(lockFile, "%d", os.Getpid()); err != nil {
		os.Remove(lockPath)
		if isMediaError(err) {
			return "", backup.NewError(backup.ErrMedia, "failed to write lock file", err)
		}
		return "", backup.NewError(backup.ErrIO, "failed to write lock file", err)
	}

	return lockPath, nil
}

// cleanupPartialBackup removes incomplete backup files
func (s *SQLiteSource) cleanupPartialBackup(backupPath, lockPath string) {
	if lockPath != "" {
		if err := os.Remove(lockPath); err != nil {
			s.logger.Printf("WARNING: Failed to remove lock file: %v", err)
		}
	}
	if backupPath != "" {
		if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
			s.logger.Printf("WARNING: Failed to remove partial backup: %v", err)
		}
	}
}

// setupSignalHandler sets up handling for system signals
func (s *SQLiteSource) setupSignalHandler(ctx context.Context, cleanup func()) context.Context {
	ctx, cancel := context.WithCancel(ctx)
	sigChan := make(chan os.Signal, 1)

	// Platform-specific signal handling
	if runtime.GOOS == "windows" {
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	} else {
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	}

	go func() {
		select {
		case sig := <-sigChan:
			s.logger.Printf("Received signal %v, cleaning up...", sig)
			cleanup()
			cancel()
		case <-ctx.Done():
			return
		}
	}()

	return ctx
}

// withDatabase executes a function with a managed database connection
func (s *SQLiteSource) withDatabase(dbPath string, readOnly bool, fn func(*DatabaseConnection) error) error {
	conn, err := s.openDatabase(dbPath, readOnly)
	if err != nil {
		return err
	}
	defer conn.Close()

	return fn(conn)
}

// verifyDatabase performs comprehensive database verification
func (s *SQLiteSource) verifyDatabase(dbPath string, readOnly bool) error {
	// First verify the file exists and is accessible
	if err := s.verifySourceDatabase(dbPath); err != nil {
		return err
	}

	// Then verify database connection and integrity
	return s.withDatabase(dbPath, readOnly, func(conn *DatabaseConnection) error {
		return s.verifyDatabaseIntegrity(conn.DB)
	})
}

// performBackupOperation executes the actual backup operation with timeout
func (s *SQLiteSource) performBackupOperation(ctx context.Context, sourceConn *DatabaseConnection, backupPath string) error {
	backupCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		s.logger.Printf("Performing VACUUM INTO backup...")
		vacuumSQL := fmt.Sprintf("VACUUM INTO '%s'", backupPath)
		done <- sourceConn.DB.Exec(vacuumSQL).Error
	}()

	select {
	case <-backupCtx.Done():
		return backup.NewError(backup.ErrTimeout, "backup operation timed out", backupCtx.Err())
	case err := <-done:
		if err != nil {
			if isMediaError(err) {
				return backup.NewError(backup.ErrMedia, "backup operation failed", err)
			}
			return backup.NewError(backup.ErrDatabase, "backup operation failed", err)
		}
		return nil
	}
}

// verifyBackupIntegrity verifies the integrity of the backup file
func (s *SQLiteSource) verifyBackupIntegrity(backupPath string) error {
	// First verify the file exists and is readable
	if err := s.verifyBackupFile(backupPath); err != nil {
		return err
	}

	// Then verify database integrity
	return s.withDatabase(backupPath, true, func(conn *DatabaseConnection) error {
		return s.verifyDatabaseIntegrity(conn.DB)
	})
}

// Backup performs a backup of the SQLite database using VACUUM INTO
func (s *SQLiteSource) Backup(ctx context.Context) (string, error) {
	op := NewOperation()
	s.logf(op, "Starting SQLite backup operation")

	var lockPath string
	var tempDir string
	var backupPath string

	// Setup cleanup function for error cases
	cleanup := func() {
		if lockPath != "" {
			s.cleanupPartialBackup(backupPath, lockPath)
		}
		// Only remove tempDir if we have an error
		if tempDir != "" && backupPath == "" {
			os.RemoveAll(tempDir)
		}
	}

	// Setup signal handler
	ctx = s.setupSignalHandler(ctx, cleanup)
	// Only cleanup on error, not on successful completion
	defer func() {
		if backupPath == "" {
			cleanup()
		}
	}()

	// Validate configuration and get database path
	dbPath, err := s.validateConfig()
	if err != nil {
		return "", fmt.Errorf("configuration validation failed: %w", err)
	}

	s.logf(op, "Initiating backup for database: %s", dbPath)

	// Verify source database
	if err := s.verifyDatabase(dbPath, false); err != nil {
		return "", fmt.Errorf("database verification failed: %w", err)
	}

	// Create temporary directory
	tempDir, err = s.createBackupDirectory()
	if err != nil {
		return "", fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Verify disk space before proceeding
	if err := s.verifyDiskSpace(dbPath, tempDir); err != nil {
		return "", fmt.Errorf("disk space verification failed: %w", err)
	}

	// Generate backup path and create lock file
	backupPath = s.generateBackupPath(tempDir)
	lockPath, err = s.createLockFile(backupPath)
	if err != nil {
		return "", fmt.Errorf("failed to create lock file: %w", err)
	}

	// Perform backup with source database
	err = s.withDatabase(dbPath, false, func(sourceConn *DatabaseConnection) error {
		// Perform the backup operation
		if err := s.performBackupOperation(ctx, sourceConn, backupPath); err != nil {
			return fmt.Errorf("backup operation failed: %w", err)
		}

		// Verify backup integrity
		if err := s.verifyBackupIntegrity(backupPath); err != nil {
			return fmt.Errorf("backup integrity verification failed: %w", err)
		}

		return nil
	})

	if err != nil {
		// Reset backupPath to trigger cleanup
		backupPath = ""
		return "", err
	}

	// Remove lock file after successful backup
	if err := os.Remove(lockPath); err != nil {
		s.logf(op, "WARNING: Failed to remove lock file: %v", err)
	}
	lockPath = "" // Prevent cleanup from removing the lock file again

	s.logf(op, "SQLite backup completed successfully")
	return backupPath, nil
}

// verifyBackupFile checks if the backup file was created and is readable
func (s *SQLiteSource) verifyBackupFile(backupPath string) error {
	s.logger.Printf("Verifying backup file...")
	info, err := os.Stat(backupPath)
	if err != nil {
		return fmt.Errorf("backup file was not created successfully: %w", err)
	}
	s.logger.Printf("Verified backup file, size: %d bytes", info.Size())
	return nil
}

// Validate checks if the source configuration is valid
func (s *SQLiteSource) Validate() error {
	// Validate configuration and get database path
	dbPath, err := s.validateConfig()
	if err != nil {
		return err
	}

	// Verify database with read-only access
	return s.verifyDatabase(dbPath, true)
}
