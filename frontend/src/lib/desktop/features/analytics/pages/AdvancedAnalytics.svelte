<!-- Advanced Analytics Page with D3.js Charts -->
<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '$lib/i18n';

  import TimeOfDaySpeciesChart from '../components/charts/d3/TimeOfDaySpeciesChart.svelte';
  import DailySpeciesTrendChart from '../components/charts/d3/DailySpeciesTrendChart.svelte';
  import SpeciesDiversityChart from '../components/charts/d3/SpeciesDiversityChart.svelte';
  import SpeciesSelector from '$lib/components/ui/SpeciesSelector.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import Input from '$lib/desktop/components/ui/Input.svelte';
  import type { Species, SpeciesId } from '$lib/types/species';
  import { createSpeciesId } from '$lib/types/species';
  import { getLogger } from '$lib/utils/logger';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { parseLocalDateString } from '$lib/utils/date';

  const logger = getLogger('advanced-analytics');

  // Chart data interfaces
  interface TimeOfDayDatum {
    hour: number;
    count: number;
  }

  interface TimeOfDaySpeciesData {
    species: string;
    commonName: string;
    data: TimeOfDayDatum[];
    visible: boolean;
  }

  interface DailyTrendDatum {
    date: Date;
    count: number;
  }

  interface DailyTrendSpeciesData {
    species: string;
    commonName: string;
    data: DailyTrendDatum[];
    visible: boolean;
  }

  // API response interfaces
  interface SpeciesSummaryResponse {
    scientific_name?: string;
    common_name?: string;
    count?: number;
  }

  interface HourlyDataItem {
    hour: number;
    count: number;
  }

  interface DailyDataItem {
    date: string;
    count: number;
  }

  interface SpeciesDailyData {
    start_date: string;
    end_date: string;
    species: string;
    data: DailyDataItem[];
    total: number;
  }

  interface DiversityDataItem {
    date: string;
    unique_species: number;
  }

  interface DiversityResponse {
    start_date: string;
    end_date: string;
    data: DiversityDataItem[];
    max_diversity: number;
  }

  interface DiversityDatum {
    date: Date;
    uniqueSpecies: number;
  }

  // Component state
  let isLoading = $state(false);
  let error = $state<string | null>(null);

  // Date range controls
  let dateRange = $state<'week' | 'month' | 'quarter' | 'year' | 'custom'>('month');
  let startDate = $state('');
  let endDate = $state('');

  // Species selection
  let availableSpecies = $state<Species[]>([]);
  let selectedSpecies = $state<SpeciesId[]>([]);
  let maxSpecies = 10;

  // Abort controllers for preventing race conditions
  let speciesController: AbortController | null = null;
  let timeOfDayController: AbortController | null = null;
  let dailyTrendController: AbortController | null = null;
  let diversityController: AbortController | null = null;

  // Chart options
  let showRelativeTrends = $state(false);
  let enableZoom = $state(true);
  let enableBrush = $state(false);

  // Chart data
  let timeOfDayData = $state<TimeOfDaySpeciesData[]>([]);
  let dailyTrendData = $state<DailyTrendSpeciesData[]>([]);
  let diversityData = $state<DiversityDatum[]>([]);

  // Date range options for custom Select component
  const dateRangeOptions = $derived([
    { value: 'week', label: t('analytics.advanced.dateRangeOptions.week') },
    { value: 'month', label: t('analytics.advanced.dateRangeOptions.month') },
    { value: 'quarter', label: t('analytics.advanced.dateRangeOptions.quarter') },
    { value: 'year', label: t('analytics.advanced.dateRangeOptions.year') },
    { value: 'custom', label: t('analytics.advanced.dateRangeOptions.custom') },
  ]);

  // Computed date range
  const computedDateRange = $derived(
    (() => {
      const today = new Date();
      let start: Date, end: Date;

      switch (dateRange) {
        case 'week':
          end = today;
          start = new Date(today.getTime() - 7 * 24 * 60 * 60 * 1000);
          break;
        case 'month':
          end = today;
          start = new Date(today.getTime() - 30 * 24 * 60 * 60 * 1000);
          break;
        case 'quarter':
          end = today;
          start = new Date(today.getTime() - 90 * 24 * 60 * 60 * 1000);
          break;
        case 'year':
          end = today;
          start = new Date(today.getTime() - 365 * 24 * 60 * 60 * 1000);
          break;
        case 'custom':
          start = startDate
            ? (parseLocalDateString(startDate) ??
              new Date(today.getTime() - 30 * 24 * 60 * 60 * 1000))
            : new Date(today.getTime() - 30 * 24 * 60 * 60 * 1000);
          end = endDate ? (parseLocalDateString(endDate) ?? today) : today;
          break;
        default:
          end = today;
          start = new Date(today.getTime() - 30 * 24 * 60 * 60 * 1000);
      }

      return [start, end] as [Date, Date];
    })()
  );

  // Format date for API calls (avoid timezone issues)
  function formatDateForAPI(date: Date): string {
    const year = date.getFullYear();
    const month = (date.getMonth() + 1).toString().padStart(2, '0');
    const day = date.getDate().toString().padStart(2, '0');
    return `${year}-${month}-${day}`;
  }

  // Fetch available species
  async function fetchAvailableSpecies() {
    try {
      // Abort any previous species fetch
      if (speciesController) {
        speciesController.abort();
      }
      speciesController = new AbortController();

      const [start, end] = computedDateRange;
      const params = new URLSearchParams({
        start_date: formatDateForAPI(start),
        end_date: formatDateForAPI(end),
        limit: '50', // Get top 50 species
      });

      const response = await fetch(buildAppUrl(`/api/v2/analytics/species/summary?${params}`), {
        signal: speciesController.signal,
      });
      if (!response.ok) {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`);
      }

      const data: unknown = await response.json();

      availableSpecies = Array.isArray(data)
        ? (data as SpeciesSummaryResponse[]).map((item, index) => {
            const count = item.count ?? 0;
            const frequency =
              count > 100
                ? 'very-common'
                : count > 50
                  ? 'common'
                  : count > 10
                    ? 'uncommon'
                    : 'rare';
            return {
              id: createSpeciesId(item.scientific_name ?? `species-${index}`),
              commonName: item.common_name ?? t('common.unknown'),
              scientificName: item.scientific_name ?? t('common.unknown'),
              frequency: frequency as 'very-common' | 'common' | 'uncommon' | 'rare',
              category: t('analytics.advanced.categories.birds'),
              description: t('analytics.advanced.detections', { count }),
              count, // Keep count for backwards compatibility
            };
          })
        : [];

      // Auto-select top species if none selected (limited by backend cap)
      if (selectedSpecies.length === 0 && availableSpecies.length > 0) {
        const maxToSelect = Math.min(availableSpecies.length, 5, 10); // Client wants 5, backend allows 10
        selectedSpecies = availableSpecies.slice(0, maxToSelect).map(s => s.id);
      }
    } catch (err) {
      // Don't log abort errors as they're expected
      if (err instanceof Error && err.name === 'AbortError') {
        return;
      }
      logger.error('Error fetching available species:', err);
      availableSpecies = [];
    }
  }

  // Fetch time of day data for selected species
  async function fetchTimeOfDayData() {
    if (selectedSpecies.length === 0) {
      timeOfDayData = [];
      return;
    }

    try {
      // Abort any previous time of day fetch
      if (timeOfDayController) {
        timeOfDayController.abort();
      }
      timeOfDayController = new AbortController();

      const [start] = computedDateRange;
      const params = new URLSearchParams({
        date: formatDateForAPI(start),
        min_confidence: '0',
      });

      // Add species parameters (convert IDs back to scientific names)
      selectedSpecies.forEach(speciesId => {
        const species = availableSpecies.find(s => s.id === speciesId);
        if (species?.scientificName) {
          params.append('species', species.scientificName);
        }
      });

      const response = await fetch(buildAppUrl(`/api/v2/analytics/time/hourly/batch?${params}`), {
        signal: timeOfDayController.signal,
      });
      if (!response.ok) {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`);
      }

      const data: unknown = await response.json();

      if (!data || typeof data !== 'object' || Array.isArray(data)) {
        throw new Error('Invalid hourly batch response: expected an object');
      }

      // Convert API response to chart format
      const newTimeOfDayData: TimeOfDaySpeciesData[] = Object.entries(
        data as Record<string, unknown>
      ).map(([species, hourlyData]) => {
        const speciesInfo = availableSpecies.find(s => s.scientificName === species);
        return {
          species,
          commonName: speciesInfo?.commonName ?? species,
          data: Array.isArray(hourlyData)
            ? (hourlyData as HourlyDataItem[]).map(item => ({
                hour: typeof item.hour === 'number' ? item.hour : 0,
                count: typeof item.count === 'number' ? item.count : 0,
              }))
            : [],
          visible: true,
        };
      });

      // Only update if we have valid data
      if (newTimeOfDayData.length > 0) {
        timeOfDayData = newTimeOfDayData;
      }
    } catch (err) {
      // Don't log abort errors as they're expected
      if (err instanceof Error && err.name === 'AbortError') {
        return;
      }
      logger.error('Error fetching time of day data:', err);
      // Don't clear existing data on error, just log it
    }
  }

  // Fetch daily trend data for selected species
  async function fetchDailyTrendData() {
    if (selectedSpecies.length === 0) {
      dailyTrendData = [];
      return;
    }

    try {
      // Abort any previous daily trend fetch
      if (dailyTrendController) {
        dailyTrendController.abort();
      }
      dailyTrendController = new AbortController();

      const [start, end] = computedDateRange;
      const params = new URLSearchParams({
        start_date: formatDateForAPI(start),
        end_date: formatDateForAPI(end),
      });

      // Add species parameters (convert IDs back to scientific names)
      selectedSpecies.forEach(speciesId => {
        const species = availableSpecies.find(s => s.id === speciesId);
        if (species?.scientificName) {
          params.append('species', species.scientificName);
        }
      });

      const response = await fetch(buildAppUrl(`/api/v2/analytics/time/daily/batch?${params}`), {
        signal: dailyTrendController.signal,
      });
      if (!response.ok) {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`);
      }

      const data: unknown = await response.json();

      if (!data || typeof data !== 'object' || Array.isArray(data)) {
        throw new Error('Invalid daily batch response: expected an object');
      }

      // Convert API response to chart format
      const newDailyTrendData: DailyTrendSpeciesData[] = Object.entries(
        data as Record<string, unknown>
      ).map(([species, trendData]) => {
        const speciesInfo = availableSpecies.find(s => s.scientificName === species);

        // Validate that trendData has the expected structure
        if (!trendData || typeof trendData !== 'object') {
          return {
            species,
            commonName: speciesInfo?.commonName ?? species,
            data: [],
            visible: true,
          };
        }

        const apiData = trendData as SpeciesDailyData;
        const dataArray = Array.isArray(apiData.data) ? apiData.data : [];

        return {
          species,
          commonName: speciesInfo?.commonName ?? species,
          data: dataArray
            .map(item => {
              if (!item || typeof item !== 'object') return null;

              const date = parseLocalDateString(item.date);
              const count = typeof item.count === 'number' ? item.count : 0;

              // Skip invalid dates
              if (!date || isNaN(date.getTime())) return null;

              return { date, count };
            })
            .filter((item): item is DailyTrendDatum => item !== null),
          visible: true,
        };
      });

      // Only update if we have valid data
      if (newDailyTrendData.length > 0) {
        dailyTrendData = newDailyTrendData;
      }
    } catch (err) {
      // Don't log abort errors as they're expected
      if (err instanceof Error && err.name === 'AbortError') {
        return;
      }
      logger.error('Error fetching daily trend data:', err);
      // Don't clear existing data on error, just log it
    }
  }

  // Fetch species diversity data (independent of species selection)
  async function fetchDiversityData() {
    try {
      if (diversityController) {
        diversityController.abort();
      }
      diversityController = new AbortController();

      const [start, end] = computedDateRange;
      const params = new URLSearchParams({
        start_date: formatDateForAPI(start),
        end_date: formatDateForAPI(end),
      });

      const response = await fetch(buildAppUrl(`/api/v2/analytics/species/diversity?${params}`), {
        signal: diversityController.signal,
      });
      if (!response.ok) {
        throw new Error(`HTTP ${response.status}: ${response.statusText}`);
      }

      const data: unknown = await response.json();

      if (!data || typeof data !== 'object' || Array.isArray(data)) {
        throw new Error('Invalid diversity response: expected an object');
      }

      const result = data as DiversityResponse;

      diversityData = (result.data ?? [])
        .map(item => {
          const date = parseLocalDateString(item.date);
          if (!date || isNaN(date.getTime())) return null;
          return { date, uniqueSpecies: item.unique_species };
        })
        .filter((item): item is DiversityDatum => item !== null);
    } catch (err) {
      if (err instanceof Error && err.name === 'AbortError') {
        return;
      }
      logger.error('Error fetching diversity data:', err);
    }
  }

  // Fetch all data
  async function fetchAllData() {
    isLoading = true;
    error = null;

    try {
      // Fetch species list and diversity data in parallel (diversity is species-independent)
      await Promise.all([fetchAvailableSpecies(), fetchDiversityData()]);

      if (selectedSpecies.length > 0) {
        await Promise.all([fetchTimeOfDayData(), fetchDailyTrendData()]);
      } else {
        // Clear species-dependent chart data if no species selected
        timeOfDayData = [];
        dailyTrendData = [];
      }
    } catch (err) {
      // Don't log abort errors as they're expected
      if (err instanceof Error && err.name === 'AbortError') {
        return;
      }
      logger.error('Error fetching analytics data:', err);
      error = 'Failed to load analytics data. Please try again.';
    } finally {
      isLoading = false;
    }
  }

  // Handle date range changes
  function handleDateRangeChange(range: [Date, Date]) {
    startDate = formatDateForAPI(range[0]);
    endDate = formatDateForAPI(range[1]);
    dateRange = 'custom';
    fetchAllData();
  }

  // Initialize default custom date inputs on mount
  onMount(() => {
    const today = new Date();
    const monthAgo = new Date(today.getTime() - 30 * 24 * 60 * 60 * 1000);

    startDate = formatDateForAPI(monthAgo);
    endDate = formatDateForAPI(today);
  });

  // Watch for changes that require data refresh
  $effect(() => {
    // Only fetch if we have a valid date range and are not in the initial loading phase
    if (dateRange !== 'custom' || (dateRange === 'custom' && startDate && endDate)) {
      // Debounce multiple rapid changes
      const timeoutId = setTimeout(() => {
        fetchAllData();
      }, 100);

      return () => clearTimeout(timeoutId);
    }
  });
</script>

<div class="col-span-12 space-y-6" role="region" aria-label="Advanced Analytics">
  <!-- Error Display -->
  {#if error}
    <div
      class="p-4 bg-red-50 border border-red-300 text-red-800 rounded-lg flex items-center gap-3"
    >
      <svg
        xmlns="http://www.w3.org/2000/svg"
        class="stroke-current shrink-0 h-6 w-6"
        fill="none"
        viewBox="0 0 24 24"
      >
        <path
          stroke-linecap="round"
          stroke-linejoin="round"
          stroke-width="2"
          d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z"
        />
      </svg>
      <span>{error}</span>
    </div>
  {/if}

  <!-- Controls Section -->
  <div class="bg-base-100 rounded-xl shadow-sm border border-base-200">
    <div class="p-6 overflow-visible">
      <h2 class="text-lg font-semibold mb-4">{t('analytics.advanced.chartControls')}</h2>

      <!-- Top Row: Date Range and Chart Options -->
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-4">
        <!-- Date Range Selection -->
        <div class="space-y-2">
          <SelectDropdown
            bind:value={dateRange}
            options={dateRangeOptions}
            label={t('analytics.advanced.dateRange')}
            variant="select"
            size="sm"
            menuSize="sm"
          />

          {#if dateRange === 'custom'}
            <div class="grid grid-cols-2 gap-2 mt-2">
              <label for="startDateInput" class="sr-only"
                >{t('analytics.advanced.filters.startDate')}</label
              >
              <Input
                id="startDateInput"
                type="date"
                bind:value={startDate}
                max={endDate}
                aria-label={t('analytics.advanced.filters.startDate')}
              />
              <label for="endDateInput" class="sr-only"
                >{t('analytics.advanced.filters.endDate')}</label
              >
              <Input
                id="endDateInput"
                type="date"
                bind:value={endDate}
                min={startDate}
                aria-label={t('analytics.advanced.filters.endDate')}
              />
            </div>
          {/if}
        </div>

        <!-- Chart Options -->
        <div class="space-y-2">
          <div class="label">
            <span class="label-text font-medium">{t('analytics.advanced.chartOptions')}</span>
          </div>

          <div class="flex flex-wrap gap-x-6 gap-y-2">
            <Checkbox
              bind:checked={showRelativeTrends}
              label={t('analytics.advanced.options.relativeTrends')}
              size="sm"
            />

            <Checkbox
              bind:checked={enableZoom}
              label={t('analytics.advanced.options.zoomPan')}
              size="sm"
            />

            <Checkbox
              bind:checked={enableBrush}
              label={t('analytics.advanced.options.brushSelection')}
              size="sm"
            />
          </div>
        </div>
      </div>

      <!-- Bottom Row: Species Selection (Full Width) -->
      <div class="space-y-2">
        <div class="flex items-baseline justify-between">
          <span class="label-text font-medium"
            >{t('analytics.advanced.speciesSelection', {
              count: selectedSpecies.length,
              max: maxSpecies,
            })}</span
          >
          <span class="label-text-alt text-xs text-base-content opacity-60">
            {t('analytics.advanced.speciesSelectionHint')}
          </span>
        </div>

        <div class="w-full">
          <div class="min-h-[100px] relative">
            <SpeciesSelector
              species={availableSpecies}
              selected={selectedSpecies}
              variant="chip"
              size="md"
              maxSelections={maxSpecies}
              placeholder={t('analytics.advanced.speciesPlaceholder')}
              searchable={true}
              showFrequency={true}
              categorized={false}
              loading={isLoading}
              emptyText={t('analytics.advanced.noSpeciesFound')}
              className="w-full"
              on:change={e => {
                selectedSpecies = e.detail.selected.map(createSpeciesId);
                // Refresh chart data when species selection changes
                if (selectedSpecies.length > 0) {
                  fetchTimeOfDayData();
                  fetchDailyTrendData();
                } else {
                  // Clear data if no species selected
                  timeOfDayData = [];
                  dailyTrendData = [];
                }
              }}
            >
              {#snippet speciesDisplay(species)}
                <div class="flex items-center justify-between gap-2">
                  <div class="flex-1 min-w-0">
                    <div class="font-medium truncate">{species.commonName}</div>
                    <div class="text-xs text-base-content opacity-60 truncate italic">
                      {species.scientificName}
                    </div>
                  </div>
                  {#if species.count !== undefined}
                    <div
                      class="inline-flex items-center px-2 py-1 text-xs font-medium rounded-full bg-base-200/50 text-base-content"
                    >
                      {t('analytics.advanced.detections', { count: species.count ?? 0 })}
                    </div>
                  {/if}
                </div>
              {/snippet}
            </SpeciesSelector>
          </div>
        </div>
      </div>
    </div>
  </div>

  <!-- Charts Section -->
  <div class="grid grid-cols-1 gap-6">
    <!-- Time of Day Chart -->
    <div class="bg-base-100 rounded-xl shadow-sm border border-base-200">
      <div class="p-6">
        <h2 class="text-lg font-semibold">{t('analytics.advanced.charts.timeOfDay.title')}</h2>
        <p class="text-sm text-base-content opacity-70 mb-4">
          {t('analytics.advanced.charts.timeOfDay.description')}
        </p>

        <div class="h-96 relative">
          <TimeOfDaySpeciesChart data={timeOfDayData} {selectedSpecies} width={1200} height={384} />

          {#if isLoading}
            <div
              class="absolute inset-0 bg-base-100/80 backdrop-blur-xs flex items-center justify-center rounded-lg"
              role="status"
              aria-busy="true"
              aria-label={t('analytics.advanced.aria.loadingAnalytics')}
            >
              <div
                class="w-12 h-12 border-4 border-primary border-t-transparent rounded-full animate-spin"
              ></div>
              <span class="sr-only">{t('analytics.advanced.aria.loadingAnalytics')}</span>
            </div>
          {:else if timeOfDayData.length === 0}
            <div
              class="absolute inset-0 flex items-center justify-center text-base-content opacity-60 rounded-lg"
              role="status"
              aria-label={t('analytics.advanced.charts.timeOfDay.noData')}
            >
              <div class="text-center">
                <p class="text-lg mb-2">{t('analytics.advanced.charts.timeOfDay.noData')}</p>
                <p class="text-sm">{t('analytics.advanced.charts.timeOfDay.noDataHint')}</p>
              </div>
            </div>
          {/if}
        </div>
      </div>
    </div>

    <!-- Daily Trend Chart -->
    <div class="bg-base-100 rounded-xl shadow-sm border border-base-200">
      <div class="p-6">
        <h2 class="text-lg font-semibold">{t('analytics.advanced.charts.dailyTrend.title')}</h2>
        <p class="text-sm text-base-content opacity-70 mb-4">
          {t('analytics.advanced.charts.dailyTrend.description')}
        </p>

        <div class="h-96 relative">
          <DailySpeciesTrendChart
            data={dailyTrendData}
            {selectedSpecies}
            dateRange={computedDateRange}
            showRelative={showRelativeTrends}
            {enableZoom}
            {enableBrush}
            onDateRangeChange={handleDateRangeChange}
            width={1200}
            height={384}
          />

          {#if isLoading}
            <div
              class="absolute inset-0 bg-base-100/80 backdrop-blur-xs flex items-center justify-center rounded-lg"
              role="status"
              aria-busy="true"
              aria-label={t('analytics.advanced.aria.loadingTrends')}
            >
              <div
                class="w-12 h-12 border-4 border-primary border-t-transparent rounded-full animate-spin"
              ></div>
              <span class="sr-only">{t('analytics.advanced.aria.loadingTrends')}</span>
            </div>
          {:else if dailyTrendData.length === 0}
            <div
              class="absolute inset-0 flex items-center justify-center text-base-content opacity-60 rounded-lg"
              role="status"
              aria-label={t('analytics.advanced.charts.dailyTrend.noData')}
            >
              <div class="text-center">
                <p class="text-lg mb-2">{t('analytics.advanced.charts.dailyTrend.noData')}</p>
                <p class="text-sm">{t('analytics.advanced.charts.dailyTrend.noDataHint')}</p>
              </div>
            </div>
          {/if}
        </div>
      </div>
    </div>
  </div>

  <!-- Species Diversity Chart (Full Width) -->
  <div class="bg-base-100 rounded-xl shadow-sm border border-base-200">
    <div class="p-6">
      <h2 class="text-lg font-semibold">{t('analytics.advanced.charts.diversity.title')}</h2>
      <p class="text-sm text-base-content opacity-70 mb-4">
        {t('analytics.advanced.charts.diversity.description')}
      </p>

      <div class="h-96 relative">
        <SpeciesDiversityChart
          data={diversityData}
          dateRange={computedDateRange}
          width={1200}
          height={384}
        />

        {#if isLoading}
          <div
            class="absolute inset-0 bg-base-100/80 backdrop-blur-xs flex items-center justify-center rounded-lg"
            role="status"
            aria-busy="true"
            aria-label={t('analytics.advanced.aria.loadingDiversity')}
          >
            <div
              class="w-12 h-12 border-4 border-primary border-t-transparent rounded-full animate-spin"
            ></div>
            <span class="sr-only">{t('analytics.advanced.aria.loadingDiversity')}</span>
          </div>
        {:else if diversityData.length === 0}
          <div
            class="absolute inset-0 flex items-center justify-center text-base-content opacity-60 rounded-lg"
            role="status"
            aria-label={t('analytics.advanced.charts.diversity.noData')}
          >
            <div class="text-center">
              <p class="text-lg mb-2">{t('analytics.advanced.charts.diversity.noData')}</p>
              <p class="text-sm">{t('analytics.advanced.charts.diversity.noDataHint')}</p>
            </div>
          </div>
        {/if}
      </div>
    </div>
  </div>
</div>

<style>
  /* Card-like containers */
  .bg-base-100 {
    transition: box-shadow 0.2s ease;
  }
</style>
