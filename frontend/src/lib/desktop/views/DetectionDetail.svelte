<!--
  DetectionDetail.svelte - Single Detection View

  Purpose: everything about one detection on a single page, without tabs.

  Layout, top to bottom:
  - navigation bar: breadcrumb, older/newer for all detections and for the species
  - identity and review beside the time and conditions card
  - recording and spectrogram, with the recording's facts and download
  - models and activity log beside species statistics

  The model's other predictions open in a dialog from the identity card; the same
  dialog is where a reviewer changes the species.

  The panels live in features/detections/components/detail/. This view loads the
  data and hands it to them.

  Props:
  - detectionId: string - The ID of the detection to display
-->
<script lang="ts">
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import SpectrogramImage from '$lib/desktop/components/media/SpectrogramImage.svelte';
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import ActivityLog from '$lib/desktop/features/detections/components/detail/ActivityLog.svelte';
  import ConditionsCard from '$lib/desktop/features/detections/components/detail/ConditionsCard.svelte';
  import DetailNavBar from '$lib/desktop/features/detections/components/detail/DetailNavBar.svelte';
  import IdentityCard from '$lib/desktop/features/detections/components/detail/IdentityCard.svelte';
  import ModelsCard from '$lib/desktop/features/detections/components/detail/ModelsCard.svelte';
  import RecordingHeader from '$lib/desktop/features/detections/components/detail/RecordingHeader.svelte';
  import ReviewPanel from '$lib/desktop/features/detections/components/detail/ReviewPanel.svelte';
  import SpeciesChangeDialog from '$lib/desktop/features/detections/components/detail/SpeciesChangeDialog.svelte';
  import SpeciesStatsCard from '$lib/desktop/features/detections/components/detail/SpeciesStatsCard.svelte';
  import {
    nearestSunEvent,
    wallClockMinutes,
  } from '$lib/desktop/features/detections/components/detail/detailTime';
  import type {
    DailyCounts,
    DetectionDetailData,
    DetectionNeighbors,
    DetectionPredictions,
    DetectionWeather,
    HourlyBucket,
    SpeciesRarity,
    SpeciesReviewCounts,
    SpeciesSummary,
    SunTimes,
    TaxonomyHierarchy,
  } from '$lib/desktop/features/detections/components/detail/detailTypes';
  import { loadSpeciesSummary } from '$lib/desktop/features/detections/components/detail/speciesSummary';
  import { t } from '$lib/i18n';
  import { navigation } from '$lib/stores/navigation.svelte';
  import type { ImageAttribution } from '$lib/types/detection.types';
  import { fetchWithCSRF } from '$lib/utils/api';
  import { hasReviewPermission, isAuthenticated } from '$lib/utils/auth';
  import { addDays, getLocalDateString } from '$lib/utils/date';
  import { loggers } from '$lib/utils/logger';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';
  import { buildAppUrl } from '$lib/utils/urlHelpers';

  interface SpeciesInfo {
    rarity?: SpeciesRarity;
  }

  interface TaxonomyInfo {
    taxonomy?: TaxonomyHierarchy;
  }

  const logger = loggers.ui;

  // Days of daily counts shown in the species statistics, ending on the detection's day.
  const DAILY_WINDOW_DAYS = 30;
  // Earliest date asked of the hour-of-day distribution: before any stored detection.
  const ACTIVITY_START_DATE = '2000-01-01';
  // Answer of the routes that need the current database format when it is not in use.
  const HTTP_CONFLICT = 409;

  interface Props {
    detectionId?: string;
  }

  const { detectionId: detectionIdProp }: Props = $props();

  // Resolved detection ID - set by the $effect below so prop changes are followed.
  let resolvedDetectionId = $state<string | undefined>(undefined);

  let canReview = $derived($hasReviewPermission);
  let clipExtractionEnabled = $derived($isAuthenticated);

  let detection = $state<DetectionDetailData | null>(null);
  let isLoadingDetection = $state(true);
  let detectionError = $state<string | null>(null);

  // Data loaded after the detection. Each is null until its request answers.
  let speciesInfo = $state<SpeciesInfo | null>(null);
  let taxonomyInfo = $state<TaxonomyInfo | null>(null);
  let imageAttribution = $state<ImageAttribution | null>(null);
  let weather = $state<DetectionWeather | null>(null);
  let sun = $state<SunTimes | null>(null);
  let neighbors = $state<DetectionNeighbors | null>(null);
  let predictions = $state<DetectionPredictions | null>(null);
  let hourlyActivity = $state<HourlyBucket[] | null>(null);
  let dailyCounts = $state<DailyCounts | null>(null);
  let speciesSummary = $state<SpeciesSummary | null>(null);
  let summaryLoading = $state(false);
  let reviewCounts = $state<SpeciesReviewCounts | null>(null);

  // Whether the server can step between detections. It cannot on the legacy
  // database format, where the navigation groups are left out.
  let stepping = $state<'unknown' | 'ready' | 'unavailable'>('unknown');
  // Whether the server runs the current database format; null until a route that
  // depends on it has answered. Kept across detections: it does not change.
  let enhancedDatabase = $state<boolean | null>(null);

  // AbortControllers: the detection itself, the data tied to that detection, and
  // the data tied to its species (kept while stepping within one species).
  let detectionController: AbortController | null = null;
  let relatedController: AbortController | null = null;
  let speciesController: AbortController | null = null;
  let attributionController: AbortController | null = null;

  let displayName = $derived(
    detection ? localizeSpeciesName(detection.scientificName, detection.commonName) : ''
  );

  let dailyStart = $derived(
    detection ? addDays(detection.date, -(DAILY_WINDOW_DAYS - 1)) : getLocalDateString()
  );

  // The species dialog: the model's other predictions, and the place to change the species.
  let speciesDialogOpen = $state(false);
  const DEFAULT_MODEL_VARIANT = 'default';

  // The detecting model's predictions; it is the one model that stores them.
  let predictionSet = $derived(predictions?.modelPredictions?.at(0) ?? null);
  let alternates = $derived(predictionSet?.predictions ?? []);
  let predictionModel = $derived.by(() => {
    if (!predictionSet) return '';
    const name = [predictionSet.modelName, predictionSet.modelVersion].filter(Boolean).join(' ');
    return predictionSet.modelVariant && predictionSet.modelVariant !== DEFAULT_MODEL_VARIANT
      ? `${name} (${predictionSet.modelVariant})`
      : name;
  });
  // How many other predictions there are; null while the request is out. A legacy
  // database stores none.
  let predictionCount = $derived.by(() => {
    if (predictions) return alternates.length;
    return enhancedDatabase === false ? 0 : null;
  });

  let canChangeSpecies = $derived(
    canReview && detection !== null && !detection.locked && enhancedDatabase !== false
  );
  let speciesBlockedReason = $derived.by(() => {
    if (!canReview) return t('detections.detail.review.loginRequired');
    if (detection?.locked) return t('detections.detail.review.lockedHelp');
    if (enhancedDatabase === false) return t('detections.detail.review.legacyHelp');
    return '';
  });

  // The model's own pick, present among the predictions once a reviewer changed the species.
  let originalPrediction = $derived(
    predictions?.modelPredictions
      ?.flatMap(set => set.predictions ?? [])
      .find(item => item.original === true) ?? null
  );
  let originalSpeciesName = $derived(
    originalPrediction
      ? localizeSpeciesName(
          originalPrediction.scientificName,
          originalPrediction.commonName ?? originalPrediction.scientificName
        )
      : ''
  );

  let sunOffset = $derived(
    detection
      ? nearestSunEvent(
          wallClockMinutes(detection.timestamp ?? `T${detection.time}`),
          wallClockMinutes(sun?.sunrise ?? weather?.daily?.sunrise),
          wallClockMinutes(sun?.sunset ?? weather?.daily?.sunset)
        )
      : null
  );

  // Only alphanumeric characters, hyphens and underscores are valid in a detection ID.
  function isValidDetectionId(id: string): boolean {
    return /^[a-zA-Z0-9_-]+$/.test(id);
  }

  // Resolve detection ID from the URL if not provided via prop
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

  // Load the detection whenever the ID changes
  $effect(() => {
    if (resolvedDetectionId) {
      void fetchDetection(resolvedDetectionId, false);
    }

    return () => {
      detectionController?.abort();
      relatedController?.abort();
      speciesController?.abort();
      attributionController?.abort();
      cancelAttributionRetry();
    };
  });

  function errorMessageForStatus(status: number): string {
    switch (status) {
      case 404:
        return t('detections.errors.notFound');
      case 403:
        return t('detections.errors.noPermission');
      case 401:
        return t('detections.errors.loginRequired');
      case 500:
      case 502:
      case 503:
        return t('detections.errors.serverError');
      default:
        return t('detections.errors.loadFailed', { status });
    }
  }

  /**
   * Loads a detection. A refresh (after a review, lock or note change) reloads
   * the same detection in place and leaves the related data alone; otherwise the
   * page keeps showing the previous detection until the new one arrives, so
   * stepping through detections does not flash a skeleton.
   */
  async function fetchDetection(id: string, refresh: boolean) {
    detectionController?.abort();
    const controller = new AbortController();
    detectionController = controller;
    const { signal } = controller;

    if (!refresh) {
      isLoadingDetection = true;
      detectionError = null;
    }

    try {
      const response = await fetch(buildAppUrl(`/api/v2/detections/${id}`), { signal });
      // Check the captured signal, not the shared controller: a newer request may
      // have replaced detectionController, and this stale response must not win.
      if (signal.aborted) return;
      if (!response.ok) {
        throw new Error(errorMessageForStatus(response.status));
      }
      const data = (await response.json()) as DetectionDetailData;
      if (signal.aborted) return;

      const speciesChanged = detection?.scientificName !== data.scientificName;
      detection = data;
      if (!refresh) {
        loadDetectionData(data);
        if (speciesChanged) loadSpeciesData(data);
      } else if (speciesChanged) {
        // A reviewer changed the species: everything tied to the species is stale.
        loadDetectionData(data);
        loadSpeciesData(data);
      } else {
        // A verdict may have changed, and with it the species' review counts.
        void loadReviewCounts(data.scientificName);
      }
    } catch (error) {
      if (signal.aborted || (error instanceof Error && error.name === 'AbortError')) {
        return;
      }
      logger.error('Error fetching detection:', error);
      if (refresh) return;
      detection = null;
      detectionError =
        error instanceof Error ? error.message : t('detections.errors.loadFailed', { status: '' });
    } finally {
      // Only the request that still owns the controller may reset shared state.
      if (detectionController === controller) {
        isLoadingDetection = false;
        detectionController = null;
      }
    }
  }

  /** Reloads the current detection after a change made on this page. */
  function refreshDetection() {
    if (resolvedDetectionId) {
      void fetchDetection(resolvedDetectionId, true);
    }
  }

  /** GET a JSON document; null when the request fails or the server has no answer. */
  async function loadJson<T>(path: string, signal: AbortSignal): Promise<T | null> {
    try {
      const response = await fetch(buildAppUrl(path), { signal });
      if (signal.aborted || !response.ok) return null;
      return (await response.json()) as T;
    } catch (error) {
      if (!signal.aborted && !(error instanceof Error && error.name === 'AbortError')) {
        logger.error('Error loading detection detail data:', error, { path });
      }
      return null;
    }
  }

  /** Wraps a setter so a superseded request cannot write its late answer. */
  function unlessAborted<T>(signal: AbortSignal, assign: (_value: T | null) => void) {
    return (value: T | null) => {
      if (!signal.aborted) assign(value);
    };
  }

  /**
   * Loads the data tied to one detection. Each request fills its own panel when
   * it answers; none blocks the page.
   */
  function loadDetectionData(det: DetectionDetailData) {
    relatedController?.abort();
    const controller = new AbortController();
    relatedController = controller;
    const { signal } = controller;

    // Weather, sun times and daily counts keep showing the previous detection's
    // values until the new ones arrive, so their cards keep their size during the
    // step. Neighbours and predictions are cleared at once instead: both feed
    // actions (stepping, picking a species), and stale ones would act on the wrong
    // detection.
    neighbors = null;
    predictions = null;

    const species = encodeURIComponent(det.scientificName);

    void loadJson<DetectionWeather>(`/api/v2/weather/detection/${det.id}`, signal).then(
      unlessAborted(signal, value => (weather = value))
    );
    void loadJson<SunTimes>(`/api/v2/weather/sun/${det.date}`, signal).then(
      unlessAborted(signal, value => (sun = value))
    );
    void loadNeighbors(det.id, signal);
    void loadJson<DetectionPredictions>(`/api/v2/detections/${det.id}/predictions`, signal).then(
      unlessAborted(signal, value => (predictions = value))
    );
    void loadJson<DailyCounts>(
      `/api/v2/analytics/time/daily?start_date=${addDays(det.date, -(DAILY_WINDOW_DAYS - 1))}&end_date=${det.date}&species=${species}`,
      signal
    ).then(unlessAborted(signal, value => (dailyCounts = value)));
  }

  /**
   * Loads the neighbours used by the older and newer buttons. The route needs the
   * current database format and answers 409 without it; that answer also tells
   * the page that changing a detection's species is not possible there.
   */
  async function loadNeighbors(id: number, signal: AbortSignal) {
    try {
      const response = await fetch(buildAppUrl(`/api/v2/detections/${id}/neighbors`), { signal });
      if (signal.aborted) return;
      if (response.status === HTTP_CONFLICT) {
        enhancedDatabase = false;
      }
      if (!response.ok) {
        stepping = 'unavailable';
        return;
      }
      const data = (await response.json()) as DetectionNeighbors;
      if (signal.aborted) return;
      neighbors = data;
      stepping = 'ready';
      enhancedDatabase = true;
    } catch (error) {
      if (signal.aborted || (error instanceof Error && error.name === 'AbortError')) return;
      logger.error('Error loading detection neighbours:', error);
      stepping = 'unavailable';
    }
  }

  /** Counts the species' detections reviewers marked correct and false positive. */
  async function loadReviewCounts(scientificName: string) {
    const count = async (verifiedStatus: string): Promise<number> => {
      const result = await fetchWithCSRF<{ total?: number }>('/api/v2/search', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ speciesScientific: [scientificName], verifiedStatus, page: 1 }),
      });
      return result.total ?? 0;
    };
    try {
      const [correct, falsePositive] = await Promise.all([
        count('correct'),
        count('false_positive'),
      ]);
      // A late answer for a species the page has left is dropped.
      if (detection?.scientificName === scientificName) {
        reviewCounts = { correct, falsePositive };
      }
    } catch (error) {
      logger.error('Error loading species review counts:', error);
    }
  }

  /**
   * Loads the data tied to the detection's species. Called only when the species
   * changes, so stepping through detections of one species keeps what is loaded.
   */
  function loadSpeciesData(det: DetectionDetailData) {
    speciesController?.abort();
    const controller = new AbortController();
    speciesController = controller;
    const { signal } = controller;

    speciesInfo = null;
    taxonomyInfo = null;
    imageAttribution = null;
    hourlyActivity = null;
    speciesSummary = null;
    reviewCounts = null;

    const species = encodeURIComponent(det.scientificName);

    void loadJson<SpeciesInfo>(`/api/v2/species?scientific_name=${species}`, signal).then(
      unlessAborted(signal, value => (speciesInfo = value))
    );
    void loadJson<TaxonomyInfo>(`/api/v2/species/taxonomy?scientific_name=${species}`, signal).then(
      unlessAborted(signal, value => (taxonomyInfo = value))
    );
    void loadJson<HourlyBucket[]>(
      `/api/v2/analytics/time/distribution/hourly?start_date=${ACTIVITY_START_DATE}&end_date=${getLocalDateString()}&species=${species}`,
      signal
    ).then(unlessAborted(signal, value => (hourlyActivity = Array.isArray(value) ? value : null)));

    summaryLoading = true;
    loadSpeciesSummary(det.scientificName)
      .then(value => {
        if (!signal.aborted) speciesSummary = value;
      })
      .catch((error: unknown) => {
        if (!signal.aborted) logger.error('Error loading species summary:', error);
      })
      .finally(() => {
        if (!signal.aborted) summaryLoading = false;
      });

    void loadReviewCounts(det.scientificName);
    void fetchImageAttribution(det.scientificName);
  }

  /**
   * Delays before re-requesting attribution that answered "not resolved yet".
   * Attribution is supporting detail; the image itself is what the user waits on.
   */
  const ATTRIBUTION_RETRY_DELAYS_MS = [5000, 15000, 40000];

  /**
   * Pending 503 retry. Tracked apart from attributionController because the
   * controller is released as soon as its request settles: without this a
   * scheduled retry would still fire after the view was unmounted or moved to
   * another species.
   */
  let attributionRetryTimer: ReturnType<typeof globalThis.setTimeout> | undefined;

  function cancelAttributionRetry() {
    if (attributionRetryTimer !== undefined) {
      globalThis.clearTimeout(attributionRetryTimer);
      attributionRetryTimer = undefined;
    }
  }

  // Fetch image attribution metadata
  async function fetchImageAttribution(scientificName: string, attempt = 0) {
    if (!scientificName.trim()) return;

    attributionController?.abort();
    cancelAttributionRetry();
    const controller = new AbortController();
    attributionController = controller;
    const { signal } = controller;

    try {
      const url = buildAppUrl(
        `/api/v2/media/species-image/info?name=${encodeURIComponent(scientificName)}`
      );
      const response = await fetch(url, { signal });
      if (signal.aborted) return;
      if (response.ok) {
        const data = await response.json();
        if (signal.aborted) return;
        imageAttribution = data as ImageAttribution;
        return;
      }
      // 503 means the image is still being resolved in the background, not that
      // it has no attribution. Without a retry a CC-BY photo that the thumbnail's
      // own retry recovers seconds later would be shown with no author or licence
      // credit, which is a licensing problem and not merely a cosmetic one.
      if (response.status === 503 && attempt < ATTRIBUTION_RETRY_DELAYS_MS.length) {
        const delay = ATTRIBUTION_RETRY_DELAYS_MS.at(attempt) ?? 0;
        attributionRetryTimer = globalThis.setTimeout(() => {
          attributionRetryTimer = undefined;
          if (!signal.aborted) void fetchImageAttribution(scientificName, attempt + 1);
        }, delay);
        // Return before the finally block releases the controller, so this
        // request's signal stays reachable and an abort can cancel the retry.
        return;
      }
    } catch (error) {
      if (signal.aborted || (error instanceof Error && error.name === 'AbortError')) return;
      // Attribution is non-critical - fail silently
    } finally {
      if (attributionController === controller) {
        attributionController = null;
      }
    }
  }

  function openDetection(id: number) {
    navigation.navigate(`/ui/detections/${id}`);
  }

  function backToList() {
    navigation.navigate('/ui/detections');
  }
