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
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import ConfidenceCircle from '$lib/desktop/components/data/ConfidenceCircle.svelte';
  import WeatherDetails from '$lib/desktop/components/data/WeatherDetails.svelte';
  import SpeciesThumbnail from '$lib/desktop/components/modals/SpeciesThumbnail.svelte';
  import SpeciesBadges from '$lib/desktop/components/modals/SpeciesBadges.svelte';
  import { fetchWithCSRF } from '$lib/utils/api';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { mediaIcons } from '$lib/utils/icons';
  import type { Detection } from '$lib/types/detection.types';
  import { hasReviewPermission } from '$lib/utils/auth';

  const logger = loggers.ui;

  interface Props {
    detectionId?: string;
  }

  let { detectionId }: Props = $props();

  // State
  let activeTab = $state<'overview' | 'taxonomy' | 'history' | 'notes' | 'review'>('overview');

  // Dynamic review component loading
  let ReviewCard: any = $state(null);

  // Use the existing auth store pattern (same as DesktopSidebar)
  let canReview = $derived(hasReviewPermission());
  let detection = $state<Detection | null>(null);
  let speciesInfo = $state<any>(null);
  let taxonomyInfo = $state<any>(null);
  let isLoadingDetection = $state(true);
  let isLoadingTaxonomy = $state(false);
  let detectionError = $state<string | null>(null);

  // Extract detection ID from URL if not provided and fetch data reactively
  $effect(() => {
    if (!detectionId) {
      const pathParts = window.location.pathname.split('/');
      const detectionIndex = pathParts.indexOf('detections');
      if (detectionIndex !== -1 && pathParts[detectionIndex + 1]) {
        detectionId = pathParts[detectionIndex + 1];
      }
    }

    // Fetch detection data when detectionId changes
    if (detectionId) {
      fetchDetection();
    }
  });

  // Fetch detection data
  async function fetchDetection() {
    if (!detectionId) {
      detectionError = 'No detection ID provided';
      isLoadingDetection = false;
      return;
    }

    isLoadingDetection = true;
    detectionError = null;

    try {
      detection = await fetchWithCSRF<Detection>(`/api/v2/detections/${detectionId}`);

      // Fetch additional data after detection is loaded
      if (detection) {
        fetchSpeciesInfo();
        fetchTaxonomy();
      }
    } catch (error) {
      detectionError = error instanceof Error ? error.message : 'Failed to load detection';
      logger.error('Error fetching detection:', error);
    } finally {
      isLoadingDetection = false;
    }
  }

  // Fetch species information
  async function fetchSpeciesInfo() {
    if (!detection?.scientificName) return;

    try {
      speciesInfo = await fetchWithCSRF<any>(
        `/api/v2/species?scientific_name=${encodeURIComponent(detection.scientificName)}`
      );
    } catch (error) {
      logger.error('Error fetching species info:', error);
    }
  }

  // Fetch taxonomy information
  async function fetchTaxonomy() {
    if (!detection?.scientificName) return;

    isLoadingTaxonomy = true;
    try {
      taxonomyInfo = await fetchWithCSRF<any>(
        `/api/v2/species/taxonomy?scientific_name=${encodeURIComponent(detection.scientificName)}`
      );
    } catch (error) {
      logger.error('Error fetching taxonomy info:', error);
    } finally {
      isLoadingTaxonomy = false;
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
    if (detectionId) {
      fetchDetection();
    }
  }
</script>

<!-- Snippets for better organization -->
{#snippet heroSection(detection: Detection)}
  <div class="card bg-base-100 shadow-sm">
    <div class="card-body">
      <!-- Species info container - similar to ReviewModal -->
      <div class="bg-base-200/50 rounded-lg p-4">
        <!-- Single Row Layout: All 4 segments in one row using flex -->
        <div class="flex gap-4 items-start">
          <!-- Section 1: Thumbnail + Species Names (flex-grow for more space) -->
          <div class="flex gap-4 items-center flex-1 min-w-0">
            <SpeciesThumbnail
              scientificName={detection.scientificName}
              commonName={detection.commonName}
              size="lg"
            />
            <div class="flex-1 min-w-0">
              <h1 class="text-3xl font-semibold text-base-content mb-1 truncate">
                {detection.commonName}
              </h1>
              <p class="text-lg text-base-content/60 italic truncate">
                {detection.scientificName}
              </p>
              <div class="mt-3">
                <SpeciesBadges {detection} size="lg" />
              </div>
            </div>
          </div>

          <!-- Section 2: Date & Time (fixed width) -->
          <div class="flex-shrink-0 text-center" style:min-width="120px">
            <div class="text-sm text-base-content/60 mb-2">
              {t('detections.headers.dateTime')}
            </div>
            <div class="text-base text-base-content">{detection.date}</div>
            <div class="text-base text-base-content">{detection.time}</div>
            {#if detection.timeOfDay}
              <div class="text-sm text-base-content/60 mt-1 capitalize">
                {detection.timeOfDay}
              </div>
            {/if}
          </div>

          <!-- Section 3: Weather Conditions (fixed width) -->
          <div class="flex-shrink-0 text-center" style:min-width="180px">
            <div class="text-sm text-base-content/60 mb-2">
              {t('detections.headers.weather')}
            </div>
            {#if detection.weather}
              <div class="flex justify-center">
                <WeatherDetails
                  weatherIcon={detection.weather.weatherIcon}
                  weatherDescription={detection.weather.description}
                  temperature={detection.weather.temperature}
                  windSpeed={detection.weather.windSpeed}
                  windGust={detection.weather.windGust}
                  units={detection.weather.units}
                  size="md"
                  className="text-center"
                />
              </div>
            {:else}
              <div class="text-sm text-base-content/40 italic">
                {t('detections.weather.noData')}
              </div>
            {/if}
          </div>

          <!-- Section 4: Confidence + Actions (fixed width) -->
          <div class="flex-shrink-0 flex flex-col items-center" style:min-width="120px">
            <div class="text-sm text-base-content/60 mb-2">
              {t('common.labels.confidence')}
            </div>
            <ConfidenceCircle confidence={detection.confidence} size="xl" />

            <!-- Actions below confidence -->
            <div class="flex flex-col gap-2 mt-4">
              {#if detection.clipName}
                <a
                  href="/api/v2/media/audio/{detection.clipName}"
                  download
                  class="btn btn-ghost btn-sm gap-2"
                >
                  {@html mediaIcons.download}
                  {t('common.actions.download')}
                </a>
              {/if}
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
{/snippet}

{#snippet overviewTab(detection: Detection)}
  <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
    <!-- Environmental Conditions -->
    <div>
      <h3 class="text-lg font-semibold mb-4">{t('detections.environmental.title')}</h3>
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
        <p class="text-base-content/60 italic">{t('detections.weather.noData')}</p>
      {/if}
    </div>

    <!-- Detection Metadata -->
    <div>
      <h3 class="text-lg font-semibold mb-4">{t('detections.metadata.title')}</h3>
      <div class="space-y-2">
        <div class="flex justify-between">
          <span class="text-base-content/60">{t('detections.metadata.source')}:</span>
          <span>{detection.source || 'Unknown'}</span>
        </div>
        <div class="flex justify-between">
          <span class="text-base-content/60">{t('detections.metadata.duration')}:</span>
          <span>{parseFloat(detection.endTime) - parseFloat(detection.beginTime)}s</span>
        </div>
        {#if detection.verified !== 'unverified'}
          <div class="flex justify-between">
            <span class="text-base-content/60">{t('detections.metadata.status')}:</span>
            <span class="capitalize">{detection.verified}</span>
          </div>
        {/if}
        {#if detection.locked}
          <div class="flex justify-between">
            <span class="text-base-content/60">{t('detections.metadata.locked')}:</span>
            <span>{t('common.values.yes')}</span>
          </div>
        {/if}
      </div>
    </div>

    <!-- Species Rarity (if available) -->
    {#if speciesInfo?.rarity}
      <div>
        <h3 class="text-lg font-semibold mb-4">{t('species.rarity.title')}</h3>
        <div class="bg-base-200 rounded-lg p-4">
          <div class="flex items-center justify-between mb-2">
            <span class="text-lg font-medium capitalize">{speciesInfo.rarity.status}</span>
            <span class="text-sm text-base-content/60">
              Score: {(speciesInfo.rarity.score * 100).toFixed(1)}%
            </span>
          </div>
          {#if speciesInfo.rarity.location_based}
            <p class="text-sm text-base-content/60">
              Based on location: {speciesInfo.rarity.latitude.toFixed(2)}, {speciesInfo.rarity.longitude.toFixed(
                2
              )}
            </p>
          {/if}
        </div>
      </div>
    {/if}
  </div>
{/snippet}

{#snippet taxonomyTab()}
  <div>
    {#if isLoadingTaxonomy}
      <LoadingSpinner size="md" />
    {:else if taxonomyInfo?.taxonomy}
      <h3 class="text-lg font-semibold mb-4">{t('species.taxonomy.hierarchy')}</h3>
      <div class="bg-base-200 rounded-lg p-6">
        <!-- Visual Family Tree -->
        <div class="space-y-3">
          <div class="flex items-center gap-3">
            <span class="text-sm text-base-content/60 w-24">Kingdom:</span>
            <span class="font-medium">{taxonomyInfo.taxonomy.kingdom}</span>
          </div>
          <div class="flex items-center gap-3 ml-6">
            <span class="text-sm text-base-content/60 w-24">Phylum:</span>
            <span class="font-medium">{taxonomyInfo.taxonomy.phylum}</span>
          </div>
          <div class="flex items-center gap-3 ml-12">
            <span class="text-sm text-base-content/60 w-24">Class:</span>
            <span class="font-medium">{taxonomyInfo.taxonomy.class}</span>
          </div>
          <div class="flex items-center gap-3 ml-18">
            <span class="text-sm text-base-content/60 w-24">Order:</span>
            <span class="font-medium">{taxonomyInfo.taxonomy.order}</span>
          </div>
          <div class="flex items-center gap-3 ml-24">
            <span class="text-sm text-base-content/60 w-24">Family:</span>
            <span class="font-medium">
              {taxonomyInfo.taxonomy.family}
              {#if taxonomyInfo.taxonomy.family_common}
                <span class="text-base-content/60"> ({taxonomyInfo.taxonomy.family_common})</span>
              {/if}
            </span>
          </div>
          <div class="flex items-center gap-3 ml-30">
            <span class="text-sm text-base-content/60 w-24">Genus:</span>
            <span class="font-medium">{taxonomyInfo.taxonomy.genus}</span>
          </div>
          <div class="flex items-center gap-3 ml-36">
            <span class="text-sm text-base-content/60 w-24">Species:</span>
            <span class="font-medium italic">{taxonomyInfo.taxonomy.species}</span>
          </div>
        </div>
      </div>

      {#if taxonomyInfo.subspecies?.length > 0}
        <h3 class="text-lg font-semibold mt-6 mb-4">{t('species.taxonomy.subspecies')}</h3>
        <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
          {#each taxonomyInfo.subspecies as subspecies}
            <div class="bg-base-200 rounded-lg p-3">
              <p class="font-medium italic">{subspecies.scientific_name}</p>
              {#if subspecies.common_name}
                <p class="text-sm text-base-content/60">{subspecies.common_name}</p>
              {/if}
            </div>
          {/each}
        </div>
      {/if}
    {:else}
      <p class="text-base-content/60 italic">{t('species.taxonomy.noData')}</p>
    {/if}
  </div>
{/snippet}

{#snippet historyTab()}
  <div>
    <h3 class="text-lg font-semibold mb-4">{t('detections.history.title')}</h3>
    <p class="text-base-content/60 italic">{t('detections.history.comingSoon')}</p>
  </div>
{/snippet}

{#snippet notesTab(detection: Detection)}
  <div>
    <h3 class="text-lg font-semibold mb-4">{t('detections.notes.title')}</h3>
    {#if detection.comments && detection.comments.length > 0}
      <div class="space-y-3">
        {#each detection.comments as comment}
          <div class="bg-base-200 rounded-lg p-4">
            <p>{comment.entry}</p>
            <p class="text-sm text-base-content/60 mt-2">
              {new Date(comment.createdAt).toLocaleString()}
            </p>
          </div>
        {/each}
      </div>
    {:else}
      <p class="text-base-content/60 italic">{t('detections.notes.noComments')}</p>
    {/if}
  </div>
{/snippet}

<!-- Main component -->
<div class="col-span-12 space-y-4">
  {#if isLoadingDetection}
    <div class="card bg-base-100 shadow-sm">
      <div class="card-body">
        <div class="flex justify-center items-center h-64">
          <LoadingSpinner size="lg" />
        </div>
      </div>
    </div>
  {:else if detectionError}
    <div class="card bg-base-100 shadow-sm">
      <div class="card-body">
        <ErrorAlert message={detectionError} />
      </div>
    </div>
  {:else if detection}
    <!-- Hero Section -->
    {@render heroSection(detection)}

    <!-- Media Section -->
    <div class="card bg-base-100 shadow-sm">
      <div class="card-body">
        <h2 class="text-xl font-semibold mb-4">{t('detections.media.title')}</h2>
        <AudioPlayer
          audioUrl="/api/v2/audio/{detection.id}"
          detectionId={detection.id.toString()}
          showSpectrogram={true}
          spectrogramSize="xl"
          spectrogramRaw={false}
          responsive={true}
          className="w-full"
        />
      </div>
    </div>

    <!-- Tabbed Content -->
    <div class="card bg-base-100 shadow-sm">
      <div class="card-body">
        <!-- Tab Navigation -->
        <div class="tabs tabs-boxed mb-6">
          <button
            class="tab"
            class:tab-active={activeTab === 'overview'}
            onclick={() => (activeTab = 'overview')}
          >
            {t('detections.tabs.overview')}
          </button>
          <button
            class="tab"
            class:tab-active={activeTab === 'taxonomy'}
            onclick={() => (activeTab = 'taxonomy')}
          >
            {t('detections.tabs.taxonomy')}
          </button>
          <button
            class="tab"
            class:tab-active={activeTab === 'history'}
            onclick={() => (activeTab = 'history')}
          >
            {t('detections.tabs.history')}
          </button>
          <button
            class="tab"
            class:tab-active={activeTab === 'notes'}
            onclick={() => (activeTab = 'notes')}
          >
            {t('detections.tabs.notes')}
          </button>
          {#if canReview}
            <button
              class="tab"
              class:tab-active={activeTab === 'review'}
              onclick={() => (activeTab = 'review')}
            >
              {t('common.actions.review')}
            </button>
          {/if}
        </div>

        <!-- Tab Content -->
        <div>
          {#if activeTab === 'overview'}
            {@render overviewTab(detection)}
          {:else if activeTab === 'taxonomy'}
            {@render taxonomyTab()}
          {:else if activeTab === 'history'}
            {@render historyTab()}
          {:else if activeTab === 'notes'}
            {@render notesTab(detection)}
          {:else if activeTab === 'review' && canReview && ReviewCard}
            <ReviewCard {detection} onSaveComplete={handleReviewComplete} />
          {/if}
        </div>
      </div>
    </div>
  {/if}
</div>
