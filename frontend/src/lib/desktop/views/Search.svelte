<script lang="ts">
  import WeatherInfo from '$lib/desktop/components/data/WeatherInfo.svelte';
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import MobileAudioPlayer from '$lib/desktop/components/media/MobileAudioPlayer.svelte';
  import DatePicker from '$lib/desktop/components/ui/DatePicker.svelte';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils';
  import TimeOfDayIcon from '$lib/desktop/components/ui/TimeOfDayIcon.svelte';
  import { getLocale, t } from '$lib/i18n';
  import { dashboardSettings } from '$lib/stores/settings';
  import { toastActions } from '$lib/stores/toast';
  import { api } from '$lib/utils/api';
  import { getLocalDateString, parseLocalDateString } from '$lib/utils/date';
  import type { TemperatureUnit } from '$lib/utils/formatters';
  import {
    ArrowDownUp,
    ChevronDown,
    Eye,
    FrownIcon,
    Music,
    Search,
    Volume2,
    XCircle,
  } from '@lucide/svelte';

  // SPINNER CONTROL: Set to false to disable loading spinners (reduces flickering)
  // Change back to true to re-enable spinners for testing
  const ENABLE_LOADING_SPINNERS = false;

  // Map user's temperature preference to TemperatureUnit format
  // Settings store uses 'celsius'/'fahrenheit', but formatters use 'metric'/'imperial'/'standard'
  const temperatureUnits = $derived.by((): TemperatureUnit => {
    const setting = $dashboardSettings?.temperatureUnit;
    if (setting === 'fahrenheit') return 'imperial';
    return 'metric'; // Default to metric (Celsius)
  });

  // Type definitions
  interface DateRange {
    start: string;
    end: string;
  }

  interface ConfidenceRange {
    min: number;
    max: number;
  }

  interface SearchResult {
    id: string;
    timestamp: string;
    timeOfDay: string;
    commonName: string;
    scientificName: string;
    confidence: number;
    verified: string;
    locked: boolean;
    hasAudio: boolean;
  }

  type VerifiedStatus = 'any' | 'verified' | 'unverified';
  type LockedStatus = 'any' | 'locked' | 'unlocked';
  type TimeOfDayFilter = 'any' | 'day' | 'night' | 'sunrise' | 'sunset';
  type SortBy = 'date_desc' | 'date_asc' | 'species_asc' | 'confidence_desc';

  // Component state
  let speciesSearchTerm = $state('');
  let dateRange = $state<DateRange>({ start: '', end: '' });
  let confidenceRange = $state<ConfidenceRange>({ min: 0, max: 100 });
  let verifiedStatus = $state<VerifiedStatus>('any');
  let lockedStatus = $state<LockedStatus>('any');
  let timeOfDayFilter = $state<TimeOfDayFilter>('any');
  let formSubmitted = $state(false);
  let advancedFilters = $state(false);
  let isLoading = $state(false);
  let currentPage = $state(1);
  let totalPages = $state(1);
  let results = $state<SearchResult[]>([]);
  let totalResults = $state(0);
  let sortBy = $state<SortBy>('date_desc');
  let errorMessage = $state('');
  // PERFORMANCE OPTIMIZATION: Use Set instead of object for expandedItems
  // Set operations (has/add/delete) are faster than object property access
  // and provide better memory efficiency for tracking expanded table rows
  let expandedItems = $state(new Set<string>());
  let hasConfidenceError = $state(false);
  let showTooltip = $state<string | null>(null);

  // Localized pluralized results count using i18n keys
  function formatResultsCount(count: number) {
    if (!count || count === 0) return t('search.resultsCountZero');
    if (count === 1) return t('search.resultsCountOne');
    return t('search.resultsCountOther', { count });
  }

  // Mobile audio overlay state
  let showMobilePlayer = $state(false);
  let selectedAudioUrl = $state('');
  let selectedSpeciesName = $state('');
  let selectedDetectionId = $state<string | undefined>(undefined);

  function openMobilePlayer(result: SearchResult) {
    if (!result?.id) return;
    selectedAudioUrl = `/api/v2/audio/${result.id}`;
    selectedSpeciesName = result.commonName || '';
    selectedDetectionId = result.id;
    showMobilePlayer = true;
  }

  function closeMobilePlayer() {
    showMobilePlayer = false;
    selectedAudioUrl = '';
    selectedSpeciesName = '';
    selectedDetectionId = undefined;
  }

  // Form validation
  function validateForm() {
    hasConfidenceError = false;
    if (confidenceRange.min > confidenceRange.max) {
      hasConfidenceError = true;
      return false;
    }
    return true;
  }

  // Form submission
  async function submitSearch(page = 1) {
    if (!validateForm()) return;

    isLoading = true;
    errorMessage = '';
    currentPage = page;
    expandedItems.clear(); // Reset expanded state when loading new results

    try {
      // Build request body
      const requestBody = {
        species: speciesSearchTerm,
        dateStart: dateRange.start,
        dateEnd: dateRange.end,
        confidenceMin: confidenceRange.min / 100,
        confidenceMax: confidenceRange.max / 100,
        verifiedStatus: verifiedStatus,
        lockedStatus: lockedStatus,
        timeOfDay: timeOfDayFilter,
        page: currentPage,
        sortBy: sortBy,
      };

      interface SearchResponse {
        results: SearchResult[];
        total: number;
        pages: number;
      }

      const data = await api.post<SearchResponse>('/api/v2/search', requestBody);
      results = data.results ?? [];
      totalResults = data.total ?? 0;
      totalPages = data.pages ?? 1;
      formSubmitted = true;
    } catch (error: unknown) {
      // Handle search error silently
      errorMessage = t('search.errors.searchFailed', {
        error: error instanceof Error ? error.message : 'Unknown error',
      });
      results = [];
    } finally {
      isLoading = false;
    }
  }

  // Reset form
  function resetForm() {
    speciesSearchTerm = '';
    dateRange.start = '';
    dateRange.end = '';
    confidenceRange.min = 0;
    confidenceRange.max = 100;
    verifiedStatus = 'any';
    lockedStatus = 'any';
    timeOfDayFilter = 'any';
    formSubmitted = false;
    results = [];
    errorMessage = '';
    expandedItems.clear();
  }

  // Format date for display
  function formatDate(dateString: string) {
    if (!dateString) return '';
    const date = parseLocalDateString(dateString);
    if (!date) return '';
    return date.toLocaleString(getLocale(), { dateStyle: 'medium', timeStyle: 'short' });
  }

  // Handle pagination
  function goToPage(page: number) {
    if (page < 1 || page > totalPages) return;
    submitSearch(page);
  }

  // Handle sorting
  function changeSort(sortOption: SortBy) {
    sortBy = sortOption;
    submitSearch(1);
  }

  // Toggle expand state of a row
  function toggleExpand(recordId: string) {
    if (expandedItems.has(recordId)) {
      expandedItems.delete(recordId);
    } else {
      expandedItems.add(recordId);
    }
    // PERFORMANCE OPTIMIZATION: Create new Set instance to trigger Svelte 5 reactivity
    // Svelte 5's fine-grained reactivity requires new object references to detect changes
    // This is more efficient than spreading into object: {...expanded, [id]: !expanded[id]}
    expandedItems = new Set(expandedItems);
  }

  function isExpanded(recordId: string) {
    return expandedItems.has(recordId);
  }

  // Memoized today value - only recalculates when component mounts or when day changes
  // This prevents unnecessary recalculations on every state change
  const today = $derived.by(() => {
    // Force recalculation periodically to handle day changes
    // Using Math.floor to update once per day
    const daysSinceEpoch = Math.floor(Date.now() / (1000 * 60 * 60 * 24));
    // The variable access ensures reactivity but the calculation is stable per day
    void daysSinceEpoch; // Acknowledge the dependency
    return getLocalDateString();
  });

  // Optimized reactive date constraints - only recalculate when relevant dependencies change
  const startDateConstraints = $derived.by(() => {
    // Only depends on: dateRange.end and today
    const todayValue = today;
    const endDate = dateRange.end;

    const constraints: { maxDate?: string; minDate?: string } = {};

    // Use the earlier of end date or today as maximum
    if (endDate && endDate < todayValue) {
      constraints.maxDate = endDate;
    } else {
      constraints.maxDate = endDate || todayValue;
    }

    return constraints;
  });

  const endDateConstraints = $derived.by(() => {
    // Only depends on: dateRange.start and today
    const todayValue = today;
    const startDate = dateRange.start;

    const constraints: { maxDate?: string; minDate?: string } = {
      maxDate: todayValue, // End date cannot be in future
    };

    // If start date is set, end date must be after or equal to start date
    if (startDate) {
      constraints.minDate = startDate;
    }

    return constraints;
  });

  // Date picker handlers with smart edge case handling
  function handleStartDateChange(date: string) {
    dateRange.start = date;

    // Smart edge case: If start date is set after existing end date, clear end date
    // This prevents confusion and guides user to set valid range
    if (date && dateRange.end && date > dateRange.end) {
      dateRange.end = '';
      toastActions.info(t('components.datePicker.feedback.endDateCleared'));
    }
  }

  function handleEndDateChange(date: string) {
    dateRange.end = date;

    // Smart edge case: If end date is set before existing start date, clear start date
    // This allows users to work backwards (end date first, then start date)
    if (date && dateRange.start && date < dateRange.start) {
      dateRange.start = '';
      toastActions.info(t('components.datePicker.feedback.startDateCleared'));
    }
  }
