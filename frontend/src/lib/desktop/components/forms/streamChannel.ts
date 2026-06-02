import type { ChannelMode } from '$lib/stores/settings';

/**
 * Normalize a channel mode value coming from the API or config into a concrete
 * {@link ChannelMode}.
 *
 * The backend tags `ChannelMode` with `omitempty` for YAML but not for JSON, so
 * an unset mode arrives over the API as the empty string `""` rather than
 * `undefined`. A plain `?? 'downmix'` fallback does not catch `""`, which is why
 * the edit form previously rendered a blank "Select..." placeholder instead of
 * the documented default. Treat any empty/unknown value as downmix.
 */
export function normalizeChannelMode(mode: string | null | undefined): ChannelMode {
  return mode === 'left' || mode === 'right' ? mode : 'downmix';
}

/**
 * Convert a sample rate in Hz to kHz, dropping insignificant trailing zeros
 * (48000 -> 48, 44100 -> 44.1) for compact display in the stream format line.
 */
export function sampleRateKhz(hz: number): number {
  return Number((hz / 1000).toFixed(1));
}

/**
 * Describes how the channel controls should render for a given stream, based on
 * the detected channel count and the currently selected mode.
 */
export interface ChannelUIState {
  /** Show the downmix/left/right selector. */
  showSelector: boolean;
  /** Show the energy analysis (detect-best-channel button, bars, stereo warnings). */
  showAnalysis: boolean;
  /** Source is confirmed single-channel: no channel selection is meaningful. */
  isMono: boolean;
}

/**
 * Decide which channel controls to surface for a stream.
 *
 * - `channels === 0`: unknown (not yet probed). Hide the selector and prompt to
 *   test, unless an explicit left/right mode is already configured.
 * - `channels === 1`: mono. All modes produce identical audio, so hide the
 *   selector and show a "single channel" note, unless left/right is configured.
 * - `channels > 1`: multi-channel. Show the selector plus the energy analysis.
 *
 * An explicit `left`/`right` selection always keeps the selector visible so a
 * setting made while the source was multi-channel remains reviewable and
 * editable even when the stream is currently offline or probes as mono.
 */
export function channelUIState(channels: number, mode: ChannelMode): ChannelUIState {
  const explicit = mode === 'left' || mode === 'right';
  const multiChannel = channels > 1;
  return {
    showSelector: multiChannel || explicit,
    showAnalysis: multiChannel,
    isMono: channels === 1 && !explicit,
  };
}
