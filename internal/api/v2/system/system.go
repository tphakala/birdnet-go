// internal/api/v2/system/system.go
package system

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/restart"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"gorm.io/gorm"
)

// System info constants (file-local)
const (
	bytesPerKB          = 1024 // Bytes per kilobyte
	maxPercentage       = 100  // Maximum percentage value
	minRequiredElements = 2    // Minimum required elements for various checks
)

// SystemInfo represents basic system information
type SystemInfo struct {
	Hostname       string    `json:"hostname"`
	PlatformVer    string    `json:"platform_version"`
	KernelVersion  string    `json:"kernel_version"`
	UpTime         uint64    `json:"uptime_seconds"`
	BootTime       time.Time `json:"boot_time"`
	AppStart       time.Time `json:"app_start_time"`
	AppUptime      int64     `json:"app_uptime_seconds"`
	NumCPU         int       `json:"num_cpu"`
	SystemModel    string    `json:"system_model,omitempty"`
	TimeZone       string    `json:"time_zone,omitempty"`
	OSDisplay      string    `json:"os_display"`
	Architecture   string    `json:"architecture"`
	CPUModel       string    `json:"cpu_model,omitempty"`
	Environment    string    `json:"environment"`
	Virtualization string    `json:"virtualization,omitempty"`
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

// RestartStatus represents the restart availability and pending state.
type RestartStatus struct {
	BinaryRestartAvailable    bool     `json:"binary_restart_available"`
	ContainerRestartAvailable bool     `json:"container_restart_available"`
	RestartRequired           bool     `json:"restart_required"`
	RestartReasons            []string `json:"restart_reasons"`
}

// Use monotonic clock for start time
var processStartTime = time.Now()
var startMonotonicTime = time.Now() // This inherently includes monotonic clock reading

// JobQueueStats represents the job queue statistics
type JobQueueStats struct {
	Queue     map[string]any `json:"queue"`
	Actions   map[string]any `json:"actions"`
	Timestamp string         `json:"timestamp"`
}

// GetJobQueueStats returns statistics about the job queue
func (c *Handler) GetJobQueueStats(ctx echo.Context) error {
	c.LogInfoIfEnabled("Getting job queue statistics",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	proc := c.Processor
	if proc == nil {
		c.LogErrorIfEnabled("Processor not available for job queue stats",
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, fmt.Errorf("processor not available"), "Processor not available", http.StatusInternalServerError)
	}

	jq := proc.JobQueue
	if jq == nil {
		c.LogErrorIfEnabled("Job queue not available",
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, fmt.Errorf("job queue not available"), "Job queue not available", http.StatusInternalServerError)
	}

	stats := jq.GetStats()

	// Convert to JSON
	jsonStats, err := stats.ToJSON()
	if err != nil {
		c.LogErrorIfEnabled("Failed to convert job queue stats to JSON",
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Failed to convert job queue stats to JSON", http.StatusInternalServerError)
	}

	// Parse the JSON string back to a map for proper JSON response
	var statsMap map[string]any
	if err := json.Unmarshal([]byte(jsonStats), &statsMap); err != nil {
		c.LogErrorIfEnabled("Failed to parse job queue stats JSON",
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Failed to parse job queue stats JSON", http.StatusInternalServerError)
	}

	c.LogInfoIfEnabled("Job queue statistics retrieved successfully",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, statsMap)
}

// NetworkInterface represents a bindable network interface address.
type NetworkInterface struct {
	Address string `json:"address"`
	Name    string `json:"name"`
	Label   string `json:"label"`
	Status  string `json:"status"`
}

// GetNetworkInterfaces returns the IPv4 network interfaces available for binding.
// GET /api/v2/system/network-interfaces
func (c *Handler) GetNetworkInterfaces(ctx echo.Context) error {
	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Getting network interfaces")

	interfaces := []NetworkInterface{
		{Address: "0.0.0.0", Name: "all", Label: "All interfaces", Status: "up"},
	}

	// Track addresses to avoid duplicates
	seen := map[string]bool{"0.0.0.0": true}

	ifaces, err := net.Interfaces()
	if err != nil {
		// Log but don't fail - return at least the wildcard and loopback
		c.LogAPIRequest(ctx, logger.LogLevelWarn, "Failed to enumerate network interfaces", logger.Error(err))
		interfaces = append(interfaces, NetworkInterface{
			Address: "127.0.0.1", Name: "lo", Label: "Loopback", Status: "up",
		})
		c.LogAPIRequest(ctx, logger.LogLevelInfo, "Network interfaces retrieved (fallback)",
			logger.Int("count", len(interfaces)))
		return ctx.JSON(http.StatusOK, map[string]any{"interfaces": interfaces})
	}

	for _, iface := range ifaces {
		// Determine interface status
		status := "down"
		if iface.Flags&net.FlagUp != 0 {
			status = "up"
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			// Skip non-IPv4 addresses
			if ip == nil || ip.To4() == nil {
				continue
			}

			addrStr := ip.String()
			if seen[addrStr] {
				continue
			}
			seen[addrStr] = true

			label := iface.Name
			if ip.IsLoopback() {
				label = "Loopback"
			}

			interfaces = append(interfaces, NetworkInterface{
				Address: addrStr,
				Name:    iface.Name,
				Label:   label,
				Status:  status,
			})
		}
	}

	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Network interfaces retrieved successfully",
		logger.Int("count", len(interfaces)))
	return ctx.JSON(http.StatusOK, map[string]any{"interfaces": interfaces})
}

// GetRestartStatus handles GET /api/v2/system/restart-status
func (c *Handler) GetRestartStatus(ctx echo.Context) error {
	canRestart := c.GetShutdownRequester() != nil
	status := RestartStatus{
		BinaryRestartAvailable:    canRestart,
		ContainerRestartAvailable: canRestart && sysinfo.IsContainer(),
		RestartRequired:           restart.IsRestartRequired(),
		RestartReasons:            restart.GetRestartReasons(),
	}

	return ctx.JSON(http.StatusOK, status)
}

// GetSystemInfo handles GET /api/v2/system/info
func (c *Handler) GetSystemInfo(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.LogInfoIfEnabled("Getting system information", logger.String("path", path), logger.String("ip", ip))

	hostInfo, err := host.Info()
	if err != nil {
		c.LogErrorIfEnabled("Failed to get host information", logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Failed to get host information", http.StatusInternalServerError)
	}

	hostname := c.getHostnameWithFallback(ip, path)
	systemModel := c.getSystemModelWithLogging(ip, path)
	envType, envDetail := sysinfo.GetEnvironment()

	info := SystemInfo{
		Architecture:   sysinfo.GetCPUArch(),
		Hostname:       hostname,
		PlatformVer:    hostInfo.PlatformVersion,
		KernelVersion:  hostInfo.KernelVersion,
		UpTime:         hostInfo.Uptime,
		BootTime:       time.Unix(int64(hostInfo.BootTime), 0), // #nosec G115 -- BootTime from system APIs, safe conversion for timestamp
		AppStart:       processStartTime,
		AppUptime:      int64(time.Since(startMonotonicTime).Seconds()),
		NumCPU:         runtime.NumCPU(),
		SystemModel:    systemModel,
		TimeZone:       getTimeZoneString(),
		OSDisplay:      getOSDisplayString(hostInfo.Platform),
		CPUModel:       sysinfo.GetCPUModel(),
		Environment:    envType,
		Virtualization: envDetail,
	}

	c.LogInfoIfEnabled("System information retrieved successfully", logger.String("os_display", info.OSDisplay), logger.String("arch", info.Architecture), logger.String("hostname", info.Hostname), logger.Any("uptime", info.UpTime), logger.Int64("app_uptime", info.AppUptime), logger.String("timezone", info.TimeZone), logger.String("path", path), logger.String("ip", ip))

	return ctx.JSON(http.StatusOK, info)
}

// getHostnameWithFallback gets hostname or returns "unknown" on error
func (c *Handler) getHostnameWithFallback(ip, path string) string {
	hostname, err := os.Hostname()
	if err != nil {
		c.LogWarnIfEnabled("Failed to get hostname, using 'unknown'", logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return valueUnknown
	}
	return hostname
}

// getSystemModelWithLogging gets system model on Linux with logging
func (c *Handler) getSystemModelWithLogging(ip, path string) string {
	if runtime.GOOS != osLinux {
		return ""
	}
	systemModel := getSystemModelFromProc()
	if systemModel == "" {
		c.LogDebugIfEnabled("Could not determine system model from /proc/cpuinfo", logger.String("path", path), logger.String("ip", ip))
	}
	return systemModel
}

// getTimeZoneString returns formatted timezone string
func getTimeZoneString() string {
	loc := time.Local
	timeZoneStr := loc.String()

	if timeZoneStr != "Local" && timeZoneStr != "" {
		return timeZoneStr
	}

	// Fallback if Olson name is "Local" or empty
	name, offset := time.Now().Zone()
	offsetHours := offset / secondsPerHour
	offsetMinutes := (offset % secondsPerHour) / secondsPerMinute
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
	case osLinux:
		if platformName != "" {
			return fmt.Sprintf("%s Linux", platformName)
		}
		return "Linux"
	case osWindows:
		return "Microsoft Windows"
	case osDarwin:
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
		apicore.GetLogger().Warn("Could not read /proc/cpuinfo", logger.Error(err))
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
func (c *Handler) GetResourceInfo(ctx echo.Context) error {
	c.LogInfoIfEnabled("Getting system resource information",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Get memory statistics
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		c.LogErrorIfEnabled("Failed to get memory information",
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Failed to get memory information", http.StatusInternalServerError)
	}

	// Get swap statistics
	swapInfo, err := mem.SwapMemory()
	if err != nil {
		c.LogErrorIfEnabled("Failed to get swap information",
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Failed to get swap information", http.StatusInternalServerError)
	}

	// Get CPU usage from cache instead of blocking
	cpuPercent := apicore.GetCachedCPUUsage()

	// Get process information for the current process. Reuse the cached
	// *process.Process (see the selfProc field) and sample CPU via Percent(0) so
	// the value reflects usage since the previous request — interval usage —
	// rather than the lifetime average a freshly created instance's CPUPercent()
	// returns and which grows steadily more dampened as the process ages. The
	// instance access is serialized because Percent(0) mutates its stored sample.
	c.selfProcMu.Lock()
	proc, err := c.selfProcessLocked()
	if err != nil {
		c.selfProcMu.Unlock()
		c.LogErrorIfEnabled("Failed to get process information",
			logger.Error(err),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		return c.HandleError(ctx, err, "Failed to get process information", http.StatusInternalServerError)
	}
	procMem, memErr := proc.MemoryInfo()
	// Percent(0) returns the CPU used since the previous call; the first call
	// after startup primes the sample and returns 0.
	procCPU, cpuErr := proc.Percent(0)
	c.selfProcMu.Unlock()

	if memErr != nil {
		c.Debug("Failed to get process memory info: %v", memErr)
		c.LogWarnIfEnabled("Failed to get process memory info",
			logger.Error(memErr),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		// Continue with nil procMem, handled below
	}

	if cpuErr != nil {
		c.Debug("Failed to get process CPU info: %v", cpuErr)
		c.LogWarnIfEnabled("Failed to get process CPU info",
			logger.Error(cpuErr),
			logger.String("path", ctx.Request().URL.Path),
			logger.String("ip", ctx.RealIP()),
		)
		// Will use 0 as default value
		procCPU = 0
	}
	// Normalize to a share of total system capacity (0-100%) so it matches the
	// system CPU gauge and cannot exceed 100% on multi-core hosts.
	procCPU = normalizeProcessCPU(procCPU)

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

	c.LogInfoIfEnabled("System resource information retrieved successfully",
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

// normalizeProcessCPU converts a gopsutil per-process CPU percentage (reported
// relative to a single core, so potentially >100% for a multi-threaded process)
// into the process's share of total system capacity in the range
// [0, maxPercentage]. Dividing by the logical core count matches the semantics of
// the system CPU gauge; the clamp is a defensive guard against transient sampling
// overshoot so the value never exceeds 100%.
func normalizeProcessCPU(cpuPercent float64) float64 {
	if numCPU := runtime.NumCPU(); numCPU > 0 {
		cpuPercent /= float64(numCPU)
	}
	switch {
	case cpuPercent > maxPercentage:
		return maxPercentage
	case cpuPercent < 0:
		return 0
	default:
		return cpuPercent
	}
}

// processCPUPercent returns p's CPU usage since the previous request, in gopsutil's
// per-single-core units (so still normalizeProcessCPU's input).
//
// It samples through a retained per-PID instance (see the procSamples field) rather
// than the caller's freshly listed p, because Percent(0) measures against the sample
// the instance itself stored last time. createTimeMillis identifies the process
// behind the PID; a mismatch means the PID was recycled and the retained history
// belongs to a dead process, so the entry is replaced.
//
// The first sample for a PID has nothing to diff against and returns 0. That costs
// one poll per process per server run — the cache lives as long as the handler — not
// one per page load.
func (c *Handler) processCPUPercent(p *process.Process, createTimeMillis int64) (float64, error) {
	c.procSamplesMu.Lock()
	defer c.procSamplesMu.Unlock()

	if c.procSamples == nil {
		c.procSamples = make(map[int32]*procSample)
	}

	if sample, ok := c.procSamples[p.Pid]; ok && sample.createTime == createTimeMillis {
		return sample.proc.Percent(0)
	}

	c.procSamples[p.Pid] = &procSample{proc: p, createTime: createTimeMillis}
	return p.Percent(0)
}

// pruneProcSamples drops retained samplers whose PID is no longer running, keeping
// procSamples bounded by the live process count on a long-lived host. live is the
// full process list, not the filtered table, so a process the table hides is not
// evicted and re-primed on every request.
func (c *Handler) pruneProcSamples(live []*process.Process) {
	liveSet := make(map[int32]struct{}, len(live))
	for _, p := range live {
		liveSet[p.Pid] = struct{}{}
	}

	c.procSamplesMu.Lock()
	defer c.procSamplesMu.Unlock()
	for pid := range c.procSamples {
		if _, ok := liveSet[pid]; !ok {
			delete(c.procSamples, pid)
		}
	}
}

// selfProcessLocked returns the cached *process.Process for the current PID,
// creating it on first use. Reusing a single instance is what lets Percent(0)
// report interval CPU usage (see the selfProc field). Callers must hold
// c.selfProcMu.
func (c *Handler) selfProcessLocked() (*process.Process, error) {
	if c.selfProc == nil {
		p, err := process.NewProcess(int32(os.Getpid())) // #nosec G115 -- PID conversion safe, PIDs are within int32 range
		if err != nil {
			return nil, err
		}
		c.selfProc = p
	}
	return c.selfProc, nil
}

// GetDiskInfo handles GET /api/v2/system/disks
func (c *Handler) GetDiskInfo(ctx echo.Context) error {
	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Getting disk information")

	partitions, err := disk.Partitions(false)
	if err != nil {
		c.LogAPIRequest(ctx, logger.LogLevelError, "Failed to get disk partitions",
			logger.Error(err))
		return c.HandleError(ctx, err, "Failed to get disk partitions", http.StatusInternalServerError)
	}

	ioCounters, err := disk.IOCounters()
	if err != nil {
		c.LogAPIRequest(ctx, logger.LogLevelWarn, "Failed to get IO counters, continuing without IO metrics",
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

	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Disk information retrieved successfully",
		logger.Int("disk_count", len(disks)))
	return ctx.JSON(http.StatusOK, disks)
}

// getUptimeMs returns system uptime in milliseconds
func (c *Handler) getUptimeMs() uint64 {
	hostInfo, err := host.Info()
	if err != nil {
		c.Debug("Failed to get host information for uptime: %v", err)
		return 0
	}
	return hostInfo.Uptime * millisecondsPerSecond
}

// buildDiskInfo creates a DiskInfo struct from partition data
func (c *Handler) buildDiskInfo(partition disk.PartitionStat, ioCounters map[string]disk.IOCountersStat, uptimeMs uint64) DiskInfo {
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
func (c *Handler) populateDiskUsage(info *DiskInfo, mountpoint string) {
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
func (c *Handler) populateIOMetrics(info *DiskInfo, device string, ioCounters map[string]disk.IOCountersStat, uptimeMs uint64) {
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
func (c *Handler) calculateIOBusyPerc(counter *disk.IOCountersStat, uptimeMs uint64) float64 {
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
	for i, b := range slices.Backward([]byte(base)) {
		if b < '0' || b > '9' {
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
	switch fstype {
	case "nfs", "nfs4", "cifs", "smbfs", "sshfs", "fuse.sshfs", "afs", "9p", "ncpfs":
		return true
	default:
		return false
	}
}

// isReadOnlyMount returns true if the filesystem is mounted as read-only
func isReadOnlyMount(opts []string) bool {
	// Look for read-only option in the mount options
	return slices.Contains(opts, "ro")
}

// getSingleProcessInfo retrieves detailed information for a single process.
// It handles errors gracefully and returns a ProcessInfo struct.
func (c *Handler) getSingleProcessInfo(p *process.Process) (ProcessInfo, error) {
	name, err := p.Name()
	if err != nil {
		// Log error but continue, maybe process terminated?
		c.LogWarnIfEnabled("Failed to get process name", logger.Any("pid", p.Pid), logger.Error(err))
		// Return an error to indicate this process couldn't be fully processed
		return ProcessInfo{}, fmt.Errorf("failed to get process name for pid %d: %w", p.Pid, err)
	}

	statusList, err := p.Status()
	var status string
	switch {
	case err != nil:
		status = valueUnknown
		c.LogWarnIfEnabled("Failed to get process status", logger.Any("pid", p.Pid), logger.String("name", name), logger.Error(err))
	case len(statusList) > 0:
		// Use the first status code returned
		status = mapProcessStatus(statusList[0])
	default:
		status = valueUnknown
	}

	// Read the create time before CPU: it doubles as the identity check that keeps a
	// retained CPU sampler from being diffed against a recycled PID's history.
	createTimeMillis, createErr := p.CreateTime()
	var uptimeSeconds int64
	if createErr != nil {
		// Log error but default to 0
		c.LogWarnIfEnabled("Failed to get process create time", logger.Any("pid", p.Pid), logger.String("name", name), logger.Error(createErr))
		uptimeSeconds = 0
	} else {
		// Calculate uptime relative to now
		uptimeSeconds = max(time.Now().Unix()-(createTimeMillis/millisecondsPerSecond),
			// Sanity check for clock skew
			0)
	}

	var cpuPercent float64
	if createErr != nil {
		// Without a create time a recycled PID is indistinguishable from the original, so
		// the retained sampler cannot be trusted; fall back to the lifetime average.
		cpuPercent, err = p.CPUPercent()
	} else {
		cpuPercent, err = c.processCPUPercent(p, createTimeMillis)
	}
	if err != nil {
		// Log error but default to 0
		c.LogWarnIfEnabled("Failed to get process CPU percent", logger.Any("pid", p.Pid), logger.String("name", name), logger.Error(err))
		cpuPercent = 0.0
	}
	// gopsutil reports per-process CPU relative to a single core, so a process
	// spread across multiple cores can exceed 100% (e.g. 200% for two full cores).
	// Normalize by the logical core count so the value is the process's share of
	// total system capacity (0-100%), consistent with the system CPU gauge and
	// never exceeding 100%.
	cpuPercent = normalizeProcessCPU(cpuPercent)

	memInfo, err := p.MemoryInfo()
	var memRSS uint64
	if err != nil {
		// Log error but default to 0
		c.LogWarnIfEnabled("Failed to get process memory info", logger.Any("pid", p.Pid), logger.String("name", name), logger.Error(err))
		memRSS = 0
	} else {
		memRSS = memInfo.RSS // Resident Set Size
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
func (c *Handler) GetProcessInfo(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.LogInfoIfEnabled("Getting process information", logger.String("path", path), logger.String("ip", ip), logger.String("query", ctx.QueryString()))

	showAll := ctx.QueryParam("all") == "true"

	procs, err := process.Processes()
	if err != nil {
		c.LogErrorIfEnabled("Failed to list processes", logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Failed to list processes", http.StatusInternalServerError)
	}

	c.pruneProcSamples(procs)

	processInfos := c.collectProcessInfos(procs, showAll)

	c.LogInfoIfEnabled("Process information retrieved successfully", logger.Int("count", len(processInfos)), logger.Bool("filter_applied", !showAll), logger.String("path", path), logger.String("ip", ip))

	return ctx.JSON(http.StatusOK, processInfos)
}

// collectProcessInfos filters and collects process information
func (c *Handler) collectProcessInfos(procs []*process.Process, showAll bool) []ProcessInfo {
	currentPID := int32(os.Getpid()) // #nosec G115 -- PID conversion safe, PIDs are within int32 range
	processInfos := make([]ProcessInfo, 0, len(procs))

	for _, p := range procs {
		if !showAll && !c.isRelevantProcess(p, currentPID) {
			continue
		}

		info, err := c.getSingleProcessInfo(p)
		if err != nil {
			c.LogWarnIfEnabled("Skipping process due to error retrieving details", logger.Any("pid", p.Pid), logger.Error(err))
			continue
		}

		processInfos = append(processInfos, info)
	}

	return processInfos
}

// isRelevantProcess checks if process is main process or direct child
func (c *Handler) isRelevantProcess(p *process.Process, currentPID int32) bool {
	parentPID, err := p.Ppid()
	if err != nil {
		c.LogWarnIfEnabled("Failed to get parent PID, skipping process", logger.Any("pid", p.Pid), logger.Error(err))
		return false
	}
	return p.Pid == currentPID || parentPID == currentPID
}

// checkThermalZone attempts to read and validate the temperature from a specific thermal zone.
// It returns the Celsius temperature, details about the sensor/error, whether it was valid, and any critical error.
func (c *Handler) checkThermalZone(zonePath string, targetTypes map[string]bool) (celsius float64, details string, isValid bool, err error) {
	zoneName := filepath.Base(zonePath)
	typePath := filepath.Join(zonePath, "type")

	//nolint:gosec // G304: typePath is from filepath.Glob on /sys/class/thermal/, not user input
	typeData, err := os.ReadFile(typePath)
	if err != nil {
		// Not a critical error for the overall request, just skip this zone.
		c.LogDebugIfEnabled("Failed to read type file for zone", logger.String("zone", zoneName), logger.String("type_path", typePath), logger.Error(err))
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
		c.LogWarnIfEnabled(details, logger.String("temp_path", tempFilePath), logger.Error(err))
		return 0, details, false, nil // Error reading temp, but might find another valid zone.
	}

	tempStr := strings.TrimSpace(string(tempData))
	tempMillCelsius, err := strconv.Atoi(tempStr)
	if err != nil {
		details = fmt.Sprintf("Error parsing temp from %s (type: %s, value: '%s')", zoneName, sensorType, tempStr)
		c.LogWarnIfEnabled(details, logger.Error(err))
		return 0, details, false, nil // Error parsing temp.
	}

	celsius = float64(tempMillCelsius) / float64(millisecondsPerSecond)

	// Validate temperature range (0 to 100 °C inclusive)
	if celsius < 0.0 || celsius > 100.0 {
		details = fmt.Sprintf("Invalid temp from %s (type: %s, value: %.1f°C, expected 0-100°C)", zoneName, sensorType, celsius)
		c.LogWarnIfEnabled("Temperature reading out of valid range", logger.String("details", details))
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

// defaultNoSensorMessage is the default message when no suitable CPU temperature sensor is found
const defaultNoSensorMessage = "No suitable CPU temperature sensor found or temperature out of valid range."

// cpuThermalTypes contains sensor types for CPU temperature
var cpuThermalTypes = map[string]bool{
	"cpu-thermal":     true, // Common on Raspberry Pi
	"x86_pkg_temp":    true, // Common on Intel x86 systems (like NUC)
	"soc_thermal":     true, // Common on some ARM SoCs
	"cpu_thermal":     true, // Alternative name
	"thermal-fan-est": true, // Seen on some systems
}

func (c *Handler) GetSystemCPUTemperature(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.LogInfoIfEnabled("Getting system CPU temperature", logger.String("path", path), logger.String("ip", ip))

	response := SystemTemperature{
		IsAvailable: false,
		Message:     defaultNoSensorMessage,
	}

	// Check thermal directory access
	exists, err := c.checkThermalDirectoryAccess(&response, ip, path)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to access thermal information due to filesystem error", http.StatusInternalServerError)
	}
	if !exists {
		return ctx.JSON(http.StatusOK, response)
	}

	// Get thermal zones
	zones, err := c.getThermalZones(ctx, ip, path)
	if err != nil {
		return err
	}
	if len(zones) == 0 {
		response.Message = "No thermal zones found. This feature is typically available on Linux systems."
		c.LogInfoIfEnabled("No thermal zones found via Glob.", logger.String("pattern", filepath.Join(thermalBasePath, "thermal_zone*")), logger.String("os", runtime.GOOS), logger.String("request_path", path), logger.String("ip", ip))
		return ctx.JSON(http.StatusOK, response)
	}

	// Find valid thermal zone
	c.findValidThermalZone(zones, &response, ip, path)

	return ctx.JSON(http.StatusOK, response)
}

// checkThermalDirectoryAccess checks if thermal directory exists and is accessible.
// Returns (true, nil) if the directory exists, (false, nil) if not found (with response
// fields set), or (false, error) for other filesystem failures.
func (c *Handler) checkThermalDirectoryAccess(response *SystemTemperature, ip, path string) (bool, error) {
	_, err := os.Stat(thermalBasePath)
	if err == nil {
		return true, nil
	}

	if errors.Is(err, os.ErrNotExist) {
		response.Message = "Thermal zone directory not found. This feature is typically available on Linux systems."
		c.LogDebugIfEnabled("Thermal zone directory not found, CPU temperature feature unavailable.", logger.String("path", thermalBasePath), logger.String("os", runtime.GOOS), logger.String("request_path", path), logger.String("ip", ip))
		return false, nil
	}

	c.LogErrorIfEnabled("Failed to stat thermal base path", logger.String("path", thermalBasePath), logger.Error(err), logger.String("request_path", path), logger.String("ip", ip))
	return false, fmt.Errorf("failed to access thermal information: %w", err)
}

// getThermalZones retrieves available thermal zone paths
func (c *Handler) getThermalZones(ctx echo.Context, ip, path string) ([]string, error) {
	zones, err := filepath.Glob(filepath.Join(thermalBasePath, "thermal_zone*"))
	if err != nil {
		c.LogErrorIfEnabled("Failed to glob for thermal zones", logger.String("base_path", thermalBasePath), logger.Error(err), logger.String("request_path", path), logger.String("ip", ip))
		return nil, c.HandleError(ctx, err, "Error scanning for thermal zones", http.StatusInternalServerError)
	}
	return zones, nil
}

// findValidThermalZone searches for a valid CPU thermal zone and updates response
func (c *Handler) findValidThermalZone(zones []string, response *SystemTemperature, ip, path string) {
	var lastAttemptDetails string

	for _, zonePath := range zones {
		celsius, details, isValid, err := c.checkThermalZone(zonePath, cpuThermalTypes)
		if err != nil {
			c.LogErrorIfEnabled("Unexpected error checking thermal zone", logger.String("zone", zonePath), logger.Error(err))
			continue
		}

		if isValid {
			response.Celsius = celsius
			response.IsAvailable = true
			response.SensorDetails = details
			response.Message = "CPU temperature retrieved successfully."
			c.LogInfoIfEnabled("CPU temperature retrieved successfully", logger.Float64("temperature_celsius", response.Celsius), logger.String("sensor_details", response.SensorDetails), logger.String("request_path", path), logger.String("ip", ip))
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
func (c *Handler) setTemperatureNotFoundResponse(response *SystemTemperature, lastAttemptDetails, ip, path string) {
	response.SensorDetails = lastAttemptDetails
	if lastAttemptDetails != "" {
		response.Message = fmt.Sprintf("A targeted CPU sensor was found but could not be read successfully or value was invalid. Last attempt details: %s", lastAttemptDetails)
	} else {
		response.Message = "No targeted CPU temperature sensor types (e.g., cpu-thermal, x86_pkg_temp) found or readable in available thermal zones."
	}

	c.LogInfoIfEnabled("Could not retrieve a valid CPU temperature after checking all zones.", logger.String("final_message", response.Message), logger.String("sensor_details_attempted", response.SensorDetails), logger.String("request_path", path), logger.String("ip", ip))
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

// skipFsPrefixes lists filesystem type prefixes that indicate virtual or system filesystems.
var skipFsPrefixes = [...]string{"fuse", "cgroup", "proc", "sys", "dev"}

// skipFilesystem returns true if the filesystem type should be skipped
func skipFilesystem(fstype string) bool {
	if _, exists := fsTypeCategories[fstype]; exists {
		return true
	}

	for _, prefix := range skipFsPrefixes {
		if strings.HasPrefix(fstype, prefix) {
			return true
		}
	}

	return false
}

// GetDatabaseStats handles GET /api/v2/system/database/stats
// Delegates to c.DS.GetDatabaseStats which returns the correct stats
// for the active store (legacy SQLite/MySQL or v2only.Datastore).
func (c *Handler) GetDatabaseStats(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.LogInfoIfEnabled("Getting database statistics",
		logger.String("path", path),
		logger.String("ip", ip),
	)

	ds := c.DS
	if ds == nil {
		c.LogErrorIfEnabled("Datastore not available",
			logger.String("path", path),
			logger.String("ip", ip),
		)
		return c.HandleError(ctx, fmt.Errorf("datastore not available"), "Database not configured", http.StatusServiceUnavailable)
	}

	stats, err := ds.GetDatabaseStats(ctx.Request().Context())

	// Handle errors first
	isPartialStats := false
	if err != nil {
		// If the database is not connected, log as warning and return partial stats with 200 OK
		if errors.Is(err, datastore.ErrDBNotConnected) {
			c.LogWarnIfEnabled("Database not connected, returning partial stats",
				logger.Error(err),
				logger.String("path", path),
				logger.String("ip", ip),
			)
			isPartialStats = true
			// Continue to return partial stats below
		} else {
			c.LogErrorIfEnabled("Failed to get database stats",
				logger.Error(err),
				logger.String("path", path),
				logger.String("ip", ip),
			)
			return c.HandleError(ctx, err, "Failed to retrieve database statistics", http.StatusInternalServerError)
		}
	}

	// Guard against nil stats (defensive - future implementations might return nil)
	if stats == nil {
		c.LogErrorIfEnabled("GetDatabaseStats returned nil stats",
			logger.String("path", path),
			logger.String("ip", ip),
		)
		return c.HandleError(ctx, fmt.Errorf("database stats unavailable"), "Failed to retrieve database statistics", http.StatusInternalServerError)
	}

	// Log with appropriate message based on whether stats are partial or complete
	if isPartialStats {
		c.LogInfoIfEnabled("Database statistics retrieved (partial)",
			logger.String("type", stats.Type),
			logger.Bool("connected", stats.Connected),
			logger.String("path", path),
			logger.String("ip", ip),
		)
	} else {
		c.LogInfoIfEnabled("Database statistics retrieved successfully",
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

// getV2ManagerStats retrieves v2 database statistics directly from V2Manager.
func (c *Handler) getV2ManagerStats(ctx context.Context) (*datastore.DatabaseStats, bool) {
	mgr := c.V2Manager
	if mgr == nil || !mgr.Exists() {
		return nil, false
	}

	stats := &datastore.DatabaseStats{
		Type:      datastore.DialectSQLite,
		Location:  mgr.Path(),
		Connected: true,
	}

	if mgr.IsMySQL() {
		stats.Type = datastore.DialectMySQL
	}

	db := mgr.DB()
	if db == nil {
		stats.Connected = false
		return stats, true
	}

	if !mgr.IsMySQL() {
		if err := db.WithContext(ctx).Raw("SELECT page_count * page_size FROM pragma_page_count(), pragma_page_size()").Scan(&stats.SizeBytes).Error; err != nil {
			c.LogWarnIfEnabled("Failed to get v2 SQLite database size", logger.Error(err))
		}
	} else {
		prefix := mgr.TablePrefix()
		if prefix != "" {
			// Escape underscore for MySQL LIKE (underscore is single-char wildcard)
			escaped := strings.ReplaceAll(prefix, "_", "\\_")
			if err := db.WithContext(ctx).Raw(`
				SELECT COALESCE(SUM(data_length + index_length), 0)
				FROM information_schema.TABLES
				WHERE table_schema = DATABASE() AND table_name LIKE ?
			`, escaped+"%").Scan(&stats.SizeBytes).Error; err != nil {
				c.LogWarnIfEnabled("Failed to get v2 MySQL database size", logger.Error(err))
			}
		} else {
			if err := db.WithContext(ctx).Raw(`
				SELECT COALESCE(SUM(data_length + index_length), 0)
				FROM information_schema.TABLES
				WHERE table_schema = DATABASE()
			`).Scan(&stats.SizeBytes).Error; err != nil {
				c.LogWarnIfEnabled("Failed to get v2 MySQL database size", logger.Error(err))
			}
		}
	}

	var count int64
	if err := db.WithContext(ctx).Table(mgr.TablePrefix() + "detections").Count(&count).Error; err != nil {
		c.LogWarnIfEnabled("Failed to count v2 detections", logger.Error(err))
	} else {
		stats.TotalDetections = count
	}

	return stats, true
}

// GetV2DatabaseStats handles GET /api/v2/system/database/v2/stats
func (c *Handler) GetV2DatabaseStats(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.LogInfoIfEnabled("Getting v2 database statistics",
		logger.String("path", path), logger.String("ip", ip))

	stats, ok := c.getV2ManagerStats(ctx.Request().Context())
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
)

// DownloadDatabaseBackup handles POST /api/v2/system/database/backup
// Creates a safe backup using SQLite's VACUUM INTO command.
// Uses chunked transfer encoding to keep connection alive during long VACUUM operations.
func (c *Handler) DownloadDatabaseBackup(ctx echo.Context) error {
	backupStart := time.Now()
	ip, reqPath := ctx.RealIP(), ctx.Request().URL.Path
	dbType := ctx.QueryParam("type")

	c.LogInfoIfEnabled("Database backup requested",
		logger.String("db_type", dbType),
		logger.String("path", reqPath), logger.String("ip", ip))

	// Validate dbType parameter
	if dbType != apicore.DBTypeLegacy && dbType != apicore.DBTypeV2 {
		return c.HandleError(ctx, fmt.Errorf("invalid type"),
			"Type must be 'legacy' or 'v2'", http.StatusBadRequest)
	}

	// Get database path based on type
	var dbPath string
	var gormDB *gorm.DB

	if dbType == apicore.DBTypeLegacy {
		// Check if it's SQLite
		settings := c.CurrentSettings()
		if settings == nil || settings.Output.SQLite.Path == "" {
			return c.HandleError(ctx, fmt.Errorf("not sqlite"),
				"Backup download only available for SQLite databases. Use MySQL tools for MySQL backup.",
				http.StatusBadRequest)
		}
		dbPath = settings.Output.SQLite.Path

		// Get the underlying GORM DB from the datastore
		ds := c.DS
		if ds == nil {
			return c.HandleError(ctx, fmt.Errorf("datastore not available"),
				"Database not configured", http.StatusServiceUnavailable)
		}
		sqliteStore, ok := ds.(*datastore.SQLiteStore)
		if !ok {
			return c.HandleError(ctx, fmt.Errorf("unsupported datastore type"),
				"Cannot perform backup on this datastore type", http.StatusInternalServerError)
		}
		gormDB = sqliteStore.DB
	} else {
		// V2 database
		mgr := c.V2Manager
		if mgr == nil {
			return c.HandleError(ctx, fmt.Errorf("v2 not available"),
				"V2 database not initialized", http.StatusNotFound)
		}
		if mgr.IsMySQL() {
			return c.HandleError(ctx, fmt.Errorf("not sqlite"),
				"Backup download only available for SQLite databases. Use MySQL tools for MySQL backup.",
				http.StatusBadRequest)
		}
		dbPath = mgr.Path()
		gormDB = mgr.DB()
	}

	// Verify we have a valid database handle before proceeding
	if gormDB == nil {
		return c.HandleError(ctx, fmt.Errorf("database handle not available"),
			"Database connection not initialized", http.StatusServiceUnavailable)
	}

	// Get source database size for disk space check
	fileInfo, err := os.Stat(dbPath)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get database info", http.StatusInternalServerError)
	}
	dbSize := fileInfo.Size()

	c.LogInfoIfEnabled("Database backup: source database info",
		logger.String("db_type", dbType),
		logger.String("db_path", dbPath),
		// #nosec G115 -- dbSize from os.FileInfo.Size() is always non-negative
		logger.String("db_size", apicore.FormatBytesUint64(uint64(dbSize))))

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
				apicore.FormatBytesUint64(requiredSpace), apicore.FormatBytesUint64(usage.Free)),
			http.StatusInsufficientStorage) // HTTP 507
	}

	c.LogInfoIfEnabled("Database backup: disk space check passed",
		logger.String("temp_dir", tempDir),
		logger.String("free_space", apicore.FormatBytesUint64(usage.Free)),
		logger.String("required_space", apicore.FormatBytesUint64(requiredSpace)))

	// Create temp file path for VACUUM INTO with random suffix to prevent
	// predictable filenames (symlink attacks on shared /tmp).
	timestamp := time.Now().Format("20060102-150405")
	randomBytes := make([]byte, 8)
	if _, randErr := cryptorand.Read(randomBytes); randErr != nil {
		return c.HandleError(ctx, randErr, "Failed to generate secure backup filename", http.StatusInternalServerError)
	}
	randomSuffix := "-" + hex.EncodeToString(randomBytes)
	tempPath := filepath.Join(os.TempDir(), fmt.Sprintf("birdnet-%s-backup-%s%s.db", dbType, timestamp, randomSuffix))
	filename := fmt.Sprintf("birdnet-%s-backup-%s.db", dbType, timestamp)

	// Set response headers BEFORE starting VACUUM to establish the connection.
	// This prevents connection timeout during the long VACUUM operation.
	// We use chunked transfer encoding since we don't know the final size yet.
	resp := ctx.Response()
	resp.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	resp.Header().Set("Content-Type", "application/octet-stream")
	resp.Header().Set("Transfer-Encoding", "chunked")
	resp.Header().Set("X-Content-Type-Options", "nosniff")
	resp.WriteHeader(http.StatusOK)

	// Flush headers to client immediately to establish connection
	if flusher, ok := resp.Writer.(http.Flusher); ok {
		flusher.Flush()
	}

	c.LogInfoIfEnabled("Database backup: headers sent, starting VACUUM INTO",
		logger.String("db_type", dbType),
		logger.String("temp_path", tempPath))

	// Execute VACUUM INTO for safe, consistent backup.
	// Escape single quotes in tempPath to prevent SQL injection via crafted temp paths.
	escapedTempPath := strings.ReplaceAll(tempPath, "'", "''")
	vacuumSQL := fmt.Sprintf("VACUUM INTO '%s'", escapedTempPath)
	vacuumStart := time.Now()

	vacuumErr := gormDB.Exec(vacuumSQL).Error
	vacuumDuration := time.Since(vacuumStart)

	if vacuumErr != nil {
		c.LogWarnIfEnabled("Database backup: VACUUM INTO failed",
			logger.String("db_type", dbType),
			logger.Duration("duration", vacuumDuration),
			logger.Error(vacuumErr))
		// Headers already sent, can't return error response - just log and close
		return vacuumErr
	}

	// Ensure cleanup of temp file after response is sent
	defer func() {
		if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
			c.LogWarnIfEnabled("Failed to cleanup backup temp file",
				logger.String("path", tempPath), logger.Error(err))
		} else {
			c.LogInfoIfEnabled("Database backup: temp file cleaned up",
				logger.String("path", tempPath))
		}
	}()

	// Get backup file size
	backupInfo, statErr := os.Stat(tempPath)
	backupSize := "unknown"
	var backupSizeBytes int64
	if statErr == nil {
		backupSizeBytes = backupInfo.Size()
		// #nosec G115 -- backupSizeBytes from os.FileInfo.Size() is always non-negative
		backupSize = apicore.FormatBytesUint64(uint64(backupSizeBytes))
	}

	c.LogInfoIfEnabled("Database backup: VACUUM INTO completed, streaming file",
		logger.String("db_type", dbType),
		logger.Duration("vacuum_duration", vacuumDuration),
		logger.String("backup_size", backupSize))

	// Open and stream the backup file
	//#nosec G304 -- tempPath is constructed from trusted sources (os.TempDir + controlled filename)
	backupFile, err := os.Open(tempPath)
	if err != nil {
		c.LogWarnIfEnabled("Database backup: failed to open backup file",
			logger.String("path", tempPath),
			logger.Error(err))
		return err
	}
	defer func() {
		if err := backupFile.Close(); err != nil {
			c.LogWarnIfEnabled("Database backup: failed to close backup file", logger.Error(err))
		}
	}()

	// Stream the file to the response
	written, copyErr := io.Copy(resp.Writer, backupFile)

	totalDuration := time.Since(backupStart)
	if copyErr != nil {
		c.LogWarnIfEnabled("Database backup: file transfer failed",
			logger.String("db_type", dbType),
			logger.String("filename", filename),
			logger.Int64("bytes_written", written),
			logger.Duration("total_duration", totalDuration),
			logger.Error(copyErr))
		return copyErr
	}

	c.LogInfoIfEnabled("Database backup: completed successfully",
		logger.String("db_type", dbType),
		logger.String("filename", filename),
		logger.String("backup_size", backupSize),
		logger.Int64("bytes_sent", written),
		logger.Duration("vacuum_duration", vacuumDuration),
		logger.Duration("total_duration", totalDuration),
		logger.String("ip", ip))

	return nil
}
