<!--
  Analysis Settings Page Component

  Purpose: Configure BirdNET-Go analysis settings including detection thresholds,
  false positive filtering, range filter, dynamic threshold, and manage the
  model gallery (install/uninstall additional classifier models).

  Features:
  - Two main tabs: Settings and Models
  - Settings tab: Detection settings, false positive filter, range filter,
    dynamic threshold, and advanced options
  - Models tab: Model gallery with Installed and Available tabs
  - Confidence threshold slider for bird detection
  - Bat detection threshold slider (visible when a bat model is installed)
  - Locale selector with flag icons for species labels
  - False positive filter with colored level badge
  - Range filter with species count, view/download functionality
  - Dynamic threshold with enable/disable and parameter tuning
  - Advanced section with processing threads and custom classifier paths
  - License acceptance dialog for model installation
  - Remove confirmation dialog for model uninstallation
  - Real-time download progress via SSE

  Props: None - This is a page component that uses global settings stores

  @component
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import { downloadBlob } from '$lib/utils/fileHelpers';
  import type {
    CatalogEntry,
    CatalogVariant,
    DownloadProgress,
    InstalledModel,
    ModelRegionsResponse,
  } from '$lib/types/models';
  import {
    fetchCatalog,
    fetchInstalled,
    fetchModelRegions,
    installModel,
    reinstallModel,
    uninstallModel,
    subscribeInstallProgress,
    isNetworkDownloadError,
  } from '$lib/utils/modelsApi';
  import { invalidateModels } from '$lib/stores/models.svelte';
  import SettingsTabs from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import type { TabDefinition } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
  import ModelVariantPicker from '$lib/desktop/features/settings/components/ModelVariantPicker.svelte';
  import ModelRegionSelector from '$lib/desktop/features/settings/components/ModelRegionSelector.svelte';
  import NumberField from '$lib/desktop/components/forms/NumberField.svelte';
  import FalsePositiveFilterControl, {
    type FilterLevel,
  } from '$lib/desktop/components/forms/FalsePositiveFilterControl.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { SelectOption } from '$lib/desktop/components/forms/SelectDropdown.types';
  import FlagIcon, { type FlagLocale } from '$lib/desktop/components/ui/FlagIcon.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import {
    settingsStore,
    settingsActions,
    birdnetSettings,
    dynamicThresholdSettings,
    realtimeSettings,
    batSettings,
    perchSettings,
    birdNetV3Settings,
  } from '$lib/stores/settings';
  import { cn } from '$lib/utils/cn.js';
  import { api, ApiError, getCsrfToken } from '$lib/utils/api';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { toastActions } from '$lib/stores/toast';
  import { formatBytes, formatNumber } from '$lib/utils/formatters';
  import {
    pickPreselectedVariant,
    translateReason,
    normalizeRegionMode,
    DEFAULT_REGION_MODE,
    GLOBAL_REGION_MODE,
    optimizeOffers,
    variantHardwareLabel,
    CHANNEL_PREVIEW,
    type OptimizeOffer,
  } from '$lib/utils/variantSelection';
  import OptimizeReviewDialog from '$lib/desktop/features/settings/components/OptimizeReviewDialog.svelte';
  import { safeArrayAccess } from '$lib/utils/security';
  import { loggers } from '$lib/utils/logger';
  import { t } from '$lib/i18n';
  import {
    Download,
    Trash2,
    Shield,
    ShieldAlert,
    Package,
    BrainCircuit,
    AlertTriangle,
    TriangleAlert,
    Loader2,
    RefreshCw,
    Radar,
    Globe,
    XCircle,
    X,
    Check,
    Sparkles,
    Settings as SettingsIcon,
  } from '@lucide/svelte';

  import logoBirdnet from '$lib/assets/logos/logo-birdnet.png';
  import logoGoogle from '$lib/assets/logos/logo-google.png';
  import logoJyu from '$lib/assets/logos/logo-jyu.jpeg';

  const logger = loggers.settings;

  // Shown as the endpoint placeholder. Mirrors conf.DefaultHuggingFaceEndpoint
  // by hand; nothing enforces it, but it is only placeholder text, so drift
  // would be cosmetic.
  const DEFAULT_HUGGINGFACE_ENDPOINT = 'https://huggingface.co';

  const MODEL_LOGOS: Record<string, string> = {
    birdnet: logoBirdnet,
    perch: logoGoogle,
    bsg: logoJyu,
  };

  function getModelLogo(id: string): string | null {
    for (const [prefix, logo] of Object.entries(MODEL_LOGOS)) {
      if (id.startsWith(prefix)) return logo;
    }
    return null;
  }

  // Render an entry-level incompatibility code (e.g. "backend.onnx_unavailable")
  // through the same i18n reason path the variant picker uses, falling back to a
  // generic localized line when the code is absent or has no translation, so a
  // structured code never surfaces to the user as a raw dotted string. The
  // fallback is cause-neutral on purpose: it is only reached when the code is
  // missing or unmapped, which is exactly when the specific cause is not known.
  // Entry-level codes carry no interpolation args (unlike variant reasons), so
  // undefined args are passed; a future parameterized entry-level code would need
  // an args field on CatalogEntryResponse.IncompatibleReason and a change here.
  function entryIncompatibleText(code: string | undefined): string {
    const fallback = t('analysis.gallery.entryIncompatible');
    return code ? translateReason(code, undefined, fallback) : fallback;
  }

  // ── Page-level tab state ──────────────────────────────────────────────
  type PageTab = 'settings' | 'models';
  let pageTab = $state<PageTab>('settings');

  // ── Gallery (Models tab) state ────────────────────────────────────────
  let catalog = $state<CatalogEntry[]>([]);
  // Installed models incl. hidden ones (drives the secondary-model threshold
  // sections, which the visibility-filtered catalog cannot).
  let installedModels = $state<InstalledModel[]>([]);
  let loading = $state(true);
  // Catalog loading failure: gates the two gallery tab bodies and drives the
  // banner whose Retry re-runs loadCatalog.
  let catalogError = $state<string | null>(null);

  // A failed per-model action (install, reinstall, remove). Rendered as a
  // dismissible banner above the gallery tabs so it never replaces the grid, and
  // so switching tabs cannot strand it. Carries enough to offer a real Retry and,
  // for a download-reachability failure, a pointer to the Download Source setting.
  type GalleryActionKind = 'install' | 'reinstall' | 'remove';
  interface GalleryActionError {
    modelId: string;
    modelName: string;
    kind: GalleryActionKind;
    message: string; // raw backend/SSE/ApiError text, kept inspectable
    variantId?: string; // reused when retrying an install
    network: boolean; // download could not reach the model host
  }
  let installError = $state<GalleryActionError | null>(null);

  let installingId = $state<string | null>(null);
  let deletingId = $state<string | null>(null);
  let reinstallingId = $state<string | null>(null);
  let downloadProgress = $state<DownloadProgress | null>(null);
  let completionTimer: ReturnType<typeof setTimeout> | undefined;

  // One shared "any gallery action in flight" predicate. Install and reinstall
  // share the single downloadProgress state and progressCleanup subscription, so
  // starting a second action while one runs can strand the first's UI state; every
  // action gate uses this rather than an ad-hoc subset of the three ids.
  const galleryActionInFlight = $derived(
    installingId !== null || reinstallingId !== null || deletingId !== null
  );

  // Shared DOM id for the "actions paused" status line rendered while any gallery
  // action is in flight. Every cross-action-disabled button points its
  // aria-describedby here so a keyboard/screen-reader user hears why it is
  // blocked; the line is rendered exactly when a button references it.
  const GALLERY_ACTION_STATUS_ID = 'gallery-action-status';

  // Model regions, for localized region names in variant labels and the
  // region-aware picker. Fetched once on mount; a failure degrades to raw slugs.
  let regionsData = $state<ModelRegionsResponse | null>(null);
  // Monotonic sequence guarding loadCatalog against out-of-order responses: a slow
  // in-flight fetch must not overwrite a newer one (a region-change re-fetch, or
  // the Retry button). Plain let: intentionally untracked.
  let catalogRequestSeq = 0;
  // The saved region the loaded catalog reflects, so the region effect below
  // re-fetches only when a save actually changes it. Plain let: intentionally
  // untracked, so mutating it never re-runs the effect.
  let catalogLoadedRegion: string | null = null;

  // Shared DOM id for the Download Source endpoint input, referenced both by the
  // input's own id and by scrollToDownloadSource's getElementById lookup, so the
  // two cannot drift apart.
  const HUGGINGFACE_ENDPOINT_ID = 'huggingface-endpoint';

  let licenseModel = $state<CatalogEntry | null>(null);
  // The variant preselected in the license/install dialog: the server-recommended
  // one by default, overridable by the user. Empty for flat (variant-less) entries.
  let selectedVariantId = $state('');
  // The download-size row and the install target follow the selected variant when
  // the entry has variants; otherwise they fall back to the whole-entry size.
  const licenseSelectedVariant = $derived(
    licenseModel?.variants?.find(v => v.id === selectedVariantId) ?? null
  );
  const licenseDownloadSize = $derived(
    licenseSelectedVariant?.sizeBytes ?? licenseModel?.totalSizeBytes ?? 0
  );
  // Block install when the selected variant cannot run on this host (e.g. every
  // variant is incompatible, so the preselected one is blocked). The default
  // path preselects a compatible variant, so this only fires in the all-blocked case.
  const installBlocked = $derived(
    licenseSelectedVariant != null && !licenseSelectedVariant.compatible
  );
  let removeConfirmModel = $state<CatalogEntry | null>(null);

  // Element bindings should NOT use $state - causes showModal() to fail
  let licenseDialogRef: HTMLDialogElement | null = null;
  let removeDialogRef: HTMLDialogElement | null = null;

  type GalleryTab = 'installed' | 'available';
  let galleryTab = $state<GalleryTab>('installed');

  // ── Store-derived state ───────────────────────────────────────────────
  let store = $derived($settingsStore);
  let birdnet = $derived($birdnetSettings);

  // ── Region-aware picker wiring ────────────────────────────────────────
  // slug -> localized name, from the same regions endpoint the region selector
  // uses, so the picker labels a region exactly as the selector names it. Guarded
  // against a missing or still-loading response.
  const regionNameMap = $derived(new Map((regionsData?.regions ?? []).map(r => [r.slug, r.name])));
  // The live selected region mode from the unsaved form store (mirrors the
  // ModelRegionSelector), so the picker's region scoping tracks a selector click
  // instantly, before any save. Note: until a save the server-computed
  // recommended flags still reflect the SAVED region, so the recommended variant
  // may lag the live selection; the effect below re-fetches on save to reconcile.
  const liveModelRegion = $derived(normalizeRegionMode(birdnet?.modelRegion));
  const activeRegionSlug = $derived.by<string>(() => {
    if (!regionsData) return '';
    if (liveModelRegion === GLOBAL_REGION_MODE) return '';
    if (liveModelRegion === DEFAULT_REGION_MODE)
      return regionsData.locationConfigured ? regionsData.resolved.slug : '';
    return regionNameMap.has(liveModelRegion) ? liveModelRegion : '';
  });
  // The SAVED region the catalog's recommendation flags were computed from. Those
  // flags come from the persisted setting (resolveRecommendRegion on the server),
  // so the picker's server-side recommendation only needs a re-fetch when this
  // changes, not on unsaved toggles.
  const savedModelRegion = $derived.by<string | null>(() => {
    const b = store.originalData.birdnet;
    if (!b) return null; // settings not loaded yet
    return normalizeRegionMode(b.modelRegion);
  });

  // Re-fetch the catalog when the SAVED region changes mid-session (a save), so
  // the server-computed recommendation flags follow the region selector. Unsaved
  // selector toggles do not fire this (they only move activeRegionSlug, which
  // scopes the picker client-side); the first observation after settings load does
  // not either (onMount already loaded the catalog for the persisted region). The
  // only tracked read is savedModelRegion, a primitive $derived, so Svelte re-runs
  // this only when its value actually changes; the catalogLoadedRegion guard is
  // belt-and-braces.
  $effect(() => {
    const region = savedModelRegion;
    if (region === null) return;
    if (catalogLoadedRegion === null) {
      catalogLoadedRegion = region; // onMount already loaded this region's catalog
      return;
    }
    if (region === catalogLoadedRegion) return;
    catalogLoadedRegion = region;
    loadCatalog();
  });
  let dynamicThreshold = $derived(
    $dynamicThresholdSettings ?? {
      enabled: false,
      debug: false,
      trigger: 0.8,
      min: 0.3,
      validHours: 24,
    }
  );
  let falsePositiveFilter = $derived($realtimeSettings?.falsePositiveFilter ?? { level: 0 });
  let bat = $derived(
    $batSettings ?? {
      enabled: false,
      threshold: 0.5,
      filterEnabled: false,
      nighttimeOnly: true,
      falsePositiveFilter: { level: 0 },
      ultrasonicFilter: { enabled: true },
    }
  );

  // Check if a bat model is installed
  const hasBatModel = $derived(catalog.some(e => e.installed && e.category === 'bat'));
  const batFPLevel = $derived(bat.falsePositiveFilter?.level ?? 0);

  // Secondary acoustic classifiers with a per-model threshold override. These are
  // gated on the installed-models list (not the catalog): the catalog endpoint
  // hides foundation/preview models such as BirdNET v3.0 via VisibleCatalog(), so
  // a catalog-based check would never light up the v3.0 section. The installed
  // endpoint returns every downloaded model, hidden or not, keyed by catalogId.
  let perch = $derived($perchSettings ?? { overrideThreshold: false, threshold: 0.5 });
  let birdnetV3 = $derived($birdNetV3Settings ?? { overrideThreshold: false, threshold: 0.5 });
  const hasPerchModel = $derived(installedModels.some(m => m.catalogId.startsWith('perch')));
  const hasBirdNetV3Model = $derived(installedModels.some(m => m.catalogId === 'birdnet-v3.0'));

  // ── Derived catalog views ─────────────────────────────────────────────
  // Installed models, with the permanent built-in classifier (BirdNET v2.4) sorted
  // first so it heads the list where the old hardcoded static card used to sit.
  // JS sort is stable, so the remaining entries keep their catalog order.
  const installedEntries = $derived(
    catalog.filter(e => e.installed).sort((a, b) => Number(!!b.permanent) - Number(!!a.permanent))
  );

  // ── Within-model "optimize" offers ────────────────────────────────────
  // Derived entirely client-side from the catalog: an installed model whose
  // host-recommended variant differs from the installed one and is compatible.
  const offers = $derived(optimizeOffers(catalog));
  const offerByEntry = $derived(new Map(offers.map(o => [o.entry.id, o])));

  // Session-scoped banner dismissal, guarded so a private-window/blocked
  // sessionStorage never throws (see frontend/CLAUDE.md).
  const OPTIMIZE_BANNER_DISMISS_KEY = 'birdnet.optimizeBannerDismissed';
  function readOptimizeDismissed(): boolean {
    try {
      return sessionStorage.getItem(OPTIMIZE_BANNER_DISMISS_KEY) === '1';
    } catch {
      return false;
    }
  }
  let optimizeBannerDismissed = $state(readOptimizeDismissed());
  function dismissOptimizeBanner() {
    optimizeBannerDismissed = true;
    try {
      sessionStorage.setItem(OPTIMIZE_BANNER_DISMISS_KEY, '1');
    } catch {
      /* sessionStorage unavailable: dismissal is in-memory only for this session */
    }
  }

  // Review dialog + apply-flow state. Applies reuse the normal install/variant-swap
  // path (startInstall); a within-model swap never changes the license, so no extra
  // consent is needed beyond the review dialog itself.
  let optimizeReviewOpen = $state(false);
  let appliedOptimizeIds = $state(new Set<string>());
  // Entry ids whose optimize swap failed this session (drives the dialog's per-row
  // failed marker); the shared installError banner carries the detail.
  let failedOptimizeIds = $state(new Set<string>());
  // The entry id whose optimize swap is in flight (drives the dialog's per-row
  // spinner) and the sequential apply-all queue of entry ids still to apply.
  let optimizeApplyingId = $state<string | null>(null);
  let applyAllQueue = $state<string[]>([]);
  // Plain (untracked) marker so the completion effect can detect installingId
  // transitioning back to null without re-triggering itself.
  let lastInstallingId: string | null = null;

  function openOptimizeReview() {
    optimizeReviewOpen = true;
  }
  function closeOptimizeReview() {
    optimizeReviewOpen = false;
  }

  // Apply one offer: start the within-model swap to its recommended variant. Clear
  // any prior failed marker for it so a retry shows progress, not the stale failure.
  function applyOffer(offer: OptimizeOffer) {
    if (galleryActionInFlight) return;
    if (failedOptimizeIds.has(offer.entry.id)) {
      const next = new Set(failedOptimizeIds);
      next.delete(offer.entry.id);
      failedOptimizeIds = next;
    }
    optimizeApplyingId = offer.entry.id;
    startInstall(offer.entry.id, offer.entry.name, offer.to.id);
  }

  // Apply every current offer, sequentially: queue their entry ids and start the
  // first; the completion effect below advances the queue as each swap finishes.
  function applyAllOffers() {
    if (galleryActionInFlight) return;
    applyAllQueue = offers.map(o => o.entry.id);
    applyNextInQueue();
  }
  function applyNextInQueue() {
    while (applyAllQueue.length > 0) {
      const nextId = applyAllQueue[0];
      const offer = offerByEntry.get(nextId);
      if (offer && !appliedOptimizeIds.has(nextId)) {
        optimizeApplyingId = nextId;
        startInstall(offer.entry.id, offer.entry.name, offer.to.id);
        return;
      }
      applyAllQueue = applyAllQueue.slice(1);
    }
  }

  // Detect an optimize swap completing (installingId returns to null after being
  // set to the applying id): mark it applied on success, then advance the batch
  // queue. A failed swap (installError names the id) is not marked applied but still
  // advances the queue so one failure does not stall the rest.
  $effect(() => {
    const current = installingId;
    if (lastInstallingId !== null && current === null && lastInstallingId === optimizeApplyingId) {
      const done = lastInstallingId;
      optimizeApplyingId = null;
      const failed = installError?.modelId === done;
      if (failed) failedOptimizeIds = new Set(failedOptimizeIds).add(done);
      else appliedOptimizeIds = new Set(appliedOptimizeIds).add(done);
      if (applyAllQueue[0] === done) {
        applyAllQueue = applyAllQueue.slice(1);
        applyNextInQueue();
      }
    }
    lastInstallingId = current;
  });

  // Choose which variant a card should describe: the installed variant on the
  // Installed tab, the host-recommended (else default) variant on the Available tab.
  // Returns null for a flat (variant-less) entry.
  function displayVariantFor(
    entry: CatalogEntry,
    mode: 'available' | 'installed'
  ): CatalogVariant | null {
    const variants = entry.variants ?? [];
    if (variants.length === 0) return null;
    if (mode === 'installed') {
      return variants.find(v => v.id === entry.installedVariantId) ?? null;
    }
    return (
      variants.find(v => v.id === entry.recommendedVariantId) ??
      variants.find(v => v.default) ??
      null
    );
  }

  // The region display for a variant-bearing entry's card: the variant's region
  // name (localized), or the "Global" label when the variant is global.
  function displayRegionName(variant: CatalogVariant | null): string {
    if (!variant?.region) return t('analysis.gallery.regionGlobal');
    return regionNameMap.get(variant.region) ?? variant.region;
  }
  const availableWildlife = $derived(
    catalog.filter(e => !e.installed && e.category === 'wildlife')
  );
  const availableBirds = $derived(catalog.filter(e => !e.installed && e.category === 'bird'));
  const availableBats = $derived(catalog.filter(e => !e.installed && e.category === 'bat'));
  const availableGeomodels = $derived(
    catalog.filter(e => !e.installed && e.category === 'geomodel')
  );

  // ── BirdNET locale loading ────────────────────────────────────────────
  interface BirdnetLocaleOption extends SelectOption {
    localeCode: FlagLocale;
  }

  let birdnetLocales = $state<{
    loading: boolean;
    error: string | null;
    data: Array<{ value: string; label: string }>;
  }>({
    loading: true,
    error: null,
    data: [],
  });

  let birdnetLocaleOptions = $derived<BirdnetLocaleOption[]>(
    birdnetLocales.data.map(locale => ({
      value: locale.value,
      label: locale.label,
      localeCode: locale.value as FlagLocale,
    }))
  );

  async function loadBirdnetLocales() {
    birdnetLocales.loading = true;
    birdnetLocales.error = null;

    try {
      const localesData = await api.get<Record<string, string>>('/api/v2/settings/locales');
      birdnetLocales.data = Object.entries(localesData || {}).map(([value, label]) => ({
        value,
        label: label as string,
      }));
    } catch (err) {
      if (err instanceof ApiError) {
        toastActions.warning(t('settings.main.errors.localesLoadFailed'));
      }
      birdnetLocales.error = t('settings.main.errors.localesLoadFailed');
      birdnetLocales.data = [{ value: 'en', label: 'English' }];
    } finally {
      birdnetLocales.loading = false;
    }
  }

  // ── False Positive Filter helpers ─────────────────────────────────────
  const OVERLAP_COMPARISON_TOLERANCE = 0.001;

  const falsePositiveFilterLevels = [
    {
      value: 0,
      descriptionKey: 'settings.main.sections.falsePositiveFilter.levels.off',
      minOverlap: 0.0,
      threshold: 0.0,
    },
    {
      value: 1,
      descriptionKey: 'settings.main.sections.falsePositiveFilter.levels.lenient',
      minOverlap: 2.0,
      threshold: 0.2,
    },
    {
      value: 2,
      descriptionKey: 'settings.main.sections.falsePositiveFilter.levels.moderate',
      minOverlap: 2.2,
      threshold: 0.3,
    },
    {
      value: 3,
      descriptionKey: 'settings.main.sections.falsePositiveFilter.levels.balanced',
      minOverlap: 2.4,
      threshold: 0.5,
    },
    {
      value: 4,
      descriptionKey: 'settings.main.sections.falsePositiveFilter.levels.strict',
      minOverlap: 2.7,
      threshold: 0.6,
    },
    {
      value: 5,
      descriptionKey: 'settings.main.sections.falsePositiveFilter.levels.maximum',
      minOverlap: 2.8,
      threshold: 0.7,
    },
  ];

  // Constants matching backend: internal/analysis/processor/processor.go
  const CHUNK_DURATION_SECONDS = 3.0;
  const REFERENCE_WINDOW_SECONDS = 6.0;
  const MIN_SEGMENT_LENGTH = 0.1;
  const FLOAT_EPSILON = 1e-9;

  function calculateMinDetections(level: number, overlap: number): number {
    if (level === 0) return 1;

    const levelData = safeArrayAccess(falsePositiveFilterLevels, level);
    if (!levelData) return 1;

    const segmentLength = Math.max(MIN_SEGMENT_LENGTH, CHUNK_DURATION_SECONDS - overlap);
    const maxDetectionsIn6s = REFERENCE_WINDOW_SECONDS / segmentLength;
    const required = maxDetectionsIn6s * levelData.threshold - FLOAT_EPSILON;
    return Math.max(1, Math.ceil(required));
  }

  function getFalsePositiveFilterDescription(level: number, overlap: number): string {
    const levelData = safeArrayAccess(falsePositiveFilterLevels, level);
    if (!levelData) return '';

    const minDet = calculateMinDetections(level, overlap);
    const baseDescription = t(levelData.descriptionKey);

    if (level === 0) return baseDescription;

    return t('settings.main.sections.falsePositiveFilter.detectionCount', {
      count: minDet.toString(),
      description: baseDescription,
    });
  }

  function getMinimumOverlapForLevel(level: number): number {
    return safeArrayAccess(falsePositiveFilterLevels, level)?.minOverlap ?? 0.0;
  }

  function updateFalsePositiveFilterLevel(newLevel: number) {
    const oldLevel = falsePositiveFilter.level;
    const oldMinOverlap = getMinimumOverlapForLevel(oldLevel);
    const newMinOverlap = getMinimumOverlapForLevel(newLevel);
    const currentOverlap = birdnet?.overlap ?? 0;

    settingsActions.updateSection('realtime', {
      falsePositiveFilter: { level: newLevel },
    });

    if (currentOverlap < newMinOverlap) {
      settingsActions.updateSection('birdnet', { overlap: newMinOverlap });
      toastActions.info(
        t('settings.main.sections.falsePositiveFilter.overlapAdjusted', {
          overlap: newMinOverlap.toFixed(1),
        })
      );
    } else if (
      newMinOverlap < oldMinOverlap &&
      Math.abs(currentOverlap - oldMinOverlap) < OVERLAP_COMPARISON_TOLERANCE
    ) {
      settingsActions.updateSection('birdnet', { overlap: newMinOverlap });
      toastActions.info(
        t('settings.main.sections.falsePositiveFilter.overlapReduced', {
          overlap: newMinOverlap.toFixed(1),
        })
      );
    }
  }

  // ── Range filter state and functions ──────────────────────────────────
  interface RangeFilterSpecies {
    commonName?: string;
    scientificName?: string;
    label?: string;
  }

  interface GeomodelStatus {
    version: string;
    totalSpecies: number;
    autoSelected: boolean;
  }

  interface ClassifierCoverage {
    id: string;
    name: string;
    totalSpecies: number;
    withRangeData: number;
    withoutRangeData: number;
  }

  interface RangeFilterStatus {
    geomodel: GeomodelStatus | null;
    classifiers: ClassifierCoverage[];
    passUnmappedSpecies: boolean;
    threshold: number;
    locationConfigured: boolean;
    lastUpdated: string;
  }

  let rangeFilterStatus = $state<RangeFilterStatus | null>(null);

  async function loadRangeFilterStatus() {
    try {
      rangeFilterStatus = await api.get<RangeFilterStatus>('/api/v2/range/status');
    } catch (err) {
      logger.error('Failed to load range filter status:', err);
    }
  }

  let rangeFilterState = $state<{
    speciesCount: number | null;
    loading: boolean;
    testing: boolean;
    downloading: boolean;
    error: string | null;
    showModal: boolean;
    species: RangeFilterSpecies[];
  }>({
    speciesCount: null,
    loading: false,
    testing: false,
    downloading: false,
    error: null,
    showModal: false,
    species: [],
  });

  // Focus management for modal accessibility
  let previouslyFocusedElement: HTMLElement | null = null;

  function getFocusableElements(container: HTMLElement): HTMLElement[] {
    const focusableSelectors = [
      'button:not([disabled])',
      'input:not([disabled])',
      'select:not([disabled])',
      'textarea:not([disabled])',
      'a[href]',
      '[tabindex]:not([tabindex="-1"])',
    ];

    const elements = container.querySelectorAll(focusableSelectors.join(', '));
    return Array.from(elements).filter(el => {
      const style = window.getComputedStyle(el as HTMLElement);
      return style.display !== 'none' && style.visibility !== 'hidden';
    }) as HTMLElement[];
  }

  function handleFocusTrap(event: KeyboardEvent, modal: HTMLElement) {
    if (event.key !== 'Tab') return;

    const focusableElements = getFocusableElements(modal);
    if (focusableElements.length === 0) return;

    const firstElement = focusableElements[0];
    const lastElement = focusableElements[focusableElements.length - 1];

    if (event.shiftKey) {
      if (document.activeElement === firstElement) {
        event.preventDefault();
        lastElement.focus();
      }
    } else {
      if (document.activeElement === lastElement) {
        event.preventDefault();
        firstElement.focus();
      }
    }
  }

  let modalTrapHandler: ((_event: KeyboardEvent) => void) | null = null;
  let modalElement: HTMLElement | null = null;

  $effect(() => {
    let focusTimer: ReturnType<typeof setTimeout> | undefined;

    if (rangeFilterState.showModal) {
      previouslyFocusedElement = document.activeElement as HTMLElement;

      focusTimer = setTimeout(() => {
        const modal = document.querySelector(
          '[role="dialog"][aria-labelledby="modal-title"]'
        ) as HTMLElement;
        if (modal) {
          const focusableElements = getFocusableElements(modal);
          if (focusableElements.length > 0) {
            focusableElements[0].focus();
          } else {
            modal.focus();
          }

          modalElement = modal;
          modalTrapHandler = (event: KeyboardEvent) => handleFocusTrap(event, modal);
          modal.addEventListener('keydown', modalTrapHandler);
        }
      }, 0);
    } else if (previouslyFocusedElement) {
      previouslyFocusedElement.focus();
      previouslyFocusedElement = null;
    }

    return () => {
      clearTimeout(focusTimer);
      if (modalElement && modalTrapHandler) {
        modalElement.removeEventListener('keydown', modalTrapHandler);
        modalElement = null;
        modalTrapHandler = null;
      }
    };
  });

  let debounceTimer: ReturnType<typeof setTimeout> | undefined;
  let loadingDelayTimer: ReturnType<typeof setTimeout> | undefined;
  let rangeFilterAbortController: AbortController | null = null;

  function debouncedTestRangeFilter() {
    rangeFilterAbortController?.abort();
    rangeFilterAbortController = null;
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => {
      testCurrentRangeFilter();
    }, 150);
  }

  async function loadRangeFilterCount() {
    try {
      interface CountResponse {
        count: number;
      }
      const data = await api.get<CountResponse>('/api/v2/range/species/count');
      rangeFilterState.speciesCount = data.count;
    } catch (err) {
      logger.error('Failed to load range filter count:', err);
      rangeFilterState.error = t('settings.main.errors.rangeFilterCountFailed');
    }
  }

  async function testCurrentRangeFilter() {
    if (rangeFilterState.testing || !birdnet?.locationConfigured) return;

    clearTimeout(loadingDelayTimer);

    loadingDelayTimer = setTimeout(() => {
      rangeFilterState.testing = true;
    }, 100);

    rangeFilterState.error = null;
    rangeFilterAbortController = new AbortController();

    try {
      const data = await api.post<{ count: number; species?: RangeFilterSpecies[] }>(
        '/api/v2/range/species/test',
        {
          latitude: birdnet?.latitude,
          longitude: birdnet?.longitude,
          threshold: birdnet?.rangeFilter?.threshold,
        },
        { signal: rangeFilterAbortController.signal }
      );

      rangeFilterState.speciesCount = data.count;

      if (rangeFilterState.showModal) {
        rangeFilterState.species = data.species || [];
      }
    } catch (err) {
      if (err instanceof Error && err.name === 'AbortError') return;
      logger.error('Failed to test range filter:', err);
      rangeFilterState.error = t('settings.main.errors.rangeFilterTestFailed');
      rangeFilterState.speciesCount = null;
    } finally {
      clearTimeout(loadingDelayTimer);
      rangeFilterState.testing = false;
      rangeFilterAbortController = null;
    }
  }

  async function loadRangeFilterSpecies() {
    if (rangeFilterState.loading || !birdnet?.locationConfigured) return;

    rangeFilterState.loading = true;
    rangeFilterState.error = null;

    try {
      const data = await settingsActions.loadRangeFilterSpecies();
      rangeFilterState.species = data.species;
      rangeFilterState.speciesCount = data.count;
    } catch (err) {
      logger.error('Failed to load species list:', err);
      rangeFilterState.error = t('settings.main.errors.rangeFilterLoadFailed');
    } finally {
      rangeFilterState.loading = false;
    }
  }

  // Narrow derived values so the effect only fires when coordinates or threshold change
  const rangeFilterLat = $derived($birdnetSettings?.latitude);
  const rangeFilterLng = $derived($birdnetSettings?.longitude);
  const rangeFilterThreshold = $derived($birdnetSettings?.rangeFilter?.threshold);
  const rangeFilterConfigured = $derived($birdnetSettings?.locationConfigured);

  $effect(() => {
    const _lat = rangeFilterLat;
    const _lng = rangeFilterLng;
    const _threshold = rangeFilterThreshold;
    const configured = rangeFilterConfigured;

    if (configured && _lat != null && _lng != null && _threshold != null) {
      debouncedTestRangeFilter();
    }

    return () => {
      clearTimeout(debounceTimer);
      clearTimeout(loadingDelayTimer);
      rangeFilterAbortController?.abort();
    };
  });

  async function downloadSpeciesCSV() {
    if (rangeFilterState.downloading || !birdnet?.locationConfigured) return;

    try {
      rangeFilterState.downloading = true;

      const params = new URLSearchParams({
        latitude: (birdnet?.latitude ?? 0).toString(),
        longitude: (birdnet?.longitude ?? 0).toString(),
        threshold: (birdnet?.rangeFilter?.threshold ?? 0.01).toString(),
      });

      const response = await fetch(buildAppUrl(`/api/v2/range/species/csv?${params}`), {
        headers: {
          'X-CSRF-Token': getCsrfToken() || '',
          Accept: 'text/csv',
        },
      });

      if (!response.ok) {
        let msg = t('settings.errors.csvDownloadFailed');
        if (response.headers.get('Content-Type')?.includes('application/json')) {
          try {
            const data: unknown = await response.clone().json();
            if (
              data &&
              typeof data === 'object' &&
              'message' in data &&
              typeof (data as Record<string, unknown>).message === 'string'
            ) {
              msg = (data as Record<string, unknown>).message as string;
            }
          } catch {
            // ignore parsing errors
          }
        }
        throw new Error(msg);
      }

      const cd =
        response.headers.get('Content-Disposition') ||
        response.headers.get('content-disposition') ||
        '';
      let filename = 'birdnet_species.csv';
      const fnStar = cd.match(/filename\*\s*=\s*([^']*)''([^;]+)/i);
      if (fnStar && fnStar[2]) {
        try {
          filename = decodeURIComponent(fnStar[2]);
        } catch {
          /* keep default */
        }
      } else {
        const fn = cd.match(/filename\s*=\s*"([^"]+)"/i) || cd.match(/filename\s*=\s*([^;]+)/i);
        if (fn && fn[1]) filename = fn[1].trim();
      }

      const blob = await response.blob();
      downloadBlob(blob, filename);

      toastActions.success(t('settings.main.sections.rangeFilter.csvDownloaded'));
    } catch (err) {
      logger.error('Failed to download species CSV:', err);
      toastActions.error(t('settings.main.sections.rangeFilter.csvDownloadFailed'));
    } finally {
      rangeFilterState.downloading = false;
    }
  }

  // ── Update handlers ───────────────────────────────────────────────────
  function updateBirdnetSetting(key: string, value: string | number) {
    settingsActions.updateSection('birdnet', { [key]: value });
  }

  function updateDynamicThreshold(key: string, value: number | boolean) {
    settingsActions.updateSection('realtime', {
      dynamicThreshold: { ...dynamicThreshold, [key]: value },
    });
  }

  function updateBatThreshold(value: number) {
    settingsActions.updateSection('bat', { threshold: value });
  }

  function updateBatNighttimeOnly(value: boolean) {
    settingsActions.updateSection('bat', { nighttimeOnly: value });
  }

  function updateBatUltrasonicFilter(value: boolean) {
    settingsActions.updateSection('bat', {
      ultrasonicFilter: { ...bat.ultrasonicFilter, enabled: value },
    });
  }

  function updateBatFalsePositiveFilterLevel(newLevel: number) {
    settingsActions.updateSection('bat', {
      falsePositiveFilter: { level: newLevel },
    });
  }

  // ── FP filter level definitions for the shared component ─────────────
  const BADGE_OFF = 'bg-black/5 dark:bg-white/5 text-[var(--color-base-content)]';
  const BADGE_SUCCESS = 'bg-[var(--color-success)] text-[var(--color-success-content)]';
  const BADGE_INFO = 'bg-[var(--color-info)] text-[var(--color-info-content)]';
  const BADGE_WARNING = 'bg-[var(--color-warning)] text-[var(--color-warning-content)]';
  const BADGE_ERROR = 'bg-[var(--color-error)] text-[var(--color-error-content)]';

  const BIRD_FP_LEVELS: FilterLevel[] = [
    {
      value: 0,
      nameKey: 'settings.main.sections.falsePositiveFilter.levelNames.off',
      badgeClass: BADGE_OFF,
    },
    {
      value: 1,
      nameKey: 'settings.main.sections.falsePositiveFilter.levelNames.lenient',
      badgeClass: BADGE_SUCCESS,
    },
    {
      value: 2,
      nameKey: 'settings.main.sections.falsePositiveFilter.levelNames.moderate',
      badgeClass: BADGE_INFO,
    },
    {
      value: 3,
      nameKey: 'settings.main.sections.falsePositiveFilter.levelNames.balanced',
      badgeClass: BADGE_WARNING,
    },
    {
      value: 4,
      nameKey: 'settings.main.sections.falsePositiveFilter.levelNames.strict',
      badgeClass: BADGE_ERROR,
    },
    {
      value: 5,
      nameKey: 'settings.main.sections.falsePositiveFilter.levelNames.maximum',
      badgeClass: BADGE_ERROR,
    },
  ];

  // Bat has only 3 meaningful levels (fixed 50% overlap, 4 detections in window):
  // Off=bypass (1 det), Moderate=2 det, Strict=3 det.
  // Lenient(1 det) is functionally identical to Off, so it's excluded.
  const BAT_FP_LEVELS: FilterLevel[] = [
    {
      value: 0,
      nameKey: 'settings.main.sections.falsePositiveFilter.levelNames.off',
      badgeClass: BADGE_OFF,
    },
    {
      value: 2,
      nameKey: 'settings.main.sections.falsePositiveFilter.levelNames.moderate',
      badgeClass: BADGE_INFO,
    },
    {
      value: 4,
      nameKey: 'settings.main.sections.falsePositiveFilter.levelNames.strict',
      badgeClass: BADGE_ERROR,
    },
  ];

  // Bat FP filter calculation helpers.
  // The bat model uses a fixed 50% overlap (1.5s step for 3s clip),
  // yielding 4 possible detections in a 6-second reference window.
  const BAT_MAX_DETECTIONS_IN_WINDOW = 4;

  function calculateBatMinDetections(level: number): number {
    if (level === 0) return 1;
    const levelData = safeArrayAccess(falsePositiveFilterLevels, level);
    if (!levelData) return 1;
    const required = BAT_MAX_DETECTIONS_IN_WINDOW * levelData.threshold - FLOAT_EPSILON;
    return Math.max(1, Math.ceil(required));
  }

  const BAT_FP_DESCRIPTION_KEYS: Record<number, string> = {
    0: 'analysis.detection.batFalsePositiveFilter.levels.off',
    2: 'analysis.detection.batFalsePositiveFilter.levels.moderate',
    4: 'analysis.detection.batFalsePositiveFilter.levels.strict',
  };

  function getBatFalsePositiveFilterDescription(level: number): string {
    // eslint-disable-next-line security/detect-object-injection
    const descKey = BAT_FP_DESCRIPTION_KEYS[level];
    if (!descKey) return '';

    const baseDescription = t(descKey);
    if (level === 0) return baseDescription;

    const minDet = calculateBatMinDetections(level);
    return t('analysis.detection.batFalsePositiveFilter.detectionCount', {
      count: minDet.toString(),
      description: baseDescription,
    });
  }

  function updateThreshold(value: number) {
    settingsActions.updateSection('birdnet', { threshold: value });
  }

  // Config for the shared secondary-model threshold section snippet.
  interface SecondaryModelThresholdConfig {
    titleKey: string;
    descKey: string;
    thresholdLabelKey: string;
    // The BirdNET threshold this model follows while the override is off; shown
    // in the disabled field so the number on screen matches the effective value.
    effectiveThreshold: number;
    current: { overrideThreshold: boolean; threshold: number };
    originalOverride: boolean;
    originalThreshold: number;
    onOverride: (_value: boolean) => void;
    onThreshold: (_value: number) => void;
  }

  // Secondary-model threshold override handlers. When the override is off, the
  // model follows the BirdNET threshold (backend modelGlobalConfidenceThreshold),
  // so the threshold field is disabled until the override is enabled.
  function updatePerchOverride(value: boolean) {
    settingsActions.updateSection('perch', { overrideThreshold: value });
  }

  function updatePerchThreshold(value: number) {
    settingsActions.updateSection('perch', { threshold: value });
  }

  function updateBirdNetV3Override(value: boolean) {
    settingsActions.updateSection('birdnetv3', { overrideThreshold: value });
  }

  function updateBirdNetV3Threshold(value: number) {
    settingsActions.updateSection('birdnetv3', { threshold: value });
  }

  // ── Gallery tab definitions ───────────────────────────────────────────
  const galleryTabs: TabDefinition[] = $derived([
    {
      id: 'installed',
      label: t('analysis.gallery.tabs.installed'),
      icon: Package,
      content: installedTabContent,
    },
    {
      id: 'available',
      label: t('analysis.gallery.tabs.available'),
      icon: Download,
      content: availableTabContent,
    },
  ]);

  // ── Page-level tab definitions ────────────────────────────────────────
  const pageTabs: TabDefinition[] = $derived([
    {
      id: 'settings',
      label: t('analysis.tabs.settings'),
      icon: SettingsIcon,
      content: settingsTabContent,
    },
    {
      id: 'models',
      label: t('analysis.tabs.models'),
      icon: Package,
      content: modelsTabContent,
    },
  ]);

  // ── SSE cleanup handle ────────────────────────────────────────────────
  let progressCleanup: (() => void) | null = null;

  onMount(() => {
    loadCatalog();
    loadModelRegions();
    loadBirdnetLocales();
    loadRangeFilterCount();
    loadRangeFilterStatus();
    return () => {
      if (progressCleanup) progressCleanup();
      clearTimeout(completionTimer);
    };
  });

  // ── Gallery functions ─────────────────────────────────────────────────
  async function loadCatalog() {
    const seq = ++catalogRequestSeq;
    loading = true;
    catalogError = null;
    try {
      const response = await fetchCatalog();
      // Bail if a newer loadCatalog started while this one awaited, so a slow
      // response cannot overwrite the newer region's catalog.
      if (seq !== catalogRequestSeq) return;
      catalog = response.catalog;
      // Refresh the installed list alongside the catalog so the secondary-model
      // threshold sections track install/uninstall. Swallows its own errors.
      await loadInstalledModels(seq);
    } catch (e) {
      if (seq !== catalogRequestSeq) return;
      catalogError =
        e instanceof Error ? e.message : t('analysis.gallery.errors.catalogLoadFailed');
    } finally {
      // Only the newest request owns the loading flag, so a superseded response
      // does not clear the spinner out from under the one still running.
      if (seq === catalogRequestSeq) loading = false;
    }
  }

  // seq, when passed, is the owning loadCatalog request's sequence: a stale
  // installed-list response is dropped rather than overwriting a newer one.
  async function loadInstalledModels(seq?: number) {
    try {
      const installed = await fetchInstalled();
      if (seq !== undefined && seq !== catalogRequestSeq) return;
      installedModels = installed;
    } catch (e) {
      logger.error('Failed to load installed models:', e);
    }
  }

  // Load the model regions for name resolution and region-aware picker scoping.
  // Best-effort: on failure the picker degrades to raw slugs with no region
  // scoping, which is strictly better than blocking the gallery.
  async function loadModelRegions() {
    try {
      regionsData = await fetchModelRegions();
    } catch (e) {
      logger.error('Failed to load model regions:', e);
    }
  }

  function openLicenseDialog(entry: CatalogEntry) {
    licenseModel = entry;
    // Smart default: preselect the recommended variant (falls back sensibly when
    // there is no recommendation). Empty string for flat entries.
    selectedVariantId = pickPreselectedVariant(entry);
    licenseDialogRef?.showModal();
  }

  function closeLicenseDialog() {
    licenseDialogRef?.close();
    licenseModel = null;
  }

  function handleInstall() {
    if (!licenseModel) return;
    // Never install a variant the recommender flagged incompatible with this host
    // (the button is disabled in this state; this guards a programmatic call too).
    if (installBlocked) return;
    // Do not start an install while any gallery action is in flight; they share
    // the single downloadProgress state and SSE subscription.
    if (galleryActionInFlight) return;
    const modelId = licenseModel.id;
    const modelName = licenseModel.name;
    // Only send a variantId when the entry actually offers variants; a flat entry
    // installs its single build with no variant.
    const variantId = licenseModel.variants?.length ? selectedVariantId : undefined;
    closeLicenseDialog();
    startInstall(modelId, modelName, variantId);
  }

  // The install body, extracted so a failed install's Retry can re-run it without
  // reopening the license dialog (the license was accepted this session).
  async function startInstall(modelId: string, modelName: string, variantId: string | undefined) {
    // Defensive boundary: callers already gate on galleryActionInFlight, but keep
    // the invariant here so a future caller cannot start an overlapping action.
    if (galleryActionInFlight) return;
    installError = null;
    installingId = modelId;
    downloadProgress = null;

    try {
      await installModel(modelId, variantId);

      if (progressCleanup) progressCleanup();
      progressCleanup = subscribeInstallProgress(
        modelId,
        (progress: DownloadProgress) => {
          downloadProgress = progress;
        },
        () => {
          downloadProgress = {
            catalogId: modelId,
            status: 'complete',
            downloadedBytes: 0,
            totalBytes: 0,
            currentFile: 0,
            totalFiles: 0,
          };
          progressCleanup = null;
          clearTimeout(completionTimer);
          completionTimer = setTimeout(() => {
            if (installingId === modelId) {
              installingId = null;
              downloadProgress = null;
            }
            invalidateModels();
            loadCatalog();
          }, 2000);
        },
        (err: string) => {
          installError = reportActionError(modelId, modelName, 'install', err, variantId);
          installingId = null;
          downloadProgress = null;
          progressCleanup = null;
        }
      );
    } catch (e) {
      const message = e instanceof Error ? e.message : t('analysis.gallery.errors.installFailed');
      installError = reportActionError(modelId, modelName, 'install', message, variantId);
      installingId = null;
    }
  }

  // Build a GalleryActionError, classifying whether a mirror endpoint could help.
  function reportActionError(
    modelId: string,
    modelName: string,
    kind: GalleryActionKind,
    message: string,
    variantId?: string
  ): GalleryActionError {
    return {
      modelId,
      modelName,
      kind,
      message,
      variantId,
      // A remove failure never involves a download, so it is never network-shaped;
      // enforce that structurally rather than trusting the delete error's text not
      // to contain a download-error substring.
      network: kind !== 'remove' && isNetworkDownloadError(message),
    };
  }

  function openRemoveDialog(entry: CatalogEntry) {
    removeConfirmModel = entry;
    removeDialogRef?.showModal();
  }

  function closeRemoveDialog() {
    removeDialogRef?.close();
    removeConfirmModel = null;
  }

  async function handleUninstall() {
    if (!removeConfirmModel) return;
    // Do not start a remove while any gallery action is in flight.
    if (galleryActionInFlight) return;
    const modelId = removeConfirmModel.id;
    const modelName = removeConfirmModel.name;
    closeRemoveDialog();
    installError = null;
    deletingId = modelId;

    try {
      await uninstallModel(modelId);
      invalidateModels();
      await loadCatalog();
      toastActions.success(t('analysis.gallery.removeSuccess', { name: modelName }));
    } catch (e) {
      const message = e instanceof Error ? e.message : t('analysis.gallery.errors.removeFailed');
      // A remove failure never involves a download, so it is never network-shaped.
      installError = reportActionError(modelId, modelName, 'remove', message);
    } finally {
      deletingId = null;
    }
  }

  function handleReinstall(entry: CatalogEntry) {
    if (galleryActionInFlight) return;
    startReinstall(entry.id, entry.name);
  }

  // The reinstall body, extracted so a failed reinstall's Retry can re-run it.
  async function startReinstall(modelId: string, modelName: string) {
    // Defensive boundary: callers already gate on galleryActionInFlight.
    if (galleryActionInFlight) return;
    installError = null;
    reinstallingId = modelId;
    downloadProgress = null;

    try {
      await reinstallModel(modelId);

      if (progressCleanup) progressCleanup();
      progressCleanup = subscribeInstallProgress(
        modelId,
        (progress: DownloadProgress) => {
          downloadProgress = progress;
        },
        () => {
          downloadProgress = {
            catalogId: modelId,
            status: 'complete',
            downloadedBytes: 0,
            totalBytes: 0,
            currentFile: 0,
            totalFiles: 0,
          };
          progressCleanup = null;
          clearTimeout(completionTimer);
          completionTimer = setTimeout(() => {
            if (reinstallingId === modelId) {
              reinstallingId = null;
              downloadProgress = null;
            }
            invalidateModels();
            loadCatalog();
          }, 2000);
        },
        (err: string) => {
          installError = reportActionError(modelId, modelName, 'reinstall', err);
          reinstallingId = null;
          downloadProgress = null;
          progressCleanup = null;
        }
      );
    } catch (e) {
      const message = e instanceof Error ? e.message : t('analysis.gallery.errors.installFailed');
      installError = reportActionError(modelId, modelName, 'reinstall', message);
      reinstallingId = null;
    }
  }

  // Re-run whichever action failed; the stored error object carries what is needed.
  // A remove failure offers no retry (the card's Remove button is right there).
  function retryFailedAction() {
    if (!installError) return;
    // Defensive in-flight guard, mirroring handleReinstall: installError is only set
    // when nothing is in flight (each start clears it, every failure resets its id),
    // so this is currently unreachable, but it keeps the invariant explicit and
    // survives future refactors that might retry while an action is running.
    if (galleryActionInFlight) return;
    const { modelId, modelName, variantId, kind } = installError;
    if (kind === 'install') startInstall(modelId, modelName, variantId);
    else if (kind === 'reinstall') startReinstall(modelId, modelName);
  }

  function dismissInstallError() {
    installError = null;
  }

  // Bring the Download Source setting into view and focus it: the mirror endpoint
  // is the remedy for a download-reachability failure, and it lives on this same
  // Models tab, just below the gallery.
  function scrollToDownloadSource() {
    const el = document.getElementById(HUGGINGFACE_ENDPOINT_ID);
    el?.scrollIntoView({ behavior: 'smooth', block: 'center' });
    el?.focus();
  }

  /** Compute download percentage for progress bar */
  function progressPercent(p: DownloadProgress): number {
    if (p.totalBytes <= 0) return 0;
    return Math.min(100, Math.round((p.downloadedBytes / p.totalBytes) * 100));
  }

  /** Human-readable status label */
  function statusLabel(status: DownloadProgress['status']): string {
    switch (status) {
      case 'downloading':
        return t('analysis.gallery.progress.downloading');
      case 'verifying':
        return t('analysis.gallery.progress.verifying');
      case 'loading':
        return t('analysis.gallery.progress.loading');
      case 'complete':
        return t('analysis.gallery.progress.complete');
      case 'failed':
        return t('analysis.gallery.progress.failed');
      default:
        return '';
    }
  }
</script>

<!-- ── Secondary-model threshold override section (Perch v2, BirdNET v3.0) ── -->
{#snippet secondaryModelThreshold(cfg: SecondaryModelThresholdConfig)}
  <SettingsSection
    title={t(cfg.titleKey)}
    description={t(cfg.descKey)}
    defaultOpen={true}
    originalData={{ override: cfg.originalOverride, threshold: cfg.originalThreshold }}
    currentData={{ override: cfg.current.overrideThreshold, threshold: cfg.current.threshold }}
  >
    <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
      <Checkbox
        checked={cfg.current.overrideThreshold}
        label={t('analysis.detection.secondaryThresholdOverride.label')}
        helpText={t('analysis.detection.secondaryThresholdOverride.helpText')}
        disabled={store.isLoading || store.isSaving}
        onchange={cfg.onOverride}
      />
      <NumberField
        label={t(cfg.thresholdLabelKey)}
        value={cfg.current.overrideThreshold ? cfg.current.threshold : cfg.effectiveThreshold}
        onUpdate={cfg.onThreshold}
        min={0.01}
        max={0.99}
        step={0.01}
        disabled={store.isLoading || store.isSaving || !cfg.current.overrideThreshold}
        helpText={cfg.current.overrideThreshold
          ? t('analysis.detection.secondaryThreshold.helpText')
          : t('analysis.detection.secondaryThreshold.followsBirdnet')}
      />
    </div>
  </SettingsSection>
{/snippet}

<!-- ── Settings Tab Content ──────────────────────────────────────────── -->
{#snippet settingsTabContent()}
  <div class="space-y-6">
    <!-- 1. Bird Detection -->
    <SettingsSection
      title={t('analysis.bird.title')}
      description={t('analysis.bird.description')}
      defaultOpen={true}
      originalData={{
        threshold: store.originalData.birdnet?.threshold,
        locale: store.originalData.birdnet?.locale,
        fpFilter: store.originalData.realtime?.falsePositiveFilter?.level ?? 0,
      }}
      currentData={{
        threshold: birdnet?.threshold,
        locale: birdnet?.locale,
        fpFilter: falsePositiveFilter.level,
      }}
    >
      <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
        <NumberField
          label={t('analysis.detection.confidenceThreshold.label')}
          value={birdnet?.threshold ?? 0.3}
          onUpdate={updateThreshold}
          min={0}
          max={1}
          step={0.05}
          disabled={store.isLoading || store.isSaving}
          helpText={t('analysis.detection.confidenceThreshold.helpText')}
        />

        <SelectDropdown
          options={birdnetLocaleOptions}
          value={birdnet?.locale ?? 'en'}
          label={t('analysis.detection.locale.label')}
          helpText={t('analysis.detection.locale.helpText')}
          disabled={store.isLoading || store.isSaving || birdnetLocales.loading}
          variant="select"
          groupBy={false}
          searchable={true}
          onChange={value => updateBirdnetSetting('locale', value as string)}
        >
          {#snippet renderOption(option)}
            {@const localeOption = option as BirdnetLocaleOption}
            <div class="flex items-center gap-2">
              <FlagIcon locale={localeOption.localeCode} className="size-4" />
              <span>{localeOption.label}</span>
            </div>
          {/snippet}
          {#snippet renderSelected(options)}
            {#if options[0]}
              {@const localeOption = options[0] as BirdnetLocaleOption}
              <span class="flex items-center gap-2">
                <FlagIcon locale={localeOption.localeCode} className="size-4" />
                <span>{localeOption.label}</span>
              </span>
            {:else}
              <span>{birdnet?.locale ?? 'en'}</span>
            {/if}
          {/snippet}
        </SelectDropdown>
      </div>

      <!-- Bird False Positive Filter -->
      <div class="mt-6">
        <FalsePositiveFilterControl
          id="false-positive-filter-level"
          level={falsePositiveFilter.level}
          levels={BIRD_FP_LEVELS}
          onUpdate={updateFalsePositiveFilterLevel}
          getDescription={level => getFalsePositiveFilterDescription(level, birdnet?.overlap ?? 0)}
          disabled={store.isLoading || store.isSaving}
        />
      </div>

      {#if falsePositiveFilter.level === 0}
        <SettingsNote>
          {#snippet icon()}<AlertTriangle class="size-4 text-[var(--color-warning)]" />{/snippet}
          <span>{t('settings.main.sections.falsePositiveFilter.warningOff')}</span>
        </SettingsNote>
      {:else if falsePositiveFilter.level >= 4}
        <SettingsNote>
          <span>{t('settings.main.sections.falsePositiveFilter.hardwareNote')}</span>
        </SettingsNote>
      {/if}
    </SettingsSection>

    <!-- 2. Perch v2 threshold override (only when the Perch model is installed) -->
    {#if hasPerchModel}
      {@render secondaryModelThreshold({
        titleKey: 'analysis.perch.title',
        descKey: 'analysis.perch.description',
        thresholdLabelKey: 'analysis.detection.perchThreshold.label',
        effectiveThreshold: birdnet?.threshold ?? 0.3,
        current: perch,
        originalOverride: store.originalData.perch?.overrideThreshold ?? false,
        originalThreshold: store.originalData.perch?.threshold ?? 0.5,
        onOverride: updatePerchOverride,
        onThreshold: updatePerchThreshold,
      })}
    {/if}

    <!-- 3. BirdNET v3.0 threshold override (only when the v3.0 model is installed) -->
    {#if hasBirdNetV3Model}
      {@render secondaryModelThreshold({
        titleKey: 'analysis.birdnetv3.title',
        descKey: 'analysis.birdnetv3.description',
        thresholdLabelKey: 'analysis.detection.birdnetv3Threshold.label',
        effectiveThreshold: birdnet?.threshold ?? 0.3,
        current: birdnetV3,
        originalOverride: store.originalData.birdnetv3?.overrideThreshold ?? false,
        originalThreshold: store.originalData.birdnetv3?.threshold ?? 0.5,
        onOverride: updateBirdNetV3Override,
        onThreshold: updateBirdNetV3Threshold,
      })}
    {/if}

    <!-- 4. Bat Detection (only when a bat model is installed) -->
    {#if hasBatModel}
      <SettingsSection
        title={t('analysis.bat.title')}
        description={t('analysis.bat.description')}
        defaultOpen={true}
        originalData={{
          batThreshold: store.originalData.bat?.threshold,
          batNighttimeOnly: store.originalData.bat?.nighttimeOnly,
          batUltrasonicFilter: store.originalData.bat?.ultrasonicFilter?.enabled ?? true,
          batFPFilter: store.originalData.bat?.falsePositiveFilter?.level ?? 0,
        }}
        currentData={{
          batThreshold: bat.threshold,
          batNighttimeOnly: bat.nighttimeOnly,
          batUltrasonicFilter: bat.ultrasonicFilter?.enabled ?? true,
          batFPFilter: batFPLevel,
        }}
      >
        <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
          <NumberField
            label={t('analysis.detection.batThreshold.label')}
            value={bat.threshold}
            onUpdate={updateBatThreshold}
            min={0.01}
            max={0.99}
            step={0.01}
            disabled={store.isLoading || store.isSaving}
            helpText={t('analysis.detection.batThreshold.helpText')}
          />
          <div></div>

          <Checkbox
            checked={bat.nighttimeOnly ?? true}
            label={t('analysis.detection.batNighttimeOnly.label')}
            helpText={t('analysis.detection.batNighttimeOnly.helpText')}
            disabled={store.isLoading || store.isSaving}
            onchange={updateBatNighttimeOnly}
          />
          <Checkbox
            checked={bat.ultrasonicFilter?.enabled ?? true}
            label={t('analysis.detection.batUltrasonicFilter.label')}
            helpText={t('analysis.detection.batUltrasonicFilter.helpText')}
            disabled={store.isLoading || store.isSaving}
            onchange={updateBatUltrasonicFilter}
          />
        </div>

        <!-- Bat False Positive Filter -->
        <div class="mt-6">
          <FalsePositiveFilterControl
            id="bat-false-positive-filter-level"
            level={batFPLevel}
            levels={BAT_FP_LEVELS}
            onUpdate={updateBatFalsePositiveFilterLevel}
            getDescription={level => getBatFalsePositiveFilterDescription(level)}
            disabled={store.isLoading || store.isSaving}
          />
        </div>

        {#if batFPLevel === 0}
          <SettingsNote>
            {#snippet icon()}<AlertTriangle class="size-4 text-[var(--color-warning)]" />{/snippet}
            <span>{t('analysis.detection.batFalsePositiveFilter.warningOff')}</span>
          </SettingsNote>
        {/if}
      </SettingsSection>
    {/if}

    <!-- 5. Range Filter -->
    <SettingsSection
      title={t('settings.main.sections.rangeFilter.title')}
      description={t('settings.main.sections.rangeFilter.description')}
      originalData={store.originalData.birdnet?.rangeFilter}
      currentData={birdnet?.rangeFilter}
    >
      <SettingsNote><span>{t('analysis.rangeFilter.birdOnlyNote')}</span></SettingsNote>

      <div class="grid grid-cols-1 md:grid-cols-2 gap-6 mt-4">
        <NumberField
          label={t('settings.main.sections.rangeFilter.threshold.label')}
          value={birdnet?.rangeFilter?.threshold ?? 0.01}
          onUpdate={value =>
            settingsActions.updateSection('birdnet', {
              rangeFilter: { ...birdnet?.rangeFilter, threshold: value },
            })}
          min={0.0}
          max={0.99}
          step={0.01}
          helpText={t('settings.main.sections.rangeFilter.threshold.helpText')}
          disabled={store.isLoading || store.isSaving}
        />

        <div>
          <div class="flex justify-start mb-1">
            <span class="text-sm text-[var(--color-base-content)]"
              >{t('settings.main.sections.rangeFilter.speciesCount.label')}</span
            >
          </div>
          <div class="flex items-center gap-3">
            <div
              class="text-2xl font-bold text-[var(--color-primary)] tabular-nums"
              class:opacity-60={rangeFilterState.testing}
            >
              {rangeFilterState.speciesCount !== null
                ? formatNumber(rangeFilterState.speciesCount)
                : '-'}
            </div>
            {#if rangeFilterState.testing}
              <span
                class="inline-block w-4 h-4 border-2 border-[var(--color-base-300)] border-t-[var(--color-primary)] rounded-full animate-spin"
              ></span>
            {/if}
          </div>
          <div class="flex gap-2 mt-2">
            <button
              type="button"
              class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg border border-[var(--color-base-content)]/30 bg-transparent hover:bg-black/5 dark:hover:bg-white/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-2 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              disabled={!rangeFilterState.speciesCount ||
                rangeFilterState.loading ||
                !birdnet?.locationConfigured}
              onclick={() => {
                rangeFilterState.showModal = true;
                loadRangeFilterSpecies();
              }}
            >
              {t('settings.main.sections.rangeFilter.speciesCount.viewSpecies')}
            </button>
            <button
              type="button"
              class="inline-flex items-center justify-center gap-2 h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-primary)] text-[var(--color-primary-content)] hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-2 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
              disabled={!rangeFilterState.speciesCount ||
                rangeFilterState.downloading ||
                !birdnet?.locationConfigured}
              onclick={downloadSpeciesCSV}
              aria-label={t('common.aria.downloadCsv')}
            >
              <Download class="size-4" />
              {t('analytics.filters.exportCsv')}
            </button>
          </div>
          <span class="help-text mt-1">
            {t('settings.main.sections.rangeFilter.speciesCount.helpText')}
          </span>
        </div>
      </div>

      {#if rangeFilterStatus && rangeFilterStatus.geomodel}
        <details
          class="mt-4 rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-200)]/50"
        >
          <summary
            class="cursor-pointer select-none px-4 py-3 text-sm font-medium text-[var(--color-base-content)] hover:bg-[var(--color-base-200)] rounded-lg transition-colors"
          >
            {t('analysis.rangeFilter.status.title')}
          </summary>
          <div class="px-4 pb-4">
            <!-- Geomodel info line -->
            <div class="flex items-center gap-2 text-sm mb-3">
              <span class="font-medium">
                {t('analysis.rangeFilter.status.geomodelInfo', {
                  version: rangeFilterStatus.geomodel.version,
                  species: formatNumber(rangeFilterStatus.geomodel.totalSpecies),
                })}
              </span>
              <span
                class={cn(
                  'inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium',
                  rangeFilterStatus.geomodel.autoSelected
                    ? 'bg-[var(--color-success)]/15 text-[var(--color-success)]'
                    : 'bg-[var(--color-base-300)] text-[var(--color-base-content)]/80'
                )}
              >
                {rangeFilterStatus.geomodel.autoSelected
                  ? t('analysis.rangeFilter.status.autoSelected')
                  : t('analysis.rangeFilter.status.manual')}
              </span>
            </div>

            <!-- Per-classifier coverage table -->
            {#if rangeFilterStatus.classifiers.length > 0}
              <div class="overflow-x-auto">
                <table class="w-full text-sm">
                  <thead>
                    <tr class="border-b border-[var(--color-base-300)]">
                      <th
                        class="text-left py-2 pr-4 font-medium text-[var(--color-base-content)]/60"
                        >{t('analysis.rangeFilter.status.classifier')}</th
                      >
                      <th
                        class="text-right py-2 px-4 font-medium text-[var(--color-base-content)]/60"
                        >{t('analysis.rangeFilter.status.totalSpecies')}</th
                      >
                      <th
                        class="text-right py-2 px-4 font-medium text-[var(--color-base-content)]/60"
                        title={t('analysis.rangeFilter.status.withRangeDataTooltip')}
                        >{t('analysis.rangeFilter.status.withRangeData')}</th
                      >
                      <th
                        class="text-right py-2 pl-4 font-medium text-[var(--color-base-content)]/60"
                        title={t('analysis.rangeFilter.status.withoutRangeDataTooltip')}
                        >{t('analysis.rangeFilter.status.withoutRangeData')}</th
                      >
                    </tr>
                  </thead>
                  <tbody>
                    {#each rangeFilterStatus.classifiers as classifier (classifier.id)}
                      <tr class="border-b border-[var(--color-base-300)]/50 last:border-0">
                        <td class="py-2 pr-4 font-medium">{classifier.name}</td>
                        <td class="py-2 px-4 text-right tabular-nums"
                          >{formatNumber(classifier.totalSpecies)}</td
                        >
                        <td class="py-2 px-4 text-right tabular-nums"
                          >{formatNumber(classifier.withRangeData)}</td
                        >
                        <td class="py-2 pl-4 text-right tabular-nums"
                          >{formatNumber(classifier.withoutRangeData)}</td
                        >
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {/if}
          </div>
        </details>

        <!-- Pass unmapped species toggle (always visible, outside collapsible) -->
        <div class="mt-3">
          <Checkbox
            label={t('analysis.rangeFilter.status.passUnmapped.label')}
            checked={birdnet?.rangeFilter?.passUnmappedSpecies ?? false}
            onchange={value =>
              settingsActions.updateSection('birdnet', {
                rangeFilter: { ...birdnet?.rangeFilter, passUnmappedSpecies: value },
              })}
            helpText={t('analysis.rangeFilter.status.passUnmapped.helpText')}
            disabled={store.isLoading || store.isSaving}
          />
        </div>
      {/if}

      {#if rangeFilterState.error}
        <div
          class="flex items-start gap-3 p-4 rounded-lg mt-4 bg-[color-mix(in_srgb,var(--color-error)_15%,transparent)] text-[var(--color-error)]"
          role="alert"
        >
          <XCircle class="size-5 shrink-0" aria-hidden="true" />
          <span>{rangeFilterState.error}</span>
          <button
            type="button"
            class="ml-auto inline-flex items-center justify-center p-1.5 rounded-md bg-transparent hover:bg-black/5 dark:hover:bg-white/5 transition-colors"
            aria-label={t('common.aria.dismissAlert')}
            onclick={() => (rangeFilterState.error = null)}
          >
            <X class="size-4" aria-hidden="true" />
          </button>
        </div>
      {/if}
    </SettingsSection>

    <!-- 6. Dynamic Threshold -->
    <SettingsSection
      title={t('settings.main.sections.dynamicThreshold.title')}
      description={t('settings.main.sections.dynamicThreshold.description')}
      originalData={store.originalData.realtime?.dynamicThreshold}
      currentData={store.formData.realtime?.dynamicThreshold}
    >
      <SettingsNote><span>{t('analysis.dynamicThreshold.birdOnlyNote')}</span></SettingsNote>

      <div class="mt-4">
        <Checkbox
          checked={dynamicThreshold.enabled}
          label={t('settings.main.sections.dynamicThreshold.enable.label')}
          helpText={t('settings.main.sections.dynamicThreshold.enable.helpText')}
          disabled={store.isLoading || store.isSaving}
          onchange={value => updateDynamicThreshold('enabled', value)}
        />
      </div>

      {#if dynamicThreshold.enabled}
        <div class="grid grid-cols-1 md:grid-cols-2 gap-6 mt-4">
          <NumberField
            label={t('settings.main.sections.dynamicThreshold.trigger.label')}
            value={dynamicThreshold.trigger}
            onUpdate={value => updateDynamicThreshold('trigger', value)}
            min={0.0}
            max={1.0}
            step={0.01}
            helpText={t('settings.main.sections.dynamicThreshold.trigger.helpText')}
            disabled={store.isLoading || store.isSaving}
          />

          <NumberField
            label={t('settings.main.sections.dynamicThreshold.minimum.label')}
            value={dynamicThreshold.min}
            onUpdate={value => updateDynamicThreshold('min', value)}
            min={0.0}
            max={0.99}
            step={0.01}
            helpText={t('settings.main.sections.dynamicThreshold.minimum.helpText')}
            disabled={store.isLoading || store.isSaving}
          />

          <NumberField
            label={t('settings.main.sections.dynamicThreshold.expireTime.label')}
            value={dynamicThreshold.validHours}
            onUpdate={value => updateDynamicThreshold('validHours', value)}
            min={0}
            max={1000}
            step={1}
            helpText={t('settings.main.sections.dynamicThreshold.expireTime.helpText')}
            disabled={store.isLoading || store.isSaving}
          />
        </div>
      {/if}
    </SettingsSection>

    <!-- 7. Advanced (collapsed by default) -->
    <SettingsSection
      title={t('analysis.advanced.title')}
      description={t('analysis.advanced.description')}
      defaultOpen={false}
      originalData={{
        threads: store.originalData.birdnet?.threads,
        modelPath: store.originalData.birdnet?.modelPath,
        labelPath: store.originalData.birdnet?.labelPath,
      }}
      currentData={{
        threads: birdnet?.threads,
        modelPath: birdnet?.modelPath,
        labelPath: birdnet?.labelPath,
      }}
    >
      <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
        <NumberField
          label={t('settings.main.fields.tensorflowThreads.label')}
          value={birdnet?.threads ?? 0}
          onUpdate={value => updateBirdnetSetting('threads', value)}
          min={0}
          max={32}
          step={1}
          helpText={t('settings.main.fields.tensorflowThreads.helpText')}
          disabled={store.isLoading || store.isSaving}
        />
      </div>

      <div class="grid grid-cols-1 md:grid-cols-2 gap-6 mt-6">
        <TextInput
          id="model-path"
          value={birdnet?.modelPath ?? ''}
          label={t('settings.main.sections.customClassifier.modelPath.label')}
          placeholder={t('settings.main.sections.customClassifier.modelPath.placeholder')}
          helpText={t('settings.main.sections.customClassifier.modelPath.helpText')}
          disabled={store.isLoading || store.isSaving}
          onchange={value => updateBirdnetSetting('modelPath', value)}
        />

        <TextInput
          id="label-path"
          value={birdnet?.labelPath ?? ''}
          label={t('settings.main.sections.customClassifier.labelPath.label')}
          placeholder={t('settings.main.sections.customClassifier.labelPath.placeholder')}
          helpText={t('settings.main.sections.customClassifier.labelPath.helpText')}
          disabled={store.isLoading || store.isSaving}
          onchange={value => updateBirdnetSetting('labelPath', value)}
        />
      </div>
    </SettingsSection>
  </div>
{/snippet}

<!-- ── Models Tab Content ────────────────────────────────────────────── -->
{#snippet modelsTabContent()}
  <div class="space-y-6">
    <SettingsSection
      title={t('analysis.gallery.title')}
      description={t('analysis.gallery.description')}
      defaultOpen={true}
      originalData={{ modelRegion: normalizeRegionMode(store.originalData.birdnet?.modelRegion) }}
      currentData={{ modelRegion: normalizeRegionMode(birdnet?.modelRegion) }}
    >
      <ModelRegionSelector disabled={store.isLoading || store.isSaving} />

      <!-- A failed install/reinstall/remove surfaces here, above the gallery tabs,
           so it never replaces the model grid and stays visible across tabs. -->
      {#if installError}
        <div
          class="mt-4 flex flex-col gap-2 rounded-lg border border-[var(--color-error)]/30 bg-[var(--color-error)]/10 px-4 py-3 text-sm"
          role="alert"
        >
          <div class="flex items-start gap-3">
            <AlertTriangle class="size-5 shrink-0 text-[var(--color-error)]" aria-hidden="true" />
            <div class="min-w-0 flex-1">
              <p class="font-medium text-[var(--color-base-content)]">
                {t('analysis.gallery.errors.actionFailed', { name: installError.modelName })}
              </p>
              {#if installError.network}
                <p class="mt-1 text-[var(--color-base-content)]/80">
                  {t('analysis.gallery.errors.downloadSourceHint')}
                </p>
              {:else if installError.kind === 'remove'}
                <!-- A remove failure has no in-banner Retry (removes are not
                     re-run from here); point the user back to the card's own
                     Remove button so the recovery path is never left implicit. -->
                <p class="mt-1 text-[var(--color-base-content)]/80">
                  {t('analysis.gallery.errors.removeRetryHint')}
                </p>
              {/if}
              <!-- Raw backend/SSE/ApiError text is often long and technical; lead
                   with the plain-English title (and hint where classifiable) and
                   keep the raw message one disclosure click away. -->
              <details class="mt-1">
                <summary
                  class="cursor-pointer text-[var(--color-base-content)]/70 hover:text-[var(--color-base-content)]"
                >
                  {t('analysis.gallery.errors.details')}
                </summary>
                <p class="mt-1 break-words text-[var(--color-base-content)]/80">
                  {installError.message}
                </p>
              </details>
            </div>
            <button
              type="button"
              class="ml-auto inline-flex items-center justify-center rounded-md p-1.5 bg-transparent hover:bg-black/5 dark:hover:bg-white/5 transition-colors"
              aria-label={t('analysis.gallery.errors.dismiss')}
              onclick={dismissInstallError}
            >
              <X class="size-4" />
            </button>
          </div>
          {#if installError.kind !== 'remove' || installError.network}
            <div class="flex flex-wrap items-center gap-2 pl-8">
              {#if installError.kind !== 'remove'}
                <button
                  type="button"
                  class="inline-flex items-center gap-1.5 rounded-md bg-[var(--color-base-200)] px-3 py-1.5 text-xs font-medium text-[var(--color-base-content)] hover:bg-[var(--color-base-300)] transition-colors"
                  onclick={retryFailedAction}
                >
                  <RefreshCw class="size-3.5" aria-hidden="true" />
                  {t('analysis.gallery.retry')}
                </button>
              {/if}
              {#if installError.network}
                <button
                  type="button"
                  class="inline-flex items-center gap-1.5 rounded-md bg-[var(--color-base-200)] px-3 py-1.5 text-xs font-medium text-[var(--color-base-content)] hover:bg-[var(--color-base-300)] transition-colors"
                  onclick={scrollToDownloadSource}
                >
                  <SettingsIcon class="size-3.5" aria-hidden="true" />
                  {t('analysis.gallery.errors.goToDownloadSource')}
                </button>
              {/if}
            </div>
          {/if}
        </div>
      {/if}

      {#if galleryActionInFlight}
        <!-- One shared, live status line naming why the other cards' actions are
             blocked. Touch users get no tooltip and aria-disabled buttons carry no
             native title semantics, so this line (referenced via aria-describedby)
             is how the reason reaches keyboard, screen-reader and tablet users. -->
        <p
          id={GALLERY_ACTION_STATUS_ID}
          role="status"
          class="mb-3 text-xs text-[var(--color-base-content)]/70"
        >
          {t('analysis.gallery.actionInProgress')}
        </p>
      {/if}

      <!-- Optimize banner: one or more installed models have a faster or
           better-matched build available for this host. Dismissible for the
           session; opens the review dialog. -->
      {#if offers.length > 0 && !optimizeBannerDismissed}
        <div
          class="mb-4 flex flex-wrap items-center gap-3 rounded-lg border border-[var(--color-primary)]/30 bg-[var(--color-primary)]/10 px-4 py-3 text-sm"
          role="status"
        >
          <Sparkles class="size-5 shrink-0 text-[var(--color-primary)]" aria-hidden="true" />
          <p class="min-w-0 flex-1 font-medium text-[var(--color-base-content)]">
            {t('analysis.gallery.optimize.bannerTitle', { count: offers.length })}
          </p>
          <button
            type="button"
            onclick={openOptimizeReview}
            class="inline-flex items-center gap-1.5 rounded-md bg-[var(--color-primary)] px-3 py-1.5 text-xs font-medium text-[var(--color-primary-content)] transition-colors hover:bg-[var(--color-primary)]/80"
          >
            {t('analysis.gallery.optimize.review')}
          </button>
          <button
            type="button"
            onclick={dismissOptimizeBanner}
            class="inline-flex items-center gap-1.5 rounded-md bg-[var(--color-base-200)] px-3 py-1.5 text-xs font-medium text-[var(--color-base-content)] transition-colors hover:bg-[var(--color-base-300)]"
          >
            {t('analysis.gallery.optimize.dismiss')}
          </button>
        </div>
      {/if}

      <SettingsTabs tabs={galleryTabs} bind:activeTab={galleryTab} showActions={false} />
    </SettingsSection>

    <SettingsSection
      title={t('analysis.downloadSource.title')}
      description={t('analysis.downloadSource.description')}
      originalData={{
        huggingFaceEndpoint: store.originalData.birdnet?.huggingFaceEndpoint ?? '',
      }}
      currentData={{ huggingFaceEndpoint: birdnet?.huggingFaceEndpoint ?? '' }}
    >
      <div class="grid grid-cols-1 md:grid-cols-2 gap-6">
        <!--
          Pattern kept deliberately close to conf.normalizeHuggingFaceEndpoint so
          the field neither accepts a value the backend will discard nor rejects
          one it would take. Specifically: the scheme is matched case-insensitively
          because the backend canonicalizes it; "?" and "#" are excluded
          everywhere, because the backend rejects a query or a fragment; and "@"
          is excluded from the authority only, because that is where userinfo
          lives. An "@" later in the path is legitimate and stays allowed.

          The two validators split the rest of the work. type="url" rejects a
          hostless authority such as "https://:8080", which this pattern accepts.
          The backend alone rejects what neither can express (a ".." path
          segment, a non-ASCII host, a backslash in the host) and reports those
          as a startup validation warning.

          Two things must not change: every "/" stays escaped, because browsers
          compile this attribute with the `v` flag where a bare "/" in a
          character class throws and silently disables validation altogether;
          and it stays a static attribute, so svelte-pattern-attributes.test.ts
          keeps compiling it the way the browser does.
        -->
        <TextInput
          id={HUGGINGFACE_ENDPOINT_ID}
          type="url"
          pattern="[Hh][Tt][Tt][Pp][Ss]?:\/\/[^\/?#@]+(\/[^?#]*)?"
          value={birdnet?.huggingFaceEndpoint ?? ''}
          label={t('analysis.downloadSource.endpoint.label')}
          placeholder={DEFAULT_HUGGINGFACE_ENDPOINT}
          helpText={t('analysis.downloadSource.endpoint.helpText')}
          validationMessage={t('analysis.downloadSource.endpoint.validationMessage')}
          disabled={store.isLoading || store.isSaving}
          onchange={value => updateBirdnetSetting('huggingFaceEndpoint', value.trim())}
        />
      </div>

      <SettingsNote>
        {t('analysis.downloadSource.note')}
      </SettingsNote>
    </SettingsSection>
  </div>
{/snippet}

<!-- ── Gallery: Installed Tab ────────────────────────────────────────── -->
<!-- Shared catalog-load-error banner, rendered in both gallery tabs. A single
     definition keeps the two tabs' error UI (markup, retry action, a11y) from
     drifting, and its decorative icons carry aria-hidden. -->
{#snippet catalogErrorBanner()}
  <div
    class="flex items-center gap-3 rounded-lg border border-[var(--color-error)]/30 bg-[var(--color-error)]/10 px-4 py-3 text-sm"
    role="alert"
  >
    <AlertTriangle class="size-5 shrink-0 text-[var(--color-error)]" aria-hidden="true" />
    <span class="text-[var(--color-base-content)]">{catalogError}</span>
    <button
      type="button"
      onclick={loadCatalog}
      class="ml-auto flex items-center gap-1.5 rounded-md bg-[var(--color-base-200)] px-3 py-1.5 text-xs font-medium text-[var(--color-base-content)] hover:bg-[var(--color-base-300)] transition-colors"
    >
      <RefreshCw class="size-3.5" aria-hidden="true" />
      {t('analysis.gallery.retry')}
    </button>
  </div>
{/snippet}

<!-- Variant-aware metadata rows for a gallery card: region, species count, and a
     friendly hardware chip, all describing the RELEVANT variant (the installed one
     on the Installed tab, the recommended one on the Available tab) rather than the
     entry-level globals. Flat (variant-less) entries keep the entry region/species. -->
{#snippet cardMetaRows(entry: CatalogEntry, mode: 'available' | 'installed')}
  {@const dv = displayVariantFor(entry, mode)}
  {#if entry.variants && entry.variants.length > 0}
    <div class="text-[var(--color-base-content)]/80">{t('analysis.gallery.regionLabel')}</div>
    <div class="text-[var(--color-base-content)]/80">{displayRegionName(dv)}</div>
    <div class="text-[var(--color-base-content)]/80">{t('analysis.gallery.speciesLabel')}</div>
    <div class="text-[var(--color-base-content)]/80">
      {t('analysis.gallery.species', { count: dv?.speciesCount ?? entry.speciesCount })}
    </div>
    <div class="text-[var(--color-base-content)]/80">{t('analysis.gallery.hardwareLabel')}</div>
    <div>
      {#if dv}
        <span
          class="inline-flex items-center gap-1 rounded-full bg-[var(--color-base-300)] px-2 py-0.5 text-xs text-[var(--color-base-content)]"
        >
          {variantHardwareLabel(dv)}
        </span>
      {:else}
        <span class="text-[var(--color-base-content)]/80">-</span>
      {/if}
    </div>
  {:else}
    {#if entry.region}
      <div class="text-[var(--color-base-content)]/80">{t('analysis.gallery.regionLabel')}</div>
      <div class="text-[var(--color-base-content)]/80">{entry.region}</div>
    {/if}
    <div class="text-[var(--color-base-content)]/80">{t('analysis.gallery.speciesLabel')}</div>
    <div class="text-[var(--color-base-content)]/80">
      {t('analysis.gallery.species', { count: entry.speciesCount })}
    </div>
  {/if}
{/snippet}

{#snippet installedTabContent()}
  <div class="space-y-4">
    {#if loading}
      <div class="flex items-center justify-center py-12">
        <Loader2 class="size-6 animate-spin text-[var(--color-primary)]" />
        <span class="ml-3 text-sm text-[var(--color-base-content)]/80"
          >{t('analysis.gallery.loading')}</span
        >
      </div>
    {:else if catalogError}
      {@render catalogErrorBanner()}
    {:else}
      <div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
        <!-- Installed models. The permanent built-in classifier (BirdNET v2.4) sorts
             first and renders a built-in badge instead of Remove/Reinstall; every
             installed model with a better host-recommended variant shows an Optimize
             action. -->
        {#each installedEntries as entry (entry.id)}
          {@const isDeleting = deletingId === entry.id}
          {@const isReinstalling = reinstallingId === entry.id}
          {@const reinstallProgress = isReinstalling ? downloadProgress : null}
          <!-- "Paused" = another gallery action is running, so THIS button is
               cross-disabled (vs its own in-progress spinner). aria-disabled keeps
               it tab-focusable to read the reason; native disabled stays for the
               button's own running state. -->
          {@const reinstallPaused = galleryActionInFlight && !isReinstalling}
          {@const removePaused = galleryActionInFlight && !isDeleting}
          {@const offer = offerByEntry.get(entry.id)}
          {@const isSwapping = installingId === entry.id}
          {@const optimizePaused = galleryActionInFlight && !isSwapping}
          {@const cardProgress = reinstallProgress ?? (isSwapping ? downloadProgress : null)}
          {@const logo = getModelLogo(entry.id)}
          <div
            class="rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-200)] p-4"
          >
            <div class="flex items-start gap-3">
              {#if logo}
                <img src={logo} alt="" class="size-10 shrink-0 rounded-lg" />
              {:else}
                <div class="shrink-0 rounded-lg bg-[var(--color-primary)]/10 p-2.5">
                  {#if entry.category === 'geomodel'}
                    <Globe size={24} class="text-[var(--color-primary)]" />
                  {:else if entry.category === 'bat'}
                    <Radar size={24} class="text-[var(--color-primary)]" />
                  {:else}
                    <BrainCircuit size={24} class="text-[var(--color-primary)]" />
                  {/if}
                </div>
              {/if}
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
                  <h4 class="text-sm font-semibold text-[var(--color-base-content)]">
                    {entry.name}
                  </h4>
                  {#if entry.channel === CHANNEL_PREVIEW}
                    <span
                      class="inline-flex items-center rounded-full bg-[var(--color-warning)]/15 px-2 py-0.5 text-xs font-bold uppercase tracking-wide text-[var(--color-warning)]"
                    >
                      {t('analysis.gallery.preview.badge')}
                    </span>
                  {/if}
                  {#if entry.buildLabel}
                    <span class="font-mono text-xs text-[var(--color-base-content)]/70">
                      {t('analysis.gallery.preview.buildLabel', {
                        version: entry.version,
                        build: entry.buildLabel,
                      })}
                    </span>
                  {/if}
                </div>
                <p class="mt-0.5 line-clamp-2 text-xs text-[var(--color-base-content)]/80">
                  {entry.description}
                </p>
                {#if entry.upstreamUrl}
                  <a
                    href={entry.upstreamUrl}
                    target="_blank"
                    rel="noopener noreferrer"
                    class="mt-1 inline-block text-xs text-[var(--color-primary)]/80 hover:text-[var(--color-primary)] transition-colors"
                  >
                    {entry.author}
                  </a>
                {:else}
                  <p class="mt-1 text-xs text-[var(--color-base-content)]/80">{entry.author}</p>
                {/if}
              </div>
            </div>
            <!-- Progress bar (shown during reinstall, not for companion entries) -->
            {#if cardProgress}
              <div class="mt-3 space-y-1.5">
                {#if cardProgress.status === 'complete'}
                  <div
                    class="flex items-center gap-2 text-sm font-medium text-[var(--color-success)]"
                  >
                    <Check class="h-4 w-4" />
                    <span
                      >{isSwapping
                        ? t('analysis.gallery.progress.complete')
                        : t('analysis.gallery.reinstallComplete')}</span
                    >
                  </div>
                {:else}
                  <div class="h-2 w-full overflow-hidden rounded-full bg-[var(--color-base-300)]">
                    <div
                      class="h-full rounded-full bg-[var(--color-primary)] transition-all duration-300"
                      style:width="{progressPercent(cardProgress)}%"
                    ></div>
                  </div>
                  <div
                    class="flex items-center justify-between text-xs text-[var(--color-base-content)]/80"
                  >
                    <span>
                      {statusLabel(
                        cardProgress.status
                      )}{#if cardProgress.status === 'downloading' && cardProgress.totalFiles > 1}
                        ({cardProgress.currentFile}/{cardProgress.totalFiles})
                      {/if}
                    </span>
                    {#if cardProgress.status === 'downloading' && cardProgress.totalBytes > 0}
                      <span>
                        {formatBytes(cardProgress.downloadedBytes)} / {formatBytes(
                          cardProgress.totalBytes
                        )}
                      </span>
                    {/if}
                  </div>
                {/if}
              </div>
            {/if}
            <!-- Incompatible warning for installed models -->
            {#if !entry.compatible}
              <div
                class="mt-3 flex items-start gap-2 rounded-lg bg-[var(--color-error)]/10 p-3 text-sm"
                role="status"
              >
                <XCircle
                  class="h-4 w-4 shrink-0 mt-0.5 text-[var(--color-error)]"
                  aria-hidden="true"
                />
                <span class="text-[var(--color-base-content)]"
                  >{entryIncompatibleText(entry.incompatibleReason)}</span
                >
              </div>
            {/if}
            <!-- Metadata grid -->
            <div
              class="mt-3 grid grid-cols-2 gap-x-4 gap-y-1 border-t border-[var(--color-base-300)] pt-3 text-xs"
            >
              {@render cardMetaRows(entry, 'installed')}
              <div class="text-[var(--color-base-content)]/80">
                {t('analysis.gallery.license.license')}
              </div>
              <div>
                {#if entry.commercialUse}
                  <span
                    class="inline-flex items-center gap-1 rounded-full bg-[var(--color-success)]/15 px-2 py-0.5 text-xs text-[var(--color-success)]"
                    title={t('analysis.gallery.license.commercialUseAllowed')}
                  >
                    <Shield class="size-3" />
                    {entry.license}
                  </span>
                {:else}
                  <span
                    class="inline-flex items-center gap-1 rounded-full bg-[var(--color-warning)]/15 px-2 py-0.5 text-xs text-[var(--color-warning)]"
                    title={t('analysis.gallery.license.nonCommercialOnly')}
                  >
                    <ShieldAlert class="size-3" />
                    {entry.license}
                  </span>
                {/if}
              </div>
            </div>
            <!-- Geomodel badge (for acoustic classifiers that bundle a geomodel) -->
            {#if entry.hasGeomodel && entry.category !== 'geomodel'}
              <div class="mt-2">
                <span
                  class="inline-flex items-center gap-1 rounded-full bg-[var(--color-info)]/15 px-2.5 py-0.5 text-xs font-medium text-[var(--color-info)]"
                >
                  {t('analysis.gallery.geomodelBadge')}
                </span>
              </div>
            {/if}
            <!-- Optimize badge: a faster/better-matched build is available here. -->
            {#if offer}
              <div class="mt-2">
                <span
                  class="inline-flex items-center gap-1 rounded-full bg-[var(--color-primary)]/15 px-2.5 py-0.5 text-xs font-medium text-[var(--color-primary)]"
                  title={t('analysis.gallery.optimize.badgeTitle')}
                >
                  <Sparkles class="size-3" aria-hidden="true" />
                  {t('analysis.gallery.optimize.badgeTitle')}
                </span>
              </div>
            {/if}
            <!-- Action footer. The permanent built-in classifier shows a built-in
                 badge instead of Remove and hides Reinstall; only its variant can be
                 swapped (the Optimize action). -->
            <div class="mt-3 flex items-center justify-end gap-2">
              {#if offer}
                <button
                  type="button"
                  onclick={e => {
                    if (optimizePaused) {
                      e.preventDefault();
                      return;
                    }
                    openLicenseDialog(entry);
                  }}
                  disabled={isSwapping}
                  aria-disabled={optimizePaused ? 'true' : undefined}
                  aria-describedby={optimizePaused ? GALLERY_ACTION_STATUS_ID : undefined}
                  title={optimizePaused ? t('analysis.gallery.actionInProgress') : undefined}
                  class={cn(
                    'inline-flex items-center gap-1.5 rounded-md bg-[var(--color-primary)] px-2.5 py-1.5 text-xs font-medium text-[var(--color-primary-content)] transition-colors',
                    optimizePaused
                      ? 'cursor-not-allowed opacity-50'
                      : 'hover:bg-[var(--color-primary)]/80'
                  )}
                  aria-label="{t('analysis.gallery.optimize.swap')} {entry.name}"
                >
                  <Sparkles class="size-3.5" aria-hidden="true" />
                  {t('analysis.gallery.optimize.swap')}
                </button>
              {/if}
              {#if entry.permanent}
                <span
                  class="inline-flex items-center gap-1 rounded-full bg-[var(--color-primary)]/15 px-2.5 py-0.5 text-xs font-medium text-[var(--color-primary)]"
                >
                  {t('analysis.gallery.builtIn')}
                </span>
              {:else}
                <button
                  type="button"
                  onclick={e => {
                    if (reinstallPaused) {
                      e.preventDefault();
                      return;
                    }
                    handleReinstall(entry);
                  }}
                  disabled={isReinstalling}
                  aria-disabled={reinstallPaused ? 'true' : undefined}
                  aria-describedby={reinstallPaused ? GALLERY_ACTION_STATUS_ID : undefined}
                  title={reinstallPaused ? t('analysis.gallery.actionInProgress') : undefined}
                  class={cn(
                    'inline-flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-xs font-medium text-[var(--color-base-content)]/80 transition-colors',
                    isReinstalling || reinstallPaused
                      ? 'opacity-50'
                      : 'hover:bg-[var(--color-base-300)]',
                    reinstallPaused && 'cursor-not-allowed'
                  )}
                  aria-label="{t('analysis.gallery.reinstall')} {entry.name}"
                >
                  {#if isReinstalling}
                    <Loader2 class="size-3.5 animate-spin" />
                    {t('analysis.gallery.reinstalling')}
                  {:else}
                    <RefreshCw class="size-3.5" />
                    {t('analysis.gallery.reinstall')}
                  {/if}
                </button>
                <button
                  type="button"
                  onclick={e => {
                    if (removePaused) {
                      e.preventDefault();
                      return;
                    }
                    openRemoveDialog(entry);
                  }}
                  disabled={isDeleting}
                  aria-disabled={removePaused ? 'true' : undefined}
                  aria-describedby={removePaused ? GALLERY_ACTION_STATUS_ID : undefined}
                  title={removePaused ? t('analysis.gallery.actionInProgress') : undefined}
                  class={cn(
                    'inline-flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-xs font-medium text-[var(--color-error)] transition-colors',
                    isDeleting || removePaused ? 'opacity-50' : 'hover:bg-[var(--color-error)]/10',
                    removePaused && 'cursor-not-allowed'
                  )}
                  aria-label="{t('analysis.gallery.remove')} {entry.name}"
                >
                  {#if isDeleting}
                    <Loader2 class="size-3.5 animate-spin" />
                    {t('analysis.gallery.removing')}
                  {:else}
                    <Trash2 class="size-3.5" />
                    {t('analysis.gallery.remove')}
                  {/if}
                </button>
              {/if}
            </div>
          </div>
        {/each}
      </div>

      {#if installedEntries.length === 0}
        <p class="py-4 text-center text-sm text-[var(--color-base-content)]/80">
          {t('analysis.gallery.noInstalledModels')}
        </p>
      {/if}
    {/if}
  </div>
{/snippet}

{#snippet modelCard(entry: CatalogEntry)}
  {@const isInstalling = installingId === entry.id}
  {@const progress = isInstalling ? downloadProgress : null}
  <!-- Paused only when this Install would otherwise be available: another action
       is running, this one is not, and the entry is compatible. Incompatible
       entries keep their permanent native-disabled state (explained by the banner
       above), never a transient "action in progress" reason. -->
  {@const installPaused = galleryActionInFlight && !isInstalling && entry.compatible}
  {@const logo = getModelLogo(entry.id)}
  <div
    class={cn(
      'flex h-full flex-col rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-200)] p-4',
      !entry.compatible && 'opacity-60'
    )}
  >
    <!-- Header: logo + name/description/author -->
    <div class="flex items-start gap-3">
      {#if logo}
        <img src={logo} alt="" class="size-10 shrink-0 rounded-lg" />
      {:else}
        <div class="shrink-0 rounded-lg bg-[var(--color-primary)]/10 p-2.5">
          {#if entry.category === 'geomodel'}
            <Globe size={24} class="text-[var(--color-primary)]" />
          {:else if entry.category === 'bat'}
            <Radar size={24} class="text-[var(--color-primary)]" />
          {:else}
            <BrainCircuit size={24} class="text-[var(--color-primary)]" />
          {/if}
        </div>
      {/if}
      <div class="min-w-0 flex-1">
        <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
          <h4 class="text-sm font-semibold text-[var(--color-base-content)]">
            {entry.name}
          </h4>
          {#if entry.channel === CHANNEL_PREVIEW}
            <span
              class="inline-flex items-center rounded-full bg-[var(--color-warning)]/15 px-2 py-0.5 text-xs font-bold uppercase tracking-wide text-[var(--color-warning)]"
            >
              {t('analysis.gallery.preview.badge')}
            </span>
          {/if}
          {#if entry.buildLabel}
            <span class="font-mono text-xs text-[var(--color-base-content)]/70">
              {t('analysis.gallery.preview.buildLabel', {
                version: entry.version,
                build: entry.buildLabel,
              })}
            </span>
          {/if}
        </div>
        <p class="mt-0.5 line-clamp-2 text-xs text-[var(--color-base-content)]/80">
          {entry.description}
        </p>
        {#if entry.upstreamUrl}
          <a
            href={entry.upstreamUrl}
            target="_blank"
            rel="noopener noreferrer"
            class="mt-1 inline-block text-xs text-[var(--color-primary)]/80 hover:text-[var(--color-primary)] transition-colors"
          >
            {entry.author}
          </a>
        {:else}
          <p class="mt-1 text-xs text-[var(--color-base-content)]/80">{entry.author}</p>
        {/if}
      </div>
    </div>

    <!-- Developer-preview notice: v3.0 and any future preview build are flagged as
         not the final GA release so users know what they are installing. -->
    {#if entry.channel === CHANNEL_PREVIEW}
      <div
        class="mt-3 flex items-start gap-2 rounded-lg bg-[var(--color-warning)]/10 p-3 text-xs"
        role="note"
      >
        <TriangleAlert
          class="h-4 w-4 shrink-0 mt-0.5 text-[var(--color-warning)]"
          aria-hidden="true"
        />
        <span class="text-[var(--color-base-content)]"
          >{t('analysis.gallery.preview.cardNotice')}</span
        >
      </div>
    {/if}

    <!-- Progress bar (shown during install, not for companion entries) -->
    {#if progress}
      <div class="mt-3 space-y-1.5">
        {#if progress.status === 'complete'}
          <div class="flex items-center gap-2 text-sm font-medium text-[var(--color-success)]">
            <Check class="h-4 w-4" />
            <span>{t('analysis.gallery.progress.complete')}</span>
          </div>
        {:else}
          <div class="h-2 w-full overflow-hidden rounded-full bg-[var(--color-base-300)]">
            <div
              class="h-full rounded-full bg-[var(--color-primary)] transition-all duration-300"
              style:width="{progressPercent(progress)}%"
            ></div>
          </div>
          <div
            class="flex items-center justify-between text-xs text-[var(--color-base-content)]/80"
          >
            <span>
              {statusLabel(
                progress.status
              )}{#if progress.status === 'downloading' && progress.totalFiles > 1}
                ({progress.currentFile}/{progress.totalFiles})
              {/if}
            </span>
            {#if progress.status === 'downloading' && progress.totalBytes > 0}
              <span>
                {formatBytes(progress.downloadedBytes)} / {formatBytes(progress.totalBytes)}
              </span>
            {/if}
          </div>
        {/if}
      </div>
    {/if}

    <!-- Incompatible warning banner -->
    {#if !entry.compatible}
      <div
        class="mt-3 flex items-start gap-2 rounded-lg bg-[var(--color-warning)]/10 p-3 text-sm"
        role="status"
      >
        <TriangleAlert
          class="h-4 w-4 shrink-0 mt-0.5 text-[var(--color-warning)]"
          aria-hidden="true"
        />
        <span class="text-[var(--color-base-content)]"
          >{entryIncompatibleText(entry.incompatibleReason)}</span
        >
      </div>
    {/if}

    <!-- Metadata grid -->
    <div
      class="mt-3 grid grid-cols-2 gap-x-4 gap-y-1 border-t border-[var(--color-base-300)] pt-3 text-xs"
    >
      {@render cardMetaRows(entry, 'available')}
      <div class="text-[var(--color-base-content)]/80">{t('analysis.gallery.license.license')}</div>
      <div>
        {#if entry.commercialUse}
          <span
            class="inline-flex items-center gap-1 rounded-full bg-[var(--color-success)]/15 px-2 py-0.5 text-xs text-[var(--color-success)]"
            title={t('analysis.gallery.license.commercialUseAllowed')}
          >
            <Shield class="size-3" />
            {entry.license}
          </span>
        {:else}
          <span
            class="inline-flex items-center gap-1 rounded-full bg-[var(--color-warning)]/15 px-2 py-0.5 text-xs text-[var(--color-warning)]"
            title={t('analysis.gallery.license.nonCommercialOnly')}
          >
            <ShieldAlert class="size-3" />
            {entry.license}
          </span>
        {/if}
      </div>
    </div>

    <!-- Geomodel badge (for acoustic classifiers that bundle a geomodel) -->
    {#if entry.hasGeomodel && entry.category !== 'geomodel'}
      <div class="mt-2">
        <span
          class="inline-flex items-center gap-1 rounded-full bg-[var(--color-info)]/15 px-2.5 py-0.5 text-xs font-medium text-[var(--color-info)]"
        >
          {t('analysis.gallery.geomodelBadge')}
        </span>
      </div>
    {/if}

    <!-- Action footer (pushed to bottom via mt-auto) -->
    <div class="mt-auto flex items-center justify-end pt-3">
      <button
        type="button"
        onclick={e => {
          if (installPaused) {
            e.preventDefault();
            return;
          }
          openLicenseDialog(entry);
        }}
        disabled={!entry.compatible || isInstalling}
        aria-disabled={installPaused ? 'true' : undefined}
        aria-describedby={installPaused ? GALLERY_ACTION_STATUS_ID : undefined}
        title={installPaused ? t('analysis.gallery.actionInProgress') : undefined}
        class={cn(
          'inline-flex items-center gap-1.5 rounded-md bg-[var(--color-primary)] px-3 py-1.5 text-xs font-medium text-[var(--color-primary-content)] transition-colors',
          !entry.compatible || isInstalling || installPaused
            ? 'opacity-50'
            : 'hover:bg-[var(--color-primary)]/80',
          installPaused && 'cursor-not-allowed'
        )}
        aria-label="{t('analysis.gallery.install')} {entry.name}"
      >
        {#if isInstalling}
          <Loader2 class="size-3.5 animate-spin" />
          {t('analysis.gallery.installing')}
        {:else}
          <Download class="size-3.5" />
          {t('analysis.gallery.install')}
        {/if}
      </button>
    </div>
  </div>
{/snippet}

{#snippet availableTabContent()}
  <div class="space-y-6">
    {#if loading}
      <div class="flex items-center justify-center py-12">
        <Loader2 class="size-6 animate-spin text-[var(--color-primary)]" />
        <span class="ml-3 text-sm text-[var(--color-base-content)]/80"
          >{t('analysis.gallery.loading')}</span
        >
      </div>
    {:else if catalogError}
      {@render catalogErrorBanner()}
    {:else}
      <!-- Acoustic Classifiers section -->
      {#if availableWildlife.length > 0 || availableBirds.length > 0 || availableBats.length > 0}
        <div class="space-y-4">
          <h2 class="text-sm font-bold uppercase tracking-wider text-[var(--color-base-content)]">
            {t('analysis.gallery.sections.acoustic')}
          </h2>

          {#if availableWildlife.length > 0}
            <div>
              <h3
                class="mb-3 text-sm font-semibold uppercase tracking-wider text-[var(--color-base-content)]/80"
              >
                {t('analysis.gallery.categories.wildlife')}
              </h3>
              <div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
                {#each availableWildlife as entry (entry.id)}
                  {@render modelCard(entry)}
                {/each}
              </div>
            </div>
          {/if}

          {#if availableBirds.length > 0}
            <div>
              <h3
                class="mb-3 text-sm font-semibold uppercase tracking-wider text-[var(--color-base-content)]/80"
              >
                {t('analysis.gallery.categories.bird')}
              </h3>
              <div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
                {#each availableBirds as entry (entry.id)}
                  {@render modelCard(entry)}
                {/each}
              </div>
            </div>
          {/if}

          {#if availableBats.length > 0}
            <div>
              <h3
                class="mb-3 text-sm font-semibold uppercase tracking-wider text-[var(--color-base-content)]/80"
              >
                {t('analysis.gallery.categories.bat')}
              </h3>
              <div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
                {#each availableBats as entry (entry.id)}
                  {@render modelCard(entry)}
                {/each}
              </div>
            </div>
          {/if}
        </div>
      {/if}

      <!-- Geomodels section -->
      {#if availableGeomodels.length > 0}
        <div class="space-y-4">
          <h2 class="text-sm font-bold uppercase tracking-wider text-[var(--color-base-content)]">
            {t('analysis.gallery.sections.geomodel')}
          </h2>
          <div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
            {#each availableGeomodels as entry (entry.id)}
              {@render modelCard(entry)}
            {/each}
          </div>
        </div>
      {/if}

      {#if availableWildlife.length === 0 && availableBirds.length === 0 && availableBats.length === 0 && availableGeomodels.length === 0}
        <p class="py-8 text-center text-sm text-[var(--color-base-content)]/80">
          {t('analysis.gallery.noAvailableModels')}
        </p>
      {/if}
    {/if}
  </div>
{/snippet}

<!-- ── Main Content ──────────────────────────────────────────────────── -->
<main class="settings-page-content space-y-6" aria-label={t('analysis.title')}>
  <SettingsTabs tabs={pageTabs} bind:activeTab={pageTab} />
</main>

<!-- License Acceptance Dialog -->
<dialog
  bind:this={licenseDialogRef}
  class="m-auto w-full max-w-3xl rounded-xl border border-[var(--color-base-300)] bg-[var(--color-base-100)] p-0 shadow-xl backdrop:bg-black/50"
  aria-labelledby="license-dialog-title"
>
  {#if licenseModel}
    <div class="p-6">
      <h3 id="license-dialog-title" class="text-lg font-semibold text-[var(--color-base-content)]">
        {t('analysis.gallery.license.title')}
      </h3>
      {#if licenseModel.channel === CHANNEL_PREVIEW}
        <div
          class="mt-4 flex items-start gap-2 rounded-lg bg-[var(--color-warning)]/10 p-3 text-sm"
          role="note"
        >
          <TriangleAlert
            class="h-4 w-4 shrink-0 mt-0.5 text-[var(--color-warning)]"
            aria-hidden="true"
          />
          <span class="text-[var(--color-base-content)]">
            {t('analysis.gallery.preview.dialogNotice', {
              build: licenseModel.buildLabel ?? licenseModel.version,
            })}
          </span>
        </div>
      {/if}
      <div class="mt-4 space-y-3">
        <table
          class="w-full overflow-hidden rounded-lg border-separate border-spacing-0 bg-[var(--color-base-200)] text-sm"
        >
          <tbody>
            <tr>
              <th
                scope="row"
                class="px-4 pt-4 pb-1 text-left font-normal align-top text-[var(--color-base-content)]/80"
                >{t('analysis.gallery.license.model')}</th
              >
              <td
                class="px-4 pt-4 pb-1 text-right align-top font-medium text-[var(--color-base-content)]"
                >{licenseModel.name}</td
              >
            </tr>
            <tr>
              <th
                scope="row"
                class="px-4 py-1 text-left font-normal align-top text-[var(--color-base-content)]/80"
                >{t('analysis.gallery.license.author')}</th
              >
              <td class="px-4 py-1 text-right align-top text-[var(--color-base-content)]"
                >{licenseModel.author}</td
              >
            </tr>
            <tr>
              <th
                scope="row"
                class="px-4 py-1 text-left font-normal align-top text-[var(--color-base-content)]/80"
                >{t('analysis.gallery.license.license')}</th
              >
              <td class="px-4 py-1 text-right align-top text-[var(--color-base-content)]"
                >{licenseModel.license}</td
              >
            </tr>
            <tr>
              <th
                scope="row"
                class="px-4 py-1 text-left font-normal align-top text-[var(--color-base-content)]/80"
                >{t('analysis.gallery.license.commercialUse')}</th
              >
              <td class="px-4 py-1 text-right align-top">
                {#if licenseModel.commercialUse}
                  <span class="inline-flex items-center gap-1 text-[var(--color-success)]">
                    <Shield class="size-3.5" />
                    {t('analysis.gallery.license.allowed')}
                  </span>
                {:else}
                  <span class="inline-flex items-center gap-1 text-[var(--color-warning)]">
                    <ShieldAlert class="size-3.5" />
                    {t('analysis.gallery.license.notAllowed')}
                  </span>
                {/if}
              </td>
            </tr>
            <tr>
              <th
                scope="row"
                class="px-4 pt-1 pb-4 text-left font-normal align-top text-[var(--color-base-content)]/80"
                >{t('analysis.gallery.license.downloadSize')}</th
              >
              <td class="px-4 pt-1 pb-4 text-right align-top text-[var(--color-base-content)]"
                >{formatBytes(licenseDownloadSize)}</td
              >
            </tr>
          </tbody>
        </table>

        {#if licenseModel.variants && licenseModel.variants.length > 0}
          <ModelVariantPicker
            variants={licenseModel.variants}
            installedVariantId={licenseModel.installedVariantId}
            {selectedVariantId}
            {activeRegionSlug}
            regionNames={regionNameMap}
            onSelect={id => (selectedVariantId = id)}
            idPrefix="license-variant"
          />
          <!-- Plain-language help for the precision jargon (FP32/FP16/INT8) and the
               Default-vs-Recommended distinction, for the non-technical audience. A
               native <details> is keyboard- and touch-accessible, unlike a
               hover-only tooltip. -->
          <details class="mt-2 text-xs text-[var(--color-base-content)]/70">
            <summary class="cursor-pointer hover:text-[var(--color-base-content)]">
              {t('analysis.gallery.variants.precisionInfo')}
            </summary>
            <p class="mt-1">{t('analysis.gallery.variants.precisionHelp')}</p>
          </details>
        {/if}

        {#if !licenseModel.commercialUse}
          <div
            class="flex items-start gap-2 rounded-lg border border-[var(--color-warning)]/30 bg-[var(--color-warning)]/10 px-3 py-2.5 text-sm"
          >
            <ShieldAlert
              class="mt-0.5 size-4 shrink-0 text-[var(--color-warning)]"
              aria-hidden="true"
            />
            <p class="text-[var(--color-base-content)]">
              {t('analysis.gallery.license.nonCommercialWarning')}
            </p>
          </div>
        {/if}
      </div>

      {#if installBlocked}
        <p class="mt-4 text-sm text-[var(--color-error)]" role="alert">
          {t('analysis.gallery.variants.incompatible')}
        </p>
      {/if}
      <div class="mt-6 flex justify-end gap-3">
        <button
          type="button"
          onclick={closeLicenseDialog}
          class="rounded-lg border border-[var(--color-base-300)] px-4 py-2 text-sm font-medium text-[var(--color-base-content)] hover:bg-[var(--color-base-200)] transition-colors"
        >
          {t('common.cancel')}
        </button>
        <button
          type="button"
          onclick={handleInstall}
          disabled={installBlocked}
          title={installBlocked ? t('analysis.gallery.variants.incompatible') : undefined}
          class="inline-flex items-center gap-2 rounded-lg bg-[var(--color-primary)] px-4 py-2 text-sm font-medium text-[var(--color-primary-content)] hover:bg-[var(--color-primary)]/80 transition-colors disabled:cursor-not-allowed disabled:opacity-50"
        >
          <Download class="size-4" />
          {t('analysis.gallery.license.acceptAndInstall')}
        </button>
      </div>
    </div>
  {/if}
</dialog>

<!-- Remove Confirmation Dialog -->
<dialog
  bind:this={removeDialogRef}
  class="m-auto rounded-xl border border-[var(--color-base-300)] bg-[var(--color-base-100)] p-0 shadow-xl backdrop:bg-black/50"
  aria-labelledby="remove-dialog-title"
>
  {#if removeConfirmModel}
    <div class="w-full max-w-md p-6">
      <div class="flex items-start gap-3">
        <div class="shrink-0 rounded-full bg-[var(--color-error)]/10 p-2">
          <AlertTriangle class="size-5 text-[var(--color-error)]" aria-hidden="true" />
        </div>
        <div>
          <h3
            id="remove-dialog-title"
            class="text-lg font-semibold text-[var(--color-base-content)]"
          >
            {t('analysis.gallery.removeDialog.title', { name: removeConfirmModel.name })}
          </h3>
          <p class="mt-2 text-sm text-[var(--color-base-content)]/80">
            {t('analysis.gallery.removeDialog.confirmation')}
          </p>
        </div>
      </div>

      <div class="mt-6 flex justify-end gap-3">
        <button
          type="button"
          onclick={closeRemoveDialog}
          class="rounded-lg border border-[var(--color-base-300)] px-4 py-2 text-sm font-medium text-[var(--color-base-content)] hover:bg-[var(--color-base-200)] transition-colors"
        >
          {t('common.cancel')}
        </button>
        <button
          type="button"
          onclick={handleUninstall}
          class="inline-flex items-center gap-2 rounded-lg bg-[var(--color-error)] px-4 py-2 text-sm font-medium text-white hover:bg-[var(--color-error)]/80 transition-colors"
        >
          <Trash2 class="size-4" />
          {t('analysis.gallery.remove')}
        </button>
      </div>
    </div>
  {/if}
</dialog>

<!-- Optimize Review Dialog -->
<OptimizeReviewDialog
  open={optimizeReviewOpen}
  {offers}
  regionNames={regionNameMap}
  inFlight={galleryActionInFlight}
  applyingId={optimizeApplyingId}
  appliedIds={appliedOptimizeIds}
  failedIds={failedOptimizeIds}
  onApply={applyOffer}
  onApplyAll={applyAllOffers}
  onClose={closeOptimizeReview}
/>

<!-- Range Filter Species Modal -->
{#if rangeFilterState.showModal}
  <div
    class="fixed inset-0 bg-black/50 flex items-center justify-center backdrop-blur-sm"
    style:z-index="9999"
    role="dialog"
    aria-modal="true"
    aria-labelledby="modal-title"
    tabindex="-1"
    onclick={e => e.target === e.currentTarget && (rangeFilterState.showModal = false)}
    onkeydown={e => e.key === 'Escape' && (rangeFilterState.showModal = false)}
  >
    <div
      class="bg-[var(--color-base-100)] rounded-2xl p-6 max-w-4xl max-h-[80vh] overflow-hidden flex flex-col shadow-2xl"
      role="document"
    >
      <div class="flex justify-between items-center mb-4">
        <h3 id="modal-title" class="text-xl font-semibold">
          {t('settings.main.sections.rangeFilter.modal.title')}
        </h3>
        <button
          type="button"
          class="inline-flex items-center justify-center w-8 h-8 rounded-full bg-transparent hover:bg-black/5 dark:hover:bg-white/10 transition-colors"
          aria-label={t('common.aria.closeModal')}
          onclick={() => (rangeFilterState.showModal = false)}
        >
          <X class="size-5" />
        </button>
      </div>

      <div class="mb-4 p-3 bg-[var(--color-base-200)]/50 rounded-lg">
        <div class="grid grid-cols-2 md:grid-cols-4 gap-4 text-sm">
          <div>
            <span class="text-[var(--color-base-content)] opacity-60"
              >{t('settings.main.sections.rangeFilter.modal.speciesCount')}</span
            >
            <span class="font-medium ml-1">{rangeFilterState.speciesCount}</span>
          </div>
          <div>
            <span class="text-[var(--color-base-content)] opacity-60"
              >{t('settings.main.sections.rangeFilter.modal.threshold')}</span
            >
            <span class="font-medium ml-1">{birdnet?.rangeFilter?.threshold}</span>
          </div>
          <div>
            <span class="text-[var(--color-base-content)] opacity-60"
              >{t('settings.main.sections.rangeFilter.modal.latitude')}</span
            >
            <span class="font-medium ml-1">{birdnet?.latitude}</span>
          </div>
          <div>
            <span class="text-[var(--color-base-content)] opacity-60"
              >{t('settings.main.sections.rangeFilter.modal.longitude')}</span
            >
            <span class="font-medium ml-1">{birdnet?.longitude}</span>
          </div>
        </div>
      </div>

      {#if rangeFilterState.error}
        <div
          class="flex items-start gap-3 p-4 rounded-lg mb-4 bg-[color-mix(in_srgb,var(--color-error)_15%,transparent)] text-[var(--color-error)]"
          role="alert"
        >
          <XCircle class="size-5 shrink-0" aria-hidden="true" />
          <span>{rangeFilterState.error}</span>
          <button
            type="button"
            class="ml-auto inline-flex items-center justify-center p-1.5 rounded-md bg-transparent hover:bg-black/5 dark:hover:bg-white/5 transition-colors"
            aria-label={t('common.aria.dismissAlert')}
            onclick={() => (rangeFilterState.error = null)}
          >
            <X class="size-4" aria-hidden="true" />
          </button>
        </div>
      {/if}

      <div class="flex-1 overflow-auto">
        {#if rangeFilterState.loading}
          <div class="text-center py-12">
            <span
              class="inline-block w-8 h-8 border-4 border-[var(--color-base-300)] border-t-[var(--color-primary)] rounded-full animate-spin"
            ></span>
            <p class="mt-3 text-[var(--color-base-content)] opacity-90">
              {t('settings.main.sections.rangeFilter.modal.loadingSpecies')}
            </p>
          </div>
        {:else if rangeFilterState.species.length > 0}
          <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-2">
            {#each rangeFilterState.species as species, index (`${species.scientificName}_${species.commonName}_${index}`)}
              <div class="p-3 rounded-lg hover:bg-[var(--color-base-200)]/50 transition-colors">
                <div class="font-medium">{species.commonName}</div>
                <div class="text-sm text-[var(--color-base-content)] opacity-60 italic">
                  {species.scientificName}
                </div>
              </div>
            {/each}
          </div>
        {:else}
          <div class="text-center py-12 text-[var(--color-base-content)] opacity-60">
            {t('settings.main.sections.rangeFilter.modal.noSpeciesFound')}
          </div>
        {/if}
      </div>

      <div
        class="flex justify-between items-center mt-4 pt-4 border-t border-[var(--color-base-200)]"
      >
        <button
          type="button"
          class="inline-flex items-center justify-center gap-2 h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-primary)] text-[var(--color-primary-content)] hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-2 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          onclick={downloadSpeciesCSV}
          disabled={rangeFilterState.loading ||
            rangeFilterState.downloading ||
            !rangeFilterState.speciesCount}
          aria-label={t('common.aria.downloadCsv')}
        >
          <Download class="size-4" />
          {t('analytics.filters.exportCsv')}
        </button>
        <button
          type="button"
          class="inline-flex items-center justify-center h-10 px-4 text-sm font-medium rounded-lg border border-[var(--color-base-content)]/30 bg-transparent hover:bg-black/5 dark:hover:bg-white/10 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-2 transition-colors"
          onclick={() => (rangeFilterState.showModal = false)}
        >
          {t('settings.main.sections.rangeFilter.modal.close')}
        </button>
      </div>
    </div>
  </div>
{/if}
