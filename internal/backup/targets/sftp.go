package targets

import (
	"context"
	"encoding/json"
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

	"github.com/pkg/sftp"
	"github.com/tphakala/birdnet-go/internal/backup"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// SFTP-specific constants (shared constants imported from common.go)
const (
	sftpTempFilePrefix  = "tmp-"
	sftpMetadataFileExt = ".meta"
	sftpMetadataVersion = 1
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
	log         logger.Logger
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
func NewSFTPTarget(settings map[string]any, lg logger.Logger) (*SFTPTarget, error) {
	p := NewSettingsParser(settings)

	config := SFTPTargetConfig{
		// Required settings
		Host:     p.RequireString("host", "sftp"),
		BasePath: p.RequirePath("path", "sftp", true), // preserveRoot=true for "/"

		// Optional settings with defaults
		Port:          p.OptionalInt("port", DefaultSSHPort),
		Username:      p.OptionalString("username", ""),
		Password:      p.OptionalString("password", ""),
		KeyFile:       p.OptionalString("key_file", ""),
		KnownHostFile: p.OptionalString("known_hosts_file", DefaultKnownHostsFile()),
		Timeout:       p.OptionalDuration("timeout", DefaultTimeout, "sftp"),
		Debug:         p.OptionalBool("debug", false),

		// Fixed defaults
		MaxRetries:   DefaultMaxRetries,
		RetryBackoff: DefaultRetryBackoff,
		MaxConns:     DefaultMaxConns,
	}

	if err := p.Error(); err != nil {
		return nil, err
	}

	if lg == nil {
		lg = logger.Global().Module("backup")
	}

	return &SFTPTarget{
		config:    config,
		connPool:  make(chan *sftp.Client, config.MaxConns),
		tempFiles: make(map[string]bool),
		log:       lg.Module("sftp"),
	}, nil
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
		if err := client.Close(); err != nil {
			if t.config.Debug {
				t.log.Debug("SFTP: Failed to close dead connection", logger.Error(err))
			}
		}
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
		if err := client.Close(); err != nil {
			if t.config.Debug {
				t.log.Debug("SFTP: Failed to close excess connection", logger.Error(err))
			}
		}
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
// Delegates to the shared implementation in common.go
func (t *SFTPTarget) isTransientError(err error) bool {
	return IsTransientError(err)
}

// withRetry executes an operation with retry logic
func (t *SFTPTarget) withRetry(ctx context.Context, op func(*sftp.Client) error) error {
	var lastErr error
	for attempt := range t.config.MaxRetries {
		select {
		case <-ctx.Done():
			return errors.New(ctx.Err()).
				Component("backup").
				Category(errors.CategorySystem).
				Context("operation", "sftp_retry").
				Context("error_type", "cancelled").
				Build()
		default:
		}

		client, err := t.getConnection(ctx)
		if err != nil {
			lastErr = err
			if !t.isTransientError(err) {
				return err
			}
			time.Sleep(t.config.RetryBackoff * time.Duration(attempt+1))
			continue
		}

		if err = op(client); err == nil {
			t.returnConnection(client)
			return nil
		}

		lastErr = err
		if closeErr := client.Close(); closeErr != nil && t.config.Debug {
			t.log.Debug("SFTP: Failed to close connection after operation error", logger.Error(closeErr))
		}

		if !t.isTransientError(err) {
			return err
		}

		if t.config.Debug {
			t.log.Debug("SFTP: Retrying operation after error",
				logger.Error(err),
				logger.Int("attempt", attempt+1),
				logger.Int("max_retries", t.config.MaxRetries))
		}
		time.Sleep(t.config.RetryBackoff * time.Duration(attempt+1))
	}

	return errors.New(lastErr).
		Component("backup").
		Category(errors.CategoryNetwork).
		Context("operation", "sftp_retry").
		Context("max_retries", t.config.MaxRetries).
		Build()
}

// setupSSHConfig creates and configures an SSH client config with authentication.
// Returns the configured SSH client config or an error if setup fails.
func (t *SFTPTarget) setupSSHConfig() (*ssh.ClientConfig, error) {
	config := &ssh.ClientConfig{
		User:    t.config.Username,
		Timeout: t.config.Timeout,
	}

	// Set host key callback - known_hosts file is required
	if t.config.KnownHostFile == "" {
		return nil, errors.Newf("sftp: known_hosts file is required for secure host key verification").
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "verify_known_hosts").
			Build()
	}

	callback, err := knownHostsCallback(t.config.KnownHostFile)
	if err != nil {
		return nil, errors.New(err).
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "setup_known_hosts").
			Build()
	}
	config.HostKeyCallback = callback

	// Set authentication method
	switch {
	case t.config.KeyFile != "":
		key, err := os.ReadFile(t.config.KeyFile)
		if err != nil {
			return nil, errors.New(err).
				Component("backup").
				Category(errors.CategoryFileIO).
				Context("operation", "read_private_key").
				Build()
		}
		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, errors.New(err).
				Component("backup").
				Category(errors.CategoryValidation).
				Context("operation", "parse_private_key").
				Build()
		}
		config.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	case t.config.Password != "":
		config.Auth = []ssh.AuthMethod{ssh.Password(t.config.Password)}
	default:
		return nil, errors.Newf("sftp: no authentication method provided").
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "verify_auth_method").
			Build()
	}

	return config, nil
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
		// Setup SSH configuration using helper
		config, err := t.setupSSHConfig()
		if err != nil {
			resultChan <- connResult{nil, err}
			return
		}

		// Connect to SSH server
		// Use net.JoinHostPort for proper IPv6 support (handles bracketing automatically)
		addr := net.JoinHostPort(t.config.Host, strconv.Itoa(t.config.Port))
		sshConn, err := ssh.Dial("tcp", addr, config)
		if err != nil {
			resultChan <- connResult{nil, errors.New(err).
				Component("backup").
				Category(errors.CategoryNetwork).
				Context("operation", "sftp_connect").
				Context("host", t.config.Host).
				Build()}
			return
		}

		// Create SFTP client
		client, err := sftp.NewClient(sshConn)
		if err != nil {
			if closeErr := sshConn.Close(); closeErr != nil && t.config.Debug {
				t.log.Debug("SFTP: Failed to close SSH connection after client creation failure", logger.Error(closeErr))
			}
			resultChan <- connResult{nil, errors.New(err).
				Component("backup").
				Category(errors.CategoryNetwork).
				Context("operation", "create_sftp_client").
				Build()}
			return
		}

		resultChan <- connResult{client, nil}
	}()

	// Wait for either context cancellation or connection completion
	select {
	case <-ctx.Done():
		return nil, errors.New(ctx.Err()).
			Component("backup").
			Category(errors.CategorySystem).
			Context("operation", "sftp_connect").
			Context("error_type", "cancelled").
			Build()
	case result := <-resultChan:
		return result.client, result.err
	}
}

