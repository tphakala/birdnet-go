import { describe, it, expect, vi } from 'vitest';
import { useRetryAction } from './useRetryAction.svelte';

describe('useRetryAction', () => {
  it('ignores a second run while the first is pending', async () => {
    let finish: () => void = () => {};
    const refetch = vi.fn(() => new Promise<void>(resolve => (finish = resolve)));
    const retry = useRetryAction(refetch);

    const first = retry.run();
    await retry.run();
    expect(retry.busy).toBe(true);
    finish();
    await first;

    expect(refetch).toHaveBeenCalledTimes(1);
    expect(retry.busy).toBe(false);
  });

  it('resolves and clears busy when the refetch rejects', async () => {
    const retry = useRetryAction(() => Promise.reject(new Error('offline')));

    await expect(retry.run()).resolves.toBeUndefined();

    expect(retry.busy).toBe(false);
  });
});
