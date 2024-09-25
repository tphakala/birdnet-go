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

	"github.com/tphakala/birdnet-go/internal/conf"
)

// CollectDiagnostics gathers system information and logs
func CollectDiagnostics() (string, error) {
	tmpDir, err := os.MkdirTemp("", "birdnet-go-diagnostics-")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Collect OS-specific diagnostics
	switch runtime.GOOS {
	case "linux":
		err = collectLinuxDiagnostics(tmpDir)
	case "windows":
		err = collectWindowsDiagnostics(tmpDir)
	case "darwin":
		err = collectMacOSDiagnostics(tmpDir)
	default:
		err = fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	if err != nil {
		return "", err
	}

	// Compress the diagnostics files
	zipFile := tmpDir + ".zip"
	err = zipDirectory(tmpDir, zipFile)
	if err != nil {
		return "", fmt.Errorf("failed to compress diagnostics: %w", err)
	}

	// Clean up the temporary directory
	os.RemoveAll(tmpDir)

	return zipFile, nil
}

func collectLinuxDiagnostics(tmpDir string) error {
	// Check if system is systemd-based with journald
	if hasSystemd() {
		collectJournaldLogs(tmpDir)
	}

	// Collect hardware details
	runCommand("lshw", []string{"-short"}, filepath.Join(tmpDir, "hardware_info.txt"))

	// Check for Raspberry Pi
	if isRaspberryPi() {
		runCommand("cat", []string{"/proc/cpuinfo"}, filepath.Join(tmpDir, "raspberry_pi_info.txt"))
	}

	// Collect package list
	collectPackageList(tmpDir)

	// Collect sound devices
	collectSoundDevices(tmpDir)

	// Collect resource information
	collectResourceInfo(tmpDir)

	// Collect config file
	if err := collectConfigFile(tmpDir); err != nil {
		fmt.Printf("Warning: Failed to collect config file: %v\n", err)
	}

	return nil
}

func collectWindowsDiagnostics(tmpDir string) error {
	// Implement Windows-specific diagnostics collection
	fmt.Println("Not implemented yet")
	return nil
}

func collectMacOSDiagnostics(tmpDir string) error {
	// Implement macOS-specific diagnostics collection
	fmt.Println("Not implemented yet")
	return nil
}

func hasSystemd() bool {
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

func collectJournaldLogs(tmpDir string) {
	sevenDaysAgo := time.Now().AddDate(0, 0, -7).Format("2006-01-02 15:04:05")
	runCommand("journalctl", []string{"-u", "birdnet-go", "--since", sevenDaysAgo}, filepath.Join(tmpDir, "birdnet-go_logs.txt"))
}

func isRaspberryPi() bool {
	content, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return false
	}
	return strings.Contains(string(content), "Raspberry Pi")
}

func collectPackageList(tmpDir string) {
	if _, err := exec.LookPath("dpkg"); err == nil {
		runCommand("dpkg", []string{"-l"}, filepath.Join(tmpDir, "package_list_dpkg.txt"))
	} else if _, err := exec.LookPath("rpm"); err == nil {
		runCommand("rpm", []string{"-qa"}, filepath.Join(tmpDir, "package_list_rpm.txt"))
	} else {
		// Fallback to a generic package list method
		runCommand("ls", []string{"/var/lib/dpkg/info/*.list"}, filepath.Join(tmpDir, "package_list_generic.txt"))
	}
}

func collectSoundDevices(tmpDir string) {
	runCommand("aplay", []string{"-l"}, filepath.Join(tmpDir, "alsa_devices.txt"))
	runCommand("pactl", []string{"list"}, filepath.Join(tmpDir, "pulseaudio_info.txt"))
	runCommand("pw-cli", []string{"list-objects"}, filepath.Join(tmpDir, "pipewire_info.txt"))
	runCommand("lsusb", []string{}, filepath.Join(tmpDir, "usb_devices.txt"))
}

func collectResourceInfo(tmpDir string) {
	runCommand("free", []string{"-h"}, filepath.Join(tmpDir, "memory_info.txt"))
	runCommand("df", []string{"-h"}, filepath.Join(tmpDir, "disk_space.txt"))
	runCommand("lsblk", []string{}, filepath.Join(tmpDir, "block_devices.txt"))
	runCommand("top", []string{"-bn1"}, filepath.Join(tmpDir, "cpu_info.txt"))
}

func runCommand(command string, args []string, outputFile string) error {
	cmd := exec.Command(command, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error running command %s: %w", command, err)
	}
	if err := os.WriteFile(outputFile, output, 0644); err != nil {
		return fmt.Errorf("error writing output to file %s: %w", outputFile, err)
	}
	return nil
}

func zipDirectory(source, target string) error {
	zipfile, err := os.Create(target)
	if err != nil {
		return err
	}
	defer zipfile.Close()

	archive := zip.NewWriter(zipfile)
	defer archive.Close()

	err = filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		header.Name = strings.TrimPrefix(path, source+"/")
		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}

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

func collectConfigFile(tmpDir string) error {
	configPaths, err := conf.GetDefaultConfigPaths()
	if err != nil {
		return fmt.Errorf("error getting default config paths: %w", err)
	}

	var configPath string
	for _, path := range configPaths {
		possiblePath := filepath.Join(path, "config.yaml")
		if _, err := os.Stat(possiblePath); err == nil {
			configPath = possiblePath
			break
		}
	}

	if configPath == "" {
		return fmt.Errorf("config.yaml not found in any of the default paths")
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("error reading config file: %w", err)
	}

	maskedContent := maskSensitiveInfo(string(content))

	outputPath := filepath.Join(tmpDir, "config.yaml")
	err = os.WriteFile(outputPath, []byte(maskedContent), 0644)
	if err != nil {
		return fmt.Errorf("error writing masked config file: %w", err)
	}

	return nil
}

func maskSensitiveInfo(content string) string {
	lines := strings.Split(content, "\n")
	sensitiveFields := map[string]bool{
		"id":       true,
		"apikey":   true,
		"username": true,
		"password": true,
		"broker":   true,
		"topic":    true,
		"urls":     true,
	}

	for i, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(strings.ToLower(parts[0]))
			value := strings.TrimSpace(parts[1])

			if sensitiveFields[key] {
				maskedValue := maskValue(value)
				lines[i] = fmt.Sprintf("%s: %s", parts[0], maskedValue)
			} else if isIPOrURL(value) && !isLocalhost(value) {
				maskedValue := maskIPOrURL(value)
				lines[i] = fmt.Sprintf("%s: %s", parts[0], maskedValue)
			}
		}
	}

	return strings.Join(lines, "\n")
}

func maskValue(value string) string {
	length := len(value)
	return strings.Repeat("*", length)
}

func isIPOrURL(value string) bool {
	ipRegex := regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}(:\d+)?$`)
	urlRegex := regexp.MustCompile(`^(http|https|rtsp):\/\/`)
	return ipRegex.MatchString(value) || urlRegex.MatchString(value)
}

func isLocalhost(value string) bool {
	return value == "127.0.0.1" || value == "0.0.0.0" || strings.HasPrefix(value, "localhost")
}

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
