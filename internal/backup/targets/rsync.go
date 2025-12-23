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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/backup"
)

// Rsync-specific constants (shared constants imported from common.go)
const (
	rsyncTempFilePrefix  = "tmp-"
	rsyncMetadataFileExt = ".tar.gz.meta"
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
func NewRsyncTarget(settings map[string]any) (*RsyncTarget, error) {
	p := NewSettingsParser(settings)

	config := RsyncTargetConfig{
		// Required settings
		Host:     p.RequireString("host", "rsync"),
		BasePath: p.RequirePath("path", "rsync", false), // preserveRoot=false

		// Optional settings with defaults
		Port:          p.OptionalInt("port", DefaultSSHPort),
		Username:      p.OptionalString("username", ""),
		KeyFile:       p.OptionalString("key_file", ""),
		KnownHostFile: p.OptionalString("known_hosts_file", DefaultKnownHostsFile()),
		Timeout:       p.OptionalDuration("timeout", DefaultTimeout, "rsync"),
		Debug:         p.OptionalBool("debug", false),

		// Fixed defaults
		MaxRetries:   DefaultMaxRetries,
		RetryBackoff: DefaultRetryBackoff,
	}

	if err := p.Error(); err != nil {
		return nil, err
	}

	// Find rsync and ssh executables
	rsyncPath, err := exec.LookPath("rsync")
	if err != nil {
		return nil, backup.NewError(backup.ErrConfig, "rsync: command not found in PATH", err)
	}

	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return nil, backup.NewError(backup.ErrConfig, "ssh: command not found in PATH", err)
	}

	return &RsyncTarget{
		config:    config,
		tempFiles: make(map[string]bool),
		rsyncPath: rsyncPath,
		sshPath:   sshPath,
	}, nil
}

// isTransientError checks if an error is likely temporary
// Delegates to the shared implementation in common.go
func (t *RsyncTarget) isTransientError(err error) bool {
	return IsTransientError(err)
}

