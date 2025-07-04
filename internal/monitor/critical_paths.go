package monitor

import (
	"path/filepath"
	"strings"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// GetCriticalPaths returns filesystem paths critical to BirdNET-Go operation
// that should be automatically monitored for disk usage.
// These paths are added at runtime and not persisted to the configuration file.
func GetCriticalPaths(settings *conf.Settings) []string {
	paths := make([]string, 0)

	// Always monitor root filesystem
	paths = append(paths, "/")

	// Add SQLite database path if enabled
	if settings.Output.SQLite.Enabled && settings.Output.SQLite.Path != "" {
		dbPath := resolvePath(settings.Output.SQLite.Path)
		// Get the directory containing the database
		dbDir := filepath.Dir(dbPath)
		if dbDir != "." && dbDir != "" {
			paths = append(paths, dbDir)
		}
	}

	// Add audio export path if enabled
	if settings.Realtime.Audio.Export.Enabled && settings.Realtime.Audio.Export.Path != "" {
		clipPath := resolvePath(settings.Realtime.Audio.Export.Path)
		if clipPath != "." && clipPath != "" {
			paths = append(paths, clipPath)
		}
	}

	// Add config directory
	if configPath, err := conf.FindConfigFile(); err == nil {
		configDir := filepath.Dir(configPath)
		if configDir != "." && configDir != "" {
			paths = append(paths, configDir)
		}
	}

	// If running in container, ensure critical volumes are monitored
	if conf.RunningInContainer() {
		// In Docker, these are the standard volume mount points
		paths = append(paths, "/data", "/config")
	}

	// Remove duplicates and clean paths
	return deduplicatePaths(paths)
}

// resolvePath converts a relative path to absolute path
func resolvePath(path string) string {
	// Expand environment variables
	path = conf.GetBasePath(path)
	
	// Clean the path
	path = filepath.Clean(path)
	
	// Convert to absolute if not already
	if !filepath.IsAbs(path) {
		if absPath, err := filepath.Abs(path); err == nil {
			path = absPath
		}
	}
	
	return path
}

// deduplicatePaths removes duplicate paths and returns unique, cleaned paths
func deduplicatePaths(paths []string) []string {
	seen := make(map[string]bool)
	unique := make([]string, 0)
	
	for _, path := range paths {
		// Clean and resolve the path
		cleaned := filepath.Clean(path)
		
		// Skip empty paths
		if cleaned == "" || cleaned == "." {
			continue
		}
		
		// Convert to absolute path if possible
		if !filepath.IsAbs(cleaned) {
			if absPath, err := filepath.Abs(cleaned); err == nil {
				cleaned = absPath
			}
		}
		
		// Add if not seen before
		if !seen[cleaned] {
			seen[cleaned] = true
			unique = append(unique, cleaned)
		}
	}
	
	return unique
}

// mergePaths combines user-configured paths with auto-detected critical paths
func mergePaths(configured, critical []string) []string {
	// Start with configured paths
	allPaths := make([]string, len(configured))
	copy(allPaths, configured)
	
	// Add critical paths
	allPaths = append(allPaths, critical...)
	
	// Remove duplicates and return
	return deduplicatePaths(allPaths)
}

// isSameFilesystem checks if two paths are on the same filesystem
// This helps avoid duplicate monitoring of the same disk
func isSameFilesystem(path1, path2 string) bool {
	// For now, we do simple prefix matching for subdirectories
	// A more robust implementation would use syscalls to check device IDs
	path1 = filepath.Clean(path1)
	path2 = filepath.Clean(path2)
	
	// Check if one is a subdirectory of the other
	if strings.HasPrefix(path1, path2+string(filepath.Separator)) {
		return true
	}
	if strings.HasPrefix(path2, path1+string(filepath.Separator)) {
		return true
	}
	
	return path1 == path2
}

// filterRedundantPaths removes paths that are subdirectories of already monitored paths
func filterRedundantPaths(paths []string) []string {
	// Sort paths by length (shorter first)
	filtered := make([]string, 0)
	
	for _, path := range paths {
		redundant := false
		for _, existing := range filtered {
			if isSameFilesystem(path, existing) {
				// If the new path is a subdirectory of an existing path, skip it
				if strings.HasPrefix(path, existing+string(filepath.Separator)) {
					redundant = true
					break
				}
			}
		}
		
		if !redundant {
			// Check if any existing paths are subdirectories of this new path
			// If so, replace them
			newFiltered := []string{path}
			for _, existing := range filtered {
				if !strings.HasPrefix(existing, path+string(filepath.Separator)) {
					newFiltered = append(newFiltered, existing)
				}
			}
			filtered = newFiltered
		}
	}
	
	return filtered
}

// GetMonitoringPathsInfo returns information about configured and auto-detected paths
func GetMonitoringPathsInfo(settings *conf.Settings) (configured, autoDetected, merged []string) {
	configured = settings.Realtime.Monitoring.Disk.Paths
	autoDetected = GetCriticalPaths(settings)
	merged = mergePaths(configured, autoDetected)
	return configured, autoDetected, merged
}