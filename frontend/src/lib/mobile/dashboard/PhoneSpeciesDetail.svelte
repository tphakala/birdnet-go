<!--
  PhoneSpeciesDetail.svelte - Expanded view of one species row in the phone summary table.

  Shows the day's hourly chart with an hour axis, peak / first / last heard, the novelty
  marker, and links to the day's detections, eBird and the species history.
-->
<script lang="ts">
  import { ExternalLink, X } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import type { DailySpeciesSummary } from '$lib/types/detection.types';
  import type { NoveltyCategory } from '$lib/desktop/features/dashboard/utils/noveltyCategory';
  import HourAxis from './HourAxis.svelte';
  import HourBars from './HourBars.svelte';
  import { formatClock, formatHour, peakHour } from './phoneSummary';

  interface Props {
    item: DailySpeciesSummary;
    displayName: string;
    novelty: NoveltyCategory | null;
    speciesUrl: string;
    maxHour: number;
    sunriseHour: number | null;
    sunsetHour: number | null;
    selectedDate: string;
    onCollapse: () => void;
  }

  let {
    item,
    displayName,
    novelty,
    speciesUrl,
    maxHour,
    sunriseHour,
    sunsetHour,
    selectedDate,
    onCollapse,
  }: Props = $props();

  const logger = loggers.ui;
  const CHART_HEIGHT_PX = 64;
  const EBIRD_SPECIES_URL = 'https://ebird.org/species/';

  const NOVELTY_LABEL_KEYS = {
    lifetime: 'dashboard.newSpeciesHighlights.categoryLifetime',
    year: 'dashboard.newSpeciesHighlights.categoryYear',
    season: 'dashboard.newSpeciesHighlights.categorySeason',
    infrequent: 'dashboard.newSpeciesHighlights.categoryInfrequent',
  } as const;

  // eslint-disable-next-line security/detect-object-injection -- category is a typed union literal
  const noveltyLabel = (category: NoveltyCategory) => t(NOVELTY_LABEL_KEYS[category]);

  let historyOpen = $state(false);

  // A failed chunk load (e.g. a stale bundle after an upgrade) closes the request so the
  // button works again; returns '' so it can be called from the template.
  function logLoadError(error: unknown): string {
    logger.error('Failed to load species history', error, { component: 'PhoneSpeciesDetail' });
    queueMicrotask(() => (historyOpen = false));
    return '';
  }

  const facts = $derived.by(() => {
    const peak = peakHour(item.hourly_counts, maxHour);
    const first = formatClock(item.first_heard);
    const last = formatClock(item.latest_heard);
    return [
      peak !== null && t('dashboard.dailySummary.phone.peakAt', { time: formatHour(peak) }),
      first && t('dashboard.dailySummary.phone.firstHeard', { time: first }),
      last && t('dashboard.dailySummary.phone.lastHeard', { time: last }),
    ].filter(Boolean);
  });
</script>

<div class="phone-detail" aria-label={displayName} role="group" tabindex="-1" data-species-detail>
  <div class="flex items-start gap-2">
    <div class="min-w-0 flex-1">
      <p class="font-medium leading-tight">{displayName}</p>
      <p class="text-xs italic text-[var(--color-base-content)]/60">{item.scientific_name}</p>
    </div>
    <button
      type="button"
      class="p-2 rounded hover:bg-[var(--color-base-content)]/10 transition-colors flex-shrink-0"
      aria-label={t('common.close')}
      onclick={onCollapse}
    >
      <X class="size-4 text-[var(--color-base-content)]/60" />
    </button>
  </div>

  <p class="mt-1 text-sm">
    {t('dashboard.newSpeciesHighlights.detections', { count: item.count })}
    {#if novelty}
      · <span class="font-medium">{noveltyLabel(novelty)}</span>
    {/if}
  </p>

  <div class="mt-2">
    <HourBars
      counts={item.hourly_counts}
      {maxHour}
      height={CHART_HEIGHT_PX}
      {sunriseHour}
      {sunsetHour}
    />
    <span class="text-[var(--color-base-content)]/60"><HourAxis {maxHour} /></span>
  </div>

  {#if facts.length > 0}
    <p class="mt-2 text-xs text-[var(--color-base-content)]/70">{facts.join(' · ')}</p>
  {/if}

  <div class="mt-3 flex flex-wrap gap-2">
    <a href={speciesUrl} class="btn btn-sm btn-primary">
      {t('dashboard.dailySummary.columns.detections')}
    </a>
    <button type="button" class="btn btn-sm btn-ghost" onclick={() => (historyOpen = true)}>
      {t('dashboard.dailySummary.phone.history')}
    </button>
    {#if item.species_code}
      <a
        href="{EBIRD_SPECIES_URL}{encodeURIComponent(item.species_code)}"
        class="btn btn-sm btn-ghost"
        target="_blank"
        rel="noopener noreferrer"
        aria-label={t('common.aria.visitEbirdLink')}
      >
        eBird <ExternalLink class="size-3" />
      </a>
    {/if}
  </div>
</div>

<!-- Loaded on first use: it pulls in the d3 chart code, which the table itself never needs. -->
{#if historyOpen}
  {#await import('$lib/desktop/features/analytics/components/SpeciesHistoryModal.svelte') then { default: SpeciesHistoryModal }}
    <SpeciesHistoryModal
      isOpen
      scientificName={item.scientific_name}
      {displayName}
      endDate={selectedDate}
      onClose={() => (historyOpen = false)}
    />
  {:catch error}
    {logLoadError(error)}
  {/await}
{/if}

<style>
  .phone-detail {
    padding: 0.75rem 0.5rem;
    border-radius: 0.5rem;
    background-color: var(--color-base-200);
  }
</style>
