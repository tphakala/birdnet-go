/**
 * Shared props for rendering AudioToolbar in tests: an idle toolbar on a clip of
 * AUDIO_TOOLBAR_CLIP_SECONDS with no selection and every callback a spy.
 */
import type { ComponentProps } from 'svelte';
import { vi } from 'vitest';
import type AudioToolbar from '$lib/desktop/components/media/AudioToolbar.svelte';

/**
 * Global rule the toolbar relies on that browser-mode tests do not load: Tailwind's
 * sr-only, which hides the Denoise control word. Without it that word renders beside
 * the label and widens the button past a narrow test page.
 */
export const AUDIO_TOOLBAR_GLOBAL_CSS =
  '.sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border-width: 0; }';

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
