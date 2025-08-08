package notification

import (
	"testing"
	"time"
)

// TestGenerateContentHash verifies that content hash generation works correctly
func TestGenerateContentHash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		notif1   *Notification
		notif2   *Notification
		sameHash bool
	}{
		{
			name: "identical notifications produce same hash",
			notif1: &Notification{
				Type:      TypeError,
				Title:     "Error Title",
				Message:   "Error message",
				Component: "test",
			},
			notif2: &Notification{
				Type:      TypeError,
				Title:     "Error Title",
				Message:   "Error message",
				Component: "test",
			},
			sameHash: true,
		},
		{
			name: "different messages produce different hashes",
			notif1: &Notification{
				Type:      TypeError,
				Title:     "Error Title",
				Message:   "Error message 1",
				Component: "test",
			},
			notif2: &Notification{
				Type:      TypeError,
				Title:     "Error Title",
				Message:   "Error message 2",
				Component: "test",
			},
			sameHash: false,
		},
		{
			name: "different components produce different hashes",
			notif1: &Notification{
				Type:      TypeError,
				Title:     "Error Title",
				Message:   "Error message",
				Component: "component1",
			},
			notif2: &Notification{
				Type:      TypeError,
				Title:     "Error Title",
				Message:   "Error message",
				Component: "component2",
			},
			sameHash: false,
		},
		{
			name: "component case normalization",
			notif1: &Notification{
				Type:      TypeError,
				Title:     "Error Title",
				Message:   "Error message",
				Component: "DiskManager",
			},
			notif2: &Notification{
				Type:      TypeError,
				Title:     "Error Title",
				Message:   "Error message",
				Component: "diskmanager",
			},
			sameHash: true,
		},
		{
			name: "whitespace trimming in message",
			notif1: &Notification{
				Type:      TypeError,
				Title:     "Error Title",
				Message:   "  Error message  ",
				Component: "test",
			},
			notif2: &Notification{
				Type:      TypeError,
				Title:     "Error Title",
				Message:   "Error message",
				Component: "test",
			},
			sameHash: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash1 := tt.notif1.GenerateContentHash()
			hash2 := tt.notif2.GenerateContentHash()

			if tt.sameHash && hash1 != hash2 {
				t.Errorf("Expected same hash, got different: %s != %s", hash1, hash2)
			}
			if !tt.sameHash && hash1 == hash2 {
				t.Errorf("Expected different hashes, got same: %s", hash1)
			}
		})
	}
}

// TestInMemoryStoreDeduplication tests the deduplication logic in the store
func TestInMemoryStoreDeduplication(t *testing.T) {
	t.Parallel()

	t.Run("duplicate within window increments count", testDuplicateWithinWindow)
	t.Run("duplicate outside window creates new notification", testDuplicateOutsideWindow)
	t.Run("priority escalation on duplicate", testPriorityEscalation)
	t.Run("read status reset on duplicate", testReadStatusReset)
	t.Run("hash index cleanup on delete", testHashIndexCleanup)
	t.Run("multiple different notifications", testMultipleDifferentNotifications)
}

// testDuplicateWithinWindow tests that duplicates within the deduplication window increment the count
func testDuplicateWithinWindow(t *testing.T) {
	t.Parallel()
	store := NewInMemoryStore(100)
	store.SetDeduplicationWindow(5 * time.Minute)

	// Create first notification
	notif1 := NewNotification(TypeError, PriorityMedium, "Test Error", "Disk error occurred").
		WithComponent("diskmanager")

	// Save first notification
	_, err := store.Save(notif1)
	if err != nil {
		t.Fatalf("Failed to save first notification: %v", err)
	}

	// Create duplicate notification
	notif2 := NewNotification(TypeError, PriorityMedium, "Test Error", "Disk error occurred").
		WithComponent("diskmanager")

	// Save duplicate - should deduplicate
	_, err = store.Save(notif2)
	if err != nil {
		t.Fatalf("Failed to save duplicate notification: %v", err)
	}

	// Verify only one notification exists
	notifications, err := store.List(nil)
	if err != nil {
		t.Fatalf("Failed to list notifications: %v", err)
	}

	if len(notifications) != 1 {
		t.Errorf("Expected 1 notification, got %d", len(notifications))
	}

	// Verify occurrence count was incremented
	if notifications[0].OccurrenceCount != 2 {
		t.Errorf("Expected occurrence count 2, got %d", notifications[0].OccurrenceCount)
	}
}

