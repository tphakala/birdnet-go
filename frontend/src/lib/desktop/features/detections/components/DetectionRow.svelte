<!--
  DetectionRow.svelte

  A comprehensive row component for displaying individual bird detection records with interactive features.
  Includes confidence indicators, status badges, weather information, and action controls.

  Usage:
  - Detection listings and tables
  - Search results display
  - Administrative detection management
  - Any context requiring detailed detection information

  Features:
  - Confidence circle visualization
  - Status badges (verified, false positive, etc.)
  - Weather condition display
  - Action menu wired to parent-owned handlers (review/lock/ignore/delete)
  - Thumbnail image support
  - Responsive design

  Props:
  - detection: Detection - The detection data object
  - isExcluded?: boolean - Whether this detection's species is excluded
  - onDetailsClick?: (id: number) => void - Handler for detail view
  - onReview / onMarkCorrect / onMarkFalsePositive / onToggleSpecies / onToggleLock / onDelete -
    action callbacks supplied by the parent (DetectionsList) via useDetectionActions
-->
<script lang="ts">
  import ConfidenceCircle from '$lib/desktop/components/data/ConfidenceCircle.svelte';
  import VerificationBadges from '$lib/desktop/components/ui/VerificationBadges.svelte';
  import WeatherMetrics from '$lib/desktop/components/data/WeatherMetrics.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SourceBadge from '$lib/desktop/features/dashboard/components/SourceBadge.svelte';
  import SpectrogramPlayer from '$lib/desktop/components/media/SpectrogramPlayer.svelte';
  import ActionMenu from '$lib/desktop/components/ui/ActionMenu.svelte';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils.js';
  import { t } from '$lib/i18n';
  import type { Detection } from '$lib/types/detection.types';
  import { useImageDelayedLoading } from '$lib/utils/delayedLoading.svelte.js';
  import { loggers } from '$lib/utils/logger';
  import { navigation } from '$lib/stores/navigation.svelte';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';

  const logger = loggers.ui;

  // Presentational row: the parent (DetectionsList) owns the action handlers
  // and the ConfirmModal via the shared useDetectionActions composable, and
  // passes them in as callbacks plus the server-hydrated isExcluded state.
  interface Props {
    detection: Detection;
    /**
     * Whether the Recording column exists in this table. The parent shows it when
     * audio export is enabled or any visible row has a clip. The cell content is
     * gated per-detection on detection.clipName, so rows without a clip render an
     * empty cell to keep the table columns aligned.
     */
    showRecordingColumn?: boolean;
    isExcluded?: boolean;
    onDetailsClick?: (_id: number) => void;
    selectionActive?: boolean;
    selected?: boolean;
    onToggleSelect?: (_id: string, _shiftKey: boolean) => void;
    onReview?: () => void;
    onMarkCorrect?: () => void;
    onMarkFalsePositive?: () => void;
    onToggleSpecies?: () => void;
    onToggleLock?: () => void;
    onDelete?: () => void;
  }

  let {
    detection,
    showRecordingColumn = true,
    isExcluded = false,
    onDetailsClick,
    selectionActive = false,
    selected = false,
    onToggleSelect,
    onReview,
    onMarkCorrect,
    onMarkFalsePositive,
    onToggleSpecies,
    onToggleLock,
    onDelete,
  }: Props = $props();

  // Localized common name for display in the visitor's UI locale. Falls back to
  // the server-provided common name, then the scientific name.
  const displayName = $derived(localizeSpeciesName(detection.scientificName, detection.commonName));

  // Thumbnail loading with delayed spinner and URL failure tracking
  const thumbnailLoader = useImageDelayedLoading({
    delayMs: 150,
    timeoutMs: 10000,
    onTimeout: () => {
      logger.warn('Thumbnail loading timeout', {
        scientificName: detection.scientificName,
        detectionId: detection.id,
      });
    },
  });

  function handleDetailsClick(e: Event) {
    e.preventDefault();
    if (onDetailsClick) {
      onDetailsClick(detection.id);
    } else {
      // Default navigation to detection detail page
      navigation.navigate(`/ui/detections/${detection.id}`);
    }
  }

  // Placeholder function for thumbnail URL. buildAppUrl prepends the
  // configured base path so the image resolves through reverse proxies.
  function getThumbnailUrl(scientificName: string): string {
    // TODO: Replace with actual thumbnail API endpoint
    return buildAppUrl(`/api/v2/media/species-image?name=${encodeURIComponent(scientificName)}`);
  }

  // Thumbnail loading handlers
  function handleThumbnailLoad() {
    thumbnailLoader.setLoading(false);
  }

  function handleThumbnailError() {
    const currentUrl = getThumbnailUrl(detection.scientificName);
    thumbnailLoader.markUrlFailed(currentUrl);
  }

  // Track previous URL to avoid unnecessary resets
  let previousThumbnailUrl: string | null = null;

  // Handle thumbnail loading state when detection changes
  $effect(() => {
    const currentUrl = getThumbnailUrl(detection.scientificName);
    // Only reset loading state if URL actually changed
    if (detection.scientificName && currentUrl !== previousThumbnailUrl) {
      previousThumbnailUrl = currentUrl;

      // Check if this URL has previously failed to prevent retry loops
      if (thumbnailLoader.hasUrlFailed(currentUrl)) {
        thumbnailLoader.setError();
        return; // Don't try to load known failed URLs
      }

      // Start loading when URL changes
      thumbnailLoader.setLoading(true);
    }
  });

  // Cleanup is handled automatically by useImageDelayedLoading
</script>

