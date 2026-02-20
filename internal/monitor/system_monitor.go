// Package monitor provides system resource monitoring with threshold-based notifications
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
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
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

// AlertLevel constants for recovery notifications
const (
	alertLevelCritical = "critical"
	alertLevelWarning  = "warning"
)

// Default configuration values
const (
	defaultCriticalResendInterval = 30 * time.Minute
	defaultHysteresisPercent      = 5.0
	stateKeySeparator             = "|"
	bytesPerGB                    = 1024 * 1024 * 1024
)

// AlertState tracks the current alert state for a resource
type AlertState struct {
	InWarning            bool
	InCritical           bool
	LastValue            float64
	LastCheck            time.Time
	LastNotificationID   string    // ID of the last notification sent
	LastNotificationTime time.Time // When the last notification was sent
	CriticalStartTime    time.Time // When resource first entered critical state
}

// SystemMonitor monitors system resources and sends notifications when thresholds are exceeded
type SystemMonitor struct {
	config         *conf.Settings
	interval       time.Duration
	alertStates    map[string]*AlertState
	validatedPaths map[string]bool // Cache for validated disk paths
	mu             sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	log            logger.Logger
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

	// Use the package-level logger instead of creating a new one
	monitor := &SystemMonitor{
		config:         config,
		interval:       interval,
		alertStates:    make(map[string]*AlertState),
		validatedPaths: make(map[string]bool),
		ctx:            ctx,
		cancel:         cancel,
		log:            GetLogger(), // Use package-level logger
	}

	// Log creation using the cached logger
	monitor.log.Info("System monitor instance created",
		logger.Bool("enabled", config.Realtime.Monitoring.Enabled),
		logger.Duration("interval", interval),
		logger.Bool("cpu_enabled", config.Realtime.Monitoring.CPU.Enabled),
		logger.Bool("memory_enabled", config.Realtime.Monitoring.Memory.Enabled),
		logger.Bool("disk_enabled", config.Realtime.Monitoring.Disk.Enabled),
		logger.Any("disk_paths", config.Realtime.Monitoring.Disk.Paths),
		logger.Float64("disk_warning", config.Realtime.Monitoring.Disk.Warning),
		logger.Float64("disk_critical", config.Realtime.Monitoring.Disk.Critical),
	)

	return monitor
}

// Start begins monitoring system resources
func (m *SystemMonitor) Start() {
	// Log monitoring state using cached logger
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
		logger.Float64("cpu_warning", m.config.Realtime.Monitoring.CPU.Warning),
		logger.Float64("cpu_critical", m.config.Realtime.Monitoring.CPU.Critical),
		logger.Float64("memory_warning", m.config.Realtime.Monitoring.Memory.Warning),
		logger.Float64("memory_critical", m.config.Realtime.Monitoring.Memory.Critical),
		logger.Float64("disk_warning", m.config.Realtime.Monitoring.Disk.Warning),
		logger.Float64("disk_critical", m.config.Realtime.Monitoring.Disk.Critical),
		logger.Any("disk_paths", m.config.Realtime.Monitoring.Disk.Paths),
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

	// Check CPU usage
	if m.config.Realtime.Monitoring.CPU.Enabled {
		m.checkCPU()
	}

	// Check memory usage
	if m.config.Realtime.Monitoring.Memory.Enabled {
		m.checkMemory()
	}

	// Check disk usage
	if m.config.Realtime.Monitoring.Disk.Enabled {
		m.checkDisk()
	} else {
		m.log.Debug("Disk monitoring is disabled")
	}

	m.log.Debug("Completed resource checks")
}

