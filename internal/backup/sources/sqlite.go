// Package sources provides backup source implementations
package sources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-sqlite3"
	"github.com/tphakala/birdnet-go/internal/backup"
	"github.com/tphakala/birdnet-go/internal/conf"
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
	// Use the original database filename without extension as the source name
	dbPath := s.config.Output.SQLite.Path
	baseName := filepath.Base(dbPath)
	return strings.TrimSuffix(baseName, filepath.Ext(baseName))
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
	// Only log detailed operation messages if debug is enabled
	if op != nil {
		if s.config.Backup.Debug {
			elapsed := time.Since(op.StartTime).Round(time.Millisecond)
			s.logger.Printf("[%s +%v] %s", op.ID, elapsed, fmt.Sprintf(format, args...))
		} else {
			// For non-debug mode, only log start and completion messages with emojis
			msg := fmt.Sprintf(format, args...)
			if strings.Contains(msg, "Starting") {
				s.logger.Printf("🔄 Starting SQLite backup")
			} else if strings.Contains(msg, "completed successfully") {
				s.logger.Printf("✅ SQLite backup completed successfully")
			}
		}
	} else {
		s.logger.Printf(format, args...)
	}
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

	return nil
}

// DatabaseConnection represents a managed database connection
type DatabaseConnection struct {
	db     *sql.DB
	closed bool
}

// Close safely closes the database connection
func (dc *DatabaseConnection) Close() error {
	if dc.closed {
		return nil
	}
	if dc.db != nil {
		if err := dc.db.Close(); err != nil {
			return fmt.Errorf("failed to close database connection: %w", err)
		}
		dc.closed = true
	}
	return nil
}

// openDatabase opens a database connection with the given path
func (s *SQLiteSource) openDatabase(dbPath string, readOnly bool) (*DatabaseConnection, error) {
	// Build DSN with additional safety parameters
	dsn := dbPath
	if readOnly {
		dsn += "?mode=ro"
	}
	dsn += "&_busy_timeout=30000" // 30 second timeout
	dsn += "&_journal_mode=WAL"   // Ensure WAL mode
	dsn += "&_sync=NORMAL"        // Less aggressive syncing for better performance

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		if isMediaError(err) {
			return nil, backup.NewError(backup.ErrMedia, "failed to open database", err)
		}
		return nil, backup.NewError(backup.ErrDatabase, "failed to open database", err)
	}

	// Verify connection
	if err := db.Ping(); err != nil {
		db.Close()
		if isMediaError(err) {
			return nil, backup.NewError(backup.ErrMedia, "failed to verify database connection", err)
		}
		return nil, backup.NewError(backup.ErrDatabase, "failed to verify database connection", err)
	}

	return &DatabaseConnection{
		db: db,
	}, nil
}

// verifyDatabaseIntegrity checks database integrity
func (s *SQLiteSource) verifyDatabaseIntegrity(db *sql.DB) error {
	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
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
		return s.verifyDatabaseIntegrity(conn.db)
	})
}

// getDatabaseInfo retrieves and logs database information
func (s *SQLiteSource) getDatabaseInfo(db *sql.DB) (int, error) {
	var sourcePages int
	err := db.QueryRow("PRAGMA page_count").Scan(&sourcePages)
	if err != nil {
		if isMediaError(err) {
			return 0, backup.NewError(backup.ErrMedia, "failed to get page count", err)
		}
		return 0, backup.NewError(backup.ErrDatabase, "failed to get page count", err)
	}

	var pageSize int
	err = db.QueryRow("PRAGMA page_size").Scan(&pageSize)
	if err != nil {
		if isMediaError(err) {
			return 0, backup.NewError(backup.ErrMedia, "failed to get page size", err)
		}
		return 0, backup.NewError(backup.ErrDatabase, "failed to get page size", err)
	}

	var journalMode string
	err = db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		if isMediaError(err) {
			return 0, backup.NewError(backup.ErrMedia, "failed to get journal mode", err)
		}
		return 0, backup.NewError(backup.ErrDatabase, "failed to get journal mode", err)
	}

	return sourcePages, nil
}

// initializeBackupConnection sets up the backup connection between source and destination
func (s *SQLiteSource) initializeBackupConnection(srcDb, dstDb *sqlite3.SQLiteConn) (*sqlite3.SQLiteBackup, int, error) {
	// Start the backup
	backupConn, err := dstDb.Backup("main", srcDb, "main")
	if err != nil {
		return nil, 0, backup.NewError(backup.ErrDatabase, "failed to initialize backup", err)
	}

	// Get the initial page count
	_, total := backupConn.PageCount(), backupConn.PageCount()

	return backupConn, total, nil
}

// validatePageCount checks if the page count is valid and adjusts if necessary
func (s *SQLiteSource) validatePageCount(total, sourcePages int) (totalPages, remainingPages int, err error) {
	if total <= 0 {
		// If total is 0 but we have pages in source, try using source page count
		if sourcePages > 0 {
			return sourcePages, sourcePages, nil
		} else {
			return 0, 0, backup.NewError(backup.ErrDatabase, "invalid page count", nil)
		}
	}

	return total, total, nil
}

