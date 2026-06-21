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
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';
  import { cn } from '$lib/utils/cn';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { createSpectrogramLoader } from '$lib/utils/spectrogramLoader.svelte';
  import { DEFAULT_PLAYBACK_SPEED } from '$lib/utils/audio';
  import { get } from 'svelte/store';
  import { dashboardSettings } from '$lib/stores/settings';
  import { fetchWithCSRF } from '$lib/utils/api';
  import { toastActions } from '$lib/stores/toast';
  import { setDetectionVerification } from '$lib/utils/reviewDetection';
  import { navigation } from '$lib/stores/navigation.svelte';
  import { t } from '$lib/i18n';

  const getDefaultAudioGain = () => get(dashboardSettings)?.defaultAudioGain ?? 0;
  const DEFAULT_AUDIO_FILTER_FREQ = 20;
  const DEFAULT_DOWNLOAD_NAME = 'detection';
  const AUDIO_FILE_EXTENSION = '.wav';

  interface Props {
    detection: Detection;
    onDetailsClick?: (_id: number) => void;
    onRefresh?: () => void;
  }

  let { detection, onDetailsClick, onRefresh }: Props = $props();

  const loader = createSpectrogramLoader({ size: 'md', raw: true });

  let cardElement = $state<HTMLElement | undefined>(undefined);
  let isVisible = $state(false);
  let isMenuOpen = $state(false);

  let audioGainValue = $state(getDefaultAudioGain());
  let audioFilterFreq = $state(DEFAULT_AUDIO_FILTER_FREQ);
  let audioPlaybackSpeed = $state(DEFAULT_PLAYBACK_SPEED);

  let showConfirmModal = $state(false);
  let confirmModalConfig = $state({
    title: '',
    message: '',
    confirmLabel: 'Confirm',
    onConfirm: async () => {},
  });

  $effect(() => {
    if (isVisible) {
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

  function handleReview() {
    navigation.navigate(`/ui/detections/${detection.id}?tab=review`);
  }

  async function handleMarkCorrect() {
    if (await setDetectionVerification(detection.id, 'correct')) {
      onRefresh?.();
    }
  }

  async function handleMarkFalsePositive() {
    if (await setDetectionVerification(detection.id, 'false_positive')) {
      onRefresh?.();
    }
  }

  function handleToggleSpecies() {
    confirmModalConfig = {
      title: t('dashboard.recentDetections.modals.ignoreSpecies', {
        species: detection.commonName,
      }),
      message: t('dashboard.recentDetections.modals.ignoreSpeciesConfirm', {
        species: detection.commonName,
      }),
      confirmLabel: t('common.confirm'),
      onConfirm: async () => {
        try {
          await fetchWithCSRF('/api/v2/detections/ignore', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ common_name: detection.commonName }),
          });
          onRefresh?.();
        } catch {
          toastActions.error(t('dashboard.recentDetections.errors.toggleSpeciesFailed'));
        }
      },
    };
    showConfirmModal = true;
  }

  function handleToggleLock() {
    confirmModalConfig = {
      title: detection.locked
        ? t('dashboard.recentDetections.modals.unlockDetection')
        : t('dashboard.recentDetections.modals.lockDetection'),
      message: detection.locked
        ? t('dashboard.recentDetections.modals.unlockDetectionConfirm', {
            species: detection.commonName,
          })
        : t('dashboard.recentDetections.modals.lockDetectionConfirm', {
            species: detection.commonName,
          }),
      confirmLabel: t('common.confirm'),
      onConfirm: async () => {
        try {
          await fetchWithCSRF(`/api/v2/detections/${detection.id}/lock`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ locked: !detection.locked }),
          });
          onRefresh?.();
        } catch {
          toastActions.error(t('dashboard.recentDetections.errors.toggleLockFailed'));
        }
      },
    };
    showConfirmModal = true;
  }

  function handleDelete() {
    confirmModalConfig = {
      title: t('dashboard.recentDetections.modals.deleteDetection', {
        species: detection.commonName,
      }),
      message: t('dashboard.recentDetections.modals.deleteDetectionConfirm', {
        species: detection.commonName,
      }),
      confirmLabel: t('common.delete'),
      onConfirm: async () => {
        try {
          await fetchWithCSRF(`/api/v2/detections/${detection.id}`, { method: 'DELETE' });
          onRefresh?.();
        } catch {
          toastActions.error(t('dashboard.recentDetections.errors.deleteFailed'));
        }
      },
    };
    showConfirmModal = true;
  }

  function handleDownload() {
    const link = document.createElement('a');
    link.href = buildAppUrl(`/api/v2/audio/${detection.id}`);
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
  class={cn('detection-card group relative rounded-xl', isMenuOpen && 'z-[60]')}
>
  <!-- Inner container with overflow-hidden for spectrogram clipping -->
  <div class="detection-card-inner">
    <!-- Spectrogram Background -->
    <div class="spectrogram-container">
      {#if loader.showSpinner}
        <div class="spectrogram-loading">
          <span class="loading loading-spinner loading-md text-[var(--color-base-content)]/50"
          ></span>
          {#if loader.isQueued}
            <span class="text-xs text-[var(--color-base-content)]/40 mt-1">Waiting...</span>
          {:else if loader.isGenerating}
            <span class="text-xs text-[var(--color-base-content)]/40 mt-1">Generating...</span>
          {/if}
        </div>
      {/if}

      {#if loader.error}
        <div class="spectrogram-error">
          <span class="text-sm text-[var(--color-base-content)]/50">Spectrogram unavailable</span>
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
      {#if detection.weather?.moonPhaseName && detection.timeOfDay === 'night'}
        <MoonBadge moonPhaseName={detection.weather.moonPhaseName} />
      {/if}
      <SourceBadge {detection} variant="overlay" />
    </div>

    <!-- Center Play Button -->
    <PlayOverlay
      detectionId={detection.id}
      gainValue={audioGainValue}
      filterFreq={audioFilterFreq}
      playbackSpeed={audioPlaybackSpeed}
    />

    <!-- Bottom Species Info Bar: tappable for all auth levels to view details -->
    <button
      type="button"
      class="absolute inset-x-0 bottom-0 z-[11] text-left"
      onclick={() =>
        onDetailsClick
          ? onDetailsClick(detection.id)
          : navigation.navigate(`/ui/detections/${detection.id}`)}
      aria-label={t('detections.viewDetails', { species: detection.commonName })}
    >
      <SpeciesInfoBar {detection} />
    </button>
  </div>

  <!-- Top-Right Action Menu - OUTSIDE overflow-hidden container -->
  <div class="absolute top-2 right-2 z-50">
    <ActionMenu
      {detection}
      variant="overlay"
      onMarkCorrect={handleMarkCorrect}
      onMarkFalsePositive={handleMarkFalsePositive}
      onReview={handleReview}
      onToggleSpecies={handleToggleSpecies}
      onToggleLock={handleToggleLock}
      onDelete={handleDelete}
      onDownload={handleDownload}
      onMenuOpen={handleMenuOpen}
      onMenuClose={handleMenuClose}
    />
  </div>
</article>

<ConfirmModal
  isOpen={showConfirmModal}
  title={confirmModalConfig.title}
  message={confirmModalConfig.message}
  confirmLabel={confirmModalConfig.confirmLabel}
  onClose={() => (showConfirmModal = false)}
  onConfirm={async () => {
    await confirmModalConfig.onConfirm();
    showConfirmModal = false;
  }}
/>

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

  :global([data-theme='dark']) .spectrogram-loading,
  :global([data-theme='dark']) .spectrogram-error {
    background: linear-gradient(135deg, rgb(30 41 59 / 0.9) 0%, rgb(15 23 42 / 0.95) 100%);
  }
</style>
