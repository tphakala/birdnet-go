import { describe, it, expect, vi } from 'vitest';
import userEvent from '@testing-library/user-event';
import { fireEvent } from '@testing-library/svelte';
import { createComponentTestFactory, screen } from '../../../../test/render-helpers';
import { expectNoA11yViolations } from '$lib/utils/axe-utils';
import AudioToolbar from './AudioToolbar.svelte';

describe('AudioToolbar export menu', () => {
  const toolbarTest = createComponentTestFactory(AudioToolbar);

  function renderToolbar(
    onExport = vi.fn(),
    playback = { isPlaying: false, isLoading: false },
    onDenoiseChange = vi.fn()
  ) {
    toolbarTest.render({
      props: {
        isPlaying: playback.isPlaying,
        isLoading: playback.isLoading,
        currentTime: 0,
        duration: 10,
        progress: 0,
        loop: false,
        onPlayPause: vi.fn(),
        onSeek: vi.fn(),
        onLoopToggle: vi.fn(),
        selectionStart: 1,
        selectionEnd: 3,
        hasSelection: true,
        gainDb: 0,
        denoise: '',
        normalize: false,
        isProcessing: false,
        isPlayingSelection: false,
        isExtracting: false,
        extractionError: null,
        onPlaySelection: vi.fn(),
        onStopSelection: vi.fn(),
        onSkipToSelection: vi.fn(),
        onClearSelection: vi.fn(),
        onGainChange: vi.fn(),
        onDenoiseChange,
        onNormalizeToggle: vi.fn(),
        onExport,
      },
    });
  }

  it('labels the play button as loading, not Pause, while a playing clip reloads', () => {
    renderToolbar(vi.fn(), { isPlaying: true, isLoading: true });

    expect(screen.getByRole('button', { name: 'media.audio.loading' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'media.audio.pause' })).not.toBeInTheDocument();
  });

  it('offers exactly the original and the five backend formats, without ALAC', async () => {
    const user = userEvent.setup();
    renderToolbar();

    await user.click(
      screen.getByRole('button', { name: 'components.audioPlayer.processing.export' })
    );

    const menu = document.querySelector('.export-menu');
    expect(menu).not.toBeNull();
    const labels = Array.from(menu?.querySelectorAll('.export-option') ?? []).map(option =>
      option.textContent.trim()
    );
    expect(labels).toEqual([
      'components.audioPlayer.processing.exportOriginal',
      'WAV',
      'FLAC',
      'MP3',
      'AAC',
      'Opus',
    ]);
    expect(screen.queryByText('ALAC')).not.toBeInTheDocument();
  });

  it('reports the chosen format to the export callback', async () => {
    const user = userEvent.setup();
    const onExport = vi.fn();
    renderToolbar(onExport);

    await user.click(
      screen.getByRole('button', { name: 'components.audioPlayer.processing.export' })
    );
    await user.click(screen.getByRole('button', { name: 'Opus' }));

    expect(onExport).toHaveBeenCalledWith('opus');
  });

  it('lists the denoise presets from off to heavy, with light-medium between light and medium', async () => {
    const user = userEvent.setup();
    renderToolbar();

    await user.click(
      screen.getByRole('button', { name: 'components.audioPlayer.processing.denoise' })
    );

    const menu = document.querySelector('.denoise-menu');
    expect(menu).not.toBeNull();
    const labels = Array.from(menu?.querySelectorAll('.denoise-option') ?? []).map(option =>
      option.textContent.trim()
    );
    expect(labels).toEqual([
      'components.audioPlayer.processing.denoiseOff',
      'components.audioPlayer.processing.denoiseLight',
      'components.audioPlayer.processing.denoiseLightMedium',
      'components.audioPlayer.processing.denoiseMedium',
      'components.audioPlayer.processing.denoiseHeavy',
    ]);
  });

  it('reports the backend preset id when light-medium is chosen', async () => {
    const user = userEvent.setup();
    const onDenoiseChange = vi.fn();
    renderToolbar(vi.fn(), { isPlaying: false, isLoading: false }, onDenoiseChange);

    await user.click(
      screen.getByRole('button', { name: 'components.audioPlayer.processing.denoise' })
    );
    await user.click(
      screen.getByRole('button', { name: 'components.audioPlayer.processing.denoiseLightMedium' })
    );

    expect(onDenoiseChange).toHaveBeenCalledWith('light-medium');
  });
});

