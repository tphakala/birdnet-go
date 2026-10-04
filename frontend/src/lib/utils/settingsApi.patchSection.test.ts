/**
 * Contract tests for PATCH /api/v2/settings/:section.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { SettingsSectionPayloads } from './settingsApi';

// Unmock settingsApi so we can exercise the real patchSection() path
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

describe('settingsAPI.patchSection', () => {
  let mockFetch: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    mockFetch = vi.fn();
    globalThis.fetch = mockFetch as unknown as typeof fetch;
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('patchSection sends PATCH to /api/v2/settings/<section> with the JSON body', async () => {
    mockFetch.mockResolvedValueOnce(jsonResponse(200, { skippedFields: null }));
    const { settingsAPI } = await import('./settingsApi.js');

    const response = await settingsAPI.patchSection('privacyfilter', { enabled: true });

    expect(response).toEqual({ skippedFields: null });
    expect(mockFetch).toHaveBeenCalledTimes(1);
    const [url, init] = mockFetch.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('/api/v2/settings/privacyfilter');
    expect(init.method).toBe('PATCH');
    expect(JSON.parse(init.body as string)).toEqual({ enabled: true });
    expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('test-csrf-token');
  });

  it('patchSection rejects on a 400 response', async () => {
    mockFetch.mockResolvedValueOnce(jsonResponse(400, { error: 'invalid birdweather id' }));
    const { settingsAPI } = await import('./settingsApi.js');

    await expect(settingsAPI.patchSection('birdweather', { id: 'bad' })).rejects.toMatchObject({
      status: 400,
    });
  });

  it('section payload types leave out fields the server never lets the API change', () => {
    const birdnet = (body: SettingsSectionPayloads['birdnet']) => body;
    const audio = (body: SettingsSectionPayloads['audio']) => body;

    // @ts-expect-error rangeFilter holds server-owned fields (model, species, lastUpdated)
    birdnet({ rangeFilter: undefined });
    // @ts-expect-error ffmpegPath is validated at startup and blocked on the API
    audio({ ffmpegPath: '/usr/bin/ffmpeg' });
    // @ts-expect-error soxPath is validated at startup and blocked on the API
    audio({ soxPath: '/usr/bin/sox' });

    expect(birdnet({ threshold: 0.8 })).toEqual({ threshold: 0.8 });
  });
});
