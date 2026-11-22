/**
 * Gain and high-pass defaults in AudioPlayer: the toolbar gain slider, the
 * overlay settings sliders and the overlay's modified dot all start from the
 * station defaults, so a player never opens looking modified.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { screen, fireEvent, cleanup } from '@testing-library/svelte';
import { renderTyped } from '../../../../test/render-helpers';
import { appState } from '$lib/stores/appState.svelte';
import AudioPlayer from './AudioPlayer.svelte';

vi.mock('$lib/utils/audioContextManager', () => ({
  getAudioContext: vi.fn().mockResolvedValue({ state: 'running' }),
  isAudioContextSupported: vi.fn().mockReturnValue(true),
  releaseAudioContext: vi.fn(),
}));

vi.mock('$lib/utils/audioNodes', () => ({
  attachAudioGraphWhenRunning: vi.fn(),
  disconnectAudioNodes: vi.fn(),
}));

const GAIN_LABEL = 'components.audioPlayer.processing.gain';
const SETTINGS_BUTTON = 'media.audio.settings';
const RESET_BUTTON = 'common.reset';
const DEFAULT_GAIN_DB = -6.5;
const DEFAULT_CUTOFF_HZ = 300;
const MOVED_CUTOFF_HZ = 1000;
// Wide enough for the overlay controls, which a zero-width jsdom player hides.
const WIDE_CONTAINER_PX = 400;

describe('AudioPlayer gain and high-pass defaults', () => {
  const originalOffsetWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth');

  beforeEach(() => {
    Object.defineProperty(HTMLElement.prototype, 'offsetWidth', {
      configurable: true,
      get: () => WIDE_CONTAINER_PX,
    });
    appState.playbackDefaults = { gainDb: DEFAULT_GAIN_DB, highPassHz: DEFAULT_CUTOFF_HZ };
  });

  afterEach(() => {
    cleanup();
    if (originalOffsetWidth) {
      Object.defineProperty(HTMLElement.prototype, 'offsetWidth', originalOffsetWidth);
    }
    appState.playbackDefaults = { gainDb: 0, highPassHz: 0 };
  });

  function mount() {
    return renderTyped(AudioPlayer, {
      props: {
        audioUrl: '/api/v2/audio/4242',
        detectionId: '4242',
        enableClipExtraction: true,
        showSpectrogram: false,
      },
    });
  }

  function overlaySliders(container: HTMLElement) {
    const [gain, filter] = Array.from(
      container.querySelectorAll<HTMLInputElement>('.settings-menu input[type="range"]')
    );
    return { gain, filter };
  }

  it('starts the toolbar gain slider on the default, negative included', () => {
    mount();

    const slider = screen.getByLabelText<HTMLInputElement>(GAIN_LABEL);
    expect(slider.value).toBe(String(DEFAULT_GAIN_DB));
  });

  it('starts the overlay sliders on the defaults without the modified dot', async () => {
    const { container } = mount();
    expect(container.querySelector('.settings-indicator')).toBeNull();

    await fireEvent.click(screen.getByRole('button', { name: SETTINGS_BUTTON }));

    expect(overlaySliders(container).gain.value).toBe(String(DEFAULT_GAIN_DB));
    expect(overlaySliders(container).filter.value).toBe(String(DEFAULT_CUTOFF_HZ));
  });

  it('Reset in the overlay returns the filter to the default cutoff, not the floor', async () => {
    const { container } = mount();
    await fireEvent.click(screen.getByRole('button', { name: SETTINGS_BUTTON }));

    await fireEvent.input(overlaySliders(container).filter, {
      target: { value: String(MOVED_CUTOFF_HZ) },
    });
    expect(container.querySelector('.settings-indicator')).not.toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: RESET_BUTTON }));

    expect(overlaySliders(container).filter.value).toBe(String(DEFAULT_CUTOFF_HZ));
    expect(container.querySelector('.settings-indicator')).toBeNull();
  });
});
