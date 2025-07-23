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
  import { cn } from '$lib/utils/cn';
  import type { DetectionsListData } from '$lib/types/detection.types';
  import Pagination from '$lib/desktop/components/ui/Pagination.svelte';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import EmptyState from '$lib/desktop/components/ui/EmptyState.svelte';
  import DetectionRow from './DetectionRow.svelte';
  import { alertIconsSvg } from '$lib/utils/icons';
  import { t } from '$lib/i18n/index.js';

  interface Props {
    data: DetectionsListData | null;
    loading?: boolean;
    error?: string | null;
    onPageChange?: (page: number) => void;
    onDetailsClick?: (id: number) => void;
    onRefresh?: () => void;
    className?: string;
  }

  let {
    data,
    loading = false,
    error = null,
    onPageChange,
    onDetailsClick,
    onRefresh,
    className = '',
  }: Props = $props();

  // Generate title based on query type
  const title = $derived(() => {
    if (!data) return t('detections.title');

    switch (data.queryType) {
      case 'hourly':
        if (data.duration && data.duration > 1) {
          return t('detections.titles.hourlyRange', { 
            startHour: data.hour, 
            endHour: (data.hour || 0) + data.duration, 
            date: data.date 
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
</script>

<div class={cn(className)}>
  <div class="card-body grow-0 p-2 sm:p-4 sm:pt-3">
    <div class="flex justify-between">
      <!-- Title -->
      <span class="card-title grow text-base sm:text-xl">
        {title()}
      </span>
    </div>
  </div>

  <!-- Content -->
  <div class="block w-full overflow-x-auto">
    {#if loading}
      <div class="flex justify-center items-center py-8">
        <LoadingSpinner size="lg" />
      </div>
    {:else if error}
      <div class="px-4 py-8">
        <div class="alert alert-error">
          {@html alertIconsSvg.error}
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
      <!-- Header -->
      <div class="grid grid-cols-12 gap-4 text-xs px-4 pb-2 border-b border-gray-200">
        <div class="col-span-2">{t('detections.headers.dateTime')}</div>
        <div class="col-span-1">{t('detections.headers.weather')}</div>
        <div class="col-span-3">{t('detections.headers.species')}</div>
        {#if data.dashboardSettings?.thumbnails?.summary}
          <div class="col-span-1">{t('detections.headers.thumbnail')}</div>
        {/if}
        <div class="col-span-2">{t('detections.headers.status')}</div>
        <div class={data.dashboardSettings?.thumbnails?.summary ? 'col-span-2' : 'col-span-3'}>
          {t('detections.headers.recording')}
        </div>
        <div class="col-span-1 text-right">{t('detections.headers.actions')}</div>
      </div>

      <!-- Detection rows -->
      <div class="divide-y divide-gray-100">
        {#each data.notes as detection}
          <DetectionRow
            {detection}
            showThumbnails={data.dashboardSettings?.thumbnails?.summary}
            {onDetailsClick}
            {onRefresh}
          />
        {/each}
      </div>
    {/if}
  </div>

  <!-- Pagination Controls -->
  {#if data && data.totalResults > data.itemsPerPage}
    <div class="border-t border-base-200">
      <div class="flex flex-col sm:flex-row justify-between items-center p-4 gap-4">
        <div class="text-sm text-base-content/70 order-2 sm:order-1">
          {t('detections.pagination.showing', { 
            from: data.showingFrom, 
            to: data.showingTo, 
            total: data.totalResults 
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
</div>
