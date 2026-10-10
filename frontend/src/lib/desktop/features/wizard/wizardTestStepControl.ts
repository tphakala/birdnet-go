import { vi } from 'vitest';
import type { TranslationKey } from '$lib/i18n';

/**
 * Shared controller for WizardTestStep.test.svelte. Tests set the behaviour of
 * the fixture step here; reset() restores the defaults between tests.
 */
export const stepControl = {
  /** Validity each mounted step reports at mount, consumed in mount order. Defaults to valid. */
  validQueue: [] as boolean[],
  /** Reason each mounted step reports with its validity, consumed in mount order. Defaults to none. */
  reasonQueue: [] as Array<TranslationKey | undefined>,
  /** The leave handler every fixture step registers. */
  leave: vi.fn<() => Promise<void>>(() => Promise.resolve()),
  reset(): void {
    this.validQueue = [];
    this.reasonQueue = [];
    this.leave = vi.fn<() => Promise<void>>(() => Promise.resolve());
  },
};
