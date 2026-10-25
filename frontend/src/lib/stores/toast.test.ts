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

describe('toastActions.show de-duplication', () => {
  const ALT_DURATION_MS = 2500;

  beforeEach(() => {
    toastActions.clear();
    toastActions.setDefaultPosition('top-right');
    toastActions.setDefaultDuration(DEFAULT_DURATION_MS);
  });

  it('replaces an identical visible toast with the new one at the newest position', () => {
    const firstId = toastActions.error('A');
    toastActions.info('B');
    const thirdId = toastActions.error('A');

    const list = get(toasts);
    expect(list.map(t => t.message)).toEqual(['B', 'A']);
    expect(list[1].id).toBe(thirdId);
    expect(list.some(t => t.id === firstId)).toBe(false);
  });

  it('removes an identical toast that is not the most recent one', () => {
    toastActions.error('A');
    toastActions.error('A', { duration: ALT_DURATION_MS });
    const thirdId = toastActions.error('A');

    const list = get(toasts);
    expect(list).toHaveLength(2);
    expect(list.map(t => t.duration)).toEqual([ALT_DURATION_MS, DEFAULT_DURATION_MS]);
    expect(list[1].id).toBe(thirdId);
  });

  it.each([
    {
      field: 'type',
      first: () => toastActions.error('A'),
      second: () => toastActions.warning('A'),
    },
    {
      field: 'message',
      first: () => toastActions.error('A'),
      second: () => toastActions.error('B'),
    },
    {
      field: 'position',
      first: () => toastActions.error('A', { position: 'top-left' }),
      second: () => toastActions.error('A', { position: 'bottom-right' }),
    },
    {
      field: 'duration',
      first: () => toastActions.error('A', { duration: null }),
      second: () => toastActions.error('A'),
    },
  ])('keeps toasts that differ in $field', ({ first, second }) => {
    first();
    second();
    expect(get(toasts)).toHaveLength(2);
  });

  it('treats an omitted position and duration as the resolved defaults', () => {
    toastActions.error('A', { position: 'top-right', duration: DEFAULT_DURATION_MS });
    const secondId = toastActions.error('A');
    const list = get(toasts);
    expect(list).toHaveLength(1);
    expect(list[0].id).toBe(secondId);
  });

  it('treats a null and a zero duration as the same no-auto-dismiss setting', () => {
    toastActions.error('A', { duration: null });
    const secondId = toastActions.error('A', { duration: 0 });
    const list = get(toasts);
    expect(list).toHaveLength(1);
    expect(list[0].id).toBe(secondId);
  });

  it('does not evict unrelated toasts when it replaces one at the toast limit', () => {
    toastActions.info('B');
    toastActions.info('C');
    toastActions.error('A');
    toastActions.error('A');
    expect(get(toasts).map(t => t.message)).toEqual(['B', 'C', 'A']);
  });
});
