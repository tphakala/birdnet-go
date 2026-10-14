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
  // The shared test setup installs a fetch mock; keep it and put it back after each test.
  const originalFetch = globalThis.fetch;
  let mockFetch: ReturnType<typeof vi.fn<(url: string, init?: RequestInit) => Promise<unknown>>>;

  beforeEach(() => {
    mockFetch = vi.fn<(url: string, init?: RequestInit) => Promise<unknown>>();
    // The client only reads ok, status, headers and json(), so the fake response is
    // not a full Response and the mock does not match the fetch signature.
    globalThis.fetch = mockFetch as unknown as typeof fetch;
  });

  afterEach(() => {
    globalThis.fetch = originalFetch;
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
    const [url, init] = mockFetch.mock.calls[0] ?? [];
    expect(url).toBe('/api/v2/settings/connection');
    expect(init?.method ?? 'GET').toBe('GET');
  });
});
