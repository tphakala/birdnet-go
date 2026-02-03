// conf/utils.go various util functions for configuration package
package conf

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// OS name constants for runtime.GOOS comparisons.
const (
	osLinux   = "linux"
	osWindows = "windows"
)

// getDefaultConfigPaths returns a list of default configuration paths for the current operating system.
// It determines paths based on standard conventions for storing application configuration files.
// If a config.yaml file is found in any of the paths, it returns that path as the default.
func GetDefaultConfigPaths() ([]string, error) {
	var configPaths []string

	// Fetch the directory of the executable.
	exePath, err := os.Executable()
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategorySystem).
			Context("operation", "get-executable-path").
			Build()
	}
	exeDir := filepath.Dir(exePath)

	// Fetch the user's home directory.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategorySystem).
			Context("operation", "get-home-directory").
			Build()
	}

	// Define default paths based on the operating system.
	switch runtime.GOOS {
	case osWindows:
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
		return "", errors.New(err).
			Category(errors.CategoryConfiguration).
			Context("operation", "find-config-paths").
			Build()
	}

	for _, path := range configPaths {
		configFilePath := filepath.Join(path, "config.yaml")
		if _, err := os.Stat(configFilePath); err == nil {
			return configFilePath, nil
		}
	}

	return "", errors.Newf("config file not found").
		Category(errors.CategoryFileIO).
		Context("operation", "find-config-file").
		Build()
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
		if err := os.MkdirAll(basePath, 0o750); err != nil {
			fmt.Printf("failed to create directory '%s': %v\n", basePath, err)
			// Note: In a robust application, you might want to handle this error more gracefully.
		}
	}

	return basePath
}

// GetHLSDirectory returns the directory where HLS files should be stored
func GetHLSDirectory() (string, error) {
	// Get config directory paths
	configPaths, err := GetDefaultConfigPaths()
	if err != nil {
		return "", errors.New(err).
			Category(errors.CategoryConfiguration).
			Context("operation", "hls-get-config-paths").
			Build()
	}

	if len(configPaths) == 0 {
		return "", fmt.Errorf("no config paths found")
	}

	// Use the first config path as the base
	baseDir := configPaths[0]

	// Create HLS directory path
	hlsDir := filepath.Join(baseDir, "hls")

	// Get absolute path for consistent operations
	absPath, err := filepath.Abs(hlsDir)
	if err != nil {
		return "", errors.New(err).
			Category(errors.CategoryFileIO).
			Context("operation", "hls-get-abs-path").
			Context("path", hlsDir).
			Build()
	}

	// Create directory if it doesn't exist
	if err := os.MkdirAll(absPath, 0o750); err != nil {
		return "", errors.New(err).
			Category(errors.CategoryFileIO).
			Context("operation", "hls-create-directory").
			Context("path", absPath).
			Build()
	}

	return absPath, nil
}

