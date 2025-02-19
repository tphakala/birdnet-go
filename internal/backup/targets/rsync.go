package targets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/backup"
)

const (
	defaultRsyncPort     = 22
	defaultRsyncTimeout  = 30 * time.Second
	rsyncMaxRetries      = 3 // Renamed from defaultMaxRetries
	rsyncRetryBackoff    = time.Second
	rsyncTempFilePrefix  = ".tmp."
	rsyncMetadataFileExt = ".meta"
	rsyncMetadataVersion = 1
)

// RsyncMetadataV1 represents version 1 of the backup metadata format
type RsyncMetadataV1 struct {
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

// RsyncTargetConfig holds configuration for the rsync target
type RsyncTargetConfig struct {
	Host          string
	Port          int
	Username      string
	KeyFile       string
	KnownHostFile string // Path to SSH known_hosts file
	BasePath      string
	Timeout       time.Duration
	Debug         bool
	MaxRetries    int
	RetryBackoff  time.Duration
}

// RsyncTarget implements the backup.Target interface using the system rsync command
type RsyncTarget struct {
	config      RsyncTargetConfig
	rsyncPath   string
	sshPath     string
	mu          sync.Mutex // Protects operations
	tempFiles   map[string]bool
	tempFilesMu sync.Mutex // Protects tempFiles map
}

// ProgressReader wraps an io.Reader to track progress
type RsyncProgressReader struct {
	io.Reader
	Progress func(n int64)
}

func (r *RsyncProgressReader) Read(p []byte) (n int, err error) {
	n, err = r.Reader.Read(p)
	if n > 0 && r.Progress != nil {
		r.Progress(int64(n))
	}
	return
}

// NewRsyncTarget creates a new rsync target with the given configuration
func NewRsyncTarget(settings map[string]interface{}) (*RsyncTarget, error) {
	config := RsyncTargetConfig{}

	// Required settings
	host, ok := settings["host"].(string)
	if !ok {
		return nil, backup.NewError(backup.ErrConfig, "rsync: host is required", nil)
	}
	config.Host = host

	// Optional settings with defaults
	if port, ok := settings["port"].(int); ok {
		config.Port = port
	} else {
		config.Port = defaultRsyncPort
	}

	if username, ok := settings["username"].(string); ok {
		config.Username = username
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

	if path, ok := settings["path"].(string); ok {
		config.BasePath = strings.TrimRight(path, "/")
	} else {
		config.BasePath = "backups"
	}

	if timeout, ok := settings["timeout"].(string); ok {
		duration, err := time.ParseDuration(timeout)
		if err != nil {
			return nil, backup.NewError(backup.ErrValidation, "rsync: invalid timeout format", err)
		}
		config.Timeout = duration
	} else {
		config.Timeout = defaultRsyncTimeout
	}

	if debug, ok := settings["debug"].(bool); ok {
		config.Debug = debug
	}

	config.MaxRetries = rsyncMaxRetries
	config.RetryBackoff = rsyncRetryBackoff

	target := &RsyncTarget{
		config:    config,
		tempFiles: make(map[string]bool),
	}

	// Find rsync and ssh executables
	rsyncPath, err := exec.LookPath("rsync")
	if err != nil {
		return nil, backup.NewError(backup.ErrConfig, "rsync: command not found in PATH", err)
	}
	target.rsyncPath = rsyncPath

	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return nil, backup.NewError(backup.ErrConfig, "ssh: command not found in PATH", err)
	}
	target.sshPath = sshPath

	return target, nil
}

// isTransientError checks if an error is likely temporary
func (t *RsyncTarget) isTransientError(err error) bool {
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
		strings.Contains(errStr, "ssh: handshake failed") ||
		strings.Contains(errStr, "EOF")
}

// withRetry executes an operation with retry logic
func (t *RsyncTarget) withRetry(ctx context.Context, op func() error) error {
	var lastErr error
	for i := 0; i < t.config.MaxRetries; i++ {
		select {
		case <-ctx.Done():
			return backup.NewError(backup.ErrCanceled, "rsync: operation canceled", ctx.Err())
		default:
		}

		err := op()
		if err == nil {
			return nil
		}

		lastErr = err
		if !t.isTransientError(err) {
			return err
		}

		if t.config.Debug {
			fmt.Printf("Rsync: Retrying operation after error: %v (attempt %d/%d)\n", err, i+1, t.config.MaxRetries)
		}
		time.Sleep(t.config.RetryBackoff * time.Duration(i+1))
	}

	return backup.NewError(backup.ErrIO, "rsync: operation failed after retries", lastErr)
}

// RsyncError represents a specific rsync operation error
type RsyncError struct {
	Op      string
	Command string
	Output  string
	Err     error
}

func (e *RsyncError) Error() string {
	if e.Output != "" {
		return fmt.Sprintf("%s failed: %v (output: %s)", e.Op, e.Err, e.Output)
	}
	return fmt.Sprintf("%s failed: %v", e.Op, e.Err)
}

// sanitizePath performs security checks and sanitization on a path
func (t *RsyncTarget) sanitizePath(pathToCheck string) (string, error) {
	if pathToCheck == "" {
		return "", backup.NewError(backup.ErrValidation, "path cannot be empty", nil)
	}

	// Clean the path according to the current OS rules
	clean := filepath.Clean(pathToCheck)

	// On Windows, remove drive letter if present
	if len(clean) >= 2 && clean[1] == ':' {
		clean = clean[2:]
	}

	// Convert to forward slashes for rsync (which uses Unix-style paths)
	clean = filepath.ToSlash(clean)

	// Remove leading slashes to ensure path is relative
	clean = strings.TrimPrefix(clean, "/")

	// Check for directory traversal attempts
	if strings.Contains(clean, "..") {
		return "", backup.NewError(backup.ErrSecurity, "path contains directory traversal", nil)
	}

	// Check for suspicious path components
	components := strings.Split(clean, "/")
	for _, component := range components {
		// Skip empty components
		if component == "" {
			continue
		}

		// Check for hidden files/directories
		if strings.HasPrefix(component, ".") && component != ".write_test" {
			return "", backup.NewError(backup.ErrSecurity, "hidden files/directories are not allowed", nil)
		}

		// Check for suspicious characters that could be used for command injection
		if strings.ContainsAny(component, "<>:\"\\|?*$()[]{}!&;#`") {
			return "", backup.NewError(backup.ErrSecurity, "path contains invalid characters", nil)
		}

		// Check component length
		if len(component) > 255 {
			return "", backup.NewError(backup.ErrSecurity, "path component exceeds maximum length", nil)
		}
	}

	// Validate total path length
	if len(clean) > 4096 {
		return "", backup.NewError(backup.ErrSecurity, "path exceeds maximum length", nil)
	}

	return clean, nil
}

// buildSSHCmd returns a secure SSH command string
func (t *RsyncTarget) buildSSHCmd() string {
	sshCmd := fmt.Sprintf("ssh -p %d", t.config.Port)

	// Add strict security options
	sshCmd += " -o StrictHostKeyChecking=yes"
	if t.config.KnownHostFile != "" {
		sshCmd += fmt.Sprintf(" -o UserKnownHostsFile=%s", t.config.KnownHostFile)
	}
	sshCmd += " -o ControlMaster=no"
	sshCmd += " -o IdentitiesOnly=yes"

	if t.config.KeyFile != "" {
		sshCmd += fmt.Sprintf(" -i %s", t.config.KeyFile)
	}

	return sshCmd
}

// executeCommand executes a command with proper error handling
func (t *RsyncTarget) executeCommand(ctx context.Context, cmd *exec.Cmd) error {
	// Set up command with context
	cmd = exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return &RsyncError{
				Op:      "execute",
				Command: cmd.String(),
				Output:  string(output),
				Err:     ctx.Err(),
			}
		}
		return &RsyncError{
			Op:      "execute",
			Command: cmd.String(),
			Output:  string(output),
			Err:     err,
		}
	}
	return nil
}

