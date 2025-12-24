//nolint:gocognit // Table-driven tests have expected complexity
package notification

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/events"
)

func TestResourceEventWorker_ProcessResourceEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		event          events.ResourceEvent
		wantNotifType  Type
		wantPriority   Priority
		shouldThrottle bool
		timeAdvance    time.Duration // Time to advance before this test
	}{
		{
			name: "CPU warning notification",
			event: events.NewResourceEvent(
				events.ResourceCPU,
				85.5,
				80.0,
				events.SeverityWarning,
			),
			wantNotifType: TypeWarning,
			wantPriority:  PriorityHigh,
		},
		{
			name: "Memory critical notification",
			event: events.NewResourceEvent(
				events.ResourceMemory,
				95.0,
				90.0,
				events.SeverityCritical,
			),
			wantNotifType: TypeWarning,
			wantPriority:  PriorityCritical,
		},
		{
			name: "Disk recovery notification",
			event: events.NewResourceEvent(
				events.ResourceDisk,
				75.0,
				0.0,
				events.SeverityRecovery,
			),
			wantNotifType: TypeInfo,
			wantPriority:  PriorityMedium,
		},
		{
			name: "Throttled CPU warning",
			event: events.NewResourceEvent(
				events.ResourceCPU,
				86.0,
				80.0,
				events.SeverityWarning,
			),
			shouldThrottle: true,
			timeAdvance:    10 * time.Millisecond, // Within throttle window
		},
		{
			name: "CPU warning after throttle expires",
			event: events.NewResourceEvent(
				events.ResourceCPU,
				87.0,
				80.0,
				events.SeverityWarning,
			),
			wantNotifType: TypeWarning,
			wantPriority:  PriorityHigh,
			timeAdvance:   150 * time.Millisecond, // After throttle window
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel() // Now safe to run in parallel

			// Create isolated service and worker for each test
			config := DefaultServiceConfig()
			config.MaxNotifications = 100
			service := NewService(config)
			defer service.Stop()

			// Create resource worker with short throttle for testing
			workerConfig := DefaultResourceWorkerConfig()
			workerConfig.AlertThrottle = 100 * time.Millisecond

			worker, err := NewResourceEventWorker(service, workerConfig)
			require.NoError(t, err, "Failed to create resource worker")

			// For throttling tests, we need to simulate time passing
			switch tt.name {
			case "Throttled CPU warning":
				// Process first CPU warning to establish baseline
				firstEvent := events.NewResourceEvent(
					events.ResourceCPU,
					85.5,
					80.0,
					events.SeverityWarning,
				)
				_ = worker.ProcessResourceEvent(firstEvent)
			case "CPU warning after throttle expires":
				// Process first CPU warning and update last alert time to simulate expiry
				firstEvent := events.NewResourceEvent(
					events.ResourceCPU,
					85.5,
					80.0,
					events.SeverityWarning,
				)
				_ = worker.ProcessResourceEvent(firstEvent)

				// Manually update the last alert time to simulate throttle expiry
				worker.mu.Lock()
				alertKey := fmt.Sprintf("%s|%s", events.ResourceCPU, events.SeverityWarning)
				worker.lastAlertTime[alertKey] = time.Now().Add(-200 * time.Millisecond)
				worker.mu.Unlock()
			}

			// Get notification count before processing
			beforeCount := getNotificationCount(t, service)

			// Process the event
			err = worker.ProcessResourceEvent(tt.event)
			require.NoError(t, err, "ProcessResourceEvent() error")

			// Get notification count after processing
			afterCount := getNotificationCount(t, service)

			if tt.shouldThrottle {
				// Should not create a new notification
				assert.Equal(t, beforeCount, afterCount, "Expected throttled event, but notification was created")
			} else {
				// Should create a new notification
				assert.Equal(t, beforeCount+1, afterCount, "Expected new notification")

				// Verify the latest notification
				notifications, err := service.List(&FilterOptions{
					Limit: 1,
				})
				require.NoError(t, err, "Failed to list notifications")
				require.NotEmpty(t, notifications, "No notifications found")

				latest := notifications[0]

				// Check notification type
				assert.Equal(t, tt.wantNotifType, latest.Type, "Notification type mismatch")

				// Check priority
				assert.Equal(t, tt.wantPriority, latest.Priority, "Notification priority mismatch")

				// Check component
				assert.Equal(t, "system-monitor", latest.Component, "Notification component mismatch")

				// Check metadata
				require.NotNil(t, latest.Metadata, "Notification metadata is nil")

				// Verify resource type in metadata
				resType, ok := latest.Metadata["resource_type"].(string)
				require.True(t, ok, "resource_type should be a string")
				assert.Equal(t, tt.event.GetResourceType(), resType, "Metadata resource_type mismatch")

				// Verify severity in metadata
				severity, ok := latest.Metadata["severity"].(string)
				require.True(t, ok, "severity should be a string")
				assert.Equal(t, tt.event.GetSeverity(), severity, "Metadata severity mismatch")
			}
		})
	}
}

func TestResourceEventWorker_PerResourceThrottle(t *testing.T) {
	t.Parallel()

	// Create service
	service := NewService(DefaultServiceConfig())
	defer service.Stop()

	// Create worker with custom per-resource throttles
	config := &ResourceWorkerConfig{
		AlertThrottle: 5 * time.Minute, // Default
		ResourceThrottles: map[string]time.Duration{
			events.ResourceCPU:  1 * time.Minute,  // Shorter for CPU
			events.ResourceDisk: 10 * time.Minute, // Longer for disk
		},
		Debug: false,
	}

	worker, err := NewResourceEventWorker(service, config)
	require.NoError(t, err, "Failed to create worker")
	defer worker.Stop()

	// Test CPU with 1-minute throttle
	cpuEvent := events.NewResourceEvent(events.ResourceCPU, 90.0, 80.0, events.SeverityWarning)

	// First event should go through
	err = worker.ProcessResourceEvent(cpuEvent)
	require.NoError(t, err, "First CPU event failed")

	// Second event immediately should be throttled
	err = worker.ProcessResourceEvent(cpuEvent)
	require.NoError(t, err, "Second CPU event failed")

	// Check that only one notification was created
	notifications, _ := service.List(nil)
	assert.Len(t, notifications, 1, "Expected 1 notification")

	// Test that memory uses default throttle (5 minutes)
	memEvent := events.NewResourceEvent(events.ResourceMemory, 90.0, 80.0, events.SeverityWarning)

	// Should create notification (different resource type)
	err = worker.ProcessResourceEvent(memEvent)
	require.NoError(t, err, "Memory event failed")

	notifications, _ = service.List(nil)
	assert.Len(t, notifications, 2, "Expected 2 notifications after memory alert")
}

func TestResourceEventWorker_NilEvent(t *testing.T) {
	t.Parallel()

	// Create a test notification service
	service := NewService(DefaultServiceConfig())
	defer service.Stop()

	worker, _ := NewResourceEventWorker(service, nil)
	defer worker.Stop()

	// Process nil event should not panic
	err := worker.ProcessResourceEvent(nil)
	require.NoError(t, err, "ProcessResourceEvent(nil) should not error")
}

func TestResourceEventWorker_InvalidService(t *testing.T) {
	t.Parallel()

	// Try to create worker with nil service
	_, err := NewResourceEventWorker(nil, nil)
	require.Error(t, err, "NewResourceEventWorker(nil, nil) should error")
}

// Helper function to get notification count
func getNotificationCount(t *testing.T, service *Service) int {
	t.Helper()

	notifications, err := service.List(nil)
	require.NoError(t, err, "Failed to list notifications")
	return len(notifications)
}
