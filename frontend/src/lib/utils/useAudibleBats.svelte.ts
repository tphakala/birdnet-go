/**
 * useAudibleBats.svelte.ts
 *
 * Composable that owns the "audible bats" derived-audio lifecycle for a single
 * detection: it fetches the detection's WAV or FLAC clip (optionally denoised
 * via the shared playback denoise endpoint), time-expands it client-side into a
 * playable WAV object URL, and tracks active/generating/error state.
 *
 * It deliberately does NOT touch any <audio> element. The consuming player owns
 * playback and reacts to the generated `url` (or the optional onActivate /
 * onDeactivate callbacks) to swap its source. This keeps the lifecycle shared
 * between the full AudioPlayer and the compact PlayOverlay (grid cards) without
 * duplicating fetch/blob/abort handling.
 */
import { audioUrlFor } from '$lib/utils/playbackDenoise';
import { timeExpandAudio } from '$lib/utils/timeExpandAudio';
import { t } from '$lib/i18n';
import { loggers } from '$lib/utils/logger';
import type { AudibleBatsSettings } from '$lib/desktop/features/dashboard/components/AudibleBatsButton.svelte';

const logger = loggers.audio;

interface UseAudibleBatsOptions {
  /** Returns the current detection ID (read lazily so it tracks card recycling). */
  getDetectionId: () => string | number;
  /**
   * Returns the denoise preset ('' off) for the fetched source audio, read when
   * activation runs. When non-empty, the server serves a denoised WAV at the
   * capture sample rate.
   */
  getDenoise?: () => string;
  /** Called once the derived audio URL is ready, so the player can swap to it. */
  onActivate?: (_url: string) => void;
  /** Called before the derived URL is revoked, so the player can swap away first. */
  onDeactivate?: () => void;
}

export function useAudibleBats(options: UseAudibleBatsOptions) {
  let active = $state(false);
  let generating = $state(false);
  let error = $state<string | null>(null);
  let url = $state<string | null>(null);
  let abortController: AbortController | null = null;

  function revokeUrl() {
    if (url) {
      URL.revokeObjectURL(url);
      url = null;
    }
  }

  // Fetch the clip and time-expand it client-side into a playable WAV Blob URL.
  async function enable(settings: AudibleBatsSettings) {
    // Abort any in-flight generation so a stale response can't win.
    if (abortController) {
      abortController.abort();
    }
    error = null;
    generating = true;
    const controller = new AbortController();
    abortController = controller;

    try {
      const detectionId = options.getDetectionId();
      const denoise = options.getDenoise?.() ?? '';
      const response = await fetch(audioUrlFor(detectionId, denoise), {
        signal: controller.signal,
      });

      if (!response.ok) {
        let errorMsg = t('media.audio.audibleBats.error');
        try {
          const errorData: { message?: string } = await response.json();
          errorMsg = errorData.message ?? errorMsg;
        } catch {
          // Use default error message
        }
        throw new Error(errorMsg);
      }

      const buffer = await response.arrayBuffer();
      if (abortController !== controller || controller.signal.aborted) return;

      const blob = await timeExpandAudio(buffer, settings.expansion, controller.signal);

      // Discard if a newer request (or a disable) superseded this one.
      if (abortController !== controller) return;

      // Revoke any previous derived URL before replacing it (safe: the player
      // swaps to the new URL, never back to the old one).
      revokeUrl();
      url = URL.createObjectURL(blob);
      active = true;
      options.onActivate?.(url);
    } catch (err) {
      if (err instanceof Error && err.name === 'AbortError') {
        return; // Superseded, leave state to the newer request/disable
      }
      if (abortController !== controller) return;
      active = false;
      error = err instanceof Error ? err.message : t('media.audio.audibleBats.error');
      logger.error('Audible bats generation failed', err);
      // If there was an active derived URL, deactivate cleanly before revoking it
      // so the player can swap back to the original source before the blob is freed.
      if (url !== null) {
        options.onDeactivate?.();
        revokeUrl();
      }
    } finally {
      if (abortController === controller) {
        generating = false;
        abortController = null;
      }
    }
  }

  // Return to normal playback. Called when the user disables the mode and
  // whenever a setting changes while a derived copy is active.
  function disable() {
    if (abortController) {
      abortController.abort();
      abortController = null;
    }
    generating = false;

    const hadDerivedAudio = active || url !== null;
    active = false;
    error = null;

    // Let the player swap back to its normal source before we revoke the blob.
    if (hadDerivedAudio) {
      options.onDeactivate?.();
    }
    revokeUrl();
  }

  // Clear all derived-playback state without invoking the swap callbacks (used
  // when the detection itself changes; the player resets its own source).
  function reset() {
    if (abortController) {
      abortController.abort();
      abortController = null;
    }
    revokeUrl();
    active = false;
    generating = false;
    error = null;
  }

  // Release resources on unmount.
  function cleanup() {
    if (abortController) {
      abortController.abort();
      abortController = null;
    }
    revokeUrl();
  }

  return {
    get active() {
      return active;
    },
    get generating() {
      return generating;
    },
    get error() {
      return error;
    },
    get url() {
      return url;
    },
    enable,
    disable,
    reset,
    cleanup,
  };
}
