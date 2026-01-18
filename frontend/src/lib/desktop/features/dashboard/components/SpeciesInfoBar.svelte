<!--
  SpeciesInfoBar.svelte

  Bottom info bar displaying species information on detection cards.
  Features species thumbnail, name, verification status, and time info.

  Props:
  - detection: Detection - The detection data to display
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import type { Detection } from '$lib/types/detection.types';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils.js';
  import { formatRelativeTime } from '$lib/utils/formatters';
  import { Check } from '@lucide/svelte';
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';

  interface Props {
    detection: Detection;
    className?: string;
  }

  let { detection, className = '' }: Props = $props();

  // Get detection datetime for relative time
  function getDetectionDateTime(date: string, time: string): Date {
    const parsed = new Date(`${date}T${time}`);
    return isNaN(parsed.getTime()) ? new Date() : parsed;
  }

  const detectionDateTime = $derived(getDetectionDateTime(detection.date, detection.time));
  const relativeTime = $derived(formatRelativeTime(detectionDateTime));

  // Check verification status (API returns verified directly on detection)
  const isVerified = $derived(detection.verified === 'correct');
  const isFalsePositive = $derived(detection.verified === 'false_positive');

  // Thumbnail URL
  const thumbnailUrl = $derived(
    `/api/v2/media/species-image?name=${encodeURIComponent(detection.scientificName)}`
  );
</script>

<div class={cn('species-info-bar', className)}>
  <!-- Species Thumbnail -->
  <div class="species-thumbnail">
    <img
      src={thumbnailUrl}
      alt={detection.commonName}
      class="thumbnail-image"
      loading="lazy"
      onerror={handleBirdImageError}
    />
  </div>

  <!-- Species Info (flex-1) -->
  <div class="species-details">
    <!-- Name row with verification badge -->
    <div class="species-name-row">
      <span class="species-name">{detection.commonName}</span>
      {#if isVerified}
        <span
          class="verified-badge"
          title={t('dashboard.recentDetections.status.verified')}
          aria-label={t('dashboard.recentDetections.status.verified')}
        >
          <Check class="size-3" />
        </span>
      {:else if isFalsePositive}
        <span
          class="false-positive-badge"
          role="status"
          aria-label={t('dashboard.recentDetections.status.false')}
          >{t('dashboard.recentDetections.status.false')}</span
        >
      {:else}
        <span
          class="unverified-badge"
          role="status"
          aria-label={t('dashboard.recentDetections.status.unverified')}
          >{t('dashboard.recentDetections.status.unverified')}</span
        >
      {/if}
    </div>

    <!-- Scientific name -->
    <div class="scientific-name">{detection.scientificName}</div>
  </div>

  <!-- Time Info (right-aligned) -->
  <div class="time-info">
    <span class="detection-time">{detection.time}</span>
    <span class="relative-time">{relativeTime}</span>
  </div>
</div>

<style>
  .species-info-bar {
    position: absolute;
    bottom: 0;
    left: 0;
    right: 0;
    display: flex;
    align-items: flex-end;
    gap: 0.75rem;
    padding: 0.75rem 1rem;
    z-index: 10;
  }

  /* Species thumbnail */
  .species-thumbnail {
    flex-shrink: 0;
    width: 3rem;
    height: 3rem;
    border-radius: 0.5rem;
    overflow: hidden;
    border: 2px solid rgb(51 65 85 / 0.8);
    background-color: rgb(30 41 59);
  }

  .thumbnail-image {
    width: 100%;
    height: 100%;
    object-fit: cover;
  }

  /* Species details */
  .species-details {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 0.125rem;
  }

  .species-name-row {
    display: flex;
    align-items: center;
    gap: 0.375rem;
  }

  .species-name {
    font-weight: 600;
    font-size: 0.9375rem;
    color: white;
    line-height: 1.3;
    text-shadow: 0 1px 2px rgb(0 0 0 / 0.5);
  }

  .verified-badge {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 1rem;
    height: 1rem;
    border-radius: 9999px;
    background-color: rgb(34 197 94);
    color: white;
  }

  .false-positive-badge {
    display: inline-flex;
    align-items: center;
    padding: 0 0.375rem;
    height: 1rem;
    border-radius: 9999px;
    background-color: rgb(239 68 68 / 0.9);
    color: white;
    font-size: 0.625rem;
    font-weight: 500;
  }

  .unverified-badge {
    display: inline-flex;
    align-items: center;
    padding: 0 0.375rem;
    height: 1rem;
    border-radius: 9999px;
    background-color: rgb(51 65 85 / 0.8);
    color: rgb(203 213 225);
    font-size: 0.625rem;
    font-weight: 500;
  }

  .scientific-name {
    font-size: 0.8125rem;
    font-style: italic;
    color: rgb(148 163 184);
    line-height: 1.3;
    text-shadow: 0 1px 2px rgb(0 0 0 / 0.5);
  }

  /* Time info */
  .time-info {
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    align-items: flex-end;
    gap: 0.125rem;
  }

  .detection-time {
    font-size: 0.875rem;
    font-weight: 500;
    font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
    color: white;
    text-shadow: 0 1px 2px rgb(0 0 0 / 0.5);
  }

  .relative-time {
    font-size: 0.75rem;
    color: rgb(148 163 184);
    text-shadow: 0 1px 2px rgb(0 0 0 / 0.5);
  }
</style>
