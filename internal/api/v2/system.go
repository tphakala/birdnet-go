// internal/api/v2/system.go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"log"

	"github.com/labstack/echo/v4"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
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

// ProcessInfo represents information about a running process
type ProcessInfo struct {
	PID    int32   `json:"pid"`
	Name   string  `json:"name"`
	Status string  `json:"status"` // e.g., "running", "sleeping", "zombie"
	CPU    float64 `json:"cpu"`    // CPU usage percentage
	Memory uint64  `json:"memory"` // Memory usage in bytes (RSS)
	Uptime int64   `json:"uptime"` // Uptime in seconds
}

// Use monotonic clock for start time
var startTime = time.Now()
var startMonotonicTime = time.Now() // This inherently includes monotonic clock reading

// CPUCache holds the cached CPU usage data
type CPUCache struct {
	mu          sync.RWMutex
	cpuPercent  []float64
	lastUpdated time.Time
}

// Global CPU cache instance
var cpuCache = &CPUCache{
	cpuPercent:  []float64{0}, // Initialize with 0 value
	lastUpdated: time.Now(),
}

// Store the cancel function for CPU monitoring to enable proper cleanup
var cpuMonitorCancel context.CancelFunc

// UpdateCPUCache updates the cached CPU usage data
func UpdateCPUCache(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			// Context canceled, exit the goroutine
			return
		default:
			// Get CPU usage (this will block for 1 second)
			percent, err := cpu.Percent(time.Second, false)
			if err == nil && len(percent) > 0 {
				// Update the cache
				cpuCache.mu.Lock()
				cpuCache.cpuPercent = percent
				cpuCache.lastUpdated = time.Now()
				cpuCache.mu.Unlock()
			}

			// Wait before next update (can be adjusted based on needs)
			// We add a small buffer to ensure we don't constantly block
			// Use time.After in a select to make it cancellable
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
				// Continue to next iteration
			}
		}
	}
}

// GetCachedCPUUsage returns the cached CPU usage
func GetCachedCPUUsage() []float64 {
	cpuCache.mu.RLock()
	defer cpuCache.mu.RUnlock()

	// Return a copy to avoid race conditions
	result := make([]float64, len(cpuCache.cpuPercent))
	copy(result, cpuCache.cpuPercent)
	return result
}

// JobQueueStats represents the job queue statistics
type JobQueueStats struct {
	Queue     map[string]interface{} `json:"queue"`
	Actions   map[string]interface{} `json:"actions"`
	Timestamp string                 `json:"timestamp"`
}

// GetJobQueueStats returns statistics about the job queue
func (c *Controller) GetJobQueueStats(ctx echo.Context) error {
	if c.apiLogger != nil {
		c.apiLogger.Info("Getting job queue statistics",
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// Get the processor from the context
	processorObj := ctx.Get("processor")
	if processorObj == nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Processor not available for job queue stats",
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, fmt.Errorf("processor not available"), "Processor not available", http.StatusInternalServerError)
	}

	// Get the processor with the correct type
	p, ok := processorObj.(*processor.Processor)
	if !ok {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid processor type for job queue stats",
				"actual_type", fmt.Sprintf("%T", processorObj),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, fmt.Errorf("invalid processor type"), "Invalid processor type", http.StatusInternalServerError)
	}

	// Check if job queue is available
	if p.JobQueue == nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Job queue not available",
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, fmt.Errorf("job queue not available"), "Job queue not available", http.StatusInternalServerError)
	}

	// Get job queue stats
	stats := p.JobQueue.GetStats()

	// Convert to JSON
	jsonStats, err := stats.ToJSON()
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to convert job queue stats to JSON",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err, "Failed to convert job queue stats to JSON", http.StatusInternalServerError)
	}

	// Parse the JSON string back to a map for proper JSON response
	var statsMap map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStats), &statsMap); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to parse job queue stats JSON",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err, "Failed to parse job queue stats JSON", http.StatusInternalServerError)
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Job queue statistics retrieved successfully",
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	return ctx.JSON(http.StatusOK, statsMap)
}

