<!--
  DetectionDetail.svelte - Single Detection View Component

  Purpose: Display comprehensive details for a single bird detection

  Features:
  - Editorial hero section with large species thumbnail and display typography
  - Horizontal metadata stat bar (confidence, date/time, weather, download)
  - Audio player with large spectrogram visualization
  - Tabbed content: overview, taxonomy, history, notes, review
  - Weather and environmental context
  - Species rarity and taxonomy information

  Props:
  - detectionId: string - The ID of the detection to display
-->
<script lang="ts">
  import ConfidenceCircle from '$lib/desktop/components/data/ConfidenceCircle.svelte';
  import WeatherDetails from '$lib/desktop/components/data/WeatherDetails.svelte';
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import SpeciesBadges from '$lib/desktop/components/modals/SpeciesBadges.svelte';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils.js';
  import { t } from '$lib/i18n';
  import type { Detection, ImageAttribution } from '$lib/types/detection.types';
  import { hasReviewPermission } from '$lib/utils/auth';
  import { formatLocalDateTime } from '$lib/utils/date';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { loggers } from '$lib/utils/logger';
  import { Download, Calendar, Clock, Sun, Moon, Sunrise, Sunset } from '@lucide/svelte';

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
    [key: string]: unknown;
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
  type TabType = 'overview' | 'history' | 'notes' | 'review';

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
  let imageAttribution = $state<ImageAttribution | null>(null);

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
  let attributionController: AbortController | null = null;

  // Validate detection ID to prevent path traversal attacks
  // Only allow alphanumeric characters, hyphens, and underscores
  function isValidDetectionId(id: string): boolean {
    return /^[a-zA-Z0-9_-]+$/.test(id);
  }

  // Resolve detection ID from URL if not provided via prop
  $effect(() => {
    if (!detectionIdProp) {
      const pathParts = window.location.pathname.split('/');
      const detectionIndex = pathParts.indexOf('detections');
      if (detectionIndex !== -1 && pathParts[detectionIndex + 1]) {
        const candidateId = pathParts[detectionIndex + 1];
        if (isValidDetectionId(candidateId)) {
          resolvedDetectionId = candidateId;
        } else {
          detectionError = t('detections.errors.noIdProvided');
        }
      }
    } else if (isValidDetectionId(detectionIdProp)) {
      resolvedDetectionId = detectionIdProp;
    } else {
      detectionError = t('detections.errors.noIdProvided');
    }
  });

  // Initialize tab from URL query parameter (with permission check for review tab)
  $effect(() => {
    const urlParams = new URLSearchParams(window.location.search);
    const tabParam = urlParams.get('tab');
    const validTabs: TabType[] = ['overview', 'history', 'notes', 'review'];
    if (tabParam && validTabs.includes(tabParam as TabType)) {
      activeTab = tabParam === 'review' && !canReview ? 'overview' : (tabParam as TabType);
    }
  });

  // Fetch detection data when resolvedDetectionId changes
  $effect(() => {
    if (resolvedDetectionId) {
      fetchDetection();
    }

    return () => {
      detectionController?.abort();
      speciesController?.abort();
      taxonomyController?.abort();
      attributionController?.abort();
    };
  });

  // Fetch detection data
  async function fetchDetection() {
    if (!resolvedDetectionId) {
      detectionError = t('detections.errors.noIdProvided');
      isLoadingDetection = false;
      return;
    }

    detectionController?.abort();
    detectionController = new AbortController();

    isLoadingDetection = true;
    detectionError = null;

    try {
      const response = await fetch(buildAppUrl(`/api/v2/detections/${resolvedDetectionId}`), {
        signal: detectionController.signal,
      });

      if (detectionController.signal.aborted) return;

      if (response.ok) {
        const data = (await response.json()) as Detection;
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

      if (detection) {
        fetchSpeciesInfo();
        fetchTaxonomy();
        fetchImageAttribution();
      }
    } catch (error) {
      if (error instanceof Error && error.name === 'AbortError') {
        return;
      }
      detectionError =
        error instanceof Error ? error.message : t('detections.errors.loadFailed', { status: '' });
      logger.error('Error fetching detection:', error);
    } finally {
      isLoadingDetection = false;
    }
  }

  // Fetch image attribution metadata
  async function fetchImageAttribution() {
    if (!detection?.scientificName) return;

    attributionController?.abort();
    attributionController = new AbortController();

    try {
      const url = buildAppUrl(
        `/api/v2/media/species-image/info?name=${encodeURIComponent(detection.scientificName)}`
      );
      const response = await fetch(url, { signal: attributionController.signal });
      if (attributionController.signal.aborted) return;
      if (response.ok) {
        const data = await response.json();
        if (attributionController.signal.aborted) return;
        imageAttribution = data as ImageAttribution;
      }
    } catch (error) {
      if (error instanceof Error && error.name === 'AbortError') return;
      // Attribution is non-critical — fail silently
    } finally {
      attributionController = null;
    }
  }

  // Fetch species information (public data - no auth required)
  async function fetchSpeciesInfo() {
    if (!detection?.scientificName) return;

    speciesController?.abort();
    speciesController = new AbortController();

    try {
      const response = await fetch(
        buildAppUrl(
          `/api/v2/species?scientific_name=${encodeURIComponent(detection.scientificName)}`
        ),
        { signal: speciesController.signal }
      );
      if (speciesController.signal.aborted) return;
      if (response.ok) {
        const data = await response.json();
        if (speciesController.signal.aborted) return;
        speciesInfo = data;
      }
    } catch (error) {
      if (error instanceof Error && error.name === 'AbortError') {
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

    taxonomyController?.abort();
    taxonomyController = new AbortController();

    isLoadingTaxonomy = true;
    try {
      const response = await fetch(
        buildAppUrl(
          `/api/v2/species/taxonomy?scientific_name=${encodeURIComponent(detection.scientificName)}`
        ),
        { signal: taxonomyController.signal }
      );
      if (taxonomyController.signal.aborted) return;
      if (response.ok) {
        const data = await response.json();
        if (taxonomyController.signal.aborted) return;
        taxonomyInfo = data;
      }
    } catch (error) {
      if (error instanceof Error && error.name === 'AbortError') {
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
    if (resolvedDetectionId) {
      fetchDetection();
    }
  }

  // Keyboard navigation handler for tab buttons
  function handleTabKeydown(e: KeyboardEvent) {
    const tabs: TabType[] = ['overview', 'history', 'notes'];
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
    const activePanel = document.getElementById(`tab-panel-${activeTab}`);
    let timeoutId: ReturnType<typeof setTimeout> | null = null;

    if (activePanel && document.activeElement?.getAttribute('role') === 'tab') {
      timeoutId = setTimeout(() => {
        activePanel.focus();
      }, TAB_FOCUS_DELAY_MS);
    }

    return () => {
      if (timeoutId) clearTimeout(timeoutId);
    };
  });

  // URL state management - update URL when tab changes
  $effect(() => {
    if (typeof window !== 'undefined' && resolvedDetectionId) {
      const url = new URL(window.location.href);

      if (activeTab === 'overview') {
        url.searchParams.delete('tab');
      } else {
        url.searchParams.set('tab', activeTab);
      }

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
        const urlParams = new URLSearchParams(window.location.search);
        const tabParam = urlParams.get('tab');
        const validTabs = ['overview', 'history', 'notes', 'review'] as const;

        if (tabParam && validTabs.includes(tabParam as typeof activeTab)) {
          if (tabParam === 'review' && !canReview) {
            activeTab = 'overview';
          } else {
            activeTab = tabParam as typeof activeTab;
          }
        } else {
          activeTab = 'overview';
        }
      }

      window.addEventListener('popstate', handlePopState);

      return () => {
        window.removeEventListener('popstate', handlePopState);
      };
    }
  });
</script>

<!-- Snippets for better organization -->

{#snippet heroSection(det: Detection)}
  <section class="detection-hero" aria-labelledby="species-heading">
    <!-- Row 1: Thumbnail + Species + Confidence/DateTime -->
    <div class="hero-row">
      <!-- Species thumbnail with credit overlay -->
      <div class="hero-thumbnail">
        <img
          src="/api/v2/media/species-image?name={encodeURIComponent(det.scientificName)}"
          alt={det.commonName}
          class="w-full h-full object-contain"
          onerror={handleBirdImageError}
          loading="eager"
        />
        {#if imageAttribution?.authorName}
          <div class="thumbnail-credit" aria-label="Image credit: {imageAttribution.authorName}">
            <span class="credit-text">📷 {imageAttribution.authorName}</span>
            {#if imageAttribution.licenseName}
              <span class="credit-separator">·</span>
              {#if imageAttribution.licenseURL}
                <a
                  href={imageAttribution.licenseURL}
                  target="_blank"
                  rel="noopener noreferrer"
                  class="credit-license">{imageAttribution.licenseName}</a
                >
              {:else}
                <span class="credit-license">{imageAttribution.licenseName}</span>
              {/if}
            {/if}
          </div>
        {/if}
      </div>

      <!-- Species identity -->
      <div class="hero-species">
        <h1 id="species-heading" class="species-display-name">
          {det.commonName}
          <span class="sr-only">detection details</span>
        </h1>
        <p class="species-scientific-name" aria-label="Scientific name">
          {det.scientificName}
        </p>
        <div class="mt-3" aria-label="Species classification badges">
          <SpeciesBadges detection={det} size="sm" />
        </div>
      </div>

      <!-- Right side: Confidence + Date/Time -->
      <div class="hero-meta" role="region" aria-label="Detection metadata">
        <!-- Confidence -->
        <div class="hero-meta-block" aria-label="Detection confidence {det.confidence}%">
          <ConfidenceCircle confidence={det.confidence} size="xl" />
        </div>

        <!-- Vertical divider -->
        <div class="hero-meta-divider" aria-hidden="true"></div>

        <!-- Date & Time -->
        <div class="hero-meta-block">
          <span class="stat-value flex items-center gap-1.5">
            <Calendar class="w-3.5 h-3.5 opacity-50" />
            {det.date}
          </span>
          <span class="stat-value flex items-center gap-1.5 mt-0.5">
            <Clock class="w-3.5 h-3.5 opacity-50" />
            {det.time}
          </span>
          {#if det.timeOfDay}
            {@const todColors = {
              day: { bg: 'oklch(0.92 0.08 90)', fg: 'oklch(0.45 0.12 75)' },
              night: { bg: 'oklch(0.3 0.06 270)', fg: 'oklch(0.8 0.08 270)' },
              sunrise: { bg: 'oklch(0.92 0.1 60)', fg: 'oklch(0.48 0.14 45)' },
              sunset: { bg: 'oklch(0.9 0.1 30)', fg: 'oklch(0.45 0.14 25)' },
            }}
            {@const colors = todColors[det.timeOfDay as keyof typeof todColors] ?? todColors.day}
            <span
              class="time-of-day-badge"
              style:background-color={colors.bg}
              style:color={colors.fg}
            >
              {#if det.timeOfDay === 'day'}
                <Sun size={12} />
              {:else if det.timeOfDay === 'night'}
                <Moon size={12} />
              {:else if det.timeOfDay === 'sunrise'}
                <Sunrise size={12} />
              {:else if det.timeOfDay === 'sunset'}
                <Sunset size={12} />
              {/if}
              <span>{t(`detections.timeOfDay.${det.timeOfDay}`)}</span>
            </span>
          {/if}
        </div>

        <!-- Weather column -->
        {#if det.weather}
          <div class="hero-meta-divider" aria-hidden="true"></div>
          <div
            class="hero-meta-block hero-weather"
            aria-label="Weather conditions at time of detection"
          >
            <WeatherDetails
              weatherIcon={det.weather.weatherIcon}
              weatherDescription={det.weather.description}
              temperature={det.weather.temperature}
              windSpeed={det.weather.windSpeed}
              windGust={det.weather.windGust}
              units={det.weather.units}
              size="md"
            />
          </div>
        {/if}

        <!-- Download -->
        {#if det.clipName}
          <div class="hero-meta-divider" aria-hidden="true"></div>
          <a
            href={buildAppUrl(`/api/v2/media/audio/${det.clipName}`)}
            download
            class="hero-download-btn"
            aria-label="Download audio clip for {det.commonName} detection"
          >
            <Download class="w-4 h-4" />
          </a>
        {/if}
      </div>
    </div>
  </section>
{/snippet}

{#snippet overviewTab(det: Detection)}
  <div class="grid grid-cols-1 md:grid-cols-2 gap-8">
    <!-- Species Rarity -->
    {#if speciesInfo?.rarity}
      <section aria-labelledby="rarity-heading">
        <h3 id="rarity-heading" class="section-heading">{t('species.rarity.title')}</h3>
        <div class="content-panel">
          <div class="flex items-center gap-3 mb-1">
            <span class="rarity-label">{speciesInfo.rarity.status.replace(/_/g, ' ')}</span>
            <span class="rarity-score">
              {(speciesInfo.rarity.score * 100).toFixed(0)}%
            </span>
          </div>
          {#if speciesInfo.rarity.location_based}
            <p class="text-xs text-base-content/40">
              {t('species.rarity.basedOnLocation', {
                latitude: speciesInfo.rarity.latitude.toFixed(2),
                longitude: speciesInfo.rarity.longitude.toFixed(2),
              })}
            </p>
          {/if}
        </div>
      </section>
    {/if}

    <!-- Species Tracking -->
    {#if det.isNewSpecies || det.isNewThisYear || det.isNewThisSeason || (det.daysSinceFirstSeen != null && det.daysSinceFirstSeen > 0)}
      <section aria-labelledby="tracking-heading">
        <h3 id="tracking-heading" class="section-heading">{t('species.tracking.title')}</h3>
        <div class="content-panel">
          <dl class="metadata-list">
            {#if det.isNewSpecies}
              <div class="metadata-row">
                <dt>{t('species.tracking.newSpecies')}</dt>
                <dd class="text-success font-medium">{t('common.values.yes')}</dd>
              </div>
            {/if}
            {#if det.isNewThisYear}
              <div class="metadata-row">
                <dt>{t('species.tracking.newThisYear')}</dt>
                <dd class="text-success font-medium">{t('common.values.yes')}</dd>
              </div>
            {/if}
            {#if det.isNewThisSeason && det.currentSeason}
              <div class="metadata-row">
                <dt>{t('species.tracking.newThisSeason')}</dt>
                <dd class="capitalize">{det.currentSeason}</dd>
              </div>
            {/if}
            {#if det.daysSinceFirstSeen != null && det.daysSinceFirstSeen > 0}
              <div class="metadata-row">
                <dt>{t('species.tracking.daysSinceFirst')}</dt>
                <dd>{det.daysSinceFirstSeen}</dd>
              </div>
            {/if}
          </dl>
        </div>
      </section>
    {/if}

    <!-- Taxonomy -->
    {#if isLoadingTaxonomy}
      <section aria-labelledby="taxonomy-heading" class="md:col-span-2">
        <h3 id="taxonomy-heading" class="section-heading">
          {t('species.taxonomy.hierarchy')}
        </h3>
        <div class="content-panel">
          <div class="animate-pulse space-y-3">
            {#each Array(7) as _, i (i)}
              <div class="flex items-center gap-2">
                <div class="h-3 rounded bg-base-300/60 w-14"></div>
                <div class="h-3 rounded bg-base-300/60 w-24"></div>
              </div>
            {/each}
          </div>
        </div>
      </section>
    {:else if taxonomyInfo?.taxonomy}
      <section aria-labelledby="taxonomy-heading" class="md:col-span-2">
        <h3 id="taxonomy-heading" class="section-heading">
          {t('species.taxonomy.hierarchy')}
        </h3>
        <div class="content-panel taxonomy-panel">
          <div class="taxonomy-tree">
            {#each [{ key: 'kingdom', value: taxonomyInfo.taxonomy.kingdom, depth: 0 }, { key: 'phylum', value: taxonomyInfo.taxonomy.phylum, depth: 1 }, { key: 'class', value: taxonomyInfo.taxonomy.class, depth: 2 }, { key: 'order', value: taxonomyInfo.taxonomy.order, depth: 3 }, { key: 'family', value: taxonomyInfo.taxonomy.family, depth: 4, extra: taxonomyInfo.taxonomy.family_common }, { key: 'genus', value: taxonomyInfo.taxonomy.genus, depth: 5 }, { key: 'species', value: taxonomyInfo.taxonomy.species, depth: 6 }] as rank, i (rank.key)}
              <div class="taxonomy-node" style:--depth={rank.depth}>
                {#if i > 0}
                  <div class="taxonomy-connector" aria-hidden="true">
                    <div class="connector-vert"></div>
                    <div class="connector-horiz"></div>
                  </div>
                {/if}
                <span class="taxonomy-rank-label">
                  {t(`species.taxonomy.labels.${rank.key}`)}
                </span>
                <span class="taxonomy-rank-value" class:italic={rank.key === 'species'}>
                  {rank.value}
                  {#if rank.extra}
                    <span class="taxonomy-family-common">({rank.extra})</span>
                  {/if}
                </span>
              </div>
            {/each}
          </div>

          {#if subspeciesList.length > 0}
            <div class="taxonomy-subspecies">
              <h4 class="taxonomy-subspecies-heading">{t('species.taxonomy.subspecies')}</h4>
              <div class="taxonomy-subspecies-list">
                {#each subspeciesList as subspecies (subspecies.scientific_name)}
                  <div class="taxonomy-subspecies-item">
                    <span class="italic">{subspecies.scientific_name}</span>
                    {#if subspecies.common_name}
                      <span class="taxonomy-subspecies-common">{subspecies.common_name}</span>
                    {/if}
                  </div>
                {/each}
              </div>
            </div>
          {/if}
        </div>
      </section>
    {/if}
  </div>
{/snippet}

{#snippet historyTab()}
  <section aria-labelledby="history-heading">
    <h3 id="history-heading" class="section-heading">{t('detections.history.title')}</h3>
    <p class="text-sm text-base-content/50 italic" role="status">
      {t('detections.history.comingSoon')}
    </p>
  </section>
{/snippet}

{#snippet notesTab(det: Detection)}
  <section aria-labelledby="notes-heading">
    <h3 id="notes-heading" class="section-heading">{t('detections.notes.title')}</h3>
    {#if det.comments && det.comments.length > 0}
      <div class="space-y-3" role="list" aria-label="Detection comments">
        {#each det.comments as comment (comment.id ?? comment.createdAt)}
          <article class="content-panel" role="listitem">
            <p class="text-sm leading-relaxed" aria-label="Comment text">{comment.entry}</p>
            <p class="text-xs text-base-content/40 mt-2" aria-label="Comment timestamp">
              {formatLocalDateTime(new Date(comment.createdAt))}
            </p>
          </article>
        {/each}
      </div>
    {:else}
      <p class="text-sm text-base-content/50 italic" role="status">
        {t('detections.notes.noComments')}
      </p>
    {/if}
  </section>
{/snippet}

<!-- Main component -->
<main class="col-span-12 detection-detail" aria-label="Detection details">
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
    <!-- Loading skeleton -->
    <div class="surface-card p-8">
      <div class="flex justify-center items-center h-64" aria-label="Loading detection details">
        <LoadingSpinner size="lg" />
      </div>
    </div>
  {:else if detectionError}
    <!-- Error state -->
    <div class="surface-card p-6">
      <div role="alert" aria-live="assertive">
        <ErrorAlert message={detectionError} />
      </div>
    </div>
  {:else if detection}
    <!-- Hero Section -->
    {@render heroSection(detection)}

    <!-- Media Section -->
    <section class="surface-card" aria-labelledby="media-heading">
      <div class="p-5 md:p-6">
        <h2 id="media-heading" class="section-heading mb-4">{t('detections.media.title')}</h2>
        <div role="region" aria-label="Audio recording and spectrogram for {detection.commonName}">
          <div class="detail-audio-container">
            <AudioPlayer
              audioUrl={buildAppUrl(`/api/v2/audio/${detection.id}`)}
              detectionId={detection.id.toString()}
              showSpectrogram={true}
              spectrogramSize="lg"
              spectrogramRaw={false}
              responsive={true}
              className="w-full"
            />
          </div>
        </div>
      </div>
    </section>

    <!-- Tabbed Content -->
    <section class="surface-card" aria-labelledby="tabs-heading">
      <div class="p-5 md:p-6">
        <h2 id="tabs-heading" class="sr-only">Detection information tabs</h2>

        <!-- Tab Navigation -->
        <div class="tab-nav" role="tablist" aria-label="Detection details tabs">
          {#each ['overview', 'history', 'notes'] as tab (tab)}
            <button
              id="tab-{tab}"
              role="tab"
              class="tab-button"
              class:tab-active={activeTab === tab}
              aria-selected={activeTab === tab}
              aria-controls="tab-panel-{tab}"
              tabindex={activeTab === tab ? 0 : -1}
              onclick={() => (activeTab = tab as TabType)}
              onkeydown={handleTabKeydown}
            >
              {t(`detections.tabs.${tab}`)}
            </button>
          {/each}
          {#if canReview}
            <button
              id="tab-review"
              role="tab"
              class="tab-button"
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
        <div class="mt-6">
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
      </div>
    </section>
  {/if}
</main>

<style>
  /* ===========================================
     DETECTION DETAIL - Editorial Design System
     =========================================== */

  .detection-detail {
    display: flex;
    flex-direction: column;
    gap: 1rem;
    max-width: 960px;
    margin: 0 auto;
    width: 100%;
  }

  /* ----- Surface card (replaces DaisyUI card) ----- */
  .surface-card {
    background-color: var(--color-base-100);
    border-radius: var(--radius-box);
    border: 1px solid var(--border-100);
  }

  /* ----- Hero Section ----- */
  .detection-hero {
    background-color: var(--color-base-100);
    border-radius: var(--radius-box);
    border: 1px solid var(--border-100);
    padding: 1.5rem;
  }

  @media (min-width: 768px) {
    .detection-hero {
      padding: 2rem;
    }
  }

  /* ----- Hero Row: Thumbnail + Species + Meta ----- */
  .hero-row {
    display: flex;
    flex-direction: column;
    gap: 1.25rem;
  }

  @media (min-width: 768px) {
    .hero-row {
      flex-direction: row;
      align-items: flex-start;
      gap: 1.5rem;
    }
  }

  /* Species thumbnail — 4:3 to match avicommons 320×240 source images */
  .hero-thumbnail {
    position: relative;
    width: 100%;
    aspect-ratio: 4 / 3;
    max-height: 140px;
    border-radius: 0.75rem;
    overflow: hidden;
    background: linear-gradient(135deg, var(--color-base-200), var(--color-base-300));
    box-shadow: var(--shadow-md);
    flex-shrink: 0;
  }

  @media (min-width: 768px) {
    .hero-thumbnail {
      width: 180px;
      min-width: 180px;
      max-height: none;
    }
  }

  /* Photo credit — bottom-right corner of thumbnail */
  .thumbnail-credit {
    position: absolute;
    right: 0;
    bottom: 0;
    display: flex;
    align-items: center;
    gap: 0.2rem;
    padding: 0.2rem 0.4rem;
    background: oklch(10% 0 0deg / 0.55);
    border-top-left-radius: 0.375rem;
  }

  .credit-text {
    font-size: 0.5625rem;
    color: oklch(95% 0 0deg);
    line-height: 1;
    white-space: nowrap;
  }

  .credit-separator {
    font-size: 0.5625rem;
    color: oklch(70% 0 0deg);
    flex-shrink: 0;
    line-height: 1;
  }

  .credit-license {
    font-size: 0.5625rem;
    color: oklch(80% 0 0deg);
    text-decoration: none;
    line-height: 1;
    flex-shrink: 0;
    white-space: nowrap;
  }

  a.credit-license:hover {
    color: white;
    text-decoration: underline;
  }

  /* Species identity - takes available space */
  .hero-species {
    flex: 1;
    min-width: 0;
    padding-top: 0.125rem;
  }

  /* Species display name - refined Inter typography */
  .species-display-name {
    font-family: 'Inter Variable', Inter, ui-sans-serif, sans-serif;
    font-size: 1.375rem;
    font-weight: 650;
    line-height: 1.2;
    letter-spacing: -0.025em;
    color: var(--color-base-content);
    margin: 0;
  }

  @media (min-width: 768px) {
    .species-display-name {
      font-size: 1.5rem;
    }
  }

  .species-scientific-name {
    font-style: italic;
    font-size: 0.875rem;
    font-weight: 400;
    color: var(--color-base-content);
    opacity: 0.5;
    margin-top: 0.125rem;
    letter-spacing: 0.01em;
  }

  /* Right-side metadata (confidence + date/time) */
  .hero-meta {
    display: flex;
    align-items: center;
    gap: 1rem;
    flex-shrink: 0;
  }

  @media (max-width: 767px) {
    .hero-meta {
      border-top: 1px solid var(--border-100);
      padding-top: 1rem;
    }
  }

  @media (min-width: 768px) {
    .hero-meta {
      border-left: 1px solid var(--border-100);
      padding-left: 1.5rem;
      margin-left: auto;
    }
  }

  .hero-meta-block {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
  }

  .hero-meta-divider {
    width: 1px;
    align-self: stretch;
    background-color: var(--border-100);
  }

  .stat-value {
    font-size: 0.875rem;
    font-weight: 500;
    color: var(--color-base-content);
    line-height: 1.4;
  }

  /* Time of day badge — colors applied via inline styles */
  .time-of-day-badge {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    font-size: 0.6875rem;
    font-weight: 550;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.2rem 0.5rem;
    border-radius: 9999px;
    margin-top: 0.5rem;
    line-height: 1;
  }

  /* Weather column in hero */
  .hero-weather :global(.wd-container) {
    flex-direction: column;
    align-items: flex-start;
    gap: 0.25rem;
  }

  .hero-weather :global(.wd-weather-row),
  .hero-weather :global(.wd-temperature-row),
  .hero-weather :global(.wd-wind-row) {
    gap: 0.375rem;
    font-size: 0.8125rem;
  }

  /* Download icon button in hero */
  .hero-download-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 2.25rem;
    height: 2.25rem;
    border-radius: var(--radius-field);
    color: var(--color-base-content);
    opacity: 0.55;
    transition:
      opacity 0.15s ease,
      background-color 0.15s ease;
    flex-shrink: 0;
  }

  .hero-download-btn:hover {
    opacity: 1;
    background-color: var(--color-base-200);
  }

  /* ----- Section Heading ----- */
  .section-heading {
    font-size: 0.8125rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--color-base-content);
    opacity: 0.65;
    margin-bottom: 0.75rem;
  }

  /* ----- Content Panel (inner blocks) ----- */
  .content-panel {
    background-color: var(--color-base-200);
    border-radius: var(--radius-field);
    padding: 1rem;
  }

  /* ----- Metadata list (key-value pairs) ----- */
  .metadata-list {
    display: flex;
    flex-direction: column;
    gap: 0.625rem;
  }

  .metadata-row {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    font-size: 0.875rem;
  }

  .metadata-row dt {
    color: var(--color-base-content);
    opacity: 0.5;
  }

  .metadata-row dd {
    font-weight: 500;
    color: var(--color-base-content);
  }

  /* ----- Rarity display ----- */
  .rarity-label {
    font-size: 0.9375rem;
    font-weight: 600;
    color: var(--color-base-content);
    text-transform: capitalize;
  }

  .rarity-score {
    font-size: 0.75rem;
    font-weight: 500;
    color: var(--color-base-content);
    opacity: 0.4;
  }

  /* ----- Taxonomy tree with connector lines ----- */
  .taxonomy-panel {
    padding: 1.25rem 1.5rem;
  }

  .taxonomy-tree {
    display: flex;
    flex-direction: column;
    gap: 0;
  }

  .taxonomy-node {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding-left: calc(var(--depth) * 1.25rem);
    min-height: 2rem;
    position: relative;
  }

  /* Connector lines */
  .taxonomy-connector {
    position: absolute;
    left: calc(var(--depth) * 1.25rem - 0.75rem);
    top: 0;
    width: 0.75rem;
    height: 100%;
    pointer-events: none;
  }

  .connector-vert {
    position: absolute;
    left: 0;
    top: -0.25rem;
    width: 1px;
    height: calc(50% + 0.25rem);
    background-color: var(--border-200);
  }

  .connector-horiz {
    position: absolute;
    left: 0;
    top: 50%;
    width: 100%;
    height: 1px;
    background-color: var(--border-200);
  }

  .taxonomy-rank-label {
    font-size: 0.6875rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--color-base-content);
    opacity: 0.35;
    flex-shrink: 0;
    min-width: 3.5rem;
  }

  .taxonomy-rank-value {
    font-size: 0.875rem;
    font-weight: 500;
    color: var(--color-base-content);
  }

  .taxonomy-family-common {
    color: var(--color-base-content);
    opacity: 0.45;
    font-style: normal;
  }

  /* Subspecies section */
  .taxonomy-subspecies {
    margin-top: 1rem;
    padding-top: 0.75rem;
    border-top: 1px solid var(--border-100);
  }

  .taxonomy-subspecies-heading {
    font-size: 0.6875rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--color-base-content);
    opacity: 0.35;
    margin-bottom: 0.5rem;
  }

  .taxonomy-subspecies-list {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
  }

  .taxonomy-subspecies-item {
    display: flex;
    align-items: baseline;
    gap: 0.375rem;
    font-size: 0.8125rem;
    color: var(--color-base-content);
    padding: 0.25rem 0.625rem;
    background-color: var(--color-base-100);
    border: 1px solid var(--border-100);
    border-radius: var(--radius-field);
  }

  .taxonomy-subspecies-common {
    font-size: 0.75rem;
    opacity: 0.5;
  }

  /* ----- Tab Navigation ----- */
  .tab-nav {
    display: flex;
    gap: 0;
    border-bottom: 1px solid var(--border-100);
    overflow-x: auto;
    -webkit-overflow-scrolling: touch;
  }

  .tab-button {
    position: relative;
    padding: 0.625rem 1rem;
    font-size: 0.8125rem;
    font-weight: 500;
    color: var(--color-base-content);
    opacity: 0.55;
    background: none;
    border: none;
    cursor: pointer;
    white-space: nowrap;
    transition:
      opacity 0.15s ease,
      color 0.15s ease;
    border-bottom: 2px solid transparent;
    margin-bottom: -1px;
  }

  .tab-button:hover {
    opacity: 0.85;
  }

  .tab-button:focus-visible {
    outline: 2px solid var(--color-primary);
    outline-offset: -2px;
    border-radius: var(--radius-selector);
  }

  .tab-button.tab-active {
    opacity: 1;
    color: var(--color-primary);
    border-bottom-color: var(--color-primary);
    font-weight: 600;
  }

  /* ----- Audio Container ----- */
  .detail-audio-container {
    position: relative;
    width: 100%;
    max-width: 1200px;
    margin: 0 auto;
    min-height: var(--spectrogram-min-height, 60px);
    aspect-ratio: var(--spectrogram-aspect-ratio, 2 / 1);
    background: linear-gradient(to bottom, rgb(128 128 128 / 0.04), rgb(128 128 128 / 0.01));
    border-radius: var(--radius-field);
    overflow: hidden;
  }

  .detail-audio-container :global(.group) {
    width: 100%;
    height: 100%;
    position: absolute;
    top: 0;
    left: 0;
  }

  .detail-audio-container > :global(div > .group) {
    width: 100%;
    height: 100%;
    position: absolute;
    top: 0;
    left: 0;
  }

  .detail-audio-container :global(img) {
    object-fit: contain;
    height: 100%;
    width: 100%;
  }

  .detail-audio-container :global(.group img),
  .detail-audio-container :global(div img) {
    object-fit: contain;
    height: 100%;
    width: 100%;
  }
</style>
