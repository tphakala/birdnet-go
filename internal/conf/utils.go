// conf/utils.go various util functions for configuration package
package conf

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// getDefaultConfigPaths returns a list of default configuration paths for the current operating system.
// It determines paths based on standard conventions for storing application configuration files.
// If a config.yaml file is found in any of the paths, it returns that path as the default.
func GetDefaultConfigPaths() ([]string, error) {
	var configPaths []string

	// Prioritize CONFIG_PATH environment variable if set
	configPath := os.Getenv("CONFIG_PATH")
	if configPath != "" {
		return []string{configPath}, nil
	}

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

	// Check if config.yaml exists in any of the paths
	for _, path := range configPaths {
		configFile := filepath.Join(path, "config.yaml")
		if _, err := os.Stat(configFile); err == nil {
			// Config file found, return this path as the only default path
			return []string{path}, nil
		}
	}

	// If no config.yaml is found, return all paths
	return configPaths, nil
}

// findConfigFile locates the configuration file.
func FindConfigFile() (string, error) {
	configPaths, err := GetDefaultConfigPaths()
	if err != nil {
		return "", fmt.Errorf("error getting default config paths: %w", err)
	}

	for _, path := range configPaths {
		configFilePath := filepath.Join(path, "config.yaml")
		if _, err := os.Stat(configFilePath); err == nil {
			return configFilePath, nil
		}
	}

	return "", fmt.Errorf("config file not found")
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
	// Initialize a flag to check if the user is a member of the audio group
	var audioMember bool = false

	// Check if the operating system is Linux
	if runtime.GOOS == "linux" {
		// Get current user information
		currentUser, err := user.Current()
		if err != nil {
			fmt.Printf("Failed to get current user: %v\n", err)
			return
		}

		// If current user is root, return as it has all permissions anyway
		if currentUser.Username == "root" {
			return
		}

		// Get group memberships for the current user
		groupIDs, err := currentUser.GroupIds()
		if err != nil {
			log.Printf("Failed to get group memberships: %v\n", err)
			return
		}

		// Iterate through each group ID
		for _, gid := range groupIDs {
			// Look up the group information for each group ID
			group, err := user.LookupGroupId(gid)
			if err != nil {
				log.Printf("Failed to lookup group for ID %s: %v\n", gid, err)
				continue
			}
			// Uncomment the following line to print group information
			//fmt.Printf(" - %s (ID: %s)\n", group.Name, group.Gid)

			// Check if the user is a member of the 'audio' group
			if group.Name == "audio" {
				audioMember = true
			}
		}

		// If the user is not a member of the 'audio' group, print an error message
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

// ParseWeekday converts a string to time.Weekday
func ParseWeekday(day string) (time.Weekday, error) {
	switch strings.ToLower(day) {
	case "sunday":
		return time.Sunday, nil
	case "monday":
		return time.Monday, nil
	case "tuesday":
		return time.Tuesday, nil
	case "wednesday":
		return time.Wednesday, nil
	case "thursday":
		return time.Thursday, nil
	case "friday":
		return time.Friday, nil
	case "saturday":
		return time.Saturday, nil
	default:
		return time.Sunday, fmt.Errorf("invalid weekday: %s", day)
	}
}

// GetRotationDay returns the time.Weekday representation of RotationDay
func (lc *LogConfig) GetRotationDay() (time.Weekday, error) {
	return ParseWeekday(lc.RotationDay)
}

// GetLocalTimezone returns the local time zone of the system.
func GetLocalTimezone() (*time.Location, error) {
	return time.Local, nil
}

// ConvertUTCToLocal converts a UTC time to the local time zone.
func ConvertUTCToLocal(utcTime time.Time) (time.Time, error) {
	localLoc, err := GetLocalTimezone()
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get local timezone: %w", err)
	}
	return utcTime.In(localLoc), nil
}

// GetFfmpegBinaryName returns the binary name for ffmpeg based on the current OS.
func GetFfmpegBinaryName() string {
	if runtime.GOOS == "windows" {
		return "ffmpeg.exe"
	}
	return "ffmpeg"
}

// GetSoxBinaryName returns the binary name for sox based on the current OS.
func GetSoxBinaryName() string {
	if runtime.GOOS == "windows" {
		return "sox.exe"
	}
	return "sox"
}

// IsFfmpegAvailable checks if ffmpeg is available in the system PATH.
func IsFfmpegAvailable() bool {
	_, err := exec.LookPath(GetFfmpegBinaryName())
	return err == nil
}

// IsSoxAvailable checks if SoX is available in the system PATH and returns its supported audio formats.
// It returns a boolean indicating if SoX is available and a slice of supported audio format strings.
func IsSoxAvailable() (bool, []string) {
	// Look for the SoX binary in the system PATH
	soxPath, err := exec.LookPath(GetSoxBinaryName())
	if err != nil {
		return false, nil // SoX is not available
	}

	// Execute SoX with the help flag to get its output
	cmd := exec.Command(soxPath, "-h")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false, nil // Failed to execute SoX
	}

	// Convert the output to a string and split it into lines
	outputStr := string(output)
	lines := strings.Split(outputStr, "\n")

	var audioFormats []string
	// Iterate through the lines to find the supported audio formats
	for _, line := range lines {
		if strings.HasPrefix(line, "AUDIO FILE FORMATS:") {
			// Extract and process the list of audio formats
			formats := strings.TrimPrefix(line, "AUDIO FILE FORMATS:")
			formats = strings.TrimSpace(formats)
			audioFormats = strings.Fields(formats)
			break
		}
	}

	return true, audioFormats // SoX is available, return the list of supported formats
}

// moveFile moves a file from src to dst, working across devices
func moveFile(src, dst string) error {
	// Try to rename the file first (this works for moves within the same filesystem)
	if err := os.Rename(src, dst); err == nil {
		return nil // If rename succeeds, we're done
	}

	// If rename fails, fall back to copy and delete method
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("error opening source file: %w", err)
	}
	defer srcFile.Close() // Ensure the source file is closed when we're done

	dstFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("error creating destination file: %w", err)
	}
	defer dstFile.Close() // Ensure the destination file is closed when we're done

	// Copy the contents from source to destination
	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return fmt.Errorf("error copying file contents: %w", err)
	}

	// After successful copy, delete the source file
	if err := os.Remove(src); err != nil {
		// If we can't remove the source, we should inform the caller
		// The move was partially successful (the copy succeeded)
		return fmt.Errorf("error removing source file after copy: %w", err)
	}

	return nil // Move completed successfully
}

// IsSafePath ensures the given path is internal
func IsSafePath(path string) bool {
	return strings.HasPrefix(path, "/") &&
		!strings.Contains(path, "//") &&
		!strings.Contains(path, "\\") &&
		!strings.Contains(path, "://") &&
		!strings.Contains(path, "..") &&
		!strings.Contains(path, "\x00") &&
		len(path) < 512
}
