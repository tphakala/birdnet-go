package events

import (
	"testing"
	"time"
)

func TestNewResourceEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		resourceType string
		currentValue float64
		threshold    float64
		severity     string
		wantMessage  string
	}{
		{
			name:         "CPU warning",
			resourceType: ResourceCPU,
			currentValue: 85.5,
			threshold:    80.0,
			severity:     SeverityWarning,
			wantMessage:  "CPU usage warning: 85.5% (threshold: 80.0%)",
		},
		{
			name:         "Memory critical",
			resourceType: ResourceMemory,
			currentValue: 95.2,
			threshold:    90.0,
			severity:     SeverityCritical,
			wantMessage:  "Memory usage critical: 95.2% (threshold: 90.0%)",
		},
		{
			name:         "Disk recovery",
			resourceType: ResourceDisk,
			currentValue: 75.0,
			threshold:    0.0, // threshold not applicable for recovery
			severity:     SeverityRecovery,
			wantMessage:  "Disk usage has returned to normal (75.0%)",
		},
		{
			name:         "Unknown resource",
			resourceType: "network",
			currentValue: 50.0,
			threshold:    40.0,
			severity:     SeverityWarning,
			wantMessage:  "network usage warning: 50.0% (threshold: 40.0%)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			before := time.Now()
			event := NewResourceEvent(tt.resourceType, tt.currentValue, tt.threshold, tt.severity)
			after := time.Now()

			// Verify interface implementation
			if event == nil {
				t.Fatal("NewResourceEvent returned nil")
			}

			// Check resource type
			if got := event.GetResourceType(); got != tt.resourceType {
				t.Errorf("GetResourceType() = %v, want %v", got, tt.resourceType)
			}

			// Check current value
			if got := event.GetCurrentValue(); got != tt.currentValue {
				t.Errorf("GetCurrentValue() = %v, want %v", got, tt.currentValue)
			}

			// Check threshold
			if got := event.GetThreshold(); got != tt.threshold {
				t.Errorf("GetThreshold() = %v, want %v", got, tt.threshold)
			}

			// Check severity
			if got := event.GetSeverity(); got != tt.severity {
				t.Errorf("GetSeverity() = %v, want %v", got, tt.severity)
			}

			// Check timestamp is reasonable
			timestamp := event.GetTimestamp()
			if timestamp.Before(before) || timestamp.After(after) {
				t.Errorf("GetTimestamp() = %v, want between %v and %v", timestamp, before, after)
			}

			// Check metadata is initialized
			if metadata := event.GetMetadata(); metadata == nil {
				t.Error("GetMetadata() returned nil, want initialized map")
			}

			// Check message
			if got := event.GetMessage(); got != tt.wantMessage {
				t.Errorf("GetMessage() = %v, want %v", got, tt.wantMessage)
			}
		})
	}
}

func TestNewResourceEventWithMetadata(t *testing.T) {
	t.Parallel()

	metadata := map[string]any{
		"host":     "server01",
		"location": "/var/log",
		"pid":      12345,
	}

	event := NewResourceEventWithMetadata(
		ResourceDisk,
		90.5,
		85.0,
		SeverityCritical,
		metadata,
	)

	// Verify metadata is preserved
	gotMetadata := event.GetMetadata()
	if gotMetadata == nil {
		t.Fatal("GetMetadata() returned nil")
	}

	// Check each metadata field
	if host, ok := gotMetadata["host"].(string); !ok || host != "server01" {
		t.Errorf("metadata[host] = %v, want server01", gotMetadata["host"])
	}

	if location, ok := gotMetadata["location"].(string); !ok || location != "/var/log" {
		t.Errorf("metadata[location] = %v, want /var/log", gotMetadata["location"])
	}

	if pid, ok := gotMetadata["pid"].(int); !ok || pid != 12345 {
		t.Errorf("metadata[pid] = %v, want 12345", gotMetadata["pid"])
	}
}

func TestResourceEventMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		resourceType string
		severity     string
		wantPrefix   string
	}{
		{
			name:         "CPU recovery message",
			resourceType: ResourceCPU,
			severity:     SeverityRecovery,
			wantPrefix:   "CPU usage has returned to normal",
		},
		{
			name:         "Memory warning message",
			resourceType: ResourceMemory,
			severity:     SeverityWarning,
			wantPrefix:   "Memory usage warning:",
		},
		{
			name:         "Disk critical message",
			resourceType: ResourceDisk,
			severity:     SeverityCritical,
			wantPrefix:   "Disk usage critical:",
		},
		{
			name:         "Unknown severity",
			resourceType: ResourceCPU,
			severity:     "unknown",
			wantPrefix:   "CPU usage:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			event := NewResourceEvent(tt.resourceType, 50.0, 40.0, tt.severity)
			message := event.GetMessage()

			if message == "" {
				t.Error("GetMessage() returned empty string")
			}

			// Check message starts with expected prefix
			if !hasPrefix(message, tt.wantPrefix) {
				t.Errorf("GetMessage() = %v, want prefix %v", message, tt.wantPrefix)
			}
		})
	}
}

// hasPrefix is a simple string prefix check to avoid importing strings package
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
