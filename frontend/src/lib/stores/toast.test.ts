import { describe, it, expect, vi, beforeEach } from 'vitest';
import { get } from 'svelte/store';

// setup.ts mocks the toast store; these tests exercise the real one.
vi.unmock('$lib/stores/toast');

import { toastActions, toasts } from './toast';

const DEFAULT_DURATION_MS = 5000;
const CUSTOM_DURATION_MS = 1234;

describe('toastActions.show duration', () => {
  beforeEach(() => {
    toastActions.clear();
    toastActions.setDefaultDuration(DEFAULT_DURATION_MS);
  });

  function lastToast() {
    const list = get(toasts);
    return list[list.length - 1];
  }

  it('uses the default duration when no duration is given', () => {
    toastActions.show('hello');
    expect(lastToast().duration).toBe(DEFAULT_DURATION_MS);
  });

  it('uses the default duration when duration is explicitly undefined', () => {
    toastActions.show('hello', 'info', { duration: undefined });
    expect(lastToast().duration).toBe(DEFAULT_DURATION_MS);
  });

  it('keeps a null duration so the toast is not auto-dismissed', () => {
    toastActions.show('stay', 'error', { duration: null });
    expect(lastToast().duration).toBeNull();
  });

  it('keeps a null duration passed through the typed helpers', () => {
    toastActions.error('stay', { duration: null });
    expect(lastToast().duration).toBeNull();
  });

  it('keeps an explicit duration', () => {
    toastActions.success('brief', { duration: CUSTOM_DURATION_MS });
    expect(lastToast().duration).toBe(CUSTOM_DURATION_MS);
  });

  it('follows a changed default duration for toasts that omit one', () => {
    toastActions.setDefaultDuration(CUSTOM_DURATION_MS);
    toastActions.show('hello');
    expect(lastToast().duration).toBe(CUSTOM_DURATION_MS);
  });
});
