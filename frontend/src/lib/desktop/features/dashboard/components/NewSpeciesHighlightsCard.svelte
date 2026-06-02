<!--
NewSpeciesHighlightsCard.svelte - Highlights newly detected species for the selected day

Renders nothing when no qualifying species exist for the day.
Data source: same DailySpeciesSummary[] already loaded by the dashboard page.

Two view modes (persisted in the dashboard element config via onViewModeChange):
- compact: dense cards; the bird picture is shown only when the daily-summary
  thumbnail setting is enabled, otherwise a colored initials badge is used.
- full: larger cards with the bird picture as background and text overlaid.
-->

<script lang="ts">
  import Card from '$lib/desktop/components/ui/Card.svelte';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils';
  import { t } from '$lib/i18n';
  import type { DailySpeciesSummary } from '$lib/types/detection.types';
  import { buildSpeciesDetectionUrl } from '$lib/utils/detectionUrls';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { CalendarDays, Image as ImageIcon, LayoutGrid, Leaf, Star } from '@lucide/svelte';

  interface Props {
    data?: DailySpeciesSummary[];
    selectedDate: string;
    /** Global daily-summary thumbnail setting; controls images in compact view. */
    showThumbnails?: boolean;
    /** true = reduced card view, false = full image-background view. */
    compact?: boolean;
    /** Show the full/compact view toggle (hidden during dashboard edit mode). */
    showViewToggle?: boolean;
    /** Persist a view-mode change to the dashboard element config. */
    onViewModeChange?: (_compact: boolean) => void;
  }

  let {
    data = [],
    selectedDate,
    showThumbnails = true,
    compact = true,
    showViewToggle = true,
    onViewModeChange,
  }: Props = $props();

  // Maximum cards rendered before collapsing the rest into a "+N more" link.
  const MAX_VISIBLE = 12;

  // Consistent placeholder colors for the compact view (matches daily summary).
  const BADGE_COLORS = [
    '#10b981',
    '#f59e0b',
    '#ef4444',
    '#8b5cf6',
    '#06b6d4',
    '#ec4899',
    '#84cc16',
    '#f97316',
    '#6366f1',
    '#14b8a6',
    '#a855f7',
    '#eab308',
  ];

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

  const visibleHighlights = $derived(highlights.slice(0, MAX_VISIBLE));
  const overflowCount = $derived(Math.max(0, highlights.length - MAX_VISIBLE));
  const moreUrl = buildAppUrl('/ui/analytics/species');

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

  function speciesUrl(item: DailySpeciesSummary): string {
    return buildSpeciesDetectionUrl(item.scientific_name, selectedDate);
  }

  function thumbnailUrl(item: DailySpeciesSummary): string {
    return item.thumbnail_url
      ? buildAppUrl(item.thumbnail_url)
      : buildAppUrl(`/api/v2/media/species-image?name=${encodeURIComponent(item.scientific_name)}`);
  }

  function badgeColor(name: string): string {
    let hash = 0;
    for (let i = 0; i < name.length; i++) {
      hash = name.charCodeAt(i) + ((hash << 5) - hash);
    }
    return BADGE_COLORS[Math.abs(hash) % BADGE_COLORS.length];
  }

  function initials(name: string): string {
    const words = name.trim().split(/\s+/).filter(Boolean);
    if (words.length === 0) return '??';
    if (words.length === 1) return words[0].slice(0, 2).toUpperCase();
    return (words[0][0] + words[1][0]).toUpperCase();
  }

  function setMode(nextCompact: boolean) {
    if (nextCompact === compact) return;
    onViewModeChange?.(nextCompact);
  }
</script>

