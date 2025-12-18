// Package telemetry provides system ID generation and management
package telemetry

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tphakala/birdnet-go/internal/privacy"
)

// GenerateSystemID creates a unique system identifier
// Delegates to the privacy package for consistent implementation
func GenerateSystemID() (string, error) {
	return privacy.GenerateSystemID()
}

// File permission constants
const (
	// dirPermSecure is the permission for secure directories (owner rwx only)
	dirPermSecure = 0o700
	// filePermSecure is the permission for secure files (owner rw only)
	filePermSecure = 0o600
)

// LoadOrCreateSystemID loads an existing system ID from file or creates a new one
func LoadOrCreateSystemID(configDir string) (string, error) {
	// Clean the path for defense-in-depth, even though configDir is trusted.
	// This normalizes the path and removes any traversal elements.
	configDir = filepath.Clean(configDir)

	// Ensure config directory exists with secure permissions
	if err := os.MkdirAll(configDir, dirPermSecure); err != nil {
		return "", fmt.Errorf("failed to create config directory: %w", err)
	}

	// Path to system ID file - constructed from cleaned configDir and constant filename
	idFile := filepath.Join(configDir, ".system_id")

	// Try to read existing ID
	// G304: idFile is constructed from configDir parameter (trusted) and constant filename
	if data, err := os.ReadFile(idFile); err == nil { //nolint:gosec // path constructed from trusted input
		id := strings.TrimSpace(string(data))
		if id != "" && privacy.IsValidSystemID(id) {
			return id, nil
		}
	}

	// Generate new ID
	id, err := GenerateSystemID()
	if err != nil {
		return "", err
	}

	// Save to file with secure permissions
	if err := os.WriteFile(idFile, []byte(id), filePermSecure); err != nil {
		return "", fmt.Errorf("failed to save system ID: %w", err)
	}

	return id, nil
}