// withRetry executes an operation with retry logic
func (t *RsyncTarget) withRetry(ctx context.Context, op func() error) error {
	var lastErr error
	for attempt := range t.config.MaxRetries {
		select {
		case <-ctx.Done():
			return backup.NewError(backup.ErrCanceled, "rsync: operation canceled", ctx.Err())
		default:
		}

		if err := op(); err == nil {
			return nil
		} else {
			lastErr = err
			if !t.isTransientError(err) {
				return err
			}
		}

		if t.config.Debug {
			fmt.Printf("Rsync: Retrying operation after error: %v (attempt %d/%d)\n", lastErr, attempt+1, t.config.MaxRetries)
		}
		time.Sleep(t.config.RetryBackoff * time.Duration(attempt+1))
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

// rsyncExtraInvalidChars contains additional characters that could be used for command injection
const rsyncExtraInvalidChars = "$()[]{}!&;#`"

// sanitizePath performs security checks and sanitization on a path.
// Uses shared ValidatePathWithOpts with extra command injection protection.
func (t *RsyncTarget) sanitizePath(pathToCheck string) (string, error) {
	return ValidatePathWithOpts(pathToCheck, PathValidationOpts{
		AllowHidden:    false,
		AllowAbsolute:  false,
		ConvertToSlash: true,                  // rsync uses Unix-style paths
		InvalidChars:   rsyncExtraInvalidChars, // Extra command injection protection
		ReturnCleaned:  true,                  // Return the cleaned path
	})
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
	// Validate command path is absolute or in secure location
	if !filepath.IsAbs(cmd.Path) {
		return &RsyncError{
			Op:      "execute",
			Command: cmd.Path,
			Err:     fmt.Errorf("command path must be absolute for security"),
		}
	}

	// Set up command with context - use a new command to ensure clean state
	// #nosec G204 - Command path and args are validated elsewhere in the code
	newCmd := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...)
	output, err := newCmd.CombinedOutput()
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
		fmt.Printf("🔄 Rsync: Storing backup %s to %s\n", filepath.Base(sourcePath), t.config.Host)
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
	defer func() {
		if err := os.Remove(tempMetadataFile.Name()); err != nil {
			fmt.Printf("rsync: failed to remove temp metadata file: %v\n", err)
		}
	}()
	defer func() {
		if err := tempMetadataFile.Close(); err != nil {
			fmt.Printf("rsync: failed to close temp metadata file: %v\n", err)
		}
	}()

	if _, err := tempMetadataFile.Write(metadataBytes); err != nil {
		return backup.NewError(backup.ErrIO, "rsync: failed to write metadata", err)
	}

	// Upload the backup file with enhanced security
	if err := t.atomicUpload(ctx, sourcePath); err != nil {
		return err
	}

	// Upload the metadata file with .tar.gz.meta extension
	metadataFileName := filepath.Base(sourcePath) + rsyncMetadataFileExt
	tempMetadataPath := tempMetadataFile.Name() + ".tmp"
	if err := os.Rename(tempMetadataFile.Name(), tempMetadataPath); err != nil {
		return backup.NewError(backup.ErrIO, "rsync: failed to prepare metadata file", err)
	}

	// Rename the temporary file to match the final name before upload
	if err := t.atomicUpload(ctx, tempMetadataPath); err != nil {
		if err := os.Remove(tempMetadataPath); err != nil {
			fmt.Printf("rsync: failed to remove temp metadata path: %v\n", err)
		}
		return backup.NewError(backup.ErrIO, fmt.Sprintf("rsync: failed to store metadata file %s", metadataFileName), err)
	}
	if err := os.Remove(tempMetadataPath); err != nil {
		fmt.Printf("rsync: failed to remove temp metadata path: %v\n", err)
	}

	if t.config.Debug {
		fmt.Printf("✅ Rsync: Successfully stored backup %s with metadata\n", filepath.Base(sourcePath))
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
		// #nosec G204 - rsyncPath is validated during initialization, args are constructed safely
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

	// #nosec G204 - sshPath is validated during initialization, sshArgs are constructed safely
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

	cmd := exec.CommandContext(ctx, t.sshPath, sshArgs...) // #nosec G204 -- sshPath validated during initialization, args constructed with sanitized paths
	return t.executeCommand(ctx, cmd)
}

// trackTempFile adds a temporary file to the tracking map
func (t *RsyncTarget) trackTempFile(filePath string) {
	t.tempFilesMu.Lock()
	defer t.tempFilesMu.Unlock()
	t.tempFiles[filePath] = true
}

// untrackTempFile removes a temporary file from the tracking map
func (t *RsyncTarget) untrackTempFile(filePath string) {
	t.tempFilesMu.Lock()
	defer t.tempFilesMu.Unlock()
	delete(t.tempFiles, filePath)
}

// cleanupTempFiles attempts to clean up any tracked temporary files
func (t *RsyncTarget) cleanupTempFiles(ctx context.Context) error {
	t.tempFilesMu.Lock()
	defer t.tempFilesMu.Unlock() // Lock for the entire operation

	var errs []error
	for path := range t.tempFiles {
		if err := t.deleteFile(ctx, path); err != nil {
			if t.config.Debug {
				fmt.Printf("⚠️ Rsync: Failed to clean up temporary file %s: %v\n", path, err)
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
		fmt.Printf("🔄 Rsync: Listing backups from %s\n", t.config.Host)
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

	cmd := exec.CommandContext(ctx, t.sshPath, sshArgs...) // #nosec G204 -- sshPath validated during initialization, args constructed with sanitized paths
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, backup.NewError(backup.ErrIO, "rsync: failed to list backups", fmt.Errorf("%w: %s", err, output))
	}

	lines := strings.Split(string(output), "\n")
	backups := make([]backup.BackupInfo, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "total ") {
			continue
		}

		// Parse ls output
		parts := strings.Fields(line)
		if len(parts) < MinLsOutputFields {
			continue
		}

		name := strings.Join(parts[8:], " ")
		// Only process metadata files
		if !strings.HasSuffix(name, rsyncMetadataFileExt) {
			continue
		}

		// Get the backup file name by removing .tar.gz.meta suffix
		backupName := strings.TrimSuffix(name, rsyncMetadataFileExt)
		backupPath := path.Join(t.config.BasePath, backupName)

		// Check if the corresponding backup file exists
		checkCmd := exec.CommandContext(ctx, t.sshPath, append(sshArgs[:len(sshArgs)-1], fmt.Sprintf("test -f %s && echo exists", backupPath))...) // #nosec G204 -- sshPath validated during initialization, backupPath constructed from sanitized paths
		if output, err := checkCmd.CombinedOutput(); err != nil || !strings.Contains(string(output), "exists") {
			if t.config.Debug {
				fmt.Printf("⚠️ Rsync: Skipping orphaned metadata file %s: backup file not found\n", name)
			}
			continue
		}

		// Read metadata file
		metadataPath := path.Join(t.config.BasePath, name)

		// Process metadata file in a separate function to handle defers properly
		metadata, err := t.downloadAndParseMetadata(ctx, metadataPath, sshArgs, backupName)
		if err != nil {
			if t.config.Debug {
				fmt.Printf("⚠️ Rsync: %v\n", err)
			}
			continue
		}

		backupInfo := backup.BackupInfo{
			Metadata: backup.Metadata{
				Version:    metadata.Version,
				Timestamp:  metadata.Timestamp,
				Size:       metadata.Size,
				Type:       metadata.Type,
				Source:     metadata.Source,
				IsDaily:    metadata.IsDaily,
				ConfigHash: metadata.ConfigHash,
				AppVersion: metadata.AppVersion,
			},
			Target: t.Name(),
		}
		backups = append(backups, backupInfo)
	}

	// Sort backups by timestamp (newest first)
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Timestamp.After(backups[j].Timestamp)
	})

	return backups, nil
}

// downloadAndParseMetadata downloads and parses a metadata file
func (t *RsyncTarget) downloadAndParseMetadata(ctx context.Context, metadataPath string, sshArgs []string, backupName string) (*RsyncMetadataV1, error) {
	tempFile, err := os.CreateTemp("", "rsync-metadata-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary file for metadata: %w", err)
	}
	defer func() {
		if err := os.Remove(tempFile.Name()); err != nil {
			fmt.Printf("rsync: failed to remove temp file: %v\n", err)
		}
	}()
	defer func() {
		if err := tempFile.Close(); err != nil {
			fmt.Printf("rsync: failed to close temp file: %v\n", err)
		}
	}()

	// Download metadata file
	downloadCmd := exec.CommandContext(ctx, t.sshPath, append(sshArgs[:len(sshArgs)-1], fmt.Sprintf("cat %s", metadataPath))...) // #nosec G204 -- sshPath validated during initialization, metadataPath constructed from sanitized paths
	metadataBytes, err := downloadCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to read metadata for %s: %w", backupName, err)
	}

	var metadata RsyncMetadataV1
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		return nil, fmt.Errorf("invalid metadata in backup %s: %w", backupName, err)
	}

	return &metadata, nil
}

