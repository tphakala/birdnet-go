package telemetry

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logging"
	"log/slog"
)

var (
	// telemetryWorker is the singleton telemetry worker
	telemetryWorker *TelemetryWorker
	logger          *slog.Logger
)

func init() {
	logger = logging.ForService("telemetry-integration")
	if logger == nil {
		logger = slog.Default().With("service", "telemetry-integration")
	}
}

// InitializeEventBusIntegration sets up the telemetry worker as an event consumer
// This should be called after both Sentry and event bus are initialized
func InitializeEventBusIntegration(settings *conf.Settings) error {
	// Check if Sentry is enabled (skip check in test mode)
	if atomic.LoadInt32(&testMode) == 0 {
		if settings == nil || !settings.Sentry.Enabled {
			logger.Info("Sentry telemetry disabled, skipping event bus integration")
			return nil
		}
	}

	// Check if event bus is initialized
	if !events.IsInitialized() {
		logger.Warn("event bus not initialized, skipping telemetry integration")
		return nil
	}

	// Check if already initialized
	if telemetryWorker != nil {
		logger.Debug("telemetry worker already initialized, skipping")
		return nil
	}

	// Create telemetry worker with custom config
	config := &WorkerConfig{
		FailureThreshold:   10,
		RecoveryTimeout:    60 * time.Second,
		HalfOpenMaxEvents:  5,
		RateLimitWindow:    1 * time.Minute,
		RateLimitMaxEvents: 100, // Reasonable limit for Sentry
		SamplingRate:       1.0, // 100% sampling by default
		BatchingEnabled:    true,
		BatchSize:          10,
		BatchTimeout:       100 * time.Millisecond,
	}

	worker, err := NewTelemetryWorker(true, config)
	if err != nil {
		return fmt.Errorf("failed to create telemetry worker: %w", err)
	}

	// Get event bus
	eventBus := events.GetEventBus()
	if eventBus == nil {
		return fmt.Errorf("event bus is nil")
	}

	// Register the worker as a consumer
	if err := eventBus.RegisterConsumer(worker); err != nil {
		return fmt.Errorf("failed to register telemetry worker: %w", err)
	}

	// Store reference for stats/monitoring
	telemetryWorker = worker

	// Log registration details
	logTelemetryInfo(logger, "telemetry worker registered with event bus",
		"consumer", worker.Name(),
		"supports_batching", worker.SupportsBatching(),
		"batching_enabled", config.BatchingEnabled,
		"batch_size", config.BatchSize,
		"batch_timeout", config.BatchTimeout,
		"circuit_breaker_threshold", config.FailureThreshold,
		"recovery_timeout", config.RecoveryTimeout,
		"rate_limit", config.RateLimitMaxEvents,
		"sampling_rate", config.SamplingRate,
	)

	return nil
}

// GetTelemetryWorker returns the telemetry worker instance
func GetTelemetryWorker() *TelemetryWorker {
	return telemetryWorker
}

// GetWorkerStats returns telemetry worker statistics
func GetWorkerStats() *WorkerStats {
	if telemetryWorker == nil {
		return nil
	}
	stats := telemetryWorker.GetStats()
	return &stats
}

// UpdateSamplingRate allows dynamic adjustment of sampling rate
func UpdateSamplingRate(rate float64) error {
	if telemetryWorker == nil {
		return fmt.Errorf("telemetry worker not initialized")
	}

	if rate < 0.0 || rate > 1.0 {
		return fmt.Errorf("sampling rate must be between 0.0 and 1.0")
	}

	telemetryWorker.configMu.Lock()
	telemetryWorker.config.SamplingRate = rate
	telemetryWorker.configMu.Unlock()

	// Log the update
	logTelemetryInfo(logger, "updated telemetry sampling rate", "rate", rate)

	return nil
}

