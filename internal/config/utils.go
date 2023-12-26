package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// getDefaultConfigPaths returns a list of default config paths for the current OS
func getDefaultConfigPaths() ([]string, error) {
	var configPaths []string

	// Get the executable directory
	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("error fetching executable path: %v", err)
	}
	exeDir := filepath.Dir(exePath)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("error fetching user directory: %v", err)
	}

	switch runtime.GOOS {
	case "windows":
		// Windows path
		configPaths = []string{
			exeDir,
			filepath.Join(homeDir, "AppData", "Roaming", "birdnet-go"),
		}
	default:
		// Linux and macOS path
		configPaths = []string{
			filepath.Join(homeDir, ".config", "birdnet-go"),
			"/etc/birdnet-go",
		}
	}

	return configPaths, nil
}

// getBasePath expands variables such as $HOME or %appdata%,
// and if the path is relative, assumes it's relative to the directory of the executing binary
func GetBasePath(path string) string {
	// Expand environment variables
	expandedPath := os.ExpandEnv(path)

	// Clean the path to remove trailing slashes and fix any irregularities
	basePath := filepath.Clean(expandedPath)

	// Create the directory if it doesn't exist
	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		// Create the directory with appropriate permissions
		if err := os.MkdirAll(basePath, 0755); err != nil {
			fmt.Printf("failed to create directory '%s': %v\n", basePath, err)
			//return fmt.Errorf("failed to create directory '%s': %w", basePath, err)
		}
	}

	return basePath
}