// testDuplicateOutsideWindow tests that duplicates outside the deduplication window create new notifications
func testDuplicateOutsideWindow(t *testing.T) {
	t.Parallel()
	store := NewInMemoryStore(100)
	store.SetDeduplicationWindow(100 * time.Millisecond) // Very short window for testing

	// Create first notification with timestamp in the past
	notif1 := NewNotification(TypeError, PriorityMedium, "Test Error", "Disk error occurred").
		WithComponent("diskmanager")
	// Manually set timestamp to be in the past (outside deduplication window)
	pastTime := time.Now().Add(-200 * time.Millisecond)
	notif1.Timestamp = pastTime

	// Save first notification
	_, err := store.Save(notif1)
	if err != nil {
		t.Fatalf("Failed to save first notification: %v", err)
	}

	// Create duplicate notification with current timestamp
	notif2 := NewNotification(TypeError, PriorityMedium, "Test Error", "Disk error occurred").
		WithComponent("diskmanager")

	// Save duplicate - should create new notification since window expired
	_, err = store.Save(notif2)
	if err != nil {
		t.Fatalf("Failed to save duplicate notification: %v", err)
	}

	// Verify two notifications exist
	notifications, err := store.List(nil)
	if err != nil {
		t.Fatalf("Failed to list notifications: %v", err)
	}

	if len(notifications) != 2 {
		t.Errorf("Expected 2 notifications, got %d", len(notifications))
	}

	// Both should have occurrence count of 1
	for i, n := range notifications {
		if n.OccurrenceCount != 1 {
			t.Errorf("Notification %d: expected occurrence count 1, got %d", i, n.OccurrenceCount)
		}
	}
}

// testPriorityEscalation tests that duplicate notifications with higher priority update the existing one
func testPriorityEscalation(t *testing.T) {
	t.Parallel()
	store := NewInMemoryStore(100)
	store.SetDeduplicationWindow(5 * time.Minute)

	// Create first notification with medium priority
	notif1 := NewNotification(TypeError, PriorityMedium, "Test Error", "Disk error occurred").
		WithComponent("diskmanager")

	_, err := store.Save(notif1)
	if err != nil {
		t.Fatalf("Failed to save first notification: %v", err)
	}

	// Create duplicate with higher priority
	notif2 := NewNotification(TypeError, PriorityHigh, "Test Error", "Disk error occurred").
		WithComponent("diskmanager")

	_, err = store.Save(notif2)
	if err != nil {
		t.Fatalf("Failed to save duplicate notification: %v", err)
	}

	// Verify priority was escalated
	notifications, err := store.List(nil)
	if err != nil {
		t.Fatalf("Failed to list notifications: %v", err)
	}

	if len(notifications) != 1 {
		t.Errorf("Expected 1 notification, got %d", len(notifications))
	}

	if notifications[0].Priority != PriorityHigh {
		t.Errorf("Expected priority to be escalated to high, got %s", notifications[0].Priority)
	}
}

// testReadStatusReset tests that duplicate notifications reset the read status
func testReadStatusReset(t *testing.T) {
	t.Parallel()
	store := NewInMemoryStore(100)
	store.SetDeduplicationWindow(5 * time.Minute)

	// Create and save first notification
	notif1 := NewNotification(TypeError, PriorityMedium, "Test Error", "Disk error occurred").
		WithComponent("diskmanager")

	_, err := store.Save(notif1)
	if err != nil {
		t.Fatalf("Failed to save first notification: %v", err)
	}

	// Get the notification from store and mark as read
	savedNotif, err := store.Get(notif1.ID)
	if err != nil {
		t.Fatalf("Failed to get notification: %v", err)
	}
	savedNotif.MarkAsRead()
	err = store.Update(savedNotif)
	if err != nil {
		t.Fatalf("Failed to update notification: %v", err)
	}

	// Create duplicate notification
	notif2 := NewNotification(TypeError, PriorityMedium, "Test Error", "Disk error occurred").
		WithComponent("diskmanager")

	_, err = store.Save(notif2)
	if err != nil {
		t.Fatalf("Failed to save duplicate notification: %v", err)
	}

	// Verify status was reset to unread
	notifications, err := store.List(nil)
	if err != nil {
		t.Fatalf("Failed to list notifications: %v", err)
	}

	if notifications[0].Status != StatusUnread {
		t.Errorf("Expected status to be reset to unread, got %s", notifications[0].Status)
	}

	// Verify unread count
	unreadCount, err := store.GetUnreadCount()
	if err != nil {
		t.Fatalf("Failed to get unread count: %v", err)
	}

	if unreadCount != 1 {
		t.Errorf("Expected unread count 1, got %d", unreadCount)
	}
}

