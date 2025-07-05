package audiocore

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/logging"
)

// AudioHealthMonitor monitors the health of audio sources and pipelines
type AudioHealthMonitor struct {
	silenceThresholdDB float64
	silenceTimeout     time.Duration
	checkInterval      time.Duration
	onSilenceAction    string // "restart", "alert", etc.

	sources map[string]*sourceHealth
	mu      sync.RWMutex
	logger  *slog.Logger
}

// sourceHealth tracks health metrics for a single source
type sourceHealth struct {
	sourceID         string
	lastAudioTime    time.Time
	lastLevel        float64
	silenceDuration  time.Duration
	isHealthy        bool
}

// HealthMonitorConfig holds configuration for health monitoring
type HealthMonitorConfig struct {
	SilenceThresholdDB float64
	SilenceTimeout     time.Duration
	CheckInterval      time.Duration
	OnSilenceAction    string
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
	}
}

// MonitorSource starts monitoring a specific audio source
func (h *AudioHealthMonitor) MonitorSource(source AudioSource) {
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
		health.silenceDuration = 0
		health.isHealthy = true
	} else {
		// Update silence duration
		health.silenceDuration = time.Since(health.lastAudioTime)
		
		// Check if silence timeout exceeded
		if health.silenceDuration > h.silenceTimeout {
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
		// TODO: Implement source restart logic
		h.logger.Info("attempting to restart source",
			"source_id", sourceID)
	case "alert":
		// TODO: Send alert/notification
		h.logger.Info("sending health alert",
			"source_id", sourceID)
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

	now := time.Now()
	for sourceID, health := range h.sources {
		// Update silence duration
		if health.lastLevel <= h.silenceThresholdDB {
			health.silenceDuration = now.Sub(health.lastAudioTime)
			
			// Check if newly unhealthy
			if health.isHealthy && health.silenceDuration > h.silenceTimeout {
				health.isHealthy = false
				h.handleUnhealthySource(sourceID)
			}
		}
	}
}