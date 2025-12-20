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
	"slices"
	"strconv"
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
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// System info constants (file-local)
const (
	cpuCacheUpdateInterval = 2 * time.Second // Interval for CPU cache updates
	bytesPerKB             = 1024            // Bytes per kilobyte
	maxPercentage          = 100             // Maximum percentage value
	defaultAudioSampleRate = 48000           // Standard BirdNET audio sample rate
	defaultAudioBitDepth   = 16              // Standard audio bit depth
	minRequiredElements    = 2               // Minimum required elements for various checks
)

// SystemInfo represents basic system information
type SystemInfo struct {
	Hostname      string    `json:"hostname"`
	PlatformVer   string    `json:"platform_version"`
	KernelVersion string    `json:"kernel_version"`
	UpTime        uint64    `json:"uptime_seconds"`
	BootTime      time.Time `json:"boot_time"`
	AppStart      time.Time `json:"app_start_time"`
	AppUptime     int64     `json:"app_uptime_seconds"`
	NumCPU        int       `json:"num_cpu"`
	SystemModel   string    `json:"system_model,omitempty"`
	TimeZone      string    `json:"time_zone,omitempty"`
	OSDisplay     string    `json:"os_display"`
	Architecture  string    `json:"architecture"`
}

// ResourceInfo represents system resource usage data
type ResourceInfo struct {
	CPUUsage        float64 `json:"cpu_usage_percent"`
	MemoryTotal     uint64  `json:"memory_total"`
	MemoryUsed      uint64  `json:"memory_used"`
	MemoryFree      uint64  `json:"memory_free"`
	MemoryAvailable uint64  `json:"memory_available"`
	MemoryBuffers   uint64  `json:"memory_buffers"`
	MemoryCached    uint64  `json:"memory_cached"`
	MemoryUsage     float64 `json:"memory_usage_percent"`
	SwapTotal       uint64  `json:"swap_total"`
	SwapUsed        uint64  `json:"swap_used"`
	SwapFree        uint64  `json:"swap_free"`
	SwapUsage       float64 `json:"swap_usage_percent"`
	ProcessMem      float64 `json:"process_memory_mb"`
	ProcessCPU      float64 `json:"process_cpu_percent"`
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

// SystemTemperature reports the CPU temperature, if available.
type SystemTemperature struct {
	Celsius       float64 `json:"celsius,omitempty"`        // CPU temperature in Celsius
	IsAvailable   bool    `json:"is_available"`             // True if temperature reading is available and valid
	SensorDetails string  `json:"sensor_details,omitempty"` // Details about the sensor used or why it's not available
	Message       string  `json:"message,omitempty"`        // Overall status message
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
			case <-time.After(cpuCacheUpdateInterval):
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
	Queue     map[string]any `json:"queue"`
	Actions   map[string]any `json:"actions"`
	Timestamp string         `json:"timestamp"`
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
	var statsMap map[string]any
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

	// Start CPU usage monitoring in background with controller's context for controlled shutdown
	// Go 1.25: Using WaitGroup.Go() for cleaner goroutine management
	c.wg.Go(func() {
		UpdateCPUCache(c.ctx)
	})

	if c.apiLogger != nil {
		c.apiLogger.Info("Started CPU usage monitoring")
	}

	// Create system API group
	systemGroup := c.Group.Group("/system")

	// Get the appropriate auth middleware
	authMiddleware := c.authMiddleware

	// Create auth-protected group using the appropriate middleware
	protectedGroup := systemGroup.Group("", authMiddleware)

	// Add system routes (all protected)
	protectedGroup.GET("/info", c.GetSystemInfo)
	protectedGroup.GET("/resources", c.GetResourceInfo)
	protectedGroup.GET("/disks", c.GetDiskInfo)
	protectedGroup.GET("/jobs", c.GetJobQueueStats)
	protectedGroup.GET("/processes", c.GetProcessInfo)
	protectedGroup.GET("/temperature/cpu", c.GetSystemCPUTemperature)

	// Audio device routes (all protected)
	audioGroup := protectedGroup.Group("/audio")
	audioGroup.GET("/devices", c.GetAudioDevices)
	audioGroup.GET("/active", c.GetActiveAudioDevice)
	audioGroup.GET("/equalizer/config", c.GetEqualizerConfig)

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
		hostname = ValueUnknown
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

	// Get System Model on Linux
	var systemModel string
	if runtime.GOOS == OSLinux {
		systemModel = getSystemModelFromProc()
		if systemModel == "" && c.apiLogger != nil {
			c.apiLogger.Debug("Could not determine system model from /proc/cpuinfo",
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
	}

	// Get system time zone
	loc := time.Now().Location()
	timeZoneStr := loc.String()

	if timeZoneStr == "Local" || timeZoneStr == "" {
		// Fallback if Olson name is "Local" or empty
		name, offset := time.Now().Zone() // Get abbreviation and offset in seconds
		offsetHours := offset / SecondsPerHour
		offsetMinutes := (offset % SecondsPerHour) / SecondsPerMinute
		if offsetMinutes < 0 { // Ensure minutes are positive for formatting
			offsetMinutes = -offsetMinutes
		}
		// If name is "Local" or empty, just use UTC offset. Otherwise, include the name.
		if name == "Local" || name == "" || name == "UTC" { // Also handle plain "UTC" from Zone()
			timeZoneStr = fmt.Sprintf("UTC%+03d:%02d", offsetHours, offsetMinutes)
		} else {
			timeZoneStr = fmt.Sprintf("%s (UTC%+03d:%02d)", name, offsetHours, offsetMinutes)
		}
	}

	// Construct OSDisplay string
	var osDisplay string
	tcaser := cases.Title(language.Und, cases.NoLower)
	platformName := tcaser.String(hostInfo.Platform)

	switch runtime.GOOS {
	case OSLinux:
		if platformName != "" {
			osDisplay = fmt.Sprintf("%s Linux", platformName)
		} else {
			osDisplay = "Linux"
		}
	case OSWindows:
		osDisplay = "Microsoft Windows"
	case OSDarwin:
		osDisplay = "Apple macOS" // More user-friendly than Darwin
	default:
		if platformName != "" {
			osDisplay = fmt.Sprintf("%s (%s)", platformName, runtime.GOOS)
		} else {
			osDisplay = tcaser.String(runtime.GOOS)
		}
	}

	// Create response
	info := SystemInfo{
		Architecture:  runtime.GOARCH,
		Hostname:      hostname,
		PlatformVer:   hostInfo.PlatformVersion,
		KernelVersion: hostInfo.KernelVersion,
		UpTime:        hostInfo.Uptime,
		BootTime:      time.Unix(int64(hostInfo.BootTime), 0), // #nosec G115 -- BootTime from system APIs, safe conversion for timestamp
		AppStart:      startTime,
		AppUptime:     appUptime,
		NumCPU:        runtime.NumCPU(),
		SystemModel:   systemModel,
		TimeZone:      timeZoneStr,
		OSDisplay:     osDisplay,
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("System information retrieved successfully",
			"os_display", info.OSDisplay,
			"arch", info.Architecture,
			"hostname", info.Hostname,
			"uptime", info.UpTime,
			"app_uptime", info.AppUptime,
			"timezone", info.TimeZone,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	return ctx.JSON(http.StatusOK, info)
}

// Helper function to read system model from /proc/cpuinfo on Linux
// It assumes the relevant "Model" line is the last one found.
func getSystemModelFromProc() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		log.Printf("Warning: Could not read /proc/cpuinfo: %v", err)
		return ""
	}

	systemModel := ""
	lines := strings.SplitSeq(string(data), "\n")
	for line := range lines {
		// Look specifically for lines starting with "Model" (case-sensitive)
		if strings.HasPrefix(line, "Model") {
			parts := strings.SplitN(line, ":", minRequiredElements)
			if len(parts) == minRequiredElements {
				model := strings.TrimSpace(parts[1])
				if model != "" {
					systemModel = model // Keep overwriting, last one wins
				}
			}
		}
	}
	return systemModel
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
	proc, err := process.NewProcess(int32(os.Getpid())) // #nosec G115 -- PID conversion safe, PIDs are within int32 range
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
		procMemMB = float64(procMem.RSS) / bytesPerKB / bytesPerKB
	}

	// Create response
	resourceInfo := ResourceInfo{
		MemoryTotal:     memInfo.Total,
		MemoryUsed:      memInfo.Used,
		MemoryFree:      memInfo.Free,
		MemoryAvailable: memInfo.Available,
		MemoryBuffers:   memInfo.Buffers,
		MemoryCached:    memInfo.Cached,
		MemoryUsage:     memInfo.UsedPercent,
		SwapTotal:       swapInfo.Total,
		SwapUsed:        swapInfo.Used,
		SwapFree:        swapInfo.Free,
		SwapUsage:       swapInfo.UsedPercent,
		ProcessMem:      procMemMB,
		ProcessCPU:      procCPU,
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
		uptimeMs = hostInfo.Uptime * MillisecondsPerSecond
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
				diskInfo.IOBusyPerc = float64(counter.IoTime) / float64(uptimeMs) * maxPercentage

				// Cap at 100% (in case of measurement anomalies)
				if diskInfo.IOBusyPerc > maxPercentage {
					diskInfo.IOBusyPerc = maxPercentage
				}
			} else if counter.ReadTime > 0 || counter.WriteTime > 0 {
				// Alternative calculation using read/write times if IoTime is not available
				// This is less accurate but provides a reasonable approximation
				totalIOTime := counter.ReadTime + counter.WriteTime
				if uptimeMs > 0 {
					diskInfo.IOBusyPerc = float64(totalIOTime) / float64(uptimeMs) * maxPercentage

					// Cap at 100%
					if diskInfo.IOBusyPerc > maxPercentage {
						diskInfo.IOBusyPerc = maxPercentage
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
	return slices.Contains(opts, "ro")
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
		return ctx.JSON(http.StatusOK, map[string]any{
			"device":   nil,
			"active":   false,
			"verified": false,
			"message":  "No audio device currently active",
		})
	}

	// Create response with default values
	activeDevice := ActiveAudioDevice{
		Name:       deviceName,
		SampleRate: defaultAudioSampleRate, // Standard BirdNET sample rate
		BitDepth:   defaultAudioBitDepth,   // Assuming 16-bit as per the capture.go implementation
		Channels:   1,     // Assuming mono as per the capture.go implementation
	}

	// Diagnostic information map
	diagnostics := map[string]any{
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
		case OSWindows:
			diagnostics["note"] = "On Windows, check that audio drivers are properly installed and the device is not disabled in Sound settings"
		case OSDarwin:
			diagnostics["note"] = "On macOS, check System Preferences > Sound and ensure the device has proper permissions"
		case OSLinux:
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
		return ctx.JSON(http.StatusOK, map[string]any{
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

		return ctx.JSON(http.StatusOK, map[string]any{
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
	return ctx.JSON(http.StatusOK, map[string]any{
		"device":      activeDevice,
		"active":      true,
		"verified":    true,
		"diagnostics": diagnostics,
	})
}

// getSingleProcessInfo retrieves detailed information for a single process.
// It handles errors gracefully and returns a ProcessInfo struct.
func (c *Controller) getSingleProcessInfo(p *process.Process) (ProcessInfo, error) {
	name, err := p.Name()
	if err != nil {
		// Log error but continue, maybe process terminated?
		if c.apiLogger != nil {
			c.apiLogger.Warn("Failed to get process name", "pid", p.Pid, "error", err.Error())
		}
		// Return an error to indicate this process couldn't be fully processed
		return ProcessInfo{}, fmt.Errorf("failed to get process name for pid %d: %w", p.Pid, err)
	}

	statusList, err := p.Status()
	var status string
	switch {
	case err != nil:
		status = ValueUnknown
		if c.apiLogger != nil {
			c.apiLogger.Warn("Failed to get process status", "pid", p.Pid, "name", name, "error", err.Error())
		}
	case len(statusList) > 0:
		// Use the first status code returned
		status = mapProcessStatus(statusList[0])
	default:
		status = ValueUnknown
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
		uptimeSeconds = max(time.Now().Unix()-(createTimeMillis/MillisecondsPerSecond),
			// Sanity check for clock skew
			0)
	}

	return ProcessInfo{
		PID:    p.Pid,
		Name:   name,
		Status: status,
		CPU:    cpuPercent,
		Memory: memRSS,
		Uptime: uptimeSeconds,
	}, nil
}

// mapProcessStatus converts OS-specific process status codes to readable strings.
func mapProcessStatus(statusCode string) string {
	switch statusCode {
	case "R": // Running or Runnable (Linux/macOS)
		return "running"
	case "S": // Sleeping (Linux/macOS)
		return "sleeping"
	case "D": // Disk Sleep (Linux)
		return "disk sleep"
	case "Z": // Zombie (Linux/macOS)
		return "zombie"
	case "T": // Stopped (Linux/macOS)
		return "stopped"
	case "W": // Paging (Linux)
		return "paging"
	case "I": // Idle (macOS/FreeBSD)
		return "idle"
	// Add more specific codes if needed, or default
	default:
		return strings.ToLower(statusCode) // Use the code itself if not recognized
	}
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
	currentPID := int32(os.Getpid()) // #nosec G115 -- PID conversion safe, PIDs are within int32 range

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
			parentPID, err := p.Ppid()
			if err != nil {
				// Log error and skip this process if PPID can't be determined
				if c.apiLogger != nil {
					c.apiLogger.Warn("Failed to get parent PID, skipping process", "pid", p.Pid, "error", err.Error())
				}
				continue
			}
			if p.Pid != currentPID && parentPID != currentPID {
				// Skip if not the main process or a direct child
				continue
			}
		}

		// Get info for this process using the helper
		info, err := c.getSingleProcessInfo(p)
		if err != nil {
			// Log the error from getSingleProcessInfo (already logged specifics inside)
			if c.apiLogger != nil {
				c.apiLogger.Warn("Skipping process due to error retrieving details", "pid", p.Pid, "error", err.Error())
			}
			continue // Skip this process if we couldn't get full details
		}

		processInfos = append(processInfos, info)
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

// checkThermalZone attempts to read and validate the temperature from a specific thermal zone.
// It returns the Celsius temperature, details about the sensor/error, whether it was valid, and any critical error.
func (c *Controller) checkThermalZone(zonePath string, targetTypes map[string]bool) (celsius float64, details string, isValid bool, err error) {
	zoneName := filepath.Base(zonePath)
	typePath := filepath.Join(zonePath, "type")

	typeData, err := os.ReadFile(typePath)
	if err != nil {
		// Not a critical error for the overall request, just skip this zone.
		if c.apiLogger != nil {
			c.apiLogger.Debug("Failed to read type file for zone", "zone", zoneName, "type_path", typePath, "error", err.Error())
		}
		return 0, fmt.Sprintf("Failed to read type for %s", zoneName), false, nil
	}

	sensorType := strings.ToLower(strings.TrimSpace(string(typeData)))

	if _, isTarget := targetTypes[sensorType]; !isTarget {
		// Not a target sensor type, skip silently.
		return 0, "", false, nil
	}

	// It's a target type, now try to read and validate the temperature.
	tempFilePath := filepath.Join(zonePath, "temp")
	tempData, err := os.ReadFile(tempFilePath)
	if err != nil {
		details = fmt.Sprintf("Error reading temp from %s (type: %s)", zoneName, sensorType)
		if c.apiLogger != nil {
			c.apiLogger.Warn(details, "temp_path", tempFilePath, "error", err.Error())
		}
		return 0, details, false, nil // Error reading temp, but might find another valid zone.
	}

	tempStr := strings.TrimSpace(string(tempData))
	tempMillCelsius, err := strconv.Atoi(tempStr)
	if err != nil {
		details = fmt.Sprintf("Error parsing temp from %s (type: %s, value: '%s')", zoneName, sensorType, tempStr)
		if c.apiLogger != nil {
			c.apiLogger.Warn(details, "error", err.Error())
		}
		return 0, details, false, nil // Error parsing temp.
	}

	celsius = float64(tempMillCelsius) / float64(MillisecondsPerSecond)

	// Validate temperature range (0 to 100 °C inclusive)
	if celsius < 0.0 || celsius > 100.0 {
		details = fmt.Sprintf("Invalid temp from %s (type: %s, value: %.1f°C, expected 0-100°C)", zoneName, sensorType, celsius)
		if c.apiLogger != nil {
			c.apiLogger.Warn("Temperature reading out of valid range", "details", details)
		}
		return 0, details, false, nil // Temp out of range.
	}

	// Valid temperature found
	details = fmt.Sprintf("Source: %s, Type: %s", zoneName, sensorType)
	return celsius, details, true, nil
}

// GetSystemCPUTemperature handles GET /api/v2/system/temperature/cpu
// It attempts to read the CPU temperature by scanning /sys/class/thermal/thermal_zone*
// for specific types like 'cpu-thermal' or 'x86_pkg_temp'.
// It validates the temperature to be within a reasonable range (0-100°C).
func (c *Controller) GetSystemCPUTemperature(ctx echo.Context) error {
	if c.apiLogger != nil {
		c.apiLogger.Info("Getting system CPU temperature",
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	response := SystemTemperature{
		IsAvailable: false,
		Message:     "No suitable CPU temperature sensor found or temperature out of valid range.", // Default message
	}

	// Base path for thermal zones
	const thermalBasePath = "/sys/class/thermal/"
	// Target sensor types for CPU temperature
	targetTypes := map[string]bool{
		"cpu-thermal":     true, // Common on Raspberry Pi
		"x86_pkg_temp":    true, // Common on Intel x86 systems (like NUC)
		"soc_thermal":     true, // Common on some ARM SoCs
		"cpu_thermal":     true, // Alternative name
		"thermal-fan-est": true, // Seen on some systems
	}

	// Check if the base thermal directory exists (quick check for non-Linux/unsupported)
	if _, err := os.Stat(thermalBasePath); err != nil {
		if os.IsNotExist(err) {
			response.Message = "Thermal zone directory not found. This feature is typically available on Linux systems."
			if c.apiLogger != nil {
				c.apiLogger.Info("Thermal zone directory not found, CPU temperature feature unavailable.",
					"path", thermalBasePath, "os", runtime.GOOS,
					"request_path", ctx.Request().URL.Path, "ip", ctx.RealIP())
			}
			return ctx.JSON(http.StatusOK, response)
		} else {
			// Other filesystem error (e.g., permissions)
			if c.apiLogger != nil {
				c.apiLogger.Error("Failed to stat thermal base path",
					"path", thermalBasePath, "error", err.Error(),
					"request_path", ctx.Request().URL.Path, "ip", ctx.RealIP())
			}
			return c.HandleError(ctx, err, "Failed to access thermal information due to filesystem error", http.StatusInternalServerError)
		}
	}

	zones, err := filepath.Glob(filepath.Join(thermalBasePath, "thermal_zone*"))
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to glob for thermal zones",
				"base_path", thermalBasePath, "error", err.Error(),
				"request_path", ctx.Request().URL.Path, "ip", ctx.RealIP())
		}
		return c.HandleError(ctx, err, "Error scanning for thermal zones", http.StatusInternalServerError)
	}

	if len(zones) == 0 {
		response.Message = "No thermal zones found. This feature is typically available on Linux systems."
		if c.apiLogger != nil {
			c.apiLogger.Info("No thermal zones found via Glob.",
				"pattern", filepath.Join(thermalBasePath, "thermal_zone*"), "os", runtime.GOOS,
				"request_path", ctx.Request().URL.Path, "ip", ctx.RealIP())
		}
		return ctx.JSON(http.StatusOK, response)
	}

	var lastAttemptDetails string
	foundValid := false

	for _, zonePath := range zones {
		celsius, details, isValid, err := c.checkThermalZone(zonePath, targetTypes)
		// We don't expect critical errors from checkThermalZone currently, but check just in case.
		if err != nil {
			// Log unexpected critical error from helper
			if c.apiLogger != nil {
				c.apiLogger.Error("Unexpected error checking thermal zone", "zone", zonePath, "error", err.Error())
			}
			continue // Skip this zone on critical error
		}

		if isValid {
			response.Celsius = celsius
			response.IsAvailable = true
			response.SensorDetails = details
			response.Message = "CPU temperature retrieved successfully."
			foundValid = true
			if c.apiLogger != nil {
				c.apiLogger.Info("CPU temperature retrieved successfully",
					"temperature_celsius", response.Celsius,
					"sensor_details", response.SensorDetails,
					"request_path", ctx.Request().URL.Path, "ip", ctx.RealIP())
			}
			break // Found the first valid sensor, stop searching.
		}

		// If it wasn't valid, but details were returned (meaning it was a target sensor with an issue),
		// store the details of the last failed attempt on a target sensor.
		if details != "" {
			lastAttemptDetails = details
		}
	}

	// If loop completes and no valid sensor was found.
	if !foundValid {
		response.SensorDetails = lastAttemptDetails // Show details of the last failed attempt if any
		if lastAttemptDetails != "" {
			// A target sensor was found but had issues (read error, parse error, out of range)
			response.Message = fmt.Sprintf("A targeted CPU sensor was found but could not be read successfully or value was invalid. Last attempt details: %s", lastAttemptDetails)
		} else {
			// No target sensors were found at all, or they were skipped due to non-critical errors before validation.
			response.Message = "No targeted CPU temperature sensor types (e.g., cpu-thermal, x86_pkg_temp) found or readable in available thermal zones."
		}

		if c.apiLogger != nil {
			c.apiLogger.Info("Could not retrieve a valid CPU temperature after checking all zones.",
				"final_message", response.Message, "sensor_details_attempted", response.SensorDetails,
				"request_path", ctx.Request().URL.Path, "ip", ctx.RealIP())
		}
	}

	return ctx.JSON(http.StatusOK, response)
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
	if len(fstype) >= minRequiredElements {
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

// GetEqualizerConfig handles GET /api/v2/system/audio/equalizer/config
func (c *Controller) GetEqualizerConfig(ctx echo.Context) error {
	if c.apiLogger != nil {
		c.apiLogger.Info("Getting equalizer filter configuration",
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// Set cache headers for static configuration data
	ctx.Response().Header().Set("Cache-Control", "public, max-age=3600")

	// Return the equalizer filter configuration
	return ctx.JSON(http.StatusOK, conf.EqFilterConfig)
}
