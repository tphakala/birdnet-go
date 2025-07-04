// Package monitor provides system resource monitoring with threshold-based notifications
package monitor

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"log/slog"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// Package-level logger following the common pattern
var logger *slog.Logger
var loggerCloseFunc func() error

func init() {
	// Create a dedicated log file for system monitor
	// Create a new LevelVar with Info level as default
	levelVar := new(slog.LevelVar)
	levelVar.Set(slog.LevelInfo)
	
	fileLogger, closeFunc, err := logging.NewFileLogger("logs/monitor.log", "system-monitor", levelVar)
	if err != nil {
		// Fallback to using the main logger
		logger = logging.ForService("system-monitor")
		if logger == nil {
			logger = slog.Default().With("service", "system-monitor")
		}
	} else {
		logger = fileLogger
		loggerCloseFunc = closeFunc
	}
}

// ResourceType represents the type of system resource being monitored
type ResourceType string

const (
	ResourceCPU    ResourceType = "cpu"
	ResourceMemory ResourceType = "memory"
	ResourceDisk   ResourceType = "disk"
)

// AlertState tracks the current alert state for a resource
type AlertState struct {
	InWarning           bool
	InCritical          bool
	LastValue           float64
	LastCheck           time.Time
	LastNotificationID  string    // ID of the last notification sent
	LastNotificationTime time.Time // When the last notification was sent
	CriticalStartTime   time.Time // When resource first entered critical state
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
	logger         *slog.Logger
}

// NewSystemMonitor creates a new system monitor instance
func NewSystemMonitor(config *conf.Settings) *SystemMonitor {
	ctx, cancel := context.WithCancel(context.Background())

	// Default check interval if not configured
	interval := 30 * time.Second
	if config.Realtime.Monitoring.CheckInterval > 0 {
		interval = time.Duration(config.Realtime.Monitoring.CheckInterval) * time.Second
	}

	// Use the package-level logger instead of creating a new one
	monitor := &SystemMonitor{
		config:         config,
		interval:       interval,
		alertStates:    make(map[string]*AlertState),
		validatedPaths: make(map[string]bool),
		ctx:            ctx,
		cancel:         cancel,
		logger:         logger, // Use package-level logger
	}

	// Always log creation to monitor.log (use package-level logger)
	logger.Info("System monitor instance created",
		"enabled", config.Realtime.Monitoring.Enabled,
		"interval", interval,
		"cpu_enabled", config.Realtime.Monitoring.CPU.Enabled,
		"memory_enabled", config.Realtime.Monitoring.Memory.Enabled,
		"disk_enabled", config.Realtime.Monitoring.Disk.Enabled,
		"disk_paths", config.Realtime.Monitoring.Disk.Paths,
		"disk_warning", config.Realtime.Monitoring.Disk.Warning,
		"disk_critical", config.Realtime.Monitoring.Disk.Critical,
	)

	return monitor
}

// Start begins monitoring system resources
func (m *SystemMonitor) Start() {
	// Always log to monitor.log, even if disabled
	logger.Info("Start() called",
		"monitoring_enabled", m.config.Realtime.Monitoring.Enabled,
	)

	if !m.config.Realtime.Monitoring.Enabled {
		logger.Warn("System monitoring is disabled in configuration",
			"config_path", "realtime.monitoring.enabled",
			"suggestion", "Set 'realtime.monitoring.enabled: true' in your configuration to enable monitoring",
		)
		return
	}

	m.logger.Info("Starting system resource monitoring",
		"interval", m.interval,
		"cpu_warning", m.config.Realtime.Monitoring.CPU.Warning,
		"cpu_critical", m.config.Realtime.Monitoring.CPU.Critical,
		"memory_warning", m.config.Realtime.Monitoring.Memory.Warning,
		"memory_critical", m.config.Realtime.Monitoring.Memory.Critical,
		"disk_warning", m.config.Realtime.Monitoring.Disk.Warning,
		"disk_critical", m.config.Realtime.Monitoring.Disk.Critical,
		"disk_paths", m.config.Realtime.Monitoring.Disk.Paths,
	)

	m.wg.Add(1)
	go m.monitorLoop()
	m.logger.Info("Monitor goroutine started")
}

// Stop stops the system monitor
func (m *SystemMonitor) Stop() {
	m.logger.Info("Stopping system resource monitoring")
	m.cancel()
	m.wg.Wait()
}

// monitorLoop is the main monitoring loop
func (m *SystemMonitor) monitorLoop() {
	defer m.wg.Done()

	m.logger.Info("System monitor loop started", "check_interval", m.interval)

	// Perform initial check
	m.checkAllResources()

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.checkAllResources()
		case <-m.ctx.Done():
			m.logger.Info("System monitor loop stopping")
			return
		}
	}
}

