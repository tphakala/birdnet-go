<!--
NewSpeciesHighlightsCard.svelte - Highlights newly detected species for the selected day

Renders nothing when no qualifying species exist for the day.
Data source: same DailySpeciesSummary[] already loaded by the dashboard page.
-->

<script lang="ts">
  import Card from '$lib/desktop/components/ui/Card.svelte';
  import { t } from '$lib/i18n';
  import type { DailySpeciesSummary } from '$lib/types/detection.types';
  import { buildSpeciesDetectionUrl } from '$lib/utils/detectionUrls';
  import { Star } from '@lucide/svelte';

  interface Props {
    data?: DailySpeciesSummary[];
    selectedDate: string;
  }

  let { data = [], selectedDate }: Props = $props();

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

  function categoryIcon(category: HighlightCategory): string {
    switch (category) {
      case 'lifetime':
        return '⭐';
      case 'year':
        return '📅';
      case 'season':
        return '🌿';
    }
  }

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

  function categoryBorderColor(category: HighlightCategory): string {
    switch (category) {
      case 'lifetime':
        return 'var(--color-warning)';
      case 'year':
        return 'var(--color-info)';
      case 'season':
        return 'var(--color-success)';
    }
  }

  function formatTime(value: string | undefined): string {
    if (!value) return '';
    const parts = value.split(':');
    return parts.length >= 2 ? `${parts[0]}:${parts[1]}` : value;
  }

  function speciesUrl(item: DailySpeciesSummary): string {
    return buildSpeciesDetectionUrl(item.scientific_name, selectedDate);
  }
</script>

{#if highlights.length > 0}
  <Card padding={false}>
    {#snippet header()}
      <div class="flex items-center gap-2 px-4 py-3">
        <Star class="size-4 fill-current text-[var(--color-warning)]" />
        <h2 class="text-sm font-semibold">
          {t('dashboard.newSpeciesHighlights.title')}
        </h2>
        <span
          class="inline-flex items-center justify-center rounded-full bg-[var(--color-base-200)] px-1.5 text-xs font-medium text-[var(--color-base-content)]/70"
        >
          {highlights.length}
        </span>
      </div>
    {/snippet}

    <div class="grid grid-cols-2 gap-2 px-3 pb-3 lg:grid-cols-3 xl:grid-cols-4">
      {#each highlights as { species, category } (species.scientific_name)}
        <a
          href={speciesUrl(species)}
          class="flex flex-col justify-between gap-1 rounded-md border border-[var(--color-base-200)] bg-[var(--color-base-100)] p-2.5 transition-colors hover:bg-[var(--color-base-200)]"
          style:border-left-width="3px"
          style:border-left-color={categoryBorderColor(category)}
          title={categoryLabel(category, species.current_season)}
        >
          <!-- Species name -->
          <div class="min-w-0">
            <span class="block truncate text-sm font-medium leading-snug">
              {categoryIcon(category)}&nbsp;{species.common_name}
            </span>
            <span class="block truncate text-xs italic text-[var(--color-base-content)]/55">
              {species.scientific_name}
            </span>
          </div>

          <!-- Stats line -->
          <div
            class="flex flex-wrap items-center gap-x-2 text-xs text-[var(--color-base-content)]/65"
          >
            <span>{species.count}&times;</span>
            {#if species.max_confidence !== undefined && species.max_confidence > 0}
              <span>{Math.round(species.max_confidence * 100)}%</span>
            {/if}
            {#if species.latest_heard}
              <span>{formatTime(species.latest_heard)}</span>
            {/if}
          </div>
        </a>
      {/each}
    </div>
  </Card>
{/if}
