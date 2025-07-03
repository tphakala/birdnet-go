package notification

import (
	"testing"
	"time"
)

// TestInMemoryStoreUnreadCount tests the optimized unread count tracking
func TestInMemoryStoreUnreadCount(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore(100)

	// Test 1: Initial count should be 0
	count, err := store.GetUnreadCount()
	if err != nil {
		t.Fatalf("GetUnreadCount failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected initial unread count to be 0, got %d", count)
	}

	// Test 2: Save unread notifications
	notif1 := NewNotification(TypeInfo, PriorityMedium, "Test 1", "Message 1")
	notif2 := NewNotification(TypeWarning, PriorityHigh, "Test 2", "Message 2")
	
	err = store.Save(notif1)
	if err != nil {
		t.Fatalf("Failed to save notification 1: %v", err)
	}
	
	err = store.Save(notif2)
	if err != nil {
		t.Fatalf("Failed to save notification 2: %v", err)
	}

	count, err = store.GetUnreadCount()
	if err != nil {
		t.Fatalf("GetUnreadCount failed: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected unread count to be 2, got %d", count)
	}

	// Test 3: Update notification to read
	// Get a fresh copy from the store to avoid pointer issues
	notif1Copy, _ := store.Get(notif1.ID)
	notif1Copy.MarkAsRead()
	err = store.Update(notif1Copy)
	if err != nil {
		t.Fatalf("Failed to update notification: %v", err)
	}

	count, err = store.GetUnreadCount()
	if err != nil {
		t.Fatalf("GetUnreadCount failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected unread count to be 1 after marking one as read, got %d", count)
	}

	// Test 4: Update read notification back to unread
	// First, get the notification from the store to ensure we have the current state
	storedNotif1, err := store.Get(notif1.ID)
	if err != nil {
		t.Fatalf("Failed to get notification: %v", err)
	}
	storedNotif1.Status = StatusUnread
	err = store.Update(storedNotif1)
	if err != nil {
		t.Fatalf("Failed to update notification: %v", err)
	}

	count, err = store.GetUnreadCount()
	if err != nil {
		t.Fatalf("GetUnreadCount failed: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected unread count to be 2 after marking back to unread, got %d", count)
	}

	// Test 5: Delete unread notification (use storedNotif1's ID)
	err = store.Delete(storedNotif1.ID)
	if err != nil {
		t.Fatalf("Failed to delete notification: %v", err)
	}

	count, err = store.GetUnreadCount()
	if err != nil {
		t.Fatalf("GetUnreadCount failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected unread count to be 1 after deleting unread notification, got %d", count)
	}

	// Test 6: Delete read notification (should not affect count)
	// Get a fresh copy to avoid pointer issues
	notif2Copy, _ := store.Get(notif2.ID)
	notif2Copy.MarkAsAcknowledged()
	err = store.Update(notif2Copy)
	if err != nil {
		t.Fatalf("Failed to update notification: %v", err)
	}

	// Count should be 0 now
	count, err = store.GetUnreadCount()
	if err != nil {
		t.Fatalf("GetUnreadCount failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected unread count to be 0 after marking as acknowledged, got %d", count)
	}

	err = store.Delete(notif2.ID)
	if err != nil {
		t.Fatalf("Failed to delete notification: %v", err)
	}

	count, err = store.GetUnreadCount()
	if err != nil {
		t.Fatalf("GetUnreadCount failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected unread count to still be 0 after deleting read notification, got %d", count)
	}
}

// TestInMemoryStoreDeleteExpired tests that unread count is updated when expired notifications are deleted
func TestInMemoryStoreDeleteExpired(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore(100)

	// Create notifications
	notif1 := NewNotification(TypeInfo, PriorityMedium, "Test 1", "Message 1")
	notif2 := NewNotification(TypeWarning, PriorityHigh, "Test 2", "Message 2")
	notif3 := NewNotification(TypeError, PriorityCritical, "Test 3", "Message 3")
	notif3.MarkAsRead() // This one is read

	// Set expiry times deterministically
	// notif1 and notif3 are expired (1 hour ago)
	pastTime := time.Now().Add(-1 * time.Hour)
	notif1.ExpiresAt = &pastTime
	notif3.ExpiresAt = &pastTime
	
	// notif2 expires in the future (1 hour from now)
	futureTime := time.Now().Add(1 * time.Hour)
	notif2.ExpiresAt = &futureTime

	// Save all notifications
	for _, notif := range []*Notification{notif1, notif2, notif3} {
		err := store.Save(notif)
		if err != nil {
			t.Fatalf("Failed to save notification: %v", err)
		}
	}

	// Initial count should be 2 (notif1 and notif2 are unread)
	count, err := store.GetUnreadCount()
	if err != nil {
		t.Fatalf("GetUnreadCount failed: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected initial unread count to be 2, got %d", count)
	}

	// Delete expired notifications (no sleep needed!)
	err = store.DeleteExpired()
	if err != nil {
		t.Fatalf("DeleteExpired failed: %v", err)
	}

	// Count should be 1 now (only notif2 remains and is unread)
	count, err = store.GetUnreadCount()
	if err != nil {
		t.Fatalf("GetUnreadCount failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected unread count to be 1 after deleting expired, got %d", count)
	}

	// Verify notif2 still exists
	retrieved, err := store.Get(notif2.ID)
	if err != nil {
		t.Fatalf("Failed to get notification: %v", err)
	}
	if retrieved == nil {
		t.Error("Expected notif2 to still exist")
	}
	
	// Verify notif1 and notif3 were deleted
	retrieved1, _ := store.Get(notif1.ID)
	if retrieved1 != nil {
		t.Error("Expected notif1 to be deleted")
	}
	
	retrieved3, _ := store.Get(notif3.ID)
	if retrieved3 != nil {
		t.Error("Expected notif3 to be deleted")
	}
}

// TestInMemoryStoreMaxSize tests that unread count is maintained when old notifications are removed
func TestInMemoryStoreMaxSize(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore(3) // Small size for testing

	// Create 4 notifications (more than max size)
	notifications := make([]*Notification, 4)
	baseTime := time.Now()
	for i := range 4 {
		notifications[i] = NewNotification(TypeInfo, PriorityMedium, "Test", "Message")
		// Set timestamps deterministically to ensure ordering
		notifications[i].Timestamp = baseTime.Add(time.Duration(i) * time.Second)
		err := store.Save(notifications[i])
		if err != nil {
			t.Fatalf("Failed to save notification %d: %v", i, err)
		}
	}

	// Should have 3 notifications (max size), all unread
	count, err := store.GetUnreadCount()
	if err != nil {
		t.Fatalf("GetUnreadCount failed: %v", err)
	}
	if count != 3 {
		t.Errorf("Expected unread count to be 3 (max size), got %d", count)
	}

	// Oldest notification should have been removed
	oldest, err := store.Get(notifications[0].ID)
	if err != nil {
		t.Fatalf("Failed to get notification: %v", err)
	}
	if oldest != nil {
		t.Error("Expected oldest notification to be removed")
	}

	// Newer notifications should still exist
	for i := range 3 {
		idx := i + 1 // Start from index 1
		notif, err := store.Get(notifications[idx].ID)
		if err != nil {
			t.Fatalf("Failed to get notification %d: %v", idx, err)
		}
		if notif == nil {
			t.Errorf("Expected notification %d to exist", idx)
		}
	}
}