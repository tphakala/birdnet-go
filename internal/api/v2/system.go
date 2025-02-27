// internal/api/v2/system.go
package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// SystemInfo represents basic system information
type SystemInfo struct {
	OS            string    `json:"os"`
	Architecture  string    `json:"architecture"`
	Hostname      string    `json:"hostname"`
	Platform      string    `json:"platform"`
	PlatformVer   string    `json:"platform_version"`
	KernelVersion string    `json:"kernel_version"`
	UpTime        uint64    `json:"uptime_seconds"`
	BootTime      time.Time `json:"boot_time"`
	AppStart      time.Time `json:"app_start_time"`
	AppUptime     int64     `json:"app_uptime_seconds"`
	NumCPU        int       `json:"num_cpu"`
	GoVersion     string    `json:"go_version"`
}

// ResourceInfo represents system resource usage data
type ResourceInfo struct {
	CPUUsage    float64 `json:"cpu_usage_percent"`
	MemoryTotal uint64  `json:"memory_total"`
	MemoryUsed  uint64  `json:"memory_used"`
	MemoryFree  uint64  `json:"memory_free"`
	MemoryUsage float64 `json:"memory_usage_percent"`
	SwapTotal   uint64  `json:"swap_total"`
	SwapUsed    uint64  `json:"swap_used"`
	SwapFree    uint64  `json:"swap_free"`
	SwapUsage   float64 `json:"swap_usage_percent"`
	ProcessMem  float64 `json:"process_memory_mb"`
	ProcessCPU  float64 `json:"process_cpu_percent"`
}

// DiskInfo represents information about a disk
type DiskInfo struct {
	Device     string  `json:"device"`
	Mountpoint string  `json:"mountpoint"`
	Fstype     string  `json:"fstype"`
	Total      uint64  `json:"total"`
	Used       uint64  `json:"used"`
	Free       uint64  `json:"free"`
	UsagePerc  float64 `json:"usage_percent"`
	// Fields added for more comprehensive disk info
	InodesTotal     uint64  `json:"inodes_total,omitempty"`         // Total number of inodes (Unix-like only)
	InodesUsed      uint64  `json:"inodes_used,omitempty"`          // Number of used inodes (Unix-like only)
	InodesFree      uint64  `json:"inodes_free,omitempty"`          // Number of free inodes (Unix-like only)
	InodesUsagePerc float64 `json:"inodes_usage_percent,omitempty"` // Percentage of inodes used (Unix-like only)
	ReadBytes       uint64  `json:"read_bytes,omitempty"`           // Total number of bytes read
	WriteBytes      uint64  `json:"write_bytes,omitempty"`          // Total number of bytes written
	ReadCount       uint64  `json:"read_count,omitempty"`           // Total number of read operations
	WriteCount      uint64  `json:"write_count,omitempty"`          // Total number of write operations
	ReadTime        uint64  `json:"read_time,omitempty"`            // Time spent reading (in milliseconds)
	WriteTime       uint64  `json:"write_time,omitempty"`           // Time spent writing (in milliseconds)
	IOBusyPerc      float64 `json:"io_busy_percent,omitempty"`      // Percentage of time the disk was busy with I/O operations
	IOTime          uint64  `json:"io_time,omitempty"`              // Total time spent on I/O operations (in milliseconds)
	IsRemote        bool    `json:"is_remote"`                      // Whether the filesystem is a network mount
	IsReadOnly      bool    `json:"is_read_only"`                   // Whether the filesystem is mounted as read-only
}

// AudioDeviceInfo wraps the myaudio.AudioDeviceInfo struct for API responses
type AudioDeviceInfo struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	ID    string `json:"id"`
}

// ActiveAudioDevice represents the currently active audio device
type ActiveAudioDevice struct {
	Name       string `json:"name"`
	ID         string `json:"id"`
	SampleRate int    `json:"sample_rate"`
	BitDepth   int    `json:"bit_depth"`
	Channels   int    `json:"channels"`
}

