package notification

import (
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/events"
)

func TestResourceEventWorker_ProcessResourceEvent(t *testing.T) {
	t.Parallel()

	// Create a test notification service
	config := DefaultServiceConfig()
	config.MaxNotifications = 100
	service := NewService(config)

	// Create resource worker
	workerConfig := DefaultResourceWorkerConfig()
	workerConfig.AlertThrottle = 100 * time.Millisecond // Short throttle for testing
	
	worker, err := NewResourceEventWorker(service, workerConfig)
	if err != nil {
		t.Fatalf("Failed to create resource worker: %v", err)
	}

	tests := []struct {
		name           string
		event          events.ResourceEvent
		wantNotifType  Type
		wantPriority   Priority
		shouldThrottle bool
		sleepBefore    time.Duration
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
			wantPriority:  PriorityLow,
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
			sleepBefore:    10 * time.Millisecond, // Within throttle window
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
			sleepBefore:   150 * time.Millisecond, // After throttle window
		},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			// Note: Can't use t.Parallel() here due to shared service state

			if tt.sleepBefore > 0 {
				time.Sleep(tt.sleepBefore)
			}

			// Get notification count before processing
			beforeCount := getNotificationCount(t, service)

			// Process the event
			err := worker.ProcessResourceEvent(tt.event)
			if err != nil {
				t.Errorf("ProcessResourceEvent() error = %v", err)
			}

			// Get notification count after processing
			afterCount := getNotificationCount(t, service)

			if tt.shouldThrottle {
				// Should not create a new notification
				if afterCount != beforeCount {
					t.Errorf("Expected throttled event, but notification was created")
				}
			} else {
				// Should create a new notification
				if afterCount != beforeCount+1 {
					t.Errorf("Expected new notification, got count before=%d, after=%d", 
						beforeCount, afterCount)
					return
				}

				// Verify the latest notification
				notifications, err := service.List(&FilterOptions{
					Limit: 1,
				})
				if err != nil {
					t.Fatalf("Failed to list notifications: %v", err)
				}

				if len(notifications) == 0 {
					t.Fatal("No notifications found")
				}

				latest := notifications[0]
				
				// Check notification type
				if latest.Type != tt.wantNotifType {
					t.Errorf("Notification type = %v, want %v", latest.Type, tt.wantNotifType)
				}

				// Check priority
				if latest.Priority != tt.wantPriority {
					t.Errorf("Notification priority = %v, want %v", latest.Priority, tt.wantPriority)
				}

				// Check component
				if latest.Component != "system-monitor" {
					t.Errorf("Notification component = %v, want system-monitor", latest.Component)
				}

				// Check metadata
				if latest.Metadata == nil {
					t.Error("Notification metadata is nil")
				} else {
					// Verify resource type in metadata
					if resType, ok := latest.Metadata["resource_type"].(string); !ok || resType != tt.event.GetResourceType() {
						t.Errorf("Metadata resource_type = %v, want %v", resType, tt.event.GetResourceType())
					}

					// Verify severity in metadata
					if severity, ok := latest.Metadata["severity"].(string); !ok || severity != tt.event.GetSeverity() {
						t.Errorf("Metadata severity = %v, want %v", severity, tt.event.GetSeverity())
					}
				}
			}
		})
	}

	// Check final stats
	stats := worker.GetStats()
	if stats.ProcessedCount == 0 {
		t.Error("No events were processed")
	}
	if stats.SuppressedCount == 0 {
		t.Error("No events were suppressed (expected some throttling)")
	}
}

func TestResourceEventWorker_NilEvent(t *testing.T) {
	t.Parallel()

	// Create a test notification service
	service := NewService(DefaultServiceConfig())
	worker, _ := NewResourceEventWorker(service, nil)

	// Process nil event should not panic
	err := worker.ProcessResourceEvent(nil)
	if err != nil {
		t.Errorf("ProcessResourceEvent(nil) error = %v, want nil", err)
	}
}

func TestResourceEventWorker_InvalidService(t *testing.T) {
	t.Parallel()

	// Try to create worker with nil service
	_, err := NewResourceEventWorker(nil, nil)
	if err == nil {
		t.Error("NewResourceEventWorker(nil, nil) error = nil, want error")
	}
}

// Helper function to get notification count
func getNotificationCount(t *testing.T, service *Service) int {
	t.Helper()
	
	notifications, err := service.List(nil)
	if err != nil {
		t.Fatalf("Failed to list notifications: %v", err)
	}
	return len(notifications)
}