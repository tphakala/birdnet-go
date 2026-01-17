package events

import (
	"fmt"
	"strings"
	"time"
)

// resourceEventImpl is the concrete implementation of ResourceEvent
type resourceEventImpl struct {
	resourceType string
	currentValue float64
	threshold    float64
	severity     string
	timestamp    time.Time
	metadata     map[string]any
	path         string // Path for disk resources
}

// NewResourceEvent creates a new resource monitoring event
func NewResourceEvent(resourceType string, currentValue, threshold float64, severity string) ResourceEvent {
	return &resourceEventImpl{
		resourceType: resourceType,
		currentValue: currentValue,
		threshold:    threshold,
		severity:     severity,
		timestamp:    time.Now(),
		metadata:     make(map[string]any),
	}
}

// NewResourceEventWithMetadata creates a new resource event with metadata
func NewResourceEventWithMetadata(resourceType string, currentValue, threshold float64, severity string, metadata map[string]any) ResourceEvent {
	if metadata == nil {
		metadata = make(map[string]any)
	}
	return &resourceEventImpl{
		resourceType: resourceType,
		currentValue: currentValue,
		threshold:    threshold,
		severity:     severity,
		timestamp:    time.Now(),
		metadata:     metadata,
	}
}

// NewResourceEventWithPath creates a new resource event with path (for disk resources)
func NewResourceEventWithPath(resourceType string, currentValue, threshold float64, severity, path string) ResourceEvent {
	event := &resourceEventImpl{
		resourceType: resourceType,
		currentValue: currentValue,
		threshold:    threshold,
		severity:     severity,
		timestamp:    time.Now(),
		metadata:     make(map[string]any),
		path:         path,
	}
	// Also store path in metadata for backward compatibility
	if path != "" {
		event.metadata["path"] = path
	}
	return event
}

// NewResourceEventWithPaths creates a new resource event with multiple paths (for aggregated disk alerts)
func NewResourceEventWithPaths(resourceType string, currentValue, threshold float64, severity, mountPoint string, paths []string) ResourceEvent {
	event := &resourceEventImpl{
		resourceType: resourceType,
		currentValue: currentValue,
		threshold:    threshold,
		severity:     severity,
		timestamp:    time.Now(),
		metadata:     make(map[string]any),
		path:         mountPoint,
	}
	// Store mount point and all affected paths in metadata
	if mountPoint != "" {
		event.metadata["path"] = mountPoint
	}
	if len(paths) > 0 {
		event.metadata["paths"] = paths
	}
	return event
}

// GetResourceType returns the type of resource
func (e *resourceEventImpl) GetResourceType() string {
	return e.resourceType
}

// GetCurrentValue returns the current usage percentage
func (e *resourceEventImpl) GetCurrentValue() float64 {
	return e.currentValue
}

// GetThreshold returns the threshold that was crossed
func (e *resourceEventImpl) GetThreshold() float64 {
	return e.threshold
}

// GetSeverity returns the severity level
func (e *resourceEventImpl) GetSeverity() string {
	return e.severity
}

// GetTimestamp returns when the event occurred
func (e *resourceEventImpl) GetTimestamp() time.Time {
	return e.timestamp
}

// GetMetadata returns additional context data
func (e *resourceEventImpl) GetMetadata() map[string]any {
	return e.metadata
}

// GetMessage returns a human-readable message
func (e *resourceEventImpl) GetMessage() string {
	var resourceName string
	switch e.resourceType {
	case ResourceCPU:
		resourceName = "CPU"
	case ResourceMemory:
		resourceName = "Memory"
	case ResourceDisk:
		resourceName = "Disk"
	default:
		resourceName = e.resourceType
	}

	// Include path in message for disk resources
	if e.resourceType == ResourceDisk && e.path != "" {
		resourceName = fmt.Sprintf("%s (%s)", resourceName, e.path)
	}

	var baseMessage string
	switch e.severity {
	case SeverityRecovery:
		baseMessage = fmt.Sprintf("%s usage has returned to normal (%.1f%%)", resourceName, e.currentValue)
	case SeverityWarning:
		baseMessage = fmt.Sprintf("%s usage warning: %.1f%% (threshold: %.1f%%)", resourceName, e.currentValue, e.threshold)
	case SeverityCritical:
		baseMessage = fmt.Sprintf("%s usage critical: %.1f%% (threshold: %.1f%%)", resourceName, e.currentValue, e.threshold)
	default:
		baseMessage = fmt.Sprintf("%s usage: %.1f%%", resourceName, e.currentValue)
	}

	// Append affected paths if multiple paths are present
	if paths, ok := e.metadata["paths"].([]string); ok && len(paths) > 1 {
		baseMessage += fmt.Sprintf("\nAffected paths: %s", strings.Join(paths, ", "))
	}

	return baseMessage
}

// GetPath returns the path for disk resources or empty string for others
func (e *resourceEventImpl) GetPath() string {
	return e.path
}

// Severity constants for resource events
const (
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
	SeverityRecovery = "recovery"
)

// Resource type constants
const (
	ResourceCPU    = "cpu"
	ResourceMemory = "memory"
	ResourceDisk   = "disk"
)