// testHashIndexCleanup tests that the hash index is cleaned up when notifications are deleted
func testHashIndexCleanup(t *testing.T) {
	t.Parallel()
	store := NewInMemoryStore(100)
	store.SetDeduplicationWindow(5 * time.Minute)

	// Create and save notification
	notif := NewNotification(TypeError, PriorityMedium, "Test Error", "Disk error occurred").
		WithComponent("diskmanager")

	_, err := store.Save(notif)
	if err != nil {
		t.Fatalf("Failed to save notification: %v", err)
	}

	// Verify it exists in hash index
	existing, found := store.FindByContentHash(notif.ContentHash)
	if !found {
		t.Error("Expected to find notification in hash index")
	}
	if existing.ID != notif.ID {
		t.Errorf("Expected notification ID %s, got %s", notif.ID, existing.ID)
	}

	// Delete notification
	err = store.Delete(notif.ID)
	if err != nil {
		t.Fatalf("Failed to delete notification: %v", err)
	}

	// Verify it's removed from hash index
	_, found = store.FindByContentHash(notif.ContentHash)
	if found {
		t.Error("Expected notification to be removed from hash index")
	}
}

// testMultipleDifferentNotifications tests that multiple different notifications are stored separately
func testMultipleDifferentNotifications(t *testing.T) {
	t.Parallel()
	store := NewInMemoryStore(100)
	store.SetDeduplicationWindow(5 * time.Minute)

	// Create different notifications
	notif1 := NewNotification(TypeError, PriorityMedium, "Error 1", "First error").
		WithComponent("component1")

	notif2 := NewNotification(TypeWarning, PriorityLow, "Warning 1", "First warning").
		WithComponent("component2")

	notif3 := NewNotification(TypeInfo, PriorityLow, "Info 1", "Information").
		WithComponent("component3")

	// Save all notifications
	for _, n := range []*Notification{notif1, notif2, notif3} {
		_, err := store.Save(n)
		if err != nil {
			t.Fatalf("Failed to save notification: %v", err)
		}
	}

	// Verify all three exist
	notifications, err := store.List(nil)
	if err != nil {
		t.Fatalf("Failed to list notifications: %v", err)
	}

	if len(notifications) != 3 {
		t.Errorf("Expected 3 notifications, got %d", len(notifications))
	}

	// All should have occurrence count of 1
	for i, n := range notifications {
		if n.OccurrenceCount != 1 {
			t.Errorf("Notification %d: expected occurrence count 1, got %d", i, n.OccurrenceCount)
		}
	}
}

