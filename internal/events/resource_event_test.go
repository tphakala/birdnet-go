package events

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			require.NotNil(t, event, "NewResourceEvent returned nil")

			// Check resource type
			assert.Equal(t, tt.resourceType, event.GetResourceType())

			// Check current value
			assert.InDelta(t, tt.currentValue, event.GetCurrentValue(), 0.001)

			// Check threshold
			assert.InDelta(t, tt.threshold, event.GetThreshold(), 0.001)

			// Check severity
			assert.Equal(t, tt.severity, event.GetSeverity())

			// Check timestamp is reasonable
			timestamp := event.GetTimestamp()
			assert.False(t, timestamp.Before(before), "timestamp should be after or at the same time as 'before'")
			assert.False(t, timestamp.After(after), "timestamp should be before or at the same time as 'after'")

			// Check metadata is initialized
			assert.NotNil(t, event.GetMetadata(), "GetMetadata() returned nil, want initialized map")

			// Check message
			assert.Equal(t, tt.wantMessage, event.GetMessage())
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
	require.NotNil(t, gotMetadata, "GetMetadata() returned nil")

	// Check each metadata field
	host, ok := gotMetadata["host"].(string)
	require.True(t, ok, "host should be a string")
	assert.Equal(t, "server01", host)

	location, ok := gotMetadata["location"].(string)
	require.True(t, ok, "location should be a string")
	assert.Equal(t, "/var/log", location)

	pid, ok := gotMetadata["pid"].(int)
	require.True(t, ok, "pid should be an int")
	assert.Equal(t, 12345, pid)
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

			assert.NotEmpty(t, message, "GetMessage() returned empty string")

			// Check message starts with expected prefix
			assert.True(t, hasPrefix(message, tt.wantPrefix),
				"GetMessage() = %v, want prefix %v", message, tt.wantPrefix)
		})
	}
}

// hasPrefix is a simple string prefix check to avoid importing strings package
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func TestNewResourceEventWithPaths(t *testing.T) {
	t.Parallel()

	paths := []string{"/", "/home", "/var"}
	event := NewResourceEventWithPaths("disk", 87.5, 85.0, SeverityWarning, "/", paths)

	assert.Equal(t, "disk", event.GetResourceType())
	assert.InDelta(t, 87.5, event.GetCurrentValue(), 0.001)
	assert.InDelta(t, 85.0, event.GetThreshold(), 0.001)
	assert.Equal(t, SeverityWarning, event.GetSeverity())
	assert.Equal(t, "/", event.GetPath())

	// Check paths in metadata
	metadata := event.GetMetadata()
	require.Contains(t, metadata, "paths")
	assert.Equal(t, paths, metadata["paths"])
	assert.Equal(t, "/", metadata["path"])
}

func TestResourceEventMessageWithMultiplePaths(t *testing.T) {
	t.Parallel()

	paths := []string{"/", "/home/user", "/var/data"}
	event := NewResourceEventWithPaths("disk", 87.5, 85.0, SeverityWarning, "/", paths)

	msg := event.GetMessage()
	assert.Contains(t, msg, "Disk (/)")
	assert.Contains(t, msg, "87.5%")
	assert.Contains(t, msg, "85.0%")
	assert.Contains(t, msg, "Affected paths:")
	assert.Contains(t, msg, "/home/user")
	assert.Contains(t, msg, "/var/data")
}

func TestResourceEventMessageSinglePath(t *testing.T) {
	t.Parallel()

	// Single path should not show "Affected paths"
	event := NewResourceEventWithPath("disk", 87.5, 85.0, SeverityWarning, "/")

	msg := event.GetMessage()
	assert.Contains(t, msg, "Disk (/)")
	assert.NotContains(t, msg, "Affected paths:")
}

func TestResourceEventMessageSinglePathInArray(t *testing.T) {
	t.Parallel()

	// Single path in array should also not show "Affected paths"
	paths := []string{"/"}
	event := NewResourceEventWithPaths("disk", 87.5, 85.0, SeverityWarning, "/", paths)

	msg := event.GetMessage()
	assert.Contains(t, msg, "Disk (/)")
	assert.NotContains(t, msg, "Affected paths:")
}

func TestResourceEventWithPathsRecovery(t *testing.T) {
	t.Parallel()

	paths := []string{"/", "/home", "/var"}
	event := NewResourceEventWithPaths("disk", 75.0, 0, SeverityRecovery, "/", paths)

	msg := event.GetMessage()
	assert.Contains(t, msg, "Disk (/) usage has returned to normal")
	assert.Contains(t, msg, "75.0%")
	assert.Contains(t, msg, "Affected paths:")
}

func TestResourceEventWithPathsCritical(t *testing.T) {
	t.Parallel()

	paths := []string{"/", "/home"}
	event := NewResourceEventWithPaths("disk", 95.0, 90.0, SeverityCritical, "/", paths)

	msg := event.GetMessage()
	assert.Contains(t, msg, "Disk (/) usage critical:")
	assert.Contains(t, msg, "95.0%")
	assert.Contains(t, msg, "90.0%")
	assert.Contains(t, msg, "Affected paths:")
}
