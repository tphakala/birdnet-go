import { describe, it, expect } from 'vitest';
import {
  deduplicateNotifications,
  groupNotifications,
  createGroupingKey,
  sanitizeNotificationMessage,
  isValidNotification,
  mapApiNotification,
  type Notification,
} from './notifications';

// Helper to create a test notification
function createTestNotification(overrides: Partial<Notification> = {}): Notification {
  return {
    id: 'test-id',
    type: 'info',
    title: 'Test Title',
    message: 'Test message',
    timestamp: '2025-01-01T12:00:00Z',
    read: false,
    priority: 'medium',
    ...overrides,
  };
}

describe('mapApiNotification', () => {
  it('maps status "unread" to read: false', () => {
    const apiNotification = {
      id: 'test-1',
      type: 'info' as const,
      title: 'Test',
      message: 'Test message',
      timestamp: '2025-01-01T12:00:00Z',
      priority: 'medium' as const,
      status: 'unread' as const,
    };

    const result = mapApiNotification(apiNotification);

    expect(result.read).toBe(false);
    expect(result.status).toBe('unread');
  });

  it('maps status "read" to read: true', () => {
    const apiNotification = {
      id: 'test-2',
      type: 'info' as const,
      title: 'Test',
      message: 'Test message',
      timestamp: '2025-01-01T12:00:00Z',
      priority: 'medium' as const,
      status: 'read' as const,
    };

    const result = mapApiNotification(apiNotification);

    expect(result.read).toBe(true);
    expect(result.status).toBe('read');
  });

  it('maps status "acknowledged" to read: true', () => {
    const apiNotification = {
      id: 'test-3',
      type: 'warning' as const,
      title: 'Test',
      message: 'Test message',
      timestamp: '2025-01-01T12:00:00Z',
      priority: 'high' as const,
      status: 'acknowledged' as const,
    };

    const result = mapApiNotification(apiNotification);

    expect(result.read).toBe(true);
    expect(result.status).toBe('acknowledged');
  });

  it('preserves all other notification fields', () => {
    const apiNotification = {
      id: 'test-4',
      type: 'error' as const,
      title: 'Error Title',
      message: 'Error message',
      timestamp: '2025-01-01T12:00:00Z',
      priority: 'critical' as const,
      status: 'unread' as const,
      component: 'database',
      metadata: { note_id: 123 },
    };

    const result = mapApiNotification(apiNotification);

    expect(result.id).toBe('test-4');
    expect(result.type).toBe('error');
    expect(result.title).toBe('Error Title');
    expect(result.message).toBe('Error message');
    expect(result.timestamp).toBe('2025-01-01T12:00:00Z');
    expect(result.priority).toBe('critical');
    expect(result.component).toBe('database');
    expect(result.metadata).toEqual({ note_id: 123 });
  });

  it('preserves existing read value when already set to false', () => {
    // Test notifications that already have 'read' field (e.g., from test mocks)
    // This simulates a notification that was already processed or from a test mock
    const notificationWithRead = {
      id: 'test-5',
      type: 'info' as const,
      title: 'Test',
      message: 'Test message',
      timestamp: '2025-01-01T12:00:00Z',
      priority: 'medium' as const,
      status: 'read' as const, // status says read
      read: false, // but read field says unread - should preserve this
    };

    const result = mapApiNotification(notificationWithRead);

    expect(result.read).toBe(false); // Should preserve existing read value
  });

  it('preserves existing read value when already set to true', () => {
    const notificationWithRead = {
      id: 'test-6',
      type: 'info' as const,
      title: 'Test',
      message: 'Test message',
      timestamp: '2025-01-01T12:00:00Z',
      priority: 'medium' as const,
      status: 'unread' as const, // status says unread
      read: true, // but read field says read - should preserve this
    };

    const result = mapApiNotification(notificationWithRead);

    expect(result.read).toBe(true); // Should preserve existing read value
  });
});

