<script lang="ts">
  import { untrack } from 'svelte';
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import ConfidenceCircle from '$lib/desktop/components/data/ConfidenceCircle.svelte';
  import StatusBadges from '$lib/desktop/components/data/StatusBadges.svelte';
  import WeatherIcon from '$lib/desktop/components/data/WeatherIcon.svelte';
  import ActionMenu from '$lib/desktop/components/ui/ActionMenu.svelte';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';
  import { fetchWithCSRF } from '$lib/utils/api';
  import type { Detection } from '$lib/types/detection.types';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils.js';
  import { RefreshCw, XCircle } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { cn } from '$lib/utils/cn';
  import { formatRelativeTime } from '$lib/utils/formatters';

  const logger = loggers.ui;

  /**
   * Converts detection date and time strings to a Date object
   * @param date - Date string in YYYY-MM-DD format
   * @param time - Time string in HH:MM:SS format
   * @returns Date object
   */
  function getDetectionDateTime(date: string, time: string): Date {
    return new Date(`${date}T${time}`);
  }

  /**
   * Formats temperature with appropriate unit symbol
   * @param temp - Temperature value
   * @param units - Unit system ('metric', 'imperial', 'standard')
   * @returns Formatted temperature string
   */
  function formatTemperature(temp: number | undefined, units: string | undefined): string {
    if (temp === undefined) return '';
    const rounded = Math.round(temp);
    if (units === 'imperial') return `${rounded}°F`;
    return `${rounded}°C`;
  }

  // Animation control - set to false to disable all animations
  const ENABLE_NEW_DETECTION_ANIMATIONS = false;

  interface Props {
    data: Detection[];
    loading?: boolean;
    error?: string | null;
    onRowClick?: (_detection: Detection) => void;
    onRefresh: () => void;
    limit?: number;
    // eslint-disable-next-line no-unused-vars
    onLimitChange?: (limit: number) => void;
    newDetectionIds?: Set<number>;
    detectionArrivalTimes?: Map<number, number>;
    onFreezeStart?: () => void;
    onFreezeEnd?: () => void;
    updatesAreFrozen?: boolean;
    className?: string;
  }

  let {
    data = [],
    loading = false,
    error = null,
    onRowClick,
    onRefresh,
    limit = 5,
    onLimitChange,
    newDetectionIds = new Set(),
    // eslint-disable-next-line no-unused-vars
    detectionArrivalTimes: _detectionArrivalTimes = new Map(), // Reserved for future staggered animations
    onFreezeStart,
    onFreezeEnd,
    updatesAreFrozen = false,
    className = '',
  }: Props = $props();

  // State for number of detections to show - captures initial prop value without creating dependency
  // Uses untrack() to explicitly capture initial value only (local state is independent after init)
  let selectedLimit = $state(untrack(() => limit));

  // Updates the number of detections to display and persists the preference
  function handleLimitChange(newLimit: number) {
    selectedLimit = newLimit;

    // Save to localStorage
    if (typeof window !== 'undefined') {
      try {
        localStorage.setItem('recentDetectionLimit', newLimit.toString());
      } catch (e) {
        logger.error('Failed to save detection limit:', e);
      }
    }

    // Call parent callback
    if (onLimitChange) {
      onLimitChange(newLimit);
    }
  }

  // Modal state for expanded audio player (removed - not currently used)

  // Handles clicking on a detection row to trigger parent callback
  function handleRowClick(detection: Detection) {
    if (onRowClick) {
      onRowClick(detection);
    }
  }

  // Modal states
  let showConfirmModal = $state(false);
  let selectedDetection = $state<Detection | null>(null);
  let confirmModalConfig = $state({
    title: '',
    message: '',
    confirmLabel: 'Confirm',
    onConfirm: async () => {},
  });

  // Action handlers
  // Navigate to detection detail page for review
  function handleReview(detection: Detection) {
    window.location.href = `/ui/detections/${detection.id}?tab=review`;
  }

  // Toggles whether a species should be ignored in future detections
  function handleToggleSpecies(detection: Detection) {
    const isExcluded = false; // TODO: determine if species is excluded
    confirmModalConfig = {
      title: isExcluded
        ? t('dashboard.recentDetections.modals.showSpecies', { species: detection.commonName })
        : t('dashboard.recentDetections.modals.ignoreSpecies', { species: detection.commonName }),
      message: isExcluded
        ? t('dashboard.recentDetections.modals.showSpeciesConfirm', {
            species: detection.commonName,
          })
        : t('dashboard.recentDetections.modals.ignoreSpeciesConfirm', {
            species: detection.commonName,
          }),
      confirmLabel: t('common.buttons.confirm'),
      onConfirm: async () => {
        try {
          await fetchWithCSRF('/api/v2/detections/ignore', {
            method: 'POST',
            headers: {
              'Content-Type': 'application/json',
            },
            body: JSON.stringify({
              common_name: detection.commonName,
            }),
          });
          onRefresh();
        } catch (error) {
          logger.error('Error toggling species exclusion:', error);
        }
      },
    };
    selectedDetection = detection;
    showConfirmModal = true;
  }

  // Toggles the lock status of a detection to prevent/allow automatic cleanup
  function handleToggleLock(detection: Detection) {
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
      confirmLabel: t('common.buttons.confirm'),
      onConfirm: async () => {
        try {
          await fetchWithCSRF(`/api/v2/detections/${detection.id}/lock`, {
            method: 'POST',
            headers: {
              'Content-Type': 'application/json',
            },
            body: JSON.stringify({
              locked: !detection.locked,
            }),
          });
          onRefresh();
        } catch (error) {
          logger.error('Error toggling lock status:', error);
        }
      },
    };
    selectedDetection = detection;
    showConfirmModal = true;
  }

  // Permanently deletes a detection after user confirmation
  function handleDelete(detection: Detection) {
    confirmModalConfig = {
      title: t('dashboard.recentDetections.modals.deleteDetection', {
        species: detection.commonName,
      }),
      message: t('dashboard.recentDetections.modals.deleteDetectionConfirm', {
        species: detection.commonName,
      }),
      confirmLabel: t('common.buttons.delete'),
      onConfirm: async () => {
        try {
          await fetchWithCSRF(`/api/v2/detections/${detection.id}`, {
            method: 'DELETE',
          });
          onRefresh();
        } catch (error) {
          logger.error('Error deleting detection:', error);
        }
      },
    };
    selectedDetection = detection;
    showConfirmModal = true;
  }
