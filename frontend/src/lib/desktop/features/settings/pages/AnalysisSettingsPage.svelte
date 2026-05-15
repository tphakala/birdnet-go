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
  import type { CatalogEntry, DownloadProgress } from '$lib/types/models';
  import {
    fetchCatalog,
    installModel,
    reinstallModel,
    uninstallModel,
    subscribeInstallProgress,
  } from '$lib/utils/modelsApi';
  import { invalidateModels } from '$lib/stores/models.svelte';
  import SettingsTabs from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import type { TabDefinition } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsNote from '$lib/desktop/features/settings/components/SettingsNote.svelte';
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
  } from '$lib/stores/settings';
  import { cn } from '$lib/utils/cn.js';
  import { api, ApiError, getCsrfToken } from '$lib/utils/api';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { toastActions } from '$lib/stores/toast';
  import { formatBytes } from '$lib/utils/formatters';
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
    Loader2,
    RefreshCw,
    Radar,
    Globe,
    XCircle,
    X,
    Check,
    Settings as SettingsIcon,
  } from '@lucide/svelte';

  import logoBirdnet from '$lib/assets/logos/logo-birdnet.png';
  import logoGoogle from '$lib/assets/logos/logo-google.png';
  import logoJyu from '$lib/assets/logos/logo-jyu.jpeg';

  const logger = loggers.settings;

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

  // ── Page-level tab state ──────────────────────────────────────────────
  type PageTab = 'settings' | 'models';
  let pageTab = $state<PageTab>('settings');

  // ── Gallery (Models tab) state ────────────────────────────────────────
  let catalog = $state<CatalogEntry[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);

  let installingId = $state<string | null>(null);
  let deletingId = $state<string | null>(null);
  let reinstallingId = $state<string | null>(null);
  let downloadProgress = $state<DownloadProgress | null>(null);
  let completionTimer: ReturnType<typeof setTimeout> | undefined;

  let licenseModel = $state<CatalogEntry | null>(null);
  let removeConfirmModel = $state<CatalogEntry | null>(null);

  // Element bindings should NOT use $state - causes showModal() to fail
  let licenseDialogRef: HTMLDialogElement | null = null;
  let removeDialogRef: HTMLDialogElement | null = null;

  type GalleryTab = 'installed' | 'available';
  let galleryTab = $state<GalleryTab>('installed');

  // ── Store-derived state ───────────────────────────────────────────────
  let store = $derived($settingsStore);
  let birdnet = $derived($birdnetSettings);
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

  // ── Derived catalog views ─────────────────────────────────────────────
  const installedEntries = $derived(catalog.filter(e => e.installed));
  const availableWildlife = $derived(
    catalog.filter(e => !e.installed && e.compatible && e.category === 'wildlife')
  );
  const availableBirds = $derived(
    catalog.filter(e => !e.installed && e.compatible && e.category === 'bird')
  );
  const availableBats = $derived(
    catalog.filter(e => !e.installed && e.compatible && e.category === 'bat')
  );
  const availableGeomodels = $derived(
    catalog.filter(e => !e.installed && e.compatible && e.category === 'geomodel')
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

  function debouncedTestRangeFilter() {
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

    try {
      const data = await api.post<{ count: number; species?: RangeFilterSpecies[] }>(
        '/api/v2/range/species/test',
        {
          latitude: birdnet?.latitude,
          longitude: birdnet?.longitude,
          threshold: birdnet?.rangeFilter?.threshold,
        }
      );

      rangeFilterState.speciesCount = data.count;

      if (rangeFilterState.showModal) {
        rangeFilterState.species = data.species || [];
      }
    } catch (err) {
      logger.error('Failed to test range filter:', err);
      rangeFilterState.error = t('settings.main.errors.rangeFilterTestFailed');
      rangeFilterState.speciesCount = null;
    } finally {
      clearTimeout(loadingDelayTimer);
      rangeFilterState.testing = false;
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
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      window.URL.revokeObjectURL(url);

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
    loading = true;
    error = null;
    try {
      const response = await fetchCatalog();
      catalog = response.catalog;
    } catch (e) {
      error = e instanceof Error ? e.message : t('analysis.gallery.errors.catalogLoadFailed');
    } finally {
      loading = false;
    }
  }

  function openLicenseDialog(entry: CatalogEntry) {
    licenseModel = entry;
    licenseDialogRef?.showModal();
  }

  function closeLicenseDialog() {
    licenseDialogRef?.close();
    licenseModel = null;
  }

  async function handleInstall() {
    if (!licenseModel) return;
    const modelId = licenseModel.id;
    closeLicenseDialog();
    installingId = modelId;
    downloadProgress = null;

    try {
      await installModel(modelId);

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
          error = err;
          installingId = null;
          downloadProgress = null;
          progressCleanup = null;
        }
      );
    } catch (e) {
      error = e instanceof Error ? e.message : t('analysis.gallery.errors.installFailed');
      installingId = null;
    }
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
    const modelId = removeConfirmModel.id;
    closeRemoveDialog();
    deletingId = modelId;

    try {
      await uninstallModel(modelId);
      invalidateModels();
      await loadCatalog();
    } catch (e) {
      error = e instanceof Error ? e.message : t('analysis.gallery.errors.removeFailed');
    } finally {
      deletingId = null;
    }
  }

  async function handleReinstall(entry: CatalogEntry) {
    if (reinstallingId || installingId) return;
    reinstallingId = entry.id;
    downloadProgress = null;

    try {
      await reinstallModel(entry.id);

      progressCleanup = subscribeInstallProgress(
        entry.id,
        (progress: DownloadProgress) => {
          downloadProgress = progress;
        },
        () => {
          downloadProgress = {
            catalogId: entry.id,
            status: 'complete',
            downloadedBytes: 0,
            totalBytes: 0,
            currentFile: 0,
            totalFiles: 0,
          };
          progressCleanup = null;
          clearTimeout(completionTimer);
          completionTimer = setTimeout(() => {
            if (reinstallingId === entry.id) {
              reinstallingId = null;
              downloadProgress = null;
            }
            invalidateModels();
            loadCatalog();
          }, 2000);
        },
        (err: string) => {
          error = err;
          reinstallingId = null;
          downloadProgress = null;
          progressCleanup = null;
        }
      );
    } catch (e) {
      error = e instanceof Error ? e.message : t('analysis.gallery.errors.installFailed');
      reinstallingId = null;
    }
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

    <!-- 2. Bat Detection (only when a bat model is installed) -->
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

    <!-- 3. Range Filter -->
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
                ? rangeFilterState.speciesCount.toLocaleString()
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
                  species: rangeFilterStatus.geomodel.totalSpecies.toLocaleString(),
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
                          >{classifier.totalSpecies.toLocaleString()}</td
                        >
                        <td class="py-2 px-4 text-right tabular-nums"
                          >{classifier.withRangeData.toLocaleString()}</td
                        >
                        <td class="py-2 pl-4 text-right tabular-nums"
                          >{classifier.withoutRangeData.toLocaleString()}</td
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
          <XCircle class="size-5 shrink-0" />
          <span>{rangeFilterState.error}</span>
          <button
            type="button"
            class="ml-auto inline-flex items-center justify-center p-1.5 rounded-md bg-transparent hover:bg-black/5 dark:hover:bg-white/5 transition-colors"
            onclick={() => (rangeFilterState.error = null)}
          >
            <X class="size-4" />
          </button>
        </div>
      {/if}
    </SettingsSection>

    <!-- 4. Dynamic Threshold -->
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

    <!-- 5. Advanced (collapsed by default) -->
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
  <SettingsSection
    title={t('analysis.gallery.title')}
    description={t('analysis.gallery.description')}
    defaultOpen={true}
  >
    <SettingsTabs tabs={galleryTabs} bind:activeTab={galleryTab} showActions={false} />
  </SettingsSection>
{/snippet}

<!-- ── Gallery: Installed Tab ────────────────────────────────────────── -->
{#snippet installedTabContent()}
  <div class="space-y-4">
    {#if loading}
      <div class="flex items-center justify-center py-12">
        <Loader2 class="size-6 animate-spin text-[var(--color-primary)]" />
        <span class="ml-3 text-sm text-[var(--color-base-content)]/80"
          >{t('analysis.gallery.loading')}</span
        >
      </div>
    {:else if error}
      <div
        class="flex items-center gap-3 rounded-lg border border-[var(--color-error)]/30 bg-[var(--color-error)]/10 px-4 py-3 text-sm"
        role="alert"
      >
        <AlertTriangle class="size-5 shrink-0 text-[var(--color-error)]" />
        <span class="text-[var(--color-base-content)]">{error}</span>
        <button
          onclick={loadCatalog}
          class="ml-auto flex items-center gap-1.5 rounded-md bg-[var(--color-base-200)] px-3 py-1.5 text-xs font-medium text-[var(--color-base-content)] hover:bg-[var(--color-base-300)] transition-colors"
        >
          <RefreshCw class="size-3.5" />
          {t('analysis.gallery.retry')}
        </button>
      </div>
    {:else}
      <div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
        <!-- Built-in BirdNET model (always present) -->
        <div
          class="rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-200)] p-4"
        >
          <div class="flex items-start gap-3">
            <img src={logoBirdnet} alt="" class="size-10 shrink-0 rounded-lg" />
            <div class="min-w-0 flex-1">
              <h4 class="text-sm font-semibold text-[var(--color-base-content)]">BirdNET v2.4</h4>
              <p class="mt-0.5 line-clamp-2 text-xs text-[var(--color-base-content)]/80">
                {t('analysis.gallery.builtInDescription')}
              </p>
              <p class="mt-1 text-xs text-[var(--color-base-content)]/80">
                Cornell Lab of Ornithology / Chemnitz University
              </p>
            </div>
          </div>
          <div
            class="mt-3 flex items-center justify-between border-t border-[var(--color-base-300)] pt-3"
          >
            <div class="flex items-center gap-2 text-xs text-[var(--color-base-content)]/80">
              <span>v2.4</span>
              <span>{t('analysis.gallery.species', { count: '6,000+' })}</span>
            </div>
            <span
              class="inline-flex items-center gap-1 rounded-full bg-[var(--color-primary)]/15 px-2.5 py-0.5 text-xs font-medium text-[var(--color-primary)]"
            >
              {t('analysis.gallery.builtIn')}
            </span>
          </div>
        </div>

        <!-- Installed additional models -->
        {#each installedEntries as entry (entry.id)}
          {@const isDeleting = deletingId === entry.id}
          {@const isReinstalling = reinstallingId === entry.id}
          {@const reinstallProgress = isReinstalling ? downloadProgress : null}
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
                <h4 class="text-sm font-semibold text-[var(--color-base-content)]">{entry.name}</h4>
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
            {#if reinstallProgress}
              <div class="mt-3 space-y-1.5">
                {#if reinstallProgress.status === 'complete'}
                  <div
                    class="flex items-center gap-2 text-sm font-medium text-[var(--color-success)]"
                  >
                    <Check class="h-4 w-4" />
                    <span>{t('analysis.gallery.reinstallComplete')}</span>
                  </div>
                {:else}
                  <div class="h-2 w-full overflow-hidden rounded-full bg-[var(--color-base-300)]">
                    <div
                      class="h-full rounded-full bg-[var(--color-primary)] transition-all duration-300"
                      style:width="{progressPercent(reinstallProgress)}%"
                    ></div>
                  </div>
                  <div
                    class="flex items-center justify-between text-xs text-[var(--color-base-content)]/80"
                  >
                    <span>
                      {statusLabel(
                        reinstallProgress.status
                      )}{#if reinstallProgress.status === 'downloading' && reinstallProgress.totalFiles > 1}
                        ({reinstallProgress.currentFile}/{reinstallProgress.totalFiles})
                      {/if}
                    </span>
                    {#if reinstallProgress.status === 'downloading' && reinstallProgress.totalBytes > 0}
                      <span>
                        {formatBytes(reinstallProgress.downloadedBytes)} / {formatBytes(
                          reinstallProgress.totalBytes
                        )}
                      </span>
                    {/if}
                  </div>
                {/if}
              </div>
            {/if}
            <!-- Metadata grid -->
            <div
              class="mt-3 grid grid-cols-2 gap-x-4 gap-y-1 border-t border-[var(--color-base-300)] pt-3 text-xs"
            >
              {#if entry.region}
                <div class="text-[var(--color-base-content)]/80">
                  {t('analysis.gallery.regionLabel')}
                </div>
                <div class="text-[var(--color-base-content)]/80">{entry.region}</div>
              {/if}
              <div class="text-[var(--color-base-content)]/80">
                {t('analysis.gallery.speciesLabel')}
              </div>
              <div class="text-[var(--color-base-content)]/80">
                {t('analysis.gallery.species', { count: entry.speciesCount })}
              </div>
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
            <!-- Action footer -->
            <div class="mt-3 flex items-center justify-end gap-2">
              <button
                onclick={() => handleReinstall(entry)}
                disabled={reinstallingId !== null || installingId !== null || isDeleting}
                class="inline-flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-xs font-medium text-[var(--color-base-content)]/80 hover:bg-[var(--color-base-300)] transition-colors disabled:opacity-50"
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
                onclick={() => openRemoveDialog(entry)}
                disabled={isDeleting || isReinstalling || installingId !== null}
                class="inline-flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-xs font-medium text-[var(--color-error)] hover:bg-[var(--color-error)]/10 transition-colors disabled:opacity-50"
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
  {@const logo = getModelLogo(entry.id)}
  <div
    class="flex h-full flex-col rounded-lg border border-[var(--color-base-300)] bg-[var(--color-base-200)] p-4"
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
        <h4 class="text-sm font-semibold text-[var(--color-base-content)]">
          {entry.name}
        </h4>
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

    <!-- Metadata grid -->
    <div
      class="mt-3 grid grid-cols-2 gap-x-4 gap-y-1 border-t border-[var(--color-base-300)] pt-3 text-xs"
    >
      {#if entry.region}
        <div class="text-[var(--color-base-content)]/80">{t('analysis.gallery.regionLabel')}</div>
        <div class="text-[var(--color-base-content)]">{entry.region}</div>
      {/if}
      <div class="text-[var(--color-base-content)]/80">{t('analysis.gallery.speciesLabel')}</div>
      <div class="text-[var(--color-base-content)]">
        {t('analysis.gallery.species', { count: entry.speciesCount })}
      </div>
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
        onclick={() => openLicenseDialog(entry)}
        disabled={isInstalling || installingId !== null}
        class="inline-flex items-center gap-1.5 rounded-md bg-[var(--color-primary)] px-3 py-1.5 text-xs font-medium text-[var(--color-primary-content)] hover:bg-[var(--color-primary)]/80 transition-colors disabled:opacity-50"
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
    {:else if error}
      <div
        class="flex items-center gap-3 rounded-lg border border-[var(--color-error)]/30 bg-[var(--color-error)]/10 px-4 py-3 text-sm"
        role="alert"
      >
        <AlertTriangle class="size-5 shrink-0 text-[var(--color-error)]" />
        <span class="text-[var(--color-base-content)]">{error}</span>
        <button
          onclick={loadCatalog}
          class="ml-auto flex items-center gap-1.5 rounded-md bg-[var(--color-base-200)] px-3 py-1.5 text-xs font-medium text-[var(--color-base-content)] hover:bg-[var(--color-base-300)] transition-colors"
        >
          <RefreshCw class="size-3.5" />
          {t('analysis.gallery.retry')}
        </button>
      </div>
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
  class="m-auto w-full max-w-md rounded-xl border border-[var(--color-base-300)] bg-[var(--color-base-100)] p-0 shadow-xl backdrop:bg-black/50"
  aria-labelledby="license-dialog-title"
>
  {#if licenseModel}
    <div class="p-6">
      <h3 id="license-dialog-title" class="text-lg font-semibold text-[var(--color-base-content)]">
        {t('analysis.gallery.license.title')}
      </h3>
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
                >{formatBytes(licenseModel.totalSizeBytes)}</td
              >
            </tr>
          </tbody>
        </table>

        {#if !licenseModel.commercialUse}
          <div
            class="flex items-start gap-2 rounded-lg border border-[var(--color-warning)]/30 bg-[var(--color-warning)]/10 px-3 py-2.5 text-sm"
          >
            <ShieldAlert class="mt-0.5 size-4 shrink-0 text-[var(--color-warning)]" />
            <p class="text-[var(--color-base-content)]">
              {t('analysis.gallery.license.nonCommercialWarning')}
            </p>
          </div>
        {/if}
      </div>

      <div class="mt-6 flex justify-end gap-3">
        <button
          onclick={closeLicenseDialog}
          class="rounded-lg border border-[var(--color-base-300)] px-4 py-2 text-sm font-medium text-[var(--color-base-content)] hover:bg-[var(--color-base-200)] transition-colors"
        >
          {t('common.cancel')}
        </button>
        <button
          onclick={handleInstall}
          class="inline-flex items-center gap-2 rounded-lg bg-[var(--color-primary)] px-4 py-2 text-sm font-medium text-[var(--color-primary-content)] hover:bg-[var(--color-primary)]/80 transition-colors"
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
          <AlertTriangle class="size-5 text-[var(--color-error)]" />
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
          onclick={closeRemoveDialog}
          class="rounded-lg border border-[var(--color-base-300)] px-4 py-2 text-sm font-medium text-[var(--color-base-content)] hover:bg-[var(--color-base-200)] transition-colors"
        >
          {t('common.cancel')}
        </button>
        <button
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
          <XCircle class="size-5 shrink-0" />
          <span>{rangeFilterState.error}</span>
          <button
            type="button"
            class="ml-auto inline-flex items-center justify-center p-1.5 rounded-md bg-transparent hover:bg-black/5 dark:hover:bg-white/5 transition-colors"
            onclick={() => (rangeFilterState.error = null)}
          >
            <X class="size-4" />
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
