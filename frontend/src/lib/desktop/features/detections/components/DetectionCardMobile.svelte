<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import type { Detection } from '$lib/types/detection.types';
  import ConfidenceBadge from '$lib/desktop/features/dashboard/components/ConfidenceBadge.svelte';
  import WeatherBadge from '$lib/desktop/features/dashboard/components/WeatherBadge.svelte';
  import MoonBadge from '$lib/desktop/features/dashboard/components/MoonBadge.svelte';
  import SourceBadge from '$lib/desktop/features/dashboard/components/SourceBadge.svelte';
  import PlayOverlay from '$lib/desktop/features/dashboard/components/PlayOverlay.svelte';
  import SpeciesInfoBar from '$lib/desktop/features/dashboard/components/SpeciesInfoBar.svelte';
  import ActionMenu from '$lib/desktop/components/ui/ActionMenu.svelte';
  import AudibleBatsButton from '$lib/desktop/features/dashboard/components/AudibleBatsButton.svelte';
  import { useAudibleBats } from '$lib/utils/useAudibleBats.svelte';
  import { cn } from '$lib/utils/cn';
  import { downloadDetectionAudio } from '$lib/utils/audioDownload';
  import { createSpectrogramLoader } from '$lib/utils/spectrogramLoader.svelte';
  import { DEFAULT_PLAYBACK_SPEED } from '$lib/utils/audio';
  import { get } from 'svelte/store';
  import { dashboardSettings } from '$lib/stores/settings';
  import { navigation } from '$lib/stores/navigation.svelte';
  import { t } from '$lib/i18n';

  const getDefaultAudioGain = () => get(dashboardSettings)?.defaultAudioGain ?? 0;
  const DEFAULT_AUDIO_FILTER_FREQ = 20;

  // Presentational card: the parent (DetectionsList) owns the action handlers
  // and the ConfirmModal via the shared useDetectionActions composable, and
  // passes them in as callbacks plus the server-hydrated isExcluded state.
  interface Props {
    detection: Detection;
    isExcluded?: boolean;
    onDetailsClick?: (_id: number) => void;
    onReview?: () => void;
    onMarkCorrect?: () => void;
    onMarkFalsePositive?: () => void;
    onToggleSpecies?: () => void;
    onToggleLock?: () => void;
    onDelete?: () => void;
  }

  let {
    detection,
    isExcluded = false,
    onDetailsClick,
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
  let isMenuOpen = $state(false);

  let audioGainValue = $state(getDefaultAudioGain());
  let audioFilterFreq = $state(DEFAULT_AUDIO_FILTER_FREQ);
  let audioPlaybackSpeed = $state(DEFAULT_PLAYBACK_SPEED);

  // Audible bats: only offered for bat detections (matches DetectionCard). The
  // request lifecycle lives in the shared composable; PlayOverlay swaps to the
  // generated `url` while the spectrogram keeps spanning the full original clip.
  const MODEL_TYPE_BAT = 'bat';
  const isBatDetection = $derived(detection.modelType === MODEL_TYPE_BAT);
  const audibleBats = useAudibleBats({ getDetectionId: () => detection.id });

  // Reset derived playback if this card instance is recycled to a different
  // detection (keyed {#each} normally avoids this, but guard defensively).
  // svelte-ignore state_referenced_locally
  let previousDetectionId = detection.id;
  $effect(() => {
    if (detection.id !== previousDetectionId) {
      previousDetectionId = detection.id;
      audibleBats.reset();
    }
  });

  $effect(() => {
    if (detection.clipName && isVisible) {
      loader.start(detection.id);
    } else {
      loader.stop();
    }
  });

  function handleMenuOpen() {
    isMenuOpen = true;
  }

  function handleMenuClose() {
    isMenuOpen = false;
  }

  function handleViewDetails() {
    if (onDetailsClick) {
      onDetailsClick(detection.id);
    } else {
      navigation.navigate(`/ui/detections/${detection.id}`);
    }
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
    audibleBats.cleanup();
  });
</script>

<article
  bind:this={cardElement}
  class={cn('detection-card group relative rounded-xl', isMenuOpen && 'z-[60]')}
>
  <!-- Inner container with overflow-hidden for spectrogram clipping -->
  <!-- Compact (shorter) layout when there is no spectrogram to display -->
  <div class="detection-card-inner" class:compact={!detection.clipName}>
    <!-- Spectrogram Background (hidden when this detection has no clip) -->
    {#if detection.clipName}
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

    <!-- Center Play Button (hidden when this detection has no clip) -->
    {#if detection.clipName}
      <PlayOverlay
        detectionId={detection.id}
        gainValue={audioGainValue}
        filterFreq={audioFilterFreq}
        playbackSpeed={audioPlaybackSpeed}
        audibleBatsSrc={audibleBats.url}
      />
    {/if}

    <!-- Bottom Species Info Bar: tappable for all auth levels to view details -->
    <button
      type="button"
      class="absolute inset-x-0 bottom-0 z-[11] text-left"
      onclick={handleViewDetails}
      aria-label={t('detections.row.viewDetails', { species: detection.commonName })}
    >
      <SpeciesInfoBar {detection} />
    </button>
  </div>

  <!-- Top-Right Controls - OUTSIDE overflow-hidden container -->
  <div class="absolute top-2 right-2 z-50 flex items-center gap-1.5">
    {#if audioEnabled && isBatDetection}
      <AudibleBatsButton
        active={audibleBats.active}
        generating={audibleBats.generating}
        error={audibleBats.error}
        onEnable={settings => audibleBats.enable(settings)}
        onDisable={() => audibleBats.disable()}
        onMenuOpen={handleMenuOpen}
        onMenuClose={handleMenuClose}
      />
    {/if}
    <ActionMenu
      {detection}
      variant="overlay"
      {onMarkCorrect}
      {onMarkFalsePositive}
      {onReview}
      {isExcluded}
      {onToggleSpecies}
      {onToggleLock}
      {onDelete}
      onDownload={detection.clipName ? () => downloadDetectionAudio(detection) : undefined}
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
    height: 15rem;
    border-radius: 0.75rem;
    overflow: hidden;
  }

  /* Without a spectrogram, collapse to fit the badges + species-info bar only. */
  .detection-card-inner.compact {
    height: 7rem;
  }

  .spectrogram-container {
    position: absolute;
    inset: 0;
    overflow: hidden;
  }

  .spectrogram-image {
    position: absolute;
    left: 0;
    bottom: 0;
    width: 100%;
    min-height: 100%;
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

  :global([data-theme='dark']) .spectrogram-loading,
  :global([data-theme='dark']) .spectrogram-error,
  :global([data-theme='dark']) .spectrogram-placeholder {
    background: linear-gradient(135deg, rgb(30 41 59 / 0.9) 0%, rgb(15 23 42 / 0.95) 100%);
  }
</style>
