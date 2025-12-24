package notification

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestInMemoryStoreUnreadCount tests the optimized unread count tracking
func TestInMemoryStoreUnreadCount(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore(100)

	// Test 1: Initial count should be 0
	assertStoreUnreadCount(t, store, 0, "Initial unread count")

	// Test 2: Save unread notifications
	notif1 := NewNotification(TypeInfo, PriorityMedium, "Test 1", "Message 1")
	notif2 := NewNotification(TypeWarning, PriorityHigh, "Test 2", "Message 2")

	mustStoreSave(t, store, notif1)
	mustStoreSave(t, store, notif2)
	assertStoreUnreadCount(t, store, 2, "Unread count after saving 2 notifications")

	// Test 3: Update notification to read
	notif1Copy := mustStoreGet(t, store, notif1.ID)
	notif1Copy.MarkAsRead()
	mustStoreUpdate(t, store, notif1Copy)
	assertStoreUnreadCount(t, store, 1, "Unread count after marking one as read")

	// Test 4: Update read notification back to unread
	storedNotif1 := mustStoreGet(t, store, notif1.ID)
	storedNotif1.Status = StatusUnread
	mustStoreUpdate(t, store, storedNotif1)
	assertStoreUnreadCount(t, store, 2, "Unread count after marking back to unread")

	// Test 5: Delete unread notification
	mustStoreDelete(t, store, storedNotif1.ID)
	assertStoreUnreadCount(t, store, 1, "Unread count after deleting unread notification")

	// Test 6: Delete read notification (should not affect count)
	notif2Copy := mustStoreGet(t, store, notif2.ID)
	notif2Copy.MarkAsAcknowledged()
	mustStoreUpdate(t, store, notif2Copy)
	assertStoreUnreadCount(t, store, 0, "Unread count after marking as acknowledged")

	mustStoreDelete(t, store, notif2.ID)
	assertStoreUnreadCount(t, store, 0, "Unread count after deleting read notification")
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
		mustStoreSave(t, store, notif)
	}

	// Initial count should be 2 (notif1 and notif2 are unread)
	assertStoreUnreadCount(t, store, 2, "Initial unread count")

	// Delete expired notifications
	err := store.DeleteExpired()
	require.NoError(t, err, "DeleteExpired should not fail")

	// Count should be 1 now (only notif2 remains and is unread)
	assertStoreUnreadCount(t, store, 1, "Unread count after deleting expired")

	// Verify notif2 still exists
	assertStoreNotificationExists(t, store, notif2.ID, true)

	// Verify notif1 and notif3 were deleted
	assertStoreNotificationExists(t, store, notif1.ID, false)
	assertStoreNotificationExists(t, store, notif3.ID, false)
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
		mustStoreSave(t, store, notifications[i])
	}

	// Should have 3 notifications (max size), all unread
	assertStoreUnreadCount(t, store, 3, "Unread count at max size")

	// Oldest notification should have been removed
	assertStoreNotificationExists(t, store, notifications[0].ID, false)

	// Newer notifications should still exist
	for i := range 3 {
		idx := i + 1 // Start from index 1
		assertStoreNotificationExists(t, store, notifications[idx].ID, true)
	}
}

// TestInMemoryStoreDelete tests the Delete method edge cases
func TestInMemoryStoreDelete(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore(100)

	// Test 1: Delete non-existent notification (should not error)
	err := store.Delete("non-existent-id")
	require.NoError(t, err, "Delete non-existent notification should not error")

	// Test 2: Delete empty ID
	err = store.Delete("")
	require.NoError(t, err, "Delete empty ID should not error")

	// Test 3: Create and delete notification
	notif := NewNotification(TypeInfo, PriorityMedium, "Test", "Message")
	mustStoreSave(t, store, notif)
	assertStoreNotificationExists(t, store, notif.ID, true)

	mustStoreDelete(t, store, notif.ID)
	assertStoreNotificationExists(t, store, notif.ID, false)

	// Test 4: Double delete (should not error)
	err = store.Delete(notif.ID)
	require.NoError(t, err, "Double delete should not error")

	// Test 5: Delete updates unread count correctly
	notif1 := NewNotification(TypeInfo, PriorityMedium, "Test 1", "Message 1")
	notif2 := NewNotification(TypeInfo, PriorityMedium, "Test 2", "Message 2")
	notif2.MarkAsRead()

	mustStoreSave(t, store, notif1)
	mustStoreSave(t, store, notif2)
	assertStoreUnreadCount(t, store, 1, "Initial unread count")

	// Delete read notification - count should not change
	mustStoreDelete(t, store, notif2.ID)
	assertStoreUnreadCount(t, store, 1, "Unread count after deleting read notification")

	// Delete unread notification - count should decrease
	mustStoreDelete(t, store, notif1.ID)
	assertStoreUnreadCount(t, store, 0, "Unread count after deleting unread notification")
}
