// internal/api/v2/system.go
package api

import (
	"context"
	"encoding/json"
	"errors"
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

	"github.com/labstack/echo/v4"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
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
	c.logInfoIfEnabled("Getting job queue statistics",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Get the processor from the context
	processorObj := ctx.Get("processor")
	if processorObj == nil {
		c.logErrorIfEnabled("Processor not available for job queue stats",
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, fmt.Errorf("processor not available"), "Processor not available", http.StatusInternalServerError)
	}

	// Get the processor with the correct type
	p, ok := processorObj.(*processor.Processor)
	if !ok {
		c.logErrorIfEnabled("Invalid processor type for job queue stats",
			logger.String("actual_type", fmt.Sprintf("%T", processorObj)),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, fmt.Errorf("invalid processor type"), "Invalid processor type", http.StatusInternalServerError)
	}

	// Check if job queue is available
	if p.JobQueue == nil {
		c.logErrorIfEnabled("Job queue not available",
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, fmt.Errorf("job queue not available"), "Job queue not available", http.StatusInternalServerError)
	}

	// Get job queue stats
	stats := p.JobQueue.GetStats()

	// Convert to JSON
	jsonStats, err := stats.ToJSON()
	if err != nil {
		c.logErrorIfEnabled("Failed to convert job queue stats to JSON",
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Failed to convert job queue stats to JSON", http.StatusInternalServerError)
	}

	// Parse the JSON string back to a map for proper JSON response
	var statsMap map[string]any
	if err := json.Unmarshal([]byte(jsonStats), &statsMap); err != nil {
		c.logErrorIfEnabled("Failed to parse job queue stats JSON",
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Failed to parse job queue stats JSON", http.StatusInternalServerError)
	}

	c.logInfoIfEnabled("Job queue statistics retrieved successfully",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, statsMap)
}

// Initialize system routes
func (c *Controller) initSystemRoutes() {
	c.logInfoIfEnabled("Initializing system routes")

	// Start CPU usage monitoring in background with controller's context for controlled shutdown
	// Go 1.25: Using WaitGroup.Go() for cleaner goroutine management
	c.wg.Go(func() {
		UpdateCPUCache(c.ctx)
	})

	c.logInfoIfEnabled("Started CPU usage monitoring")

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
	protectedGroup.GET("/database/stats", c.GetDatabaseStats)
	protectedGroup.GET("/database/v2/stats", c.GetV2DatabaseStats)
	protectedGroup.POST("/database/backup", c.DownloadDatabaseBackup)

	// Audio device routes (all protected)
	audioGroup := protectedGroup.Group("/audio")
	audioGroup.GET("/devices", c.GetAudioDevices)
	audioGroup.GET("/active", c.GetActiveAudioDevice)
	audioGroup.GET("/equalizer/config", c.GetEqualizerConfig)

	// Initialize migration routes
	c.initMigrationRoutes()

	c.logInfoIfEnabled("System routes initialized successfully")
}

// GetSystemInfo handles GET /api/v2/system/info
func (c *Controller) GetSystemInfo(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.logInfoIfEnabled("Getting system information", logger.String("path", path), logger.String("ip", ip))

	hostInfo, err := host.Info()
	if err != nil {
		c.logErrorIfEnabled("Failed to get host information", logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Failed to get host information", http.StatusInternalServerError)
	}

	hostname := c.getHostnameWithFallback(ip, path)
	systemModel := c.getSystemModelWithLogging(ip, path)

	info := SystemInfo{
		Architecture:  runtime.GOARCH,
		Hostname:      hostname,
		PlatformVer:   hostInfo.PlatformVersion,
		KernelVersion: hostInfo.KernelVersion,
		UpTime:        hostInfo.Uptime,
		BootTime:      time.Unix(int64(hostInfo.BootTime), 0), // #nosec G115 -- BootTime from system APIs, safe conversion for timestamp
		AppStart:      startTime,
		AppUptime:     int64(time.Since(startMonotonicTime).Seconds()),
		NumCPU:        runtime.NumCPU(),
		SystemModel:   systemModel,
		TimeZone:      getTimeZoneString(),
		OSDisplay:     getOSDisplayString(hostInfo.Platform),
	}

	c.logInfoIfEnabled("System information retrieved successfully", logger.String("os_display", info.OSDisplay), logger.String("arch", info.Architecture), logger.String("hostname", info.Hostname), logger.Any("uptime", info.UpTime), logger.Int64("app_uptime", info.AppUptime), logger.String("timezone", info.TimeZone), logger.String("path", path), logger.String("ip", ip))

	return ctx.JSON(http.StatusOK, info)
}

// getHostnameWithFallback gets hostname or returns "unknown" on error
func (c *Controller) getHostnameWithFallback(ip, path string) string {
	hostname, err := os.Hostname()
	if err != nil {
		c.logWarnIfEnabled("Failed to get hostname, using 'unknown'", logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return ValueUnknown
	}
	return hostname
}

// getSystemModelWithLogging gets system model on Linux with logging
func (c *Controller) getSystemModelWithLogging(ip, path string) string {
	if runtime.GOOS != OSLinux {
		return ""
	}
	systemModel := getSystemModelFromProc()
	if systemModel == "" {
		c.logDebugIfEnabled("Could not determine system model from /proc/cpuinfo", logger.String("path", path), logger.String("ip", ip))
	}
	return systemModel
}

// getTimeZoneString returns formatted timezone string
func getTimeZoneString() string {
	loc := time.Now().Location()
	timeZoneStr := loc.String()

	if timeZoneStr != "Local" && timeZoneStr != "" {
		return timeZoneStr
	}

	// Fallback if Olson name is "Local" or empty
	name, offset := time.Now().Zone()
	offsetHours := offset / SecondsPerHour
	offsetMinutes := (offset % SecondsPerHour) / SecondsPerMinute
	if offsetMinutes < 0 {
		offsetMinutes = -offsetMinutes
	}

	if name == "Local" || name == "" || name == "UTC" {
		return fmt.Sprintf("UTC%+03d:%02d", offsetHours, offsetMinutes)
	}
	return fmt.Sprintf("%s (UTC%+03d:%02d)", name, offsetHours, offsetMinutes)
}

// getOSDisplayString returns user-friendly OS display string
func getOSDisplayString(platform string) string {
	tcaser := cases.Title(language.Und, cases.NoLower)
	platformName := tcaser.String(platform)

	switch runtime.GOOS {
	case OSLinux:
		if platformName != "" {
			return fmt.Sprintf("%s Linux", platformName)
		}
		return "Linux"
	case OSWindows:
		return "Microsoft Windows"
	case OSDarwin:
		return "Apple macOS"
	default:
		if platformName != "" {
			return fmt.Sprintf("%s (%s)", platformName, runtime.GOOS)
		}
		return tcaser.String(runtime.GOOS)
	}
}

// Helper function to read system model from /proc/cpuinfo on Linux
// It assumes the relevant "Model" line is the last one found.
func getSystemModelFromProc() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		GetLogger().Warn("Could not read /proc/cpuinfo", logger.Error(err))
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
	c.logInfoIfEnabled("Getting system resource information",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Get memory statistics
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		c.logErrorIfEnabled("Failed to get memory information",
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Failed to get memory information", http.StatusInternalServerError)
	}

	// Get swap statistics
	swapInfo, err := mem.SwapMemory()
	if err != nil {
		c.logErrorIfEnabled("Failed to get swap information",
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Failed to get swap information", http.StatusInternalServerError)
	}

	// Get CPU usage from cache instead of blocking
	cpuPercent := GetCachedCPUUsage()

	// Get process information (current process)
	proc, err := process.NewProcess(int32(os.Getpid())) // #nosec G115 -- PID conversion safe, PIDs are within int32 range
	if err != nil {
		c.logErrorIfEnabled("Failed to get process information",
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Failed to get process information", http.StatusInternalServerError)
	}

	procMem, err := proc.MemoryInfo()
	if err != nil {
		c.Debug("Failed to get process memory info: %v", err)
		c.logWarnIfEnabled("Failed to get process memory info",
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		// Continue with nil procMem, handled below
	}

	procCPU, err := proc.CPUPercent()
	if err != nil {
		c.Debug("Failed to get process CPU info: %v", err)
		c.logWarnIfEnabled("Failed to get process CPU info",
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
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

	c.logInfoIfEnabled("System resource information retrieved successfully",
		logger.Float64("cpu_usage", resourceInfo.CPUUsage),
		logger.Float64("memory_usage", resourceInfo.MemoryUsage),
		logger.Float64("swap_usage", resourceInfo.SwapUsage),
		logger.Float64("process_mem_mb", resourceInfo.ProcessMem),
		logger.Float64("process_cpu", resourceInfo.ProcessCPU),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, resourceInfo)
}

// GetDiskInfo handles GET /api/v2/system/disks
func (c *Controller) GetDiskInfo(ctx echo.Context) error {
	c.logAPIRequest(ctx, logger.LogLevelInfo, "Getting disk information")

	partitions, err := disk.Partitions(false)
	if err != nil {
		c.logAPIRequest(ctx, logger.LogLevelError, "Failed to get disk partitions",
			logger.Error(err))
		return c.HandleError(ctx, err, "Failed to get disk partitions", http.StatusInternalServerError)
	}

	ioCounters, err := disk.IOCounters()
	if err != nil {
		c.logAPIRequest(ctx, logger.LogLevelWarn, "Failed to get IO counters, continuing without IO metrics",
			logger.Error(err))
	}
	uptimeMs := c.getUptimeMs()

	disks := make([]DiskInfo, 0, len(partitions))
	for _, partition := range partitions {
		if skipFilesystem(partition.Fstype) {
			continue
		}
		diskInfo := c.buildDiskInfo(partition, ioCounters, uptimeMs)
		disks = append(disks, diskInfo)
	}

	c.logAPIRequest(ctx, logger.LogLevelInfo, "Disk information retrieved successfully",
		logger.Int("disk_count", len(disks)))
	return ctx.JSON(http.StatusOK, disks)
}

// getUptimeMs returns system uptime in milliseconds
func (c *Controller) getUptimeMs() uint64 {
	hostInfo, err := host.Info()
	if err != nil {
		c.Debug("Failed to get host information for uptime: %v", err)
		return 0
	}
	return hostInfo.Uptime * MillisecondsPerSecond
}

// buildDiskInfo creates a DiskInfo struct from partition data
func (c *Controller) buildDiskInfo(partition disk.PartitionStat, ioCounters map[string]disk.IOCountersStat, uptimeMs uint64) DiskInfo {
	diskInfo := DiskInfo{
		Device:     partition.Device,
		Mountpoint: partition.Mountpoint,
		Fstype:     partition.Fstype,
		IsRemote:   isRemoteFilesystem(partition.Fstype),
		IsReadOnly: isReadOnlyMount(partition.Opts),
	}

	c.populateDiskUsage(&diskInfo, partition.Mountpoint)
	c.populateIOMetrics(&diskInfo, partition.Device, ioCounters, uptimeMs)

	return diskInfo
}

// populateDiskUsage adds usage statistics to disk info
func (c *Controller) populateDiskUsage(info *DiskInfo, mountpoint string) {
	usage, err := disk.Usage(mountpoint)
	if err != nil {
		c.Debug("Failed to get usage for %s: %v", mountpoint, err)
		return
	}

	info.Total = usage.Total
	info.Used = usage.Used
	info.Free = usage.Free
	info.UsagePerc = usage.UsedPercent

	if usage.InodesTotal > 0 {
		info.InodesTotal = usage.InodesTotal
		info.InodesUsed = usage.InodesUsed
		info.InodesFree = usage.InodesFree
		info.InodesUsagePerc = usage.InodesUsedPercent
	}
}

// populateIOMetrics adds IO metrics to disk info
func (c *Controller) populateIOMetrics(info *DiskInfo, device string, ioCounters map[string]disk.IOCountersStat, uptimeMs uint64) {
	deviceName := getDeviceBaseName(device)
	counter, exists := ioCounters[deviceName]
	if !exists {
		return
	}

	info.ReadBytes = counter.ReadBytes
	info.WriteBytes = counter.WriteBytes
	info.ReadCount = counter.ReadCount
	info.WriteCount = counter.WriteCount
	info.ReadTime = counter.ReadTime
	info.WriteTime = counter.WriteTime
	info.IOTime = counter.IoTime
	info.IOBusyPerc = c.calculateIOBusyPerc(&counter, uptimeMs)
}

// calculateIOBusyPerc calculates IO busy percentage
func (c *Controller) calculateIOBusyPerc(counter *disk.IOCountersStat, uptimeMs uint64) float64 {
	if uptimeMs == 0 {
		return 0
	}

	var ioTime uint64
	switch {
	case counter.IoTime > 0:
		ioTime = counter.IoTime
	case counter.ReadTime > 0 || counter.WriteTime > 0:
		ioTime = counter.ReadTime + counter.WriteTime
	default:
		return 0
	}

	busyPerc := float64(ioTime) / float64(uptimeMs) * maxPercentage
	if busyPerc > maxPercentage {
		return maxPercentage
	}
	return busyPerc
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
	c.logInfoIfEnabled("Getting audio devices",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Get audio devices
	devices, err := myaudio.ListAudioSources()
	if err != nil {
		c.logErrorIfEnabled("Failed to list audio devices",
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Failed to list audio devices", http.StatusInternalServerError)
	}

	// Check if no devices were found
	if len(devices) == 0 {
		c.Debug("No audio devices found on the system")
		c.logWarnIfEnabled("No audio devices found on the system",
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
			logger.String("os", runtime.GOOS),
		)
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

	deviceNames := make([]string, len(devices))
	for i, device := range devices {
		deviceNames[i] = device.Name
	}

	c.logInfoIfEnabled("Audio devices retrieved successfully",
		logger.Int("device_count", len(apiDevices)),
		logger.String("devices", strings.Join(deviceNames, ", ")),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, apiDevices)
}

// GetActiveAudioDevice handles GET /api/v2/system/audio/active
func (c *Controller) GetActiveAudioDevice(ctx echo.Context) error {
	c.logInfoIfEnabled("Getting active audio device",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Get active audio device from settings
	deviceName := c.Settings.Realtime.Audio.Source

	// Check if no device is configured
	if deviceName == "" {
		c.logInfoIfEnabled("No audio device currently active",
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
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
		Channels:   1,                      // Assuming mono as per the capture.go implementation
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

		c.logWarnIfEnabled("Failed to list audio devices for verification",
			logger.String("device_name", deviceName),
			logger.Error(err),
			logger.String("os", runtime.GOOS),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)

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

		c.logWarnIfEnabled("Configured audio device not found on system",
			logger.String("configured_device", deviceName),
			logger.String("available_devices", strings.Join(availableDevices, ", ")),
			logger.String("os", runtime.GOOS),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)

		return ctx.JSON(http.StatusOK, map[string]any{
			"device":      activeDevice,
			"active":      true,
			"verified":    false,
			"message":     errorMsg,
			"diagnostics": diagnostics,
		})
	}

	c.logInfoIfEnabled("Active audio device verified",
		logger.String("device_name", deviceName),
		logger.String("device_id", activeDevice.ID),
		logger.Int("sample_rate", activeDevice.SampleRate),
		logger.Int("bit_depth", activeDevice.BitDepth),
		logger.Int("channels", activeDevice.Channels),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

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
		c.logWarnIfEnabled("Failed to get process name", logger.Any("pid", p.Pid), logger.Error(err))
		// Return an error to indicate this process couldn't be fully processed
		return ProcessInfo{}, fmt.Errorf("failed to get process name for pid %d: %w", p.Pid, err)
	}

	statusList, err := p.Status()
	var status string
	switch {
	case err != nil:
		status = ValueUnknown
		c.logWarnIfEnabled("Failed to get process status", logger.Any("pid", p.Pid), logger.String("name", name), logger.Error(err))
	case len(statusList) > 0:
		// Use the first status code returned
		status = mapProcessStatus(statusList[0])
	default:
		status = ValueUnknown
	}

	cpuPercent, err := p.CPUPercent()
	if err != nil {
		// Log error but default to 0
		c.logWarnIfEnabled("Failed to get process CPU percent", logger.Any("pid", p.Pid), logger.String("name", name), logger.Error(err))
		cpuPercent = 0.0
	}

	memInfo, err := p.MemoryInfo()
	var memRSS uint64
	if err != nil {
		// Log error but default to 0
		c.logWarnIfEnabled("Failed to get process memory info", logger.Any("pid", p.Pid), logger.String("name", name), logger.Error(err))
		memRSS = 0
	} else {
		memRSS = memInfo.RSS // Resident Set Size
	}

	createTimeMillis, err := p.CreateTime()
	var uptimeSeconds int64
	if err != nil {
		// Log error but default to 0
		c.logWarnIfEnabled("Failed to get process create time", logger.Any("pid", p.Pid), logger.String("name", name), logger.Error(err))
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
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.logInfoIfEnabled("Getting process information", logger.String("path", path), logger.String("ip", ip), logger.String("query", ctx.QueryString()))

	showAll := ctx.QueryParam("all") == "true"

	procs, err := process.Processes()
	if err != nil {
		c.logErrorIfEnabled("Failed to list processes", logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Failed to list processes", http.StatusInternalServerError)
	}

	processInfos := c.collectProcessInfos(procs, showAll)

	c.logInfoIfEnabled("Process information retrieved successfully", logger.Int("count", len(processInfos)), logger.Bool("filter_applied", !showAll), logger.String("path", path), logger.String("ip", ip))

	return ctx.JSON(http.StatusOK, processInfos)
}

// collectProcessInfos filters and collects process information
func (c *Controller) collectProcessInfos(procs []*process.Process, showAll bool) []ProcessInfo {
	currentPID := int32(os.Getpid()) // #nosec G115 -- PID conversion safe, PIDs are within int32 range
	processInfos := make([]ProcessInfo, 0, len(procs))

	for _, p := range procs {
		if !showAll && !c.isRelevantProcess(p, currentPID) {
			continue
		}

		info, err := c.getSingleProcessInfo(p)
		if err != nil {
			c.logWarnIfEnabled("Skipping process due to error retrieving details", logger.Any("pid", p.Pid), logger.Error(err))
			continue
		}

		processInfos = append(processInfos, info)
	}

	return processInfos
}

// isRelevantProcess checks if process is main process or direct child
func (c *Controller) isRelevantProcess(p *process.Process, currentPID int32) bool {
	parentPID, err := p.Ppid()
	if err != nil {
		c.logWarnIfEnabled("Failed to get parent PID, skipping process", logger.Any("pid", p.Pid), logger.Error(err))
		return false
	}
	return p.Pid == currentPID || parentPID == currentPID
}

// checkThermalZone attempts to read and validate the temperature from a specific thermal zone.
// It returns the Celsius temperature, details about the sensor/error, whether it was valid, and any critical error.
func (c *Controller) checkThermalZone(zonePath string, targetTypes map[string]bool) (celsius float64, details string, isValid bool, err error) {
	zoneName := filepath.Base(zonePath)
	typePath := filepath.Join(zonePath, "type")

	//nolint:gosec // G304: typePath is from filepath.Glob on /sys/class/thermal/, not user input
	typeData, err := os.ReadFile(typePath)
	if err != nil {
		// Not a critical error for the overall request, just skip this zone.
		c.logDebugIfEnabled("Failed to read type file for zone", logger.String("zone", zoneName), logger.String("type_path", typePath), logger.Error(err))
		return 0, fmt.Sprintf("Failed to read type for %s", zoneName), false, nil
	}

	sensorType := strings.ToLower(strings.TrimSpace(string(typeData)))

	if _, isTarget := targetTypes[sensorType]; !isTarget {
		// Not a target sensor type, skip silently.
		return 0, "", false, nil
	}

	// It's a target type, now try to read and validate the temperature.
	tempFilePath := filepath.Join(zonePath, "temp")
	//nolint:gosec // G304: tempFilePath is from filepath.Glob on /sys/class/thermal/, not user input
	tempData, err := os.ReadFile(tempFilePath)
	if err != nil {
		details = fmt.Sprintf("Error reading temp from %s (type: %s)", zoneName, sensorType)
		c.logWarnIfEnabled(details, logger.String("temp_path", tempFilePath), logger.Error(err))
		return 0, details, false, nil // Error reading temp, but might find another valid zone.
	}

	tempStr := strings.TrimSpace(string(tempData))
	tempMillCelsius, err := strconv.Atoi(tempStr)
	if err != nil {
		details = fmt.Sprintf("Error parsing temp from %s (type: %s, value: '%s')", zoneName, sensorType, tempStr)
		c.logWarnIfEnabled(details, logger.Error(err))
		return 0, details, false, nil // Error parsing temp.
	}

	celsius = float64(tempMillCelsius) / float64(MillisecondsPerSecond)

	// Validate temperature range (0 to 100 °C inclusive)
	if celsius < 0.0 || celsius > 100.0 {
		details = fmt.Sprintf("Invalid temp from %s (type: %s, value: %.1f°C, expected 0-100°C)", zoneName, sensorType, celsius)
		c.logWarnIfEnabled("Temperature reading out of valid range", logger.String("details", details))
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
// thermalBasePath is the base directory for thermal zones on Linux
const thermalBasePath = "/sys/class/thermal/"

// cpuThermalTypes contains sensor types for CPU temperature
var cpuThermalTypes = map[string]bool{
	"cpu-thermal":     true, // Common on Raspberry Pi
	"x86_pkg_temp":    true, // Common on Intel x86 systems (like NUC)
	"soc_thermal":     true, // Common on some ARM SoCs
	"cpu_thermal":     true, // Alternative name
	"thermal-fan-est": true, // Seen on some systems
}

func (c *Controller) GetSystemCPUTemperature(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.logInfoIfEnabled("Getting system CPU temperature", logger.String("path", path), logger.String("ip", ip))

	response := SystemTemperature{
		IsAvailable: false,
		Message:     "No suitable CPU temperature sensor found or temperature out of valid range.",
	}

	// Check thermal directory access
	if err := c.checkThermalDirectoryAccess(ctx, &response, ip, path); err != nil {
		return err
	}
	if response.Message != "" && !response.IsAvailable {
		// Early return if directory doesn't exist - checkThermalDirectoryAccess already set response
		if response.Message == "Thermal zone directory not found. This feature is typically available on Linux systems." {
			return ctx.JSON(http.StatusOK, response)
		}
	}

	// Get thermal zones
	zones, err := c.getThermalZones(ctx, ip, path)
	if err != nil {
		return err
	}
	if len(zones) == 0 {
		response.Message = "No thermal zones found. This feature is typically available on Linux systems."
		c.logInfoIfEnabled("No thermal zones found via Glob.", logger.String("pattern", filepath.Join(thermalBasePath, "thermal_zone*")), logger.String("os", runtime.GOOS), logger.String("request_path", path), logger.String("ip", ip))
		return ctx.JSON(http.StatusOK, response)
	}

	// Find valid thermal zone
	c.findValidThermalZone(zones, &response, ip, path)

	return ctx.JSON(http.StatusOK, response)
}

// checkThermalDirectoryAccess checks if thermal directory exists and is accessible
func (c *Controller) checkThermalDirectoryAccess(ctx echo.Context, response *SystemTemperature, ip, path string) error {
	_, err := os.Stat(thermalBasePath)
	if err == nil {
		return nil
	}

	if os.IsNotExist(err) {
		response.Message = "Thermal zone directory not found. This feature is typically available on Linux systems."
		c.logInfoIfEnabled("Thermal zone directory not found, CPU temperature feature unavailable.", logger.String("path", thermalBasePath), logger.String("os", runtime.GOOS), logger.String("request_path", path), logger.String("ip", ip))
		return ctx.JSON(http.StatusOK, response)
	}

	c.logErrorIfEnabled("Failed to stat thermal base path", logger.String("path", thermalBasePath), logger.Error(err), logger.String("request_path", path), logger.String("ip", ip))
	return c.HandleError(ctx, err, "Failed to access thermal information due to filesystem error", http.StatusInternalServerError)
}

// getThermalZones retrieves available thermal zone paths
func (c *Controller) getThermalZones(ctx echo.Context, ip, path string) ([]string, error) {
	zones, err := filepath.Glob(filepath.Join(thermalBasePath, "thermal_zone*"))
	if err != nil {
		c.logErrorIfEnabled("Failed to glob for thermal zones", logger.String("base_path", thermalBasePath), logger.Error(err), logger.String("request_path", path), logger.String("ip", ip))
		return nil, c.HandleError(ctx, err, "Error scanning for thermal zones", http.StatusInternalServerError)
	}
	return zones, nil
}

// findValidThermalZone searches for a valid CPU thermal zone and updates response
func (c *Controller) findValidThermalZone(zones []string, response *SystemTemperature, ip, path string) {
	var lastAttemptDetails string

	for _, zonePath := range zones {
		celsius, details, isValid, err := c.checkThermalZone(zonePath, cpuThermalTypes)
		if err != nil {
			c.logErrorIfEnabled("Unexpected error checking thermal zone", logger.String("zone", zonePath), logger.Error(err))
			continue
		}

		if isValid {
			response.Celsius = celsius
			response.IsAvailable = true
			response.SensorDetails = details
			response.Message = "CPU temperature retrieved successfully."
			c.logInfoIfEnabled("CPU temperature retrieved successfully", logger.Float64("temperature_celsius", response.Celsius), logger.String("sensor_details", response.SensorDetails), logger.String("request_path", path), logger.String("ip", ip))
			return
		}

		if details != "" {
			lastAttemptDetails = details
		}
	}

	// No valid sensor found
	c.setTemperatureNotFoundResponse(response, lastAttemptDetails, ip, path)
}

// setTemperatureNotFoundResponse sets appropriate message when no valid sensor found
func (c *Controller) setTemperatureNotFoundResponse(response *SystemTemperature, lastAttemptDetails, ip, path string) {
	response.SensorDetails = lastAttemptDetails
	if lastAttemptDetails != "" {
		response.Message = fmt.Sprintf("A targeted CPU sensor was found but could not be read successfully or value was invalid. Last attempt details: %s", lastAttemptDetails)
	} else {
		response.Message = "No targeted CPU temperature sensor types (e.g., cpu-thermal, x86_pkg_temp) found or readable in available thermal zones."
	}

	c.logInfoIfEnabled("Could not retrieve a valid CPU temperature after checking all zones.", logger.String("final_message", response.Message), logger.String("sensor_details_attempted", response.SensorDetails), logger.String("request_path", path), logger.String("ip", ip))
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
	c.logInfoIfEnabled("Getting equalizer filter configuration",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Set cache headers for static configuration data
	ctx.Response().Header().Set("Cache-Control", "public, max-age=3600")

	// Return the equalizer filter configuration
	return ctx.JSON(http.StatusOK, conf.EqFilterConfig)
}

// GetDatabaseStats handles GET /api/v2/system/database/stats
// This endpoint returns statistics for the primary database.
// In v2-only mode (fresh install or post-migration), it returns v2 database stats.
// In legacy mode, it returns legacy database stats.
func (c *Controller) GetDatabaseStats(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.logInfoIfEnabled("Getting database statistics",
		logger.String("path", path),
		logger.String("ip", ip),
	)

	// In v2-only mode, return v2 database stats as the primary database
	if isV2OnlyMode {
		c.logInfoIfEnabled("Running in v2-only mode, returning v2 database as primary",
			logger.String("path", path),
			logger.String("ip", ip),
		)
		// Get v2 database stats
		v2Stats, ok := c.getV2Stats(path, ip)
		if ok {
			// Return v2 stats as the primary database stats
			return ctx.JSON(http.StatusOK, &datastore.DatabaseStats{
				Type:            v2Stats.Type,
				Location:        v2Stats.Location,
				SizeBytes:       v2Stats.SizeBytes,
				TotalDetections: v2Stats.TotalDetections,
				Connected:       v2Stats.Connected,
			})
		}
		// V2 database not available, return disconnected state
		c.logWarnIfEnabled("V2-only mode but v2 database not available",
			logger.String("path", path),
			logger.String("ip", ip),
		)
		return ctx.JSON(http.StatusOK, &datastore.DatabaseStats{
			Type:      "none",
			Connected: false,
			Location:  "",
		})
	}

	// Check if datastore is available
	if c.DS == nil {
		c.logErrorIfEnabled("Datastore not available",
			logger.String("path", path),
			logger.String("ip", ip),
		)
		return c.HandleError(ctx, fmt.Errorf("datastore not available"), "Database not configured", http.StatusServiceUnavailable)
	}

	// Get database stats from the datastore
	stats, err := c.DS.GetDatabaseStats()

	// Handle errors first
	isPartialStats := false
	if err != nil {
		// If the database is not connected, log as warning and return partial stats with 200 OK
		if errors.Is(err, datastore.ErrDBNotConnected) {
			c.logWarnIfEnabled("Database not connected, returning partial stats",
				logger.Error(err),
				logger.String("path", path),
				logger.String("ip", ip),
			)
			isPartialStats = true
			// Continue to return partial stats below
		} else {
			c.logErrorIfEnabled("Failed to get database stats",
				logger.Error(err),
				logger.String("path", path),
				logger.String("ip", ip),
			)
			return c.HandleError(ctx, err, "Failed to retrieve database statistics", http.StatusInternalServerError)
		}
	}

	// Guard against nil stats (defensive - future implementations might return nil)
	if stats == nil {
		c.logErrorIfEnabled("GetDatabaseStats returned nil stats",
			logger.String("path", path),
			logger.String("ip", ip),
		)
		return c.HandleError(ctx, fmt.Errorf("database stats unavailable"), "Failed to retrieve database statistics", http.StatusInternalServerError)
	}

	// Log with appropriate message based on whether stats are partial or complete
	if isPartialStats {
		c.logInfoIfEnabled("Database statistics retrieved (partial)",
			logger.String("type", stats.Type),
			logger.Bool("connected", stats.Connected),
			logger.String("path", path),
			logger.String("ip", ip),
		)
	} else {
		c.logInfoIfEnabled("Database statistics retrieved successfully",
			logger.String("type", stats.Type),
			logger.Any("size_bytes", stats.SizeBytes),
			logger.Any("total_detections", stats.TotalDetections),
			logger.Bool("connected", stats.Connected),
			logger.String("path", path),
			logger.String("ip", ip),
		)
	}

	return ctx.JSON(http.StatusOK, stats)
}

// V2DatabaseStatsResponse represents v2 database statistics.
type V2DatabaseStatsResponse struct {
	Type            string `json:"type"`
	Location        string `json:"location"`
	SizeBytes       int64  `json:"size_bytes"`
	TotalDetections int64  `json:"total_detections"`
	Connected       bool   `json:"connected"`
}

// getV2Stats is a helper method that retrieves v2 database statistics.
// It returns the stats and a boolean indicating if stats were successfully retrieved.
// If the v2 database is not available, it returns nil and false.
func (c *Controller) getV2Stats(logPath, logIP string) (*V2DatabaseStatsResponse, bool) {
	// Check if V2Manager is available
	if c.V2Manager == nil {
		c.logInfoIfEnabled("V2 database not initialized",
			logger.String("path", logPath), logger.String("ip", logIP))
		return nil, false
	}

	// Check if database exists
	if !c.V2Manager.Exists() {
		c.logInfoIfEnabled("V2 database does not exist yet",
			logger.String("path", logPath), logger.String("ip", logIP))
		return nil, false
	}

	// Build response
	response := &V2DatabaseStatsResponse{
		Type:      "SQLite",
		Location:  c.V2Manager.Path(),
		Connected: true,
	}

	// Adjust type for MySQL
	if c.V2Manager.IsMySQL() {
		response.Type = "MySQL"
	}

	// Get database size
	db := c.V2Manager.DB()
	if !c.V2Manager.IsMySQL() {
		// SQLite: get file size
		if fi, err := os.Stat(c.V2Manager.Path()); err == nil {
			response.SizeBytes = fi.Size()
		}
	} else if db != nil {
		// MySQL: query information_schema for database size
		// Extract database name from location (format: host:port/database)
		location := c.V2Manager.Path()
		if idx := strings.LastIndex(location, "/"); idx != -1 {
			dbName := location[idx+1:]
			var sizeBytes int64
			err := db.Raw(`
				SELECT COALESCE(SUM(data_length + index_length), 0) as size
				FROM information_schema.TABLES
				WHERE table_schema = ?
			`, dbName).Scan(&sizeBytes).Error
			if err == nil {
				response.SizeBytes = sizeBytes
			} else {
				c.logWarnIfEnabled("Failed to get MySQL database size",
					logger.Error(err), logger.String("path", logPath), logger.String("ip", logIP))
			}
		}
	}

	// Count total detections from v2 database
	if db != nil {
		var count int64
		if err := db.Table("detections").Count(&count).Error; err == nil {
			response.TotalDetections = count
		} else {
			c.logWarnIfEnabled("Failed to count v2 detections",
				logger.Error(err), logger.String("path", logPath), logger.String("ip", logIP))
		}
	}

	c.logInfoIfEnabled("V2 database statistics retrieved successfully",
		logger.String("type", response.Type),
		logger.Any("size_bytes", response.SizeBytes),
		logger.Any("total_detections", response.TotalDetections),
		logger.Bool("connected", response.Connected),
		logger.String("path", logPath), logger.String("ip", logIP))

	return response, true
}

// GetV2DatabaseStats handles GET /api/v2/system/database/v2/stats
func (c *Controller) GetV2DatabaseStats(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.logInfoIfEnabled("Getting v2 database statistics",
		logger.String("path", path), logger.String("ip", ip))

	stats, ok := c.getV2Stats(path, ip)
	if !ok {
		return c.HandleError(ctx, fmt.Errorf("v2 database not available"),
			"V2 database not initialized", http.StatusNotFound)
	}

	return ctx.JSON(http.StatusOK, stats)
}

// Backup constants
const (
	// backupDiskSpaceBuffer is the additional disk space required beyond the database size for backup.
	backupDiskSpaceBuffer = 100 * 1024 * 1024 // 100 MB
	// Database type constants for backup API
	dbTypeLegacy = "legacy"
	dbTypeV2     = "v2"
)

// DownloadDatabaseBackup handles POST /api/v2/system/database/backup
// Creates a safe backup using SQLite's VACUUM INTO command.
func (c *Controller) DownloadDatabaseBackup(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	dbType := ctx.QueryParam("type")

	c.logInfoIfEnabled("Database backup requested",
		logger.String("db_type", dbType),
		logger.String("path", path), logger.String("ip", ip))

	// Validate dbType parameter
	if dbType != dbTypeLegacy && dbType != dbTypeV2 {
		return c.HandleError(ctx, fmt.Errorf("invalid type"),
			"Type must be 'legacy' or 'v2'", http.StatusBadRequest)
	}

	// Get database path based on type
	var dbPath string

	if dbType == dbTypeLegacy {
		// Check if it's SQLite
		if c.Settings.Output.SQLite.Path == "" {
			return c.HandleError(ctx, fmt.Errorf("not sqlite"),
				"Backup download only available for SQLite databases. Use MySQL tools for MySQL backup.",
				http.StatusBadRequest)
		}
		dbPath = c.Settings.Output.SQLite.Path

		// Get the underlying GORM DB from the datastore
		if c.DS == nil {
			return c.HandleError(ctx, fmt.Errorf("datastore not available"),
				"Database not configured", http.StatusServiceUnavailable)
		}
	} else {
		// V2 database
		if c.V2Manager == nil {
			return c.HandleError(ctx, fmt.Errorf("v2 not available"),
				"V2 database not initialized", http.StatusNotFound)
		}
		if c.V2Manager.IsMySQL() {
			return c.HandleError(ctx, fmt.Errorf("not sqlite"),
				"Backup download only available for SQLite databases. Use MySQL tools for MySQL backup.",
				http.StatusBadRequest)
		}
		dbPath = c.V2Manager.Path()
	}

	// Get source database size for disk space check
	fileInfo, err := os.Stat(dbPath)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get database info", http.StatusInternalServerError)
	}
	dbSize := fileInfo.Size()

	// Check disk space on temp directory where VACUUM INTO will write
	tempDir := os.TempDir()
	usage, err := disk.Usage(tempDir)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to check disk space", http.StatusInternalServerError)
	}
	// Calculate required space. dbSize is always non-negative from FileInfo.Size()
	// #nosec G115 -- dbSize from os.FileInfo.Size() is always non-negative
	requiredSpace := uint64(dbSize) + backupDiskSpaceBuffer
	if usage.Free < requiredSpace {
		return c.HandleError(ctx, fmt.Errorf("insufficient space"),
			fmt.Sprintf("Not enough disk space for backup. Need %s free, have %s available.",
				formatBytesUint64(requiredSpace), formatBytesUint64(usage.Free)),
			http.StatusInsufficientStorage) // HTTP 507
	}

	// Create temp file path for VACUUM INTO
	timestamp := time.Now().Format("20060102-150405")
	tempPath := filepath.Join(os.TempDir(), fmt.Sprintf("birdnet-%s-backup-%s.db", dbType, timestamp))

	// Ensure cleanup of temp file after response is sent
	defer func() {
		if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
			c.logWarnIfEnabled("Failed to cleanup backup temp file",
				logger.String("path", tempPath), logger.Error(err))
		}
	}()

	// Execute VACUUM INTO for safe, consistent backup
	vacuumSQL := fmt.Sprintf("VACUUM INTO '%s'", tempPath)
	if dbType == dbTypeLegacy {
		// Use the legacy datastore's DB
		sqliteStore, ok := c.DS.(*datastore.SQLiteStore)
		if !ok {
			return c.HandleError(ctx, fmt.Errorf("unsupported datastore type"),
				"Cannot perform backup on this datastore type", http.StatusInternalServerError)
		}
		if err := sqliteStore.DB.Exec(vacuumSQL).Error; err != nil {
			return c.HandleError(ctx, err, "Failed to create backup", http.StatusInternalServerError)
		}
	} else {
		// Use V2Manager's DB
		if err := c.V2Manager.DB().Exec(vacuumSQL).Error; err != nil {
			return c.HandleError(ctx, err, "Failed to create backup", http.StatusInternalServerError)
		}
	}

	// Set response headers and stream file
	filename := fmt.Sprintf("birdnet-%s-backup-%s.db", dbType, timestamp)
	ctx.Response().Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", filename))
	ctx.Response().Header().Set("Content-Type", "application/octet-stream")

	c.logInfoIfEnabled("Database backup created successfully",
		logger.String("db_type", dbType),
		logger.String("backup_file", filename),
		logger.String("path", path), logger.String("ip", ip))

	return ctx.File(tempPath)
}

// formatBytesUint64 formats bytes into human-readable format (for uint64 values).
func formatBytesUint64(bytes uint64) string {
	const unit uint64 = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := unit, 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
