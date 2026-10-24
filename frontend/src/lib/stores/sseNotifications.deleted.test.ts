import { describe, it, expect, vi, afterEach } from 'vitest';

type Listener = (event: Event) => void;

const { sources, log } = vi.hoisted(() => ({
  sources: [] as Array<{ listeners: Map<string, Listener> }>,
  log: { debug: vi.fn(), info: vi.fn(), warn: vi.fn(), error: vi.fn() },
}));

vi.mock('$lib/utils/ReconnectingEventSource', () => ({
  ReconnectingEventSource: class ReconnectingEventSource {
    private readonly record = { listeners: new Map<string, Listener>() };
    onopen: (() => void) | null = null;
    onmessage: ((event: MessageEvent) => void) | null = null;
    onerror: (() => void) | null = null;
    constructor() {
      sources.push(this.record);
    }
    addEventListener(type: string, listener: Listener): void {
      this.record.listeners.set(type, listener);
    }
    close(): void {}
  },
}));

vi.mock('$lib/utils/logger', async importOriginal => {
  const actual = await importOriginal<typeof import('$lib/utils/logger')>();
  return { ...actual, loggers: { ...actual.loggers, sse: log } };
});

import { resetDeletedNotificationsForTest } from '$lib/utils/notifications';
import {
  NOTIFICATION_DELETED_SSE_EVENT,
  NOTIFICATION_DELETED_WINDOW_EVENT,
  parseDeletedNotificationId,
  sseNotifications,
} from './sseNotifications';

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

describe('notification_deleted relay', () => {
  afterEach(() => {
    sseNotifications.disconnect();
    resetDeletedNotificationsForTest();
  });

  function deliver(data: string): CustomEvent[] {
    sseNotifications.connect();
    const listener = sources.at(-1)?.listeners.get(NOTIFICATION_DELETED_SSE_EVENT);
    expect(listener, 'the store listens for notification_deleted').toBeDefined();

    const received: CustomEvent[] = [];
    const onDeleted = (event: Event) => received.push(event as CustomEvent);
    window.addEventListener(NOTIFICATION_DELETED_WINDOW_EVENT, onDeleted);
    try {
      listener?.(new MessageEvent(NOTIFICATION_DELETED_SSE_EVENT, { data }));
    } finally {
      window.removeEventListener(NOTIFICATION_DELETED_WINDOW_EVENT, onDeleted);
    }
    return received;
  }

  it('re-dispatches a server delete on the window event the bell and page use', () => {
    const received = deliver(JSON.stringify({ id: 'abc-123' }));

    expect(received).toHaveLength(1);
    expect(received[0].detail).toEqual({ id: 'abc-123', wasUnread: false });
  });

  it('ignores a malformed payload with a warning, not an error', () => {
    log.warn.mockClear();
    log.error.mockClear();
    expect(deliver(JSON.stringify({ nope: true }))).toHaveLength(0);
    expect(log.warn).toHaveBeenCalledTimes(1);
    expect(log.error).not.toHaveBeenCalled();
  });

  it('drops a create event that arrives after its own delete', () => {
    deliver(JSON.stringify({ id: 'late-1' }));
    const received: unknown[] = [];
    const unsubscribe = sseNotifications.registerNotificationCallback(n => received.push(n));
    try {
      const onNotification = sources.at(-1)?.listeners.get('notification');
      const payload = {
        id: 'late-1',
        type: 'error',
        title: 't',
        message: 'm',
        timestamp: '2026-01-01T00:00:00Z',
        priority: 'high',
      };
      onNotification?.(new MessageEvent('notification', { data: JSON.stringify(payload) }));
      onNotification?.(
        new MessageEvent('notification', { data: JSON.stringify({ ...payload, id: 'fresh-1' }) })
      );
    } finally {
      unsubscribe();
    }

    expect(received).toHaveLength(1);
    expect((received[0] as { id: string }).id).toBe('fresh-1');
  });
});
