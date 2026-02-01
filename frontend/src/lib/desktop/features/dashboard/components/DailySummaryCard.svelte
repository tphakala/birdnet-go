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
  import DatePicker from '$lib/desktop/components/ui/DatePicker.svelte';
  import SkeletonDailySummary from '$lib/desktop/components/ui/SkeletonDailySummary.svelte';
  import { t } from '$lib/i18n';
  import type { DailySpeciesSummary } from '$lib/types/detection.types';
  import { getLocalDateString, getLocalTimeString, parseLocalDateString } from '$lib/utils/date';
  import { loggers } from '$lib/utils/logger';
  import { LRUCache } from '$lib/utils/LRUCache';
  import { safeArrayAccess, safeGet } from '$lib/utils/security';
  import {
    WEATHER_ICON_MAP,
    UNKNOWN_WEATHER_INFO,
    getEffectiveWeatherCode,
    translateWeatherCondition,
  } from '$lib/utils/weather';
  import {
    convertTemperature,
    getTemperatureSymbol,
    type TemperatureUnit,
  } from '$lib/utils/formatters';
  import { ChevronLeft, ChevronRight, Star, Sunrise, Sunset, XCircle } from '@lucide/svelte';
  import { untrack } from 'svelte';
  import AnimatedCounter from './AnimatedCounter.svelte';
  import BirdThumbnailPopup from './BirdThumbnailPopup.svelte';

  const logger = loggers.ui;

  // Progressive loading timing constants (optimized for Svelte 5)
  const LOADING_PHASES = $state.raw({
    skeleton: 0, // 0ms - show skeleton immediately to reserve space
    spinner: 650, // 650ms - show spinner if still loading
  });

  // Heatmap scaling configuration
  // MAX_HEAT_COUNT: detection count at which maximum intensity (9) is reached
  // INTENSITY_LEVELS: number of color intensity levels (1-9, plus 0 for empty)
  const HEATMAP_CONFIG = {
    MAX_HEAT_COUNT: 50,
    INTENSITY_LEVELS: 9,
  } as const;

  // Consolidated configuration for magic numbers
  const CONFIG = {
    CACHE: {
      SUN_TIMES_MAX_ENTRIES: 30, // Max days of sun times to cache
      URL_MAX_ENTRIES: 500, // Max URLs to cache for memoization
    },
    DAYLIGHT: {
      DAWN_DUSK_HOURS_OFFSET: 2, // Hours before sunrise / after sunset for pre-dawn/dusk
      MIDDAY_INTENSITY_THRESHOLD: 0.3, // Distance from midday for "mid-day" classification
      DAY_INTENSITY_THRESHOLD: 0.7, // Distance from midday for "day" classification
      DEEP_NIGHT_END: 4, // Hour when deep night ends (0-4)
      DEEP_NIGHT_START: 21, // Hour when deep night starts (21-23)
      NIGHT_MORNING: 5, // Morning twilight hour
      NIGHT_EVENING: 20, // Evening twilight hour
    },
    QUERY: {
      DEFAULT_NUM_RESULTS: 25, // Default number of results for detection queries
    },
    SKELETON: {
      SPECIES_COUNT: 8, // Number of skeleton rows to show during loading
    },
    SPECIES_COLUMN: {
      BASE_WIDTH: 4, // rem - thumbnail (2) + gap (0.5) + padding (1) + buffer (0.5)
      CHAR_WIDTH: 0.52, // rem per character for text-sm font
      MIN_WIDTH: 9, // rem - minimum column width
      MAX_WIDTH: 22, // rem - maximum column width (prevents excessive width)
    },
  } as const;

  interface SunTimes {
    sunrise: string; // ISO date string
    sunset: string; // ISO date string
  }

  // Hourly weather data from API
  interface HourlyWeatherResponse {
    time: string; // "HH:mm:ss"
    temperature: number;
    weather_main?: string;
    weather_desc?: string; // yr.no symbol like "partlycloudy_night"
    weather_icon?: string; // icon code or "unknown"
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
    speciesLimit?: number;
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
    speciesLimit = 0,
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

  // Hourly weather state
  let hourlyWeather = $state<HourlyWeatherResponse[]>([]);
  // Map for O(1) hour lookup (populated when hourlyWeather changes)
  let hourlyWeatherMap = $state(new Map<number, HourlyWeatherResponse>());

  // Temperature unit preference (fetched from dashboard config)
  let temperatureUnit = $state<TemperatureUnit>('metric');

  // Cache for sun times to avoid repeated API calls - use LRUCache to limit memory usage
  const sunTimesCache = $state.raw(
    new LRUCache<string, SunTimes>(CONFIG.CACHE.SUN_TIMES_MAX_ENTRIES)
  );

  // Cache for hourly weather to avoid repeated API calls
  const hourlyWeatherCache = $state.raw(
    new LRUCache<string, HourlyWeatherResponse[]>(CONFIG.CACHE.SUN_TIMES_MAX_ENTRIES)
  );

  // Fetch dashboard config for temperature unit preference
  async function fetchDashboardConfig(): Promise<void> {
    try {
      const response = await fetch('/api/v2/settings/dashboard');
      if (!response.ok) return;
      const config = await response.json();
      // Map config temperatureUnit to TemperatureUnit type
      if (config.temperatureUnit === 'fahrenheit') {
        temperatureUnit = 'imperial';
      } else {
        temperatureUnit = 'metric';
      }
    } catch {
      // Keep default 'metric' on error
    }
  }

  // Fetch dashboard config on mount
  $effect(() => {
    fetchDashboardConfig();
  });

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
      const responseData = await response.json();
      const sunTimesData: SunTimes = {
        sunrise: responseData.sunrise,
        sunset: responseData.sunset,
      };

      // Cache the result
      sunTimesCache.set(date, sunTimesData);

      return sunTimesData;
    } catch (fetchError) {
      const errorMsg =
        fetchError instanceof Error ? fetchError.message : 'Unknown error fetching sun times';
      logger.warn('Error fetching sun times:', errorMsg);
      return null;
    }
  }

  // Update sun times when selected date changes
  // Uses captured date to prevent stale data from overwriting fresh data on rapid date changes
  $effect(() => {
    const currentDate = selectedDate;
    if (currentDate) {
      fetchSunTimes(currentDate).then(times => {
        // Only update if this is still the current date (prevents race condition)
        if (selectedDate === currentDate) {
          sunTimes = times;
        }
      });
    }
  });

  // Fetch hourly weather data from API with caching
  async function fetchHourlyWeather(date: string): Promise<HourlyWeatherResponse[]> {
    // Validate date format (YYYY-MM-DD) before making API request
    const dateRegex = /^\d{4}-\d{2}-\d{2}$/;
    if (!dateRegex.test(date)) {
      logger.warn(`Invalid date format provided to fetchHourlyWeather: ${date}`);
      return [];
    }

    // Check cache first
    const cached = hourlyWeatherCache.get(date);
    if (cached) {
      return cached;
    }

    try {
      const response = await fetch(`/api/v2/weather/hourly/${date}`);
      if (!response.ok) {
        logger.warn(`Failed to fetch hourly weather: ${response.status} ${response.statusText}`);
        return [];
      }
      const responseData = await response.json();
      const weatherData: HourlyWeatherResponse[] = responseData.data || [];

      // Cache the result
      hourlyWeatherCache.set(date, weatherData);

      return weatherData;
    } catch (fetchError) {
      const errorMsg =
        fetchError instanceof Error ? fetchError.message : 'Unknown error fetching hourly weather';
      logger.warn('Error fetching hourly weather:', errorMsg);
      return [];
    }
  }

  // Update hourly weather when selected date changes
  // Uses captured date to prevent stale data from overwriting fresh data on rapid date changes
  $effect(() => {
    const currentDate = selectedDate;
    if (currentDate) {
      fetchHourlyWeather(currentDate).then(data => {
        // Only update if this is still the current date (prevents race condition)
        if (selectedDate === currentDate) {
          hourlyWeather = data;
          // Build hour-keyed map for O(1) lookup
          hourlyWeatherMap = new Map(data.map(w => [parseInt(w.time.split(':')[0], 10), w]));
        }
      });
    }
  });

  // Calculate which hour column corresponds to sunrise/sunset
  const getSunHourFromTime = (timeStr: string): number | null => {
    if (!timeStr) return null;
    try {
      const date = new Date(timeStr);
      return date.getHours();
    } catch (parseError) {
      logger.error('Error parsing time', parseError, { timeStr });
      return null;
    }
  };

  // Pre-computed sunrise/sunset hours to avoid recalculating in template loops
  const sunriseHour = $derived(sunTimes ? getSunHourFromTime(sunTimes.sunrise) : null);
  const sunsetHour = $derived(sunTimes ? getSunHourFromTime(sunTimes.sunset) : null);

  // Find weather data for a specific hour (O(1) map lookup)
  const getHourlyWeatherData = (hour: number): HourlyWeatherResponse | undefined => {
    return hourlyWeatherMap.get(hour);
  };

  // Get weather emoji for a specific hour
  const getHourlyWeatherEmoji = (hour: number): string => {
    const hourData = getHourlyWeatherData(hour);
    if (!hourData) return '';

    const iconCode = getEffectiveWeatherCode(hourData.weather_icon, hourData.weather_desc);
    if (!iconCode) return '';

    // Determine if it's night based on hour relative to sunrise/sunset
    // Fallback checks both OpenWeatherMap 'n' suffix and yr.no '_night' suffix in description
    const isNight =
      sunriseHour !== null && sunsetHour !== null
        ? hour < sunriseHour || hour >= sunsetHour
        : (hourData.weather_icon?.endsWith('n') ?? false) ||
          (hourData.weather_desc?.includes('_night') ?? false);

    const weatherInfo = safeGet(WEATHER_ICON_MAP, iconCode, UNKNOWN_WEATHER_INFO);
    return isNight ? weatherInfo.night : weatherInfo.day;
  };

  // Get tooltip text for hourly weather
  const getHourlyWeatherTooltip = (hour: number): string => {
    const hourData = getHourlyWeatherData(hour);
    if (!hourData) return '';

    // Translate raw weather description to human-readable text
    const rawDesc = hourData.weather_main || hourData.weather_desc || '';
    const desc = translateWeatherCondition(rawDesc);

    // Convert temperature from Celsius (API storage) to user's preferred unit
    let temp = '';
    if (hourData.temperature !== undefined) {
      const convertedTemp = convertTemperature(hourData.temperature, temperatureUnit);
      const symbol = getTemperatureSymbol(temperatureUnit);
      temp = `${convertedTemp.toFixed(1)}${symbol}`;
    }

    return [desc, temp].filter(Boolean).join(', ');
  };

  // Get daylight class for an hour based on its position relative to sunrise/sunset
  // Returns: 'deep-night', 'night', 'pre-dawn', 'sunrise', 'early-day', 'day', 'mid-day', 'late-day', 'sunset', 'dusk', 'evening'
  const getDaylightClass = (hour: number): string => {
    const { DAWN_DUSK_HOURS_OFFSET, MIDDAY_INTENSITY_THRESHOLD, DAY_INTENSITY_THRESHOLD } =
      CONFIG.DAYLIGHT;
    const { DEEP_NIGHT_END, DEEP_NIGHT_START, NIGHT_MORNING, NIGHT_EVENING } = CONFIG.DAYLIGHT;

    // Use pre-computed derived values for performance
    if (sunriseHour === null || sunsetHour === null) return 'night';

    // Sunrise hour - special gradient
    if (hour === sunriseHour) return 'sunrise';
    // Sunset hour - special gradient
    if (hour === sunsetHour) return 'sunset';

    // Pre-dawn (hours before sunrise)
    if (hour >= sunriseHour - DAWN_DUSK_HOURS_OFFSET && hour < sunriseHour) return 'pre-dawn';

    // Dusk (hours after sunset)
    if (hour > sunsetHour && hour <= sunsetHour + DAWN_DUSK_HOURS_OFFSET) return 'dusk';

    // Daylight hours
    if (hour > sunriseHour && hour < sunsetHour) {
      const midday = (sunriseHour + sunsetHour) / 2;
      const distanceFromMidday = Math.abs(hour - midday);
      const daylightDuration = (sunsetHour - sunriseHour) / 2;

      // Categorize daylight intensity
      if (distanceFromMidday < daylightDuration * MIDDAY_INTENSITY_THRESHOLD) return 'mid-day';
      if (distanceFromMidday < daylightDuration * DAY_INTENSITY_THRESHOLD) return 'day';
      return hour < midday ? 'early-day' : 'late-day';
    }

    // Night hours - vary by distance from midnight
    if (hour >= 0 && hour <= DEEP_NIGHT_END) return 'deep-night';
    if (hour >= DEEP_NIGHT_START && hour <= 23) return 'deep-night';
    if (hour === NIGHT_MORNING || hour === NIGHT_EVENING) return 'night';
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
    const words = commonName.trim().split(/\s+/).filter(Boolean);
    if (words.length === 0) return '??';
    if (words.length === 1) {
      return words[0].substring(0, 2).toUpperCase();
    }
    return (words[0][0] + words[1][0]).toUpperCase();
  };

  /**
   * Calculate heatmap intensity using simple fixed-range scaling.
   * Maps detection counts evenly across intensity levels 1-9 based on HEATMAP_CONFIG.
   * - 0 detections → intensity 0 (empty cell)
   * - 1-6 detections → intensity 1
   * - 7-12 detections → intensity 2
   * - ...
   * - 45-50 detections → intensity 9
   * - 50+ detections → intensity 9
   *
   * @param count - The detection count for this cell
   * @returns Intensity value from 0-9
   */
  const getHeatmapIntensity = (count: number): number => {
    if (count <= 0) return 0;
    const { MAX_HEAT_COUNT, INTENSITY_LEVELS } = HEATMAP_CONFIG;
    const stepSize = MAX_HEAT_COUNT / INTENSITY_LEVELS;
    return Math.min(INTENSITY_LEVELS, Math.max(1, Math.ceil(count / stepSize)));
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
  const urlCache = $state.raw(new LRUCache<string, string>(CONFIG.CACHE.URL_MAX_ENTRIES));
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
      const cacheKey = `species:${species.scientific_name}:${selectedDate}`;
      if (!urlCache.has(cacheKey)) {
        const params = new URLSearchParams({
          queryType: 'species',
          species: species.scientific_name,
          date: selectedDate,
          numResults: CONFIG.QUERY.DEFAULT_NUM_RESULTS.toString(),
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
      const cacheKey = `species-hour:${species.scientific_name}:${selectedDate}:${hour}:${duration}`;
      if (!urlCache.has(cacheKey)) {
        const params = new URLSearchParams({
          queryType: 'species',
          species: species.scientific_name,
          date: selectedDate,
          hour: hour.toString(),
          duration: duration.toString(),
          numResults: CONFIG.QUERY.DEFAULT_NUM_RESULTS.toString(),
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
          numResults: CONFIG.QUERY.DEFAULT_NUM_RESULTS.toString(),
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
  // Also applies speciesLimit to cap the number of displayed species
  const sortedData = $derived.by(() => {
    // Early return for empty data
    if (data.length === 0) return [];

    // Use spread + sort with stable ordering
    const sorted = [...data].sort((a: DailySpeciesSummary, b: DailySpeciesSummary) => {
      // Primary sort: by detection count (descending)
      if (b.count !== a.count) {
        return b.count - a.count;
      }
      // Secondary sort: by latest detection time (descending - most recent first)
      // This ensures stable ordering when counts are equal
      return (b.latest_heard ?? '').localeCompare(a.latest_heard ?? '');
    });

    // Apply species limit after sorting to ensure top N species are shown
    if (speciesLimit > 0 && sorted.length > speciesLimit) {
      return sorted.slice(0, speciesLimit);
    }

    return sorted;
  });

  // Calculate dynamic species column width based on longest name
  // This ensures all rows align properly regardless of name length
  // Uses CONFIG.SPECIES_COLUMN constants for easy adjustment
  const speciesColumnWidth = $derived.by(() => {
    const { BASE_WIDTH, CHAR_WIDTH, MIN_WIDTH, MAX_WIDTH } = CONFIG.SPECIES_COLUMN;

    if (data.length === 0) return `${MIN_WIDTH}rem`;

    // Find the longest species name
    const longestName = data.reduce(
      (longest, item) => (item.common_name.length > longest.length ? item.common_name : longest),
      ''
    );
    const maxLength = longestName.length;

    // Calculate width: base (thumbnail + gap + icons) + character width estimate
    const calculatedWidth = BASE_WIDTH + maxLength * CHAR_WIDTH;

    // Clamp between min and max
    const finalWidth = Math.max(MIN_WIDTH, Math.min(MAX_WIDTH, calculatedWidth));

    return `${finalWidth}rem`;
  });
</script>

{#snippet navigationControls()}
  <div class="flex items-center gap-2 w-full justify-between md:w-auto md:justify-end">
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
      className="mx-auto md:mx-2 w-auto"
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

{#snippet sunIcon(sunType: 'sunrise' | 'sunset', sunTime: string | undefined, shouldShow: boolean)}
  {#if shouldShow && sunTime}
    {@const parsedDate = parseLocalDateString(sunTime)}
    {#if parsedDate}
      {@const formattedTime = getLocalTimeString(parsedDate, false)}
      <div
        class="sun-icon-wrapper"
        title={t(`dashboard.dailySummary.daylight.${sunType}`, { time: formattedTime })}
      >
        {#if sunType === 'sunrise'}
          <Sunrise class="size-3.5 text-orange-700" />
        {:else}
          <Sunset class="size-3.5 text-rose-700" />
        {/if}
        <span class="sun-tooltip sun-tooltip-{sunType}">{formattedTime}</span>
      </div>
    {/if}
  {/if}
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
  <SkeletonDailySummary {showThumbnails} speciesCount={CONFIG.SKELETON.SPECIES_COUNT} />
{:else if loadingPhase === 'spinner'}
  <SkeletonDailySummary
    {showThumbnails}
    showSpinner={showDelayedIndicator}
    speciesCount={CONFIG.SKELETON.SPECIES_COUNT}
  />
{:else if loadingPhase === 'error'}
  <section
    class="daily-summary-card card col-span-12 bg-base-100 shadow-sm rounded-2xl border border-border-100 overflow-visible"
  >
    <div class="px-6 py-4 border-b border-base-200 overflow-visible">
      <div
        class="flex flex-col gap-2 md:flex-row md:items-center md:justify-between overflow-visible"
      >
        <div class="flex flex-col">
          <h3 class="font-semibold">{t('dashboard.dailySummary.title')}</h3>
          <p class="text-sm text-base-content/60">{t('dashboard.dailySummary.subtitle')}</p>
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
  <section
    class="daily-summary-card card col-span-12 bg-base-100 shadow-sm rounded-2xl border border-border-100 overflow-visible"
  >
    <!-- Card Header with Date Navigation -->
    <div class="px-6 py-4 border-b border-base-200 overflow-visible">
      <div
        class="flex flex-col gap-2 md:flex-row md:items-center md:justify-between overflow-visible"
      >
        <div class="flex flex-col">
          <h3 class="font-semibold">{t('dashboard.dailySummary.title')}</h3>
          <p class="text-sm text-base-content/60">{t('dashboard.dailySummary.subtitle')}</p>
        </div>
        {@render navigationControls()}
      </div>
    </div>

    <!-- Grid Content -->
    <div class="p-6 pt-8">
      <div class="overflow-x-auto overflow-y-visible">
        <div
          class="daily-summary-grid min-w-[900px]"
          style:--species-col-width={speciesColumnWidth}
        >
          <!-- Hourly weather visualization row (only shown if weather data exists) -->
          {#if hourlyWeather.length > 0}
            <div class="flex mb-1">
              <!-- Empty label column to align with other rows -->
              <div class="species-label-col shrink-0"></div>

              <!-- Hourly weather (desktop) -->
              <div class="hourly-grid flex-1 grid">
                {#each Array(24) as _, hour (hour)}
                  {@const emoji = getHourlyWeatherEmoji(hour)}
                  <div
                    class="h-5 flex items-center justify-center text-sm weather-cell"
                    title={getHourlyWeatherTooltip(hour)}
                  >
                    {emoji || ''}
                  </div>
                {/each}
              </div>

              <!-- Bi-hourly weather (tablet/mobile) -->
              <div class="bi-hourly-grid flex-1 grid">
                {#each Array(12) as _, i (i)}
                  {@const hour = i * 2}
                  {@const emoji = getHourlyWeatherEmoji(hour)}
                  <div
                    class="h-5 flex items-center justify-center text-sm weather-cell"
                    title={getHourlyWeatherTooltip(hour)}
                  >
                    {emoji || ''}
                  </div>
                {/each}
              </div>

              <!-- Six-hourly weather (small mobile) -->
              <div class="six-hourly-grid flex-1 grid">
                {#each Array(4) as _, i (i)}
                  {@const hour = i * 6}
                  {@const emoji = getHourlyWeatherEmoji(hour)}
                  <div
                    class="h-5 flex items-center justify-center text-base weather-cell"
                    title={getHourlyWeatherTooltip(hour)}
                  >
                    {emoji || ''}
                  </div>
                {/each}
              </div>
            </div>
          {/if}

          <!-- Daylight visualization row -->
          <div class="flex mb-1">
            <div class="species-label-col shrink-0 flex items-center">
              <span class="text-xs text-base-content/60 font-normal whitespace-nowrap"
                >{t('dashboard.dailySummary.daylight.label')}</span
              >
            </div>
            <!-- Hourly daylight (desktop) -->
            <div class="hourly-grid flex-1 grid">
              {#each Array(24) as _, hour (hour)}
                {@const daylightClass = getDaylightClass(hour)}
                <div
                  class="h-5 rounded-sm daylight-cell daylight-{daylightClass} relative flex items-center justify-center"
                >
                  {@render sunIcon('sunrise', sunTimes?.sunrise, hour === sunriseHour)}
                  {@render sunIcon('sunset', sunTimes?.sunset, hour === sunsetHour)}
                </div>
              {/each}
            </div>
            <!-- Bi-hourly daylight (tablet/mobile) -->
            <div class="bi-hourly-grid flex-1 grid">
              {#each Array(12) as _, i (i)}
                {@const hour = i * 2}
                {@const daylightClass = getDaylightClass(hour)}
                {@const showSunrise =
                  sunriseHour !== null && hour <= sunriseHour && sunriseHour < hour + 2}
                {@const showSunset =
                  sunsetHour !== null &&
                  hour <= sunsetHour &&
                  sunsetHour < hour + 2 &&
                  !showSunrise}
                <div
                  class="h-5 rounded-sm daylight-cell daylight-{daylightClass} relative flex items-center justify-center"
                >
                  {@render sunIcon('sunrise', sunTimes?.sunrise, showSunrise)}
                  {@render sunIcon('sunset', sunTimes?.sunset, showSunset)}
                </div>
              {/each}
            </div>
            <!-- Six-hourly daylight (small mobile) -->
            <div class="six-hourly-grid flex-1 grid">
              {#each Array(4) as _, i (i)}
                {@const hour = i * 6}
                {@const daylightClass = getDaylightClass(hour)}
                {@const showSunrise =
                  sunriseHour !== null && hour <= sunriseHour && sunriseHour < hour + 6}
                {@const showSunset =
                  sunsetHour !== null &&
                  hour <= sunsetHour &&
                  sunsetHour < hour + 6 &&
                  !showSunrise}
                <div
                  class="h-5 rounded-sm daylight-cell daylight-{daylightClass} relative flex items-center justify-center"
                >
                  {@render sunIcon('sunrise', sunTimes?.sunrise, showSunrise)}
                  {@render sunIcon('sunset', sunTimes?.sunset, showSunset)}
                </div>
              {/each}
            </div>
          </div>

          <!-- Hours header row -->
          <div class="flex mb-1">
            <div class="species-label-col shrink-0"></div>
            <!-- Hourly headers (desktop) -->
            <div class="hourly-grid flex-1 grid text-xs">
              {#each Array(24) as _, hour (hour)}
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
              {#each Array(12) as _, i (i)}
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
              {#each Array(4) as _, i (i)}
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
          <div class="flex flex-col" style:gap="var(--grid-gap)">
            {#each sortedData as item (item.scientific_name)}
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
                    class="text-sm hover:text-primary cursor-pointer font-medium leading-tight flex items-center gap-1 overflow-hidden"
                    title={item.common_name}
                  >
                    <span class="truncate flex-1">{item.common_name}</span>
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
                  {#each Array(24) as _, hour (hour)}
                    {@const count = safeArrayAccess(item.hourly_counts, hour, 0) ?? 0}
                    {@const intensity = getHeatmapIntensity(count)}
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
                          <AnimatedCounter value={count} />
                        </a>
                      {/if}
                    </div>
                  {/each}
                </div>

                <!-- Bi-hourly heatmap cells (tablet/mobile) -->
                <div class="bi-hourly-grid flex-1 grid">
                  {#each Array(12) as _, i (i)}
                    {@const hour = i * 2}
                    {@const count = renderFunctions['bi-hourly'](item, hour)}
                    {@const intensity = getHeatmapIntensity(count)}
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
                          <AnimatedCounter value={count} />
                        </a>
                      {/if}
                    </div>
                  {/each}
                </div>

                <!-- Six-hourly heatmap cells (small mobile) -->
                <div class="six-hourly-grid flex-1 grid">
                  {#each Array(4) as _, i (i)}
                    {@const hour = i * 6}
                    {@const count = renderFunctions['six-hourly'](item, hour)}
                    {@const intensity = getHeatmapIntensity(count)}
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
                          <AnimatedCounter value={count} />
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
              {#each [0, 1, 2, 3, 4, 5, 6, 7, 8, 9] as intensity (intensity)}
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
     Scoped to component to avoid global conflicts
     ======================================================================== */
  .daily-summary-card {
    /* Grid layout properties */
    --grid-cell-height: 1.25rem;
    --grid-cell-radius: 4px;
    --grid-gap: 4px; /* Gap between grid cells */

    /* Species column width fallbacks (actual width is set dynamically via JS)
       These are fallbacks only - the dynamic width is set via style:--species-col-width */
    --species-col-min-width: 9rem; /* Fallback, matches CONFIG.SPECIES_COLUMN.MIN_WIDTH */
    --species-col-max-width: 16rem; /* Fallback, matches CONFIG.SPECIES_COLUMN.MAX_WIDTH */

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

  /* Species label column - fixed width calculated from longest species name */
  .species-label-col {
    width: var(--species-col-width, var(--species-col-min-width));
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

  /* Species row - consistent height */
  .species-row {
    min-height: 2rem;
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
  /* Must use .daily-summary-card scope to override the light theme vars defined above */
  :global([data-theme='dark']) .daily-summary-card {
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
    max-width: var(--species-col-max-width, 18rem);
    padding: 0 0.75rem 0 0.5rem !important;
  }

  .species-badge {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 2rem; /* w-8 - match thumbnail width */
    height: 1.75rem; /* h-7 - match thumbnail height */
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
