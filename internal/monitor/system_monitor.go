// Package monitor provides system resource monitoring with threshold-based notifications
package monitor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"log/slog"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// ResourceType represents the type of system resource being monitored
type ResourceType string

const (
	ResourceCPU    ResourceType = "cpu"
	ResourceMemory ResourceType = "memory"
	ResourceDisk   ResourceType = "disk"
)

// AlertState tracks the current alert state for a resource
type AlertState struct {
	InWarning  bool
	InCritical bool
	LastValue  float64
	LastCheck  time.Time
}

// SystemMonitor monitors system resources and sends notifications when thresholds are exceeded
type SystemMonitor struct {
	config      *conf.Settings
	interval    time.Duration
	alertStates map[string]*AlertState
	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	logger      *slog.Logger
}

// NewSystemMonitor creates a new system monitor instance
func NewSystemMonitor(config *conf.Settings) *SystemMonitor {
	ctx, cancel := context.WithCancel(context.Background())

	// Default check interval if not configured
	interval := 30 * time.Second
	if config.Realtime.Monitoring.CheckInterval > 0 {
		interval = time.Duration(config.Realtime.Monitoring.CheckInterval) * time.Second
	}

	return &SystemMonitor{
		config:      config,
		interval:    interval,
		alertStates: make(map[string]*AlertState),
		ctx:         ctx,
		cancel:      cancel,
		logger:      logging.ForService("system-monitor"),
	}
}

// Start begins monitoring system resources
func (m *SystemMonitor) Start() {
	if !m.config.Realtime.Monitoring.Enabled {
		m.logger.Info("System monitoring disabled")
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
	)

	m.wg.Add(1)
	go m.monitorLoop()
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

	// Perform initial check
	m.checkAllResources()

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.checkAllResources()
		case <-m.ctx.Done():
			return
		}
	}
}

// checkAllResources checks all monitored resources
func (m *SystemMonitor) checkAllResources() {
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
	}
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

// checkDisk monitors disk usage
func (m *SystemMonitor) checkDisk() {
	// Get disk usage for the configured path (default to root)
	path := m.config.Realtime.Monitoring.Disk.Path
	if path == "" {
		path = "/"
	}

	usage, err := disk.Usage(path)
	if err != nil {
		m.logger.Error("Failed to get disk usage", "error", err, "path", path)
		return
	}

	m.checkThresholds(ResourceDisk, usage.UsedPercent,
		m.config.Realtime.Monitoring.Disk.Warning,
		m.config.Realtime.Monitoring.Disk.Critical)
}

// checkThresholds evaluates resource usage against configured thresholds
func (m *SystemMonitor) checkThresholds(resource ResourceType, current, warningThreshold, criticalThreshold float64) {
	stateKey := string(resource)

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
			m.sendNotification(resource, current, criticalThreshold, notification.PriorityCritical)
			state.InCritical = true
			state.InWarning = true // Critical implies warning
		}
	case current >= warningThreshold:
		// Check warning threshold
		if !state.InWarning {
			m.sendNotification(resource, current, warningThreshold, notification.PriorityHigh)
			state.InWarning = true
		}
		// Clear critical if we're below critical threshold (with hysteresis)
		if state.InCritical && current < (criticalThreshold-5.0) {
			m.sendRecoveryNotification(resource, current, "critical")
			state.InCritical = false
		}
	default:
		// Below all thresholds - send recovery notifications if needed
		if state.InWarning && current < (warningThreshold-5.0) {
			m.sendRecoveryNotification(resource, current, "warning")
			state.InWarning = false
			state.InCritical = false
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

// sendNotification sends a threshold exceeded notification
func (m *SystemMonitor) sendNotification(resource ResourceType, current, threshold float64, priority notification.Priority) {
	if !notification.IsInitialized() {
		return
	}

	level := "Warning"
	if priority == notification.PriorityCritical {
		level = "Critical"
	}

	// NotifyResourceAlert will format the message internally
	notification.NotifyResourceAlert(string(resource), current, threshold, "%")

	m.logger.Warn("Resource threshold exceeded",
		"resource", resource,
		"current", fmt.Sprintf("%.1f%%", current),
		"threshold", fmt.Sprintf("%.1f%%", threshold),
		"level", level,
	)
}

// sendRecoveryNotification sends a notification when resource usage returns to normal
func (m *SystemMonitor) sendRecoveryNotification(resource ResourceType, current float64, level string) {
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

	notification.NotifyInfo(title, message)

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
