import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Mock dependencies before importing the module under test
vi.mock('$lib/utils/urlHelpers', () => ({
  buildAppUrl: (url: string) => url,
}));

vi.mock('$lib/utils/logger', () => ({
  getLogger: () => ({
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
    debug: vi.fn(),
  }),
}));

describe('connectionState', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.resetModules();
    // Reset fetch mock
    vi.stubGlobal('fetch', vi.fn());
  });

  afterEach(async () => {
    // Deactivate watchdog to clean up timers from the current module instance
    try {
      const { deactivateWatchdog } = await import('$lib/stores/connectionState.svelte');
      deactivateWatchdog();
    } catch {
      // Module may not be loaded
    }
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it('should start with isOnline = true', async () => {
    const { connectionState } = await import('$lib/stores/connectionState.svelte');
    expect(connectionState.isOnline).toBe(true);
  });

  it('markOnline should update lastContact and set isOnline', async () => {
    const { connectionState, markOnline } = await import('$lib/stores/connectionState.svelte');
    const before = Date.now();
    markOnline();
    expect(connectionState.isOnline).toBe(true);
    expect(connectionState.lastContact).toBeGreaterThanOrEqual(before);
  });

  it('isBackendOnline should return current online state', async () => {
    const { isBackendOnline } = await import('$lib/stores/connectionState.svelte');
    expect(isBackendOnline()).toBe(true);
  });

  it('activateWatchdog should start the watchdog timer', async () => {
    const { connectionState, activateWatchdog, deactivateWatchdog } =
      await import('$lib/stores/connectionState.svelte');

    activateWatchdog();

    // Advance past watchdog timeout (35s)
    vi.advanceTimersByTime(36_000);

    expect(connectionState.isOnline).toBe(false);

    deactivateWatchdog();
  });

  it('onSSEActivity should reset the watchdog and prevent offline', async () => {
    const { connectionState, activateWatchdog, onSSEActivity, deactivateWatchdog } =
      await import('$lib/stores/connectionState.svelte');

    activateWatchdog();

    // Advance 30s (close to timeout but not expired)
    vi.advanceTimersByTime(30_000);
    onSSEActivity(); // Reset watchdog

    // Advance another 30s — would have expired without the reset
    vi.advanceTimersByTime(30_000);
    expect(connectionState.isOnline).toBe(true);

    deactivateWatchdog();
  });

  it('onSSEError should shorten the watchdog timeout', async () => {
    const { connectionState, activateWatchdog, onSSEError, deactivateWatchdog } =
      await import('$lib/stores/connectionState.svelte');

    activateWatchdog();
    onSSEError();

    // Advance just past the shortened timeout (5s)
    vi.advanceTimersByTime(6_000);

    expect(connectionState.isOnline).toBe(false);

    deactivateWatchdog();
  });

  it('should start ping polling when going offline', async () => {
    const fetchMock = vi.fn().mockRejectedValue(new Error('network error'));
    vi.stubGlobal('fetch', fetchMock);

    const { activateWatchdog, onSSEError, deactivateWatchdog } =
      await import('$lib/stores/connectionState.svelte');

    activateWatchdog();
    onSSEError();
    vi.advanceTimersByTime(6_000); // Trigger offline

    // Ping should have been called immediately
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v2/ping',
      expect.objectContaining({ method: 'GET' })
    );

    deactivateWatchdog();
  });

  it('should recover when ping succeeds', async () => {
    const fetchMock = vi
      .fn()
      .mockRejectedValueOnce(new Error('network error')) // First ping fails
      .mockResolvedValueOnce({ ok: true }); // Second ping succeeds
    vi.stubGlobal('fetch', fetchMock);

    const { connectionState, activateWatchdog, onSSEError, deactivateWatchdog } =
      await import('$lib/stores/connectionState.svelte');

    activateWatchdog();
    onSSEError();
    vi.advanceTimersByTime(6_000); // Trigger offline

    // Allow first ping (immediate) to resolve
    await vi.advanceTimersByTimeAsync(0);

    // Advance to next ping interval
    await vi.advanceTimersByTimeAsync(5_000);

    expect(connectionState.isOnline).toBe(true);

    deactivateWatchdog();
  });

  it('deactivateWatchdog should clean up all timers', async () => {
    const { activateWatchdog, deactivateWatchdog, connectionState } =
      await import('$lib/stores/connectionState.svelte');

    activateWatchdog();
    deactivateWatchdog();

    // Advance past all timeouts — state should not change
    vi.advanceTimersByTime(60_000);
    expect(connectionState.isOnline).toBe(true);
  });
});
