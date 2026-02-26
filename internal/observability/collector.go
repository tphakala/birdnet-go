package observability

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/tphakala/birdnet-go/internal/datastore/dbstats"
)

// CPUUsageFunc is a function that returns the current total CPU usage percentage.
// This allows the collector to reuse the existing CPUCache from the API package
// instead of making a conflicting concurrent cpu.Percent call.
type CPUUsageFunc func() float64

// Collector periodically samples system metrics and records them into a MetricsStore.
type Collector struct {
	store    MetricsStore
	interval time.Duration
	cpuFunc  CPUUsageFunc

	// Internal state for disk I/O delta computation
	prevDiskIO   map[string]disk.IOCountersStat
	prevDiskTime time.Time

	// Database latency tracking (optional, set via SetDBCounters)
	dbCounters *dbstats.Counters
	prevDBSnap *dbstats.Snapshot

	// Track which metrics have had logged errors to avoid log spam
	loggedErrors map[string]bool
}

// NewCollector creates a Collector that samples system metrics at the given interval
// and stores them in the provided MetricsStore.
// The cpuFunc should return the current total CPU usage percentage (e.g. from CPUCache).
// Panics if store is nil or interval is non-positive.
func NewCollector(store MetricsStore, interval time.Duration, cpuFunc CPUUsageFunc) *Collector {
	if store == nil {
		panic("observability: NewCollector requires a non-nil MetricsStore")
	}
	if interval <= 0 {
		panic("observability: NewCollector requires a positive interval")
	}
	return &Collector{
		store:        store,
		interval:     interval,
		cpuFunc:      cpuFunc,
		prevDiskIO:   make(map[string]disk.IOCountersStat),
		loggedErrors: make(map[string]bool),
	}
}

// Start runs the collection loop until the context is cancelled.
// It should be called in a goroutine.
func (c *Collector) Start(ctx context.Context) {
	// Collect immediately on start, then on each tick
	c.collect()

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.collect()
		}
	}
}

// Metric key constants for collected system metrics.
const (
	// expectedMetricCount is the pre-allocation hint for the number of metrics collected per tick.
	expectedMetricCount = 8

	metricCPUTotal          = "cpu.total"
	metricMemoryUsedPercent = "memory.used_percent"
	metricCPUTemperature    = "cpu.temperature"
	metricDiskUsedPercent   = "disk.used_percent.%s"
	metricDiskIORead        = "disk.io.read.%s"
	metricDiskIOWrite       = "disk.io.write.%s"
	metricDBReadLatency     = "db.read_latency_ms"
	metricDBWriteLatency    = "db.write_latency_ms"
	metricDBReadLatencyMax  = "db.read_latency_max_ms"
	metricDBWriteLatencyMax = "db.write_latency_max_ms"
	metricDBQueriesPerSec   = "db.queries_per_sec"

	// maxValidCelsius is the upper bound for valid CPU temperature readings.
	// 120°C captures overheating events before thermal shutdown while filtering bogus values.
	maxValidCelsius = 120.0
)

// collect gathers all system metrics and records them as a single batch.
func (c *Collector) collect() {
	points := make(map[string]float64, expectedMetricCount)

	c.collectCPU(points)
	c.collectMemory(points)
	c.collectTemperature(points)
	c.collectDisk(points)
	c.collectDatabase(points)

	if len(points) > 0 {
		c.store.RecordBatch(points)
	}
}

// collectCPU reads CPU usage from the injected function.
func (c *Collector) collectCPU(points map[string]float64) {
	if c.cpuFunc != nil {
		points[metricCPUTotal] = c.cpuFunc()
	}
}

// collectMemory reads memory usage via gopsutil.
func (c *Collector) collectMemory(points map[string]float64) {
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		c.logOnce("memory", "failed to collect memory metrics: %v", err)
		return
	}
	points[metricMemoryUsedPercent] = memInfo.UsedPercent
}

// collectTemperature reads CPU temperature from Linux thermal zones.
// Gracefully skipped on non-Linux or if no suitable sensor is found.
func (c *Collector) collectTemperature(points map[string]float64) {
	temp, ok := readCPUTemperature()
	if ok {
		points[metricCPUTemperature] = temp
	}
}

