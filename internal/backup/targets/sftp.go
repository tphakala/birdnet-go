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

	"github.com/pkg/sftp"
	"github.com/tphakala/birdnet-go/internal/backup"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	defaultSFTPMaxRetries   = 3
	defaultSFTPRetryBackoff = time.Second
	defaultSFTPMaxConns     = 5
	defaultSFTPTimeout      = 30 * time.Second
	defaultSFTPPort         = 22
	sftpTempFilePrefix      = "tmp-"
	sftpMetadataFileExt     = ".meta"
	sftpMetadataVersion     = 1
)

// SFTPTargetConfig holds configuration for the SFTP target
type SFTPTargetConfig struct {
	Host          string
	Port          int
	Username      string
	Password      string
	KeyFile       string
	KnownHostFile string
	BasePath      string
	Timeout       time.Duration
	Debug         bool
	MaxConns      int
	MaxRetries    int
	RetryBackoff  time.Duration
}

// SFTPTarget implements the backup.Target interface for SFTP storage
type SFTPTarget struct {
	config      SFTPTargetConfig
	connPool    chan *sftp.Client
	mu          sync.Mutex // Protects connPool operations
	tempFiles   map[string]bool
	tempFilesMu sync.Mutex // Protects tempFiles map
}

// ProgressReader wraps an io.Reader to track progress
type SFTPProgressReader struct {
	io.Reader
	Progress func(n int64)
}

func (r *SFTPProgressReader) Read(p []byte) (n int, err error) {
	n, err = r.Reader.Read(p)
	if n > 0 && r.Progress != nil {
		r.Progress(int64(n))
	}
	return
}

// NewSFTPTarget creates a new SFTP target with the given configuration
func NewSFTPTarget(settings map[string]interface{}) (*SFTPTarget, error) {
	config := SFTPTargetConfig{}

	// Required settings
	host, ok := settings["host"].(string)
	if !ok {
		return nil, backup.NewError(backup.ErrConfig, "sftp: host is required", nil)
	}
	config.Host = host

	path, ok := settings["path"].(string)
	if !ok {
		return nil, backup.NewError(backup.ErrConfig, "sftp: path is required", nil)
	}
	config.BasePath = strings.TrimRight(path, "/")

	// Optional settings with defaults
	if port, ok := settings["port"].(int); ok {
		config.Port = port
	} else {
		config.Port = defaultSFTPPort
	}

	if username, ok := settings["username"].(string); ok {
		config.Username = username
	}

	if password, ok := settings["password"].(string); ok {
		config.Password = password
	}

	if keyFile, ok := settings["key_file"].(string); ok {
		config.KeyFile = keyFile
	}

	if knownHostFile, ok := settings["known_hosts_file"].(string); ok {
		config.KnownHostFile = knownHostFile
	} else {
		// Default to user's known_hosts file
		homeDir, err := os.UserHomeDir()
		if err == nil {
			config.KnownHostFile = filepath.Join(homeDir, ".ssh", "known_hosts")
		}
	}

	if timeout, ok := settings["timeout"].(string); ok {
		duration, err := time.ParseDuration(timeout)
		if err != nil {
			return nil, backup.NewError(backup.ErrValidation, "sftp: invalid timeout format", err)
		}
		config.Timeout = duration
	} else {
		config.Timeout = defaultSFTPTimeout
	}

	if debug, ok := settings["debug"].(bool); ok {
		config.Debug = debug
	}

	config.MaxRetries = defaultSFTPMaxRetries
	config.RetryBackoff = defaultSFTPRetryBackoff
	config.MaxConns = defaultSFTPMaxConns

	target := &SFTPTarget{
		config:    config,
		connPool:  make(chan *sftp.Client, config.MaxConns),
		tempFiles: make(map[string]bool),
	}

	return target, nil
}

// Name returns the name of this target
func (t *SFTPTarget) Name() string {
	return "sftp"
}

// getConnection gets a connection from the pool or creates a new one
func (t *SFTPTarget) getConnection(ctx context.Context) (*sftp.Client, error) {
	// Try to get a connection from the pool
	select {
	case client := <-t.connPool:
		if t.isConnectionAlive(client) {
			return client, nil
		}
		// Connection is dead, close it and create a new one
		client.Close()
	default:
		// Pool is empty, create a new connection
	}

	return t.connect(ctx)
}

// returnConnection returns a connection to the pool or closes it if the pool is full
func (t *SFTPTarget) returnConnection(client *sftp.Client) {
	if client == nil {
		return
	}

	select {
	case t.connPool <- client:
		// Connection returned to pool
	default:
		// Pool is full, close the connection
		client.Close()
	}
}