// Store implements the backup.Target interface with enhanced security
func (t *RsyncTarget) Store(ctx context.Context, sourcePath string, metadata *backup.Metadata) error {
	if t.config.Debug {
		fmt.Printf("Rsync: Storing backup %s to %s\n", filepath.Base(sourcePath), t.config.Host)
	}

	// Create versioned metadata
	rsyncMetadata := RsyncMetadataV1{
		Version:    rsyncMetadataVersion,
		Timestamp:  metadata.Timestamp,
		Size:       metadata.Size,
		Type:       metadata.Type,
		Source:     metadata.Source,
		IsDaily:    metadata.IsDaily,
		ConfigHash: metadata.ConfigHash,
		AppVersion: metadata.AppVersion,
	}

	// Marshal metadata
	metadataBytes, err := json.Marshal(rsyncMetadata)
	if err != nil {
		return backup.NewError(backup.ErrIO, "rsync: failed to marshal metadata", err)
	}

	// Create temporary metadata file
	tempMetadataFile, err := os.CreateTemp("", "rsync-metadata-*")
	if err != nil {
		return backup.NewError(backup.ErrIO, "rsync: failed to create temporary metadata file", err)
	}
	defer os.Remove(tempMetadataFile.Name())
	defer tempMetadataFile.Close()

	if _, err := tempMetadataFile.Write(metadataBytes); err != nil {
		return backup.NewError(backup.ErrIO, "rsync: failed to write metadata", err)
	}

	// Upload the backup file with enhanced security
	if err := t.atomicUpload(ctx, sourcePath); err != nil {
		return err
	}

	// Upload the metadata file
	if err := t.atomicUpload(ctx, tempMetadataFile.Name()); err != nil {
		return backup.NewError(backup.ErrIO, "rsync: failed to store metadata", err)
	}

	if t.config.Debug {
		fmt.Printf("Rsync: Successfully stored backup %s with metadata\n", filepath.Base(sourcePath))
	}

	return nil
}

