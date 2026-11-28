<!--
  PhoneSpeciesTable.svelte - Compact daily species table for phone screens.

  Renders in place of the Daily Activity heatmap (via DailySummaryCard's `body` snippet).
  One row per species with an abundance bar, max confidence, count and an hourly chart;
  headers sort the table, a row expands into PhoneSpeciesDetail, and a taxon filter appears
  when the day holds more than one group (birds, bats, other).
-->
<script lang="ts">
  import { CalendarDays, ChevronDown, ChevronUp, History, Leaf, Star } from '@lucide/svelte';
  import { tick, type Component } from 'svelte';
  import { t } from '$lib/i18n';
  import type { DailySpeciesSummary } from '$lib/types/detection.types';
  import type { DailySummaryBodyContext } from '$lib/desktop/features/dashboard/components/DailySummaryCard.svelte';
  import {
    noveltyCategoryColorVar,
    type NoveltyCategory,
  } from '$lib/desktop/features/dashboard/utils/noveltyCategory';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';
  import { getStoredValue, setStoredValue } from '$lib/utils/storage';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils';
  import HourAxis from './HourAxis.svelte';
  import HourBars from './HourBars.svelte';
  import PhoneSpeciesDetail from './PhoneSpeciesDetail.svelte';
  import {
    BAR_STRIDE_PX,
    DEFAULT_PHONE_SORT,
    formatHour,
    nextSort,
    peakHour,
    sortRows,
    taxonGroup,
    TAXON_GROUPS,
    type PhoneSort,
    type PhoneSortKey,
    type TaxonGroup,
  } from './phoneSummary';

  let {
    data,
    selectedDate,
    showThumbnails,
    sunriseHour,
    sunsetHour,
    maxHour,
    speciesUrl,
    noveltyOf,
  }: DailySummaryBodyContext = $props();

  const SORT_STORAGE_KEY = 'birdnet-phone-summary-sort';
  const ROW_CHART_HEIGHT_PX = 20;
  const NO_PEAK = '-';
  const SORT_KEYS: readonly PhoneSortKey[] = ['count', 'name', 'conf', 'latest'];

  const NOVELTY_ICONS: Record<NoveltyCategory, Component> = {
    lifetime: Star,
    year: CalendarDays,
    season: Leaf,
    infrequent: History,
  };

  const TAXON_LABEL_KEYS = {
    bird: 'dashboard.dailySummary.phone.taxon.bird',
    bat: 'dashboard.dailySummary.phone.taxon.bat',
    other: 'dashboard.dailySummary.phone.taxon.other',
  } as const;

  const COLUMN_NAME_KEYS = {
    name: 'dashboard.dailySummary.phone.columnNames.name',
    conf: 'dashboard.dailySummary.phone.columnNames.conf',
    count: 'dashboard.dailySummary.phone.columnNames.count',
    latest: 'dashboard.dailySummary.phone.columnNames.latest',
  } as const;

  function isPhoneSort(value: unknown): value is PhoneSort {
    return (
      typeof value === 'object' &&
      value !== null &&
      'key' in value &&
      'dir' in value &&
      SORT_KEYS.some(k => k === value.key) &&
      (value.dir === 'asc' || value.dir === 'desc')
    );
  }

  const storedSort = getStoredValue<unknown>(SORT_STORAGE_KEY, DEFAULT_PHONE_SORT);
  let sort = $state<PhoneSort>(isPhoneSort(storedSort) ? storedSort : DEFAULT_PHONE_SORT);
  let taxonFilter = $state<TaxonGroup | 'all'>('all');
  let expandedSpecies = $state<string | null>(null);

  // A card left open across a day change would show another day's numbers.
  $effect(() => {
    void selectedDate;
    expandedSpecies = null;
  });

  const nameOf = (row: DailySpeciesSummary) =>
    localizeSpeciesName(row.scientific_name, row.common_name);

  // The payload can repeat a species; keep its first (busiest) row so rows stay keyed uniquely.
  const unique = $derived.by(() => {
    const seen = new Set<string>();
    return data.filter(r => !seen.has(r.scientific_name) && seen.add(r.scientific_name));
  });
  const groupsPresent = $derived(TAXON_GROUPS.filter(g => unique.some(r => taxonGroup(r) === g)));
  const filterOptions = $derived<(TaxonGroup | 'all')[]>(['all', ...groupsPresent]);
  // A group that leaves the data (new day, refetch) falls back to showing every row.
  const activeFilter = $derived(
    taxonFilter !== 'all' && groupsPresent.includes(taxonFilter) ? taxonFilter : 'all'
  );
  const filtered = $derived(
    activeFilter === 'all'
      ? unique
      : unique.filter(r => [activeFilter, null].includes(taxonGroup(r)))
  );
  const rows = $derived(sortRows(filtered, sort, nameOf));
  const maxCount = $derived(Math.max(1, ...unique.map(r => r.count)));
  const totalDetections = $derived(unique.reduce((sum, r) => sum + r.count, 0));
  const newSpecies = $derived(unique.filter(r => noveltyOf(r) === 'lifetime').length);

  // Move focus into an opened card, and back to its row when it closes, so keyboard and
  // screen reader users keep their place.
  async function expand(scientificName: string) {
    expandedSpecies = scientificName;
    await tick();
    tableEl?.querySelector<HTMLElement>('[data-species-detail]')?.focus();
  }

  let tableEl = $state<HTMLDivElement>();
  async function collapse(scientificName: string) {
    expandedSpecies = null;
    await tick();
    const rowEls = tableEl?.querySelectorAll<HTMLElement>('[data-species]') ?? [];
    [...rowEls].find(el => el.dataset.species === scientificName)?.focus();
  }

  // Keep the column header in view while the list scrolls. CSS sticky cannot do it here:
  // the app shell's .drawer-content is an overflow container that never scrolls itself
  // (the document does), so the header is shifted down by how far the table has scrolled
  // past the viewport top, stopping at the end of the list.
  let headerEl = $state<HTMLDivElement>();
  $effect(() => {
    const table = tableEl;
    const header = headerEl;
    if (!table || !header) return;
    let frame = 0;
    const pin = () => {
      frame = 0;
      // offsetTop is relative to the table (position: relative) and ignores the transform.
      const scrolledPast = -(table.getBoundingClientRect().top + header.offsetTop);
      const maxShift = table.offsetHeight - header.offsetTop - header.offsetHeight;
      const shift = Math.min(Math.max(0, scrolledPast), Math.max(0, maxShift));
      header.style.transform = shift > 0 ? `translateY(${shift}px)` : '';
    };
    const schedule = () => {
      frame ||= window.requestAnimationFrame(pin);
    };
    window.addEventListener('scroll', schedule, { passive: true });
    window.addEventListener('resize', schedule, { passive: true });
    // Expanding or collapsing a row changes the table height, and with it the pin limit.
    const resizeObserver = new window.ResizeObserver(schedule);
    resizeObserver.observe(table);
    pin();
    return () => {
      window.removeEventListener('scroll', schedule);
      window.removeEventListener('resize', schedule);
      resizeObserver.disconnect();
      window.cancelAnimationFrame(frame);
    };
  });

  function changeSort(key: PhoneSortKey) {
    sort = nextSort(sort, key);
    setStoredValue(SORT_STORAGE_KEY, sort);
  }

  /* eslint-disable security/detect-object-injection -- keys are typed union literals */
  const taxonLabel = (group: TaxonGroup) => t(TAXON_LABEL_KEYS[group]);
  const noveltyIcon = (novelty: NoveltyCategory) => NOVELTY_ICONS[novelty];
  /* eslint-enable security/detect-object-injection */

  function sortLabel(key: PhoneSortKey): string {
    // eslint-disable-next-line security/detect-object-injection -- key is a typed PhoneSortKey literal
    const column = t(COLUMN_NAME_KEYS[key]);
    if (sort.key !== key) return t('dashboard.dailySummary.phone.sortBy', { column });
    return sort.dir === 'asc'
      ? t('dashboard.dailySummary.phone.sortedAscending', { column })
      : t('dashboard.dailySummary.phone.sortedDescending', { column });
  }

  function rowLabel(row: DailySpeciesSummary): string {
    const peak = peakHour(row.hourly_counts, maxHour);
    return t('dashboard.dailySummary.phone.rowLabel', {
      name: nameOf(row),
      count: row.count,
      confidence: confidencePercent(row),
      peak: peak === null ? NO_PEAK : formatHour(peak),
    });
  }

  function confidencePercent(row: DailySpeciesSummary): number {
    return Math.round(Math.min(1, Math.max(0, row.max_confidence ?? 0)) * 100);
  }

  function thumbnailUrl(row: DailySpeciesSummary): string {
    return buildAppUrl(
      row.thumbnail_url ||
        `/api/v2/media/species-image?name=${encodeURIComponent(row.scientific_name)}`
    );
  }
</script>

{#snippet sortButton(key: PhoneSortKey, label: string)}
  <button
    type="button"
    class="phone-sort"
    class:active={sort.key === key}
    aria-label={sortLabel(key)}
    onclick={() => changeSort(key)}
  >
    {label}
    {#if sort.key === key}
      {#if sort.dir === 'asc'}<ChevronUp class="size-3" />{:else}<ChevronDown class="size-3" />{/if}
    {/if}
  </button>
{/snippet}

<div bind:this={tableEl} class="phone-summary" style:--chart-w="{(maxHour + 1) * BAR_STRIDE_PX}px">
  <dl class="phone-overview">
    <div>
      <dt>{t('dashboard.dailySummary.columns.species')}</dt>
      <dd>{unique.length}</dd>
    </div>
    <div>
      <dt>{t('dashboard.dailySummary.columns.detections')}</dt>
      <dd>{totalDetections}</dd>
    </div>
    <div>
      <dt>{t('dashboard.newSpeciesHighlights.categoryLifetime')}</dt>
      <dd>{newSpecies}</dd>
    </div>
  </dl>

  {#if groupsPresent.length > 1}
    <div class="phone-filter">
      {#each filterOptions as group (group)}
        <button
          type="button"
          class="btn btn-xs {activeFilter === group ? 'btn-primary' : 'btn-ghost'}"
          aria-pressed={activeFilter === group}
          onclick={() => (taxonFilter = group)}
        >
          {group === 'all' ? t('dashboard.dailySummary.phone.taxon.all') : taxonLabel(group)}
        </button>
      {/each}
    </div>
  {/if}

  {#if rows.length === 0}
    <p class="py-6 text-center text-sm text-[var(--color-base-content)]/60">
      {t('dashboard.dailySummary.noSpecies')}
    </p>
  {:else}
    <div bind:this={headerEl} class="phone-row phone-header">
      {@render sortButton('name', t('dashboard.dailySummary.columns.species'))}
      {@render sortButton('conf', t('dashboard.dailySummary.phone.columns.conf'))}
      {@render sortButton('count', t('dashboard.dailySummary.phone.columns.count'))}
      <button
        type="button"
        class="phone-sort"
        class:active={sort.key === 'latest'}
        aria-label={sortLabel('latest')}
        onclick={() => changeSort('latest')}
      >
        <HourAxis {maxHour} />
      </button>
    </div>

    {#each rows as row (row.scientific_name)}
      {@const novelty = noveltyOf(row)}
      {#if expandedSpecies === row.scientific_name}
        <PhoneSpeciesDetail
          item={row}
          displayName={nameOf(row)}
          {novelty}
          speciesUrl={speciesUrl(row)}
          {maxHour}
          {sunriseHour}
          {sunsetHour}
          {selectedDate}
          onCollapse={() => collapse(row.scientific_name)}
        />
      {:else}
        <button
          type="button"
          class="phone-row phone-species"
          data-species={row.scientific_name}
          aria-label={rowLabel(row)}
          onclick={() => expand(row.scientific_name)}
        >
          <span class="phone-name">
            <span class="flex items-center gap-1.5 min-w-0">
              {#if showThumbnails}
                <img
                  src={thumbnailUrl(row)}
                  alt=""
                  class="phone-thumb"
                  loading="lazy"
                  onerror={handleBirdImageError}
                />
              {/if}
              <span class="truncate text-sm font-medium">{nameOf(row)}</span>
              {#if novelty}
                {@const Icon = noveltyIcon(novelty)}
                <span class="shrink-0" style:color={noveltyCategoryColorVar(novelty)}>
                  <Icon class="size-3" />
                </span>
              {/if}
            </span>
            <span class="phone-abundance" style:width="{(row.count / maxCount) * 100}%"></span>
          </span>
          <span class="phone-num">{confidencePercent(row)}%</span>
          <span class="phone-num">{row.count}</span>
          <HourBars counts={row.hourly_counts} {maxHour} height={ROW_CHART_HEIGHT_PX} />
        </button>
      {/if}
    {/each}
  {/if}
</div>

<style>
  .phone-overview {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: 0.5rem;
    margin-bottom: 0.75rem;
    text-align: center;
  }

  .phone-overview dt {
    font-size: 0.75rem;
    color: color-mix(in srgb, var(--color-base-content) 60%, transparent);
  }

  .phone-overview dd {
    font-size: 1.125rem;
    font-weight: 600;
  }

  .phone-filter {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem;
    margin-bottom: 0.5rem;
  }

  /* Name | confidence | count | chart. The chart column grows with the time of day
     (--chart-w), so early in the day the species name gets the space. */
  .phone-row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 2.5rem 2.25rem var(--chart-w);
    column-gap: 0.375rem;
    align-items: center;
    width: 100%;
  }

  .phone-summary {
    position: relative;
  }

  .phone-header {
    position: relative;
    z-index: 1;
    padding: 0.25rem 0;
    background-color: var(--color-base-100);
    border-bottom: 1px solid var(--color-base-200);
  }

  .phone-sort {
    display: inline-flex;
    min-height: 2rem;
    align-items: center;
    justify-content: flex-end;
    gap: 0.125rem;
    font-size: 0.75rem;
    color: color-mix(in srgb, var(--color-base-content) 60%, transparent);
  }

  .phone-sort:first-child {
    justify-content: flex-start;
  }

  .phone-sort.active {
    color: var(--color-base-content);
    font-weight: 600;
    text-decoration: underline;
  }

  /* Off-screen rows skip layout and paint; 2.25rem matches the row's min-height. */
  .phone-species {
    min-height: 2.25rem;
    padding: 0 0.125rem;
    text-align: left;
    border-bottom: 1px solid var(--color-base-200);
    content-visibility: auto;
    contain-intrinsic-size: auto 2.25rem;
  }

  .phone-species:focus-visible {
    outline: 2px solid var(--color-primary);
    outline-offset: -2px;
  }

  .phone-name {
    display: flex;
    flex-direction: column;
    gap: 0.125rem;
    min-width: 0;
  }

  .phone-thumb {
    width: 1.5rem;
    height: 1.125rem;
    flex-shrink: 0;
    object-fit: cover;
    border-radius: 0.25rem;
  }

  .phone-abundance {
    height: 2px;
    border-radius: 1px;
    background-color: var(--color-primary);
    opacity: 0.6;
  }

  .phone-num {
    font-size: 0.75rem;
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
</style>
