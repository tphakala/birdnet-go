// Package monitor provides system resource metric collection.
// Thresholds and notifications are managed by the alerting engine rules.
package monitor

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/tphakala/birdnet-go/internal/alerting"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// GetLogger returns the module logger for system monitor
func GetLogger() logger.Logger {
	return logger.Global().Module("monitor")
}

// ResourceType represents the type of system resource being monitored
type ResourceType string

const (
	ResourceCPU    ResourceType = "cpu"
	ResourceMemory ResourceType = "memory"
	ResourceDisk   ResourceType = "disk"
)

const bytesPerGB = 1024 * 1024 * 1024

// SystemMonitor collects system resource metrics and publishes them
// to the alerting engine via alerting.TryPublish().
type SystemMonitor struct {
	config   *conf.Settings
	interval time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	log      logger.Logger
}

// NewSystemMonitor creates a new system monitor instance
func NewSystemMonitor(config *conf.Settings) *SystemMonitor {
	ctx, cancel := context.WithCancel(context.Background())

	// Default check interval if not configured
	interval := 30 * time.Second
	if config.Realtime.Monitoring.CheckInterval > 0 {
		interval = time.Duration(config.Realtime.Monitoring.CheckInterval) * time.Second
	}

	// Auto-append critical paths if disk monitoring is enabled
	if config.Realtime.Monitoring.Disk.Enabled {
		// Get information about paths
		userConfigured, autoDetected, merged := GetMonitoringPathsInfo(config)

		// Update the runtime configuration with merged paths
		config.Realtime.Monitoring.Disk.Paths = merged

		// Log detailed information about path monitoring
		GetLogger().Info("Disk monitoring paths configured",
			logger.Any("user_configured", userConfigured),
			logger.Any("auto_detected", autoDetected),
			logger.Any("total_monitored", merged),
			logger.String("note", "Auto-detected paths are added at runtime only"),
		)

		// If there are auto-detected paths, provide guidance
		if len(autoDetected) > 0 && len(userConfigured) == 0 {
			GetLogger().Info("To persist auto-detected paths, add them to your config.yaml under realtime.monitoring.disk.paths")
		}
	}

	monitor := &SystemMonitor{
		config:   config,
		interval: interval,
		ctx:      ctx,
		cancel:   cancel,
		log:      GetLogger(),
	}

	monitor.log.Info("System monitor instance created",
		logger.Bool("enabled", config.Realtime.Monitoring.Enabled),
		logger.Duration("interval", interval),
		logger.Bool("cpu_enabled", config.Realtime.Monitoring.CPU.Enabled),
		logger.Bool("memory_enabled", config.Realtime.Monitoring.Memory.Enabled),
		logger.Bool("disk_enabled", config.Realtime.Monitoring.Disk.Enabled),
		logger.Any("disk_paths", config.Realtime.Monitoring.Disk.Paths),
	)

	return monitor
}

// Start begins monitoring system resources
func (m *SystemMonitor) Start() {
	m.log.Info("Start() called",
		logger.Bool("monitoring_enabled", m.config.Realtime.Monitoring.Enabled),
	)

	if !m.config.Realtime.Monitoring.Enabled {
		m.log.Warn("System monitoring is disabled in configuration",
			logger.String("config_path", "realtime.monitoring.enabled"),
			logger.String("suggestion", "Set 'realtime.monitoring.enabled: true' in your configuration to enable monitoring"),
		)
		return
	}

	m.log.Info("Starting system resource monitoring",
		logger.Duration("interval", m.interval),
	)

	m.wg.Add(1)
	go m.monitorLoop()
	m.log.Info("Monitor goroutine started")
}

// Stop stops the system monitor
func (m *SystemMonitor) Stop() {
	m.log.Info("Stopping system resource monitoring")
	m.cancel()
	m.wg.Wait()
}

// monitorLoop is the main monitoring loop
func (m *SystemMonitor) monitorLoop() {
	defer m.wg.Done()

	m.log.Info("System monitor loop started", logger.Duration("check_interval", m.interval))

	// Perform initial check
	m.checkAllResources()

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.checkAllResources()
		case <-m.ctx.Done():
			m.log.Info("System monitor loop stopping")
			return
		}
	}
}

// checkAllResources checks all monitored resources
func (m *SystemMonitor) checkAllResources() {
	m.log.Debug("Starting resource checks",
		logger.Bool("cpu_enabled", m.config.Realtime.Monitoring.CPU.Enabled),
		logger.Bool("memory_enabled", m.config.Realtime.Monitoring.Memory.Enabled),
		logger.Bool("disk_enabled", m.config.Realtime.Monitoring.Disk.Enabled),
	)

	if m.config.Realtime.Monitoring.CPU.Enabled {
		m.checkCPU()
	}

	if m.config.Realtime.Monitoring.Memory.Enabled {
		m.checkMemory()
	}

	if m.config.Realtime.Monitoring.Disk.Enabled {
		m.checkDisk()
	} else {
		m.log.Debug("Disk monitoring is disabled")
	}

	m.log.Debug("Completed resource checks")
}