// Initialize system routes
func (c *Controller) initSystemRoutes() {
	if c.apiLogger != nil {
		c.apiLogger.Info("Initializing system routes")
	}

	// Start CPU usage monitoring in background with context for controlled shutdown
	ctx, cancel := context.WithCancel(context.Background())
	cpuMonitorCancel = cancel // Store for later cleanup
	go UpdateCPUCache(ctx)

	if c.apiLogger != nil {
		c.apiLogger.Info("Started CPU usage monitoring")
	}

	// Create system API group
	systemGroup := c.Group.Group("/system")

	// Determine which middleware to use for authentication
	var authMiddleware echo.MiddlewareFunc

	// Use the new auth middleware if available
	if c.AuthMiddlewareFn != nil {
		if c.apiLogger != nil {
			c.apiLogger.Info("Using new auth middleware for system routes")
		}
		authMiddleware = c.AuthMiddlewareFn
	} else {
		// Fall back to the legacy middleware
		if c.apiLogger != nil {
			c.apiLogger.Warn("New auth middleware not available, using legacy auth middleware")
		}
		authMiddleware = c.AuthMiddleware
	}

	// Create auth-protected group using the appropriate middleware
	protectedGroup := systemGroup.Group("", authMiddleware)

	// Add system routes (all protected)
	protectedGroup.GET("/info", c.GetSystemInfo)
	protectedGroup.GET("/resources", c.GetResourceInfo)
	protectedGroup.GET("/disks", c.GetDiskInfo)
	protectedGroup.GET("/jobs", c.GetJobQueueStats)
	protectedGroup.GET("/processes", c.GetProcessInfo)

	// Audio device routes (all protected)
	audioGroup := protectedGroup.Group("/audio")
	audioGroup.GET("/devices", c.GetAudioDevices)
	audioGroup.GET("/active", c.GetActiveAudioDevice)

	if c.apiLogger != nil {
		c.apiLogger.Info("System routes initialized successfully")
	}
}

