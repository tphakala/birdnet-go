package monitor

import (
	"os"
	"path/filepath"

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
	// Expand environment variables without creating directories
	path = os.ExpandEnv(path)
	
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
	allPaths := make([]string, len(configured), len(configured)+len(critical))
	copy(allPaths, configured)

	// Add critical paths
	allPaths = append(allPaths, critical...)

	// Remove duplicates and return
	return deduplicatePaths(allPaths)
}


// GetMonitoringPathsInfo returns information about configured and auto-detected paths
func GetMonitoringPathsInfo(settings *conf.Settings) (configured, autoDetected, merged []string) {
	configured = settings.Realtime.Monitoring.Disk.Paths
	autoDetected = GetCriticalPaths(settings)
	merged = mergePaths(configured, autoDetected)
	return configured, autoDetected, merged
}