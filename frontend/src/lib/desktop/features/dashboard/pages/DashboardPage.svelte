<script lang="ts">
  import { onMount } from 'svelte';
  import DailySummaryCard from '$lib/desktop/features/dashboard/components/DailySummaryCard.svelte';
  import RecentDetectionsCard from '$lib/desktop/features/dashboard/components/RecentDetectionsCard.svelte';
  import type {
    DailySpeciesSummary,
    Detection,
    DetectionQueryParams,
  } from '$lib/types/detection.types';

  // State management
  let dailySummary = $state<DailySpeciesSummary[]>([]);
  let recentDetections = $state<Detection[]>([]);
  let selectedDate = $state(new Date().toISOString().split('T')[0]);
  let isLoadingSummary = $state(true);
  let isLoadingDetections = $state(true);
  let summaryError = $state<string | null>(null);
  let detectionsError = $state<string | null>(null);

  // Fetch functions
  async function fetchDailySummary() {
    isLoadingSummary = true;
    summaryError = null;

    try {
      const response = await fetch(`/api/v2/analytics/species/daily?date=${selectedDate}`);
      if (!response.ok) {
        throw new Error(`Failed to fetch daily summary: ${response.statusText}`);
      }
      dailySummary = await response.json();
    } catch (error) {
      summaryError = error instanceof Error ? error.message : 'Failed to load daily summary';
      console.error('Error fetching daily summary:', error);
    } finally {
      isLoadingSummary = false;
    }
  }

  async function fetchRecentDetections() {
    isLoadingDetections = true;
    detectionsError = null;

    try {
      const response = await fetch('/api/v2/detections/recent?limit=10');
      if (!response.ok) {
        throw new Error(`Failed to fetch recent detections: ${response.statusText}`);
      }
      recentDetections = await response.json();
    } catch (error) {
      detectionsError = error instanceof Error ? error.message : 'Failed to load recent detections';
      console.error('Error fetching recent detections:', error);
    } finally {
      isLoadingDetections = false;
    }
  }

  // Auto-refresh recent detections every 30 seconds
  let refreshInterval: ReturnType<typeof setInterval>;

  onMount(() => {
    fetchDailySummary();
    fetchRecentDetections();

    refreshInterval = setInterval(() => {
      fetchRecentDetections();
    }, 30000);

    return () => {
      if (refreshInterval) {
        clearInterval(refreshInterval);
      }
    };
  });

  // Date navigation
  function previousDay() {
    const date = new Date(selectedDate);
    date.setDate(date.getDate() - 1);
    selectedDate = date.toISOString().split('T')[0];
    fetchDailySummary();
  }

  function nextDay() {
    const date = new Date(selectedDate);
    date.setDate(date.getDate() + 1);
    const today = new Date().toISOString().split('T')[0];
    if (date.toISOString().split('T')[0] <= today) {
      selectedDate = date.toISOString().split('T')[0];
      fetchDailySummary();
    }
  }

  function goToToday() {
    selectedDate = new Date().toISOString().split('T')[0];
    fetchDailySummary();
  }

  function handleDateChange(date: string) {
    selectedDate = date;
    fetchDailySummary();
  }

  // Handle detection click
  function handleDetectionClick(detection: Detection) {
    // Navigate to detection details or open modal
    console.log('Detection clicked:', detection);
    // You can implement navigation to detection details here
    // window.location.href = `/detections/${detection.id}`;
  }

  // Handle species click from daily summary
  function handleSpeciesClick(species: DailySpeciesSummary) {
    // Navigate to species details page
    window.location.href = `/ui/species/${species.species_code}`;
  }

  // Handle detection view navigation from daily summary
  function handleDetectionView(params: DetectionQueryParams) {
    // Build query string from parameters
    const searchParams = new URLSearchParams();

    Object.entries(params).forEach(([key, value]) => {
      if (value !== undefined && value !== null && value !== '') {
        searchParams.append(key, String(value));
      }
    });

    // Navigate to detections page with query parameters
    const queryString = searchParams.toString();
    window.location.href = `/ui/detections${queryString ? `?${queryString}` : ''}`;
  }
</script>

<div class="col-span-12 space-y-6">
  <!-- Daily Summary Section -->
  <DailySummaryCard
    data={dailySummary}
    loading={isLoadingSummary}
    error={summaryError}
    {selectedDate}
    onRowClick={handleSpeciesClick}
    onDetectionView={handleDetectionView}
    onPreviousDay={previousDay}
    onNextDay={nextDay}
    onGoToToday={goToToday}
    onDateChange={handleDateChange}
  />

  <!-- Recent Detections Section -->
  <RecentDetectionsCard
    data={recentDetections}
    loading={isLoadingDetections}
    error={detectionsError}
    onRowClick={handleDetectionClick}
    onRefresh={fetchRecentDetections}
  />
</div>
