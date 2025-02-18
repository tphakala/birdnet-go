// Package targets provides backup target implementations
package targets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/tphakala/birdnet-go/internal/backup"
)

// FTPTarget implements the backup.Target interface for FTP storage
type FTPTarget struct {
	host     string
	port     int
	username string
	password string
	basePath string
	timeout  time.Duration
	debug    bool
	logger   backup.Logger
}

// FTPTargetConfig holds configuration for the FTP target
type FTPTargetConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	BasePath string
	Timeout  time.Duration
	Debug    bool
}

// NewFTPTarget creates a new FTP target with the given configuration
func NewFTPTarget(settings map[string]interface{}) (*FTPTarget, error) {
	target := &FTPTarget{}

	// Required settings
	host, ok := settings["host"].(string)
	if !ok {
		return nil, fmt.Errorf("ftp: host is required")
	}
	target.host = host

	// Optional settings with defaults
	if port, ok := settings["port"].(int); ok {
		target.port = port
	} else {
		target.port = 21 // Default FTP port
	}

	if username, ok := settings["username"].(string); ok {
		target.username = username
	}

	if password, ok := settings["password"].(string); ok {
		target.password = password
	}

	if path, ok := settings["path"].(string); ok {
		target.basePath = strings.TrimRight(path, "/")
	} else {
		target.basePath = "backups"
	}

	if timeout, ok := settings["timeout"].(string); ok {
		duration, err := time.ParseDuration(timeout)
		if err != nil {
			return nil, fmt.Errorf("ftp: invalid timeout format: %w", err)
		}
		target.timeout = duration
	} else {
		target.timeout = 30 * time.Second // Default timeout
	}

	if debug, ok := settings["debug"].(bool); ok {
		target.debug = debug
	}

	if logger, ok := settings["logger"].(backup.Logger); ok {
		target.logger = logger
	} else {
		target.logger = backup.DefaultLogger()
	}

	return target, nil
}

// Name returns the name of this target
func (t *FTPTarget) Name() string {
	return "ftp"
}

// connect establishes a connection to the FTP server with context support
func (t *FTPTarget) connect(ctx context.Context) (*ftp.ServerConn, error) {
	// Create a channel to handle connection timeout
	connChan := make(chan *ftp.ServerConn, 1)
	errChan := make(chan error, 1)

	go func() {
		addr := fmt.Sprintf("%s:%d", t.host, t.port)
		conn, err := ftp.Dial(addr, ftp.DialWithTimeout(t.timeout))
		if err != nil {
			errChan <- fmt.Errorf("ftp: connection failed: %w", err)
			return
		}

		if t.username != "" {
			if err := conn.Login(t.username, t.password); err != nil {
				if quitErr := conn.Quit(); quitErr != nil {
					t.logger.Printf("Warning: failed to quit FTP connection after login error: %v", quitErr)
				}
				errChan <- fmt.Errorf("ftp: login failed: %w", err)
				return
			}
		}

		connChan <- conn
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-errChan:
		return nil, err
	case conn := <-connChan:
		return conn, nil
	}
}

