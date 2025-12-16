//nolint:gocognit // Table-driven tests have expected complexity
package notification

import (
	"sync"
	"testing"
	"time"
)

// TestBroadcastMetadataRace tests for the race condition where metadata is modified
// after a notification has been broadcast to subscribers.
//
// This test demonstrates the bug where:
// 1. CreateWithComponent() broadcasts a notification (sends pointer to subscribers)
// 2. Subscribers start reading from notif.Metadata
// 3. The caller continues to modify notif.Metadata via WithMetadata()
// 4. RACE: concurrent read and write on the same map
//
// Run with: go test -race -run TestBroadcastMetadataRace ./internal/notification/
func TestBroadcastMetadataRace(t *testing.T) {
	// Create service with test configuration
	config := &ServiceConfig{
		MaxNotifications:   100,
		CleanupInterval:    time.Minute,
		RateLimitWindow:    time.Minute,
		RateLimitMaxEvents: 1000, // High limit to avoid rate limiting
		Debug:              false,
	}
	service := NewService(config)
	defer service.Stop()

	// Create multiple subscribers to increase chance of race detection
	const numSubscribers = 5
	subscribers := make([]<-chan *Notification, numSubscribers)
	for i := range numSubscribers {
		ch, _ := service.Subscribe()
		subscribers[i] = ch
	}
	// Cleanup subscribers at end of test (not in loop to avoid deferInLoop lint)
	defer func() {
		for _, ch := range subscribers {
			service.Unsubscribe(ch)
		}
	}()

	// WaitGroup to coordinate goroutines
	var wg sync.WaitGroup

	// Run multiple iterations to increase chance of race detection
	const iterations = 100
	for i := range iterations {
		// Create notification
		notif := NewNotification(TypeInfo, PriorityMedium, "Race Test", "Testing race condition").
			WithComponent("test").
			WithMetadata("initial", true)

		// Start subscriber goroutines that will read from Metadata
		for j, ch := range subscribers {
			wg.Add(1)
			go func(subscriberID int, notifCh <-chan *Notification) {
				defer wg.Done()

				select {
				case received := <-notifCh:
					if received == nil {
						return
					}
					// Read from Metadata - this is what SSE clients do
					// This read races with the write below
					_ = received.Metadata["initial"]
					_ = received.Metadata["post_broadcast"]
					_ = received.Metadata["context_key"]

					// Also iterate over the map (more likely to trigger race)
					for k, v := range received.Metadata {
						_, _ = k, v
					}

				case <-time.After(100 * time.Millisecond):
					// Timeout - notification not received
				}
			}(j, ch)
		}

		// Broadcast the notification via CreateWithMetadata
		// This sends the pointer to all subscribers
		if err := service.CreateWithMetadata(notif); err != nil {
			t.Logf("iteration %d: CreateWithMetadata error (expected due to rate limiting): %v", i, err)
			wg.Wait()
			continue
		}

		// RACE CONDITION: Write to Metadata AFTER broadcast
		// This simulates what NotificationWorker.ProcessEvent does at lines 233-241
		notif.WithMetadata("post_broadcast", true)
		notif.WithMetadata("context_key", "context_value")
		notif.WithMetadata("iteration", i)

		// Wait for all subscribers to finish reading
		wg.Wait()
	}
}

// TestBroadcastMetadataRaceWithWorkerPattern tests the exact pattern used by
// NotificationWorker.ProcessEvent that causes the race condition.
//
// The bug is in worker.go lines 200-241:
//   notification, err := w.service.CreateWithComponent(...)  // broadcasts pointer
//   // ... error handling ...
//   if notification != nil && event.GetContext() != nil {
//       for k, v := range event.GetContext() {
//           notification.WithMetadata(k, v)  // WRITE after broadcast!
//       }
//   }
func TestBroadcastMetadataRaceWithWorkerPattern(t *testing.T) {
	config := &ServiceConfig{
		MaxNotifications:   100,
		CleanupInterval:    time.Minute,
		RateLimitWindow:    time.Minute,
		RateLimitMaxEvents: 1000,
		Debug:              false,
	}
	service := NewService(config)
	defer service.Stop()

	// Create multiple subscribers
	const numSubscribers = 10
	subscribers := make([]<-chan *Notification, numSubscribers)
	for i := range numSubscribers {
		ch, _ := service.Subscribe()
		subscribers[i] = ch
	}
	// Cleanup subscribers at end of test (not in loop to avoid deferInLoop lint)
	defer func() {
		for _, ch := range subscribers {
			service.Unsubscribe(ch)
		}
	}()

	var wg sync.WaitGroup

	// Simulate multiple concurrent error events being processed
	const iterations = 50
	for range iterations {
		// Start subscribers reading
		for j, ch := range subscribers {
			wg.Add(1)
			go func(id int, notifCh <-chan *Notification) {
				defer wg.Done()

				select {
				case received := <-notifCh:
					if received == nil {
						return
					}

					// Simulate what processNotificationEvent does at notifications.go:282
					// isToast, _ := notif.Metadata[notification.MetadataKeyIsToast].(bool)
					_, _ = received.Metadata[MetadataKeyIsToast].(bool)

					// Simulate createToastEventData reading multiple metadata fields
					_ = received.Metadata["toastType"]
					_ = received.Metadata["duration"]
					_ = received.Metadata["action"]
					_ = received.Metadata["toastId"]

					// Read all metadata (like JSON serialization would do)
					for k, v := range received.Metadata {
						_, _ = k, v
					}

				case <-time.After(50 * time.Millisecond):
					// Timeout
				}
			}(j, ch)
		}

		// Simulate NotificationWorker.ProcessEvent pattern:
		// 1. Call CreateWithComponent (which broadcasts)
		notification, err := service.CreateWithComponent(
			TypeError,
			PriorityMedium,
			"Error Title",
			"Error message",
			"test-component",
		)
		if err != nil {
			wg.Wait()
			continue
		}

		// 2. RACE: Add context metadata AFTER broadcast (worker.go:233-241)
		if notification != nil {
			// Simulate: for k, v := range event.GetContext() { notification.WithMetadata(k, v) }
			eventContext := map[string]any{
				"operation":  "test_operation",
				"error_code": 500,
				"retry":      true,
				"timestamp":  time.Now().Unix(),
				"details":    "additional error details",
			}
			for k, v := range eventContext {
				notification.WithMetadata(k, v)
			}

			// 3. RACE: Set expiry after broadcast (worker.go:239-241)
			notification.WithExpiry(24 * time.Hour)
		}

		wg.Wait()
	}
}

