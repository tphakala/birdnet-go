import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { ReconnectingEventSource } from './ReconnectingEventSource';

// Native EventSource readyState constants
const CONNECTING = 0;
const OPEN = 1;
const CLOSED = 2;

interface MockListener {
  type: string;
  listener: (event: Event) => void;
}

/**
 * Minimal stand-in for the browser EventSource, injected via the
 * `eventSourceClass` option. jsdom does not provide EventSource, so tests must
 * supply their own. Exposes simulate* helpers to drive the connection state
 * machine deterministically.
 */
class MockEventSource {
  static readonly CONNECTING = CONNECTING;
  static readonly OPEN = OPEN;
  static readonly CLOSED = CLOSED;
  static instances: MockEventSource[] = [];

  url: string;
  withCredentials: boolean;
  readyState: number = CONNECTING;
  onopen: ((event: Event) => void) | null = null;
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: ((event: Event) => void) | null = null;
  closed = false;
  private listeners: MockListener[] = [];

  constructor(url: string, init?: EventSourceInit) {
    this.url = url;
    this.withCredentials = init?.withCredentials ?? false;
    MockEventSource.instances.push(this);
  }

  addEventListener(type: string, listener: (event: Event) => void): void {
    this.listeners.push({ type, listener });
  }

  removeEventListener(type: string, listener: (event: Event) => void): void {
    this.listeners = this.listeners.filter(l => !(l.type === type && l.listener === listener));
  }

  close(): void {
    this.closed = true;
    this.readyState = CLOSED;
  }

  // --- test helpers ---
  simulateOpen(): void {
    this.readyState = OPEN;
    this.onopen?.(new Event('open'));
  }

  simulateMessage(data: string): void {
    this.onmessage?.(new MessageEvent('message', { data }));
  }

  simulateNamedEvent(type: string, data: string): void {
    const event = new MessageEvent(type, { data });
    for (const entry of this.listeners.filter(l => l.type === type)) {
      entry.listener(event);
    }
  }

  /** Native source dropped the connection and gave up (enters CLOSED, errors). */
  simulateFailure(): void {
    this.readyState = CLOSED;
    this.onerror?.(new Event('error'));
  }
}

const ESClass = MockEventSource as unknown as typeof EventSource;

function lastInstance(): MockEventSource {
  const inst = MockEventSource.instances.at(-1);
  if (!inst) throw new Error('no MockEventSource instance was created');
  return inst;
}