// checkCPU monitors CPU usage
func (m *SystemMonitor) checkCPU() {
	// Get CPU usage percentage with 0 interval for instant reading
	// This is less accurate than a 1-second sample but doesn't block
	cpuPercent, err := cpu.Percent(0, false)
	if err != nil {
		m.log.Error("Failed to get CPU usage", logger.Error(err))
		return
	}

	if len(cpuPercent) == 0 {
		return
	}

	usage := cpuPercent[0]

	// Publish CPU metric to alert engine for sustained threshold detection
	alerting.TryPublish(&alerting.AlertEvent{
		ObjectType: alerting.ObjectTypeSystem,
		MetricName: alerting.MetricCPUUsage,
		Properties: map[string]any{alerting.PropertyValue: usage},
	})

	m.checkThresholds(ResourceCPU, usage,
		m.config.Realtime.Monitoring.CPU.Warning,
		m.config.Realtime.Monitoring.CPU.Critical)
}

// checkMemory monitors memory usage
func (m *SystemMonitor) checkMemory() {
	// Get memory info
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		m.log.Error("Failed to get memory info", logger.Error(err))
		return
	}

	// Publish memory metric to alert engine for sustained threshold detection
	alerting.TryPublish(&alerting.AlertEvent{
		ObjectType: alerting.ObjectTypeSystem,
		MetricName: alerting.MetricMemoryUsage,
		Properties: map[string]any{alerting.PropertyValue: memInfo.UsedPercent},
	})

	m.checkThresholds(ResourceMemory, memInfo.UsedPercent,
		m.config.Realtime.Monitoring.Memory.Warning,
		m.config.Realtime.Monitoring.Memory.Critical)
}

// checkDisk monitors disk usage for all configured paths, grouped by mount point
func (m *SystemMonitor) checkDisk() {
	// Get configured paths or default to root
	paths := m.config.Realtime.Monitoring.Disk.Paths
	if len(paths) == 0 {
		paths = []string{"/"}
	}

	m.log.Debug("Starting disk usage checks", logger.Any("paths", paths))

	// Group paths by mount point to avoid duplicate notifications
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

	// Check each mount group
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
	m.mu.RLock()
	validated, exists := m.validatedPaths[group.MountPoint]
	m.mu.RUnlock()

	if !exists || !validated {
		if _, err := os.Stat(group.MountPoint); err != nil {
			m.log.Error("Mount point does not exist or is not accessible",
				logger.String("mount_point", group.MountPoint),
				logger.Error(err),
			)
			m.mu.Lock()
			m.validatedPaths[group.MountPoint] = false
			m.mu.Unlock()
			return
		}
		m.mu.Lock()
		m.validatedPaths[group.MountPoint] = true
		m.mu.Unlock()
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
		logger.String("warning_threshold", fmt.Sprintf("%.2f%%", m.config.Realtime.Monitoring.Disk.Warning)),
		logger.String("critical_threshold", fmt.Sprintf("%.2f%%", m.config.Realtime.Monitoring.Disk.Critical)),
	)

	// Publish disk metric to alert engine for sustained threshold detection
	alerting.TryPublish(&alerting.AlertEvent{
		ObjectType: alerting.ObjectTypeSystem,
		MetricName: alerting.MetricDiskUsage,
		Properties: map[string]any{alerting.PropertyValue: usage.UsedPercent},
	})

	m.checkThresholdsWithGroup(ResourceDisk, usage.UsedPercent,
		m.config.Realtime.Monitoring.Disk.Warning,
		m.config.Realtime.Monitoring.Disk.Critical, group)
}