// checkCPU monitors CPU usage and publishes the metric
func (m *SystemMonitor) checkCPU() {
	// Get CPU usage percentage with 0 interval for instant reading
	cpuPercent, err := cpu.Percent(0, false)
	if err != nil {
		m.log.Error("Failed to get CPU usage", logger.Error(err))
		return
	}

	if len(cpuPercent) == 0 {
		return
	}

	usage := cpuPercent[0]

	alerting.TryPublish(&alerting.AlertEvent{
		ObjectType: alerting.ObjectTypeSystem,
		MetricName: alerting.MetricCPUUsage,
		Properties: map[string]any{alerting.PropertyValue: usage},
	})
}

// checkMemory monitors memory usage and publishes the metric
func (m *SystemMonitor) checkMemory() {
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		m.log.Error("Failed to get memory info", logger.Error(err))
		return
	}

	alerting.TryPublish(&alerting.AlertEvent{
		ObjectType: alerting.ObjectTypeSystem,
		MetricName: alerting.MetricMemoryUsage,
		Properties: map[string]any{alerting.PropertyValue: memInfo.UsedPercent},
	})
}

// checkDisk monitors disk usage for all configured paths, grouped by mount point
func (m *SystemMonitor) checkDisk() {
	paths := m.config.Realtime.Monitoring.Disk.Paths
	if len(paths) == 0 {
		paths = []string{"/"}
	}

	m.log.Debug("Starting disk usage checks", logger.Any("paths", paths))

	// Group paths by mount point to avoid duplicate metrics
	groups, err := groupPathsByMountPoint(paths)
	if err != nil {
		m.log.Error("Failed to group paths by mount point", logger.Error(err))
		// Fall back to checking each path individually
		for _, path := range paths {
			m.checkDiskPath(path)
		}
		return
	}

	m.log.Debug("Grouped paths by mount point",
		logger.Int("original_paths", len(paths)),
		logger.Int("mount_groups", len(groups)),
	)

	for _, group := range groups {
		m.checkDiskGroup(group)
	}
}

// checkDiskGroup monitors disk usage for a group of paths sharing a mount point
func (m *SystemMonitor) checkDiskGroup(group MountGroup) {
	m.log.Debug("Checking disk group",
		logger.String("mount_point", group.MountPoint),
		logger.Any("paths", group.Paths),
	)

	// Validate mount point exists
	if _, err := os.Stat(group.MountPoint); err != nil {
		m.log.Error("Mount point does not exist or is not accessible",
			logger.String("mount_point", group.MountPoint),
			logger.Error(err),
		)
		return
	}

	usage, err := disk.Usage(group.MountPoint)
	if err != nil {
		m.log.Error("Failed to get disk usage",
			logger.Error(err),
			logger.String("mount_point", group.MountPoint),
		)
		return
	}

	m.log.Debug("Disk usage check completed",
		logger.String("mount_point", group.MountPoint),
		logger.Any("affected_paths", group.Paths),
		logger.String("total_gb", fmt.Sprintf("%.2f", float64(usage.Total)/bytesPerGB)),
		logger.String("used_gb", fmt.Sprintf("%.2f", float64(usage.Used)/bytesPerGB)),
		logger.String("free_gb", fmt.Sprintf("%.2f", float64(usage.Free)/bytesPerGB)),
		logger.String("used_percent", fmt.Sprintf("%.2f%%", usage.UsedPercent)),
		logger.String("filesystem", usage.Fstype),
	)

	alerting.TryPublish(&alerting.AlertEvent{
		ObjectType: alerting.ObjectTypeSystem,
		MetricName: alerting.MetricDiskUsage,
		Properties: map[string]any{
			alerting.PropertyValue: usage.UsedPercent,
			alerting.PropertyPath:  group.MountPoint,
		},
	})
}

// checkDiskPath monitors disk usage for a single path (fallback when mount grouping fails)
func (m *SystemMonitor) checkDiskPath(path string) {
	m.log.Debug("Starting disk usage check", logger.String("path", path))

	if _, err := os.Stat(path); err != nil {
		m.log.Error("Disk monitoring path does not exist or is not accessible",
			logger.String("path", path),
			logger.Error(err),
		)
		return
	}

	usage, err := disk.Usage(path)
	if err != nil {
		m.log.Error("Failed to get disk usage", logger.Error(err), logger.String("path", path))
		return
	}

	m.log.Debug("Disk usage check completed",
		logger.String("path", path),
		logger.String("total_gb", fmt.Sprintf("%.2f", float64(usage.Total)/bytesPerGB)),
		logger.String("used_gb", fmt.Sprintf("%.2f", float64(usage.Used)/bytesPerGB)),
		logger.String("free_gb", fmt.Sprintf("%.2f", float64(usage.Free)/bytesPerGB)),
		logger.String("used_percent", fmt.Sprintf("%.2f%%", usage.UsedPercent)),
		logger.String("filesystem", usage.Fstype),
	)

	alerting.TryPublish(&alerting.AlertEvent{
		ObjectType: alerting.ObjectTypeSystem,
		MetricName: alerting.MetricDiskUsage,
		Properties: map[string]any{
			alerting.PropertyValue: usage.UsedPercent,
			alerting.PropertyPath:  path,
		},
	})
}

// GetMonitoredPaths returns the list of paths being monitored for disk usage
func (m *SystemMonitor) GetMonitoredPaths() []string {
	if m.config.Realtime.Monitoring.Disk.Enabled {
		return m.config.Realtime.Monitoring.Disk.Paths
	}
	return nil
}
