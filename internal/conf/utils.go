// conf/utils.go
package conf

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
)

// getDefaultConfigPaths returns a list of default configuration paths for the current operating system.
// It determines paths based on standard conventions for storing application configuration files.
func GetDefaultConfigPaths() ([]string, error) {
	var configPaths []string

	// Fetch the directory of the executable.
	exePath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("error fetching executable path: %v", err)
	}
	exeDir := filepath.Dir(exePath)

	// Fetch the user's home directory.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("error fetching user home directory: %v", err)
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
