<script lang="ts">
  import CollapsibleSection from '$lib/desktop/components/ui/CollapsibleSection.svelte';
  import TimeOfDayIcon from '$lib/desktop/components/ui/TimeOfDayIcon.svelte';
  import WeatherInfo from '$lib/desktop/components/data/WeatherInfo.svelte';
  import AudioPlayer from '$lib/desktop/components/media/AudioPlayer.svelte';
  import { actionIcons, alertIconsSvg, dataIcons, mediaIcons, systemIcons, navigationIcons } from '$lib/utils/icons';

  // Type definitions
  interface DateRange {
    start: string;
    end: string;
  }

  interface ConfidenceRange {
    min: number;
    max: number;
  }

  interface SearchResult {
    id: string;
    timestamp: string;
    timeOfDay: string;
    commonName: string;
    scientificName: string;
    confidence: number;
    verified: string;
    locked: boolean;
    hasAudio: boolean;
  }

  type VerifiedStatus = 'any' | 'verified' | 'unverified';
  type LockedStatus = 'any' | 'locked' | 'unlocked';
  type TimeOfDayFilter = 'any' | 'day' | 'night' | 'sunrise' | 'sunset';
  type SortBy = 'date_desc' | 'date_asc' | 'species_asc' | 'confidence_desc';

  // Component state
  let speciesSearchTerm = $state('');
  let dateRange = $state<DateRange>({ start: '', end: '' });
  let confidenceRange = $state<ConfidenceRange>({ min: 0, max: 100 });
  let verifiedStatus = $state<VerifiedStatus>('any');
  let lockedStatus = $state<LockedStatus>('any');
  let timeOfDayFilter = $state<TimeOfDayFilter>('any');
  let formSubmitted = $state(false);
  let advancedFilters = $state(false);
  let isLoading = $state(false);
  let currentPage = $state(1);
  let totalPages = $state(1);
  let results = $state<SearchResult[]>([]);
  let totalResults = $state(0);
  let sortBy = $state<SortBy>('date_desc');
  let errorMessage = $state('');
  let expanded = $state<Record<string, boolean>>({});
  let confidenceError = $state('');
  let showTooltip = $state<string | null>(null);

  // Form validation
  function validateForm() {
    confidenceError = '';
    if (confidenceRange.min > confidenceRange.max) {
      confidenceError = 'Minimum confidence cannot be greater than maximum confidence.';
      return false;
    }
    return true;
  }

  // Form submission
  async function submitSearch(page = 1) {
    if (!validateForm()) return;

    isLoading = true;
    errorMessage = '';
    currentPage = page;
    expanded = {}; // Reset expanded state when loading new results

    try {
      // Build request body
      const requestBody = {
        species: speciesSearchTerm,
        dateStart: dateRange.start,
        dateEnd: dateRange.end,
        confidenceMin: confidenceRange.min / 100,
        confidenceMax: confidenceRange.max / 100,
        verifiedStatus: verifiedStatus,
        lockedStatus: lockedStatus,
        timeOfDay: timeOfDayFilter,
        page: currentPage,
        sortBy: sortBy,
      };

      // Debug: Search parameters submitted

      const response = await fetch('/api/v2/search', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-CSRF-Token': (document.querySelector('meta[name="csrf-token"]') as any)?.content || '',
        },
        body: JSON.stringify(requestBody),
      });

      if (!response.ok) {
        throw new Error(`Server responded with ${response.status}`);
      }

      const data = await response.json();
      results = data.results || [];
      totalResults = data.total || 0;
      totalPages = data.pages || 1;
      formSubmitted = true;
    } catch (error: unknown) {
      // Handle search error silently
      errorMessage = `Search failed: ${error instanceof Error ? error.message : 'Unknown error'}`;
      results = [];
    } finally {
      isLoading = false;
    }
  }

  // Reset form
  function resetForm() {
    speciesSearchTerm = '';
    dateRange.start = '';
    dateRange.end = '';
    confidenceRange.min = 0;
    confidenceRange.max = 100;
    verifiedStatus = 'any';
    lockedStatus = 'any';
    timeOfDayFilter = 'any';
    formSubmitted = false;
    results = [];
    errorMessage = '';
    expanded = {};
  }

  // Format date for display
  function formatDate(dateString: string) {
    if (!dateString) return '';
    const date = new Date(dateString);
    return date.toLocaleString();
  }

  // Handle pagination
  function goToPage(page: number) {
    if (page < 1 || page > totalPages) return;
    submitSearch(page);
  }

  // Handle sorting
  function changeSort(sortOption: SortBy) {
    sortBy = sortOption;
    submitSearch(1);
  }

  // Toggle expand state of a row
  function toggleExpand(recordId: string) {
    // Toggle expand for record

    expanded = { ...expanded, [recordId]: !expanded[recordId] };
    // Update expanded state
  }

  function isExpanded(recordId: string) {
    return Boolean(expanded[recordId]);
  }
