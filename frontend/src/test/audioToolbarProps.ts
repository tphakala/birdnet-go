/**
 * Shared props for rendering AudioToolbar in tests: an idle toolbar on a clip of
 * AUDIO_TOOLBAR_CLIP_SECONDS with no selection and every callback a spy.
 */
import type { ComponentProps } from 'svelte';
import { vi } from 'vitest';
import type AudioToolbar from '$lib/desktop/components/media/AudioToolbar.svelte';

/** Length of the clip the default props describe. */
export const AUDIO_TOOLBAR_CLIP_SECONDS = 29;

/** Props for an idle toolbar, with any of them replaced by `overrides`. */
export function audioToolbarProps(
  overrides: Partial<ComponentProps<typeof AudioToolbar>> = {}
): ComponentProps<typeof AudioToolbar> {
  return {
    isPlaying: false,
    isLoading: false,
    currentTime: 0,
    duration: AUDIO_TOOLBAR_CLIP_SECONDS,
    progress: 0,
    loop: false,
    onPlayPause: vi.fn(),
    onSeek: vi.fn(),
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
  };
}
