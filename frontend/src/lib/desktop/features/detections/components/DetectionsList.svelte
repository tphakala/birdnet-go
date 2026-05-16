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
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import MobileAudioPlayer from '$lib/desktop/components/media/MobileAudioPlayer.svelte';
  import ConfirmModal from '$lib/desktop/components/modals/ConfirmModal.svelte';
  import EmptyState from '$lib/desktop/components/ui/EmptyState.svelte';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import Pagination from '$lib/desktop/components/ui/Pagination.svelte';
  import SelectionToolbar from '$lib/desktop/components/ui/SelectionToolbar.svelte';
  import SortableHeader from '$lib/desktop/components/ui/SortableHeader.svelte';
  import ViewToggle from '$lib/desktop/components/ui/ViewToggle.svelte';
  import { t } from '$lib/i18n';
  import { auth } from '$lib/stores/auth';
  import { toastActions } from '$lib/stores/toast';
  import type { DetectionSortBy, DetectionsListData } from '$lib/types/detection.types';
  import { fetchWithCSRF } from '$lib/utils/api';
  import { cn } from '$lib/utils/cn';
  import { loggers } from '$lib/utils/logger';
  import {
    CheckSquare,
    CircleCheck,
    CircleX,
    Lock,
    LockOpen,
    Trash2,
    XCircle,
  } from '@lucide/svelte';
  import { untrack } from 'svelte';
  import { useSelectionMode } from '../composables/useSelectionMode.svelte';
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
    onSortChange?: (_sortBy: DetectionSortBy) => void;
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
    onSortChange,
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
    selection.clear();
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
    selection.clear();
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

  // --- Sort state (driven by backend via URL params) ---

  /** Map backend sortBy value to frontend field + direction */
  function parseSortBy(sortBy?: string): { field: SortField; direction: SortDirection } {
    switch (sortBy) {
      case 'date_asc':
        return { field: 'dateTime', direction: 'asc' };
      case 'species_asc':
        return { field: 'species', direction: 'asc' };
      case 'confidence_desc':
        return { field: 'confidence', direction: 'desc' };
      case 'status':
        return { field: 'status', direction: 'asc' };
      default:
        return { field: 'dateTime', direction: 'desc' };
    }
  }

  /** Map frontend field + direction to backend sortBy value */
  function toBackendSortBy(field: SortField, direction: SortDirection): DetectionSortBy {
    switch (field) {
      case 'dateTime':
        return direction === 'asc' ? 'date_asc' : 'date_desc';
      case 'species':
        return 'species_asc';
      case 'confidence':
        return 'confidence_desc';
      case 'status':
        return 'status';
    }
  }

  // Initialize from URL sortBy parameter
  const initialSort = parseSortBy(
    typeof window !== 'undefined'
      ? (new URLSearchParams(window.location.search).get('sortBy') ?? undefined)
      : undefined
  );
  let sortField = $state<SortField>(initialSort.field);
  let sortDirection = $state<SortDirection>(initialSort.direction);

  const SORT_FIELDS: Set<string> = new Set<string>(['dateTime', 'species', 'confidence', 'status']);

  function isSortField(field: string): field is SortField {
    return SORT_FIELDS.has(field);
  }

  function handleSort(field: string) {
    if (!isSortField(field)) return;
    selection.clear();
    if (sortField === field) {
      sortDirection = sortDirection === 'asc' ? 'desc' : 'asc';
    } else {
      sortField = field;
      sortDirection = field === 'dateTime' ? 'desc' : 'asc';
    }
    const newBackendSort = toBackendSortBy(sortField, sortDirection);
    // For columns with a fixed backend direction, snap the visual direction to match
    const parsed = parseSortBy(newBackendSort);
    sortDirection = parsed.direction;
    onSortChange?.(newBackendSort);
  }

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

  // Selection mode
  let canEdit = $derived(!$auth.security.enabled || $auth.security.accessAllowed);
  const selection = useSelectionMode(() => data?.totalResults ?? 0);

  const pageIds = $derived((data?.notes ?? []).map(d => String(d.id)));

  const headerChecked = $derived(selection.allOnPageSelected(pageIds));

  function handleToggleSelect(id: string, shiftKey: boolean) {
    selection.toggleWithShift(id, pageIds, shiftKey);
  }

  function handleRowClick(id: string, event: MouseEvent) {
    if (selection.selectionActive) {
      selection.toggleWithShift(id, pageIds, event.shiftKey);
    }
  }

  // Bulk action modal
  let showBulkConfirmModal = $state(false);
  let bulkConfirmConfig = $state({
    title: '',
    message: '',
    confirmLabel: '',
    onConfirm: async () => {},
  });

  async function executeBulkAction(endpoint: string, body: Record<string, unknown>) {
    try {
      const resp = await fetchWithCSRF<{ processed: number; skipped: number }>(endpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      });
      if (resp.skipped > 0) {
        toastActions.info(
          t('detections.selection.bulkPartial', {
            processed: resp.processed,
            skipped: resp.skipped,
          })
        );
      } else {
        toastActions.success(t('detections.selection.bulkSuccess', { count: resp.processed }));
      }
      selection.deactivate();
      onRefresh?.();
    } catch (err) {
      loggers.ui.error('Bulk action failed:', err);
      toastActions.error(t('dashboard.recentDetections.errors.deleteFailed'));
    }
  }

  async function getIdsForBulkAction(): Promise<string[] | null> {
    if (!selection.allMatchingSelected) {
      const ids = selection.getSelectedIds();
      return ids.length > 0 ? ids : null;
    }
    if (!data) return null;
    try {
      const resp = await fetchWithCSRF<{ ids: string[]; count: number }>(
        '/api/v2/detections/batch/resolve',
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            queryType: data.queryType,
            species: data.species,
            date: data.date,
            search: data.search,
            hour: data.hour !== undefined ? String(data.hour) : undefined,
          }),
        }
      );
      return resp.ids;
    } catch (err) {
      loggers.ui.error('Failed to resolve matching detections:', err);
      toastActions.error(t('detections.selection.tooManyDetections', { count: data.totalResults }));
      return null;
    }
  }

  function openBulkConfirm(
    title: string,
    messageKey: string,
    confirmLabel: string,
    endpoint: string,
    extraBody: Record<string, unknown> = {}
  ) {
    bulkConfirmConfig = {
      title,
      message: t(messageKey, { count: selection.selectedCount }),
      confirmLabel,
      onConfirm: async () => {
        const ids = await getIdsForBulkAction();
        if (ids) await executeBulkAction(endpoint, { ids, ...extraBody });
      },
    };
    showBulkConfirmModal = true;
  }

  const handleBulkDelete = () =>
    openBulkConfirm(
      t('dashboard.recentDetections.modals.deleteDetection', { species: '' }),
      'detections.selection.confirmBulkDelete',
      t('common.buttons.delete'),
      '/api/v2/detections/batch/delete'
    );

  const handleBulkMarkCorrect = () =>
    openBulkConfirm(
      t('dashboard.recentDetections.actions.markCorrect'),
      'detections.selection.confirmBulkMarkCorrect',
      t('common.buttons.confirm'),
      '/api/v2/detections/batch/review',
      { verified: 'correct' }
    );

  const handleBulkMarkFalsePositive = () =>
    openBulkConfirm(
      t('dashboard.recentDetections.actions.markFalsePositive'),
      'detections.selection.confirmBulkMarkFalsePositive',
      t('common.buttons.confirm'),
      '/api/v2/detections/batch/review',
      { verified: 'false_positive' }
    );

  const handleBulkLock = () =>
    openBulkConfirm(
      t('dashboard.recentDetections.modals.lockDetection'),
      'detections.selection.confirmBulkLock',
      t('common.buttons.confirm'),
      '/api/v2/detections/batch/lock',
      { locked: true }
    );

  const handleBulkUnlock = () =>
    openBulkConfirm(
      t('dashboard.recentDetections.modals.unlockDetection'),
      'detections.selection.confirmBulkUnlock',
      t('common.buttons.confirm'),
      '/api/v2/detections/batch/lock',
      { locked: false }
    );

  function handleKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape' && selection.selectionActive) {
      selection.deactivate();
    }
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class={cn(className)} onkeydown={handleKeydown}>
  <div class="card-body grow-0 p-2 sm:p-4 sm:pt-3">
    <div class="flex justify-between items-center">
      <!-- Title -->
      <span class="card-title grow text-base sm:text-xl">
        {title}
      </span>

      <!-- Controls: view toggle + results selector -->
      <div class="flex items-center gap-3">
        {#if canEdit}
          <div class="hidden md:block">
            <button
              type="button"
              class={cn(
                'inline-flex items-center justify-center gap-1.5 px-3 py-1.5 rounded-lg text-sm font-medium transition-colors',
                selection.selectionActive
                  ? 'bg-[var(--color-primary)] text-[var(--color-primary-content)]'
                  : 'border border-[var(--color-base-300)] text-[var(--color-base-content)] hover:bg-[var(--color-base-200)]'
              )}
              onclick={() =>
                selection.selectionActive ? selection.deactivate() : selection.activate()}
              aria-pressed={selection.selectionActive}
            >
              <CheckSquare class="size-4" />
              <span>{t('detections.selection.select')}</span>
            </button>
          </div>
        {/if}

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
          className="w-22"
          onChange={handleNumResultsChange}
        />
      </div>
    </div>
  </div>

  {#if selection.selectionActive && selection.selectedCount > 0}
    <SelectionToolbar
      selectedCount={selection.selectedCount}
      totalCount={data?.totalResults ?? 0}
      allSelected={selection.allMatchingSelected}
      allOnPageSelected={selection.allOnPageSelected(pageIds)}
      pageSize={data?.itemsPerPage ?? 25}
      onSelectAll={() => selection.selectAllMatching()}
      onClear={() => selection.clear()}
    >
      {#snippet actions()}
        <button
          type="button"
          class="inline-flex items-center gap-1 px-2 py-1 rounded text-sm hover:bg-[var(--color-base-200)] transition-colors"
          onclick={handleBulkMarkCorrect}
          title={t('dashboard.recentDetections.actions.markCorrect')}
          aria-label={t('dashboard.recentDetections.actions.markCorrect')}
        >
          <CircleCheck class="size-4 text-[var(--color-success)]" />
        </button>
        <button
          type="button"
          class="inline-flex items-center gap-1 px-2 py-1 rounded text-sm hover:bg-[var(--color-base-200)] transition-colors"
          onclick={handleBulkMarkFalsePositive}
          title={t('dashboard.recentDetections.actions.markFalsePositive')}
          aria-label={t('dashboard.recentDetections.actions.markFalsePositive')}
        >
          <CircleX class="size-4 text-[var(--color-error)]" />
        </button>
        <button
          type="button"
          class="inline-flex items-center gap-1 px-2 py-1 rounded text-sm hover:bg-[var(--color-base-200)] transition-colors"
          onclick={handleBulkLock}
          title={t('dashboard.recentDetections.modals.lockDetection')}
          aria-label={t('dashboard.recentDetections.modals.lockDetection')}
        >
          <Lock class="size-4" />
        </button>
        <button
          type="button"
          class="inline-flex items-center gap-1 px-2 py-1 rounded text-sm hover:bg-[var(--color-base-200)] transition-colors"
          onclick={handleBulkUnlock}
          title={t('dashboard.recentDetections.modals.unlockDetection')}
          aria-label={t('dashboard.recentDetections.modals.unlockDetection')}
        >
          <LockOpen class="size-4" />
        </button>
        <div class="w-px h-5 bg-[var(--color-base-300)] mx-1" role="separator"></div>
        <button
          type="button"
          class="inline-flex items-center gap-1 px-2 py-1 rounded text-sm text-[var(--color-error)] hover:bg-[var(--color-error)]/10 transition-colors"
          onclick={handleBulkDelete}
          title={t('dashboard.recentDetections.actions.deleteDetection')}
          aria-label={t('dashboard.recentDetections.actions.deleteDetection')}
        >
          <Trash2 class="size-4" />
        </button>
      {/snippet}
    </SelectionToolbar>
  {/if}

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
      <div
        class="absolute inset-0 bg-[var(--color-base-100)]/50 z-10 flex justify-center items-center"
      >
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
                {#if selection.selectionActive}
                  <th scope="col" class="w-10 text-center">
                    <Checkbox
                      checked={headerChecked}
                      size="sm"
                      variant="primary"
                      onchange={() => selection.toggleAllOnPage(pageIds)}
                    />
                  </th>
                {/if}
                <SortableHeader
                  label={t('detections.headers.dateTime')}
                  field="dateTime"
                  activeField={sortField}
                  direction={sortDirection}
                  onSort={handleSort}
                />
                <th scope="col" class="hidden md:table-cell">{t('detections.headers.weather')}</th>
                <th scope="col" class="hidden lg:table-cell">{t('detections.headers.source')}</th>
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
            <tbody class="divide-y divide-[var(--color-base-200)]">
              {#each data.notes as detection (detection.id)}
                <tr
                  class={cn(
                    selection.selectionActive &&
                      selection.isSelected(String(detection.id)) &&
                      'bg-[var(--color-primary)]/5',
                    selection.selectionActive && 'cursor-pointer'
                  )}
                  onclick={e => handleRowClick(String(detection.id), e)}
                >
                  <DetectionRow
                    {detection}
                    {onDetailsClick}
                    {onRefresh}
                    onPlayMobileAudio={handlePlayMobileAudio}
                    selectionActive={selection.selectionActive}
                    selected={selection.isSelected(String(detection.id))}
                    onToggleSelect={handleToggleSelect}
                  />
                </tr>
              {/each}
            </tbody>
          </table>
        {:else}
          <DetectionsCardView
            detections={data.notes}
            {onRefresh}
            selectionActive={selection.selectionActive}
            selectedIds={id => selection.isSelected(id)}
            onToggleSelect={handleToggleSelect}
          />
        {/if}
      </div>

      <!-- Mobile: card layout (always mobile cards on small screens) -->
      <div class="md:hidden space-y-2">
        {#each data.notes as detection (detection.id)}
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
    <div class="border-t border-[var(--color-base-200)]">
      <div class="flex flex-col sm:flex-row justify-between items-center p-4 gap-4">
        <div class="text-sm text-[var(--color-base-content)] opacity-70 order-2 sm:order-1">
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

  <ConfirmModal
    isOpen={showBulkConfirmModal}
    title={bulkConfirmConfig.title}
    message={bulkConfirmConfig.message}
    confirmLabel={bulkConfirmConfig.confirmLabel}
    onClose={() => (showBulkConfirmModal = false)}
    onConfirm={async () => {
      await bulkConfirmConfig.onConfirm();
      showBulkConfirmModal = false;
    }}
  />
</div>
