// Package targets provides backup target implementations
package targets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/tphakala/birdnet-go/internal/backup"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// FTP-specific constants (shared constants imported from common.go)
const (
	ftpMetadataVersion = 1
	ftpTempFilePrefix  = "tmp-"
	ftpMetadataFileExt = ".meta"
)

// FTPTarget implements the backup.Target interface for FTP storage
type FTPTarget struct {
	config      FTPTargetConfig
	log         logger.Logger
	connPool    chan *ftp.ServerConn
	mu          sync.Mutex // Protects connPool operations
	tempFiles   map[string]bool
	tempFilesMu sync.Mutex // Protects tempFiles map
	initialDir  string     // Initial working directory after login
}

// FTPTargetConfig holds configuration for the FTP target
type FTPTargetConfig struct {
	Host         string
	Port         int
	Username     string
	Password     string
	BasePath     string
	Timeout      time.Duration
	Debug        bool
	MaxConns     int
	MaxRetries   int
	RetryBackoff time.Duration
	Features     []string // Required server features
	MinSpace     int64    // Minimum required space in bytes
}

// NewFTPTarget creates a new FTP target with the given configuration
func NewFTPTarget(config *FTPTargetConfig, lg logger.Logger) (*FTPTarget, error) {
	// Validate required fields
	if config.Host == "" {
		return nil, backup.NewError(backup.ErrConfig, "ftp: host is required", nil)
	}
	if config.BasePath == "" {
		return nil, backup.NewError(backup.ErrConfig, "ftp: base path is required", nil)
	}

	// Set defaults for optional fields
	if config.Port == 0 {
		config.Port = DefaultFTPPort
	}
	if config.Timeout == 0 {
		config.Timeout = DefaultTimeout
	}
	config.BasePath = strings.TrimRight(config.BasePath, "/")
	if config.MaxConns == 0 {
		config.MaxConns = DefaultMaxConns
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = DefaultMaxRetries
	}
	if config.RetryBackoff == 0 {
		config.RetryBackoff = time.Second
	}

	if lg == nil {
		lg = logger.Global().Module("backup")
	}

	target := &FTPTarget{
		config:    *config,
		log:       lg.Module("ftp"),
		connPool:  make(chan *ftp.ServerConn, config.MaxConns),
		tempFiles: make(map[string]bool),
	}

	return target, nil
}

// NewFTPTargetFromMap creates a new FTP target from a map configuration (for backward compatibility)
func NewFTPTargetFromMap(settings map[string]any) (*FTPTarget, error) {
	config := FTPTargetConfig{}

	// Required settings
	host, ok := settings["host"].(string)
	if !ok {
		return nil, backup.NewError(backup.ErrConfig, "ftp: host is required", nil)
	}
	config.Host = host

	// Optional settings
	if port, ok := settings["port"].(int); ok {
		config.Port = port
	}
	if username, ok := settings["username"].(string); ok {
		config.Username = username
	}
	if password, ok := settings["password"].(string); ok {
		config.Password = password
	}
	if basePath, ok := settings["path"].(string); ok {
		config.BasePath = basePath
	}
	if timeout, ok := settings["timeout"].(string); ok {
		duration, err := time.ParseDuration(timeout)
		if err != nil {
			return nil, backup.NewError(backup.ErrValidation, "ftp: invalid timeout format", err)
		}
		config.Timeout = duration
	}
	if debug, ok := settings["debug"].(bool); ok {
		config.Debug = debug
	}

	return NewFTPTarget(&config, nil)
}

// Name returns the name of this target
func (t *FTPTarget) Name() string {
	return "ftp"
}

// getConnection gets a connection from the pool or creates a new one
func (t *FTPTarget) getConnection(ctx context.Context) (*ftp.ServerConn, error) {
	// Try to get a connection from the pool
	select {
	case conn := <-t.connPool:
		if t.isConnectionAlive(conn) {
			return conn, nil
		}
		// Connection is dead, close it and create a new one
		_ = conn.Quit()
	default:
		// Pool is empty, create a new connection
	}

	return t.connect(ctx)
}

