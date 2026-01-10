<!--
  DetectionCard.svelte

  A card component displaying a detection with a prominent spectrogram background.
  Features overlaid metadata including confidence, weather, species info, and action menu.

  Props:
  - detection: Detection - The detection data to display
  - isNew?: boolean - Whether this is a newly arrived detection (for animation)
  - onFreezeStart?: () => void - Callback when interaction starts
  - onFreezeEnd?: () => void - Callback when interaction ends
  - onReview?: () => void - Callback for review action
  - onToggleSpecies?: () => void - Callback for toggle species action
  - onToggleLock?: () => void - Callback for toggle lock action
  - onDelete?: () => void - Callback for delete action
-->
<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import type { Detection } from '$lib/types/detection.types';
  import ConfidenceBadge from './ConfidenceBadge.svelte';
  import WeatherBadge from './WeatherBadge.svelte';
  import PlayOverlay from './PlayOverlay.svelte';
  import SpeciesInfoBar from './SpeciesInfoBar.svelte';
  import CardActionMenu from './CardActionMenu.svelte';
  import AudioSettingsButton from './AudioSettingsButton.svelte';
  import { cn } from '$lib/utils/cn';
  import { loggers } from '$lib/utils/logger';
  import { useDelayedLoading } from '$lib/utils/delayedLoading.svelte.js';
  import { acquireSlot, releaseSlot } from '$lib/utils/imageLoadQueue';

  const logger = loggers.ui;

  interface Props {
    detection: Detection;
    isNew?: boolean;
    isExcluded?: boolean;
    onFreezeStart?: () => void;
    onFreezeEnd?: () => void;
    onReview?: () => void;
    onToggleSpecies?: () => void;
    onToggleLock?: () => void;
    onDelete?: () => void;
  }

  let {
    detection,
    isNew = false,
    isExcluded = false,
    onFreezeStart,
    onFreezeEnd,
    onReview,
    onToggleSpecies,
    onToggleLock,
    onDelete,
  }: Props = $props();

  // Spectrogram loading state
  const spectrogramLoader = useDelayedLoading({
    delayMs: 150,
    timeoutMs: 60000,
    onTimeout: () => {
      logger.warn('Spectrogram loading timeout', { detectionId: detection.id });
    },
  });

  // Spectrogram retry configuration
  const MAX_RETRIES = 4;
  const RETRY_DELAYS = [500, 1000, 2000, 4000];
  let retryCount = $state(0);
  let retryTimer: ReturnType<typeof setTimeout> | undefined;
  // svelte-ignore non_reactive_update
  let spectrogramImage: HTMLImageElement;

  // Menu state for z-index management
  let isMenuOpen = $state(false);

  // Audio settings state (per-card, not shared)
  let audioGainValue = $state(0);
  let audioFilterFreq = $state(20);
  let audioContextAvailable = $state(true);

  // Queue slot management
  let slotHandle: ReturnType<typeof acquireSlot> | undefined;
  let hasSlot = $state(false);

  function handleGainChange(value: number) {
    audioGainValue = value;
  }

  function handleFilterChange(value: number) {
    audioFilterFreq = value;
  }

  function handleAudioContextAvailable(available: boolean) {
    audioContextAvailable = available;
  }

  // Spectrogram URL - only set when we have a queue slot
  const spectrogramUrl = $derived(
    hasSlot ? `/api/v2/spectrogram/${detection.id}?size=md&raw=true` : ''
  );

  // Track previous detection ID for cleanup
  let previousDetectionId: number | undefined;

  // Reset state when detection changes (component reuse)
  $effect(() => {
    const currentId = detection.id;
    if (previousDetectionId !== undefined && previousDetectionId !== currentId) {
      // Detection changed - cleanup any pending retry
      if (retryTimer) {
        clearTimeout(retryTimer);
        retryTimer = undefined;
      }
      retryCount = 0;
      spectrogramLoader.reset();

      // Release old slot and request new one
      if (hasSlot) {
        releaseSlot();
        hasSlot = false;
      }
      if (slotHandle) {
        slotHandle.cancel();
      }

      // Request new slot for new detection
      spectrogramLoader.setLoading(true);
      const capturedId = currentId;
      slotHandle = acquireSlot();
      slotHandle.promise.then(acquired => {
        if (acquired && detection.id === capturedId) {
          hasSlot = true;
        } else if (acquired) {
          // Detection changed while waiting, release the stale slot
          releaseSlot();
        }
      });

      // Reset audio settings to defaults for new detection
      audioGainValue = 0;
      audioFilterFreq = 20;
      audioContextAvailable = true;
    }
    previousDetectionId = currentId;
  });

  function handleSpectrogramLoad() {
    spectrogramLoader.setLoading(false);
    retryCount = 0;
    if (retryTimer) {
      clearTimeout(retryTimer);
      retryTimer = undefined;
    }
    // Release queue slot on successful load
    if (hasSlot) {
      releaseSlot();
      hasSlot = false;
    }
  }

  function handleSpectrogramError() {
    if (retryCount < MAX_RETRIES) {
      const delayIndex = Math.min(retryCount, RETRY_DELAYS.length - 1);
      const retryDelay = RETRY_DELAYS.at(delayIndex) ?? 4000;

      logger.debug('Spectrogram load failed, retrying', {
        detectionId: detection.id,
        retryCount: retryCount + 1,
        retryDelay,
      });

      retryCount++;

      if (retryTimer) {
        clearTimeout(retryTimer);
      }

      retryTimer = setTimeout(() => {
        if (spectrogramImage) {
          const url = new URL(spectrogramImage.src);
          url.searchParams.set('retry', retryCount.toString());
          url.searchParams.set('t', Date.now().toString());
          spectrogramImage.src = url.toString();
        }
      }, retryDelay);
    } else {
      spectrogramLoader.setError();
      // Release queue slot on final failure
      if (hasSlot) {
        releaseSlot();
        hasSlot = false;
      }
    }
  }

  function handleMenuOpen() {
    isMenuOpen = true;
    onFreezeStart?.();
  }

  function handleMenuClose() {
    isMenuOpen = false;
    onFreezeEnd?.();
  }

  function handleDownload() {
    // Create a temporary anchor element to trigger download
    const link = document.createElement('a');
    link.href = `/api/v2/audio/${detection.id}`;
    // Use species name and date/time for filename
    const dateTime =
      detection.date && detection.time
        ? `${detection.date}_${detection.time.replace(/:/g, '-')}`
        : String(detection.id);
    link.download = `${detection.commonName || 'detection'}_${dateTime}.wav`;
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
  }

  onMount(() => {
    spectrogramLoader.setLoading(true);

    // Request a slot from the image load queue
    const mountDetectionId = detection.id;
    slotHandle = acquireSlot();
    slotHandle.promise.then(acquired => {
      if (acquired && detection.id === mountDetectionId) {
        hasSlot = true;
      } else if (acquired) {
        // Detection changed while waiting, release the stale slot
        releaseSlot();
      } else {
        // Cancelled (component unmounted while waiting)
        spectrogramLoader.setLoading(false);
      }
    });
  });

  onDestroy(() => {
    if (retryTimer) {
      clearTimeout(retryTimer);
    }
    spectrogramLoader.cleanup();

    // Cancel pending slot request or release acquired slot
    if (slotHandle) {
      slotHandle.cancel();
    }
    if (hasSlot) {
      releaseSlot();
      hasSlot = false;
    }
  });
