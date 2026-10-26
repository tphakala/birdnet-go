import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

interface Deferred<T> {
  promise: Promise<T>;
  resolve: (value: T) => void;
}

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(res => {
    resolve = res;
  });
  return { promise, resolve };
}

const SENTRY_BLOCK = { enabled: true, dsn: 'https://public@example.invalid/1', systemId: 'sys-1' };

const mocks = vi.hoisted(() => ({
  initSentry: vi.fn(),
  captureError: vi.fn(),
  shutdownSentry: vi.fn(),
  // When set, the mocked sentry chunk does not load until it resolves.
  chunkGate: null as { promise: Promise<unknown> } | null,
}));

vi.mock('$lib/telemetry/sentry', async () => {
  if (mocks.chunkGate) await mocks.chunkGate.promise;
  return {
    initSentry: mocks.initSentry,
    captureError: mocks.captureError,
    shutdownSentry: mocks.shutdownSentry,
  };
});

function configResponse(sentry: typeof SENTRY_BLOCK | undefined) {
  return {
    csrfToken: 'csrf-1',
    version: '1.2.3',
    security: {
      enabled: false,
      accessAllowed: true,
      authConfig: { basicEnabled: false, enabledProviders: [] },
    },
    sentry,
  };
}

describe('appState browser Sentry lifecycle', () => {
  // What the next /api/v2/app/config GET returns, and an optional hold on that GET.
  let sentryBlock: typeof SENTRY_BLOCK | undefined;
  let fetchGate: Deferred<null> | null;
  let fetchCalls: number;

  let appState: typeof import('$lib/stores/appState.svelte');
  let setHook: ReturnType<typeof vi.fn>;

  const lastHook = () => setHook.mock.calls.at(-1)?.[0];
  const holdChunk = () => {
    const gate = deferred<null>();
    mocks.chunkGate = gate;
    return () => {
      mocks.chunkGate = null;
      gate.resolve(null);
    };
  };

  beforeEach(async () => {
    vi.resetModules();
    mocks.initSentry.mockReset();
    mocks.captureError.mockReset();
    mocks.shutdownSentry.mockReset().mockResolvedValue(undefined);
    mocks.chunkGate = null;
    sentryBlock = undefined;
    fetchGate = null;
    fetchCalls = 0;

    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        fetchCalls++;
        const body = configResponse(sentryBlock);
        if (fetchGate) await fetchGate.promise;
        return { ok: true, json: async () => body };
      })
    );

    const logger = await import('$lib/utils/logger');
    setHook = vi.mocked(logger.setSentryCaptureError);
    appState = await import('$lib/stores/appState.svelte');
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('opt-out stops sending in the same turn', async () => {
    sentryBlock = SENTRY_BLOCK;
    await appState.initApp();
    await vi.dynamicImportSettled();
    expect(mocks.initSentry).toHaveBeenCalledTimes(1);
    expect(lastHook()).toBe(mocks.captureError);

    void appState.applySavedSentrySetting(false);
    expect(appState.isSentryEnabled()).toBe(false);
    expect(lastHook()).toBeNull();
    expect(mocks.shutdownSentry).toHaveBeenCalledTimes(1);
  });

  it('opt-out while the first chunk is loading never starts the SDK', async () => {
    sentryBlock = SENTRY_BLOCK;
    const openChunk = holdChunk();
    await appState.initApp();
    void appState.applySavedSentrySetting(false);
    openChunk();
    await vi.dynamicImportSettled();

    expect(mocks.initSentry).not.toHaveBeenCalled();
    expect(appState.isSentryEnabled()).toBe(false);
  });

  it('off then on while the first chunk is loading starts the SDK exactly once', async () => {
    sentryBlock = SENTRY_BLOCK;
    const openChunk = holdChunk();
    await appState.initApp();
    void appState.applySavedSentrySetting(false);
    await appState.applySavedSentrySetting(true);
    openChunk();
    await vi.dynamicImportSettled();

    expect(mocks.initSentry).toHaveBeenCalledTimes(1);
    expect(appState.isSentryEnabled()).toBe(true);
  });

  it('enable after a load without a DSN re-fetches the config and starts with that DSN', async () => {
    await appState.initApp();
    expect(fetchCalls).toBe(1);
    expect(appState.isSentryEnabled()).toBe(false);

    sentryBlock = SENTRY_BLOCK;
    await appState.applySavedSentrySetting(true);
    await vi.dynamicImportSettled();

    expect(fetchCalls).toBe(2);
    expect(mocks.initSentry).toHaveBeenCalledTimes(1);
    expect(mocks.initSentry).toHaveBeenCalledWith({
      dsn: SENTRY_BLOCK.dsn,
      systemId: SENTRY_BLOCK.systemId,
      version: '1.2.3',
    });
    expect(appState.isSentryEnabled()).toBe(true);
    expect(lastHook()).toBe(mocks.captureError);
  });

  it('enable does not start the SDK when the server offers no DSN', async () => {
    await appState.initApp();
    await appState.applySavedSentrySetting(true);
    await vi.dynamicImportSettled();

    expect(fetchCalls).toBe(2);
    expect(mocks.initSentry).not.toHaveBeenCalled();
    expect(appState.isSentryEnabled()).toBe(false);
  });

  it('enable twice while live fetches and inits once', async () => {
    await appState.initApp();
    sentryBlock = SENTRY_BLOCK;
    await appState.applySavedSentrySetting(true);
    await vi.dynamicImportSettled();
    await appState.applySavedSentrySetting(true);
    await vi.dynamicImportSettled();

    expect(fetchCalls).toBe(2);
    expect(mocks.initSentry).toHaveBeenCalledTimes(1);
  });

  it('off while the enable fetch is pending never starts the SDK', async () => {
    await appState.initApp();
    sentryBlock = SENTRY_BLOCK;
    fetchGate = deferred<null>();

    const enabling = appState.applySavedSentrySetting(true);
    void appState.applySavedSentrySetting(false);
    fetchGate.resolve(null);
    await enabling;
    await vi.dynamicImportSettled();

    expect(mocks.initSentry).not.toHaveBeenCalled();
    expect(appState.isSentryEnabled()).toBe(false);
  });

  it('off, on, off starts a new client and stops it again', async () => {
    sentryBlock = SENTRY_BLOCK;
    await appState.initApp();
    await vi.dynamicImportSettled();
    void appState.applySavedSentrySetting(false);

    await appState.applySavedSentrySetting(true);
    await vi.dynamicImportSettled();
    expect(appState.isSentryEnabled()).toBe(true);

    void appState.applySavedSentrySetting(false);

    expect(mocks.initSentry).toHaveBeenCalledTimes(2);
    expect(mocks.shutdownSentry).toHaveBeenCalledTimes(2);
    expect(appState.isSentryEnabled()).toBe(false);
    expect(lastHook()).toBeNull();
  });
});