// performBackupSteps executes the backup process in chunks
func (s *SQLiteSource) performBackupSteps(ctx context.Context, backupConn *sqlite3.SQLiteBackup, total int) error {
	remaining := total
	const pagesPerStep = 1000

	for remaining > 0 {
		select {
		case <-ctx.Done():
			return backup.NewError(backup.ErrCanceled, "backup operation cancelled", ctx.Err())
		default:
		}

		// Step the backup process
		done, err := backupConn.Step(pagesPerStep)
		if err != nil {
			if isMediaError(err) {
				return backup.NewError(backup.ErrMedia, "failed during backup step", err)
			}
			return backup.NewError(backup.ErrDatabase, "failed during backup step", err)
		}

		if done {
			break
		}
	}

	return nil
}

// copyBackupToWriter copies the temporary backup file to the writer
func (s *SQLiteSource) copyBackupToWriter(tempPath string, w io.Writer) error {
	backupFile, err := os.Open(tempPath)
	if err != nil {
		return backup.NewError(backup.ErrIO, "failed to open backup file", err)
	}
	defer backupFile.Close()

	if _, err := io.Copy(w, backupFile); err != nil {
		if isMediaError(err) {
			return backup.NewError(backup.ErrMedia, "failed to write backup", err)
		}
		return backup.NewError(backup.ErrIO, "failed to write backup", err)
	}

	return nil
}

// streamBackupToWriter performs a streaming backup of the SQLite database to the provided writer
func (s *SQLiteSource) streamBackupToWriter(ctx context.Context, db *sql.DB, w io.Writer) error {
	// Create a temporary file for the backup
	tempFile, err := os.CreateTemp("", "birdnet-go-backup-*.db")
	if err != nil {
		return backup.NewError(backup.ErrIO, "failed to create temporary file", err)
	}
	tempPath := tempFile.Name()
	tempFile.Close() // Close it so we can use it as a destination
	defer os.Remove(tempPath)

	// Open the destination database
	destDB, err := sql.Open("sqlite3", tempPath)
	if err != nil {
		return backup.NewError(backup.ErrDatabase, "failed to open destination database", err)
	}
	defer destDB.Close()

	// Get the SQLite connection objects using the internal driver connection
	srcConn, err := db.Conn(ctx)
	if err != nil {
		return backup.NewError(backup.ErrDatabase, "failed to get source connection", err)
	}
	defer srcConn.Close()

	dstConn, err := destDB.Conn(ctx)
	if err != nil {
		return backup.NewError(backup.ErrDatabase, "failed to get destination connection", err)
	}
	defer dstConn.Close()

	// Execute backup within the connection context
	err = srcConn.Raw(func(srcDrv interface{}) error {
		return dstConn.Raw(func(dstDrv interface{}) error {
			// Type assert to get the SQLite connections
			srcDb, ok := srcDrv.(*sqlite3.SQLiteConn)
			if !ok {
				return backup.NewError(backup.ErrDatabase, "source is not a SQLite connection", nil)
			}

			dstDb, ok := dstDrv.(*sqlite3.SQLiteConn)
			if !ok {
				return backup.NewError(backup.ErrDatabase, "destination is not a SQLite connection", nil)
			}

			// Get database information
			sourcePages, err := s.getDatabaseInfo(db)
			if err != nil {
				return err
			}

			// Initialize backup connection
			backupConn, total, err := s.initializeBackupConnection(srcDb, dstDb)
			if err != nil {
				return err
			}
			defer backupConn.Close()

			// Validate and adjust page counts
			_, remaining, err := s.validatePageCount(total, sourcePages)
			if err != nil {
				return err
			}

			// Perform the backup in chunks
			return s.performBackupSteps(ctx, backupConn, remaining)
		})
	})

	if err != nil {
		return err
	}

	// Ensure all changes are written to disk
	if err := destDB.Close(); err != nil {
		return backup.NewError(backup.ErrIO, "failed to close destination database", err)
	}

	// Copy the backup file to the writer
	return s.copyBackupToWriter(tempPath, w)
}

// Backup performs a streaming backup of the SQLite database
func (s *SQLiteSource) Backup(ctx context.Context) (io.ReadCloser, error) {
	op := NewOperation()
	s.logf(op, "Starting SQLite streaming backup operation")

	// Validate configuration and get database path
	dbPath, err := s.validateConfig()
	if err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	s.logf(op, "Initiating backup for database: %s", dbPath)

	// Verify source database
	if err := s.verifyDatabase(dbPath, false); err != nil {
		return nil, fmt.Errorf("database verification failed: %w", err)
	}

	// Create pipe for streaming
	pr, pw := io.Pipe()

	// Start backup in a goroutine
	go func() {
		defer pw.Close()

		err := s.withDatabase(dbPath, true, func(conn *DatabaseConnection) error {
			return s.streamBackupToWriter(ctx, conn.db, pw)
		})

		if err != nil {
			pw.CloseWithError(err)
			return
		}

		s.logf(op, "SQLite backup completed successfully")
	}()

	return pr, nil
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
