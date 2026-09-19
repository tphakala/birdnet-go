/**
 * Tests for the dashboard no-model banner.
 *
 * The banner is an allow-list render: only the two explicit no-model verdicts
 * produce output. "ok", the "" sentinel, null and unknown strings render
 * nothing, so a healthy dashboard never shows an empty frame.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, screen } from '@testing-library/svelte';
import { renderTyped } from '../../../../../test/render-helpers';

const { acousticModelsState, watchAcousticModels, unwatch } = vi.hoisted(() => {
  const unwatch = vi.fn();
  return {
    unwatch,
    acousticModelsState: vi.fn<() => string | null>(() => null),
    watchAcousticModels: vi.fn(() => unwatch),
  };
});

vi.mock('$lib/stores/acousticModels.svelte', () => ({
  acousticModelsState,
  watchAcousticModels,
}));

vi.mock('$lib/stores/navigation.svelte', () => ({
  handleAppLinkClick: vi.fn(),
}));

import AcousticModelBanner from './AcousticModelBanner.svelte';

describe('AcousticModelBanner', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    acousticModelsState.mockReturnValue(null);
  });

  afterEach(() => {
    cleanup();
  });

  it.each([
    ['null (not fetched)', null],
    ['ok', 'ok'],
    ['the empty sentinel', ''],
    ['an unknown verdict', 'degraded'],
  ])('renders nothing for %s', (_label, state) => {
    acousticModelsState.mockReturnValue(state);
    const { container } = renderTyped(AcousticModelBanner);

    expect(container.querySelector('[role="status"]')).toBeNull();
    expect(container.querySelector('[role="alert"]')).toBeNull();
    expect(container.textContent.trim()).toBe('');
  });

  it('shows a warning status with a gallery link when no model is installed', () => {
    acousticModelsState.mockReturnValue('none_installed');
    renderTyped(AcousticModelBanner, { props: { class: 'mb-6' } });

    const banner = screen.getByRole('status');
    expect(banner).toHaveTextContent('dashboard.acousticModels.noneTitle');
    expect(banner).toHaveTextContent('dashboard.acousticModels.noneMessage');
    expect(banner).toHaveClass('mb-6');

    const link = screen.getByRole('link', { name: 'dashboard.acousticModels.noneAction' });
    expect(link.getAttribute('href')).toContain('/ui/settings/analysis?tab=models');
    expect(banner.querySelector('svg')?.getAttribute('aria-hidden')).toBe('true');
  });

  it('shows an error alert with an AI Models link when a model failed to load', () => {
    acousticModelsState.mockReturnValue('load_failed');
    renderTyped(AcousticModelBanner);

    const banner = screen.getByRole('alert');
    expect(banner).toHaveTextContent('dashboard.acousticModels.loadFailedTitle');
    expect(banner).toHaveTextContent('dashboard.acousticModels.loadFailedMessage');

    const link = screen.getByRole('link', { name: 'dashboard.acousticModels.loadFailedAction' });
    expect(link.getAttribute('href')).toContain('/ui/system/inference');
    expect(screen.queryByRole('status')).toBeNull();
  });

  it('watches the store while mounted and releases the watch on unmount', () => {
    const { unmount } = renderTyped(AcousticModelBanner);
    expect(watchAcousticModels).toHaveBeenCalledTimes(1);
    expect(unwatch).not.toHaveBeenCalled();

    unmount();
    expect(unwatch).toHaveBeenCalledTimes(1);
  });
});
