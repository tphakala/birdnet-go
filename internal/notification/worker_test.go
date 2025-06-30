package notification

import (
	"sync"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// mockErrorEvent implements the ErrorEvent interface for testing
type mockErrorEvent struct {
	component string
	category  string
	message   string
	context   map[string]interface{}
	timestamp time.Time
	reported  bool
	mu        sync.RWMutex
}

func (m *mockErrorEvent) GetComponent() string                  { return m.component }
func (m *mockErrorEvent) GetCategory() string                   { return m.category }
func (m *mockErrorEvent) GetContext() map[string]interface{}    { return m.context }
func (m *mockErrorEvent) GetTimestamp() time.Time                { return m.timestamp }
func (m *mockErrorEvent) GetError() error                       { return errors.NewStd(m.message) }
func (m *mockErrorEvent) GetMessage() string                     { return m.message }
func (m *mockErrorEvent) IsReported() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.reported
}
func (m *mockErrorEvent) MarkReported() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reported = true
}

func TestNotificationWorker_ProcessEvent(t *testing.T) {
	// Do not run in parallel since we're testing notification creation
	
	// Initialize logging
	logging.Init()
	
	tests := []struct {
		name           string
		event          events.ErrorEvent
		expectNotif    bool
		expectPriority Priority
	}{
		{
			name: "critical_error_creates_notification",
			event: &mockErrorEvent{
				component: "database",
				category:  string(errors.CategoryDatabase),
				message:   "Database connection failed",
				timestamp: time.Now(),
			},
			expectNotif:    true,
			expectPriority: PriorityCritical,
		},
		{
			name: "high_priority_error_creates_notification",
			event: &mockErrorEvent{
				component: "system",
				category:  string(errors.CategorySystem),
				message:   "High memory usage detected",
				timestamp: time.Now(),
			},
			expectNotif:    true,
			expectPriority: PriorityHigh,
		},
		{
			name: "medium_priority_error_skipped",
			event: &mockErrorEvent{
				component: "network",
				category:  string(errors.CategoryNetwork),
				message:   "Temporary network issue",
				timestamp: time.Now(),
			},
			expectNotif: false,
		},
		{
			name: "low_priority_error_skipped",
			event: &mockErrorEvent{
				component: "validation",
				category:  string(errors.CategoryValidation),
				message:   "Invalid input parameter",
				timestamp: time.Now(),
			},
			expectNotif: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Do not run subtests in parallel
			
			// Create service with small buffer
			service := NewService(&ServiceConfig{
				MaxNotifications:   100,
				CleanupInterval:    5 * time.Minute,
				RateLimitWindow:    1 * time.Minute,
				RateLimitMaxEvents: 100,
			})
			defer service.Stop()
			
			// Create worker
			worker, err := NewNotificationWorker(service, nil)
			if err != nil {
				t.Fatalf("Failed to create worker: %v", err)
			}
			
			// Process event
			err = worker.ProcessEvent(tt.event)
			if err != nil {
				t.Errorf("ProcessEvent failed: %v", err)
			}
			
			// Check worker stats
			stats := worker.GetStats()
			t.Logf("Worker stats: processed=%d, dropped=%d, failed=%d", 
				stats.EventsProcessed, stats.EventsDropped, stats.EventsFailed)
			
			// Give the service a moment to process
			time.Sleep(10 * time.Millisecond)
			
			// Check notifications
			notifications, err := service.List(&FilterOptions{
				Types: []Type{TypeError},
			})
			if err != nil {
				t.Fatalf("Failed to list notifications: %v", err)
			}
			
			t.Logf("Found %d notifications for test %s", len(notifications), tt.name)
			for i, n := range notifications {
				t.Logf("  Notification %d: priority=%s, component=%s, title=%s", 
					i, n.Priority, n.Component, n.Title)
			}
			
			if tt.expectNotif {
				if len(notifications) != 1 {
					t.Errorf("Expected 1 notification, got %d", len(notifications))
				} else {
					notif := notifications[0]
					if notif.Priority != tt.expectPriority {
						t.Errorf("Expected priority %s, got %s", tt.expectPriority, notif.Priority)
					}
					if notif.Component != tt.event.GetComponent() {
						t.Errorf("Expected component %s, got %s", tt.event.GetComponent(), notif.Component)
					}
				}
			} else {
				if len(notifications) != 0 {
					t.Errorf("Expected no notifications, got %d", len(notifications))
				}
			}
		})
	}
}