// checkAllResources checks all monitored resources
func (m *SystemMonitor) checkAllResources() {
	m.logger.Debug("Starting resource checks",
		"cpu_enabled", m.config.Realtime.Monitoring.CPU.Enabled,
		"memory_enabled", m.config.Realtime.Monitoring.Memory.Enabled,
		"disk_enabled", m.config.Realtime.Monitoring.Disk.Enabled,
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
		m.logger.Debug("Disk monitoring is disabled")
	}

	m.logger.Debug("Completed resource checks")
}

// checkCPU monitors CPU usage
func (m *SystemMonitor) checkCPU() {
	// Get CPU usage percentage with 0 interval for instant reading
	// This is less accurate than a 1-second sample but doesn't block
	cpuPercent, err := cpu.Percent(0, false)
	if err != nil {
		m.logger.Error("Failed to get CPU usage", "error", err)
		return
	}

	if len(cpuPercent) == 0 {
		return
	}

	usage := cpuPercent[0]
	m.checkThresholds(ResourceCPU, usage,
		m.config.Realtime.Monitoring.CPU.Warning,
		m.config.Realtime.Monitoring.CPU.Critical)
}

// checkMemory monitors memory usage
func (m *SystemMonitor) checkMemory() {
	// Get memory info
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		m.logger.Error("Failed to get memory info", "error", err)
		return
	}

	m.checkThresholds(ResourceMemory, memInfo.UsedPercent,
		m.config.Realtime.Monitoring.Memory.Warning,
		m.config.Realtime.Monitoring.Memory.Critical)
}

// checkDisk monitors disk usage for all configured paths
func (m *SystemMonitor) checkDisk() {
	// Get configured paths or default to root
	paths := m.config.Realtime.Monitoring.Disk.Paths
	if len(paths) == 0 {
		paths = []string{"/"}
	}

	m.logger.Debug("Starting disk usage checks", "paths", paths)

	// Check each configured path
	for _, path := range paths {
		m.checkDiskPath(path)
	}
}

