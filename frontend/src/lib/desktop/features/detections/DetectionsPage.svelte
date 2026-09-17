<script lang="ts">
  import { onMount } from 'svelte';
  import { t } from '$lib/i18n';
  import { fetchWithCSRF } from '$lib/utils/api';
  import type {
    Detection,
    DetectionFilters,
    DetectionsListData,
    DetectionQueryParams,
    DetectionSortBy,
  } from '$lib/types/detection.types';
  import DetectionsCard from './components/DetectionsCard.svelte';
  import DetectionFilterPanel from './components/DetectionFilterPanel.svelte';
  import { getLogger } from '$lib/utils/logger';
  import { getLocalDateString } from '$lib/utils/date';
  import { navigation } from '$lib/stores/navigation.svelte';
  import {
    applyDetectionFiltersToParams,
    emptyDetectionFilters,
    hasActiveDetectionFilters,
    parseDetectionFilters,
  } from '$lib/utils/detectionFilters';

  const logger = getLogger('app');

  let detectionsData = $state<DetectionsListData | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;

  // The applied filter set, mirrored from the URL. The filter panel edits a draft
  // of this and only writes back on submit.
  let filters = $state<DetectionFilters>(emptyDetectionFilters());

  // Local storage keys for user preferences
  const RESULTS_PER_PAGE_KEY = 'birdnet-detections-results-per-page';
  const SORT_BY_KEY = 'birdnet-detections-sort-by';

  const ALLOWED_SORT_VALUES = new Set<string>([
    'date_desc',
    'date_asc',
    'species_asc',
    'species_desc',
    'confidence_asc',
    'confidence_desc',
    'status',
  ]);

  // Get saved preference from localStorage
  function getSavedResultsPerPage(): number {
    if (typeof window !== 'undefined') {
      const saved = localStorage.getItem(RESULTS_PER_PAGE_KEY);
      if (saved && !isNaN(parseInt(saved))) {
        const value = parseInt(saved);
        // Validate it's one of our allowed values
        if ([10, 25, 50, 100].includes(value)) {
          return value;
        }
      }
    }
    return 25; // Default
  }

  // Extract query parameters from URL
  function getQueryParams(): DetectionQueryParams {
    const params = new URLSearchParams(window.location.search);
    const activeFilters = parseDetectionFilters(params);
    const search = activeFilters.search;

    // Set queryType to 'search' if a free-text query is present
    let queryType = params.get('queryType') as DetectionQueryParams['queryType'];
    if (search && !queryType) {
      queryType = 'search';
    } else if (!queryType) {
      queryType = 'all';
    }

    // Parse and validate numResults
    const numResultsParam = params.get('numResults');
    let numResults = numResultsParam ? parseInt(numResultsParam) : getSavedResultsPerPage();

    // Validate numResults is one of allowed values
    if (isNaN(numResults) || ![10, 25, 50, 100].includes(numResults)) {
      numResults = getSavedResultsPerPage();
    }

    // Parse and validate sortBy from URL or localStorage
    const sortByParam = params.get('sortBy');
    let sortBy: DetectionSortBy | undefined;
    if (sortByParam && ALLOWED_SORT_VALUES.has(sortByParam)) {
      sortBy = sortByParam as DetectionSortBy;
    } else if (typeof window !== 'undefined') {
      const saved = localStorage.getItem(SORT_BY_KEY);
      if (saved && ALLOWED_SORT_VALUES.has(saved)) {
        sortBy = saved as DetectionSortBy;
      }
    }

    // The unfiltered view is pinned to a single day, which is what makes the
    // default page useful. A filtered view must not inherit that pin: the backend
    // restricts results to that one date, so a search for a species last heard in
    // spring would come back empty. Any active filter (not just a free-text query)
    // therefore drops the implicit date.
    const explicitDate = params.get('date')?.trim();
    const date =
      explicitDate || (hasActiveDetectionFilters(activeFilters) ? undefined : getLocalDateString());

    return {
      queryType,
      date,
      hour: params.get('hour') || undefined,
      duration: params.get('duration') ? parseInt(params.get('duration')!) : undefined,
      species: params.get('species') || undefined,
      search: search || undefined,
      numResults,
      offset: parseInt(params.get('offset') || '0'),
      sortBy,
      // Advanced filters. Defaults are omitted so an unfiltered request stays on
      // the cheap query path and shares a cache key with other unfiltered ones.
      start_date: activeFilters.startDate || undefined,
      end_date: activeFilters.endDate || undefined,
      confidenceMin: activeFilters.confidenceMin > 0 ? activeFilters.confidenceMin : undefined,
      confidenceMax: activeFilters.confidenceMax < 100 ? activeFilters.confidenceMax : undefined,
      verified: activeFilters.verified || undefined,
      locked: activeFilters.locked || undefined,
      timeOfDay: activeFilters.timeOfDay || undefined,
      source: activeFilters.source || undefined,
    };
  }

  /** The shape the detections list endpoint returns. */
  interface DetectionsApiResponse {
    data?: Detection[];
    total?: number;
    limit?: number;
    current_page?: number;
    total_pages?: number;
    dashboardSettings?: DetectionsListData['dashboardSettings'];
  }

  // Fetch detections data
  async function fetchDetections() {
    loading = true;
    error = null;

    // Keep the panel in step with the URL before the request goes out, so the
    // form reflects what is being fetched even if the request is slow or fails.
    filters = parseDetectionFilters(new URLSearchParams(window.location.search));

    try {
      const queryParams = getQueryParams();
      // Build query string
      const queryString = new URLSearchParams();
      Object.entries(queryParams).forEach(([key, value]) => {
        if (value !== undefined) {
          queryString.append(key, String(value));
        }
      });

      // Always include weather data for the detections page
      queryString.append('includeWeather', 'true');

      const data = await fetchWithCSRF<DetectionsApiResponse>(
        `/api/v2/detections?${queryString.toString()}`
      );

      // Validate numResults before using
      const validatedNumResults =
        queryParams.numResults !== undefined && [10, 25, 50, 100].includes(queryParams.numResults)
          ? queryParams.numResults
          : getSavedResultsPerPage();

      // Transform API response to match our expected format
      detectionsData = {
        notes: data.data || [],
        queryType: queryParams.queryType || 'all',
        date: queryParams.date?.trim() || getLocalDateString(),
        hour: queryParams.hour ? parseInt(queryParams.hour) : undefined,
        duration: queryParams.duration,
        species: queryParams.species,
        search: queryParams.search,
        filters,
        numResults: validatedNumResults,
        offset: queryParams.offset!,
        totalResults: data.total || 0,
        itemsPerPage: data.limit || validatedNumResults,
        currentPage: data.current_page || 1,
        totalPages: data.total_pages || 1,
        showingFrom: (queryParams.offset || 0) + 1,
        showingTo: Math.min((queryParams.offset || 0) + (data.data?.length || 0), data.total || 0),
        dashboardSettings: data.dashboardSettings,
      };
    } catch (err) {
      error = err instanceof Error ? err.message : t('detections.errors.fetchFailed');
      logger.error('Error fetching detections:', err);
    } finally {
      loading = false;
    }
  }

  /**
   * Push a new query string and refetch.
   *
   * Filter and pagination changes are history entries rather than replacements so
   * the browser back button steps back through them, which is the behaviour the
   * standalone search page could not offer because it kept its filters off the URL.
   */
  function navigateWithParams(params: URLSearchParams) {
    const query = params.toString();
    window.history.pushState(
      {},
      '',
      query ? `${window.location.pathname}?${query}` : window.location.pathname
    );
    fetchDetections();
  }

  // Handle page change
  function handlePageChange(newPage: number) {
    if (detectionsData) {
      const newOffset = (newPage - 1) * detectionsData.itemsPerPage;
      const params = new URLSearchParams(window.location.search);
      params.set('offset', String(newOffset));
      navigateWithParams(params);
    }
  }

  // Handle numResults change with debouncing
  function handleNumResultsChange(newNumResults: number) {
    // Save user preference to localStorage
    if (typeof window !== 'undefined') {
      localStorage.setItem(RESULTS_PER_PAGE_KEY, String(newNumResults));
    }

    // Clear existing timer
    if (debounceTimer) {
      clearTimeout(debounceTimer);
    }

    // Set loading state immediately for user feedback
    loading = true;

    // Debounce the actual fetch
    debounceTimer = setTimeout(() => {
      const params = new URLSearchParams(window.location.search);
      params.set('numResults', String(newNumResults));
      params.set('offset', '0'); // Reset to first page
      navigateWithParams(params);
    }, 300); // 300ms debounce delay
  }

  // Handle sort change from DetectionsList
  function handleSortChange(newSortBy: DetectionSortBy) {
    // Save preference to localStorage
    if (typeof window !== 'undefined') {
      localStorage.setItem(SORT_BY_KEY, newSortBy);
    }

    const params = new URLSearchParams(window.location.search);
    if (newSortBy && newSortBy !== 'date_desc') {
      params.set('sortBy', newSortBy);
    } else {
      params.delete('sortBy');
    }
    params.set('offset', '0'); // Reset to first page
    navigateWithParams(params);
  }

  /**
   * Apply the filter panel's submission.
   *
   * A changed filter set invalidates the current page, so the offset resets. The
   * implicit `date` pin is dropped too: it belongs to the unfiltered single-day
   * view, and leaving it in place would silently restrict a filtered search to
   * one day.
   */
  function handleApplyFilters(newFilters: DetectionFilters) {
    const params = new URLSearchParams(window.location.search);
    applyDetectionFiltersToParams(params, newFilters);
    params.set('offset', '0');

    if (hasActiveDetectionFilters(newFilters)) {
      params.delete('date');
      // queryType is inferred from the parameters server-side; a stale
      // 'hourly'/'species' type would keep applying a drill-down the user has
      // just replaced with an explicit filter set.
      params.delete('queryType');
    }

    navigateWithParams(params);
  }

  /**
   * Clear every filter and drill-down, returning to the default detections view.
   * Page-size and sort preferences live in localStorage, so they survive.
   */
  function handleResetFilters() {
    navigateWithParams(new URLSearchParams());
  }

  // Handle details click
  function handleDetailsClick(id: number) {
    // Navigate to detection details page
    navigation.navigate(`/ui/detections/${id}`);
  }

  // Listen for search updates from the header SearchBox, which writes its own
  // parsed query and filters straight into the URL. Re-reading the URL here keeps
  // the panel and the list consistent with whatever it wrote.
  function handleSearchUpdate() {
    fetchDetections();
  }

  // Handle browser back/forward buttons
  function handlePopState() {
    fetchDetections();
  }

  onMount(() => {
    fetchDetections();

    // Listen for search updates from SearchBox
    window.addEventListener('searchUpdate', handleSearchUpdate);

    // Listen for browser navigation
    window.addEventListener('popstate', handlePopState);

    return () => {
      window.removeEventListener('searchUpdate', handleSearchUpdate);
      window.removeEventListener('popstate', handlePopState);

      // Clear any pending debounce timer
      if (debounceTimer) {
        clearTimeout(debounceTimer);
      }
    };
  });
</script>

<div class="col-span-12 space-y-6">
  <DetectionFilterPanel
    {filters}
    {loading}
    onApply={handleApplyFilters}
    onReset={handleResetFilters}
  />

  <DetectionsCard
    data={detectionsData}
    {loading}
    {error}
    onPageChange={handlePageChange}
    onDetailsClick={handleDetailsClick}
    onRefresh={fetchDetections}
    onNumResultsChange={handleNumResultsChange}
    onSortChange={handleSortChange}
  />
</div>
