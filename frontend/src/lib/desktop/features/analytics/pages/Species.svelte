<script module lang="ts">
  // Range-filter scores require a full geo evaluation, so cache them for the
  // browser session. Module-scoped so the cache survives navigating away from
  // and back to the Species page within the same SPA session.
  let cachedRangeScores: Map<string, number> | null = null;
</script>

<script lang="ts">
  import { t, getLocale, type TranslationKey } from '$lib/i18n';
  import { getLocalDateString, parseLocalDateString } from '$lib/utils/date';
  import { downloadBlob } from '$lib/utils/fileHelpers';
  import { formatNumber, formatDateTime } from '$lib/utils/formatters';
  import { loggers } from '$lib/utils/logger';
  import { getStoredValue, setStoredValue } from '$lib/utils/storage';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';
  import { isAuthenticated } from '$lib/utils/auth';
  import { settingsActions } from '$lib/stores/settings';
  import { api } from '$lib/utils/api';
  import { onMount, onDestroy } from 'svelte';
  import { SvelteSet, SvelteMap } from 'svelte/reactivity';
  import { Trash2, SlidersHorizontal } from '@lucide/svelte';
  import SortableHeader from '$lib/desktop/components/ui/SortableHeader.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import SpeciesFilterForm from '../components/forms/SpeciesFilterForm.svelte';
  import SpeciesDetailModal from '../components/modals/SpeciesDetailModal.svelte';
  import SpeciesCard from '../components/ui/SpeciesCard.svelte';
  import SpeciesCardMobile from '../components/ui/SpeciesCardMobile.svelte';
  import StatCard from '../components/ui/StatCard.svelte';

  const logger = loggers.analytics;

  // Type definitions
  interface SpeciesFilters {
    timePeriod: 'all' | 'today' | 'week' | 'month' | '90days' | 'year' | 'custom';
    startDate: string;
    endDate: string;
    sortOrder:
      | 'count_desc'
      | 'count_asc'
      | 'name_asc'
      | 'name_desc'
      | 'first_seen_desc'
      | 'first_seen_asc'
      | 'last_seen_desc'
      | 'last_seen_asc'
      | 'confidence_desc'
      | 'confidence_asc'
      | 'max_confidence_desc'
      | 'max_confidence_asc';
    searchTerm: string;
  }

  type SortOrder = SpeciesFilters['sortOrder'];

  interface SpeciesData {
    common_name: string;
    scientific_name: string;
    count: number;
    avg_confidence: number;
    max_confidence: number;
    first_heard: string;
    last_heard: string;
    thumbnail_url?: string;
  }

  type ViewMode = 'grid' | 'list' | 'manage';

  // Per-species review counts used by the Manage view.
  interface ReviewStat {
    scientificName: string;
    total: number;
    verified: number;
    rejected: number;
  }

  // Manage-only sort keys. Kept separate from SortOrder (and never persisted to
  // localStorage) so a Manage-only column never leaks into grid/list sorting.
  type ManageSortKey = 'name' | 'count' | 'max_confidence' | 'last_seen' | 'review' | 'range';

  // A species row paired with the visitor-localized common name, used inside
  // filteredSpecies for search + name-sort so they match what the user sees.
  interface LocalizedRow {
    species: SpeciesData;
    displayName: string;
  }

  // Species name defaults to ascending (A→Z); every other column defaults to
  // descending (most/highest/most recent first) on first click.
  const SORTABLE_COLUMNS: {
    field: string;
    labelKey: TranslationKey;
    asc: SortOrder;
    desc: SortOrder;
  }[] = [
    {
      field: 'species',
      labelKey: 'analytics.species.headers.species',
      asc: 'name_asc',
      desc: 'name_desc',
    },
    {
      field: 'count',
      labelKey: 'analytics.species.headers.detections',
      asc: 'count_asc',
      desc: 'count_desc',
    },
    {
      field: 'avg_confidence',
      labelKey: 'analytics.species.headers.avgConfidence',
      asc: 'confidence_asc',
      desc: 'confidence_desc',
    },
    {
      field: 'max_confidence',
      labelKey: 'analytics.species.headers.maxConfidence',
      asc: 'max_confidence_asc',
      desc: 'max_confidence_desc',
    },
    {
      field: 'first_seen',
      labelKey: 'analytics.species.headers.firstDetected',
      asc: 'first_seen_asc',
      desc: 'first_seen_desc',
    },
    {
      field: 'last_seen',
      labelKey: 'analytics.species.headers.lastDetected',
      asc: 'last_seen_asc',
      desc: 'last_seen_desc',
    },
  ];

  // Default sort and persistence (survives a page refresh).
  const DEFAULT_SORT_ORDER: SortOrder = 'count_desc';
  const SORT_STORAGE_KEY = 'analytics.species.sortOrder';
  // Only the species-name column defaults to ascending (A→Z) on first click.
  const SPECIES_COLUMN_FIELD = 'species';
  const VALID_SORT_ORDERS: Set<string> = new Set<string>(
    SORTABLE_COLUMNS.flatMap(column => [column.asc, column.desc])
  );

  function isSortOrder(value: unknown): value is SortOrder {
    return typeof value === 'string' && VALID_SORT_ORDERS.has(value);
  }

  let isLoading = $state<boolean>(true);
  let speciesData = $state<SpeciesData[]>([]);
  // Debounced copy of filters.searchTerm that actually drives filtering; the raw
  // filters.searchTerm stays live for the input box and the "filtered" badge.
  let debouncedSearchTerm = $state<string>('');
  let viewMode = $state<ViewMode>('grid');
  let selectedSpecies = $state<SpeciesData | null>(null);
  let showDetailModal = $state(false);

  // Read once so both filters and the applied-sort indicator start at the same persisted value.
  const restoredSortOrder = getStoredValue<SortOrder>(
    SORT_STORAGE_KEY,
    DEFAULT_SORT_ORDER,
    isSortOrder
  );

  let filters = $state<SpeciesFilters>({
    timePeriod: 'all',
    startDate: '',
    endDate: '',
    sortOrder: restoredSortOrder,
    searchTerm: '',
  });

  // Tracks the sort order that is actually applied to the table. Only the
  // explicit commit points (header click in handleSort, Apply Filters/mount/reset
  // via fetchData) update it; the filteredSpecies $derived sorts from it. This
  // keeps a pending dropdown change (filters.sortOrder) from reordering the table
  // until the user commits it, so an unrelated rerender never applies it early.
  let appliedSortOrder = $state<SortOrder>(restoredSortOrder);

  // Active column + direction for the header indicators, derived from the
  // applied sort (not the pending dropdown selection).
  let activeColumn = $derived(
    SORTABLE_COLUMNS.find(
      column => column.asc === appliedSortOrder || column.desc === appliedSortOrder
    )
  );
  let sortField = $derived(activeColumn?.field ?? '');
  let sortDirection: 'asc' | 'desc' = $derived(
    activeColumn?.desc === appliedSortOrder ? 'desc' : 'asc'
  );

  // Clicking a header: re-clicking the active column toggles direction; a new
  // column starts at its default (ascending for species name, descending else).
  function handleSort(field: string) {
    const column = SORTABLE_COLUMNS.find(c => c.field === field);
    if (!column) return;
    const next =
      sortField === field
        ? appliedSortOrder === column.asc
          ? column.desc
          : column.asc
        : field === SPECIES_COLUMN_FIELD
          ? column.asc
          : column.desc;
    filters.sortOrder = next;
    appliedSortOrder = next;
    setStoredValue<SortOrder>(SORT_STORAGE_KEY, next);
    // filteredSpecies is $derived and re-sorts automatically on appliedSortOrder.
  }

  // Set default dates on mount
  onMount(() => {
    const today = new Date();
    const lastMonth = new Date();
    lastMonth.setDate(today.getDate() - 30);

    filters.endDate = formatDateForInput(today);
    filters.startDate = formatDateForInput(lastMonth);

    // Fetch initial data
    fetchData();
  });

  function formatDateForInput(date: Date): string {
    return getLocalDateString(date);
  }

  function formatPercentage(value: number): string {
    return (value * 100).toFixed(1) + '%';
  }

  // Increments on every fetchData so an in-flight thumbnail loop from a previous
  // fetch can detect it has been superseded and stop mutating speciesData.
  let thumbnailFetchSeq = 0;

  async function fetchData() {
    isLoading = true;
    const fetchSeq = ++thumbnailFetchSeq;
    // Apply Filters (and mount/reset) commit the pending dropdown selection.
    appliedSortOrder = filters.sortOrder;
    setStoredValue<SortOrder>(SORT_STORAGE_KEY, filters.sortOrder);
    // Commit the search term immediately too, cancelling any pending debounce so
    // a just-typed term is not re-applied a moment later.
    clearTimeout(searchDebounce);
    debouncedSearchTerm = filters.searchTerm;

    try {
      // Determine date range based on time period
      let startDate, endDate;
      const today = new Date();

      switch (filters.timePeriod) {
        case 'today':
          startDate = formatDateForInput(today);
          endDate = startDate;
          break;
        case 'week':
          endDate = formatDateForInput(today);
          startDate = formatDateForInput(new Date(today.getTime() - 6 * 24 * 60 * 60 * 1000));
          break;
        case 'month':
          endDate = formatDateForInput(today);
          startDate = formatDateForInput(new Date(today.getTime() - 29 * 24 * 60 * 60 * 1000));
          break;
        case '90days':
          endDate = formatDateForInput(today);
          startDate = formatDateForInput(new Date(today.getTime() - 89 * 24 * 60 * 60 * 1000));
          break;
        case 'year':
          endDate = formatDateForInput(today);
          startDate = formatDateForInput(new Date(today.getTime() - 364 * 24 * 60 * 60 * 1000));
          break;
        case 'custom':
          startDate = filters.startDate;
          endDate = filters.endDate;
          break;
        case 'all':
        default:
          startDate = null;
          endDate = null;
          break;
      }

      // Build query parameters
      const params = new URLSearchParams();
      if (startDate) params.set('start_date', startDate);
      if (endDate) params.set('end_date', endDate);

      // Fetch species summary data
      const response = await fetch(
        buildAppUrl(`/api/v2/analytics/species/summary?${params.toString()}`)
      );

      if (!response.ok) {
        throw new Error(`Server responded with ${response.status}`);
      }

      const rawSpecies: SpeciesData[] = await response.json();
      // Backend returns relative URLs (e.g. /api/v2/media/image/...). Run them
      // through buildAppUrl so they include the configured base path (e.g.
      // /birdnet, HA Ingress token) before they end up in <img src=...>.
      speciesData = rawSpecies.map(species =>
        species.thumbnail_url
          ? { ...species, thumbnail_url: buildAppUrl(species.thumbnail_url) }
          : species
      );

      // Load thumbnails asynchronously after main data is displayed
      loadThumbnailsAsync(fetchSeq);
    } catch (error) {
      logger.error('Error fetching species data:', error);
      speciesData = [];
    } finally {
      isLoading = false;
    }
  }

  function makeDateComparator(field: 'first_heard' | 'last_heard', ascending: boolean) {
    return (a: LocalizedRow, b: LocalizedRow) => {
      // eslint-disable-next-line security/detect-object-injection
      const da = parseLocalDateString(a.species[field]);
      // eslint-disable-next-line security/detect-object-injection
      const db = parseLocalDateString(b.species[field]);
      // Sort invalid/missing dates consistently to the end so the comparator
      // stays transitive (returning 0 for any null pair would break sort order).
      if (!da && !db) return 0;
      if (!da) return 1;
      if (!db) return -1;
      return ascending ? da.getTime() - db.getTime() : db.getTime() - da.getTime();
    };
  }

  // Filtered + sorted rows for display. Search and name-sort operate on the
  // visitor-localized common name (displayName) so they match what the user
  // sees, while scientific name and the server common name stay searchable too.
  // Because localizeSpeciesName reads the species-dictionary $state, this
  // $derived re-runs when the dictionary loads or the locale changes - so the
  // page no longer needs an imperative applyFilters(). Sorting uses the
  // committed appliedSortOrder, never the pending filters.sortOrder dropdown.
  let filteredSpecies = $derived.by<SpeciesData[]>(() => {
    // Read the locale once for the name comparators below (avoids an O(n log n)
    // getLocale() per comparison). The locale/dictionary dependency is already
    // tracked via localizeSpeciesName, so this still re-runs on a locale switch.
    const locale = getLocale();
    // localize once per row; reused for search + sort below (small dataset: one
    // row per detected species, so a per-row Map lookup is negligible).
    const rows: LocalizedRow[] = speciesData.map(species => ({
      species,
      displayName: localizeSpeciesName(species.scientific_name, species.common_name),
    }));

    const searchLower = debouncedSearchTerm.trim().toLowerCase();
    const filtered = searchLower
      ? rows.filter(
          ({ species, displayName }) =>
            displayName.toLowerCase().includes(searchLower) ||
            species.common_name.toLowerCase().includes(searchLower) ||
            species.scientific_name.toLowerCase().includes(searchLower)
        )
      : rows;

    switch (appliedSortOrder) {
      case 'count_desc':
        filtered.sort((a, b) => b.species.count - a.species.count);
        break;
      case 'count_asc':
        filtered.sort((a, b) => a.species.count - b.species.count);
        break;
      case 'name_asc':
        filtered.sort((a, b) => a.displayName.localeCompare(b.displayName, locale));
        break;
      case 'name_desc':
        filtered.sort((a, b) => b.displayName.localeCompare(a.displayName, locale));
        break;
      case 'first_seen_desc':
        filtered.sort(makeDateComparator('first_heard', false));
        break;
      case 'first_seen_asc':
        filtered.sort(makeDateComparator('first_heard', true));
        break;
      case 'last_seen_desc':
        filtered.sort(makeDateComparator('last_heard', false));
        break;
      case 'last_seen_asc':
        filtered.sort(makeDateComparator('last_heard', true));
        break;
      case 'confidence_desc':
        filtered.sort((a, b) => b.species.avg_confidence - a.species.avg_confidence);
        break;
      case 'confidence_asc':
        filtered.sort((a, b) => a.species.avg_confidence - b.species.avg_confidence);
        break;
      case 'max_confidence_desc':
        filtered.sort((a, b) => b.species.max_confidence - a.species.max_confidence);
        break;
      case 'max_confidence_asc':
        filtered.sort((a, b) => a.species.max_confidence - b.species.max_confidence);
        break;
      default: {
        // Exhaustiveness guard: adding a SortOrder value without a case is a compile error.
        const _exhaustive: never = appliedSortOrder;
        void _exhaustive;
      }
    }

    return filtered.map(row => row.species);
  });

  function getFilteredCount(): number {
    return filteredSpecies.length;
  }

  function getTotalSpeciesCount(): number {
    return speciesData.length;
  }

  function getTotalDetections(): number {
    return speciesData.reduce((sum, species) => sum + species.count, 0);
  }

  function getTotalDetectionsText(): string {
    const total = getTotalDetections();
    return `${formatNumber(total)} ${t('analytics.stats.detections')}`;
  }

  function getAverageConfidence(): string {
    if (speciesData.length === 0) return '0%';
    const totalWeighted = speciesData.reduce(
      (sum, species) => sum + species.avg_confidence * species.count,
      0
    );
    const totalCount = getTotalDetections();
    if (totalCount === 0) return '0%';
    return ((totalWeighted / totalCount) * 100).toFixed(1) + '%';
  }

  function resetFilters() {
    filters.timePeriod = 'all';
    filters.sortOrder = DEFAULT_SORT_ORDER;
    // fetchData() below commits and persists the reset sort order (single commit point).
    filters.searchTerm = '';

    const today = new Date();
    const lastMonth = new Date();
    lastMonth.setDate(today.getDate() - 30);

    filters.endDate = formatDateForInput(today);
    filters.startDate = formatDateForInput(lastMonth);

    fetchData();
  }

  async function loadThumbnailsAsync(fetchSeq: number) {
    // Skip if we don't have species data
    if (!speciesData || speciesData.length === 0) {
      return;
    }

    // Get scientific names that need thumbnails
    const scientificNames = speciesData
      .filter(species => !species.thumbnail_url)
      .map(species => species.scientific_name);

    if (scientificNames.length === 0) {
      return;
    }

    try {
      // Fetch thumbnails in batches to avoid overwhelming the server
      const batchSize = 20;
      for (let i = 0; i < scientificNames.length; i += batchSize) {
        // A newer fetchData superseded this run; stop fetching and mutating stale state.
        if (fetchSeq !== thumbnailFetchSeq) return;
        const batch = scientificNames.slice(i, i + batchSize);

        // Create query parameters for this batch
        const params = new URLSearchParams();
        batch.forEach(name => params.append('species', name));

        // Fetch thumbnails for this batch
        const response = await fetch(
          buildAppUrl(`/api/v2/analytics/species/thumbnails?${params.toString()}`)
        );
        if (response.ok) {
          const thumbnails = await response.json();
          // Re-check after the await: a newer fetch may have replaced speciesData
          // while this batch was in flight, so don't apply stale thumbnails to it.
          if (fetchSeq !== thumbnailFetchSeq) return;

          // Update species data with fetched thumbnails. Backend URLs are
          // relative; buildAppUrl prepends the configured base path so the
          // image request resolves correctly behind a reverse proxy.
          speciesData = speciesData.map(species => {
            const url = thumbnails[species.scientific_name];
            if (url) {
              return { ...species, thumbnail_url: buildAppUrl(url) };
            }
            return species;
          });
          // filteredSpecies is $derived; reassigning speciesData re-renders it.
        }

        // Small delay between batches
        if (i + batchSize < scientificNames.length) {
          await new Promise(resolve => setTimeout(resolve, 100));
        }
      }
    } catch (error) {
      logger.error('Error loading thumbnails:', error);
      // Continue without thumbnails - don't break the UI
    }
  }

  function exportData() {
    // Generate CSV content
    const headers = [
      'Common Name',
      'Scientific Name',
      'Count',
      'Avg Confidence',
      'Max Confidence',
      'First Detected',
      'Last Detected',
    ];
    // Export stays canonical (server-locale common name + scientific name) so the
    // CSV is locale-stable and re-importable; do not substitute the localized name.
    const rows = filteredSpecies.map(species => [
      species.common_name,
      species.scientific_name,
      species.count,
      (species.avg_confidence * 100).toFixed(1) + '%',
      (species.max_confidence * 100).toFixed(1) + '%',
      species.first_heard ? formatDateTime(species.first_heard) : '',
      species.last_heard ? formatDateTime(species.last_heard) : '',
    ]);

    // Create CSV string
    const csvContent = [
      headers.join(','),
      ...rows.map(row => row.map(cell => `"${cell}"`).join(',')),
    ].join('\n');

    // Create and download file
    const blob = new Blob([csvContent], { type: 'text/csv;charset=utf-8;' });
    downloadBlob(blob, `birdnet-species-${getLocalDateString()}.csv`);
  }

  let searchDebounce: ReturnType<typeof setTimeout> | undefined;

  // Cancel a pending debounce timer when the page unmounts so it can't fire
  // (and write state) after the component is gone.
  onDestroy(() => clearTimeout(searchDebounce));

  function handleSearchInput(e: Event): void {
    const target = e.target as HTMLInputElement;
    // Keep filters.searchTerm live (input box + "filtered" badge), but debounce
    // committing it to debouncedSearchTerm, which the filteredSpecies $derived reads.
    filters.searchTerm = target.value;
    clearTimeout(searchDebounce);
    searchDebounce = setTimeout(() => {
      debouncedSearchTerm = filters.searchTerm;
    }, 300);
  }

  function handleSpeciesClick(species: SpeciesData) {
    selectedSpecies = species;
    showDetailModal = true;
  }

  function handleCloseDetailModal() {
    showDetailModal = false;
    selectedSpecies = null;
  }

  // ---- Manage view (authenticated, desktop-only species curation) ----

  // Membership sets keyed by the species' server common name, matching the
  // existing exclude endpoint contract. Reactive collections re-render toggles.
  let excludedSet = new SvelteSet<string>();
  let includedSet = new SvelteSet<string>();
  let confirmedSet = new SvelteSet<string>();

  // Per-list in-flight species (keyed by common name) to block double-click races.
  let togglingExcluded = new SvelteSet<string>();
  let togglingIncluded = new SvelteSet<string>();
  let togglingConfirmed = new SvelteSet<string>();

  // Per-species review stats and range scores (keyed by scientific name).
  let reviewStats = new SvelteMap<string, ReviewStat>();
  let rangeScores = new SvelteMap<string, number>();
  let rangeLoading = $state(false);
  let manageDataLoaded = $state(false);

  // Manage-only sort state, never persisted to localStorage.
  let manageSortKey = $state<ManageSortKey>('count');
  let manageSortDirection = $state<'asc' | 'desc'>('desc');

  // Delete confirmation modal state.
  let deleteTarget = $state<SpeciesData | null>(null);
  let showDeleteModal = $state(false);
  let deleteInFlight = $state(false);
  let deleteError = $state<string | null>(null);

  // Default sort direction per Manage-only column (only the name column starts ascending).
  const MANAGE_SORT_COLUMNS: { field: ManageSortKey; defaultAsc: boolean }[] = [
    { field: 'name', defaultAsc: true },
    { field: 'count', defaultAsc: false },
    { field: 'max_confidence', defaultAsc: false },
    { field: 'last_seen', defaultAsc: false },
    { field: 'review', defaultAsc: false },
    { field: 'range', defaultAsc: false },
  ];

  function showManageView() {
    viewMode = 'manage';
    void fetchManageData();
  }

  async function fetchManageData() {
    if (manageDataLoaded) {
      void loadRangeScores();
      return;
    }
    try {
      const [stats, included, confirmed, excluded] = await Promise.all([
        api.get<ReviewStat[]>('/api/v2/analytics/species/review-stats'),
        api.get<{ species: string[] }>('/api/v2/detections/included'),
        api.get<{ species: string[] }>('/api/v2/detections/confirmed'),
        api.get<{ species: string[] }>('/api/v2/detections/ignored'),
      ]);
      reviewStats.clear();
      for (const s of stats) reviewStats.set(s.scientificName, s);
      includedSet.clear();
      for (const n of included.species ?? []) includedSet.add(n);
      confirmedSet.clear();
      for (const n of confirmed.species ?? []) confirmedSet.add(n);
      excludedSet.clear();
      for (const n of excluded.species ?? []) excludedSet.add(n);
      manageDataLoaded = true;
    } catch (error) {
      logger.error('Error fetching manage data:', error);
    }
    // Render the table immediately; range scores stream in asynchronously.
    void loadRangeScores();
  }

  async function loadRangeScores() {
    if (cachedRangeScores) {
      if (rangeScores.size === 0) {
        for (const [name, score] of cachedRangeScores) rangeScores.set(name, score);
      }
      return;
    }
    rangeLoading = true;
    try {
      const result = await settingsActions.loadRangeFilterSpecies();
      const map = new Map<string, number>();
      for (const s of result.species) {
        if (s.scientificName && typeof s.score === 'number') {
          map.set(s.scientificName, s.score);
        }
      }
      cachedRangeScores = map;
      for (const [name, score] of map) rangeScores.set(name, score);
    } catch (error) {
      logger.error('Error loading range scores:', error);
    } finally {
      rangeLoading = false;
    }
  }

  function handleManageSort(field: string) {
    const column = MANAGE_SORT_COLUMNS.find(c => c.field === field);
    if (!column) return;
    if (manageSortKey === column.field) {
      manageSortDirection = manageSortDirection === 'asc' ? 'desc' : 'asc';
    } else {
      manageSortKey = column.field;
      manageSortDirection = column.defaultAsc ? 'asc' : 'desc';
    }
  }

  // Review activity total used for the "review" sort (most-reviewed first).
  function reviewActivity(species: SpeciesData): number {
    const stat = reviewStats.get(species.scientific_name);
    return stat ? stat.verified + stat.rejected : 0;
  }

  // Search-filtered rows (reuses filteredSpecies) re-sorted by the Manage-only key.
  let manageRows = $derived.by<SpeciesData[]>(() => {
    if (viewMode !== 'manage') return filteredSpecies;
    const locale = getLocale();
    const dir = manageSortDirection === 'asc' ? 1 : -1;
    const rows = [...filteredSpecies];
    rows.sort((a, b) => {
      switch (manageSortKey) {
        case 'name':
          return (
            dir *
            localizeSpeciesName(a.scientific_name, a.common_name).localeCompare(
              localizeSpeciesName(b.scientific_name, b.common_name),
              locale
            )
          );
        case 'count':
          return dir * (a.count - b.count);
        case 'max_confidence':
          return dir * (a.max_confidence - b.max_confidence);
        case 'last_seen': {
          const da = parseLocalDateString(a.last_heard);
          const db = parseLocalDateString(b.last_heard);
          if (!da && !db) return 0;
          if (!da) return 1;
          if (!db) return -1;
          return dir * (da.getTime() - db.getTime());
        }
        case 'review':
          return dir * (reviewActivity(a) - reviewActivity(b));
        case 'range':
          return (
            dir *
            ((rangeScores.get(a.scientific_name) ?? -1) -
              (rangeScores.get(b.scientific_name) ?? -1))
          );
        default: {
          const _exhaustive: never = manageSortKey;
          void _exhaustive;
          return 0;
        }
      }
    });
    return rows;
  });

  function formatDateOnly(value: string): string {
    if (!value) return '—';
    const parsed = parseLocalDateString(value);
    return parsed ? getLocalDateString(parsed) : '—';
  }

  function reviewRatioText(species: SpeciesData): string {
    const stat = reviewStats.get(species.scientific_name);
    if (!stat || (stat.verified === 0 && stat.rejected === 0)) return '—';
    return `${stat.verified} / ${stat.rejected}`;
  }

  async function toggleMembership(
    list: 'excluded' | 'included' | 'confirmed',
    species: SpeciesData
  ) {
    const name = species.common_name;
    const inflight =
      list === 'excluded'
        ? togglingExcluded
        : list === 'included'
          ? togglingIncluded
          : togglingConfirmed;
    if (inflight.has(name)) return;
    const set =
      list === 'excluded' ? excludedSet : list === 'included' ? includedSet : confirmedSet;
    const path =
      list === 'excluded'
        ? '/api/v2/detections/ignore'
        : list === 'included'
          ? '/api/v2/detections/include'
          : '/api/v2/detections/confirm';

    inflight.add(name);
    try {
      const resp = await api.post<{ action: string }>(path, { common_name: name });
      if (resp.action === 'removed') {
        set.delete(name);
      } else {
        set.add(name);
      }
    } catch (error) {
      logger.error(`Error toggling ${list} membership:`, error);
    } finally {
      inflight.delete(name);
    }
  }

  function requestDelete(species: SpeciesData) {
    deleteTarget = species;
    deleteError = null;
    showDeleteModal = true;
  }

  // All-time detection count for the confirmation modal (not the filtered count).
  let deleteAllTimeCount = $derived(
    deleteTarget ? (reviewStats.get(deleteTarget.scientific_name)?.total ?? deleteTarget.count) : 0
  );

  async function confirmDelete() {
    if (!deleteTarget) return;
    const target = deleteTarget;
    deleteInFlight = true;
    deleteError = null;
    try {
      const resp = await api.post<{ deleted: number; skipped: number }>(
        '/api/v2/detections/species/delete',
        { scientific_name: target.scientific_name }
      );
      if (resp.skipped === 0) {
        // Full deletion: drop the row locally.
        speciesData = speciesData.filter(s => s.scientific_name !== target.scientific_name);
      } else {
        // Partial deletion (locked detections remain): refresh summary + review data.
        manageDataLoaded = false;
        await fetchData();
        await fetchManageData();
      }
      showDeleteModal = false;
      deleteTarget = null;
    } catch (error) {
      logger.error('Error deleting species detections:', error);
      deleteError = t('analytics.species.manage.deleteFailed');
    } finally {
      deleteInFlight = false;
    }
  }

  function cancelDelete() {
    showDeleteModal = false;
    deleteTarget = null;
    deleteError = null;
  }
