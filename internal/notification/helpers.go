package notification

import (
	"fmt"
	"time"
)

// NotifyError creates an error notification with appropriate priority
func NotifyError(component string, err error) {
	if !IsInitialized() {
		return
	}

	service := GetService()
	if service == nil {
		return
	}

	// Use the service's error notification method
	_, _ = service.CreateErrorNotification(err)
}

// NotifySystemAlert creates a system alert notification
func NotifySystemAlert(priority Priority, title, message string) {
	if !IsInitialized() {
		return
	}

	service := GetService()
	if service == nil {
		return
	}

	_, _ = service.CreateWithComponent(TypeSystem, priority, title, message, "system")
}

// NotifyDetection creates a bird detection notification
func NotifyDetection(species string, confidence float64, metadata map[string]interface{}) {
	if !IsInitialized() {
		return
	}

	service := GetService()
	if service == nil {
		return
	}

	title := fmt.Sprintf("Detected: %s", species)
	message := fmt.Sprintf("Confidence: %.1f%%", confidence*100)

	notification, err := service.CreateWithComponent(
		TypeDetection,
		PriorityMedium,
		title,
		message,
		"detection",
	)

	if err == nil && notification != nil && metadata != nil {
		// Add metadata
		for k, v := range metadata {
			notification.WithMetadata(k, v)
		}
		_ = service.store.Update(notification)
	}
}

// NotifyIntegrationFailure creates a notification for integration failures
func NotifyIntegrationFailure(integration string, err error) {
	if !IsInitialized() {
		return
	}

	service := GetService()
	if service == nil {
		return
	}

	title := fmt.Sprintf("%s Integration Failed", integration)
	message := fmt.Sprintf("Failed to connect or send data: %v", err)

	_, _ = service.CreateWithComponent(
		TypeError,
		PriorityHigh,
		title,
		message,
		integration,
	)
}

// NotifyResourceAlert creates notifications for resource threshold violations
func NotifyResourceAlert(resource string, current, threshold float64, unit string) {
	if !IsInitialized() {
		return
	}

	service := GetService()
	if service == nil {
		return
	}

	var priority Priority
	switch {
	case current >= threshold*1.5:
		priority = PriorityCritical
	case current >= threshold*1.2:
		priority = PriorityHigh
	default:
		priority = PriorityMedium
	}

	title := fmt.Sprintf("High %s Usage", resource)
	message := fmt.Sprintf("Current: %.1f%s (Threshold: %.1f%s)", current, unit, threshold, unit)

	notification, _ := service.CreateWithComponent(TypeWarning, priority, title, message, "system")
	if notification != nil {
		notification.
			WithMetadata("resource", resource).
			WithMetadata("current_value", current).
			WithMetadata("threshold", threshold).
			WithMetadata("unit", unit).
			WithExpiry(30 * time.Minute) // Auto-expire resource alerts after 30 minutes
		_ = service.store.Update(notification)
	}
}

// NotifyInfo creates an informational notification
func NotifyInfo(title, message string) {
	if !IsInitialized() {
		return
	}

	service := GetService()
	if service == nil {
		return
	}

	_, _ = service.Create(TypeInfo, PriorityLow, title, message)
}

// NotifyWarning creates a warning notification
func NotifyWarning(component, title, message string) {
	if !IsInitialized() {
		return
	}

	service := GetService()
	if service == nil {
		return
	}

	_, _ = service.CreateWithComponent(TypeWarning, PriorityMedium, title, message, component)
}

// NotifyStartup creates a notification when the application starts
func NotifyStartup(version string) {
	if !IsInitialized() {
		return
	}

	service := GetService()
	if service == nil {
		return
	}

	title := "BirdNET-Go Started"
	message := fmt.Sprintf("Application started successfully (v%s)", version)

	notification, _ := service.CreateWithComponent(TypeInfo, PriorityLow, title, message, "system")
	if notification != nil {
		notification.WithExpiry(5 * time.Minute) // Auto-expire after 5 minutes
		_ = service.store.Update(notification)
	}
}

// NotifyShutdown creates a notification when the application is shutting down
func NotifyShutdown() {
	if !IsInitialized() {
		return
	}

	service := GetService()
	if service == nil {
		return
	}

	title := "BirdNET-Go Shutting Down"
	message := "Application is shutting down gracefully"

	_, _ = service.CreateWithComponent(TypeInfo, PriorityMedium, title, message, "system")
}