// createDirectory ensures the target directory exists on the FTP server
func (t *FTPTarget) createDirectory(ctx context.Context, conn *ftp.ServerConn, path string) error {
	parts := strings.Split(path, "/")
	currentPath := ""

	for _, part := range parts {
		if part == "" {
			continue
		}

		currentPath += "/" + part
		err := conn.MakeDir(currentPath)
		if err != nil {
			// Ignore directory exists error
			if !strings.Contains(err.Error(), "File exists") {
				return fmt.Errorf("ftp: failed to create directory %s: %w", currentPath, err)
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}

	return nil
}

// Store implements the backup.Target interface
func (t *FTPTarget) Store(ctx context.Context, sourcePath string, metadata *backup.Metadata) error {
	if t.debug {
		log.Printf("FTP: Storing backup %s to %s", filepath.Base(sourcePath), t.host)
	}

	// Open source file
	srcFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("ftp: failed to open source file: %w", err)
	}
	defer srcFile.Close()

	conn, err := t.connect(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := conn.Quit(); err != nil {
			t.logger.Printf("Warning: failed to quit FTP connection: %v", err)
		}
	}()

	// Ensure the target directory exists
	if err := t.createDirectory(ctx, conn, t.basePath); err != nil {
		return err
	}

	// Store the backup file
	backupPath := path.Join(t.basePath, filepath.Base(sourcePath))
	if err := conn.Stor(backupPath, srcFile); err != nil {
		if quitErr := conn.Quit(); quitErr != nil {
			t.logger.Printf("Warning: failed to quit FTP connection after store error: %v", quitErr)
		}
		return fmt.Errorf("ftp: failed to store backup: %w", err)
	}

	if t.debug {
		log.Printf("FTP: Successfully stored backup %s", filepath.Base(sourcePath))
	}

	return nil
}

// List implements the backup.Target interface
func (t *FTPTarget) List(ctx context.Context) ([]backup.BackupInfo, error) {
	if t.debug {
		log.Printf("FTP: Listing backups from %s", t.host)
	}

	conn, err := t.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := conn.Quit(); err != nil {
			t.logger.Printf("Warning: failed to quit FTP connection: %v", err)
		}
	}()

	entries, err := conn.List(t.basePath)
	if err != nil {
		if strings.Contains(err.Error(), "No such file or directory") {
			return []backup.BackupInfo{}, nil
		}
		return nil, fmt.Errorf("ftp: failed to list backups: %w", err)
	}

	var backups []backup.BackupInfo
	for _, entry := range entries {
		if entry.Type == ftp.EntryTypeFile {
			backups = append(backups, backup.BackupInfo{
				Target: entry.Name,
				Metadata: backup.Metadata{
					Timestamp: entry.Time,
					Size:      int64(entry.Size), // Convert uint64 to int64
				},
			})
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}

	return backups, nil
}

// Delete implements the backup.Target interface
func (t *FTPTarget) Delete(ctx context.Context, target string) error {
	if t.debug {
		log.Printf("FTP: Deleting backup %s from %s", target, t.host)
	}

	conn, err := t.connect(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if err := conn.Quit(); err != nil {
			t.logger.Printf("Warning: failed to quit FTP connection: %v", err)
		}
	}()

	backupPath := path.Join(t.basePath, target)
	if err := conn.Delete(backupPath); err != nil {
		return fmt.Errorf("ftp: failed to delete backup: %w", err)
	}

	if t.debug {
		log.Printf("FTP: Successfully deleted backup %s", target)
	}

	return nil
}

// Validate checks if the target configuration is valid
func (t *FTPTarget) Validate() error {
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()

	// Try to connect to the FTP server
	conn, err := t.connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to validate FTP connection: %w", err)
	}
	defer func() {
		if err := conn.Quit(); err != nil {
			t.logger.Printf("Warning: failed to quit FTP connection: %v", err)
		}
	}()

	// Try to create and remove a test directory
	testDir := path.Join(t.basePath, ".write_test")
	if err := t.createDirectory(ctx, conn, testDir); err != nil {
		return fmt.Errorf("failed to create test directory: %w", err)
	}

	if err := conn.RemoveDir(testDir); err != nil {
		t.logger.Printf("Warning: failed to remove test directory %s: %v", testDir, err)
	}

	return nil
}

// Helper functions

func (t *FTPTarget) uploadFile(ctx context.Context, conn *ftp.ServerConn, localPath, remotePath string) error {
	// Open local file
	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open local file: %w", err)
	}
	defer file.Close()

	// Create a pipe for streaming
	pr, pw := io.Pipe()

	// Start upload in a goroutine
	errChan := make(chan error, 1)
	go func() {
		defer pw.Close()
		_, err := io.Copy(pw, file)
		if err != nil {
			errChan <- fmt.Errorf("failed to copy file data: %w", err)
			return
		}
		errChan <- nil
	}()

	// Store the file on the FTP server
	err = conn.Stor(remotePath, pr)
	if err != nil {
		return fmt.Errorf("failed to store file on FTP server: %w", err)
	}

	// Wait for upload completion or context cancellation
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errChan:
		return err
	}
}

func (t *FTPTarget) listDirectory(ctx context.Context, conn *ftp.ServerConn, dir string) ([]*ftp.Entry, error) {
	// Create channels for result and error
	entriesChan := make(chan []*ftp.Entry)
	errChan := make(chan error)

	go func() {
		entries, err := conn.List(dir)
		if err != nil {
			errChan <- fmt.Errorf("failed to list directory: %w", err)
			return
		}
		entriesChan <- entries
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-errChan:
		return nil, err
	case entries := <-entriesChan:
		return entries, nil
	}
}

func (t *FTPTarget) readMetadata(ctx context.Context, conn *ftp.ServerConn, path string) (backup.Metadata, error) {
	var metadata backup.Metadata

	// Create a pipe for reading
	pr, pw := io.Pipe()

	// Start download in a goroutine
	errChan := make(chan error, 1)
	go func() {
		defer pw.Close()
		resp, err := conn.Retr(path)
		if err != nil {
			errChan <- fmt.Errorf("failed to retrieve metadata file: %w", err)
			return
		}
		defer resp.Close()

		_, err = io.Copy(pw, resp)
		if err != nil {
			errChan <- fmt.Errorf("failed to copy metadata file: %w", err)
			return
		}
		errChan <- nil
	}()

	// Read and decode metadata
	decoder := json.NewDecoder(pr)
	if err := decoder.Decode(&metadata); err != nil {
		return metadata, fmt.Errorf("failed to decode metadata: %w", err)
	}

	// Wait for download completion or context cancellation
	select {
	case <-ctx.Done():
		return metadata, ctx.Err()
	case err := <-errChan:
		return metadata, err
	}
}
