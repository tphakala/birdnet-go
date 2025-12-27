package notification

import (
	"fmt"

	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logger"
)

var (
	// notificationWorker is the singleton notification worker
	notificationWorker *NotificationWorker
	// resourceWorker is the singleton resource event worker
	resourceWorker *ResourceEventWorker
	// detectionConsumer is the singleton detection notification consumer
	detectionConsumer *DetectionNotificationConsumer
	integrationLogger logger.Logger
)

func init() {
	integrationLogger = logger.Global().Module("notification-integration")
}

// InitializeEventBusIntegration sets up the notification worker as an event consumer
// This should be called after both the notification service and event bus are initialized
func InitializeEventBusIntegration() error {
	integrationLogger.Info("initializing notification event bus integration")

	// Check if notification service is initialized
	if !IsInitialized() {
		integrationLogger.Warn("notification service not initialized, skipping event bus integration")
		return nil
	}

	// Check if event bus is initialized
	if !events.IsInitialized() {
		integrationLogger.Warn("event bus not initialized, skipping notification integration")
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

	integrationLogger.Info("notification worker registered with event bus",
		logger.String("consumer", worker.Name()),
		logger.Bool("supports_batching", worker.SupportsBatching()),
		logger.Bool("batching_enabled", config.BatchingEnabled),
		logger.Int("batch_size", config.BatchSize),
		logger.Duration("batch_timeout", config.BatchTimeout),
		logger.Int("circuit_breaker_threshold", config.FailureThreshold),
		logger.Duration("recovery_timeout", config.RecoveryTimeout),
		logger.Bool("debug", config.Debug))

	// Create and register resource event worker
	resourceConfig := DefaultResourceWorkerConfig()
	if service.config != nil {
		resourceConfig.Debug = service.config.Debug
	}

	resWorker, err := NewResourceEventWorker(service, resourceConfig)
	if err != nil {
		return fmt.Errorf("failed to create resource worker: %w", err)
	}

	// Register resource worker
	if err := eventBus.RegisterConsumer(resWorker); err != nil {
		return fmt.Errorf("failed to register resource worker: %w", err)
	}

	// Store reference
	resourceWorker = resWorker

	integrationLogger.Info("resource worker registered with event bus",
		logger.String("consumer", resWorker.Name()),
		logger.Duration("alert_throttle", resourceConfig.AlertThrottle),
		logger.Bool("debug", resourceConfig.Debug))

	// Create and register detection notification consumer
	detectionConsumer = NewDetectionNotificationConsumer(service)
	if err := eventBus.RegisterConsumer(detectionConsumer); err != nil {
		return fmt.Errorf("failed to register detection notification consumer: %w", err)
	}

	integrationLogger.Info("detection notification consumer registered with event bus",
		logger.String("consumer", detectionConsumer.Name()),
		logger.Bool("debug", resourceConfig.Debug))

	return nil
}

// GetNotificationWorker returns the notification worker instance
func GetNotificationWorker() *NotificationWorker {
	return notificationWorker
}

// GetResourceWorker returns the resource event worker instance
func GetResourceWorker() *ResourceEventWorker {
	return resourceWorker
}

// GetDetectionConsumer returns the detection notification consumer instance
func GetDetectionConsumer() *DetectionNotificationConsumer {
	return detectionConsumer
}

// GetWorkerStats returns notification worker statistics
func GetWorkerStats() *WorkerStats {
	if notificationWorker == nil {
		return nil
	}
	stats := notificationWorker.GetStats()
	return &stats
}
