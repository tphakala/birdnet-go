/**
 * Notifications Integration Tests
 *
 * Tests the notification system API endpoints and workflows.
 * These tests run in a real browser against a real backend.
 *
 * Usage:
 *   1. Start backend: task integration-backend (in separate terminal)
 *   2. Run tests: npm run test:integration -- --run
 */

import { describe, expect, it } from 'vitest';
import { apiCall } from './integration-setup';

// Helper to generate unique IDs for test isolation
function uniqueTestId(): string {
  return `test-${Date.now()}-${Math.random().toString(36).substring(7)}`;
}

// Helper to create a test notification and return its ID
async function createTestNotification(): Promise<string> {
  const response = await apiCall('/notifications/test/new-species', {
    method: 'POST',
  });

  if (!response.ok) {
    throw new Error(`Failed to create test notification: ${response.status}`);
  }

  const notification = await response.json();
  return notification.id;
}

// Helper to get a notification by ID
async function getNotification(id: string): Promise<Response> {
  return apiCall(`/notifications/${id}`);
}

// Helper to delete a notification
async function deleteNotification(id: string): Promise<Response> {
  return apiCall(`/notifications/${id}`, { method: 'DELETE' });
}

// Helper to mark notification as read
async function markAsRead(id: string): Promise<Response> {
  return apiCall(`/notifications/${id}/read`, { method: 'PUT' });
}

// Helper to mark notification as acknowledged
async function markAsAcknowledged(id: string): Promise<Response> {
  return apiCall(`/notifications/${id}/acknowledge`, { method: 'PUT' });
}

// ============================================================================
// Basic Notification CRUD Operations
// ============================================================================

describe('Notifications CRUD API', () => {
  it('can list notifications', async () => {
    const response = await apiCall('/notifications');

    expect(response.ok).toBe(true);

    const data = await response.json();
    expect(data).toHaveProperty('notifications');
    expect(data).toHaveProperty('count');
    expect(Array.isArray(data.notifications)).toBe(true);
  });

  it('can create test notification', async () => {
    const response = await apiCall('/notifications/test/new-species', {
      method: 'POST',
    });

    expect(response.ok).toBe(true);

    const notification = await response.json();
    expect(notification).toHaveProperty('id');
    expect(notification).toHaveProperty('type');
    expect(notification).toHaveProperty('priority');
    expect(notification).toHaveProperty('title');
    expect(notification).toHaveProperty('message');
    expect(notification).toHaveProperty('status');
    expect(notification).toHaveProperty('timestamp');
    expect(notification.type).toBe('detection');
    expect(notification.priority).toBe('high');
    expect(notification.status).toBe('unread');

    // Clean up
    await deleteNotification(notification.id);
  });

  it('can get notification by ID', async () => {
    // Create a notification first
    const notificationId = await createTestNotification();

    // Get the notification
    const response = await getNotification(notificationId);

    expect(response.ok).toBe(true);

    const notification = await response.json();
    expect(notification.id).toBe(notificationId);
    expect(notification).toHaveProperty('type');
    expect(notification).toHaveProperty('title');
    expect(notification).toHaveProperty('message');

    // Clean up
    await deleteNotification(notificationId);
  });

  it('returns 404 for non-existent notification', async () => {
    const fakeId = 'non-existent-notification-id-12345';
    const response = await getNotification(fakeId);

    expect(response.status).toBe(404);
  });

  it('can delete notification', async () => {
    // Create a notification
    const notificationId = await createTestNotification();

    // Verify it exists
    const getResponse = await getNotification(notificationId);
    expect(getResponse.ok).toBe(true);

    // Delete it
    const deleteResponse = await deleteNotification(notificationId);
    expect(deleteResponse.ok).toBe(true);

    const deleteResult = await deleteResponse.json();
    expect(deleteResult.message).toBe('Notification deleted');

    // Verify it's gone
    const verifyResponse = await getNotification(notificationId);
    expect(verifyResponse.status).toBe(404);
  });
});

// ============================================================================
// Notification State Transitions
// ============================================================================

