/**
 * Contract tests for the conditional headers on GET and PUT /api/v2/settings:
 * the response ETag reaches the caller and If-Match is sent only when asked for.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { serverSettings } from '../../test/settings-helpers';

// Unmock settingsApi so we can exercise the real load() and save() paths
vi.unmock('$lib/utils/settingsApi.js');
vi.unmock('$lib/utils/logger');

vi.mock('$lib/stores/appState.svelte', () => ({
  getCsrfToken: () => 'test-csrf-token',
  isSentryEnabled: () => false,
  refreshCsrfToken: vi.fn().mockResolvedValue(false),
}));

function jsonResponse(status: number, data: unknown, headers: Record<string, string> = {}) {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers({ 'content-type': 'application/json', ...headers }),
    json: () => Promise.resolve(data),
  };
}

describe('settingsAPI conditional headers', () => {
  let mockFetch: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    mockFetch = vi.fn();
    globalThis.fetch = mockFetch as unknown as typeof fetch;
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('load reports the response ETag header to onETag', async () => {
    mockFetch.mockResolvedValueOnce(jsonResponse(200, { main: {} }, { ETag: '"e-7"' }));
    const { settingsAPI } = await import('./settingsApi.js');
    const onETag = vi.fn();

    const data = await settingsAPI.load({ onETag });

    expect(data).toEqual({ main: {} });
    expect(onETag).toHaveBeenCalledExactlyOnceWith('"e-7"');
  });

  it('load reports null when the response has no ETag', async () => {
    mockFetch.mockResolvedValueOnce(jsonResponse(200, {}));
    const { settingsAPI } = await import('./settingsApi.js');
    const onETag = vi.fn();

    await settingsAPI.load({ onETag });

    expect(onETag).toHaveBeenCalledExactlyOnceWith(null);
  });

  it('save sends If-Match when ifMatch is given and none otherwise', async () => {
    mockFetch.mockResolvedValue(jsonResponse(200, {}));
    const { settingsAPI } = await import('./settingsApi.js');

    const data = serverSettings();
    await settingsAPI.save(data, { ifMatch: '"e-7"' });
    await settingsAPI.save(data);

    const first = new Headers((mockFetch.mock.calls[0] as [string, RequestInit])[1].headers);
    const second = new Headers((mockFetch.mock.calls[1] as [string, RequestInit])[1].headers);
    expect(first.get('If-Match')).toBe('"e-7"');
    expect(second.has('If-Match')).toBe(false);
  });
});
