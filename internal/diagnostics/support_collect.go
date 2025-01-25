// Package diagnostics provides functions for collecting and reporting diagnostics information
package diagnostics

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// CollectDiagnostics gathers system information and logs
func CollectDiagnostics() (string, error) {
	// Check if running in a container
	if conf.RunningInContainer() {
		orange := color.New(color.FgYellow).Add(color.Bold).SprintFunc()
		fmt.Println(orange("Notice: Diagnostics collection is running within a container."))
		fmt.Println(orange("Access to system resources may be limited, and some diagnostic information may not be available."))
	}

	tmpDir, err := os.MkdirTemp("", "birdnet-go-diagnostics-")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir) // Clean up the temporary directory

	// Collect OS-specific diagnostics
	switch runtime.GOOS {
	case "linux":
		err = collectLinuxDiagnostics(tmpDir)
	case "windows":
		err = collectWindowsDiagnostics()
	case "darwin":
		err = collectMacOSDiagnostics()
	default:
		err = fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	// We got an error, bail out
	if err != nil {
		return "", err
	}

	// Get the config file path
	configPath, err := conf.FindConfigFile()
	if err != nil {
		return "", fmt.Errorf("failed to find config file: %w", err)
	}

	// Create the zip file next to the config file
	zipFileName := fmt.Sprintf("birdnet-go-diagnostics-%s.zip", time.Now().Format("20060102-150405"))
	zipFilePath := filepath.Join(filepath.Dir(configPath), zipFileName)

	// Compress the diagnostics files
	if err := zipDirectory(tmpDir, zipFilePath); err != nil {
		return "", fmt.Errorf("failed to compress diagnostics: %w", err)
	}

	return zipFilePath, nil
}

// collectLinuxDiagnostics gathers Linux-specific diagnostic information
func collectLinuxDiagnostics(tmpDir string) error {
	// Collect system information
	collectSystemInfo(tmpDir)

	if hasSystemd() {
		if err := collectJournaldLogs(tmpDir); err != nil {
			fmt.Printf("Notice: Failed to collect journald logs: %v\n", err)
			// We continue execution despite this error, as it's not critical
		}
	} else {
		if err := collectLegacyLogs(tmpDir); err != nil {
			return fmt.Errorf("failed to collect legacy logs: %w", err)
		}
	}

	if err := collectHardwareInfo(tmpDir); err != nil {
		return fmt.Errorf("failed to collect hardware info: %w", err)
	}

	return nil
}

// collectSystemInfo gathers system information
func collectSystemInfo(tmpDir string) {
	commands := []struct {
		cmd  string
		args []string
		out  string
	}{
		{"uname", []string{"-a"}, "system_info.txt"},
		{"env", nil, "environment_variables.txt"},
		{"ps", []string{"aux"}, "process_list.txt"},
		{"dmesg", nil, "kernel_messages.txt"},
	}

	for _, c := range commands {
		if err := runCommand(c.cmd, c.args, filepath.Join(tmpDir, c.out)); err != nil {
			fmt.Printf("Notice: Failed to collect %s: %v\n", c.out, err)
		}
	}
}

// collectLegacyLogs collects logs from non-systemd systems
func collectLegacyLogs(tmpDir string) error {
	logFile := filepath.Join(tmpDir, "container_logs.txt")
	f, err := os.Create(logFile)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}
	defer f.Close()

	// Check for /var/log directory
	if _, err := os.Stat("/var/log"); !os.IsNotExist(err) {
		cmd := exec.Command("find", "/var/log", "-type", "f", "-name", "*.log", "-o", "-name", "*.txt", "-o", "-name", "*[^.]*")
		output, err := cmd.Output()
		if err != nil {
			fmt.Printf("Notice: Failed to find log files: %v\n", err)
		} else {
			logFiles := strings.Split(string(output), "\n")
			for _, file := range logFiles {
				if file == "" {
					continue
				}
				tailCmd := exec.Command("tail", "-n", "1000", file)
				tailOutput, err := tailCmd.Output()
				if err != nil {
					fmt.Printf("Notice: Failed to tail %s: %v\n", file, err)
					continue
				}
				if _, err := fmt.Fprintf(f, "=== %s ===\n", file); err != nil {
					return fmt.Errorf("failed to write file header: %w", err)
				}
				if _, err := f.Write(tailOutput); err != nil {
					return fmt.Errorf("failed to write tail output: %w", err)
				}
				if _, err := f.WriteString("\n\n"); err != nil {
					return fmt.Errorf("failed to write file footer: %w", err)
				}
			}
		}
	}

	return nil
}