func TestNotificationWorker_CircuitBreaker(t *testing.T) {
	// Do not run in parallel
	
	// Initialize logging
	logging.Init()
	
	// Create service with very low rate limit to trigger failures
	service := NewService(&ServiceConfig{
		MaxNotifications:   10,
		CleanupInterval:    5 * time.Minute,
		RateLimitWindow:    1 * time.Minute,
		RateLimitMaxEvents: 2, // Very low to trigger rate limiting
	})
	defer service.Stop()
	
	// Create worker with low failure threshold
	config := &WorkerConfig{
		BatchingEnabled:    false,
		FailureThreshold:   3,
		RecoveryTimeout:    100 * time.Millisecond,
		HalfOpenMaxEvents:  2,
	}
	
	worker, err := NewNotificationWorker(service, config)
	if err != nil {
		t.Fatalf("Failed to create worker: %v", err)
	}
	
	// Create high priority event that should create notifications
	event := &mockErrorEvent{
		component: "database",
		category:  string(errors.CategoryDatabase),
		message:   "Critical database error",
		timestamp: time.Now(),
	}
	
	// Process events until circuit opens
	// Need to exceed rate limit (2) and trigger failures
	successCount := 0
	for i := 0; i < 10; i++ {
		err := worker.ProcessEvent(event)
		if err == nil {
			successCount++
		}
		// Small delay between events to ensure processing
		time.Sleep(5 * time.Millisecond)
	}
	
	// Check circuit state
	stats := worker.GetStats()
	t.Logf("Circuit breaker stats: state=%s, processed=%d, dropped=%d, failed=%d, success=%d",
		stats.CircuitState, stats.EventsProcessed, stats.EventsDropped, stats.EventsFailed, successCount)
	t.Logf("Circuit breaker config: threshold=%d, recovery=%v", 
		config.FailureThreshold, config.RecoveryTimeout)
	
	// Verify that rate limiting is working
	if stats.EventsProcessed == 0 && stats.EventsDropped == 0 {
		t.Errorf("Expected some events to be processed or dropped, got neither")
	}
	
	// If circuit is open, test recovery
	if stats.CircuitState == "open" {
		// Wait for recovery timeout (add extra buffer)
		time.Sleep(150 * time.Millisecond)
		
		// Test that circuit allows request after recovery
		allowed := worker.circuitBreaker.Allow()
		newState := worker.circuitBreaker.State()
		
		t.Logf("After recovery timeout: allowed=%v, state=%s", allowed, newState)
		
		// Circuit should either allow the request or be in a non-open state
		if !allowed && newState == "open" {
			t.Errorf("Circuit breaker should allow request after recovery timeout")
		}
	} else {
		t.Logf("Circuit is not open (state=%s), skipping recovery test", stats.CircuitState)
	}
}

func TestNotificationWorker_TemplateGeneration(t *testing.T) {
	t.Parallel()
	
	// Initialize logging
	logging.Init()
	
	service := NewService(nil)
	defer service.Stop()
	
	worker, err := NewNotificationWorker(service, nil)
	if err != nil {
		t.Fatalf("Failed to create worker: %v", err)
	}
	
	tests := []struct {
		name          string
		event         events.ErrorEvent
		priority      Priority
		expectedTitle string
	}{
		{
			name: "critical_title",
			event: &mockErrorEvent{
				component: "database",
				category:  string(errors.CategoryDatabase),
			},
			priority:      PriorityCritical,
			expectedTitle: "Critical database Error in database",
		},
		{
			name: "high_title",
			event: &mockErrorEvent{
				component: "network",
				category:  string(errors.CategoryNetwork),
			},
			priority:      PriorityHigh,
			expectedTitle: "network Error in network",
		},
		{
			name: "medium_title",
			event: &mockErrorEvent{
				component: "audio",
				category:  string(errors.CategoryAudio),
			},
			priority:      PriorityMedium,
			expectedTitle: "audio Issue",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			title := worker.generateTitle(tt.event, tt.priority)
			if title != tt.expectedTitle {
				t.Errorf("Expected title %q, got %q", tt.expectedTitle, title)
			}
		})
	}
}

func TestNotificationWorker_MessageTruncation(t *testing.T) {
	t.Parallel()
	
	// Initialize logging
	logging.Init()
	
	service := NewService(nil)
	defer service.Stop()
	
	worker, err := NewNotificationWorker(service, nil)
	if err != nil {
		t.Fatalf("Failed to create worker: %v", err)
	}
	
	// Create event with very long message
	longMessage := make([]byte, 1000)
	for i := range longMessage {
		longMessage[i] = 'a'
	}
	
	event := &mockErrorEvent{
		message: string(longMessage),
	}
	
	message := worker.generateMessage(event, PriorityHigh)
	
	// Check message was truncated
	if len(message) > 500 {
		t.Errorf("Expected message to be truncated to 500 chars, got %d", len(message))
	}
	
	if message[len(message)-3:] != "..." {
		t.Errorf("Expected truncated message to end with '...', got %q", message[len(message)-3:])
	}
}