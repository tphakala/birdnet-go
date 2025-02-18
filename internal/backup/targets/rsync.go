package targets

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/backup"
)

// RsyncTarget implements the backup.Target interface using the system rsync command
type RsyncTarget struct {
	host      string
	port      int
	username  string
	keyFile   string
	basePath  string
	rsyncPath string
	sshPath   string
	debug     bool
}

// NewRsyncTarget creates a new rsync target with the given configuration
func NewRsyncTarget(settings map[string]interface{}) (*RsyncTarget, error) {
	target := &RsyncTarget{}

	// Required settings
	host, ok := settings["host"].(string)
	if !ok {
		return nil, fmt.Errorf("rsync: host is required")
	}
	target.host = host

	// Optional settings with defaults
	if port, ok := settings["port"].(int); ok {
		target.port = port
	} else {
		target.port = 22 // Default SSH port
	}

	if username, ok := settings["username"].(string); ok {
		target.username = username
	}

	if keyFile, ok := settings["key_file"].(string); ok {
		target.keyFile = keyFile
	}

	if path, ok := settings["path"].(string); ok {
		target.basePath = strings.TrimRight(path, "/")
	} else {
		target.basePath = "backups"
	}

	if debug, ok := settings["debug"].(bool); ok {
		target.debug = debug
	}

	// Find rsync and ssh executables
	rsyncPath, err := exec.LookPath("rsync")
	if err != nil {
		return nil, fmt.Errorf("rsync: command not found in PATH")
	}
	target.rsyncPath = rsyncPath

	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return nil, fmt.Errorf("ssh: command not found in PATH")
	}
	target.sshPath = sshPath

	return target, nil
}

// Name returns the name of this target
func (t *RsyncTarget) Name() string {
	return "rsync"
}

// Store implements the backup.Target interface
func (t *RsyncTarget) Store(ctx context.Context, info *backup.BackupInfo, reader io.Reader) error {
	if t.debug {
		fmt.Printf("Rsync: Storing backup %s to %s\n", info.Target, t.host)
	}

	// Create a temporary file to store the backup
	tempDir, err := os.MkdirTemp("", "rsync-backup-*")
	if err != nil {
		return fmt.Errorf("rsync: failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	tempFile := filepath.Join(tempDir, info.Target)
	f, err := os.Create(tempFile)
	if err != nil {
		return fmt.Errorf("rsync: failed to create temp file: %w", err)
	}

	if _, err := io.Copy(f, reader); err != nil {
		f.Close()
		return fmt.Errorf("rsync: failed to write temp file: %w", err)
	}
	f.Close()

	// Build rsync command
	args := []string{
		"-av",                 // Archive mode and verbose
		"--protect-args",      // Protect special characters
		"-e", t.buildSSHCmd(), // SSH command with custom port
	}

	if t.debug {
		args = append(args, "--progress")
	}

	// Add source and destination
	dest := fmt.Sprintf("%s@%s:%s", t.username, t.host, t.basePath)
	args = append(args, tempFile, dest)

	// Execute rsync command
	cmd := exec.CommandContext(ctx, t.rsyncPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rsync: command failed: %w\nOutput: %s", err, output)
	}

	if t.debug {
		fmt.Printf("Rsync: Successfully stored backup %s\n", info.Target)
	}

	return nil
}

// List implements the backup.Target interface
func (t *RsyncTarget) List(ctx context.Context) ([]backup.BackupInfo, error) {
	if t.debug {
		fmt.Printf("Rsync: Listing backups from %s\n", t.host)
	}

	// Build SSH command to list files
	sshArgs := []string{
		"-p", fmt.Sprintf("%d", t.port),
	}
	if t.keyFile != "" {
		sshArgs = append(sshArgs, "-i", t.keyFile)
	}
	sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", t.username, t.host),
		fmt.Sprintf("ls -l --time-style=full-iso %s", t.basePath))

	cmd := exec.CommandContext(ctx, t.sshPath, sshArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("rsync: failed to list backups: %w\nOutput: %s", err, output)
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

		size, _ := parseInt64(parts[4])
		timestamp, _ := time.Parse("2006-01-02 15:04:05.000000000 -0700", parts[5]+" "+parts[6]+" "+parts[7])
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

// Delete implements the backup.Target interface
func (t *RsyncTarget) Delete(ctx context.Context, target string) error {
	if t.debug {
		fmt.Printf("Rsync: Deleting backup %s from %s\n", target, t.host)
	}

	// Build SSH command to delete file
	sshArgs := []string{
		"-p", fmt.Sprintf("%d", t.port),
	}
	if t.keyFile != "" {
		sshArgs = append(sshArgs, "-i", t.keyFile)
	}
	sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", t.username, t.host),
		fmt.Sprintf("rm %s/%s", t.basePath, target))

	cmd := exec.CommandContext(ctx, t.sshPath, sshArgs...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rsync: failed to delete backup: %w\nOutput: %s", err, output)
	}

	if t.debug {
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
		"-p", fmt.Sprintf("%d", t.port),
	}
	if t.keyFile != "" {
		sshArgs = append(sshArgs, "-i", t.keyFile)
	}
	sshArgs = append(sshArgs, fmt.Sprintf("%s@%s", t.username, t.host), "echo test")

	cmd := exec.CommandContext(ctx, t.sshPath, sshArgs...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rsync: SSH connection test failed: %w\nOutput: %s", err, output)
	}

	// Test rsync access
	args := []string{
		"--dry-run",
		"-e", t.buildSSHCmd(),
		"/dev/null",
		fmt.Sprintf("%s@%s:%s/.test", t.username, t.host, t.basePath),
	}

	cmd = exec.CommandContext(ctx, t.rsyncPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rsync: access test failed: %w\nOutput: %s", err, output)
	}

	return nil
}

// Helper functions

func (t *RsyncTarget) buildSSHCmd() string {
	sshCmd := fmt.Sprintf("ssh -p %d", t.port)
	if t.keyFile != "" {
		sshCmd += fmt.Sprintf(" -i %s", t.keyFile)
	}
	return sshCmd
}

func parseInt64(s string) (int64, error) {
	var result int64
	_, err := fmt.Sscanf(s, "%d", &result)
	return result, err
}