// collectHardwareInfo gathers hardware information
func collectHardwareInfo(tmpDir string) error {
	// Collect hardware details
	if err := runCommand("lshw", []string{"-short"}, filepath.Join(tmpDir, "hardware_info.txt")); err != nil {
		fmt.Println("Notice: 'lshw' not found on system, skipping hardware info collection")
	}

	// Check for Raspberry Pi
	if isRaspberryPi() {
		if err := runCommand("cat", []string{"/proc/cpuinfo"}, filepath.Join(tmpDir, "raspberry_pi_info.txt")); err != nil {
			fmt.Println("Notice: Unable to read Raspberry Pi info, skipping this collection")
		}
	}

	// Collect package list
	if err := collectPackageList(tmpDir); err != nil {
		fmt.Println("Notice: Package list collection skipped due to unavailable package managers")
	}

	// Collect sound devices
	if err := collectSoundDevices(tmpDir); err != nil {
		fmt.Println("Notice: Sound devices info collection incomplete or skipped")
	}

	// Collect resource information
	if err := collectResourceInfo(tmpDir); err != nil {
		fmt.Println("Notice: Resource info collection incomplete or skipped")
	}

	return nil
}

// collectWindowsDiagnostics gathers Windows-specific diagnostic information
func collectWindowsDiagnostics() error {
	// TODO: Implement Windows-specific diagnostics collection
	// This function should gather information such as:
	// - System information (using 'systeminfo' command)
	// - List of installed programs
	// - Windows event logs
	// - Sound device information
	// - Hardware details
	// Each piece of information should be saved to a separate file in the tmpDir
	fmt.Println("Windows diagnostics collection not implemented yet")
	return nil
}

// collectMacOSDiagnostics gathers macOS-specific diagnostic information
func collectMacOSDiagnostics() error {
	// TODO: Implement macOS-specific diagnostics collection
	// This function should gather information such as:
	// - System information (using 'system_profiler' command)
	// - List of installed applications
	// - System logs
	// - Sound device information (using 'system_profiler SPAudioDataType')
	// - Hardware details
	// Each piece of information should be saved to a separate file in the tmpDir
	fmt.Println("macOS diagnostics collection not implemented yet")
	return nil
}

// hasSystemd checks if the system is using systemd
func hasSystemd() bool {
	// Check if the systemd directory exists
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

// collectJournaldLogs collects logs from journald for the birdnet-go service
func collectJournaldLogs(tmpDir string) error {
	// Calculate the date 7 days ago
	sevenDaysAgo := time.Now().AddDate(0, 0, -7).Format("2006-01-02 15:04:05")
	// Run journalctl command to collect logs
	return runCommand("journalctl", []string{"-u", "birdnet-go", "--since", sevenDaysAgo}, filepath.Join(tmpDir, "birdnet-go_logs.txt"))
}

// isRaspberryPi checks if the system is a Raspberry Pi
func isRaspberryPi() bool {
	// Read the contents of /proc/cpuinfo
	content, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return false
	}
	// Check if the content contains "Raspberry Pi"
	return strings.Contains(string(content), "Raspberry Pi")
}

// collectPackageList gathers the list of installed packages
func collectPackageList(tmpDir string) error {
	// Check for dpkg (Debian-based systems)
	if _, err := exec.LookPath("dpkg"); err == nil {
		return runCommand("dpkg", []string{"-l"}, filepath.Join(tmpDir, "package_list_dpkg.txt"))
		// Check for rpm (Red Hat-based systems)
	} else if _, err := exec.LookPath("rpm"); err == nil {
		return runCommand("rpm", []string{"-qa"}, filepath.Join(tmpDir, "package_list_rpm.txt"))
	} else {
		// Fallback to a generic package list method
		return runCommand("ls", []string{"/var/lib/dpkg/info/*.list"}, filepath.Join(tmpDir, "package_list_generic.txt"))
	}
}

// collectSoundDevices gathers information about sound devices
func collectSoundDevices(tmpDir string) error {
	commands := []struct {
		cmd  string
		args []string
		out  string
	}{
		{"aplay", []string{"-l"}, "alsa_devices.txt"},
		{"pactl", []string{"list"}, "pulseaudio_info.txt"},
		{"pw-cli", []string{"list-objects"}, "pipewire_info.txt"},
		{"lsusb", []string{}, "usb_devices.txt"},
	}

	// Run each command and save its output
	for _, c := range commands {
		if err := runCommand(c.cmd, c.args, filepath.Join(tmpDir, c.out)); err != nil {
			fmt.Printf("Notice: '%s' not found or unable to run, skipping %s collection\n", c.cmd, c.out)
		}
	}
	return nil
}

// collectResourceInfo gathers information about system resources
func collectResourceInfo(tmpDir string) error {
	commands := []struct {
		cmd  string
		args []string
		out  string
	}{
		{"free", []string{"-h"}, "memory_info.txt"},
		{"df", []string{"-h"}, "disk_space.txt"},
		{"lsblk", []string{}, "block_devices.txt"},
		{"top", []string{"-bn1"}, "cpu_info.txt"},
	}

	// Run each command and save its output
	for _, c := range commands {
		if err := runCommand(c.cmd, c.args, filepath.Join(tmpDir, c.out)); err != nil {
			fmt.Printf("Notice: '%s' not found or unable to run, skipping %s collection\n", c.cmd, c.out)
		}
	}
	return nil
}

