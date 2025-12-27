package notification

import (
	"fmt"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// ResourceEventWorker consumes resource monitoring events from the event bus
type ResourceEventWorker struct {
	service           *Service
	logger            logger.Logger
	lastAlertTime     map[string]time.Time
	alertThrottle     time.Duration
	resourceThrottles map[string]time.Duration // Per-resource type throttles
	mu                sync.RWMutex
	processedCount    atomic.Uint64  // Thread-safe counter
	suppressedCount   atomic.Uint64  // Thread-safe counter
	cleanupTicker     *time.Ticker   // For periodic cleanup of lastAlertTime
	stopCleanup       chan struct{}  // Signal to stop cleanup goroutine
	wg                sync.WaitGroup // Wait for cleanup to finish
}

// ResourceWorkerConfig holds configuration for the resource event worker
type ResourceWorkerConfig struct {
	// AlertThrottle is the minimum time between alerts for the same resource
	AlertThrottle time.Duration
	// ResourceThrottles allows per-resource type throttle overrides
	// If not specified for a resource type, AlertThrottle is used
	ResourceThrottles map[string]time.Duration
	// Debug enables debug logging
	Debug bool
}

// DefaultResourceWorkerConfig returns default configuration
func DefaultResourceWorkerConfig() *ResourceWorkerConfig {
	return &ResourceWorkerConfig{
		AlertThrottle:     DefaultAlertThrottle,           // Don't spam alerts for same resource
		ResourceThrottles: make(map[string]time.Duration), // Empty by default, can be customized
		Debug:             false,
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

	log := service.logger
	if log == nil {
		log = GetLogger()
	}

	// Copy resource throttles to avoid mutation
	resourceThrottles := make(map[string]time.Duration)
	maps.Copy(resourceThrottles, config.ResourceThrottles)

	worker := &ResourceEventWorker{
		service:           service,
		logger:            log,
		lastAlertTime:     make(map[string]time.Time),
		alertThrottle:     config.AlertThrottle,
		resourceThrottles: resourceThrottles,
		cleanupTicker:     time.NewTicker(DefaultCleanupInterval), // Cleanup every 5 minutes
		stopCleanup:       make(chan struct{}),
	}

	// Start cleanup goroutine
	worker.wg.Add(1)
	go worker.cleanupLoop()

	return worker, nil
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
func (w *ResourceEventWorker) ProcessBatch(errorEvents []events.ErrorEvent) error {
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

	alertKey := w.buildAlertKey(event)

	if w.shouldThrottle(alertKey, event.GetResourceType()) {
		w.suppressedCount.Add(1)
		if w.logger != nil {
			w.logger.Debug("suppressing duplicate resource alert",
				logger.String("resource_type", event.GetResourceType()),
				logger.String("severity", event.GetSeverity()),
				logger.Duration("throttle_duration", w.alertThrottle))
		}
		return nil
	}

	w.updateLastAlertTime(alertKey)

	notifType, priority, title := w.mapSeverityToNotification(event)
	if notifType == "" {
		return nil // Unknown severity
	}

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

	w.enrichResourceMetadata(notification, event)
	w.processedCount.Add(1)

	if w.logger != nil {
		w.logger.Info("resource alert notification created",
			logger.String("resource_type", event.GetResourceType()),
			logger.String("severity", event.GetSeverity()),
			logger.Float64("current_value", event.GetCurrentValue()),
			logger.Float64("threshold", event.GetThreshold()),
			logger.String("notification_id", notification.ID))
	}

	return nil
}

// buildAlertKey creates a throttling key for the resource event.
func (w *ResourceEventWorker) buildAlertKey(event events.ResourceEvent) string {
	// Use "|" as separator since it cannot appear in file paths
	if event.GetResourceType() == events.ResourceDisk && event.GetPath() != "" {
		sanitizedPath := strings.ReplaceAll(event.GetPath(), "|", "_")
		return fmt.Sprintf("%s|%s|%s", event.GetResourceType(), sanitizedPath, event.GetSeverity())
	}
	return fmt.Sprintf("%s|%s", event.GetResourceType(), event.GetSeverity())
}

// mapSeverityToNotification maps event severity to notification type, priority, and title.
// Returns empty type if severity is unknown.
func (w *ResourceEventWorker) mapSeverityToNotification(event events.ResourceEvent) (Type, Priority, string) {
	resourceName := w.getResourceNameWithPath(event)

	switch event.GetSeverity() {
	case events.SeverityRecovery:
		priority := PriorityLow
		if event.GetResourceType() == events.ResourceDisk {
			priority = PriorityMedium
		}
		return TypeInfo, priority, fmt.Sprintf("%s Usage Recovered", resourceName)

	case events.SeverityWarning:
		return TypeWarning, PriorityHigh, fmt.Sprintf("High %s Usage", resourceName)

	case events.SeverityCritical:
		return TypeWarning, PriorityCritical, fmt.Sprintf("Critical %s Usage", resourceName)

	default:
		return "", "", ""
	}
}

// getResourceNameWithPath returns the resource display name, including path for disk resources.
func (w *ResourceEventWorker) getResourceNameWithPath(event events.ResourceEvent) string {
	resourceName := getResourceDisplayName(event.GetResourceType())
	if event.GetResourceType() == events.ResourceDisk && event.GetPath() != "" {
		return fmt.Sprintf("%s (%s)", resourceName, event.GetPath())
	}
	return resourceName
}

// enrichResourceMetadata adds metadata and expiry to the notification.
func (w *ResourceEventWorker) enrichResourceMetadata(notification *Notification, event events.ResourceEvent) {
	if notification == nil {
		return
	}

	// Copy event metadata
	if event.GetMetadata() != nil {
		for k, v := range event.GetMetadata() {
			notification.WithMetadata(k, v)
		}
	}

	// Add standard resource metadata
	notification.
		WithMetadata("resource_type", event.GetResourceType()).
		WithMetadata("current_value", event.GetCurrentValue()).
		WithMetadata("threshold", event.GetThreshold()).
		WithMetadata("severity", event.GetSeverity())

	if event.GetPath() != "" {
		notification.WithMetadata("path", event.GetPath())
	}

	notification.WithExpiry(w.determineResourceExpiry(event))
	_ = w.service.store.Update(notification)
}

// determineResourceExpiry returns the appropriate expiry duration based on event severity and type.
func (w *ResourceEventWorker) determineResourceExpiry(event events.ResourceEvent) time.Duration {
	isDisk := event.GetResourceType() == events.ResourceDisk

	switch event.GetSeverity() {
	case events.SeverityRecovery:
		if isDisk {
			return DefaultAlertExpiry
		}
		return DefaultQuickExpiry

	case events.SeverityCritical:
		if isDisk {
			return DefaultDetectionExpiry
		}
		return DefaultAlertExpiry

	default:
		return DefaultAlertExpiry
	}
}

// shouldThrottle checks if an alert should be throttled
func (w *ResourceEventWorker) shouldThrottle(alertKey, resourceType string) bool {
	w.mu.RLock()
	lastTime, exists := w.lastAlertTime[alertKey]
	throttleDuration := w.alertThrottle

	// Check if there's a specific throttle for this resource type
	if duration, ok := w.resourceThrottles[resourceType]; ok {
		throttleDuration = duration
	}
	w.mu.RUnlock()

	if !exists {
		return false
	}

	return time.Since(lastTime) < throttleDuration
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
		ProcessedCount:  w.processedCount.Load(),
		SuppressedCount: w.suppressedCount.Load(),
	}
}

// cleanupLoop periodically removes old entries from lastAlertTime map
func (w *ResourceEventWorker) cleanupLoop() {
	defer w.wg.Done()

	for {
		select {
		case <-w.cleanupTicker.C:
			w.cleanupOldAlerts()
		case <-w.stopCleanup:
			return
		}
	}
}

// cleanupOldAlerts removes entries older than the maximum throttle duration
func (w *ResourceEventWorker) cleanupOldAlerts() {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now()
	maxAge := w.alertThrottle

	// Check all resource-specific throttles to find the maximum
	for _, duration := range w.resourceThrottles {
		if duration > maxAge {
			maxAge = duration
		}
	}

	// Add some buffer to ensure we don't remove entries too early
	maxAge *= 2

	// Remove old entries
	for key, lastTime := range w.lastAlertTime {
		if now.Sub(lastTime) > maxAge {
			delete(w.lastAlertTime, key)
			if w.logger != nil {
				w.logger.Debug("cleaned up old alert time entry",
					logger.String("key", key),
					logger.Duration("age", now.Sub(lastTime)))
			}
		}
	}
}

// Stop stops the worker and cleans up resources
func (w *ResourceEventWorker) Stop() {
	close(w.stopCleanup)
	w.cleanupTicker.Stop()
	w.wg.Wait()
}
