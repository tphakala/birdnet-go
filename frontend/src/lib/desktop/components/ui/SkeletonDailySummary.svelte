<!--
SkeletonDailySummary.svelte - Loading skeleton for the daily species summary table

Purpose:
- Provides visual placeholder during daily summary data loading
- Maintains consistent layout structure to prevent layout shift
- Supports responsive breakpoints for hourly/bi-hourly/six-hourly views
- Generates realistic skeleton patterns for improved perceived performance

Usage:
<SkeletonDailySummary 
  showSpinner={true}
  showThumbnails={true} 
  speciesCount={8}
/>

Props:
- showSpinner: Display loading spinner overlay (default: false)
- showThumbnails: Include thumbnail placeholders (default: true)  
- speciesCount: Number of species rows to render (default: 8)

Features:
- Accessibility-compliant with ARIA attributes for screen readers
- Responsive design matching actual table breakpoints
- Realistic skeleton patterns with varied widths and data distributions
- Smooth animations with proper cleanup
-->

<script lang="ts">
  import { safeArrayAccess } from '$lib/utils/security';

  interface Props {
    showSpinner?: boolean;
    showThumbnails?: boolean;
    speciesCount?: number;
  }

  let { showSpinner = false, showThumbnails = true, speciesCount = 8 }: Props = $props();

  // Generate realistic skeleton data using $state.raw for performance
  const skeletonSpecies = $state.raw(
    Array(speciesCount)
      .fill(null)
      .map((_, i) => ({
        id: i,
        nameWidth: `${60 + Math.random() * 40}%`, // Vary widths realistically
        detectionCount: Math.floor(Math.random() * 20) + 1,
        hourlyPattern: Array(24)
          .fill(null)
          .map(() => (Math.random() > 0.7 ? 1 : 0)),
      }))
  );
</script>

<section
  class="card col-span-12 bg-base-100 shadow-sm"
  role="status"
  aria-live="polite"
  aria-label="Loading daily species summary data"