// checkDiskPath monitors disk usage for a single path
func (m *SystemMonitor) checkDiskPath(path string) {
	m.log.Debug("Starting disk usage check", logger.String("path", path))

	// Check if path is already validated
	m.mu.RLock()
	validated, exists := m.validatedPaths[path]
	m.mu.RUnlock()

	if !exists || !validated {
		// Verify the path exists
		if _, err := os.Stat(path); err != nil {
			m.log.Error("Disk monitoring path does not exist or is not accessible",
				logger.String("path", path),
				logger.Error(err),
			)
			// Mark as validated (even if invalid) to avoid repeated checks
			m.mu.Lock()
			m.validatedPaths[path] = false
			m.mu.Unlock()
			return
		}
		// Mark as validated
		m.mu.Lock()
		m.validatedPaths[path] = true
		m.mu.Unlock()
	}

	usage, err := disk.Usage(path)
	if err != nil {
		m.log.Error("Failed to get disk usage", logger.Error(err), logger.String("path", path))
		return
	}

	// Log detailed disk information
	m.log.Debug("Disk usage check completed",
		logger.String("path", path),
		logger.String("total_gb", fmt.Sprintf("%.2f", float64(usage.Total)/bytesPerGB)),
		logger.String("used_gb", fmt.Sprintf("%.2f", float64(usage.Used)/bytesPerGB)),
		logger.String("free_gb", fmt.Sprintf("%.2f", float64(usage.Free)/bytesPerGB)),
		logger.String("used_percent", fmt.Sprintf("%.2f%%", usage.UsedPercent)),
		logger.String("filesystem", usage.Fstype),
		logger.String("warning_threshold", fmt.Sprintf("%.2f%%", m.config.Realtime.Monitoring.Disk.Warning)),
		logger.String("critical_threshold", fmt.Sprintf("%.2f%%", m.config.Realtime.Monitoring.Disk.Critical)),
	)

	// Publish disk metric to alert engine for sustained threshold detection
	alerting.TryPublish(&alerting.AlertEvent{
		ObjectType: alerting.ObjectTypeSystem,
		MetricName: alerting.MetricDiskUsage,
		Properties: map[string]any{alerting.PropertyValue: usage.UsedPercent},
	})

	m.checkThresholdsWithPath(ResourceDisk, usage.UsedPercent,
		m.config.Realtime.Monitoring.Disk.Warning,
		m.config.Realtime.Monitoring.Disk.Critical, path)
}

// checkThresholds evaluates resource usage against configured thresholds
func (m *SystemMonitor) checkThresholds(resource ResourceType, current, warningThreshold, criticalThreshold float64) {
	m.checkThresholdsWithPath(resource, current, warningThreshold, criticalThreshold, "")
}

