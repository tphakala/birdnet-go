import { describe, it, expect, vi, beforeEach } from 'vitest';
import { deferred } from '../../test/async-helpers';

vi.mock('$lib/utils/api', () => ({
  api: { get: vi.fn(), post: vi.fn() },
  ApiError: class ApiError extends Error {},
}));

const { api } = await import('$lib/utils/api');
const { fetchRestartStatus, restartState } = await import('./restart.svelte');

interface Status {
  binary_restart_available: boolean;
  container_restart_available: boolean;
  restart_required: boolean;
  restart_reasons: string[];
}

function status(restartRequired: boolean): Status {
  return {
    binary_restart_available: true,
    container_restart_available: false,
    restart_required: restartRequired,
    restart_reasons: restartRequired ? ['audio'] : [],
  };
}

describe('fetchRestartStatus', () => {
  beforeEach(() => {
    vi.mocked(api.get).mockReset();
    Object.assign(restartState, status(false));
  });

  it('drops an older response that arrives after a newer one', async () => {
    const older = deferred<Status>();
    const newer = deferred<Status>();
    vi.mocked(api.get)
      .mockImplementationOnce(() => older.promise)
      .mockImplementationOnce(() => newer.promise);

    const first = fetchRestartStatus();
    const second = fetchRestartStatus();

    newer.resolve(status(true));
    await second;
    expect(restartState.restart_required).toBe(true);

    older.resolve(status(false));
    await first;

    expect(restartState.restart_required).toBe(true);
    expect(restartState.restart_reasons).toEqual(['audio']);
  });

  it('applies responses that arrive in order', async () => {
    vi.mocked(api.get).mockResolvedValueOnce(status(true)).mockResolvedValueOnce(status(false));

    await fetchRestartStatus();
    expect(restartState.restart_required).toBe(true);

    await fetchRestartStatus();
    expect(restartState.restart_required).toBe(false);
  });

  it('applies an older response when the newer request failed', async () => {
    const older = deferred<Status>();
    vi.mocked(api.get)
      .mockImplementationOnce(() => older.promise)
      .mockRejectedValueOnce(new Error('network'));

    const first = fetchRestartStatus();
    await fetchRestartStatus();

    older.resolve(status(true));
    await first;

    expect(restartState.restart_required).toBe(true);
  });
});
