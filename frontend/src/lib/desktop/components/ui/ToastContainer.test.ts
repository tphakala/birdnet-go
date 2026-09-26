import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import ToastContainer from './ToastContainer.svelte';

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