// checkThresholdsWithPath evaluates resource usage against configured thresholds with optional path
func (m *SystemMonitor) checkThresholdsWithPath(resource ResourceType, current, warningThreshold, criticalThreshold float64, path string) {
	// Create state key that includes path for disk resources
	stateKey := string(resource)
	if resource == ResourceDisk && path != "" {
		stateKey = fmt.Sprintf("%s"+stateKeySeparator+"%s", resource, path)
	}

	m.mu.Lock()
	state, exists := m.alertStates[stateKey]
	if !exists {
		state = &AlertState{}
		m.alertStates[stateKey] = state
	}
	m.mu.Unlock()

	// Update current value and check time
	state.LastValue = current
	state.LastCheck = time.Now()

	// Check thresholds
	switch {
	case current >= criticalThreshold:
		if !state.InCritical {
			// First time entering critical state
			m.log.Warn("Critical threshold exceeded",
				logger.String("resource", string(resource)),
				logger.String("path", path),
				logger.String("current", fmt.Sprintf("%.2f%%", current)),
				logger.String("threshold", fmt.Sprintf("%.2f%%", criticalThreshold)),
			)
			m.sendNotificationWithPath(resource, current, criticalThreshold, notification.PriorityCritical, state, path)
			state.InCritical = true
			state.InWarning = true // Critical implies warning
			state.CriticalStartTime = time.Now()
		} else {
			// Still in critical state - check if we need to resend notification
			resendInterval := time.Duration(m.config.Realtime.Monitoring.CriticalResendInterval) * time.Minute
			if resendInterval == 0 {
				resendInterval = defaultCriticalResendInterval // Default fallback
			}
			if resource == ResourceDisk && time.Since(state.LastNotificationTime) > resendInterval {
				m.log.Info("Resending critical disk notification after expiry",
					logger.String("resource", string(resource)),
					logger.String("current", fmt.Sprintf("%.2f%%", current)),
					logger.String("last_notification", state.LastNotificationTime.Format(time.RFC3339)),
				)
				m.sendNotificationWithPath(resource, current, criticalThreshold, notification.PriorityCritical, state, path)
			} else {
				m.log.Debug("Resource still in critical state",
					logger.String("resource", string(resource)),
					logger.String("current", fmt.Sprintf("%.2f%%", current)),
				)
			}
		}
	case current >= warningThreshold:
		// Check warning threshold
		if !state.InWarning {
			m.log.Warn("Warning threshold exceeded",
				logger.String("resource", string(resource)),
				logger.String("current", fmt.Sprintf("%.2f%%", current)),
				logger.String("threshold", fmt.Sprintf("%.2f%%", warningThreshold)),
			)
			m.sendNotificationWithPath(resource, current, warningThreshold, notification.PriorityHigh, state, path)
			state.InWarning = true
		} else {
			m.log.Debug("Resource still in warning state",
				logger.String("resource", string(resource)),
				logger.String("current", fmt.Sprintf("%.2f%%", current)),
			)
		}
		// Clear critical if we're below critical threshold (with hysteresis)
		hysteresis := m.config.Realtime.Monitoring.HysteresisPercent
		if hysteresis == 0 {
			hysteresis = defaultHysteresisPercent
		}
		if state.InCritical && current < (criticalThreshold-hysteresis) {
			m.sendRecoveryNotificationWithPath(resource, current, alertLevelCritical, state, path)
			state.InCritical = false
			state.CriticalStartTime = time.Time{} // Reset
		}
	default:
		// Below all thresholds - send recovery notifications if needed
		hysteresis := m.config.Realtime.Monitoring.HysteresisPercent
		if hysteresis == 0 {
			hysteresis = defaultHysteresisPercent
		}
		if state.InWarning && current < (warningThreshold-hysteresis) {
			m.sendRecoveryNotificationWithPath(resource, current, alertLevelWarning, state, path)
			state.InWarning = false
			state.InCritical = false
			state.CriticalStartTime = time.Time{} // Reset
		}
	}

	// Log current status
	m.log.Debug("Resource check completed",
		logger.String("resource", string(resource)),
		logger.String("current", fmt.Sprintf("%.1f%%", current)),
		logger.String("warning_threshold", fmt.Sprintf("%.1f%%", warningThreshold)),
		logger.String("critical_threshold", fmt.Sprintf("%.1f%%", criticalThreshold)),
		logger.Bool("in_warning", state.InWarning),
		logger.Bool("in_critical", state.InCritical),
	)
}

// sendNotification sends a threshold exceeded notification (backward compatibility)
func (m *SystemMonitor) sendNotification(resource ResourceType, current, threshold float64, priority notification.Priority, state *AlertState) {
	m.sendNotificationWithPath(resource, current, threshold, priority, state, "")
}

// sendNotificationWithPath sends a threshold exceeded notification with optional path
func (m *SystemMonitor) sendNotificationWithPath(resource ResourceType, current, threshold float64, priority notification.Priority, state *AlertState, path string) {
	// Determine severity based on priority
	var severity string
	if priority == notification.PriorityCritical {
		severity = events.SeverityCritical
	} else {
		severity = events.SeverityWarning
	}

	// Try to publish via event bus first
	if eventBus := events.GetEventBus(); eventBus != nil {
		var event events.ResourceEvent
		if resource == ResourceDisk && path != "" {
			event = events.NewResourceEventWithPath(string(resource), current, threshold, severity, path)
		} else {
			event = events.NewResourceEvent(string(resource), current, threshold, severity)
		}
		if eventBus.TryPublishResource(event) {
			m.log.Info("Resource event published to event bus",
				logger.String("resource", string(resource)),
				logger.String("current", fmt.Sprintf("%.1f%%", current)),
				logger.String("threshold", fmt.Sprintf("%.1f%%", threshold)),
				logger.String("severity", severity),
			)
			// Update state with notification time
			state.LastNotificationTime = time.Now()
			return
		} else {
			m.log.Warn("Failed to publish resource event to event bus",
				logger.String("resource", string(resource)),
				logger.String("current", fmt.Sprintf("%.1f%%", current)),
				logger.String("threshold", fmt.Sprintf("%.1f%%", threshold)),
				logger.String("severity", severity),
			)
		}
	} else {
		m.log.Debug("Event bus not available for resource notification",
			logger.String("resource", string(resource)),
			logger.String("current", fmt.Sprintf("%.1f%%", current)),
			logger.String("threshold", fmt.Sprintf("%.1f%%", threshold)),
			logger.String("severity", severity),
		)
	}

	// Fallback to direct notification if event bus unavailable or failed
	if !notification.IsInitialized() {
		return
	}

	notification.NotifyResourceAlert(string(resource), current, threshold, "%")

	m.log.Warn("Resource threshold exceeded",
		logger.String("resource", string(resource)),
		logger.String("current", fmt.Sprintf("%.1f%%", current)),
		logger.String("threshold", fmt.Sprintf("%.1f%%", threshold)),
		logger.String("severity", severity),
	)
}

