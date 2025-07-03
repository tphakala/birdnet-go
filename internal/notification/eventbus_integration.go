package notification

import (
	"fmt"

	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logging"
	"log/slog"
)

var (
	// notificationWorker is the singleton notification worker
	notificationWorker *NotificationWorker
	logger            *slog.Logger
)

func init() {
	logger = logging.ForService("notification-integration")
	if logger == nil {
		logger = slog.Default().With("service", "notification-integration")
	}
}

// InitializeEventBusIntegration sets up the notification worker as an event consumer
// This should be called after both the notification service and event bus are initialized
func InitializeEventBusIntegration() error {
	// Check if notification service is initialized
	if !IsInitialized() {
		logger.Warn("notification service not initialized, skipping event bus integration")
		return nil
	}
	
	// Check if event bus is initialized
	if !events.IsInitialized() {
		logger.Warn("event bus not initialized, skipping notification integration")
		return nil
	}
	
	// Get the notification service
	service := GetService()
	if service == nil {
		return fmt.Errorf("notification service is nil")
	}
	
	// Create notification worker
	config := DefaultWorkerConfig()
	// Inherit debug setting from the service
	if service.config != nil {
		config.Debug = service.config.Debug
	}
	worker, err := NewNotificationWorker(service, config)
	if err != nil {
		return fmt.Errorf("failed to create notification worker: %w", err)
	}
	
	// Get event bus
	eventBus := events.GetEventBus()
	if eventBus == nil {
		return fmt.Errorf("event bus is nil")
	}
	
	// Register the worker as a consumer
	if err := eventBus.RegisterConsumer(worker); err != nil {
		return fmt.Errorf("failed to register notification worker: %w", err)
	}
	
	// Store reference for stats/monitoring
	notificationWorker = worker
	
	logger.Info("notification worker registered with event bus",
		"batching_enabled", config.BatchingEnabled,
		"circuit_breaker_threshold", config.FailureThreshold,
		"debug", config.Debug,
	)
	
	return nil
}

// GetNotificationWorker returns the notification worker instance
func GetNotificationWorker() *NotificationWorker {
	return notificationWorker
}

// GetWorkerStats returns notification worker statistics
func GetWorkerStats() *WorkerStats {
	if notificationWorker == nil {
		return nil
	}
	stats := notificationWorker.GetStats()
	return &stats
}