// isConnectionAlive checks if a connection is still usable
func (t *SFTPTarget) isConnectionAlive(client *sftp.Client) bool {
	if client == nil {
		return false
	}
	// Try a simple stat operation to check connection
	_, err := client.Stat(".")
	return err == nil
}

// isTransientError checks if an error is likely temporary
func (t *SFTPTarget) isTransientError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "temporary") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no route to host") ||
		strings.Contains(errStr, "connection closed") ||
		strings.Contains(errStr, "EOF")
}

// withRetry executes an operation with retry logic
func (t *SFTPTarget) withRetry(ctx context.Context, op func(*sftp.Client) error) error {
	var lastErr error
	for i := 0; i < t.config.MaxRetries; i++ {
		select {
		case <-ctx.Done():
			return backup.NewError(backup.ErrCanceled, "sftp: operation canceled", ctx.Err())
		default:
		}

		client, err := t.getConnection(ctx)
		if err != nil {
			lastErr = err
			if !t.isTransientError(err) {
				return err
			}
			time.Sleep(t.config.RetryBackoff * time.Duration(i+1))
			continue
		}

		err = op(client)
		if err == nil {
			t.returnConnection(client)
			return nil
		}

		lastErr = err
		client.Close()

		if !t.isTransientError(err) {
			return err
		}

		if t.config.Debug {
			fmt.Printf("SFTP: Retrying operation after error: %v (attempt %d/%d)\n", err, i+1, t.config.MaxRetries)
		}
		time.Sleep(t.config.RetryBackoff * time.Duration(i+1))
	}

	return backup.NewError(backup.ErrIO, "sftp: operation failed after retries", lastErr)
}

// connect establishes an SFTP connection
func (t *SFTPTarget) connect(ctx context.Context) (*sftp.Client, error) {
	// Create a channel for connection results
	type connResult struct {
		client *sftp.Client
		err    error
	}
	resultChan := make(chan connResult, 1)

	// Start connection in a goroutine
	go func() {
		// Prepare SSH client configuration
		config := &ssh.ClientConfig{
			User:    t.config.Username,
			Timeout: t.config.Timeout,
		}

		// Set host key callback based on known_hosts file
		if t.config.KnownHostFile != "" {
			callback, err := knownHostsCallback(t.config.KnownHostFile)
			if err != nil {
				resultChan <- connResult{nil, backup.NewError(backup.ErrValidation, "sftp: failed to setup known hosts", err)}
				return
			}
			config.HostKeyCallback = callback
		} else {
			resultChan <- connResult{nil, backup.NewError(backup.ErrValidation, "sftp: known_hosts file is required for secure host key verification", nil)}
			return
		}

		// Set authentication method
		switch {
		case t.config.KeyFile != "":
			key, err := os.ReadFile(t.config.KeyFile)
			if err != nil {
				resultChan <- connResult{nil, backup.NewError(backup.ErrIO, "sftp: failed to read private key", err)}
				return
			}

			signer, err := ssh.ParsePrivateKey(key)
			if err != nil {
				resultChan <- connResult{nil, backup.NewError(backup.ErrValidation, "sftp: failed to parse private key", err)}
				return
			}
			config.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
		case t.config.Password != "":
			config.Auth = []ssh.AuthMethod{ssh.Password(t.config.Password)}
		default:
			resultChan <- connResult{nil, backup.NewError(backup.ErrValidation, "sftp: no authentication method provided", nil)}
			return
		}

		// Connect to SSH server
		addr := fmt.Sprintf("%s:%d", t.config.Host, t.config.Port)
		sshConn, err := ssh.Dial("tcp", addr, config)
		if err != nil {
			resultChan <- connResult{nil, backup.NewError(backup.ErrIO, "sftp: failed to connect", err)}
			return
		}

		// Create SFTP client
		client, err := sftp.NewClient(sshConn)
		if err != nil {
			sshConn.Close()
			resultChan <- connResult{nil, backup.NewError(backup.ErrIO, "sftp: failed to create client", err)}
			return
		}

		resultChan <- connResult{client, nil}
	}()

	// Wait for either context cancellation or connection completion
	select {
	case <-ctx.Done():
		return nil, backup.NewError(backup.ErrCanceled, "sftp: connection attempt canceled", ctx.Err())
	case result := <-resultChan:
		return result.client, result.err
	}
}

