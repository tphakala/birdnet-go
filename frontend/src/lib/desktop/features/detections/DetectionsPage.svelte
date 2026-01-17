<script lang="ts">
  import { onMount } from 'svelte';
  import { fetchWithCSRF } from '$lib/utils/api';
  import type { DetectionsListData, DetectionQueryParams } from '$lib/types/detection.types';
  import DetectionsCard from './components/DetectionsCard.svelte';
  import { getLogger } from '$lib/utils/logger';
  import { getLocalDateString } from '$lib/utils/date';
  import { buildAppUrl } from '$lib/utils/urlHelpers';

  const logger = getLogger('app');

  let detectionsData = $state<DetectionsListData | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let debounceTimer: ReturnType<typeof setTimeout> | null = null;

  // Local storage key for user preference
  const RESULTS_PER_PAGE_KEY = 'birdnet-detections-results-per-page';

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
    const search = params.get('search');

    // Set queryType to 'search' if search parameter is present
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

    return {
      queryType,
      date: params.get('date') || getLocalDateString(),
      hour: params.get('hour') || undefined,
      duration: params.get('duration') ? parseInt(params.get('duration')!) : undefined,
      species: params.get('species') || undefined,
      search: search || undefined,
      numResults,
      offset: parseInt(params.get('offset') || '0'),
    };
  }

  // Fetch detections data
  async function fetchDetections() {
    loading = true;
    error = null;

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

      const data = (await fetchWithCSRF(`/api/v2/detections?${queryString.toString()}`)) as any;

      // Validate numResults before using
      const validatedNumResults =
        queryParams.numResults !== undefined && [10, 25, 50, 100].includes(queryParams.numResults)
          ? queryParams.numResults
          : getSavedResultsPerPage();

      // Transform API response to match our expected format
      detectionsData = {
        notes: data.data || [],
        queryType: queryParams.queryType || 'all',
        date: queryParams.date!,
        hour: queryParams.hour ? parseInt(queryParams.hour) : undefined,
        duration: queryParams.duration,
        species: queryParams.species,
        search: queryParams.search,
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
      error = err instanceof Error ? err.message : 'Failed to fetch detections';
      logger.error('Error fetching detections:', err);
    } finally {
      loading = false;
    }
  }

  // Handle page change
  function handlePageChange(newPage: number) {
    if (detectionsData) {
      const newOffset = (newPage - 1) * detectionsData.itemsPerPage;
      const params = new URLSearchParams(window.location.search);
      params.set('offset', String(newOffset));

      // Update URL without navigation
      window.history.pushState({}, '', `${window.location.pathname}?${params.toString()}`);

      // Fetch new data
      fetchDetections();
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

      // Update URL without navigation
      window.history.pushState({}, '', `${window.location.pathname}?${params.toString()}`);

      // Fetch new data
      fetchDetections();
    }, 300); // 300ms debounce delay
  }

  // Handle details click
  function handleDetailsClick(id: number) {
    // Navigate to detection details page
    window.location.href = buildAppUrl(`/ui/detections/${id}`);
  }

  // Listen for search updates from SearchBox
  function handleSearchUpdate(event: Event) {
    const customEvent = event as CustomEvent<{ search: string }>;
    const { search } = customEvent.detail;
    // Update URL parameters to include new search
    const params = new URLSearchParams(window.location.search);
    if (search) {
      params.set('search', search);
    } else {
      params.delete('search');
    }

    // Update URL without navigation
    const url = new URL(window.location.href);
    url.search = params.toString();
    window.history.replaceState({}, '', url.toString());

    // Refresh detections with new search
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
  <DetectionsCard
    data={detectionsData}
    {loading}
    {error}
    onPageChange={handlePageChange}
    onDetailsClick={handleDetailsClick}
    onRefresh={fetchDetections}
    onNumResultsChange={handleNumResultsChange}
  />
</div>
