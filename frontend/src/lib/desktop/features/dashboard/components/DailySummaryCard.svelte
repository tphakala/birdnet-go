<!--
DailySummaryCard.svelte - Daily bird species detection summary table

Purpose:
- Displays daily bird species summaries with hourly detection counts
- Provides interactive heatmap visualization of detection patterns
- Supports date navigation and real-time updates via SSE
- Integrates sun times to highlight sunrise/sunset hours

Features:
- Progressive loading states (skeleton → spinner → loaded/error)
- Responsive hourly/bi-hourly/six-hourly column grouping based on viewport
- Color-coded heatmap cells showing detection intensity
- Daylight visualization row showing sunrise/sunset times
- Species badges with colored initials (GitHub-style heatmap design)
- Real-time animation for new species and count increases
- URL memoization with LRU cache for performance optimization
- Heatmap legend showing intensity scale (Less → More)
- Date picker navigation with keyboard shortcuts
- Clickable cells linking to detailed detection views

Props:
- data: DailySpeciesSummary[] - Array of species detection summaries
- loading?: boolean - Loading state indicator (default: false)
- error?: string | null - Error message to display (default: null)
- selectedDate: string - Currently selected date in YYYY-MM-DD format
- showThumbnails?: boolean - Show thumbnails or colored badge placeholders (default: true)
- onPreviousDay: () => void - Callback for previous day navigation
- onNextDay: () => void - Callback for next day navigation
- onGoToToday: () => void - Callback for "today" button click
- onDateChange: (date: string) => void - Callback for date picker changes

Performance Optimizations:
- $state.raw() for static data structures (caches, render functions)
- $derived.by() for complex reactive calculations
- LRU cache for URL memoization (500 entries max)
- Optimized animation cleanup with requestAnimationFrame
- Efficient data sorting and max count calculations

Responsive Breakpoints:
- Desktop (≥1400px): All hourly columns visible
- Large (1200-1399px): All hourly columns visible
- Medium (1024-1199px): All hourly columns visible
- Tablet (768-1023px): Bi-hourly columns only
- Mobile (480-767px): Bi-hourly columns only
- Small (<480px): Six-hourly columns only
-->