</script>

<section
  class={cn(
    'card col-span-12 bg-base-100 shadow-sm rounded-2xl border border-border-100 overflow-hidden',
    className
  )}
>
  <!-- Card Header -->
  <div class="px-6 py-4 border-b border-base-200">
    <div class="flex items-center justify-between">
      <div class="flex flex-col">
        <h3 class="font-semibold">{t('dashboard.recentDetections.title')}</h3>
        <p class="text-sm" style:color="#94a3b8">{t('dashboard.recentDetections.subtitle')}</p>
      </div>
      <div class="flex items-center gap-2">
        <label for="numDetections" class="label-text text-sm"
          >{t('dashboard.recentDetections.controls.show')}</label
        >
        <select
          id="numDetections"
          bind:value={selectedLimit}
          onchange={e => handleLimitChange(parseInt(e.currentTarget.value, 10))}
          class="select select-sm focus-visible:outline-hidden"
        >
          <option value={5}>5</option>
          <option value={10}>10</option>
          <option value={25}>25</option>
          <option value={50}>50</option>
        </select>
        <button
          onclick={onRefresh}
          class="btn btn-sm btn-ghost"
          class:opacity-50={updatesAreFrozen}
          disabled={loading || updatesAreFrozen}
          title={updatesAreFrozen
            ? 'Refresh paused while interaction is active'
            : t('dashboard.recentDetections.controls.refresh')}
          aria-label={t('dashboard.recentDetections.controls.refresh')}
        >
          <RefreshCw class={loading ? 'size-4 animate-spin' : 'size-4'} />
        </button>
      </div>
    </div>
  </div>

  <!-- Content -->
  <div class="p-6">
    {#if error}
      <div class="alert alert-error">
        <XCircle class="size-6" />
        <span>{error}</span>
      </div>
    {:else}
      <!-- Desktop Layout -->
      <div class="relative">
        <!-- Loading overlay -->
        {#if loading}
          <div
            class="absolute inset-0 bg-base-100/80 z-20 flex items-center justify-center rounded-lg pointer-events-none"
          >
            <span class="loading loading-spinner loading-md"></span>
          </div>
          <!-- Disable pointer events on content during loading -->
          <div class="absolute inset-0 z-10" style:pointer-events="auto" style:cursor="wait"></div>
        {/if}

        <!-- Header Row -->
        <div class="rd-header">
          <div class="rd-header-species">{t('dashboard.recentDetections.headers.species')}</div>
          <div class="rd-header-time">{t('dashboard.recentDetections.headers.time')}</div>
          <div class="rd-header-confidence">
            {t('dashboard.recentDetections.headers.confidence')}
          </div>
          <div class="rd-header-weather">{t('dashboard.recentDetections.headers.weather')}</div>
          <div class="rd-header-recording">{t('dashboard.recentDetections.headers.recording')}</div>
          <div class="rd-header-actions">{t('dashboard.recentDetections.headers.actions')}</div>
        </div>

        <!-- Detection Rows -->
        <div>
          {#each data.slice(0, selectedLimit) as detection (detection.id)}
            {@const isNew = ENABLE_NEW_DETECTION_ANIMATIONS && newDetectionIds.has(detection.id)}
            {@const detectionDateTime = getDetectionDateTime(detection.date, detection.time)}
            <div
              class="rd-row detection-row border-b border-base-200 last:border-b-0"
              class:cursor-pointer={onRowClick}
              class:new-detection={isNew}
              role="button"
              tabindex="0"
              onclick={() => handleRowClick(detection)}
              onkeydown={e =>
                e.key === 'Enter' || e.key === ' ' ? handleRowClick(detection) : null}
            >
              <!-- Species Column: Thumbnail + Name + Status -->
              <div class="rd-species-cell">
                <!-- Thumbnail -->
                <div class="rd-thumbnail-wrapper">
                  <button
                    class="rd-thumbnail-button"
                    onclick={() => handleRowClick(detection)}
                    tabindex="-1"
                  >
                    <div class="rd-thumbnail-placeholder"></div>
                    <img
                      src="/api/v2/media/species-image?name={encodeURIComponent(
                        detection.scientificName
                      )}"
                      alt={detection.commonName}
                      class="rd-thumbnail-image"
                      onerror={handleBirdImageError}
                      loading="lazy"
                    />
                  </button>
                </div>

                <!-- Species Info -->
                <div class="rd-species-info">
                  <!-- Common Name + Status Badges (inline) -->
                  <div class="rd-species-name-row">
                    <span class="rd-common-name">{detection.commonName}</span>
                    <StatusBadges {detection} size="sm" className="rd-status-badges" />
                  </div>
                  <!-- Scientific Name -->
                  <div class="rd-scientific-name">{detection.scientificName}</div>
                </div>
              </div>

              <!-- Time Column -->
              <div class="rd-time-cell">
                <span class="rd-time">{detection.time}</span>
                <span class="rd-relative-time">{formatRelativeTime(detectionDateTime)}</span>
              </div>

              <!-- Confidence -->
              <div class="rd-confidence-cell">
                <ConfidenceCircle confidence={detection.confidence} size="md" />
              </div>

              <!-- Weather -->
              <div class="rd-weather-cell">
                {#if detection.weather?.weatherIcon}
                  <div class="rd-weather-content">
                    <WeatherIcon
                      weatherIcon={detection.weather.weatherIcon}
                      timeOfDay={detection.timeOfDay}
                      size="md"
                    />
                    {#if detection.weather.temperature !== undefined}
                      <span class="rd-weather-temp">
                        {formatTemperature(detection.weather.temperature, detection.weather.units)}
                      </span>
                    {/if}
                  </div>
                {:else}
                  <span class="rd-weather-nodata">—</span>
                {/if}
              </div>

              <!-- Recording -->
              <div class="rd-recording-cell" onclick={e => e.stopPropagation()} role="presentation">
                <div class="rd-audio-player-container">
                  <AudioPlayer
                    audioUrl="/api/v2/audio/{detection.id}"
                    detectionId={detection.id.toString()}
                    showSpectrogram={true}
                    spectrogramSize="sm"
                    spectrogramRaw={true}
                    responsive={true}
                    className="w-full"
                    onPlayStart={onFreezeStart}
                    onPlayEnd={onFreezeEnd}
                    debug={false}
                  />
                </div>
              </div>

              <!-- Action Menu -->
              <div class="rd-actions-cell" onclick={e => e.stopPropagation()} role="presentation">
                <ActionMenu
                  {detection}
                  isExcluded={false}
                  onReview={() => handleReview(detection)}
                  onToggleSpecies={() => handleToggleSpecies(detection)}
                  onToggleLock={() => handleToggleLock(detection)}
                  onDelete={() => handleDelete(detection)}
                  onMenuOpen={onFreezeStart}
                  onMenuClose={onFreezeEnd}
                />
              </div>
            </div>
          {/each}
        </div>
      </div>

      {#if data.length === 0}
        <div
          class="text-center py-8"
          style:color="color-mix(in srgb, var(--color-base-content) 60%, transparent)"
        >
          {t('dashboard.recentDetections.noDetections')}
        </div>
      {/if}
    {/if}
  </div>
</section>

<!-- Modals -->
{#if selectedDetection}
  <ConfirmModal
    isOpen={showConfirmModal}
    title={confirmModalConfig.title}
    message={confirmModalConfig.message}
    confirmLabel={confirmModalConfig.confirmLabel}
    onClose={() => {
      showConfirmModal = false;
      selectedDetection = null;
    }}
    onConfirm={async () => {
      await confirmModalConfig.onConfirm();
      showConfirmModal = false;
      selectedDetection = null;
    }}
  />
{/if}

<style>
  /* ========================================================================
     Recent Detections Card - CSS Grid Layout (6 columns)
     ======================================================================== */

  /* Header row */
  .rd-header {
    display: grid;
    grid-template-columns: minmax(200px, 1fr) 100px 70px 70px minmax(160px, 1fr) 50px;
    gap: 1rem;
    align-items: center;
    padding: 0.5rem 0;
    font-size: 0.75rem;
    font-weight: 600;
    color: color-mix(in srgb, var(--color-base-content) 60%, transparent);
    border-bottom: 1px solid var(--color-base-200);
  }

  .rd-header-time {
    text-align: left;
  }

  .rd-header-confidence {
    text-align: center;
  }

  .rd-header-weather {
    text-align: center;
  }

  .rd-header-recording {
    text-align: center;
  }

  .rd-header-actions {
    text-align: center;
  }

  /* Detection row - CSS Grid layout */
  .rd-row {
    display: grid;
    grid-template-columns: minmax(200px, 1fr) 100px 70px 70px minmax(160px, 1fr) 50px;
    gap: 1rem;
    align-items: center;
    padding: 0.75rem 0;
  }

  /* ========================================================================
     Species Cell - Contains thumbnail, name, status badges, and time
     ======================================================================== */

  .rd-species-cell {
    display: flex;
    align-items: flex-start;
    gap: 0.75rem;
    min-width: 0;
  }

  /* Thumbnail wrapper */
  .rd-thumbnail-wrapper {
    flex-shrink: 0;
    width: 80px;
  }

  .rd-thumbnail-button {
    display: block;
    width: 100%;
    aspect-ratio: 4/3;
    position: relative;
    overflow: hidden;
    border-radius: 0.5rem;
    background-color: oklch(var(--b2) / 0.3);
  }

  .rd-thumbnail-placeholder {
    position: absolute;
    inset: 0;
    background: linear-gradient(
      90deg,
      oklch(var(--b2) / 0.5) 0%,
      oklch(var(--b2) / 0.3) 50%,
      oklch(var(--b2) / 0.5) 100%
    );
    background-size: 200% 100%;
    animation: shimmer 1.5s infinite;
  }

  .rd-thumbnail-image {
    position: absolute;
    inset: 0;
    width: 100%;
    height: 100%;
    object-fit: contain;
    z-index: 1;
    background-color: oklch(var(--b1));
  }

  /* Species info container */
  .rd-species-info {
    display: flex;
    flex-direction: column;
    gap: 0.125rem;
    min-width: 0; /* Allow text truncation */
    flex: 1;
  }

  /* Common name + status badges row */
  .rd-species-name-row {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    flex-wrap: wrap;
  }

  .rd-common-name {
    font-weight: 600;
    font-size: 0.9375rem;
    line-height: 1.3;
    color: var(--color-base-content);
  }

  /* Status badges inline - allow wrapping on small screens */
  .rd-species-name-row :global(.rd-status-badges) {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem;
  }

  /* Scientific name */
  .rd-scientific-name {
    font-size: 0.8125rem;
    font-style: italic;
    color: color-mix(in srgb, var(--color-base-content) 60%, transparent);
    line-height: 1.3;
  }

  /* ========================================================================
     Time Cell
     ======================================================================== */

  .rd-time-cell {
    display: flex;
    flex-direction: column;
    gap: 0.125rem;
  }

  .rd-time {
    font-size: 0.8125rem;
    font-weight: 500;
    color: var(--color-base-content);
  }

  .rd-relative-time {
    font-size: 0.75rem;
    color: color-mix(in srgb, var(--color-base-content) 50%, transparent);
  }

  /* ========================================================================
     Other Cells
     ======================================================================== */

  .rd-confidence-cell {
    display: flex;
    justify-content: center;
  }

  .rd-weather-cell {
    display: flex;
    justify-content: center;
    align-items: center;
  }

  .rd-weather-content {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.125rem;
  }

  .rd-weather-temp {
    font-size: 0.75rem;
    font-weight: 500;
    color: color-mix(in srgb, var(--color-base-content) 70%, transparent);
  }

  .rd-weather-nodata {
    color: color-mix(in srgb, var(--color-base-content) 30%, transparent);
    font-size: 0.875rem;
  }

  .rd-actions-cell {
    display: flex;
    justify-content: center;
  }

  .rd-recording-cell {
    display: flex;
    justify-content: center;
  }

  /* ========================================================================
     Audio Player Container
     ======================================================================== */

  .rd-audio-player-container {
    position: relative;
    width: 100%;
    max-width: 160px; /* 80px height * 2:1 aspect ratio */
    max-height: 80px;
    aspect-ratio: 2 / 1;
    background: linear-gradient(to bottom, rgb(128 128 128 / 0.1), rgb(128 128 128 / 0.05));
    border-radius: 0.5rem;
    overflow: hidden;
  }

  .rd-audio-player-container :global(.group) {
    width: 100%;
    height: 100%;
    position: absolute;
    top: 0;
    left: 0;
  }

  .rd-audio-player-container > :global(div > .group) {
    width: 100%;
    height: 100%;
    position: absolute;
    top: 0;
    left: 0;
  }

  .rd-audio-player-container :global(img) {
    object-fit: cover;
    height: 100%;
    width: 100%;
    animation: fadeIn 0.3s ease-out;
  }

  /* ========================================================================
     Animations
     ======================================================================== */

  @keyframes shimmer {
    0% {
      background-position: 200% 0;
    }

    100% {
      background-position: -200% 0;
    }
  }

  @keyframes fadeIn {
    from {
      opacity: 0;
    }

    to {
      opacity: 1;
    }
  }

  /* Detection row hover */
  .detection-row {
    transition: background-color 0.15s ease-in-out;
  }

  .detection-row:hover {
    background-color: oklch(var(--b2) / 0.5);
  }

  /* New detection animation */
  .new-detection {
    animation: subtleHighlight 2s ease-out both;
    position: relative;
    z-index: 1;
  }

  @keyframes subtleHighlight {
    0% {
      opacity: 0.95;
      background-color: oklch(var(--p) / 0.03);
      border-left: 2px solid oklch(var(--p) / 0.15);
    }

    100% {
      opacity: 1;
      background-color: transparent;
      border-left: 2px solid transparent;
    }
  }

  /* ========================================================================
     Responsive Adjustments
     ======================================================================== */

  /* Tablet and smaller */
  @media (max-width: 1024px) {
    .rd-header,
    .rd-row {
      grid-template-columns: minmax(180px, 1fr) 80px 60px 60px minmax(120px, 1fr) 50px;
      gap: 0.75rem;
    }

    .rd-thumbnail-wrapper {
      width: 70px;
    }

    .rd-weather-temp {
      font-size: 0.6875rem;
    }
  }

  /* Mobile */
  @media (max-width: 768px) {
    .rd-header,
    .rd-row {
      grid-template-columns: 1fr 70px 50px;
      gap: 0.75rem;
    }

    /* Hide weather on mobile */
    .rd-weather-cell,
    .rd-header-weather {
      display: none;
    }

    /* Hide recording on mobile */
    .rd-recording-cell,
    .rd-header-recording {
      display: none;
    }

    .rd-thumbnail-wrapper {
      width: 60px;
    }

    .rd-common-name {
      font-size: 0.875rem;
    }

    .rd-scientific-name {
      font-size: 0.75rem;
    }
  }

  /* Reduced motion */
  @media (prefers-reduced-motion: reduce) {
    .rd-thumbnail-placeholder,
    .rd-audio-player-container:not(:has(img)) {
      animation: none;
      background: oklch(var(--b2) / 0.4);
    }

    .new-detection {
      animation: none;
    }
  }
</style>
