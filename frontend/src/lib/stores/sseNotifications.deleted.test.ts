import { describe, it, expect } from 'vitest';
import { parseDeletedNotificationId } from './sseNotifications';

describe('parseDeletedNotificationId', () => {
  it('returns the id from a notification_deleted payload', () => {
    expect(parseDeletedNotificationId({ id: 'abc-123' })).toBe('abc-123');
  });

  it.each([
    ['null', null],
    ['a string', 'abc'],
    ['a missing id', {}],
    ['an empty id', { id: '' }],
    ['a numeric id', { id: 7 }],
  ])('rejects %s', (_label, payload) => {
    expect(parseDeletedNotificationId(payload)).toBeNull();
  });
});
