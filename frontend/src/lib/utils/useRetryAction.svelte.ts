/**
 * Retry action for the Retry button of an error alert.
 *
 * Marks the button busy and ignores clicks while the refetch runs, so a slow or
 * still-failing server does not collect stacked requests. A refetch that rejects is
 * logged and leaves the alert as it is; the alert's own error state decides what shows.
 * ErrorAlert's `onRetry` prop uses this, so an alert with a Retry button needs no more.
 *
 * @example
 * ```svelte
 * const retry = useRetryAction(fetchItems);
 * <Button onclick={retry.run} aria-disabled={retry.busy ? 'true' : undefined}>
 * ```
 */

import { loggers } from '$lib/utils/logger';

const logger = loggers.ui;

export interface RetryAction {
  /** Whether a retry is running */
  readonly busy: boolean;
  /** Runs the refetch unless a run started here is still pending; never rejects */
  run: () => Promise<void>;
}

export function useRetryAction(refetch: () => Promise<unknown>): RetryAction {
  let busy = $state(false);
  return {
    get busy() {
      return busy;
    },
    async run(): Promise<void> {
      if (busy) return;
      busy = true;
      try {
        await refetch();
      } catch (error) {
        logger.error('Retry failed', error, { action: 'retry' });
      } finally {
        busy = false;
      }
    },
  };
}