// Use monotonic clock for start time
var startTime = time.Now()
var startMonotonicTime = time.Now() // This inherently includes monotonic clock reading

// Initialize system routes
func (c *Controller) initSystemRoutes() {
	// Create system API group
	systemGroup := c.Group.Group("/system")

	// Create auth-protected group using our middleware
	protectedGroup := systemGroup.Group("", c.AuthMiddleware)

	// Add system routes (all protected)
	protectedGroup.GET("/info", c.GetSystemInfo)
	protectedGroup.GET("/resources", c.GetResourceInfo)
	protectedGroup.GET("/disks", c.GetDiskInfo)

	// Audio device routes (all protected)
	audioGroup := protectedGroup.Group("/audio")
	audioGroup.GET("/devices", c.GetAudioDevices)
	audioGroup.GET("/active", c.GetActiveAudioDevice)
}

// GetSystemInfo handles GET /api/v2/system/info
func (c *Controller) GetSystemInfo(ctx echo.Context) error {
	// Get host info
	hostInfo, err := host.Info()
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get host information", http.StatusInternalServerError)
	}

	// Get hostname
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	// Calculate app uptime using monotonic clock to avoid system time changes
	appUptime := int64(time.Since(startMonotonicTime).Seconds())

	// Create response
	info := SystemInfo{
		OS:            runtime.GOOS,
		Architecture:  runtime.GOARCH,
		Hostname:      hostname,
		Platform:      hostInfo.Platform,
		PlatformVer:   hostInfo.PlatformVersion,
		KernelVersion: hostInfo.KernelVersion,
		UpTime:        hostInfo.Uptime,
		BootTime:      time.Unix(int64(hostInfo.BootTime), 0),
		AppStart:      startTime,
		AppUptime:     appUptime,
		NumCPU:        runtime.NumCPU(),
		GoVersion:     runtime.Version(),
	}

	return ctx.JSON(http.StatusOK, info)
}

// GetResourceInfo handles GET /api/v2/system/resources
func (c *Controller) GetResourceInfo(ctx echo.Context) error {
	// Get memory statistics
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get memory information", http.StatusInternalServerError)
	}

	// Get swap statistics
	swapInfo, err := mem.SwapMemory()
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get swap information", http.StatusInternalServerError)
	}

	// Get CPU usage
	cpuPercent, err := cpu.Percent(time.Second, false) // Average of all cores over 1 second
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get CPU information", http.StatusInternalServerError)
	}

	// Get process information (current process)
	proc, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get process information", http.StatusInternalServerError)
	}

	procMem, err := proc.MemoryInfo()
	if err != nil {
		c.Debug("Failed to get process memory info: %v", err)
		// Continue with nil procMem, handled below
	}

	procCPU, err := proc.CPUPercent()
	if err != nil {
		c.Debug("Failed to get process CPU info: %v", err)
		// Will use 0 as default value
		procCPU = 0
	}

	// Convert process memory to MB for readability
	var procMemMB float64
	if procMem != nil {
		procMemMB = float64(procMem.RSS) / 1024 / 1024
	}

	// Create response
	resourceInfo := ResourceInfo{
		MemoryTotal: memInfo.Total,
		MemoryUsed:  memInfo.Used,
		MemoryFree:  memInfo.Free,
		MemoryUsage: memInfo.UsedPercent,
		SwapTotal:   swapInfo.Total,
		SwapUsed:    swapInfo.Used,
		SwapFree:    swapInfo.Free,
		SwapUsage:   swapInfo.UsedPercent,
		ProcessMem:  procMemMB,
		ProcessCPU:  procCPU,
	}

	// If we got CPU data, use the first value (total)
	if len(cpuPercent) > 0 {
		resourceInfo.CPUUsage = cpuPercent[0]
	}

	return ctx.JSON(http.StatusOK, resourceInfo)
}

