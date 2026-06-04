<!--
NewSpeciesHighlightsCard.svelte - Highlights newly detected / returning species for the day

Renders nothing when no qualifying species exist for the day.
Data source: same DailySpeciesSummary[] already loaded by the dashboard page.
Shows up to 12 species, ordered by novelty category then detection count.
-->

<script lang="ts">
  import Card from '$lib/desktop/components/ui/Card.svelte';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils';
  import { t } from '$lib/i18n';
  import type { DailySpeciesSummary } from '$lib/types/detection.types';
  import { buildSpeciesDetectionUrl } from '$lib/utils/detectionUrls';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { speciesTrackingSettings } from '$lib/stores/settings';
  import { AudioLines, CalendarDays, Leaf, Star } from '@lucide/svelte';

  interface Props {
    data?: DailySpeciesSummary[];
    selectedDate: string;
    /** Daily-activity thumbnail setting; the picture is shown only when enabled. */
    showThumbnails?: boolean;
    /**
     * Server-timezone-aware flag for "the selected date is today". The absence
     * gap reflects live tracker state, so the last-seen stat is only shown today.
     */
    isToday?: boolean;
  }

  let { data = [], selectedDate, showThumbnails = true, isToday = false }: Props = $props();

  // Maximum number of species shown.
  const MAX_VISIBLE = 12;

  // The widget relies on the species tracker; warn when it is turned off.
  const trackingDisabled = $derived($speciesTrackingSettings?.enabled === false);

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

  const categoryRank: Record<HighlightCategory, number> = { lifetime: 0, year: 1, season: 2 };

  const highlights = $derived.by<Highlight[]>(() => {
    const result: Highlight[] = [];
    // Guard against a null payload: the default [] only applies for undefined,
    // and the daily-summary endpoint can return a null body.
    if (!data) return result;
    for (const species of data) {
      const category = resolveCategory(species);
      if (category !== null) result.push({ species, category });
    }
    result.sort((a, b) => {
      const rankDiff = categoryRank[a.category] - categoryRank[b.category];
      return rankDiff !== 0 ? rankDiff : b.species.count - a.species.count;
    });
    return result;
  });

  const visibleHighlights = $derived(highlights.slice(0, MAX_VISIBLE));

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

  // Left-border accent color per novelty category (matches daily summary indicators).
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

  // Confidence pill colors, mirroring ConfidenceBadge thresholds for consistency.
  function confidenceClasses(percent: number): string {
    if (percent >= 90) return 'bg-[var(--color-success)] text-[var(--color-success-content)]';
    if (percent >= 70)
      return 'bg-[color-mix(in_srgb,var(--color-success)_80%,var(--color-warning))] text-white';
    if (percent >= 50) return 'bg-[var(--color-warning)] text-[var(--color-warning-content)]';
    if (percent >= 30)
      return 'bg-[color-mix(in_srgb,var(--color-warning)_60%,var(--color-error))] text-white';
    return 'bg-[var(--color-error)] text-[var(--color-error-content)]';
  }

  function confidencePercent(item: DailySpeciesSummary): number | undefined {
    if (item.max_confidence === undefined || item.max_confidence <= 0) return undefined;
    return Math.round(item.max_confidence * 100);
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

{#snippet categoryIcon(category: HighlightCategory, season: string | undefined)}
  <span
    class="shrink-0"
    style:color={categoryColorVar(category)}
    title={categoryLabel(category, season)}
    aria-label={categoryLabel(category, season)}
  >
    {#if category === 'lifetime'}
      <Star class="size-3.5 fill-current" />
    {:else if category === 'year'}
      <CalendarDays class="size-3.5" />
    {:else}
      <Leaf class="size-3.5" />
    {/if}
  </span>
{/snippet}

{#snippet cardHeader()}
  <div class="flex flex-col gap-0.5">
    <h3 class="font-semibold">{t('dashboard.newSpeciesHighlights.title')}</h3>
    <p class="text-sm text-[var(--color-base-content)]/60">
      {t('dashboard.newSpeciesHighlights.subtitle')}
    </p>
  </div>
{/snippet}

{#if trackingDisabled}
  <Card padding={false} header={cardHeader}>
    <p class="px-4 pb-4 text-sm text-[var(--color-base-content)]/70">
      {t('dashboard.newSpeciesHighlights.trackingDisabled')}
    </p>
  </Card>
{:else if highlights.length > 0}
  <Card padding={false} header={cardHeader}>
    <div class="grid grid-cols-1 gap-2 px-4 pb-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
      {#each visibleHighlights as { species, category } (species.scientific_name)}
        {@const percent = confidencePercent(species)}
        <a
          href={speciesUrl(species)}
          class="group flex items-center gap-2.5 rounded-lg border border-[var(--color-base-200)] bg-[var(--color-base-100)] p-2.5 shadow-sm transition-shadow hover:shadow-md"
          style:border-left-width="3px"
          style:border-left-color={categoryColorVar(category)}
          title={categoryLabel(category, species.current_season)}
        >
          {#if showThumbnails}
            <img
              src={thumbnailUrl(species)}
              alt={species.common_name}
              loading="lazy"
              onerror={handleBirdImageError}
              class="size-10 shrink-0 rounded-md object-cover"
            />
          {/if}

          <div class="min-w-0 flex-1">
            <!-- Common name (with novelty icon) + confidence pill -->
            <div class="flex items-center justify-between gap-1.5">
              <span class="flex min-w-0 items-center gap-1">
                <span
                  class="truncate text-sm font-medium leading-tight group-hover:text-[var(--color-primary)]"
                >
                  {species.common_name}
                </span>
                {@render categoryIcon(category, species.current_season)}
              </span>
              {#if percent !== undefined}
                <span
                  class="shrink-0 rounded-full px-1.5 py-0.5 text-xs font-semibold {confidenceClasses(
                    percent
                  )}"
                >
                  {t('dashboard.newSpeciesHighlights.maxConfidenceShort', { confidence: percent })}
                </span>
              {/if}
            </div>

            <!-- Detections + last-seen -->
            <div
              class="mt-0.5 flex items-center gap-1.5 truncate text-xs text-[var(--color-base-content)]/60"
            >
              <AudioLines class="size-3.5 shrink-0" />
              <span>{t('dashboard.newSpeciesHighlights.detections', { count: species.count })}</span
              >
              {#if isToday && species.days_since_last_seen !== undefined && species.days_since_last_seen > 0}
                <span aria-hidden="true">·</span>
                <span class="truncate">
                  {t('dashboard.newSpeciesHighlights.lastSeen', {
                    days: species.days_since_last_seen,
                  })}
                </span>
              {/if}
            </div>
          </div>
        </a>
      {/each}
    </div>
  </Card>
{/if}
