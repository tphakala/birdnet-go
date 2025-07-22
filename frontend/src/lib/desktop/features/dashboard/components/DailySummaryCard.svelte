<script lang="ts">
  import DatePicker from '$lib/desktop/components/ui/DatePicker.svelte';
  import type { Column } from '$lib/desktop/components/data/DataTable.types';
  import type { DailySpeciesSummary, DetectionQueryParams } from '$lib/types/detection.types';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils.js';
  import { navigationIcons } from '$lib/utils/icons'; // Centralized icons - see icons.ts

  interface Props {
    data: DailySpeciesSummary[];
    loading?: boolean;
    error?: string | null;
    selectedDate: string;
    showThumbnails?: boolean;
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
    showThumbnails = true,
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
      className: 'font-medium w-0 whitespace-nowrap',
    },
  ];

  // Add total detections column (only visible on XL screens)
  columns.push({
    key: 'total_detections',
    header: 'Detections',
    align: 'center',
    className: 'hidden 2xl:table-cell px-4 w-100',
    render: (item: DailySpeciesSummary) => item.count,
  });

  // Add all 24 hourly columns
  for (let hour = 0; hour < 24; hour++) {
    columns.push({
      key: `hour_${hour}`,
      header: hour.toString().padStart(2, '0'),
      align: 'center',
      className: 'hour-data hourly-count px-0',
      render: (item: DailySpeciesSummary) => item.hourly_counts[hour] || 0,
    });
  }

  // Add bi-hourly columns (every 2 hours)
  for (let hour = 0; hour < 24; hour += 2) {
    columns.push({
      key: `bi_hour_${hour}`,
      header: hour.toString().padStart(2, '0'),
      align: 'center',
      className: 'hour-data bi-hourly-count bi-hourly px-0',
      render: (item: DailySpeciesSummary) => {
        // Sum counts for 2-hour period
        const count1 = item.hourly_counts[hour] || 0;
        const count2 = item.hourly_counts[hour + 1] || 0;
        return count1 + count2;
      },
    });
  }

  // Add six-hourly columns (every 6 hours)
  for (let hour = 0; hour < 24; hour += 6) {
    columns.push({
      key: `six_hour_${hour}`,
      header: hour.toString().padStart(2, '0'),
      align: 'center',
      className: 'hour-data six-hourly-count six-hourly px-0',
      render: (item: DailySpeciesSummary) => {
        // Sum counts for 6-hour period
        let sum = 0;
        for (let h = hour; h < hour + 6 && h < 24; h++) {
          sum += item.hourly_counts[h] || 0;
        }
        return sum;
      },
    });
  }

  // Navigation handlers for detections
  // Navigates to species-specific detections view for the selected date
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

  // Navigates to detections for a specific species within a time period (1, 2, or 6 hours)
  function handleHourClick(species: DailySpeciesSummary, hour: number, duration: number = 1) {
    if (onDetectionView) {
      onDetectionView({
        queryType: 'species',
        species: species.common_name,
        date: selectedDate,
        hour: hour.toString(),
        duration: duration,
        numResults: 100,
        offset: 0,
      });
    }
  }

  // Navigates to all detections across all species for a specific time period
  function handleHourHeaderClick(hour: number, duration: number = 1) {
    if (onDetectionView) {
      onDetectionView({
        queryType: 'hourly',
        date: selectedDate,
        hour: hour.toString(),
        duration: duration,
        numResults: 100,
        offset: 0,
      });
    }
  }

  const isToday = $derived(selectedDate === new Date().toISOString().split('T')[0]);

  // Check for reduced motion preference for performance and accessibility
  const prefersReducedMotion = $derived(
    typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
  );

  // Sort data by count in descending order for dynamic updates
  const sortedData = $derived(data.length === 0 ? [] : [...data].sort((a, b) => b.count - a.count));

  // Calculate global maximum count across all species for proper heatmap scaling
  const globalMaxHourlyCount = $derived(
    sortedData.length === 0
      ? 1
      : Math.max(...sortedData.flatMap(species => species.hourly_counts.filter(c => c > 0))) || 1
  );

  // Calculate max count for bi-hourly intervals (every 2 hours) to normalize heatmap intensity
  const globalMaxBiHourlyCount = $derived(() => {
    if (sortedData.length === 0) return 1;

    let maxCount = 0;
    sortedData.forEach(species => {
      for (let hour = 0; hour < 24; hour += 2) {
        const sum = (species.hourly_counts[hour] || 0) + (species.hourly_counts[hour + 1] || 0);
        maxCount = Math.max(maxCount, sum);
      }
    });
    return maxCount || 1;
  });

  // Calculate max count for six-hourly intervals (every 6 hours) to normalize heatmap intensity
  const globalMaxSixHourlyCount = $derived(() => {
    if (sortedData.length === 0) return 1;

    let maxCount = 0;
    sortedData.forEach(species => {
      for (let hour = 0; hour < 24; hour += 6) {
        let sum = 0;
        for (let h = hour; h < hour + 6 && h < 24; h++) {
          sum += species.hourly_counts[h] || 0;
        }
        maxCount = Math.max(maxCount, sum);
      }
    });
    return maxCount || 1;
  });
