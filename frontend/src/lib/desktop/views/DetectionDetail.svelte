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
    DETAIL_SHORTCUT_IDS,
    DETAIL_SHORTCUT_SCOPE,
    createDetailShortcuts,
  } from '$lib/desktop/features/detections/components/detail/detailShortcuts';
  import {
    isDailyCounts,
    isDetectionDetail,
    isDetectionNeighbors,
    isDetectionPredictions,
    isDetectionWeather,
    isHourlyBuckets,
    isSpeciesRarity,
    isSpeciesStats,
    isSunTimes,
    isTaxonomy,
    parseImageAttribution,
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
    ReviewControls,
    SavedChange,
    SpeciesRarity,
    SpeciesStats,
    SunTimes,
    TaxonomyHierarchy,
  } from '$lib/desktop/features/detections/components/detail/detailTypes';
  import { t } from '$lib/i18n';
  import { navigation } from '$lib/stores/navigation.svelte';
  import { shortcuts } from '$lib/stores/shortcuts.svelte';
  import type { ImageAttribution } from '$lib/types/detection.types';
  import type { PlaybackControls } from '$lib/utils/audio';
  import { hasReviewPermission } from '$lib/utils/auth';
  import { isPlainObject } from '$lib/utils/security';
  import { getLocalDateString } from '$lib/utils/date';
  import { loggers } from '$lib/utils/logger';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { useShortcuts } from '$lib/utils/useShortcuts.svelte';

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
  let speciesStats = $state<SpeciesStats | null>(null);
  // The species whose data the page holds; empty before the first load.
  let speciesDataFor = '';

  // Which requests are out, so a card can tell "still loading" from "not recorded".
  let loading = $state({
    weather: false,
    sun: false,
    hourly: false,
    rarity: false,
    stats: false,
    daily: false,
  });
  type LoadingKey = keyof typeof loading;

  // True when the neighbours request failed for the detection shown.
  let neighborsFailed = $state(false);
  // Whether the server runs the current database format; null until a route that
  // depends on it has answered. Kept across detections: it does not change.
  let enhancedDatabase = $state<boolean | null>(null);
  // The server can step between detections, except on the legacy database format,
  // where the navigation groups are left out.
  let steppingAvailable = $derived(enhancedDatabase !== false);

  // AbortControllers, one per lifetime:
  // - detection: the detection itself;
  // - related: weather and sun times, tied to the detection and its date;
  // - lists: neighbours, predictions and daily counts, which also change with the species;
  // - species and attribution: tied to the species, kept while stepping within one species;
  // - stats and hourly: the species statistics and hourly activity, also asked for again
  //   after a save changed them.
  let detectionController: AbortController | null = null;
  let relatedController: AbortController | null = null;
  let listsController: AbortController | null = null;
  let speciesController: AbortController | null = null;
  let statsController: AbortController | null = null;
  let hourlyController: AbortController | null = null;
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
    const times = sunEventTimes(shownSun, shownWeather, detection.timestamp);
    return nearestSunEvent(
      detectionMinute(detection),
      wallClockMinutes(times.sunrise),
      wallClockMinutes(times.sunset)
    );
  });

  // Keyboard shortcuts. The review panel and the player hand over their controls on mount;
  // these are plain variables, read only when a key is pressed and never by the template.
  let reviewControls: ReviewControls | null = null;
  let playbackControls: PlaybackControls | null = null;

  // A control set is cleared only while the slot still holds it, so the teardown of a panel
  // replaced by the next detection's cannot clear the new panel's controls.
  function attachReviewControls(controls: ReviewControls): () => void {
    reviewControls = controls;
    return () => {
      if (reviewControls === controls) reviewControls = null;
    };
  }

  function attachPlaybackControls(controls: PlaybackControls): () => void {
    playbackControls = controls;
    return () => {
      if (playbackControls === controls) playbackControls = null;
    };
  }

  useShortcuts({
    id: DETAIL_SHORTCUT_SCOPE,
    label: () => t('pageTitle.detectionDetails'),
    definitions: createDetailShortcuts({
      settled: () => detection !== null && !isLoadingDetection,
      loading: () => isLoadingDetection,
      canReview: () => canReview,
      steppingAvailable: () => steppingAvailable,
      neighbors: () => neighbors,
      neighborsFailed: () => neighborsFailed,
      speciesName: () => displayName,
      review: () => reviewControls,
      playback: () => playbackControls,
      go: path => navigation.navigate(buildAppUrl(path)),
    }),
  });

  // How each shortcut is shown on the controls; null while shortcuts are off.
  let stepHints = $derived({
    older: shortcuts.hint(DETAIL_SHORTCUT_IDS.older),
    newer: shortcuts.hint(DETAIL_SHORTCUT_IDS.newer),
    olderSpecies: shortcuts.hint(DETAIL_SHORTCUT_IDS.olderSpecies),
    newerSpecies: shortcuts.hint(DETAIL_SHORTCUT_IDS.newerSpecies),
  });
  let reviewHints = $derived({
    correct: shortcuts.hint(DETAIL_SHORTCUT_IDS.correct),
    falsePositive: shortcuts.hint(DETAIL_SHORTCUT_IDS.falsePositive),
    wrongSpecies: shortcuts.hint(DETAIL_SHORTCUT_IDS.wrongSpecies),
    keep: shortcuts.hint(DETAIL_SHORTCUT_IDS.keep),
  });
  let playHint = $derived(shortcuts.hint(DETAIL_SHORTCUT_IDS.playPause));

  // Only alphanumeric characters, hyphens and underscores are valid in a detection ID.
  function isValidDetectionId(id: string): boolean {
    return /^[a-zA-Z0-9_-]+$/.test(id);
  }

  // The server reads "007" as detection 7, and the page compares ids as strings (the
  // answers it keeps, the detection a save was made on), so a numeric id is written
  // the way the server numbers it.
  function canonicalDetectionId(id: string): string {
    return /^\d+$/.test(id) ? String(Number(id)) : id;
  }

  // The detection's id: the prop, or else the address. A missing or invalid id leaves
  // nothing to load, so the page drops what it shows and says why instead of loading.
  $effect(() => {
    let candidate = detectionIdProp;
    if (!candidate) {
      const pathParts = window.location.pathname.split('/');
      const detectionIndex = pathParts.indexOf('detections');
      candidate = detectionIndex === -1 ? undefined : pathParts.at(detectionIndex + 1);
    }
    if (candidate && isValidDetectionId(candidate)) {
      resolvedDetectionId = canonicalDetectionId(candidate);
      return;
    }
    resolvedDetectionId = undefined;
    detection = null;
    isLoadingDetection = false;
    detectionError = t('detections.errors.noIdProvided');
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
      statsController?.abort();
      hourlyController?.abort();
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
   *
   * Resolves false when the request failed, true when it loaded or a newer load took
   * its place.
   */
  async function fetchDetection(id: string, refresh: boolean): Promise<boolean> {
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
      if (signal.aborted) return true;
      if (!response.ok) {
        throw new Error(errorMessageForStatus(response.status));
      }
      const body: unknown = await response.json();
      if (signal.aborted) return true;
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
      return true;
    } catch (error) {
      if (isAbort(error, signal)) return true;
      logger.error('Error fetching detection:', error);
      if (refresh) return false;
      detection = null;
      detectionError =
        error instanceof Error ? error.message : t('detections.errors.loadFailed', { status: '' });
      return false;
    } finally {
      // Only the request that still owns the controller may reset shared state.
      if (detectionController === controller) {
        isLoadingDetection = false;
        detectionController = null;
      }
    }
  }

  /**
   * A change was saved on a detection. The statistics of the species it touched (its
   * species, and for a species change also the new one) are loaded again when the page
   * holds that species' data, whichever detection is shown now. After a species change
   * the page holds no species until the changed detection's reload lands, so a reload that
   * fails or is cut short by a step leaves the next detection to load its species data
   * afresh. The detection itself is reloaded only when it is the one the page is on:
   * - shown and settled: reloaded in place, keeping what is on screen if that fails;
   * - being stepped to: the step's request may have been sent before the save went
   *   through, so the whole step starts again, with its sun times and daily counts;
   * - already left: nothing to reload, a step brings its own detection.
   * Resolves false when the reload failed, so a panel can show what the page holds.
   */
  function onSaved(change: SavedChange): Promise<boolean> {
    const touched = [change.scientificName, change.changedTo];
    if (change.speciesStats && speciesDataFor && touched.includes(speciesDataFor)) {
      loadStats(speciesDataFor);
      // A species change moved a detection into or out of the species on screen, so its
      // hourly activity changed too.
      if (change.changedTo) loadHourly(speciesDataFor);
    }
    if (!resolvedDetectionId || String(change.detectionId) !== resolvedDetectionId) {
      return Promise.resolve(true);
    }
    if (change.changedTo) speciesDataFor = '';
    return fetchDetection(resolvedDetectionId, !isLoadingDetection);
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
   * to error tracking, and these routes answer 404 (no weather for the time, a species
   * the lookup does not know) and 409 (statistics on the legacy database) in normal use.
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
      value => {
        // eslint-disable-next-line security/detect-object-injection -- id passed isValidDetectionId
        weatherAnswers[id] = value;
      },
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
      value => {
        // eslint-disable-next-line security/detect-object-injection -- date comes from the detection's date field
        sunAnswers[date] = value;
      },
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
      enhancedDatabase = true;
    } catch (error) {
      if (isAbort(error, signal)) return;
      logger.error('Error loading detection neighbours:', error);
      neighborsFailed = true;
    }
  }

  /**
   * The species' all-time statistics. A newer request (after a verdict, or for another
   * species) cancels the one still out, so the latest answer is the one shown; the
   * statistics on screen stay until it arrives, and stay when it fails. A change of
   * species clears them before asking (loadSpeciesData).
   */
  function loadStats(scientificName: string) {
    statsController = renew(statsController);
    load(
      `/api/v2/species/stats?scientific_name=${encodeURIComponent(scientificName)}`,
      statsController.signal,
      isSpeciesStats,
      value => {
        speciesStats = value ?? speciesStats;
      },
      'stats'
    );
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
    speciesStats = null;

    const species = encodeURIComponent(det.scientificName);

    load(
      `/api/v2/species?scientific_name=${species}`,
      signal,
      isPlainObject,
      value => (rarity = isSpeciesRarity(value?.rarity) ? value.rarity : null),
      'rarity'
    );
    load(
      `/api/v2/species/taxonomy?scientific_name=${species}`,
      signal,
      isPlainObject,
      value => (taxonomy = isTaxonomy(value?.taxonomy) ? value.taxonomy : null)
    );
    loadHourly(det.scientificName);
    loadStats(det.scientificName);
    void fetchImageAttribution(det.scientificName);
  }

  /**
   * The species' detections by hour of day, all time. Like loadStats, a newer request (after
   * a change, or for another species) cancels the one still out, so the latest answer is
   * the one shown; the activity on screen stays until it arrives, and stays when it fails.
   * A change of species clears it before asking (loadSpeciesData).
   */
  function loadHourly(scientificName: string) {
    hourlyController = renew(hourlyController);
    const species = encodeURIComponent(scientificName);
    load(
      `/api/v2/analytics/time/distribution/hourly?start_date=${ACTIVITY_START_DATE}&end_date=${getLocalDateString()}&species=${species}`,
      hourlyController.signal,
      isHourlyBuckets,
      value => {
        hourlyActivity = value ?? hourlyActivity;
      },
      'hourly'
    );
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
      stale={isLoadingDetection}
      shortcutHints={stepHints}
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
            onControls={attachReviewControls}
            shortcutHints={reviewHints}
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

    <!-- The clip, or the kept spectrogram image after retention removed its audio.
         Dimmed and inert with the grids while the next detection loads. -->
    {#if detection.clipName || detection.spectrogramOnly}
      <div
        class:dd-switching={isLoadingDetection}
        inert={isLoadingDetection}
        aria-busy={isLoadingDetection}
      >
        <RecordingCard
          {detection}
          speciesName={displayName}
          onPlaybackControls={attachPlaybackControls}
          playShortcut={playHint}
        />
      </div>
    {/if}

    <div
      class="dd-grid"
      class:dd-switching={isLoadingDetection}
      inert={isLoadingDetection}
      aria-busy={isLoadingDetection}
    >
      <div class="dd-column">
        <ModelsCard contributions={detection.modelContributions} detectedAt={detection.timestamp} />
        {#key detection.id}
          <ActivityLog {detection} speciesName={displayName} {canReview} onChanged={onSaved} />
        {/key}
      </div>
      <div class="dd-column">
        <SpeciesStatsCard
          {detection}
          speciesName={displayName}
          {rarity}
          stats={speciesStats}
          daily={dailyCounts}
          dailyDays={DAILY_WINDOW_DAYS}
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
            <div class="flex min-w-0 flex-1 flex-col justify-between gap-2">
              <div class="flex justify-between gap-4">
                <div class="min-w-0">
                  {@render line('h-9', 'h-7 w-56')}
                  {@render line('h-6', 'h-4 w-40')}
                  {@render line('mt-1 h-4.5', 'h-3 w-64')}
                </div>
                <!-- The confidence ring and its label, which set the height of this row. -->
                <div class="flex shrink-0 flex-col items-center gap-1">
                  <Skeleton className="size-18 rounded-full" />
                  {@render line('h-3.75', 'h-2.5 w-14')}
                </div>
              </div>
              {@render line('h-6.5', 'h-5 w-48 rounded-full')}
              {@render line('h-7.5', 'h-7.5 w-36')}
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

    /* The navigation bar's two lines at this width: the trail (a line of 0.875rem text in
       a link with 0.25rem padding), a 0.5rem gap, then the step links. */
    .dd-skeleton-nav {
      align-items: flex-start;
      height: calc(0.875rem * 1.5 + 0.5rem + 0.5rem + 2rem + 2px);
      padding-top: 0.25rem;
    }
  }
</style>
