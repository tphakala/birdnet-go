<script lang="ts">
  import DatePicker from '$lib/desktop/components/ui/DatePicker.svelte';
  import type { Column } from '$lib/desktop/components/data/DataTable.types';
  import type { DailySpeciesSummary, DetectionQueryParams } from '$lib/types/detection.types';

  interface Props {
    data: DailySpeciesSummary[];
    loading?: boolean;
    error?: string | null;
    selectedDate: string;
    onRowClick?: (_species: DailySpeciesSummary) => void;
    onPreviousDay: () => void;
    onNextDay: () => void;
    onGoToToday: () => void;
    onDateChange: (_date: string) => void;
    onDetectionView?: (_params: DetectionQueryParams) => void;
  }

  let {
    data = [],
    loading = false,
    error = null,
    selectedDate,
    onRowClick,
    onPreviousDay,
    onNextDay,
    onGoToToday,
    onDateChange,
    onDetectionView,
  }: Props = $props();

  // Column definitions
  const columns: Column<DailySpeciesSummary>[] = [
    {
      key: 'common_name',
      header: 'Species',
      sortable: true,
      className: 'font-medium',
    },
    {
      key: 'count',
      header: 'Count',
      sortable: true,
      align: 'center',
      className: 'text-center',
    },
  ];

  // Add hourly columns
  for (let hour = 0; hour < 24; hour++) {
    columns.push({
      key: `hour_${hour}`,
      header: hour.toString().padStart(2, '0'),
      align: 'center',
      className: 'px-1 text-xs',
      render: (item: DailySpeciesSummary) => item.hourly_counts[hour] || '',
    });
  }

  // Helper function to handle image error
  function handleImageError(e: Event) {
    const target = e.currentTarget as globalThis.HTMLImageElement;
    target.src = '/assets/images/bird-placeholder.svg';
  }

  // Navigation handlers for detections
  function handleSpeciesClick(species: DailySpeciesSummary) {
    if (onDetectionView) {
      onDetectionView({
        queryType: 'species',
        species: species.common_name,
        date: selectedDate,
        numResults: 100,
        offset: 0,
      });
    }
  }

  function handleHourClick(species: DailySpeciesSummary, hour: number) {
    if (onDetectionView) {
      onDetectionView({
        queryType: 'species',
        species: species.common_name,
        date: selectedDate,
        hour: hour.toString(),
        duration: 1,
        numResults: 100,
        offset: 0,
      });
    }
  }

  function handleCountClick(species: DailySpeciesSummary) {
    if (onDetectionView) {
      onDetectionView({
        queryType: 'species',
        species: species.common_name,
        date: selectedDate,
        numResults: 100,
        offset: 0,
      });
    }
  }

  function handleHourHeaderClick(hour: number) {
    if (onDetectionView) {
      onDetectionView({
        queryType: 'hourly',
        date: selectedDate,
        hour: hour.toString(),
        duration: 1,
        numResults: 100,
        offset: 0,
      });
    }
  }

  const isToday = $derived(selectedDate === new Date().toISOString().split('T')[0]);
</script>