// PrintUserInfo checks the operating system. If it's Linux, it prints the current user and their group memberships.
func PrintUserInfo() {
	// Initialize a flag to check if the user is a member of the audio group
	var audioMember = false

	// Check if the operating system is Linux
	if runtime.GOOS == osLinux {
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
			GetLogger().Warn("Failed to get group memberships", logger.Error(err))
			return
		}

		// Iterate through each group ID
		for _, gid := range groupIDs {
			// Look up the group information for each group ID
			group, err := user.LookupGroupId(gid)
			if err != nil {
				GetLogger().Warn("Failed to lookup group", logger.String("gid", gid), logger.Error(err))
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
			GetLogger().Error("User is not member of audio group",
				logger.String("username", currentUser.Username),
				logger.String("fix_command", fmt.Sprintf("sudo usermod -a -G audio %s", currentUser.Username)))
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
	defer func() {
		if err := file.Close(); err != nil {
			GetLogger().Warn("Failed to close /proc/self/cgroup", logger.Error(err))
		}
	}()

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
	return runtime.GOOS == osLinux && runtime.GOARCH == "arm64"
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
	if before, ok := strings.CutSuffix(percentage, "%"); ok {
		value, err := strconv.ParseFloat(before, 64)
		if err != nil {
			return 0, err
		}
		return value, nil
	}
	return 0, errors.Newf("invalid percentage format").
		Component("conf").
		Category(errors.CategoryValidation).
		Context("input", percentage).
		Build()
}

// ParseRetentionPeriod converts a string like "24h", "7d", "1w", "3m", "1y" to hours.
func ParseRetentionPeriod(retention string) (int, error) {
	if retention == "" {
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
	if runtime.GOOS == osWindows {
		return "ffmpeg.exe"
	}
	return "ffmpeg"
}

// GetSoxBinaryName returns the binary name for sox based on the current OS.
func GetSoxBinaryName() string {
	if runtime.GOOS == osWindows {
		return "sox.exe"
	}
	return "sox"
}

// GetFfprobeBinaryName returns the binary name for ffprobe based on the current OS.
func GetFfprobeBinaryName() string {
	if runtime.GOOS == osWindows {
		return "ffprobe.exe"
	}
	return "ffprobe"
}

// IsFfmpegAvailable checks if ffmpeg is available in the system PATH.
func IsFfmpegAvailable() bool {
	_, err := exec.LookPath(GetFfmpegBinaryName())
	return err == nil
}

// IsFfprobeAvailable checks if ffprobe is available in the system PATH.
func IsFfprobeAvailable() bool {
	_, err := exec.LookPath(GetFfprobeBinaryName())
	return err == nil
}

// GetFfmpegVersion detects the installed ffmpeg version and returns the version string,
// major version, and minor version. Returns empty string and 0,0 if detection fails.
func GetFfmpegVersion() (version string, major, minor int) {
	// Get the ffmpeg binary name
	ffmpegBinary := GetFfmpegBinaryName()

	// Look for ffmpeg in PATH
	ffmpegPath, err := exec.LookPath(ffmpegBinary)
	if err != nil {
		return "", 0, 0
	}

	// Execute ffmpeg -version
	cmd := exec.Command(ffmpegPath, "-version") //nolint:gosec // G204: ffmpegPath resolved via exec.LookPath()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", 0, 0
	}

	// Parse the version from output
	return ParseFfmpegVersion(string(output))
}

// ParseFfmpegVersion parses ffmpeg version output and extracts version string and numbers.
// Expected format: "ffmpeg version 7.1.2-0+deb13u1 Copyright (c) 2000-2025..."
// or: "ffmpeg version 5.1.7-0+deb12u1+rpt1 Copyright (c) 2000-2025..."
// or: "ffmpeg version N-121000-g7321e4b950 Copyright (c) 2000-2025..." (git builds)
// For git builds, it falls back to parsing library versions like "libavutil 59. 8.100"
func ParseFfmpegVersion(output string) (version string, major, minor int) {
	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return "", 0, 0
	}

	// First line contains version info
	line := lines[0]

	// Extract version string (between "version " and " Copyright" or space)
	if versionIdx := strings.Index(line, "version "); versionIdx != -1 {
		versionStart := versionIdx + len("version ")
		remaining := line[versionStart:]

		// Find the end of version string (space or copyright notice)
		versionEnd := strings.Index(remaining, " ")
		if versionEnd == -1 {
			versionEnd = len(remaining)
		}

		version = remaining[:versionEnd]

		// Parse major.minor from version string (e.g., "7.1.2-0+deb13u1" -> 7, 1)
		versionParts := strings.Split(version, ".")
		if len(versionParts) >= 2 {
			// Parse major version
			if maj, err := strconv.Atoi(versionParts[0]); err == nil {
				major = maj
			}

			// Parse minor version (may have additional characters like "1-0")
			minorPart := versionParts[1]
			// Extract just the numeric part before any dash or other character
			for i, c := range minorPart {
				if c < '0' || c > '9' {
					minorPart = minorPart[:i]
					break
				}
			}
			if minorValue, err := strconv.Atoi(minorPart); err == nil {
				minor = minorValue
			}
		}

		// If we couldn't parse a numeric version (e.g., git builds like "N-121000-g7321e4b950"),
		// try to extract version from library versions (more reliable for git builds)
		if major == 0 && minor == 0 {
			major, minor = parseLibavutilVersion(output)
		}
	}

	return version, major, minor
}

// parseLibavutilVersion extracts ffmpeg version from libavutil version line.
// libavutil versions map to ffmpeg versions:
// libavutil 60.x = FFmpeg 7.x (FFmpeg 7.1+)
// libavutil 59.x = FFmpeg 7.x (FFmpeg 7.0)
// libavutil 58.x = FFmpeg 6.x
// libavutil 57.x = FFmpeg 5.x
// libavutil 56.x = FFmpeg 4.x
func parseLibavutilVersion(output string) (major, minor int) {
	lines := strings.SplitSeq(output, "\n")
	for line := range lines {
		// Look for libavutil version line like "libavutil      59.  8.100 / 59.  8.100"
		// or "libavutil      60. 12.100 / 60. 12.100"
		if strings.Contains(line, "libavutil") {
			// Extract the version number after "libavutil"
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				// parts[0] = "libavutil"
				// parts[1] = "60." or "59."
				// parts[2] = "12.100" or "8.100"

				// Parse libavutil major version (e.g., "60." -> 60)
				libMajorStr := strings.TrimSuffix(parts[1], ".")
				if libMajor, err := strconv.Atoi(libMajorStr); err == nil {
					// Map libavutil major version to ffmpeg version
					// This is a heuristic based on historical ffmpeg releases
					switch {
					case libMajor >= 60:
						major = 7
					case libMajor >= 59:
						major = 7
					case libMajor >= 58:
						major = 6
					case libMajor >= 57:
						major = 5
					case libMajor >= 56:
						major = 4
					default:
						major = 3
					}

					// Try to parse libavutil minor version (e.g., "12.100" -> 12)
					// This roughly corresponds to ffmpeg minor version
					minorPart := parts[2]
					// Split on dot and take first part
					minorParts := strings.Split(minorPart, ".")
					if len(minorParts) > 0 {
						if minorNum, err := strconv.Atoi(minorParts[0]); err == nil {
							minor = minorNum
						}
					}
					break
				}
			}
		}
	}
	return major, minor
}