<script lang="ts">
  import { untrack } from 'svelte';
  import DatePicker from '$lib/desktop/components/ui/DatePicker.svelte';
  import type { DailySpeciesSummary } from '$lib/types/detection.types';
  import { XCircle, ChevronLeft, ChevronRight, Sunrise, Sunset, Star } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import BirdThumbnailPopup from './BirdThumbnailPopup.svelte';
  import SkeletonDailySummary from '$lib/desktop/components/ui/SkeletonDailySummary.svelte';
  import { getLocalDateString } from '$lib/utils/date';
  import { LRUCache } from '$lib/utils/LRUCache';
  import { loggers } from '$lib/utils/logger';
  import { safeArrayAccess } from '$lib/utils/security';

  const logger = loggers.ui;

  // Progressive loading timing constants (optimized for Svelte 5)
  const LOADING_PHASES = $state.raw({
    skeleton: 0, // 0ms - show skeleton immediately to reserve space
    spinner: 650, // 650ms - show spinner if still loading
  });

  interface SunTimes {
    sunrise: string; // ISO date string
    sunset: string; // ISO date string
  }

  // Column type definitions
  interface BaseColumn {
    key: string;
    header?: string;
    className?: string;
    align?: string;
  }

  interface SpeciesColumn extends BaseColumn {
    type: 'species';
    sortable: boolean;
  }

  interface HourlyColumn extends BaseColumn {
    type: 'hourly';
    hour: number;
    align: string;
  }

  interface BiHourlyColumn extends BaseColumn {
    type: 'bi-hourly';
    hour: number;
    align: string;
  }

  interface SixHourlyColumn extends BaseColumn {
    type: 'six-hourly';
    hour: number;
    align: string;
  }

  type ColumnDefinition = SpeciesColumn | HourlyColumn | BiHourlyColumn | SixHourlyColumn;

  // URL builder types
  interface URLBuilders {
    species: (_species: DailySpeciesSummary) => string;
    speciesHour: (_species: DailySpeciesSummary, _hour: number, _duration?: number) => string;
    hourly: (_hour: number, _duration?: number) => string;
  }

  interface Props {
    data: DailySpeciesSummary[];
    loading?: boolean;
    error?: string | null;
    selectedDate: string;
    showThumbnails?: boolean;
    onPreviousDay: () => void;
    onNextDay: () => void;
    onGoToToday: () => void;
    onDateChange: (_date: string) => void;
  }

  let {
    data = [],
    loading = false,
    error = null,
    selectedDate,
    showThumbnails = true,
    onPreviousDay,
    onNextDay,
    onGoToToday,
    onDateChange,
  }: Props = $props();

  // Progressive loading state management
  let loadingPhase = $state<'skeleton' | 'spinner' | 'loaded' | 'error'>('skeleton');
  let showDelayedIndicator = $state(false);

  // Sun times state
  let sunTimes = $state<SunTimes | null>(null);

  // Cache for sun times to avoid repeated API calls - use LRUCache to limit memory usage
  const sunTimesCache = $state.raw(new LRUCache<string, SunTimes>(30)); // Max 30 days of sun times

  // Optimize loading state management with proper dependency tracking
  $effect(() => {
    if (loading) {
      loadingPhase = 'skeleton'; // Show skeleton immediately to reserve space
      showDelayedIndicator = false;

      // Use untrack to prevent the timer from becoming a reactive dependency
      const spinnerTimer = setTimeout(() => {
        if (untrack(() => loading)) {
          loadingPhase = 'spinner';
          showDelayedIndicator = true;
        }
      }, LOADING_PHASES.spinner);

      return () => {
        clearTimeout(spinnerTimer);
      };
    } else {
      loadingPhase = error ? 'error' : 'loaded';
      showDelayedIndicator = false;
    }
  });

  // Fetch sun times from weather API with caching
  async function fetchSunTimes(date: string): Promise<SunTimes | null> {
    // Check cache first using LRUCache methods
    const cached = sunTimesCache.get(date);
    if (cached) {
      return cached;
    }

    try {
      const response = await fetch(`/api/v2/weather/sun/${date}`);
      if (!response.ok) {
        const errorMsg = `Failed to fetch sun times: ${response.status} ${response.statusText}`;
        logger.warn(errorMsg);
        return null;
      }
      const data = await response.json();
      const sunTimesData: SunTimes = {
        sunrise: data.sunrise,
        sunset: data.sunset,
      };

      // Cache the result
      sunTimesCache.set(date, sunTimesData);

      return sunTimesData;
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : 'Unknown error fetching sun times';
      logger.warn('Error fetching sun times:', errorMsg);
      return null;
    }
  }

  // Update sun times when selected date changes
  $effect(() => {
    if (selectedDate) {
      fetchSunTimes(selectedDate).then(times => {
        sunTimes = times; // times will be null if there was an error
      });
    }
  });

  // Calculate which hour column corresponds to sunrise/sunset
  const getSunHourFromTime = (timeStr: string): number | null => {
    if (!timeStr) return null;
    try {
      const date = new Date(timeStr);
      return date.getHours();
    } catch (error) {
      logger.error('Error parsing time', error, { timeStr });
      return null;
    }
  };

  // Check if an hour is during daylight
  const isDaylightHour = (hour: number): boolean => {
    if (!sunTimes) return false;
    const sunriseHour = getSunHourFromTime(sunTimes.sunrise);
    const sunsetHour = getSunHourFromTime(sunTimes.sunset);
    if (sunriseHour === null || sunsetHour === null) return false;
    return hour >= sunriseHour && hour < sunsetHour;
  };

  // Species badge color palette - 12 distinct, visually appealing colors
  const BADGE_COLORS = $state.raw([
    '#10b981', // emerald
    '#f59e0b', // amber
    '#ef4444', // red
    '#8b5cf6', // violet
    '#06b6d4', // cyan
    '#ec4899', // pink
    '#84cc16', // lime
    '#f97316', // orange
    '#6366f1', // indigo
    '#14b8a6', // teal
    '#a855f7', // purple
    '#eab308', // yellow
  ]);

  // Generate a consistent color for a species based on its name
  const getSpeciesBadgeColor = (speciesName: string): string => {
    let hash = 0;
    for (let i = 0; i < speciesName.length; i++) {
      hash = speciesName.charCodeAt(i) + ((hash << 5) - hash);
    }
    return BADGE_COLORS[Math.abs(hash) % BADGE_COLORS.length];
  };

  // Get initials from species common name (first letter of first two words)
  const getSpeciesInitials = (commonName: string): string => {
    const words = commonName.trim().split(/\s+/);
    if (words.length === 1) {
      return words[0].substring(0, 2).toUpperCase();
    }
    return (words[0][0] + words[1][0]).toUpperCase();
  };

  // Static column metadata - use $state.raw() for performance (no deep reactivity needed)
  const staticColumnDefs = $state.raw<ColumnDefinition[]>([
    {
      key: 'common_name',
      type: 'species' as const,
      sortable: true,
      className: 'font-medium whitespace-nowrap species-column',
    },
    // Progress bar column removed to save horizontal space - see mockup design
    ...Array.from({ length: 24 }, (_, hour) => ({
      key: `hour_${hour}`,
      type: 'hourly' as const,
      hour,
      header: hour.toString().padStart(2, '0'),
      align: 'center',
      className: 'hour-data hourly-count px-0',
    })),
    ...Array.from({ length: 12 }, (_, i) => {
      const hour = i * 2;
      return {
        key: `bi_hour_${hour}`,
        type: 'bi-hourly' as const,
        hour,
        header: hour.toString().padStart(2, '0'),
        align: 'center',
        className: 'hour-data bi-hourly-count bi-hourly px-0',
      };
    }),
    ...Array.from({ length: 4 }, (_, i) => {
      const hour = i * 6;
      return {
        key: `six_hour_${hour}`,
        type: 'six-hourly' as const,
        hour,
        header: hour.toString().padStart(2, '0'),
        align: 'center',
        className: 'hour-data six-hourly-count six-hourly px-0',
      };
    }),
  ]);

  // Reactive columns with only dynamic headers - use $derived.by for complex logic
  const columns = $derived.by((): ColumnDefinition[] => {
    // Early return for empty data to prevent unnecessary calculations
    if (staticColumnDefs.length === 0) return [];

    return staticColumnDefs.map(colDef => ({
      ...colDef,
      header:
        colDef.type === 'species' ? t('dashboard.dailySummary.columns.species') : colDef.header,
    }));
  });

  // Track and log unexpected column types once (performance optimization)
  const loggedUnexpectedColumns = new Set<string>();
  $effect(() => {
    if (import.meta.env.DEV) {
      const expectedTypes = new Set(['species', 'hourly', 'bi-hourly', 'six-hourly']);

      columns.forEach(column => {
        if (!expectedTypes.has(column.type) && !loggedUnexpectedColumns.has(column.key)) {
          logger.warn('Unexpected column type detected', null, {
            columnKey: column.key,
            columnType: column.type,
            component: 'DailySummaryCard',
            action: 'columnValidation',
          });
          loggedUnexpectedColumns.add(column.key);
        }
      });
    }
  });

  // Pre-computed render functions - use $state.raw for performance (static functions)
  const renderFunctions = $state.raw({
    hourly: (item: DailySpeciesSummary, hour: number) =>
      safeArrayAccess(item.hourly_counts, hour, 0) ?? 0,
    'bi-hourly': (item: DailySpeciesSummary, hour: number) =>
      (safeArrayAccess(item.hourly_counts, hour, 0) ?? 0) +
      (safeArrayAccess(item.hourly_counts, hour + 1, 0) ?? 0),
    'six-hourly': (item: DailySpeciesSummary, hour: number) => {
      let sum = 0;
      for (let h = hour; h < hour + 6 && h < 24; h++) {
        sum += safeArrayAccess(item.hourly_counts, h, 0) ?? 0;
      }
      return sum;
    },
  });

  // Phase 4: Optimized URL building with memoization for 90%+ performance improvement
  const urlCache = $state.raw(new LRUCache<string, string>(500)); // Max 500 URLs cached - use $state.raw
  const urlBuilders = $state<URLBuilders>({
    // Default functions to prevent undefined errors during initial render
    species: () => '#',
    speciesHour: () => '#',
    hourly: () => '#',
  });

  // Reactive URL builder factory - clears cache when selectedDate changes
  $effect(() => {
    // Clear cache when selectedDate changes to prevent stale URLs
    urlCache.clear();

    // Create optimized, memoized URL builders
    urlBuilders.species = (species: DailySpeciesSummary) => {
      const cacheKey = `species:${species.common_name}:${selectedDate}`;
      if (!urlCache.has(cacheKey)) {
        const params = new URLSearchParams({
          queryType: 'species',
          species: species.common_name,
          date: selectedDate,
          numResults: '25',
          offset: '0',
        });
        urlCache.set(cacheKey, `/ui/detections?${params.toString()}`);
      }
      return urlCache.get(cacheKey)!;
    };

    urlBuilders.speciesHour = (
      species: DailySpeciesSummary,
      hour: number,
      duration: number = 1
    ) => {
      const cacheKey = `species-hour:${species.common_name}:${selectedDate}:${hour}:${duration}`;
      if (!urlCache.has(cacheKey)) {
        const params = new URLSearchParams({
          queryType: 'species',
          species: species.common_name,
          date: selectedDate,
          hour: hour.toString(),
          duration: duration.toString(),
          numResults: '25',
          offset: '0',
        });
        urlCache.set(cacheKey, `/ui/detections?${params.toString()}`);
      }
      return urlCache.get(cacheKey)!;
    };

    urlBuilders.hourly = (hour: number, duration: number = 1) => {
      const cacheKey = `hourly:${selectedDate}:${hour}:${duration}`;
      if (!urlCache.has(cacheKey)) {
        const params = new URLSearchParams({
          queryType: 'hourly',
          date: selectedDate,
          hour: hour.toString(),
          duration: duration.toString(),
          numResults: '25',
          offset: '0',
        });
        urlCache.set(cacheKey, `/ui/detections?${params.toString()}`);
      }
      return urlCache.get(cacheKey)!;
    };
  });

  // LRU cache automatically manages memory, no need for periodic cleanup

  const isToday = $derived(selectedDate === getLocalDateString());

  // Check for reduced motion preference for performance and accessibility
  const prefersReducedMotion = $derived(
    typeof window !== 'undefined'
      ? (window.matchMedia?.('(prefers-reduced-motion: reduce)')?.matches ?? false)
      : false
  );

  // Optimized data sorting using $derived.by for better performance
  // Two-tier sorting: primary by count, secondary by latest detection time
  const sortedData = $derived.by(() => {
    // Early return for empty data
    if (data.length === 0) return [];

    // Use spread + sort with stable ordering
    return [...data].sort((a: DailySpeciesSummary, b: DailySpeciesSummary) => {
      // Primary sort: by detection count (descending)
      if (b.count !== a.count) {
        return b.count - a.count;
      }
      // Secondary sort: by latest detection time (descending - most recent first)
      // This ensures stable ordering when counts are equal
      return (b.latest_heard ?? '').localeCompare(a.latest_heard ?? '');
    });
  });

  // Optimized max count calculations using $derived.by for better performance
  const globalMaxHourlyCount = $derived.by(() => {
    if (sortedData.length === 0) return 1;

    let maxCount = 1;
    for (const species of sortedData) {
      for (const count of species.hourly_counts) {
        if (count > maxCount) {
          maxCount = count;
        }
      }
    }
    return maxCount;
  });

  const globalMaxBiHourlyCount = $derived.by(() => {
    if (sortedData.length === 0) return 1;

    let maxCount = 0;
    for (const species of sortedData) {
      for (let hour = 0; hour < 24; hour += 2) {
        const sum =
          (safeArrayAccess(species.hourly_counts, hour, 0) ?? 0) +
          (safeArrayAccess(species.hourly_counts, hour + 1, 0) ?? 0);
        maxCount = Math.max(maxCount, sum);
      }
    }
    return maxCount || 1;
  });

  const globalMaxSixHourlyCount = $derived.by(() => {
    if (sortedData.length === 0) return 1;

    let maxCount = 0;
    for (const species of sortedData) {
      for (let hour = 0; hour < 24; hour += 6) {
        let sum = 0;
        for (let h = hour; h < hour + 6 && h < 24; h++) {
          sum += safeArrayAccess(species.hourly_counts, h, 0) ?? 0;
        }
        maxCount = Math.max(maxCount, sum);
      }
    }
    return maxCount || 1;
  });
