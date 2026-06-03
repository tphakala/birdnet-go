<script lang="ts">
  import FormField from '$lib/desktop/components/forms/FormField.svelte';
  import Input from '$lib/desktop/components/ui/Input.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { SelectOption } from '$lib/desktop/components/forms/SelectDropdown.types';
  import { t } from '$lib/i18n';

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
    /** Selected source group display names. Empty array means "all sources". */
    sourceGroups: string[];
  }

  /**
   * AudioSourceOption mirrors the shape used by Analytics.svelte's picker — one entry per
   * display_name with the aggregate detection count. The parent resolves the display name
   * back to the underlying audio_sources.id list when issuing API requests.
   */
  export interface AudioSourceOption {
    displayName: string;
    ids: number[];
    count: number;
  }

  interface Props {
    filters: SpeciesFilters;
    audioSources?: AudioSourceOption[];
    isLoading?: boolean;
    filteredCount: number;
    onSubmit: () => void;
    onReset: () => void;
    onExport: () => void;
    onSearchInput: (_e: Event) => void;
  }

  let {
    filters = $bindable(),
    audioSources = [],
    isLoading = false,
    filteredCount,
    onSubmit,
    onReset,
    onExport,
    onSearchInput,
  }: Props = $props();

  const timePeriodOptions = [
    { value: 'all', label: t('analytics.timePeriodOptions.allTime') },
    { value: 'today', label: t('analytics.timePeriodOptions.today') },
    { value: 'week', label: t('analytics.timePeriodOptions.lastWeek') },
    { value: 'month', label: t('analytics.timePeriodOptions.lastMonth') },
    { value: '90days', label: t('analytics.timePeriodOptions.last90Days') },
    { value: 'year', label: t('analytics.timePeriodOptions.lastYear') },
    { value: 'custom', label: t('analytics.timePeriodOptions.customRange') },
  ];

  // Build dropdown options from the audio sources list. No locale argument on toLocaleString
  // so the browser's resolved locale formats the count (NL: "1.234", US: "1,234").
  let sourceOptions = $derived<SelectOption[]>(
    audioSources.map(src => ({
      value: src.displayName,
      label: `${src.displayName} (${src.count.toLocaleString()})`,
    }))
  );

  // Hide the picker entirely when there's only one source — nothing to filter on.
  let showSourcePicker = $derived(audioSources.length >= 2);

  const sortOptions = [
    { value: 'count_desc', label: t('analytics.sortOptions.mostDetections') },
    { value: 'count_asc', label: t('analytics.sortOptions.fewestDetections') },
    { value: 'name_asc', label: t('analytics.sortOptions.nameAZ') },
    { value: 'name_desc', label: t('analytics.sortOptions.nameZA') },
    { value: 'first_seen_desc', label: t('analytics.sortOptions.recentlyFirstSeen') },
    { value: 'first_seen_asc', label: t('analytics.sortOptions.earliestFirstSeen') },
    { value: 'last_seen_desc', label: t('analytics.sortOptions.recentlyLastSeen') },
    { value: 'last_seen_asc', label: t('analytics.sortOptions.earliestLastSeen') },
    { value: 'confidence_desc', label: t('analytics.sortOptions.highestConfidence') },
    { value: 'confidence_asc', label: t('analytics.sortOptions.lowestConfidence') },
    { value: 'max_confidence_desc', label: t('analytics.sortOptions.highestMaxConfidence') },
    { value: 'max_confidence_asc', label: t('analytics.sortOptions.lowestMaxConfidence') },
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

<div class="card bg-[var(--color-base-100)] shadow-xs">
  <div class="card-body card-padding">
    <h2 class="card-title" id="species-filters-heading">{t('analytics.filters.title')}</h2>

    <form id="speciesFiltersForm" class="space-y-4" onsubmit={handleSubmit}>
      <div
        class="filters-grid"
        style:display="grid"
        style:grid-template-columns="repeat(auto-fit, minmax(200px, 1fr))"
        style:gap="1rem"
      >
        <!-- Time Period Filter -->
        <FormField label={t('analytics.filters.timePeriod')} id="timePeriod">
          <SelectDropdown
            bind:value={filters.timePeriod}
            options={timePeriodOptions}
            variant="select"
            size="sm"
            menuSize="sm"
          />
        </FormField>

        <!-- Custom Date Range -->
        {#if filters.timePeriod === 'custom'}
          <FormField label={t('analytics.filters.from')} id="startDate">
            <Input type="date" id="startDate" bind:value={filters.startDate} />
          </FormField>

          <FormField label={t('analytics.filters.to')} id="endDate">
            <Input type="date" id="endDate" bind:value={filters.endDate} />
          </FormField>
        {/if}

        <!-- Audio Source Filter (multi-select; hidden when only one source exists) -->
        {#if showSourcePicker}
          <FormField label={t('analytics.filters.audioSource')} id="audioSource">
            <SelectDropdown
              bind:value={filters.sourceGroups}
              options={sourceOptions}
              multiple={true}
              clearable={true}
              variant="select"
              size="sm"
              menuSize="sm"
              placeholder={t('analytics.filters.audioSourceAll')}
            />
          </FormField>
        {/if}

        <!-- Sort Order -->
        <FormField label={t('analytics.filters.sortBy')} id="sortOrder">
          <SelectDropdown
            bind:value={filters.sortOrder}
            options={sortOptions}
            variant="select"
            size="sm"
            menuSize="sm"
          />
        </FormField>

        <!-- Search Filter - Full width on mobile -->
        <FormField
          label={t('analytics.filters.searchSpecies')}
          id="searchTerm"
          className="col-span-full sm:col-span-auto"
        >
          <Input
            type="text"
            id="searchTerm"
            bind:value={filters.searchTerm}
            placeholder={t('analytics.filters.searchPlaceholder')}
            {...{ oninput: onSearchInput }}
          />
        </FormField>
      </div>

      <div class="flex flex-col sm:flex-row justify-between items-start sm:items-center gap-4">
        <div class="text-sm text-[var(--color-base-content)] opacity-60">
          <span>{filteredCount}</span>
          {t('analytics.filters.species')}
          {#if filters.searchTerm}
            <span>{t('analytics.filters.filtered')}</span>
          {/if}
        </div>
        <div class="flex gap-2 w-full sm:w-auto flex-col sm:flex-row">
          <button
            type="button"
            class="btn btn-ghost btn-sm"
            onclick={handleReset}
            disabled={isLoading}
          >
            {t('analytics.filters.reset')}
          </button>
          <button
            type="button"
            class="btn btn-secondary btn-sm hidden sm:flex"
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
            {t('analytics.filters.exportCsv')}
          </button>
          <button type="submit" class="btn btn-primary btn-sm" disabled={isLoading}>
            {#if isLoading}
              <span class="loading loading-spinner loading-sm"></span>
            {/if}
            {t('analytics.filters.applyFilters')}
          </button>
        </div>
      </div>
    </form>
  </div>
</div>