describe('Notification State Transitions', () => {
  it('notification starts as unread', async () => {
    const notificationId = await createTestNotification();

    const response = await getNotification(notificationId);
    const notification = await response.json();

    expect(notification.status).toBe('unread');

    // Clean up
    await deleteNotification(notificationId);
  });

  it('can mark notification as read', async () => {
    const notificationId = await createTestNotification();

    // Mark as read
    const markResponse = await markAsRead(notificationId);
    expect(markResponse.ok).toBe(true);

    const markResult = await markResponse.json();
    expect(markResult.message).toBe('Notification marked as read');

    // Verify status changed
    const getResponse = await getNotification(notificationId);
    const notification = await getResponse.json();
    expect(notification.status).toBe('read');

    // Clean up
    await deleteNotification(notificationId);
  });

  it('can mark notification as acknowledged', async () => {
    const notificationId = await createTestNotification();

    // First mark as read
    await markAsRead(notificationId);

    // Then acknowledge
    const ackResponse = await markAsAcknowledged(notificationId);
    expect(ackResponse.ok).toBe(true);

    const ackResult = await ackResponse.json();
    expect(ackResult.message).toBe('Notification marked as acknowledged');

    // Verify status changed
    const getResponse = await getNotification(notificationId);
    const notification = await getResponse.json();
    expect(notification.status).toBe('acknowledged');

    // Clean up
    await deleteNotification(notificationId);
  });

  it('marking read twice does not error', async () => {
    const notificationId = await createTestNotification();

    // Mark as read twice
    const first = await markAsRead(notificationId);
    expect(first.ok).toBe(true);

    const second = await markAsRead(notificationId);
    expect(second.ok).toBe(true);

    // Clean up
    await deleteNotification(notificationId);
  });
});

// ============================================================================
// Unread Count
// ============================================================================

describe('Notification Unread Count', () => {
  it('can get unread count', async () => {
    const response = await apiCall('/notifications/unread/count');

    expect(response.ok).toBe(true);

    const data = await response.json();
    expect(data).toHaveProperty('unreadCount');
    expect(typeof data.unreadCount).toBe('number');
    expect(data.unreadCount).toBeGreaterThanOrEqual(0);
  });

  it('unread count increases when notification created', async () => {
    // Get initial count
    const initialResponse = await apiCall('/notifications/unread/count');
    const initialData = await initialResponse.json();
    const initialCount = initialData.unreadCount;

    // Create notification
    const notificationId = await createTestNotification();

    // Get new count
    const newResponse = await apiCall('/notifications/unread/count');
    const newData = await newResponse.json();

    expect(newData.unreadCount).toBe(initialCount + 1);

    // Clean up
    await deleteNotification(notificationId);
  });

  it('unread count decreases when notification marked as read', async () => {
    // Create notification
    const notificationId = await createTestNotification();

    // Get count with unread notification
    const beforeResponse = await apiCall('/notifications/unread/count');
    const beforeData = await beforeResponse.json();
    const beforeCount = beforeData.unreadCount;

    // Mark as read
    await markAsRead(notificationId);

    // Get count after marking read
    const afterResponse = await apiCall('/notifications/unread/count');
    const afterData = await afterResponse.json();

    expect(afterData.unreadCount).toBe(beforeCount - 1);

    // Clean up
    await deleteNotification(notificationId);
  });

  it('unread count decreases when unread notification deleted', async () => {
    // Create notification (starts as unread)
    const notificationId = await createTestNotification();

    // Get count with unread notification
    const beforeResponse = await apiCall('/notifications/unread/count');
    const beforeData = await beforeResponse.json();
    const beforeCount = beforeData.unreadCount;

    // Delete without marking as read
    await deleteNotification(notificationId);

    // Get count after deletion
    const afterResponse = await apiCall('/notifications/unread/count');
    const afterData = await afterResponse.json();

    expect(afterData.unreadCount).toBe(beforeCount - 1);
  });

  it('unread count unchanged when read notification deleted', async () => {
    // Create notification
    const notificationId = await createTestNotification();

    // Mark as read
    await markAsRead(notificationId);

    // Get count after marking read
    const beforeResponse = await apiCall('/notifications/unread/count');
    const beforeData = await beforeResponse.json();
    const beforeCount = beforeData.unreadCount;

    // Delete the read notification
    await deleteNotification(notificationId);

    // Get count after deletion
    const afterResponse = await apiCall('/notifications/unread/count');
    const afterData = await afterResponse.json();

    // Count should be unchanged since we deleted a read notification
    expect(afterData.unreadCount).toBe(beforeCount);
  });
});

