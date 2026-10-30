package telemetry

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logger"
)

var (
	// telemetryWorker is the singleton telemetry worker
	telemetryWorker     atomic.Pointer[TelemetryWorker]
	telemetryWorkerOnce sync.Once
	errWorkerInit       error // Stores worker initialization error
)

// InitializeEventBusIntegration sets up the telemetry worker as an event consumer
// This should be called after both Sentry and event bus are initialized
// Thread-safe: uses sync.Once to ensure single initialization. It returns before
// consuming the Once while Sentry is disabled, so ReconfigureSentry can call it
// again to register the worker when the user enables Error Tracking at runtime.
func InitializeEventBusIntegration() error {
	log := GetLogger()

	// Check if Sentry is enabled (skip check in test mode)
	if testMode.Load() == testModeDisabled {
		settings := conf.GetSettings()
		if settings == nil || !settings.Sentry.Enabled {
			log.Info("Sentry telemetry disabled, skipping event bus integration")
			return nil
		}
	}

	// Check if event bus is initialized
	if !events.IsInitialized() {
		log.Warn("event bus not initialized, skipping telemetry integration")
		return nil
	}

	// Thread-safe initialization using sync.Once
	telemetryWorkerOnce.Do(func() {
		// Create telemetry worker with default config
		// Uses shared constants from worker.go and system_init_manager.go
		config := DefaultWorkerConfig()

		worker, err := NewTelemetryWorker(reportingActive, config)
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
		telemetryWorker.Store(worker)

		// Log registration details
		GetLogger().Info("telemetry worker registered with event bus",
			logger.String("consumer", worker.Name()),
			logger.Bool("supports_batching", worker.SupportsBatching()),
			logger.Bool("batching_enabled", config.BatchingEnabled),
			logger.Int("batch_size", config.BatchSize),
			logger.Duration("batch_timeout", config.BatchTimeout),
			logger.Int("circuit_breaker_threshold", config.FailureThreshold),
			logger.Duration("recovery_timeout", config.RecoveryTimeout),
			logger.Int("rate_limit", config.RateLimitMaxEvents),
			logger.Float64("sampling_rate", config.SamplingRate))
	})

	return errWorkerInit
}

// GetTelemetryWorker returns the telemetry worker instance
func GetTelemetryWorker() *TelemetryWorker {
	return telemetryWorker.Load()
}

// GetWorkerStats returns telemetry worker statistics
func GetWorkerStats() *WorkerStats {
	worker := telemetryWorker.Load()
	if worker == nil {
		return nil
	}
	stats := worker.GetStats()
	return &stats
}

// UpdateSamplingRate allows dynamic adjustment of sampling rate
func UpdateSamplingRate(rate float64) error {
	worker := telemetryWorker.Load()
	if worker == nil {
		return fmt.Errorf("telemetry worker not initialized")
	}

	if err := worker.SetSamplingRate(rate); err != nil {
		return err
	}

	// Log the update
	GetLogger().Info("updated telemetry sampling rate", logger.Float64("rate", rate))

	return nil
}