{#snippet statsLine(species: DailySpeciesSummary, muted: boolean)}
  {@const percent = confidencePercent(species)}
  <div class={muted ? 'text-white/85' : 'text-[var(--color-base-content)]/60'}>
    <span>{t('dashboard.newSpeciesHighlights.detections', { count: species.count })}</span>
    {#if percent !== undefined}
      <span aria-hidden="true"> · </span>
      <span>{t('dashboard.newSpeciesHighlights.maxConfidenceShort', { confidence: percent })}</span>
    {/if}
    {#if species.is_new_species && species.days_since_first_seen !== undefined}
      <span aria-hidden="true"> · </span>
      <span
        >{t('dashboard.newSpeciesHighlights.firstSeen', {
          days: species.days_since_first_seen,
        })}</span
      >
    {/if}
  </div>
{/snippet}

{#snippet categoryBadge(category: HighlightCategory, season: string | undefined, cls: string)}
  <span
    class="flex items-center justify-center rounded-full text-white shadow {cls}"
    style:background-color={categoryColorVar(category)}
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

{#if highlights.length > 0}
  <Card padding={false}>
    {#snippet header()}
      <div class="flex items-start justify-between gap-2">
        <div class="flex flex-col gap-0.5">
          <h3 class="font-semibold">{t('dashboard.newSpeciesHighlights.title')}</h3>
          <p class="text-sm text-[var(--color-base-content)]/60">
            {t('dashboard.newSpeciesHighlights.subtitle')}
          </p>
        </div>

        <!-- View-mode toggle -->
        {#if showViewToggle}
          <div
            class="flex shrink-0 rounded-lg border border-[var(--color-base-300)] p-0.5"
            role="group"
            aria-label={t('dashboard.newSpeciesHighlights.viewToggle')}
          >
            <button
              type="button"
              onclick={() => setMode(true)}
              aria-pressed={compact}
              title={t('dashboard.newSpeciesHighlights.viewCompact')}
              aria-label={t('dashboard.newSpeciesHighlights.viewCompact')}
              class="flex size-7 items-center justify-center rounded-md transition-colors {compact
                ? 'bg-[var(--color-base-300)] text-[var(--color-base-content)]'
                : 'text-[var(--color-base-content)]/60 hover:bg-[var(--color-base-200)]'}"
            >
              <LayoutGrid class="size-4" />
            </button>
            <button
              type="button"
              onclick={() => setMode(false)}
              aria-pressed={!compact}
              title={t('dashboard.newSpeciesHighlights.viewFull')}
              aria-label={t('dashboard.newSpeciesHighlights.viewFull')}
              class="flex size-7 items-center justify-center rounded-md transition-colors {!compact
                ? 'bg-[var(--color-base-300)] text-[var(--color-base-content)]'
                : 'text-[var(--color-base-content)]/60 hover:bg-[var(--color-base-200)]'}"
            >
              <ImageIcon class="size-4" />
            </button>
          </div>
        {/if}
      </div>
    {/snippet}

    {#if compact}
      <!-- Reduced cards -->
      <div class="grid grid-cols-2 gap-3 px-4 pb-4 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5">
        {#each visibleHighlights as { species, category } (species.scientific_name)}
          {@const percent = confidencePercent(species)}
          <a
            href={speciesUrl(species)}
            class="group flex items-center gap-2.5 rounded-lg border border-[var(--color-base-200)] bg-[var(--color-base-100)] p-2 shadow-sm transition-shadow hover:shadow-md"
          >
            <!-- Picture or initials badge -->
            <div class="relative shrink-0">
              {#if showThumbnails}
                <img
                  src={thumbnailUrl(species)}
                  alt={species.common_name}
                  loading="lazy"
                  onerror={handleBirdImageError}
                  class="size-12 rounded-md object-cover"
                />
              {:else}
                <div
                  class="flex size-12 items-center justify-center rounded-md text-sm font-bold text-white"
                  style:background-color={badgeColor(species.common_name)}
                >
                  {initials(species.common_name)}
                </div>
              {/if}
              {@render categoryBadge(
                category,
                species.current_season,
                'absolute -left-1 -top-1 size-5'
              )}
            </div>

            <div class="min-w-0 flex-1">
              <div class="flex items-center justify-between gap-1.5">
                <span
                  class="truncate text-sm font-medium leading-tight group-hover:text-[var(--color-primary)]"
                  title={species.common_name}
                >
                  {species.common_name}
                </span>
                {#if percent !== undefined}
                  <span
                    class="shrink-0 rounded-full px-1.5 py-0.5 text-xs font-semibold {confidenceClasses(
                      percent
                    )}"
                  >
                    {t('dashboard.newSpeciesHighlights.maxConfidenceShort', {
                      confidence: percent,
                    })}
                  </span>
                {/if}
              </div>
              <div class="mt-0.5 truncate text-xs">
                {@render statsLine(species, false)}
              </div>
            </div>
          </a>
        {/each}
      </div>
    {:else}
      <!-- Full cards: photo background with overlaid text -->
      <div class="grid grid-cols-1 gap-3 px-4 pb-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
        {#each visibleHighlights as { species, category } (species.scientific_name)}
          {@const percent = confidencePercent(species)}
          <a
            href={speciesUrl(species)}
            class="group relative block aspect-[4/3] overflow-hidden rounded-lg shadow-sm transition-shadow hover:shadow-md"
          >
            <img
              src={thumbnailUrl(species)}
              alt={species.common_name}
              loading="lazy"
              onerror={handleBirdImageError}
              class="absolute inset-0 size-full object-cover transition-transform duration-300 group-hover:scale-105"
            />
            <!-- Scrim for legibility -->
            <div
              class="absolute inset-0 bg-gradient-to-t from-black/80 via-black/20 to-transparent"
            ></div>

            {@render categoryBadge(
              category,
              species.current_season,
              'absolute left-2 top-2 size-6'
            )}
            {#if percent !== undefined}
              <span
                class="absolute right-2 top-2 rounded-full px-1.5 py-0.5 text-xs font-semibold shadow {confidenceClasses(
                  percent
                )}"
              >
                {t('dashboard.newSpeciesHighlights.maxConfidenceShort', { confidence: percent })}
              </span>
            {/if}

            <div class="absolute inset-x-0 bottom-0 p-3 text-white">
              <span
                class="block truncate text-sm font-semibold leading-tight"
                style:text-shadow="0 1px 3px rgb(0 0 0 / 0.6)"
                title={species.common_name}
              >
                {species.common_name}
              </span>
              <div class="mt-0.5 truncate text-xs" style:text-shadow="0 1px 3px rgb(0 0 0 / 0.6)">
                {@render statsLine(species, true)}
              </div>
            </div>
          </a>
        {/each}
      </div>
    {/if}

    {#if overflowCount > 0}
      <div class="px-4 pb-4">
        <a href={moreUrl} class="text-sm font-medium text-[var(--color-primary)] hover:underline">
          {t('dashboard.newSpeciesHighlights.moreCount', { count: overflowCount })}
        </a>
      </div>
    {/if}
  </Card>
{/if}
