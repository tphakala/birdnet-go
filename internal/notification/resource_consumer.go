package notification

import (
	"fmt"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/events"
	"log/slog"
)

// ResourceEventWorker consumes resource monitoring events from the event bus
type ResourceEventWorker struct {
	service          *Service
	logger           *slog.Logger
	lastAlertTime    map[string]time.Time
	alertThrottle    time.Duration
	mu               sync.RWMutex
	processedCount   uint64
	suppressedCount  uint64
}

// ResourceWorkerConfig holds configuration for the resource event worker
type ResourceWorkerConfig struct {
	// AlertThrottle is the minimum time between alerts for the same resource
	AlertThrottle time.Duration
	// Debug enables debug logging
	Debug bool
}

// DefaultResourceWorkerConfig returns default configuration
func DefaultResourceWorkerConfig() *ResourceWorkerConfig {
	return &ResourceWorkerConfig{
		AlertThrottle: 5 * time.Minute, // Don't spam alerts for same resource
		Debug:         false,
	}
}

// NewResourceEventWorker creates a new resource event worker
func NewResourceEventWorker(service *Service, config *ResourceWorkerConfig) (*ResourceEventWorker, error) {
	if service == nil {
		return nil, fmt.Errorf("notification service is required")
	}

	if config == nil {
		config = DefaultResourceWorkerConfig()
	}

	logger := service.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "resource-worker")

	return &ResourceEventWorker{
		service:       service,
		logger:        logger,
		lastAlertTime: make(map[string]time.Time),
		alertThrottle: config.AlertThrottle,
	}, nil
}

// Name returns the consumer name
func (w *ResourceEventWorker) Name() string {
	return "notification-resource-worker"
}

// ProcessEvent processes a single error event (required by EventConsumer interface)
func (w *ResourceEventWorker) ProcessEvent(event events.ErrorEvent) error {
	// This worker only handles resource events
	return nil
}

// ProcessBatch processes multiple error events (required by EventConsumer interface)
func (w *ResourceEventWorker) ProcessBatch(events []events.ErrorEvent) error {
	// This worker only handles resource events
	return nil
}

// SupportsBatching returns false as resource events are processed individually
func (w *ResourceEventWorker) SupportsBatching() bool {
	return false
}

// ProcessResourceEvent processes a single resource monitoring event
func (w *ResourceEventWorker) ProcessResourceEvent(event events.ResourceEvent) error {
	if event == nil {
		return nil
	}

	// Create alert key for throttling
	alertKey := fmt.Sprintf("%s-%s", event.GetResourceType(), event.GetSeverity())

	// Check if we should throttle this alert
	if w.shouldThrottle(alertKey) {
		w.suppressedCount++
		if w.logger != nil {
			w.logger.Debug("suppressing duplicate resource alert",
				"resource_type", event.GetResourceType(),
				"severity", event.GetSeverity(),
				"throttle_duration", w.alertThrottle,
			)
		}
		return nil
	}

	// Update last alert time
	w.updateLastAlertTime(alertKey)

	// Convert to notification based on severity
	var notifType Type
	var priority Priority
	var title string

	resourceName := getResourceDisplayName(event.GetResourceType())

	switch event.GetSeverity() {
	case events.SeverityRecovery:
		notifType = TypeInfo
		priority = PriorityLow
		title = fmt.Sprintf("%s Usage Recovered", resourceName)
		
	case events.SeverityWarning:
		notifType = TypeWarning
		priority = PriorityHigh
		title = fmt.Sprintf("High %s Usage", resourceName)
		
	case events.SeverityCritical:
		notifType = TypeWarning
		priority = PriorityCritical
		title = fmt.Sprintf("Critical %s Usage", resourceName)
		
	default:
		// Unknown severity, skip
		return nil
	}

	// Create notification
	notification, err := w.service.CreateWithComponent(
		notifType,
		priority,
		title,
		event.GetMessage(),
		"system-monitor",
	)

	if err != nil {
		return fmt.Errorf("failed to create notification: %w", err)
	}

	// Add metadata
	if notification != nil && event.GetMetadata() != nil {
		for k, v := range event.GetMetadata() {
			notification.WithMetadata(k, v)
		}
		// Add additional metadata
		notification.
			WithMetadata("resource_type", event.GetResourceType()).
			WithMetadata("current_value", event.GetCurrentValue()).
			WithMetadata("threshold", event.GetThreshold()).
			WithMetadata("severity", event.GetSeverity())

		// Set expiry for resource alerts
		if event.GetSeverity() != events.SeverityRecovery {
			notification.WithExpiry(30 * time.Minute)
		} else {
			notification.WithExpiry(5 * time.Minute) // Recovery messages expire faster
		}

		// Update in store
		_ = w.service.store.Update(notification)
	}

	w.processedCount++

	if w.logger != nil {
		w.logger.Info("resource alert notification created",
			"resource_type", event.GetResourceType(),
			"severity", event.GetSeverity(),
			"current_value", event.GetCurrentValue(),
			"threshold", event.GetThreshold(),
			"notification_id", notification.ID,
		)
	}

	return nil
}

// shouldThrottle checks if an alert should be throttled
func (w *ResourceEventWorker) shouldThrottle(alertKey string) bool {
	w.mu.RLock()
	lastTime, exists := w.lastAlertTime[alertKey]
	w.mu.RUnlock()

	if !exists {
		return false
	}

	return time.Since(lastTime) < w.alertThrottle
}

// updateLastAlertTime updates the last alert time for a key
func (w *ResourceEventWorker) updateLastAlertTime(alertKey string) {
	w.mu.Lock()
	w.lastAlertTime[alertKey] = time.Now()
	w.mu.Unlock()
}

// getResourceDisplayName returns a display-friendly name for a resource type
func getResourceDisplayName(resourceType string) string {
	switch resourceType {
	case events.ResourceCPU:
		return "CPU"
	case events.ResourceMemory:
		return "Memory"
	case events.ResourceDisk:
		return "Disk"
	default:
		return resourceType
	}
}

// GetStats returns worker statistics
func (w *ResourceEventWorker) GetStats() struct {
	ProcessedCount  uint64
	SuppressedCount uint64
} {
	return struct {
		ProcessedCount  uint64
		SuppressedCount uint64
	}{
		ProcessedCount:  w.processedCount,
		SuppressedCount: w.suppressedCount,
	}
}