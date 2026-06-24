/**
 * Shared constants for the SoundCard sample-rate probe tests, so device IDs,
 * sample-rate option fixtures, probe timings, and the testid/i18n-key selectors
 * are declared once instead of repeated as magic literals across the suites.
 */
// Must match the prefix MockSelectDropdown.svelte builds its data-testid from.
const SELECT_TESTID_PREFIX = 'select-';

// i18n keys: the test i18n mock returns the key verbatim for unmapped keys, so
// these double as the rendered selector text.
const DEVICE_LABEL_KEY = 'settings.audio.soundCards.deviceLabel';
const SAMPLE_RATE_LABEL_KEY = 'settings.audio.soundCards.sampleRateLabel';
export const ADD_SOURCE_KEY = 'settings.audio.soundCards.addSource';
export const EDIT_BUTTON_NAME = 'common.edit';

// MockSelectDropdown derives its testid from `SELECT_TESTID_PREFIX + label`.
export const DEVICE_SELECT_TESTID = `${SELECT_TESTID_PREFIX}${DEVICE_LABEL_KEY}`;
export const SAMPLE_RATE_SELECT_TESTID = `${SELECT_TESTID_PREFIX}${SAMPLE_RATE_LABEL_KEY}`;

export type RateOption = { value: string; label: string };

export const RATE_48K: RateOption = { value: '48000', label: '48 kHz' };
export const RATE_96K: RateOption = { value: '96000', label: '96 kHz' };
export const RATE_192K: RateOption = { value: '192000', label: '192 kHz' };
export const RATE_384K: RateOption = { value: '384000', label: '384 kHz' };

export const DEFAULT_MODEL = 'birdnet';

export const ABORT_ERROR = () => new DOMException('Aborted', 'AbortError');

/**
 * A probe that resolves on the next macrotask with the given options and honours
 * the AbortSignal, mirroring the real fetchDeviceCapabilities contract (only
 * AbortError is surfaced to the caller).
 */
export function makeAbortableProbe(signal: AbortSignal | undefined, options: RateOption[]) {
  return new Promise<{ options: RateOption[]; verified: boolean }>((resolve, reject) => {
    if (signal?.aborted) {
      reject(ABORT_ERROR());
      return;
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort);
      resolve({ options, verified: true });
    }, 0);
    function onAbort() {
      clearTimeout(timer);
      signal?.removeEventListener('abort', onAbort);
      reject(ABORT_ERROR());
    }
    signal?.addEventListener('abort', onAbort);
  });
}

/** Resolve after `ms`, used to let a deliberately-slow stale probe settle. */
export const settle = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));
