//go:build linux

package datastore

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tphakala/birdnet-go/internal/logger"
	"golang.org/x/sys/unix"
)

// getMountInfoPlatform finds the mount point and filesystem type for Unix systems
func getMountInfoPlatform(path string) (*MountInfo, error) {
	// Clean and get absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	// Read /proc/mounts to find matching mount point
	file, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			// Log but don't override the main error
			GetLogger().Warn("Failed to close /proc/mounts", logger.Error(closeErr))
		}
	}()

	var bestMatch *MountInfo
	var longestMatch int

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 3 {
			mountPoint := fields[1]
			fsType := fields[2]

			// Check if this mount point is a parent of our path
			if strings.HasPrefix(absPath, mountPoint) && len(mountPoint) > longestMatch {
				bestMatch = &MountInfo{
					MountPoint:     mountPoint,
					FileSystemType: fsType,
				}
				longestMatch = len(mountPoint)
			}
		}
	}

	if bestMatch == nil {
		return nil, fmt.Errorf("no mount point found for path %s", path)
	}

	return bestMatch, scanner.Err()
}

// getInodeInfoPlatform gets inode information for Unix systems
func getInodeInfoPlatform(path string) (*InodeInfo, error) {
	var stat unix.Statfs_t
	err := unix.Statfs(path, &stat)
	if err != nil {
		return nil, fmt.Errorf("failed to get filesystem stats for %s: %w", path, err)
	}

	return &InodeInfo{
		Free:  stat.Ffree,
		Total: stat.Files,
	}, nil
}

// captureMemoryInfo gathers system memory information for Unix systems
func captureMemoryInfo() (MemoryInfo, error) {
	info := MemoryInfo{}

	// Read /proc/meminfo
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return info, fmt.Errorf("failed to read /proc/meminfo: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			GetLogger().Warn("Failed to close /proc/meminfo", logger.Error(closeErr))
		}
	}()

	memData := make(map[string]uint64)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			key := strings.TrimSuffix(fields[0], ":")
			if value, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				// Convert from KB to bytes
				memData[key] = value * 1024
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return info, fmt.Errorf("error reading /proc/meminfo: %w", err)
	}

	// Calculate memory values
	info.TotalBytes = memData["MemTotal"]
	available := memData["MemAvailable"]
	if available == 0 {
		// Fallback calculation if MemAvailable is not available
		available = memData["MemFree"] + memData["Buffers"] + memData["Cached"]
	}
	info.AvailableBytes = available
	info.UsedBytes = info.TotalBytes - info.AvailableBytes

	if info.TotalBytes > 0 {
		info.UsedPercent = float64(info.UsedBytes) / float64(info.TotalBytes) * 100.0
	}

	// Swap information
	info.SwapTotalBytes = memData["SwapTotal"]
	swapFree := memData["SwapFree"]
	info.SwapUsedBytes = info.SwapTotalBytes - swapFree

	return info, nil
}

// getProcessMemoryUsage gets memory usage for the current process on Unix systems
func getProcessMemoryUsage() (*ProcessMemoryUsage, error) {
	pid := os.Getpid()

	// Read /proc/self/status for memory information
	file, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return nil, fmt.Errorf("failed to read process status: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			GetLogger().Warn("Failed to close process status file", logger.Error(closeErr))
		}
	}()

	var vmRSS, vmSize uint64
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			switch fields[0] {
			case "VmRSS:":
				if value, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
					vmRSS = value // Already in KB
				}
			case "VmSize:":
				if value, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
					vmSize = value // Already in KB
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading process status: %w", err)
	}

	return &ProcessMemoryUsage{
		ResidentMB: int64(vmRSS / 1024),  //nolint:gosec // G115: memory in MB always fits int64 (max ~8 exabytes)
		VirtualMB:  int64(vmSize / 1024), //nolint:gosec // G115: memory in MB always fits int64 (max ~8 exabytes)
	}, nil
}