// GetDiskInfo handles GET /api/v2/system/disks
func (c *Controller) GetDiskInfo(ctx echo.Context) error {
	// Get partitions
	partitions, err := disk.Partitions(false)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get disk partitions", http.StatusInternalServerError)
	}

	// Create slice to hold disk info
	disks := []DiskInfo{}

	// Try to get IO counters for all disks
	ioCounters, ioErr := disk.IOCounters()
	if ioErr != nil {
		c.Debug("Failed to get IO counters: %v", ioErr)
		// Continue without IO metrics
	}

	// Get host info for uptime calculation
	hostInfo, err := host.Info()
	var uptimeMs uint64 = 0
	if err != nil {
		c.Debug("Failed to get host information for uptime: %v", err)
	} else {
		// Convert uptime to milliseconds for IO busy calculation
		uptimeMs = hostInfo.Uptime * 1000
	}

	// Process each partition
	for _, partition := range partitions {
		// Skip special filesystems
		if skipFilesystem(partition.Fstype) {
			continue
		}

		// Create disk info with default values
		diskInfo := DiskInfo{
			Device:     partition.Device,
			Mountpoint: partition.Mountpoint,
			Fstype:     partition.Fstype,
			IsRemote:   isRemoteFilesystem(partition.Fstype),
			IsReadOnly: isReadOnlyMount(partition.Opts),
		}

		// Get usage statistics
		usage, err := disk.Usage(partition.Mountpoint)
		if err != nil {
			c.Debug("Failed to get usage for %s: %v", partition.Mountpoint, err)
			// Add partial information to indicate the disk exists but usage couldn't be determined
			diskInfo.Total = 0
			diskInfo.Used = 0
			diskInfo.Free = 0
			diskInfo.UsagePerc = 0
		} else {
			// Add usage metrics
			diskInfo.Total = usage.Total
			diskInfo.Used = usage.Used
			diskInfo.Free = usage.Free
			diskInfo.UsagePerc = usage.UsedPercent

			// Add inode usage statistics if available (usually only on Unix-like systems)
			if usage.InodesTotal > 0 {
				diskInfo.InodesTotal = usage.InodesTotal
				diskInfo.InodesUsed = usage.InodesUsed
				diskInfo.InodesFree = usage.InodesFree
				diskInfo.InodesUsagePerc = usage.InodesUsedPercent
			}
		}

		// Add IO metrics if available
		deviceName := getDeviceBaseName(partition.Device)
		if counter, exists := ioCounters[deviceName]; exists {
			diskInfo.ReadBytes = counter.ReadBytes
			diskInfo.WriteBytes = counter.WriteBytes
			diskInfo.ReadCount = counter.ReadCount
			diskInfo.WriteCount = counter.WriteCount
			diskInfo.ReadTime = counter.ReadTime
			diskInfo.WriteTime = counter.WriteTime
			diskInfo.IOTime = counter.IoTime

			// Calculate I/O busy percentage if uptime is available
			if uptimeMs > 0 && counter.IoTime > 0 {
				// IoTime is the time spent doing I/Os (ms)
				diskInfo.IOBusyPerc = float64(counter.IoTime) / float64(uptimeMs) * 100

				// Cap at 100% (in case of measurement anomalies)
				if diskInfo.IOBusyPerc > 100 {
					diskInfo.IOBusyPerc = 100
				}
			} else if counter.ReadTime > 0 || counter.WriteTime > 0 {
				// Alternative calculation using read/write times if IoTime is not available
				// This is less accurate but provides a reasonable approximation
				totalIOTime := counter.ReadTime + counter.WriteTime
				if uptimeMs > 0 {
					diskInfo.IOBusyPerc = float64(totalIOTime) / float64(uptimeMs) * 100

					// Cap at 100%
					if diskInfo.IOBusyPerc > 100 {
						diskInfo.IOBusyPerc = 100
					}
				}
			}
		}

		// Add disk info to response
		disks = append(disks, diskInfo)
	}

	return ctx.JSON(http.StatusOK, disks)
}

