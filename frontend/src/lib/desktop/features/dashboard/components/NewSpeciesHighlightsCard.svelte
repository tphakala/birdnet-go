<!--
NewSpeciesHighlightsCard.svelte - Highlights newly detected species for the selected day

Renders nothing when no qualifying species exist for the day.
Data source: same DailySpeciesSummary[] already loaded by the dashboard page.
-->

<script lang="ts">
  import Card from '$lib/desktop/components/ui/Card.svelte';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils';
  import { t } from '$lib/i18n';
  import type { DailySpeciesSummary } from '$lib/types/detection.types';
  import { buildSpeciesDetectionUrl } from '$lib/utils/detectionUrls';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { CalendarDays, Leaf, Star } from '@lucide/svelte';

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

  // Background color for the category overlay icon (matches daily summary indicators).
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

  // latest_heard is a local time-of-day string (HH:MM:SS) for the selected day.
  function formatTime(value: string | undefined): string {
    if (!value) return '';
    const parts = value.split(':');
    return parts.length >= 2 ? `${parts[0]}:${parts[1]}` : value;
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
      <div class="flex flex-col gap-0.5">
        <h3 class="font-semibold">{t('dashboard.newSpeciesHighlights.title')}</h3>
        <p class="text-sm text-[var(--color-base-content)]/60">
          {t('dashboard.newSpeciesHighlights.subtitle')}
        </p>
      </div>
    {/snippet}

    <div class="grid grid-cols-2 gap-3 px-4 pb-4 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5">
      {#each highlights as { species, category } (species.scientific_name)}
        {@const percent = confidencePercent(species)}
        <a
          href={speciesUrl(species)}
          class="group flex flex-col overflow-hidden rounded-lg border border-[var(--color-base-200)] bg-[var(--color-base-100)] shadow-sm transition-shadow hover:shadow-md"
        >
          <!-- Thumbnail with overlays -->
          <div class="relative">
            <img
              src={thumbnailUrl(species)}
              alt={species.common_name}
              loading="lazy"
              onerror={handleBirdImageError}
              class="aspect-[4/3] w-full object-cover"
            />

            <!-- Category icon (top-left): why this species is highlighted -->
            <span
              class="absolute left-1.5 top-1.5 flex size-6 items-center justify-center rounded-full text-white shadow"
              style:background-color={categoryColorVar(category)}
              title={categoryLabel(category, species.current_season)}
              aria-label={categoryLabel(category, species.current_season)}
            >
              {#if category === 'lifetime'}
                <Star class="size-3.5 fill-current" />
              {:else if category === 'year'}
                <CalendarDays class="size-3.5" />
              {:else}
                <Leaf class="size-3.5" />
              {/if}
            </span>

            <!-- Max confidence pill (top-right) -->
            {#if percent !== undefined}
              <span
                class="absolute right-1.5 top-1.5 rounded-full px-1.5 py-0.5 text-xs font-semibold shadow {confidenceClasses(
                  percent
                )}"
              >
                {t('dashboard.newSpeciesHighlights.maxConfidenceShort', { confidence: percent })}
              </span>
            {/if}
          </div>

          <!-- Info -->
          <div class="flex flex-col gap-0.5 p-2.5">
            <span
              class="truncate text-sm font-medium leading-tight group-hover:text-[var(--color-primary)]"
              title={species.common_name}
            >
              {species.common_name}
            </span>
            <div class="flex items-center gap-1.5 text-xs text-[var(--color-base-content)]/60">
              <span title={t('dashboard.newSpeciesHighlights.detectionsToday')}>
                {species.count}&times;
              </span>
              {#if species.latest_heard}
                <span aria-hidden="true">·</span>
                <span title={t('dashboard.newSpeciesHighlights.lastDetection')}>
                  {formatTime(species.latest_heard)}
                </span>
              {/if}
            </div>
          </div>
        </a>
      {/each}
    </div>
  </Card>
{/if}
