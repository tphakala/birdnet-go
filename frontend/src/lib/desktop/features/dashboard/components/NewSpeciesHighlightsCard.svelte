<!--
NewSpeciesHighlightsCard.svelte - Highlights newly detected species for the selected day

Purpose:
- Surfaces species that triggered a "new species" indicator on the selected day,
  reusing the same novelty rules as the daily summary table:
    * new lifetime species (⭐)
    * new species this year (📅)
    * new species this season (🌿)
- Renders nothing when no qualifying species exist for the day, so the widget only
  appears when it has something meaningful to show.

Data source:
- Consumes the same `DailySpeciesSummary[]` already loaded by the dashboard page.
  No additional network request is made by this component.

Props:
- data: DailySpeciesSummary[] - Array of species detection summaries for the day
- selectedDate: string - Currently selected date in YYYY-MM-DD format (for detail links)
-->

<script lang="ts">
  import Card from '$lib/desktop/components/ui/Card.svelte';
  import { t } from '$lib/i18n';
  import type { DailySpeciesSummary } from '$lib/types/detection.types';
  import { buildSpeciesDetectionUrl } from '$lib/utils/detectionUrls';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { Star } from '@lucide/svelte';
  import BirdThumbnailPopup from './BirdThumbnailPopup.svelte';
  import ConfidenceBadge from './ConfidenceBadge.svelte';

  interface Props {
    /** Daily species summaries (already fetched by the dashboard). */
    data?: DailySpeciesSummary[];
    /** Selected date in YYYY-MM-DD format, used to build detail links. */
    selectedDate: string;
    /** Show thumbnails or rely on the colored placeholder (default: true). */
    showThumbnails?: boolean;
  }

  let { data = [], selectedDate, showThumbnails = true }: Props = $props();

  // Novelty category, mirroring the mutually-exclusive priority used by the
  // daily summary indicators: lifetime > year > season.
  type HighlightCategory = 'lifetime' | 'year' | 'season';

  interface Highlight {
    species: DailySpeciesSummary;
    category: HighlightCategory;
  }

  function resolveCategory(item: DailySpeciesSummary): HighlightCategory | null {
    if (item.is_new_species) return 'lifetime';
    if (item.is_new_this_year) return 'year';
    if (item.is_new_this_season) return 'season';
    return null;
  }

  // Order so that the most significant novelty appears first, then by detections.
  const categoryRank: Record<HighlightCategory, number> = {
    lifetime: 0,
    year: 1,
    season: 2,
  };

  const highlights = $derived.by<Highlight[]>(() => {
    const result: Highlight[] = [];
    for (const species of data) {
      const category = resolveCategory(species);
      if (category !== null) {
        result.push({ species, category });
      }
    }
    result.sort((a, b) => {
      const rankDiff = categoryRank[a.category] - categoryRank[b.category];
      if (rankDiff !== 0) return rankDiff;
      return b.species.count - a.species.count;
    });
    return result;
  });

  function categoryLabel(category: HighlightCategory, season?: string): string {
    switch (category) {
      case 'lifetime':
        return t('dashboard.newSpeciesHighlights.categoryLifetime');
      case 'year':
        return t('dashboard.newSpeciesHighlights.categoryYear');
      case 'season':
        return season
          ? t('dashboard.newSpeciesHighlights.categorySeasonNamed', { season })
          : t('dashboard.newSpeciesHighlights.categorySeason');
    }
  }

  // Theme color variable per category (matches the daily summary indicators).
  function categoryColorVar(category: HighlightCategory): string {
    switch (category) {
      case 'lifetime':
        return 'var(--color-warning)';
      case 'year':
        return 'var(--color-info)';
      case 'season':
        return 'var(--color-success)';
    }
  }

  function daysSinceFirstSeen(item: DailySpeciesSummary): number | undefined {
    return item.is_new_species ? item.days_since_first_seen : undefined;
  }

  // first_heard / latest_heard are local time-of-day strings (HH:MM:SS) for the day.
  function formatTime(value: string | undefined): string {
    if (!value) return '';
    // Trim seconds for a cleaner display while tolerating HH:MM input.
    const parts = value.split(':');
    if (parts.length >= 2) return `${parts[0]}:${parts[1]}`;
    return value;
  }

  function speciesUrl(item: DailySpeciesSummary): string {
    return buildSpeciesDetectionUrl(item.scientific_name, selectedDate);
  }

  function thumbnailUrl(item: DailySpeciesSummary): string {
    return item.thumbnail_url
      ? buildAppUrl(item.thumbnail_url)
      : buildAppUrl(`/api/v2/media/species-image?name=${encodeURIComponent(item.scientific_name)}`);
  }