</script>

<div class="col-span-12 space-y-4" role="region" aria-label={t('analytics.species.title')}>
  <!-- Page Header -->
  <div class="card bg-[var(--color-base-100)] shadow-xs">
    <div class="card-body card-padding">
      <div class="flex justify-between items-start">
        <div>
          <h1 class="card-title text-2xl">{t('analytics.species.title')}</h1>
          <p class="text-[var(--color-base-content)] opacity-60">
            {t('analytics.species.subtitle')}
          </p>
        </div>
        <div class="flex gap-4">
          <StatCard
            title={t('analytics.stats.totalSpecies')}
            value={getTotalSpeciesCount()}
            subtitle={getTotalDetectionsText()}
            iconClassName="bg-[var(--color-primary)]/20"
          >
            {#snippet icon()}
              <svg
                xmlns="http://www.w3.org/2000/svg"
                class="h-6 w-6 text-[var(--color-primary)]"
                viewBox="0 0 20 20"
                fill="currentColor"
              >
                <path
                  d="M5 3a2 2 0 00-2 2v2a2 2 0 002 2h2a2 2 0 002-2V5a2 2 0 00-2-2H5zM5 11a2 2 0 00-2 2v2a2 2 0 002 2h2a2 2 0 002-2v-2a2 2 0 00-2-2H5zM11 5a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2h-2a2 2 0 01-2-2V5zM13 11a2 2 0 00-2 2v2a2 2 0 002 2h2a2 2 0 002-2v-2a2 2 0 00-2-2h-2z"
                />
              </svg>
            {/snippet}
          </StatCard>

          <StatCard
            title={t('analytics.stats.avgConfidence')}
            value={getAverageConfidence()}
            subtitle={t('analytics.stats.overallAverage')}
            iconClassName="bg-[var(--color-secondary)]/20"
          >
            {#snippet icon()}
              <svg
                xmlns="http://www.w3.org/2000/svg"
                class="h-6 w-6 text-[var(--color-secondary)]"
                viewBox="0 0 20 20"
                fill="currentColor"
              >
                <path
                  fill-rule="evenodd"
                  d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7-4a1 1 0 11-2 0 1 1 0 012 0zM9 9a.75.75 0 000 1.5h.253a.25.25 0 01.244.304l-.459 2.066A1.75 1.75 0 0010.747 15H11a.75.75 0 000-1.5h-.253a.25.25 0 01-.244-.304l.459-2.066A1.75 1.75 0 009.253 9H9z"
                  clip-rule="evenodd"
                />
              </svg>
            {/snippet}
          </StatCard>
        </div>
      </div>
    </div>
  </div>

  <!-- Filter Controls -->
  <SpeciesFilterForm
    bind:filters
    {isLoading}
    filteredCount={getFilteredCount()}
    onSubmit={fetchData}
    onReset={resetFilters}
    onExport={exportData}
    onSearchInput={handleSearchInput}
  />

  <!-- Species Grid/List -->
  <div class="card bg-[var(--color-base-100)] shadow-xs">
    <div class="card-body card-padding">
      <!-- View Toggle -->
      <div class="flex justify-between items-center mb-4">
        <h2 class="card-title">{t('analytics.species.speciesList')}</h2>
        <div class="join hidden sm:flex">
          <button
            class="btn btn-sm join-item"
            class:btn-active={viewMode === 'grid'}
            onclick={() => (viewMode = 'grid')}
            aria-label={t('analytics.species.switchToGrid')}
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-4 w-4"
              viewBox="0 0 20 20"
              fill="currentColor"
            >
              <path
                d="M5 3a2 2 0 00-2 2v2a2 2 0 002 2h2a2 2 0 002-2V5a2 2 0 00-2-2H5zM5 11a2 2 0 00-2 2v2a2 2 0 002 2h2a2 2 0 002-2v-2a2 2 0 00-2-2H5zM11 5a2 2 0 012-2h2a2 2 0 012 2v2a2 2 0 01-2 2h-2a2 2 0 01-2-2V5zM13 11a2 2 0 00-2 2v2a2 2 0 002 2h2a2 2 0 002-2v-2a2 2 0 00-2-2h-2z"
              />
            </svg>
          </button>
          <button
            class="btn btn-sm join-item"
            class:btn-active={viewMode === 'list'}
            onclick={() => (viewMode = 'list')}
            aria-label={t('analytics.species.switchToList')}
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-4 w-4"
              viewBox="0 0 20 20"
              fill="currentColor"
            >
              <path
                fill-rule="evenodd"
                d="M3 4a1 1 0 011-1h12a1 1 0 110 2H4a1 1 0 01-1-1zm0 4a1 1 0 011-1h12a1 1 0 110 2H4a1 1 0 01-1-1zm0 4a1 1 0 011-1h12a1 1 0 110 2H4a1 1 0 01-1-1zm0 4a1 1 0 011-1h12a1 1 0 110 2H4a1 1 0 01-1-1z"
                clip-rule="evenodd"
              />
            </svg>
          </button>
          {#if $isAuthenticated}
            <button
              type="button"
              class="btn btn-sm join-item"
              class:btn-active={viewMode === 'manage'}
              onclick={showManageView}
              aria-label={t('analytics.species.switchToManage')}
            >
              <SlidersHorizontal class="h-4 w-4" />
            </button>
          {/if}
        </div>
      </div>

      <!-- Loading State -->
      {#if isLoading}
        <div class="flex justify-center items-center p-8">
          <span class="loading loading-spinner loading-lg text-[var(--color-primary)]"></span>
        </div>
      {/if}

      <!-- Mobile View - Compact List -->
      {#if !isLoading && viewMode === 'grid' && filteredSpecies.length > 0}
        <div class="sm:hidden space-y-2">
          {#each filteredSpecies as species, index (`${species.scientific_name}_${index}`)}
            <SpeciesCardMobile {species} variant="compact" onClick={handleSpeciesClick} />
          {/each}
        </div>
      {/if}

      <!-- Desktop Grid View -->
      {#if !isLoading && viewMode === 'grid' && filteredSpecies.length > 0}
        <div class="species-grid hidden sm:grid">
          {#each filteredSpecies as species, index (`${species.scientific_name}_${index}`)}
            <SpeciesCard {species} />
          {/each}
        </div>
      {/if}

      <!-- List View -->
      {#if !isLoading && viewMode === 'list'}
        <div class="overflow-x-auto">
          <table class="table w-full hidden sm:table">
            <thead>
              <tr>
                {#each SORTABLE_COLUMNS as { field, labelKey } (field)}
                  <SortableHeader
                    label={t(labelKey)}
                    {field}
                    activeField={sortField}
                    direction={sortDirection}
                    onSort={handleSort}
                  />
                {/each}
              </tr>
            </thead>
            <tbody>
              {#each filteredSpecies as species, index (`${species.scientific_name}_${index}`)}
                {@const displayName = localizeSpeciesName(
                  species.scientific_name,
                  species.common_name
                )}
                <tr
                  class={index % 2 === 0
                    ? 'bg-[var(--color-base-100)]'
                    : 'bg-[var(--color-base-200)]'}
                >
                  <td>
                    <div class="flex items-center gap-3">
                      <div class="avatar">
                        <div
                          class="mask mask-squircle w-12 h-12"
                          class:bg-[var(--color-base-300)]={!species.thumbnail_url}
                        >
                          {#if species.thumbnail_url}
                            <img
                              src={species.thumbnail_url}
                              alt={displayName}
                              onerror={e => {
                                const img = e.target as HTMLImageElement;
                                if (img) {
                                  img.style.display = 'none';
                                  img.parentElement?.classList.add('bg-[var(--color-base-300)]');
                                }
                              }}
                            />
                          {/if}
                        </div>
                      </div>
                      <div>
                        <div class="font-bold">
                          {displayName}
                        </div>
                        <div class="text-sm opacity-50 italic">{species.scientific_name}</div>
                      </div>
                    </div>
                  </td>
                  <td class="font-semibold">{species.count}</td>
                  <td>
                    <div class="flex items-center gap-2">
                      <progress
                        class="progress w-20 {species.avg_confidence >= 0.8
                          ? 'progress-success'
                          : species.avg_confidence >= 0.4
                            ? 'progress-warning'
                            : 'progress-error'}"
                        value={species.avg_confidence}
                        max="1"
                      ></progress>
                      <span class="text-sm">{formatPercentage(species.avg_confidence)}</span>
                    </div>
                  </td>
                  <td>{formatPercentage(species.max_confidence)}</td>
                  <td class="text-sm">{formatDateTime(species.first_heard)}</td>
                  <td class="text-sm">{formatDateTime(species.last_heard)}</td>
                </tr>
              {/each}
            </tbody>
          </table>
          <!-- Mobile list view -->
          <div class="sm:hidden space-y-2">
            {#each filteredSpecies as species, index (`${species.scientific_name}_${index}`)}
              <SpeciesCardMobile {species} variant="list" onClick={handleSpeciesClick} />
            {/each}
          </div>
        </div>
      {/if}

      <!-- Manage View (authenticated, desktop-only) -->
      {#if !isLoading && viewMode === 'manage' && manageRows.length > 0}
        <div class="overflow-x-auto hidden sm:block">
          <table class="table w-full">
            <thead>
              <tr>
                <SortableHeader
                  label={t('analytics.species.headers.species')}
                  field="name"
                  activeField={manageSortKey}
                  direction={manageSortDirection}
                  onSort={handleManageSort}
                />
                <SortableHeader
                  label={t('analytics.species.headers.detections')}
                  field="count"
                  activeField={manageSortKey}
                  direction={manageSortDirection}
                  onSort={handleManageSort}
                />
                <SortableHeader
                  label={t('analytics.species.headers.maxConfidence')}
                  field="max_confidence"
                  activeField={manageSortKey}
                  direction={manageSortDirection}
                  onSort={handleManageSort}
                />
                <SortableHeader
                  label={t('analytics.species.headers.lastDetected')}
                  field="last_seen"
                  activeField={manageSortKey}
                  direction={manageSortDirection}
                  onSort={handleManageSort}
                />
                <th>{t('analytics.species.manage.headers.excluded')}</th>
                <th>{t('analytics.species.manage.headers.included')}</th>
                <SortableHeader
                  label={t('analytics.species.manage.headers.reviewRatio')}
                  field="review"
                  activeField={manageSortKey}
                  direction={manageSortDirection}
                  onSort={handleManageSort}
                />
                <SortableHeader
                  label={t('analytics.species.manage.headers.rangeProbability')}
                  field="range"
                  activeField={manageSortKey}
                  direction={manageSortDirection}
                  onSort={handleManageSort}
                />
                <th>{t('analytics.species.manage.headers.confirmed')}</th>
                <th>{t('analytics.species.manage.headers.actions')}</th>
              </tr>
            </thead>
            <tbody>
              {#each manageRows as species, index (`${species.scientific_name}_${index}`)}
                {@const displayName = localizeSpeciesName(
                  species.scientific_name,
                  species.common_name
                )}
                <tr
                  class={index % 2 === 0
                    ? 'bg-[var(--color-base-100)]'
                    : 'bg-[var(--color-base-200)]'}
                >
                  <td>
                    <div class="font-bold">{displayName}</div>
                    <div class="text-sm opacity-50 italic">{species.scientific_name}</div>
                  </td>
                  <td class="font-semibold">{species.count}</td>
                  <td>{formatPercentage(species.max_confidence)}</td>
                  <td class="text-sm">{formatDateOnly(species.last_heard)}</td>
                  <td>
                    <Checkbox
                      checked={excludedSet.has(species.common_name)}
                      disabled={togglingExcluded.has(species.common_name)}
                      onchange={() => toggleMembership('excluded', species)}
                    />
                  </td>
                  <td>
                    <Checkbox
                      checked={includedSet.has(species.common_name)}
                      disabled={togglingIncluded.has(species.common_name)}
                      onchange={() => toggleMembership('included', species)}
                    />
                  </td>
                  <td class="text-sm">{reviewRatioText(species)}</td>
                  <td class="text-sm">
                    {#if rangeLoading && !rangeScores.has(species.scientific_name)}
                      <span
                        class="loading loading-spinner loading-xs"
                        aria-label={t('common.ui.loading')}
                      ></span>
                    {:else if rangeScores.has(species.scientific_name)}
                      {formatPercentage(rangeScores.get(species.scientific_name) ?? 0)}
                    {:else}
                      —
                    {/if}
                  </td>
                  <td>
                    <Checkbox
                      checked={confirmedSet.has(species.common_name)}
                      disabled={togglingConfirmed.has(species.common_name)}
                      onchange={() => toggleMembership('confirmed', species)}
                    />
                  </td>
                  <td>
                    <button
                      type="button"
                      class="btn btn-ghost btn-xs text-[var(--color-error)]"
                      onclick={() => requestDelete(species)}
                      aria-label={t('analytics.species.manage.delete')}
                    >
                      <Trash2 class="h-4 w-4" />
                    </button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}

      <!-- Empty State -->
      {#if !isLoading && filteredSpecies.length === 0}
        <div class="text-center py-8 text-[var(--color-base-content)] opacity-50">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-16 w-16 mx-auto mb-4 opacity-20"
            viewBox="0 0 20 20"
            fill="currentColor"
          >
            <path
              fill-rule="evenodd"
              d="M5 3a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2V5a2 2 0 00-2-2H5zm9 4a1 1 0 10-2 0v6a1 1 0 102 0V7zm-3 2a1 1 0 10-2 0v4a1 1 0 102 0V9zm-3 3a1 1 0 10-2 0v1a1 1 0 102 0v-1z"
              clip-rule="evenodd"
            />
          </svg>
          <p>{t('analytics.species.noSpeciesFound')}</p>
        </div>
      {/if}
    </div>
  </div>
</div>

<!-- Mobile Species Detail Modal -->
<SpeciesDetailModal
  species={selectedSpecies}
  isOpen={showDetailModal}
  onClose={handleCloseDetailModal}
/>

<!-- Delete species confirmation (Manage view) -->
<Modal
  isOpen={showDeleteModal}
  type="confirm"
  title={t('analytics.species.manage.deleteTitle')}
  confirmLabel={t('analytics.species.manage.deleteConfirm')}
  confirmVariant="error"
  loading={deleteInFlight}
  onClose={cancelDelete}
  onConfirm={confirmDelete}
>
  {#if deleteTarget}
    <p>
      {t('analytics.species.manage.deleteMessage', {
        species: localizeSpeciesName(deleteTarget.scientific_name, deleteTarget.common_name),
        count: formatNumber(deleteAllTimeCount),
      })}
    </p>
    <p class="text-sm opacity-70 mt-2">{t('analytics.species.manage.deleteWarning')}</p>
    {#if deleteError}
      <p class="text-sm text-[var(--color-error)] mt-2" role="alert">{deleteError}</p>
    {/if}
  {/if}
</Modal>

<!-- Mobile Audio Player -->

<style>
  .card-padding {
    padding: 1rem;
  }

  @media (min-width: 768px) {
    .card-padding {
      padding: 1.5rem;
    }
  }

  .species-grid {
    display: grid;
    grid-template-columns: 1fr;
    gap: 1rem;
  }

  @media (min-width: 768px) {
    .species-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (min-width: 1024px) {
    .species-grid {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }
  }

  @media (min-width: 1280px) {
    .species-grid {
      grid-template-columns: repeat(4, minmax(0, 1fr));
    }
  }
</style>
