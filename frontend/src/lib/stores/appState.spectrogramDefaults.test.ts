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

describe('appState spectrogram defaults from /app/config', () => {
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

  it('starts with md and raw=true until config arrives', () => {
    expect(appStateModule.appState.spectrogramDefaults).toEqual({ size: 'md', raw: true });
  });

  it('hydrates valid spectrogram defaults from /app/config', async () => {
    body = configResponse({ spectrogramDefaults: { size: 'sm', raw: false } });

    await appStateModule.initApp();

    expect(appStateModule.appState.spectrogramDefaults).toEqual({ size: 'sm', raw: false });
  });

  it('falls back safely for unknown size values or missing config', async () => {
    body = configResponse({ spectrogramDefaults: { size: 'huge', raw: false } });

    await appStateModule.initApp();

    expect(appStateModule.appState.spectrogramDefaults).toEqual({ size: 'md', raw: false });
  });
});