describe('AudioToolbar Accessibility', () => {
  const toolbarTest = createComponentTestFactory(AudioToolbar);

  const CLIP_DURATION_SECONDS = 29;
  const CLIP_POSITION_SECONDS = 10;
  const BAR_LEFT_PX = 100;
  const BAR_WIDTH_PX = 200;

  function renderToolbar(overrides: Record<string, unknown> = {}) {
    const onSeek = vi.fn();
    const result = toolbarTest.render({
      props: {
        isPlaying: false,
        isLoading: false,
        currentTime: CLIP_POSITION_SECONDS,
        duration: CLIP_DURATION_SECONDS,
        progress: 0,
        loop: false,
        onPlayPause: vi.fn(),
        onSeek,
        onLoopToggle: vi.fn(),
        selectionStart: null,
        selectionEnd: null,
        hasSelection: false,
        gainDb: 0,
        denoise: '',
        normalize: false,
        isProcessing: false,
        isPlayingSelection: false,
        isExtracting: false,
        extractionError: null,
        onPlaySelection: vi.fn(),
        onStopSelection: vi.fn(),
        onSkipToSelection: vi.fn(),
        onClearSelection: vi.fn(),
        onGainChange: vi.fn(),
        onDenoiseChange: vi.fn(),
        onNormalizeToggle: vi.fn(),
        onExport: vi.fn(),
        ...overrides,
      },
    });
    return { onSeek, ...result };
  }

  function seekSlider(): HTMLElement {
    // The gain range input is a slider too, so find the seek bar by its class.
    const bar = document.querySelector<HTMLElement>('.progress-bar');
    if (!bar) throw new Error('seek bar not rendered');
    return bar;
  }

  it.each([
    ['ArrowRight', 10, 11],
    ['ArrowUp', 10, 11],
    ['ArrowLeft', 10, 9],
    ['ArrowDown', 10, 9],
    ['PageUp', 10, 15],
    ['PageDown', 10, 5],
    ['Home', 10, 0],
    ['End', 10, CLIP_DURATION_SECONDS],
    ['ArrowLeft', 0.4, 0],
    ['PageUp', 27, CLIP_DURATION_SECONDS],
  ])(
    'moves the seek slider on %s from %s s to %s s and keeps the page from scrolling',
    async (key, currentTime, expected) => {
      const { onSeek } = renderToolbar({ currentTime });

      const notPrevented = await fireEvent.keyDown(seekSlider(), { key });

      expect(onSeek).toHaveBeenCalledTimes(1);
      expect(onSeek).toHaveBeenCalledWith(expected);
      expect(notPrevented).toBe(false);
    }
  );

  it('does not seek on Enter or Space', async () => {
    const { onSeek } = renderToolbar();

    await fireEvent.keyDown(seekSlider(), { key: 'Enter' });
    await fireEvent.keyDown(seekSlider(), { key: ' ' });

    expect(onSeek).not.toHaveBeenCalled();
  });

  it('leaves modified arrow keys to the browser', async () => {
    const { onSeek } = renderToolbar();

    const notPrevented = await fireEvent.keyDown(seekSlider(), { key: 'ArrowLeft', altKey: true });

    expect(onSeek).not.toHaveBeenCalled();
    expect(notPrevented).toBe(true);
  });

  it('seeks to the clicked fraction of the bar', async () => {
    const { onSeek } = renderToolbar();
    // Partial DOMRect: the toolbar reads only left and width, and jsdom has no layout.
    const rect = { left: BAR_LEFT_PX, width: BAR_WIDTH_PX } as unknown as DOMRect;
    vi.spyOn(seekSlider(), 'getBoundingClientRect').mockReturnValue(rect);

    await fireEvent.click(seekSlider(), { clientX: BAR_LEFT_PX + BAR_WIDTH_PX / 4 });

    expect(onSeek).toHaveBeenCalledWith(CLIP_DURATION_SECONDS * 0.25);
  });

  it('names the seek slider statically and exposes the position as value text', async () => {
    const { rerender } = renderToolbar({ currentTime: 6.4, duration: 29.2 });

    const slider = screen.getByRole('slider', { name: 'media.audio.seek' });
    expect(slider).toHaveAttribute('aria-valuetext', '0:06 of 0:29');
    expect(slider).toHaveAttribute('aria-valuenow', '6.4');
    expect(slider).toHaveAttribute('aria-valuemax', '29.2');

    await rerender({ currentTime: 12 });

    expect(screen.getByRole('slider', { name: 'media.audio.seek' })).toBeInTheDocument();
  });

  it('reports the loop and normalize state with aria-pressed', async () => {
    const { rerender } = renderToolbar();
    const loop = () => screen.getByRole('button', { name: 'media.audio.loop' });
    const normalize = () =>
      screen.getByRole('button', { name: 'components.audioPlayer.processing.normalize' });

    expect(loop()).toHaveAttribute('aria-pressed', 'false');
    expect(normalize()).toHaveAttribute('aria-pressed', 'false');

    await rerender({ loop: true, normalize: true });

    expect(loop()).toHaveAttribute('aria-pressed', 'true');
    expect(normalize()).toHaveAttribute('aria-pressed', 'true');
  });

  it('has no axe violations in the playback controls', async () => {
    const { container } = renderToolbar({ loop: true });
    const group = container.querySelector('.playback-controls');
    expect(group).not.toBeNull();

    await expectNoA11yViolations(group ?? container);
  });
});
