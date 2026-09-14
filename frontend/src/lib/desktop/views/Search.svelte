<script lang="ts">
  import SourceBadge from '$lib/desktop/features/dashboard/components/SourceBadge.svelte';
  import DetectionResultRow from '$lib/desktop/components/data/DetectionResultRow.svelte';
  import MobileAudioPlayer from '$lib/desktop/components/media/MobileAudioPlayer.svelte';
  import ActionMenu from '$lib/desktop/components/ui/ActionMenu.svelte';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';
  import DatePicker from '$lib/desktop/components/ui/DatePicker.svelte';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils';
  import TimeOfDayIcon from '$lib/desktop/components/ui/TimeOfDayIcon.svelte';
  import { getLocale, t } from '$lib/i18n';
  import { toastActions } from '$lib/stores/toast';
  import { api, fetchWithCSRF } from '$lib/utils/api';
  import { getLocalDateString, parseLocalDateString } from '$lib/utils/date';
  import { ArrowDownUp, ChevronDown, FrownIcon, Search, Volume2, XCircle } from '@lucide/svelte';
  import { navigation } from '$lib/stores/navigation.svelte';
  import { onMount, untrack } from 'svelte';
  import { isAuthenticated } from '$lib/utils/auth';
  import { loggers } from '$lib/utils/logger';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { useDetectionActions } from '$lib/desktop/features/detections/composables/useDetectionActions.svelte';
  import {
    hydrateExcludedSpecies,
    isExcluded as isSpeciesExcluded,
    setExcluded,
  } from '$lib/stores/excludedSpecies.svelte';
  import { downloadDetectionAudio } from '$lib/utils/audioDownload';
  import type { Detection } from '$lib/types/detection.types';
  import {
    loadDictionary,
    searchScientificByCommon,
    PER_VISITOR_SPECIES_LOCALE_ENABLED,
  } from '$lib/stores/speciesDictionary.svelte';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';
  import DetectionExpandedPanel from '$lib/desktop/components/data/DetectionExpandedPanel.svelte';

  // SPINNER CONTROL: Set to false to disable loading spinners (reduces flickering)
  // Change back to true to re-enable spinners for testing
  const ENABLE_LOADING_SPINNERS = false;

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
    source?: string;
    modelType?: string;
  }

  interface AudioSourceOption {
    id: string;
    name: string;
  }

  type VerifiedStatus = 'any' | 'correct' | 'unverified' | 'false_positive';
  type LockedStatus = 'any' | 'locked' | 'unlocked';
  type TimeOfDayFilter = 'any' | 'day' | 'night' | 'sunrise' | 'sunset';
  type SortBy = 'date_desc' | 'date_asc' | 'species_asc' | 'confidence_desc';

  const logger = loggers.ui;

  /**
   * Submit a verification status change for a search result.
   * Updates the local results array on success so the UI reflects the change immediately.
   */
  async function submitVerification(
    resultId: string,
    status: 'correct' | 'false_positive'
  ): Promise<void> {
    try {
      await fetchWithCSRF(`/api/v2/detections/${resultId}/review`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ verified: status }),
      });

      // Update the local results array so the status badge reflects the change.
      // If a verification filter is active (not 'any'), remove the item from results
      // since it no longer matches the active filter, and adjust the total count.
      const idx = results.findIndex(r => r.id === resultId);
      if (idx !== -1) {
        if (verifiedStatus !== 'any') {
          results.splice(idx, 1);
          totalResults = Math.max(0, totalResults - 1);
        } else {
          // eslint-disable-next-line security/detect-object-injection -- idx is validated from findIndex
          results[idx].verified = status;
        }
      }

      toastActions.success(
        status === 'correct'
          ? t('search.review.markedCorrect')
          : t('search.review.markedFalsePositive')
      );
    } catch (error) {
      toastActions.error(t('search.review.failed'));
      logger.error('Error updating verification status:', error);
    }
  }

  /**
   * Adapt a search result to the Detection shape the shared ActionMenu and
   * detection action handlers expect. /api/v2/search returns a flatter row than
   * the detections API, so the fields the menu never reads are filled from the
   * timestamp (date/time drive the audio download filename) or left empty.
   */
  function toDetection(result: SearchResult): Detection {
    const [date = '', time = ''] = (result.timestamp ?? '').split(/[T ]/);
    return {
      id: Number(result.id),
      date,
      time: time.slice(0, 8),
      timestamp: result.timestamp,
      source: result.source ? { id: result.source, displayName: result.source } : null,
      beginTime: '',
      endTime: '',
      speciesCode: '',
      scientificName: result.scientificName,
      commonName: result.commonName,
      confidence: result.confidence,
      modelType: result.modelType,
      verified:
        result.verified === 'correct' || result.verified === 'false_positive'
          ? result.verified
          : 'unverified',
      locked: result.locked,
      timeOfDay: result.timeOfDay,
    };
  }

  // Per-detection action handlers (review/ignore/lock/delete), shared with the
  // dashboard, detections list and analytics summary so this list's Actions
  // column never diverges from the rest of the app. Verification stays on the
  // local submitVerification(), which updates the row in place (and drops it
  // when a verification filter is active) instead of re-running the search.
  const detectionActions = useDetectionActions({
    onRefresh: () => void submitSearch(currentPage),
    isSpeciesExcluded,
    onToggleExclusion: setExcluded,
  });

  onMount(() => {
    void hydrateExcludedSpecies();
  });

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
  let sourceFilter = $state('');
  let availableSources = $state<AudioSourceOption[]>([]);
  let errorMessage = $state('');
  // PERFORMANCE OPTIMIZATION: Use Set instead of object for expandedItems
  // Set operations (has/add/delete) are faster than object property access
  // and provide better memory efficiency for tracking expanded table rows
  let expandedItems = $state(new Set<string>());
  let hasConfidenceError = $state(false);
  let showTooltip = $state<string | null>(null);

  // Fetch available audio sources for the source filter dropdown.
  // The endpoint requires authentication, so skip the request for guests
  // instead of letting it fail with a 401 on every page visit.
  $effect(() => {
    if (!$isAuthenticated) {
      availableSources = [];
      return;
    }
    const controller = new AbortController();
    untrack(() => {
      api
        .get<{ sources: AudioSourceOption[] }>('/api/v2/system/audio/sources', {
          signal: controller.signal,
        })
        .then(data => {
          availableSources = data.sources ?? [];
        })
        .catch(error => {
          if (!controller.signal.aborted) {
            logger.warn('Failed to load audio sources for source filter:', error);
            availableSources = [];
          }
        });
    });
    return () => controller.abort();
  });

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
  let selectedModelType = $state('');

  function openMobilePlayer(result: SearchResult) {
    if (!result?.id) return;
    selectedAudioUrl = buildAppUrl(`/api/v2/audio/${result.id}`);
    selectedSpeciesName = localizeSpeciesName(result.scientificName, result.commonName);
    selectedDetectionId = result.id;
    selectedModelType = result.modelType ?? '';
    showMobilePlayer = true;
  }

  function closeMobilePlayer() {
    showMobilePlayer = false;
    selectedAudioUrl = '';
    selectedSpeciesName = '';
    selectedDetectionId = undefined;
    selectedModelType = '';
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

  // Form submission. Guarded against overlapping calls so any caller
  // (form submit, pagination, sort dropdown, future programmatic trigger)
  // cannot start a new request while one is still in flight; an older
  // response would otherwise clobber newer state on completion.
  async function submitSearch(page = 1) {
    if (isLoading) return;
    if (!validateForm()) return;

    isLoading = true;
    errorMessage = '';
    currentPage = page;
    expandedItems.clear(); // Reset expanded state when loading new results

    try {
      // Resolve the typed text to scientific names via the visitor's per-locale
      // dictionary. Always send the raw term as the free-text species filter too:
      // the backend OR-s the free-text species (scientific_name LIKE) with the
      // resolved speciesScientific label IDs, so sending both makes the resolved
      // path a strict superset and avoids dropping scientific-substring matches
      // when the typed text is both a resolvable common name and a substring of a
      // scientific name.
      //
      // Ensure the per-locale dictionary is loaded before resolving. The submit
      // handler can fire immediately after first paint or a locale switch, before
      // the dictionary fetch has completed; resolving against empty maps would
      // silently fall back to the raw term (which the backend cannot resolve for a
      // foreign-locale name). loadDictionary is cached, so awaiting it when already
      // loaded is effectively instant.
      //
      // PARKED behind PER_VISITOR_SPECIES_LOCALE_ENABLED: while off, we skip the
      // per-visitor dictionary entirely and send only the raw term, so search
      // resolves in the server-side species language (settings.BirdNET.Locale).
      let resolvedScientific: string[] = [];
      if (PER_VISITOR_SPECIES_LOCALE_ENABLED) {
        await loadDictionary();
        resolvedScientific = searchScientificByCommon(speciesSearchTerm);
      }

      // Build request body
      const requestBody = {
        species: speciesSearchTerm,
        speciesScientific: resolvedScientific,
        dateStart: dateRange.start,
        dateEnd: dateRange.end,
        confidenceMin: confidenceRange.min / 100,
        confidenceMax: confidenceRange.max / 100,
        verifiedStatus: verifiedStatus,
        lockedStatus: lockedStatus,
        deviceFilter: sourceFilter,
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
    sourceFilter = '';
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

  // Handle sorting. Guarded against concurrent in-flight searches so a
  // rapidly-clicked sort option cannot race an already-running submitSearch
  // and clobber newer state with a stale response.
  function changeSort(sortOption: SortBy) {
    if (isLoading) return;
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

  /**
   * Navigate to the detection detail page.
   */
  function goToDetectionDetail(recordId: string) {
    navigation.navigate(`/ui/detections/${recordId}`);
  }

  /**
   * Handle a tap on a mobile result card: tapping anywhere on the card
   * except an action button (review, play, view) opens the detection
   * details, mirroring the dedicated "View" button for keyboard users.
   */
  function handleMobileCardClick(recordId: string, event: MouseEvent) {
    if ((event.target as HTMLElement).closest('button')) return;
    goToDetectionDetail(recordId);
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
  <div class="card bg-[var(--color-base-100)] shadow-xs">
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
                <div class="text-[var(--color-error)] text-sm mt-1" role="alert">
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
                  <option value="correct">{t('search.verifiedOptions.verified')}</option>
                  <option value="unverified">{t('search.verifiedOptions.unverified')}</option>
                  <option value="false_positive">{t('common.review.status.falsePositive')}</option>
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

              <!-- Audio Source -->
              {#if availableSources.length > 1}
                <div class="form-control">
                  <label class="label" for="sourceFilter">
                    <span class="label-text">{t('search.fields.source')}</span>
                  </label>
                  <select id="sourceFilter" bind:value={sourceFilter} class="select w-full">
                    <option value="">{t('search.sourceOptions.any')}</option>
                    {#each availableSources as source (source.id)}
                      <option value={source.name}>{source.name}</option>
                    {/each}
                  </select>
                </div>
              {/if}
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
  <div class="card bg-[var(--color-base-100)] shadow-xs">
    <div class="card-body card-padding">
      <div class="flex items-center justify-between">
        <h2 class="card-title" id="search-results-heading">{t('search.results')}</h2>

        <!-- Results Count & Sorting -->
        {#if formSubmitted}
          <div class="flex items-center gap-4">
            <span class="text-sm text-[var(--color-base-content)] opacity-70" aria-live="polite"
              >{formatResultsCount(totalResults)}</span
            >
            <div class="dropdown dropdown-end">
              <div
                tabindex={isLoading ? -1 : 0}
                role="button"
                class="btn btn-sm btn-outline"
                class:opacity-50={isLoading}
                class:pointer-events-none={isLoading}
                aria-haspopup="true"
                aria-expanded="false"
                aria-disabled={isLoading}
                aria-label={t('common.sort')}
              >
                <ArrowDownUp class="size-5" />
                {t('common.sort')}
              </div>
              <ul
                tabindex="0"
                class="dropdown-content z-1 menu p-2 shadow-xs bg-[var(--color-base-100)] rounded-box w-52"
                role="menu"
              >
                <li role="menuitem">
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm justify-start w-full"
                    disabled={isLoading}
                    onclick={() => changeSort('date_desc')}
                    >{t('search.sortOptions.dateDesc')}</button
                  >
                </li>
                <li role="menuitem">
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm justify-start w-full"
                    disabled={isLoading}
                    onclick={() => changeSort('date_asc')}>{t('search.sortOptions.dateAsc')}</button
                  >
                </li>
                <li role="menuitem">
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm justify-start w-full"
                    disabled={isLoading}
                    onclick={() => changeSort('species_asc')}
                    >{t('search.sortOptions.speciesAsc')}</button
                  >
                </li>
                <li role="menuitem">
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm justify-start w-full"
                    disabled={isLoading}
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
          class="mt-6 bg-[var(--color-base-200)] rounded-lg p-4 flex flex-col items-center justify-center min-h-[200px]"
          aria-labelledby="search-results-heading"
        >
          <span class="text-[var(--color-base-content)] opacity-30 text-[4rem]" aria-hidden="true">
            <Search class="size-12" />
          </span>
          <p class="text-[var(--color-base-content)] opacity-50 text-center mt-4">
            {t('search.noSearchPerformed')}
          </p>
          <p class="text-[var(--color-base-content)] opacity-50 text-center text-sm">
            {t('search.noSearchPerformedHint')}
          </p>
        </div>
      {/if}

      <!-- Loading indicator -->
      {#if ENABLE_LOADING_SPINNERS && isLoading && formSubmitted}
        <div
          class="mt-6 bg-[var(--color-base-200)] rounded-lg p-4 flex flex-col items-center justify-center min-h-[200px]"
          aria-live="polite"
          aria-busy="true"
        >
          <span
            class="loading loading-spinner loading-lg text-[var(--color-primary)]"
            aria-hidden="true"
          ></span>
          <p class="text-[var(--color-base-content)] opacity-50 text-center mt-4">
            {t('search.loadingResults')}
          </p>
        </div>
      {/if}

      <!-- Search results - table for md+, cards for mobile -->
      {#if formSubmitted && !isLoading && results.length > 0}
        <!-- Desktop/tablet table -->
        <div class="overflow-x-auto mt-4 hidden md:block" aria-labelledby="search-results-heading">
          <table class="table table-hover w-full">
            <thead>
              <tr>
                <th scope="col">{t('search.tableHeaders.dateTime')}</th>
                <th scope="col">{t('search.tableHeaders.timeOfDay')}</th>
                <th scope="col">{t('search.tableHeaders.species')}</th>
                <th scope="col">{t('search.tableHeaders.confidence')}</th>
                <th scope="col">{t('search.tableHeaders.source')}</th>
                <th scope="col">{t('search.tableHeaders.status')}</th>
                <th scope="col">{t('search.tableHeaders.actions')}</th>
              </tr>
            </thead>
            <tbody>
              <!-- Loop through results -->
              {#each results as result, index (result.id)}
                {@const displayName = localizeSpeciesName(result.scientificName, result.commonName)}
                <DetectionResultRow
                  detectionId={result.id}
                  rowId="expanded-row-{result.id}"
                  {index}
                  timestamp={result.timestamp}
                  timeOfDay={result.timeOfDay}
                  scientificName={result.scientificName}
                  {displayName}
                  confidence={result.confidence}
                  verified={result.verified}
                  locked={result.locked}
                  source={result.source ? { id: result.source, displayName: result.source } : null}
                  expanded={isExpanded(result.id)}
                  onToggleExpand={() => toggleExpand(result.id)}
                  onViewDetails={() => goToDetectionDetail(result.id)}
                >
                  {#snippet actions()}
                    {@const detection = toDetection(result)}
                    <ActionMenu
                      {detection}
                      isExcluded={isSpeciesExcluded(result.commonName)}
                      onMarkCorrect={() => submitVerification(result.id, 'correct')}
                      onMarkFalsePositive={() => submitVerification(result.id, 'false_positive')}
                      onReview={() => detectionActions.handleReview(detection)}
                      onToggleSpecies={() => detectionActions.handleToggleSpecies(detection)}
                      onToggleLock={() => detectionActions.handleToggleLock(detection)}
                      onDelete={() => detectionActions.handleDelete(detection)}
                      onDownload={result.hasAudio
                        ? () => downloadDetectionAudio(detection)
                        : undefined}
                    />
                  {/snippet}
                </DetectionResultRow>

                <!-- Expanded row -->
                {#if isExpanded(result.id)}
                  <tr class="expanded-row" id="expanded-row-{result.id}">
                    <td colspan="7" class="p-0 border-t-0">
                      <div
                        class="expanded-panel p-4 text-left {index % 2 === 0
                          ? 'bg-[var(--color-base-100)]'
                          : 'bg-[var(--color-base-200)]'}"
                      >
                        <DetectionExpandedPanel
                          detectionId={result.id}
                          scientificName={result.scientificName}
                          commonName={result.commonName}
                          {displayName}
                          hasAudio={result.hasAudio}
                          timestamp={result.timestamp}
                          modelType={result.modelType}
                          rowId="expanded-row-{result.id}"
                          onCollapse={() => toggleExpand(result.id)}
                        />
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
            {@const displayName = localizeSpeciesName(result.scientificName, result.commonName)}
            {@const detection = toDetection(result)}
            <!-- Tapping anywhere on the card except an action button opens the
                 detection details; the "View" button remains a redundant, explicit
                 keyboard-focusable equivalent. -->
            <div
              class="bg-[var(--color-base-100)] rounded-lg p-3 cursor-pointer active:bg-[var(--color-base-200)] focus-visible:outline-2 focus-visible:outline-[var(--color-primary)]"
              role="button"
              tabindex="0"
              aria-label={t('search.detailsPanel.viewDetails', {
                species: displayName || t('search.detailsPanel.unknownSpecies'),
              })}
              onclick={e => handleMobileCardClick(result.id, e)}
              onkeydown={e => {
                if (
                  (e.key === 'Enter' || e.key === ' ') &&
                  !(e.target as HTMLElement).closest('button')
                ) {
                  e.preventDefault();
                  goToDetectionDetail(result.id);
                }
              }}
            >
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
                    <div
                      class="w-12 h-9 rounded-md overflow-hidden bg-[var(--color-base-200)] shrink-0"
                    >
                      <img
                        src={buildAppUrl(
                          `/api/v2/media/species-image?name=${encodeURIComponent(result.scientificName)}`
                        )}
                        alt={displayName || t('search.detailsPanel.unknownSpecies')}
                        class="w-full h-full object-cover"
                        onerror={handleBirdImageError}
                        loading="lazy"
                        decoding="async"
                        fetchpriority="low"
                      />
                    </div>
                    <div class="min-w-0">
                      <div class="font-semibold leading-tight truncate">
                        {displayName || t('search.detailsPanel.unknownSpecies')}
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
                            ? t('common.review.status.falsePositive')
                            : t('search.statusBadges.unverified')}
                      </div>
                      <div class="status-badge {result.locked ? 'locked' : 'unverified'}">
                        {result.locked
                          ? t('search.statusBadges.locked')
                          : t('search.statusBadges.unlocked')}
                      </div>
                    </div>
                  </div>

                  <!-- Source -->
                  {#if result.source}
                    <div class="mt-1">
                      <SourceBadge
                        detection={{ source: { id: result.source, displayName: result.source } }}
                        variant="inline"
                      />
                    </div>
                  {/if}

                  <!-- Actions -->
                  <div class="mt-2 flex items-center gap-2 flex-wrap">
                    <ActionMenu
                      {detection}
                      isExcluded={isSpeciesExcluded(result.commonName)}
                      onMarkCorrect={() => submitVerification(result.id, 'correct')}
                      onMarkFalsePositive={() => submitVerification(result.id, 'false_positive')}
                      onReview={() => detectionActions.handleReview(detection)}
                      onToggleSpecies={() => detectionActions.handleToggleSpecies(detection)}
                      onToggleLock={() => detectionActions.handleToggleLock(detection)}
                      onDelete={() => detectionActions.handleDelete(detection)}
                      onDownload={result.hasAudio
                        ? () => downloadDetectionAudio(detection)
                        : undefined}
                    />
                    {#if result.hasAudio}
                      <button
                        class="btn btn-primary btn-sm"
                        onclick={() => openMobilePlayer(result)}
                        aria-label={t('search.detailsPanel.playAudio', {
                          species: displayName || t('search.detailsPanel.unknownSpecies'),
                        })}
                      >
                        <Volume2 class="size-4" />
                        {t('common.actions.play')}
                      </button>
                    {/if}
                    <button
                      class="btn btn-outline btn-sm"
                      onclick={() => goToDetectionDetail(result.id)}
                      aria-label={t('search.detailsPanel.viewDetails', {
                        species: displayName || t('search.detailsPanel.unknownSpecies'),
                      })}
                    >
                      {t('common.actions.view')}
                    </button>
                  </div>
                </div>
              </div>
            </div>
          {/each}

          {#if showMobilePlayer}
            <div class="md:hidden">
              <MobileAudioPlayer
                audioUrl={selectedAudioUrl}
                speciesName={selectedSpeciesName}
                detectionId={selectedDetectionId}
                modelType={selectedModelType}
                onClose={closeMobilePlayer}
              />
            </div>
          {/if}
        </div>
      {/if}

      <!-- Empty state - when search returns no results -->
      {#if formSubmitted && !isLoading && results.length === 0 && !errorMessage}
        <div
          class="mt-6 bg-[var(--color-base-200)] rounded-lg p-4 flex flex-col items-center justify-center min-h-[200px]"
        >
          <FrownIcon class="size-12" />
          <p class="mt-2 text-[var(--color-base-content)] opacity-70">
            {t('search.noResultsFound')}
          </p>
          <p class="text-sm text-[var(--color-base-content)] opacity-50">
            {t('search.noResultsHint')}
          </p>
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

<!-- Per-detection confirmation (ignore/lock/delete from a result row or card) -->
{#if detectionActions.selectedDetection}
  <ConfirmModal
    isOpen={detectionActions.showConfirmModal}
    title={detectionActions.confirmModalConfig.title}
    message={detectionActions.confirmModalConfig.message}
    confirmLabel={detectionActions.confirmModalConfig.confirmLabel}
    onClose={detectionActions.closeModal}
    onConfirm={detectionActions.confirmModal}
  />
{/if}

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
      grid-template-columns: repeat(4, minmax(0, 1fr));
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
