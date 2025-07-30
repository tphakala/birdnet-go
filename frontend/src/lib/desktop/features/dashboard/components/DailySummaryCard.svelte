<script lang="ts">
  import { untrack } from 'svelte';
  import DatePicker from '$lib/desktop/components/ui/DatePicker.svelte';
  import type { DailySpeciesSummary } from '$lib/types/detection.types';
  import { alertIconsSvg, navigationIcons, weatherIcons, systemIcons } from '$lib/utils/icons'; // Centralized icons - see icons.ts
  import { t } from '$lib/i18n';
  import BirdThumbnailPopup from './BirdThumbnailPopup.svelte';
  import SkeletonDailySummary from '$lib/desktop/components/ui/SkeletonDailySummary.svelte';
  import { getLocalDateString } from '$lib/utils/date';

  // Progressive loading timing constants (optimized for Svelte 5)
  const LOADING_PHASES = $state.raw({
    skeleton: 0,     // 0ms - show skeleton immediately to reserve space
    spinner: 650,    // 650ms - show spinner if still loading
  });

  // Layout constants - use $state.raw for static values
  const PROGRESS_BAR_ROUNDING = 5; // Round to nearest 5%
  const WIDTH_THRESHOLDS = $state.raw({
    minTextDisplay: 45,
    maxTextDisplay: 59,
  });

  interface SunTimes {
    sunrise: string; // ISO date string
    sunset: string; // ISO date string
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

  // Cache for sun times to avoid repeated API calls - use $state.raw for performance
  const sunTimesCache = $state.raw(new Map<string, SunTimes>());

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
    // Check cache first
    const cached = sunTimesCache.get(date);
    if (cached) {
      return cached;
    }


    try {
      const response = await fetch(`/api/v2/weather/sun/${date}`);
      if (!response.ok) {
        const errorMsg = `Failed to fetch sun times: ${response.status} ${response.statusText}`;
        console.warn(errorMsg);
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
      console.warn('Error fetching sun times:', errorMsg);
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
      console.error('Error parsing time:', timeStr, error);
      return null;
    }
  };

  // Static column metadata - use $state.raw() for performance (no deep reactivity needed)
  const staticColumnDefs = $state.raw([
    {
      key: 'common_name',
      type: 'species',
      sortable: true,
      className: 'font-medium w-0 whitespace-nowrap',
    },
    {
      key: 'total_detections',
      type: 'progress',
      align: 'center',
      className: 'hidden 2xl:table-cell px-2 sm:px-4 w-100',
    },
    ...Array.from({ length: 24 }, (_, hour) => ({
      key: `hour_${hour}`,
      type: 'hourly',
      hour,
      header: hour.toString().padStart(2, '0'),
      align: 'center',
      className: 'hour-data hourly-count px-0',
    })),
    ...Array.from({ length: 12 }, (_, i) => {
      const hour = i * 2;
      return {
        key: `bi_hour_${hour}`,
        type: 'bi-hourly',
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
        type: 'six-hourly',
        hour,
        header: hour.toString().padStart(2, '0'),
        align: 'center',
        className: 'hour-data six-hourly-count six-hourly px-0',
      };
    }),
  ]);

  // Reactive columns with only dynamic headers - use $derived.by for complex logic
  const columns = $derived.by(() => {
    // Early return for empty data to prevent unnecessary calculations
    if (staticColumnDefs.length === 0) return [];
    
    return staticColumnDefs.map(colDef => ({
      ...colDef,
      header:
        colDef.type === 'species'
          ? t('dashboard.dailySummary.columns.species')
          : colDef.type === 'progress'
            ? t('dashboard.dailySummary.columns.detections')
            : colDef.header,
    }));
  });

  // Pre-computed render functions - use $state.raw for performance (static functions)
  const renderFunctions = $state.raw({
    hourly: (item: DailySpeciesSummary, hour: number) => item.hourly_counts[hour] || 0,
    'bi-hourly': (item: DailySpeciesSummary, hour: number) =>
      (item.hourly_counts[hour] || 0) + (item.hourly_counts[hour + 1] || 0),
    'six-hourly': (item: DailySpeciesSummary, hour: number) => {
      let sum = 0;
      for (let h = hour; h < hour + 6 && h < 24; h++) {
        sum += item.hourly_counts[h] || 0;
      }
      return sum;
    },
    progress: (item: DailySpeciesSummary) => item.count,
  });

  // Simple LRU cache implementation
  class LRUCache<K, V> {
    private cache: Map<K, V> = new Map();
    private readonly maxSize: number;

    constructor(maxSize: number) {
      this.maxSize = maxSize;
    }

    get(key: K): V | undefined {
      if (!this.cache.has(key)) return undefined;

      // Move to end (most recently used)
      const value = this.cache.get(key)!;
      this.cache.delete(key);
      this.cache.set(key, value);
      return value;
    }

    set(key: K, value: V): void {
      // If key exists, delete it to update position
      if (this.cache.has(key)) {
        this.cache.delete(key);
      } else if (this.cache.size >= this.maxSize) {
        // Remove least recently used (first item)
        const firstKey = this.cache.keys().next().value;
        this.cache.delete(firstKey);
      }

      // Add to end (most recently used)
      this.cache.set(key, value);
    }

    has(key: K): boolean {
      return this.cache.has(key);
    }

    clear(): void {
      this.cache.clear();
    }

    get size(): number {
      return this.cache.size;
    }
  }

  // Phase 4: Optimized URL building with memoization for 90%+ performance improvement
  const urlCache = $state.raw(new LRUCache<string, string>(500)); // Max 500 URLs cached - use $state.raw
  const urlBuilders = $state({
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
  const sortedData = $derived.by(() => {
    // Early return for empty data
    if (data.length === 0) return [];
    
    // Use toSorted (immutable) instead of spreading + sort for better performance
    return data.toSorted((a, b) => b.count - a.count);
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
    sortedData.forEach(species => {
      for (let hour = 0; hour < 24; hour += 2) {
        const sum = (species.hourly_counts[hour] || 0) + (species.hourly_counts[hour + 1] || 0);
        maxCount = Math.max(maxCount, sum);
      }
    });
    return maxCount || 1;
  });

  const globalMaxSixHourlyCount = $derived.by(() => {
    if (sortedData.length === 0) return 1;

    let maxCount = 0;
    sortedData.forEach(species => {
      for (let hour = 0; hour < 24; hour += 6) {
        let sum = 0;
        for (let h = hour; h < hour + 6 && h < 24; h++) {
          sum += species.hourly_counts[h] || 0;
        }
        maxCount = Math.max(maxCount, sum);
      }
    });
    return maxCount || 1;
  });
</script>

<!-- Progressive loading implementation -->
{#if loadingPhase === 'skeleton'}
  <SkeletonDailySummary {showThumbnails} speciesCount={8} />
{:else if loadingPhase === 'spinner'}
  <SkeletonDailySummary {showThumbnails} showSpinner={showDelayedIndicator} speciesCount={8} />
{:else if loadingPhase === 'error'}
  <section class="card col-span-12 bg-base-100 shadow-sm">
    <div class="card-body grow-0 p-2 sm:p-4 sm:pt-3">
      <div class="flex items-center justify-between mb-4">
        <span class="card-title grow text-base sm:text-xl">{t('dashboard.dailySummary.title')} </span>
        <div class="flex items-center gap-2">
          <!-- Previous day button -->
          <button
            onclick={onPreviousDay}
            class="btn btn-sm btn-ghost"
            aria-label={t('dashboard.dailySummary.navigation.previousDay')}
          >
            {@html navigationIcons.arrowLeft}
          </button>

          <!-- Date picker -->
          <DatePicker value={selectedDate} onChange={onDateChange} className="mx-2" />

          <!-- Next day button -->
          <button
            onclick={onNextDay}
            class="btn btn-sm btn-ghost"
            disabled={isToday}
            aria-label={t('dashboard.dailySummary.navigation.nextDay')}
          >
            {@html navigationIcons.arrowRight}
          </button>

          {#if !isToday}
            <button onclick={onGoToToday} class="btn btn-sm btn-primary"
              >{t('dashboard.dailySummary.navigation.today')}</button
            >
          {/if}
        </div>
      </div>
      <div class="alert alert-error">
        {@html alertIconsSvg.error}
        <span>{error}</span>
      </div>
    </div>
  </section>
{:else if loadingPhase === 'loaded'}
  <section class="card col-span-12 bg-base-100 shadow-sm">
    <!-- Card Header with Date Navigation -->
    <div class="card-body grow-0 p-2 sm:p-4 sm:pt-3">
      <div class="flex items-center justify-between mb-4">
        <span class="card-title grow text-base sm:text-xl">{t('dashboard.dailySummary.title')} </span>
        <div class="flex items-center gap-2">
          <!-- Previous day button -->
          <button
            onclick={onPreviousDay}  
            class="btn btn-sm btn-ghost"
            aria-label={t('dashboard.dailySummary.navigation.previousDay')}
          >
            {@html navigationIcons.arrowLeft}
          </button>

          <!-- Date picker -->
          <DatePicker value={selectedDate} onChange={onDateChange} className="mx-2" />

          <!-- Next day button -->
          <button
            onclick={onNextDay}
            class="btn btn-sm btn-ghost"
            disabled={isToday}
            aria-label={t('dashboard.dailySummary.navigation.nextDay')}
          >
            {@html navigationIcons.arrowRight}
          </button>

          {#if !isToday}
            <button onclick={onGoToToday} class="btn btn-sm btn-primary"
              >{t('dashboard.dailySummary.navigation.today')}</button
            >
          {/if}
        </div>
      </div>

      <!-- Table Content -->
      <div class="overflow-x-auto">
        <table class="table table-zebra h-full w-full table-auto daily-summary-table">
          <thead class="sticky-header text-xs">
            <tr>
              {#each columns as column}
                <!-- Hourly, bi-hourly, and six-hourly headers -->
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
                    <!-- Hourly columns -->
                    {@const hour = column.hour}
                    {@const sunriseHour = sunTimes ? getSunHourFromTime(sunTimes.sunrise) : null}
                    {@const sunsetHour = sunTimes ? getSunHourFromTime(sunTimes.sunset) : null}
                    <!-- Sun icon positioned absolutely above hour number -->
                    {#if hour === sunriseHour}
                      <span
                        class="sun-icon sun-icon-sunrise"
                        role="img"
                        aria-label="Sunrise at {sunTimes
                          ? new Date(sunTimes.sunrise).toLocaleTimeString([], {
                              hour: '2-digit',
                              minute: '2-digit',
                            })
                          : 'unknown time'}"
                        title="Sunrise: {sunTimes
                          ? new Date(sunTimes.sunrise).toLocaleTimeString([], {
                              hour: '2-digit',
                              minute: '2-digit',
                            })
                          : ''}"
                      >
                        {@html weatherIcons.sunrise}
                      </span>
                    {:else if hour === sunsetHour}
                      <span
                        class="sun-icon sun-icon-sunset"
                        role="img"
                        aria-label="Sunset at {sunTimes
                          ? new Date(sunTimes.sunset).toLocaleTimeString([], {
                              hour: '2-digit',
                              minute: '2-digit',
                            })
                          : 'unknown time'}"
                        title="Sunset: {sunTimes
                          ? new Date(sunTimes.sunset).toLocaleTimeString([], {
                              hour: '2-digit',
                              minute: '2-digit',
                            })
                          : ''}"
                      >
                        {@html weatherIcons.sunset}
                      </span>
                    {/if}
                    <!-- Hour number as direct child of th -->
                    <a
                      href={urlBuilders.hourly(hour, 1)}
                      class="hour-link"
                      title={t('dashboard.dailySummary.tooltips.viewHourly', {
                        hour: hour.toString().padStart(2, '0'),
                      })}
                    >
                      {column.header}
                    </a>
                  {:else if column.type === 'bi-hourly'}
                    <!-- Bi-hourly columns -->
                    {@const hour = column.hour}
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
                    <!-- Six-hourly columns -->
                    {@const hour = column.hour}
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
                        const hour = column.hour;
                        const count = item.hourly_counts[hour];
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
                        const count = renderFunctions['bi-hourly'](item, column.hour);
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
                        const count = renderFunctions['six-hourly'](item, column.hour);
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
                      <!-- Species thumbnail and name -->
                      <div class="flex items-center gap-2">
                        {#if showThumbnails}
                          <BirdThumbnailPopup
                            thumbnailUrl={item.thumbnail_url ||
                              `/api/v2/species/${item.species_code}/thumbnail`}
                            commonName={item.common_name}
                            scientificName={item.scientific_name}
                            detectionUrl={urlBuilders.species(item)}
                          />
                        {/if}
                        <!-- Species name -->
                        <a
                          href={urlBuilders.species(item)}
                          class="text-sm hover:text-primary cursor-pointer font-medium flex-1 min-w-0 leading-tight flex items-center gap-1"
                        >
                          {item.common_name}
                          <!-- Multi-period tracking badges -->
                          {#if item.is_new_species}
                            <span
                              class="text-warning"
                              title={`New species (first seen ${item.days_since_first_seen ?? 0} day${(item.days_since_first_seen ?? 0) === 1 ? '' : 's'} ago)`}
                            >
                              {@html systemIcons.starAnimated}
                            </span>
                          {/if}
                          {#if item.is_new_this_year && !item.is_new_species}
                            <span
                              class="text-info"
                              title={`First time this year (${item.days_this_year ?? 0} day${(item.days_this_year ?? 0) === 1 ? '' : 's'} ago)`}
                            >
                              📅
                            </span>
                          {/if}
                          {#if item.is_new_this_season && !item.is_new_species && !item.is_new_this_year}
                            <span
                              class="text-success"
                              title={`First time this ${item.current_season || 'season'} (${item.days_this_season ?? 0} day${(item.days_this_season ?? 0) === 1 ? '' : 's'} ago)`}
                            >
                              🌿
                            </span>
                          {/if}
                        </a>
                      </div>
                    {:else if column.key === 'total_detections'}
                      <!-- Total detections bar -->
                      {@const maxCount = Math.max(...sortedData.map(d => d.count))}
                      {@const width = (item.count / maxCount) * 100}
                      {@const roundedWidth =
                        Math.round(width / PROGRESS_BAR_ROUNDING) * PROGRESS_BAR_ROUNDING}
                      <div class="w-full bg-base-300 rounded-full overflow-hidden relative">
                        <div
                          class="progress progress-primary bg-primary"
                          style:width="{roundedWidth}%"
                        >
                          {#if width >= WIDTH_THRESHOLDS.minTextDisplay && width <= WIDTH_THRESHOLDS.maxTextDisplay}
                            <!-- Total detections count for large bars -->
                            <span
                              class="text-2xs text-primary-content absolute right-1 top-1/2 transform -translate-y-1/2"
                              >{item.count}</span
                            >
                          {/if}
                        </div>
                        {#if width < WIDTH_THRESHOLDS.minTextDisplay || width > WIDTH_THRESHOLDS.maxTextDisplay}
                          <!-- Total detections count for small bars -->
                          <span
                            class="text-2xs {width > WIDTH_THRESHOLDS.maxTextDisplay
                              ? 'text-primary-content'
                              : 'text-base-content/60'} absolute w-full text-center top-1/2 left-1/2 transform -translate-x-1/2 -translate-y-1/2"
                            >{item.count}</span
                          >
                        {/if}
                      </div>
                    {:else if column.type === 'hourly'}
                      <!-- Hourly detections count -->
                      {@const hour = column.hour}
                      {@const count = item.hourly_counts[hour]}
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
                      {:else}
                        -
                      {/if}
                    {:else if column.type === 'bi-hourly'}
                      <!-- Bi-hourly detections count -->
                      {@const hour = column.hour}
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
                      {:else}
                        -
                      {/if}
                    {:else if column.type === 'six-hourly'}
                      <!-- Six-hourly detections count -->
                      {@const hour = column.hour}
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
                      {:else}
                        -
                      {/if}
                    {:else if column.render}
                      {column.render(item, 0)}
                    {:else}
                      <!-- Default column rendering -->
                      <span class="text-sm">{(item as any)[column.key]}</span>
                    {/if}
                  </td>
                {/each}
              </tr>
            {/each}
          </tbody>
        </table>
          {#if sortedData.length === 0}
            <div class="text-center py-8 text-base-content/60">
              {t('dashboard.dailySummary.noSpecies')}
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

  /* Theme-specific borders for hour data cells */
  :global([data-theme='light'] .hour-data:not(.heatmap-color-0)) {
    position: relative;
    z-index: 1;
    padding: 0;
    border: 1px solid var(--theme-border-light);
    background-clip: padding-box;
    border-collapse: collapse;
  }

  :global([data-theme='dark'] .hour-data:not(.heatmap-color-0)) {
    position: relative;
    z-index: 1;
    padding: 0;
    border: 1px solid var(--theme-border-dark);
    background-clip: padding-box;
    border-collapse: collapse;
  }

  /* Flex alignment for links inside hour cells */
  :global(.hour-data a) {
    height: 2rem;
    min-height: 2rem;
    max-height: 2rem;
    box-sizing: border-box;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  /* Remove extra borders in specific table rows */
  :global(.table :where(thead tr, tbody tr:not(:last-child), tbody tr:first-child:last-child)) {
    border-bottom-width: 0;
  }

  :global(.table :where(thead td, thead th)) {
    border-bottom: 1px solid var(--fallback-b2, oklch(var(--b2) / var(--tw-border-opacity)));
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

  /* Consistent table cell sizing */
  :global(.hour-data) {
    height: 2rem;
    min-height: 2rem;
    max-height: 2rem;
    line-height: 2rem;
    box-sizing: border-box;
    vertical-align: middle;
  }

  :global(.table tr) {
    height: 2rem;
    min-height: 2rem;
    max-height: 2rem;
  }

  :global(.table td),
  :global(.table th) {
    box-sizing: border-box;
    height: 2rem;
    min-height: 2rem;
    max-height: 2rem;
    vertical-align: middle;
  }

  /* Make hour cells more compact by default */
  :global(.hour-header),
  :global(.hour-data) {
    padding-left: 0.1rem;
    padding-right: 0.1rem;
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
    --heatmap-color-9: #003366;

    /* Theme-aware border colors */
    --theme-border-light: rgba(255, 255, 255, 0.1);
    --theme-border-dark: rgba(0, 0, 0, 0.1);

    /* Animation durations (for CSS animations) */
    --anim-count-pop: 600ms;
    --anim-heart-pulse: 1000ms;
    --anim-new-species: 800ms;
  }

  /* Dark theme heatmap colors */
  :global([data-theme='dark']) {
    --heatmap-color-0: #001a20;
    --heatmap-color-1: #002933;
    --heatmap-color-2: #004466;
    --heatmap-color-3: #005c80;
    --heatmap-color-4: #007399;
    --heatmap-color-5: #008bb3;
    --heatmap-color-6: #33a3cc;
    --heatmap-color-7: #66b8e2;
    --heatmap-color-8: #99cde9;
    --heatmap-color-9: #cce3f1;
  }

  /* Dark theme heatmap text colors */
  :global([data-theme='dark']) {
    --heatmap-text-1: #fff;
    --heatmap-text-2: #fff;
    --heatmap-text-3: #fff;
    --heatmap-text-4: #fff;
    --heatmap-text-5: #fff;
    --heatmap-text-6: #000;
    --heatmap-text-7: #000;
    --heatmap-text-8: #000;
    --heatmap-text-9: #000;
  }

  /* Light theme heatmap cell styles */
  :global([data-theme='light'] .heatmap-color-1) {
    background: linear-gradient(-45deg, var(--heatmap-color-1) 45%, var(--heatmap-color-0) 95%);
    color: var(--heatmap-text-1, #000);
  }
  :global([data-theme='light'] .heatmap-color-2) {
    background: linear-gradient(-45deg, var(--heatmap-color-2) 45%, var(--heatmap-color-1) 95%);
    color: var(--heatmap-text-2, #000);
  }
  :global([data-theme='light'] .heatmap-color-3) {
    background: linear-gradient(-45deg, var(--heatmap-color-3) 45%, var(--heatmap-color-2) 95%);
    color: var(--heatmap-text-3, #000);
  }
  :global([data-theme='light'] .heatmap-color-4) {
    background: linear-gradient(-45deg, var(--heatmap-color-4) 45%, var(--heatmap-color-3) 95%);
    color: var(--heatmap-text-4, #000);
  }
  :global([data-theme='light'] .heatmap-color-5) {
    background: linear-gradient(-45deg, var(--heatmap-color-5) 45%, var(--heatmap-color-4) 95%);
    color: var(--heatmap-text-5, #fff);
  }
  :global([data-theme='light'] .heatmap-color-6) {
    background: linear-gradient(-45deg, var(--heatmap-color-6) 45%, var(--heatmap-color-5) 95%);
    color: var(--heatmap-text-6, #fff);
  }
  :global([data-theme='light'] .heatmap-color-7) {
    background: linear-gradient(-45deg, var(--heatmap-color-7) 45%, var(--heatmap-color-6) 95%);
    color: var(--heatmap-text-7, #fff);
  }
  :global([data-theme='light'] .heatmap-color-8) {
    background: linear-gradient(-45deg, var(--heatmap-color-8) 45%, var(--heatmap-color-7) 95%);
    color: var(--heatmap-text-8, #fff);
  }
  :global([data-theme='light'] .heatmap-color-9) {
    background: linear-gradient(-45deg, var(--heatmap-color-9) 45%, var(--heatmap-color-8) 95%);
    color: var(--heatmap-text-9, #fff);
  }

  /* Dark theme heatmap cell styles - FIXED to use same gradient direction */
  :global([data-theme='dark'] .heatmap-color-1) {
    background: linear-gradient(-45deg, var(--heatmap-color-1) 45%, var(--heatmap-color-0) 95%);
    color: var(--heatmap-text-1, #000);
  }
  :global([data-theme='dark'] .heatmap-color-2) {
    background: linear-gradient(-45deg, var(--heatmap-color-2) 45%, var(--heatmap-color-1) 95%);
    color: var(--heatmap-text-2, #000);
  }
  :global([data-theme='dark'] .heatmap-color-3) {
    background: linear-gradient(-45deg, var(--heatmap-color-3) 45%, var(--heatmap-color-2) 95%);
    color: var(--heatmap-text-3, #000);
  }
  :global([data-theme='dark'] .heatmap-color-4) {
    background: linear-gradient(-45deg, var(--heatmap-color-4) 45%, var(--heatmap-color-3) 95%);
    color: var(--heatmap-text-4, #000);
  }
  :global([data-theme='dark'] .heatmap-color-5) {
    background: linear-gradient(-45deg, var(--heatmap-color-5) 45%, var(--heatmap-color-4) 95%);
    color: var(--heatmap-text-5, #fff);
  }
  :global([data-theme='dark'] .heatmap-color-6) {
    background: linear-gradient(-45deg, var(--heatmap-color-6) 45%, var(--heatmap-color-5) 95%);
    color: var(--heatmap-text-6, #fff);
  }
  :global([data-theme='dark'] .heatmap-color-7) {
    background: linear-gradient(-45deg, var(--heatmap-color-7) 45%, var(--heatmap-color-6) 95%);
    color: var(--heatmap-text-7, #fff);
  }
  :global([data-theme='dark'] .heatmap-color-8) {
    background: linear-gradient(-45deg, var(--heatmap-color-8) 45%, var(--heatmap-color-7) 95%);
    color: var(--heatmap-text-8, #fff);
  }
  :global([data-theme='dark'] .heatmap-color-9) {
    background: linear-gradient(-45deg, var(--heatmap-color-9) 45%, var(--heatmap-color-8) 95%);
    color: var(--heatmap-text-9, #fff);
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
    height: 2rem;
    min-height: 2rem;
    max-height: 2rem;
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
    vertical-align: bottom;
  }

  /* Sun icon positioning */
  .sun-icon {
    position: absolute;
    top: 2px;
    left: 50%;
    transform: translateX(-50%);
    z-index: 1;
    font-size: 0.75rem;
    line-height: 1;
    pointer-events: none;
  }

  .sun-icon-sunrise {
    color: #fb923c; /* text-orange-400 */
  }

  .sun-icon-sunset {
    color: #ea580c; /* text-orange-600 */
  }

  /* Hour link styling */
  .hour-link {
    display: block;
    width: 100%;
    height: 100%;
    min-height: 1.5rem;
    color: inherit;
    text-decoration: none;
    font-size: 0.75rem;
    padding-top: 1rem; /* Space for sun icon */
    box-sizing: border-box;
    text-align: center;
    cursor: pointer;
    transition: color 0.15s ease;
  }

  .hour-link:hover {
    color: oklch(var(--p));
    text-decoration: none;
  }

  /* Dark theme adjustments */
  :global([data-theme='dark']) .sun-icon-sunrise {
    color: #fdba74; /* Slightly lighter orange for dark theme */
  }

  :global([data-theme='dark']) .sun-icon-sunset {
    color: #f97316; /* Slightly lighter orange for dark theme */
  }
</style>