// validatePath performs security checks on a path to prevent directory traversal
// and ensure proper path formatting. Uses shared ValidatePathWithOpts.
func (t *SFTPTarget) validatePath(pathToCheck string) error {
	_, err := ValidatePathWithOpts(pathToCheck, PathValidationOpts{
		AllowHidden:    false,
		AllowAbsolute:  false,
		ConvertToSlash: true, // SFTP uses Unix-style paths
		ReturnCleaned:  false,
	})
	return err
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
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "marshal_metadata").
			Build()
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

		// Store metadata file using shared helper
		metadataPath := backupPath + sftpMetadataFileExt
		tempResult, err := WriteTempFile(metadataBytes, "sftp-metadata")
		if err != nil {
			return err
		}
		defer tempResult.Cleanup()

		// Upload metadata file atomically
		if err := t.atomicUpload(ctx, client, tempResult.Path, metadataPath); err != nil {
			return errors.New(err).
				Component("backup").
				Category(errors.CategoryNetwork).
				Context("operation", "store_metadata").
				Build()
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
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "create_temp_file").
			Build()
	}
	tempName := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		if t.config.Debug {
			t.log.Debug("SFTP: Failed to close temp file", logger.String("path", tempName), logger.Error(err))
		}
	}
	if err := os.Remove(tempName); err != nil && !os.IsNotExist(err) {
		if t.config.Debug {
			t.log.Debug("SFTP: Failed to remove local temp file", logger.String("path", tempName), logger.Error(err))
		}
	}

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
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryNetwork).
			Context("operation", "rename_temp_file").
			Context("temp_name", tempName).
			Context("remote_path", remotePath).
			Build()
	}

	return nil
}