// sendRecoveryNotification sends a notification when resource usage returns to normal (backward compatibility)
func (m *SystemMonitor) sendRecoveryNotification(resource ResourceType, current float64, level string, state *AlertState) {
	m.sendRecoveryNotificationWithPath(resource, current, level, state, "")
}

// sendRecoveryNotificationWithPath sends a notification when resource usage returns to normal with optional path
func (m *SystemMonitor) sendRecoveryNotificationWithPath(resource ResourceType, current float64, level string, state *AlertState, path string) {
	// Calculate duration if recovering from critical
	var duration time.Duration
	if level == alertLevelCritical && !state.CriticalStartTime.IsZero() {
		duration = time.Since(state.CriticalStartTime)
	}

	// Try to publish via event bus first
	if eventBus := events.GetEventBus(); eventBus != nil {
		// For recovery, threshold is not applicable, use 0
		var event events.ResourceEvent
		if resource == ResourceDisk && path != "" {
			event = events.NewResourceEventWithPath(string(resource), current, 0, events.SeverityRecovery, path)
		} else {
			event = events.NewResourceEvent(string(resource), current, 0, events.SeverityRecovery)
		}

		// Add duration metadata if available
		if duration > 0 {
			if metadata := event.GetMetadata(); metadata != nil {
				metadata["duration"] = duration.String()
				metadata["duration_minutes"] = int(duration.Minutes())
			}
		}

		if eventBus.TryPublishResource(event) {
			m.log.Info("Resource recovery event published to event bus",
				logger.String("resource", string(resource)),
				logger.String("current", fmt.Sprintf("%.1f%%", current)),
				logger.String("recovered_from", level),
				logger.Duration("duration", duration),
			)
			// Clear notification tracking
			state.LastNotificationID = ""
			state.LastNotificationTime = time.Time{}
			return
		}
	}

	// Fallback to direct notification if event bus unavailable or failed
	if !notification.IsInitialized() {
		return
	}

	var resourceName string
	switch resource {
	case ResourceCPU:
		resourceName = "CPU"
	case ResourceMemory:
		resourceName = "Memory"
	case ResourceDisk:
		resourceName = "Disk"
	default:
		resourceName = string(resource)
	}

	title := fmt.Sprintf("%s Usage Recovered", resourceName)
	message := fmt.Sprintf("%s usage has returned to normal (%.1f%%)", resourceName, current)

	// Add duration info if available
	if duration > 0 {
		message += fmt.Sprintf(" after %s in %s state", duration.Round(time.Minute), level)
	}

	// Use higher priority for recovery from critical state
	if level == alertLevelCritical {
		notification.NotifyWarning("system", title, message)
	} else {
		notification.NotifyInfo(title, message)
	}

	m.log.Info("Resource usage recovered",
		logger.String("resource", string(resource)),
		logger.String("current", fmt.Sprintf("%.1f%%", current)),
		logger.String("recovered_from", level),
	)
}