// atomicUpload performs an atomic upload using rsync with enhanced security
func (t *RsyncTarget) atomicUpload(ctx context.Context, sourcePath string) error {
	tempName := fmt.Sprintf("%s%s", rsyncTempFilePrefix, filepath.Base(sourcePath))
	cleanTempName, err := t.sanitizePath(tempName)
	if err != nil {
		return err
	}

	// Sanitize base path
	cleanBasePath, err := t.sanitizePath(t.config.BasePath)
	if err != nil {
		return err
	}

	t.trackTempFile(cleanTempName)
	defer t.untrackTempFile(cleanTempName)

	return t.withRetry(ctx, func() error {
		// Build rsync command with enhanced security options
		args := []string{
			"-av",                                  // Archive mode and verbose
			"--protect-args",                       // Protect special characters
			"--chmod=Du=rwx,Dg=,Do=,Fu=rw,Fg=,Fo=", // Strict permissions
			"--timeout=300",                        // Connection timeout
			"--delete-during",                      // Clean deletions
			"--checksum",                           // Verify checksums
			"--no-implied-dirs",                    // Prevent directory creation outside target
			"--no-relative",                        // Disable relative path mode
			"-e", t.buildSSHCmd(),                  // SSH command with custom port and security options
		}

		if t.config.Debug {
			args = append(args, "--progress")
		}

		// Add source and temporary destination
		// Use --rsh option to handle the SSH connection securely
		tempDest := fmt.Sprintf("%s@%s:%s/%s",
			t.config.Username,
			t.config.Host,
			cleanBasePath,
			cleanTempName)
		args = append(args, sourcePath, tempDest)

		// Execute rsync command
		cmd := exec.CommandContext(ctx, t.rsyncPath, args...)
		if err := t.executeCommand(ctx, cmd); err != nil {
			return backup.NewError(backup.ErrIO, "rsync: upload failed", err)
		}

		// Rename to final destination
		finalName := filepath.Base(sourcePath)
		cleanFinalName, err := t.sanitizePath(finalName)
		if err != nil {
			return err
		}

		if err := t.renameFile(ctx, cleanTempName, cleanFinalName); err != nil {
			// Try to clean up the temporary file
			_ = t.deleteFile(ctx, cleanTempName)
			return err
		}

		return nil
	})
}

