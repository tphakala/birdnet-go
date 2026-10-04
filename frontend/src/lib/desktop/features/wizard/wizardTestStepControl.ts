import { vi } from 'vitest';

/**
 * Shared controller for WizardTestStep.test.svelte. Tests set the behaviour of
 * the fixture step here; reset() restores the defaults between tests.
 */
export const stepControl = {
  /** Validity each mounted step reports at mount, consumed in mount order. Defaults to valid. */
  validQueue: [] as boolean[],
  /** The leave handler every fixture step registers. */
  leave: vi.fn<() => Promise<void>>(() => Promise.resolve()),
  reset(): void {
    this.validQueue = [];
    this.leave = vi.fn<() => Promise<void>>(() => Promise.resolve());
  },
};
