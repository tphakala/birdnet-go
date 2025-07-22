<script lang="ts">
  import { onMount } from 'svelte';
  import { fetchWithCSRF } from '$lib/utils/api';
  import type { DetectionsListData, DetectionQueryParams } from '$lib/types/detection.types';
  import DetectionsCard from './components/DetectionsCard.svelte';

  let detectionsData = $state<DetectionsListData | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

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

    return {
      queryType,
      date: params.get('date') || new Date().toISOString().split('T')[0],
      hour: params.get('hour') || undefined,
      duration: params.get('duration') ? parseInt(params.get('duration')!) : undefined,
      species: params.get('species') || undefined,
      search: search || undefined,
      numResults: parseInt(params.get('numResults') || '50'),
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

      const data = (await fetchWithCSRF(`/api/v2/detections?${queryString.toString()}`)) as any;

      // Transform API response to match our expected format
      detectionsData = {
        notes: data.data || [],
        queryType: queryParams.queryType || 'all',
        date: queryParams.date!,
        hour: queryParams.hour ? parseInt(queryParams.hour) : undefined,
        duration: queryParams.duration,
        species: queryParams.species,
        search: queryParams.search,
        numResults: queryParams.numResults!,
        offset: queryParams.offset!,
        totalResults: data.total || 0,
        itemsPerPage: data.limit || queryParams.numResults || 50,
        currentPage: data.current_page || 1,
        totalPages: data.total_pages || 1,
        showingFrom: (queryParams.offset || 0) + 1,
        showingTo: Math.min((queryParams.offset || 0) + (data.data?.length || 0), data.total || 0),
        dashboardSettings: data.dashboardSettings,
      };
    } catch (err) {
      error = err instanceof Error ? err.message : 'Failed to fetch detections';
      console.error('Error fetching detections:', err);
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

      // Navigate to new URL with updated offset
      window.location.href = `${window.location.pathname}?${params.toString()}`;
    }
  }

  // Handle details click
  function handleDetailsClick(id: number) {
    // Navigate to detection details page
    window.location.href = `/detections/${id}`;
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

  onMount(() => {
    fetchDetections();

    // Listen for search updates from SearchBox
    window.addEventListener('searchUpdate', handleSearchUpdate);

    return () => {
      window.removeEventListener('searchUpdate', handleSearchUpdate);
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
  />
</div>