</script>

<main class="col-span-12 dd-page" aria-label={t('detections.detail.aria.mainRegion')}>
  <!-- Loading state with live region -->
  <div role="status" aria-live="polite" class="sr-only">
    {#if isLoadingDetection}
      {t('detections.aria.loading')}
    {:else if detection}
      {t('detections.aria.loaded', { species: displayName })}
    {:else if detectionError}
      {t('detections.aria.error', { error: detectionError })}
    {/if}
  </div>

  {#if detection}
    <DetailNavBar
      speciesName={displayName}
      date={detection.date}
      time={detection.time}
      {neighbors}
      available={stepping !== 'unavailable'}
      onNavigate={openDetection}
      onBack={backToList}
    />

    <div class="dd-top" class:dd-switching={isLoadingDetection}>
      <div class="dd-column">
        <IdentityCard
          {detection}
          {displayName}
          taxonomy={taxonomyInfo?.taxonomy}
          rarity={speciesInfo?.rarity}
          {imageAttribution}
          {sunOffset}
          speciesSetByReviewer={originalPrediction !== null}
          {predictionCount}
          onShowPredictions={() => (speciesDialogOpen = true)}
        />
        <div class="dd-grow">
          <ReviewPanel
            {detection}
            speciesName={displayName}
            {canReview}
            speciesChangeSupported={enhancedDatabase}
            onWrongSpecies={() => (speciesDialogOpen = true)}
            onChanged={refreshDetection}
          />
        </div>
      </div>
      <div class="dd-column dd-grow-children">
        <ConditionsCard {detection} {weather} {sun} {hourlyActivity} speciesName={displayName} />
      </div>
    </div>

    <!-- Media section: the clip, or the kept spectrogram image after retention removed its audio -->
    {#if detection.clipName || detection.spectrogramOnly}
      <section class="dd-media" aria-labelledby="media-heading">
        <RecordingHeader
          {detection}
          speciesName={displayName}
          showClipHint={clipExtractionEnabled}
          headingId="media-heading"
        />
        <div
          role="region"
          aria-label={detection.clipName
            ? t('detections.detail.aria.audioRecordingFor', { name: displayName })
            : t('components.audio.spectrogramForSpecies', { species: displayName })}
        >
          <div class="detail-audio-container">
            {#key detection.id}
              {#if detection.clipName}
                <AudioPlayer
                  audioUrl={buildAppUrl(`/api/v2/audio/${detection.id}`)}
                  detectionId={detection.id.toString()}
                  showSpectrogram={true}
                  spectrogramSize="lg"
                  spectrogramRaw={false}
                  responsive={true}
                  className="w-full"
                  enableClipExtraction={clipExtractionEnabled}
                  clipLabel={`${detection.commonName}_${detection.date}_${detection.time.replace(/:/g, '-')}`}
                  modelType={detection.modelType}
                  clipSampleRate={detection.clipSampleRate}
                  spectrogramFollowsDenoise={true}
                />
              {:else}
                <SpectrogramImage
                  detectionId={detection.id.toString()}
                  size="lg"
                  raw={false}
                  className="w-full"
                />
              {/if}
            {/key}
          </div>
        </div>
      </section>
    {/if}

    <div class="dd-columns" class:dd-switching={isLoadingDetection}>
      <div class="dd-column">
        <ModelsCard contributions={detection.modelContributions} />
        <ActivityLog
          {detection}
          speciesName={displayName}
          {canReview}
          {originalSpeciesName}
          originalConfidence={originalPrediction?.confidence}
          onChanged={refreshDetection}
        />
      </div>
      <div class="dd-column">
        <SpeciesStatsCard
          {detection}
          speciesName={displayName}
          rarity={speciesInfo?.rarity}
          summary={speciesSummary}
          {summaryLoading}
          daily={dailyCounts}
          {dailyStart}
          dailyDays={DAILY_WINDOW_DAYS}
          reviews={reviewCounts}
        />
      </div>
    </div>

    <SpeciesChangeDialog
      isOpen={speciesDialogOpen}
      {detection}
      speciesName={displayName}
      {alternates}
      {predictionModel}
      canChange={canChangeSpecies}
      blockedReason={speciesBlockedReason}
      onClose={() => (speciesDialogOpen = false)}
      onChanged={refreshDetection}
    />
  {:else if isLoadingDetection}
    <!-- Loading skeleton in the shape of the loaded page (decorative; the live region above speaks) -->
    <div class="dd-skeleton" aria-hidden="true">
      <div class="dd-skeleton-line dd-skeleton-nav"></div>
      <div class="dd-top">
        <div class="dd-column">
          <div class="dd-skeleton-card dd-skeleton-identity"></div>
          <div class="dd-skeleton-card dd-skeleton-review"></div>
        </div>
        <div class="dd-skeleton-card dd-skeleton-conditions"></div>
      </div>
      <div class="dd-skeleton-card dd-skeleton-media"></div>
    </div>
  {:else if detectionError}
    <div class="dd-error">
      <div role="alert" aria-live="assertive">
        <ErrorAlert message={detectionError} />
      </div>
    </div>
  {/if}
</main>

<style>
  .dd-page {
    display: flex;
    flex-direction: column;
    gap: 1rem;
    width: 100%;
    min-width: 0;
  }

  /* Top row: identity and review beside time and conditions. The left column sets
     the height of the row with content that is the same for every detection. The
     conditions card fills that height with its sky strip, so neither column ends
     in an empty tail. */
  .dd-top {
    display: grid;
    grid-template-columns: minmax(0, 1.5fr) minmax(21rem, 1fr);
    gap: 1rem;
    align-items: stretch;
  }

  .dd-columns {
    display: grid;
    grid-template-columns: minmax(0, 1.5fr) minmax(21rem, 1fr);
    gap: 1rem;
    align-items: start;
  }

  .dd-column {
    display: flex;
    flex-direction: column;
    gap: 1rem;
    min-width: 0;
  }

  .dd-grow {
    display: flex;
    flex: 1;
    flex-direction: column;
    min-width: 0;
  }

  .dd-grow > :global(*),
  .dd-grow-children > :global(*) {
    flex: 1;
  }

  /* Shown while the next detection loads over the current one. */
  .dd-switching {
    opacity: 0.6;
    transition: opacity 150ms ease;
  }

  .dd-media {
    min-width: 0;
    padding: 0.875rem 1.25rem 1.25rem;
    background-color: var(--color-base-100);
    border: 1px solid var(--border-100);
    border-radius: var(--radius-box);
  }

  /* Audio container: overflow visible so the player's menus are not clipped. */
  .detail-audio-container {
    position: relative;
    width: 100%;
    overflow: visible;
    border-radius: 0.5rem;
    background: linear-gradient(to bottom, rgb(128 128 128 / 0.1), rgb(128 128 128 / 0.05));
  }

  .dd-error {
    padding: 1.5rem;
    background-color: var(--color-base-100);
    border: 1px solid var(--border-100);
    border-radius: var(--radius-box);
  }

  .dd-skeleton {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .dd-skeleton-line,
  .dd-skeleton-card {
    border-radius: var(--radius-box);
    background-color: var(--color-base-100);
    border: 1px solid var(--border-100);
    animation: dd-pulse 1.6s ease-in-out infinite;
  }

  .dd-skeleton-nav {
    width: 18rem;
    max-width: 100%;
    height: 2rem;
  }

  .dd-skeleton-identity {
    height: 16rem;
  }

  .dd-skeleton-review {
    height: 12rem;
  }

  .dd-skeleton-conditions {
    min-height: 29rem;
  }

  .dd-skeleton-media {
    aspect-ratio: 2 / 1;
  }

  @keyframes dd-pulse {
    0%,
    100% {
      opacity: 1;
    }

    50% {
      opacity: 0.55;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .dd-skeleton-line,
    .dd-skeleton-card {
      animation: none;
    }

    .dd-switching {
      transition: none;
    }
  }

  @media (max-width: 1100px) {
    .dd-top,
    .dd-columns {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