// renameFile renames a file on the remote server with enhanced security
func (t *RsyncTarget) renameFile(ctx context.Context, oldName, newName string) error {
	// Both names should already be sanitized, but verify again
	cleanOldName, err := t.sanitizePath(oldName)
	if err != nil {
		return err
	}

	cleanNewName, err := t.sanitizePath(newName)
	if err != nil {
		return err
	}

	cleanBasePath, err := t.sanitizePath(t.config.BasePath)
	if err != nil {
		return err
	}

	sshArgs := []string{
		"-p", fmt.Sprintf("%d", t.config.Port),
	}
	if t.config.KeyFile != "" {
		sshArgs = append(sshArgs, "-i", t.config.KeyFile)
	}

	// Use mv with -- to prevent option injection
	sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", t.config.Username, t.config.Host),
		fmt.Sprintf("mv -- %s/%s %s/%s",
			cleanBasePath, cleanOldName,
			cleanBasePath, cleanNewName))

	cmd := exec.CommandContext(ctx, t.sshPath, sshArgs...)
	return t.executeCommand(ctx, cmd)
}

// deleteFile deletes a file on the remote server with enhanced security
func (t *RsyncTarget) deleteFile(ctx context.Context, filename string) error {
	cleanFilename, err := t.sanitizePath(filename)
	if err != nil {
		return err
	}

	cleanBasePath, err := t.sanitizePath(t.config.BasePath)
	if err != nil {
		return err
	}

	sshArgs := []string{
		"-p", fmt.Sprintf("%d", t.config.Port),
	}
	if t.config.KeyFile != "" {
		sshArgs = append(sshArgs, "-i", t.config.KeyFile)
	}

	// Use rm with -- to prevent option injection
	sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", t.config.Username, t.config.Host),
		fmt.Sprintf("rm -- %s/%s", cleanBasePath, cleanFilename))

	cmd := exec.CommandContext(ctx, t.sshPath, sshArgs...)
	return t.executeCommand(ctx, cmd)
}

// trackTempFile adds a temporary file to the tracking map
func (t *RsyncTarget) trackTempFile(path string) {
	t.tempFilesMu.Lock()
	defer t.tempFilesMu.Unlock()
	t.tempFiles[path] = true
}

// untrackTempFile removes a temporary file from the tracking map
func (t *RsyncTarget) untrackTempFile(path string) {
	t.tempFilesMu.Lock()
	defer t.tempFilesMu.Unlock()
	delete(t.tempFiles, path)
}

// cleanupTempFiles attempts to clean up any tracked temporary files
func (t *RsyncTarget) cleanupTempFiles(ctx context.Context) error {
	t.tempFilesMu.Lock()
	defer t.tempFilesMu.Unlock() // Lock for the entire operation

	var errs []error
	for path := range t.tempFiles {
		if err := t.deleteFile(ctx, path); err != nil {
			if t.config.Debug {
				fmt.Printf("Warning: failed to clean up temporary file %s: %v\n", path, err)
			}
			errs = append(errs, fmt.Errorf("failed to delete %s: %w", path, err))
		} else {
			delete(t.tempFiles, path) // Remove from map under the same lock
		}
	}

	if len(errs) > 0 {
		var errMsg strings.Builder
		for i, err := range errs {
			if i > 0 {
				errMsg.WriteString("; ")
			}
			errMsg.WriteString(err.Error())
		}
		return backup.NewError(backup.ErrIO, fmt.Sprintf("rsync: cleanup failed: %s", errMsg.String()), nil)
	}

	return nil
}

// Close implements proper resource cleanup with enhanced error handling
func (t *RsyncTarget) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	var errs []error

	// Set a timeout for cleanup operations
	ctx, cancel := context.WithTimeout(context.Background(), t.config.Timeout)
	defer cancel()

	// Cleanup temporary files
	if err := t.cleanupTempFiles(ctx); err != nil {
		errs = append(errs, err)
	}

	// Combine all errors
	if len(errs) > 0 {
		var errMsg strings.Builder
		for i, err := range errs {
			if i > 0 {
				errMsg.WriteString("; ")
			}
			errMsg.WriteString(err.Error())
		}
		return backup.NewError(backup.ErrIO, fmt.Sprintf("rsync: cleanup failed: %s", errMsg.String()), nil)
	}

	return nil
}

// Name returns the name of this target
func (t *RsyncTarget) Name() string {
	return "rsync"
}

