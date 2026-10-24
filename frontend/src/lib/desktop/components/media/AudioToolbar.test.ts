import { describe, it, expect, vi } from 'vitest';
import userEvent from '@testing-library/user-event';
import { createComponentTestFactory, screen } from '../../../../test/render-helpers';
import AudioToolbar from './AudioToolbar.svelte';

describe('AudioToolbar export menu', () => {
  const toolbarTest = createComponentTestFactory(AudioToolbar);

  function renderToolbar(onExport = vi.fn(), playback = { isPlaying: false, isLoading: false }) {
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
        onDenoiseChange: vi.fn(),
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
});
