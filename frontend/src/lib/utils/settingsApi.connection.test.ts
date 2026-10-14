/**
 * Contract test for GET /api/v2/settings/connection.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Unmock settingsApi so we can exercise the real client
vi.unmock('$lib/utils/settingsApi.js');
vi.unmock('$lib/utils/logger');

vi.mock('$lib/stores/appState.svelte', () => ({
  getCsrfToken: () => 'test-csrf-token',
  isSentryEnabled: () => false,
  refreshCsrfToken: vi.fn().mockResolvedValue(false),
}));

function jsonResponse(status: number, data: unknown) {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers({ 'content-type': 'application/json' }),
    json: () => Promise.resolve(data),
  };
}

describe('settingsAPI.security.getConnection', () => {
  let mockFetch: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    mockFetch = vi.fn();
    globalThis.fetch = mockFetch as unknown as typeof fetch;
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('GETs /api/v2/settings/connection and returns the body', async () => {
    const body = {
      status: 'untrusted_proxy',
      peerAddress: '172.18.0.1',
      hasForwardedHeaders: true,
      peerIsTrustedProxy: false,
      verifiedClientAddress: '',
      peerIsCloudflareEdge: false,
      cloudflarePresetWouldVerify: false,
    };
    mockFetch.mockResolvedValueOnce(jsonResponse(200, body));
    const { settingsAPI } = await import('./settingsApi.js');

    const result = await settingsAPI.security.getConnection();

    expect(result).toEqual(body);
    expect(mockFetch).toHaveBeenCalledTimes(1);
    const [url, init] = mockFetch.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/v2/settings/connection');
    expect(init.method ?? 'GET').toBe('GET');
  });
});
