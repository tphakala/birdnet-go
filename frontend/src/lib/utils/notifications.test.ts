import { describe, it, expect } from 'vitest';
import {
  deduplicateNotifications,
  mergeAndDeduplicateNotifications,
  removeNotificationById,
  groupNotifications,
  createGroupingKey,
  sanitizeNotificationMessage,
  isValidNotification,
  mapApiNotification,
  translateField,
  translateNotification,
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

  it('defaults to read: false when neither status nor read is present', () => {
    // Edge case: notification missing both status and read fields
    const notificationWithoutStatusOrRead = {
      id: 'test-7',
      type: 'info' as const,
      title: 'Test',
      message: 'Test message',
      timestamp: '2025-01-01T12:00:00Z',
      priority: 'medium' as const,
      // No status field, no read field
    };

    const result = mapApiNotification(notificationWithoutStatusOrRead);

    expect(result.read).toBe(false); // Should default to unread
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

describe('mergeAndDeduplicateNotifications', () => {
  it('refreshes an entry with the same id and keeps its local read state', () => {
    const existing = [createTestNotification({ id: 'a', read: true, priority: 'medium' })];
    const incoming = [
      createTestNotification({
        id: 'a',
        read: false,
        priority: 'high',
        timestamp: '2025-01-01T13:00:00Z',
      }),
    ];

    const result = mergeAndDeduplicateNotifications(existing, incoming);

    expect(result).toHaveLength(1);
    expect(result[0]).toMatchObject({
      id: 'a',
      read: true,
      priority: 'high',
      timestamp: '2025-01-01T13:00:00Z',
    });
  });

  it('points a repeat with identical text at the new id and keeps the read state', () => {
    const existing = [createTestNotification({ id: 'deleted', read: true, priority: 'high' })];
    const incoming = [
      createTestNotification({
        id: 'replacement',
        read: false,
        priority: 'medium',
        timestamp: '2025-01-01T13:00:00Z',
      }),
    ];

    const result = mergeAndDeduplicateNotifications(existing, incoming);

    expect(result).toHaveLength(1);
    expect(result[0].id).toBe('replacement');
    expect(result[0].read).toBe(true);
    expect(result[0].priority).toBe('high');
  });

  it('keeps distinct notifications and orders them newest first', () => {
    const existing = [
      createTestNotification({ id: 'old', message: 'Old', timestamp: '2025-01-01T10:00:00Z' }),
    ];
    const incoming = [
      createTestNotification({ id: 'new', message: 'New', timestamp: '2025-01-01T11:00:00Z' }),
    ];

    const result = mergeAndDeduplicateNotifications(existing, incoming);

    expect(result.map(n => n.id)).toEqual(['new', 'old']);
  });

  it('collapses repeats of the same text within the incoming batch to the first entry', () => {
    const incoming = [
      createTestNotification({ id: 'first', timestamp: '2025-01-01T12:00:00Z' }),
      createTestNotification({ id: 'second', timestamp: '2025-01-01T11:00:00Z' }),
    ];

    const result = mergeAndDeduplicateNotifications([], incoming);

    expect(result.map(n => n.id)).toEqual(['first']);
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

describe('translateField', () => {
  it('returns fallback when key is undefined', () => {
    expect(translateField(undefined, undefined, 'Fallback text')).toBe('Fallback text');
  });

  it('returns fallback when key is empty string', () => {
    expect(translateField('', undefined, 'Fallback text')).toBe('Fallback text');
  });

  it('returns fallback when translation key is not found (t() returns key)', () => {
    // The mock t() returns the key itself when no translation exists
    expect(translateField('notifications.content.unknown.key', undefined, 'English fallback')).toBe(
      'English fallback'
    );
  });

  it('returns translated text when key exists in translations', () => {
    // 'common.ui.loading' is defined in the test setup translations as 'Loading...'
    expect(translateField('common.ui.loading', undefined, 'Loading fallback')).toBe('Loading...');
  });

  it('interpolates params into translated text', () => {
    // 'components.forms.species.suggestionsAvailable' = '{count} species suggestions available'
    expect(
      translateField('components.forms.species.suggestionsAvailable', { count: 5 }, 'Fallback')
    ).toBe('5 species suggestions available');
  });

  it('returns fallback when key not found even with params', () => {
    expect(
      translateField('notifications.content.missing.key', { version: '1.0' }, 'Started v1.0')
    ).toBe('Started v1.0');
  });
});

describe('translateNotification', () => {
  it('returns original title and message when no keys are present', () => {
    const notification = createTestNotification({
      title: 'BirdNET-Go Started',
      message: 'Application started (v1.0)',
    });

    const result = translateNotification(notification);

    expect(result.title).toBe('BirdNET-Go Started');
    expect(result.message).toBe('Application started (v1.0)');
  });

  it('falls back to original text when translation keys are not found', () => {
    const notification = createTestNotification({
      title: 'BirdNET-Go Started',
      message: 'Application started (v1.0)',
      title_key: 'notifications.content.startup.title',
      message_key: 'notifications.content.startup.message',
      message_params: { version: '1.0' },
    });

    // These keys aren't in the test setup translations, so t() returns the key itself
    const result = translateNotification(notification);

    expect(result.title).toBe('BirdNET-Go Started');
    expect(result.message).toBe('Application started (v1.0)');
  });

  it('uses translated text when keys exist in translations', () => {
    const notification = createTestNotification({
      title: 'Fallback title',
      message: 'Fallback message',
      // Use keys that exist in the test setup translations
      title_key: 'common.ui.loading',
      message_key: 'common.close',
    });

    const result = translateNotification(notification);

    expect(result.title).toBe('Loading...');
    expect(result.message).toBe('Close');
  });

  it('handles mixed scenario: one key found, one not', () => {
    const notification = createTestNotification({
      title: 'Fallback title',
      message: 'English error message',
      title_key: 'common.ui.loading', // exists in translations
      message_key: 'notifications.content.error.unknown', // does not exist
    });

    const result = translateNotification(notification);

    expect(result.title).toBe('Loading...');
    expect(result.message).toBe('English error message');
  });

  it('preserves i18n fields on original notification (does not mutate)', () => {
    const notification = createTestNotification({
      title: 'Original',
      message: 'Original message',
      title_key: 'some.key',
      message_params: { foo: 'bar' },
    });

    translateNotification(notification);

    expect(notification.title_key).toBe('some.key');
    expect(notification.message_params).toEqual({ foo: 'bar' });
    expect(notification.title).toBe('Original');
  });
});

describe('removeNotificationById', () => {
  it('removes the notification and reports whether unread ones remain', () => {
    const list = [
      createTestNotification({ id: 'a', read: false }),
      createTestNotification({ id: 'b', read: true }),
    ];

    const result = removeNotificationById(list, 'a');

    expect(result.notifications.map(n => n.id)).toEqual(['b']);
    expect(result.hasUnread).toBe(false);
  });

  it('returns the same list for an id it does not hold', () => {
    const list = [createTestNotification({ id: 'a', read: false })];

    const result = removeNotificationById(list, 'missing');

    expect(result.notifications).toBe(list);
    expect(result.hasUnread).toBe(true);
  });
});
