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
  import { DEFAULT_PLAYBACK_SPEED } from '$lib/utils/audio';

  const logger = loggers.ui;

  // Configuration constants
  const SPECTROGRAM_SPINNER_DELAY_MS = 150;
  const SPECTROGRAM_TIMEOUT_MS = 60000;
  const DEFAULT_AUDIO_GAIN = 0;
  const DEFAULT_AUDIO_FILTER_FREQ = 20;
  const DEFAULT_DOWNLOAD_NAME = 'detection';
  const AUDIO_FILE_EXTENSION = '.wav';

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
    delayMs: SPECTROGRAM_SPINNER_DELAY_MS,
    timeoutMs: SPECTROGRAM_TIMEOUT_MS,
    onTimeout: () => {
      logger.warn('Spectrogram loading timeout', { detectionId: detection.id });
    },
  });

  // Spectrogram retry configuration
  const MAX_RETRIES = 4;
  const RETRY_DELAYS = [500, 1000, 2000, 4000];
  let retryCount = $state(0);
  let retryTimer: ReturnType<typeof setTimeout> | undefined;
  let spectrogramImage = $state<HTMLImageElement | undefined>(undefined);

  // Menu state for z-index management
  let isMenuOpen = $state(false);
  let isAudioSettingsOpen = $state(false);

  // Audio settings state (per-card, not shared)
  let audioGainValue = $state(DEFAULT_AUDIO_GAIN);
  let audioFilterFreq = $state(DEFAULT_AUDIO_FILTER_FREQ);
  let audioPlaybackSpeed = $state(DEFAULT_PLAYBACK_SPEED);
  let audioContextAvailable = $state(true);

  // Queue slot management
  let slotHandle: ReturnType<typeof acquireSlot> | undefined;
  let hasSlot = $state(false);
  // Separate URL state - persists after slot is released (so image stays visible)
  let spectrogramUrl = $state('');

  function handleGainChange(value: number) {
    audioGainValue = value;
  }

  function handleFilterChange(value: number) {
    audioFilterFreq = value;
  }

  function handleSpeedChange(value: number) {
    audioPlaybackSpeed = value;
  }

  function handleAudioContextAvailable(available: boolean) {
    audioContextAvailable = available;
  }

  function handleAudioSettingsOpen() {
    isAudioSettingsOpen = true;
    onFreezeStart?.();
  }

  function handleAudioSettingsClose() {
    isAudioSettingsOpen = false;
    onFreezeEnd?.();
  }

  // Helper: Build spectrogram URL for a detection ID
  function getSpectrogramUrl(id: number): string {
    return `/api/v2/spectrogram/${id}?size=md&raw=true`;
  }

  // Helper: Release the current slot if held
  function releaseCurrentSlot(): void {
    if (hasSlot) {
      releaseSlot();
      hasSlot = false;
    }
  }

  // Helper: Clear the retry timer if active
  function clearRetryTimer(): void {
    if (retryTimer) {
      clearTimeout(retryTimer);
      retryTimer = undefined;
    }
  }

  /**
   * Helper: Request a slot and set up the spectrogram URL when acquired.
   * Handles stale closure by verifying the detection ID hasn't changed.
   * @param forDetectionId - The detection ID this request is for
   */
  function requestSlotForDetection(forDetectionId: number): void {
    slotHandle = acquireSlot();
    slotHandle.promise.then(acquired => {
      if (acquired) {
        // Verify detection hasn't changed while waiting for slot
        if (detection.id === forDetectionId) {
          hasSlot = true;
          spectrogramUrl = getSpectrogramUrl(forDetectionId);
        } else {
          // Detection changed while waiting - release the slot immediately
          // Note: We use releaseSlot() directly, not releaseCurrentSlot(),
          // because hasSlot was never set to true for this request
          releaseSlot();
        }
      } else {
        // Cancelled (component unmounted or detection changed while waiting)
        // Only clear loading if this request is still for the current detection
        // to avoid race condition with new detection's loading state
        if (detection.id === forDetectionId) {
          spectrogramLoader.setLoading(false);
        }
      }
    });
  }

  // Track previous detection ID for cleanup
  let previousDetectionId: number | undefined;

  /**
   * Helper: Reset all state when switching to a new detection.
   * Cleans up pending operations and initializes fresh state.
   * @param newDetectionId - The ID of the new detection to load
   */
  function resetForNewDetection(newDetectionId: number): void {
    // Cleanup pending retry
    clearRetryTimer();
    retryCount = 0;
    spectrogramLoader.reset();

    // Release old slot and cancel pending request
    releaseCurrentSlot();
    if (slotHandle) {
      slotHandle.cancel();
    }

    // Request new slot for new detection
    spectrogramLoader.setLoading(true);
    spectrogramUrl = '';
    requestSlotForDetection(newDetectionId);

    // Reset audio settings to defaults
    audioGainValue = DEFAULT_AUDIO_GAIN;
    audioFilterFreq = DEFAULT_AUDIO_FILTER_FREQ;
    audioPlaybackSpeed = DEFAULT_PLAYBACK_SPEED;
    audioContextAvailable = true;
  }

  // Check if image is already loaded (handles cached images that complete before onload attaches)
  $effect(() => {
    if (
      spectrogramImage &&
      spectrogramUrl &&
      spectrogramImage.complete &&
      spectrogramImage.naturalWidth > 0 &&
      spectrogramLoader.loading
    ) {
      handleSpectrogramLoad();
    }
  });

  // Reset state when detection changes (component reuse)
  $effect(() => {
    const currentId = detection.id;
    if (previousDetectionId !== undefined && previousDetectionId !== currentId) {
      resetForNewDetection(currentId);
    }
    previousDetectionId = currentId;
  });

  function handleSpectrogramLoad() {
    spectrogramLoader.setLoading(false);
    retryCount = 0;
    clearRetryTimer();
    // Release queue slot on successful load
    releaseCurrentSlot();
  }

  function handleSpectrogramError() {
    if (retryCount < MAX_RETRIES) {
      // Index is clamped to valid range, so direct access is safe
      const retryDelay = RETRY_DELAYS[Math.min(retryCount, RETRY_DELAYS.length - 1)];

      logger.debug('Spectrogram load failed, retrying', {
        detectionId: detection.id,
        retryCount: retryCount + 1,
        retryDelay,
      });

      retryCount++;
      clearRetryTimer();

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
      releaseCurrentSlot();
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
    // Sanitize commonName to prevent path traversal (remove characters that aren't alphanumeric, space, dot, underscore, or hyphen)
    const safeCommonName = (detection.commonName || DEFAULT_DOWNLOAD_NAME).replace(
      /[^a-zA-Z0-9 ._-]/g,
      '_'
    );
    const dateTime =
      detection.date && detection.time
        ? `${detection.date}_${detection.time.replace(/:/g, '-')}`
        : String(detection.id);
    link.download = `${safeCommonName}_${dateTime}${AUDIO_FILE_EXTENSION}`;
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
  }

  onMount(() => {
    spectrogramLoader.setLoading(true);
    requestSlotForDetection(detection.id);
  });

  onDestroy(() => {
    clearRetryTimer();
    spectrogramLoader.cleanup();

    // Cancel pending slot request or release acquired slot
    if (slotHandle) {
      slotHandle.cancel();
    }
    releaseCurrentSlot();
  });
</script>

<article
  class={cn(
    'detection-card group relative rounded-xl',
    isNew && 'new-detection',
    (isMenuOpen || isAudioSettingsOpen) && 'z-[60]'
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
      playbackSpeed={audioPlaybackSpeed}
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
      playbackSpeed={audioPlaybackSpeed}
      onGainChange={handleGainChange}
      onFilterChange={handleFilterChange}
      onSpeedChange={handleSpeedChange}
      disabled={!audioContextAvailable}
      onMenuOpen={handleAudioSettingsOpen}
      onMenuClose={handleAudioSettingsClose}
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