// getDeviceBaseName extracts the base device name (e.g., "sda" from "/dev/sda1")
func getDeviceBaseName(device string) string {
	// First get the basename (remove directory path)
	base := filepath.Base(device)

	// Then remove any numbers at the end (partition numbers)
	for i := len(base) - 1; i >= 0; i-- {
		if base[i] < '0' || base[i] > '9' {
			if i < len(base)-1 {
				return base[:i+1]
			}
			return base
		}
	}
	return base
}

// isRemoteFilesystem returns true if the filesystem is a network mount
func isRemoteFilesystem(fstype string) bool {
	remoteFsTypes := map[string]bool{
		"nfs":        true,
		"nfs4":       true,
		"cifs":       true,
		"smbfs":      true,
		"sshfs":      true,
		"fuse.sshfs": true,
		"afs":        true,
		"9p":         true,
		"ncpfs":      true,
	}
	return remoteFsTypes[fstype]
}

// isReadOnlyMount returns true if the filesystem is mounted as read-only
func isReadOnlyMount(opts []string) bool {
	// Look for read-only option in the mount options
	for _, opt := range opts {
		if opt == "ro" {
			return true
		}
	}
	return false
}

// GetAudioDevices handles GET /api/v2/system/audio/devices
func (c *Controller) GetAudioDevices(ctx echo.Context) error {
	// Get audio devices
	devices, err := myaudio.ListAudioSources()
	if err != nil {
		return c.HandleError(ctx, err, "Failed to list audio devices", http.StatusInternalServerError)
	}

	// Check if no devices were found
	if len(devices) == 0 {
		c.Debug("No audio devices found on the system")
		return ctx.JSON(http.StatusOK, []AudioDeviceInfo{}) // Return empty array instead of null
	}

	// Convert to API response format
	apiDevices := make([]AudioDeviceInfo, len(devices))
	for i, device := range devices {
		apiDevices[i] = AudioDeviceInfo{
			Index: device.Index,
			Name:  device.Name,
			ID:    device.ID,
		}
	}

	return ctx.JSON(http.StatusOK, apiDevices)
}

// GetActiveAudioDevice handles GET /api/v2/system/audio/active
func (c *Controller) GetActiveAudioDevice(ctx echo.Context) error {
	// Get active audio device from settings
	deviceName := c.Settings.Realtime.Audio.Source

	// Check if no device is configured
	if deviceName == "" {
		return ctx.JSON(http.StatusOK, map[string]interface{}{
			"device":   nil,
			"active":   false,
			"verified": false,
			"message":  "No audio device currently active",
		})
	}

	// Create response with default values
	activeDevice := ActiveAudioDevice{
		Name:       deviceName,
		SampleRate: 48000, // Standard BirdNET sample rate
		BitDepth:   16,    // Assuming 16-bit as per the capture.go implementation
		Channels:   1,     // Assuming mono as per the capture.go implementation
	}

	// Diagnostic information map
	diagnostics := map[string]interface{}{
		"os":                runtime.GOOS,
		"check_time":        time.Now().Format(time.RFC3339),
		"error_details":     nil,
		"device_found":      false,
		"available_devices": []string{},
	}

	// Try to get additional device info and validate the device exists
	devices, err := myaudio.ListAudioSources()
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to list audio devices: %v", err)
		c.Debug(errorMsg)

		// Add more detailed diagnostics
		diagnostics["error_details"] = errorMsg

		// OS-specific additional checks
		switch runtime.GOOS {
		case "windows":
			diagnostics["note"] = "On Windows, check that audio drivers are properly installed and the device is not disabled in Sound settings"
		case "darwin":
			diagnostics["note"] = "On macOS, check System Preferences > Sound and ensure the device has proper permissions"
		case "linux":
			diagnostics["note"] = "On Linux, check if PulseAudio/ALSA is running and the user has proper permissions"
		}

		// Still return the configured device, but note that we couldn't verify it exists
		return ctx.JSON(http.StatusOK, map[string]interface{}{
			"device":      activeDevice,
			"active":      true,
			"verified":    false,
			"message":     "Device configured but could not verify if it exists",
			"diagnostics": diagnostics,
		})
	}

	// Populate available devices for diagnostics
	availableDevices := make([]string, len(devices))
	for i, device := range devices {
		availableDevices[i] = device.Name
	}
	diagnostics["available_devices"] = availableDevices

	// Check if the configured device exists in the system
	deviceFound := false
	for _, device := range devices {
		if device.Name == deviceName {
			activeDevice.ID = device.ID
			deviceFound = true
			diagnostics["device_found"] = true
			break
		}
	}

	if !deviceFound {
		// Device is configured but not found on the system
		errorMsg := "Configured audio device not found on the system"
		diagnostics["suggested_action"] = "Check if the device is properly connected and recognized by the system"

		if len(devices) > 0 {
			diagnostics["suggestion"] = fmt.Sprintf("Consider using one of the available devices: %s", strings.Join(availableDevices, ", "))
		}

		return ctx.JSON(http.StatusOK, map[string]interface{}{
			"device":      activeDevice,
			"active":      true,
			"verified":    false,
			"message":     errorMsg,
			"diagnostics": diagnostics,
		})
	}

	// Device is configured and verified to exist
	return ctx.JSON(http.StatusOK, map[string]interface{}{
		"device":      activeDevice,
		"active":      true,
		"verified":    true,
		"diagnostics": diagnostics,
	})
}

