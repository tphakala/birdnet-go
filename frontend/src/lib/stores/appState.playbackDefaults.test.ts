import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

function configResponse(extra: Record<string, unknown>) {
  return {
    csrfToken: 'csrf-1',
    version: '1.2.3',
    security: {
      enabled: false,
      accessAllowed: true,
      authConfig: { basicEnabled: false, enabledProviders: [] },
    },
    ...extra,
  };
}

describe('appState playback defaults from /app/config', () => {
  let body: Record<string, unknown>;
  let appStateModule: typeof import('$lib/stores/appState.svelte');

  beforeEach(async () => {
    vi.resetModules();
    body = configResponse({});
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: true, json: async () => body }))
    );
    appStateModule = await import('$lib/stores/appState.svelte');
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('keeps the gain at zero and the filter off until the config arrives', () => {
    expect(appStateModule.appState.playbackDefaults).toEqual({ gainDb: 0, highPassHz: 0 });
  });

  it('takes the default gain and cutoff the server sent, a negative gain included', async () => {
    body = configResponse({ playbackDefaults: { gainDb: -6.5, highPassHz: 300 } });

    await appStateModule.initApp();

    expect(appStateModule.appState.playbackDefaults).toEqual({ gainDb: -6.5, highPassHz: 300 });
  });

  it('falls back to zero for a server that sends no playback defaults', async () => {
    await appStateModule.initApp();

    expect(appStateModule.appState.playbackDefaults).toEqual({ gainDb: 0, highPassHz: 0 });
  });
});
