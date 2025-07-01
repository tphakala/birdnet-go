package telemetry

import (
	"fmt"
	"log/slog"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logging"
)

var (
	// asyncWorker is the singleton telemetry async worker
	asyncWorker *AsyncWorker
	// eventBusLogger is the logger for event bus integration
	eventBusLogger *slog.Logger
)

func init() {
	eventBusLogger = logging.ForService("telemetry-eventbus")
	if eventBusLogger == nil {
		eventBusLogger = slog.Default().With("service", "telemetry-eventbus")
	}
}

// InitializeEventBusIntegration sets up the telemetry worker as an event consumer
// This should be called after both the telemetry service and event bus are initialized
func InitializeEventBusIntegration(settings *conf.Settings) error {
	// Check if telemetry is enabled
	if settings == nil || !settings.Sentry.Enabled {
		eventBusLogger.Info("telemetry disabled, skipping event bus integration")
		return nil
	}
	
	// Check if event bus is initialized
	if !events.IsInitialized() {
		eventBusLogger.Warn("event bus not initialized, skipping telemetry integration")
		return nil
	}
	
	// Create telemetry worker
	config := DefaultAsyncWorkerConfig()
	worker, err := NewAsyncWorker(settings, config)
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
	asyncWorker = worker
	
	eventBusLogger.Info("telemetry worker registered with event bus",
		"rate_limit_window", config.RateLimitWindow,
		"rate_limit_events", config.RateLimitEvents,
		"circuit_breaker_threshold", config.FailureThreshold,
	)
	
	return nil
}

// GetAsyncWorker returns the telemetry worker instance
func GetAsyncWorker() *AsyncWorker {
	return asyncWorker
}

// GetAsyncWorkerStats returns telemetry worker statistics
func GetAsyncWorkerStats() *AsyncWorkerStats {
	if asyncWorker == nil {
		return nil
	}
	stats := asyncWorker.GetStats()
	return &stats
}