// checkThresholdsWithGroup evaluates resource usage against thresholds for a mount group
func (m *SystemMonitor) checkThresholdsWithGroup(resource ResourceType, current, warningThreshold, criticalThreshold float64, group MountGroup) {
	// Use mount point as state key
	stateKey := fmt.Sprintf("%s"+stateKeySeparator+"%s", resource, group.MountPoint)

	m.mu.Lock()
	state, exists := m.alertStates[stateKey]
	if !exists {
		state = &AlertState{}
		m.alertStates[stateKey] = state
	}
	m.mu.Unlock()

	state.LastValue = current
	state.LastCheck = time.Now()

	switch {
	case current >= criticalThreshold:
		if !state.InCritical {
			m.log.Warn("Critical threshold exceeded",
				logger.String("resource", string(resource)),
				logger.String("mount_point", group.MountPoint),
				logger.Any("affected_paths", group.Paths),
				logger.String("current", fmt.Sprintf("%.2f%%", current)),
				logger.String("threshold", fmt.Sprintf("%.2f%%", criticalThreshold)),
			)
			m.sendNotificationWithGroup(resource, current, criticalThreshold, notification.PriorityCritical, state, group)
			state.InCritical = true
			state.InWarning = true
			state.CriticalStartTime = time.Now()
		} else {
			resendInterval := time.Duration(m.config.Realtime.Monitoring.CriticalResendInterval) * time.Minute
			if resendInterval == 0 {
				resendInterval = defaultCriticalResendInterval
			}
			if resource == ResourceDisk && time.Since(state.LastNotificationTime) > resendInterval {
				m.log.Info("Resending critical disk notification after expiry",
					logger.String("resource", string(resource)),
					logger.String("mount_point", group.MountPoint),
					logger.String("current", fmt.Sprintf("%.2f%%", current)),
					logger.String("last_notification", state.LastNotificationTime.Format(time.RFC3339)),
				)
				m.sendNotificationWithGroup(resource, current, criticalThreshold, notification.PriorityCritical, state, group)
			}
		}
	case current >= warningThreshold:
		if !state.InWarning {
			m.log.Warn("Warning threshold exceeded",
				logger.String("resource", string(resource)),
				logger.String("mount_point", group.MountPoint),
				logger.Any("affected_paths", group.Paths),
				logger.String("current", fmt.Sprintf("%.2f%%", current)),
				logger.String("threshold", fmt.Sprintf("%.2f%%", warningThreshold)),
			)
			m.sendNotificationWithGroup(resource, current, warningThreshold, notification.PriorityHigh, state, group)
			state.InWarning = true
		}
		hysteresis := m.config.Realtime.Monitoring.HysteresisPercent
		if hysteresis == 0 {
			hysteresis = defaultHysteresisPercent
		}
		if state.InCritical && current < (criticalThreshold-hysteresis) {
			m.sendRecoveryNotificationWithGroup(resource, current, alertLevelCritical, state, group)
			state.InCritical = false
			state.CriticalStartTime = time.Time{}
		}
	default:
		hysteresis := m.config.Realtime.Monitoring.HysteresisPercent
		if hysteresis == 0 {
			hysteresis = defaultHysteresisPercent
		}
		if state.InWarning && current < (warningThreshold-hysteresis) {
			m.sendRecoveryNotificationWithGroup(resource, current, alertLevelWarning, state, group)
			state.InWarning = false
			state.InCritical = false
			state.CriticalStartTime = time.Time{}
		}
	}

	m.log.Debug("Resource check completed",
		logger.String("resource", string(resource)),
		logger.String("mount_point", group.MountPoint),
		logger.String("current", fmt.Sprintf("%.1f%%", current)),
		logger.Bool("in_warning", state.InWarning),
		logger.Bool("in_critical", state.InCritical),
	)
}

