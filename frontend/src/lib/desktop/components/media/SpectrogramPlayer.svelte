<!--
  SpectrogramPlayer.svelte

  Compact audio player for constrained contexts (table cells, card grids).
  Shows a spectrogram image with play/pause overlay and progress bar.
  Uses the shared useAudioPlayback composable for battle-tested audio engine.

  For full-featured playback with clip extraction, processing controls,
  and AudioToolbar, use AudioPlayer.svelte instead.

  Props:
  - audioUrl: URL of the audio file to play
  - detectionId: Unique ID for the detection
  - spectrogramSize: Spectrogram display size - md/lg/xl (default: md)
  - onPlayStart: Callback when audio starts playing
  - onPlayEnd: Callback when audio stops playing
-->

<script lang="ts">
  import { Play, Pause, XCircle } from '@lucide/svelte';
  import { useAudioPlayback } from '$lib/utils/useAudioPlayback.svelte';
  import { useDelayedLoading } from '$lib/utils/delayedLoading.svelte';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';

  const logger = loggers.audio;

  type SpectrogramSize = 'md' | 'lg' | 'xl';

  interface Props {
    audioUrl: string;
    detectionId: string;
    spectrogramSize?: SpectrogramSize;
    onPlayStart?: () => void;
    onPlayEnd?: () => void;
  }

  let { audioUrl, detectionId, spectrogramSize = 'md', onPlayStart, onPlayEnd }: Props = $props();

  // --- Audio engine (shared composable) ---
  // Initial values are intentionally captured here; audioUrl reactivity is
  // handled below via $effect calling audio.setAudioUrl(). The composable
  // stores detectionId only for logging and callbacks are stable references.
  // svelte-ignore state_referenced_locally
  const initialAudioUrl = audioUrl;
  // svelte-ignore state_referenced_locally
  const initialDetectionId = detectionId;
  // svelte-ignore state_referenced_locally
  const initialOnPlayStart = onPlayStart;
  // svelte-ignore state_referenced_locally
  const initialOnPlayEnd = onPlayEnd;
  const audio = useAudioPlayback({
    audioUrl: initialAudioUrl,
    detectionId: initialDetectionId,
    onPlayStart: initialOnPlayStart,
    onPlayEnd: initialOnPlayEnd,
  });

  // React to prop changes
  $effect(() => {
    audio.setAudioUrl(audioUrl);
  });

  // --- Spectrogram loading ---
  const spectrogramLoader = useDelayedLoading({
    delayMs: 150,
    timeoutMs: 30000,
    onTimeout: () => {
      logger.warn('Spectrogram loading timeout', { detectionId });
    },
  });

  // Spectrogram retry
  const MAX_RETRIES = 3;
  const RETRY_DELAYS = [500, 1000, 2000];
  let retryCount = $state(0);
  let retryTimer: ReturnType<typeof setTimeout> | undefined;
  let cacheKey = $state(0);

  const spectrogramUrl = $derived(
    buildAppUrl(
      `/api/v2/spectrogram/${encodeURIComponent(detectionId)}?size=${spectrogramSize}&raw=true${cacheKey > 0 ? `&t=${cacheKey}` : ''}`
    )
  );

  // Track previous detection ID to detect changes and reset spectrogram state.
  // Uses a non-reactive variable since we only compare inside the $effect.
  // svelte-ignore state_referenced_locally
  let previousDetectionId = detectionId;
  $effect(() => {
    // Read detectionId (reactive prop) to create the dependency
    const currentId = detectionId;
    if (currentId !== previousDetectionId) {
      previousDetectionId = currentId;
      retryCount = 0;
      cacheKey = 0;
      spectrogramLoader.setLoading(true);
      if (retryTimer) {
        clearTimeout(retryTimer);
        retryTimer = undefined;
      }
    }
  });

  // Start loading on mount
  $effect(() => {
    if (spectrogramUrl) {
      spectrogramLoader.setLoading(true);
    }
    return () => {
      if (retryTimer) {
        clearTimeout(retryTimer);
        retryTimer = undefined;
      }
    };
  });

  function handleSpectrogramLoad() {
    spectrogramLoader.setLoading(false);
    retryCount = 0;
  }

  function handleSpectrogramError() {
    if (retryCount < MAX_RETRIES) {
      const delay = RETRY_DELAYS[Math.min(retryCount, RETRY_DELAYS.length - 1)];
      retryCount++;
      retryTimer = setTimeout(() => {
        cacheKey = Date.now();
      }, delay);
    } else {
      spectrogramLoader.setError();
    }
  }

  function handlePlayClick(e: MouseEvent) {
    e.stopPropagation();
    audio.togglePlayPause();
  }