</script>

<article
  class={cn(
    'detection-card group relative rounded-xl',
    isNew && 'new-detection',
    isMenuOpen && 'z-[60]'
  )}
>
  <!-- Inner container with overflow-hidden for spectrogram clipping -->
  <div class="detection-card-inner">
    <!-- Spectrogram Background -->
    <div class="spectrogram-container">
      {#if spectrogramLoader.showSpinner}
        <div class="spectrogram-loading">
          <span class="loading loading-spinner loading-md text-base-content/50"></span>
        </div>
      {/if}

      {#if spectrogramLoader.error}
        <div class="spectrogram-error">
          <span class="text-sm text-base-content/50">Spectrogram unavailable</span>
        </div>
      {:else if spectrogramUrl}
        <img
          bind:this={spectrogramImage}
          src={spectrogramUrl}
          alt="Spectrogram for {detection.commonName}"
          class="spectrogram-image"
          class:opacity-0={spectrogramLoader.loading}
          decoding="async"
          onload={handleSpectrogramLoad}
          onerror={handleSpectrogramError}
        />
      {/if}

      <!-- Gradient Overlay -->
      <div class="gradient-overlay"></div>
    </div>

    <!-- Top-Left Badges: Confidence + Weather -->
    <div class="absolute top-3 left-3 flex items-center gap-2 z-10">
      <ConfidenceBadge confidence={detection.confidence} />
      {#if detection.weather?.weatherIcon}
        <WeatherBadge
          weatherIcon={detection.weather.weatherIcon}
          description={detection.weather.description}
          temperature={detection.weather.temperature}
          units={detection.weather.units}
          timeOfDay={detection.timeOfDay}
        />
      {/if}
    </div>

    <!-- Center Play Button -->
    <PlayOverlay
      detectionId={detection.id}
      {onFreezeStart}
      {onFreezeEnd}
      gainValue={audioGainValue}
      filterFreq={audioFilterFreq}
      onAudioContextAvailable={handleAudioContextAvailable}
    />

    <!-- Bottom Species Info Bar -->
    <SpeciesInfoBar {detection} />
  </div>

  <!-- Top-Right Controls - OUTSIDE overflow-hidden container -->
  <div class="absolute top-2 right-2 z-50 flex items-center gap-1.5">
    <AudioSettingsButton
      gainValue={audioGainValue}
      filterFreq={audioFilterFreq}
      onGainChange={handleGainChange}
      onFilterChange={handleFilterChange}
      disabled={!audioContextAvailable}
    />
    <CardActionMenu
      {detection}
      {isExcluded}
      {onReview}
      {onToggleSpecies}
      {onToggleLock}
      {onDelete}
      onDownload={handleDownload}
      onMenuOpen={handleMenuOpen}
      onMenuClose={handleMenuClose}
    />
  </div>
</article>

<style>
  .detection-card {
    background-color: var(--color-base-100);
  }

  .detection-card-inner {
    position: relative;
    height: 15rem; /* ~240px - taller for better spectrogram visibility, especially low frequencies */
    border-radius: 0.75rem;
    overflow: hidden;
  }

  /* Spectrogram container */
  .spectrogram-container {
    position: absolute;
    inset: 0;
    overflow: hidden;
  }

  .spectrogram-image {
    position: absolute;
    left: 0;
    bottom: 0; /* Anchor to bottom of container */
    width: 100%;
    min-height: 100%; /* At least fill container height */
    object-fit: cover;
    object-position: center bottom;
    image-rendering: pixelated;
    transition: opacity 0.3s ease;
  }

  .spectrogram-loading,
  .spectrogram-error {
    position: absolute;
    inset: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    background: linear-gradient(
      135deg,
      color-mix(in srgb, var(--color-base-200) 80%, transparent) 0%,
      color-mix(in srgb, var(--color-base-300) 60%, transparent) 100%
    );
  }

  /* Dark theme spectrogram background */
  :global([data-theme='dark']) .spectrogram-loading,
  :global([data-theme='dark']) .spectrogram-error {
    background: linear-gradient(135deg, rgb(30 41 59 / 0.9) 0%, rgb(15 23 42 / 0.95) 100%);
  }

  /* Gradient overlay - disabled, using backdrop on SpeciesInfoBar instead */
  .gradient-overlay {
    display: none;
  }

  /* New detection animation */
  .new-detection {
    animation: cardHighlight 2s ease-out;
  }

  @keyframes cardHighlight {
    0% {
      box-shadow: 0 0 0 2px oklch(var(--p) / 0.4);
    }

    100% {
      box-shadow: 0 0 0 0 oklch(var(--p) / 0);
    }
  }
</style>