// sendNotificationWithGroup sends a threshold exceeded notification for a mount group
func (m *SystemMonitor) sendNotificationWithGroup(resource ResourceType, current, threshold float64, priority notification.Priority, state *AlertState, group MountGroup) {
	var severity string
	if priority == notification.PriorityCritical {
		severity = events.SeverityCritical
	} else {
		severity = events.SeverityWarning
	}

	if eventBus := events.GetEventBus(); eventBus != nil {
		event := events.NewResourceEventWithPaths(string(resource), current, threshold, severity, group.MountPoint, group.Paths)
		if eventBus.TryPublishResource(event) {
			m.log.Info("Resource event published to event bus",
				logger.String("resource", string(resource)),
				logger.String("mount_point", group.MountPoint),
				logger.Any("affected_paths", group.Paths),
				logger.String("current", fmt.Sprintf("%.1f%%", current)),
				logger.String("threshold", fmt.Sprintf("%.1f%%", threshold)),
				logger.String("severity", severity),
			)
			state.LastNotificationTime = time.Now()
			return
		}
	}

	// Fallback to direct notification
	if !notification.IsInitialized() {
		return
	}

	notification.NotifyResourceAlert(string(resource), current, threshold, "%")
	state.LastNotificationTime = time.Now()
}

// sendRecoveryNotificationWithGroup sends a recovery notification for a mount group
func (m *SystemMonitor) sendRecoveryNotificationWithGroup(resource ResourceType, current float64, level string, state *AlertState, group MountGroup) {
	var duration time.Duration
	if level == alertLevelCritical && !state.CriticalStartTime.IsZero() {
		duration = time.Since(state.CriticalStartTime)
	}

	if eventBus := events.GetEventBus(); eventBus != nil {
		event := events.NewResourceEventWithPaths(string(resource), current, 0, events.SeverityRecovery, group.MountPoint, group.Paths)
		if duration > 0 {
			if metadata := event.GetMetadata(); metadata != nil {
				metadata["duration"] = duration.String()
				metadata["duration_minutes"] = int(duration.Minutes())
			}
		}
		if eventBus.TryPublishResource(event) {
			m.log.Info("Resource recovery event published to event bus",
				logger.String("resource", string(resource)),
				logger.String("mount_point", group.MountPoint),
				logger.Any("affected_paths", group.Paths),
				logger.String("current", fmt.Sprintf("%.1f%%", current)),
				logger.String("recovered_from", level),
			)
			state.LastNotificationID = ""
			state.LastNotificationTime = time.Time{}
			return
		}
	}

	// Fallback
	if !notification.IsInitialized() {
		return
	}

	title := fmt.Sprintf("Disk (%s) Usage Recovered", group.MountPoint)
	message := fmt.Sprintf("Disk usage has returned to normal (%.1f%%)", current)
	if len(group.Paths) > 1 {
		message += fmt.Sprintf(" - Paths: %v", group.Paths)
	}

	if level == alertLevelCritical {
		notification.NotifyWarning("system", title, message)
	} else {
		notification.NotifyInfo(title, message)
	}
}

// GetResourceStatus returns the current status of all monitored resources
func (m *SystemMonitor) GetResourceStatus() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := make(map[string]any)
	for resource, state := range m.alertStates {
		status[resource] = map[string]any{
			"current_value": fmt.Sprintf("%.1f%%", state.LastValue),
			"in_warning":    state.InWarning,
			"in_critical":   state.InCritical,
			"last_check":    state.LastCheck.Format(time.RFC3339),
		}
	}
	return status
}

// TriggerCheck manually triggers a resource check (useful for testing)
func (m *SystemMonitor) TriggerCheck() {
	if !m.config.Realtime.Monitoring.Enabled {
		m.log.Info("System monitoring is disabled, cannot trigger check")
		return
	}
	m.log.Info("Manually triggering resource check")
	m.checkAllResources()
}

// GetMonitoredPaths returns the list of paths being monitored for disk usage
func (m *SystemMonitor) GetMonitoredPaths() []string {
	if m.config.Realtime.Monitoring.Disk.Enabled {
		return m.config.Realtime.Monitoring.Disk.Paths
	}
	return nil
}