// returnConnection returns a connection to the pool or closes it if the pool is full
func (t *FTPTarget) returnConnection(conn *ftp.ServerConn) {
	if conn == nil {
		return
	}

	select {
	case t.connPool <- conn:
		// Connection returned to pool
	default:
		// Pool is full, close the connection
		if err := conn.Quit(); err != nil {
			t.log.Info(fmt.Sprintf("Warning: failed to close FTP connection: %v", err))
		}
	}
}

// isConnectionAlive checks if a connection is still usable
func (t *FTPTarget) isConnectionAlive(conn *ftp.ServerConn) bool {
	if conn == nil {
		return false
	}
	// Try a simple NOOP command to check connection
	return conn.NoOp() == nil
}

// isTransientError checks if an error is likely temporary
// Delegates to the shared implementation in common.go
func (t *FTPTarget) isTransientError(err error) bool {
	return IsTransientError(err)
}

// withRetry executes an operation with retry logic
func (t *FTPTarget) withRetry(ctx context.Context, op func(*ftp.ServerConn) error) error {
	var lastErr error
	for attempt := range t.config.MaxRetries {
		select {
		case <-ctx.Done():
			return backup.NewError(backup.ErrCanceled, "ftp: operation canceled", ctx.Err())
		default:
		}

		conn, err := t.getConnection(ctx)
		if err != nil {
			lastErr = err
			if !t.isTransientError(err) {
				return err
			}
			time.Sleep(t.config.RetryBackoff * time.Duration(attempt+1))
			continue
		}

		if err = op(conn); err == nil {
			t.returnConnection(conn)
			return nil
		}

		lastErr = err
		_ = conn.Quit() // Close failed connection

		if !t.isTransientError(err) {
			return err
		}

		if t.config.Debug {
			t.log.Info(fmt.Sprintf("FTP: Retrying operation after error: %v (attempt %d/%d)", err, attempt+1, t.config.MaxRetries))
		}
		time.Sleep(t.config.RetryBackoff * time.Duration(attempt+1))
	}

	return backup.NewError(backup.ErrIO, "ftp: operation failed after retries", lastErr)
}

// connect establishes a connection to the FTP server with context support
func (t *FTPTarget) connect(ctx context.Context) (*ftp.ServerConn, error) {
	// Create a channel to handle connection timeout
	connChan := make(chan *ftp.ServerConn, 1)
	errChan := make(chan error, 1)

	go func() {
		// Use net.JoinHostPort for proper IPv6 support (handles bracketing automatically)
		addr := net.JoinHostPort(t.config.Host, strconv.Itoa(t.config.Port))
		conn, err := ftp.Dial(addr, ftp.DialWithTimeout(t.config.Timeout))
		if err != nil {
			errChan <- backup.NewError(backup.ErrIO, "ftp: connection failed", err)
			return
		}

		if t.config.Username != "" {
			if err := conn.Login(t.config.Username, t.config.Password); err != nil {
				if quitErr := conn.Quit(); quitErr != nil {
					t.log.Info(fmt.Sprintf("Warning: failed to quit FTP connection after login error: %v", quitErr))
				}
				errChan <- backup.NewError(backup.ErrValidation, "ftp: login failed", err)
				return
			}
		}

		// Get and store the initial working directory
		pwd, err := conn.CurrentDir()
		if err != nil {
			if quitErr := conn.Quit(); quitErr != nil {
				t.log.Info(fmt.Sprintf("Warning: failed to quit FTP connection after pwd error: %v", quitErr))
			}
			errChan <- backup.NewError(backup.ErrIO, "ftp: failed to get working directory", err)
			return
		}
		t.initialDir = pwd

		connChan <- conn
	}()

	select {
	case <-ctx.Done():
		return nil, backup.NewError(backup.ErrCanceled, "ftp: connection attempt canceled", ctx.Err())
	case err := <-errChan:
		return nil, err
	case conn := <-connChan:
		return conn, nil
	}
}