// collectDisk reads disk usage and I/O statistics via gopsutil.
func (c *Collector) collectDisk(points map[string]float64) {
	c.collectDiskUsage(points)
	c.collectDiskIO(points)
}

// collectDiskUsage reads disk usage percentages for each partition.
func (c *Collector) collectDiskUsage(points map[string]float64) {
	partitions, err := disk.Partitions(false)
	if err != nil {
		c.logOnce("disk_partitions", "failed to list disk partitions: %v", err)
		return
	}

	for i := range partitions {
		p := &partitions[i]
		if skipCollectorFS(p.Fstype) {
			continue
		}
		usage, err := disk.Usage(p.Mountpoint)
		if err != nil {
			continue // skip individual mount failures silently
		}
		key := fmt.Sprintf(metricDiskUsedPercent, sanitizeMountpoint(p.Mountpoint))
		points[key] = usage.UsedPercent
	}
}

// collectDiskIO computes disk I/O rates (bytes/sec) as deltas between ticks.
func (c *Collector) collectDiskIO(points map[string]float64) {
	counters, err := disk.IOCounters()
	if err != nil {
		c.logOnce("disk_io", "failed to read disk I/O counters: %v", err)
		return
	}

	now := time.Now()
	if !c.prevDiskTime.IsZero() {
		elapsed := now.Sub(c.prevDiskTime).Seconds()
		if elapsed > 0 {
			for device := range counters {
				counter := counters[device]
				prev, ok := c.prevDiskIO[device]
				if !ok {
					continue
				}
				// Sanitize device name to remove any path prefixes (platform-dependent)
				name := filepath.Base(device)
				// Guard against counter resets (device swap, kernel rollover)
				if counter.ReadBytes >= prev.ReadBytes {
					readRate := float64(counter.ReadBytes-prev.ReadBytes) / elapsed
					points[fmt.Sprintf(metricDiskIORead, name)] = readRate
				}
				if counter.WriteBytes >= prev.WriteBytes {
					writeRate := float64(counter.WriteBytes-prev.WriteBytes) / elapsed
					points[fmt.Sprintf(metricDiskIOWrite, name)] = writeRate
				}
			}
		}
	}

	c.prevDiskIO = counters
	c.prevDiskTime = now
}

// SetDBCounters sets the database atomic counters for latency tracking.
// Must be called before Start. If not called, database metrics are skipped.
func (c *Collector) SetDBCounters(counters *dbstats.Counters) {
	c.dbCounters = counters
}

// usToMs converts microseconds to milliseconds.
const usToMs = 1000.0

// collectDatabase computes database latency and throughput metrics from
// atomic counter snapshots. Requires two consecutive snapshots for deltas.
func (c *Collector) collectDatabase(points map[string]float64) {
	if c.dbCounters == nil {
		return
	}

	snap := c.dbCounters.Snapshot()

	// Max values are reset-on-read from Snapshot(), always record them
	// (even on the first tick when prevDBSnap is nil)
	points[metricDBReadLatencyMax] = float64(snap.ReadMaxUs) / usToMs
	points[metricDBWriteLatencyMax] = float64(snap.WriteMaxUs) / usToMs

	if c.prevDBSnap != nil {
		elapsed := snap.CollectedAt.Sub(c.prevDBSnap.CollectedAt).Seconds()
		if elapsed > 0 {
			deltaReads := snap.ReadCount - c.prevDBSnap.ReadCount
			deltaWrites := snap.WriteCount - c.prevDBSnap.WriteCount

			if deltaReads > 0 {
				deltaUs := snap.ReadTotalUs - c.prevDBSnap.ReadTotalUs
				points[metricDBReadLatency] = float64(deltaUs) / float64(deltaReads) / usToMs
			}
			if deltaWrites > 0 {
				deltaUs := snap.WriteTotalUs - c.prevDBSnap.WriteTotalUs
				points[metricDBWriteLatency] = float64(deltaUs) / float64(deltaWrites) / usToMs
			}

			points[metricDBQueriesPerSec] = float64(deltaReads+deltaWrites) / elapsed
		}
	}

	c.prevDBSnap = &snap
}