</script>

<div class="col-span-12 space-y-4" role="region" aria-label={t('search.title')}>
  <!-- Search Form -->
  <div class="card bg-base-100 shadow-xs">
    <div class="card-body card-padding">
      <h2 class="card-title" id="search-filters-heading">{t('search.title')}</h2>

      <form
        id="searchForm"
        class="space-y-4"
        onsubmit={e => {
          e.preventDefault();
          submitSearch(1);
        }}
        aria-labelledby="search-filters-heading"
      >
        <!-- Basic Search Fields -->
        <div class="gap-4 search-form-grid">
          <!-- Species -->
          <div class="form-control">
            <label class="label" for="species">
              <span class="label-text">{t('search.fields.species')}</span>
              <span
                class="help-icon"
                onmouseenter={() => (showTooltip = 'species')}
                onmouseleave={() => (showTooltip = null)}
                onfocus={() => (showTooltip = 'species')}
                onblur={() => (showTooltip = null)}
                role="button"
                tabindex="0"
                aria-label={t('search.fields.speciesHelp')}
                aria-describedby="speciesTooltip">ⓘ</span
              >
            </label>
            <input
              type="text"
              id="species"
              bind:value={speciesSearchTerm}
              placeholder={t('search.fields.speciesPlaceholder')}
              class="input w-full"
            />
            {#if showTooltip === 'species'}
              <div class="tooltip" id="speciesTooltip" role="tooltip">
                {t('search.fields.speciesHelp')}
              </div>
            {/if}
          </div>

          <!-- Date Range -->
          <div class="form-control">
            <label class="label" for="dateRangeStart">
              <span class="label-text">{t('search.fields.dateRange')}</span>
              <span
                class="help-icon"
                onmouseenter={() => (showTooltip = 'dateRange')}
                onmouseleave={() => (showTooltip = null)}
                onfocus={() => (showTooltip = 'dateRange')}
                onblur={() => (showTooltip = null)}
                role="button"
                tabindex="0"
                aria-label={t('search.fields.dateRangeHelp')}
                aria-describedby="dateRangeTooltip">ⓘ</span
              >
            </label>
            <div class="gap-2 search-date-grid" role="group" aria-labelledby="dateRangeLabel">
              <DatePicker
                value={dateRange.start}
                onChange={handleStartDateChange}
                placeholder={t('search.fields.from')}
                className="w-full"
                size="md"
                maxDate={startDateConstraints.maxDate}
                minDate={startDateConstraints.minDate}
              />
              <DatePicker
                value={dateRange.end}
                onChange={handleEndDateChange}
                placeholder={t('search.fields.to')}
                className="w-full"
                size="md"
                maxDate={endDateConstraints.maxDate}
                minDate={endDateConstraints.minDate}
              />
            </div>
            {#if showTooltip === 'dateRange'}
              <div class="tooltip" id="dateRangeTooltip" role="tooltip">
                {t('search.fields.dateRangeHelp')}
              </div>
            {/if}
          </div>
        </div>

        <!-- Advanced Filters Toggle -->
        <div class="flex items-center justify-between">
          <button
            type="button"
            class="btn btn-sm btn-ghost"
            onclick={() => (advancedFilters = !advancedFilters)}
            aria-expanded={advancedFilters}
            aria-controls="advancedFiltersSection"
          >
            <span
              >{advancedFilters
                ? t('search.hideAdvancedFilters')
                : t('search.showAdvancedFilters')}</span
            >
            <span
              class="transition-transform duration-200"
              class:rotate-180={advancedFilters}
              aria-hidden="true"
            >
              <ChevronDown class="size-5" />
            </span>
          </button>
        </div>

        <!-- Advanced Filters Section -->
        {#if advancedFilters}
          <div class="space-y-2 pt-2" id="advancedFiltersSection">
            <!-- Confidence Range -->
            <div class="form-control">
              <label class="label" for="confidenceMin">
                <span class="label-text">{t('search.fields.confidenceRange')}</span>
                <span class="label-text-alt">{confidenceRange.min}% - {confidenceRange.max}%</span>
              </label>
              <div
                class="gap-6 search-confidence-grid"
                role="group"
                aria-labelledby="confidenceRangeLabel"
              >
                <div>
                  <input
                    type="range"
                    min="0"
                    max="100"
                    id="confidenceMin"
                    bind:value={confidenceRange.min}
                    class="range range-xs"
                    aria-label={t('search.fields.confidenceMin')}
                    aria-valuemin="0"
                    aria-valuemax="100"
                    aria-valuenow={confidenceRange.min}
                    aria-valuetext="{confidenceRange.min}%"
                  />
                  <div class="flex justify-between text-xs px-2">
                    <span>0%</span>
                    <span>{confidenceRange.min}%</span>
                  </div>
                </div>
                <div>
                  <input
                    type="range"
                    min="0"
                    max="100"
                    bind:value={confidenceRange.max}
                    class="range range-xs"
                    aria-label={t('search.fields.confidenceMax')}
                    aria-valuemin="0"
                    aria-valuemax="100"
                    aria-valuenow={confidenceRange.max}
                    aria-valuetext="{confidenceRange.max}%"
                  />
                  <div class="flex justify-between text-xs px-2">
                    <span>0%</span>
                    <span>{confidenceRange.max}%</span>
                  </div>
                </div>
              </div>
              <!-- Confidence error message -->
              {#if hasConfidenceError}
                <div class="text-error text-sm mt-1" role="alert">
                  {t('search.errors.minMaxConfidence')}
                </div>
              {/if}
            </div>

            <!-- Status & Time of Day Filters -->
            <div class="gap-6 search-filters-grid">
              <!-- Verified Status -->
              <div class="form-control">
                <label class="label" for="verifiedStatusFilter">
                  <span class="label-text">{t('search.fields.verifiedStatus')}</span>
                </label>
                <select id="verifiedStatusFilter" bind:value={verifiedStatus} class="select w-full">
                  <option value="any">{t('search.verifiedOptions.any')}</option>
                  <option value="verified">{t('search.verifiedOptions.verified')}</option>
                  <option value="unverified">{t('search.verifiedOptions.unverified')}</option>
                </select>
              </div>

              <!-- Locked Status -->
              <div class="form-control">
                <label class="label" for="lockedStatusFilter">
                  <span class="label-text">{t('search.fields.lockedStatus')}</span>
                </label>
                <select id="lockedStatusFilter" bind:value={lockedStatus} class="select w-full">
                  <option value="any">{t('search.lockedOptions.any')}</option>
                  <option value="locked">{t('search.lockedOptions.locked')}</option>
                  <option value="unlocked">{t('search.lockedOptions.unlocked')}</option>
                </select>
              </div>

              <!-- Time of Day -->
              <div class="form-control">
                <label class="label" for="timeOfDayFilter">
                  <span class="label-text">{t('search.fields.timeOfDay')}</span>
                </label>
                <select id="timeOfDayFilter" bind:value={timeOfDayFilter} class="select w-full">
                  <option value="any">{t('search.timeOfDayOptions.any')}</option>
                  <option value="day">{t('search.timeOfDayOptions.day')}</option>
                  <option value="night">{t('search.timeOfDayOptions.night')}</option>
                  <option value="sunrise">{t('search.timeOfDayOptions.sunrise')}</option>
                  <option value="sunset">{t('search.timeOfDayOptions.sunset')}</option>
                </select>
              </div>
            </div>
          </div>
        {/if}

        <!-- Form Action Buttons -->
        <div class="flex flex-row gap-4 justify-end">
          <button
            type="button"
            class="btn btn-ghost shrink-0"
            onclick={resetForm}
            aria-label={t('common.reset')}
          >
            {t('common.reset')}
          </button>
          <button
            type="submit"
            class="btn btn-primary shrink-0"
            disabled={isLoading}
            aria-label={t('common.search')}
          >
            {#if ENABLE_LOADING_SPINNERS && isLoading}
              <span class="loading loading-spinner loading-sm mr-2" aria-hidden="true"></span>
            {:else}
              <span class="mr-2" aria-hidden="true">
                <Search class="size-5" />
              </span>
            {/if}
            {t('common.search')}
          </button>
        </div>
      </form>
    </div>
  </div>

  <!-- Results Area -->
  <div class="card bg-base-100 shadow-xs">
    <div class="card-body card-padding">
      <div class="flex items-center justify-between">
        <h2 class="card-title" id="search-results-heading">{t('search.results')}</h2>

        <!-- Results Count & Sorting -->
        {#if formSubmitted}
          <div class="flex items-center gap-4">
            <span class="text-sm text-base-content opacity-70" aria-live="polite"
              >{formatResultsCount(totalResults)}</span
            >
            <div class="dropdown dropdown-end">
              <div
                tabindex="0"
                role="button"
                class="btn btn-sm btn-outline"
                aria-haspopup="true"
                aria-expanded="false"
                aria-label={t('common.sort')}
              >
                <ArrowDownUp class="size-5" />
                {t('common.sort')}
              </div>
              <ul
                tabindex="0"
                class="dropdown-content z-1 menu p-2 shadow-xs bg-base-100 rounded-box w-52"
                role="menu"
              >
                <li role="menuitem">
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm justify-start w-full"
                    onclick={() => changeSort('date_desc')}
                    >{t('search.sortOptions.dateDesc')}</button
                  >
                </li>
                <li role="menuitem">
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm justify-start w-full"
                    onclick={() => changeSort('date_asc')}>{t('search.sortOptions.dateAsc')}</button
                  >
                </li>
                <li role="menuitem">
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm justify-start w-full"
                    onclick={() => changeSort('species_asc')}
                    >{t('search.sortOptions.speciesAsc')}</button
                  >
                </li>
                <li role="menuitem">
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm justify-start w-full"
                    onclick={() => changeSort('confidence_desc')}
                    >{t('search.sortOptions.confidenceDesc')}</button
                  >
                </li>
              </ul>
            </div>
          </div>
        {/if}
      </div>

      <!-- Error message -->
      {#if errorMessage}
        <div class="alert alert-error mt-4" role="alert">
          <XCircle class="size-5" />
          <span>{errorMessage}</span>
        </div>
      {/if}

      <!-- When no search performed yet -->
      {#if !formSubmitted}
        <div
          class="mt-6 bg-base-200 rounded-lg p-4 flex flex-col items-center justify-center min-h-[200px]"
          aria-labelledby="search-results-heading"
        >
          <span class="text-base-content opacity-30 text-[4rem]" aria-hidden="true">
            <Search class="size-12" />
          </span>
          <p class="text-base-content opacity-50 text-center mt-4">
            {t('search.noSearchPerformed')}
          </p>
          <p class="text-base-content opacity-50 text-center text-sm">
            {t('search.noSearchPerformedHint')}
          </p>
        </div>
      {/if}

      <!-- Loading indicator -->
      {#if ENABLE_LOADING_SPINNERS && isLoading && formSubmitted}
        <div
          class="mt-6 bg-base-200 rounded-lg p-4 flex flex-col items-center justify-center min-h-[200px]"
          aria-live="polite"
          aria-busy="true"
        >
          <span class="loading loading-spinner loading-lg text-primary" aria-hidden="true"></span>
          <p class="text-base-content opacity-50 text-center mt-4">{t('search.loadingResults')}</p>
        </div>
      {/if}

      <!-- Search results - table for md+, cards for mobile -->
      {#if formSubmitted && !isLoading && results.length > 0}
        <!-- Desktop/tablet table -->
        <div class="overflow-x-auto mt-4 hidden md:block" aria-labelledby="search-results-heading">
          <table class="table w-full">
            <thead>
              <tr>
                <th scope="col">{t('search.tableHeaders.dateTime')}</th>
                <th scope="col">{t('search.tableHeaders.timeOfDay')}</th>
                <th scope="col">{t('search.tableHeaders.species')}</th>
                <th scope="col">{t('search.tableHeaders.confidence')}</th>
                <th scope="col">{t('search.tableHeaders.status')}</th>
                <th scope="col">{t('search.tableHeaders.actions')}</th>
              </tr>
            </thead>
            <tbody>
              <!-- Loop through results -->
              {#each results as result, index (result.id)}
                <!-- Main row -->
                <tr class={index % 2 === 0 ? 'bg-base-100' : 'bg-base-200'}>
                  <td>{formatDate(result.timestamp)}</td>
                  <td>
                    <div class="flex items-center">
                      <TimeOfDayIcon timeOfDay={result.timeOfDay as any} className="mr-1" />
                      <span>{result.timeOfDay || t('search.detailsPanel.unknownSpecies')}</span>
                    </div>
                  </td>
                  <td>
                    <div class="flex items-center gap-2">
                      <!-- Add bird image thumbnail -->
                      <div
                        class="w-12 h-12 rounded-md overflow-hidden bg-gray-100 shrink-0 cursor-pointer hover:ring-2 hover:ring-primary transition-all focus:outline-hidden focus:ring-2 focus:ring-primary"
                        onclick={() => toggleExpand(result.id)}
                        onkeydown={e => {
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault();
                            toggleExpand(result.id);
                          }
                        }}
                        aria-label={isExpanded(result.id)
                          ? t('search.detailsPanel.collapseDetails', {
                              species: result.commonName || t('search.detailsPanel.unknownSpecies'),
                            })
                          : t('search.detailsPanel.expandDetails', {
                              species: result.commonName || t('search.detailsPanel.unknownSpecies'),
                            })}
                        aria-expanded={isExpanded(result.id)}
                        role="button"
                        tabindex="0"
                      >
                        <!-- PERFORMANCE OPTIMIZATION: Enhanced image loading attributes -->
                        <!-- loading="lazy": Defer loading until image enters viewport -->
                        <!-- decoding="async": Decode image off-main-thread to prevent UI blocking -->
                        <!-- fetchpriority="low": Lower network priority for species thumbnails -->
                        <img
                          src="/api/v2/media/species-image?name={encodeURIComponent(
                            result.scientificName
                          )}"
                          alt={result.commonName || t('search.detailsPanel.unknownSpecies')}
                          class="w-full h-full object-cover"
                          onerror={e => {
                            const target = e.currentTarget as HTMLImageElement;
                            target.src = '/ui/assets/bird-placeholder.svg';
                            target.classList.add('p-2');
                          }}
                          loading="lazy"
                          decoding="async"
                          fetchpriority="low"
                        />
                      </div>
                      <div>
                        <div class="font-bold">
                          {result.commonName || t('search.detailsPanel.unknownSpecies')}
                        </div>
                        <div class="text-xs opacity-50">{result.scientificName || ''}</div>
                      </div>
                    </div>
                  </td>
                  <td>
                    <div class="flex items-center">
                      <div class="flex items-center gap-2 w-full">
                        <div
                          class="w-16 h-4 rounded-full overflow-hidden bg-base-200"
                          role="progressbar"
                          aria-valuenow={Math.round(result.confidence * 100)}
                          aria-valuemin="0"
                          aria-valuemax="100"
                          aria-valuetext="{Math.round(result.confidence * 100)}%"
                        >
                          <div
                            class="h-full {result.confidence >= 0.8
                              ? 'bg-success'
                              : result.confidence >= 0.4
                                ? 'bg-warning'
                                : 'bg-error'}"
                            style:width="{result.confidence * 100}%"
                          ></div>
                        </div>
                        <span class="ml-1 font-semibold"
                          >{Math.round(result.confidence * 100)}%</span
                        >
                      </div>
                    </div>
                  </td>
                  <td>
                    <div class="flex gap-1 flex-wrap">
                      <div
                        class="status-badge {result.verified === 'correct'
                          ? 'correct'
                          : result.verified === 'false_positive'
                            ? 'false'
                            : 'unverified'}"
                      >
                        {result.verified === 'correct'
                          ? t('search.statusBadges.verified')
                          : result.verified === 'false_positive'
                            ? t('search.statusBadges.false')
                            : t('search.statusBadges.unverified')}
                      </div>
                      <div class="status-badge {result.locked ? 'locked' : 'unverified'}">
                        {result.locked
                          ? t('search.statusBadges.locked')
                          : t('search.statusBadges.unlocked')}
                      </div>
                    </div>
                  </td>
                  <td>
                    <div class="flex gap-1">
                      <button
                        class="btn btn-xs btn-square"
                        onclick={e => {
                          e.preventDefault();
                          // TODO: Implement audio playback function
                        }}
                        disabled={!result.hasAudio}
                        aria-label={t('search.detailsPanel.playAudio', {
                          species: result.commonName || t('search.detailsPanel.unknownSpecies'),
                        })}
                        aria-pressed="false"
                      >
                        <Music class="size-4" />
                      </button>
                      <button
                        class="btn btn-xs btn-square"
                        onclick={() => (window.location.href = `/ui/detections/${result.id}`)}
                        aria-label={t('search.detailsPanel.viewDetails', {
                          species: result.commonName || t('search.detailsPanel.unknownSpecies'),
                        })}
                      >
                        <Eye class="size-4" />
                      </button>
                      <button
                        class="btn btn-xs btn-square expand-btn"
                        onclick={e => {
                          e.preventDefault();
                          toggleExpand(result.id);
                        }}
                        data-id={result.id}
                        aria-label={isExpanded(result.id)
                          ? t('search.detailsPanel.collapseDetails', {
                              species: result.commonName || t('search.detailsPanel.unknownSpecies'),
                            })
                          : t('search.detailsPanel.expandDetails', {
                              species: result.commonName || t('search.detailsPanel.unknownSpecies'),
                            })}
                        aria-expanded={isExpanded(result.id)}
                        aria-controls="expanded-row-{result.id}"
                      >
                        <span
                          class="transition-transform duration-200"
                          class:rotate-180={isExpanded(result.id)}
                          aria-hidden="true"
                        >
                          <ChevronDown class="size-4" />
                        </span>
                      </button>
                    </div>
                  </td>
                </tr>

                <!-- Expanded row -->
                {#if isExpanded(result.id)}
                  <tr class="expanded-row" id="expanded-row-{result.id}">
                    <td colspan="6" class="p-0 border-t-0">
                      <div class="p-4 {index % 2 === 0 ? 'bg-base-100' : 'bg-base-200'}">
                        <!-- Expanded content -->
                        <div class="grid grid-cols-1 lg:grid-cols-3 gap-4">
                          <!-- Weather Information Container -->
                          <div class="bg-base-200 rounded-box p-4">
                            <WeatherInfo detectionId={result.id} units={temperatureUnits} />
                          </div>

                          <!-- Bird Image Container (Middle Column) -->
                          <div
                            class="bg-base-200 rounded-box p-4 flex flex-col justify-center items-center"
                          >
                            <div
                              class="w-full aspect-square rounded-md overflow-hidden bg-gray-100 cursor-pointer hover:brightness-90 transition-all focus:outline-hidden focus:ring-2 focus:ring-primary"
                              onclick={() => toggleExpand(result.id)}
                              onkeydown={e => {
                                if (e.key === 'Enter' || e.key === ' ') {
                                  e.preventDefault();
                                  toggleExpand(result.id);
                                }
                              }}
                              role="button"
                              tabindex="0"
                              aria-label={t('search.detailsPanel.collapseDetails', {
                                species:
                                  result.commonName || t('search.detailsPanel.unknownSpecies'),
                              })}
                              aria-expanded={isExpanded(result.id)}
                              aria-controls="expanded-row-{result.id}"
                              title={t('search.detailsPanel.clickToCollapse')}
                            >
                              <img
                                src="/api/v2/media/species-image?name={encodeURIComponent(
                                  result.scientificName
                                )}"
                                alt={result.commonName || t('search.detailsPanel.unknownSpecies')}
                                class="w-full h-full object-cover"
                                onerror={e => {
                                  const target = e.currentTarget as HTMLImageElement;
                                  target.src = '/ui/assets/bird-placeholder.svg';
                                  target.classList.add('p-2');
                                }}
                                loading="lazy"
                                decoding="async"
                                fetchpriority="low"
                              />
                            </div>
                          </div>

                          <!-- Audio Player -->
                          <div class="bg-base-200 rounded-box p-4">
                            <h3 class="text-lg font-semibold mb-2">
                              {t('search.detailsPanel.audioPlayer')}
                            </h3>
                            <AudioPlayer
                              audioUrl="/api/v2/audio/{result.id}"
                              detectionId={result.id}
                              width={400}
                              height={200}
                              showDownload={true}
                              showSpectrogram={true}
                            />
                          </div>
                        </div>
                      </div>
                    </td>
                  </tr>
                {/if}
              {/each}
            </tbody>
          </table>
        </div>

        <!-- Mobile card list -->
        <div class="md:hidden mt-4 space-y-2" aria-labelledby="search-results-heading">
          {#each results as result (result.id)}
            <section class="bg-base-100 rounded-lg p-3">
              <div class="flex items-start gap-3">
                <!-- Time of Day + Date/Time -->
                <div class="w-16 shrink-0 text-sm opacity-80">
                  <div class="flex items-center gap-1">
                    <TimeOfDayIcon timeOfDay={result.timeOfDay as any} className="size-4" />
                    <span class="capitalize">{result.timeOfDay}</span>
                  </div>
                  <div class="mt-1 text-xs opacity-70 leading-tight">
                    {formatDate(result.timestamp)}
                  </div>
                </div>

                <!-- Thumbnail and names -->
                <div class="flex-1 min-w-0">
                  <div class="flex items-center gap-2">
                    <div class="w-12 h-12 rounded-md overflow-hidden bg-base-200 shrink-0">
                      <img
                        src="/api/v2/media/species-image?name={encodeURIComponent(
                          result.scientificName
                        )}"
                        alt={result.commonName || t('search.detailsPanel.unknownSpecies')}
                        class="w-full h-full object-cover"
                        onerror={handleBirdImageError}
                        loading="lazy"
                        decoding="async"
                        fetchpriority="low"
                      />
                    </div>
                    <div class="min-w-0">
                      <div class="font-semibold leading-tight truncate">
                        {result.commonName || t('search.detailsPanel.unknownSpecies')}
                      </div>
                      <div class="text-xs opacity-60 truncate">{result.scientificName || ''}</div>
                    </div>
                  </div>

                  <!-- Confidence + Status -->
                  <div class="mt-2 flex items-center gap-2">
                    <span
                      class="badge {result.confidence >= 0.8
                        ? 'badge-success'
                        : result.confidence >= 0.4
                          ? 'badge-warning'
                          : 'badge-error'}"
                    >
                      {Math.round(result.confidence * 100)}%
                    </span>
                    <div class="flex gap-1 flex-wrap">
                      <div
                        class="status-badge {result.verified === 'correct'
                          ? 'correct'
                          : result.verified === 'false_positive'
                            ? 'false'
                            : 'unverified'}"
                      >
                        {result.verified === 'correct'
                          ? t('search.statusBadges.verified')
                          : result.verified === 'false_positive'
                            ? t('search.statusBadges.false')
                            : t('search.statusBadges.unverified')}
                      </div>
                      <div class="status-badge {result.locked ? 'locked' : 'unverified'}">
                        {result.locked
                          ? t('search.statusBadges.locked')
                          : t('search.statusBadges.unlocked')}
                      </div>
                    </div>
                  </div>

                  <!-- Actions -->
                  <div class="mt-2 flex items-center gap-2">
                    <button
                      class="btn btn-primary btn-sm"
                      onclick={() => openMobilePlayer(result)}
                      disabled={!result.hasAudio}
                      aria-label={t('search.detailsPanel.playAudio', {
                        species: result.commonName || t('search.detailsPanel.unknownSpecies'),
                      })}
                    >
                      <Volume2 class="size-4" />
                      {t('common.actions.play')}
                    </button>
                    <button
                      class="btn btn-outline btn-sm"
                      onclick={() => (window.location.href = `/ui/detections/${result.id}`)}
                      aria-label={t('search.detailsPanel.viewDetails', {
                        species: result.commonName || t('search.detailsPanel.unknownSpecies'),
                      })}
                    >
                      {t('common.actions.view')}
                    </button>
                  </div>
                </div>
              </div>
            </section>
          {/each}

          {#if showMobilePlayer}
            <div class="md:hidden">
              <MobileAudioPlayer
                audioUrl={selectedAudioUrl}
                speciesName={selectedSpeciesName}
                detectionId={selectedDetectionId}
                onClose={closeMobilePlayer}
              />
            </div>
          {/if}
        </div>
      {/if}

      <!-- Empty state - when search returns no results -->
      {#if formSubmitted && !isLoading && results.length === 0 && !errorMessage}
        <div
          class="mt-6 bg-base-200 rounded-lg p-4 flex flex-col items-center justify-center min-h-[200px]"
        >
          <FrownIcon class="size-12" />
          <p class="mt-2 text-base-content opacity-70">{t('search.noResultsFound')}</p>
          <p class="text-sm text-base-content opacity-50">{t('search.noResultsHint')}</p>
        </div>
      {/if}

      <!-- Pagination - visible when results are available -->
      {#if formSubmitted && !isLoading && totalPages > 1}
        <div class="flex justify-center mt-6">
          <div class="join">
            <button
              class="join-item btn"
              onclick={() => goToPage(currentPage - 1)}
              disabled={currentPage <= 1}>«</button
            >
            <button class="join-item btn"
              >{t('search.pagination.page', { current: currentPage, total: totalPages })}</button
            >
            <button
              class="join-item btn"
              onclick={() => goToPage(currentPage + 1)}
              disabled={currentPage >= totalPages}>»</button
            >
          </div>
        </div>
      {/if}
    </div>
  </div>
</div>

<style>
  .card-padding {
    padding: 1rem;
  }

  @media (min-width: 768px) {
    .card-padding {
      padding: 1.5rem;
    }
  }

  .tooltip {
    position: absolute;
    background-color: #1f2937;
    color: white;
    padding: 0.5rem;
    border-radius: 0.25rem;
    font-size: 0.875rem;
    margin-top: 0.25rem;
    z-index: 10;
  }

  .help-icon {
    cursor: help;
    font-size: 0.875rem;
    color: #6b7280;
  }

  .status-badge {
    padding: 0.125rem 0.5rem;
    border-radius: 0.375rem;
    font-size: 0.75rem;
    font-weight: 500;
  }

  .status-badge.correct {
    background-color: #10b981;
    color: white;
  }

  .status-badge.false {
    background-color: #ef4444;
    color: white;
  }

  .status-badge.unverified {
    background-color: #6b7280;
    color: white;
  }

  .status-badge.locked {
    background-color: #f59e0b;
    color: white;
  }

  .expanded-row td {
    animation: slideDown 0.3s ease-out;
  }

  @keyframes slideDown {
    from {
      opacity: 0;
      transform: translateY(-10px);
    }

    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  .search-form-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 768px) {
    .search-form-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  .search-confidence-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 768px) {
    .search-confidence-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  .search-filters-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 768px) {
    .search-filters-grid {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }
  }

  .search-date-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 768px) {
    .search-date-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }
</style>
