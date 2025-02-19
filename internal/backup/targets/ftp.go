// Package targets provides backup target implementations
package targets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/tphakala/birdnet-go/internal/backup"
)

const (
	defaultFTPPort        = 21
	defaultTimeout        = 30 * time.Second
	defaultMaxConnections = 5
	defaultMaxRetries     = 3
	defaultBasePath       = "backups"
	metadataVersion       = 1
	tempFilePrefix        = "tmp-"
	metadataFileExt       = ".meta"
)

// FTPMetadataV1 represents version 1 of the backup metadata format
type FTPMetadataV1 struct {
	Version     int       `json:"version"`
	Timestamp   time.Time `json:"timestamp"`
	Size        int64     `json:"size"`
	Type        string    `json:"type"`
	Source      string    `json:"source"`
	IsDaily     bool      `json:"is_daily"`
	ConfigHash  string    `json:"config_hash,omitempty"`
	AppVersion  string    `json:"app_version,omitempty"`
	Compression string    `json:"compression,omitempty"`
}

// FTPTarget implements the backup.Target interface for FTP storage
type FTPTarget struct {
	config      FTPTargetConfig
	logger      backup.Logger
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
func NewFTPTarget(config *FTPTargetConfig, logger backup.Logger) (*FTPTarget, error) {
	// Validate required fields
	if config.Host == "" {
		return nil, backup.NewError(backup.ErrConfig, "ftp: host is required", nil)
	}

	// Set defaults for optional fields
	if config.Port == 0 {
		config.Port = defaultFTPPort
	}
	if config.Timeout == 0 {
		config.Timeout = defaultTimeout
	}
	if config.BasePath == "" {
		config.BasePath = defaultBasePath
	} else {
		config.BasePath = strings.TrimRight(config.BasePath, "/")
	}
	if config.MaxConns == 0 {
		config.MaxConns = defaultMaxConnections
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = defaultMaxRetries
	}
	if config.RetryBackoff == 0 {
		config.RetryBackoff = time.Second
	}

	if logger == nil {
		logger = backup.DefaultLogger()
	}

	target := &FTPTarget{
		config:    *config,
		logger:    logger,
		connPool:  make(chan *ftp.ServerConn, config.MaxConns),
		tempFiles: make(map[string]bool),
	}

	return target, nil
}

// NewFTPTargetFromMap creates a new FTP target from a map configuration (for backward compatibility)
func NewFTPTargetFromMap(settings map[string]interface{}) (*FTPTarget, error) {
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
	if path, ok := settings["path"].(string); ok {
		config.BasePath = path
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

	var logger backup.Logger
	if l, ok := settings["logger"].(backup.Logger); ok {
		logger = l
	}

	return NewFTPTarget(&config, logger)
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
			t.logger.Printf("Warning: failed to close FTP connection: %v", err)
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
func (t *FTPTarget) isTransientError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "temporary") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no route to host")
}

// withRetry executes an operation with retry logic
func (t *FTPTarget) withRetry(ctx context.Context, op func(*ftp.ServerConn) error) error {
	var lastErr error
	for i := 0; i < t.config.MaxRetries; i++ {
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
			time.Sleep(t.config.RetryBackoff * time.Duration(i+1))
			continue
		}

		err = op(conn)
		if err == nil {
			t.returnConnection(conn)
			return nil
		}

		lastErr = err
		_ = conn.Quit() // Close failed connection

		if !t.isTransientError(err) {
			return err
		}

		if t.config.Debug {
			t.logger.Printf("FTP: Retrying operation after error: %v (attempt %d/%d)", err, i+1, t.config.MaxRetries)
		}
		time.Sleep(t.config.RetryBackoff * time.Duration(i+1))
	}

	return backup.NewError(backup.ErrIO, "ftp: operation failed after retries", lastErr)
}

