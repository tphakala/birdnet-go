<script lang="ts">
  import { onMount } from 'svelte';
  import ReconnectingEventSource from 'reconnecting-eventsource';
  import DailySummaryCard from '$lib/desktop/features/dashboard/components/DailySummaryCard.svelte';
  import RecentDetectionsCard from '$lib/desktop/features/dashboard/components/RecentDetectionsCard.svelte';
  import { t } from '$lib/i18n';
  import type { DailySpeciesSummary, Detection } from '$lib/types/detection.types';

  // Constants
  const ANIMATION_CLEANUP_DELAY = 2200; // Slightly longer than 2s animation duration
  const MIN_FETCH_LIMIT = 10; // Minimum number of detections to fetch for SSE processing

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

  // Menu state tracking to prevent SSE updates during menu interactions
  let openMenuCount = $state(0);
  let pendingDetectionQueue = $state<Detection[]>([]);

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
      summaryError =
        error instanceof Error ? error.message : t('dashboard.errors.dailySummaryLoad');
      console.error('Error fetching daily summary:', error);
    } finally {
      isLoadingSummary = false;
    }
  }

  async function fetchRecentDetections(applyAnimations = false) {
    isLoadingDetections = true;
    detectionsError = null;

    // Store current detection IDs to identify new ones after fetch
    const previousIds = new Set(recentDetections.map(d => d.id));

    try {
      const response = await fetch(`/api/v2/detections/recent?limit=${Math.max(detectionLimit, MIN_FETCH_LIMIT)}`);
      if (!response.ok) {
        throw new Error(
          t('dashboard.errors.recentDetectionsFetch', { status: response.statusText })
        );
      }
      const newData = await response.json();

      // Only apply animations for SSE-triggered updates
      if (applyAnimations) {
        // Identify new detections by comparing IDs
        const arrivalTime = Date.now();
        newData.forEach((detection: Detection) => {
          if (!previousIds.has(detection.id)) {
            // This is a new detection - add to animation state
            newDetectionIds.add(detection.id);
            detectionArrivalTimes.set(detection.id, arrivalTime);

            // Remove animation after it completes
            setTimeout(() => {
              newDetectionIds.delete(detection.id);
              detectionArrivalTimes.delete(detection.id);
            }, ANIMATION_CLEANUP_DELAY);
          }
        });
      }

      recentDetections = newData;
    } catch (error) {
      detectionsError =
        error instanceof Error ? error.message : t('dashboard.errors.recentDetectionsLoad');
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

    // Just fetch recent detections - don't touch daily summary
    fetchRecentDetections();
  }

  // Animation cleanup timers and RAF manager
  let animationCleanupTimers = $state(new Set<ReturnType<typeof setTimeout>>());
  let animationFrame: number | null = null;
  let pendingCleanups = new Map<string, { fn: () => void; timestamp: number }>();

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

  // Process pending cleanups using requestAnimationFrame
  function processCleanups(currentTime: number) {
    const toExecute: Array<() => void> = [];

    pendingCleanups.forEach((cleanup, key) => {
      if (currentTime >= cleanup.timestamp) {
        toExecute.push(cleanup.fn);
        pendingCleanups.delete(key);
      }
    });

    // Execute cleanups in batch
    toExecute.forEach(fn => fn());

    // Continue if there are more pending cleanups
    if (pendingCleanups.size > 0) {
      animationFrame = requestAnimationFrame(processCleanups);
    } else {
      animationFrame = null;
    }
  }

  // Centralized animation cleanup with RAF batching
  function scheduleAnimationCleanup(cleanupFn: () => void, delay: number, key?: string) {
    // Use species code as key if available, otherwise generate one
    const cleanupKey = key || `cleanup-${Date.now()}-${Math.random()}`;

    // Performance: Limit concurrent animations to prevent overwhelming the UI
    if (pendingCleanups.size > 50) {
      console.warn('Too many concurrent animations, clearing oldest to prevent performance issues');
      const oldestKey = pendingCleanups.keys().next().value;
      if (oldestKey) {
        pendingCleanups.delete(oldestKey);
      }
    }

    // Schedule cleanup
    pendingCleanups.set(cleanupKey, {
      fn: cleanupFn,
      timestamp: performance.now() + delay,
    });

    // Start RAF loop if not already running
    if (animationFrame === null) {
      animationFrame = requestAnimationFrame(processCleanups);
    }
  }

  // SSE connection for real-time detection updates
  let eventSource: ReconnectingEventSource | null = null;
  let connectionStatus = $state<'connecting' | 'connected' | 'error' | 'polling'>('connecting');

  // Process new detection from SSE - queue if menus are open, otherwise process immediately
  function handleNewDetection(detection: Detection) {
    // If any action menus are open, queue the detection for later processing
    if (openMenuCount > 0) {
      // Avoid duplicate detections in queue - add null-safety check
      const isDuplicate = pendingDetectionQueue.some(pending => 
        pending?.id != null && detection?.id != null && pending.id === detection.id
      );
      if (!isDuplicate) {
        pendingDetectionQueue.push(detection);
      }
      return;
    }

    // Process immediately if no menus are open
    processDetectionUpdate(detection);
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

      // Cancel pending RAF
      if (animationFrame !== null) {
        cancelAnimationFrame(animationFrame);
        animationFrame = null;
      }

      // Clear pending cleanups
      pendingCleanups.clear();
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
      scheduleAnimationCleanup(
        () => {
          const currentIndex = dailySummary.findIndex(
            s => s.species_code === detection.speciesCode
          );
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
        },
        1000,
        `count-${detection.speciesCode}`
      );
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
      scheduleAnimationCleanup(
        () => {
          const currentIndex = dailySummary.findIndex(
            s => s.species_code === detection.speciesCode
          );
          if (currentIndex >= 0) {
            const cleared = { ...dailySummary[currentIndex] };
            cleared.isNew = false;

            dailySummary = [
              ...dailySummary.slice(0, currentIndex),
              cleared,
              ...dailySummary.slice(currentIndex + 1),
            ];
          }
        },
        800,
        `new-${detection.speciesCode}`
      );
    }
  }

  // Menu state management
  function handleMenuOpen() {
    openMenuCount++;
  }

  function handleMenuClose() {
    openMenuCount--;
    // Clamp to prevent negative values due to unmount edge cases
    openMenuCount = Math.max(0, openMenuCount);
    
    // Process pending detections when all menus are closed
    if (openMenuCount === 0 && pendingDetectionQueue.length > 0) {
      // Process all pending detections
      pendingDetectionQueue.forEach(detection => {
        processDetectionUpdate(detection);
      });
      
      // Clear the queue
      pendingDetectionQueue = [];
    }
  }

  // Helper function to process a detection update (extracted from handleNewDetection)
  function processDetectionUpdate(detection: Detection) {
    // Trigger API fetch to get fresh data with animations enabled
    fetchRecentDetections(true);
    
    // Queue daily summary update with debouncing
    queueDailySummaryUpdate(detection);
  }

  // Handle detection click
  function handleDetectionClick(detection: Detection) {
    // Navigate to detection details or open modal
    console.log('Detection clicked:', detection);
    // You can implement navigation to detection details here
    // window.location.href = `/detections/${detection.id}`;
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
    onMenuOpen={handleMenuOpen}
    onMenuClose={handleMenuClose}
    hasOpenMenus={openMenuCount > 0}
  />
</div>