</script>

{#if highlights.length > 0}
  <Card padding={false}>
    {#snippet header()}
      <div class="flex items-center gap-2 px-6 py-4">
        <Star class="size-4 fill-current text-[var(--color-warning)]" />
        <h2 class="text-lg font-semibold">
          {t('dashboard.newSpeciesHighlights.title')}
        </h2>
        <span
          class="ml-1 inline-flex items-center justify-center rounded-full bg-[var(--color-base-200)] px-2 text-xs font-medium text-[var(--color-base-content)]/70"
        >
          {highlights.length}
        </span>
      </div>
    {/snippet}

    <div class="grid grid-cols-1 gap-4 px-6 pb-6 sm:grid-cols-2 lg:grid-cols-3">
      {#each highlights as { species, category } (species.scientific_name)}
        {@const days = daysSinceFirstSeen(species)}
        <div
          class="flex flex-col gap-3 rounded-lg border border-[var(--color-base-200)] bg-[var(--color-base-100)] p-4 shadow-sm transition-shadow hover:shadow-md"
        >
          <!-- Category badge -->
          <div class="flex items-center justify-between">
            <span
              class="inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-semibold"
              style:color={categoryColorVar(category)}
              style:background-color="color-mix(in srgb, {categoryColorVar(category)} 12%, transparent)"
            >
              <span aria-hidden="true">
                {#if category === 'lifetime'}⭐{:else if category === 'year'}📅{:else}🌿{/if}
              </span>
              {categoryLabel(category, species.current_season)}
            </span>
            {#if species.max_confidence !== undefined && species.max_confidence > 0}
              <ConfidenceBadge confidence={species.max_confidence} />
            {/if}
          </div>

          <!-- Species identity -->
          <div class="flex items-center gap-3">
            {#if showThumbnails}
              <BirdThumbnailPopup
                thumbnailUrl={thumbnailUrl(species)}
                commonName={species.common_name}
                scientificName={species.scientific_name}
                detectionUrl={speciesUrl(species)}
              />
            {/if}
            <div class="min-w-0">
              <a
                href={speciesUrl(species)}
                class="block truncate font-medium leading-tight hover:text-[var(--color-primary)]"
                title={species.common_name}
              >
                {species.common_name}
              </a>
              <span
                class="block truncate text-xs italic text-[var(--color-base-content)]/60"
                title={species.scientific_name}
              >
                {species.scientific_name}
              </span>
            </div>
          </div>

          <!-- Stats -->
          <dl class="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
            <div>
              <dt class="text-xs text-[var(--color-base-content)]/60">
                {t('dashboard.newSpeciesHighlights.detectionsToday')}
              </dt>
              <dd class="font-semibold">{species.count}</dd>
            </div>
            {#if species.max_confidence !== undefined && species.max_confidence > 0}
              <div>
                <dt class="text-xs text-[var(--color-base-content)]/60">
                  {t('dashboard.newSpeciesHighlights.maxConfidence')}
                </dt>
                <dd class="font-semibold">{Math.round(species.max_confidence * 100)}%</dd>
              </div>
            {/if}
            {#if species.latest_heard}
              <div>
                <dt class="text-xs text-[var(--color-base-content)]/60">
                  {t('dashboard.newSpeciesHighlights.lastDetection')}
                </dt>
                <dd class="font-semibold">{formatTime(species.latest_heard)}</dd>
              </div>
            {/if}
            {#if days !== undefined}
              <div>
                <dt class="text-xs text-[var(--color-base-content)]/60">
                  {t('dashboard.newSpeciesHighlights.firstSeen')}
                </dt>
                <dd class="font-semibold">
                  {t('dashboard.newSpeciesHighlights.daysAgo', { days })}
                </dd>
              </div>
            {/if}
          </dl>
        </div>
      {/each}
    </div>
  </Card>
{/if}
