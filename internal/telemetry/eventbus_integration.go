package telemetry

import (
	"fmt"
	"sync"
	"sync/atomic"

	"log/slog"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logging"
)

var (
	// telemetryWorker is the singleton telemetry worker
	telemetryWorker     *TelemetryWorker
	telemetryWorkerOnce sync.Once
	errWorkerInit       error // Stores worker initialization error
	logger              *slog.Logger
)

func init() {
	logger = logging.ForService("telemetry-integration")
	if logger == nil {
		logger = slog.Default().With("service", "telemetry-integration")
	}
}

// InitializeEventBusIntegration sets up the telemetry worker as an event consumer
// This should be called after both Sentry and event bus are initialized
// Thread-safe: uses sync.Once to ensure single initialization
func InitializeEventBusIntegration() error {
	// Check if Sentry is enabled (skip check in test mode)
	if atomic.LoadInt32(&testMode) == 0 {
		settings := conf.GetSettings()
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

	// Thread-safe initialization using sync.Once
	telemetryWorkerOnce.Do(func() {
		// Create telemetry worker with default config
		// Uses shared constants from worker.go and system_init_manager.go
		config := DefaultWorkerConfig()

		worker, err := NewTelemetryWorker(true, config)
		if err != nil {
			errWorkerInit = fmt.Errorf("failed to create telemetry worker: %w", err)
			return
		}

		// Get event bus
		eventBus := events.GetEventBus()
		if eventBus == nil {
			errWorkerInit = fmt.Errorf("event bus is nil")
			return
		}

		// Register the worker as a consumer
		if err := eventBus.RegisterConsumer(worker); err != nil {
			errWorkerInit = fmt.Errorf("failed to register telemetry worker: %w", err)
			return
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
	})

	return errWorkerInit
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

	if err := telemetryWorker.SetSamplingRate(rate); err != nil {
		return err
	}

	// Log the update
	logTelemetryInfo(logger, "updated telemetry sampling rate", "rate", rate)

	return nil
}