// uploadFile handles the actual file upload with progress tracking
func (t *SFTPTarget) uploadFile(ctx context.Context, client *sftp.Client, localPath, remotePath string) error {
	// Open local file (from trusted internal backup manager temp directory)
	file, err := os.Open(localPath) //nolint:gosec // G304 - localPath is a trusted internal temp path from backup manager
	if err != nil {
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "open_source_file").
			Context("local_path", localPath).
			Build()
	}
	defer func() {
		if err := file.Close(); err != nil {
			if t.config.Debug {
				t.log.Debug("SFTP: Failed to close local file", logger.String("path", localPath), logger.Error(err))
			}
		}
	}()

	dstFile, err := client.Create(remotePath)
	if err != nil {
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryNetwork).
			Context("operation", "create_remote_file").
			Context("remote_path", remotePath).
			Build()
	}
	defer func() {
		if err := dstFile.Close(); err != nil {
			if t.config.Debug {
				t.log.Debug("SFTP: Failed to close remote file", logger.String("path", remotePath), logger.Error(err))
			}
		}
	}()

	// Create a pipe for streaming with context cancellation
	pr, pw := io.Pipe()

	// Use WaitGroup.Go (Go 1.25) to properly wait for both goroutines
	var wg sync.WaitGroup
	var copyErr, uploadErr error

	// Goroutine 1: Copy from local file to pipe writer
	wg.Go(func() {
		defer func() {
			if err := pw.Close(); err != nil {
				if t.config.Debug {
					t.log.Debug("SFTP: Failed to close pipe writer", logger.Error(err))
				}
			}
		}()
		_, copyErr = io.Copy(pw, file)
		if copyErr != nil {
			// Signal reader to stop on error
			pr.CloseWithError(copyErr)
		}
	})

	// Goroutine 2: Copy from pipe reader to remote file
	wg.Go(func() {
		_, uploadErr = io.Copy(dstFile, pr)
	})

	// Wait for both goroutines to complete with context cancellation support
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		// Cancel by closing both ends of the pipe
		pr.CloseWithError(ctx.Err())
		pw.CloseWithError(ctx.Err())
		<-done // Wait for goroutines to finish after cancellation
		return errors.New(ctx.Err()).
			Component("backup").
			Category(errors.CategorySystem).
			Context("operation", "upload_file").
			Context("error_type", "cancelled").
			Build()
	case <-done:
		// Both goroutines completed, check for errors
		if copyErr != nil {
			return errors.New(copyErr).
				Component("backup").
				Category(errors.CategoryFileIO).
				Context("operation", "copy_to_pipe").
				Build()
		}
		if uploadErr != nil {
			return errors.New(uploadErr).
				Component("backup").
				Category(errors.CategoryNetwork).
				Context("operation", "upload_file").
				Build()
		}
	}

	return nil
}