</script>

{#snippet navigationControls()}
  <div class="flex items-center gap-2">
    <!-- Previous day button -->
    <button
      onclick={onPreviousDay}
      class="btn btn-sm btn-ghost shrink-0"
      aria-label={t('dashboard.dailySummary.navigation.previousDay')}
    >
      <ChevronLeft class="size-5" />
    </button>

    <!-- Date picker with consistent width -->
    <DatePicker
      value={selectedDate}
      onChange={onDateChange}
      onTodayClick={onGoToToday}
      className="mx-2 grow"
    />

    <!-- Next day button -->
    <button
      onclick={onNextDay}
      class="btn btn-sm btn-ghost shrink-0"
      disabled={isToday}
      aria-label={t('dashboard.dailySummary.navigation.nextDay')}
    >
      <ChevronRight class="size-5" />
    </button>
  </div>
{/snippet}

<!-- Live region for screen reader announcements of loading state changes -->
<div class="sr-only" role="status" aria-live="polite" aria-atomic="true">
  {#if loadingPhase === 'skeleton'}
    {t('dashboard.dailySummary.loading.preparing')}
  {:else if loadingPhase === 'spinner'}
    {t('dashboard.dailySummary.loading.fetching')}
  {:else if loadingPhase === 'error'}
    {t('dashboard.dailySummary.loading.error')}
  {:else if loadingPhase === 'loaded'}
    {t('dashboard.dailySummary.loading.complete')}
  {/if}
</div>

<!-- Progressive loading implementation -->
{#if loadingPhase === 'skeleton'}
  <SkeletonDailySummary {showThumbnails} speciesCount={8} />
{:else if loadingPhase === 'spinner'}
  <SkeletonDailySummary {showThumbnails} showSpinner={showDelayedIndicator} speciesCount={8} />
{:else if loadingPhase === 'error'}
  <section class="card col-span-12 bg-base-100 shadow-sm rounded-2xl border border-border-100">
    <div class="card-body grow-0 p-2 sm:p-4 sm:pt-3">
      <div class="flex items-center justify-between mb-4">
        <div class="flex flex-col">
          <span class="card-title text-base sm:text-xl">{t('dashboard.dailySummary.title')}</span>
          <span class="text-xs text-base-content/60">{t('dashboard.dailySummary.subtitle')}</span>
        </div>
        {@render navigationControls()}
      </div>
      <div class="alert alert-error">
        <XCircle class="size-6" />
        <span>{error}</span>
      </div>
    </div>
  </section>
{:else if loadingPhase === 'loaded'}
  <section class="card col-span-12 bg-base-100 shadow-sm rounded-2xl border border-border-100">
    <!-- Card Header with Date Navigation -->
    <div class="card-body grow-0 p-2 sm:p-4 sm:pt-3">
      <div class="flex items-center justify-between mb-4">
        <div class="flex flex-col">
          <span class="card-title text-base sm:text-xl">{t('dashboard.dailySummary.title')}</span>
          <span class="text-xs text-base-content/60">{t('dashboard.dailySummary.subtitle')}</span>
        </div>
        {@render navigationControls()}
      </div>

      <!-- Table Content -->
      <div class="overflow-x-auto">
        <table class="table h-full w-full table-auto daily-summary-table">
          <thead class="sticky-header text-xs">
            <!-- Daylight visualization row (sub-header) -->
            <tr class="daylight-row">
              <th
                class="py-0 px-2 sm:px-0 text-xs text-base-content/60 font-normal whitespace-nowrap"
                >{t('dashboard.dailySummary.daylight.label')}</th
              >
              {#each columns as column}
                {#if column.type === 'hourly'}
                  {@const hour = (column as HourlyColumn).hour}
                  {@const sunriseHour = sunTimes ? getSunHourFromTime(sunTimes.sunrise) : null}
                  {@const sunsetHour = sunTimes ? getSunHourFromTime(sunTimes.sunset) : null}
                  <th
                    class="py-0 px-0 text-center daylight-cell hourly-daylight"
                    class:daylight-day={isDaylightHour(hour)}
                    class:daylight-night={!isDaylightHour(hour)}
                  >
                    {#if hour === sunriseHour}
                      <span
                        class="daylight-sun-icon"
                        title={t('dashboard.dailySummary.daylight.sunrise', {
                          time: sunTimes
                            ? new Date(sunTimes.sunrise).toLocaleTimeString([], {
                                hour: '2-digit',
                                minute: '2-digit',
                              })
                            : '',
                        })}
                      >
                        <Sunrise class="size-4" />
                      </span>
                    {:else if hour === sunsetHour}
                      <span
                        class="daylight-sun-icon"
                        title={t('dashboard.dailySummary.daylight.sunset', {
                          time: sunTimes
                            ? new Date(sunTimes.sunset).toLocaleTimeString([], {
                                hour: '2-digit',
                                minute: '2-digit',
                              })
                            : '',
                        })}
                      >
                        <Sunset class="size-4" />
                      </span>
                    {/if}
                  </th>
                {:else if column.type === 'bi-hourly'}
                  {@const hour = (column as BiHourlyColumn).hour}
                  {@const sunriseHour = sunTimes ? getSunHourFromTime(sunTimes.sunrise) : null}
                  {@const sunsetHour = sunTimes ? getSunHourFromTime(sunTimes.sunset) : null}
                  {@const hasDaylight = isDaylightHour(hour) || isDaylightHour(hour + 1)}
                  <th
                    class="py-0 px-0 text-center daylight-cell bi-hourly-daylight"
                    class:daylight-day={hasDaylight}
                    class:daylight-night={!hasDaylight}
                  >
                    {#if sunriseHour !== null && hour <= sunriseHour && sunriseHour < hour + 2}
                      <span class="daylight-sun-icon">
                        <Sunrise class="size-4" />
                      </span>
                    {:else if sunsetHour !== null && hour <= sunsetHour && sunsetHour < hour + 2}
                      <span class="daylight-sun-icon">
                        <Sunset class="size-4" />
                      </span>
                    {/if}
                  </th>
                {:else if column.type === 'six-hourly'}
                  {@const hour = (column as SixHourlyColumn).hour}
                  {@const sunriseHour = sunTimes ? getSunHourFromTime(sunTimes.sunrise) : null}
                  {@const sunsetHour = sunTimes ? getSunHourFromTime(sunTimes.sunset) : null}
                  {@const hasDaylight = Array.from({ length: 6 }, (_, i) => hour + i).some(h =>
                    isDaylightHour(h)
                  )}
                  <th
                    class="py-0 px-0 text-center daylight-cell six-hourly-daylight"
                    class:daylight-day={hasDaylight}
                    class:daylight-night={!hasDaylight}
                  >
                    {#if sunriseHour !== null && hour <= sunriseHour && sunriseHour < hour + 6}
                      <span class="daylight-sun-icon">
                        <Sunrise class="size-4" />
                      </span>
                    {:else if sunsetHour !== null && hour <= sunsetHour && sunsetHour < hour + 6}
                      <span class="daylight-sun-icon">
                        <Sunset class="size-4" />
                      </span>
                    {/if}
                  </th>
                {/if}
              {/each}
            </tr>
            <!-- Hour headers row -->
            <tr>
              {#each columns as column}
                <th
                  class="py-0 {column.key === 'common_name'
                    ? 'pl-2 pr-8 sm:pl-0 sm:pr-12'
                    : 'px-2 sm:px-4'} {column.className || ''}"
                  class:hour-header={column.type === 'hourly' ||
                    column.type === 'bi-hourly' ||
                    column.type === 'six-hourly'}
                  style:text-align={column.align || 'left'}
                  scope="col"
                >
                  {#if column.key === 'common_name'}
                    {column.header}
                    {#if sortedData.length > 0}
                      <span class="species-ball bg-primary text-primary-content ml-1"
                        >{sortedData.length}</span
                      >
                    {/if}
                  {:else if column.type === 'hourly'}
                    {@const hour = (column as HourlyColumn).hour}
                    <a
                      href={urlBuilders.hourly(hour, 1)}
                      class="hover:text-primary cursor-pointer"
                      title={t('dashboard.dailySummary.tooltips.viewHourly', {
                        hour: hour.toString().padStart(2, '0'),
                      })}
                    >
                      {column.header}
                    </a>
                  {:else if column.type === 'bi-hourly'}
                    {@const hour = (column as BiHourlyColumn).hour}
                    <a
                      href={urlBuilders.hourly(hour, 2)}
                      class="hover:text-primary cursor-pointer"
                      title={t('dashboard.dailySummary.tooltips.viewBiHourly', {
                        startHour: hour.toString().padStart(2, '0'),
                        endHour: (hour + 2).toString().padStart(2, '0'),
                      })}
                    >
                      {column.header}
                    </a>
                  {:else if column.type === 'six-hourly'}
                    {@const hour = (column as SixHourlyColumn).hour}
                    <a
                      href={urlBuilders.hourly(hour, 6)}
                      class="hover:text-primary cursor-pointer"
                      title={t('dashboard.dailySummary.tooltips.viewSixHourly', {
                        startHour: hour.toString().padStart(2, '0'),
                        endHour: (hour + 6).toString().padStart(2, '0'),
                      })}
                    >
                      {column.header}
                    </a>
                  {:else}
                    {column.header}
                  {/if}
                </th>
              {/each}
            </tr>
          </thead>
          <tbody>
            {#each sortedData as item}
              <tr class="hover" class:new-species={item.isNew && !prefersReducedMotion}>
                {#each columns as column}
                  <td
                    class="py-0 px-0 {column.className || ''} {(() => {
                      // Apply heatmap color class and text-center to td for hour columns
                      let classes = [];
                      if (column.type === 'hourly') {
                        // Hourly columns
                        const hour = (column as HourlyColumn).hour;
                        const count = safeArrayAccess(item.hourly_counts, hour, 0) ?? 0;
                        classes.push('text-center', 'h-full');
                        if (count > 0) {
                          // Calculate intensity based on count and global max count
                          const intensity = Math.min(
                            9,
                            Math.floor((count / globalMaxHourlyCount) * 9)
                          );
                          classes.push(`heatmap-color-${intensity}`);
                        } else {
                          // If no detections, set intensity to 0
                          classes.push('heatmap-color-0');
                        }
                      } else if (column.type === 'bi-hourly') {
                        // Bi-hourly columns
                        const count = renderFunctions['bi-hourly'](
                          item,
                          (column as BiHourlyColumn).hour
                        );
                        classes.push('text-center', 'h-full');
                        if (count > 0) {
                          const intensity = Math.min(
                            9,
                            Math.floor((count / globalMaxBiHourlyCount) * 9)
                          );
                          classes.push(`heatmap-color-${intensity}`);
                        } else {
                          classes.push('heatmap-color-0');
                        }
                      } else if (column.type === 'six-hourly') {
                        // Six-hourly columns
                        const count = renderFunctions['six-hourly'](
                          item,
                          (column as SixHourlyColumn).hour
                        );
                        classes.push('text-center', 'h-full');
                        if (count > 0) {
                          const intensity = Math.min(
                            9,
                            Math.floor((count / globalMaxSixHourlyCount) * 9)
                          );
                          classes.push(`heatmap-color-${intensity}`);
                        } else {
                          classes.push('heatmap-color-0');
                        }
                      } else if (column.key === 'common_name') {
                        classes.push('pl-2', 'pr-8', 'sm:pl-0', 'sm:pr-12');
                      } else {
                        classes.push('px-2', 'sm:px-4');
                      }
                      return classes.join(' ');
                    })()}"
                    style:text-align={column.align || 'left'}
                  >
                    {#if column.key === 'common_name'}
                      <!-- Species thumbnail/badge and name -->
                      <div class="flex items-center gap-2">
                        {#if showThumbnails}
                          <!-- Bird thumbnail with popup -->
                          <BirdThumbnailPopup
                            thumbnailUrl={item.thumbnail_url ||
                              `/api/v2/media/species-image?name=${encodeURIComponent(item.scientific_name)}`}
                            commonName={item.common_name}
                            scientificName={item.scientific_name}
                            detectionUrl={urlBuilders.species(item)}
                          />
                        {:else}
                          <!-- Colored species badge with initials (placeholder when thumbnails disabled) -->
                          <a
                            href={urlBuilders.species(item)}
                            class="species-badge shrink-0"
                            style:background-color={getSpeciesBadgeColor(item.common_name)}
                            title={item.scientific_name}
                          >
                            {getSpeciesInitials(item.common_name)}
                          </a>
                        {/if}
                        <!-- Species name (truncated) -->
                        <a
                          href={urlBuilders.species(item)}
                          class="text-sm hover:text-primary cursor-pointer font-medium min-w-0 leading-tight flex items-center gap-1 truncate max-w-[120px] sm:max-w-[160px]"
                          title={item.common_name}
                        >
                          <span class="truncate">{item.common_name}</span>
                          <!-- Multi-period tracking badges -->
                          {#if item.is_new_species}
                            <span
                              class="text-warning inline-block shrink-0"
                              title={`New species (first seen ${item.days_since_first_seen ?? 0} day${(item.days_since_first_seen ?? 0) === 1 ? '' : 's'} ago)`}
                            >
                              <Star class="size-3 fill-current" />
                            </span>
                          {/if}
                          {#if item.is_new_this_year && !item.is_new_species}
                            <span
                              class="text-info shrink-0"
                              title={`First time this year (${item.days_this_year ?? 0} day${(item.days_this_year ?? 0) === 1 ? '' : 's'} ago)`}
                            >
                              📅
                            </span>
                          {/if}
                          {#if item.is_new_this_season && !item.is_new_species && !item.is_new_this_year}
                            <span
                              class="text-success shrink-0"
                              title={`First time this ${item.current_season || 'season'} (${item.days_this_season ?? 0} day${(item.days_this_season ?? 0) === 1 ? '' : 's'} ago)`}
                            >
                              🌿
                            </span>
                          {/if}
                        </a>
                      </div>
                    {:else if column.type === 'hourly'}
                      <!-- Hourly detections count -->
                      {@const hour = (column as HourlyColumn).hour}
                      {@const count = safeArrayAccess(item.hourly_counts, hour, 0) ?? 0}
                      {#if count > 0}
                        <a
                          href={urlBuilders.speciesHour(item, hour, 1)}
                          class="w-full h-full block text-center cursor-pointer hover:text-primary"
                          class:hour-updated={item.hourlyUpdated?.includes(hour) &&
                            !prefersReducedMotion}
                          title={t('dashboard.dailySummary.tooltips.hourlyDetections', {
                            count,
                            hour: hour.toString().padStart(2, '0'),
                          })}
                        >
                          {count}
                        </a>
                      {/if}
                    {:else if column.type === 'bi-hourly'}
                      <!-- Bi-hourly detections count -->
                      {@const hour = (column as BiHourlyColumn).hour}
                      {@const count = renderFunctions['bi-hourly'](item, hour)}
                      {#if count > 0}
                        <!-- Bi-hourly detections count link -->
                        <a
                          href={urlBuilders.speciesHour(item, hour, 2)}
                          class="w-full h-full block text-center cursor-pointer hover:text-primary"
                          title={t('dashboard.dailySummary.tooltips.biHourlyDetections', {
                            count,
                            startHour: hour.toString().padStart(2, '0'),
                            endHour: (hour + 2).toString().padStart(2, '0'),
                          })}
                        >
                          {count}
                        </a>
                      {/if}
                    {:else if column.type === 'six-hourly'}
                      <!-- Six-hourly detections count -->
                      {@const hour = (column as SixHourlyColumn).hour}
                      {@const count = renderFunctions['six-hourly'](item, hour)}
                      {#if count > 0}
                        <!-- Six-hourly detections count link -->
                        <a
                          href={urlBuilders.speciesHour(item, hour, 6)}
                          class="w-full h-full block text-center cursor-pointer hover:text-primary"
                          title={t('dashboard.dailySummary.tooltips.sixHourlyDetections', {
                            count,
                            startHour: hour.toString().padStart(2, '0'),
                            endHour: (hour + 6).toString().padStart(2, '0'),
                          })}
                        >
                          {count}
                        </a>
                      {/if}
                    {/if}
                  </td>
                {/each}
              </tr>
            {/each}
          </tbody>
        </table>
        {#if sortedData.length === 0}
          <div
            class="text-center py-8"
            style:color="color-mix(in srgb, var(--color-base-content) 60%, transparent)"
          >
            {t('dashboard.dailySummary.noSpecies')}
          </div>
        {/if}

        <!-- Heatmap Legend -->
        {#if sortedData.length > 0}
          <div class="flex justify-end items-center gap-1.5 mt-3 text-xs text-base-content/60">
            <span>{t('dashboard.dailySummary.legend.less')}</span>
            <div class="flex gap-0.5">
              {#each [0, 1, 2, 3, 4, 5, 6, 7, 8, 9] as intensity}
                <div
                  class="w-3 h-3 rounded-sm heatmap-color-{intensity}"
                  title="Intensity {intensity}"
                ></div>
              {/each}
            </div>
            <span>{t('dashboard.dailySummary.legend.more')}</span>
          </div>
        {/if}
      </div>
    </div>
  </section>
{/if}

<style>
  /* ========================================================================
     Table & Heatmap Styles (moved from custom.css)
     ======================================================================== */

  /* Performance optimization: CSS containment */
  :global(.daily-summary-table) {
    contain: layout style paint;
    border-collapse: separate;
    border-spacing: 3px 2px; /* horizontal 3px, vertical 2px for tighter rows */
  }

  /* Sticky header for tables */
  :global(thead.sticky-header) {
    position: sticky;
    top: 0;
    z-index: 10;
    height: 2rem;
    background-color: var(--fallback-b1, oklch(var(--b1) / 1));
  }

  /* Table cell display settings */
  :global(.hour-header),
  :global(.hour-data),
  :global(.hourly-count) {
    display: none;
  }

  :global(.bi-hourly-count),
  :global(.six-hourly-count) {
    display: none;
  }

  /* Empty cells should have visible background - contrasting with card */
  :global(.heatmap-color-0) {
    background-color: var(--color-base-300); /* Uses theme variable for consistency */
    border-radius: 4px;
  }

  :global([data-theme='light'] .heatmap-color-0) {
    background-color: #e2e8f0; /* slate-200 - subtle in light mode */
  }

  :global([data-theme='dark'] .heatmap-color-0) {
    background-color: #1e293b; /* slate-800 - matches mockup empty cells */
  }

  /* Flex alignment for links inside hour cells */
  :global(.hour-data a) {
    height: 1.75rem;
    min-height: 1.75rem;
    max-height: 1.75rem;
    box-sizing: border-box;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  /* Remove all row borders for clean grid look */
  :global(.daily-summary-table tr) {
    border-bottom: none !important;
  }

  :global(.daily-summary-table td),
  :global(.daily-summary-table th) {
    border-bottom: none !important;
  }

  /* Responsive table adjustments */
  /* Extra large screens (≥1400px): show hourly view and total detections */
  @media (min-width: 1400px) {
    :global(.hour-header.hourly-count),
    :global(.hour-data.hourly-count),
    :global(.hourly-count) {
      display: table-cell;
    }

    :global([class*='hidden'][class*='2xl:table-cell']) {
      display: table-cell;
    }
  }

  /* Large screens (1200px-1399px): show hourly view, hide total detections */
  @media (min-width: 1200px) and (max-width: 1399px) {
    :global(.hour-header.hourly-count),
    :global(.hour-data.hourly-count),
    :global(.hourly-count) {
      display: table-cell;
    }

    :global([class*='hidden'][class*='2xl:table-cell']) {
      display: none !important;
    }
  }

  /* Medium-large screens (1024px-1199px): show hourly view, hide total detections */
  @media (min-width: 1024px) and (max-width: 1199px) {
    :global(.hour-header.hourly-count),
    :global(.hour-data.hourly-count),
    :global(.hourly-count) {
      display: table-cell;
    }

    :global(.hour-header.hourly-count),
    :global(.hour-data.hourly-count) {
      padding-left: 0;
      padding-right: 0;
      font-size: 0.7rem;
    }

    :global([class*='hidden'][class*='2xl:table-cell']) {
      display: none !important;
    }
  }

  /* Medium screens (768px-1023px): show bi-hourly */
  @media (min-width: 768px) and (max-width: 1023px) {
    :global(.hour-header.bi-hourly),
    :global(.hour-data.bi-hourly),
    :global(.bi-hourly-count) {
      display: table-cell;
    }

    :global(.hour-header.hourly-count),
    :global(.hour-data.hourly-count),
    :global(.hourly-count) {
      display: none;
    }

    :global([class*='hidden'][class*='2xl:table-cell']) {
      display: none !important;
    }

    :global(.hour-header.bi-hourly),
    :global(.hour-data.bi-hourly) {
      padding-left: 0;
      padding-right: 0;
      font-size: 0.7rem;
    }
  }

  /* Small screens (mobile, <768px): show bi-hourly */
  @media (max-width: 767px) {
    :global(.hour-header.bi-hourly),
    :global(.hour-data.bi-hourly),
    :global(.bi-hourly-count) {
      display: table-cell;
    }

    :global([class*='hidden'][class*='2xl:table-cell']) {
      display: none !important;
    }

    :global(.hour-header.bi-hourly),
    :global(.hour-data.bi-hourly) {
      padding-left: 0;
      padding-right: 0;
    }
  }

  /* Extra small screens (<480px): show six-hourly */
  @media (max-width: 479px) {
    :global(.hour-header.bi-hourly),
    :global(.hour-data.bi-hourly),
    :global(.bi-hourly-count) {
      display: none;
    }

    :global(.hour-header.six-hourly),
    :global(.hour-data.six-hourly),
    :global(.six-hourly-count) {
      display: table-cell;
    }
  }

  /* Consistent table cell sizing - reduced for more square cells */
  :global(.hour-data) {
    height: 1.75rem;
    min-height: 1.75rem;
    max-height: 1.75rem;
    line-height: 1.75rem;
    box-sizing: border-box;
    vertical-align: middle;
    border-radius: 4px;
    padding-left: 0.1rem;
    padding-right: 0.1rem;
  }

  :global(.hour-header) {
    padding-left: 0.1rem;
    padding-right: 0.1rem;
  }

  :global(.table tr) {
    height: 1.75rem;
    min-height: 1.75rem;
    max-height: 1.75rem;
  }

  :global(.table td),
  :global(.table th) {
    box-sizing: border-box;
    height: 1.75rem;
    min-height: 1.75rem;
    max-height: 1.75rem;
    vertical-align: middle;
  }

  /* ========================================================================
     Heatmap Colors (moved from custom.css)
     ======================================================================== */

  /* Light theme heatmap colors and theme-aware variables */
  :root {
    --heatmap-color-0: #f0f9fc;
    --heatmap-color-1: #e0f3f8;
    --heatmap-color-2: #ccebf6;
    --heatmap-color-3: #99d7ed;
    --heatmap-color-4: #66c2e4;
    --heatmap-color-5: #33ade1;
    --heatmap-color-6: #0099d8;
    --heatmap-color-7: #0077be;
    --heatmap-color-8: #005595;
    --heatmap-color-9: #036;

    /* Theme-aware border colors */
    --theme-border-light: rgb(255 255 255 / 0.1);
    --theme-border-dark: rgb(0 0 0 / 0.1);

    /* Animation durations (for CSS animations) */
    --anim-count-pop: 600ms;
    --anim-heart-pulse: 1000ms;
    --anim-new-species: 800ms;
  }

  /* Dark theme heatmap colors - more vibrant and saturated */
  :global([data-theme='dark']) {
    --heatmap-color-0: #1e293b;
    --heatmap-color-1: #164e63;
    --heatmap-color-2: #0e7490;
    --heatmap-color-3: #0891b2;
    --heatmap-color-4: #06b6d4;
    --heatmap-color-5: #22d3ee;
    --heatmap-color-6: #38bdf8;
    --heatmap-color-7: #60a5fa;
    --heatmap-color-8: #93c5fd;
    --heatmap-color-9: #bfdbfe;
    --heatmap-text-1: #fff;
    --heatmap-text-2: #fff;
    --heatmap-text-3: #fff;
    --heatmap-text-4: #000;
    --heatmap-text-5: #000;
    --heatmap-text-6: #000;
    --heatmap-text-7: #000;
    --heatmap-text-8: #000;
    --heatmap-text-9: #000;
  }

  /* Heatmap cell styles - solid colors with rounded corners */
  :global(.heatmap-color-1),
  :global(.heatmap-color-2),
  :global(.heatmap-color-3),
  :global(.heatmap-color-4),
  :global(.heatmap-color-5),
  :global(.heatmap-color-6),
  :global(.heatmap-color-7),
  :global(.heatmap-color-8),
  :global(.heatmap-color-9) {
    border-radius: 4px;
  }

  :global(.heatmap-color-1) {
    background-color: var(--heatmap-color-1);
    color: var(--heatmap-text-1, #000);
  }

  :global(.heatmap-color-2) {
    background-color: var(--heatmap-color-2);
    color: var(--heatmap-text-2, #000);
  }

  :global(.heatmap-color-3) {
    background-color: var(--heatmap-color-3);
    color: var(--heatmap-text-3, #000);
  }

  :global(.heatmap-color-4) {
    background-color: var(--heatmap-color-4);
    color: var(--heatmap-text-4, #000);
  }

  :global(.heatmap-color-5) {
    background-color: var(--heatmap-color-5);
    color: var(--heatmap-text-5, #fff);
  }

  :global(.heatmap-color-6) {
    background-color: var(--heatmap-color-6);
    color: var(--heatmap-text-6, #fff);
  }

  :global(.heatmap-color-7) {
    background-color: var(--heatmap-color-7);
    color: var(--heatmap-text-7, #fff);
  }

  :global(.heatmap-color-8) {
    background-color: var(--heatmap-color-8);
    color: var(--heatmap-text-8, #fff);
  }

  :global(.heatmap-color-9) {
    background-color: var(--heatmap-color-9);
    color: var(--heatmap-text-9, #fff);
  }

  /* Dark theme text color overrides */
  :global([data-theme='dark'] .heatmap-color-1),
  :global([data-theme='dark'] .heatmap-color-2),
  :global([data-theme='dark'] .heatmap-color-3) {
    color: #fff;
  }

  :global([data-theme='dark'] .heatmap-color-4),
  :global([data-theme='dark'] .heatmap-color-5),
  :global([data-theme='dark'] .heatmap-color-6),
  :global([data-theme='dark'] .heatmap-color-7),
  :global([data-theme='dark'] .heatmap-color-8),
  :global([data-theme='dark'] .heatmap-color-9) {
    color: #000;
  }

  /* Dynamic Update Animations - not in custom.css */

  /* Count increment animation */
  @keyframes countPop {
    0% {
      transform: scale(1);
    }

    50% {
      transform: scale(1.3);
      background-color: oklch(var(--su) / 0.3);
      box-shadow: 0 0 10px oklch(var(--su) / 0.5);
    }

    100% {
      transform: scale(1);
      background-color: transparent;
    }
  }

  .count-increased {
    animation: countPop var(--anim-count-pop) cubic-bezier(0.4, 0, 0.2, 1);
  }

  /* New species row animation */
  @keyframes newSpeciesSlide {
    0% {
      transform: translateY(-30px);
      opacity: 0;
      background-color: oklch(var(--p) / 0.15);
    }

    100% {
      transform: translateY(0);
      opacity: 1;
      background-color: transparent;
    }
  }

  .new-species {
    animation: newSpeciesSlide var(--anim-new-species) cubic-bezier(0.25, 0.46, 0.45, 0.94);
  }

  /* Heatmap cell heart pulse animation */
  @keyframes heartPulse {
    0% {
      transform: scale(1);
      box-shadow: 0 0 0 0 oklch(var(--p) / 0.7);
    }

    15% {
      transform: scale(1.15);
      box-shadow: 0 0 0 4px oklch(var(--p) / 0.5);
    }

    25% {
      transform: scale(1.05);
      box-shadow: 0 0 0 6px oklch(var(--p) / 0.3);
    }

    35% {
      transform: scale(1.12);
      box-shadow: 0 0 0 8px oklch(var(--p) / 0.1);
    }

    45% {
      transform: scale(1);
      box-shadow: 0 0 0 10px oklch(var(--p) / 0);
    }

    100% {
      transform: scale(1);
      box-shadow: 0 0 0 0 oklch(var(--p) / 0);
    }
  }

  .hour-updated {
    animation: heartPulse var(--anim-heart-pulse) ease-out;
    position: relative;
    z-index: 10;
  }

  /* Respect user's reduced motion preference */
  @media (prefers-reduced-motion: reduce) {
    .count-increased,
    .new-species,
    .hour-updated {
      animation: none;
      transition: none;
    }
  }

  /* All responsive display and heatmap styles are handled by custom.css */

  /* Link styling to match the original .hour-data a styles */
  .hour-data a {
    height: 1.75rem;
    min-height: 1.75rem;
    max-height: 1.75rem;
    box-sizing: border-box;
    display: flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    border: none;
    cursor: pointer;
    color: inherit;
    font-size: inherit;
    font-family: inherit;
    text-decoration: none;
  }

  .hour-data a:hover {
    text-decoration: none;
  }

  /* Hour header styling - ensure proper table layout */
  .hour-header {
    position: relative;
    text-align: center;
    vertical-align: middle;
  }

  /* Species column styling */
  :global(.species-column) {
    width: auto;
    min-width: 0;
    max-width: 100px;
    padding-left: 0.5rem !important;
    padding-right: 0.75rem !important;
  }

  /* ========================================================================
     Species Badge Styles
     ======================================================================== */

  .species-badge {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 1.75rem;
    height: 1.75rem;
    border-radius: 0.375rem;
    font-size: 0.625rem;
    font-weight: 700;
    color: white;
    text-decoration: none;
    text-shadow: 0 1px 2px rgb(0 0 0 / 0.3);
    transition:
      transform 0.15s ease,
      box-shadow 0.15s ease;
  }

  .species-badge:hover {
    transform: scale(1.1);
    box-shadow: 0 2px 8px rgb(0 0 0 / 0.25);
  }

  /* ========================================================================
     Daylight Row Styles
     ======================================================================== */

  .daylight-row {
    height: 1.25rem;
    min-height: 1.25rem;
    max-height: 1.25rem;
  }

  .daylight-row th {
    height: 1.25rem !important;
    min-height: 1.25rem !important;
    max-height: 1.25rem !important;
    border-bottom: none !important;
    vertical-align: middle;
  }

  /* Daylight row label cell */
  .daylight-row th:first-child {
    padding-left: 0.5rem;
    padding-right: 1rem;
    text-align: left;
  }

  .daylight-cell {
    position: relative;
    transition: background-color 0.2s ease;
  }

  /* Daylight cell responsive visibility - separate from hour-header/hour-data */
  .hourly-daylight,
  .bi-hourly-daylight,
  .six-hourly-daylight {
    display: none;
  }

  /* Extra large screens (≥1400px): show hourly daylight */
  @media (min-width: 1400px) {
    .hourly-daylight {
      display: table-cell;
    }
  }

  /* Large screens (1200px-1399px): show hourly daylight */
  @media (min-width: 1200px) and (max-width: 1399px) {
    .hourly-daylight {
      display: table-cell;
    }
  }

  /* Medium-large screens (1024px-1199px): show hourly daylight */
  @media (min-width: 1024px) and (max-width: 1199px) {
    .hourly-daylight {
      display: table-cell;
    }
  }

  /* Medium screens (768px-1023px): show bi-hourly daylight */
  @media (min-width: 768px) and (max-width: 1023px) {
    .bi-hourly-daylight {
      display: table-cell;
    }
  }

  /* Small screens (480px-767px): show bi-hourly daylight */
  @media (min-width: 480px) and (max-width: 767px) {
    .bi-hourly-daylight {
      display: table-cell;
    }
  }

  /* Extra small screens (<480px): show six-hourly daylight */
  @media (max-width: 479px) {
    .six-hourly-daylight {
      display: table-cell;
    }
  }

  .daylight-day {
    background-color: #fbbf24; /* amber-400 */
    border-radius: 4px;
  }

  .daylight-night {
    background-color: #4b5563; /* gray-600 - more visible */
    border-radius: 4px;
  }

  :global([data-theme='light']) .daylight-day {
    background-color: #fcd34d; /* amber-300 */
  }

  :global([data-theme='light']) .daylight-night {
    background-color: #9ca3af; /* gray-400 - more visible in light mode */
  }

  .daylight-sun-icon {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    height: 100%;
    color: #1f2937; /* dark gray for visibility against amber */
  }

  :global([data-theme='dark']) .daylight-sun-icon {
    color: #1f2937; /* dark gray for visibility against amber in dark mode */
  }
</style>
