<script lang="ts">
  import FormField from '$lib/desktop/components/forms/FormField.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { SelectOption } from '$lib/desktop/components/forms/SelectDropdown.types';
  import Input from '$lib/desktop/components/ui/Input.svelte';
  import { t } from '$lib/i18n';

  interface Filters {
    timePeriod: 'all' | 'today' | 'week' | 'month' | '90days' | 'year' | 'custom';
    startDate: string;
    endDate: string;
    /**
     * Selected display name groups. Empty array means "all sources".
     * Each entry corresponds to one display_name; the parent component resolves
     * display_names back to the underlying audio_sources.id values when issuing API requests.
     */
    sourceGroups: string[];
  }

  /**
   * AudioSourceOption describes one selectable audio source group for the picker.
   * `value` is the display_name (used as the selection key in the multi-select),
   * `count` is the aggregated detection count across all rows sharing that display_name,
   * `ids` is the list of audio_sources.id integers that back this group (1+).
   */
  export interface AudioSourceOption {
    displayName: string;
    ids: number[];
    count: number;
  }

  interface Props {
    filters: Filters;
    audioSources?: AudioSourceOption[];
    isLoading?: boolean;
    onSubmit: () => void;
    onReset: () => void;
  }

  let {
    filters = $bindable(),
    audioSources = [],
    isLoading = false,
    onSubmit,
    onReset,
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

  // Build dropdown options from the audio sources list. Each option's label includes the
  // aggregated detection count so the user can see which sources have meaningful data.
  // No locale argument: defer to the browser's resolved locale so e.g. Dutch users see
  // "1.234" and US users see "1,234" without us hardcoding either grouping convention.
  let sourceOptions = $derived<SelectOption[]>(
    audioSources.map(src => ({
      value: src.displayName,
      label: `${src.displayName} (${src.count.toLocaleString()})`,
    }))
  );

  // The source picker is hidden when there are fewer than two sources to choose from —
  // a single-source install has nothing to filter and would just add noise to the form.
  let showSourcePicker = $derived(audioSources.length >= 2);

  function handleSubmit(event: Event) {
    event.preventDefault();
    onSubmit();
  }

  function handleReset() {
    onReset();
  }
</script>

<div class="card bg-[var(--color-base-100)] shadow-xs">
  <div class="card-body card-padding">
    <h2 class="card-title">{t('analytics.filters.title')}</h2>

    <form class="space-y-4" onsubmit={handleSubmit}>
      <div class="grid grid-cols-1 md:grid-cols-3 gap-4">
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
      </div>

      <div class="flex justify-end gap-2">
        <button type="button" class="btn btn-ghost" onclick={handleReset} disabled={isLoading}>
          {t('analytics.filters.reset')}
        </button>
        <button type="submit" class="btn btn-primary" disabled={isLoading}>
          {#if isLoading}
            <span class="loading loading-spinner loading-sm"></span>
          {/if}
          {t('analytics.filters.applyFilters')}
        </button>
      </div>
    </form>
  </div>
</div>