// ============================================================================
// Notification Filtering
// ============================================================================

describe('Notification Filtering', () => {
  it('can filter by status', async () => {
    // Create notifications
    const id1 = await createTestNotification();
    const id2 = await createTestNotification();

    // Mark one as read
    await markAsRead(id1);

    // Filter by unread
    const unreadResponse = await apiCall('/notifications?status=unread');
    const unreadData = await unreadResponse.json();

    expect(unreadResponse.ok).toBe(true);
    // Should contain id2 but not id1
    const unreadIds = unreadData.notifications.map((n: { id: string }) => n.id);
    expect(unreadIds).toContain(id2);
    expect(unreadIds).not.toContain(id1);

    // Filter by read
    const readResponse = await apiCall('/notifications?status=read');
    const readData = await readResponse.json();

    expect(readResponse.ok).toBe(true);
    // Should contain id1 but not id2
    const readIds = readData.notifications.map((n: { id: string }) => n.id);
    expect(readIds).toContain(id1);
    expect(readIds).not.toContain(id2);

    // Clean up
    await deleteNotification(id1);
    await deleteNotification(id2);
  });

  it('can filter by type', async () => {
    // Create test notification (type: detection)
    const notificationId = await createTestNotification();

    // Filter by detection type
    const response = await apiCall('/notifications?type=detection');
    const data = await response.json();

    expect(response.ok).toBe(true);
    expect(data.notifications.some((n: { id: string }) => n.id === notificationId)).toBe(true);

    // Filter by different type
    const errorResponse = await apiCall('/notifications?type=error');
    const errorData = await errorResponse.json();

    expect(errorResponse.ok).toBe(true);
    // Our test notification shouldn't appear in error type filter
    expect(errorData.notifications.some((n: { id: string }) => n.id === notificationId)).toBe(
      false
    );

    // Clean up
    await deleteNotification(notificationId);
  });

  it('can filter by priority', async () => {
    // Create test notification (priority: high)
    const notificationId = await createTestNotification();

    // Filter by high priority
    const response = await apiCall('/notifications?priority=high');
    const data = await response.json();

    expect(response.ok).toBe(true);
    expect(data.notifications.some((n: { id: string }) => n.id === notificationId)).toBe(true);

    // Filter by different priority
    const lowResponse = await apiCall('/notifications?priority=low');
    const lowData = await lowResponse.json();

    expect(lowResponse.ok).toBe(true);
    // Our test notification shouldn't appear in low priority filter
    expect(lowData.notifications.some((n: { id: string }) => n.id === notificationId)).toBe(false);

    // Clean up
    await deleteNotification(notificationId);
  });

  it('can use limit parameter', async () => {
    // Create multiple notifications
    const id1 = await createTestNotification();
    const id2 = await createTestNotification();
    const id3 = await createTestNotification();

    // Request with limit=2
    const response = await apiCall('/notifications?limit=2');
    const data = await response.json();

    expect(response.ok).toBe(true);
    expect(data.notifications.length).toBeLessThanOrEqual(2);
    expect(data.limit).toBe(2);

    // Clean up
    await deleteNotification(id1);
    await deleteNotification(id2);
    await deleteNotification(id3);
  });

  it('can use offset parameter', async () => {
    // Create notifications
    const id1 = await createTestNotification();
    const id2 = await createTestNotification();

    // Get all notifications
    const allResponse = await apiCall('/notifications?limit=100');
    const allData = await allResponse.json();

    // Get with offset
    const offsetResponse = await apiCall('/notifications?offset=1&limit=100');
    const offsetData = await offsetResponse.json();

    expect(offsetResponse.ok).toBe(true);
    // Should have one less notification
    expect(offsetData.notifications.length).toBe(Math.max(0, allData.notifications.length - 1));

    // Clean up
    await deleteNotification(id1);
    await deleteNotification(id2);
  });
});