</script>

<div class="spectrogram-player" role="group" aria-label={t('media.audio.player')}>
  <!-- Spectrogram image -->
  <div class="spectrogram-image-container">
    {#if spectrogramLoader.showSpinner}
      <div class="spectrogram-overlay">
        <div
          class="h-5 w-5 animate-spin rounded-full border-2 border-[var(--color-primary)] border-t-transparent"
          role="status"
          aria-label={t('components.audio.spectrogramLoadingAria')}
        ></div>
      </div>
    {/if}

    {#if spectrogramLoader.error}
      <div class="spectrogram-overlay">
        <XCircle class="size-5 text-[var(--color-base-content)]/30" aria-hidden="true" />
      </div>
    {:else}
      <img
        src={spectrogramUrl}
        alt={t('components.audio.spectrogramAlt')}
        decoding="async"
        class="spectrogram-img"
        class:invisible={spectrogramLoader.loading}
        onload={handleSpectrogramLoad}
        onerror={handleSpectrogramError}
      />
    {/if}

    <!-- Play/pause overlay button -->
    <button
      class="play-overlay"
      class:is-playing={audio.isPlaying}
      onclick={handlePlayClick}
      disabled={audio.isLoading}
      aria-label={audio.isPlaying ? t('media.audio.pause') : t('media.audio.play')}
    >
      {#if audio.isLoading}
        <div
          class="w-4 h-4 border-2 border-white border-t-transparent rounded-full animate-spin"
        ></div>
      {:else if audio.isPlaying}
        <Pause class="size-4" />
      {:else}
        <Play class="size-4" />
      {/if}
    </button>

    <!-- Progress bar (bottom edge) -->
    {#if audio.progress > 0}
      <div class="progress-track">
        <div class="progress-fill" style:width="{audio.progress}%"></div>
      </div>
    {/if}
  </div>

  <!-- Audio error -->
  {#if audio.error}
    <div class="audio-error" role="alert" aria-live="assertive">
      <span class="text-xs text-[var(--color-error)]">{audio.error}</span>
    </div>
  {/if}
</div>

<style>
  .spectrogram-player {
    width: 100%;
    max-width: 200px;
  }

  .spectrogram-image-container {
    position: relative;
    width: 100%;
    background: linear-gradient(to bottom, rgb(128 128 128 / 0.1), rgb(128 128 128 / 0.05));
    border-radius: 0.375rem;
    overflow: hidden;
  }

  .spectrogram-img {
    display: block;
    width: 100%;
    height: auto;
    border-radius: 0.375rem;
  }

  .spectrogram-overlay {
    display: flex;
    align-items: center;
    justify-content: center;
    aspect-ratio: 2 / 1;
    width: 100%;
  }

  /* Play button: always visible at reduced opacity, full opacity on hover */
  .play-overlay {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    color: white;
    background: rgb(0 0 0 / 0.25);
    opacity: 0.7;
    transition: opacity 0.15s ease;
    cursor: pointer;
    border: none;
    padding: 0;
  }

  .play-overlay:hover {
    opacity: 1;
    background: rgb(0 0 0 / 0.35);
  }

  .play-overlay.is-playing {
    opacity: 0;
  }

  .play-overlay.is-playing:hover {
    opacity: 1;
  }

  /* Thin progress bar at bottom edge */
  .progress-track {
    position: absolute;
    bottom: 0;
    left: 0;
    right: 0;
    height: 3px;
    background: rgb(255 255 255 / 0.2);
  }

  .progress-fill {
    height: 100%;
    background: var(--color-primary);
    transition: width 0.1s linear;
  }

  .audio-error {
    padding: 0.125rem 0.25rem;
    text-align: center;
  }
</style>
