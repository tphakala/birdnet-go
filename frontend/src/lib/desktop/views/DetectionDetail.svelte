<!--
  DetectionDetail.svelte - Single Detection View Component

  Purpose: Display comprehensive details for a single bird detection

  Features:
  - Hero section with species information and confidence
  - Audio player with large spectrogram visualization
  - Weather and environmental context
  - Species rarity and taxonomy information
  - Detection history and tracking metadata
  - Review and action controls

  Props:
  - detectionId: string - The ID of the detection to display
-->
<script lang="ts">
  import ConfidenceCircle from '$lib/desktop/components/data/ConfidenceCircle.svelte';
  import WeatherDetails from '$lib/desktop/components/data/WeatherDetails.svelte';
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import SpeciesBadges from '$lib/desktop/components/modals/SpeciesBadges.svelte';
  import SpeciesThumbnail from '$lib/desktop/components/modals/SpeciesThumbnail.svelte';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import { t } from '$lib/i18n';
  import type { Detection } from '$lib/types/detection.types';
  import { hasReviewPermission } from '$lib/utils/auth';
  import { formatLocalDateTime } from '$lib/utils/date';
  import { loggers } from '$lib/utils/logger';
  import { Download } from '@lucide/svelte';

  // Interface definitions for API responses
  interface SpeciesRarity {
    status: string;
    score: number;
    location_based: boolean;
    latitude: number;
    longitude: number;
  }

  interface SpeciesInfo {
    rarity?: SpeciesRarity;
    [key: string]: unknown; // Allow additional properties
  }

  interface TaxonomyHierarchy {
    kingdom: string;
    phylum: string;
    class: string;
    order: string;
    family: string;
    family_common?: string;
    genus: string;
    species: string;
  }

  interface Subspecies {
    scientific_name: string;
    common_name?: string;
  }

  interface TaxonomyInfo {
    taxonomy?: TaxonomyHierarchy;
    subspecies?: Subspecies[];
  }

  // ReviewCard component type (Svelte 5 component)
  type ReviewCardComponent =
    typeof import('$lib/desktop/components/review/ReviewCard.svelte').default;

  const logger = loggers.ui;

  // Constants
  const TAB_FOCUS_DELAY_MS = 50;
  type TabType = 'overview' | 'taxonomy' | 'history' | 'notes' | 'review';

  // Helper to calculate detection duration with NaN safety
  function calculateDuration(endTime: string, beginTime: string): string {
    const end = parseFloat(endTime);
    const begin = parseFloat(beginTime);
    const duration = end - begin;
    if (Number.isNaN(duration)) return '—';
    return `${duration}s`;
  }

  interface Props {
    detectionId?: string;
  }

  const { detectionId: detectionIdProp }: Props = $props();

  // Resolved detection ID - initialized by $effect below, not directly from prop
  // to ensure reactive updates work correctly
  let resolvedDetectionId = $state<string | undefined>(undefined);

  // State
  let activeTab = $state<TabType>('overview');

  // Dynamic review component loading
  let ReviewCard: ReviewCardComponent | null = $state(null);

  // Use the existing auth store pattern (same as DesktopSidebar)
  let canReview = $derived(hasReviewPermission());
  let detection = $state<Detection | null>(null);
  let speciesInfo = $state<SpeciesInfo | null>(null);
  let taxonomyInfo = $state<TaxonomyInfo | null>(null);
  let isLoadingDetection = $state(true);
  let isLoadingTaxonomy = $state(false);
  let detectionError = $state<string | null>(null);

  // Derived state for subspecies with proper typing
  let subspeciesList = $derived<Subspecies[]>(
    taxonomyInfo?.subspecies && Array.isArray(taxonomyInfo.subspecies)
      ? taxonomyInfo.subspecies
      : []
  );

  // AbortController for preventing race conditions
  let detectionController: AbortController | null = null;
  let speciesController: AbortController | null = null;
  let taxonomyController: AbortController | null = null;

  // Resolve detection ID from URL if not provided via prop
  $effect(() => {
    if (!detectionIdProp) {
      const pathParts = window.location.pathname.split('/');
      const detectionIndex = pathParts.indexOf('detections');
      if (detectionIndex !== -1 && pathParts[detectionIndex + 1]) {
        resolvedDetectionId = pathParts[detectionIndex + 1];
      }
    } else {
      resolvedDetectionId = detectionIdProp;
    }
  });

  // Initialize tab from URL query parameter (with permission check for review tab)
  $effect(() => {
    const urlParams = new URLSearchParams(window.location.search);
    const tabParam = urlParams.get('tab');
    const validTabs: TabType[] = ['overview', 'taxonomy', 'history', 'notes', 'review'];
    if (tabParam && validTabs.includes(tabParam as TabType)) {
      // If review tab requested but user lacks permission, fall back to overview
      activeTab = tabParam === 'review' && !canReview ? 'overview' : (tabParam as TabType);
    }
  });

  // Fetch detection data when resolvedDetectionId changes
  $effect(() => {
    if (resolvedDetectionId) {
      fetchDetection();
    }

    // Cleanup: abort pending requests when component unmounts or ID changes
    return () => {
      detectionController?.abort();
      speciesController?.abort();
      taxonomyController?.abort();
    };
  });

  // Fetch detection data
  async function fetchDetection() {
    if (!resolvedDetectionId) {
      detectionError = t('detections.errors.noIdProvided');
      isLoadingDetection = false;
      return;
    }

    // Cancel previous request
    detectionController?.abort();
    detectionController = new AbortController();

    isLoadingDetection = true;
    detectionError = null;

    try {
      const response = await fetch(`/api/v2/detections/${resolvedDetectionId}`, {
        signal: detectionController.signal,
      });

      // Check if request was aborted during fetch
      if (detectionController.signal.aborted) return;

      if (response.ok) {
        const data = (await response.json()) as Detection;
        // Check again after await - signal may have been aborted during JSON parsing
        if (detectionController.signal.aborted) return;
        detection = data;
      } else {
        let errorMessage: string;
        switch (response.status) {
          case 404:
            errorMessage = t('detections.errors.notFound');
            break;
          case 403:
            errorMessage = t('detections.errors.noPermission');
            break;
          case 401:
            errorMessage = t('detections.errors.loginRequired');
            break;
          case 500:
          case 502:
          case 503:
            errorMessage = t('detections.errors.serverError');
            break;
          default:
            errorMessage = t('detections.errors.loadFailed', { status: response.status });
        }
        throw new Error(errorMessage);
      }

      // Fetch additional data after detection is loaded
      if (detection) {
        fetchSpeciesInfo();
        fetchTaxonomy();
      }
    } catch (error) {
      if (error instanceof Error && error.name === 'AbortError') {
        // Request was aborted, don't update state
        return;
      }
      detectionError =
        error instanceof Error ? error.message : t('detections.errors.loadFailed', { status: '' });
      logger.error('Error fetching detection:', error);
    } finally {
      isLoadingDetection = false;
    }
  }

  // Fetch species information (public data - no auth required)
  async function fetchSpeciesInfo() {
    if (!detection?.scientificName) return;

    // Cancel previous request
    speciesController?.abort();
    speciesController = new AbortController();

    try {
      const response = await fetch(
        `/api/v2/species?scientific_name=${encodeURIComponent(detection.scientificName)}`,
        { signal: speciesController.signal }
      );
      // Check if request was aborted during fetch
      if (speciesController.signal.aborted) return;
      if (response.ok) {
        const data = await response.json();
        // Check again after await
        if (speciesController.signal.aborted) return;
        speciesInfo = data;
      }
    } catch (error) {
      if (error instanceof Error && error.name === 'AbortError') {
        // Request was aborted, don't update state
        return;
      }
      logger.error('Error fetching species info:', error);
    } finally {
      speciesController = null;
    }
  }

  // Fetch taxonomy information (public data - no auth required)
  async function fetchTaxonomy() {
    if (!detection?.scientificName) return;

    // Cancel previous request
    taxonomyController?.abort();
    taxonomyController = new AbortController();

    isLoadingTaxonomy = true;
    try {
      const response = await fetch(
        `/api/v2/species/taxonomy?scientific_name=${encodeURIComponent(detection.scientificName)}`,
        { signal: taxonomyController.signal }
      );
      // Check if request was aborted during fetch
      if (taxonomyController.signal.aborted) return;
      if (response.ok) {
        const data = await response.json();
        // Check again after await
        if (taxonomyController.signal.aborted) return;
        taxonomyInfo = data;
      }
    } catch (error) {
      if (error instanceof Error && error.name === 'AbortError') {
        // Request was aborted, don't update state
        return;
      }
      logger.error('Error fetching taxonomy info:', error);
    } finally {
      isLoadingTaxonomy = false;
      taxonomyController = null;
    }
  }

  // Dynamically load review component when user has review permission
  $effect(() => {
    if (canReview && !ReviewCard) {
      import('$lib/desktop/components/review/ReviewCard.svelte')
        .then(module => {
          ReviewCard = module.default;
          logger.debug('ReviewCard component loaded for authenticated user');
        })
        .catch(error => {
          logger.error('Failed to load ReviewCard component:', error);
        });
    }
  });

  // Handle review card completion
  function handleReviewComplete() {
    // Switch back to overview tab after successful save
    activeTab = 'overview';
    // Refetch detection data to show updated status
    if (resolvedDetectionId) {
      fetchDetection();
    }
  }

  // Keyboard navigation handler for tab buttons (arrow keys only - Enter/Space use native button behavior)
  function handleTabKeydown(e: KeyboardEvent) {
    const tabs: TabType[] = ['overview', 'taxonomy', 'history', 'notes'];
    if (canReview) tabs.push('review');

    const currentIndex = tabs.indexOf(activeTab);
    if (currentIndex === -1) return;

    if (e.key === 'ArrowRight') {
      e.preventDefault();
      activeTab = tabs[(currentIndex + 1) % tabs.length];
    } else if (e.key === 'ArrowLeft') {
      e.preventDefault();
      activeTab = tabs[(currentIndex - 1 + tabs.length) % tabs.length];
    }
  }

  // Focus management for tab switching
  $effect(() => {
    // Focus the active tab panel when tab changes for keyboard users
    const activePanel = document.getElementById(`tab-panel-${activeTab}`);
    let timeoutId: ReturnType<typeof setTimeout> | null = null;

    if (activePanel && document.activeElement?.getAttribute('role') === 'tab') {
      // Small delay to ensure the panel is rendered
      timeoutId = setTimeout(() => {
        activePanel.focus();
      }, TAB_FOCUS_DELAY_MS);
    }

    // Cleanup: clear timeout when effect re-runs or component unmounts
    return () => {
      if (timeoutId) clearTimeout(timeoutId);
    };
  });

  // URL state management - update URL when tab changes
  $effect(() => {
    if (typeof window !== 'undefined' && resolvedDetectionId) {
      const url = new URL(window.location.href);

      // Update or remove tab parameter based on active tab
      if (activeTab === 'overview') {
        // Remove tab parameter for overview (default tab)
        url.searchParams.delete('tab');
      } else {
        // Set tab parameter for non-default tabs
        url.searchParams.set('tab', activeTab);
      }

      // Update URL without triggering navigation (replaceState)
      const newUrl = url.pathname + url.search;
      if (newUrl !== window.location.pathname + window.location.search) {
        window.history.replaceState(null, '', newUrl);
      }
    }
  });

  // Handle browser back/forward navigation
  $effect(() => {
    if (typeof window !== 'undefined') {
      function handlePopState() {
        // Read tab from current URL when user navigates back/forward
        const urlParams = new URLSearchParams(window.location.search);
        const tabParam = urlParams.get('tab');
        const validTabs = ['overview', 'taxonomy', 'history', 'notes', 'review'] as const;

        if (tabParam && validTabs.includes(tabParam as typeof activeTab)) {
          // Only update if it's a valid tab and user has permission (for review tab)
          if (tabParam === 'review' && !canReview) {
            activeTab = 'overview';
          } else {
            activeTab = tabParam as typeof activeTab;
          }
        } else {
          // Default to overview if no valid tab specified
          activeTab = 'overview';
        }
      }

      // Listen for popstate events (back/forward navigation)
      window.addEventListener('popstate', handlePopState);

      return () => {
        window.removeEventListener('popstate', handlePopState);
      };
    }
  });