// ============================================================================
// Notification Deletion Edge Cases (Bug Investigation)
// ============================================================================

describe('Notification Deletion Edge Cases', () => {
  it('deleting non-existent notification returns success (known bug)', async () => {
    // BUG: The InMemoryStore.Delete method always returns nil, even for non-existent IDs.
    // Go's delete() on a map is a no-op for missing keys, and the function doesn't check
    // if the notification exists before returning success.
    // This should ideally return 404 (not found).
    const fakeId = 'non-existent-id-' + uniqueTestId();
    const response = await deleteNotification(fakeId);

    // Current behavior: returns 200 OK even for non-existent notifications
    // Expected behavior (if bug fixed): [404, 500].includes(response.status)
    expect(response.ok).toBe(true);
  });

  it('can delete notification immediately after creation', async () => {
    // Create and immediately delete
    const notificationId = await createTestNotification();
    const deleteResponse = await deleteNotification(notificationId);

    expect(deleteResponse.ok).toBe(true);

    // Verify gone
    const verifyResponse = await getNotification(notificationId);
    expect(verifyResponse.status).toBe(404);
  });

  it('can delete notification after marking as read', async () => {
    const notificationId = await createTestNotification();

    // Mark as read
    await markAsRead(notificationId);

    // Delete
    const deleteResponse = await deleteNotification(notificationId);
    expect(deleteResponse.ok).toBe(true);

    // Verify gone
    const verifyResponse = await getNotification(notificationId);
    expect(verifyResponse.status).toBe(404);
  });

  it('can delete notification after marking as acknowledged', async () => {
    const notificationId = await createTestNotification();

    // Mark as read then acknowledged
    await markAsRead(notificationId);
    await markAsAcknowledged(notificationId);

    // Delete
    const deleteResponse = await deleteNotification(notificationId);
    expect(deleteResponse.ok).toBe(true);

    // Verify gone
    const verifyResponse = await getNotification(notificationId);
    expect(verifyResponse.status).toBe(404);
  });

  it('deleting same notification twice returns success both times (known bug)', async () => {
    // BUG: Same as above - Delete doesn't check if notification exists
    const notificationId = await createTestNotification();

    // First delete should succeed
    const firstDelete = await deleteNotification(notificationId);
    expect(firstDelete.ok).toBe(true);

    // Second delete also returns success (BUG: should fail with 404)
    const secondDelete = await deleteNotification(notificationId);
    // Current behavior: returns 200 OK
    // Expected behavior (if bug fixed): [404, 500].includes(secondDelete.status)
    expect(secondDelete.ok).toBe(true);
  });

  it('bulk delete: can delete multiple notifications in sequence', async () => {
    // Create multiple notifications
    const ids = [];
    for (let i = 0; i < 3; i++) {
      ids.push(await createTestNotification());
    }

    // Delete all in sequence
    for (const id of ids) {
      const response = await deleteNotification(id);
      expect(response.ok).toBe(true);
    }

    // Verify all gone
    for (const id of ids) {
      const response = await getNotification(id);
      expect(response.status).toBe(404);
    }
  });

  it('bulk delete: can delete multiple notifications in parallel', async () => {
    // Create multiple notifications
    const ids = [];
    for (let i = 0; i < 3; i++) {
      ids.push(await createTestNotification());
    }

    // Delete all in parallel
    const deletePromises = ids.map(id => deleteNotification(id));
    const responses = await Promise.all(deletePromises);

    // All should succeed
    for (const response of responses) {
      expect(response.ok).toBe(true);
    }

    // Verify all gone
    for (const id of ids) {
      const response = await getNotification(id);
      expect(response.status).toBe(404);
    }
  });

  it('unread count stays consistent through create-delete cycle', async () => {
    // Get initial count
    const initialResponse = await apiCall('/notifications/unread/count');
    const initialCount = (await initialResponse.json()).unreadCount;

    // Create notification
    const notificationId = await createTestNotification();

    // Verify count increased
    const midResponse = await apiCall('/notifications/unread/count');
    const midCount = (await midResponse.json()).unreadCount;
    expect(midCount).toBe(initialCount + 1);

    // Delete notification
    await deleteNotification(notificationId);

    // Verify count returned to initial
    const finalResponse = await apiCall('/notifications/unread/count');
    const finalCount = (await finalResponse.json()).unreadCount;
    expect(finalCount).toBe(initialCount);
  });

  it('unread count stays consistent through create-read-delete cycle', async () => {
    // Get initial count
    const initialResponse = await apiCall('/notifications/unread/count');
    const initialCount = (await initialResponse.json()).unreadCount;

    // Create notification
    const notificationId = await createTestNotification();

    // Mark as read (should decrease count by 1)
    await markAsRead(notificationId);

    // Verify count is back to initial
    const afterReadResponse = await apiCall('/notifications/unread/count');
    const afterReadCount = (await afterReadResponse.json()).unreadCount;
    expect(afterReadCount).toBe(initialCount);

    // Delete notification (should not change count since already read)
    await deleteNotification(notificationId);

    // Verify count still at initial
    const finalResponse = await apiCall('/notifications/unread/count');
    const finalCount = (await finalResponse.json()).unreadCount;
    expect(finalCount).toBe(initialCount);
  });
});

