/**
 * useAudibleBats.svelte.ts
 *
 * Composable that owns the "audible bats" derived-audio generation lifecycle for
 * a single detection: it POSTs the chosen settings to the server, turns the
 * returned WAV into an object URL, and tracks the active/generating/error state.
 *
 * It deliberately does NOT touch any <audio> element. The consuming player owns
 * playback and reacts to the generated `url` (or the optional onActivate /
 * onDeactivate callbacks) to swap its source. This keeps the request lifecycle
 * shared between the full AudioPlayer and the compact PlayOverlay (grid cards)
 * without duplicating fetch/blob/abort handling.
 */
import { buildAppUrl } from '$lib/utils/urlHelpers';
import { getCsrfToken } from '$lib/utils/api';
import { t } from '$lib/i18n';
import { loggers } from '$lib/utils/logger';
import type { AudibleBatsSettings } from '$lib/desktop/features/dashboard/components/AudibleBatsButton.svelte';

const logger = loggers.audio;

interface UseAudibleBatsOptions {
  /** Returns the current detection ID (read lazily so it tracks card recycling). */
  getDetectionId: () => string | number;
  /** Called once the derived audio URL is ready, so the player can swap to it. */
  onActivate?: (_url: string) => void;
  /** Called before the derived URL is revoked, so the player can swap away first. */
  onDeactivate?: () => void;
}

// Build JSON headers with CSRF token (server rejects mutating requests without it).
function jsonHeadersWithCsrf(): Record<string, string> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  const csrfToken = getCsrfToken();
  if (csrfToken) {
    headers['X-CSRF-Token'] = csrfToken;
  }
  return headers;
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

  // Generate (or regenerate) the derived audible-bats audio.
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
      const response = await fetch(
        buildAppUrl(`/api/v2/audio/${encodeURIComponent(String(detectionId))}/audible-bats`),
        {
          method: 'POST',
          headers: jsonHeadersWithCsrf(),
          signal: controller.signal,
          body: JSON.stringify({
            expansion: settings.expansion,
            normalize: settings.normalize,
            gain_db: settings.gainDb,
          }),
        }
      );

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

      const blob = await response.blob();

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
        return; // Superseded — leave state to the newer request/disable
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
  // when the detection itself changes — the player resets its own source).
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