// validatePath performs security checks on a path to prevent directory traversal
// and ensure proper path formatting
func (t *SFTPTarget) validatePath(pathToCheck string) error {
	if pathToCheck == "" {
		return backup.NewError(backup.ErrValidation, "path cannot be empty", nil)
	}

	// Clean the path according to the current OS rules
	clean := filepath.Clean(pathToCheck)

	// Check for directory traversal attempts
	if strings.Contains(clean, "..") {
		return backup.NewError(backup.ErrSecurity, "path contains directory traversal", nil)
	}

	// Convert to forward slashes for SFTP (which uses Unix-style paths)
	clean = filepath.ToSlash(clean)

	// Ensure path is relative (doesn't start with /)
	if path.IsAbs(clean) {
		return backup.NewError(backup.ErrSecurity, "absolute paths are not allowed", nil)
	}

	// Check for suspicious path components
	components := strings.Split(clean, "/")
	for _, component := range components {
		// Skip empty components
		if component == "" {
			continue
		}

		// Check for hidden files/directories
		if strings.HasPrefix(component, ".") {
			// Debug print offending component
			if t.config.Debug {
				fmt.Printf("SFTP: Hidden file/directory: %s\n", component)
			}
			return backup.NewError(backup.ErrSecurity, "hidden files/directories are not allowed", nil)
		}

		// Check for suspicious characters
		if strings.ContainsAny(component, "<>:\"\\|?*") {
			return backup.NewError(backup.ErrSecurity, "path contains invalid characters", nil)
		}

		// Check component length
		if len(component) > 255 {
			return backup.NewError(backup.ErrSecurity, "path component exceeds maximum length", nil)
		}
	}

	// Validate total path length
	if len(clean) > 4096 {
		return backup.NewError(backup.ErrSecurity, "path exceeds maximum length", nil)
	}

	return nil
}

// Store implements the backup.Target interface
func (t *SFTPTarget) Store(ctx context.Context, sourcePath string, metadata *backup.Metadata) error {
	// Validate the target path
	targetPath := path.Join(t.config.BasePath, filepath.Base(sourcePath))
	if err := t.validatePath(targetPath); err != nil {
		return err
	}

	// Marshal metadata
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return backup.NewError(backup.ErrIO, "sftp: failed to marshal metadata", err)
	}

	return t.withRetry(ctx, func(client *sftp.Client) error {
		// Ensure the target directory exists
		if err := t.createDirectory(client, t.config.BasePath); err != nil {
			return err
		}

		// Store the backup file atomically
		backupPath := path.Join(t.config.BasePath, filepath.Base(sourcePath))
		if err := t.atomicUpload(ctx, client, sourcePath, backupPath); err != nil {
			return err
		}

		// Store metadata file
		metadataPath := backupPath + sftpMetadataFileExt
		tempMetadataFile, err := os.CreateTemp("", "sftp-metadata-*")
		if err != nil {
			return backup.NewError(backup.ErrIO, "sftp: failed to create temporary metadata file", err)
		}
		defer os.Remove(tempMetadataFile.Name())
		defer tempMetadataFile.Close()

		if _, err := tempMetadataFile.Write(metadataBytes); err != nil {
			return backup.NewError(backup.ErrIO, "sftp: failed to write metadata", err)
		}
		if err := tempMetadataFile.Sync(); err != nil {
			return backup.NewError(backup.ErrIO, "sftp: failed to sync metadata file", err)
		}
		if err := tempMetadataFile.Close(); err != nil {
			return backup.NewError(backup.ErrIO, "sftp: failed to close metadata file", err)
		}

		// Upload metadata file atomically
		if err := t.atomicUpload(ctx, client, tempMetadataFile.Name(), metadataPath); err != nil {
			return backup.NewError(backup.ErrIO, "sftp: failed to store metadata", err)
		}

		return nil
	})
}

// atomicUpload performs an atomic upload operation using a temporary file
func (t *SFTPTarget) atomicUpload(ctx context.Context, client *sftp.Client, localPath, remotePath string) error {
	// Validate the remote path
	if err := t.validatePath(remotePath); err != nil {
		return err
	}

	// Create a temporary filename
	tempFile, err := os.CreateTemp(filepath.Dir(localPath), "sftp-upload-*")
	if err != nil {
		return backup.NewError(backup.ErrIO, "sftp: failed to create temporary file", err)
	}
	tempName := tempFile.Name()
	tempFile.Close()
	os.Remove(tempName) // Remove the local temp file as we only need its name pattern

	// Create the remote temp file name using the same base name
	tempName = path.Join(path.Dir(remotePath), filepath.Base(tempName))
	if err := t.validatePath(tempName); err != nil {
		return err
	}

	t.trackTempFile(tempName)
	defer t.untrackTempFile(tempName)

	// Upload to temporary file
	if err := t.uploadFile(ctx, client, localPath, tempName); err != nil {
		// Try to clean up the temporary file
		_ = client.Remove(tempName)
		return err
	}

	// Rename to final destination
	if err := client.Rename(tempName, remotePath); err != nil {
		// Try to clean up the temporary file
		_ = client.Remove(tempName)
		return backup.NewError(backup.ErrIO, "sftp: failed to rename temporary file", err)
	}

	return nil
}