describe('deduplicateNotifications', () => {
  it('removes duplicate notifications based on key', () => {
    const notifications = [
      createTestNotification({ id: '1', message: 'Same message', title: 'Same title' }),
      createTestNotification({ id: '2', message: 'Same message', title: 'Same title' }),
      createTestNotification({ id: '3', message: 'Different message', title: 'Same title' }),
    ];

    const result = deduplicateNotifications(notifications);

    expect(result.length).toBe(2);
    expect(result[0].id).toBe('1');
    expect(result[1].id).toBe('3');
  });

  it('keeps first occurrence of duplicates', () => {
    const notifications = [
      createTestNotification({ id: 'first', message: 'Test', title: 'Test', read: false }),
      createTestNotification({ id: 'second', message: 'Test', title: 'Test', read: true }),
    ];

    const result = deduplicateNotifications(notifications);

    expect(result.length).toBe(1);
    expect(result[0].id).toBe('first');
  });
});

describe('groupNotifications', () => {
  it('groups notifications by title, component, and type', () => {
    const notifications = [
      createTestNotification({
        id: '1',
        title: 'Error A',
        type: 'error',
        component: 'database',
      }),
      createTestNotification({
        id: '2',
        title: 'Error A',
        type: 'error',
        component: 'database',
        message: 'Different message',
      }),
      createTestNotification({
        id: '3',
        title: 'Error B',
        type: 'error',
        component: 'database',
      }),
    ];

    const groups = groupNotifications(notifications);

    expect(groups.length).toBe(2);
    expect(groups[0].notifications.length).toBe(2);
    expect(groups[1].notifications.length).toBe(1);
  });

  it('returns empty array for empty input', () => {
    const result = groupNotifications([]);
    expect(result).toEqual([]);
  });

  it('calculates unread count correctly', () => {
    const notifications = [
      createTestNotification({ id: '1', title: 'Test', type: 'info', read: false }),
      createTestNotification({ id: '2', title: 'Test', type: 'info', read: true }),
      createTestNotification({ id: '3', title: 'Test', type: 'info', read: false }),
    ];

    const groups = groupNotifications(notifications);

    expect(groups.length).toBe(1);
    expect(groups[0].unreadCount).toBe(2);
  });
});

describe('createGroupingKey', () => {
  it('creates consistent key from notification properties', () => {
    const notification = createTestNotification({
      title: 'Test Title',
      type: 'error',
      component: 'audio',
    });

    const key = createGroupingKey(notification);

    expect(key).toBe('Test Title|audio|error');
  });

  it('uses "unknown" for missing component', () => {
    const notification = createTestNotification({
      title: 'Test',
      type: 'info',
      component: undefined,
    });

    const key = createGroupingKey(notification);

    expect(key).toBe('Test|unknown|info');
  });
});

describe('sanitizeNotificationMessage', () => {
  it('removes http URLs from message', () => {
    const message = 'Check this out\nhttp://example.com/image.jpg\nEnd of message';
    const result = sanitizeNotificationMessage(message);
    expect(result).toBe('Check this out\nEnd of message');
  });

  it('removes https URLs from message', () => {
    const message = 'Start\nhttps://example.com/path\nEnd';
    const result = sanitizeNotificationMessage(message);
    expect(result).toBe('Start\nEnd');
  });

  it('returns empty string for empty input', () => {
    expect(sanitizeNotificationMessage('')).toBe('');
  });

  it('trims whitespace', () => {
    const message = '  Trimmed message  \n';
    const result = sanitizeNotificationMessage(message);
    expect(result).toBe('Trimmed message');
  });
});

describe('isValidNotification', () => {
  it('returns true for valid notification objects', () => {
    const notification = {
      id: '1',
      type: 'info',
      title: 'Test',
      message: 'Test message',
      timestamp: '2025-01-01T12:00:00Z',
      read: false,
      priority: 'medium',
    };

    expect(isValidNotification(notification)).toBe(true);
  });

  it('returns false for null', () => {
    expect(isValidNotification(null)).toBe(false);
  });

  it('returns false for missing required fields', () => {
    expect(isValidNotification({ id: '1' })).toBe(false);
    expect(isValidNotification({ message: 'Test' })).toBe(false);
    expect(isValidNotification({ title: 'Test', type: 'info' })).toBe(false);
  });
});