</script>

<div class="col-span-12 space-y-4" role="region" aria-label="Bird Detection Search">
  <!-- Search Form -->
  <div class="card bg-base-100 shadow-sm">
    <div class="card-body card-padding">
      <h2 class="card-title" id="search-filters-heading">Search Filters</h2>

      <form
        id="searchForm"
        class="space-y-4"
        onsubmit={e => {
          e.preventDefault();
          submitSearch(1);
        }}
        aria-labelledby="search-filters-heading"
      >
        <!-- Basic Search Fields -->
        <div class="gap-4 search-form-grid">
          <!-- Species -->
          <div class="form-control">
            <label class="label" for="species">
              <span class="label-text">Species</span>
              <span
                class="help-icon"
                onmouseenter={() => (showTooltip = 'species')}
                onmouseleave={() => (showTooltip = null)}
                onfocus={() => (showTooltip = 'species')}
                onblur={() => (showTooltip = null)}
                role="button"
                tabindex="0"
                aria-label="Help for Species"
                aria-describedby="speciesTooltip">ⓘ</span
              >
            </label>
            <input
              type="text"
              id="species"
              bind:value={speciesSearchTerm}
              placeholder="Enter species name"
              class="input input-bordered w-full"
            />
            {#if showTooltip === 'species'}
              <div class="tooltip" id="speciesTooltip" role="tooltip">
                Enter full or partial species name (not case sensitive)
              </div>
            {/if}
          </div>

          <!-- Date Range -->
          <div class="form-control">
            <label class="label" for="dateRangeStart">
              <span class="label-text">Date Range</span>
              <span
                class="help-icon"
                onmouseenter={() => (showTooltip = 'dateRange')}
                onmouseleave={() => (showTooltip = null)}
                onfocus={() => (showTooltip = 'dateRange')}
                onblur={() => (showTooltip = null)}
                role="button"
                tabindex="0"
                aria-label="Help for Date Range"
                aria-describedby="dateRangeTooltip">ⓘ</span
              >
            </label>
            <div class="gap-2 search-date-grid" role="group" aria-labelledby="dateRangeLabel">
              <input
                type="date"
                id="dateRangeStart"
                bind:value={dateRange.start}
                placeholder="Start Date"
                class="input input-bordered w-full"
                aria-label="From"
              />
              <input
                type="date"
                id="endDate"
                bind:value={dateRange.end}
                placeholder="End Date"
                class="input input-bordered w-full"
                aria-label="To"
              />
            </div>
            {#if showTooltip === 'dateRange'}
              <div class="tooltip" id="dateRangeTooltip" role="tooltip">
                Filter detections by date range (leave empty for all dates)
              </div>
            {/if}
          </div>
        </div>

        <!-- Advanced Filters Section -->
        <CollapsibleSection
          title="Advanced Filters"
          defaultOpen={advancedFilters}
          className="bg-transparent shadow-none p-0"
          contentClassName="space-y-2 pt-2"
        >
          <!-- Confidence Range -->
          <div class="form-control">
            <label class="label" for="confidenceMin">
              <span class="label-text">Confidence Range (%)</span>
              <span class="label-text-alt">{confidenceRange.min}% - {confidenceRange.max}%</span>
            </label>
            <div
              class="gap-6 search-confidence-grid"
              role="group"
              aria-labelledby="confidenceRangeLabel"
            >
              <div>
                <input
                  type="range"
                  min="0"
                  max="100"
                  id="confidenceMin"
                  bind:value={confidenceRange.min}
                  class="range range-xs"
                  aria-label="Minimum confidence percentage"
                  aria-valuemin="0"
                  aria-valuemax="100"
                  aria-valuenow={confidenceRange.min}
                  aria-valuetext="{confidenceRange.min}%"
                />
                <div class="flex justify-between text-xs px-2">
                  <span>0%</span>
                  <span>{confidenceRange.min}%</span>
                </div>
              </div>
              <div>
                <input
                  type="range"
                  min="0"
                  max="100"
                  bind:value={confidenceRange.max}
                  class="range range-xs"
                  aria-label="Maximum confidence percentage"
                  aria-valuemin="0"
                  aria-valuemax="100"
                  aria-valuenow={confidenceRange.max}
                  aria-valuetext="{confidenceRange.max}%"
                />
                <div class="flex justify-between text-xs px-2">
                  <span>0%</span>
                  <span>{confidenceRange.max}%</span>
                </div>
              </div>
            </div>
            <!-- Confidence error message -->
            {#if confidenceError}
              <div class="text-error text-sm mt-1" role="alert">{confidenceError}</div>
            {/if}
          </div>

          <!-- Status & Time of Day Filters -->
          <div class="gap-6 search-filters-grid">
            <!-- Verified Status -->
            <div class="form-control">
              <label class="label" for="verifiedStatusFilter">
                <span class="label-text">Verified Status</span>
              </label>
              <select
                id="verifiedStatusFilter"
                bind:value={verifiedStatus}
                class="select select-bordered w-full"
              >
                <option value="any">Any Status</option>
                <option value="verified">Verified Only</option>
                <option value="unverified">Unverified Only</option>
              </select>
            </div>

            <!-- Locked Status -->
            <div class="form-control">
              <label class="label" for="lockedStatusFilter">
                <span class="label-text">Locked Status</span>
              </label>
              <select
                id="lockedStatusFilter"
                bind:value={lockedStatus}
                class="select select-bordered w-full"
              >
                <option value="any">Any Status</option>
                <option value="locked">Locked Only</option>
                <option value="unlocked">Unlocked Only</option>
              </select>
            </div>

            <!-- Time of Day -->
            <div class="form-control">
              <label class="label" for="timeOfDayFilter">
                <span class="label-text">Time of Day</span>
              </label>
              <select
                id="timeOfDayFilter"
                bind:value={timeOfDayFilter}
                class="select select-bordered w-full"
              >
                <option value="any">Any Time</option>
                <option value="day">Day Only</option>
                <option value="night">Night Only</option>
                <option value="sunrise">Sunrise +/- 30min</option>
                <option value="sunset">Sunset +/- 30min</option>
              </select>
            </div>
          </div>
        </CollapsibleSection>

        <!-- Form Action Buttons -->
        <div class="flex flex-row gap-4 justify-end">
          <button
            type="button"
            class="btn btn-ghost flex-shrink-0"
            onclick={resetForm}
            aria-label="Reset all search filters"
          >
            Reset
          </button>
          <button
            type="submit"
            class="btn btn-primary flex-shrink-0"
            disabled={isLoading}
            aria-label="Search for bird detections"
          >
            {#if isLoading}
              <span class="loading loading-spinner loading-sm mr-2" aria-hidden="true"></span>
            {:else}
              <span class="mr-2" aria-hidden="true">
                {@html actionIcons.search}
              </span>
            {/if}
            Search
          </button>
        </div>
      </form>
    </div>
  </div>

  <!-- Results Area -->
  <div class="card bg-base-100 shadow-sm">
    <div class="card-body card-padding">
      <div class="flex items-center justify-between">
        <h2 class="card-title" id="search-results-heading">Search Results</h2>

        <!-- Results Count & Sorting -->
        {#if formSubmitted}
          <div class="flex items-center gap-4">
            <span class="text-sm text-base-content/70" aria-live="polite"
              >{totalResults} result{totalResults !== 1 ? 's' : ''}</span
            >
            <div class="dropdown dropdown-end">
              <div
                tabindex="0"
                role="button"
                class="btn btn-sm btn-outline"
                aria-haspopup="true"
                aria-expanded="false"
                aria-label="Sort results"
              >
                {@html actionIcons.sort}
                Sort
              </div>
              <ul
                tabindex="0"
                class="dropdown-content z-[1] menu p-2 shadow bg-base-100 rounded-box w-52"
                role="menu"
              >
                <li role="menuitem">
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm justify-start w-full"
                    onclick={() => changeSort('date_desc')}>Date (Newest First)</button
                  >
                </li>
                <li role="menuitem">
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm justify-start w-full"
                    onclick={() => changeSort('date_asc')}>Date (Oldest First)</button
                  >
                </li>
                <li role="menuitem">
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm justify-start w-full"
                    onclick={() => changeSort('species_asc')}>Species (A-Z)</button
                  >
                </li>
                <li role="menuitem">
                  <button
                    type="button"
                    class="btn btn-ghost btn-sm justify-start w-full"
                    onclick={() => changeSort('confidence_desc')}>Confidence (High-Low)</button
                  >
                </li>
              </ul>
            </div>
          </div>
        {/if}
      </div>

      <!-- Error message -->
      {#if errorMessage}
        <div class="alert alert-error mt-4" role="alert">
          {@html alertIconsSvg.error}
          <span>{errorMessage}</span>
        </div>
      {/if}

      <!-- When no search performed yet -->
      {#if !formSubmitted}
        <div
          class="mt-6 bg-base-200 rounded-lg p-4 flex flex-col items-center justify-center min-h-[200px]"
          aria-labelledby="search-results-heading"
        >
          <span class="text-base-content/30 text-[4rem]" aria-hidden="true">
            {@html systemIcons.search}
          </span>
          <p class="text-base-content/50 text-center mt-4">No search performed yet</p>
          <p class="text-base-content/50 text-center text-sm">
            Use the search filters above to find bird detections
          </p>
        </div>
      {/if}

      <!-- Loading indicator -->
      {#if isLoading && formSubmitted}
        <div
          class="mt-6 bg-base-200 rounded-lg p-4 flex flex-col items-center justify-center min-h-[200px]"
          aria-live="polite"
          aria-busy="true"
        >
          <span class="loading loading-spinner loading-lg text-primary" aria-hidden="true"></span>
          <p class="text-base-content/50 text-center mt-4">Loading results...</p>
        </div>
      {/if}

      <!-- Search results table - only visible when search performed -->
      {#if formSubmitted && !isLoading && results.length > 0}
        <div class="overflow-x-auto mt-4" aria-labelledby="search-results-heading">
          <table class="table w-full">
            <thead>
              <tr>
                <th scope="col">Date & Time</th>
                <th scope="col">Time of Day</th>
                <th scope="col">Species</th>
                <th scope="col">Confidence</th>
                <th scope="col">Status</th>
                <th scope="col">Actions</th>
              </tr>
            </thead>
            <tbody>
              <!-- Loop through results -->
              {#each results as result, index (result.id)}
                <!-- Main row -->
                <tr class={index % 2 === 0 ? 'bg-base-100' : 'bg-base-200'}>
                  <td>{formatDate(result.timestamp)}</td>
                  <td>
                    <div class="flex items-center">
                      <TimeOfDayIcon timeOfDay={result.timeOfDay as any} className="mr-1" />
                      <span>{result.timeOfDay || 'Unknown'}</span>
                    </div>
                  </td>
                  <td>
                    <div class="flex items-center gap-2">
                      <!-- Add bird image thumbnail -->
                      <div
                        class="w-12 h-12 rounded-md overflow-hidden bg-gray-100 flex-shrink-0 cursor-pointer hover:ring-2 hover:ring-primary transition-all focus:outline-none focus:ring-2 focus:ring-primary"
                        onclick={() => toggleExpand(result.id)}
                        onkeydown={e => {
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault();
                            toggleExpand(result.id);
                          }
                        }}
                        aria-label="{isExpanded(result.id)
                          ? 'Collapse'
                          : 'Expand'} details for {result.commonName || 'Unknown species'}"
                        aria-expanded={isExpanded(result.id)}
                        role="button"
                        tabindex="0"
                      >
                        <img
                          src="/api/v2/media/species-image?name={encodeURIComponent(
                            result.scientificName
                          )}"
                          alt={result.commonName || 'Unknown species'}
                          class="w-full h-full object-cover"
                          onerror={e => {
                            const target = e.target as any;
                            if (target) {
                              target.src = '/assets/images/bird-placeholder.svg';
                              target.classList.add('p-2');
                            }
                          }}
                          loading="lazy"
                        />
                      </div>
                      <div>
                        <div class="font-bold">{result.commonName || 'Unknown'}</div>
                        <div class="text-xs opacity-50">{result.scientificName || ''}</div>
                      </div>
                    </div>
                  </td>
                  <td>
                    <div class="flex items-center">
                      <div class="flex items-center gap-2 w-full">
                        <div
                          class="w-16 h-4 rounded-full overflow-hidden bg-base-200"
                          role="progressbar"
                          aria-valuenow={Math.round(result.confidence * 100)}
                          aria-valuemin="0"
                          aria-valuemax="100"
                          aria-valuetext="{Math.round(result.confidence * 100)}%"
                        >
                          <div
                            class="h-full {result.confidence >= 0.8
                              ? 'bg-success'
                              : result.confidence >= 0.4
                                ? 'bg-warning'
                                : 'bg-error'}"
                            style:width="{result.confidence * 100}%"
                          ></div>
                        </div>
                        <span class="ml-1 font-semibold"
                          >{Math.round(result.confidence * 100)}%</span
                        >
                      </div>
                    </div>
                  </td>
                  <td>
                    <div class="flex gap-1 flex-wrap">
                      <div
                        class="status-badge {result.verified === 'correct'
                          ? 'correct'
                          : result.verified === 'false_positive'
                            ? 'false'
                            : 'unverified'}"
                      >
                        {result.verified === 'correct'
                          ? 'Verified'
                          : result.verified === 'false_positive'
                            ? 'False'
                            : 'Unverified'}
                      </div>
                      <div class="status-badge {result.locked ? 'locked' : 'unverified'}">
                        {result.locked ? 'Locked' : 'Unlocked'}
                      </div>
                    </div>
                  </td>
                  <td>
                    <div class="flex gap-1">
                      <button
                        class="btn btn-xs btn-square"
                        onclick={e => {
                          e.preventDefault();
                          // TODO: Implement audio playback function
                        }}
                        disabled={!result.hasAudio}
                        aria-label="Play audio for {result.commonName || 'Unknown species'}"
                        aria-pressed="false"
                      >
                        {@html mediaIcons.music}
                      </button>
                      <button
                        class="btn btn-xs btn-square"
                        onclick={() => (window.location.href = `/api/v2/detections/${result.id}`)}
                        aria-label="View details for {result.commonName || 'Unknown species'}"
                      >
                        {@html systemIcons.eye}
                      </button>
                      <button
                        class="btn btn-xs btn-square expand-btn"
                        onclick={e => {
                          e.preventDefault();
                          toggleExpand(result.id);
                        }}
                        data-id={result.id}
                        aria-label="{isExpanded(result.id)
                          ? 'Collapse'
                          : 'Expand'} details for {result.commonName || 'Unknown species'}"
                        aria-expanded={isExpanded(result.id)}
                        aria-controls="expanded-row-{result.id}"
                      >
                        <span
                          class="transition-transform duration-200"
                          class:rotate-180={isExpanded(result.id)}
                          aria-hidden="true"
                        >
                          {@html navigationIcons.chevronDown}
                        </span>
                      </button>
                    </div>
                  </td>
                </tr>

                <!-- Expanded row -->
                {#if isExpanded(result.id)}
                  <tr class="expanded-row" id="expanded-row-{result.id}">
                    <td colspan="6" class="p-0 border-t-0">
                      <div class="p-4 {index % 2 === 0 ? 'bg-base-100' : 'bg-base-200'}">
                        <!-- Expanded content -->
                        <div class="grid grid-cols-1 lg:grid-cols-3 gap-4">
                          <!-- Weather Information Container -->
                          <div class="bg-base-200 rounded-box p-4">
                            <WeatherInfo detectionId={result.id} />
                          </div>

                          <!-- Bird Image Container (Middle Column) -->
                          <div
                            class="bg-base-200 rounded-box p-4 flex flex-col justify-center items-center"
                          >
                            <div
                              class="w-full aspect-square rounded-md overflow-hidden bg-gray-100 cursor-pointer hover:brightness-90 transition-all focus:outline-none focus:ring-2 focus:ring-primary"
                              onclick={() => toggleExpand(result.id)}
                              onkeydown={e => {
                                if (e.key === 'Enter' || e.key === ' ') {
                                  e.preventDefault();
                                  toggleExpand(result.id);
                                }
                              }}
                              role="button"
                              tabindex="0"
                              aria-label="Collapse details for {result.commonName ||
                                'Unknown species'}"
                              aria-expanded={isExpanded(result.id)}
                              aria-controls="expanded-row-{result.id}"
                              title="Click to collapse"
                            >
                              <img
                                src="/api/v2/media/species-image?name={encodeURIComponent(
                                  result.scientificName
                                )}"
                                alt={result.commonName || 'Unknown species'}
                                class="w-full h-full object-cover"
                                onerror={e => {
                                  const target = e.target as any;
                                  if (target) {
                                    target.src = '/assets/images/bird-placeholder.svg';
                                    target.classList.add('p-2');
                                  }
                                }}
                                loading="lazy"
                              />
                            </div>
                          </div>

                          <!-- Audio Player -->
                          <div class="bg-base-200 rounded-box p-4">
                            <h3 class="text-lg font-semibold mb-2">Audio Player</h3>
                            <AudioPlayer
                              audioUrl="/api/v2/audio/{result.id}"
                              detectionId={result.id}
                              width={400}
                              height={200}
                              showDownload={true}
                              showSpectrogram={true}
                            />
                          </div>
                        </div>
                      </div>
                    </td>
                  </tr>
                {/if}
              {/each}
            </tbody>
          </table>
        </div>
      {/if}

      <!-- Empty state - when search returns no results -->
      {#if formSubmitted && !isLoading && results.length === 0 && !errorMessage}
        <div
          class="mt-6 bg-base-200 rounded-lg p-4 flex flex-col items-center justify-center min-h-[200px]"
        >
          {@html dataIcons.sadFace}
          <p class="mt-2 text-base-content/70">No results found</p>
          <p class="text-sm text-base-content/50">Try adjusting your search filters</p>
        </div>
      {/if}

      <!-- Pagination - visible when results are available -->
      {#if formSubmitted && !isLoading && totalPages > 1}
        <div class="flex justify-center mt-6">
          <div class="join">
            <button
              class="join-item btn"
              onclick={() => goToPage(currentPage - 1)}
              disabled={currentPage <= 1}>«</button
            >
            <button class="join-item btn">Page {currentPage} of {totalPages}</button>
            <button
              class="join-item btn"
              onclick={() => goToPage(currentPage + 1)}
              disabled={currentPage >= totalPages}>»</button
            >
          </div>
        </div>
      {/if}
    </div>
  </div>
</div>

<style>
  .card-padding {
    padding: 1rem;
  }

  @media (min-width: 768px) {
    .card-padding {
      padding: 1.5rem;
    }
  }

  .tooltip {
    position: absolute;
    background-color: #1f2937;
    color: white;
    padding: 0.5rem;
    border-radius: 0.25rem;
    font-size: 0.875rem;
    margin-top: 0.25rem;
    z-index: 10;
  }

  .help-icon {
    cursor: help;
    font-size: 0.875rem;
    color: #6b7280;
  }

  .status-badge {
    padding: 0.125rem 0.5rem;
    border-radius: 0.375rem;
    font-size: 0.75rem;
    font-weight: 500;
  }

  .status-badge.correct {
    background-color: #10b981;
    color: white;
  }

  .status-badge.false {
    background-color: #ef4444;
    color: white;
  }

  .status-badge.unverified {
    background-color: #6b7280;
    color: white;
  }

  .status-badge.locked {
    background-color: #f59e0b;
    color: white;
  }

  .expanded-row td {
    animation: slideDown 0.3s ease-out;
  }

  @keyframes slideDown {
    from {
      opacity: 0;
      transform: translateY(-10px);
    }

    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  .search-form-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 768px) {
    .search-form-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  .search-confidence-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 768px) {
    .search-confidence-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  .search-filters-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 768px) {
    .search-filters-grid {
      grid-template-columns: repeat(3, minmax(0, 1fr));
    }
  }

  .search-date-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 768px) {
    .search-date-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }
</style>
