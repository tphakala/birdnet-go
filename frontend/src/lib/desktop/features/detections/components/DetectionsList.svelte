<!--
  DetectionsList.svelte

  A container component that orchestrates the display of multiple bird detection records.
  Manages pagination, loading states, and provides a consistent layout for detection data.

  Usage:
  - Main detection pages
  - Search results presentation
  - Filtered detection views
  - Administrative detection management interfaces

  Features:
  - Paginated detection display
  - Loading and error state handling
  - Empty state with helpful messaging
  - Responsive card-based layout
  - Integration with DetectionRow components
  - Refresh functionality

  Props:
  - data: DetectionsListData | null - Paginated detection data
  - loading?: boolean - Loading state indicator
  - error?: string | null - Error message display
  - onPageChange?: (page: number) => void - Pagination handler
  - onDetailsClick?: (id: number) => void - Detail view handler
  - onRefresh?: () => void - Data refresh handler
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import MobileAudioPlayer from '$lib/desktop/components/media/MobileAudioPlayer.svelte';
  import EmptyState from '$lib/desktop/components/ui/EmptyState.svelte';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import Pagination from '$lib/desktop/components/ui/Pagination.svelte';
  import { t } from '$lib/i18n';
  import type { DetectionsListData } from '$lib/types/detection.types';
  import { cn } from '$lib/utils/cn';
  import { XCircle } from '@lucide/svelte';
  import { untrack } from 'svelte';
  import DetectionCardMobile from './DetectionCardMobile.svelte';
  import DetectionRow from './DetectionRow.svelte';

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

  function handleNumResultsChange(event: Event) {
    const target = event.target as HTMLSelectElement;
    const numResults = parseInt(target.value);

    // Validate the parsed value
    if (isNaN(numResults) || ![10, 25, 50, 100].includes(numResults)) {
      // Reset to current valid value if invalid
      target.value = selectedNumResults;
      return;
    }

    if (onNumResultsChange) {
      onNumResultsChange(numResults);
    }
  }

  // State for number of results - captures initial value without creating dependency
  // Uses untrack() to explicitly capture initial value only (local state is independent after init)
  let selectedNumResults = $state(untrack(() => String(data?.numResults || 25)));

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

      <!-- Number of results selector -->
      <div class="flex items-center gap-2">
        <label for="num-results" class="text-sm font-medium">Results:</label>
        <select
          id="num-results"
          class="select select-sm w-20"
          bind:value={selectedNumResults}
          onchange={handleNumResultsChange}
        >
          <option value="10">10</option>
          <option value="25">25</option>
          <option value="50">50</option>
          <option value="100">100</option>
        </select>
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
      <!-- Desktop/tablet: table layout -->
      <table class="w-full hidden md:table">
        <caption class="sr-only">{t('detections.table.caption')}</caption>
        <thead>
          <tr class="detection-header-list">
            <th scope="col">{t('detections.headers.dateTime')}</th>
            <th scope="col" class="hidden lg:table-cell">{t('detections.headers.source')}</th>
            <th scope="col" class="hidden md:table-cell">{t('detections.headers.weather')}</th>
            <th scope="col">{t('detections.headers.species')}</th>
            <th scope="col">{t('detections.headers.confidence')}</th>
            <th scope="col">{t('detections.headers.status')}</th>
            <th scope="col" class="hidden md:table-cell">{t('detections.headers.recording')}</th>
            <th scope="col">{t('detections.headers.actions')}</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-base-200">
          {#each data.notes as detection (detection.id)}
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

      <!-- Mobile: card layout -->
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
