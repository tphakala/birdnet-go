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
  
  // Function to get initial detection limit from localStorage
  function getInitialDetectionLimit(): number {
    if (typeof window !== 'undefined') {
      const savedLimit = localStorage.getItem('recentDetectionLimit');
      if (savedLimit) {
        const parsed = parseInt(savedLimit, 10);
        if (!isNaN(parsed) && [5, 10, 25, 50].includes(parsed)) {
          return parsed;
        }
      }
    }
    return 5; // Default value
  }

  // Detection limit state to sync with RecentDetectionsCard
  let detectionLimit = $state(getInitialDetectionLimit());
  
  // Animation state for new detections
  let newDetectionIds = $state(new Set<number>());
  let detectionArrivalTimes = $state(new Map<number, number>());

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

  // Manual refresh function that works with both SSE and polling
  function handleManualRefresh() {
    // Clear animation state on manual refresh
    newDetectionIds.clear();
    detectionArrivalTimes.clear();
    clearDailySummaryAnimations();
    
    // For SSE mode, just fetch fresh data without affecting the connection
    if (connectionStatus === 'connected') {
      fetchRecentDetections();
    } else {
      // For polling mode or when SSE is not working, use regular fetch
      fetchRecentDetections();
    }
  }

  // Clear animation states from daily summary
  function clearDailySummaryAnimations() {
    dailySummary = dailySummary.map(species => ({
      ...species,
      isNew: false,
      countIncreased: false,
      hourlyUpdated: []
    }));
  }

  // SSE connection for real-time detection updates
  let eventSource: EventSource | null = null;
  let refreshInterval: ReturnType<typeof setInterval>;
  let connectionStatus = $state<'connecting' | 'connected' | 'error' | 'polling'>('connecting');
  let sseRetryCount = $state(0);
  const maxSSERetries = 3;

  // Connect to SSE stream for real-time updates
  function connectToDetectionStream() {
    if (eventSource) {
      eventSource.close();
    }

    connectionStatus = 'connecting';
    eventSource = new EventSource('/api/v2/detections/stream');

    eventSource.addEventListener('connected', (event) => {
      connectionStatus = 'connected';
      sseRetryCount = 0;
      console.log('Connected to detection stream:', JSON.parse(event.data));
    });

    eventSource.addEventListener('detection', (event) => {
      try {
        const detectionData = JSON.parse(event.data);
        // Convert SSEDetectionData to Detection format
        const detection: Detection = {
          id: detectionData.ID as number,
          commonName: detectionData.CommonName as string,
          scientificName: detectionData.ScientificName as string,
          confidence: detectionData.Confidence as number,
          date: detectionData.Date as string,
          time: detectionData.Time as string,
          speciesCode: detectionData.SpeciesCode as string,
          verified: (detectionData.Verified || 'unverified') as Detection['verified'],
          locked: (detectionData.Locked || false) as boolean,
          source: (detectionData.Source || '') as string,
          beginTime: (detectionData.BeginTime || '') as string,
          endTime: (detectionData.EndTime || '') as string
        };

        // Mark detection as new for animation and track arrival time
        const arrivalTime = Date.now();
        newDetectionIds.add(detection.id);
        detectionArrivalTimes.set(detection.id, arrivalTime);
        
        // Add new detection to beginning of list and limit to user's selected limit
        recentDetections = [detection, ...recentDetections].slice(0, detectionLimit);
        
        // Update daily summary with new detection
        updateDailySummary(detection);
        
        // Remove animation class after animation completes (600ms animation + 400ms buffer)
        setTimeout(() => {
          newDetectionIds.delete(detection.id);
          detectionArrivalTimes.delete(detection.id);
        }, 1000);
      } catch (error) {
        console.error('Error parsing SSE detection data:', error);
      }
    });

    eventSource.addEventListener('heartbeat', (event) => {
      // SSE connection is alive, no action needed
      const heartbeatData = JSON.parse(event.data);
      console.debug('SSE heartbeat received, clients:', heartbeatData.clients);
    });

    eventSource.onerror = (error) => {
      console.error('SSE connection error:', error);
      connectionStatus = 'error';
      
      // Retry logic with exponential backoff
      if (sseRetryCount < maxSSERetries) {
        sseRetryCount++;
        const retryDelay = Math.min(1000 * Math.pow(2, sseRetryCount), 30000);
        console.log(`Retrying SSE connection in ${retryDelay}ms (attempt ${sseRetryCount}/${maxSSERetries})`);
        
        setTimeout(() => {
          if (eventSource?.readyState === EventSource.CLOSED) {
            connectToDetectionStream();
          }
        }, retryDelay);
      } else {
        console.log('Max SSE retries reached, falling back to polling');
        fallbackToPolling();
      }
    };
  }

  // Fallback to polling when SSE fails
  function fallbackToPolling() {
    connectionStatus = 'polling';
    if (eventSource) {
      eventSource.close();
      eventSource = null;
    }

    // Start polling every 30 seconds
    refreshInterval = setInterval(() => {
      fetchRecentDetections();
    }, 30000);
  }

  onMount(() => {
    fetchDailySummary();
    fetchRecentDetections();

    // Try SSE first, fallback to polling if not supported
    if (typeof EventSource !== 'undefined') {
      connectToDetectionStream();
    } else {
      console.log('EventSource not supported, using polling');
      fallbackToPolling();
    }

    return () => {
      if (eventSource) {
        eventSource.close();
      }
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
    clearDailySummaryAnimations();
    fetchDailySummary();
  }

  function nextDay() {
    const date = new Date(selectedDate);
    date.setDate(date.getDate() + 1);
    const today = new Date().toISOString().split('T')[0];
    if (date.toISOString().split('T')[0] <= today) {
      selectedDate = date.toISOString().split('T')[0];
      clearDailySummaryAnimations();
      fetchDailySummary();
    }
  }

  function goToToday() {
    selectedDate = new Date().toISOString().split('T')[0];
    clearDailySummaryAnimations();
    fetchDailySummary();
  }

  function handleDateChange(date: string) {
    selectedDate = date;
    clearDailySummaryAnimations();
    fetchDailySummary();
  }

  // Handle detection limit change from RecentDetectionsCard
  function handleDetectionLimitChange(newLimit: number) {
    detectionLimit = newLimit;
    // Trim existing detections to new limit
    recentDetections = recentDetections.slice(0, newLimit);
  }

  // Update daily summary when new detection arrives via SSE
  function updateDailySummary(detection: Detection) {
    // Only update if detection is for the currently selected date
    if (detection.date !== selectedDate) {
      return;
    }

    const hour = new Date(`${detection.date} ${detection.time}`).getHours();
    const existingIndex = dailySummary.findIndex(
      s => s.species_code === detection.speciesCode
    );
    
    if (existingIndex >= 0) {
      // Update existing species
      const updated = { ...dailySummary[existingIndex] };
      updated.previousCount = updated.count;
      updated.count++;
      updated.countIncreased = true;
      updated.hourly_counts = [...updated.hourly_counts];
      updated.hourly_counts[hour]++;
      updated.hourlyUpdated = [hour];
      updated.latest_heard = detection.time;
      
      // Update the array immutably
      dailySummary = [
        ...dailySummary.slice(0, existingIndex),
        updated,
        ...dailySummary.slice(existingIndex + 1)
      ];
      
      // Clear animation flags after animation completes
      setTimeout(() => {
        const currentIndex = dailySummary.findIndex(s => s.species_code === detection.speciesCode);
        if (currentIndex >= 0) {
          const cleared = { ...dailySummary[currentIndex] };
          cleared.countIncreased = false;
          cleared.hourlyUpdated = [];
          
          dailySummary = [
            ...dailySummary.slice(0, currentIndex),
            cleared,
            ...dailySummary.slice(currentIndex + 1)
          ];
        }
      }, 1000);
      
    } else {
      // Add new species
      const newSpecies: DailySpeciesSummary = {
        scientific_name: detection.scientificName,
        common_name: detection.commonName,
        species_code: detection.speciesCode,
        count: 1,
        hourly_counts: Array(24).fill(0),
        high_confidence: detection.confidence >= 0.8,
        first_heard: detection.time,
        latest_heard: detection.time,
        thumbnail_url: `/api/v2/species/${detection.speciesCode}/thumbnail`,
        isNew: true
      };
      newSpecies.hourly_counts[hour] = 1;
      
      // Add to beginning of array
      dailySummary = [newSpecies, ...dailySummary];
      
      // Clear animation flag after animation completes
      setTimeout(() => {
        if (dailySummary.length > 0 && dailySummary[0].species_code === detection.speciesCode) {
          const cleared = { ...dailySummary[0] };
          cleared.isNew = false;
          
          dailySummary = [cleared, ...dailySummary.slice(1)];
        }
      }, 800);
    }
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
    limit={detectionLimit}
    onLimitChange={handleDetectionLimitChange}
    onRowClick={handleDetectionClick}
    onRefresh={handleManualRefresh}
    {connectionStatus}
    {newDetectionIds}
    {detectionArrivalTimes}
  />
</div>