</script>

<section class="card col-span-12 bg-base-100 shadow-sm">
  <!-- Card Header with Date Navigation -->
  <div class="card-body grow-0 p-2 sm:p-4 sm:pt-3">
    <div class="flex items-center justify-between mb-4">
      <span class="card-title grow text-base sm:text-xl"
        >Daily Summary
        {#if sortedData.length > 0}
          <!-- Number of species detected -->
          <span class="species-ball bg-primary text-primary-content ml-2">{sortedData.length}</span>
        {/if}
      </span>
      <div class="flex items-center gap-2">
        <!-- Previous day button -->
        <button onclick={onPreviousDay} class="btn btn-sm btn-ghost" aria-label="Previous day">
          {@html navigationIcons.arrowLeft}
        </button>

        <!-- Date picker -->
        <DatePicker value={selectedDate} onChange={onDateChange} className="mx-2" />

        <!-- Next day button -->
        <button
          onclick={onNextDay}
          class="btn btn-sm btn-ghost"
          disabled={isToday}
          aria-label="Next day"
        >
          {@html navigationIcons.arrowRight}
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
        <table class="table table-zebra h-full w-full table-auto">
          <thead class="sticky-header text-xs">
            <tr>
              {#each columns as column}
                <!-- Hourly, bi-hourly, and six-hourly headers -->
                <th
                  class="py-0 {column.key === 'common_name'
                    ? 'pl-2 pr-6 sm:pl-4 sm:pr-8'
                    : 'px-2 sm:px-4'} {column.className || ''}"
                  class:hour-header={column.key?.startsWith('hour_') ||
                    column.key?.startsWith('bi_hour_') ||
                    column.key?.startsWith('six_hour_')}
                  style:text-align={column.align || 'left'}
                  scope="col"
                >
                  {#if column.key?.startsWith('hour_')}
                    <!-- Hourly columns -->
                    {@const hour = parseInt(column.key.split('_')[1])}
                    <button
                      class="hover:text-primary cursor-pointer"
                      onclick={() => handleHourHeaderClick(hour, 1)}
                      title="View all detections for {hour.toString().padStart(2, '0')}:00"
                    >
                      {column.header}
                    </button>
                  {:else if column.key?.startsWith('bi_hour_')}
                    <!-- Bi-hourly columns -->
                    {@const hour = parseInt(column.key.split('_')[2])}
                    <button
                      class="hover:text-primary cursor-pointer"
                      onclick={() => handleHourHeaderClick(hour, 2)}
                      title="View all detections for {hour.toString().padStart(2, '0')}:00-{(
                        hour + 2
                      )
                        .toString()
                        .padStart(2, '0')}:00"
                    >
                      {column.header}
                    </button>
                  {:else if column.key?.startsWith('six_hour_')}
                    <!-- Six-hourly columns -->
                    {@const hour = parseInt(column.key.split('_')[2])}
                    <button
                      class="hover:text-primary cursor-pointer"
                      onclick={() => handleHourHeaderClick(hour, 6)}
                      title="View all detections for {hour.toString().padStart(2, '0')}:00-{(
                        hour + 6
                      )
                        .toString()
                        .padStart(2, '0')}:00"
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
            {#each sortedData as item}
              <tr
                class="hover"
                class:cursor-pointer={onRowClick || onDetectionView}
                class:new-species={item.isNew && !prefersReducedMotion}
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
                    class="py-0 px-0 {column.className || ''} {(() => {
                      // Apply heatmap color class and text-center to td for hour columns
                      let classes = [];
                      if (column.key?.startsWith('hour_')) {
                        // Hourly columns
                        const hour = parseInt(column.key.split('_')[1]);
                        const count = item.hourly_counts[hour];
                        classes.push('text-center', 'h-full');
                        if (count > 0) {
                          // Calculate intensity based on count and global max count
                          const intensity = Math.min(
                            9,
                            Math.floor((count / globalMaxHourlyCount) * 9)
                          );
                          classes.push(`heatmap-color-${intensity}`);
                        } else {
                          // If no detections, set intensity to 0
                          classes.push('heatmap-color-0');
                        }
                      } else if (column.key?.startsWith('bi_hour_')) {
                        // Bi-hourly columns
                        const count = column.render ? Number(column.render(item, 0)) : 0;
                        classes.push('text-center', 'h-full');
                        if (count > 0) {
                          const intensity = Math.min(
                            9,
                            Math.floor((count / globalMaxBiHourlyCount()) * 9)
                          );
                          classes.push(`heatmap-color-${intensity}`);
                        } else {
                          classes.push('heatmap-color-0');
                        }
                      } else if (column.key?.startsWith('six_hour_')) {
                        // Six-hourly columns
                        const count = column.render ? Number(column.render(item, 0)) : 0;
                        classes.push('text-center', 'h-full');
                        if (count > 0) {
                          const intensity = Math.min(
                            9,
                            Math.floor((count / globalMaxSixHourlyCount()) * 9)
                          );
                          classes.push(`heatmap-color-${intensity}`);
                        } else {
                          classes.push('heatmap-color-0');
                        }
                      } else if (column.key === 'common_name') {
                        classes.push('pl-2', 'pr-6', 'sm:pl-4', 'sm:pr-8');
                      } else {
                        classes.push('px-2', 'sm:px-4');
                      }
                      return classes.join(' ');
                    })()}"
                    style:text-align={column.align || 'left'}
                  >
                    {#if column.key === 'common_name'}
                      <!-- Species thumbnail and name -->
                      <div class="flex items-center gap-2">
                        {#if showThumbnails}
                          <img
                            src={item.thumbnail_url ||
                              `/api/v2/media/species-image?name=${encodeURIComponent(item.scientific_name)}`}
                            alt={item.common_name}
                            class="w-8 h-8 rounded object-cover"
                            onerror={handleBirdImageError}
                          />
                        {/if}
                        <!-- Species name -->
                        <span class="text-sm">{item.common_name}</span>
                      </div>
                    {:else if column.key === 'total_detections'}
                      <!-- Total detections bar -->
                      {@const maxCount = Math.max(...sortedData.map(d => d.count))}
                      {@const width = (item.count / maxCount) * 100}
                      {@const roundedWidth = Math.round(width / 5) * 5}
                      <div
                        class="w-full bg-base-300 dark:bg-base-300 rounded-full overflow-hidden relative"
                      >
                        <div
                          class="progress progress-primary bg-gray-400 dark:bg-gray-400 progress-width-{roundedWidth}"
                        >
                          {#if width >= 45 && width <= 59}
                            <!-- Total detections count for large bars -->
                            <span
                              class="text-2xs text-gray-100 dark:text-base-300 absolute right-1 top-1/2 transform -translate-y-1/2"
                              >{item.count}</span
                            >
                          {/if}
                        </div>
                        {#if width < 45 || width > 59}
                          <!-- Total detections count for small bars -->
                          <span
                            class="text-2xs {width > 59
                              ? 'text-gray-100 dark:text-base-300'
                              : 'text-gray-400 dark:text-base-400'} absolute w-full text-center top-1/2 left-1/2 transform -translate-x-1/2 -translate-y-1/2"
                            >{item.count}</span
                          >
                        {/if}
                      </div>
                    {:else if column.key?.startsWith('hour_')}
                      <!-- Hourly detections count -->
                      {@const hour = parseInt(column.key.split('_')[1])}
                      {@const count = item.hourly_counts[hour]}
                      {#if count > 0}
                        <button
                          type="button"
                          class="w-full h-full"
                          class:hour-updated={item.hourlyUpdated?.includes(hour) &&
                            !prefersReducedMotion}
                          title="{count} detections at {hour
                            .toString()
                            .padStart(2, '0')}:00 - Click to view"
                          onclick={e => {
                            e.stopPropagation();
                            handleHourClick(item, hour, 1);
                          }}
                        >
                          {count}
                        </button>
                      {:else}
                        -
                      {/if}
                    {:else if column.key?.startsWith('bi_hour_')}
                      <!-- Bi-hourly detections count -->
                      {@const hour = parseInt(column.key.split('_')[2])}
                      {@const count = column.render ? Number(column.render(item, 0)) : 0}
                      {#if count > 0}
                        <!-- Bi-hourly detections count button -->
                        <button
                          type="button"
                          class="w-full h-full"
                          title="{count} detections from {hour.toString().padStart(2, '0')}:00-{(
                            hour + 2
                          )
                            .toString()
                            .padStart(2, '0')}:00 - Click to view"
                          onclick={e => {
                            e.stopPropagation();
                            handleHourClick(item, hour, 2);
                          }}
                        >
                          {count}
                        </button>
                      {:else}
                        -
                      {/if}
                    {:else if column.key?.startsWith('six_hour_')}
                      <!-- Six-hourly detections count -->
                      {@const hour = parseInt(column.key.split('_')[2])}
                      {@const count = column.render ? Number(column.render(item, 0)) : 0}
                      {#if count > 0}
                        <!-- Six-hourly detections count button -->
                        <button
                          type="button"
                          class="w-full h-full"
                          title="{count} detections from {hour.toString().padStart(2, '0')}:00-{(
                            hour + 6
                          )
                            .toString()
                            .padStart(2, '0')}:00 - Click to view"
                          onclick={e => {
                            e.stopPropagation();
                            handleHourClick(item, hour, 6);
                          }}
                        >
                          {count}
                        </button>
                      {:else}
                        -
                      {/if}
                    {:else if column.render}
                      {column.render(item, 0)}
                    {:else}
                      <!-- Default column rendering -->
                      <span class="text-sm">{(item as any)[column.key]}</span>
                    {/if}
                  </td>
                {/each}
              </tr>
            {/each}
          </tbody>
        </table>
        {#if sortedData.length === 0}
          <div class="text-center py-8 text-base-content/60">No species detected on this date</div>
        {/if}
      </div>
    {/if}
  </div>
</section>

<style>
  /* Dynamic Update Animations - not in custom.css */

  /* Count increment animation */
  @keyframes countPop {
    0% {
      transform: scale(1);
    }
    50% {
      transform: scale(1.3);
      background-color: hsl(var(--su) / 0.3);
      box-shadow: 0 0 10px hsl(var(--su) / 0.5);
    }
    100% {
      transform: scale(1);
      background-color: transparent;
    }
  }

  .count-increased {
    animation: countPop 0.6s cubic-bezier(0.4, 0, 0.2, 1);
  }

  /* New species row animation */
  @keyframes newSpeciesSlide {
    0% {
      transform: translateY(-30px);
      opacity: 0;
      background-color: hsl(var(--p) / 0.15);
    }
    100% {
      transform: translateY(0);
      opacity: 1;
      background-color: transparent;
    }
  }

  .new-species {
    animation: newSpeciesSlide 0.8s cubic-bezier(0.25, 0.46, 0.45, 0.94);
  }

  /* Heatmap cell update flash */
  @keyframes heatmapFlash {
    0%,
    100% {
      box-shadow: none;
      transform: scale(1);
    }
    50% {
      box-shadow: 0 0 12px hsl(var(--p));
      transform: scale(1.1);
    }
  }

  .hour-updated {
    animation: heatmapFlash 0.8s ease-out;
  }

  /* Respect user's reduced motion preference */
  @media (prefers-reduced-motion: reduce) {
    .count-increased,
    .new-species,
    .hour-updated {
      animation: none;
      transition: none;
    }
  }

  /* All responsive display and heatmap styles are handled by custom.css */

  /* Button styling to match the original .hour-data a styles */
  .hour-data button {
    height: 2rem;
    min-height: 2rem;
    max-height: 2rem;
    box-sizing: border-box;
    display: flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    border: none;
    cursor: pointer;
    color: inherit;
    font-size: inherit;
    font-family: inherit;
  }
</style>
