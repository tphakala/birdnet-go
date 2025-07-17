<script lang="ts">
  import FormField from '$lib/desktop/components/forms/FormField.svelte';
  import Select from '$lib/desktop/components/ui/Select.svelte';
  import Input from '$lib/desktop/components/ui/Input.svelte';

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
      | 'confidence_desc';
    searchTerm: string;
  }

  interface Props {
    filters: SpeciesFilters;
    isLoading?: boolean;
    filteredCount: number;
    onSubmit: () => void;
    onReset: () => void;
    onExport: () => void;
    onSearchInput: (_e: Event) => void;
  }

  let {
    filters = $bindable(),
    isLoading = false,
    filteredCount,
    onSubmit,
    onReset,
    onExport,
    onSearchInput,
  }: Props = $props();

  const timePeriodOptions = [
    { value: 'all', label: 'All Time' },
    { value: 'today', label: 'Today' },
    { value: 'week', label: 'Last 7 Days' },
    { value: 'month', label: 'Last 30 Days' },
    { value: '90days', label: 'Last 90 Days' },
    { value: 'year', label: 'Last 12 Months' },
    { value: 'custom', label: 'Custom Range' },
  ];

  const sortOptions = [
    { value: 'count_desc', label: 'Most Detections' },
    { value: 'count_asc', label: 'Fewest Detections' },
    { value: 'name_asc', label: 'Name (A-Z)' },
    { value: 'name_desc', label: 'Name (Z-A)' },
    { value: 'first_seen_desc', label: 'Recently First Seen' },
    { value: 'first_seen_asc', label: 'Earliest First Seen' },
    { value: 'last_seen_desc', label: 'Recently Last Seen' },
    { value: 'confidence_desc', label: 'Highest Confidence' },
  ];

  function handleSubmit(event: Event) {
    event.preventDefault();
    onSubmit();
  }

  function handleReset() {
    onReset();
  }

  function handleExport() {
    onExport();
  }
</script>

<div class="card bg-base-100 shadow-sm">
  <div class="card-body card-padding">
    <h2 class="card-title" id="species-filters-heading">Filter Data</h2>

    <form id="speciesFiltersForm" class="space-y-4" onsubmit={handleSubmit}>
      <div
        class="filters-grid"
        style:display="grid"
        style:grid-template-columns="repeat(auto-fit, minmax(250px, 1fr))"
        style:gap="1rem"
      >
        <!-- Time Period Filter -->
        <FormField label="Time Period" id="timePeriod">
          <Select id="timePeriod" bind:value={filters.timePeriod} options={timePeriodOptions} />
        </FormField>

        <!-- Custom Date Range -->
        {#if filters.timePeriod === 'custom'}
          <FormField label="From" id="startDate">
            <Input type="date" id="startDate" bind:value={filters.startDate} />
          </FormField>

          <FormField label="To" id="endDate">
            <Input type="date" id="endDate" bind:value={filters.endDate} />
          </FormField>
        {/if}

        <!-- Sort Order -->
        <FormField label="Sort By" id="sortOrder">
          <Select id="sortOrder" bind:value={filters.sortOrder} options={sortOptions} />
        </FormField>

        <!-- Search Filter -->
        <FormField label="Search Species" id="searchTerm">
          <Input
            type="text"
            id="searchTerm"
            bind:value={filters.searchTerm}
            placeholder="Search by name..."
            {...{ oninput: onSearchInput }}
          />
        </FormField>
      </div>

      <div class="flex justify-between items-center">
        <div class="text-sm text-base-content/60">
          <span>{filteredCount}</span> species
          {#if filters.searchTerm}
            <span>(filtered)</span>
          {/if}
        </div>
        <div class="flex gap-2">
          <button type="button" class="btn btn-ghost" onclick={handleReset} disabled={isLoading}>
            Reset
          </button>
          <button
            type="button"
            class="btn btn-secondary"
            onclick={handleExport}
            disabled={isLoading}
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              class="h-4 w-4"
              viewBox="0 0 20 20"
              fill="currentColor"
            >
              <path
                fill-rule="evenodd"
                d="M3 17a1 1 0 011-1h12a1 1 0 110 2H4a1 1 0 01-1-1zm3.293-7.707a1 1 0 011.414 0L9 10.586V3a1 1 0 112 0v7.586l1.293-1.293a1 1 0 111.414 1.414l-3 3a1 1 0 01-1.414 0l-3-3a1 1 0 010-1.414z"
                clip-rule="evenodd"
              />
            </svg>
            Export CSV
          </button>
          <button type="submit" class="btn btn-primary" disabled={isLoading}>
            {#if isLoading}
              <span class="loading loading-spinner loading-sm"></span>
            {/if}
            Apply Filters
          </button>
        </div>
      </div>
    </form>
  </div>
</div>