// GetSystemInfo handles GET /api/v2/system/info
func (c *Controller) GetSystemInfo(ctx echo.Context) error {
	if c.apiLogger != nil {
		c.apiLogger.Info("Getting system information",
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// Get host info
	hostInfo, err := host.Info()
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get host information",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err, "Failed to get host information", http.StatusInternalServerError)
	}

	// Get hostname
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
		if c.apiLogger != nil {
			c.apiLogger.Warn("Failed to get hostname, using 'unknown'",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
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

	if c.apiLogger != nil {
		c.apiLogger.Info("System information retrieved successfully",
			"os", info.OS,
			"arch", info.Architecture,
			"hostname", info.Hostname,
			"platform", info.Platform,
			"uptime", info.UpTime,
			"app_uptime", info.AppUptime,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	return ctx.JSON(http.StatusOK, info)
}

// GetResourceInfo handles GET /api/v2/system/resources
func (c *Controller) GetResourceInfo(ctx echo.Context) error {
	if c.apiLogger != nil {
		c.apiLogger.Info("Getting system resource information",
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// Get memory statistics
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get memory information",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err, "Failed to get memory information", http.StatusInternalServerError)
	}

	// Get swap statistics
	swapInfo, err := mem.SwapMemory()
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get swap information",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err, "Failed to get swap information", http.StatusInternalServerError)
	}

	// Get CPU usage from cache instead of blocking
	cpuPercent := GetCachedCPUUsage()

	// Get process information (current process)
	proc, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get process information",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err, "Failed to get process information", http.StatusInternalServerError)
	}

	procMem, err := proc.MemoryInfo()
	if err != nil {
		c.Debug("Failed to get process memory info: %v", err)
		if c.apiLogger != nil {
			c.apiLogger.Warn("Failed to get process memory info",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		// Continue with nil procMem, handled below
	}

	procCPU, err := proc.CPUPercent()
	if err != nil {
		c.Debug("Failed to get process CPU info: %v", err)
		if c.apiLogger != nil {
			c.apiLogger.Warn("Failed to get process CPU info",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
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

	if c.apiLogger != nil {
		c.apiLogger.Info("System resource information retrieved successfully",
			"cpu_usage", resourceInfo.CPUUsage,
			"memory_usage", resourceInfo.MemoryUsage,
			"swap_usage", resourceInfo.SwapUsage,
			"process_mem_mb", resourceInfo.ProcessMem,
			"process_cpu", resourceInfo.ProcessCPU,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	return ctx.JSON(http.StatusOK, resourceInfo)
}

// GetDiskInfo handles GET /api/v2/system/disks
func (c *Controller) GetDiskInfo(ctx echo.Context) error {
	if c.apiLogger != nil {
		c.apiLogger.Info("Getting disk information",
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// Get partitions
	partitions, err := disk.Partitions(false)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get disk partitions",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err, "Failed to get disk partitions", http.StatusInternalServerError)
	}

	// Create slice to hold disk info
	disks := []DiskInfo{}

	// Try to get IO counters for all disks
	ioCounters, ioErr := disk.IOCounters()
	if ioErr != nil {
		c.Debug("Failed to get IO counters: %v", ioErr)
		if c.apiLogger != nil {
			c.apiLogger.Warn("Failed to get IO counters",
				"error", ioErr.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		// Continue without IO metrics
	}

	// Get host info for uptime calculation
	hostInfo, err := host.Info()
	var uptimeMs uint64 = 0
	if err != nil {
		c.Debug("Failed to get host information for uptime: %v", err)
		if c.apiLogger != nil {
			c.apiLogger.Warn("Failed to get host information for uptime calculation",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
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
			if c.apiLogger != nil {
				c.apiLogger.Warn("Failed to get disk usage",
					"mountpoint", partition.Mountpoint,
					"error", err.Error(),
					"path", ctx.Request().URL.Path,
					"ip", ctx.RealIP(),
				)
			}
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

	if c.apiLogger != nil {
		c.apiLogger.Info("Disk information retrieved successfully",
			"disk_count", len(disks),
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
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
	if c.apiLogger != nil {
		c.apiLogger.Info("Getting audio devices",
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// Get audio devices
	devices, err := myaudio.ListAudioSources()
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to list audio devices",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err, "Failed to list audio devices", http.StatusInternalServerError)
	}

	// Check if no devices were found
	if len(devices) == 0 {
		c.Debug("No audio devices found on the system")
		if c.apiLogger != nil {
			c.apiLogger.Warn("No audio devices found on the system",
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
				"os", runtime.GOOS,
			)
		}
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

	if c.apiLogger != nil {
		deviceNames := make([]string, len(devices))
		for i, device := range devices {
			deviceNames[i] = device.Name
		}

		c.apiLogger.Info("Audio devices retrieved successfully",
			"device_count", len(apiDevices),
			"devices", strings.Join(deviceNames, ", "),
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	return ctx.JSON(http.StatusOK, apiDevices)
}

// GetActiveAudioDevice handles GET /api/v2/system/audio/active
func (c *Controller) GetActiveAudioDevice(ctx echo.Context) error {
	if c.apiLogger != nil {
		c.apiLogger.Info("Getting active audio device",
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// Get active audio device from settings
	deviceName := c.Settings.Realtime.Audio.Source

	// Check if no device is configured
	if deviceName == "" {
		if c.apiLogger != nil {
			c.apiLogger.Info("No audio device currently active",
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
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
		c.Debug("%s", errorMsg)

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

		if c.apiLogger != nil {
			c.apiLogger.Warn("Failed to list audio devices for verification",
				"device_name", deviceName,
				"error", err.Error(),
				"os", runtime.GOOS,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
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

		if c.apiLogger != nil {
			c.apiLogger.Warn("Configured audio device not found on system",
				"configured_device", deviceName,
				"available_devices", strings.Join(availableDevices, ", "),
				"os", runtime.GOOS,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}

		return ctx.JSON(http.StatusOK, map[string]interface{}{
			"device":      activeDevice,
			"active":      true,
			"verified":    false,
			"message":     errorMsg,
			"diagnostics": diagnostics,
		})
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Active audio device verified",
			"device_name", deviceName,
			"device_id", activeDevice.ID,
			"sample_rate", activeDevice.SampleRate,
			"bit_depth", activeDevice.BitDepth,
			"channels", activeDevice.Channels,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// Device is configured and verified to exist
	return ctx.JSON(http.StatusOK, map[string]interface{}{
		"device":      activeDevice,
		"active":      true,
		"verified":    true,
		"diagnostics": diagnostics,
	})
}

// GetProcessInfo returns a list of running processes and their basic information
// It accepts an optional query parameter `?all=true` to show all processes.
// By default, it shows only the main application process and its direct children.
func (c *Controller) GetProcessInfo(ctx echo.Context) error {
	if c.apiLogger != nil {
		c.apiLogger.Info("Getting process information",
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
			"query", ctx.QueryString(),
		)
	}

	showAll := ctx.QueryParam("all") == "true"
	currentPID := int32(os.Getpid())

	procs, err := process.Processes()
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to list processes",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err, "Failed to list processes", http.StatusInternalServerError)
	}

	processInfos := make([]ProcessInfo, 0, len(procs))
	for _, p := range procs {
		// Filtering logic
		if !showAll {
			parentPID, _ := p.Ppid()
			if p.Pid != currentPID && parentPID != currentPID {
				// Skip if not the main process or a direct child
				continue
			}
		}

		name, err := p.Name()
		if err != nil {
			// Log error but continue, maybe process terminated?
			if c.apiLogger != nil {
				c.apiLogger.Warn("Failed to get process name", "pid", p.Pid, "error", err.Error())
			}
			continue
		}

		statusList, err := p.Status()
		var status string
		if err != nil {
			status = "unknown"
			if c.apiLogger != nil {
				c.apiLogger.Warn("Failed to get process status", "pid", p.Pid, "name", name, "error", err.Error())
			}
		} else if len(statusList) > 0 {
			// Use the first status code returned
			switch statusList[0] {
			case "R": // Running or Runnable (Linux/macOS)
				status = "running"
			case "S": // Sleeping (Linux/macOS)
				status = "sleeping"
			case "D": // Disk Sleep (Linux)
				status = "disk sleep"
			case "Z": // Zombie (Linux/macOS)
				status = "zombie"
			case "T": // Stopped (Linux/macOS)
				status = "stopped"
			case "W": // Paging (Linux)
				status = "paging"
			case "I": // Idle (macOS/FreeBSD)
				status = "idle"
			// Add more specific codes if needed, or default
			default:
				status = strings.ToLower(statusList[0]) // Use the code itself if not recognized
			}
		} else {
			status = "unknown"
		}

		cpuPercent, err := p.CPUPercent()
		if err != nil {
			// Log error but default to 0
			if c.apiLogger != nil {
				c.apiLogger.Warn("Failed to get process CPU percent", "pid", p.Pid, "name", name, "error", err.Error())
			}
			cpuPercent = 0.0
		}

		memInfo, err := p.MemoryInfo()
		var memRSS uint64
		if err != nil {
			// Log error but default to 0
			if c.apiLogger != nil {
				c.apiLogger.Warn("Failed to get process memory info", "pid", p.Pid, "name", name, "error", err.Error())
			}
			memRSS = 0
		} else {
			memRSS = memInfo.RSS // Resident Set Size
		}

		createTimeMillis, err := p.CreateTime()
		var uptimeSeconds int64
		if err != nil {
			// Log error but default to 0
			if c.apiLogger != nil {
				c.apiLogger.Warn("Failed to get process create time", "pid", p.Pid, "name", name, "error", err.Error())
			}
			uptimeSeconds = 0
		} else {
			// Calculate uptime relative to now
			uptimeSeconds = time.Now().Unix() - (createTimeMillis / 1000)
			if uptimeSeconds < 0 { // Sanity check for clock skew
				uptimeSeconds = 0
			}
		}

		processInfos = append(processInfos, ProcessInfo{
			PID:    p.Pid,
			Name:   name,
			Status: status,
			CPU:    cpuPercent,
			Memory: memRSS,
			Uptime: uptimeSeconds,
		})
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Process information retrieved successfully",
			"count", len(processInfos),
			"filter_applied", !showAll,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	return ctx.JSON(http.StatusOK, processInfos)
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

// StopCPUMonitoring stops the CPU monitoring goroutine by canceling its context.
// This function is called by the Controller.Shutdown method during application shutdown.
// It ensures that the background goroutine started by UpdateCPUCache is properly terminated
// to prevent resource leaks when the application exits.
//
// Note: This function is safe to call multiple times as it sets cpuMonitorCancel to nil
// after the first call.
func StopCPUMonitoring() {
	// Use a consistent logger for this function since it's static and may not have access to controller
	logger := log.Default()

	if cpuMonitorCancel != nil {
		logger.Println("Stopping CPU monitoring...")
		cpuMonitorCancel()
		cpuMonitorCancel = nil // Prevent double cancellation
		logger.Println("CPU monitoring stopped successfully")
	} else {
		logger.Println("CPU monitoring already stopped or never started")
	}
}