// connect establishes a connection to the FTP server with context support
func (t *FTPTarget) connect(ctx context.Context) (*ftp.ServerConn, error) {
	// Create a channel to handle connection timeout
	connChan := make(chan *ftp.ServerConn, 1)
	errChan := make(chan error, 1)

	go func() {
		addr := fmt.Sprintf("%s:%d", t.config.Host, t.config.Port)
		conn, err := ftp.Dial(addr, ftp.DialWithTimeout(t.config.Timeout))
		if err != nil {
			errChan <- backup.NewError(backup.ErrIO, "ftp: connection failed", err)
			return
		}

		if t.config.Username != "" {
			if err := conn.Login(t.config.Username, t.config.Password); err != nil {
				if quitErr := conn.Quit(); quitErr != nil {
					t.logger.Printf("Warning: failed to quit FTP connection after login error: %v", quitErr)
				}
				errChan <- backup.NewError(backup.ErrValidation, "ftp: login failed", err)
				return
			}
		}

		// Get and store the initial working directory
		pwd, err := conn.CurrentDir()
		if err != nil {
			if quitErr := conn.Quit(); quitErr != nil {
				t.logger.Printf("Warning: failed to quit FTP connection after pwd error: %v", quitErr)
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
	// Create a temporary filename
	tempName := path.Join(path.Dir(remotePath), tempFilePrefix+filepath.Base(remotePath))
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
	file, err := os.Open(localPath)
	if err != nil {
		return backup.NewError(backup.ErrIO, "ftp: failed to open local file", err)
	}
	defer file.Close()

	// Create a pipe for streaming
	pr, pw := io.Pipe()
	errChan := make(chan error, 1)

	// Start upload in a goroutine
	go func() {
		defer pw.Close()
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
			t.logger.Printf("FTP: Failed to store file %s: %v", remotePath, err)
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

// Store implements the backup.Target interface with atomic operations and metadata
func (t *FTPTarget) Store(ctx context.Context, sourcePath string, metadata *backup.Metadata) error {
	if t.config.Debug {
		t.logger.Printf("🔄 FTP: Storing backup %s to %s", filepath.Base(sourcePath), t.config.Host)
	}

	// Create versioned metadata
	ftpMetadata := FTPMetadataV1{
		Version:    metadataVersion,
		Timestamp:  metadata.Timestamp,
		Size:       metadata.Size,
		Type:       metadata.Type,
		Source:     metadata.Source,
		IsDaily:    metadata.IsDaily,
		ConfigHash: metadata.ConfigHash,
		AppVersion: metadata.AppVersion,
	}

	// Marshal metadata
	metadataBytes, err := json.Marshal(ftpMetadata)
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

		// Store metadata file
		metadataPath := backupPath + metadataFileExt
		tempMetadataFile, err := os.CreateTemp("", "ftp-metadata-*")
		if err != nil {
			return backup.NewError(backup.ErrIO, "ftp: failed to create temporary metadata file", err)
		}
		defer os.Remove(tempMetadataFile.Name())
		defer tempMetadataFile.Close()

		if _, err := tempMetadataFile.Write(metadataBytes); err != nil {
			return backup.NewError(backup.ErrIO, "ftp: failed to write metadata", err)
		}
		if err := tempMetadataFile.Sync(); err != nil {
			return backup.NewError(backup.ErrIO, "ftp: failed to sync metadata file", err)
		}
		if err := tempMetadataFile.Close(); err != nil {
			return backup.NewError(backup.ErrIO, "ftp: failed to close metadata file", err)
		}

		// Upload metadata file atomically
		if err := t.atomicUpload(ctx, conn, tempMetadataFile.Name(), metadataPath); err != nil {
			return backup.NewError(backup.ErrIO, "ftp: failed to store metadata", err)
		}

		if t.config.Debug {
			t.logger.Printf("✅ FTP: Successfully stored backup %s with metadata", filepath.Base(sourcePath))
		}

		return nil
	})
}

// List implements the backup.Target interface
func (t *FTPTarget) List(ctx context.Context) ([]backup.BackupInfo, error) {
	if t.config.Debug {
		t.logger.Printf("🔄 FTP: Listing backups from %s", t.config.Host)
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
			if entry.Type == ftp.EntryTypeFile {
				backups = append(backups, backup.BackupInfo{
					Target: entry.Name,
					Metadata: backup.Metadata{
						Timestamp: entry.Time,
						Size:      int64(entry.Size),
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
		t.logger.Printf("🔄 FTP: Deleting backup %s from %s", target, t.config.Host)
	}

	return t.withRetry(ctx, func(conn *ftp.ServerConn) error {
		backupPath := path.Join(t.config.BasePath, target)
		if err := conn.Delete(backupPath); err != nil {
			return backup.NewError(backup.ErrIO, "ftp: failed to delete backup", err)
		}

		if t.config.Debug {
			t.logger.Printf("✅ FTP: Successfully deleted backup %s", target)
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
		if len(t.config.Features) > 0 {
			t.logger.Printf("Warning: feature checking is not supported with current FTP library version")
		}

		// First ensure the base backup path exists
		if err := t.createDirectory(ctx, conn, t.config.BasePath); err != nil {
			if t.config.Debug {
				t.logger.Printf("FTP: Failed to create base backup directory %s: %v", t.config.BasePath, err)
			}
			return backup.NewError(backup.ErrValidation, "ftp: failed to create base backup directory", err)
		}

		// Change to the backup directory to ensure we have proper permissions
		if err := conn.ChangeDir(t.config.BasePath); err != nil {
			if t.config.Debug {
				t.logger.Printf("FTP: Failed to change to backup directory %s: %v", t.config.BasePath, err)
			}
			return backup.NewError(backup.ErrValidation, "ftp: cannot access backup directory", err)
		}

		// Create a test subdirectory (using relative path since we're in the base directory)
		testDirName := "write_test_dir"
		if err := conn.MakeDir(testDirName); err != nil {
			errStr := strings.ToLower(err.Error())
			if !strings.Contains(errStr, "file exists") &&
				!strings.Contains(errStr, "already exists") &&
				!strings.Contains(errStr, "directory exists") &&
				!strings.Contains(errStr, "550") {
				if t.config.Debug {
					t.logger.Printf("FTP: Failed to create test directory %s: %v", testDirName, err)
				}
				return backup.NewError(backup.ErrValidation, "ftp: failed to create test directory", err)
			}
		}

		// Change into test directory
		if err := conn.ChangeDir(testDirName); err != nil {
			if t.config.Debug {
				t.logger.Printf("FTP: Failed to change to test directory %s: %v", testDirName, err)
			}
			return backup.NewError(backup.ErrValidation, "ftp: cannot access test directory", err)
		}

		// Create a test file (using relative path since we're in the test directory)
		testData := []byte("test")
		tempFile, err := os.CreateTemp("", "ftp-test-*")
		if err != nil {
			return backup.NewError(backup.ErrIO, "ftp: failed to create temporary test file", err)
		}
		defer os.Remove(tempFile.Name())
		if _, err := tempFile.Write(testData); err != nil {
			return backup.NewError(backup.ErrIO, "ftp: failed to write test data", err)
		}
		if err := tempFile.Close(); err != nil {
			return backup.NewError(backup.ErrIO, "ftp: failed to close test file", err)
		}

		// Upload test file using relative path
		if err := t.uploadFile(ctx, conn, tempFile.Name(), "test.txt"); err != nil {
			if t.config.Debug {
				t.logger.Printf("FTP: Failed to upload test file: %v", err)
			}
			return backup.NewError(backup.ErrValidation, "ftp: failed to upload test file", err)
		}

		// Test file deletion - continue on error
		if err := conn.Delete("test.txt"); err != nil && t.config.Debug {
			t.logger.Printf("⚠️ FTP: Failed to delete test file: %v", err)
		}

		// Change back to parent directory
		if err := conn.ChangeDirToParent(); err != nil && t.config.Debug {
			t.logger.Printf("⚠️ FTP: Failed to change to parent directory: %v", err)
		}

		// Test directory deletion - continue on error
		if err := conn.RemoveDir(testDirName); err != nil && t.config.Debug {
			t.logger.Printf("⚠️ FTP: Failed to remove test directory %s: %v", testDirName, err)
		}

		// Change back to initial directory
		if err := conn.ChangeDir(t.initialDir); err != nil && t.config.Debug {
			t.logger.Printf("⚠️ FTP: Failed to change back to initial directory %s: %v", t.initialDir, err)
		}

		return nil
	})
}

// trackTempFile adds a temporary file to the tracking map
func (t *FTPTarget) trackTempFile(path string) {
	t.tempFilesMu.Lock()
	defer t.tempFilesMu.Unlock()
	t.tempFiles[path] = true
}

// untrackTempFile removes a temporary file from the tracking map
func (t *FTPTarget) untrackTempFile(path string) {
	t.tempFilesMu.Lock()
	defer t.tempFilesMu.Unlock()
	delete(t.tempFiles, path)
}

// cleanupTempFiles attempts to clean up any tracked temporary files
func (t *FTPTarget) cleanupTempFiles(conn *ftp.ServerConn) {
	t.tempFilesMu.Lock()
	tempFiles := make([]string, 0, len(t.tempFiles))
	for path := range t.tempFiles {
		tempFiles = append(tempFiles, path)
	}
	t.tempFilesMu.Unlock()

	for _, path := range tempFiles {
		if err := conn.Delete(path); err != nil {
			if t.config.Debug {
				t.logger.Printf("Warning: failed to clean up temporary file %s: %v", path, err)
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
				t.logger.Printf("Warning: failed to close FTP connection: %v", err)
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
		t.logger.Printf("⚠️ FTP: Failed to change back to original directory %s: %v", currentDir, err)
	}

	return nil
}