// List implements the backup.Target interface
func (t *SFTPTarget) List(ctx context.Context) ([]backup.BackupInfo, error) {
	if t.config.Debug {
		t.log.Debug("SFTP: Listing backups",
			logger.String("host", t.config.Host))
	}

	var backups []backup.BackupInfo
	err := t.withRetry(ctx, func(client *sftp.Client) error {
		entries, err := client.ReadDir(t.config.BasePath)
		if err != nil {
			if strings.Contains(err.Error(), "no such file") {
				return nil
			}
			return errors.New(err).
				Component("backup").
				Category(errors.CategoryNetwork).
				Context("operation", "list_backups").
				Build()
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
				return errors.New(ctx.Err()).
					Component("backup").
					Category(errors.CategorySystem).
					Context("operation", "list_backups").
					Context("error_type", "cancelled").
					Build()
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
		t.log.Debug("SFTP: Deleting backup",
			logger.String("target", target),
			logger.String("host", t.config.Host))
	}

	// Validate the target path
	targetPath := path.Join(t.config.BasePath, target)
	if err := t.validatePath(targetPath); err != nil {
		return err
	}

	return t.withRetry(ctx, func(client *sftp.Client) error {
		backupPath := path.Join(t.config.BasePath, target)
		if err := client.Remove(backupPath); err != nil {
			return errors.New(err).
				Component("backup").
				Category(errors.CategoryNetwork).
				Context("operation", "delete_backup").
				Context("target", target).
				Build()
		}

		// Try to delete metadata file if it exists
		metadataPath := backupPath + sftpMetadataFileExt
		_ = client.Remove(metadataPath)

		if t.config.Debug {
			t.log.Debug("SFTP: Successfully deleted backup",
				logger.String("target", target))
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
			return errors.New(err).
				Component("backup").
				Category(errors.CategoryValidation).
				Context("operation", "validate_create_test_dir").
				Build()
		}

		// Create a test file
		testFile := path.Join(testDir, "test.txt")
		testData := []byte("test")
		tempFile, err := os.CreateTemp("", "sftp-test-*")
		if err != nil {
			return errors.New(err).
				Component("backup").
				Category(errors.CategoryFileIO).
				Context("operation", "validate_create_test_file").
				Build()
		}
		defer func() {
			if err := os.Remove(tempFile.Name()); err != nil && !os.IsNotExist(err) {
				if t.config.Debug {
					t.log.Debug("SFTP: Failed to remove test file", logger.String("path", tempFile.Name()), logger.Error(err))
				}
			}
		}()
		if _, err := tempFile.Write(testData); err != nil {
			return errors.New(err).
				Component("backup").
				Category(errors.CategoryFileIO).
				Context("operation", "validate_write_test_data").
				Build()
		}
		if err := tempFile.Close(); err != nil {
			return errors.New(err).
				Component("backup").
				Category(errors.CategoryFileIO).
				Context("operation", "validate_close_test_file").
				Build()
		}

		// Test atomic upload
		if err := t.atomicUpload(ctx, client, tempFile.Name(), testFile); err != nil {
			return errors.New(err).
				Component("backup").
				Category(errors.CategoryValidation).
				Context("operation", "validate_upload_test_file").
				Build()
		}

		// Test file deletion
		if err := client.Remove(testFile); err != nil {
			t.log.Warn("SFTP: Failed to delete test file",
				logger.String("test_file", testFile),
				logger.Error(err))
		}

		// Test directory deletion
		if err := client.RemoveDirectory(testDir); err != nil {
			t.log.Warn("SFTP: Failed to remove test directory",
				logger.String("test_dir", testDir),
				logger.Error(err))
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
					t.log.Warn("Failed to close SFTP connection",
						logger.Error(err))
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
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryNetwork).
			Context("operation", "create_directory").
			Context("dir_path", dirPath).
			Build()
	}
	return nil
}

// trackTempFile adds a temporary file to the tracking map
func (t *SFTPTarget) trackTempFile(filePath string) {
	t.tempFilesMu.Lock()
	defer t.tempFilesMu.Unlock()
	t.tempFiles[filePath] = true
}

// untrackTempFile removes a temporary file from the tracking map
func (t *SFTPTarget) untrackTempFile(filePath string) {
	t.tempFilesMu.Lock()
	defer t.tempFilesMu.Unlock()
	delete(t.tempFiles, filePath)
}

// cleanupTempFiles attempts to clean up any tracked temporary files
func (t *SFTPTarget) cleanupTempFiles(client *sftp.Client) {
	t.tempFilesMu.Lock()
	tempFiles := slices.Collect(maps.Keys(t.tempFiles))
	t.tempFilesMu.Unlock()

	for _, path := range tempFiles {
		if err := client.Remove(path); err != nil {
			if t.config.Debug {
				t.log.Warn("SFTP: Failed to clean up temporary file",
					logger.String("path", path),
					logger.Error(err))
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
		if err := os.MkdirAll(sshDir, PermDir); err != nil {
			return nil, errors.New(err).
				Component("backup").
				Category(errors.CategoryFileIO).
				Context("operation", "create_ssh_directory").
				Build()
		}
		// Create an empty known_hosts file
		if err := os.WriteFile(knownHostsFile, []byte{}, PermFile); err != nil {
			return nil, errors.New(err).
				Component("backup").
				Category(errors.CategoryFileIO).
				Context("operation", "create_known_hosts_file").
				Build()
		}
		// Warn user that SSH connections will fail until host keys are added
		GetLogger().Warn("Created empty known_hosts file - SSH connections will fail until host keys are added",
			logString("path", knownHostsFile),
			logString("hint", "Run 'ssh-keyscan <hostname> >> "+knownHostsFile+"' to add host keys"))
	}

	callback, err := knownhosts.New(knownHostsFile)
	if err != nil {
		return nil, errors.New(err).
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "parse_known_hosts_file").
			Build()
	}
	return callback, nil
}
