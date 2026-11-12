import { describe, it, expect, beforeEach, vi } from 'vitest';

vi.mock('$lib/utils/api', () => ({
  api: {
    get: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status: number;
    constructor(message: string, status: number, _response?: Response) {
      super(message);
      this.name = 'ApiError';
      this.status = status;
    }
  },
}));

import { api, ApiError } from '$lib/utils/api';
import { createProcessList } from './processList.svelte';
import type { ProcessInfo } from './types';
import { deferred } from '../../../../test/async-helpers';

const mockGet = vi.mocked(api.get);

const DEFAULT_URL = '/api/v2/system/processes';
const ALL_URL = '/api/v2/system/processes?all=true';

function proc(pid: number, name: string): ProcessInfo {
  return { pid, name, status: 'running', cpu: 1, memory: 1024, uptime: 60 };
}

describe('createProcessList', () => {
  beforeEach(() => {
    mockGet.mockReset();
  });

  it('loads the app processes, or every process when Show All is on', async () => {
    mockGet.mockResolvedValue([proc(1, 'main')]);
    const list = createProcessList();

    await list.load();
    await list.setShowAll(true);

    expect(mockGet.mock.calls.map(call => call[0])).toEqual([DEFAULT_URL, ALL_URL]);
    expect(list.showAll).toBe(true);
  });

  it('a slower earlier response does not replace a newer one', async () => {
    const slow = deferred<ProcessInfo[]>();
    const fast = deferred<ProcessInfo[]>();
    mockGet.mockReturnValueOnce(slow.promise).mockReturnValueOnce(fast.promise);
    const list = createProcessList();

    const refresh = list.load();
    const toggle = list.setShowAll(true);
    fast.resolve([proc(1, 'main'), proc(2, 'systemd')]);
    await toggle;
    slow.resolve([proc(1, 'main')]);
    await refresh;

    expect(list.showAll).toBe(true);
    expect(list.processes.map(p => p.pid)).toEqual([1, 2]);
  });

  it('a stale failure does not report an error over a newer success', async () => {
    const slow = deferred<ProcessInfo[]>();
    mockGet.mockReturnValueOnce(slow.promise).mockResolvedValueOnce([proc(1, 'main')]);
    const list = createProcessList();

    const first = list.load();
    await list.load();
    slow.reject(new ApiError('Gateway timeout', 504, new Response()));
    await first;

    expect(list.error).toBeNull();
    expect(list.processes).toHaveLength(1);
  });

  it('a failed refresh keeps the rows and reports the error, and the next success clears it', async () => {
    const list = createProcessList();
    mockGet.mockResolvedValueOnce([proc(1, 'main'), proc(2, 'systemd')]);
    await list.load();

    mockGet.mockRejectedValueOnce(new ApiError('Service unavailable', 503, new Response()));
    await list.load();
    expect(list.processes.map(p => p.pid)).toEqual([1, 2]);
    expect(list.error).toBe('Service unavailable');

    mockGet.mockResolvedValueOnce([proc(3, 'main')]);
    await list.load();
    expect(list.error).toBeNull();
    expect(list.processes.map(p => p.pid)).toEqual([3]);
  });

  it('reports a generic reason when the failure is not an API error', async () => {
    mockGet.mockRejectedValueOnce(new TypeError('boom'));
    const list = createProcessList();

    await list.load();

    expect(list.error).toBe('errors.api.unknownError');
  });

  it('loaded is false until the first response', async () => {
    const pending = deferred<ProcessInfo[]>();
    mockGet.mockReturnValueOnce(pending.promise);
    const list = createProcessList();

    const load = list.load();
    expect(list.loaded).toBe(false);
    pending.reject(new ApiError('Service unavailable', 503, new Response()));
    await load;

    expect(list.loaded).toBe(true);
    expect(list.processes).toEqual([]);
  });
});
