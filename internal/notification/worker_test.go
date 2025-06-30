package notification

import (
	"strings"
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
	t.Parallel()
	
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
			t.Parallel()
			
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
			
			// Check notifications immediately - ProcessEvent is synchronous
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
	t.Parallel()
	
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
	
	// If circuit is open, test recovery behavior by checking state transitions
	if stats.CircuitState == "open" {
		// Create a test helper to wait for circuit recovery
		waitForCircuitRecovery := func(t *testing.T, cb *CircuitBreaker, timeout time.Duration) bool {
			deadline := time.Now().Add(timeout)
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			
			for time.Now().Before(deadline) {
				<-ticker.C
				if cb.Allow() {
					return true
				}
			}
			return false
		}
		
		// Wait for circuit to allow requests after recovery timeout
		recovered := waitForCircuitRecovery(t, worker.circuitBreaker, 200*time.Millisecond)
		newState := worker.circuitBreaker.State()
		
		t.Logf("After recovery wait: recovered=%v, state=%s", recovered, newState)
		
		if !recovered {
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
		name            string
		event           events.ErrorEvent
		priority        Priority
		expectedTitle   string
		expectedMessage string // Add message validation
	}{
		{
			name: "critical_title_and_message",
			event: &mockErrorEvent{
				component: "database",
				category:  string(errors.CategoryDatabase),
				message:   "Connection failed",
			},
			priority:        PriorityCritical,
			expectedTitle:   "Critical database Error in database",
			expectedMessage: "Critical database error in database: Connection failed",
		},
		{
			name: "high_title_and_message",
			event: &mockErrorEvent{
				component: "network",
				category:  string(errors.CategoryNetwork),
				message:   "Timeout occurred",
			},
			priority:        PriorityHigh,
			expectedTitle:   "network Error in network",
			expectedMessage: "network error in network: Timeout occurred",
		},
		{
			name: "medium_title_and_message",
			event: &mockErrorEvent{
				component: "audio",
				category:  string(errors.CategoryAudio),
				message:   "Buffer underrun",
			},
			priority:        PriorityMedium,
			expectedTitle:   "audio Issue",
			expectedMessage: "audio reported: Buffer underrun",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			title := worker.generateTitle(tt.event, tt.priority)
			if title != tt.expectedTitle {
				t.Errorf("Expected title %q, got %q", tt.expectedTitle, title)
			}
			
			// Test message generation with templates
			message := worker.generateMessage(tt.event, tt.priority)
			if message != tt.expectedMessage {
				t.Errorf("Expected message %q, got %q", tt.expectedMessage, message)
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
		component: "test",
		category:  string(errors.CategoryGeneric),
		message:   string(longMessage),
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

func TestNotificationWorker_BatchProcessing(t *testing.T) {
	t.Parallel()
	
	// Initialize logging
	logging.Init()
	
	service := NewService(&ServiceConfig{
		MaxNotifications:   100,
		CleanupInterval:    5 * time.Minute,
		RateLimitWindow:    1 * time.Minute,
		RateLimitMaxEvents: 100,
	})
	defer service.Stop()
	
	config := &WorkerConfig{
		BatchingEnabled: true,
		BatchSize:       10,
		BatchTimeout:    100 * time.Millisecond,
	}
	
	worker, err := NewNotificationWorker(service, config)
	if err != nil {
		t.Fatalf("Failed to create worker: %v", err)
	}
	
	// Create multiple events for batch processing
	errorEvents := []events.ErrorEvent{
		// Same component and category - should be grouped
		&mockErrorEvent{
			component: "database",
			category:  string(errors.CategoryDatabase),
			message:   "Connection failed",
			timestamp: time.Now(),
		},
		&mockErrorEvent{
			component: "database",
			category:  string(errors.CategoryDatabase),
			message:   "Connection timeout",
			timestamp: time.Now(),
		},
		&mockErrorEvent{
			component: "database",
			category:  string(errors.CategoryDatabase),
			message:   "Connection failed", // Duplicate message
			timestamp: time.Now(),
		},
		// Different component - should be separate group
		&mockErrorEvent{
			component: "system",
			category:  string(errors.CategorySystem),
			message:   "High memory usage",
			timestamp: time.Now(),
		},
		// Low priority - should be skipped
		&mockErrorEvent{
			component: "validation",
			category:  string(errors.CategoryValidation),
			message:   "Invalid input",
			timestamp: time.Now(),
		},
	}
	
	// Process batch
	err = worker.ProcessBatch(errorEvents)
	if err != nil {
		t.Errorf("ProcessBatch failed: %v", err)
	}
	
	// Check notifications created
	notifications, err := service.List(&FilterOptions{
		Types: []Type{TypeError},
	})
	if err != nil {
		t.Fatalf("Failed to list notifications: %v", err)
	}
	
	// Should have 2 notifications (database group + system)
	if len(notifications) != 2 {
		t.Errorf("Expected 2 notifications (grouped), got %d", len(notifications))
	}
	
	// Verify aggregation in titles
	for _, notif := range notifications {
		if notif.Component == "database" {
			if !strings.Contains(notif.Title, "occurrences") {
				t.Errorf("Expected database notification to show occurrences in title, got %q", notif.Title)
			}
			if !strings.Contains(notif.Message, "Multiple") {
				t.Errorf("Expected database notification to mention multiple errors, got %q", notif.Message)
			}
		}
	}
	
	// Check stats
	stats := worker.GetStats()
	// Should process 4 events (5 - 1 low priority)
	if stats.EventsProcessed != 4 {
		t.Errorf("Expected 4 events processed, got %d", stats.EventsProcessed)
	}
}