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

// LoadOrCreateSystemID loads an existing system ID from file or creates a new one
func LoadOrCreateSystemID(configDir string) (string, error) {
	// Ensure config directory exists
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create config directory: %w", err)
	}

	// Path to system ID file
	idFile := filepath.Join(configDir, ".system_id")

	// Try to read existing ID
	if data, err := os.ReadFile(idFile); err == nil {
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

	// Save to file
	if err := os.WriteFile(idFile, []byte(id), 0o600); err != nil {
		return "", fmt.Errorf("failed to save system ID: %w", err)
	}

	return id, nil
}

