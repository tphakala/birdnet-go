package notification

import (
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Test helper functions

// assertUnreadCount checks the unread count and fails the test if it doesn't match expected value
func assertUnreadCount(t *testing.T, store *InMemoryStore, expected int, message string) {
	t.Helper()
	count, err := store.GetUnreadCount()
	if err != nil {
		t.Fatalf("GetUnreadCount failed: %v", err)
	}
	if count != expected {
		t.Errorf("%s: expected %d, got %d", message, expected, count)
	}
}

// mustGetNotification retrieves a notification and fails the test if an error occurs
func mustGetNotification(t *testing.T, store *InMemoryStore, id string) *Notification {
	t.Helper()
	notif, err := store.Get(id)
	if err != nil {
		t.Fatalf("Failed to get notification %s: %v", id, err)
	}
	if notif == nil {
		t.Fatalf("Notification %s not found", id)
	}
	return notif
}

// mustSaveNotification saves a notification and fails the test if an error occurs
func mustSaveNotification(t *testing.T, store *InMemoryStore, notif *Notification) {
	t.Helper()
	if err := store.Save(notif); err != nil {
		t.Fatalf("Failed to save notification: %v", err)
	}
}

// mustUpdateNotification updates a notification and fails the test if an error occurs
func mustUpdateNotification(t *testing.T, store *InMemoryStore, notif *Notification) {
	t.Helper()
	if err := store.Update(notif); err != nil {
		t.Fatalf("Failed to update notification: %v", err)
	}
}

// mustDeleteNotification deletes a notification and fails the test if an error occurs
func mustDeleteNotification(t *testing.T, store *InMemoryStore, id string) {
	t.Helper()
	if err := store.Delete(id); err != nil {
		t.Fatalf("Failed to delete notification %s: %v", id, err)
	}
}

// assertNotificationExists checks if a notification exists in the store
func assertNotificationExists(t *testing.T, store *InMemoryStore, id string, shouldExist bool) {
	t.Helper()
	notif, err := store.Get(id)
	if shouldExist {
		if err != nil {
			t.Fatalf("Failed to get notification %s: %v", id, err)
		}
		if notif == nil {
			t.Errorf("Expected notification %s to exist, but it doesn't", id)
		}
	} else {
		if err != nil && !errors.Is(err, ErrNotificationNotFound) {
			t.Fatalf("Unexpected error getting notification %s: %v", id, err)
		}
		if err == nil {
			t.Errorf("Expected notification %s to not exist, but Get() returned no error", id)
		}
		if notif != nil {
			t.Errorf("Expected notification %s to not exist, but it does", id)
		}
	}
}

// TestInMemoryStoreUnreadCount tests the optimized unread count tracking
func TestInMemoryStoreUnreadCount(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore(100)

	// Test 1: Initial count should be 0
	assertUnreadCount(t, store, 0, "Initial unread count")

	// Test 2: Save unread notifications
	notif1 := NewNotification(TypeInfo, PriorityMedium, "Test 1", "Message 1")
	notif2 := NewNotification(TypeWarning, PriorityHigh, "Test 2", "Message 2")
	
	mustSaveNotification(t, store, notif1)
	mustSaveNotification(t, store, notif2)
	assertUnreadCount(t, store, 2, "Unread count after saving 2 notifications")

	// Test 3: Update notification to read
	notif1Copy := mustGetNotification(t, store, notif1.ID)
	notif1Copy.MarkAsRead()
	mustUpdateNotification(t, store, notif1Copy)
	assertUnreadCount(t, store, 1, "Unread count after marking one as read")

	// Test 4: Update read notification back to unread
	storedNotif1 := mustGetNotification(t, store, notif1.ID)
	storedNotif1.Status = StatusUnread
	mustUpdateNotification(t, store, storedNotif1)
	assertUnreadCount(t, store, 2, "Unread count after marking back to unread")

	// Test 5: Delete unread notification
	mustDeleteNotification(t, store, storedNotif1.ID)
	assertUnreadCount(t, store, 1, "Unread count after deleting unread notification")

	// Test 6: Delete read notification (should not affect count)
	notif2Copy := mustGetNotification(t, store, notif2.ID)
	notif2Copy.MarkAsAcknowledged()
	mustUpdateNotification(t, store, notif2Copy)
	assertUnreadCount(t, store, 0, "Unread count after marking as acknowledged")

	mustDeleteNotification(t, store, notif2.ID)
	assertUnreadCount(t, store, 0, "Unread count after deleting read notification")
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
		mustSaveNotification(t, store, notif)
	}

	// Initial count should be 2 (notif1 and notif2 are unread)
	assertUnreadCount(t, store, 2, "Initial unread count")

	// Delete expired notifications (no sleep needed!)
	if err := store.DeleteExpired(); err != nil {
		t.Fatalf("DeleteExpired failed: %v", err)
	}

	// Count should be 1 now (only notif2 remains and is unread)
	assertUnreadCount(t, store, 1, "Unread count after deleting expired")

	// Verify notif2 still exists
	assertNotificationExists(t, store, notif2.ID, true)
	
	// Verify notif1 and notif3 were deleted
	assertNotificationExists(t, store, notif1.ID, false)
	assertNotificationExists(t, store, notif3.ID, false)
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
		mustSaveNotification(t, store, notifications[i])
	}

	// Should have 3 notifications (max size), all unread
	assertUnreadCount(t, store, 3, "Unread count at max size")

	// Oldest notification should have been removed
	assertNotificationExists(t, store, notifications[0].ID, false)

	// Newer notifications should still exist
	for i := range 3 {
		idx := i + 1 // Start from index 1
		assertNotificationExists(t, store, notifications[idx].ID, true)
	}
}