// uploadFile handles the actual file upload with progress tracking
func (t *SFTPTarget) uploadFile(ctx context.Context, client *sftp.Client, localPath, remotePath string) error {
	file, err := os.Open(localPath)
	if err != nil {
		return backup.NewError(backup.ErrIO, "sftp: failed to open local file", err)
	}
	defer file.Close()

	dstFile, err := client.Create(remotePath)
	if err != nil {
		return backup.NewError(backup.ErrIO, "sftp: failed to create remote file", err)
	}
	defer dstFile.Close()

	// Create a pipe for streaming with context cancellation
	pr, pw := io.Pipe()
	errChan := make(chan error, 1)

	go func() {
		defer pw.Close()
		_, err := io.Copy(pw, file)
		errChan <- err
	}()

	// Copy data with context cancellation support
	go func() {
		_, err := io.Copy(dstFile, pr)
		if err != nil {
			errChan <- err
		}
	}()

	// Wait for completion or cancellation
	select {
	case <-ctx.Done():
		pr.Close()
		return backup.NewError(backup.ErrCanceled, "sftp: upload canceled", ctx.Err())
	case err := <-errChan:
		if err != nil {
			return backup.NewError(backup.ErrIO, "sftp: failed to upload file", err)
		}
	}

	return nil
}

