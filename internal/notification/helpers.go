package notification

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/privacy"
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
func NotifyDetection(species string, confidence float64, metadata map[string]any) {
	if !IsInitialized() {
		return
	}

	service := GetService()
	if service == nil {
		return
	}

	// Validate confidence range
	if confidence < 0 {
		confidence = 0
	} else if confidence > 1 {
		confidence = 1
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

// Privacy scrubbing helpers

// scrubContextMap sanitizes a context map for logging by removing sensitive data
func scrubContextMap(ctx map[string]any) map[string]any {
	if ctx == nil {
		return nil
	}

	scrubbed := make(map[string]any)
	for k, v := range ctx {
		switch k {
		case "url", "endpoint", "uri", "rtsp_url", "stream_url":
			// Scrub URLs
			scrubbed[k] = privacy.AnonymizeURL(fmt.Sprint(v))
		case "error", "message", "description", "reason":
			// Scrub error messages
			scrubbed[k] = privacy.ScrubMessage(fmt.Sprint(v))
		case "ip", "client_ip", "remote_addr", "source_ip":
			// Anonymize IP addresses
			scrubbed[k] = privacy.AnonymizeIP(fmt.Sprint(v))
		case "path", "file_path", "directory":
			// Scrub file paths
			scrubbed[k] = scrubPath(fmt.Sprint(v))
		case "token", "api_key", "password", "secret":
			// Never log sensitive credentials
			scrubbed[k] = "[REDACTED]"
		default:
			// Keep other values as-is
			scrubbed[k] = v
		}
	}
	return scrubbed
}

// scrubPath sanitizes file paths by removing sensitive directory information
func scrubPath(path string) string {
	if path == "" {
		return ""
	}
	return privacy.AnonymizePath(path)
}

// scrubNotificationContent scrubs sensitive data from notification content for logging
func scrubNotificationContent(content string) string {
	return privacy.ScrubMessage(content)
}

// scrubIPAddress anonymizes IP addresses for logging
func scrubIPAddress(ip string) string {
	if ip == "" {
		return ""
	}
	return privacy.AnonymizeIP(ip)
}