// logOnce logs a message for a metric category only on the first occurrence.
func (c *Collector) logOnce(category, format string, args ...any) {
	if c.loggedErrors[category] {
		return
	}
	c.loggedErrors[category] = true
	GetLogger().Debug(fmt.Sprintf(format, args...))
}

// sanitizeMountpoint converts a mountpoint path to a metric-safe name.
// Unix:    "/" -> "root", "/home" -> "home", "/mnt/data" -> "mnt-data"
// Windows: "C:\" -> "C", "C:\Users" -> "C-Users"
func sanitizeMountpoint(mount string) string {
	if mount == "/" {
		return "root"
	}
	// Remove leading slash, replace remaining slashes with dashes
	name := strings.TrimPrefix(mount, "/")
	// Handle Windows drive letters and backslashes
	name = strings.ReplaceAll(name, `\`, "-")
	name = strings.ReplaceAll(name, ":", "")
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.TrimRight(name, "-")
	return name
}

// skipCollectorFSTypes contains virtual/pseudo filesystem types that should not be tracked.
var skipCollectorFSTypes = map[string]bool{
	"sysfs": true, "proc": true, "procfs": true, "devfs": true,
	"devtmpfs": true, "debugfs": true, "securityfs": true, "tmpfs": true,
	"ramfs": true, "overlay": true, "overlayfs": true, "fusectl": true,
	"devpts": true, "hugetlbfs": true, "mqueue": true, "cgroup": true,
	"cgroupfs": true, "pstore": true, "binfmt_misc": true, "bpf": true,
	"tracefs": true, "configfs": true, "autofs": true, "efivarfs": true,
}

// skipCollectorFS returns true for virtual/pseudo filesystem types that should not be tracked.
func skipCollectorFS(fstype string) bool {
	return skipCollectorFSTypes[fstype]
}

// --- CPU Temperature reading (Linux-specific) ---

// thermalBasePath is the base directory for thermal zones on Linux.
const collectorThermalBasePath = "/sys/class/thermal/"

// cpuThermalSensorTypes contains sensor type names that indicate CPU temperature.
var cpuThermalSensorTypes = map[string]bool{
	"cpu-thermal":     true,
	"x86_pkg_temp":    true,
	"soc_thermal":     true,
	"cpu_thermal":     true,
	"thermal-fan-est": true,
}

// readCPUTemperature scans Linux thermal zones for a CPU temperature sensor.
// Returns the temperature in Celsius and true if found, or 0 and false otherwise.
func readCPUTemperature() (float64, bool) {
	zones, err := filepath.Glob(filepath.Join(collectorThermalBasePath, "thermal_zone*"))
	if err != nil || len(zones) == 0 {
		return 0, false
	}

	// Sort for deterministic sensor selection on systems with multiple thermal zones.
	slices.Sort(zones)

	for _, zone := range zones {
		temp, ok := readThermalZone(zone)
		if ok {
			return temp, true
		}
	}
	return 0, false
}

// readThermalZone reads a single thermal zone and returns its temperature
// if it matches a CPU thermal sensor type and has a valid reading.
func readThermalZone(zonePath string) (float64, bool) {
	// Read sensor type — paths are from filepath.Glob on /sys/class/thermal/, not user input.
	typeData, err := os.ReadFile(filepath.Join(zonePath, "type")) //nolint:gosec // system path from Glob
	if err != nil {
		return 0, false
	}
	sensorType := strings.ToLower(strings.TrimSpace(string(typeData)))
	if !cpuThermalSensorTypes[sensorType] {
		return 0, false
	}

	// Read temperature (in millidegrees Celsius)
	tempData, err := os.ReadFile(filepath.Join(zonePath, "temp")) //nolint:gosec // system path from Glob
	if err != nil {
		return 0, false
	}
	milliCelsius, err := strconv.Atoi(strings.TrimSpace(string(tempData)))
	if err != nil {
		return 0, false
	}

	const milliToUnit = 1000.0
	celsius := float64(milliCelsius) / milliToUnit

	if celsius < 0 || celsius > maxValidCelsius {
		return 0, false
	}
	return celsius, true
}
