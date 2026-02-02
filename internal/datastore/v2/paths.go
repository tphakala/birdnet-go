// Package v2 provides the v2 normalized database implementation.
package v2

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PathDeriver handles v2 database path derivation and validation.
type PathDeriver struct {
	configuredPath string
}

// NewPathDeriver creates a new path deriver for the given configured path.
func NewPathDeriver(configuredPath string) *PathDeriver {
	return &PathDeriver{
		configuredPath: configuredPath,
	}
}

// ConfiguredPath returns the original configured database path.
func (p *PathDeriver) ConfiguredPath() string {
	return p.configuredPath
}

// V2MigrationPath returns the derived v2 migration path.
// Inserts "_v2" before the file extension.
// Examples:
//   - /data/birdnet.db → /data/birdnet_v2.db
//   - /data/mybirds.sqlite → /data/mybirds_v2.sqlite
//   - /data/database → /data/database_v2
func (p *PathDeriver) V2MigrationPath() string {
	return deriveV2Path(p.configuredPath)
}

// DataDir returns the directory containing the database.
func (p *PathDeriver) DataDir() string {
	return filepath.Dir(p.configuredPath)
}

// ValidateV2PathAvailable checks if the v2 migration path is available for use.
// Returns nil if the path doesn't exist or exists and is a valid v2 migration database.
// Returns error if the path exists but is not a v2 migration database (collision).
func (p *PathDeriver) ValidateV2PathAvailable() error {
	v2Path := p.V2MigrationPath()

	// Check if file exists
	if _, err := os.Stat(v2Path); os.IsNotExist(err) {
		// Path doesn't exist, available for use
		return nil
	}

	// Path exists - check if it's a v2 migration database
	if CheckSQLiteHasV2Schema(v2Path) {
		// It's our migration database, OK to use
		return nil
	}

	// Path exists but is not a v2 migration database - collision
	return fmt.Errorf("path %s already exists and is not a v2 migration database; please remove or rename it", v2Path)
}

// deriveV2Path derives the v2 migration path from the configured path.
// Inserts "_v2" before the file extension.
func deriveV2Path(configuredPath string) string {
	dir := filepath.Dir(configuredPath)
	base := filepath.Base(configuredPath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)

	// Insert _v2 before extension
	// birdnet.db → birdnet_v2.db
	// mybirds.sqlite → mybirds_v2.sqlite
	// database → database_v2
	return filepath.Join(dir, name+"_v2"+ext)
}

// V2MigrationPathFromConfigured is a convenience function that derives
// the v2 migration path from a configured path without creating a PathDeriver.
func V2MigrationPathFromConfigured(configuredPath string) string {
	return deriveV2Path(configuredPath)
}
