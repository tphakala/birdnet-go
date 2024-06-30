// conf/utils.go various util functions for configuration package
package conf

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/mitchellh/mapstructure"
)

// getDefaultConfigPaths returns a list of default configuration paths for the current operating system.
// It determines paths based on standard conventions for storing application configuration files.
func GetDefaultConfigPaths() ([]string, error) {
	var configPaths []string

	// Fetch the directory of the executable.
	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("error fetching executable path: %w", err)
	}
	exeDir := filepath.Dir(exePath)

	// Fetch the user's home directory.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("error fetching user home directory: %w", err)
	}

	// Define default paths based on the operating system.
	switch runtime.GOOS {
	case "windows":
		// For Windows, use the executable directory and the AppData Roaming directory.
		configPaths = []string{
			exeDir,
			filepath.Join(homeDir, "AppData", "Roaming", "birdnet-go"),
		}
	default:
		// For Linux and macOS, use a hidden directory in the home directory and a system-wide configuration directory.
		configPaths = []string{
			filepath.Join(homeDir, ".config", "birdnet-go"),
			"/etc/birdnet-go",
		}
	}

	return configPaths, nil
}

// GetBasePath expands environment variables in the given path and ensures the resulting path exists.
// If the path is relative, it's interpreted as relative to the directory of the executing binary.
func GetBasePath(path string) string {
	// Expand environment variables in the path.
	expandedPath := os.ExpandEnv(path)

	// Normalize the path to handle any irregularities such as trailing slashes.
	basePath := filepath.Clean(expandedPath)

	// Check if the directory exists.
	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		// Attempt to create the directory if it doesn't exist.
		if err := os.MkdirAll(basePath, 0755); err != nil {
			fmt.Printf("failed to create directory '%s': %v\n", basePath, err)
			// Note: In a robust application, you might want to handle this error more gracefully.
		}
	}

	return basePath
}

// PrintUserInfo checks the operating system. If it's Linux, it prints the current user and their group memberships.
func PrintUserInfo() {
	var audioMember bool = false
	// Get current user
	if runtime.GOOS == "linux" {
		currentUser, err := user.Current()
		if err != nil {
			fmt.Printf("Failed to get current user: %v\n", err)
			return
		}

		// if current user is root, return as it has all permissions anyway
		if currentUser.Username == "root" {
			return
		}

		// Get group memberships
		groupIDs, err := currentUser.GroupIds()
		if err != nil {
			log.Printf("Failed to get group memberships: %v\n", err)
			return
		}

		for _, gid := range groupIDs {
			group, err := user.LookupGroupId(gid)
			if err != nil {
				log.Printf("Failed to lookup group for ID %s: %v\n", gid, err)
				continue
			}
			//fmt.Printf(" - %s (ID: %s)\n", group.Name, group.Gid)
			// check if audio is one of groups
			if group.Name == "audio" {
				audioMember = true
			}
		}
		if !audioMember {
			log.Printf("ERROR: User '%s' is not member of audio group, add user to audio group by executing", currentUser.Username)
			log.Println("sudo usermod -a -G audio", currentUser.Username)
		}
	}
}

// RunningInContainer checks if the program is running inside a container.
func RunningInContainer() bool {
	// Check for the existence of the /.dockerenv file (Docker-specific).
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}

	// Check for the existence of the /run/.containerenv file (Podman-specific).
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}

	// Check the container environment variable.
	if containerEnv, exists := os.LookupEnv("container"); exists && containerEnv != "" {
		return true
	}

	// Check cgroup for hints of container runtime.
	file, err := os.Open("/proc/self/cgroup")
	if err != nil {
		fmt.Println("Error opening /proc/self/cgroup:", err)
		return false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "docker") || strings.Contains(line, "podman") {
			return true
		}
	}

	return false
}

// isLinuxArm64 checks if the operating system is Linux and the architecture is arm64.
func IsLinuxArm64() bool {
	return runtime.GOOS == "linux" && runtime.GOARCH == "arm64"
}

// getBoardModel reads the SBC board model from the device tree.
func GetBoardModel() string {
	// Get the board model from the device tree.
	data, err := os.ReadFile("/proc/device-tree/model")
	if err != nil {
		return ""
	}

	// Return the board model as a string.
	model := strings.TrimSpace(string(data))
	return model
}

// structToMap converts a struct to a map using mapstructure
func structToMap(settings *Settings) (map[string]interface{}, error) {
	var result map[string]interface{}
	err := mapstructure.Decode(settings, &result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ParsePercentage converts a percentage string (e.g., "80%") to a float64
func ParsePercentage(percentage string) (float64, error) {
	if strings.HasSuffix(percentage, "%") {
		value, err := strconv.ParseFloat(strings.TrimSuffix(percentage, "%"), 64)
		if err != nil {
			return 0, err
		}
		return value, nil
	}
	return 0, errors.New("invalid percentage format")
}

// ParseRetentionPeriod converts a string like "24h", "7d", "1w", "3m", "1y" to hours.
func ParseRetentionPeriod(retention string) (int, error) {
	if len(retention) == 0 {
		return 0, fmt.Errorf("retention period cannot be empty")
	}

	// Try to parse the retention period
	lastChar := retention[len(retention)-1]
	numberPart := retention[:len(retention)-1]

	// Handle case where the input is a plain integer
	if lastChar >= '0' && lastChar <= '9' {
		hours, err := strconv.Atoi(retention)
		if err != nil {
			return 0, fmt.Errorf("invalid retention period format: %s", retention)
		}
		return hours, nil
	}

	number, err := strconv.Atoi(numberPart)
	if err != nil {
		return 0, fmt.Errorf("invalid retention period format: %s", retention)
	}

	switch lastChar {
	case 'h':
		return number, nil
	case 'd':
		return number * 24, nil
	case 'w':
		return number * 24 * 7, nil
	case 'm':
		return number * 24 * 30, nil // Approximation, as months can vary in length
	case 'y':
		return number * 24 * 365, nil // Ignoring leap years for simplicity
	default:
		return 0, fmt.Errorf("invalid suffix for retention period: %c", lastChar)
	}
}
