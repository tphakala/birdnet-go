package api

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ViteManifest represents the structure of Vite's manifest.json.
// The manifest maps source file paths to their build outputs.
type ViteManifest map[string]ViteManifestEntry

// ViteManifestEntry represents a single entry in the Vite manifest.
type ViteManifestEntry struct {
	File    string   `json:"file"`              // The output filename with hash
	Src     string   `json:"src,omitempty"`     // The source file path
	IsEntry bool     `json:"isEntry,omitempty"` // True if this is an entry point
	CSS     []string `json:"css,omitempty"`     // Associated CSS files
	Imports []string `json:"imports,omitempty"` // Dynamic imports
}

// AssetPaths holds the resolved asset paths for the SPA template.
type AssetPaths struct {
	EntryJS  string   // Main JavaScript entry point (e.g., "index-a1b2c3d4.js")
	EntryCSS []string // CSS files associated with the entry point
}

// manifestPath is the relative path to the Vite manifest within the dist directory.
const manifestPath = ".vite/manifest.json"

// LoadManifestFromFS loads and parses the Vite manifest from an embedded filesystem.
func LoadManifestFromFS(fsys fs.FS) (*AssetPaths, error) {
	data, err := fs.ReadFile(fsys, manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest from embedded FS: %w", err)
	}
	return parseManifest(data)
}

// LoadManifestFromDisk loads and parses the Vite manifest from the local filesystem.
// The distDir is a trusted path from our own configuration (frontend/dist).
func LoadManifestFromDisk(distDir string) (*AssetPaths, error) {
	path := filepath.Join(distDir, manifestPath)
	data, err := os.ReadFile(path) //nolint:gosec // path is constructed from trusted distDir + constant manifestPath
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest from disk: %w", err)
	}
	return parseManifest(data)
}

// parseManifest parses the manifest JSON and extracts the entry point assets.
// Note: This assumes a single entry point (standard SPA configuration). Vite produces
// exactly one entry with isEntry: true for our build configuration (index.html).
func parseManifest(data []byte) (*AssetPaths, error) {
	var manifest ViteManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest JSON: %w", err)
	}

	// Find the entry point - it's marked with isEntry: true
	// Map iteration order is non-deterministic in Go, but Vite only produces
	// one entry with isEntry: true for a standard SPA build.
	for _, entry := range manifest {
		if entry.IsEntry {
			return &AssetPaths{
				EntryJS:  entry.File,
				EntryCSS: entry.CSS,
			}, nil
		}
	}

	return nil, fmt.Errorf("no entry point found in manifest")
}

// ManifestExistsOnDisk checks if the Vite manifest exists in the given dist directory.
func ManifestExistsOnDisk(distDir string) bool {
	path := filepath.Join(distDir, manifestPath)
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