// IsSoxAvailable checks if SoX is available in the system PATH and returns its supported audio formats.
// It returns a boolean indicating if SoX is available and a slice of supported audio format strings.
func IsSoxAvailable() (isAvailable bool, formats []string) {
	// Look for the SoX binary in the system PATH
	soxPath, err := exec.LookPath(GetSoxBinaryName())
	if err != nil {
		return false, nil // SoX is not available
	}

	// Execute SoX with the help flag to get its output
	cmd := exec.Command(soxPath, "-h") //nolint:gosec // G204: soxPath resolved via exec.LookPath()
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
		if formats, found := strings.CutPrefix(line, "AUDIO FILE FORMATS:"); found {
			// Extract and process the list of audio formats
			formats = strings.TrimSpace(formats)
			audioFormats = strings.Fields(formats)
			break
		}
	}

	return true, audioFormats // SoX is available, return the list of supported formats
}

// ValidateToolPath checks if a tool is available, either at an explicit path or in the system PATH.
// It returns the validated path to the tool if found, or an empty string and an error otherwise.
func ValidateToolPath(configuredPath, toolName string) (string, error) {
	if configuredPath != "" {
		// Check if the explicitly configured path exists and is a file
		if info, err := os.Stat(configuredPath); err == nil && !info.IsDir() {
			// Ideally, we'd check execute permissions here, but os.Stat doesn't provide a cross-platform way.
			// We assume if it exists and isn't a directory, it's likely the executable.
			// The actual execution will fail later if it's not executable.
			return configuredPath, nil
		}
		// If configured path is invalid, log a warning but still check PATH as a fallback
		GetLogger().Warn("Configured tool path invalid or not found, checking system PATH",
			logger.String("configured_path", configuredPath),
			logger.String("tool", toolName))
	}

	// If no configured path or the configured path was invalid, check the system PATH
	pathFromLookPath, err := exec.LookPath(toolName)
	if err == nil {
		return pathFromLookPath, nil // Found in PATH
	}

	// If not found in configured path or system PATH
	if configuredPath != "" {
		return "", fmt.Errorf("tool '%s' not found at configured path '%s' or in system PATH", toolName, configuredPath)
	}
	return "", fmt.Errorf("tool '%s' not found in system PATH and no path configured", toolName)
}

// moveFile moves a file from src to dst, working across devices
func moveFile(src, dst string) error {
	// Try to rename the file first (this works for moves within the same filesystem)
	if err := os.Rename(src, dst); err == nil {
		return nil // If rename succeeds, we're done
	}

	// If rename fails, fall back to copy and delete method
	// Validate paths to prevent directory traversal
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("error resolving source path: %w", err)
	}
	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("error resolving destination path: %w", err)
	}

	srcFile, err := os.Open(srcAbs) //nolint:gosec // G304: srcAbs is filepath.Abs resolved path
	if err != nil {
		return fmt.Errorf("error opening source file: %w", err)
	}
	defer func() {
		if err := srcFile.Close(); err != nil {
			GetLogger().Warn("Failed to close source file", logger.Error(err))
		}
	}() // Ensure the source file is closed when we're done

	dstFile, err := os.Create(dstAbs) //nolint:gosec // G304: dstAbs is filepath.Abs resolved path
	if err != nil {
		return fmt.Errorf("error creating destination file: %w", err)
	}
	defer func() {
		if err := dstFile.Close(); err != nil {
			GetLogger().Warn("Failed to close destination file", logger.Error(err))
		}
	}() // Ensure the destination file is closed when we're done

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

