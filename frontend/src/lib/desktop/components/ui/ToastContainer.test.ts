import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import { tick } from 'svelte';
import ToastContainer from './ToastContainer.svelte';
import { toastActions } from '$lib/stores/toast';

// setup.ts mocks the toast store without the `toasts` store this component
// reads; use the real store here.
vi.unmock('$lib/stores/toast');

// The shared i18n mock in src/test/setup.ts returns the key, so each region's
// accessible name is its translation key.
const REGION_KEYS = [
  'common.aria.toastRegion.topLeft',
  'common.aria.toastRegion.topCenter',
  'common.aria.toastRegion.topRight',
  'common.aria.toastRegion.bottomLeft',
  'common.aria.toastRegion.bottomCenter',
  'common.aria.toastRegion.bottomRight',
];

describe('ToastContainer', () => {
  it('renders one translated notification region per position', () => {
    render(ToastContainer);

    for (const key of REGION_KEYS) {
      expect(screen.getByRole('region', { name: key })).toBeInTheDocument();
    }
    expect(screen.getAllByRole('region')).toHaveLength(REGION_KEYS.length);
  });
});

describe('ToastContainer auto-dismiss', () => {
  const DEFAULT_DURATION_MS = 5000;
  const MARGIN_MS = 1000;

  afterEach(() => {
    vi.useRealTimers();
    toastActions.clear();
  });

  it('keeps a toast with a null duration on screen after the default duration', async () => {
    vi.useFakeTimers();
    render(ToastContainer);

    toastActions.error('Persistent failure', { duration: null });
    toastActions.info('Brief note');
    await tick();
    expect(screen.getByText('Persistent failure')).toBeInTheDocument();
    expect(screen.getByText('Brief note')).toBeInTheDocument();

    await vi.advanceTimersByTimeAsync(DEFAULT_DURATION_MS + MARGIN_MS);

    expect(screen.getByText('Persistent failure')).toBeInTheDocument();
    expect(screen.queryByText('Brief note')).not.toBeInTheDocument();
  });
});
