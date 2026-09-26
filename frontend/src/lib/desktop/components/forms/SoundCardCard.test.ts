import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, cleanup } from '@testing-library/svelte';
import { renderTyped } from '../../../../test/render-helpers';
import type { AudioSourceConfig } from '$lib/stores/settings';
import type { AcousticModelAvailability } from '$lib/types/models';

// Mocks for common form dependencies to keep the render lightweight.
vi.mock('./SelectDropdown.svelte', () => ({
  default: vi.fn(() => ({ $set: vi.fn(), $destroy: vi.fn(), $on: vi.fn() })),
}));
vi.mock('./InlineSlider.svelte', () => ({
  default: vi.fn(() => ({ $set: vi.fn(), $destroy: vi.fn(), $on: vi.fn() })),
}));
vi.mock('./QuietHoursEditor.svelte', () => ({
  default: vi.fn(() => ({ $set: vi.fn(), $destroy: vi.fn(), $on: vi.fn() })),
}));
vi.mock('$lib/desktop/features/settings/components/AudioEqualizerSettings.svelte', () => ({
  default: vi.fn(() => ({ $set: vi.fn(), $destroy: vi.fn(), $on: vi.fn() })),
}));

// The classifier verdict decides how an empty model list is described in view
// mode; default to "unknown" (nothing fetched) and override per test.
const { acousticModelAvailability } = vi.hoisted(() => ({
  acousticModelAvailability: vi.fn<() => AcousticModelAvailability>(() => ({ kind: 'unknown' })),
}));
vi.mock('$lib/stores/acousticModels.svelte', () => ({
  acousticModelAvailability,
}));

import SoundCardCard from './SoundCardCard.svelte';

const DEFAULT_PENDING_BADGE_KEY = 'settings.audio.models.defaultPendingBadge';
const DEFAULT_BADGE_KEY = 'settings.audio.models.defaultBadge';
const NONE_BADGE_KEY = 'settings.audio.models.noneBadge';

describe('SoundCardCard defensive model guards', () => {
  const audioDevices = [{ index: 0, name: 'Default Mic', id: 'sysdefault' }];
  const modelOptions = [
    { value: 'birdnet', label: 'BirdNET v2.4' },
    { value: 'perch_v2', label: 'Perch v2' },
  ];
  const availableModels = [
    { id: 'birdnet', registryId: 'BirdNET_V2.4', name: 'BirdNET v2.4', category: 'bird' },
    { id: 'perch_v2', registryId: 'Perch_V2', name: 'Perch v2', category: 'wildlife' },
  ];

  const baseSource: Omit<AudioSourceConfig, 'models'> = {
    name: 'Living room',
    device: 'sysdefault',
    gain: 0,
  };

  beforeEach(() => {
    vi.clearAllMocks();
    acousticModelAvailability.mockReturnValue({ kind: 'unknown' });
  });

  afterEach(() => {
    cleanup();
  });

  function renderWithModels(models: AudioSourceConfig['models'] | undefined | null) {
    const source = {
      ...baseSource,
      models,
    } as unknown as AudioSourceConfig;

    return renderTyped(SoundCardCard, {
      props: {
        source,
        index: 0,
        sources: [source],
        audioDevices,
        modelOptions,
        availableModels,
        disabled: false,
        onUpdate: vi.fn(() => true),
        onDelete: vi.fn(),
      },
    });
  }

  it('renders without throwing when source.models is undefined', () => {
    expect(() => renderWithModels(undefined)).not.toThrow();

    // No verdict yet: the badge hedges rather than naming a model.
    expect(screen.getByText(DEFAULT_PENDING_BADGE_KEY)).toBeInTheDocument();
  });

  it('renders without throwing when source.models is null', () => {
    expect(() =>
      // eslint-disable-next-line @typescript-eslint/no-explicit-any -- legacy JSON deserializes omitted arrays as null
      renderWithModels(null as any)
    ).not.toThrow();

    expect(screen.getByText(DEFAULT_PENDING_BADGE_KEY)).toBeInTheDocument();
  });

  it('renders without throwing when source.models is an empty array', () => {
    expect(() => renderWithModels([])).not.toThrow();

    expect(screen.getByText(DEFAULT_PENDING_BADGE_KEY)).toBeInTheDocument();
  });

  it('names the mapped default targets when the classifier is ready', () => {
    acousticModelAvailability.mockReturnValue({
      kind: 'ready',
      defaultTargets: ['BirdNET_V2.4', 'Perch_V2'],
    });
    renderWithModels([]);

    // The i18n test mock returns the key; the interpolated labels are passed as params.
    expect(screen.getByText(DEFAULT_BADGE_KEY)).toBeInTheDocument();
    expect(screen.queryByText(NONE_BADGE_KEY)).toBeNull();
  });

  it('shows a warning "no model loaded" pill with an explanatory tooltip at N=0', () => {
    acousticModelAvailability.mockReturnValue({ kind: 'none', reason: 'none_installed' });
    renderWithModels([]);

    const badge = screen.getByText(NONE_BADGE_KEY);
    expect(badge).toBeInTheDocument();
    expect(badge).toHaveAttribute('title', 'settings.audio.models.noneEnabledHelp');
    expect(badge.className).toContain('--color-warning');
  });

  it('keeps the explicit model names when the source lists models', () => {
    acousticModelAvailability.mockReturnValue({ kind: 'none', reason: 'load_failed' });
    renderWithModels(['perch_v2']);

    expect(screen.getByText('Perch v2')).toBeInTheDocument();
    expect(screen.queryByText(NONE_BADGE_KEY)).toBeNull();
  });
});