// Helper functions

// FileSystemCategory represents categories of filesystems that should be handled similarly
type FileSystemCategory string

const (
	// System filesystems related to OS functionality
	SystemFS FileSystemCategory = "system"
	// Virtual filesystems that don't represent physical storage
	VirtualFS FileSystemCategory = "virtual"
	// Temporary filesystems that don't persist data
	TempFS FileSystemCategory = "temp"
	// Special filesystems with specific purposes
	SpecialFS FileSystemCategory = "special"
)

// fsTypeCategories maps filesystem types to their categories
var fsTypeCategories = map[string]FileSystemCategory{
	// System filesystems
	"sysfs":      SystemFS,
	"proc":       SystemFS,
	"procfs":     SystemFS,
	"devfs":      SystemFS,
	"devtmpfs":   SystemFS,
	"debugfs":    SystemFS,
	"securityfs": SystemFS,
	"kernfs":     SystemFS,

	// Virtual filesystems
	"fusectl":   VirtualFS,
	"fuse":      VirtualFS,
	"fuseblk":   VirtualFS,
	"overlay":   VirtualFS,
	"overlayfs": VirtualFS,

	// Temporary filesystems
	"tmpfs": TempFS,
	"ramfs": TempFS,

	// Special filesystems
	"devpts":      SpecialFS,
	"hugetlbfs":   SpecialFS,
	"mqueue":      SpecialFS,
	"cgroup":      SpecialFS,
	"cgroupfs":    SpecialFS,
	"cgroupfs2":   SpecialFS,
	"pstore":      SpecialFS,
	"binfmt_misc": SpecialFS,
	"bpf":         SpecialFS,
	"tracefs":     SpecialFS,
	"configfs":    SpecialFS,
	"autofs":      SpecialFS,
	"efivarfs":    SpecialFS,
	"rpc_pipefs":  SpecialFS,
}

// skipFilesystem returns true if the filesystem type should be skipped
func skipFilesystem(fstype string) bool {
	// Check if we have a category for this filesystem type
	if _, exists := fsTypeCategories[fstype]; exists {
		return true
	}

	// Additional checks for common patterns in filesystem types
	// that might indicate a virtual or system filesystem
	if len(fstype) >= 2 {
		// Check for common filesystem type prefixes
		commonPrefixes := []string{"fuse", "cgroup", "proc", "sys", "dev"}
		for _, prefix := range commonPrefixes {
			if len(fstype) >= len(prefix) && fstype[:len(prefix)] == prefix {
				return true
			}
		}
	}

	return false
}