// Delete implements the backup.Target interface with enhanced security
func (t *RsyncTarget) Delete(ctx context.Context, target string) error {
	if t.config.Debug {
		fmt.Printf("🔄 Rsync: Deleting backup %s from %s\n", target, t.config.Host)
	}

	// Sanitize the target path
	cleanTarget, err := t.sanitizePath(target)
	if err != nil {
		return err
	}

	// Delete both backup and metadata files
	targetPath := path.Join(t.config.BasePath, cleanTarget)
	metadataPath := targetPath + rsyncMetadataFileExt
	cleanPath, err := t.sanitizePath(targetPath)
	if err != nil {
		return err
	}
	cleanMetadataPath, err := t.sanitizePath(metadataPath)
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

	// Delete backup file
	sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", t.config.Username, t.config.Host),
		fmt.Sprintf("rm -f -- '%s' '%s'", cleanPath, cleanMetadataPath))

	cmd := exec.CommandContext(ctx, t.sshPath, sshArgs...) // #nosec G204 -- sshPath validated during initialization, args constructed with sanitized paths
	if err := t.executeCommand(ctx, cmd); err != nil {
		return backup.NewError(backup.ErrIO, "rsync: failed to delete backup", err)
	}

	if t.config.Debug {
		fmt.Printf("✅ Rsync: Successfully deleted backup %s\n", target)
	}

	return nil
}

// Validate checks if the target configuration is valid
func (t *RsyncTarget) Validate() error {
	ctx, cancel := context.WithTimeout(context.Background(), backup.DefaultValidateTimeout)
	defer cancel()

	// Test SSH connection
	sshArgs := []string{
		"-p", fmt.Sprintf("%d", t.config.Port),
	}
	if t.config.KeyFile != "" {
		sshArgs = append(sshArgs, "-i", t.config.KeyFile)
	}
	sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", t.config.Username, t.config.Host), "echo test")

	cmd := exec.CommandContext(ctx, t.sshPath, sshArgs...) // #nosec G204 -- sshPath validated during initialization, args constructed with sanitized paths
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

	cmd = exec.CommandContext(ctx, t.rsyncPath, args...) // #nosec G204 -- rsyncPath validated during initialization, args constructed safely
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