// TestCloneProvidesSafeAccess verifies that Clone() creates an isolated copy
// that can be safely accessed even when the original is modified.
// This simulates the broadcast scenario: multiple subscribers (readers) receive
// clones while the caller (writer) modifies the original.
func TestCloneProvidesSafeAccess(t *testing.T) {
	original := NewNotification(TypeInfo, PriorityMedium, "Test", "Test")
	original.WithMetadata("initial", "value")
	original.WithMetadata("count", 0)

	var wg sync.WaitGroup
	const numReaders = 10
	const iterations = 100

	// Create clones for readers (simulating what broadcast does)
	clones := make([]*Notification, numReaders)
	for i := range numReaders {
		clones[i] = original.Clone()
	}

	// Start readers - they read from their own clones (simulating SSE clients)
	for i := range numReaders {
		wg.Add(1)
		go func(id int, clone *Notification) {
			defer wg.Done()
			for j := range iterations {
				// Read operations on clone - should be safe
				_ = clone.Metadata["initial"]
				_ = clone.Metadata["dynamic"]
				_ = clone.Metadata[MetadataKeyIsToast]

				// Iterate - what JSON marshaling does
				for k, v := range clone.Metadata {
					_, _, _ = k, v, j
				}
			}
		}(i, clones[i])
	}

	// Single writer modifies the original (simulating NotificationWorker.ProcessEvent)
	// This runs concurrently with readers, but readers have clones so it's safe
	wg.Go(func() {
		for j := range iterations {
			// Write operations on original - clones are isolated
			original.WithMetadata("dynamic", j)
			original.WithMetadata("writer", 0)
			original.WithMetadata("iteration", j)
		}
	})

	wg.Wait()
}

// TestCloneCreatesDeepCopy verifies that Clone() creates a true deep copy
func TestCloneCreatesDeepCopy(t *testing.T) {
	original := NewNotification(TypeInfo, PriorityMedium, "Original Title", "Original Message")
	original.WithComponent("original-component")
	original.WithMetadata("key1", "value1")
	original.WithMetadata("key2", 42)
	original.WithExpiry(time.Hour)

	clone := original.Clone()

	// Verify all fields are copied
	if clone.ID != original.ID {
		t.Errorf("ID mismatch: got %s, want %s", clone.ID, original.ID)
	}
	if clone.Title != original.Title {
		t.Errorf("Title mismatch: got %s, want %s", clone.Title, original.Title)
	}
	if clone.Message != original.Message {
		t.Errorf("Message mismatch: got %s, want %s", clone.Message, original.Message)
	}
	if clone.Component != original.Component {
		t.Errorf("Component mismatch: got %s, want %s", clone.Component, original.Component)
	}
	if clone.ExpiresAt == nil || original.ExpiresAt == nil {
		t.Error("ExpiresAt should not be nil")
	} else if !clone.ExpiresAt.Equal(*original.ExpiresAt) {
		t.Errorf("ExpiresAt mismatch: got %v, want %v", clone.ExpiresAt, original.ExpiresAt)
	}

	// Verify metadata is copied
	if clone.Metadata["key1"] != "value1" {
		t.Errorf("Metadata key1 mismatch: got %v, want value1", clone.Metadata["key1"])
	}
	if clone.Metadata["key2"] != 42 {
		t.Errorf("Metadata key2 mismatch: got %v, want 42", clone.Metadata["key2"])
	}

	// Verify modifying clone doesn't affect original
	clone.Title = "Modified Title"
	clone.WithMetadata("key1", "modified")
	clone.WithMetadata("newKey", "newValue")

	if original.Title != "Original Title" {
		t.Error("Modifying clone should not affect original Title")
	}
	if original.Metadata["key1"] != "value1" {
		t.Error("Modifying clone metadata should not affect original")
	}
	if _, exists := original.Metadata["newKey"]; exists {
		t.Error("Adding to clone metadata should not affect original")
	}

	// Verify modifying original doesn't affect clone
	original.WithMetadata("key2", 999)
	if clone.Metadata["key2"] != 42 {
		t.Error("Modifying original should not affect clone")
	}
}

// TestCloneNilNotification verifies Clone() handles nil correctly
func TestCloneNilNotification(t *testing.T) {
	var nilNotif *Notification
	clone := nilNotif.Clone()
	if clone != nil {
		t.Error("Clone of nil should return nil")
	}
}