// List implements the backup.Target interface
func (t *SFTPTarget) List(ctx context.Context) ([]backup.BackupInfo, error) {
	if t.config.Debug {
		fmt.Printf("🔄 SFTP: Listing backups from %s\n", t.config.Host)
	}

	var backups []backup.BackupInfo
	err := t.withRetry(ctx, func(client *sftp.Client) error {
		entries, err := client.ReadDir(t.config.BasePath)
		if err != nil {
			if strings.Contains(err.Error(), "no such file") {
				return nil
			}
			return backup.NewError(backup.ErrIO, "sftp: failed to list backups", err)
		}

		for _, entry := range entries {
			if !entry.IsDir() && !strings.HasPrefix(entry.Name(), "sftp-upload-") {
				// Skip metadata files
				if strings.HasSuffix(entry.Name(), sftpMetadataFileExt) {
					continue
				}

				backups = append(backups, backup.BackupInfo{
					Target: entry.Name(),
					Metadata: backup.Metadata{
						Timestamp: entry.ModTime(),
						Size:      entry.Size(),
					},
				})
			}

			select {
			case <-ctx.Done():
				return backup.NewError(backup.ErrCanceled, "sftp: listing operation canceled", ctx.Err())
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
func (t *SFTPTarget) Delete(ctx context.Context, target string) error {
	if t.config.Debug {
		fmt.Printf("🔄 SFTP: Deleting backup %s from %s\n", target, t.config.Host)
	}

	// Validate the target path
	targetPath := path.Join(t.config.BasePath, target)
	if err := t.validatePath(targetPath); err != nil {
		return err
	}

	return t.withRetry(ctx, func(client *sftp.Client) error {
		backupPath := path.Join(t.config.BasePath, target)
		if err := client.Remove(backupPath); err != nil {
			return backup.NewError(backup.ErrIO, "sftp: failed to delete backup", err)
		}

		// Try to delete metadata file if it exists
		metadataPath := backupPath + sftpMetadataFileExt
		_ = client.Remove(metadataPath)

		if t.config.Debug {
			fmt.Printf("✅ SFTP: Successfully deleted backup %s\n", target)
		}

		return nil
	})
}

// Validate checks if the target configuration is valid
func (t *SFTPTarget) Validate() error {
	ctx, cancel := context.WithTimeout(context.Background(), t.config.Timeout)
	defer cancel()

	return t.withRetry(ctx, func(client *sftp.Client) error {
		// Try to create and remove a test directory
		testDir := path.Join(t.config.BasePath, "write_test_dir")
		if err := t.createDirectory(client, testDir); err != nil {
			return backup.NewError(backup.ErrValidation, "sftp: failed to create test directory", err)
		}

		// Create a test file
		testFile := path.Join(testDir, "test.txt")
		testData := []byte("test")
		tempFile, err := os.CreateTemp("", "sftp-test-*")
		if err != nil {
			return backup.NewError(backup.ErrIO, "sftp: failed to create temporary test file", err)
		}
		defer os.Remove(tempFile.Name())
		if _, err := tempFile.Write(testData); err != nil {
			return backup.NewError(backup.ErrIO, "sftp: failed to write test data", err)
		}
		if err := tempFile.Close(); err != nil {
			return backup.NewError(backup.ErrIO, "sftp: failed to close test file", err)
		}

		// Test atomic upload
		if err := t.atomicUpload(ctx, client, tempFile.Name(), testFile); err != nil {
			return backup.NewError(backup.ErrValidation, "sftp: failed to upload test file", err)
		}

		// Test file deletion
		if err := client.Remove(testFile); err != nil {
			fmt.Printf("⚠️ SFTP: Failed to delete test file %s: %v\n", testFile, err)
		}

		// Test directory deletion
		if err := client.RemoveDirectory(testDir); err != nil {
			fmt.Printf("⚠️ SFTP: Failed to remove test directory %s: %v\n", testDir, err)
		}

		return nil
	})
}

// Close implements proper resource cleanup
func (t *SFTPTarget) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	var lastErr error

	// Try to clean up temporary files with one last connection
	ctx, cancel := context.WithTimeout(context.Background(), t.config.Timeout)
	defer cancel()

	client, err := t.connect(ctx)
	if err == nil {
		t.cleanupTempFiles(client)
		if err := client.Close(); err != nil {
			lastErr = err
		}
	}

	// Close all pooled connections
	for {
		select {
		case client := <-t.connPool:
			if err := client.Close(); err != nil {
				lastErr = err
				if t.config.Debug {
					fmt.Printf("Warning: failed to close SFTP connection: %v\n", err)
				}
			}
		default:
			return lastErr
		}
	}
}

// Helper functions

func (t *SFTPTarget) createDirectory(client *sftp.Client, dirPath string) error {
	if err := client.MkdirAll(dirPath); err != nil {
		return backup.NewError(backup.ErrIO, fmt.Sprintf("sftp: failed to create directory %s", dirPath), err)
	}
	return nil
}

// trackTempFile adds a temporary file to the tracking map
func (t *SFTPTarget) trackTempFile(path string) {
	t.tempFilesMu.Lock()
	defer t.tempFilesMu.Unlock()
	t.tempFiles[path] = true
}

// untrackTempFile removes a temporary file from the tracking map
func (t *SFTPTarget) untrackTempFile(path string) {
	t.tempFilesMu.Lock()
	defer t.tempFilesMu.Unlock()
	delete(t.tempFiles, path)
}

// cleanupTempFiles attempts to clean up any tracked temporary files
func (t *SFTPTarget) cleanupTempFiles(client *sftp.Client) {
	t.tempFilesMu.Lock()
	tempFiles := make([]string, 0, len(t.tempFiles))
	for path := range t.tempFiles {
		tempFiles = append(tempFiles, path)
	}
	t.tempFilesMu.Unlock()

	for _, path := range tempFiles {
		if err := client.Remove(path); err != nil {
			if t.config.Debug {
				fmt.Printf("⚠️ SFTP: Failed to clean up temporary file %s: %v\n", path, err)
			}
		} else {
			t.untrackTempFile(path)
		}
	}
}

// knownHostsCallback creates a host key callback function from a known_hosts file
func knownHostsCallback(knownHostsFile string) (ssh.HostKeyCallback, error) {
	// Check if the known_hosts file exists
	if _, err := os.Stat(knownHostsFile); os.IsNotExist(err) {
		// Create the .ssh directory if it doesn't exist
		sshDir := filepath.Dir(knownHostsFile)
		if err := os.MkdirAll(sshDir, 0o700); err != nil {
			return nil, backup.NewError(backup.ErrIO, "failed to create .ssh directory", err)
		}
		// Create an empty known_hosts file
		if err := os.WriteFile(knownHostsFile, []byte{}, 0o600); err != nil {
			return nil, backup.NewError(backup.ErrIO, "failed to create known_hosts file", err)
		}
	}

	callback, err := knownhosts.New(knownHostsFile)
	if err != nil {
		return nil, backup.NewError(backup.ErrValidation, "failed to parse known_hosts file", err)
	}
	return callback, nil
}