// atomicUpload performs an atomic upload operation using a temporary file
func (t *FTPTarget) atomicUpload(ctx context.Context, conn *ftp.ServerConn, localPath, remotePath string) error {
	// Generate a unique temporary filename without creating a file
	timestamp := time.Now().UnixNano()
	tempBaseName := fmt.Sprintf("ftp-upload-%d-%d", timestamp, os.Getpid())

	// Create the remote temp file name
	tempName := path.Join(path.Dir(remotePath), tempBaseName)
	t.trackTempFile(tempName)
	defer t.untrackTempFile(tempName)

	// Upload to temporary file
	if err := t.uploadFile(ctx, conn, localPath, tempName); err != nil {
		// Try to clean up the temporary file
		_ = conn.Delete(tempName)
		return err
	}

	// Rename to final destination
	if err := conn.Rename(tempName, remotePath); err != nil {
		// Try to clean up the temporary file
		_ = conn.Delete(tempName)
		return backup.NewError(backup.ErrIO, "ftp: failed to rename temporary file", err)
	}

	return nil
}

// uploadFile handles the actual file upload
func (t *FTPTarget) uploadFile(ctx context.Context, conn *ftp.ServerConn, localPath, remotePath string) error {
	// Open local file (from trusted internal backup manager temp directory)
	file, err := os.Open(localPath) //nolint:gosec // G304 - localPath is a trusted internal temp path from backup manager
	if err != nil {
		return backup.NewError(backup.ErrIO, fmt.Sprintf("ftp: failed to open local file: %s", localPath), err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.log.Info(fmt.Sprintf("ftp: failed to close file %s: %v", localPath, err))
		}
	}()

	// Create a pipe for streaming
	pr, pw := io.Pipe()
	errChan := make(chan error, 1)

	// Start upload in a goroutine
	go func() {
		defer func() {
			if err := pw.Close(); err != nil {
				t.log.Info(fmt.Sprintf("ftp: failed to close pipe writer: %v", err))
			}
		}()
		_, err := io.Copy(pw, file)
		if err != nil {
			errChan <- backup.NewError(backup.ErrIO, "ftp: failed to copy file data", err)
			return
		}
		errChan <- nil
	}()

	// Store the file on the FTP server
	if err := conn.Stor(remotePath, pr); err != nil {
		// Debug print failing file
		if t.config.Debug {
			t.log.Info(fmt.Sprintf("FTP: Failed to store file %s: %v", remotePath, err))
		}
		return backup.NewError(backup.ErrIO, "ftp: failed to store file", err)
	}

	// Wait for upload completion or context cancellation
	select {
	case <-ctx.Done():
		return backup.NewError(backup.ErrCanceled, "ftp: upload operation canceled", ctx.Err())
	case err := <-errChan:
		return err
	}
}

// Store implements the backup.Target interface
func (t *FTPTarget) Store(ctx context.Context, sourcePath string, metadata *backup.Metadata) error {
	if t.config.Debug {
		t.log.Info(fmt.Sprintf("🔄 FTP: Storing backup %s to %s", filepath.Base(sourcePath), t.config.Host))
	}

	// Marshal metadata
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return backup.NewError(backup.ErrIO, "ftp: failed to marshal metadata", err)
	}

	return t.withRetry(ctx, func(conn *ftp.ServerConn) error {
		// Ensure the target directory exists
		if err := t.createDirectory(ctx, conn, t.config.BasePath); err != nil {
			return err
		}

		// Store the backup file atomically
		backupPath := path.Join(t.config.BasePath, filepath.Base(sourcePath))
		if err := t.atomicUpload(ctx, conn, sourcePath, backupPath); err != nil {
			return err
		}

		// Store metadata file using shared helper
		metadataPath := backupPath + ftpMetadataFileExt
		tempResult, err := WriteTempFile(metadataBytes, "ftp-metadata")
		if err != nil {
			return err
		}
		defer tempResult.Cleanup()

		// Upload metadata file atomically
		if err := t.atomicUpload(ctx, conn, tempResult.Path, metadataPath); err != nil {
			return backup.NewError(backup.ErrIO, "ftp: failed to store metadata", err)
		}

		if t.config.Debug {
			t.log.Info(fmt.Sprintf("✅ FTP: Successfully stored backup %s with metadata", filepath.Base(sourcePath)))
		}

		return nil
	})
}