</script>

<!-- Snippets for better organization -->
{#snippet heroSection(detection: Detection)}
  <section class="card bg-base-100 shadow-xs" aria-labelledby="species-heading">
    <div class="card-body">
      <!-- Species info container - similar to ReviewModal -->
      <div class="bg-base-200 rounded-lg p-4" role="region" aria-label="Species information">
        <!-- Responsive layout: stack on mobile, row on md+ -->
        <div class="flex flex-col md:flex-row gap-4 items-start">
          <!-- Section 1: Thumbnail + Species Names (flex-grow for more space) -->
          <div class="flex gap-3 md:gap-4 items-center flex-1 min-w-0 md:min-w-[240px] w-full">
            <SpeciesThumbnail
              scientificName={detection.scientificName}
              commonName={detection.commonName}
              size="lg"
            />
            <div class="flex-1 min-w-0">
              <h1
                id="species-heading"
                class="text-2xl md:text-3xl font-semibold text-base-content mb-1 truncate"
              >
                {detection.commonName}
                <span class="sr-only">detection details</span>
              </h1>
              <p
                class="text-base md:text-lg text-base-content opacity-60 italic truncate"
                aria-label="Scientific name"
              >
                {detection.scientificName}
              </p>
              <div class="mt-3" aria-label="Species classification badges">
                <SpeciesBadges {detection} size="lg" />
              </div>
            </div>
          </div>

          <!-- Mobile: 2-column grid; md+: flex row with fixed-width sections -->
          <div
            class="grid grid-cols-2 gap-4 w-full md:flex md:flex-row md:gap-4 md:w-auto md:shrink-0"
          >
            <!-- Section 2: Date & Time (fixed width) -->
            <div
              class="md:shrink-0 md:text-center md:min-w-[120px] w-full md:w-auto"
              role="region"
              aria-labelledby="datetime-heading"
            >
              <h2 id="datetime-heading" class="text-sm text-base-content opacity-60 mb-2">
                {t('detections.headers.dateTime')}
              </h2>
              <div class="text-base text-base-content" aria-label="Detection date">
                {detection.date}
              </div>
              <div class="text-base text-base-content" aria-label="Detection time">
                {detection.time}
              </div>
              {#if detection.timeOfDay}
                <div
                  class="text-sm text-base-content opacity-60 mt-1 capitalize"
                  aria-label="Time of day"
                >
                  {detection.timeOfDay}
                </div>
              {/if}
            </div>

            <!-- Section 3: Weather Conditions (fixed width) -->
            <div
              class="hidden md:block md:shrink-0 md:text-center md:min-w-[180px] w-full md:w-auto"
              role="region"
              aria-labelledby="weather-heading"
            >
              <h2 id="weather-heading" class="text-sm text-base-content opacity-60 mb-2">
                {t('detections.headers.weather')}
              </h2>
              {#if detection.weather}
                <div
                  class="flex justify-start md:justify-center"
                  aria-label="Weather conditions at time of detection"
                >
                  <WeatherDetails
                    weatherIcon={detection.weather.weatherIcon}
                    weatherDescription={detection.weather.description}
                    temperature={detection.weather.temperature}
                    windSpeed={detection.weather.windSpeed}
                    windGust={detection.weather.windGust}
                    units={detection.weather.units}
                    size="md"
                    className="text-left md:text-center"
                  />
                </div>
              {:else}
                <div class="text-sm text-base-content opacity-40 italic" role="status">
                  {t('detections.weather.noData')}
                </div>
              {/if}
            </div>

            <!-- Section 4: Confidence + Actions (fixed width) -->
            <div
              class="md:shrink-0 flex flex-col md:items-center w-full md:w-auto md:min-w-[120px]"
              role="region"
              aria-labelledby="confidence-heading"
            >
              <div class="flex items-center gap-3 md:flex-col md:items-center">
                <h2
                  id="confidence-heading"
                  class="text-sm text-base-content opacity-60 mb-0 md:mb-2"
                >
                  {t('common.labels.confidence')}
                </h2>
                <div
                  aria-label="Detection confidence {detection.confidence}%"
                  class="md:self-auto self-start md:scale-100 scale-90"
                >
                  <ConfidenceCircle confidence={detection.confidence} size="xl" />
                </div>
              </div>

              <!-- Actions below confidence -->
              <div
                class="flex flex-row md:flex-col gap-2 mt-3"
                role="group"
                aria-label="Detection actions"
              >
                {#if detection.clipName}
                  <a
                    href={`/api/v2/media/audio/${detection.clipName}`}
                    download
                    class="btn btn-ghost btn-sm gap-2"
                    aria-label="Download audio clip for {detection.commonName} detection"
                  >
                    <Download class="size-5" />
                    {t('common.actions.download')}
                  </a>
                {/if}
              </div>
            </div>
          </div>
          <!-- end mobile grid wrapper -->
        </div>
      </div>
    </div>
  </section>
{/snippet}

{#snippet overviewTab(detection: Detection)}
  <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
    <!-- Environmental Conditions -->
    <section aria-labelledby="environmental-heading">
      <h3 id="environmental-heading" class="text-lg font-semibold mb-4">
        {t('detections.environmental.title')}
      </h3>
      {#if detection.weather}
        <WeatherDetails
          weatherIcon={detection.weather.weatherIcon}
          weatherDescription={detection.weather.description}
          temperature={detection.weather.temperature}
          windSpeed={detection.weather.windSpeed}
          windGust={detection.weather.windGust}
          units={detection.weather.units}
          size="lg"
          className="bg-base-200 rounded-lg p-4"
        />
      {:else}
        <p class="text-base-content opacity-60 italic">{t('detections.weather.noData')}</p>
      {/if}
    </section>

    <!-- Detection Metadata -->
    <section aria-labelledby="metadata-heading">
      <h3 id="metadata-heading" class="text-lg font-semibold mb-4">
        {t('detections.metadata.title')}
      </h3>
      <div
        class="bg-base-200 rounded-lg p-4 space-y-2"
        role="table"
        aria-label="Detection metadata"
      >
        <div class="flex justify-between">
          <span class="text-base-content opacity-60">{t('detections.metadata.source')}:</span>
          <span>{detection.source ?? t('common.values.unknown')}</span>
        </div>
        <div class="flex justify-between">
          <span class="text-base-content opacity-60">{t('detections.metadata.duration')}:</span>
          <span>{calculateDuration(detection.endTime, detection.beginTime)}</span>
        </div>
        {#if detection.verified !== 'unverified'}
          <div class="flex justify-between">
            <span class="text-base-content opacity-60">{t('detections.metadata.status')}:</span>
            <span class="capitalize">{detection.verified}</span>
          </div>
        {/if}
        {#if detection.locked}
          <div class="flex justify-between">
            <span class="text-base-content opacity-60">{t('detections.metadata.locked')}:</span>
            <span>{t('common.values.yes')}</span>
          </div>
        {/if}
      </div>
    </section>

    <!-- Species Rarity (if available) -->
    {#if speciesInfo?.rarity}
      <section aria-labelledby="rarity-heading">
        <h3 id="rarity-heading" class="text-lg font-semibold mb-4">{t('species.rarity.title')}</h3>
        <div class="bg-base-200 rounded-lg p-4">
          <div class="flex items-center justify-between mb-2">
            <span class="text-lg font-medium capitalize">{speciesInfo.rarity.status}</span>
            <span class="text-sm text-base-content opacity-60">
              {t('species.rarity.score')}: {(speciesInfo.rarity.score * 100).toFixed(1)}%
            </span>
          </div>
          {#if speciesInfo.rarity.location_based}
            <p class="text-sm text-base-content opacity-60">
              {t('species.rarity.basedOnLocation', {
                latitude: speciesInfo.rarity.latitude.toFixed(2),
                longitude: speciesInfo.rarity.longitude.toFixed(2),
              })}
            </p>
          {/if}
        </div>
      </section>
    {/if}
  </div>
{/snippet}

{#snippet taxonomyTab()}
  <div>
    {#if isLoadingTaxonomy}
      <div role="status" aria-label="Loading taxonomy information">
        <!-- Skeleton Loader for Taxonomy -->
        <section aria-labelledby="taxonomy-skeleton-heading">
          <div class="animate-pulse">
            <!-- Skeleton heading -->
            <div class="h-6 bg-base-300 rounded-sm w-48 mb-4"></div>

            <!-- Skeleton taxonomy hierarchy container -->
            <div class="bg-base-200 rounded-lg p-6">
              <div class="space-y-3">
                <!-- Kingdom skeleton -->
                <div class="flex items-center gap-3">
                  <div class="h-4 bg-base-300 rounded-sm w-16"></div>
                  <div class="h-4 bg-base-300 rounded-sm w-24"></div>
                </div>
                <!-- Phylum skeleton -->
                <div class="flex items-center gap-3 ml-6">
                  <div class="h-4 bg-base-300 rounded-sm w-14"></div>
                  <div class="h-4 bg-base-300 rounded-sm w-20"></div>
                </div>
                <!-- Class skeleton -->
                <div class="flex items-center gap-3 ml-12">
                  <div class="h-4 bg-base-300 rounded-sm w-10"></div>
                  <div class="h-4 bg-base-300 rounded-sm w-16"></div>
                </div>
                <!-- Order skeleton -->
                <div class="flex items-center gap-3 ml-18">
                  <div class="h-4 bg-base-300 rounded-sm w-12"></div>
                  <div class="h-4 bg-base-300 rounded-sm w-28"></div>
                </div>
                <!-- Family skeleton -->
                <div class="flex items-center gap-3 ml-24">
                  <div class="h-4 bg-base-300 rounded-sm w-14"></div>
                  <div class="h-4 bg-base-300 rounded-sm w-32"></div>
                </div>
                <!-- Genus skeleton -->
                <div class="flex items-center gap-3 ml-30">
                  <div class="h-4 bg-base-300 rounded-sm w-12"></div>
                  <div class="h-4 bg-base-300 rounded-sm w-20"></div>
                </div>
                <!-- Species skeleton -->
                <div class="flex items-center gap-3 ml-36">
                  <div class="h-4 bg-base-300 rounded-sm w-16"></div>
                  <div class="h-4 bg-base-300 rounded-sm w-24"></div>
                </div>
              </div>
            </div>

            <!-- Skeleton subspecies section -->
            <div class="mt-6">
              <div class="h-6 bg-base-300 rounded-sm w-32 mb-4"></div>
              <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
                <div class="bg-base-200 rounded-lg p-3">
                  <div class="h-4 bg-base-300 rounded-sm w-full mb-2"></div>
                  <div class="h-3 bg-base-300 rounded-sm w-3/4"></div>
                </div>
                <div class="bg-base-200 rounded-lg p-3">
                  <div class="h-4 bg-base-300 rounded-sm w-full mb-2"></div>
                  <div class="h-3 bg-base-300 rounded-sm w-2/3"></div>
                </div>
              </div>
            </div>
          </div>
        </section>
      </div>
    {:else if taxonomyInfo?.taxonomy}
      <section aria-labelledby="taxonomy-hierarchy-heading">
        <h3 id="taxonomy-hierarchy-heading" class="text-lg font-semibold mb-4">
          {t('species.taxonomy.hierarchy')}
        </h3>
        <div class="bg-base-200 rounded-lg p-6">
          <!-- Visual Family Tree -->
          <div class="space-y-3">
            <div class="flex items-center gap-3">
              <span class="text-sm text-base-content opacity-60 w-24"
                >{t('species.taxonomy.labels.kingdom')}:</span
              >
              <span class="font-medium">{taxonomyInfo.taxonomy.kingdom}</span>
            </div>
            <div class="flex items-center gap-3 ml-6">
              <span class="text-sm text-base-content opacity-60 w-24"
                >{t('species.taxonomy.labels.phylum')}:</span
              >
              <span class="font-medium">{taxonomyInfo.taxonomy.phylum}</span>
            </div>
            <div class="flex items-center gap-3 ml-12">
              <span class="text-sm text-base-content opacity-60 w-24"
                >{t('species.taxonomy.labels.class')}:</span
              >
              <span class="font-medium">{taxonomyInfo.taxonomy.class}</span>
            </div>
            <div class="flex items-center gap-3 ml-18">
              <span class="text-sm text-base-content opacity-60 w-24"
                >{t('species.taxonomy.labels.order')}:</span
              >
              <span class="font-medium">{taxonomyInfo.taxonomy.order}</span>
            </div>
            <div class="flex items-center gap-3 ml-24">
              <span class="text-sm text-base-content opacity-60 w-24"
                >{t('species.taxonomy.labels.family')}:</span
              >
              <span class="font-medium">
                {taxonomyInfo.taxonomy.family}
                {#if taxonomyInfo.taxonomy.family_common}
                  <span class="text-base-content opacity-60">
                    ({taxonomyInfo.taxonomy.family_common})</span
                  >
                {/if}
              </span>
            </div>
            <div class="flex items-center gap-3 ml-30">
              <span class="text-sm text-base-content opacity-60 w-24"
                >{t('species.taxonomy.labels.genus')}:</span
              >
              <span class="font-medium">{taxonomyInfo.taxonomy.genus}</span>
            </div>
            <div class="flex items-center gap-3 ml-36">
              <span class="text-sm text-base-content opacity-60 w-24"
                >{t('species.taxonomy.labels.species')}:</span
              >
              <span class="font-medium italic">{taxonomyInfo.taxonomy.species}</span>
            </div>
          </div>
        </div>
      </section>

      {#if subspeciesList.length > 0}
        <section aria-labelledby="subspecies-heading">
          <h3 id="subspecies-heading" class="text-lg font-semibold mt-6 mb-4">
            {t('species.taxonomy.subspecies')}
          </h3>
          <div
            class="grid grid-cols-1 md:grid-cols-2 gap-3"
            role="list"
            aria-label="Subspecies list"
          >
            {#each subspeciesList as subspecies (subspecies.scientific_name)}
              <div class="bg-base-200 rounded-lg p-3" role="listitem">
                <p class="font-medium italic" aria-label="Scientific name">
                  {subspecies.scientific_name}
                </p>
                {#if subspecies.common_name}
                  <p class="text-sm text-base-content opacity-60" aria-label="Common name">
                    {subspecies.common_name}
                  </p>
                {/if}
              </div>
            {/each}
          </div>
        </section>
      {/if}
    {:else}
      <p class="text-base-content opacity-60 italic">{t('species.taxonomy.noData')}</p>
    {/if}
  </div>
{/snippet}

{#snippet historyTab()}
  <section aria-labelledby="history-heading">
    <h3 id="history-heading" class="text-lg font-semibold mb-4">{t('detections.history.title')}</h3>
    <p class="text-base-content opacity-60 italic" role="status">
      {t('detections.history.comingSoon')}
    </p>
  </section>
{/snippet}

{#snippet notesTab(detection: Detection)}
  <section aria-labelledby="notes-heading">
    <h3 id="notes-heading" class="text-lg font-semibold mb-4">{t('detections.notes.title')}</h3>
    {#if detection.comments && detection.comments.length > 0}
      <div class="space-y-3" role="list" aria-label="Detection comments">
        {#each detection.comments as comment (comment.id ?? comment.createdAt)}
          <article class="bg-base-200 rounded-lg p-4" role="listitem">
            <p aria-label="Comment text">{comment.entry}</p>
            <p class="text-sm text-base-content opacity-60 mt-2" aria-label="Comment timestamp">
              {formatLocalDateTime(new Date(comment.createdAt))}
            </p>
          </article>
        {/each}
      </div>
    {:else}
      <p class="text-base-content opacity-60 italic" role="status">
        {t('detections.notes.noComments')}
      </p>
    {/if}
  </section>
{/snippet}

<!-- Main component -->
<main class="col-span-12 space-y-4" aria-label="Detection details">
  <!-- Loading state with live region -->
  <div role="status" aria-live="polite" class="sr-only">
    {#if isLoadingDetection}
      {t('detections.aria.loading')}
    {:else if detection}
      {t('detections.aria.loaded', { species: detection.commonName })}
    {:else if detectionError}
      {t('detections.aria.error', { error: detectionError })}
    {/if}
  </div>

  {#if isLoadingDetection}
    <div class="card bg-base-100 shadow-xs">
      <div class="card-body">
        <div class="flex justify-center items-center h-64" aria-label="Loading detection details">
          <LoadingSpinner size="lg" />
        </div>
      </div>
    </div>
  {:else if detectionError}
    <div class="card bg-base-100 shadow-xs">
      <div class="card-body">
        <div role="alert" aria-live="assertive">
          <ErrorAlert message={detectionError} />
        </div>
      </div>
    </div>
  {:else if detection}
    <!-- Hero Section -->
    {@render heroSection(detection)}

    <!-- Media Section -->
    <section class="card bg-base-100 shadow-xs" aria-labelledby="media-heading">
      <div class="card-body">
        <h2 id="media-heading" class="text-xl font-semibold mb-4">{t('detections.media.title')}</h2>
        <div role="region" aria-label="Audio recording and spectrogram for {detection.commonName}">
          <div class="detail-audio-container">
            <AudioPlayer
              audioUrl={`/api/v2/audio/${detection.id}`}
              detectionId={detection.id.toString()}
              showSpectrogram={true}
              spectrogramSize="xl"
              spectrogramRaw={false}
              responsive={true}
              className="w-full"
            />
          </div>
        </div>
      </div>
    </section>

    <!-- Tabbed Content -->
    <section class="card bg-base-100 shadow-xs" aria-labelledby="tabs-heading">
      <div class="card-body">
        <h2 id="tabs-heading" class="sr-only">Detection information tabs</h2>
        <!-- Tab Navigation -->
        <div
          class="tabs tabs-boxed mb-6 overflow-x-auto flex-nowrap"
          role="tablist"
          aria-label="Detection details tabs"
        >
          <button
            id="tab-overview"
            role="tab"
            class="tab"
            class:tab-active={activeTab === 'overview'}
            aria-selected={activeTab === 'overview'}
            aria-controls="tab-panel-overview"
            tabindex={activeTab === 'overview' ? 0 : -1}
            onclick={() => (activeTab = 'overview')}
            onkeydown={handleTabKeydown}
          >
            {t('detections.tabs.overview')}
          </button>
          <button
            id="tab-taxonomy"
            role="tab"
            class="tab"
            class:tab-active={activeTab === 'taxonomy'}
            aria-selected={activeTab === 'taxonomy'}
            aria-controls="tab-panel-taxonomy"
            tabindex={activeTab === 'taxonomy' ? 0 : -1}
            onclick={() => (activeTab = 'taxonomy')}
            onkeydown={handleTabKeydown}
          >
            {t('detections.tabs.taxonomy')}
          </button>
          <button
            id="tab-history"
            role="tab"
            class="tab"
            class:tab-active={activeTab === 'history'}
            aria-selected={activeTab === 'history'}
            aria-controls="tab-panel-history"
            tabindex={activeTab === 'history' ? 0 : -1}
            onclick={() => (activeTab = 'history')}
            onkeydown={handleTabKeydown}
          >
            {t('detections.tabs.history')}
          </button>
          <button
            id="tab-notes"
            role="tab"
            class="tab"
            class:tab-active={activeTab === 'notes'}
            aria-selected={activeTab === 'notes'}
            aria-controls="tab-panel-notes"
            tabindex={activeTab === 'notes' ? 0 : -1}
            onclick={() => (activeTab = 'notes')}
            onkeydown={handleTabKeydown}
          >
            {t('detections.tabs.notes')}
          </button>
          {#if canReview}
            <button
              id="tab-review"
              role="tab"
              class="tab"
              class:tab-active={activeTab === 'review'}
              aria-selected={activeTab === 'review'}
              aria-controls="tab-panel-review"
              tabindex={activeTab === 'review' ? 0 : -1}
              onclick={() => (activeTab = 'review')}
              onkeydown={handleTabKeydown}
            >
              {t('common.actions.review')}
            </button>
          {/if}
        </div>

        <!-- Tab Content -->
        {#if activeTab === 'overview'}
          <div
            role="tabpanel"
            id="tab-panel-overview"
            aria-labelledby="tab-overview"
            aria-hidden="false"
            tabindex="0"
          >
            {@render overviewTab(detection)}
          </div>
        {:else if activeTab === 'taxonomy'}
          <div
            role="tabpanel"
            id="tab-panel-taxonomy"
            aria-labelledby="tab-taxonomy"
            aria-hidden="false"
            tabindex="0"
          >
            {@render taxonomyTab()}
          </div>
        {:else if activeTab === 'history'}
          <div
            role="tabpanel"
            id="tab-panel-history"
            aria-labelledby="tab-history"
            aria-hidden="false"
            tabindex="0"
          >
            {@render historyTab()}
          </div>
        {:else if activeTab === 'notes'}
          <div
            role="tabpanel"
            id="tab-panel-notes"
            aria-labelledby="tab-notes"
            aria-hidden="false"
            tabindex="0"
          >
            {@render notesTab(detection)}
          </div>
        {:else if activeTab === 'review' && canReview && ReviewCard}
          <div
            role="tabpanel"
            id="tab-panel-review"
            aria-labelledby="tab-review"
            aria-hidden="false"
            tabindex="0"
          >
            <ReviewCard {detection} onSaveComplete={handleReviewComplete} />
          </div>
        {/if}
      </div>
    </section>
  {/if}
</main>

<style>
  /* Detail view audio container - 2:1 aspect ratio matching spectrogram dimensions */
  .detail-audio-container {
    position: relative;
    width: 100%;
    max-width: 1200px; /* Limit maximum width for very large screens */
    margin: 0 auto; /* Center the container */
    min-height: var(--spectrogram-min-height, 60px); /* Fallback to 60px if var not defined */
    aspect-ratio: var(--spectrogram-aspect-ratio, 2 / 1); /* Fallback to 2:1 if var not defined */
    background: linear-gradient(to bottom, rgb(128 128 128 / 0.05), rgb(128 128 128 / 0.02));
    border-radius: 0.5rem;
    overflow: hidden;
  }

  /* Ensure AudioPlayer fills container - using more specific selectors to avoid !important */
  .detail-audio-container :global(.group) {
    width: 100%;
    height: 100%;
    position: absolute;
    top: 0;
    left: 0;
  }

  /* Override any conflicting styles with higher specificity */
  .detail-audio-container > :global(div > .group) {
    width: 100%;
    height: 100%;
    position: absolute;
    top: 0;
    left: 0;
  }

  /* Spectrogram image sizing for detail view
     Use 'contain' to show the full spectrogram for detailed analysis without cropping.
     This differs from table/card views which use 'cover' for space-efficient previews. */
  .detail-audio-container :global(img) {
    object-fit: contain; /* Show full spectrogram for detailed analysis */
    height: 100%;
    width: 100%;
  }

  /* Higher specificity for image styles if needed */
  .detail-audio-container :global(.group img),
  .detail-audio-container :global(div img) {
    object-fit: contain;
    height: 100%;
    width: 100%;
  }
</style>
