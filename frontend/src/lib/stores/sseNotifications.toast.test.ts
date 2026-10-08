import { describe, it, expect, vi, afterEach } from 'vitest';

const { sources, show } = vi.hoisted(() => ({
  sources: [] as Array<{ onmessage: ((event: MessageEvent) => void) | null }>,
  show: vi.fn(),
}));

vi.mock('$lib/utils/ReconnectingEventSource', () => ({
  ReconnectingEventSource: class ReconnectingEventSource {
    onopen: (() => void) | null = null;
    onmessage: ((event: MessageEvent) => void) | null = null;
    onerror: (() => void) | null = null;
    constructor() {
      sources.push(this);
    }
    addEventListener(): void {}
    close(): void {}
  },
}));

// setup.ts mocks the toast store without `show`, which the SSE store calls.
vi.mock('$lib/stores/toast', () => ({ toastActions: { show } }));

import { sseNotifications } from './sseNotifications';

const TOAST_DEFAULT_DURATION_MS = 5000;

describe('SSE general message toasts', () => {
  afterEach(() => {
    sseNotifications.disconnect();
    show.mockClear();
  });

  function deliver(type: string, message: string): void {
    sseNotifications.connect();
    sources
      .at(-1)
      ?.onmessage?.(new MessageEvent('message', { data: JSON.stringify({ type, message }) }));
  }

  it('shows an error notification without auto-dismiss', () => {
    deliver('error', 'Disk full');

    expect(show).toHaveBeenCalledTimes(1);
    expect(show).toHaveBeenCalledWith('Disk full', 'error', {
      duration: null,
      showIcon: true,
    });
  });

  it.each(['info', 'success', 'warning'])('auto-dismisses a %s notification', type => {
    deliver(type, 'Heads up');

    expect(show).toHaveBeenCalledTimes(1);
    expect(show.mock.calls[0][2]).toMatchObject({ duration: TOAST_DEFAULT_DURATION_MS });
  });
});
