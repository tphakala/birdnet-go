package events

import (
	"fmt"
	"time"
)

// resourceEventImpl is the concrete implementation of ResourceEvent
type resourceEventImpl struct {
	resourceType string
	currentValue float64
	threshold    float64
	severity     string
	timestamp    time.Time
	metadata     map[string]interface{}
}

// NewResourceEvent creates a new resource monitoring event
func NewResourceEvent(resourceType string, currentValue, threshold float64, severity string) ResourceEvent {
	return &resourceEventImpl{
		resourceType: resourceType,
		currentValue: currentValue,
		threshold:    threshold,
		severity:     severity,
		timestamp:    time.Now(),
		metadata:     make(map[string]interface{}),
	}
}

// NewResourceEventWithMetadata creates a new resource event with metadata
func NewResourceEventWithMetadata(resourceType string, currentValue, threshold float64, severity string, metadata map[string]interface{}) ResourceEvent {
	if metadata == nil {
		metadata = make(map[string]interface{})
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
func (e *resourceEventImpl) GetMetadata() map[string]interface{} {
	return e.metadata
}

// GetMessage returns a human-readable message
func (e *resourceEventImpl) GetMessage() string {
	var resourceName string
	switch e.resourceType {
	case "cpu":
		resourceName = "CPU"
	case "memory":
		resourceName = "Memory"
	case "disk":
		resourceName = "Disk"
	default:
		resourceName = e.resourceType
	}

	switch e.severity {
	case "recovery":
		return fmt.Sprintf("%s usage has returned to normal (%.1f%%)", resourceName, e.currentValue)
	case "warning":
		return fmt.Sprintf("%s usage warning: %.1f%% (threshold: %.1f%%)", resourceName, e.currentValue, e.threshold)
	case "critical":
		return fmt.Sprintf("%s usage critical: %.1f%% (threshold: %.1f%%)", resourceName, e.currentValue, e.threshold)
	default:
		return fmt.Sprintf("%s usage: %.1f%%", resourceName, e.currentValue)
	}
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