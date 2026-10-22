import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

const FakeHls = { isSupported: () => true };
const UnsupportedHls = { isSupported: () => false };

// Each test imports a fresh hls-loader so its module-level cache starts empty.
async function freshLoader(hlsFactory: () => { default: typeof FakeHls }) {
  vi.resetModules();
  vi.doMock('hls.js', hlsFactory);
  return import('./hls-loader');
}

describe('hls-loader', () => {
  beforeEach(() => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
  });

  afterEach(() => {
    vi.doUnmock('hls.js');
    vi.restoreAllMocks();
  });

  it('loads HLS.js once and shares it between concurrent calls', async () => {
    const factory = vi.fn(() => ({ default: FakeHls }));
    const { loadHls } = await freshLoader(factory);

    const [first, second] = await Promise.all([loadHls(), loadHls()]);

    expect(first).toBe(FakeHls);
    expect(second).toBe(FakeHls);
    expect(await loadHls()).toBe(FakeHls);
    expect(factory).toHaveBeenCalledTimes(1);
  });

  it('retries the import after a failed load', async () => {
    let fail = true;
    const { loadHls } = await freshLoader(() => {
      if (fail) throw new Error('chunk failed to load');
      return { default: FakeHls };
    });

    await expect(loadHls()).rejects.toThrow();

    fail = false;
    expect(await loadHls()).toBe(FakeHls);
  });

  it('tryLoadHls resolves to null when the import fails', async () => {
    const { tryLoadHls } = await freshLoader(() => {
      throw new Error('chunk failed to load');
    });

    expect(await tryLoadHls()).toBeNull();
  });
});

describe('canPlayHls', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('is true when HLS.js loaded and is supported', async () => {
    const { canPlayHls } = await freshLoader(() => ({ default: FakeHls }));
    expect(canPlayHls(FakeHls as never)).toBe(true);
  });

  it('is false when HLS.js loaded but is unsupported and there is no native HLS', async () => {
    const { canPlayHls } = await freshLoader(() => ({ default: FakeHls }));
    vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('');

    expect(canPlayHls(UnsupportedHls as never)).toBe(false);
  });

  it('uses native HLS when HLS.js loaded but is unsupported', async () => {
    const { canPlayHls } = await freshLoader(() => ({ default: FakeHls }));
    vi.spyOn(HTMLMediaElement.prototype, 'canPlayType').mockReturnValue('maybe');

    expect(canPlayHls(UnsupportedHls as never)).toBe(true);
  });

  it('falls back to native HLS support when HLS.js is missing', async () => {
    const { canPlayHls } = await freshLoader(() => ({ default: FakeHls }));
    const canPlayType = vi.spyOn(HTMLMediaElement.prototype, 'canPlayType');

    canPlayType.mockReturnValue('maybe');
    expect(canPlayHls(null)).toBe(true);

    canPlayType.mockReturnValue('');
    expect(canPlayHls(null)).toBe(false);
  });
});
