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
  import { createSpectrogramLoader } from '$lib/utils/spectrogramLoader.svelte';
  import { DEFAULT_PLAYBACK_SPEED } from '$lib/utils/audio';

  // Configuration constants
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

  const loader = createSpectrogramLoader({ size: 'md', raw: true });

  let cardElement = $state<HTMLElement | undefined>(undefined);
  let isVisible = $state(false);

  // Menu state for z-index management
  let isMenuOpen = $state(false);
  let isAudioSettingsOpen = $state(false);

  // Audio settings state (per-card, not shared)
  let audioGainValue = $state(DEFAULT_AUDIO_GAIN);
  let audioFilterFreq = $state(DEFAULT_AUDIO_FILTER_FREQ);
  let audioPlaybackSpeed = $state(DEFAULT_PLAYBACK_SPEED);
  let audioContextAvailable = $state(true);

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

  // Start/stop loader based on visibility
  $effect(() => {
    if (isVisible) {
      loader.start(detection.id);
    } else {
      loader.stop();
    }
  });

  // Handle detection ID changes (component reuse in keyed each)
  let previousDetectionId: number | undefined;
  $effect(() => {
    const currentId = detection.id;
    if (previousDetectionId !== undefined && previousDetectionId !== currentId) {
      audioGainValue = DEFAULT_AUDIO_GAIN;
      audioFilterFreq = DEFAULT_AUDIO_FILTER_FREQ;
      audioPlaybackSpeed = DEFAULT_PLAYBACK_SPEED;
      audioContextAvailable = true;

      if (isVisible) {
        loader.start(currentId);
      }
    }
    previousDetectionId = currentId;
  });

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

  // eslint-disable-next-line no-undef -- browser global
  let observer: IntersectionObserver | undefined;

  onMount(() => {
    if (!cardElement) return;

    // eslint-disable-next-line no-undef -- browser global
    observer = new IntersectionObserver(
      entries => {
        for (const entry of entries) {
          isVisible = entry.isIntersecting;
        }
      },
      { rootMargin: '200px 0px' }
    );
    observer.observe(cardElement);
  });

  onDestroy(() => {
    observer?.disconnect();
    loader.destroy();
  });
</script>

<article
  bind:this={cardElement}
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
      {#if loader.showSpinner}
        <div class="spectrogram-loading">
          <span class="loading loading-spinner loading-md text-base-content/50"></span>
          {#if loader.isQueued}
            <span class="text-xs text-base-content/40 mt-1">Waiting...</span>
          {:else if loader.isGenerating}
            <span class="text-xs text-base-content/40 mt-1">Generating...</span>
          {/if}
        </div>
      {/if}

      {#if loader.error}
        <div class="spectrogram-error">
          <span class="text-sm text-base-content/50">Spectrogram unavailable</span>
        </div>
      {:else if loader.spectrogramUrl}
        <img
          src={loader.spectrogramUrl}
          alt="Spectrogram for {detection.commonName}"
          class="spectrogram-image"
          class:opacity-0={loader.state === 'loading'}
          decoding="async"
          onload={() => loader.handleImageLoad()}
          onerror={() => loader.handleImageError()}
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
    flex-direction: column;
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
