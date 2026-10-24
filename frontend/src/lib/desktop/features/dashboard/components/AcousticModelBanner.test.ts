/**
 * Tests for the dashboard no-model banner.
 *
 * The banner is an allow-list render: only the two explicit no-model verdicts,
 * and loaded models that fail every analysis, produce output. "ok" with no
 * failing model, the "" sentinel, null and unknown strings render nothing, so a
 * healthy dashboard never shows an empty frame.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, screen } from '@testing-library/svelte';
import { renderTyped } from '../../../../../test/render-helpers';

const { acousticModelsState, acousticFailingModels, watchAcousticModels, unwatch } = vi.hoisted(
  () => {
    const unwatch = vi.fn();
    return {
      unwatch,
      acousticModelsState: vi.fn<() => string | null>(() => null),
      acousticFailingModels: vi.fn<() => { id: string; name: string }[]>(() => []),
      watchAcousticModels: vi.fn(() => unwatch),
    };
  }
);

vi.mock('$lib/stores/acousticModels.svelte', () => ({
  acousticModelsState,
  acousticFailingModels,
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
    acousticFailingModels.mockReturnValue([]);
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

  it('shows an error alert naming the model when a loaded model fails every analysis', () => {
    acousticModelsState.mockReturnValue('ok');
    acousticFailingModels.mockReturnValue([{ id: 'BirdNET_V2.4', name: 'BirdNET v2.4' }]);
    renderTyped(AcousticModelBanner);

    const banner = screen.getByTestId('acoustic-model-failing-banner');
    expect(banner).toHaveAttribute('role', 'alert');
    expect(banner).toHaveTextContent('dashboard.acousticModels.failingTitle');
    expect(banner).toHaveTextContent('dashboard.acousticModels.failingMessage');

    const link = screen.getByRole('link', { name: 'dashboard.acousticModels.loadFailedAction' });
    expect(link.getAttribute('href')).toContain('/ui/system/inference');
  });

  it('lets a no-model verdict take precedence over failing models', () => {
    acousticModelsState.mockReturnValue('load_failed');
    acousticFailingModels.mockReturnValue([{ id: 'm', name: 'Model' }]);
    renderTyped(AcousticModelBanner);

    expect(screen.queryByTestId('acoustic-model-failing-banner')).toBeNull();
    expect(screen.getByRole('alert')).toHaveTextContent('dashboard.acousticModels.loadFailedTitle');
  });

  it('watches the store while mounted and releases the watch on unmount', () => {
    const { unmount } = renderTyped(AcousticModelBanner);
    expect(watchAcousticModels).toHaveBeenCalledTimes(1);
    expect(unwatch).not.toHaveBeenCalled();

    unmount();
    expect(unwatch).toHaveBeenCalledTimes(1);
  });
});
