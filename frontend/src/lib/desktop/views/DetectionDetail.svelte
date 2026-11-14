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
  import ErrorAlert from '$lib/desktop/components/ui/ErrorAlert.svelte';
  import Skeleton from '$lib/desktop/components/ui/Skeleton.svelte';
  import SkeletonStatus from '$lib/desktop/components/ui/SkeletonStatus.svelte';
  import ActivityLog from '$lib/desktop/features/detections/components/detail/ActivityLog.svelte';
  import ConditionsCard from '$lib/desktop/features/detections/components/detail/ConditionsCard.svelte';
  import DetailCard from '$lib/desktop/features/detections/components/detail/DetailCard.svelte';
  import DetailNavBar from '$lib/desktop/features/detections/components/detail/DetailNavBar.svelte';
  import IdentityCard from '$lib/desktop/features/detections/components/detail/IdentityCard.svelte';
  import ModelsCard from '$lib/desktop/features/detections/components/detail/ModelsCard.svelte';
  import RecordingCard from '$lib/desktop/features/detections/components/detail/RecordingCard.svelte';
  import ReviewPanel from '$lib/desktop/features/detections/components/detail/ReviewPanel.svelte';
  import SpeciesChangeDialog from '$lib/desktop/features/detections/components/detail/SpeciesChangeDialog.svelte';
  import SpeciesStatsCard from '$lib/desktop/features/detections/components/detail/SpeciesStatsCard.svelte';
  import { formatModelLabel } from '$lib/desktop/features/detections/components/detail/detailFormat';
  import {
    isDailyCounts,
    isDetectionDetail,
    isDetectionNeighbors,
    isDetectionPredictions,
    isDetectionWeather,
    isHourlyBuckets,
    isSpeciesRarity,
    isSunTimes,
    isTaxonomy,
    parseImageAttribution,
    searchTotal,
  } from '$lib/desktop/features/detections/components/detail/detailGuards';
  import {
    dailyWindowStart,
    detectionMinute,
    nearestSunEvent,
    sunEventTimes,
    wallClockMinutes,
  } from '$lib/desktop/features/detections/components/detail/detailTime';
  import type {
    DailyCounts,
    DetectionDetailData,
    DetectionNeighbors,
    DetectionPredictions,
    DetectionWeather,
    HourlyBucket,
    SavedChange,
    SpeciesRarity,
    SpeciesReviewCounts,
    SpeciesSummary,
    SunTimes,
    TaxonomyHierarchy,
  } from '$lib/desktop/features/detections/components/detail/detailTypes';
  import { loadSpeciesSummary } from '$lib/desktop/features/detections/components/detail/speciesSummary';
  import { t } from '$lib/i18n';
  import type { ImageAttribution } from '$lib/types/detection.types';
  import { api } from '$lib/utils/api';
  import { hasReviewPermission } from '$lib/utils/auth';
  import { isPlainObject } from '$lib/utils/security';
  import { getLocalDateString } from '$lib/utils/date';
  import { loggers } from '$lib/utils/logger';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';
  import { buildAppUrl } from '$lib/utils/urlHelpers';

  const logger = loggers.ui;

  // Days of daily counts shown in the species statistics, ending on the detection's day.
  const DAILY_WINDOW_DAYS = 30;
  // Earliest date asked of the hour-of-day distribution: before any stored detection.
  const ACTIVITY_START_DATE = '2000-01-01';
  // Answer of the routes that need the current database format when it is not in use.
  const HTTP_CONFLICT = 409;
  const HTTP_SERVICE_UNAVAILABLE = 503;

  interface Props {
    detectionId?: string;
  }

  const { detectionId: detectionIdProp }: Props = $props();

  // Resolved detection ID - set by the $effect below so prop changes are followed.
  let resolvedDetectionId = $state<string | undefined>(undefined);

  let canReview = $derived($hasReviewPermission);

  let detection = $state<DetectionDetailData | null>(null);
  let isLoadingDetection = $state(true);
  let detectionError = $state<string | null>(null);

  // Data loaded beside the detection. Each is null until its request answers.
  let rarity = $state<SpeciesRarity | null>(null);
  let taxonomy = $state<TaxonomyHierarchy | null>(null);
  let imageAttribution = $state<ImageAttribution | null>(null);
  // The weather and sun answers by what they belong to (the detection's id, its date), so a
  // detection never shows the previous one's. A key with a null value answered "none".
  let weatherAnswers = $state<Record<string, DetectionWeather | null>>({});
  let sunAnswers = $state<Record<string, SunTimes | null>>({});
  let neighbors = $state<DetectionNeighbors | null>(null);
  let predictions = $state<DetectionPredictions | null>(null);
  let hourlyActivity = $state<HourlyBucket[] | null>(null);
  let dailyCounts = $state<DailyCounts | null>(null);
  let speciesSummary = $state<SpeciesSummary | null>(null);
  let reviewCounts = $state<SpeciesReviewCounts | null>(null);
  // The species whose data (and review counts) the page holds; empty before the first load.
  let speciesDataFor = '';

  // Which requests are out, so a card can tell "still loading" from "not recorded".
  let loading = $state({
    weather: false,
    sun: false,
    hourly: false,
    rarity: false,
    summary: false,
    daily: false,
    reviews: false,
  });
  type LoadingKey = keyof typeof loading;

  // Whether the server can step between detections. It cannot on the legacy
  // database format, where the navigation groups are left out.
  let steppingAvailable = $state(true);
  // True when the neighbours request failed for the detection shown.
  let neighborsFailed = $state(false);
  // Whether the server runs the current database format; null until a route that
  // depends on it has answered. Kept across detections: it does not change.
  let enhancedDatabase = $state<boolean | null>(null);

  // AbortControllers, one per lifetime:
  // - detection: the detection itself;
  // - related: weather and sun times, tied to the detection and its date;
  // - lists: neighbours, predictions and daily counts, which also change with the species;
  // - species and attribution: tied to the species, kept while stepping within one species.
  let detectionController: AbortController | null = null;
  // Number of the latest review count request.
  let reviewCountsRequest = 0;
  let relatedController: AbortController | null = null;
  let listsController: AbortController | null = null;
  let speciesController: AbortController | null = null;
  let attributionController: AbortController | null = null;

  let displayName = $derived(
    detection ? localizeSpeciesName(detection.scientificName, detection.commonName) : ''
  );

  // The species dialog: the model's other predictions, and the place to change the species.
  let speciesDialogOpen = $state(false);

  // The dialog belongs to the detection it was opened on; another detection closes it.
  $effect(() => {
    void resolvedDetectionId;
    speciesDialogOpen = false;
  });

  // The detecting model's predictions; it is the one model that stores them.
  let predictionSet = $derived(predictions?.modelPredictions?.at(0) ?? null);
  let alternates = $derived(predictionSet?.predictions ?? []);
  let predictionModel = $derived(
    predictionSet
      ? formatModelLabel(
          predictionSet.modelName,
          predictionSet.modelVersion,
          predictionSet.modelVariant
        )
      : ''
  );
  // How many other predictions there are; null while the request is out. A legacy
  // database stores none.
  let predictionCount = $derived.by(() => {
    if (predictions) return alternates.length;
    return enhancedDatabase === false ? 0 : null;
  });

  // Why the species cannot be changed, the most pressing reason first; empty when it can.
  // The review panel shows the same reason as its status line.
  // The database format is known once the neighbours route has answered; a change is not
  // offered before that, since the legacy database refuses it.
  let speciesBlockedReason = $derived.by(() => {
    if (!canReview) return t('detections.detail.review.loginRequired');
    if (detection?.locked) return t('detections.detail.review.lockedHelp');
    if (enhancedDatabase === false) return t('detections.detail.review.legacyHelp');
    if (enhancedDatabase === null) {
      return neighborsFailed
        ? t('detections.detail.review.databaseUnknown')
        : t('detections.detail.review.databaseChecking');
    }
    return '';
  });

  // The answers that belong to the detection shown; null while they are out or when there are none.
  let shownWeather = $derived(detection ? (weatherAnswers[String(detection.id)] ?? null) : null);
  let shownSun = $derived(detection ? (sunAnswers[detection.date] ?? null) : null);

  let sunOffset = $derived.by(() => {
    if (!detection) return null;
    const times = sunEventTimes(shownSun, shownWeather);
    return nearestSunEvent(
      detectionMinute(detection),
      wallClockMinutes(times.sunrise),
      wallClockMinutes(times.sunset)
    );
  });

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

  // Load the detection whenever the ID changes. Stepping to another detection
  // cancels what belongs to the one left behind, but not the species data: a step
  // within one species keeps it, and loadSpeciesData replaces it on a species change.
  $effect(() => {
    if (resolvedDetectionId) {
      void fetchDetection(resolvedDetectionId, false);
    }

    return () => {
      detectionController?.abort();
      relatedController?.abort();
      listsController?.abort();
    };
  });

  // Everything else stops when the page closes.
  $effect(() => {
    return () => {
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

  /** True when a request ended because it was cancelled, not because it failed. */
  function isAbort(error: unknown, signal: AbortSignal): boolean {
    return signal.aborted || (error instanceof Error && error.name === 'AbortError');
  }

  /** Replaces a controller with a fresh one, cancelling the requests of the old one. */
  function renew(controller: AbortController | null): AbortController {
    controller?.abort();
    return new AbortController();
  }

  /**
   * Loads a detection. A refresh (after a review, lock, note or species change)
   * reloads the same detection in place; otherwise the page keeps showing the
   * previous detection until the new one arrives, so stepping through detections
   * does not flash a skeleton.
   *
   * What does not need the detection's answer (weather, neighbours, predictions)
   * is requested at once beside it; what needs its date or species follows it.
   */
  async function fetchDetection(id: string, refresh: boolean) {
    detectionController = renew(detectionController);
    const controller = detectionController;
    const { signal } = controller;

    // The signals of the requests started beside this detection, for those that follow it.
    let related: AbortSignal | null = null;
    let lists: AbortSignal | null = null;
    if (!refresh) {
      isLoadingDetection = true;
      detectionError = null;
      relatedController = renew(relatedController);
      listsController = renew(listsController);
      related = relatedController.signal;
      lists = listsController.signal;
      loadWeather(id, related);
      loadLists(id, lists);
    }

    try {
      const response = await fetch(buildAppUrl(`/api/v2/detections/${id}`), { signal });
      // Check the captured signal, not the shared controller: a newer request may
      // have replaced detectionController, and this stale response must not win.
      if (signal.aborted) return;
      if (!response.ok) {
        throw new Error(errorMessageForStatus(response.status));
      }
      const body: unknown = await response.json();
      if (signal.aborted) return;
      if (!isDetectionDetail(body)) {
        throw new Error(t('detections.errors.loadFailed', { status: response.status }));
      }
      const data = body;

      const speciesChanged = speciesDataFor !== data.scientificName;
      detection = data;
      pruneAnswers(data);

      if (related && lists) {
        loadSun(data, related);
        loadDaily(data, lists);
      } else if (speciesChanged) {
        // A reviewer changed the species: the lists that depend on it are stale.
        // Weather and sun times belong to the detection and its date, and stay.
        listsController = renew(listsController);
        loadLists(id, listsController.signal);
        loadDaily(data, listsController.signal);
      }

      // The species data is loaded when the species changes; a step within one species keeps it.
      if (speciesChanged) loadSpeciesData(data);
    } catch (error) {
      if (isAbort(error, signal)) return;
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

  /**
   * A change was saved on a detection. Its species' review counts are counted again when
   * the page holds that species' data, whichever detection is shown now. The detection
   * itself is reloaded only when it is the one the page is on:
   * - shown and settled: reloaded in place, keeping what is on screen if that fails;
   * - being stepped to: the step's request may have been sent before the save went
   *   through, so the whole step starts again, with its sun times and daily counts;
   * - already left: nothing to reload, a step brings its own detection.
   */
  function onSaved(change: SavedChange) {
    if (change.reviewCounts && change.scientificName === speciesDataFor && speciesController) {
      void loadReviewCounts(change.scientificName, speciesController.signal);
    }
    if (!resolvedDetectionId || String(change.detectionId) !== resolvedDetectionId) return;
    void fetchDetection(resolvedDetectionId, !isLoadingDetection);
  }

  /** Drops the answers of detections and days the page has moved on from. */
  function pruneAnswers(shown: DetectionDetailData) {
    const id = String(shown.id);
    for (const key of Object.keys(weatherAnswers)) {
      // eslint-disable-next-line security/detect-object-injection -- key comes from Object.keys
      if (key !== id) delete weatherAnswers[key];
    }
    for (const key of Object.keys(sunAnswers)) {
      // eslint-disable-next-line security/detect-object-injection -- key comes from Object.keys
      if (key !== shown.date) delete sunAnswers[key];
    }
  }

  /**
   * GET a JSON document; null when the request fails, the server has no answer or the
   * answer is not the shape `isShape` accepts.
   * Plain fetch on purpose: the shared API client reports every unexpected answer
   * to error tracking, and these routes answer 404 (no weather for the time) and
   * 400 (no species row for a sound class) in normal use.
   */
  async function loadJson<T>(
    path: string,
    signal: AbortSignal,
    isShape: (_value: unknown) => _value is T
  ): Promise<T | null> {
    try {
      const response = await fetch(buildAppUrl(path), { signal });
      if (signal.aborted || !response.ok) return null;
      const body: unknown = await response.json();
      if (isShape(body)) return body;
      logger.error('Unexpected shape of detection detail data', undefined, { path });
      return null;
    } catch (error) {
      if (!isAbort(error, signal)) {
        logger.error('Error loading detection detail data:', error, { path });
      }
      return null;
    }
  }

  /**
   * Requests a JSON document and hands its answer (null when there is none) to
   * `assign`, unless a newer request has replaced it. With a `key`, the matching
   * loading flag is set while the request is out.
   */
  function load<T>(
    path: string,
    signal: AbortSignal,
    isShape: (_value: unknown) => _value is T,
    assign: (_value: T | null) => void,
    key?: LoadingKey
  ) {
    // eslint-disable-next-line security/detect-object-injection -- key is a typed union
    if (key) loading[key] = true;
    void loadJson(path, signal, isShape).then(value => {
      if (signal.aborted) return;
      assign(value);
      // eslint-disable-next-line security/detect-object-injection -- key is a typed union
      if (key) loading[key] = false;
    });
  }

  /**
   * The weather at the detection. The detection shown keeps the weather it has until
   * its own answer arrives; the next detection shows placeholders instead of it.
   */
  function loadWeather(id: string, signal: AbortSignal) {
    load(
      `/api/v2/weather/detection/${id}`,
      signal,
      isDetectionWeather,
      // eslint-disable-next-line security/detect-object-injection -- id passed isValidDetectionId
      value => (weatherAnswers[id] = value),
      'weather'
    );
  }

  /** Sunrise and sunset of the detection's day. */
  function loadSun(det: DetectionDetailData, signal: AbortSignal) {
    const { date } = det;
    load(
      `/api/v2/weather/sun/${date}`,
      signal,
      isSunTimes,
      // eslint-disable-next-line security/detect-object-injection -- date comes from the detection's date field
      value => (sunAnswers[date] = value),
      'sun'
    );
  }

  /**
   * Neighbours and predictions. Both feed actions (stepping, picking a species),
   * so stale ones are cleared at once instead of being kept on screen.
   */
  function loadLists(id: string, signal: AbortSignal) {
    neighbors = null;
    neighborsFailed = false;
    predictions = null;
    void loadNeighbors(id, signal);
    load(
      `/api/v2/detections/${id}/predictions`,
      signal,
      isDetectionPredictions,
      value => (predictions = value)
    );
  }

  /** The species' detections per day for the weeks up to the detection. */
  function loadDaily(det: DetectionDetailData, signal: AbortSignal) {
    // Counts for another window or species are not shown while these load.
    dailyCounts = null;
    const species = encodeURIComponent(det.scientificName);
    const start = dailyWindowStart(det.date, DAILY_WINDOW_DAYS);
    load(
      `/api/v2/analytics/time/daily?start_date=${start}&end_date=${det.date}&species=${species}`,
      signal,
      isDailyCounts,
      value => (dailyCounts = value),
      'daily'
    );
  }

  /**
   * Loads the neighbours used by the older and newer links. The route needs the
   * current database format and answers 409 without it; that answer also tells
   * the page that changing a detection's species is not possible there.
   */
  async function loadNeighbors(id: string, signal: AbortSignal) {
    try {
      const response = await fetch(buildAppUrl(`/api/v2/detections/${id}/neighbors`), { signal });
      if (signal.aborted) return;
      // Only the legacy database cannot step at all; another failed answer leaves the
      // links in place, blocked, so the one in focus stays.
      if (response.status === HTTP_CONFLICT) {
        enhancedDatabase = false;
        steppingAvailable = false;
        return;
      }
      if (!response.ok) {
        neighborsFailed = true;
        return;
      }
      const body: unknown = await response.json();
      if (signal.aborted) return;
      if (!isDetectionNeighbors(body)) {
        logger.error('Unexpected shape of detection neighbours');
        neighborsFailed = true;
        return;
      }
      neighbors = body;
      steppingAvailable = true;
      enhancedDatabase = true;
    } catch (error) {
      if (isAbort(error, signal)) return;
      logger.error('Error loading detection neighbours:', error);
      neighborsFailed = true;
    }
  }

  /** Counts the species' detections reviewers marked correct and false positive. */
  async function loadReviewCounts(scientificName: string, signal: AbortSignal) {
    const count = async (verifiedStatus: string): Promise<number> => {
      const result = await api.post<unknown>(
        '/api/v2/search',
        { speciesScientific: [scientificName], verifiedStatus, page: 1 },
        { signal }
      );
      return searchTotal(result);
    };
    // Counts asked for later (after a verdict) win over earlier ones still out.
    reviewCountsRequest += 1;
    const request = reviewCountsRequest;
    const latest = () => !signal.aborted && request === reviewCountsRequest;
    loading.reviews = true;
    try {
      const [correct, falsePositive] = await Promise.all([
        count('correct'),
        count('false_positive'),
      ]);
      if (!latest()) return;
      reviewCounts = { correct, falsePositive };
    } catch (error) {
      if (!latest() || isAbort(error, signal)) return;
      logger.error('Error loading species review counts:', error);
    }
    loading.reviews = false;
  }

  /** The body of GET /api/v2/species: an object that may hold a rarity block. */
  function isSpeciesAnswer(value: unknown): value is { rarity?: unknown } {
    return isPlainObject(value);
  }

  /** The body of GET /api/v2/species/taxonomy: an object that may hold a taxonomy block. */
  function isTaxonomyAnswer(value: unknown): value is { taxonomy?: unknown } {
    return isPlainObject(value);
  }

  /**
   * Loads the data tied to the detection's species. Called only when the species
   * changes, so stepping through detections of one species keeps what is loaded.
   */
  function loadSpeciesData(det: DetectionDetailData) {
    speciesController = renew(speciesController);
    const { signal } = speciesController;
    speciesDataFor = det.scientificName;

    rarity = null;
    taxonomy = null;
    imageAttribution = null;
    hourlyActivity = null;
    speciesSummary = null;
    reviewCounts = null;

    const species = encodeURIComponent(det.scientificName);

    load(
      `/api/v2/species?scientific_name=${species}`,
      signal,
      isSpeciesAnswer,
      value => (rarity = isSpeciesRarity(value?.rarity) ? value.rarity : null),
      'rarity'
    );
    load(
      `/api/v2/species/taxonomy?scientific_name=${species}`,
      signal,
      isTaxonomyAnswer,
      value => (taxonomy = isTaxonomy(value?.taxonomy) ? value.taxonomy : null)
    );
    load(
      `/api/v2/analytics/time/distribution/hourly?start_date=${ACTIVITY_START_DATE}&end_date=${getLocalDateString()}&species=${species}`,
      signal,
      isHourlyBuckets,
      value => (hourlyActivity = value),
      'hourly'
    );

    loading.summary = true;
    loadSpeciesSummary(det.scientificName)
      .then(value => {
        if (!signal.aborted) speciesSummary = value;
      })
      .catch((error: unknown) => {
        if (!signal.aborted) logger.error('Error loading species summary:', error);
      })
      .finally(() => {
        if (!signal.aborted) loading.summary = false;
      });

    void loadReviewCounts(det.scientificName, signal);
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

    cancelAttributionRetry();
    attributionController = renew(attributionController);
    const controller = attributionController;
    const { signal } = controller;

    try {
      const url = buildAppUrl(
        `/api/v2/media/species-image/info?name=${encodeURIComponent(scientificName)}`
      );
      const response = await fetch(url, { signal });
      if (signal.aborted) return;
      if (response.ok) {
        const body: unknown = await response.json();
        if (signal.aborted) return;
        imageAttribution = parseImageAttribution(body);
        return;
      }
      // 503 means the image is still being resolved in the background, not that
      // it has no attribution. Without a retry a CC-BY photo that the thumbnail's
      // own retry recovers seconds later would be shown with no author or licence
      // credit, which is a licensing problem and not merely a cosmetic one.
      if (
        response.status === HTTP_SERVICE_UNAVAILABLE &&
        attempt < ATTRIBUTION_RETRY_DELAYS_MS.length
      ) {
        const delay = ATTRIBUTION_RETRY_DELAYS_MS.at(attempt) ?? 0;
        attributionRetryTimer = globalThis.setTimeout(() => {
          attributionRetryTimer = undefined;
          if (!signal.aborted) void fetchImageAttribution(scientificName, attempt + 1);
        }, delay);
        // The finally block still releases the controller; the pending retry is
        // cancelled through attributionRetryTimer (cancelAttributionRetry).
        return;
      }
    } catch (error) {
      if (isAbort(error, signal)) return;
      // Attribution is non-critical - fail silently
    } finally {
      if (attributionController === controller) {
        attributionController = null;
      }
    }
  }
</script>

<!-- A skeleton line: one bar inside a box of the loaded line's height. -->
{#snippet line(boxClass: string, barClass: string)}
  <div class="flex items-center {boxClass}"><Skeleton className={barClass} /></div>
{/snippet}

<div class="col-span-12 dd-page">
  <!-- Announces the loaded detection. While it loads, the visible label in the
       skeleton says so. -->
  <div role="status" class="sr-only">
    {#if !isLoadingDetection && detection}
      {t('detections.aria.loaded', { species: displayName })}
    {/if}
  </div>

  {#if detection}
    <DetailNavBar
      speciesName={displayName}
      date={detection.date}
      time={detection.time}
      {neighbors}
      failed={neighborsFailed}
      available={steppingAvailable}
    />

    <!-- While the next detection loads, the previous one stays on screen, dimmed and
         inert, so no review lands on a detection the user has left. -->
    <div
      class="dd-grid dd-grid-top"
      class:dd-switching={isLoadingDetection}
      inert={isLoadingDetection}
      aria-busy={isLoadingDetection}
    >
      <div class="dd-column">
        <IdentityCard
          {detection}
          {displayName}
          {taxonomy}
          {rarity}
          {imageAttribution}
          {sunOffset}
          {predictionCount}
          onShowPredictions={() => (speciesDialogOpen = true)}
        />
        <!-- One panel per detection: a draft, an error or a save still out belongs to the
             detection it was made on and does not carry over to the next. -->
        {#key detection.id}
          <ReviewPanel
            {detection}
            speciesName={displayName}
            {canReview}
            {speciesBlockedReason}
            grow
            onWrongSpecies={() => (speciesDialogOpen = true)}
            onChanged={onSaved}
          />
        {/key}
      </div>
      <ConditionsCard
        {detection}
        weather={shownWeather}
        sun={shownSun}
        {hourlyActivity}
        weatherLoading={loading.weather || loading.sun}
        activityLoading={loading.hourly}
        speciesName={displayName}
      />
    </div>

    <!-- The clip, or the kept spectrogram image after retention removed its audio -->
    {#if detection.clipName || detection.spectrogramOnly}
      <RecordingCard {detection} speciesName={displayName} />
    {/if}

    <div
      class="dd-grid"
      class:dd-switching={isLoadingDetection}
      inert={isLoadingDetection}
      aria-busy={isLoadingDetection}
    >
      <div class="dd-column">
        <ModelsCard contributions={detection.modelContributions} />
        {#key detection.id}
          <ActivityLog {detection} speciesName={displayName} {canReview} onChanged={onSaved} />
        {/key}
      </div>
      <div class="dd-column">
        <SpeciesStatsCard
          {detection}
          speciesName={displayName}
          {rarity}
          summary={speciesSummary}
          daily={dailyCounts}
          dailyDays={DAILY_WINDOW_DAYS}
          reviews={reviewCounts}
          {loading}
        />
      </div>
    </div>

    <!-- One dialog per detection, like the review panels: a save still out belongs to
         the detection it was made on. -->
    {#key detection.id}
      <SpeciesChangeDialog
        isOpen={speciesDialogOpen}
        {detection}
        speciesName={displayName}
        {alternates}
        {predictionModel}
        blockedReason={speciesBlockedReason}
        onClose={() => (speciesDialogOpen = false)}
        onChanged={onSaved}
      />
    {/key}
  {:else if isLoadingDetection}
    <!-- First load: the loaded page's grid and card chrome, with bars for content. The
         one visible loading label sits where the breadcrumb will be. -->
    <div class="dd-skeleton-nav">
      <SkeletonStatus label={t('detections.aria.loading')} className="text-sm" />
    </div>
    <div class="dd-grid dd-grid-top">
      <div class="dd-column">
        <DetailCard padding="even">
          <div class="flex gap-5">
            <!-- The photo is 13.5rem wide, 10.5rem in a window of 900 px or less (IdentityCard). -->
            <Skeleton className="aspect-[4/3] w-54 shrink-0 max-[900px]:w-42" />
            <div class="flex min-w-0 flex-1 flex-col justify-between">
              <div>
                {@render line('h-9', 'h-7 w-56')}
                {@render line('h-6', 'h-4 w-40')}
                {@render line('mt-1 h-4.5', 'h-3 w-64')}
              </div>
              {@render line('h-6.5', 'h-5 w-48 rounded-full')}
              {@render line('h-8', 'h-8 w-36')}
            </div>
          </div>
          <div class="mt-3 border-t border-[var(--border-100)] pt-2.5">
            {@render line('h-6.5', 'h-4 w-3/4')}
          </div>
        </DetailCard>
        <DetailCard title={t('detections.detail.review.title')} padding="snug" grow={true}>
          {@render line('h-6', 'h-4 w-48')}
          <div class="mt-2 flex gap-2">
            {#each ['w-28', 'w-32', 'w-36'] as width (width)}
              <Skeleton className="h-9.5 {width}" />
            {/each}
          </div>
          <div class="mt-3 border-t border-[var(--border-100)] pt-2.5">
            {@render line('h-10', 'h-5 w-64')}
          </div>
        </DetailCard>
      </div>
      <DetailCard title={t('detections.detail.conditions.title')} fill={true}>
        <Skeleton className="min-h-22 w-full flex-1" />
        {@render line('mt-1 h-15', 'h-3 w-3/4')}
        <div class="mt-2.5 border-t border-[var(--border-100)] pt-2.5">
          {@render line('h-14', 'h-10 w-40')}
        </div>
        <div class="mt-2.5 grid grid-cols-3 gap-1.5">
          {#each { length: 6 } as _, index (index)}
            <Skeleton className="h-11.5 w-full rounded-lg" />
          {/each}
        </div>
        {@render line('mt-2 h-4', 'h-2.5 w-48')}
      </DetailCard>
    </div>
    <DetailCard title={t('detections.media.title')}>
      <Skeleton className="aspect-[2/1] w-full" />
    </DetailCard>
  {:else if detectionError}
    <DetailCard padding="even">
      <ErrorAlert message={detectionError} />
    </DetailCard>
  {/if}
</div>

<style>
  /* The page is the container its layout switches on: the width it gets, not the
     window's, which also holds the sidebar when it is open. */
  .dd-page {
    display: flex;
    flex-direction: column;
    gap: 1rem;
    width: 100%;
    min-width: 0;
    container: dd-page / inline-size;
  }

  /* The page's two-column rows: the top row (identity and review beside time and
     conditions) and the lower row (models and activity log beside species
     statistics). In the top row the left column sets the height with content that
     is the same for every detection, and the conditions card fills that height
     with its sky strip, so neither column ends in an empty tail. */
  .dd-grid {
    display: grid;
    grid-template-columns: minmax(0, 1.5fr) minmax(21rem, 1fr);
    gap: 1rem;
    align-items: start;
  }

  .dd-grid-top {
    align-items: stretch;
  }

  .dd-column {
    display: flex;
    flex-direction: column;
    gap: 1rem;
    min-width: 0;
  }

  /* Shown while the next detection loads over the current one. */
  .dd-switching {
    opacity: 0.6;
  }

  @media (prefers-reduced-motion: no-preference) {
    .dd-switching {
      transition: opacity 150ms ease;
    }
  }

  /* The navigation bar's line: 2rem step links inside a 1px border. */
  .dd-skeleton-nav {
    display: flex;
    align-items: center;
    height: calc(2rem + 2px);
  }

  /* Page breakpoint: one column when the page is narrower than 64rem. Below that the
     left column leaves the identity card too little room for a species name, its chips
     and the review buttons on one row. DetailNavBar puts its trail and step links on
     separate lines at the same width. */
  @container dd-page (width < 64rem) {
    .dd-grid {
      grid-template-columns: minmax(0, 1fr);
    }
  }
</style>
