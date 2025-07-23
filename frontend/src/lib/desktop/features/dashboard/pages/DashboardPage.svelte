<script lang="ts">
  import { onMount } from 'svelte';
  import ReconnectingEventSource from 'reconnecting-eventsource';
  import DailySummaryCard from '$lib/desktop/features/dashboard/components/DailySummaryCard.svelte';
  import RecentDetectionsCard from '$lib/desktop/features/dashboard/components/RecentDetectionsCard.svelte';
  import { t } from '$lib/i18n';
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
  let showThumbnails = $state(true); // Default to true for backward compatibility

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

  // Debouncing for rapid daily summary updates
  let updateQueue = $state(new Map<string, Detection>());
  let updateTimer: ReturnType<typeof setTimeout> | null = null;

  // Fetch functions
  async function fetchDailySummary() {
    isLoadingSummary = true;
    summaryError = null;

    try {
      const response = await fetch(`/api/v2/analytics/species/daily?date=${selectedDate}`);
      if (!response.ok) {
        throw new Error(t('dashboard.errors.dailySummaryFetch', { status: response.statusText }));
      }
      dailySummary = await response.json();
    } catch (error) {
      summaryError = error instanceof Error ? error.message : t('dashboard.errors.dailySummaryLoad');
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
        throw new Error(t('dashboard.errors.recentDetectionsFetch', { status: response.statusText }));
      }
      recentDetections = await response.json();
    } catch (error) {
      detectionsError = error instanceof Error ? error.message : t('dashboard.errors.recentDetectionsLoad');
      console.error('Error fetching recent detections:', error);
    } finally {
      isLoadingDetections = false;
    }
  }

  async function fetchDashboardConfig() {
    try {
      const response = await fetch('/api/v2/settings/dashboard');
      if (!response.ok) {
        throw new Error(t('dashboard.errors.configFetch', { status: response.statusText }));
      }
      const config = await response.json();
      // API returns uppercase field names (e.g., "Summary" not "summary")
      showThumbnails = config.Thumbnails?.Summary ?? true;
      console.log('Dashboard config loaded:', {
        Summary: config.Thumbnails?.Summary,
        showThumbnails,
      });
    } catch (error) {
      console.error('Error fetching dashboard config:', error);
      // Keep default value (true) on error
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

  // Animation cleanup timers
  let animationCleanupTimers = $state(new Set<ReturnType<typeof setTimeout>>());

  // Clear animation states from daily summary
  function clearDailySummaryAnimations() {
    dailySummary = dailySummary.map(species => ({
      ...species,
      isNew: false,
      countIncreased: false,
      hourlyUpdated: [],
    }));

    // Clear any pending animation cleanup timers
    animationCleanupTimers.forEach(timer => clearTimeout(timer));
    animationCleanupTimers.clear();
  }

  // Centralized animation cleanup with timer tracking and limit
  function scheduleAnimationCleanup(cleanupFn: () => void, delay: number) {
    // Performance: Limit concurrent animations to prevent overwhelming the UI
    if (animationCleanupTimers.size > 50) {
      console.warn('Too many concurrent animations, clearing oldest to prevent performance issues');
      const oldestTimer = animationCleanupTimers.values().next().value;
      if (oldestTimer) {
        clearTimeout(oldestTimer);
        animationCleanupTimers.delete(oldestTimer);
      }
    }

    const timer = setTimeout(() => {
      cleanupFn();
      animationCleanupTimers.delete(timer);
    }, delay);

    animationCleanupTimers.add(timer);
    return timer;
  }

  // SSE connection for real-time detection updates
  let eventSource: ReconnectingEventSource | null = null;
  let connectionStatus = $state<'connecting' | 'connected' | 'error' | 'polling'>('connecting');

  // Process new detection from SSE
  function handleNewDetection(detection: Detection) {
    // Mark detection as new for animation and track arrival time
    const arrivalTime = Date.now();
    newDetectionIds.add(detection.id);
    detectionArrivalTimes.set(detection.id, arrivalTime);

    // Add new detection to beginning of list and limit to user's selected limit
    recentDetections = [detection, ...recentDetections].slice(0, detectionLimit);

    // Queue daily summary update with debouncing
    queueDailySummaryUpdate(detection);

    // Remove animation class after animation completes (600ms animation + 400ms buffer)
    setTimeout(() => {
      newDetectionIds.delete(detection.id);
      detectionArrivalTimes.delete(detection.id);
    }, 1000);
  }

  // Connect to SSE stream for real-time updates using ReconnectingEventSource
  function connectToDetectionStream() {
    console.log('Connecting to SSE stream at /api/v2/detections/stream');
    connectionStatus = 'connecting';

    // Clean up existing connection
    if (eventSource) {
      eventSource.close();
      eventSource = null;
    }

    try {
      // ReconnectingEventSource with configuration
      eventSource = new ReconnectingEventSource('/api/v2/detections/stream', {
        max_retry_time: 30000, // Max 30 seconds between reconnection attempts
        withCredentials: false,
      });

      eventSource.onopen = () => {
        console.log('SSE connection opened');
        connectionStatus = 'connected';
      };

      eventSource.onmessage = event => {
        try {
          const data = JSON.parse(event.data);

          // Check if this is a structured message with eventType
          if (data.eventType) {
            switch (data.eventType) {
              case 'connected':
                console.log('Connected to detection stream:', data);
                connectionStatus = 'connected';
                break;

              case 'detection':
                handleSSEDetection(data);
                break;

              case 'heartbeat':
                console.debug('SSE heartbeat received, clients:', data.clients);
                connectionStatus = 'connected';
                break;

              default:
                console.log('Unknown event type:', data.eventType);
            }
          } else if (data.ID && data.CommonName) {
            // This looks like a direct detection event
            connectionStatus = 'connected';
            handleSSEDetection(data);
          }
        } catch (error) {
          console.error('Failed to parse SSE message:', error);
        }
      };

      // Handle specific event types
      // Handle specific event types
      eventSource.addEventListener('connected', (event: Event) => {
        try {
          // eslint-disable-next-line no-undef
          const messageEvent = event as MessageEvent;
          const data = JSON.parse(messageEvent.data);
          console.log('Connected event received:', data);
          connectionStatus = 'connected';
        } catch (error) {
          console.error('Failed to parse connected event:', error);
        }
      });

      eventSource.addEventListener('detection', (event: Event) => {
        try {
          // eslint-disable-next-line no-undef
          const messageEvent = event as MessageEvent;
          const data = JSON.parse(messageEvent.data);
          connectionStatus = 'connected';
          handleSSEDetection(data);
        } catch (error) {
          console.error('Failed to parse detection event:', error);
        }
      });

      eventSource.addEventListener('heartbeat', (event: Event) => {
        try {
          // eslint-disable-next-line no-undef
          const messageEvent = event as MessageEvent;
          const data = JSON.parse(messageEvent.data);
          console.debug('Heartbeat event received, clients:', data.clients);
          connectionStatus = 'connected';
        } catch (error) {
          console.error('Failed to parse heartbeat event:', error);
        }
      });

      eventSource.onerror = (error: Event) => {
        console.error('SSE connection error:', error);
        connectionStatus = 'error';
        // ReconnectingEventSource handles reconnection automatically
        // No need for manual reconnection logic
      };
    } catch (error) {
      console.error('Failed to create ReconnectingEventSource:', error);
      connectionStatus = 'error';
      // Try again in 5 seconds if initial setup fails
      setTimeout(() => connectToDetectionStream(), 5000);
    }
  }

  // Helper function to process SSE detection data
  function handleSSEDetection(detectionData: any) {
    try {
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
        endTime: (detectionData.EndTime || '') as string,
      };

      handleNewDetection(detection);
    } catch (error) {
      console.error('Error processing detection data:', error);
    }
  }

  onMount(() => {
    fetchDailySummary();
    fetchRecentDetections();
    fetchDashboardConfig();

    // Setup SSE connection for real-time updates
    connectToDetectionStream();

    return () => {
      // Clean up SSE connection
      if (eventSource) {
        eventSource.close();
        eventSource = null;
      }

      // Clean up debounce timer
      if (updateTimer) {
        clearTimeout(updateTimer);
      }

      // Clean up animation timers
      animationCleanupTimers.forEach(timer => clearTimeout(timer));
      animationCleanupTimers.clear();
    };
  });

  // Date navigation
  // Enhanced date change handler with cleanup
  function handleDateChangeWithCleanup() {
    // Clear pending updates for old date
    if (updateTimer) {
      clearTimeout(updateTimer);
      updateTimer = null;
    }
    updateQueue.clear();

    // Clear animations
    clearDailySummaryAnimations();
  }

  function previousDay() {
    const date = new Date(selectedDate);
    date.setDate(date.getDate() - 1);
    selectedDate = date.toISOString().split('T')[0];
    handleDateChangeWithCleanup();
    fetchDailySummary();
  }

  function nextDay() {
    const date = new Date(selectedDate);
    date.setDate(date.getDate() + 1);
    const today = new Date().toISOString().split('T')[0];
    if (date.toISOString().split('T')[0] <= today) {
      selectedDate = date.toISOString().split('T')[0];
      handleDateChangeWithCleanup();
      fetchDailySummary();
    }
  }

  function goToToday() {
    selectedDate = new Date().toISOString().split('T')[0];
    handleDateChangeWithCleanup();
    fetchDailySummary();
  }

  function handleDateChange(date: string) {
    selectedDate = date;
    handleDateChangeWithCleanup();
    fetchDailySummary();
  }

  // Handle detection limit change from RecentDetectionsCard
  function handleDetectionLimitChange(newLimit: number) {
    detectionLimit = newLimit;
    // Trim existing detections to new limit
    recentDetections = recentDetections.slice(0, newLimit);
  }

  // Queue daily summary updates with debouncing for rapid updates
  function queueDailySummaryUpdate(detection: Detection) {
    // Only update if detection is for the currently selected date
    if (detection.date !== selectedDate) {
      return;
    }

    // Performance: Skip if too many pending updates to prevent UI freeze
    if (updateQueue.size > 20) {
      console.warn(
        'Too many pending daily summary updates, skipping to prevent performance issues'
      );
      return;
    }

    // Add to queue (overwrites previous detection for same species)
    updateQueue.set(detection.speciesCode, detection);

    // Clear existing timer and set new one
    if (updateTimer) {
      clearTimeout(updateTimer);
    }

    updateTimer = setTimeout(() => {
      // Process all queued updates in order of species code for consistency
      const sortedUpdates = Array.from(updateQueue.entries()).sort(([a], [b]) =>
        a.localeCompare(b)
      );

      sortedUpdates.forEach(([_, queuedDetection]) => {
        updateDailySummary(queuedDetection);
      });

      updateQueue.clear();
      updateTimer = null;
    }, 150); // Batch updates within 150ms window
  }

  // Update daily summary when new detection arrives via SSE
  function updateDailySummary(detection: Detection) {
    // Only update if detection is for the currently selected date
    if (detection.date !== selectedDate) {
      return;
    }

    const hour = new Date(`${detection.date} ${detection.time}`).getHours();
    const existingIndex = dailySummary.findIndex(s => s.species_code === detection.speciesCode);

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

      // Remove the species from its current position
      const withoutUpdated = [
        ...dailySummary.slice(0, existingIndex),
        ...dailySummary.slice(existingIndex + 1),
      ];

      // Find the correct position for the updated species based on its new count
      const newPosition = withoutUpdated.findIndex(s => s.count < updated.count);
      if (newPosition === -1) {
        // Add to end if it has the lowest count
        dailySummary = [...withoutUpdated, updated];
      } else {
        // Insert at the correct position
        dailySummary = [
          ...withoutUpdated.slice(0, newPosition),
          updated,
          ...withoutUpdated.slice(newPosition),
        ];
      }

      console.log(
        `Updated existing species: ${detection.commonName} (count: ${updated.count}, hour: ${hour})`
      );

      // Clear animation flags after animation completes
      scheduleAnimationCleanup(() => {
        const currentIndex = dailySummary.findIndex(s => s.species_code === detection.speciesCode);
        if (currentIndex >= 0) {
          const cleared = { ...dailySummary[currentIndex] };
          cleared.countIncreased = false;
          cleared.hourlyUpdated = [];

          dailySummary = [
            ...dailySummary.slice(0, currentIndex),
            cleared,
            ...dailySummary.slice(currentIndex + 1),
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
        isNew: true,
      };
      newSpecies.hourly_counts[hour] = 1;

      // Insert new species in correct position based on count (sorted descending)
      const insertPosition = dailySummary.findIndex(s => s.count < newSpecies.count);
      if (insertPosition === -1) {
        // Add to end if it has the lowest count
        dailySummary = [...dailySummary, newSpecies];
      } else {
        // Insert at the correct position
        dailySummary = [
          ...dailySummary.slice(0, insertPosition),
          newSpecies,
          ...dailySummary.slice(insertPosition),
        ];
      }

      console.log(
        `Added new species: ${detection.commonName} (count: 1, hour: ${hour}) at position ${insertPosition === -1 ? dailySummary.length - 1 : insertPosition}`
      );

      // Clear animation flag after animation completes
      scheduleAnimationCleanup(() => {
        const currentIndex = dailySummary.findIndex(s => s.species_code === detection.speciesCode);
        if (currentIndex >= 0) {
          const cleared = { ...dailySummary[currentIndex] };
          cleared.isNew = false;

          dailySummary = [
            ...dailySummary.slice(0, currentIndex),
            cleared,
            ...dailySummary.slice(currentIndex + 1),
          ];
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
    {showThumbnails}
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
    {newDetectionIds}
    {detectionArrivalTimes}
  />
</div>
