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
    hourBandToParam,
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

  /** Page sizes the results selector offers, and the one used when none is stored. */
  const RESULTS_PER_PAGE_OPTIONS = [10, 25, 50, 100];
  const DEFAULT_RESULTS_PER_PAGE = 25;
  const DEFAULT_SORT_BY: DetectionSortBy = 'date_desc';

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
        if (RESULTS_PER_PAGE_OPTIONS.includes(value)) {
          return value;
        }
      }
    }
    return DEFAULT_RESULTS_PER_PAGE;
  }

  // Extract query parameters from URL
  function getQueryParams(): DetectionQueryParams {
    const params = new URLSearchParams(window.location.search);
    const activeFilters = parseDetectionFilters(params);
    // The panel's species field is filled from either `search` or `species`, but
    // the request has to keep them apart: `species` is an exact match and
    // `search` a free-text one, so sending both would intersect them and narrow
    // a drill-down to the rows that happen to match twice.
    const search = params.get('search')?.trim() ?? '';

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
    if (isNaN(numResults) || !RESULTS_PER_PAGE_OPTIONS.includes(numResults)) {
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

    // A dashboard or analytics drill-down links in with `date`, `hour` and
    // `duration`, which the dedicated hourly and species query paths read
    // natively; the panel spells the same two constraints as a date range and an
    // hour band. Both spellings describe one filter each, so exactly one of them
    // is sent. The drill-down keeps its own spelling -- and with it the query type
    // that gives the list its "07:00 on <date>" heading -- until the panel is
    // submitted, at which point applyDetectionFiltersToParams has replaced it
    // with the canonical parameters and removed the originals.
    const isDrillDown = !params.has('start_date') && !params.has('end_date');
    const explicitDate = isDrillDown ? params.get('date')?.trim() : undefined;

    // The unfiltered view is pinned to a single day, which is what makes the
    // default page useful. A filtered view must not inherit that pin: the backend
    // restricts results to that one date, so a search for a species last heard in
    // spring would come back empty. Any active filter (not just a free-text query)
    // therefore drops the implicit date.
    const date =
      explicitDate || (hasActiveDetectionFilters(activeFilters) ? undefined : getLocalDateString());

    // queryType=hourly is rejected without an `hour`, so a drill-down that still
    // carries it keeps sending the pair rather than the equivalent band.
    const sendsDrillDownHour = isDrillDown && Boolean(params.get('hour'));
    const hourRange = hourBandToParam(activeFilters);

    return {
      queryType,
      date,
      hour: sendsDrillDownHour ? (params.get('hour') ?? undefined) : undefined,
      duration:
        sendsDrillDownHour && params.get('duration')
          ? parseInt(params.get('duration')!)
          : undefined,
      species: params.get('species') || undefined,
      search: search || undefined,
      numResults,
      offset: parseInt(params.get('offset') || '0'),
      sortBy,
      // Advanced filters. Defaults are omitted so an unfiltered request stays on
      // the cheap query path and shares a cache key with other unfiltered ones.
      start_date: isDrillDown ? undefined : activeFilters.startDate || undefined,
      end_date: isDrillDown ? undefined : activeFilters.endDate || undefined,
      confidenceMin: activeFilters.confidenceMin > 0 ? activeFilters.confidenceMin : undefined,
      confidenceMax: activeFilters.confidenceMax < 100 ? activeFilters.confidenceMax : undefined,
      verified: activeFilters.verified || undefined,
      locked: activeFilters.locked || undefined,
      timeOfDay: activeFilters.timeOfDay || undefined,
      hourRange: sendsDrillDownHour ? undefined : hourRange || undefined,
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

  /**
   * Write the page size and sort order into the URL when they come from stored
   * preferences rather than the link itself.
   *
   * Without this a shared or bookmarked link reproduces the filters but not the
   * pagination, so the recipient sees a different slice of the same query than
   * the sender did. It is a replaceState, not a push: the view has not changed,
   * only its description, and a history entry here would make Back a no-op.
   * Defaults are left out so the plain /ui/detections URL stays clean.
   */
  function syncViewPreferencesToUrl(queryParams: DetectionQueryParams) {
    const params = new URLSearchParams(window.location.search);
    let changed = false;

    if (!params.has('numResults') && queryParams.numResults !== DEFAULT_RESULTS_PER_PAGE) {
      params.set('numResults', String(queryParams.numResults));
      changed = true;
    }
    if (!params.has('sortBy') && queryParams.sortBy && queryParams.sortBy !== DEFAULT_SORT_BY) {
      params.set('sortBy', queryParams.sortBy);
      changed = true;
    }
    if (!changed) return;

    const query = params.toString();
    window.history.replaceState({}, '', `${window.location.pathname}?${query}`);
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
      syncViewPreferencesToUrl(queryParams);
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
        queryParams.numResults !== undefined &&
        RESULTS_PER_PAGE_OPTIONS.includes(queryParams.numResults)
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
    if (newSortBy && newSortBy !== DEFAULT_SORT_BY) {
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
   * drill-down parameters are dropped by applyDetectionFiltersToParams, which has
   * already written the equivalent date range and hour band; what the panel shows
   * is then the whole of what narrows the list.
   */
  function handleApplyFilters(newFilters: DetectionFilters) {
    const params = new URLSearchParams(window.location.search);
    applyDetectionFiltersToParams(params, newFilters);
    params.set('offset', '0');

    // queryType is inferred from the parameters server-side. A stale
    // 'hourly'/'species' type would keep applying a drill-down the user has just
    // replaced with an explicit filter set -- and 'hourly' is rejected outright
    // once the `hour` it requires has been folded into the panel's hour band.
    params.delete('queryType');

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

  // Handle browser back/forward buttons
  function handlePopState() {
    fetchDetections();
  }

  onMount(() => {
    fetchDetections();

    // Listen for browser navigation
    window.addEventListener('popstate', handlePopState);

    return () => {
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