// checkDiskPath monitors disk usage for a single path
func (m *SystemMonitor) checkDiskPath(path string) {
	m.logger.Debug("Starting disk usage check", "path", path)

	// Check if path is already validated
	m.mu.RLock()
	validated, exists := m.validatedPaths[path]
	m.mu.RUnlock()
	
	if !exists || !validated {
		// Verify the path exists
		if _, err := os.Stat(path); err != nil {
			m.logger.Error("Disk monitoring path does not exist or is not accessible", 
				"path", path, 
				"error", err,
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
	} else if !validated {
		// Path was previously checked and found invalid
		return
	}

	usage, err := disk.Usage(path)
	if err != nil {
		m.logger.Error("Failed to get disk usage", "error", err, "path", path)
		return
	}

	// Log detailed disk information - use Info level for visibility
	m.logger.Info("Disk usage check completed",
		"path", path,
		"total_gb", fmt.Sprintf("%.2f", float64(usage.Total)/(1024*1024*1024)),
		"used_gb", fmt.Sprintf("%.2f", float64(usage.Used)/(1024*1024*1024)),
		"free_gb", fmt.Sprintf("%.2f", float64(usage.Free)/(1024*1024*1024)),
		"used_percent", fmt.Sprintf("%.2f%%", usage.UsedPercent),
		"filesystem", usage.Fstype,
		"warning_threshold", fmt.Sprintf("%.2f%%", m.config.Realtime.Monitoring.Disk.Warning),
		"critical_threshold", fmt.Sprintf("%.2f%%", m.config.Realtime.Monitoring.Disk.Critical),
	)

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
	// Use "|" as separator since it cannot appear in file paths
	stateKey := string(resource)
	if resource == ResourceDisk && path != "" {
		stateKey = fmt.Sprintf("%s|%s", resource, path)
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
			m.logger.Warn("Critical threshold exceeded",
				"resource", resource,
				"path", path,
				"current", fmt.Sprintf("%.2f%%", current),
				"threshold", fmt.Sprintf("%.2f%%", criticalThreshold),
			)
			m.sendNotificationWithPath(resource, current, criticalThreshold, notification.PriorityCritical, state, path)
			state.InCritical = true
			state.InWarning = true // Critical implies warning
			state.CriticalStartTime = time.Now()
		} else {
			// Still in critical state - check if we need to resend notification
			resendInterval := time.Duration(m.config.Realtime.Monitoring.CriticalResendInterval) * time.Minute
			if resendInterval == 0 {
				resendInterval = 30 * time.Minute // Default fallback
			}
			if resource == ResourceDisk && time.Since(state.LastNotificationTime) > resendInterval {
				m.logger.Info("Resending critical disk notification after expiry",
					"resource", resource,
					"current", fmt.Sprintf("%.2f%%", current),
					"last_notification", state.LastNotificationTime.Format(time.RFC3339),
				)
				m.sendNotificationWithPath(resource, current, criticalThreshold, notification.PriorityCritical, state, path)
			} else {
				m.logger.Debug("Resource still in critical state",
					"resource", resource,
					"current", fmt.Sprintf("%.2f%%", current),
				)
			}
		}
	case current >= warningThreshold:
		// Check warning threshold
		if !state.InWarning {
			m.logger.Warn("Warning threshold exceeded",
				"resource", resource,
				"current", fmt.Sprintf("%.2f%%", current),
				"threshold", fmt.Sprintf("%.2f%%", warningThreshold),
			)
			m.sendNotificationWithPath(resource, current, warningThreshold, notification.PriorityHigh, state, path)
			state.InWarning = true
		} else {
			m.logger.Debug("Resource still in warning state",
				"resource", resource,
				"current", fmt.Sprintf("%.2f%%", current),
			)
		}
		// Clear critical if we're below critical threshold (with hysteresis)
		hysteresis := m.config.Realtime.Monitoring.HysteresisPercent
		if hysteresis == 0 {
			hysteresis = 5.0 // Default fallback
		}
		if state.InCritical && current < (criticalThreshold-hysteresis) {
			m.sendRecoveryNotificationWithPath(resource, current, "critical", state, path)
			state.InCritical = false
			state.CriticalStartTime = time.Time{} // Reset
		}
	default:
		// Below all thresholds - send recovery notifications if needed
		hysteresis := m.config.Realtime.Monitoring.HysteresisPercent
		if hysteresis == 0 {
			hysteresis = 5.0 // Default fallback
		}
		if state.InWarning && current < (warningThreshold-hysteresis) {
			m.sendRecoveryNotificationWithPath(resource, current, "warning", state, path)
			state.InWarning = false
			state.InCritical = false
			state.CriticalStartTime = time.Time{} // Reset
		}
	}

	// Log current status
	m.logger.Debug("Resource check completed",
		"resource", resource,
		"current", fmt.Sprintf("%.1f%%", current),
		"warning_threshold", fmt.Sprintf("%.1f%%", warningThreshold),
		"critical_threshold", fmt.Sprintf("%.1f%%", criticalThreshold),
		"in_warning", state.InWarning,
		"in_critical", state.InCritical,
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
			m.logger.Info("Resource event published to event bus",
				"resource", resource,
				"current", fmt.Sprintf("%.1f%%", current),
				"threshold", fmt.Sprintf("%.1f%%", threshold),
				"severity", severity,
			)
			// Update state with notification time
			state.LastNotificationTime = time.Now()
			return
		} else {
			m.logger.Warn("Failed to publish resource event to event bus",
				"resource", resource,
				"current", fmt.Sprintf("%.1f%%", current),
				"threshold", fmt.Sprintf("%.1f%%", threshold),
				"severity", severity,
			)
		}
	} else {
		m.logger.Debug("Event bus not available for resource notification",
			"resource", resource,
			"current", fmt.Sprintf("%.1f%%", current),
			"threshold", fmt.Sprintf("%.1f%%", threshold),
			"severity", severity,
		)
	}

	// Fallback to direct notification if event bus unavailable or failed
	if !notification.IsInitialized() {
		return
	}

	notification.NotifyResourceAlert(string(resource), current, threshold, "%")

	m.logger.Warn("Resource threshold exceeded",
		"resource", resource,
		"current", fmt.Sprintf("%.1f%%", current),
		"threshold", fmt.Sprintf("%.1f%%", threshold),
		"severity", severity,
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
	if level == "critical" && !state.CriticalStartTime.IsZero() {
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
			m.logger.Info("Resource recovery event published to event bus",
				"resource", resource,
				"current", fmt.Sprintf("%.1f%%", current),
				"recovered_from", level,
				"duration", duration,
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
	if level == "critical" {
		notification.NotifyWarning("system", title, message)
	} else {
		notification.NotifyInfo(title, message)
	}

	m.logger.Info("Resource usage recovered",
		"resource", resource,
		"current", fmt.Sprintf("%.1f%%", current),
		"recovered_from", level,
	)
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
		m.logger.Info("System monitoring is disabled, cannot trigger check")
		return
	}
	m.logger.Info("Manually triggering resource check")
	m.checkAllResources()
}

// CloseLogger closes the monitor log file if it was opened
func CloseLogger() error {
	if loggerCloseFunc != nil {
		return loggerCloseFunc()
	}
	return nil
}