<section class="card col-span-12 bg-base-100 shadow-sm">
  <!-- Card Header with Date Navigation -->
  <div class="card-body grow-0 p-2 sm:p-4 sm:pt-3">
    <div class="flex items-center justify-between mb-4">
      <span class="card-title grow text-base sm:text-xl"
        >Daily Summary
        {#if data.length > 0}
          <span class="species-ball bg-blue-500 text-white ml-2">{data.length}</span>
        {/if}
      </span>
      <div class="flex items-center gap-2">
        <button onclick={onPreviousDay} class="btn btn-sm btn-ghost" aria-label="Previous day">
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-4 w-4"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M15 19l-7-7 7-7"
            />
          </svg>
        </button>

        <DatePicker value={selectedDate} onChange={onDateChange} className="mx-2" />

        <button
          onclick={onNextDay}
          class="btn btn-sm btn-ghost"
          disabled={isToday}
          aria-label="Next day"
        >
          <svg
            xmlns="http://www.w3.org/2000/svg"
            class="h-4 w-4"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              stroke-width="2"
              d="M9 5l7 7-7 7"
            />
          </svg>
        </button>

        {#if !isToday}
          <button onclick={onGoToToday} class="btn btn-sm btn-primary"> Today </button>
        {/if}
      </div>
    </div>

    <!-- Table Content -->
    {#if loading}
      <div class="flex justify-center py-8">
        <span class="loading loading-spinner loading-md"></span>
      </div>
    {:else if error}
      <div class="alert alert-error">
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="stroke-current shrink-0 h-6 w-6"
          fill="none"
          viewBox="0 0 24 24"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z"
          />
        </svg>
        <span>{error}</span>
      </div>
    {:else}
      <div class="overflow-x-auto">
        <table class="table table-zebra h-full">
          <thead class="sticky-header text-xs">
            <tr>
              {#each columns as column}
                <th
                  class="py-0 px-2 sm:px-4 {column.className || ''} {column.key?.startsWith('hour_')
                    ? 'hour-header hourly-count'
                    : ''}"
                  style:text-align={column.align || 'left'}
                  scope="col"
                >
                  {#if column.key?.startsWith('hour_')}
                    {@const hour = parseInt(column.key.split('_')[1])}
                    <button
                      class="hover:text-blue-600 cursor-pointer"
                      onclick={() => handleHourHeaderClick(hour)}
                      title="View all detections for {hour.toString().padStart(2, '0')}:00"
                    >
                      {column.header}
                    </button>
                  {:else}
                    {column.header}
                  {/if}
                </th>
              {/each}
            </tr>
          </thead>
          <tbody>
            {#each data as item}
              <tr
                class="hover"
                class:cursor-pointer={onRowClick || onDetectionView}
                onclick={() => {
                  if (onRowClick) {
                    onRowClick(item);
                  } else if (onDetectionView) {
                    handleSpeciesClick(item);
                  }
                }}
              >
                {#each columns as column}
                  <td
                    class="py-0 px-2 sm:px-4 {column.className || ''} {column.key?.startsWith(
                      'hour_'
                    )
                      ? 'hour-data hourly-count'
                      : ''}"
                    style:text-align={column.align || 'left'}
                  >
                    {#if column.key === 'common_name'}
                      <div class="flex items-center gap-2">
                        <img
                          src={item.thumbnail_url}
                          alt={item.common_name}
                          class="w-8 h-8 rounded object-cover"
                          onerror={handleImageError}
                        />
                        <span class="text-sm">{item.common_name}</span>
                      </div>
                    {:else if column.key === 'count'}
                      <button
                        class="w-full bg-base-300 dark:bg-base-300 rounded-full overflow-hidden relative h-6 hover:bg-base-200 transition-colors cursor-pointer"
                        onclick={e => {
                          e.stopPropagation();
                          handleCountClick(item);
                        }}
                        title="View all detections for {item.common_name}"
                      >
                        <div
                          class="progress progress-primary bg-gray-400 dark:bg-gray-400 h-full"
                          style:width="{Math.min(
                            100,
                            (item.count / Math.max(...data.map(d => d.count))) * 100
                          )}%"
                        >
                          <span
                            class="text-xs text-gray-100 dark:text-base-300 absolute right-1 top-1/2 transform -translate-y-1/2"
                          >
                            {item.count}
                          </span>
                        </div>
                      </button>
                    {:else if column.key?.startsWith('hour_')}
                      {@const hour = parseInt(column.key.split('_')[1])}
                      {@const count = item.hourly_counts[hour]}
                      {#if count > 0}
                        {@const maxCount = Math.max(...item.hourly_counts.filter(c => c > 0))}
                        {@const intensity = Math.min(9, Math.floor((count / maxCount) * 9))}
                        <button
                          class="heatmap-cell cursor-pointer"
                          style:background-color="var(--heatmap-color-{intensity})"
                          title="{count} detections at {hour
                            .toString()
                            .padStart(2, '0')}:00 - Click to view"
                          onclick={e => {
                            e.stopPropagation();
                            handleHourClick(item, hour);
                          }}
                        >
                          <span class="text-xs">{count}</span>
                        </button>
                      {:else}
                        <div class="heatmap-cell" style:background-color="var(--heatmap-color-0)">
                          <span class="text-xs text-base-content/30">·</span>
                        </div>
                      {/if}
                    {:else if column.render}
                      {column.render(item, 0)}
                    {:else}
                      <span class="text-sm">{(item as any)[column.key]}</span>
                    {/if}
                  </td>
                {/each}
              </tr>
            {/each}
          </tbody>
        </table>
        {#if data.length === 0}
          <div class="text-center py-8 text-base-content/60">No species detected on this date</div>
        {/if}
      </div>
    {/if}
  </div>
</section>

<style>
  :root {
    --heatmap-color-0: #f0f9fc;
    --heatmap-color-1: #e0f3f8;
    --heatmap-color-2: #d1edf4;
    --heatmap-color-3: #c1e7f0;
    --heatmap-color-4: #b2e1ec;
    --heatmap-color-5: #a2dbe8;
    --heatmap-color-6: #93d5e4;
    --heatmap-color-7: #83cfe0;
    --heatmap-color-8: #74c9dc;
    --heatmap-color-9: #64c3d8;
  }

  :global([data-theme='dark']) {
    --heatmap-color-0: #001a20;
    --heatmap-color-1: #003440;
    --heatmap-color-2: #004e60;
    --heatmap-color-3: #006880;
    --heatmap-color-4: #0082a0;
    --heatmap-color-5: #009cc0;
    --heatmap-color-6: #00b6e0;
    --heatmap-color-7: #00d0ff;
    --heatmap-color-8: #1ad6ff;
    --heatmap-color-9: #33dcff;
  }

  .species-ball {
    min-width: 1rem;
    height: 1.25rem;
    display: inline-flex;
    padding: 0.2rem 0.25rem;
    align-items: center;
    justify-content: center;
    border-radius: 1rem;
    font-size: 0.75rem;
    line-height: 1;
  }

  thead.sticky-header {
    position: sticky;
    top: 0;
    z-index: 10;
    height: 2rem;
    background-color: var(--fallback-b1, oklch(var(--b1) / 1));
  }

  .heatmap-cell {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 1.5rem;
    min-width: 2rem;
    border-radius: 0.25rem;
    transition: background-color 0.2s ease;
  }

  .heatmap-cell:hover {
    transform: scale(1.1);
    z-index: 5;
    position: relative;
  }

  /* Responsive hour column display */
  .hour-header,
  .hour-data,
  .hourly-count {
    display: none;
  }

  /* Show hourly columns on larger screens */
  @media (min-width: 1400px) {
    .hour-header.hourly-count,
    .hour-data.hourly-count,
    .hourly-count {
      display: table-cell;
    }
  }

  /* Medium screens - show some hourly columns */
  @media (min-width: 1024px) and (max-width: 1399px) {
    .hour-header.hourly-count:nth-child(n + 5):nth-child(-n + 25):nth-child(2n),
    .hour-data.hourly-count:nth-child(n + 5):nth-child(-n + 25):nth-child(2n) {
      display: table-cell;
    }
  }

  /* Smaller screens - show fewer hourly columns */
  @media (min-width: 768px) and (max-width: 1023px) {
    .hour-header.hourly-count:nth-child(n + 5):nth-child(-n + 25):nth-child(4n),
    .hour-data.hourly-count:nth-child(n + 5):nth-child(-n + 25):nth-child(4n) {
      display: table-cell;
    }
  }
</style>
