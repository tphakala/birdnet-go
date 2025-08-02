<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import ReconnectingEventSource from 'reconnecting-eventsource';
  import DailySummaryCard from '$lib/desktop/features/dashboard/components/DailySummaryCard.svelte';
  import RecentDetectionsCard from '$lib/desktop/features/dashboard/components/RecentDetectionsCard.svelte';
  import { t } from '$lib/i18n';
  import type { DailySpeciesSummary, Detection } from '$lib/types/detection.types';
  import { getLocalDateString, isFutureDate, parseHour } from '$lib/utils/date';
  import { getLogger } from '$lib/utils/logger';
  import { safeArrayAccess } from '$lib/utils/security';

  const logger = getLogger('app');

  // Constants
  const ANIMATION_CLEANUP_DELAY = 2200; // Slightly longer than 2s animation duration
  const MIN_FETCH_LIMIT = 10; // Minimum number of detections to fetch for SSE processing

  // State management
  let dailySummary = $state<DailySpeciesSummary[]>([]);
  let recentDetections = $state<Detection[]>([]);
  let selectedDate = $state(getLocalDateString());
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

  // Update freeze tracking to prevent SSE updates during user interactions (menus, audio playback, etc.)
  let freezeCount = $state(0);
  let pendingDetectionQueue = $state<Detection[]>([]);

  // Debouncing for rapid daily summary updates
  let updateQueue = $state(new Map<string, Detection>());
  let updateTimer: ReturnType<typeof setTimeout> | null = null;

  // Daily summary response caching for performance optimization
  interface CachedDailySummary {
    data: DailySpeciesSummary[];
    timestamp: number;
  }

  // Use $state.raw() for non-mutated cache objects to avoid proxy overhead
  const dailySummaryCache = $state.raw(new Map<string, CachedDailySummary>());
  const CACHE_TTL = 60000; // 1 minute TTL for daily summary cache
  const CACHE_MAX_ENTRIES = 30; // ~1 month of data to prevent memory issues

  // Selective cache update functions for incremental SSE updates
  function updateDailySummaryCacheEntry(date: string, updatedSummary: DailySpeciesSummary[]) {
    const cached = dailySummaryCache.get(date);
    if (cached && Date.now() - cached.timestamp < CACHE_TTL) {
      // Update cache with new data, preserve timestamp to maintain TTL
      cached.data = updatedSummary;
      logger.debug(`Daily summary cache updated incrementally for ${date}`);
    }
  }

  // Fetch functions
  async function fetchDailySummary() {
    isLoadingSummary = true;
    summaryError = null;

    try {
      // Check cache first - if valid entry exists within TTL, return it
      const cached = dailySummaryCache.get(selectedDate);
      if (cached && Date.now() - cached.timestamp < CACHE_TTL) {
        // Cache hit - use cached data directly
        dailySummary = cached.data;
        isLoadingSummary = false;
        logger.debug(`Daily summary cache hit for ${selectedDate}`);
        return;
      }

      // Cache miss or expired - fetch from API
      logger.debug(`Daily summary cache miss for ${selectedDate}, fetching from API`);
      const response = await fetch(`/api/v2/analytics/species/daily?date=${selectedDate}`);
      if (!response.ok) {
        throw new Error(t('dashboard.errors.dailySummaryFetch', { status: response.statusText }));
      }
      const data = await response.json();

      // Update UI
      dailySummary = data;

      // Cache the result for future requests
      dailySummaryCache.set(selectedDate, {
        data: data,
        timestamp: Date.now(),
      });

      // Cleanup old cache entries to prevent memory leaks
      cleanupDailySummaryCache();

      // Enforce maximum cache size limit
      if (dailySummaryCache.size > CACHE_MAX_ENTRIES) {
        enforceMaxCacheSize();
      }
    } catch (error) {
      summaryError =
        error instanceof Error ? error.message : t('dashboard.errors.dailySummaryLoad');
      logger.error('Error fetching daily summary:', error);
    } finally {
      isLoadingSummary = false;
    }
  }

  // Cleanup expired cache entries to prevent unbounded memory growth
  function cleanupDailySummaryCache() {
    const now = Date.now();
    for (const [date, cached] of dailySummaryCache.entries()) {
      if (now - cached.timestamp >= CACHE_TTL) {
        dailySummaryCache.delete(date);
      }
    }
  }

  // Enforce maximum cache size by evicting oldest entries
  function enforceMaxCacheSize() {
    if (dailySummaryCache.size <= CACHE_MAX_ENTRIES) return;

    // Convert to array for sorting by timestamp
    const entries = Array.from(dailySummaryCache.entries());
    entries.sort((a, b) => {
      const timestampA = safeArrayAccess(a, 1)?.timestamp ?? 0;
      const timestampB = safeArrayAccess(b, 1)?.timestamp ?? 0;
      return timestampA - timestampB;
    });

    // Remove oldest entries until within limit
    const entriesToRemove = dailySummaryCache.size - CACHE_MAX_ENTRIES;
    for (let i = 0; i < entriesToRemove; i++) {
      const entry = safeArrayAccess(entries, i);
      if (entry) {
        const key = safeArrayAccess(entry, 0);
        if (key !== undefined) {
          dailySummaryCache.delete(key);
        }
      }
    }

    logger.debug(`Cache size enforced: removed ${entriesToRemove} oldest entries`);
  }

  async function fetchRecentDetections(applyAnimations = false) {
    isLoadingDetections = true;
    detectionsError = null;

    // Store current detection IDs to identify new ones after fetch
    const previousIds = new Set(recentDetections.map(d => d.id));

    try {
      const response = await fetch(
        `/api/v2/detections/recent?limit=${Math.max(detectionLimit, MIN_FETCH_LIMIT)}&includeWeather=true`
      );
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
      logger.error('Error fetching recent detections:', error);
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
      logger.debug('Dashboard config loaded:', {
        Summary: config.Thumbnails?.Summary,
        showThumbnails,
      });
    } catch (error) {
      logger.error('Error fetching dashboard config:', error);
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

  // Animation cleanup timers and RAF manager - use $state.raw() for performance
  let animationCleanupTimers = $state.raw(new Set<ReturnType<typeof setTimeout>>());
  let animationFrame: number | null = null;
  let pendingCleanups = $state.raw(new Map<string, { fn: () => void; timestamp: number }>());

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
      animationFrame = window.requestAnimationFrame(processCleanups);
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
      logger.warn('Too many concurrent animations, clearing oldest to prevent performance issues');
      const oldestKey = pendingCleanups.keys().next().value;
      if (oldestKey) {
        pendingCleanups.delete(oldestKey);
      }
    }

    // Schedule cleanup
    pendingCleanups.set(cleanupKey, {
      fn: cleanupFn,
      timestamp: window.performance.now() + delay,
    });

    // Start RAF loop if not already running
    if (animationFrame === null) {
      animationFrame = window.requestAnimationFrame(processCleanups);
    }
  }

  // SSE connection for real-time detection updates
  let eventSource: ReconnectingEventSource | null = null;

  // Process new detection from SSE - queue if updates are frozen, otherwise process immediately
  function handleNewDetection(detection: Detection) {
    // If any interactions are active (menus, audio playback), queue the detection for later processing
    if (freezeCount > 0) {
      // Avoid duplicate detections in queue - add null-safety check
      const isDuplicate = pendingDetectionQueue.some(
        pending => pending?.id != null && detection?.id != null && pending.id === detection.id
      );
      if (!isDuplicate) {
        pendingDetectionQueue.push(detection);
      }
      return;
    }

    // Process immediately if no interactions are active
    processDetectionUpdate(detection);
  }

  // Connect to SSE stream for real-time updates using ReconnectingEventSource
  function connectToDetectionStream() {
    logger.debug('Connecting to SSE stream at /api/v2/detections/stream');

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
        logger.debug('SSE connection opened');
      };

      eventSource.onmessage = event => {
        try {
          const data = JSON.parse(event.data);

          // Check if this is a structured message with eventType
          if (data.eventType) {
            switch (data.eventType) {
              case 'connected':
                logger.debug('Connected to detection stream:', data);
                break;

              case 'detection':
                handleSSEDetection(data);
                break;

              case 'heartbeat':
                logger.debug('SSE heartbeat received, clients:', data.clients);
                break;

              default:
                logger.debug('Unknown event type:', data.eventType);
            }
          } else if (data.ID && data.CommonName) {
            // This looks like a direct detection event
            handleSSEDetection(data);
          }
        } catch (error) {
          logger.error('Failed to parse SSE message:', error);
        }
      };

      // Handle specific event types
      // Handle specific event types
      eventSource.addEventListener('connected', (event: Event) => {
        try {
          // eslint-disable-next-line no-undef
          const messageEvent = event as MessageEvent;
          const data = JSON.parse(messageEvent.data);
          logger.debug('Connected event received:', data);
        } catch (error) {
          logger.error('Failed to parse connected event:', error);
        }
      });

      eventSource.addEventListener('detection', (event: Event) => {
        try {
          // eslint-disable-next-line no-undef
          const messageEvent = event as MessageEvent;
          const data = JSON.parse(messageEvent.data);
          handleSSEDetection(data);
        } catch (error) {
          logger.error('Failed to parse detection event:', error);
        }
      });

      eventSource.addEventListener('heartbeat', (event: Event) => {
        try {
          // eslint-disable-next-line no-undef
          const messageEvent = event as MessageEvent;
          const data = JSON.parse(messageEvent.data);
          logger.debug('Heartbeat event received, clients:', data.clients);
        } catch (error) {
          logger.error('Failed to parse heartbeat event:', error);
        }
      });

      eventSource.onerror = (error: Event) => {
        logger.error('SSE connection error:', error);
        // ReconnectingEventSource handles reconnection automatically
        // No need for manual reconnection logic
      };
    } catch (error) {
      logger.error('Failed to create ReconnectingEventSource:', error);
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
      logger.error('Error processing detection data:', error);
    }
  }

  onMount(() => {
    fetchDailySummary();
    fetchRecentDetections();
    fetchDashboardConfig();

    // Setup SSE connection for real-time updates
    connectToDetectionStream();

    // Initial preload of adjacent dates (reactive effect will handle subsequent preloads)
    triggerAdjacentPreload(selectedDate);

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

      // Clean up preload debounce timer
      if (preloadDebounceTimer) {
        clearTimeout(preloadDebounceTimer);
        preloadDebounceTimer = null;
      }

      // Clean up animation timers
      animationCleanupTimers.forEach(timer => clearTimeout(timer));
      animationCleanupTimers.clear();

      // Cancel pending RAF
      if (animationFrame !== null) {
        window.cancelAnimationFrame(animationFrame);
        animationFrame = null;
      }

      // Clear pending cleanups
      pendingCleanups.clear();

      // Clean up daily summary cache
      dailySummaryCache.clear();

      // Cancel any pending preload requests
      preloadCache.clear();
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
    selectedDate = getLocalDateString(date);
    handleDateChangeWithCleanup();
    fetchDailySummary();
  }

  function nextDay() {
    const date = new Date(selectedDate);
    date.setDate(date.getDate() + 1);
    const newDateString = getLocalDateString(date);
    if (!isFutureDate(newDateString)) {
      selectedDate = newDateString;
      handleDateChangeWithCleanup();
      fetchDailySummary();
    }
  }

  function goToToday() {
    selectedDate = getLocalDateString();
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

  // Derived state to check if we're viewing today's data
  const isViewingToday = $derived(selectedDate === getLocalDateString());

  // Queue daily summary updates with debouncing for rapid updates
  function queueDailySummaryUpdate(detection: Detection) {
    // Only allow SSE updates to daily summary when viewing today's data
    if (!isViewingToday) {
      logger.debug('Skipping daily summary SSE update - viewing historical data:', selectedDate);
      return;
    }

    // Additional safety check: ensure detection is for today
    if (detection.date !== selectedDate) {
      logger.debug(
        'Skipping daily summary update - detection date mismatch:',
        detection.date,
        'vs',
        selectedDate
      );
      return;
    }

    // Performance: Skip if too many pending updates to prevent UI freeze
    if (updateQueue.size > 20) {
      logger.warn('Too many pending daily summary updates, skipping to prevent performance issues');
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

  // Incremental daily summary update when new detection arrives via SSE
  function updateDailySummary(detection: Detection) {
    // Only allow SSE updates to daily summary when viewing today's data
    if (!isViewingToday) {
      logger.debug('Skipping daily summary update - viewing historical data:', selectedDate);
      return;
    }

    // Additional safety check: ensure detection is for today and matches selected date
    if (detection.date !== selectedDate && detection.date !== getLocalDateString()) {
      logger.debug(
        'Skipping daily summary update - detection date mismatch:',
        detection.date,
        'vs',
        selectedDate,
        'today:',
        getLocalDateString()
      );
      return;
    }

    // Parse the time string (HH:MM:SS format) to extract the hour
    let hour: number;
    try {
      hour = parseHour(detection.time);
    } catch (error) {
      logger.error(`Failed to parse detection time: ${detection.time}`, error);
      // Default to hour 0 if parsing fails
      hour = 0;
    }

    const existingIndex = dailySummary.findIndex(s => s.species_code === detection.speciesCode);

    if (existingIndex >= 0) {
      // Incremental update for existing species - minimize object creation
      const existing = safeArrayAccess(dailySummary, existingIndex);
      if (!existing) return;
      const updated = { ...existing };
      updated.previousCount = updated.count;
      updated.count++;
      updated.countIncreased = true;
      updated.hourly_counts = [...updated.hourly_counts];
      const currentHourCount = safeArrayAccess(updated.hourly_counts, hour, 0) ?? 0;
      // Use splice to safely assign at dynamic index
      if (hour >= 0 && hour < 24) {
        updated.hourly_counts.splice(hour, 1, currentHourCount + 1);
      }
      updated.hourlyUpdated = [hour];
      updated.latest_heard = detection.time;

      // Optimized position update using $derived.by pattern
      const currentPosition = existingIndex;
      const newPosition = dailySummary.findIndex(
        (species, i) => i < currentPosition && species.count < updated.count
      );

      if (newPosition !== -1) {
        // Species needs to move up - rebuild array with minimal changes
        dailySummary = [
          ...dailySummary.slice(0, newPosition),
          updated,
          ...dailySummary.slice(newPosition, currentPosition),
          ...dailySummary.slice(currentPosition + 1),
        ];
        logger.debug(
          `Moved species up: ${detection.commonName} from position ${currentPosition} to ${newPosition} (count: ${updated.count})`
        );
      } else {
        // Species stays in same position - just update in place
        dailySummary = [
          ...dailySummary.slice(0, currentPosition),
          updated,
          ...dailySummary.slice(currentPosition + 1),
        ];
        logger.debug(
          `Updated species in place: ${detection.commonName} at position ${currentPosition} (count: ${updated.count})`
        );
      }

      // Update cache incrementally instead of invalidating
      updateDailySummaryCacheEntry(selectedDate, dailySummary);

      // Clear animation flags after animation completes
      scheduleAnimationCleanup(
        () => {
          const currentIndex = dailySummary.findIndex(
            s => s.species_code === detection.speciesCode
          );
          if (currentIndex >= 0) {
            const currentItem = safeArrayAccess(dailySummary, currentIndex);
            if (!currentItem) return;
            const cleared = { ...currentItem };
            cleared.countIncreased = false;
            cleared.hourlyUpdated = [];

            dailySummary = [
              ...dailySummary.slice(0, currentIndex),
              cleared,
              ...dailySummary.slice(currentIndex + 1),
            ];

            // Update cache after animation cleanup too
            updateDailySummaryCacheEntry(selectedDate, dailySummary);
          }
        },
        1000,
        `count-${detection.speciesCode}`
      );
    } else {
      // Add new species with optimized insertion
      const newSpecies: DailySpeciesSummary = {
        scientific_name: detection.scientificName,
        common_name: detection.commonName,
        species_code: detection.speciesCode,
        count: 1,
        hourly_counts: Array(24).fill(0),
        high_confidence: detection.confidence >= 0.8,
        first_heard: detection.time,
        latest_heard: detection.time,
        thumbnail_url: '', // Empty string will trigger fallback in BirdThumbnailPopup
        isNew: true,
      };
      // Set the hourly count for the specific hour safely using splice
      if (hour >= 0 && hour < 24) {
        newSpecies.hourly_counts.splice(hour, 1, 1);
      }

      // Find insertion position with early termination for performance
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

      logger.debug(
        `Added new species: ${detection.commonName} (count: 1, hour: ${hour}) at position ${insertPosition === -1 ? dailySummary.length - 1 : insertPosition}`
      );

      // Update cache incrementally with new species included
      updateDailySummaryCacheEntry(selectedDate, dailySummary);

      // Clear animation flag after animation completes
      scheduleAnimationCleanup(
        () => {
          const currentIndex = dailySummary.findIndex(
            s => s.species_code === detection.speciesCode
          );
          if (currentIndex >= 0) {
            const currentItem = safeArrayAccess(dailySummary, currentIndex);
            if (!currentItem) return;
            const cleared = { ...currentItem };
            cleared.isNew = false;

            dailySummary = [
              ...dailySummary.slice(0, currentIndex),
              cleared,
              ...dailySummary.slice(currentIndex + 1),
            ];

            // Update cache after animation cleanup too
            updateDailySummaryCacheEntry(selectedDate, dailySummary);
          }
        },
        800,
        `new-${detection.speciesCode}`
      );
    }
  }

  // Preloading cache for batch requests - use $state.raw() for performance
  const preloadCache = $state.raw(new Set<string>());
  let preloadDebounceTimer: ReturnType<typeof setTimeout> | null = null;

  // Generate adjacent dates for preloading
  function getAdjacentDates(baseDate: string): string[] {
    const dates: string[] = [];
    const base = new Date(baseDate);

    // Previous date
    const prevDate = new Date(base);
    prevDate.setDate(prevDate.getDate() - 1);
    dates.push(getLocalDateString(prevDate));

    // Next date (only if not future)
    const nextDate = new Date(base);
    nextDate.setDate(nextDate.getDate() + 1);
    const nextDateString = getLocalDateString(nextDate);
    if (!isFutureDate(nextDateString)) {
      dates.push(nextDateString);
    }

    return dates;
  }

  // Batch preload adjacent dates using the new batch API
  function batchPreloadAdjacentDates(baseDate: string = selectedDate): void {
    const adjacentDates = getAdjacentDates(baseDate);

    // Filter out dates that are already cached or being preloaded
    const datesToPreload = adjacentDates.filter(
      date => !dailySummaryCache.has(date) && !preloadCache.has(date)
    );

    if (datesToPreload.length === 0) {
      logger.debug(`No adjacent dates need preloading for ${baseDate}`);
      return;
    }

    // Mark dates as being preloaded to prevent duplicate requests
    datesToPreload.forEach(date => preloadCache.add(date));

    // Start batch preloading using untrack to prevent reactive dependencies
    // Fire-and-forget operation for performance optimization
    // eslint-disable-next-line no-unused-vars
    const batchPreloadPromise = untrack(() => {
      const datesParam = datesToPreload.join(',');
      return fetch(`/api/v2/analytics/species/daily/batch?dates=${datesParam}`)
        .then(response => {
          if (!response.ok) {
            throw new Error(`Batch preload failed: ${response.statusText}`);
          }
          return response.json();
        })
        .then((batchData: Record<string, DailySpeciesSummary[]>) => {
          const timestamp = Date.now();
          let successCount = 0;

          // Cache all successfully loaded dates
          for (const [dateString, data] of Object.entries(batchData)) {
            if (data && Array.isArray(data)) {
              dailySummaryCache.set(dateString, {
                data: data,
                timestamp: timestamp,
              });
              successCount++;
            }
          }

          logger.debug(
            `Batch preloaded ${successCount}/${datesToPreload.length} adjacent dates for ${baseDate}`
          );
          return batchData;
        })
        .catch(error => {
          logger.debug(`Batch preload failed for ${baseDate}:`, error);
          // TODO: Add Sentry.io telemetry for batch preload failures to track network issues

          // Fall back to individual requests if batch fails
          logger.debug('Falling back to individual preload requests');
          datesToPreload.forEach(dateString => {
            fetch(`/api/v2/analytics/species/daily?date=${dateString}`)
              .then(response => (response.ok ? response.json() : null))
              .then(data => {
                if (data) {
                  dailySummaryCache.set(dateString, {
                    data: data,
                    timestamp: Date.now(),
                  });
                  logger.debug(`Individual fallback preload succeeded for ${dateString}`);
                }
              })
              .catch(fallbackError => {
                logger.debug(
                  `Individual fallback preload failed for ${dateString}:`,
                  fallbackError
                );
                // TODO: Add Sentry.io telemetry for individual fallback failures
              });
          });
        })
        .finally(() => {
          // Clean up preload tracking
          datesToPreload.forEach(date => preloadCache.delete(date));
        });
    });
  }

  // Trigger batch preload of adjacent dates with debouncing
  function triggerAdjacentPreload(baseDate: string = selectedDate) {
    // Clear existing debounce timer
    if (preloadDebounceTimer) {
      clearTimeout(preloadDebounceTimer);
    }

    // Debounce preloading to avoid excessive requests during rapid date changes
    preloadDebounceTimer = setTimeout(() => {
      logger.debug(`Triggering batch adjacent preload for ${baseDate}`);

      // Use batch preloading for better performance
      batchPreloadAdjacentDates(baseDate);

      preloadDebounceTimer = null;
    }, 150); // Wait 150ms for settling
  }

  // Reactive preloading - triggers when selectedDate changes
  $effect(() => {
    // Only preload if we have a valid selectedDate and not during initial load
    if (selectedDate) {
      triggerAdjacentPreload(selectedDate);
    }
  });

  // Update freeze state management
  function handleFreezeStart() {
    freezeCount++;
  }

  function handleFreezeEnd() {
    freezeCount--;
    // Clamp to prevent negative values due to unmount edge cases
    freezeCount = Math.max(0, freezeCount);

    // Process pending detections when all interactions are complete
    if (freezeCount === 0 && pendingDetectionQueue.length > 0) {
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
    logger.debug('Detection clicked:', detection);
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
    onFreezeStart={handleFreezeStart}
    onFreezeEnd={handleFreezeEnd}
    updatesAreFrozen={freezeCount > 0}
  />
</div>
