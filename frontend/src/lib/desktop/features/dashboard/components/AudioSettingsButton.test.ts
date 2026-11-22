import { describe, it, expect, vi } from 'vitest';
import { fireEvent } from '@testing-library/svelte';
import type { ComponentProps } from 'svelte';
import { renderTyped } from '../../../../../test/render-helpers';
import AudioSettingsButton from './AudioSettingsButton.svelte';

const DEFAULT_FILTER_HZ = 300;

function renderButton(props: Partial<ComponentProps<typeof AudioSettingsButton>> = {}) {
  const handlers = {
    onGainChange: vi.fn(),
    onFilterChange: vi.fn(),
    onSpeedChange: vi.fn(),
  };
  const result = renderTyped(AudioSettingsButton, {
    props: {
      gainValue: 0,
      filterFreq: 20,
      playbackSpeed: 1,
      ...handlers,
      ...props,
    },
  });
  return { ...result, ...handlers };
}

describe('AudioSettingsButton defaults', () => {
  it('shows no modified dot when the filter sits at the default cutoff', () => {
    const { container } = renderButton({
      filterFreq: DEFAULT_FILTER_HZ,
      defaultFilterFreq: DEFAULT_FILTER_HZ,
    });
    expect(container.querySelector('.settings-indicator')).toBeNull();
  });

  it('shows the modified dot when the filter was moved off a non-zero default', () => {
    const { container } = renderButton({
      filterFreq: 20,
      defaultFilterFreq: DEFAULT_FILTER_HZ,
    });
    expect(container.querySelector('.settings-indicator')).not.toBeNull();
  });

  it('shows no dot at the floor without a default filter', () => {
    const { container } = renderButton();
    expect(container.querySelector('.settings-indicator')).toBeNull();
  });

  it('Reset returns gain and filter to their defaults', async () => {
    const { container, onGainChange, onFilterChange, onSpeedChange } = renderButton({
      gainValue: 3,
      defaultGainValue: -6.5,
      filterFreq: 1000,
      defaultFilterFreq: DEFAULT_FILTER_HZ,
    });

    const trigger = container.querySelector('.audio-settings-trigger');
    expect(trigger).not.toBeNull();
    if (trigger) await fireEvent.click(trigger);
    const reset = container.querySelector('.reset-button');
    expect(reset).not.toBeNull();
    if (reset) await fireEvent.click(reset);

    expect(onGainChange).toHaveBeenCalledWith(-6.5);
    expect(onFilterChange).toHaveBeenCalledWith(DEFAULT_FILTER_HZ);
    expect(onSpeedChange).toHaveBeenCalledWith(1);
  });

  it('offers the shared gain and high-pass ranges and steps', async () => {
    const { container } = renderButton();
    const trigger = container.querySelector('.audio-settings-trigger');
    if (trigger) await fireEvent.click(trigger);

    const sliders = container.querySelectorAll<HTMLInputElement>('input[type="range"]');
    const [gain, filter] = [sliders[0], sliders[1]];
    expect([gain.min, gain.max, gain.step]).toEqual(['-12', '24', '0.5']);
    expect([filter.min, filter.max, filter.step]).toEqual(['20', '5000', '10']);
  });
});
