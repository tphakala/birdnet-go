import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, cleanup } from '@testing-library/svelte';
import { renderTyped } from '../../../../test/render-helpers';
import SoundCardCard from './SoundCardCard.svelte';
import type { AudioSourceConfig } from '$lib/stores/settings';

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

describe('SoundCardCard defensive model guards', () => {
  const audioDevices = [{ index: 0, name: 'Default Mic', id: 'sysdefault' }];
  const modelOptions = [
    { value: 'birdnet', label: 'BirdNET v2.4' },
    { value: 'perch_v2', label: 'Perch v2' },
  ];

  const baseSource: Omit<AudioSourceConfig, 'models'> = {
    name: 'Living room',
    device: 'sysdefault',
    gain: 0,
  };

  beforeEach(() => {
    vi.clearAllMocks();
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
        disabled: false,
        onUpdate: vi.fn(() => true),
        onDelete: vi.fn(),
      },
    });
  }

  it('renders without throwing when source.models is undefined', () => {
    expect(() => renderWithModels(undefined)).not.toThrow();

    // Falls back to the first model option's label.
    expect(screen.getByText('BirdNET v2.4')).toBeInTheDocument();
  });

  it('renders without throwing when source.models is null', () => {
    expect(() =>
      // eslint-disable-next-line @typescript-eslint/no-explicit-any -- legacy JSON deserializes omitted arrays as null
      renderWithModels(null as any)
    ).not.toThrow();

    expect(screen.getByText('BirdNET v2.4')).toBeInTheDocument();
  });

  it('renders without throwing when source.models is an empty array', () => {
    expect(() => renderWithModels([])).not.toThrow();

    expect(screen.getByText('BirdNET v2.4')).toBeInTheDocument();
  });
});
