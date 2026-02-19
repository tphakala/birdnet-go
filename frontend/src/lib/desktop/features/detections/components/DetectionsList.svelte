<!--
  DetectionsList.svelte

  A container component that orchestrates the display of multiple bird detection records.
  Manages pagination, loading states, sorting, and view mode switching.

  Usage:
  - Main detection pages
  - Search results presentation
  - Filtered detection views
  - Administrative detection management interfaces

  Features:
  - Paginated detection display with sortable columns
  - Toggle between table and card views (persisted in localStorage)
  - Loading and error state handling
  - Empty state with helpful messaging
  - Responsive layout (table on desktop, cards on mobile)
  - Integration with DetectionRow and DetectionCard components
  - Refresh functionality

  Props:
  - data: DetectionsListData | null - Paginated detection data
  - loading?: boolean - Loading state indicator
  - error?: string | null - Error message display
  - onPageChange?: (page: number) => void - Pagination handler
  - onDetailsClick?: (id: number) => void - Detail view handler
  - onRefresh?: () => void - Data refresh handler
  - onNumResultsChange?: (numResults: number) => void - Results per page handler
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import MobileAudioPlayer from '$lib/desktop/components/media/MobileAudioPlayer.svelte';
  import EmptyState from '$lib/desktop/components/ui/EmptyState.svelte';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import Pagination from '$lib/desktop/components/ui/Pagination.svelte';
  import SortableHeader from '$lib/desktop/components/ui/SortableHeader.svelte';
  import ViewToggle from '$lib/desktop/components/ui/ViewToggle.svelte';
  import { t } from '$lib/i18n';
  import type { Detection, DetectionsListData } from '$lib/types/detection.types';
  import { cn } from '$lib/utils/cn';
  import { XCircle } from '@lucide/svelte';
  import { untrack } from 'svelte';
  import DetectionCardMobile from './DetectionCardMobile.svelte';
  import DetectionRow from './DetectionRow.svelte';
  import DetectionsCardView from './DetectionsCardView.svelte';

  type SortField = 'dateTime' | 'species' | 'confidence' | 'status';
  type SortDirection = 'asc' | 'desc';

  interface Props {
    data: DetectionsListData | null;
    loading?: boolean;
    error?: string | null;
    onPageChange?: (_page: number) => void;
    onDetailsClick?: (_id: number) => void;
    onRefresh?: () => void;
    onNumResultsChange?: (_numResults: number) => void;
    className?: string;
  }

  let {
    data,
    loading = false,
    error = null,
    onPageChange,
    onDetailsClick,
    onRefresh,
    onNumResultsChange,
    className = '',
  }: Props = $props();

  // Generate title based on query type
  const title = $derived.by(() => {
    if (!data) return t('detections.title');

    switch (data.queryType) {
      case 'hourly':
        if (data.duration && data.duration > 1) {
          return t('detections.titles.hourlyRange', {
            startHour: data.hour,
            endHour: (data.hour || 0) + data.duration,
            date: data.date,
          });
        }
        return t('detections.titles.hourly', { hour: data.hour, date: data.date });

      case 'species':
        return t('detections.titles.species', { species: data.species, date: data.date });

      case 'search':
        return t('detections.titles.search', { query: data.search });

      default:
        return t('detections.titles.allDetections', { date: data.date });
    }
  });

  function handlePageChange(page: number) {
    if (onPageChange && data) {
      onPageChange(page);
    }
  }

  const RESULTS_OPTIONS = [
    { value: '10', label: '10' },
    { value: '25', label: '25' },
    { value: '50', label: '50' },
    { value: '100', label: '100' },
  ];

  function handleNumResultsChange(value: string | string[]) {
    const numResults = parseInt(value as string);
    if (isNaN(numResults) || ![10, 25, 50, 100].includes(numResults)) return;
    selectedNumResults = String(numResults);
    onNumResultsChange?.(numResults);
  }

  // State for number of results - captures initial value without creating dependency
  // Uses untrack() to explicitly capture initial value only (local state is independent after init)
  let selectedNumResults = $state(untrack(() => String(data?.numResults ?? 25)));

  // --- View mode state (persisted in localStorage) ---
  const VIEW_STORAGE_KEY = 'detectionsViewMode';

  function loadViewMode(): 'table' | 'cards' {
    if (typeof window === 'undefined') return 'table';
    try {
      const stored = localStorage.getItem(VIEW_STORAGE_KEY);
      if (stored === 'cards') return 'cards';
    } catch {
      // localStorage unavailable
    }
    return 'table';
  }

  let viewMode = $state<'table' | 'cards'>(loadViewMode());

  function handleViewChange(mode: 'table' | 'cards') {
    viewMode = mode;
    try {
      localStorage.setItem(VIEW_STORAGE_KEY, mode);
    } catch {
      // localStorage unavailable
    }
  }

  // --- Sort state ---
  let sortField = $state<SortField>('dateTime');
  let sortDirection = $state<SortDirection>('desc');

  const SORT_FIELDS: Set<string> = new Set<string>(['dateTime', 'species', 'confidence', 'status']);

  function isSortField(field: string): field is SortField {
    return SORT_FIELDS.has(field);
  }

  function handleSort(field: string) {
    if (!isSortField(field)) return;
    if (sortField === field) {
      sortDirection = sortDirection === 'asc' ? 'desc' : 'asc';
    } else {
      sortField = field;
      sortDirection = field === 'dateTime' ? 'desc' : 'asc';
    }
  }

  /** Verification status sort order: correct > unverified > false_positive */
  const STATUS_ORDER: Record<string, number> = {
    correct: 0,
    unverified: 1,
    false_positive: 2,
  };

  /** Sort detections client-side within the current page */
  const sortedDetections = $derived.by(() => {
    if (!data) return [];
    const items = [...data.notes];

    items.sort((a: Detection, b: Detection) => {
      let cmp = 0;

      switch (sortField) {
        case 'dateTime': {
          // Compare date+time as string (YYYY-MM-DD HH:MM:SS sorts lexicographically)
          const aKey = `${a.date} ${a.time}`;
          const bKey = `${b.date} ${b.time}`;
          cmp = aKey.localeCompare(bKey);
          break;
        }
        case 'species':
          cmp = a.commonName.localeCompare(b.commonName);
          break;
        case 'confidence':
          cmp = a.confidence - b.confidence;
          break;
        case 'status':
          cmp = (STATUS_ORDER[a.verified] ?? 1) - (STATUS_ORDER[b.verified] ?? 1);
          break;
      }

      return sortDirection === 'asc' ? cmp : -cmp;
    });

    return items;
  });

  // Mobile audio player state
  let showMobilePlayer = $state(false);
  let selectedAudioUrl = $state('');
  let selectedSpeciesName = $state('');
  let selectedDetectionId = $state<number | undefined>(undefined);

  function handlePlayMobileAudio(payload: {
    audioUrl: string;
    speciesName: string;
    detectionId: number;
  }) {
    selectedAudioUrl = payload.audioUrl;
    selectedSpeciesName = payload.speciesName;
    selectedDetectionId = payload.detectionId;
    showMobilePlayer = true;
  }

  function handleCloseMobilePlayer() {
    showMobilePlayer = false;
    selectedAudioUrl = '';
    selectedSpeciesName = '';
    selectedDetectionId = undefined;
  }
