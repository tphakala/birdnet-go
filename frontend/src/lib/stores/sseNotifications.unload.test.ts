import { describe, it, expect, vi, afterEach } from 'vitest';
import { sseNotifications } from './sseNotifications';

// A cancelled unload (the user stays on the page at the leave prompt) must not close the
// notification stream for good, so it closes on pagehide, which only fires when the page goes.
describe('sseNotifications unload handling', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('does not disconnect on beforeunload', () => {
    const disconnect = vi.spyOn(sseNotifications, 'disconnect');

    window.dispatchEvent(new Event('beforeunload', { cancelable: true }));

    expect(disconnect).not.toHaveBeenCalled();
  });

  it('disconnects on pagehide', () => {
    const disconnect = vi.spyOn(sseNotifications, 'disconnect');

    window.dispatchEvent(new Event('pagehide'));

    expect(disconnect).toHaveBeenCalledTimes(1);
  });
});
