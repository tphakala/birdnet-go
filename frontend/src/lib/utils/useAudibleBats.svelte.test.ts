/**
 * Tests for the useAudibleBats composable.
 *
 * The composable owns only the request/blob lifecycle (no $effect/onMount), so it
 * can be exercised directly. fetch and the URL object-URL helpers are mocked.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { useAudibleBats } from './useAudibleBats.svelte';
import type { AudibleBatsSettings } from '$lib/desktop/features/dashboard/components/AudibleBatsButton.svelte';

const SETTINGS: AudibleBatsSettings = {
  expansion: 10,
  gainDb: 6,
  normalize: true,
  remember: true,
};

function okResponse() {
  return {
    ok: true,
    blob: () => Promise.resolve(new Blob(['wav'], { type: 'audio/wav' })),
    json: () => Promise.resolve({}),
  } as unknown as Response;
}

function errorResponse(message: string) {
  return {
    ok: false,
    blob: () => Promise.resolve(new Blob()),
    json: () => Promise.resolve({ message }),
  } as unknown as Response;
}

describe('useAudibleBats', () => {
  let revokeObjectURL: ReturnType<typeof vi.fn>;
  let urlCounter: number;

  beforeEach(() => {
    urlCounter = 0;
    revokeObjectURL = vi.fn();
    URL.createObjectURL = ((_obj: Blob | MediaSource) =>
      `blob:mock-${++urlCounter}`) as typeof URL.createObjectURL;
    URL.revokeObjectURL = revokeObjectURL as unknown as typeof URL.revokeObjectURL;
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('generates a derived URL and reports active on enable', async () => {
    const fetchMock = vi.fn().mockResolvedValue(okResponse());
    vi.stubGlobal('fetch', fetchMock);
    const onActivate = vi.fn();

    const bats = useAudibleBats({ getDetectionId: () => 123, onActivate });
    await bats.enable(SETTINGS);

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toContain('/api/v2/audio/123/audible-bats');
    expect(init.method).toBe('POST');
    expect(JSON.parse(init.body)).toEqual({ expansion: 10, normalize: true, gain_db: 6 });

    expect(bats.active).toBe(true);
    expect(bats.generating).toBe(false);
    expect(bats.error).toBeNull();
    expect(bats.url).toBe('blob:mock-1');
    expect(onActivate).toHaveBeenCalledWith('blob:mock-1');
  });

  it('surfaces the server message and stays inactive on failure', async () => {
    const fetchMock = vi.fn().mockResolvedValue(errorResponse('boom'));
    vi.stubGlobal('fetch', fetchMock);

    const bats = useAudibleBats({ getDetectionId: () => 1 });
    await bats.enable(SETTINGS);

    expect(bats.active).toBe(false);
    expect(bats.generating).toBe(false);
    expect(bats.error).toBe('boom');
    expect(bats.url).toBeNull();
  });

  it('returns to normal playback and revokes the URL on disable', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(okResponse()));
    const onDeactivate = vi.fn();

    const bats = useAudibleBats({ getDetectionId: () => 1, onDeactivate });
    await bats.enable(SETTINGS);
    expect(bats.active).toBe(true);

    bats.disable();

    expect(bats.active).toBe(false);
    expect(bats.url).toBeNull();
    expect(onDeactivate).toHaveBeenCalledTimes(1);
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock-1');
  });

  it('reset clears state without invoking the swap callbacks', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(okResponse()));
    const onDeactivate = vi.fn();

    const bats = useAudibleBats({ getDetectionId: () => 1, onDeactivate });
    await bats.enable(SETTINGS);

    bats.reset();

    expect(bats.active).toBe(false);
    expect(bats.url).toBeNull();
    expect(onDeactivate).not.toHaveBeenCalled();
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock-1');
  });

  it('revokes the previous derived URL when regenerating', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(okResponse()));

    const bats = useAudibleBats({ getDetectionId: () => 1 });
    await bats.enable(SETTINGS);
    expect(bats.url).toBe('blob:mock-1');

    await bats.enable({ ...SETTINGS, expansion: 20 });
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock-1');
    expect(bats.url).toBe('blob:mock-2');
  });
});
