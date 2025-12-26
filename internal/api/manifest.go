package api

import (
	"os"
	"path/filepath"
)

// manifestPath is the relative path to the Vite manifest within the dist directory.
const manifestPath = ".vite/manifest.json"

// ManifestExistsOnDisk checks if the Vite manifest exists in the given dist directory.
// The manifest is generated during production builds. If it exists on disk alongside
// index.html, we serve the built frontend from disk rather than embedded assets.
func ManifestExistsOnDisk(distDir string) bool {
	path := filepath.Join(distDir, manifestPath)
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
