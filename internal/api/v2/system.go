// internal/api/v2/system.go
package api

import (
	"net/http"
	"os"
	"runtime"
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

// AuthMiddleware middleware function for system routes that require authentication
func (c *Controller) AuthMiddleware(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		// Check if this is an API request with Authorization header (for Svelte UI)
		if ctx.Request().Header.Get("Authorization") != "" {
			// TODO: Implement proper token-based authentication when Svelte UI is developed
			// For now, we'll assume any request with Authorization header is authenticated
			return next(ctx)
		}

		// For browser/web UI requests, check for authenticated session
		authenticated := false

		// If authentication is enabled, check that it passes the requirements
		server := ctx.Get("server")
		if server != nil {
			// Try to use server's authentication methods
			if s, ok := server.(interface {
				IsAccessAllowed(c echo.Context) bool
				isAuthenticationEnabled(c echo.Context) bool
			}); ok {
				if !s.isAuthenticationEnabled(ctx) || s.IsAccessAllowed(ctx) {
					authenticated = true
				}
			}
		}

		if !authenticated {
			// Return JSON error for API calls
			return ctx.JSON(http.StatusUnauthorized, map[string]string{
				"error": "Authentication required",
			})
		}

		return next(ctx)
	}
}

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

	procMem, _ := proc.MemoryInfo()
	procCPU, _ := proc.CPUPercent()

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

	// Process each partition
	for _, partition := range partitions {
		// Skip special filesystems
		if skipFilesystem(partition.Fstype) {
			continue
		}

		// Get usage statistics
		usage, err := disk.Usage(partition.Mountpoint)
		if err != nil {
			c.Debug("Failed to get usage for %s: %v", partition.Mountpoint, err)
			continue
		}

		// Add disk info to response
		disks = append(disks, DiskInfo{
			Device:     partition.Device,
			Mountpoint: partition.Mountpoint,
			Fstype:     partition.Fstype,
			Total:      usage.Total,
			Used:       usage.Used,
			Free:       usage.Free,
			UsagePerc:  usage.UsedPercent,
		})
	}

	return ctx.JSON(http.StatusOK, disks)
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
			"active":  false,
			"message": "No audio device currently active",
		})
	}

	// Create response with default values
	activeDevice := ActiveAudioDevice{
		Name:       deviceName,
		SampleRate: 48000, // Standard BirdNET sample rate
		BitDepth:   16,    // Assuming 16-bit as per the capture.go implementation
		Channels:   1,     // Assuming mono as per the capture.go implementation
	}

	// Try to get additional device info and validate the device exists
	devices, err := myaudio.ListAudioSources()
	if err != nil {
		c.Debug("Failed to list audio devices: %v", err)
		// Still return the configured device, but note that we couldn't verify it exists
		return ctx.JSON(http.StatusOK, map[string]interface{}{
			"device":   activeDevice,
			"active":   true,
			"verified": false,
			"message":  "Device configured but could not verify if it exists",
		})
	}

	// Check if the configured device exists in the system
	deviceFound := false
	for _, device := range devices {
		if device.Name == deviceName {
			activeDevice.ID = device.ID
			deviceFound = true
			break
		}
	}

	if !deviceFound {
		// Device is configured but not found on the system
		return ctx.JSON(http.StatusOK, map[string]interface{}{
			"device":   activeDevice,
			"active":   true,
			"verified": false,
			"message":  "Configured audio device not found on the system",
		})
	}

	// Device is configured and verified to exist
	return ctx.JSON(http.StatusOK, map[string]interface{}{
		"device":   activeDevice,
		"active":   true,
		"verified": true,
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
