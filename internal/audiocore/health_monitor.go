package audiocore

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/logging"
)

// RestartHandler defines the interface for handling source restarts
type RestartHandler interface {
	// RestartSource attempts to restart the given audio source
	// Returns error if restart fails
	RestartSource(sourceID string) error
}

// AlertHandler defines the interface for handling health alerts
type AlertHandler interface {
	// SendAlert sends an alert for the given condition
	// alertType: Type of alert (e.g., "audio_silence", "source_failure")
	// sourceID: ID of the affected source
	// details: Additional details about the alert condition
	SendAlert(alertType, sourceID string, details map[string]interface{}) error
}

// AudioHealthMonitor monitors the health of audio sources and pipelines
type AudioHealthMonitor struct {
	silenceThresholdDB float64
	silenceTimeout     time.Duration
	checkInterval      time.Duration
	onSilenceAction    string // "restart", "alert", etc.

	sources map[string]*sourceHealth
	mu      sync.RWMutex
	logger  *slog.Logger
	
	// Handlers for actions
	restartHandler RestartHandler
	alertHandler   AlertHandler
}

// sourceHealth tracks health metrics for a single source
type sourceHealth struct {
	sourceID         string
	lastAudioTime    time.Time
	lastLevel        float64
	isHealthy        bool
}

// HealthMonitorConfig holds configuration for health monitoring
type HealthMonitorConfig struct {
	SilenceThresholdDB float64
	SilenceTimeout     time.Duration
	CheckInterval      time.Duration
	OnSilenceAction    string
	RestartHandler     RestartHandler
	AlertHandler       AlertHandler
}

// NewAudioHealthMonitor creates a new health monitor
func NewAudioHealthMonitor(config HealthMonitorConfig) *AudioHealthMonitor {
	logger := logging.ForService("audiocore")
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "health_monitor")

	return &AudioHealthMonitor{
		silenceThresholdDB: config.SilenceThresholdDB,
		silenceTimeout:     config.SilenceTimeout,
		checkInterval:      config.CheckInterval,
		onSilenceAction:    config.OnSilenceAction,
		sources:            make(map[string]*sourceHealth),
		logger:             logger,
		restartHandler:     config.RestartHandler,
		alertHandler:       config.AlertHandler,
	}
}

// MonitorSource starts monitoring a specific audio source
func (h *AudioHealthMonitor) MonitorSource(source AudioSource) {
	// Check for nil source to prevent panic
	if source == nil {
		h.logger.Warn("attempted to monitor nil source")
		return
	}
	
	h.mu.Lock()
	sourceID := source.ID()
	if _, exists := h.sources[sourceID]; exists {
		h.mu.Unlock()
		return
	}

	health := &sourceHealth{
		sourceID:      sourceID,
		lastAudioTime: time.Now(),
		isHealthy:     true,
	}
	h.sources[sourceID] = health
	h.mu.Unlock()

	h.logger.Info("started monitoring source",
		"source_id", sourceID)
}

// StopMonitoring stops monitoring a specific source
func (h *AudioHealthMonitor) StopMonitoring(sourceID string) {
	h.mu.Lock()
	delete(h.sources, sourceID)
	h.mu.Unlock()

	h.logger.Info("stopped monitoring source",
		"source_id", sourceID)
}

// UpdateAudioLevel updates the audio level for a source
func (h *AudioHealthMonitor) UpdateAudioLevel(sourceID string, levelDB float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	health, exists := h.sources[sourceID]
	if !exists {
		return
	}

	health.lastLevel = levelDB

	// Check if audio is above silence threshold
	if levelDB > h.silenceThresholdDB {
		health.lastAudioTime = time.Now()
		health.isHealthy = true
	} else {
		// Calculate silence duration on demand
		silenceDuration := time.Since(health.lastAudioTime)
		
		// Check if silence timeout exceeded
		if silenceDuration > h.silenceTimeout {
			health.isHealthy = false
			h.handleUnhealthySource(sourceID)
		}
	}
}

// handleUnhealthySource handles an unhealthy source based on configuration
func (h *AudioHealthMonitor) handleUnhealthySource(sourceID string) {
	h.logger.Warn("source unhealthy - silence detected",
		"source_id", sourceID,
		"action", h.onSilenceAction)

	switch h.onSilenceAction {
	case "restart":
		h.logger.Info("attempting to restart source",
			"source_id", sourceID)
		
		// Record restart attempt in metrics
		if metrics := GetMetrics(); metrics != nil {
			metrics.RecordProcessingError("health_monitor", sourceID, "source_restart_attempted")
		}
		
		// Call restart handler if available
		if h.restartHandler != nil {
			if err := h.restartHandler.RestartSource(sourceID); err != nil {
				h.logger.Error("failed to restart source",
					"source_id", sourceID,
					"error", err)
			} else {
				h.logger.Info("source restart initiated successfully",
					"source_id", sourceID)
			}
		} else {
			h.logger.Warn("restart handler not configured",
				"source_id", sourceID)
		}
		
	case "alert":
		h.logger.Error("source health alert - prolonged silence detected",
			"source_id", sourceID,
			"silence_threshold_db", h.silenceThresholdDB,
			"silence_timeout", h.silenceTimeout)
		
		// Record alert in metrics
		if metrics := GetMetrics(); metrics != nil {
			metrics.RecordProcessingError("health_monitor", sourceID, "silence_alert_triggered")
		}
		
		// Call alert handler if available
		if h.alertHandler != nil {
			details := map[string]interface{}{
				"silence_threshold_db": h.silenceThresholdDB,
				"silence_timeout":      h.silenceTimeout.String(),
				"timestamp":            time.Now().Format(time.RFC3339),
			}
			
			if err := h.alertHandler.SendAlert("audio_silence", sourceID, details); err != nil {
				h.logger.Error("failed to send alert",
					"source_id", sourceID,
					"error", err)
			} else {
				h.logger.Info("alert sent successfully",
					"source_id", sourceID)
			}
		} else {
			h.logger.Warn("alert handler not configured",
				"source_id", sourceID)
		}
		
	default:
		// No action configured
	}
}

// GetSourceHealth returns health status for a source
func (h *AudioHealthMonitor) GetSourceHealth(sourceID string) (bool, *sourceHealth) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	health, exists := h.sources[sourceID]
	if !exists {
		return false, nil
	}

	return health.isHealthy, health
}

// GetAllHealth returns health status for all monitored sources
func (h *AudioHealthMonitor) GetAllHealth() map[string]bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make(map[string]bool)
	for id, health := range h.sources {
		result[id] = health.isHealthy
	}

	return result
}

// Start begins the health monitoring loop
func (h *AudioHealthMonitor) Start(ctx context.Context) {
	ticker := time.NewTicker(h.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			h.checkAllSources()
		case <-ctx.Done():
			return
		}
	}
}

// checkAllSources checks the health of all monitored sources
func (h *AudioHealthMonitor) checkAllSources() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for sourceID, health := range h.sources {
		// Calculate silence duration on demand
		if health.lastLevel <= h.silenceThresholdDB {
			silenceDuration := time.Since(health.lastAudioTime)
			
			// Check if newly unhealthy
			if health.isHealthy && silenceDuration > h.silenceTimeout {
				health.isHealthy = false
				h.handleUnhealthySource(sourceID)
			}
		}
	}
}