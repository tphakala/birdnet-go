package api

import (
	"os"
	"path/filepath"
)

// manifestPath is the relative path to the Vite manifest within the dist directory.
const manifestPath = ".vite/manifest.json"

// ManifestExistsOnDisk checks if the Vite manifest exists in the given dist directory.
// This is used to detect dev mode - if the manifest exists on disk, we're in dev mode.
func ManifestExistsOnDisk(distDir string) bool {
	path := filepath.Join(distDir, manifestPath)
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
