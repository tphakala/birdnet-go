package targets

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"github.com/tphakala/birdnet-go/internal/backup"
	"golang.org/x/crypto/ssh"
)

// SFTPTarget implements the backup.Target interface for SFTP storage
type SFTPTarget struct {
	host     string
	port     int
	username string
	password string
	keyFile  string
	basePath string
	timeout  time.Duration
	debug    bool
}

// NewSFTPTarget creates a new SFTP target with the given configuration
func NewSFTPTarget(settings map[string]interface{}) (*SFTPTarget, error) {
	target := &SFTPTarget{}

	// Required settings
	host, ok := settings["host"].(string)
	if !ok {
		return nil, fmt.Errorf("sftp: host is required")
	}
	target.host = host

	// Optional settings with defaults
	if port, ok := settings["port"].(int); ok {
		target.port = port
	} else {
		target.port = 22 // Default SFTP port
	}

	if username, ok := settings["username"].(string); ok {
		target.username = username
	}

	if password, ok := settings["password"].(string); ok {
		target.password = password
	}

	if keyFile, ok := settings["key_file"].(string); ok {
		target.keyFile = keyFile
	}

	if path, ok := settings["path"].(string); ok {
		target.basePath = strings.TrimRight(path, "/")
	} else {
		target.basePath = "backups"
	}

	if timeout, ok := settings["timeout"].(string); ok {
		duration, err := time.ParseDuration(timeout)
		if err != nil {
			return nil, fmt.Errorf("sftp: invalid timeout format: %w", err)
		}
		target.timeout = duration
	} else {
		target.timeout = 30 * time.Second // Default timeout
	}

	if debug, ok := settings["debug"].(bool); ok {
		target.debug = debug
	}

	return target, nil
}

// Name returns the name of this target
func (t *SFTPTarget) Name() string {
	return "sftp"
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
			User:            t.username,
			HostKeyCallback: ssh.InsecureIgnoreHostKey(), // Note: In production, use ssh.FixedHostKey() or ssh.KnownHosts()
			Timeout:         t.timeout,
		}

		// Set authentication method
		switch {
		case t.keyFile != "":
			key, err := os.ReadFile(t.keyFile)
			if err != nil {
				resultChan <- connResult{nil, fmt.Errorf("sftp: failed to read private key: %w", err)}
				return
			}

			signer, err := ssh.ParsePrivateKey(key)
			if err != nil {
				resultChan <- connResult{nil, fmt.Errorf("sftp: failed to parse private key: %w", err)}
				return
			}
			config.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
		case t.password != "":
			config.Auth = []ssh.AuthMethod{ssh.Password(t.password)}
		default:
			resultChan <- connResult{nil, fmt.Errorf("sftp: no authentication method provided")}
			return
		}

		// Connect to SSH server
		addr := fmt.Sprintf("%s:%d", t.host, t.port)
		sshConn, err := ssh.Dial("tcp", addr, config)
		if err != nil {
			resultChan <- connResult{nil, fmt.Errorf("sftp: failed to connect: %w", err)}
			return
		}

		// Create SFTP client
		client, err := sftp.NewClient(sshConn)
		if err != nil {
			sshConn.Close()
			resultChan <- connResult{nil, fmt.Errorf("sftp: failed to create client: %w", err)}
			return
		}

		resultChan <- connResult{client, nil}
	}()

	// Wait for either context cancellation or connection completion
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultChan:
		return result.client, result.err
	}
}

// Store implements the backup.Target interface
func (t *SFTPTarget) Store(ctx context.Context, info *backup.BackupInfo, reader io.Reader) error {
	if t.debug {
		fmt.Printf("SFTP: Storing backup %s to %s\n", info.Target, t.host)
	}

	client, err := t.connect(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	// Ensure the target directory exists
	if err := t.createDirectory(client, t.basePath); err != nil {
		return err
	}

	// Store the backup file
	backupPath := path.Join(t.basePath, info.Target)
	dstFile, err := client.Create(backupPath)
	if err != nil {
		return fmt.Errorf("sftp: failed to create file: %w", err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, reader); err != nil {
		return fmt.Errorf("sftp: failed to write file: %w", err)
	}

	if t.debug {
		fmt.Printf("SFTP: Successfully stored backup %s\n", info.Target)
	}

	return nil
}

// List implements the backup.Target interface
func (t *SFTPTarget) List(ctx context.Context) ([]backup.BackupInfo, error) {
	if t.debug {
		fmt.Printf("SFTP: Listing backups from %s\n", t.host)
	}

	client, err := t.connect(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	entries, err := client.ReadDir(t.basePath)
	if err != nil {
		if strings.Contains(err.Error(), "no such file") {
			return []backup.BackupInfo{}, nil
		}
		return nil, fmt.Errorf("sftp: failed to list backups: %w", err)
	}

	var backups []backup.BackupInfo
	for _, entry := range entries {
		if !entry.IsDir() {
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
			return nil, ctx.Err()
		default:
		}
	}

	return backups, nil
}

// Delete implements the backup.Target interface
func (t *SFTPTarget) Delete(ctx context.Context, target string) error {
	if t.debug {
		fmt.Printf("SFTP: Deleting backup %s from %s\n", target, t.host)
	}

	client, err := t.connect(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	backupPath := path.Join(t.basePath, target)
	if err := client.Remove(backupPath); err != nil {
		return fmt.Errorf("sftp: failed to delete backup: %w", err)
	}

	if t.debug {
		fmt.Printf("SFTP: Successfully deleted backup %s\n", target)
	}

	return nil
}

// Validate checks if the target configuration is valid
func (t *SFTPTarget) Validate() error {
	ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
	defer cancel()

	client, err := t.connect(ctx)
	if err != nil {
		return fmt.Errorf("failed to validate SFTP connection: %w", err)
	}
	defer client.Close()

	// Try to create and remove a test directory
	testDir := path.Join(t.basePath, ".write_test")
	if err := t.createDirectory(client, testDir); err != nil {
		return fmt.Errorf("failed to create test directory: %w", err)
	}

	if err := client.RemoveDirectory(testDir); err != nil {
		fmt.Printf("Warning: failed to remove test directory %s: %v\n", testDir, err)
	}

	return nil
}

// Helper functions

func (t *SFTPTarget) createDirectory(client *sftp.Client, dirPath string) error {
	if err := client.MkdirAll(dirPath); err != nil {
		return fmt.Errorf("sftp: failed to create directory %s: %w", dirPath, err)
	}
	return nil
}