</script>

<div class={cn(className)}>
  <div class="card-body grow-0 p-2 sm:p-4 sm:pt-3">
    <div class="flex justify-between items-center">
      <!-- Title -->
      <span class="card-title grow text-base sm:text-xl">
        {title}
      </span>

      <!-- Controls: view toggle + results selector -->
      <div class="flex items-center gap-3">
        <!-- View toggle (hidden on mobile - always shows mobile cards) -->
        <div class="hidden md:block">
          <ViewToggle view={viewMode} onViewChange={handleViewChange} />
        </div>

        <SelectDropdown
          options={RESULTS_OPTIONS}
          value={selectedNumResults}
          size="xs"
          menuSize="sm"
          variant="button"
          className="w-16"
          onChange={handleNumResultsChange}
        />
      </div>
    </div>
  </div>

  <!-- ARIA live region for accessibility -->
  <div class="sr-only" aria-live="polite">
    {#if loading}
      Loading {selectedNumResults} results...
    {:else if data}
      Showing {data.showingFrom} to {data.showingTo} of {data.totalResults} results
    {/if}
  </div>

  <!-- Content -->
  <div class="block w-full overflow-x-auto relative">
    {#if loading && data}
      <!-- Loading overlay when updating existing data -->
      <div class="absolute inset-0 bg-base-100/50 z-10 flex justify-center items-center">
        <LoadingSpinner size="lg" />
      </div>
    {/if}

    {#if loading && !data}
      <!-- Initial loading state -->
      <div class="flex justify-center items-center py-8">
        <LoadingSpinner size="lg" />
      </div>
    {:else if error}
      <div class="px-4 py-8">
        <div class="alert alert-error">
          <XCircle class="size-6" />
          <span>{error}</span>
        </div>
      </div>
    {:else if !data || data.notes.length === 0}
      <EmptyState
        title={t('detections.empty.title')}
        description={t('detections.empty.description')}
        className="py-8"
      />
    {:else}
      <!-- Desktop/tablet: table or card view -->
      <div class="hidden md:block">
        {#if viewMode === 'table'}
          <table class="w-full">
            <caption class="sr-only">{t('detections.table.caption')}</caption>
            <thead>
              <tr class="detection-header-list">
                <SortableHeader
                  label={t('detections.headers.dateTime')}
                  field="dateTime"
                  activeField={sortField}
                  direction={sortDirection}
                  onSort={handleSort}
                />
                <th scope="col" class="hidden md:table-cell">{t('detections.headers.weather')}</th>
                <SortableHeader
                  label={t('detections.headers.species')}
                  field="species"
                  activeField={sortField}
                  direction={sortDirection}
                  onSort={handleSort}
                />
                <SortableHeader
                  label={t('detections.headers.confidence')}
                  field="confidence"
                  activeField={sortField}
                  direction={sortDirection}
                  onSort={handleSort}
                />
                <SortableHeader
                  label={t('detections.headers.status')}
                  field="status"
                  activeField={sortField}
                  direction={sortDirection}
                  onSort={handleSort}
                />
                <th scope="col" class="hidden md:table-cell">{t('detections.headers.recording')}</th
                >
                <th scope="col">{t('detections.headers.actions')}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-base-200">
              {#each sortedDetections as detection (detection.id)}
                <tr>
                  <DetectionRow
                    {detection}
                    {onDetailsClick}
                    {onRefresh}
                    onPlayMobileAudio={handlePlayMobileAudio}
                  />
                </tr>
              {/each}
            </tbody>
          </table>
        {:else}
          <DetectionsCardView detections={sortedDetections} {onRefresh} />
        {/if}
      </div>

      <!-- Mobile: card layout (always mobile cards on small screens) -->
      <div class="md:hidden space-y-2">
        {#each sortedDetections as detection (detection.id)}
          <DetectionCardMobile
            {detection}
            {onDetailsClick}
            onPlayMobileAudio={handlePlayMobileAudio}
          />
        {/each}
      </div>
    {/if}
  </div>

  <!-- Pagination Controls -->
  {#if data && data.totalResults > data.itemsPerPage}
    <div class="border-t border-base-200">
      <div class="flex flex-col sm:flex-row justify-between items-center p-4 gap-4">
        <div class="text-sm text-base-content opacity-70 order-2 sm:order-1">
          {t('detections.pagination.showing', {
            from: data.showingFrom,
            to: data.showingTo,
            total: data.totalResults,
          })}
        </div>
        <div class="order-1 sm:order-2">
          <Pagination
            currentPage={data.currentPage}
            totalPages={data.totalPages}
            onPageChange={handlePageChange}
            showPageInfo={false}
          />
        </div>
      </div>
    </div>
  {/if}

  <!-- Mobile Audio Player Overlay -->
  {#if showMobilePlayer}
    <div class="md:hidden">
      <MobileAudioPlayer
        audioUrl={selectedAudioUrl}
        speciesName={selectedSpeciesName}
        detectionId={selectedDetectionId}
        onClose={handleCloseMobilePlayer}
      />
    </div>
  {/if}
</div>