<!-- DetectionRow now returns table cells for proper table structure -->
{#if selectionActive}
  <td class="w-10 text-center" onclick={e => e.stopPropagation()}>
    <Checkbox
      checked={selected}
      size="sm"
      variant="primary"
      onchange={(_checked, event) =>
        onToggleSelect?.(String(detection.id), (event as MouseEvent).shiftKey ?? false)}
    />
  </td>
{/if}

<!-- Date & Time -->
<td class="text-sm">
  <span>{detection.date} {detection.time}</span>
</td>

<!-- Weather Column -->
<td class="text-sm hidden md:table-cell">
  {#if detection.weather}
    <div class="flex flex-col gap-1">
      <WeatherMetrics
        weatherIcon={detection.weather.weatherIcon}
        weatherDescription={detection.weather.description}
        temperature={detection.weather.temperature}
        windSpeed={detection.weather.windSpeed}
        windGust={detection.weather.windGust}
        units={detection.weather.units}
        size="md"
        className="ml-1"
      />
    </div>
  {:else}
    <div class="text-[var(--color-base-content)] opacity-50 text-xs">
      {t('detections.weather.noData')}
    </div>
  {/if}
</td>

<!-- Source -->
<td class="text-sm hidden lg:table-cell">
  <SourceBadge {detection} variant="inline" />
</td>

<!-- Bird species (with thumbnail) -->
<td class="text-sm">
  <div class="sp-species-container sp-layout-detections">
    <!-- Thumbnail -->
    <div class="sp-thumbnail-wrapper">
      <button class="sp-thumbnail-button" onclick={handleDetailsClick} tabindex="0">
        <!-- Screen reader announcement for loading state -->
        <span class="sr-only" role="status" aria-live="polite">
          {thumbnailLoader.loading
            ? t('detections.aria.thumbnailLoading', { species: displayName })
            : t('detections.aria.thumbnailLoaded', { species: displayName })}
        </span>

        <!-- Loading spinner overlay -->
        {#if thumbnailLoader.showSpinner}
          <div
            class="absolute inset-0 flex items-center justify-center bg-[var(--color-base-200)]/75 rounded-md"
          >
            <div class="loading loading-spinner loading-sm text-[var(--color-primary)]"></div>
          </div>
        {/if}

        {#if thumbnailLoader.error}
          <!-- Error placeholder -->
          <div
            class="absolute inset-0 flex items-center justify-center bg-[var(--color-base-200)] rounded-md"
          >
            <svg
              class="w-8 h-8 text-[var(--color-base-content)] opacity-30"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
              xmlns="http://www.w3.org/2000/svg"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M4 16l4.586-4.586a2 2 0 012.828 0L16 16m-2-2l1.586-1.586a2 2 0 012.828 0L20 14m-6-6h.01M6 20h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z"
              />
            </svg>
            <span class="sr-only">{t('detections.row.imageFailedToLoad')}</span>
          </div>
        {:else if !thumbnailLoader.hasUrlFailed(getThumbnailUrl(detection.scientificName))}
          <!-- Only render img element if URL hasn't failed before -->
          <img
            loading="lazy"
            decoding="async"
            fetchpriority="low"
            src={getThumbnailUrl(detection.scientificName)}
            alt={displayName}
            class="sp-thumbnail-image"
            class:opacity-0={thumbnailLoader.loading}
            onload={handleThumbnailLoad}
            onerror={e => {
              handleThumbnailError();
              handleBirdImageError(e);
            }}
          />
        {/if}
      </button>
    </div>

    <!-- Species Names -->
    <div class="sp-species-info-wrapper">
      <div class="sp-species-names">
        <button
          onclick={handleDetailsClick}
          class="sp-species-common-name hover:text-primary transition-colors cursor-pointer text-left"
        >
          {displayName}
        </button>
        <div class="sp-species-scientific-name">{detection.scientificName}</div>
      </div>
    </div>
  </div>
</td>

<!-- Confidence -->
<td class="text-sm">
  <ConfidenceCircle confidence={detection.confidence} size="md" />
</td>

<!-- Status -->
<td>
  <VerificationBadges {detection} />
</td>

<!-- Recording/Spectrogram column. The column is omitted entirely when no visible
     row has a clip and export is disabled; within a shown column, the player is
     rendered only for detections that actually have a clip. -->
{#if showRecordingColumn}
  <td class="hidden md:table-cell">
    {#if detection.clipName}
      <SpectrogramPlayer
        audioUrl={buildAppUrl(`/api/v2/audio/${detection.id}`)}
        detectionId={detection.id.toString()}
        spectrogramSize="md"
      />
    {/if}
  </td>
{/if}

<!-- Action Menu -->
<td onclick={e => e.stopPropagation()}>
  <ActionMenu
    {detection}
    {isExcluded}
    {onMarkCorrect}
    {onMarkFalsePositive}
    {onReview}
    {onToggleSpecies}
    {onToggleLock}
    {onDelete}
  />
</td>

<style>
  /* Thumbnail wrapper - responsive width */
  .sp-thumbnail-wrapper {
    flex: 0 0 30%; /* Reduced to give more space to names */
    min-width: 40px; /* Minimum size on very small screens */
    max-width: 80px; /* Maximum size on large screens */
  }

  /* Thumbnail button - maintains aspect ratio */
  .sp-thumbnail-button {
    display: block;
    width: 100%;
    aspect-ratio: 4/3; /* Consistent aspect ratio */
    position: relative;
    overflow: hidden;
    border-radius: 0.375rem;
    background-color: color-mix(in srgb, var(--color-base-200) 30%, transparent);
  }

  /* Thumbnail image */
  .sp-thumbnail-image {
    position: absolute;
    top: 0;
    left: 0;
    width: 100%;
    height: 100%;
    object-fit: contain;
  }
</style>