// List implements the backup.Target interface
func (t *FTPTarget) List(ctx context.Context) ([]backup.BackupInfo, error) {
	if t.config.Debug {
		t.log.Info(fmt.Sprintf("🔄 FTP: Listing backups from %s", t.config.Host))
	}

	var backups []backup.BackupInfo
	err := t.withRetry(ctx, func(conn *ftp.ServerConn) error {
		entries, err := conn.List(t.config.BasePath)
		if err != nil {
			if strings.Contains(err.Error(), "No such file or directory") {
				return nil
			}
			return backup.NewError(backup.ErrIO, "ftp: failed to list backups", err)
		}

		for _, entry := range entries {
			if entry.Type == ftp.EntryTypeFile && !strings.HasPrefix(entry.Name, "ftp-upload-") {
				// Skip metadata files
				if strings.HasSuffix(entry.Name, ftpMetadataFileExt) {
					continue
				}

				backups = append(backups, backup.BackupInfo{
					Target: entry.Name,
					Metadata: backup.Metadata{
						Timestamp: entry.Time,
						Size:      int64(entry.Size), // #nosec G115 -- file size conversion safe for FTP listing
					},
				})
			}

			select {
			case <-ctx.Done():
				return backup.NewError(backup.ErrCanceled, "ftp: listing operation canceled", ctx.Err())
			default:
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}
	return backups, nil
}

// Delete implements the backup.Target interface
func (t *FTPTarget) Delete(ctx context.Context, target string) error {
	if t.config.Debug {
		t.log.Info(fmt.Sprintf("🔄 FTP: Deleting backup %s from %s", target, t.config.Host))
	}

	return t.withRetry(ctx, func(conn *ftp.ServerConn) error {
		backupPath := path.Join(t.config.BasePath, target)
		if err := conn.Delete(backupPath); err != nil {
			return backup.NewError(backup.ErrIO, "ftp: failed to delete backup", err)
		}

		if t.config.Debug {
			t.log.Info(fmt.Sprintf("✅ FTP: Successfully deleted backup %s", target))
		}

		return nil
	})
}

// Validate performs comprehensive validation of the FTP target
func (t *FTPTarget) Validate() error {
	ctx, cancel := context.WithTimeout(context.Background(), t.config.Timeout)
	defer cancel()

	return t.withRetry(ctx, func(conn *ftp.ServerConn) error {
		// Check server features if required
		t.checkServerFeatures()

		// Validate base directory access
		if err := t.validateBaseDirectory(ctx, conn); err != nil {
			return err
		}

		// Test write permissions
		if err := t.testWritePermissions(ctx, conn); err != nil {
			return err
		}

		// Cleanup test artifacts
		t.cleanupTestArtifacts(conn)

		return nil
	})
}

// checkServerFeatures checks if required server features are supported
func (t *FTPTarget) checkServerFeatures() {
	if len(t.config.Features) > 0 {
		t.log.Info("Warning: feature checking is not supported with current FTP library version")
	}
}

// validateBaseDirectory ensures the base backup directory exists and is accessible
func (t *FTPTarget) validateBaseDirectory(ctx context.Context, conn *ftp.ServerConn) error {
	// First ensure the base backup path exists
	if err := t.createDirectory(ctx, conn, t.config.BasePath); err != nil {
		if t.config.Debug {
			t.log.Info(fmt.Sprintf("FTP: Failed to create base backup directory %s: %v", t.config.BasePath, err))
		}
		return backup.NewError(backup.ErrValidation, "ftp: failed to create base backup directory", err)
	}

	// Change to the backup directory to ensure we have proper permissions
	if err := conn.ChangeDir(t.config.BasePath); err != nil {
		if t.config.Debug {
			t.log.Info(fmt.Sprintf("FTP: Failed to change to backup directory %s: %v", t.config.BasePath, err))
		}
		return backup.NewError(backup.ErrValidation, "ftp: cannot access backup directory", err)
	}

	return nil
}

// testWritePermissions tests if we can create directories and files
func (t *FTPTarget) testWritePermissions(ctx context.Context, conn *ftp.ServerConn) error {
	testDirName := "write_test_dir"

	// Create test directory
	if err := t.createTestDirectory(conn, testDirName); err != nil {
		return err
	}

	// Change into test directory
	if err := conn.ChangeDir(testDirName); err != nil {
		if t.config.Debug {
			t.log.Info(fmt.Sprintf("FTP: Failed to change to test directory %s: %v", testDirName, err))
		}
		return backup.NewError(backup.ErrValidation, "ftp: cannot access test directory", err)
	}

	// Test file upload
	if err := t.testFileUpload(ctx, conn); err != nil {
		return err
	}

	return nil
}

// createTestDirectory creates a test directory for validation
func (t *FTPTarget) createTestDirectory(conn *ftp.ServerConn, dirName string) error {
	if err := conn.MakeDir(dirName); err != nil {
		errStr := strings.ToLower(err.Error())
		if !t.isDirectoryExistsError(errStr) {
			if t.config.Debug {
				t.log.Info(fmt.Sprintf("FTP: Failed to create test directory %s: %v", dirName, err))
			}
			return backup.NewError(backup.ErrValidation, "ftp: failed to create test directory", err)
		}
	}
	return nil
}

// isDirectoryExistsError checks if the error indicates the directory already exists
func (t *FTPTarget) isDirectoryExistsError(errStr string) bool {
	return strings.Contains(errStr, "file exists") ||
		strings.Contains(errStr, "already exists") ||
		strings.Contains(errStr, "directory exists") ||
		strings.Contains(errStr, "550")
}

// testFileUpload tests uploading a file to the FTP server
func (t *FTPTarget) testFileUpload(ctx context.Context, conn *ftp.ServerConn) error {
	// Create a test file
	testData := []byte("test")
	tempFile, err := os.CreateTemp("", "ftp-test-*")
	if err != nil {
		return backup.NewError(backup.ErrIO, "ftp: failed to create temporary test file", err)
	}
	defer func() {
		if err := os.Remove(tempFile.Name()); err != nil {
			t.log.Info(fmt.Sprintf("ftp: failed to remove temp file: %v", err))
		}
	}()

	if _, err := tempFile.Write(testData); err != nil {
		return backup.NewError(backup.ErrIO, "ftp: failed to write test data", err)
	}
	if err := tempFile.Close(); err != nil {
		return backup.NewError(backup.ErrIO, "ftp: failed to close test file", err)
	}

	// Upload test file
	if err := t.uploadFile(ctx, conn, tempFile.Name(), "test.txt"); err != nil {
		if t.config.Debug {
			t.log.Info(fmt.Sprintf("FTP: Failed to upload test file: %v", err))
		}
		return backup.NewError(backup.ErrValidation, "ftp: failed to upload test file", err)
	}

	return nil
}

// cleanupTestArtifacts removes test files and directories created during validation
func (t *FTPTarget) cleanupTestArtifacts(conn *ftp.ServerConn) {
	// Test file deletion - continue on error
	if err := conn.Delete("test.txt"); err != nil && t.config.Debug {
		t.log.Info(fmt.Sprintf("⚠️ FTP: Failed to delete test file: %v", err))
	}

	// Change back to parent directory
	if err := conn.ChangeDirToParent(); err != nil && t.config.Debug {
		t.log.Info(fmt.Sprintf("⚠️ FTP: Failed to change to parent directory: %v", err))
	}

	// Test directory deletion - continue on error
	if err := conn.RemoveDir("write_test_dir"); err != nil && t.config.Debug {
		t.log.Info(fmt.Sprintf("⚠️ FTP: Failed to remove test directory: %v", err))
	}

	// Change back to initial directory
	if err := conn.ChangeDir(t.initialDir); err != nil && t.config.Debug {
		t.log.Info(fmt.Sprintf("⚠️ FTP: Failed to change back to initial directory %s: %v", t.initialDir, err))
	}
}

// trackTempFile adds a temporary file to the tracking map
func (t *FTPTarget) trackTempFile(filePath string) {
	t.tempFilesMu.Lock()
	defer t.tempFilesMu.Unlock()
	t.tempFiles[filePath] = true
}

// untrackTempFile removes a temporary file from the tracking map
func (t *FTPTarget) untrackTempFile(filePath string) {
	t.tempFilesMu.Lock()
	defer t.tempFilesMu.Unlock()
	delete(t.tempFiles, filePath)
}

// cleanupTempFiles attempts to clean up any tracked temporary files
func (t *FTPTarget) cleanupTempFiles(conn *ftp.ServerConn) {
	t.tempFilesMu.Lock()
	tempFiles := slices.Collect(maps.Keys(t.tempFiles))
	t.tempFilesMu.Unlock()

	for _, path := range tempFiles {
		if err := conn.Delete(path); err != nil {
			if t.config.Debug {
				t.log.Info(fmt.Sprintf("Warning: failed to clean up temporary file %s: %v", path, err))
			}
		} else {
			t.untrackTempFile(path)
		}
	}
}

// Close closes all connections in the pool and cleans up resources
func (t *FTPTarget) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	var lastErr error

	// Try to clean up temporary files with one last connection
	conn, err := t.connect(context.Background())
	if err == nil {
		t.cleanupTempFiles(conn)
		if err := conn.Quit(); err != nil {
			lastErr = err
		}
	}

	// Close all pooled connections
	for {
		select {
		case conn := <-t.connPool:
			if err := conn.Quit(); err != nil {
				lastErr = err
				t.log.Info(fmt.Sprintf("Warning: failed to close FTP connection: %v", err))
			}
		default:
			return lastErr
		}
	}
}

// createDirectory ensures the target directory exists on the FTP server
func (t *FTPTarget) createDirectory(ctx context.Context, conn *ftp.ServerConn, dirPath string) error {
	if dirPath == "" {
		return nil
	}

	// Store current directory
	currentDir, err := conn.CurrentDir()
	if err != nil {
		return backup.NewError(backup.ErrIO, "ftp: failed to get current directory", err)
	}

	// First try to change to the directory to see if it exists
	if err := conn.ChangeDir(dirPath); err == nil {
		// Directory exists, change back to original directory and return
		_ = conn.ChangeDir(currentDir)
		return nil
	}

	// Try to create the directory
	err = conn.MakeDir(dirPath)
	if err != nil {
		// Check if error indicates directory already exists
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "file exists") ||
			strings.Contains(errStr, "already exists") ||
			strings.Contains(errStr, "directory exists") ||
			strings.Contains(errStr, "550") { // Common FTP error code for existing directory
			return nil // Directory exists, that's fine
		}
		return backup.NewError(backup.ErrIO, fmt.Sprintf("ftp: failed to create directory %s", dirPath), err)
	}

	// Change back to original directory
	if err := conn.ChangeDir(currentDir); err != nil && t.config.Debug {
		t.log.Info(fmt.Sprintf("⚠️ FTP: Failed to change back to original directory %s: %v", currentDir, err))
	}

	return nil
}