// TestHashIndexCleanup tests the automatic cleanup of expired hash index entries
func TestHashIndexCleanup(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore(100)
	store.SetDeduplicationWindow(100 * time.Millisecond) // Short window for testing

	// Create notifications with old timestamps
	oldNotif1 := NewNotification(TypeError, PriorityMedium, "Old Error 1", "Old error message 1").
		WithComponent("component1")
	oldNotif1.Timestamp = time.Now().Add(-2 * time.Hour) // Way outside deduplication window

	oldNotif2 := NewNotification(TypeError, PriorityMedium, "Old Error 2", "Old error message 2").
		WithComponent("component2")
	oldNotif2.Timestamp = time.Now().Add(-2 * time.Hour) // Way outside deduplication window

	// Save old notifications
	_, err := store.Save(oldNotif1)
	if err != nil {
		t.Fatalf("Failed to save old notification 1: %v", err)
	}
	_, err = store.Save(oldNotif2)
	if err != nil {
		t.Fatalf("Failed to save old notification 2: %v", err)
	}

	// Delete one of the old notifications from main store but keep in hash index
	err = store.Delete(oldNotif1.ID)
	if err != nil {
		t.Fatalf("Failed to delete notification: %v", err)
	}

	// Manually add back to hash index to simulate orphaned entry
	store.forceHashIndexEntry(oldNotif1.ContentHash, oldNotif1)
	initialHashCount := store.getHashIndexCount()

	// Verify we have entries in hash index
	if initialHashCount < 2 {
		t.Errorf("Expected at least 2 entries in hash index, got %d", initialHashCount)
	}

	// Force cleanup by setting lastCleanup to over an hour ago
	store.forceCleanupTrigger()

	// Create a new notification to trigger cleanup
	newNotif := NewNotification(TypeInfo, PriorityLow, "New Info", "New message").
		WithComponent("component3")

	_, err = store.Save(newNotif)
	if err != nil {
		t.Fatalf("Failed to save new notification: %v", err)
	}

	// Check that cleanup occurred
	finalHashCount := store.getHashIndexCount()
	
	// Should have the new notification and oldNotif2 (still in main store)
	// oldNotif1 should be cleaned up as it was deleted from main store
	hasNewNotif := store.hasHashIndexEntry(newNotif.ContentHash)
	hasOldNotif1 := store.hasHashIndexEntry(oldNotif1.ContentHash)
	hasOldNotif2 := store.hasHashIndexEntry(oldNotif2.ContentHash)

	// Verify cleanup results
	if !hasNewNotif {
		t.Error("Expected new notification to be in hash index")
	}
	if hasOldNotif1 {
		t.Error("Expected oldNotif1 to be cleaned up from hash index (was deleted from main store)")
	}
	if !hasOldNotif2 {
		t.Error("Expected oldNotif2 to remain in hash index (still in main store)")
	}

	// The hash index should have exactly 2 entries: newNotif and oldNotif2
	if finalHashCount != 2 {
		t.Errorf("Expected 2 entries in hash index after cleanup, got %d", finalHashCount)
	}
}

// TestServiceDeduplication tests deduplication at the service level
func TestServiceDeduplication(t *testing.T) {
	t.Parallel()

	t.Run("service deduplicates identical notifications", func(t *testing.T) {
		config := &ServiceConfig{
			MaxNotifications:    100,
			CleanupInterval:     1 * time.Hour,
			RateLimitWindow:     1 * time.Minute,
			RateLimitMaxEvents:  100,
			DeduplicationWindow: 5 * time.Minute,
		}

		service := NewService(config)
		defer service.Stop()

		// Create multiple identical notifications
		for i := 0; i < 5; i++ {
			_, err := service.CreateWithComponent(
				TypeError,
				PriorityMedium,
				"diskmanager Issue",
				"diskmanager: invalid audio filename format 'out.m4a' (has 1 parts, expected at least 3)",
				"diskmanager",
			)
			if err != nil {
				t.Fatalf("Failed to create notification %d: %v", i, err)
			}
		}

		// List notifications
		notifications, err := service.List(nil)
		if err != nil {
			t.Fatalf("Failed to list notifications: %v", err)
		}

		// Should have only 1 notification due to deduplication
		if len(notifications) != 1 {
			t.Errorf("Expected 1 notification after deduplication, got %d", len(notifications))
		}

		// Occurrence count should be 5
		if notifications[0].OccurrenceCount != 5 {
			t.Errorf("Expected occurrence count 5, got %d", notifications[0].OccurrenceCount)
		}
	})
}

// TestPriorityWeight tests the priority weight function
func TestPriorityWeight(t *testing.T) {
	t.Parallel()

	tests := []struct {
		priority Priority
		expected int
	}{
		{PriorityCritical, 4},
		{PriorityHigh, 3},
		{PriorityMedium, 2},
		{PriorityLow, 1},
		{Priority("unknown"), 0},
	}

	for _, tt := range tests {
		t.Run(string(tt.priority), func(t *testing.T) {
			weight := getPriorityWeight(tt.priority)
			if weight != tt.expected {
				t.Errorf("Expected weight %d for priority %s, got %d", tt.expected, tt.priority, weight)
			}
		})
	}
}