// List implements the backup.Target interface
func (t *RsyncTarget) List(ctx context.Context) ([]backup.BackupInfo, error) {
	if t.config.Debug {
		fmt.Printf("Rsync: Listing backups from %s\n", t.config.Host)
	}

	// Build SSH command to list files
	sshArgs := []string{
		"-p", fmt.Sprintf("%d", t.config.Port),
	}
	if t.config.KeyFile != "" {
		sshArgs = append(sshArgs, "-i", t.config.KeyFile)
	}
	sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", t.config.Username, t.config.Host),
		fmt.Sprintf("ls -l --time-style=full-iso %s", t.config.BasePath))

	cmd := exec.CommandContext(ctx, t.sshPath, sshArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, backup.NewError(backup.ErrIO, "rsync: failed to list backups", fmt.Errorf("%w: %s", err, output))
	}

	var backups []backup.BackupInfo
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "total ") {
			continue
		}

		// Parse ls output
		parts := strings.Fields(line)
		if len(parts) < 8 {
			continue
		}

		size, err := parseInt64(parts[4])
		if err != nil {
			return nil, backup.NewError(backup.ErrValidation, "rsync: failed to parse file size", err)
		}
		timestamp, err := time.Parse("2006-01-02 15:04:05.000000000 -0700", parts[5]+" "+parts[6]+" "+parts[7])
		if err != nil {
			return nil, backup.NewError(backup.ErrValidation, "rsync: failed to parse timestamp", err)
		}
		name := strings.Join(parts[8:], " ")

		backups = append(backups, backup.BackupInfo{
			Target: name,
			Metadata: backup.Metadata{
				Timestamp: timestamp,
				Size:      size,
			},
		})
	}

	return backups, nil
}

// Delete implements the backup.Target interface with enhanced security
func (t *RsyncTarget) Delete(ctx context.Context, target string) error {
	if t.config.Debug {
		fmt.Printf("Rsync: Deleting backup %s from %s\n", target, t.config.Host)
	}

	// Sanitize the target path
	cleanTarget, err := t.sanitizePath(target)
	if err != nil {
		return err
	}

	targetPath := path.Join(t.config.BasePath, cleanTarget)
	cleanPath, err := t.sanitizePath(targetPath)
	if err != nil {
		return err
	}

	// Build SSH command with proper escaping
	sshArgs := []string{
		"-p", fmt.Sprintf("%d", t.config.Port),
	}
	if t.config.KeyFile != "" {
		sshArgs = append(sshArgs, "-i", t.config.KeyFile)
	}

	// Use rm with properly escaped paths
	sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", t.config.Username, t.config.Host),
		fmt.Sprintf("rm -- '%s'", cleanPath))

	cmd := exec.CommandContext(ctx, t.sshPath, sshArgs...)
	if err := t.executeCommand(ctx, cmd); err != nil {
		return backup.NewError(backup.ErrIO, "rsync: failed to delete backup", err)
	}

	if t.config.Debug {
		fmt.Printf("Rsync: Successfully deleted backup %s\n", target)
	}

	return nil
}

// Validate checks if the target configuration is valid
func (t *RsyncTarget) Validate() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Test SSH connection
	sshArgs := []string{
		"-p", fmt.Sprintf("%d", t.config.Port),
	}
	if t.config.KeyFile != "" {
		sshArgs = append(sshArgs, "-i", t.config.KeyFile)
	}
	sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", t.config.Username, t.config.Host), "echo test")

	cmd := exec.CommandContext(ctx, t.sshPath, sshArgs...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return backup.NewError(backup.ErrValidation, "rsync: SSH connection test failed", fmt.Errorf("%w: %s", err, output))
	}

	// Test rsync access
	args := []string{
		"--dry-run",
		"-e", t.buildSSHCmd(),
		"/dev/null",
		fmt.Sprintf("%s@%s:%s/.test", t.config.Username, t.config.Host, t.config.BasePath),
	}

	cmd = exec.CommandContext(ctx, t.rsyncPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return backup.NewError(backup.ErrValidation, "rsync: access test failed", fmt.Errorf("%w: %s", err, output))
	}

	return nil
}

// Helper functions

func parseInt64(s string) (int64, error) {
	var result int64
	_, err := fmt.Sscanf(s, "%d", &result)
	return result, err
}