>
  <div class="card-body grow-0 p-2 sm:p-4 sm:pt-3">
    <!-- Header skeleton -->
    <div class="flex items-center justify-between mb-4">
      <div class="h-6 bg-base-300 rounded w-48 animate-pulse"></div>
      <div class="flex items-center gap-2">
        <div class="h-8 w-8 bg-base-300 rounded animate-pulse"></div>
        <div class="h-8 w-32 bg-base-300 rounded animate-pulse"></div>
        <div class="h-8 w-8 bg-base-300 rounded animate-pulse"></div>
      </div>
    </div>

    {#if showSpinner}
      <div class="absolute top-4 right-4 z-10">
        <span class="loading loading-spinner loading-sm opacity-60"></span>
      </div>
    {/if}

    <!-- Table skeleton -->
    <div class="overflow-x-auto">
      <table
        class="table table-zebra h-full w-full daily-summary-table"
        aria-label="Daily bird species detection summary loading skeleton"
      >
        <thead class="sticky-header text-xs">
          <tr>
            <th class="py-0 pl-2 pr-8 sm:pl-0 sm:pr-12" role="columnheader">
              <div class="h-4 bg-base-300 rounded w-16 animate-pulse"></div>
            </th>
            <!-- Total detections header skeleton -->
            <th class="py-0 px-4 w-100 h-11 hidden 2xl:table-cell" role="columnheader">
              <div class="h-4 bg-base-300 rounded w-20 animate-pulse"></div>
            </th>
            <!-- Hourly headers skeleton -->
            {#each Array(24) as _}
              <th class="px-0 hour-header hourly-count text-center" role="columnheader">
                <div class="h-4 bg-base-300 rounded w-6 mx-auto animate-pulse"></div>
              </th>
            {/each}
            <!-- Bi-hourly headers skeleton -->
            {#each Array(12) as _}
              <th
                class="px-0 hour-header bi-hourly-count bi-hourly text-center"
                role="columnheader"
              >
                <div class="h-4 bg-base-300 rounded w-6 mx-auto animate-pulse"></div>
              </th>
            {/each}
            <!-- Six-hourly headers skeleton -->
            {#each Array(4) as _}
              <th
                class="px-0 hour-header six-hourly-count six-hourly text-center"
                role="columnheader"
              >
                <div class="h-4 bg-base-300 rounded w-6 mx-auto animate-pulse"></div>
              </th>
            {/each}
          </tr>
        </thead>
        <tbody>
          {#each skeletonSpecies as species}
            <tr class="hover">
              <!-- Species name skeleton -->
              <td class="py-0 px-2 sm:px-4" role="cell">
                <div class="flex items-center gap-2">
                  {#if showThumbnails}
                    <div class="w-8 h-8 bg-base-300 rounded animate-pulse"></div>
                  {/if}
                  <div
                    class="h-4 bg-base-300 rounded animate-pulse"
                    style:width={species.nameWidth}
                  ></div>
                </div>
              </td>

              <!-- Total detections skeleton -->
              <td class="py-1 px-3 hidden 2xl:table-cell" role="cell">
                <div class="w-full bg-base-300 rounded-full overflow-hidden relative">
                  <div
                    class="h-6 bg-base-200 rounded-full animate-pulse"
                    style:width="{species.detectionCount * 4}%"
                  ></div>
                </div>
              </td>

              <!-- Hourly counts skeleton -->
              {#each species.hourlyPattern as hasData}
                <td
                  class="hour-data hourly-count text-center py-0 px-0
                         {hasData ? 'heatmap-color-3' : 'heatmap-color-0'}"
                  role="cell"
                >
                  {#if hasData}
                    <div class="h-4 w-4 bg-base-300 rounded mx-auto animate-pulse opacity-60"></div>
                  {:else}
                    <span class="opacity-30">-</span>
                  {/if}
                </td>
              {/each}

              <!-- Bi-hourly counts skeleton -->
              {#each Array(12) as _, i}
                {@const hour = i * 2}
                {@const hasData =
                  safeArrayAccess(species.hourlyPattern, hour) ||
                  safeArrayAccess(species.hourlyPattern, hour + 1)}
                <td
                  class="hour-data bi-hourly-count bi-hourly text-center py-0 px-0
                         {hasData ? 'heatmap-color-2' : 'heatmap-color-0'}"
                  role="cell"
                >
                  {#if hasData}
                    <div class="h-4 w-4 bg-base-300 rounded mx-auto animate-pulse opacity-60"></div>
                  {:else}
                    <span class="opacity-30">-</span>
                  {/if}
                </td>
              {/each}

              <!-- Six-hourly counts skeleton -->
              {#each Array(4) as _, i}
                {@const hour = i * 6}
                {@const hasData = species.hourlyPattern.slice(hour, hour + 6).some(Boolean)}
                <td
                  class="hour-data six-hourly-count six-hourly text-center py-0 px-0
                         {hasData ? 'heatmap-color-1' : 'heatmap-color-0'}"
                  role="cell"
                >
                  {#if hasData}
                    <div class="h-4 w-4 bg-base-300 rounded mx-auto animate-pulse opacity-60"></div>
                  {:else}
                    <span class="opacity-30">-</span>
                  {/if}
                </td>
              {/each}
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </div>
</section>

<style>
  /* Ensure skeleton follows the same responsive patterns as the real table */
  @media (min-width: 1400px) {
    :global(.hourly-count) {
      display: table-cell;
    }
  }

  @media (min-width: 1200px) and (max-width: 1399px) {
    :global(.hourly-count) {
      display: table-cell;
    }
  }

  @media (min-width: 1024px) and (max-width: 1199px) {
    :global(.hourly-count) {
      display: table-cell;
    }
  }

  @media (min-width: 768px) and (max-width: 1023px) {
    :global(.bi-hourly-count) {
      display: table-cell;
    }

    :global(.hourly-count) {
      display: none;
    }
  }

  @media (max-width: 767px) {
    :global(.bi-hourly-count) {
      display: table-cell;
    }

    :global(.hourly-count) {
      display: none;
    }
  }

  @media (max-width: 479px) {
    :global(.six-hourly-count) {
      display: table-cell;
    }

    :global(.bi-hourly-count) {
      display: none;
    }
  }
</style>
