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

  // Get daylight class for an hour based on its position relative to sunrise/sunset
  // Returns: 'deep-night', 'night', 'pre-dawn', 'sunrise', 'early-day', 'mid-day', 'late-day', 'sunset', 'dusk', 'evening'
  const getDaylightClass = (hour: number): string => {
    if (!sunTimes) return 'night';
    const sunriseHour = getSunHourFromTime(sunTimes.sunrise);
    const sunsetHour = getSunHourFromTime(sunTimes.sunset);
    if (sunriseHour === null || sunsetHour === null) return 'night';

    // Sunrise hour - special gradient
    if (hour === sunriseHour) return 'sunrise';
    // Sunset hour - special gradient
    if (hour === sunsetHour) return 'sunset';

    // Pre-dawn (1-2 hours before sunrise)
    if (hour >= sunriseHour - 2 && hour < sunriseHour) return 'pre-dawn';

    // Dusk (1-2 hours after sunset)
    if (hour > sunsetHour && hour <= sunsetHour + 2) return 'dusk';

    // Daylight hours
    if (hour > sunriseHour && hour < sunsetHour) {
      const midday = (sunriseHour + sunsetHour) / 2;
      const distanceFromMidday = Math.abs(hour - midday);
      const daylightDuration = (sunsetHour - sunriseHour) / 2;

      // Categorize daylight intensity
      if (distanceFromMidday < daylightDuration * 0.3) return 'mid-day';
      if (distanceFromMidday < daylightDuration * 0.7) return 'day';
      return hour < midday ? 'early-day' : 'late-day';
    }

    // Night hours - vary by distance from midnight
    if (hour >= 0 && hour <= 4) return 'deep-night';
    if (hour >= 21 && hour <= 23) return 'deep-night';
    if (hour === 5 || hour === 20) return 'night';
    return 'evening';
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
  <section
    class="card col-span-12 bg-base-100 shadow-sm rounded-2xl border border-border-100 overflow-hidden"
  >
    <div class="px-6 py-4 border-b border-base-200">
      <div class="flex items-center justify-between">
        <div class="flex flex-col">
          <h3 class="font-semibold">{t('dashboard.dailySummary.title')}</h3>
          <p class="text-sm" style:color="#94a3b8">{t('dashboard.dailySummary.subtitle')}</p>
        </div>
        {@render navigationControls()}
      </div>
    </div>
    <div class="p-6">
      <div class="alert alert-error">
        <XCircle class="size-6" />
        <span>{error}</span>
      </div>
    </div>
  </section>
{:else if loadingPhase === 'loaded'}
  <section class="card col-span-12 bg-base-100 shadow-sm rounded-2xl border border-border-100">
    <!-- Card Header with Date Navigation -->
    <div class="px-6 py-4 border-b border-base-200">
      <div class="flex items-center justify-between">
        <div class="flex flex-col">
          <h3 class="font-semibold">{t('dashboard.dailySummary.title')}</h3>
          <p class="text-sm" style:color="#94a3b8">{t('dashboard.dailySummary.subtitle')}</p>
        </div>
        {@render navigationControls()}
      </div>
    </div>

    <!-- Grid Content -->
    <div class="p-6 pt-8">
      <div class="overflow-x-auto overflow-y-visible">
        <div class="daily-summary-grid min-w-[900px]">
          <!-- Daylight visualization row -->
          <div class="flex mb-1">
            <div class="species-label-col shrink-0 flex items-center">
              <span class="text-xs text-base-content/60 font-normal whitespace-nowrap"
                >{t('dashboard.dailySummary.daylight.label')}</span
              >
            </div>
            <!-- Hourly daylight (desktop) -->
            <div class="hourly-grid flex-1 grid">
              {#each Array(24) as _, hour}
                {@const sunriseHour = sunTimes ? getSunHourFromTime(sunTimes.sunrise) : null}
                {@const sunsetHour = sunTimes ? getSunHourFromTime(sunTimes.sunset) : null}
                {@const daylightClass = getDaylightClass(hour)}
                <div
                  class="h-5 rounded-sm daylight-cell daylight-{daylightClass} relative flex items-center justify-center"
                >
                  {#if hour === sunriseHour}
                    {@const sunriseTime = sunTimes
                      ? new Date(sunTimes.sunrise).toLocaleTimeString([], {
                          hour: '2-digit',
                          minute: '2-digit',
                        })
                      : ''}
                    <div
                      class="sun-icon-wrapper"
                      title={t('dashboard.dailySummary.daylight.sunrise', { time: sunriseTime })}
                    >
                      <Sunrise class="size-3.5 text-orange-700" />
                      <span class="sun-tooltip sun-tooltip-sunrise">{sunriseTime}</span>
                    </div>
                  {:else if hour === sunsetHour}
                    {@const sunsetTime = sunTimes
                      ? new Date(sunTimes.sunset).toLocaleTimeString([], {
                          hour: '2-digit',
                          minute: '2-digit',
                        })
                      : ''}
                    <div
                      class="sun-icon-wrapper"
                      title={t('dashboard.dailySummary.daylight.sunset', { time: sunsetTime })}
                    >
                      <Sunset class="size-3.5 text-rose-700" />
                      <span class="sun-tooltip sun-tooltip-sunset">{sunsetTime}</span>
                    </div>
                  {/if}
                </div>
              {/each}
            </div>
            <!-- Bi-hourly daylight (tablet/mobile) -->
            <div class="bi-hourly-grid flex-1 grid">
              {#each Array(12) as _, i}
                {@const hour = i * 2}
                {@const sunriseHour = sunTimes ? getSunHourFromTime(sunTimes.sunrise) : null}
                {@const sunsetHour = sunTimes ? getSunHourFromTime(sunTimes.sunset) : null}
                {@const daylightClass = getDaylightClass(hour)}
                <div
                  class="h-5 rounded-sm daylight-cell daylight-{daylightClass} relative flex items-center justify-center"
                >
                  {#if sunriseHour !== null && hour <= sunriseHour && sunriseHour < hour + 2}
                    {@const sunriseTime = sunTimes
                      ? new Date(sunTimes.sunrise).toLocaleTimeString([], {
                          hour: '2-digit',
                          minute: '2-digit',
                        })
                      : ''}
                    <div
                      class="sun-icon-wrapper"
                      title={t('dashboard.dailySummary.daylight.sunrise', { time: sunriseTime })}
                    >
                      <Sunrise class="size-3.5 text-orange-700" />
                      <span class="sun-tooltip sun-tooltip-sunrise">{sunriseTime}</span>
                    </div>
                  {:else if sunsetHour !== null && hour <= sunsetHour && sunsetHour < hour + 2}
                    {@const sunsetTime = sunTimes
                      ? new Date(sunTimes.sunset).toLocaleTimeString([], {
                          hour: '2-digit',
                          minute: '2-digit',
                        })
                      : ''}
                    <div
                      class="sun-icon-wrapper"
                      title={t('dashboard.dailySummary.daylight.sunset', { time: sunsetTime })}
                    >
                      <Sunset class="size-3.5 text-rose-700" />
                      <span class="sun-tooltip sun-tooltip-sunset">{sunsetTime}</span>
                    </div>
                  {/if}
                </div>
              {/each}
            </div>
            <!-- Six-hourly daylight (small mobile) -->
            <div class="six-hourly-grid flex-1 grid">
              {#each Array(4) as _, i}
                {@const hour = i * 6}
                {@const sunriseHour = sunTimes ? getSunHourFromTime(sunTimes.sunrise) : null}
                {@const sunsetHour = sunTimes ? getSunHourFromTime(sunTimes.sunset) : null}
                {@const daylightClass = getDaylightClass(hour)}
                <div
                  class="h-5 rounded-sm daylight-cell daylight-{daylightClass} relative flex items-center justify-center"
                >
                  {#if sunriseHour !== null && hour <= sunriseHour && sunriseHour < hour + 6}
                    {@const sunriseTime = sunTimes
                      ? new Date(sunTimes.sunrise).toLocaleTimeString([], {
                          hour: '2-digit',
                          minute: '2-digit',
                        })
                      : ''}
                    <div
                      class="sun-icon-wrapper"
                      title={t('dashboard.dailySummary.daylight.sunrise', { time: sunriseTime })}
                    >
                      <Sunrise class="size-3.5 text-orange-700" />
                      <span class="sun-tooltip sun-tooltip-sunrise">{sunriseTime}</span>
                    </div>
                  {:else if sunsetHour !== null && hour <= sunsetHour && sunsetHour < hour + 6}
                    {@const sunsetTime = sunTimes
                      ? new Date(sunTimes.sunset).toLocaleTimeString([], {
                          hour: '2-digit',
                          minute: '2-digit',
                        })
                      : ''}
                    <div
                      class="sun-icon-wrapper"
                      title={t('dashboard.dailySummary.daylight.sunset', { time: sunsetTime })}
                    >
                      <Sunset class="size-3.5 text-rose-700" />
                      <span class="sun-tooltip sun-tooltip-sunset">{sunsetTime}</span>
                    </div>
                  {/if}
                </div>
              {/each}
            </div>
          </div>

          <!-- Hours header row -->
          <div class="flex mb-1">
            <div class="species-label-col shrink-0"></div>
            <!-- Hourly headers (desktop) -->
            <div class="hourly-grid flex-1 grid text-xs">
              {#each Array(24) as _, hour}
                <a
                  href={urlBuilders.hourly(hour, 1)}
                  class="text-center hover:text-primary cursor-pointer"
                  style:color="color-mix(in srgb, var(--color-base-content) 50%, transparent)"
                  title={t('dashboard.dailySummary.tooltips.viewHourly', {
                    hour: hour.toString().padStart(2, '0'),
                  })}
                >
                  {hour.toString().padStart(2, '0')}
                </a>
              {/each}
            </div>
            <!-- Bi-hourly headers (tablet/mobile) -->
            <div class="bi-hourly-grid flex-1 grid text-xs">
              {#each Array(12) as _, i}
                {@const hour = i * 2}
                <a
                  href={urlBuilders.hourly(hour, 2)}
                  class="text-center hover:text-primary cursor-pointer"
                  style:color="color-mix(in srgb, var(--color-base-content) 50%, transparent)"
                  title={t('dashboard.dailySummary.tooltips.viewBiHourly', {
                    startHour: hour.toString().padStart(2, '0'),
                    endHour: (hour + 2).toString().padStart(2, '0'),
                  })}
                >
                  {hour.toString().padStart(2, '0')}
                </a>
              {/each}
            </div>
            <!-- Six-hourly headers (small mobile) -->
            <div class="six-hourly-grid flex-1 grid text-xs">
              {#each Array(4) as _, i}
                {@const hour = i * 6}
                <a
                  href={urlBuilders.hourly(hour, 6)}
                  class="text-center hover:text-primary cursor-pointer"
                  style:color="color-mix(in srgb, var(--color-base-content) 50%, transparent)"
                  title={t('dashboard.dailySummary.tooltips.viewSixHourly', {
                    startHour: hour.toString().padStart(2, '0'),
                    endHour: (hour + 6).toString().padStart(2, '0'),
                  })}
                >
                  {hour.toString().padStart(2, '0')}
                </a>
              {/each}
            </div>
          </div>

          <!-- Species rows -->
          <div class="space-y-0">
            {#each sortedData as item}
              <div
                class="flex items-center species-row"
                class:new-species={item.isNew && !prefersReducedMotion}
              >
                <!-- Species info column -->
                <div class="species-label-col shrink-0 flex items-center gap-2 pr-4">
                  {#if showThumbnails}
                    <BirdThumbnailPopup
                      thumbnailUrl={item.thumbnail_url ||
                        `/api/v2/media/species-image?name=${encodeURIComponent(item.scientific_name)}`}
                      commonName={item.common_name}
                      scientificName={item.scientific_name}
                      detectionUrl={urlBuilders.species(item)}
                    />
                  {:else}
                    <a
                      href={urlBuilders.species(item)}
                      class="species-badge shrink-0"
                      style:background-color={getSpeciesBadgeColor(item.common_name)}
                      title={item.scientific_name}
                    >
                      {getSpeciesInitials(item.common_name)}
                    </a>
                  {/if}
                  <a
                    href={urlBuilders.species(item)}
                    class="text-sm hover:text-primary cursor-pointer font-medium min-w-0 leading-tight flex items-center gap-1 truncate"
                    title={item.common_name}
                  >
                    <span class="truncate">{item.common_name}</span>
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

                <!-- Hourly heatmap cells (desktop) -->
                <div class="hourly-grid flex-1 grid">
                  {#each Array(24) as _, hour}
                    {@const count = safeArrayAccess(item.hourly_counts, hour, 0) ?? 0}
                    {@const intensity =
                      count > 0 ? Math.min(9, Math.floor((count / globalMaxHourlyCount) * 9)) : 0}
                    <div
                      class="heatmap-cell h-8 rounded-sm heatmap-color-{intensity} flex items-center justify-center text-xs font-medium"
                      class:hour-updated={item.hourlyUpdated?.includes(hour) &&
                        !prefersReducedMotion}
                    >
                      {#if count > 0}
                        <a
                          href={urlBuilders.speciesHour(item, hour, 1)}
                          class="w-full h-full flex items-center justify-center cursor-pointer hover:opacity-80"
                          title={t('dashboard.dailySummary.tooltips.hourlyDetections', {
                            count,
                            hour: hour.toString().padStart(2, '0'),
                          })}
                        >
                          {count}
                        </a>
                      {/if}
                    </div>
                  {/each}
                </div>

                <!-- Bi-hourly heatmap cells (tablet/mobile) -->
                <div class="bi-hourly-grid flex-1 grid">
                  {#each Array(12) as _, i}
                    {@const hour = i * 2}
                    {@const count = renderFunctions['bi-hourly'](item, hour)}
                    {@const intensity =
                      count > 0 ? Math.min(9, Math.floor((count / globalMaxBiHourlyCount) * 9)) : 0}
                    <div
                      class="heatmap-cell h-8 rounded-sm heatmap-color-{intensity} flex items-center justify-center text-xs font-medium"
                    >
                      {#if count > 0}
                        <a
                          href={urlBuilders.speciesHour(item, hour, 2)}
                          class="w-full h-full flex items-center justify-center cursor-pointer hover:opacity-80"
                          title={t('dashboard.dailySummary.tooltips.biHourlyDetections', {
                            count,
                            startHour: hour.toString().padStart(2, '0'),
                            endHour: (hour + 2).toString().padStart(2, '0'),
                          })}
                        >
                          {count}
                        </a>
                      {/if}
                    </div>
                  {/each}
                </div>

                <!-- Six-hourly heatmap cells (small mobile) -->
                <div class="six-hourly-grid flex-1 grid">
                  {#each Array(4) as _, i}
                    {@const hour = i * 6}
                    {@const count = renderFunctions['six-hourly'](item, hour)}
                    {@const intensity =
                      count > 0
                        ? Math.min(9, Math.floor((count / globalMaxSixHourlyCount) * 9))
                        : 0}
                    <div
                      class="heatmap-cell h-8 rounded-sm heatmap-color-{intensity} flex items-center justify-center text-xs font-medium"
                    >
                      {#if count > 0}
                        <a
                          href={urlBuilders.speciesHour(item, hour, 6)}
                          class="w-full h-full flex items-center justify-center cursor-pointer hover:opacity-80"
                          title={t('dashboard.dailySummary.tooltips.sixHourlyDetections', {
                            count,
                            startHour: hour.toString().padStart(2, '0'),
                            endHour: (hour + 6).toString().padStart(2, '0'),
                          })}
                        >
                          {count}
                        </a>
                      {/if}
                    </div>
                  {/each}
                </div>
              </div>
            {/each}
          </div>
        </div>

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
     CSS Custom Properties for Daily Summary Grid
     ======================================================================== */
  :root {
    /* Grid layout properties */
    --grid-cell-height: 1.25rem;
    --grid-cell-radius: 4px;
    --grid-gap: 4px; /* Gap between grid cells */

    /* Light theme heatmap colors */
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

    /* Animation durations */
    --anim-count-pop: 600ms;
    --anim-heart-pulse: 1000ms;
    --anim-new-species: 800ms;
  }

  /* ========================================================================
     CSS Grid Layout Styles
     ======================================================================== */

  /* Species label column - fixed width like mockup */
  .species-label-col {
    width: 10rem; /* w-40 equivalent */
  }

  /* CSS Grid for hour columns - equal columns using minmax(0, 1fr) */
  /* Default: show hourly (desktop), hide bi-hourly and six-hourly */
  .hourly-grid {
    display: grid;
    grid-template-columns: repeat(24, minmax(0, 1fr));
    gap: var(--grid-gap);
  }

  .bi-hourly-grid {
    display: none;
    grid-template-columns: repeat(12, minmax(0, 1fr));
    gap: var(--grid-gap);
  }

  .six-hourly-grid {
    display: none;
    grid-template-columns: repeat(4, minmax(0, 1fr));
    gap: var(--grid-gap);
  }

  /* Heatmap cell base styles */
  .heatmap-cell {
    transition:
      opacity 0.15s ease,
      transform 0.15s ease;
  }

  .heatmap-cell a {
    color: inherit;
    text-decoration: none;
  }

  /* Species row hover effect */
  .species-row {
    border-radius: var(--grid-cell-radius);
    transition: background-color 0.15s ease;
  }

  .species-row:hover {
    background-color: var(--hover-overlay);
  }

  /* Empty cells background */
  :global(.heatmap-color-0) {
    background-color: var(--color-base-300);
    border-radius: var(--grid-cell-radius);
  }

  :global([data-theme='light'] .heatmap-color-0) {
    background-color: #e2e8f0;
  }

  :global([data-theme='dark'] .heatmap-color-0) {
    background-color: #1e293b;
  }

  /* ========================================================================
     Responsive Grid Display
     ======================================================================== */

  /* Tablet (768-1023px): show bi-hourly */
  @media (min-width: 768px) and (max-width: 1023px) {
    .hourly-grid {
      display: none;
    }

    .bi-hourly-grid {
      display: grid;
    }

    .six-hourly-grid {
      display: none;
    }
  }

  /* Mobile (<768px): show bi-hourly */
  @media (max-width: 767px) {
    .hourly-grid {
      display: none;
    }

    .bi-hourly-grid {
      display: grid;
    }

    .six-hourly-grid {
      display: none;
    }
  }

  /* Small mobile (<480px): show six-hourly */
  @media (max-width: 479px) {
    .hourly-grid,
    .bi-hourly-grid {
      display: none;
    }

    .six-hourly-grid {
      display: grid;
    }
  }

  /* ========================================================================
     Heatmap Colors
     ======================================================================== */

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
    border-radius: var(--grid-cell-radius);
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

  /* ========================================================================
     Species Column & Badge Styles
     ======================================================================== */

  :global(.species-column) {
    width: auto;
    min-width: 0;
    max-width: 100px;
    padding: 0 0.75rem 0 0.5rem !important;
  }

  .species-badge {
    display: flex;
    align-items: center;
    justify-content: center;
    width: var(--grid-cell-height);
    height: var(--grid-cell-height);
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

  .daylight-cell {
    position: relative;
    transition: background-color 0.2s ease;
    overflow: visible;
  }

  :global(.overflow-y-visible) {
    overflow-y: visible !important;
  }

  /* ========================================================================
     Daylight Color Classes - Gradual shading from night to day
     ======================================================================== */

  /* Deep night (midnight - 4am, 9pm - midnight) - darkest indigo */
  .daylight-deep-night {
    background-color: rgb(30 27 75 / 0.5); /* indigo-950/50 */
    border-radius: var(--grid-cell-radius);
  }

  :global([data-theme='light']) .daylight-deep-night {
    background-color: rgb(30 27 75 / 0.3); /* indigo-950/30 */
  }

  /* Night (5am, 8pm) - lighter indigo */
  .daylight-night {
    background-color: rgb(49 46 129 / 0.4); /* indigo-900/40 */
    border-radius: var(--grid-cell-radius);
  }

  :global([data-theme='light']) .daylight-night {
    background-color: rgb(49 46 129 / 0.2); /* indigo-900/20 */
  }

  /* Evening (6-7pm) - transition indigo */
  .daylight-evening {
    background-color: rgb(67 56 202 / 0.3); /* indigo-700/30 */
    border-radius: var(--grid-cell-radius);
  }

  :global([data-theme='light']) .daylight-evening {
    background-color: rgb(67 56 202 / 0.15); /* indigo-700/15 */
  }

  /* Pre-dawn (1-2 hours before sunrise) - transitional purple/indigo */
  .daylight-pre-dawn {
    background-color: rgb(99 102 241 / 0.3); /* indigo-500/30 */
    border-radius: var(--grid-cell-radius);
  }

  :global([data-theme='light']) .daylight-pre-dawn {
    background-color: rgb(99 102 241 / 0.2); /* indigo-500/20 */
  }

  /* Sunrise - gradient from orange to amber */
  .daylight-sunrise {
    background: linear-gradient(to right, #fb923c, #fbbf24); /* orange-400 to amber-400 */
    border-radius: var(--grid-cell-radius);
  }

  :global([data-theme='light']) .daylight-sunrise {
    background: linear-gradient(to right, #f97316, #fcd34d); /* orange-500 to amber-300 */
  }

  /* Early day (just after sunrise) - soft warm amber */
  .daylight-early-day {
    background-color: rgb(251 191 36 / 0.4); /* amber-400/40 */
    border-radius: var(--grid-cell-radius);
  }

  :global([data-theme='light']) .daylight-early-day {
    background-color: rgb(252 211 77 / 0.6); /* amber-300/60 */
  }

  /* Day (mid-morning, mid-afternoon) - medium amber */
  .daylight-day {
    background-color: rgb(251 191 36 / 0.5); /* amber-400/50 */
    border-radius: var(--grid-cell-radius);
  }

  :global([data-theme='light']) .daylight-day {
    background-color: rgb(252 211 77 / 0.7); /* amber-300/70 */
  }

  /* Mid-day (peak daylight) - brightest amber/yellow */
  .daylight-mid-day {
    background-color: rgb(253 224 71 / 0.6); /* yellow-300/60 */
    border-radius: var(--grid-cell-radius);
  }

  :global([data-theme='light']) .daylight-mid-day {
    background-color: rgb(254 240 138 / 0.8); /* yellow-200/80 */
  }

  /* Late day (before sunset) - soft warm amber */
  .daylight-late-day {
    background-color: rgb(251 191 36 / 0.4); /* amber-400/40 */
    border-radius: var(--grid-cell-radius);
  }

  :global([data-theme='light']) .daylight-late-day {
    background-color: rgb(252 211 77 / 0.6); /* amber-300/60 */
  }

  /* Sunset - gradient from rose to purple */
  .daylight-sunset {
    background: linear-gradient(to right, #fda4af, #c084fc); /* rose-300 to purple-400 */
    border-radius: var(--grid-cell-radius);
  }

  :global([data-theme='light']) .daylight-sunset {
    background: linear-gradient(to right, #fb7185, #a855f7); /* rose-400 to purple-500 */
  }

  /* Dusk (1-2 hours after sunset) - transitional purple */
  .daylight-dusk {
    background-color: rgb(139 92 246 / 0.25); /* violet-500/25 */
    border-radius: var(--grid-cell-radius);
  }

  :global([data-theme='light']) .daylight-dusk {
    background-color: rgb(139 92 246 / 0.15); /* violet-500/15 */
  }

  /* Sun icon wrapper and tooltip styles */
  .sun-icon-wrapper {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    height: 1.25rem; /* 20px - matches grid-daylight-height */
    position: relative;
    cursor: pointer;
  }

  .sun-tooltip {
    position: absolute;
    bottom: 100%;
    left: 50%;
    transform: translateX(-50%);
    margin-bottom: 4px;
    padding: 2px 6px;
    font-size: 10px;
    font-weight: 600;
    white-space: nowrap;
    border-radius: 4px;
    opacity: 0;
    visibility: hidden;
    transition:
      opacity 0.15s ease-in-out,
      visibility 0.15s ease-in-out;
    pointer-events: none;

    /* Force a new stacking context to escape sticky header */
    isolation: isolate;
    z-index: 9999;
  }

  .sun-icon-wrapper:hover .sun-tooltip {
    opacity: 1;
    visibility: visible;
  }

  /* Sunrise tooltip - orange theme */
  .sun-tooltip-sunrise {
    background-color: #fff7ed; /* orange-50 */
    color: #c2410c; /* orange-700 */
    border: 1px solid #fed7aa; /* orange-200 */
    box-shadow: 0 2px 8px rgb(251 146 60 / 0.25);
  }

  :global([data-theme='dark']) .sun-tooltip-sunrise {
    background-color: #431407; /* orange-950 */
    color: #fdba74; /* orange-300 */
    border: 1px solid #7c2d12; /* orange-900 */
  }

  /* Sunset tooltip - rose/pink theme */
  .sun-tooltip-sunset {
    background-color: #fff1f2; /* rose-50 */
    color: #be123c; /* rose-700 */
    border: 1px solid #fecdd3; /* rose-200 */
    box-shadow: 0 2px 8px rgb(251 113 133 / 0.25);
  }

  :global([data-theme='dark']) .sun-tooltip-sunset {
    background-color: #4c0519; /* rose-950 */
    color: #fda4af; /* rose-300 */
    border: 1px solid #881337; /* rose-900 */
  }
</style>