/*
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
*/

// GetHostIP returns the host IP address, resolving host.docker.internal if running in a container
func GetHostIP() (net.IP, error) {
	// If we're running in a container, try to get the host IP
	if RunningInContainer() {
		// Try various methods to get the host IP from inside the container
		if ip := resolveDockerHost(); ip != nil {
			return ip, nil
		}

		if ip := resolveGatewayFromRoute(); ip != nil {
			return ip, nil
		}
	}

	// Fall back to local interface IP if not in container or previous methods failed
	return getLocalInterfaceIP()
}

// resolveDockerHost attempts to resolve host.docker.internal or host-gateway
func resolveDockerHost() net.IP {
	// Try host.docker.internal first (works on Docker for Mac/Windows)
	if ip := lookupHostname("host.docker.internal"); ip != nil {
		return ip
	}

	// Try host-gateway (works on newer Docker versions)
	return lookupHostname("host-gateway")
}

// lookupHostname tries to resolve a hostname to a usable IPv4 address
func lookupHostname(hostname string) net.IP {
	ips, err := net.LookupIP(hostname)
	if err == nil && len(ips) > 0 {
		for _, ip := range ips {
			// Return the first non-loopback IPv4 address
			if ip.To4() != nil && !ip.IsLoopback() {
				return ip
			}
		}
	}
	return nil
}

// resolveGatewayFromRoute tries to find the default gateway from /proc/net/route
func resolveGatewayFromRoute() net.IP {
	file, err := os.Open("/proc/net/route")
	if err != nil {
		return nil
	}
	defer func() {
		if err := file.Close(); err != nil {
			// Log error but don't fail - this is a best-effort operation
			GetLogger().Warn("Failed to close /proc/net/route", logger.Error(err))
		}
	}()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		// Check for default route (destination 0.0.0.0)
		if len(fields) >= 3 && fields[1] == "00000000" {
			return parseGatewayHex(fields[2])
		}
	}
	return nil
}

// parseGatewayHex converts a hex gateway address to net.IP
func parseGatewayHex(gatewayHex string) net.IP {
	if len(gatewayHex) != 8 {
		return nil
	}

	ip := make([]byte, 4)
	for i := range 4 {
		b, err := strconv.ParseUint(gatewayHex[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil
		}
		ip[3-i] = byte(b)
	}
	return ip
}

// getLocalInterfaceIP returns the first non-loopback IPv4 address
func getLocalInterfaceIP() (net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("failed to get interface addresses: %w", err)
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipv4 := ipnet.IP.To4(); ipv4 != nil {
				return ipv4, nil
			}
		}
	}

	return nil, fmt.Errorf("no suitable IP address found")
}

// IsInHostSubnet checks if the given IP is in the same subnet as the host
func IsInHostSubnet(clientIP net.IP) bool {
	if clientIP == nil {
		return false
	}

	// Get the host IP
	hostIP, err := GetHostIP()
	if err != nil {
		GetLogger().Warn("Error getting host IP", logger.Error(err))
		return false
	}

	// Get the /24 subnet for client
	clientSubnet := getIPv4Subnet(clientIP, 24)
	if clientSubnet == nil {
		return false
	}

	// Get the /24 subnet for host
	hostSubnet := getIPv4Subnet(hostIP, 24)
	if hostSubnet == nil {
		return false
	}

	// Compare subnets
	return clientSubnet.Equal(hostSubnet)
}

// getIPv4Subnet converts an IP address to its subnet address with specified mask bits
func getIPv4Subnet(ip net.IP, bits int) net.IP {
	if ip == nil {
		return nil
	}

	// Convert to IPv4 if possible
	ipv4 := ip.To4()
	if ipv4 == nil {
		return nil
	}

	// Apply the subnet mask (e.g., for bits=24, this creates a 255.255.255.0 mask)
	return ipv4.Mask(net.CIDRMask(bits, 32))
}