// ============================================================================
// Notification Structure Validation
// ============================================================================

describe('Notification Structure', () => {
  it('notification has all required fields', async () => {
    const notificationId = await createTestNotification();

    const response = await getNotification(notificationId);
    const notification = await response.json();

    // Required fields from API
    expect(notification).toHaveProperty('id');
    expect(notification).toHaveProperty('type');
    expect(notification).toHaveProperty('priority');
    expect(notification).toHaveProperty('title');
    expect(notification).toHaveProperty('message');
    expect(notification).toHaveProperty('status');
    expect(notification).toHaveProperty('timestamp');
    expect(notification).toHaveProperty('component');

    // Clean up
    await deleteNotification(notificationId);
  });

  it('test notification has correct metadata', async () => {
    const notificationId = await createTestNotification();

    const response = await getNotification(notificationId);
    const notification = await response.json();

    // Test notification should have species metadata
    expect(notification).toHaveProperty('metadata');
    expect(notification.metadata).toHaveProperty('species');
    expect(notification.metadata).toHaveProperty('is_new_species');
    expect(notification.metadata.is_new_species).toBe(true);

    // Clean up
    await deleteNotification(notificationId);
  });

  it('list response has correct structure', async () => {
    const response = await apiCall('/notifications');
    const data = await response.json();

    expect(data).toHaveProperty('notifications');
    expect(data).toHaveProperty('count');
    expect(data).toHaveProperty('limit');
    expect(data).toHaveProperty('offset');

    expect(Array.isArray(data.notifications)).toBe(true);
    expect(typeof data.count).toBe('number');
    expect(typeof data.limit).toBe('number');
    expect(typeof data.offset).toBe('number');
  });
});

// ============================================================================
// Error Handling
// ============================================================================

describe('Notification Error Handling', () => {
  it('returns 404 for random UUID that does not exist', async () => {
    // Valid UUID format but doesn't exist
    const fakeId = 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee';
    const response = await getNotification(fakeId);
    expect(response.status).toBe(404);
  });

  it('mark read on non-existent notification returns error', async () => {
    const fakeId = 'non-existent-' + uniqueTestId();
    const response = await markAsRead(fakeId);

    expect([404, 500]).toContain(response.status);
  });

  it('acknowledge on non-existent notification returns error', async () => {
    const fakeId = 'non-existent-' + uniqueTestId();
    const response = await markAsAcknowledged(fakeId);

    expect([404, 500]).toContain(response.status);
  });
});