describe('ReconnectingEventSource', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    MockEventSource.instances = [];
  });

  it('opens the native EventSource with the given url and forwards withCredentials', () => {
    const es = new ReconnectingEventSource('/api/v2/stream', {
      withCredentials: true,
      eventSourceClass: ESClass,
    });

    const native = lastInstance();
    expect(native.url).toBe('/api/v2/stream');
    expect(native.withCredentials).toBe(true);
    expect(es.url).toBe('/api/v2/stream');

    es.close();
  });

  it('defaults withCredentials to false when omitted', () => {
    new ReconnectingEventSource('/x', { eventSourceClass: ESClass });
    expect(lastInstance().withCredentials).toBe(false);
  });

  it('starts in the CONNECTING state', () => {
    const es = new ReconnectingEventSource('/x', { eventSourceClass: ESClass });
    expect(es.readyState).toBe(CONNECTING);
    es.close();
  });

  it('transitions to OPEN and invokes onopen when the native source opens', () => {
    const es = new ReconnectingEventSource('/x', { eventSourceClass: ESClass });
    const onopen = vi.fn();
    es.onopen = onopen;

    lastInstance().simulateOpen();

    expect(es.readyState).toBe(OPEN);
    expect(onopen).toHaveBeenCalledOnce();
    es.close();
  });

  it('delivers default messages via onmessage', () => {
    const es = new ReconnectingEventSource('/x', { eventSourceClass: ESClass });
    const onmessage = vi.fn();
    es.onmessage = onmessage;

    lastInstance().simulateOpen();
    lastInstance().simulateMessage('hello');

    expect(onmessage).toHaveBeenCalledOnce();
    expect((onmessage.mock.calls[0][0] as MessageEvent).data).toBe('hello');
    es.close();
  });

  it('delivers named SSE events to addEventListener handlers', () => {
    const es = new ReconnectingEventSource('/x', { eventSourceClass: ESClass });
    const handler = vi.fn();
    es.addEventListener('notification', handler);

    lastInstance().simulateOpen();
    lastInstance().simulateNamedEvent('notification', '{"foo":1}');

    expect(handler).toHaveBeenCalledOnce();
    expect((handler.mock.calls[0][0] as MessageEvent).data).toBe('{"foo":1}');
    es.close();
  });

  it('stops delivering after removeEventListener', () => {
    const es = new ReconnectingEventSource('/x', { eventSourceClass: ESClass });
    const handler = vi.fn();
    es.addEventListener('metrics', handler);
    es.removeEventListener('metrics', handler);

    lastInstance().simulateNamedEvent('metrics', 'x');

    expect(handler).not.toHaveBeenCalled();
    es.close();
  });

  it('invokes onerror and returns to CONNECTING when an open connection drops', () => {
    const es = new ReconnectingEventSource('/x', {
      eventSourceClass: ESClass,
      max_retry_time: 3000,
    });
    const onerror = vi.fn();
    es.onerror = onerror;

    lastInstance().simulateOpen();
    expect(es.readyState).toBe(OPEN);

    lastInstance().simulateFailure();

    expect(onerror).toHaveBeenCalledOnce();
    expect(es.readyState).toBe(CONNECTING);
    es.close();
  });

  it('closes the native source and reports CLOSED on close()', () => {
    const es = new ReconnectingEventSource('/x', { eventSourceClass: ESClass });
    const native = lastInstance();

    es.close();

    expect(native.closed).toBe(true);
    expect(es.readyState).toBe(CLOSED);
  });

  it('throws when no EventSource implementation is available', () => {
    const original = globalThis.EventSource;
    // @ts-expect-error - force-remove the global to exercise the unavailable path
    delete globalThis.EventSource;
    try {
      expect(() => new ReconnectingEventSource('/x')).toThrow(/EventSource/);
    } finally {
      globalThis.EventSource = original;
    }
  });

  describe('reconnection', () => {
    beforeEach(() => {
      vi.useFakeTimers();
      // Force the jitter to the full delay window so timing is deterministic.
      vi.spyOn(Math, 'random').mockReturnValue(1);
    });

    afterEach(() => {
      vi.restoreAllMocks();
      vi.useRealTimers();
    });

    it('reconnects with a new native EventSource after the backoff delay', () => {
      const es = new ReconnectingEventSource('/x', { eventSourceClass: ESClass });
      lastInstance().simulateOpen();
      lastInstance().simulateFailure();

      expect(MockEventSource.instances).toHaveLength(1);
      vi.advanceTimersByTime(499);
      expect(MockEventSource.instances).toHaveLength(1);
      vi.advanceTimersByTime(1); // 500ms initial window reached
      expect(MockEventSource.instances).toHaveLength(2);

      es.close();
    });

    it('re-subscribes addEventListener handlers on the reconnected source', () => {
      const es = new ReconnectingEventSource('/x', { eventSourceClass: ESClass });
      const handler = vi.fn();
      es.addEventListener('detection', handler);

      lastInstance().simulateOpen();
      lastInstance().simulateFailure();
      vi.advanceTimersByTime(500);

      lastInstance().simulateNamedEvent('detection', 'y');
      expect(handler).toHaveBeenCalledOnce();

      es.close();
    });

    it('does not create a new source for a transient drop the native source will retry', () => {
      const es = new ReconnectingEventSource('/x', { eventSourceClass: ESClass });
      const native = lastInstance();
      native.simulateOpen();

      // Native source stays CONNECTING and reconnects internally.
      native.readyState = CONNECTING;
      native.onerror?.(new Event('error'));

      vi.advanceTimersByTime(10000);
      expect(MockEventSource.instances).toHaveLength(1);

      es.close();
    });

    it('grows the backoff delay on consecutive failures and caps at max_retry_time', () => {
      const es = new ReconnectingEventSource('/x', {
        eventSourceClass: ESClass,
        max_retry_time: 3000,
      });

      // failure 1: window 500ms
      lastInstance().simulateOpen();
      lastInstance().simulateFailure();
      vi.advanceTimersByTime(500);
      expect(MockEventSource.instances).toHaveLength(2);

      // failure 2 (no successful open): window 1000ms
      lastInstance().simulateFailure();
      vi.advanceTimersByTime(999);
      expect(MockEventSource.instances).toHaveLength(2);
      vi.advanceTimersByTime(1);
      expect(MockEventSource.instances).toHaveLength(3);

      // failure 3: window 2000ms
      lastInstance().simulateFailure();
      vi.advanceTimersByTime(2000);
      expect(MockEventSource.instances).toHaveLength(4);

      // failure 4: window would be 4000ms but is capped at 3000ms
      lastInstance().simulateFailure();
      vi.advanceTimersByTime(2999);
      expect(MockEventSource.instances).toHaveLength(4);
      vi.advanceTimersByTime(1);
      expect(MockEventSource.instances).toHaveLength(5);

      es.close();
    });

    it('resets the backoff delay after a successful reconnection', () => {
      const es = new ReconnectingEventSource('/x', { eventSourceClass: ESClass });

      lastInstance().simulateOpen();
      lastInstance().simulateFailure();
      vi.advanceTimersByTime(500); // reconnect #2
      lastInstance().simulateOpen(); // success resets the backoff window
      lastInstance().simulateFailure();

      // Window is back to the initial 500ms, not grown.
      vi.advanceTimersByTime(499);
      expect(MockEventSource.instances).toHaveLength(2);
      vi.advanceTimersByTime(1);
      expect(MockEventSource.instances).toHaveLength(3);

      es.close();
    });

    it('reconnects without firing onerror when the first connection fails before opening', () => {
      const es = new ReconnectingEventSource('/x', { eventSourceClass: ESClass });
      const onerror = vi.fn();
      es.onerror = onerror;

      // Never opened (e.g. 401 or connection refused): the native source fails
      // straight from CONNECTING with a CLOSED readyState.
      lastInstance().simulateFailure();

      // onerror is suppressed because there was no OPEN -> CONNECTING transition,
      // but a reconnect is still scheduled.
      expect(onerror).not.toHaveBeenCalled();
      expect(es.readyState).toBe(CONNECTING);
      vi.advanceTimersByTime(500);
      expect(MockEventSource.instances).toHaveLength(2);

      es.close();
    });

    it('does not reconnect after close()', () => {
      const es = new ReconnectingEventSource('/x', { eventSourceClass: ESClass });
      lastInstance().simulateOpen();
      lastInstance().simulateFailure();

      es.close();
      vi.advanceTimersByTime(10000);

      expect(MockEventSource.instances).toHaveLength(1);
    });
  });

  describe('onreconnectfailed escalation hook', () => {
    beforeEach(() => {
      vi.useFakeTimers();
      vi.spyOn(Math, 'random').mockReturnValue(1);
    });

    afterEach(() => {
      vi.restoreAllMocks();
      vi.useRealTimers();
    });

    it('fires with an incrementing count on each never-opened (re)connect failure', () => {
      const es = new ReconnectingEventSource('/x', { eventSourceClass: ESClass });
      const onreconnectfailed = vi.fn();
      es.onreconnectfailed = onreconnectfailed;

      // 404-style failure: fails straight from CONNECTING, never opens.
      lastInstance().simulateFailure();
      expect(onreconnectfailed).toHaveBeenNthCalledWith(1, 1);

      vi.advanceTimersByTime(500); // reconnect #2 created
      lastInstance().simulateFailure();
      expect(onreconnectfailed).toHaveBeenNthCalledWith(2, 2);

      vi.advanceTimersByTime(1000); // reconnect #3 created
      lastInstance().simulateFailure();
      expect(onreconnectfailed).toHaveBeenNthCalledWith(3, 3);

      es.close();
    });

    it('resets the failure count after a successful open', () => {
      const es = new ReconnectingEventSource('/x', { eventSourceClass: ESClass });
      const onreconnectfailed = vi.fn();
      es.onreconnectfailed = onreconnectfailed;

      lastInstance().simulateFailure();
      expect(onreconnectfailed).toHaveBeenLastCalledWith(1);
      vi.advanceTimersByTime(500);
      lastInstance().simulateFailure();
      expect(onreconnectfailed).toHaveBeenLastCalledWith(2);

      // Reconnect succeeds, resetting the counter.
      vi.advanceTimersByTime(1000);
      lastInstance().simulateOpen();

      // A subsequent failure counts from 1 again.
      lastInstance().simulateFailure();
      expect(onreconnectfailed).toHaveBeenLastCalledWith(1);

      es.close();
    });

    it('does nothing when no callback is set (no throw, reconnect unaffected)', () => {
      const es = new ReconnectingEventSource('/x', { eventSourceClass: ESClass });
      lastInstance().simulateFailure();
      vi.advanceTimersByTime(500);
      expect(MockEventSource.instances).toHaveLength(2);
      es.close();
    });

    it('stops reconnecting when the callback closes the source', () => {
      const es = new ReconnectingEventSource('/x', { eventSourceClass: ESClass });
      es.onreconnectfailed = () => es.close();

      lastInstance().simulateFailure();
      vi.advanceTimersByTime(10000);

      // The scheduled reconnect timer was cleared by close() inside the callback.
      expect(MockEventSource.instances).toHaveLength(1);
    });
  });
});
