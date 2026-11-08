import { describe, it, expect, vi } from 'vitest';
import type { ComponentProps } from 'svelte';
import userEvent from '@testing-library/user-event';
import { fireEvent } from '@testing-library/svelte';
import { createComponentTestFactory, screen } from '../../../../test/render-helpers';
import { expectNoA11yViolations } from '$lib/utils/axe-utils';
import AudioToolbar from './AudioToolbar.svelte';
import toolbarSource from './AudioToolbar.svelte?raw';
import { DENOISE_OPTIONS } from '$lib/utils/denoisePresets';
import { SEEK_PAGE_STEP_SECONDS, SEEK_STEP_SECONDS } from '$lib/utils/seek';
import { DENOISE_BUTTON_NAME } from '../../../../test/audioPlayerQueries';
import { AUDIO_TOOLBAR_CLIP_SECONDS, audioToolbarProps } from '../../../../test/audioToolbarProps';

describe('AudioToolbar export menu', () => {
  const toolbarTest = createComponentTestFactory(AudioToolbar);

  function renderToolbar(
    onExport = vi.fn(),
    playback = { isPlaying: false, isLoading: false },
    onDenoiseChange = vi.fn()
  ) {
    toolbarTest.render({
      props: audioToolbarProps({
        ...playback,
        duration: 10,
        selectionStart: 1,
        selectionEnd: 3,
        hasSelection: true,
        onDenoiseChange,
        onExport,
      }),
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

    await user.click(screen.getByRole('button', { name: DENOISE_BUTTON_NAME }));

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

    await user.click(screen.getByRole('button', { name: DENOISE_BUTTON_NAME }));
    await user.click(
      screen.getByRole('button', { name: 'components.audioPlayer.processing.denoiseLightMedium' })
    );

    expect(onDenoiseChange).toHaveBeenCalledWith('light-medium');
  });
});

describe('AudioToolbar Accessibility', () => {
  const toolbarTest = createComponentTestFactory(AudioToolbar);

  const CLIP_DURATION_SECONDS = AUDIO_TOOLBAR_CLIP_SECONDS;
  const CLIP_POSITION_SECONDS = 10;
  // How far past the end of the clip a stale position can be
  const CLIP_OVERSHOOT_SECONDS = 11;
  const BAR_LEFT_PX = 100;
  const BAR_WIDTH_PX = 200;

  function renderToolbar(overrides: Partial<ComponentProps<typeof AudioToolbar>> = {}) {
    const props = audioToolbarProps({ currentTime: CLIP_POSITION_SECONDS, ...overrides });
    const result = toolbarTest.render({ props });
    return { onSeek: props.onSeek, ...result };
  }

  function seekSlider(): HTMLElement {
    // The gain range input is a slider too, so find the seek bar by its class.
    const bar = document.querySelector<HTMLElement>('.progress-bar');
    if (!bar) throw new Error('seek bar not rendered');
    return bar;
  }

  it.each([
    ['ArrowRight', CLIP_POSITION_SECONDS, CLIP_POSITION_SECONDS + SEEK_STEP_SECONDS],
    ['ArrowUp', CLIP_POSITION_SECONDS, CLIP_POSITION_SECONDS + SEEK_STEP_SECONDS],
    ['ArrowLeft', CLIP_POSITION_SECONDS, CLIP_POSITION_SECONDS - SEEK_STEP_SECONDS],
    ['ArrowDown', CLIP_POSITION_SECONDS, CLIP_POSITION_SECONDS - SEEK_STEP_SECONDS],
    ['PageUp', CLIP_POSITION_SECONDS, CLIP_POSITION_SECONDS + SEEK_PAGE_STEP_SECONDS],
    ['PageDown', CLIP_POSITION_SECONDS, CLIP_POSITION_SECONDS - SEEK_PAGE_STEP_SECONDS],
    ['Home', CLIP_POSITION_SECONDS, 0],
    ['End', CLIP_POSITION_SECONDS, CLIP_DURATION_SECONDS],
    // Within one step of the clip edges, the target stops at the edge
    ['ArrowLeft', SEEK_STEP_SECONDS / 2, 0],
    ['PageUp', CLIP_DURATION_SECONDS - SEEK_STEP_SECONDS, CLIP_DURATION_SECONDS],
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

  it('does not seek on Enter, and on Space it does not seek or scroll the page', async () => {
    const { onSeek } = renderToolbar();

    const enterNotPrevented = await fireEvent.keyDown(seekSlider(), { key: 'Enter' });
    const spaceNotPrevented = await fireEvent.keyDown(seekSlider(), { key: ' ' });

    expect(onSeek).not.toHaveBeenCalled();
    expect(enterNotPrevented).toBe(true);
    expect(spaceNotPrevented).toBe(false);
  });

  it.each(['altKey', 'ctrlKey', 'metaKey'])(
    'leaves arrow keys held with %s to the browser',
    async modifier => {
      const { onSeek } = renderToolbar();

      const notPrevented = await fireEvent.keyDown(seekSlider(), {
        key: 'ArrowLeft',
        [modifier]: true,
      });

      expect(onSeek).not.toHaveBeenCalled();
      expect(notPrevented).toBe(true);
    }
  );

  it('steps from the end of the clip when the position is past the duration', async () => {
    const { onSeek } = renderToolbar({
      currentTime: CLIP_DURATION_SECONDS + CLIP_OVERSHOOT_SECONDS,
    });

    expect(seekSlider()).toHaveAttribute('aria-valuenow', String(CLIP_DURATION_SECONDS));
    expect(seekSlider()).toHaveAttribute('aria-valuetext', '0:29 of 0:29');
    await fireEvent.keyDown(seekSlider(), { key: 'ArrowLeft' });

    expect(onSeek).toHaveBeenCalledWith(CLIP_DURATION_SECONDS - SEEK_STEP_SECONDS);
  });

  it.each([0, Number.NaN, Number.POSITIVE_INFINITY])(
    'does not seek and reports an empty range while the duration is %s',
    async duration => {
      const { onSeek } = renderToolbar({ duration, currentTime: 0 });
      vi.spyOn(seekSlider(), 'getBoundingClientRect').mockReturnValue(
        new DOMRect(BAR_LEFT_PX, 0, BAR_WIDTH_PX, 0)
      );

      const notPrevented = await fireEvent.keyDown(seekSlider(), { key: 'ArrowRight' });
      await fireEvent.click(seekSlider(), { clientX: BAR_LEFT_PX + BAR_WIDTH_PX / 2 });

      expect(onSeek).not.toHaveBeenCalled();
      expect(notPrevented).toBe(true);
      expect(seekSlider()).toHaveAttribute('aria-valuemax', '0');
      expect(seekSlider()).toHaveAttribute('aria-disabled', 'true');
      expect(seekSlider()).not.toHaveAttribute('aria-valuetext');
    }
  );

  it.each([0, Number.NaN, Number.POSITIVE_INFINITY])(
    'keeps Space from scrolling the page while the duration is %s',
    async duration => {
      const { onSeek } = renderToolbar({ duration, currentTime: 0 });

      const notPrevented = await fireEvent.keyDown(seekSlider(), { key: ' ' });

      expect(onSeek).not.toHaveBeenCalled();
      expect(notPrevented).toBe(false);
    }
  );

  it.each([
    [
      'a known clip',
      { currentTime: CLIP_POSITION_SECONDS, duration: CLIP_DURATION_SECONDS },
      '0:10 / 0:29',
    ],
    ['an unknown duration', { currentTime: 3, duration: Number.NaN }, '0:03 / 0:00'],
    ['an infinite duration', { currentTime: 3, duration: Number.POSITIVE_INFINITY }, '0:03 / 0:00'],
    ['a clip over an hour', { currentTime: 3725, duration: 4500 }, '1:02:05 / 1:15:00'],
  ])('shows the elapsed and total time for %s', (_name, props, expected) => {
    renderToolbar(props);

    expect(document.querySelector('.playback-time')).toHaveTextContent(expected);
  });

  it('lets the seek slider take keyboard focus', () => {
    renderToolbar();

    seekSlider().focus();

    expect(seekSlider()).toHaveFocus();
  });

  it('is not marked disabled while the duration is known', () => {
    renderToolbar();

    expect(seekSlider()).not.toHaveAttribute('aria-disabled');
  });

  it('seeks to the clicked fraction of the bar', async () => {
    const { onSeek } = renderToolbar();
    vi.spyOn(seekSlider(), 'getBoundingClientRect').mockReturnValue(
      new DOMRect(BAR_LEFT_PX, 0, BAR_WIDTH_PX, 0)
    );

    await fireEvent.click(seekSlider(), { clientX: BAR_LEFT_PX + BAR_WIDTH_PX / 4 });

    expect(onSeek).toHaveBeenCalledWith(CLIP_DURATION_SECONDS * 0.25);
  });

  it.each([
    ['left of the bar', BAR_LEFT_PX - 40, 0],
    ['right of the bar', BAR_LEFT_PX + BAR_WIDTH_PX + 40, CLIP_DURATION_SECONDS],
  ])('seeks to the clip edge when the click lands %s', async (_name, clientX, expected) => {
    const { onSeek } = renderToolbar();
    vi.spyOn(seekSlider(), 'getBoundingClientRect').mockReturnValue(
      new DOMRect(BAR_LEFT_PX, 0, BAR_WIDTH_PX, 0)
    );

    await fireEvent.click(seekSlider(), { clientX });

    expect(onSeek).toHaveBeenCalledWith(expected);
  });

  it('does not seek when the bar has no width', async () => {
    const { onSeek } = renderToolbar();
    vi.spyOn(seekSlider(), 'getBoundingClientRect').mockReturnValue(
      new DOMRect(BAR_LEFT_PX, 0, 0, 0)
    );

    await fireEvent.click(seekSlider(), { clientX: BAR_LEFT_PX });

    expect(onSeek).not.toHaveBeenCalled();
  });

  it('names the seek slider statically and exposes the position as value text', async () => {
    const { rerender } = renderToolbar({ currentTime: 6.4, duration: 29.2 });

    const slider = screen.getByRole('slider', { name: 'media.audio.seek' });
    expect(slider).toHaveAttribute('aria-valuemin', '0');
    expect(slider).toHaveAttribute('aria-valuetext', '0:06 of 0:29');
    expect(slider).toHaveAttribute('aria-valuenow', '6.4');
    expect(slider).toHaveAttribute('aria-valuemax', '29.2');

    await rerender({ currentTime: 12 });

    const moved = screen.getByRole('slider', { name: 'media.audio.seek' });
    expect(moved).toHaveAttribute('aria-valuetext', '0:12 of 0:29');
    expect(moved).toHaveAttribute('aria-valuenow', '12');
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

  it('keeps the loop and normalize toggles from submitting a surrounding form', () => {
    renderToolbar();

    for (const name of ['media.audio.loop', 'components.audioPlayer.processing.normalize']) {
      expect(screen.getByRole('button', { name })).toHaveAttribute('type', 'button');
    }
  });

  // jsdom computes no cascade, so the real colours are checked in the browser-mode test
  // audio-toolbar-active-hover.browser.test.ts, which CI does not run. This pins the active
  // hover rule that test relies on, so a revert cannot pass every automated check.
  it('gives active buttons their own hover background instead of the grey one', () => {
    const css = toolbarSource.replace(/\/\*[\s\S]*?\*\//g, '');

    expect(css).toMatch(
      /\.toolbar-btn\.active:hover:not\(:disabled\)\s*\{[^}]*background:\s*var\(--color-primary-hover\)/
    );
  });

  it.each(DENOISE_OPTIONS.map(option => [option.id, option.labelKey] as const))(
    'names the Denoise button with the state it shows when denoise is %j',
    (id, labelKey) => {
      renderToolbar({ denoise: id });

      const button = screen.getByRole('button', { name: DENOISE_BUTTON_NAME });
      expect(button).toHaveAccessibleName(`components.audioPlayer.processing.denoise ${labelKey}`);
      expect(button).toHaveTextContent(labelKey);
      // The control word is visually hidden; without sr-only it would render beside the state
      expect(button.querySelector('.sr-only')).toHaveTextContent(
        'components.audioPlayer.processing.denoise'
      );
    }
  );

  it('announces an export error through role=alert alone, without an extra aria-live', async () => {
    renderToolbar({ extractionError: 'Export failed', hasSelection: true });

    const alert = screen.getByRole('alert');

    expect(alert).toHaveTextContent('Export failed');
    expect(alert).not.toHaveAttribute('aria-live');
  });

  it('has no axe violations in the playback controls', async () => {
    const { container } = renderToolbar({ loop: true });
    const group = container.querySelector('.playback-controls');
    expect(group).not.toBeNull();

    await expectNoA11yViolations(group ?? container);
  });
});
