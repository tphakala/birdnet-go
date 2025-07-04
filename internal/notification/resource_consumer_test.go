package notification

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/events"
)

// mockTimeSource allows controlling time in tests
type mockTimeSource struct {
	currentTime time.Time
	mu          sync.Mutex
}

func (m *mockTimeSource) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentTime
}

func (m *mockTimeSource) Advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentTime = m.currentTime.Add(d)
}

// timeSource interface for dependency injection
type timeSource interface {
	Now() time.Time
}

// realTimeSource uses actual time
type realTimeSource struct{}

func (r realTimeSource) Now() time.Time {
	return time.Now()
}

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
			if err != nil {
				t.Fatalf("Failed to create resource worker: %v", err)
			}

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
				alertKey := fmt.Sprintf("%s-%s", events.ResourceCPU, events.SeverityWarning)
				worker.lastAlertTime[alertKey] = time.Now().Add(-200 * time.Millisecond)
				worker.mu.Unlock()
			}

			// Get notification count before processing
			beforeCount := getNotificationCount(t, service)

			// Process the event
			err = worker.ProcessResourceEvent(tt.event)
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
	if err != nil {
		t.Fatalf("Failed to create worker: %v", err)
	}

	// Test CPU with 1-minute throttle
	cpuEvent := events.NewResourceEvent(events.ResourceCPU, 90.0, 80.0, events.SeverityWarning)
	
	// First event should go through
	err = worker.ProcessResourceEvent(cpuEvent)
	if err != nil {
		t.Errorf("First CPU event failed: %v", err)
	}

	// Second event immediately should be throttled
	err = worker.ProcessResourceEvent(cpuEvent)
	if err != nil {
		t.Errorf("Second CPU event failed: %v", err)
	}

	// Check that only one notification was created
	notifications, _ := service.List(nil)
	if len(notifications) != 1 {
		t.Errorf("Expected 1 notification, got %d", len(notifications))
	}

	// Test that memory uses default throttle (5 minutes)
	memEvent := events.NewResourceEvent(events.ResourceMemory, 90.0, 80.0, events.SeverityWarning)
	
	// Should create notification (different resource type)
	err = worker.ProcessResourceEvent(memEvent)
	if err != nil {
		t.Errorf("Memory event failed: %v", err)
	}

	notifications, _ = service.List(nil)
	if len(notifications) != 2 {
		t.Errorf("Expected 2 notifications after memory alert, got %d", len(notifications))
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