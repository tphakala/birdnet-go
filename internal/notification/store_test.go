package notification

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

// TestInMemoryStoreUnreadCountExcludesToasts locks in that toast-flagged
// notifications are not counted by GetUnreadCount. Regression guard: the
// previous counter-based implementation blindly incremented on every Save,
// which caused the guest NotificationBell badge to include ephemeral toasts
// even though they never appeared in the list view.
func TestInMemoryStoreUnreadCountExcludesToasts(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore(100)

	regular := NewNotification(TypeDetection, PriorityHigh, "Detection", "body")
	mustStoreSave(t, store, regular)

	toast := NewNotification(TypeInfo, PriorityLow, "Toast", "toast body").
		WithMetadata(MetadataKeyIsToast, true)
	mustStoreSave(t, store, toast)

	// GetUnreadCount must count only the non-toast unread notification.
	assertStoreUnreadCount(t, store, 1, "toast must not be counted in unread")

	// Opt-in via Count with IncludeToasts still sees the toast when asked.
	total, err := store.Count(&FilterOptions{
		Status:        []Status{StatusUnread},
		IncludeToasts: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, total, "IncludeToasts must surface toast entries")
}

// TestInMemoryStoreCountWithFilter exercises the typed Count method directly,
// covering the filter paths callers rely on (type-only, status-only, empty,
// and IncludeToasts opt-in).
func TestInMemoryStoreCountWithFilter(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore(100)

	detectionUnread := NewNotification(TypeDetection, PriorityHigh, "Det unread", "body")
	detectionRead := NewNotification(TypeDetection, PriorityHigh, "Det read", "body")
	detectionRead.MarkAsRead()
	errorUnread := NewNotification(TypeError, PriorityCritical, "Err", "body")
	toast := NewNotification(TypeInfo, PriorityLow, "Toast", "body").
		WithMetadata(MetadataKeyIsToast, true)

	for _, n := range []*Notification{detectionUnread, detectionRead, errorUnread, toast} {
		mustStoreSave(t, store, n)
	}

	cases := []struct {
		name   string
		filter *FilterOptions
		want   int
	}{
		{"nil filter excludes toast", nil, 3},
		{"empty filter excludes toast", &FilterOptions{}, 3},
		{"detection only", &FilterOptions{Types: []Type{TypeDetection}}, 2},
		{"unread only", &FilterOptions{Status: []Status{StatusUnread}}, 2},
		{"detection + unread", &FilterOptions{
			Types:  []Type{TypeDetection},
			Status: []Status{StatusUnread},
		}, 1},
		{"include toasts", &FilterOptions{IncludeToasts: true}, 4},
		{"include toasts + unread", &FilterOptions{
			Status:        []Status{StatusUnread},
			IncludeToasts: true,
		}, 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := store.Count(tc.filter)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestInMemoryStore_MarkAllRead(t *testing.T) {
	t.Parallel()

	t.Run("marks unread non-toast notifications", func(t *testing.T) {
		t.Parallel()
		store := NewInMemoryStore(100)

		n1 := NewNotification(TypeInfo, PriorityMedium, "Info", "msg1")
		n2 := NewNotification(TypeWarning, PriorityHigh, "Warning", "msg2")
		n3 := NewNotification(TypeInfo, PriorityMedium, "Already read", "msg3")
		n3.MarkAsRead()

		mustStoreSave(t, store, n1)
		mustStoreSave(t, store, n2)
		mustStoreSave(t, store, n3)

		changed, err := store.MarkAllRead()
		require.NoError(t, err)
		assert.Equal(t, 2, changed)

		assertStoreUnreadCount(t, store, 0, "all should be read")
	})

	t.Run("skips toast notifications", func(t *testing.T) {
		t.Parallel()
		store := NewInMemoryStore(100)

		regular := NewNotification(TypeInfo, PriorityMedium, "Regular", "msg")
		toast := NewNotification(TypeInfo, PriorityLow, "Toast", "toast msg")
		toast.Metadata = map[string]any{MetadataKeyIsToast: true}

		mustStoreSave(t, store, regular)
		mustStoreSave(t, store, toast)

		changed, err := store.MarkAllRead()
		require.NoError(t, err)
		assert.Equal(t, 1, changed)

		got, err := store.Get(toast.ID)
		require.NoError(t, err)
		assert.Equal(t, StatusUnread, got.Status, "toast should remain unread")
	})

	t.Run("empty store returns zero", func(t *testing.T) {
		t.Parallel()
		store := NewInMemoryStore(100)

		changed, err := store.MarkAllRead()
		require.NoError(t, err)
		assert.Equal(t, 0, changed)
	})

	t.Run("idempotent on already-read notifications", func(t *testing.T) {
		t.Parallel()
		store := NewInMemoryStore(100)

		n := NewNotification(TypeInfo, PriorityMedium, "Test", "msg")
		mustStoreSave(t, store, n)

		changed1, err := store.MarkAllRead()
		require.NoError(t, err)
		assert.Equal(t, 1, changed1)

		changed2, err := store.MarkAllRead()
		require.NoError(t, err)
		assert.Equal(t, 0, changed2, "second call should change nothing")
	})
}

// TestInMemoryStore_GetReturnsIndependentMetadata verifies that mutating the
// Metadata map of a notification returned by Get does not leak back into the
// stored notification. A shallow copy (*notif) aliases the map; the store must
// hand out a deep copy so REST callers cannot corrupt stored state and so
// concurrent JSON marshaling cannot race an in-place map write. Regression test
// for the GetNotification/GetNotifications shared-pointer hazard.
func TestInMemoryStore_GetReturnsIndependentMetadata(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore(100)
	n := NewNotification(TypeInfo, PriorityMedium, "Test", "msg").
		WithMetadata("seed", "value")
	mustStoreSave(t, store, n)

	got1 := mustStoreGet(t, store, n.ID)
	require.Contains(t, got1.Metadata, "seed")
	got1.Metadata["injected"] = "leaked"

	got2 := mustStoreGet(t, store, n.ID)
	assert.NotContains(t, got2.Metadata, "injected",
		"mutating a returned notification's Metadata must not affect the stored copy")
}

// TestInMemoryStore_ListReturnsIndependentMetadata is the List counterpart of
// TestInMemoryStore_GetReturnsIndependentMetadata.
func TestInMemoryStore_ListReturnsIndependentMetadata(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore(100)
	n := NewNotification(TypeInfo, PriorityMedium, "Test", "msg").
		WithMetadata("seed", "value")
	mustStoreSave(t, store, n)

	first, err := store.List(&FilterOptions{})
	require.NoError(t, err)
	require.Len(t, first, 1)
	first[0].Metadata["injected"] = "leaked"

	second, err := store.List(&FilterOptions{})
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.NotContains(t, second[0].Metadata, "injected",
		"mutating a listed notification's Metadata must not affect the stored copy")
}

// TestInMemoryStore_ConcurrentGetMetadataIsRaceFree exercises the real hazard
// from GetNotification/GetNotifications: many readers JSON-marshal notifications
// fetched from the store while other callers mutate the Metadata of their own
// fetched copies. If Get returned a shallow copy aliasing the stored map, the
// readers' marshal would race the writers' map writes and the runtime's
// concurrent-map detector (and go test -race) would abort. A deep copy makes
// every returned notification independent.
func TestInMemoryStore_ConcurrentGetMetadataIsRaceFree(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore(100)
	n := NewNotification(TypeInfo, PriorityMedium, "Concurrent", "msg").
		WithMetadata("seed", "value")
	mustStoreSave(t, store, n)
	id := n.ID

	const workers = 8
	const iterations = 200
	var wg sync.WaitGroup

	for range workers {
		wg.Go(func() {
			for range iterations {
				got, err := store.Get(id)
				if err != nil {
					continue
				}
				_, _ = json.Marshal(got)
			}
		})
	}
	for w := range workers {
		wg.Go(func() {
			for i := range iterations {
				got, err := store.Get(id)
				if err != nil {
					continue
				}
				got.Metadata["w"] = w*iterations + i
			}
		})
	}
	wg.Wait()
}

// TestInMemoryStore_GetReturnsIndependentParamsAndExpiry extends the
// independence guarantee to the other reference-typed fields that Clone copies
// and REST handlers serialize: the TitleParams/MessageParams maps and the
// ExpiresAt pointer. A future regression that deep-copied only Metadata would
// slip past the Metadata-only tests but be caught here.
func TestInMemoryStore_GetReturnsIndependentParamsAndExpiry(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore(100)
	n := NewNotification(TypeInfo, PriorityMedium, "Test", "msg").
		WithTitleKey("title.key", map[string]any{"a": "1"}).
		WithMessageKey("message.key", map[string]any{"b": "2"}).
		WithExpiry(time.Hour)
	require.NotNil(t, n.ExpiresAt)
	originalExpiry := *n.ExpiresAt
	mustStoreSave(t, store, n)

	got1 := mustStoreGet(t, store, n.ID)
	require.Contains(t, got1.TitleParams, "a")
	require.Contains(t, got1.MessageParams, "b")
	require.NotNil(t, got1.ExpiresAt)

	got1.TitleParams["injected"] = "x"
	got1.MessageParams["injected"] = "y"
	*got1.ExpiresAt = got1.ExpiresAt.Add(24 * time.Hour)

	got2 := mustStoreGet(t, store, n.ID)
	assert.NotContains(t, got2.TitleParams, "injected",
		"mutating a returned notification's TitleParams must not affect the stored copy")
	assert.NotContains(t, got2.MessageParams, "injected",
		"mutating a returned notification's MessageParams must not affect the stored copy")
	require.NotNil(t, got2.ExpiresAt)
	assert.True(t, got2.ExpiresAt.Equal(originalExpiry),
		"mutating a returned notification's ExpiresAt must not affect the stored copy")
}

// listIDs returns the IDs of all notifications from store.List, in order.
func listIDs(t *testing.T, store *InMemoryStore) []string {
	t.Helper()
	got, err := store.List(nil)
	require.NoError(t, err)
	ids := make([]string, len(got))
	for i, n := range got {
		ids[i] = n.ID
	}
	return ids
}

// TestInMemoryStoreListDeterministicOrderOnEqualTimestamps verifies that List
// returns a stable, creation-ordered result when notifications share the same
// Timestamp. Equal timestamps are routine on coarse-resolution clocks (Windows'
// ~15ms monotonic tick) and possible anywhere under load. Without a tiebreaker,
// List relied on an unstable sort over a map-ordered slice, so the order of
// equal-timestamp notifications (and which one a Limit:1 query returned) was
// nondeterministic.
func TestInMemoryStoreListDeterministicOrderOnEqualTimestamps(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore(1000)
	fixedTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	const count = 50
	createdIDs := make([]string, count)
	for i := range count {
		n := NewNotification(TypeInfo, PriorityLow, "title", "message")
		n.Timestamp = fixedTime // force identical timestamps to exercise the tiebreaker
		createdIDs[i] = n.ID
		mustStoreSave(t, store, n)
	}

	// Expected order: newest-created first. With all timestamps equal, the
	// creation-sequence tiebreaker orders by descending seq, i.e. reverse
	// insertion order.
	wantIDs := make([]string, count)
	for i := range count {
		wantIDs[i] = createdIDs[count-1-i]
	}

	first := listIDs(t, store)
	assert.Equal(t, wantIDs, first,
		"List must order equal-timestamp notifications newest-created first")

	// Repeat to defeat Go's per-range map-iteration randomization: a non-total
	// ordering would surface different orders across calls within one process.
	for range 20 {
		assert.Equal(t, first, listIDs(t, store),
			"List order must be identical across repeated calls")
	}

	// Limit:1 must deterministically return the most recently created.
	limited, err := store.List(&FilterOptions{Limit: 1})
	require.NoError(t, err)
	require.Len(t, limited, 1)
	assert.Equal(t, createdIDs[count-1], limited[0].ID,
		"Limit:1 must return the newest-created notification")
}

// TestInMemoryStoreListOrdersByTimestampNewestFirst verifies the primary sort
// key: notifications with distinct timestamps are returned newest-first. This
// guards the timestamp comparison direction independently of the equal-timestamp
// tiebreaker (a sign error here would be invisible to the all-equal-timestamp
// test above).
func TestInMemoryStoreListOrdersByTimestampNewestFirst(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore(1000)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	const count = 10
	// Save oldest-to-newest; List must return newest-first.
	wantNewestFirst := make([]string, count)
	for i := range count {
		n := NewNotification(TypeInfo, PriorityLow, "title", "message")
		n.Timestamp = base.Add(time.Duration(i) * time.Minute)
		mustStoreSave(t, store, n)
		wantNewestFirst[count-1-i] = n.ID // newest (largest i) goes first
	}

	assert.Equal(t, wantNewestFirst, listIDs(t, store),
		"List must return distinct-timestamp notifications newest-first")
}

// TestInMemoryStoreListSeqZeroFallsBackToID verifies the final tiebreaker:
// notifications built without NewNotification (seq == 0, e.g. struct literals)
// that share a Timestamp are ordered deterministically by ascending ID.
func TestInMemoryStoreListSeqZeroFallsBackToID(t *testing.T) {
	t.Parallel()

	store := NewInMemoryStore(1000)
	fixedTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Insertion order deliberately differs from sorted-ID order; all seq == 0.
	for _, id := range []string{"d", "a", "c", "b"} {
		mustStoreSave(t, store, &Notification{
			ID:        id,
			Type:      TypeInfo,
			Priority:  PriorityLow,
			Status:    StatusUnread,
			Timestamp: fixedTime,
		})
	}

	assert.Equal(t, []string{"a", "b", "c", "d"}, listIDs(t, store),
		"equal-timestamp seq-0 notifications must sort by ascending ID")
}

// TestInMemoryStoreRemoveOldestDeterministicOnEqualTimestamps verifies that
// eviction at capacity is deterministic when notifications share a Timestamp:
// the earliest-created (lowest seq) entry is evicted, consistent with List
// ranking it last.
func TestInMemoryStoreRemoveOldestDeterministicOnEqualTimestamps(t *testing.T) {
	t.Parallel()

	const maxSize = 3
	store := NewInMemoryStore(maxSize)
	fixedTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	const total = 5
	created := make([]string, total)
	for i := range total {
		n := NewNotification(TypeInfo, PriorityLow, "title", "message")
		n.Timestamp = fixedTime
		created[i] = n.ID
		mustStoreSave(t, store, n)
	}

	// The two earliest-created (created[0], created[1]) must have been evicted;
	// the three most recently created remain, newest-first.
	got := listIDs(t, store)
	require.Len(t, got, maxSize)
	assert.Equal(t, []string{created[4], created[3], created[2]}, got,
		"eviction must drop the earliest-created equal-timestamp notifications")
}
