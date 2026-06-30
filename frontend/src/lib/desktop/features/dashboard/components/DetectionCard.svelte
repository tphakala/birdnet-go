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
  import MoonBadge from './MoonBadge.svelte';
  import SourceBadge from './SourceBadge.svelte';
  import PlayOverlay from './PlayOverlay.svelte';
  import SpeciesInfoBar from './SpeciesInfoBar.svelte';
  import ActionMenu from '$lib/desktop/components/ui/ActionMenu.svelte';
  import AudioSettingsButton from './AudioSettingsButton.svelte';
  import { cn } from '$lib/utils/cn';
  import { downloadDetectionAudio } from '$lib/utils/audioDownload';
  import { createSpectrogramLoader } from '$lib/utils/spectrogramLoader.svelte';
  import { DEFAULT_PLAYBACK_SPEED } from '$lib/utils/audio';
  import { get } from 'svelte/store';
  import { dashboardSettings } from '$lib/stores/settings';
  import { t } from '$lib/i18n';

  // Configuration constants - use helper to read current default gain at call time
  // (cards are recycled via keyed {#each}, so a one-time const would go stale)
  const getDefaultAudioGain = () => get(dashboardSettings)?.defaultAudioGain ?? 0;
  const DEFAULT_AUDIO_FILTER_FREQ = 20;

  interface Props {
    detection: Detection;
    isNew?: boolean;
    isExcluded?: boolean;
    /** When false (audio export disabled), the card hides the spectrogram and audio controls */
    audioEnabled?: boolean;
    onFreezeStart?: () => void;
    onFreezeEnd?: () => void;
    onReview?: () => void;
    onMarkCorrect?: () => void;
    onMarkFalsePositive?: () => void;
    onToggleSpecies?: () => void;
    onToggleLock?: () => void;
    onDelete?: () => void;
  }

  let {
    detection,
    isNew = false,
    isExcluded = false,
    audioEnabled = true,
    onFreezeStart,
    onFreezeEnd,
    onReview,
    onMarkCorrect,
    onMarkFalsePositive,
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
  let audioGainValue = $state(getDefaultAudioGain());
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

  // Start/stop loader based on visibility. Skip entirely when audio export is
  // disabled: no clips exist, so there is no spectrogram to fetch.
  $effect(() => {
    if (audioEnabled && isVisible) {
      loader.start(detection.id);
    } else {
      loader.stop();
    }
  });

  function handleMenuOpen() {
    isMenuOpen = true;
    onFreezeStart?.();
  }

  function handleMenuClose() {
    isMenuOpen = false;
    onFreezeEnd?.();
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
  <!-- Compact (shorter) layout when there is no spectrogram to display -->
  <div class="detection-card-inner" class:compact={!audioEnabled}>
    <!-- Spectrogram Background (hidden when audio export is disabled) -->
    {#if audioEnabled}
      <div class="spectrogram-container">
        {#if loader.showSpinner}
          <div class="spectrogram-loading">
            <span class="loading loading-spinner loading-md text-[var(--color-base-content)]/50"
            ></span>
            {#if loader.isQueued}
              <span class="text-xs text-[var(--color-base-content)]/40 mt-1"
                >{t('components.audio.waiting')}</span
              >
            {:else if loader.isGenerating}
              <span class="text-xs text-[var(--color-base-content)]/40 mt-1"
                >{t('components.audio.generating')}</span
              >
            {/if}
          </div>
        {/if}

        {#if loader.error}
          <div class="spectrogram-error">
            <span class="text-sm text-[var(--color-base-content)]/50"
              >{t('components.audio.spectrogramUnavailable')}</span
            >
          </div>
        {:else if loader.spectrogramUrl}
          <img
            src={loader.spectrogramUrl}
            alt={t('components.audio.spectrogramForSpecies', { species: detection.commonName })}
            class="spectrogram-image"
            class:opacity-0={loader.state === 'loading'}
            decoding="async"
            onload={() => loader.handleImageLoad()}
            onerror={() => loader.handleImageError()}
          />
        {/if}
      </div>
    {:else}
      <!-- Neutral background placeholder keeps the card's shape/aspect ratio -->
      <div class="spectrogram-container spectrogram-placeholder"></div>
    {/if}

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
      {#if detection.weather?.moonPhaseName && detection.timeOfDay === 'night'}
        <MoonBadge moonPhaseName={detection.weather.moonPhaseName} />
      {/if}
      <SourceBadge {detection} variant="overlay" />
    </div>

    <!-- Center Play Button (hidden when audio export is disabled) -->
    {#if audioEnabled}
      <PlayOverlay
        detectionId={detection.id}
        {onFreezeStart}
        {onFreezeEnd}
        gainValue={audioGainValue}
        filterFreq={audioFilterFreq}
        playbackSpeed={audioPlaybackSpeed}
        onAudioContextAvailable={handleAudioContextAvailable}
      />
    {/if}

    <!-- Bottom Species Info Bar -->
    <SpeciesInfoBar {detection} />
  </div>

  <!-- Top-Right Controls - OUTSIDE overflow-hidden container -->
  <div class="absolute top-2 right-2 z-50 flex items-center gap-1.5">
    {#if audioEnabled}
      <AudioSettingsButton
        gainValue={audioGainValue}
        filterFreq={audioFilterFreq}
        playbackSpeed={audioPlaybackSpeed}
        defaultGainValue={getDefaultAudioGain()}
        onGainChange={handleGainChange}
        onFilterChange={handleFilterChange}
        onSpeedChange={handleSpeedChange}
        disabled={!audioContextAvailable}
        onMenuOpen={handleAudioSettingsOpen}
        onMenuClose={handleAudioSettingsClose}
      />
    {/if}
    <ActionMenu
      {detection}
      {isExcluded}
      variant="overlay"
      {onMarkCorrect}
      {onMarkFalsePositive}
      {onReview}
      {onToggleSpecies}
      {onToggleLock}
      {onDelete}
      onDownload={audioEnabled ? () => downloadDetectionAudio(detection) : undefined}
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

  /* Without a spectrogram, the card only needs room for the top badges and the
     bottom species-info bar, so collapse the reserved height. */
  .detection-card-inner.compact {
    height: 7rem; /* ~112px - fits badges + species-info bar without overlap */
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
  .spectrogram-error,
  .spectrogram-placeholder {
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
  :global([data-theme='dark']) .spectrogram-error,
  :global([data-theme='dark']) .spectrogram-placeholder {
    background: linear-gradient(135deg, rgb(30 41 59 / 0.9) 0%, rgb(15 23 42 / 0.95) 100%);
  }

  /* New detection animation */
  .new-detection {
    animation: cardHighlight 2s ease-out;
  }

  @keyframes cardHighlight {
    0% {
      box-shadow: 0 0 0 2px color-mix(in srgb, var(--color-primary) 40%, transparent);
    }

    100% {
      box-shadow: 0 0 0 0 color-mix(in srgb, var(--color-primary) 0%, transparent);
    }
  }
</style>