// runCommand executes a system command and saves its output to a file
func runCommand(command string, args []string, outputFile string) error {
	// Create the command with its arguments
	cmd := exec.Command(command, args...)

	// Execute the command and capture both stdout and stderr
	output, err := cmd.CombinedOutput()
	if err != nil {
		// If there's an error, return it with additional context
		return fmt.Errorf("error running command %s: %w", command, err)
	}

	// Write the command output to the specified file
	// 0644 sets read/write permissions for owner, and read-only for others
	return os.WriteFile(outputFile, output, 0o644)
}

// zipDirectory compresses the contents of a source directory into a zip file
func zipDirectory(source, target string) error {
	// Create the target zip file
	zipfile, err := os.Create(target)
	if err != nil {
		return err
	}
	defer zipfile.Close()

	// Create a new zip archive
	archive := zip.NewWriter(zipfile)
	defer archive.Close()

	// Walk through the source directory
	err = filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Create a zip header from the file info
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		// Set the header name, removing the source path prefix
		header.Name = strings.TrimPrefix(path, source+"/")
		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		// Create a writer for the file within the archive
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}

		// If it's not a directory, copy the file contents to the archive
		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("failed to open file %s: %w", path, err)
			}
			defer func() {
				if closeErr := file.Close(); closeErr != nil {
					err = fmt.Errorf("failed to close file %s: %w (previous error: %w)", path, closeErr, err)
				}
			}()

			_, err = io.Copy(writer, file)
			if err != nil {
				return fmt.Errorf("failed to copy file %s to zip: %w", path, err)
			}
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("error walking the path %s: %w", source, err)
	}

	return nil
}

// collectConfigFile finds the config file, masks sensitive information, and copies it to the temporary directory
func collectConfigFile(tmpDir string) error {
	// Get the default config paths
	configPaths, err := conf.GetDefaultConfigPaths()
	if err != nil {
		return fmt.Errorf("error getting default config paths: %w", err)
	}

	// Find the first existing config file
	var configPath string
	for _, path := range configPaths {
		possiblePath := filepath.Join(path, "config.yaml")
		if _, err := os.Stat(possiblePath); err == nil {
			configPath = possiblePath
			break
		}
	}

	// Check if a config file was found
	if configPath == "" {
		return fmt.Errorf("config.yaml not found in any of the default paths")
	}

	// Read the config file
	content, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("error reading config file: %w", err)
	}

	// Mask sensitive information in the config file
	maskedContent := maskSensitiveInfo(string(content))

	// Write the masked config to the temporary directory
	outputPath := filepath.Join(tmpDir, "config.yaml")
	err = os.WriteFile(outputPath, []byte(maskedContent), 0o644)
	if err != nil {
		return fmt.Errorf("error writing masked config file: %w", err)
	}

	return nil
}

// maskSensitiveInfo takes a string content and masks sensitive information
func maskSensitiveInfo(content string) string {
	lines := strings.Split(content, "\n")
	// Define sensitive fields that should be masked
	sensitiveFields := map[string]bool{
		"id":       true,
		"apikey":   true,
		"username": true,
		"password": true,
		"broker":   true,
		"topic":    true,
		"urls":     true,
	}

	// Iterate through each line of the content
	for i, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(strings.ToLower(parts[0]))
			value := strings.TrimSpace(parts[1])

			// Mask the value if it's a sensitive field
			if sensitiveFields[key] {
				maskedValue := maskValue(value)
				lines[i] = fmt.Sprintf("%s: %s", parts[0], maskedValue)
			} else if isIPOrURL(value) && !isLocalhost(value) {
				// Mask IP addresses and URLs that are not localhost
				maskedValue := maskIPOrURL(value)
				lines[i] = fmt.Sprintf("%s: %s", parts[0], maskedValue)
			}
		}
	}

	return strings.Join(lines, "\n")
}

// maskValue replaces all characters in a string with asterisks
func maskValue(value string) string {
	length := len(value)
	return strings.Repeat("*", length)
}

// isIPOrURL checks if a string is an IP address or URL
func isIPOrURL(value string) bool {
	ipRegex := regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}(:\d+)?$`)
	urlRegex := regexp.MustCompile(`^(http|https|rtsp)://`)
	return ipRegex.MatchString(value) || urlRegex.MatchString(value)
}

// isLocalhost checks if a string represents a localhost address
func isLocalhost(value string) bool {
	return value == "127.0.0.1" || value == "0.0.0.0" || strings.HasPrefix(value, "localhost")
}

// maskIPOrURL masks an IP address or URL while preserving the protocol
func maskIPOrURL(value string) string {
	parts := strings.Split(value, "://")
	if len(parts) > 1 {
		protocol := parts[0]
		rest := parts[1]
		maskedRest := maskValue(rest)
		return fmt.Sprintf("%s://%s", protocol, maskedRest)
	}
	return maskValue(value)
}
